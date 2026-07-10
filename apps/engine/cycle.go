package main

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// cycle.go — EJECUTOR DE CICLOS (Fase 2 · hito 3).
//
// El radar deja de ser solo detección: con el AUTOPILOTO activado (opt-in del
// usuario, TradingParameters.RadarAutopilot), el mejor ciclo del subgrafo del
// usuario se EJECUTA — espacial (2 libros + swap + paridad) o triangular (3
// libros dentro de Binance), con las wallets multi-activo del Sprint A/B.
//
// Diseño en dos pasos, igual de atómico que el ejecutor clásico:
//  1. planCycle (función PURA, testeable sin aleatoriedad): rota el ciclo a un
//     nodo de inicio cash, dimensiona el monto contra saldo + tope del usuario +
//     liquidez de cada pierna (mapeada a unidades de inicio), y calcula cada
//     pierna y el neto. Si el neto no supera el margen del usuario, no hay plan.
//  2. commitCycle: bajo el lock de sesión re-verifica el saldo de inicio (hard
//     block) y aplica TODAS las piernas de una vez. El Fill-or-Kill se evalúa
//     ANTES de tocar saldos: un fallo aborta con cero exposición direccional,
//     sin necesidad de deshacer nada.

// cycleLeg es una pierna ya dimensionada del plan: convierte In unidades del
// nodo From en Out unidades del nodo To (fees ya descontados).
type cycleLeg struct {
	From MarketNode
	To   MarketNode
	In   float64
	Out  float64
	Kind EdgeKind
}

// cyclePlan es el resultado de planificar un ciclo para una sesión concreta.
type cyclePlan struct {
	Start MarketNode // nodo cash de inicio (USD/USDT)
	// StartAmount es cuánto entra al ciclo, en unidades del activo de inicio
	// (cash ⇒ ≈ USD): min(saldo, tope del usuario, liquidez de las piernas).
	StartAmount float64
	Legs        []cycleLeg
	// NetProfit = lo que regresa al nodo de inicio menos StartAmount (≈ USD).
	NetProfit float64
	// FeesUSD es la fricción total del ciclo (fees + slippage estimado) en
	// unidades de inicio (≈USD): StartAmount × (Π tasas brutas − Π tasas netas).
	// Alimenta la analítica del ledger ("fees totales pagados").
	FeesUSD float64
	// VolumeBTCEquiv expresa el tamaño en BTC-equivalente (para el ledger/feed).
	VolumeBTCEquiv float64
}

// Route es la narración corta de la ruta ("USDT@Binance→BTC@Binance→…").
func (pl *cyclePlan) Route() string {
	parts := make([]string, 0, len(pl.Legs)+1)
	parts = append(parts, pl.Legs[0].From.ID())
	for _, l := range pl.Legs {
		parts = append(parts, l.To.ID())
	}
	return strings.Join(parts, "→")
}

var (
	errNoCashStart   = errors.New("ciclo sin nodo cash de inicio")
	errNoFunds       = errors.New("saldo o liquidez insuficiente para el mínimo ejecutable")
	errBelowMargin   = errors.New("el neto del ciclo no supera el margen del usuario")
	errNotProfitable = errors.New("ciclo no rentable tras fees")
)

// planCycle dimensiona y planifica un ciclo para una sesión (función PURA sobre
// una copia de saldos). btcPrice traduce el tope MaxOrderSizeBTC del usuario a
// unidades cash (tope BTC-equivalente) y el volumen del plan a BTC.
//
// Un ciclo puede tocar VARIOS nodos cash (USD@Bitso, USD@Kraken, USDT@Binance):
// se intenta cada rotación y gana el plan de mayor neto. Así un ciclo que pasa
// por un venue sin fondos (Kraken recién agregado) sigue siendo ejecutable si
// alguno de sus otros nodos cash sí tiene saldo.
func planCycle(c *Cycle, balances Balances, p TradingParameters, btcPrice float64) (*cyclePlan, error) {
	if c == nil || len(c.Edges) == 0 || btcPrice <= 0 {
		return nil, errNotProfitable
	}

	var best *cyclePlan
	hasCash, sawNoFunds, sawBelowMargin := false, false, false
	for i, e := range c.Edges {
		if !isCashAsset(e.From.Asset) {
			continue
		}
		hasCash = true
		edges := append(append([]Edge{}, c.Edges[i:]...), c.Edges[:i]...)
		plan, err := planRotation(edges, balances, p, btcPrice)
		switch err {
		case nil:
			if best == nil || plan.NetProfit > best.NetProfit {
				best = plan
			}
		case errNoFunds:
			sawNoFunds = true
		case errBelowMargin:
			sawBelowMargin = true
		}
	}
	if best != nil {
		return best, nil
	}
	switch {
	case !hasCash:
		return nil, errNoCashStart
	case sawBelowMargin:
		return nil, errBelowMargin
	case sawNoFunds:
		return nil, errNoFunds
	default:
		return nil, errNotProfitable
	}
}

// planRotation planifica UNA rotación concreta del ciclo (edges ya rotadas para
// que edges[0].From sea el nodo cash de inicio). El monto de entrada y el neto
// quedan expresados en ≈USD, consistentes con el PnL del dashboard.
func planRotation(edges []Edge, balances Balances, p TradingParameters, btcPrice float64) (*cyclePlan, error) {
	start := edges[0].From

	// Tasa efectiva del ciclo y tope de entrada por liquidez de cada pierna,
	// mapeado a unidades de inicio: en la pierna i entra X·Π(eff_j, j<i).
	// prodGross acumula las tasas SIN fees: la diferencia contra prod es la
	// fricción total (fees + slippage) que paga el ciclo.
	prod, prodGross := 1.0, 1.0
	maxStart := balances.Get(start.Venue, string(start.Asset))
	if cap := p.MaxOrderSizeBTC * btcPrice; cap < maxStart {
		maxStart = cap // tope del usuario, en BTC-equivalente
	}
	for _, e := range edges {
		eff := e.Rate * (1 - e.Fee)
		if eff <= 0 {
			return nil, errNotProfitable
		}
		if e.Kind == EdgeOrderBook && e.Liquidity > 0 {
			var limit float64
			if string(e.To.Asset) == e.BaseAsset {
				// Compra: el volumen en base sale de la pierna → X·prod·eff ≤ liq.
				limit = e.Liquidity / (prod * eff)
			} else {
				// Venta: el volumen en base entra a la pierna → X·prod ≤ liq.
				limit = e.Liquidity / prod
			}
			if limit < maxStart {
				maxStart = limit
			}
		}
		prod *= eff
		prodGross *= e.Rate
	}

	if prod <= 1 {
		return nil, errNotProfitable
	}
	if maxStart < MinExecutableVolumeBTC*btcPrice {
		return nil, errNoFunds
	}

	x := maxStart
	net := x * (prod - 1)
	if net <= p.MinNetProfitUSD {
		// El neto escala linealmente con X: si ni el máximo alcanza el margen
		// del usuario, la oportunidad es demasiado pequeña PARA ÉL.
		return nil, errBelowMargin
	}

	plan := &cyclePlan{
		Start:          start,
		StartAmount:    x,
		NetProfit:      net,
		FeesUSD:        x * (prodGross - prod),
		VolumeBTCEquiv: x / btcPrice,
	}
	amount := x
	for _, e := range edges {
		out := amount * e.Rate * (1 - e.Fee)
		plan.Legs = append(plan.Legs, cycleLeg{From: e.From, To: e.To, In: amount, Out: out, Kind: e.Kind})
		amount = out
	}
	return plan, nil
}

// cycleCreditProjection responde: ¿la línea de crédito volvería ejecutable este
// ciclo? Re-planifica sobre una copia de saldos con el préstamo HIPOTÉTICO
// aplicado (mismo reparto que activateCreditSession: el par clásico) y devuelve
// la ganancia proyectada si el plan resultante supera el margen del usuario.
// Función pura: no activa nada — la decisión es de handleLiquidityShortfall.
func cycleCreditProjection(c *Cycle, balances Balances, p TradingParameters, btcPrice float64) (float64, bool) {
	hypo := balances.Clone()
	if hypo == nil {
		hypo = make(Balances)
	}
	pair := classicVenues()
	if len(pair) == 0 {
		return 0, false
	}
	for _, v := range pair {
		hypo.Add(v.Name, v.QuoteAsset, p.CreditLineUSD/float64(len(pair)))
		hypo.Add(v.Name, v.BaseAsset, p.CreditLineBTC/float64(len(pair)))
	}
	plan, err := planCycle(c, hypo, p, btcPrice)
	if err != nil {
		return 0, false
	}
	return plan.NetProfit, true
}

// commitCycle aplica el plan sobre la sesión: re-verifica el saldo de inicio
// bajo el lock (hard block — el mundo pudo cambiar desde la planificación) y
// mueve todas las piernas de una vez. Devuelve false si ya no hay fondos.
func commitCycle(session *ClientSession, plan *cyclePlan) bool {
	session.Mu.Lock()
	defer session.Mu.Unlock()

	if session.Wallets == nil ||
		session.Wallets.Get(plan.Start.Venue, string(plan.Start.Asset)) < plan.StartAmount*(1-1e-12) {
		return false
	}

	for _, leg := range plan.Legs {
		session.Wallets.Add(leg.From.Venue, string(leg.From.Asset), -leg.In)
		session.Wallets.Add(leg.To.Venue, string(leg.To.Asset), leg.Out)
	}
	session.TotalWealth += plan.NetProfit
	session.TotalNetProfit += plan.NetProfit
	session.LastTradeTime = time.Now()
	return true
}

// executeCycleForSession es el gatillo del autopiloto: mismas compuertas que el
// ejecutor clásico (pausa, serialización, cooldown, Fill-or-Kill) aplicadas al
// ciclo detectado en el subgrafo del usuario.
func (e *HFTEngine) executeCycleForSession(session *ClientSession, cycle *Cycle, p TradingParameters) {
	session.Mu.Lock()
	if session.Wallets == nil || session.IsReplenishing || session.IsExecuting ||
		time.Now().Before(session.PausedUntil) ||
		time.Since(session.LastTradeTime) < 3*time.Second {
		session.Mu.Unlock()
		return
	}
	session.IsExecuting = true
	balances := session.Wallets.Clone()
	session.Mu.Unlock()

	defer func() {
		session.Mu.Lock()
		session.IsExecuting = false
		session.Mu.Unlock()
	}()

	btcPrice := getBTCPrice()
	plan, err := planCycle(cycle, balances, p, btcPrice)
	if err != nil {
		// Crédito para ciclos (Sprint D): si el plan murió por saldo (errNoFunds,
		// o errBelowMargin cuando el saldo es lo que acota el tamaño), se evalúa
		// si la línea de crédito lo volvería viable. La DECISIÓN es la misma del
		// modo clásico (handleLiquidityShortfall): auto-crédito si la ganancia
		// proyectada supera costo × RiskMultiplier, reequilibrio si no, o diálogo
		// asistido con los números sobre la mesa si el préstamo automático está
		// apagado. Si ni con crédito hay plan, se omite sin ruido (como antes).
		if err == errNoFunds || err == errBelowMargin {
			if projected, ok := cycleCreditProjection(cycle, balances, p, btcPrice); ok {
				e.handleLiquidityShortfall(session, projected)
			}
		}
		return
	}

	// Circuit breaker Fill-or-Kill con la probabilidad de fallo de la sesión,
	// ANTES de tocar saldos (atomicidad idéntica al camino clásico).
	if orderFails(p.OrderFailureProb) {
		session.Mu.Lock()
		session.PausedUntil = time.Now().Add(OrderFailurePause)
		session.Mu.Unlock()
		sendLog(session, "🔌 [CIRCUIT BREAKER] Fallo de liquidez en una pierna del ciclo, abortando para evitar exposición direccional")
		return
	}

	if !commitCycle(session, plan) {
		return // los fondos cambiaron entre plan y commit: hard block
	}

	sendLog(session, fmt.Sprintf("🔁 [CICLO EJECUTADO] %s | Entrada: $%.2f | Neto: +$%.2f | Vol: %.4f BTC-eq",
		plan.Route(), plan.StartAmount, plan.NetProfit, plan.VolumeBTCEquiv))

	// Feed de operaciones: compra en el venue de la primera pierna de libro,
	// venta en el de la última (espacial: Binance→Bitso; triangular: Binance→Binance).
	buyVenue, sellVenue := plan.Start.Venue, plan.Start.Venue
	for _, l := range plan.Legs {
		if l.Kind == EdgeOrderBook {
			if buyVenue == plan.Start.Venue {
				buyVenue = l.From.Venue
			}
			sellVenue = l.To.Venue
		}
	}
	sendArbExecuted(session, buyVenue, sellVenue, plan.VolumeBTCEquiv, plan.NetProfit)

	recordTradeAsync(TradeRecord{
		SessionID:    session.ID,
		Timestamp:    time.Now(),
		BuyExchange:  "Radar",
		SellExchange: plan.Route(),
		VolumeBTC:    plan.VolumeBTCEquiv,
		SpreadUSD:    0,
		FeesUSD:      plan.FeesUSD,
		NetProfitUSD: plan.NetProfit,
	})
}
