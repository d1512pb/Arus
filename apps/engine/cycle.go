package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gorilla/websocket"
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
//
// Mundo real: EdgeInventorySwap es solo conectividad (no mueve wallets). El
// sizing acota por liquidez de libros + un dry-run que aplica piernas reales
// en orden (la venta consume BTC ya fondeado; el cash de la venta financia la
// paridad siguiente).
func planRotation(edges []Edge, balances Balances, p TradingParameters, btcPrice float64) (*cyclePlan, error) {
	start := edges[0].From

	prod, prodGross := 1.0, 1.0
	maxStart := balances.Get(start.Venue, string(start.Asset))
	if cap := p.MaxOrderSizeBTC * btcPrice; cap < maxStart {
		maxStart = cap
	}
	for _, e := range edges {
		eff := e.Rate * (1 - e.Fee)
		if eff <= 0 {
			return nil, errNotProfitable
		}
		if e.Kind == EdgeOrderBook && e.Liquidity > 0 {
			var limit float64
			if string(e.To.Asset) == e.BaseAsset {
				limit = e.Liquidity / (prod * eff)
			} else {
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

	// Dry-run: reduce X hasta que todas las piernas reales tengan saldo en el
	// momento en que se ejecutan (la venta puede crear el cash de la paridad).
	for i := 0; i < 12 && maxStart >= MinExecutableVolumeBTC*btcPrice; i++ {
		if rotationFits(edges, balances, maxStart) {
			break
		}
		maxStart *= 0.5
	}
	if maxStart < MinExecutableVolumeBTC*btcPrice || !rotationFits(edges, balances, maxStart) {
		return nil, errNoFunds
	}

	x := maxStart
	net := x * (prod - 1)
	if net <= p.MinNetProfitUSD {
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

// legMovesWallet: piernas de libro/paridad siempre mueven saldos. Los swaps de
// inventario CRYPTO no (compra y venta pre-fondeada dejan el sesgo real). Los
// swaps de CASH sí se aplican: son atajos del grafo (USD Bitso→Kraken→paridad)
// sin los cuales la ruta no cierra en wallets.
func legMovesWallet(kind EdgeKind, asset Asset) bool {
	if kind != EdgeInventorySwap {
		return true
	}
	return isCashAsset(asset)
}

// rotationFits simula el commit real a tamaño x (salta solo swaps cripto).
func rotationFits(edges []Edge, balances Balances, x float64) bool {
	if x <= 0 || balances == nil {
		return false
	}
	sim := balances.Clone()
	amount := x
	for _, e := range edges {
		out := amount * e.Rate * (1 - e.Fee)
		if legMovesWallet(e.Kind, e.From.Asset) {
			if sim.Get(e.From.Venue, string(e.From.Asset)) < amount*(1-1e-12) {
				return false
			}
			sim.Add(e.From.Venue, string(e.From.Asset), -amount)
			sim.Add(e.To.Venue, string(e.To.Asset), out)
		}
		amount = out
	}
	return true
}

// cycleCreditProjection responde: ¿la línea de crédito volvería ejecutable este
// ciclo? Re-planifica sobre una copia de saldos con el préstamo HIPOTÉTICO
// aplicado (mismo reparto agnóstico que activateCreditSession) y devuelve
// la ganancia proyectada si el plan resultante supera el margen del usuario.
// Función pura: no activa nada — la decisión es de handleLiquidityShortfall.
func cycleCreditProjection(c *Cycle, balances Balances, p TradingParameters, btcPrice float64) (float64, bool) {
	hypo := balances.Clone()
	if hypo == nil {
		hypo = make(Balances)
	}
	applyHypotheticalCredit(hypo, p, creditAssetsForCycle(c))
	plan, err := planCycle(c, hypo, p, btcPrice)
	if err != nil {
		return 0, false
	}
	return plan.NetProfit, true
}

// creditAssetsForCycle lista los activos no-cash del ciclo (BTC/ETH/SOL…) para
// que el préstamo hipotético/real inyecte justo la liquidez que bloquea la ruta.
func creditAssetsForCycle(c *Cycle) []string {
	if c == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, e := range c.Edges {
		for _, a := range []Asset{e.From.Asset, e.To.Asset} {
			s := string(a)
			if isCashAsset(a) || seen[s] {
				continue
			}
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// notifyCycleUnfundable avisa (con throttle de 15 s) que hay un ciclo rentable
// pero sin fondos suficientes donde arranca — ni con la línea de crédito
// hipotética. El usuario debe depositar o pedir crédito / reequilibrar.
func (e *HFTEngine) notifyCycleUnfundable(session *ClientSession, cycle *Cycle) {
	session.Mu.Lock()
	if time.Since(session.LastUnfundableLogAt) < 15*time.Second {
		session.Mu.Unlock()
		return
	}
	session.LastUnfundableLogAt = time.Now()
	session.Mu.Unlock()

	seen := map[string]bool{}
	venues := make([]string, 0, 3)
	for _, edge := range cycle.Edges {
		for _, v := range []string{edge.From.Venue, edge.To.Venue} {
			if v != "" && !seen[v] {
				seen[v] = true
				venues = append(venues, v)
			}
		}
	}
	sendLog(session, fmt.Sprintf("💤 [RADAR] Ciclo rentable en %s pero sin fondos donde arranca — deposita, pide crédito o reequilibra inventario global para ejecutarlo.", strings.Join(venues, " · ")))
}

// commitCycle aplica el plan sobre la sesión: re-verifica saldos bajo el lock
// (hard block) y mueve las piernas REALES de una vez.
//
// Mundo real (arbitraje espacial pre-fondeado):
//   · Compra en el venue barato → −cash +crypto ahí
//   · Venta en el venue caro    → −crypto +cash ahí
//   · EdgeInventorySwap NO toca wallets: solo unía el grafo. El sesgo de
//     inventario (más BTC donde compraste, menos donde vendiste) PERMANECE
//     hasta que el usuario redistribuya o pida crédito.
// Un triangular intra-venue no tiene swaps: crypto vuelve a cash y queda flat.
func commitCycle(session *ClientSession, plan *cyclePlan) bool {
	session.Mu.Lock()
	defer session.Mu.Unlock()

	if session.Wallets == nil {
		return false
	}

	// Dry-run: cada pierna que mueve wallet debe tener saldo en From.
	sim := session.Wallets.Clone()
	for _, leg := range plan.Legs {
		if !legMovesWallet(leg.Kind, leg.From.Asset) {
			continue
		}
		if sim.Get(leg.From.Venue, string(leg.From.Asset)) < leg.In*(1-1e-12) {
			return false
		}
		sim.Add(leg.From.Venue, string(leg.From.Asset), -leg.In)
		sim.Add(leg.To.Venue, string(leg.To.Asset), leg.Out)
	}

	for _, leg := range plan.Legs {
		if !legMovesWallet(leg.Kind, leg.From.Asset) {
			continue
		}
		session.Wallets.Add(leg.From.Venue, string(leg.From.Asset), -leg.In)
		session.Wallets.Add(leg.To.Venue, string(leg.To.Asset), leg.Out)
	}
	session.TotalWealth += plan.NetProfit
	session.TotalNetProfit += plan.NetProfit
	session.LastTradeTime = time.Now()
	return true
}

// emitCycleExecuted envía el evento omni_executed con el camino REALMENTE
// ejecutado, para que el radar anime la luz verde recorriendo las aristas del
// ciclo secuencialmente (RadarView). Desde la FASE 2 del refactor de "Probar
// Bot" el evento nace SIEMPRE del plan del ejecutor de ciclos —montos, fees y
// ruta reales—, no de un plan fabricado. Sigue el patrón de sendArbExecuted:
// payload crudo por el socket (no ServerEvent).
//
// Telemetría forense (HFT Flash): el payload incluye trade_route +
// intermediate_volumes + flash_deltas con el DELTA NETO real (post-commit) de
// cada nodo tras saltar los swaps de inventario — el frontend resalta las
// celdas cuyo inventario SÍ cambió (compra/venta espacial).
func emitCycleExecuted(session *ClientSession, plan *cyclePlan) {
	if len(plan.Legs) == 0 {
		return
	}
	path := make([]string, 0, len(plan.Legs)+1)
	path = append(path, plan.Legs[0].From.ID())
	legs := make([]map[string]interface{}, 0, len(plan.Legs))
	intermediates := make([]map[string]interface{}, 0, len(plan.Legs)*2)
	// net: delta wallet REAL por nodo (ignora inventory swaps).
	net := map[string]float64{}
	meta := map[string][2]string{}

	record := func(n MarketNode, delta float64, step int, applied bool) {
		id := n.ID()
		meta[id] = [2]string{n.Venue, string(n.Asset)}
		intermediates = append(intermediates, map[string]interface{}{
			"node":    id,
			"venue":   n.Venue,
			"asset":   string(n.Asset),
			"delta":   delta,
			"step":    step,
			"applied": applied,
		})
		if applied {
			net[id] += delta
		}
	}

	for i, l := range plan.Legs {
		path = append(path, l.To.ID())
		applied := legMovesWallet(l.Kind, l.From.Asset)
		legs = append(legs, map[string]interface{}{
			"from":    l.From.ID(),
			"to":      l.To.ID(),
			"in":      l.In,
			"out":     l.Out,
			"asset":   string(l.To.Asset),
			"kind":    l.Kind.wireKind(),
			"applied": applied,
		})
		record(l.From, -l.In, i, applied)
		record(l.To, l.Out, i, applied)
	}

	flash := make([]map[string]interface{}, 0, len(net))
	flashMap := make(map[string]float64, len(net))
	for id, d := range net {
		if mathAbs(d) < 1e-12 {
			continue
		}
		va := meta[id]
		flash = append(flash, map[string]interface{}{
			"node":  id,
			"venue": va[0],
			"asset": va[1],
			"delta": d,
		})
		flashMap[id] = d
	}

	session.Mu.Lock()
	balances := session.Wallets.Clone()
	session.Mu.Unlock()

	payload := map[string]interface{}{
		"event":                "omni_executed",
		"route":                plan.Route(),
		"path":                 path,
		"trade_route":          path,
		"legs":                 legs,
		"intermediate_volumes": intermediates,
		"flash_deltas":         flash,
		"flash_delta_by_node":  flashMap,
		"balances":             balances,
		"net_profit_usd":       plan.NetProfit,
		"start_venue":          plan.Start.Venue,
		"start_asset":          string(plan.Start.Asset),
		"start_amount":         plan.StartAmount,
		"volume_btc":           plan.VolumeBTCEquiv,
		"timestamp":            time.Now().Format("15:04:05.000"),
	}
	if b, err := json.Marshal(payload); err == nil {
		session.WriteMessage(websocket.TextMessage, b)
	}
}

func mathAbs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
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
			// Marca los activos del ciclo para inyección dinámica (ETH/SOL…).
			session.Mu.Lock()
			session.Credit.PendingCrypto = creditAssetsForCycle(cycle)
			session.Mu.Unlock()
			if projected, ok := cycleCreditProjection(cycle, balances, p, btcPrice); ok {
				e.handleLiquidityShortfall(session, projected)
			} else {
				e.notifyCycleUnfundable(session, cycle)
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

	// FASE 2 (refactor Probar Bot): la animación de la luz verde recorriendo el
	// camino nace del plan REALMENTE ejecutado — vale igual para el autopiloto
	// del radar y para el botón "Oportunidad normal" (omni.go).
	emitCycleExecuted(session, plan)

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

	// Empuja el radar YA con los saldos sesgados (no esperar al barrido ~1/s).
	e.pushSessionGraph(session)

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

// pushSessionGraph envía un graph_update inmediato con los wallets actuales:
// tras un ciclo/trade el radar no debe esperar al barrido de ~1 s (en demos de
// "Probar bot" a veces ni llega un tick de mercado y los nodos se veían congelados).
func (e *HFTEngine) pushSessionGraph(session *ClientSession) {
	if e == nil || e.Graph == nil || session == nil || !session.IsInitialized() {
		return
	}
	session.Mu.Lock()
	wallets := session.Wallets.Clone()
	session.Mu.Unlock()
	p := session.Params()
	now := time.Now()
	userCycle := e.Graph.FindBestCycleFor(p, now)
	snap := e.Graph.SnapshotFor(wallets, p, userCycle, now)
	sendEvent(session, ServerEvent{Type: "graph_update", SessionID: session.ID, Graph: snap})
}
