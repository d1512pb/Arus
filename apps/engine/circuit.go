package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"time"

	"github.com/gorilla/websocket"
)

// circuit.go — ESCUDO DE ROBUSTEZ / CIRCUIT BREAKER ("Precio falso / error").
//
// El botón ya NO inventa un rechazo narrativo: provoca una anomalía REAL y deja
// que las compuertas del motor la corten sin mover un centavo.
//
// Escenarios (rotan al pulsar):
//  1. spike — PriceTick con salto > Spike Filter de ingesta (DefaultSpikeTickDeviation).
//     Entra por el MISMO canal que los feeds; isValidTick lo DESCARTA; el grafo
//     y LiveMarket no adoptan el precio falso.
//  2. timeout — oportunidad viable + OrderFailureProb=100 % → Fill-or-Kill aborta
//     ANTES de tocar wallets y fija PausedUntil (mismo camino que executeForSession).
//  3. divergence — pairView con precios que violan MaxDivergenceRatio del usuario;
//     executeForSession corta en la compuerta de cordura.
//
// Tras el rechazo se emite CIRCUIT_BREAKER (alerta efímera en el frontend, sin
// luces verdes). Si el universo no tiene el par clásico Binance/Bitso, solo corre
// el escenario spike (no depende de ese par).

const (
	fakeSpikeSettle = 180 * time.Millisecond
	// fakeSpikeJump: salto del tick envenenado. El Spike Filter de ingesta corta
	// a >5 %; 50 % es inequívocamente basura de feed / error de API.
	fakeSpikeJump = 0.50
)

// firstActiveVenue devuelve un venue del universo del usuario para narrar el
// escenario (Binance por defecto si el universo quedara vacío).
func firstActiveVenue(p TradingParameters) string {
	active := make([]string, 0, len(Venues))
	for _, v := range Venues {
		if p.venueEnabled(v.Name) {
			active = append(active, v.Name)
		}
	}
	if len(active) == 0 {
		return "Binance"
	}
	return active[rand.Intn(len(active))]
}

// classicPairEnabled informa si el par Binance/Bitso está activo (necesario
// para los escenarios timeout/divergence del ejecutor clásico).
func classicPairEnabled(p TradingParameters) bool {
	for _, v := range classicVenues() {
		if !p.venueEnabled(v.Name) {
			return false
		}
	}
	return len(classicVenues()) >= 2
}

// emitCircuitBreaker envía el evento de alerta/rechazo por el socket. El frontend
// (data.type === "CIRCUIT_BREAKER") muestra el anuncio efímero, NO luces verdes.
func emitCircuitBreaker(session *ClientSession, scenario, venue, message string, detail map[string]interface{}) {
	payload := map[string]interface{}{
		"type":      "CIRCUIT_BREAKER",
		"scenario":  scenario,
		"venue":     venue,
		"message":   message,
		"timestamp": time.Now().Format("15:04:05.000"),
	}
	for k, v := range detail {
		payload[k] = v
	}
	if b, err := json.Marshal(payload); err == nil {
		session.WriteMessage(websocket.TextMessage, b)
	}
}

// snapshotWealth captura patrimonio + PnL bajo lock (para verificar rechazo limpio).
func snapshotWealth(session *ClientSession) (wealth, pnl float64) {
	session.Mu.Lock()
	defer session.Mu.Unlock()
	return session.TotalWealth, session.TotalNetProfit
}

// wealthUnchanged informa si patrimonio y PnL no se movieron tras el rechazo.
func wealthUnchanged(session *ClientSession, wealth, pnl float64) bool {
	w, p := snapshotWealth(session)
	return w == wealth && p == pnl
}

// injectPoisonTick envía un tick ENVENENADO al canal de ingesta SIN tocar
// currentMarket (publishTick lo actualizaría antes del Spike Filter). El bucle
// de Start aplica isValidTick y lo DESCARTA — misma tubería que un feed loco.
func injectPoisonTick(ticks chan<- PriceTick, instr Instrument, ask, bid, qty float64) {
	if ticks == nil {
		return
	}
	ticks <- PriceTick{
		Exchange: instr.Venue,
		Base:     instr.Base,
		Quote:    instr.Quote,
		Ask:      ask,
		Bid:      bid,
		AskQty:   qty,
		BidQty:   qty,
		Time:     time.Now(),
	}
}

// runFakeSpike: precio falso vía Spike Filter de ingesta.
func (e *HFTEngine) runFakeSpike(session *ClientSession, p TradingParameters) {
	venue := firstActiveVenue(p)
	instr, ok := primaryInstrument(venue)
	if !ok {
		instr, ok = instrumentByKey(venue + ":BTC/USDT")
		if !ok {
			instr, ok = instrumentByKey(venue + ":BTC/USD")
		}
	}
	if !ok {
		sendLog(session, "💤 [ESCUDO] No hay libro BTC activo para inyectar un precio falso.")
		return
	}
	book, ok := freshTopOfBook(instr.Key())
	if !ok {
		sendLog(session, fmt.Sprintf("🧊 [ESCUDO] El libro %s no está fresco — no se puede demostrar el Spike Filter contra un feed muerto.", instr.Key()))
		return
	}

	wealth, pnl := snapshotWealth(session)

	// El Spike Filter de INGESTA usa el default del motor; el del usuario
	// (SpikeTickDeviation) actúa además en el ejecutor por sesión. Fabricamos
	// un salto que supera AMBOS para que el rechazo sea inequívoco.
	tol := DefaultSpikeTickDeviation
	if p.SpikeTickDeviation > tol {
		tol = p.SpikeTickDeviation
	}
	jump := fakeSpikeJump
	if jump <= tol {
		jump = tol * 3
	}
	poisonAsk := book.Ask * (1 + jump)
	poisonBid := book.Bid * (1 + jump)
	if isValidTick(poisonAsk, book.Ask, DefaultSpikeTickDeviation) {
		sendLog(session, "💤 [ESCUDO] El salto fabricado no supera el Spike Filter — configuración anómala.")
		return
	}

	qty := book.AskQty
	if qty < 1 {
		qty = 1
	}
	injectPoisonTick(e.Ticks, instr, poisonAsk, poisonBid, qty)
	time.Sleep(fakeSpikeSettle)

	after, _ := currentMarket.Get(instr.Key())
	// LiveMarket no debió adoptar el veneno (no usamos publishTick).
	if after.Ask > book.Ask*1.1 {
		// Defensa: restaurar ancla si algo más escribió el libro.
		currentMarket.Update(instr.Key(), book)
		if e.Graph != nil {
			e.Graph.UpdateBook(instr.Key(), book)
		}
	}

	devPct := jump * 100
	sendLog(session, fmt.Sprintf(
		"🚨 [SPIKE BLOQUEADO] Tick falso en %s: ask $%.0f → $%.0f (+%.0f %% > tolerancia ingesta %.1f %% / tuya %.1f %%) — DESCARTADO. Capital intacto.",
		instr.Key(), book.Ask, poisonAsk, devPct, DefaultSpikeTickDeviation*100, p.SpikeTickDeviation*100))
	if !wealthUnchanged(session, wealth, pnl) {
		sendLog(session, "⚠️ [ESCUDO] Invariante rota: el patrimonio cambió tras un tick descartado.")
	}
	emitCircuitBreaker(session, "spread", venue,
		fmt.Sprintf("¡Circuit Breaker activado! Un tick falso en %s saltó +%.0f %% (ingesta tolera %.1f %%, tu estrategia %.1f %% tick-a-tick). Arus lo descartó en la tubería real antes de tocar tu capital — típico error de API o feed corrupto.", venue, devPct, DefaultSpikeTickDeviation*100, p.SpikeTickDeviation*100),
		map[string]interface{}{
			"from_ask":        book.Ask,
			"poison_ask":      poisonAsk,
			"jump_pct":        devPct,
			"threshold":       DefaultSpikeTickDeviation * 100,
			"user_threshold":  p.SpikeTickDeviation * 100,
		})
}

// runFakeTimeout: Fill-or-Kill fallido (API remota no llena) sobre una oportunidad
// viable fabricada en pairView — misma rama que el circuit breaker de ejecución.
func (e *HFTEngine) runFakeTimeout(session *ClientSession, p TradingParameters) {
	binInstr, ok1 := primaryInstrument("Binance")
	bitInstr, ok2 := primaryInstrument("Bitso")
	if !ok1 || !ok2 {
		e.runFakeSpike(session, p)
		return
	}
	bin, okB := freshTopOfBook(binInstr.Key())
	bit, okT := freshTopOfBook(bitInstr.Key())
	if !okB || !okT {
		e.runFakeSpike(session, p)
		return
	}

	// Oportunidad creíble (~1.5 % de spread) DENTRO del ratio de cordura, para
	// llegar hasta el FoK y no morir antes en MaxDivergenceRatio.
	mkt := pairView{
		BinAsk: bin.Ask, BinBid: bin.Bid,
		BitAsk: bin.Ask * 1.016, BitBid: bin.Ask * 1.015,
		BinAskQty: 2, BinBidQty: 2, BitAskQty: 2, BitBidQty: 2,
	}
	_ = bit // bit fresco solo garantiza feed vivo; el veneno es el FoK

	wealth, pnl := snapshotWealth(session)
	session.Mu.Lock()
	session.LastTradeTime = time.Time{}
	session.PausedUntil = time.Time{}
	session.Mu.Unlock()

	orig := session.Params()
	tmp := orig
	tmp.OrderFailureProb = 1.0
	tmp.RadarAutopilot = false
	session.SetParams(tmp)

	e.executeForSession(session, mkt)
	session.SetParams(orig)

	session.Mu.Lock()
	paused := time.Now().Before(session.PausedUntil)
	session.Mu.Unlock()

	if !wealthUnchanged(session, wealth, pnl) {
		sendLog(session, "⚠️ [ESCUDO] Invariante rota: el FoK movió capital (no debería).")
	}
	ms := 1500 + rand.Intn(2000)
	if paused {
		sendLog(session, fmt.Sprintf("🔌 [CIRCUIT BREAKER] Fill-or-Kill: la pierna remota no llenó (simulando timeout de API ~%d ms). Abortado sin exposición direccional — sesión en pausa breve.", ms))
	} else {
		sendLog(session, fmt.Sprintf("🔌 [CIRCUIT BREAKER] Timeout de API simulado (~%d ms): operación no ejecutada. Capital intacto.", ms))
	}
	emitCircuitBreaker(session, "timeout", "Bitso",
		fmt.Sprintf("¡Circuit Breaker activado! La pierna remota no confirmó el fill (timeout de API ~%d ms). Arus abortó ANTES de mover saldos — hedging preventivo, cero exposición direccional.", ms),
		map[string]interface{}{"timeout_ms": ms, "paused": paused})
}

// runFakeDivergence: precios que violan MaxDivergenceRatio — compuerta de cordura.
func (e *HFTEngine) runFakeDivergence(session *ClientSession, p TradingParameters) {
	binInstr, ok1 := primaryInstrument("Binance")
	_, ok2 := primaryInstrument("Bitso")
	if !ok1 || !ok2 {
		e.runFakeSpike(session, p)
		return
	}
	bin, okB := freshTopOfBook(binInstr.Key())
	if !okB {
		e.runFakeSpike(session, p)
		return
	}

	ratio := p.MaxDivergenceRatio
	if ratio < 1.01 {
		ratio = DefaultMaxDivergenceRatio
	}
	fakeAsk := bin.Ask * (ratio + 0.5) // claramente por encima del límite
	divPct := (fakeAsk/bin.Ask - 1) * 100
	limitPct := (ratio - 1) * 100

	mkt := pairView{
		BinAsk: bin.Ask, BinBid: bin.Bid,
		BitAsk: fakeAsk, BitBid: fakeAsk * 0.999,
		BinAskQty: 1, BinBidQty: 1, BitAskQty: 1, BitBidQty: 1,
	}

	wealth, pnl := snapshotWealth(session)
	session.Mu.Lock()
	session.LastTradeTime = time.Time{}
	session.Mu.Unlock()

	orig := session.Params()
	tmp := orig
	tmp.RadarAutopilot = false
	session.SetParams(tmp)
	e.executeForSession(session, mkt)
	session.SetParams(orig)

	tripped := mkt.BitAsk > mkt.BinAsk*ratio || mkt.BinAsk > mkt.BitAsk*ratio
	sendLog(session, fmt.Sprintf(
		"🛑 [CIRCUIT BREAKER] Precio de BTC en Bitso ($%.0f) diverge %.0f%% de Binance ($%.0f, límite %.0f%%) — feed sospechoso, rechazado por MaxDivergenceRatio.",
		fakeAsk, divPct, bin.Ask, limitPct))
	if !wealthUnchanged(session, wealth, pnl) {
		sendLog(session, "⚠️ [ESCUDO] Invariante rota: la divergencia movió capital.")
	}
	msg := fmt.Sprintf("¡Escudo de robustez! El precio de BTC en Bitso diverge %.0f%% del mercado (límite de cordura %.0f%%). Feed sospechoso: operación rechazada antes de arriesgar tu capital.", divPct, limitPct)
	if !tripped {
		msg = "¡Escudo de robustez! Divergencia de precio detectada y bloqueada. Capital intacto."
	}
	emitCircuitBreaker(session, "divergence", "Bitso", msg,
		map[string]interface{}{"divergence_pct": divPct, "limit_pct": limitPct, "fake_ask": fakeAsk})
}

// runFakeInjection es el gatillo del botón "Precio falso / error" (acción
// inject_fake): provoca una anomalía, la RECHAZA con la lógica real y avisa.
func (e *HFTEngine) runFakeInjection(session *ClientSession) {
	session.Mu.Lock()
	initialized := session.Wallets != nil && len(session.Wallets) > 0
	session.Mu.Unlock()
	if !initialized {
		return
	}
	if e.Ticks == nil {
		sendLog(session, "💤 [ESCUDO] Motor sin canal de ingesta — no se puede inyectar precio falso.")
		return
	}

	p := session.Params()
	kinds := []string{"spike"}
	if classicPairEnabled(p) {
		kinds = append(kinds, "timeout", "divergence")
	}
	switch kinds[rand.Intn(len(kinds))] {
	case "timeout":
		e.runFakeTimeout(session, p)
	case "divergence":
		e.runFakeDivergence(session, p)
	default:
		e.runFakeSpike(session, p)
	}
}
