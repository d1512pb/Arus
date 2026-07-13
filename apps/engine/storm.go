package main

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// storm.go — FASE 3 del refactor de "Probar Bot": EVENTO POCO COMÚN / TORMENTA HFT.
//
// Ya NO fabrica ganancias con demoNet/commitOmni (atajo que movía wallets sin
// fees). La ráfaga sigue la misma filosofía que FASE 1 y FASE 2:
//
//  1. Lee el universo ACTIVO y enumera rutas triangulares + espaciales
//     (listOmniTargets).
//  2. Varias goroutines inyectan micro-ineficiencias como MarketTicks reales
//     (applyOmniShift → publishTick + grafo), ancladas a precios vivos y
//     dentro del Spike Filter.
//  3. El radar descubre el ciclo (FindBestCycleFor) y se ejecuta con planCycle
//     + commitCycle (fees, slippage, saldo, tope, margen del usuario) —
//     serializado por IsExecuting (mutex bajo presión).
//  4. Cada fill emite storm_trade para que el frontend pinte luces rápidas;
//     al cerrar, storm_ended con el resumen.
//
// El cooldown de 3 s del autopiloto y el Fill-or-Kill se suspenden SOLO mientras
// StormActive: la tormenta es una prueba de estrés controlada, no trading live.

const (
	stormDurationMs = 7500 // ráfaga más legible (antes ~4 s se veía como spam)
	stormWorkers    = 2    // menos paralelismo → luces más claras
	stormMinCash    = 40.0
	stormTargetTrades = 8 // menos fills: el sesgo se ve sin marear el radar
	// stormMaxOrderBTC acota cada micro-oportunidad; con 8 fills unidireccionales
	// el sesgo BTC queda bien visible (~0.08 BTC).
	stormMaxOrderBTC = 0.01
	stormTickMinMs   = 320
	stormTickMaxMs   = 520
	stormExtraReturn = 0.0010
)

// emitStormEvent envía un evento crudo de la tormenta por el socket.
func emitStormEvent(session *ClientSession, payload map[string]interface{}) {
	if b, err := json.Marshal(payload); err == nil {
		session.WriteMessage(websocket.TextMessage, b)
	}
}

// emitStormTrade anuncia UNA operación de la tormenta: el frontend dispara una
// luz verde buy→sell, actualiza P&L y sincroniza saldos (sesgo espacial).
func emitStormTrade(session *ClientSession, buyVenue, sellVenue string, net float64, balances Balances) {
	session.Mu.Lock()
	total := session.TotalWealth
	netProfit := session.TotalNetProfit
	session.Mu.Unlock()
	payload := map[string]interface{}{
		"type":             "storm_trade",
		"buy_venue":        buyVenue,
		"sell_venue":       sellVenue,
		"net_profit_usd":   net,
		"total_wealth":     total,
		"total_net_profit": netProfit,
		"timestamp":        time.Now().Format("15:04:05.000"),
	}
	if balances != nil {
		payload["balances"] = balances
	}
	emitStormEvent(session, payload)
}

// stormParams devuelve una copia de los params del usuario afinada para micro-
// fills: tope de orden pequeño y FoK apagado (la pausa de 2 s mataría la ráfaga).
// Fees, slippage, margen mínimo y universo se conservan — el veredicto sigue
// siendo el del motor real.
func stormParams(p TradingParameters) TradingParameters {
	sp := p
	if sp.MaxOrderSizeBTC <= 0 || sp.MaxOrderSizeBTC > stormMaxOrderBTC {
		sp.MaxOrderSizeBTC = stormMaxOrderBTC
	}
	sp.OrderFailureProb = 0
	return sp
}

// executeStormCycle ejecuta un ciclo detectado DURANTE la tormenta: misma
// tubería que el autopiloto (planCycle + commitCycle) pero sin cooldown de 3 s
// ni omni_executed (las luces van por storm_trade). Devuelve false si otra
// goroutine tiene el lock, no hay plan viable o el commit falla.
func (e *HFTEngine) executeStormCycle(session *ClientSession, cycle *Cycle, p TradingParameters) (net, volBTC, feesUSD float64, buyVenue, sellVenue string, wallets Balances, ok bool) {
	session.Mu.Lock()
	if session.Wallets == nil || !session.StormActive || session.IsReplenishing || session.IsExecuting {
		session.Mu.Unlock()
		return 0, 0, 0, "", "", nil, false
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
		return 0, 0, 0, "", "", nil, false
	}
	if !commitCycle(session, plan) {
		return 0, 0, 0, "", "", nil, false
	}

	buyVenue, sellVenue = plan.Start.Venue, plan.Start.Venue
	for _, l := range plan.Legs {
		if l.Kind == EdgeOrderBook {
			if buyVenue == plan.Start.Venue {
				buyVenue = l.From.Venue
			}
			sellVenue = l.To.Venue
		}
	}
	session.Mu.Lock()
	wallets = session.Wallets.Clone()
	session.Mu.Unlock()
	// Radar en vivo durante la ráfaga (no esperar storm_ended).
	e.pushSessionGraph(session)
	return plan.NetProfit, plan.VolumeBTCEquiv, plan.FeesUSD, buyVenue, sellVenue, wallets, true
}

// stormFireOnce inyecta una micro-ineficiencia sobre un target, deja que el
// radar la descubra y intenta ejecutarla. Un intento = un potencial trade.
func (e *HFTEngine) stormFireOnce(session *ClientSession, target omniTarget, p TradingParameters) bool {
	if e.Ticks == nil || e.Graph == nil {
		return false
	}

	anchors := make(map[string]TopOfBook)
	for _, in := range append([]Instrument{target.buyBook, target.sellBook}, target.support...) {
		b, ok := freshTopOfBook(in.Key())
		if !ok {
			return false
		}
		anchors[in.Key()] = b
	}

	btcPrice := getBTCPrice()
	if btcPrice <= 0 {
		return false
	}
	entry := p.MaxOrderSizeBTC * btcPrice
	session.Mu.Lock()
	bal := 0.0
	if session.Wallets != nil {
		bal = session.Wallets.Get(target.startVenue, target.startCash)
	}
	session.Mu.Unlock()
	if bal < stormMinCash {
		return false
	}
	if bal < entry {
		entry = bal * 0.08
	}
	if entry < 1 {
		return false
	}

	netReturn := stormExtraReturn + p.MinNetProfitUSD*1.4/entry
	shift := omniShiftNeeded(target, anchors, netReturn)
	half := shift / 2
	if half > omniMaxShift {
		half = omniMaxShift
	}
	if half <= 0 {
		half = 0.008 // colchón mínimo si el mercado ya estaba desalineado
	}

	qtyFor := func(in Instrument, b TopOfBook) float64 {
		q := 1.0
		if px := assetPriceUSD(in.Base); px > 0 {
			q = 3 * entry / px
		}
		return math.Max(q, math.Max(b.AskQty, b.BidQty))
	}

	buyAnchor := anchors[target.buyBook.Key()]
	sellAnchor := anchors[target.sellBook.Key()]
	applyOmniShift(e, target.buyBook, buyAnchor.Ask*(1-half), buyAnchor.Bid*(1-half), qtyFor(target.buyBook, buyAnchor))
	applyOmniShift(e, target.sellBook, sellAnchor.Ask*(1+half), sellAnchor.Bid*(1+half), qtyFor(target.sellBook, sellAnchor))

	cycle := e.Graph.FindBestCycleFor(p, time.Now())
	if cycle == nil {
		return false
	}
	if !cycleMatchesOmniTarget(cycle, target) {
		return false
	}
	net, vol, fees, buy, sell, wallets, ok := e.executeStormCycle(session, cycle, p)
	if !ok {
		return false
	}
	emitStormTrade(session, buy, sell, net, wallets)
	recordTradeAsync(TradeRecord{
		SessionID:    session.ID,
		Timestamp:    time.Now(),
		BuyExchange:  buy,
		SellExchange: sell,
		VolumeBTC:    vol,
		SpreadUSD:    0,
		FeesUSD:      fees,
		NetProfitUSD: net,
	})
	return true
}

// runStormInjection es el gatillo del botón "Evento poco común" (acción
// inject_storm): ráfaga concurrente de micro-oportunidades por la tubería real.
func (e *HFTEngine) runStormInjection(session *ClientSession) {
	session.Mu.Lock()
	initialized := session.Wallets != nil && len(session.Wallets) > 0
	replenishing := session.IsReplenishing
	already := session.StormActive
	startWealth := session.TotalWealth
	var wallets Balances
	if initialized {
		wallets = session.Wallets.Clone()
	}
	if initialized && !replenishing && !already {
		session.StormActive = true
	}
	session.Mu.Unlock()

	if !initialized {
		return
	}
	if replenishing {
		sendLog(session, "⏸️ [TORMENTA] Bot en pausa por reequilibrio — la ráfaga se ignora hasta que termine.")
		return
	}
	if already {
		sendLog(session, "⚡ [TORMENTA] Ya hay una ráfaga en curso — espera a que termine.")
		return
	}
	if e.Ticks == nil || e.Graph == nil {
		session.Mu.Lock()
		session.StormActive = false
		session.Mu.Unlock()
		return
	}

	p := stormParams(session.Params())
	if cap := analyzeUniverse(p); !cap.OK {
		session.Mu.Lock()
		session.StormActive = false
		session.Mu.Unlock()
		sendLog(session, "💤 [TORMENTA] "+cap.Reason)
		return
	}
	// Prioriza espaciales y UNA sola dirección (compra@A → venta@B).
	// Si la ráfaga mezcla A→B y B→A, el BTC se cancela al final y el radar
	// parece congelado: solo se ve subir el USD del neto.
	targets := lockOmniTargetsOneWay(preferSpatialOmniTargets(listOmniTargets(p, wallets)))
	if len(targets) == 0 {
		session.Mu.Lock()
		session.StormActive = false
		session.Mu.Unlock()
		sendLog(session, "💤 [TORMENTA] No hay rutas omnidireccionales con tu universo actual (hace falta BTC activo y libros en vivo; con una sola casa, activa ETH o SOL para triangular).")
		return
	}

	nTri, nEsp := 0, 0
	for _, t := range targets {
		if t.kind == "triangular" {
			nTri++
		} else {
			nEsp++
		}
	}
	dirNote := ""
	if nEsp > 0 {
		dirNote = fmt.Sprintf(" · dirección fija %s→%s", targets[0].buyBook.Venue, targets[0].sellBook.Venue)
	}
	sendLog(session, fmt.Sprintf(
		"⚡ [TORMENTA] Ráfaga HFT (%.1f s · %d workers): %d rutas (%d triangulares, %d espaciales)%s — micro-ticks por la tubería REAL, el radar ejecuta con TUS fees bajo presión de mutex…",
		stormDurationMs/1000.0, stormWorkers, len(targets), nTri, nEsp, dirNote))
	emitStormEvent(session, map[string]interface{}{
		"type":        "storm_started",
		"duration_ms": stormDurationMs,
		"routes":      len(targets),
	})

	end := time.Now().Add(stormDurationMs * time.Millisecond)
	var trades int64

	var wg sync.WaitGroup
	for w := 0; w < stormWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for time.Now().Before(end) {
				if atomic.LoadInt64(&trades) >= stormTargetTrades {
					return
				}
				target := targets[rand.Intn(len(targets))]
				if e.stormFireOnce(session, target, p) {
					atomic.AddInt64(&trades, 1)
				}
				time.Sleep(time.Duration(stormTickMinMs+rand.Intn(stormTickMaxMs-stormTickMinMs)) * time.Millisecond)
			}
		}()
	}
	wg.Wait()

	session.Mu.Lock()
	session.StormActive = false
	endWealth := session.TotalWealth
	session.Mu.Unlock()

	profit := endWealth - startWealth
	n := atomic.LoadInt64(&trades)
	sendLog(session, fmt.Sprintf(
		"✅ [TORMENTA] Ráfaga superada: %d micro-operaciones en %.1f s | Ganancia: +$%.2f. El motor absorbió el caos (goroutines + mutex) sin romperse.",
		n, stormDurationMs/1000.0, profit))
	emitStormEvent(session, map[string]interface{}{
		"type":        "storm_ended",
		"trades":      n,
		"profit":      profit,
		"duration_ms": stormDurationMs,
	})
	sendWalletUpdate(session)
}
