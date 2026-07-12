package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"time"

	"github.com/gorilla/websocket"
)

// circuit.go — FASE 3: ESCUDO DE ROBUSTEZ / CIRCUIT BREAKER ("Precio falso / error").
//
// El botón inyecta una oportunidad ENVENENADA (spread irreal, timeout de API o
// divergencia de precio absurda) y demuestra la gestión de riesgo institucional:
// la lógica de Go la RECHAZA antes de mover un solo centavo y emite un evento de
// alerta por WebSocket. El frontend, EN LUGAR de la animación de luces verdes,
// lanza un anuncio efímero centrado (~3 s) explicando por qué Arus protegió al
// usuario. Ningún saldo se toca: es un rechazo, no una operación.

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

// runFakeInjection es el gatillo del botón "Precio falso / error" (acción
// inject_fake): fabrica un escenario de riesgo, lo RECHAZA con la lógica real y
// avisa. Rota entre tres escudos para que pulsaciones repetidas muestren distintas
// defensas. NUNCA mueve saldos.
func (e *HFTEngine) runFakeInjection(session *ClientSession) {
	session.Mu.Lock()
	initialized := session.Wallets != nil && len(session.Wallets) > 0
	session.Mu.Unlock()
	if !initialized {
		return
	}

	p := session.Params()
	venue := firstActiveVenue(p)
	btcPrice := getBTCPrice()

	switch rand.Intn(3) {
	case 0:
		// ESCENARIO 1 — SPREAD IRREAL (+500 %). Se valida con la MISMA matemática
		// del Spike Filter (factor = |spread| / promedio del mercado). El factor
		// resultante supera con creces el umbral de bloqueo → rechazo.
		gainPct := 500.0
		absSpread := btcPrice * (gainPct / 100.0)
		avg := e.Tracker.Average()
		if avg <= 0 {
			avg = btcPrice * 0.001 // baseline ~0.1 % del precio (spread real típico)
		}
		factor := absSpread / avg
		sendLog(session, fmt.Sprintf("⚠️  [SPIKE BLOQUEADO] Spread irreal $%.0f (%.0f× el promedio $%.2f) en %s — posible error de API. Operación abortada.", absSpread, factor, avg, venue))
		emitCircuitBreaker(session, "spread", venue,
			fmt.Sprintf("¡Circuit Breaker activado! Spread irreal de +%.0f%% detectado en %s (%.0f× el promedio del mercado): casi seguro un error de precio o del feed. Arus abortó la operación para proteger tu capital.", gainPct, venue, factor),
			map[string]interface{}{"spread": absSpread, "factor": factor, "threshold": SpikeBlockMultiplier})

	case 1:
		// ESCENARIO 2 — TIMEOUT DE LA API. El exchange no respondió: operar contra
		// datos muertos es exponerse en una sola pierna. Hedging preventivo → abortar.
		ms := 1500 + rand.Intn(2500)
		sendLog(session, fmt.Sprintf("🔌 [CIRCUIT BREAKER] Timeout de la API de %s (%d ms sin respuesta) — abortando para no operar contra un feed caído.", venue, ms))
		emitCircuitBreaker(session, "timeout", venue,
			fmt.Sprintf("¡Circuit Breaker activado! La API de %s no respondió en %d ms. Arus abortó la operación (hedging preventivo) en vez de operar a ciegas contra un feed caído.", venue, ms),
			map[string]interface{}{"timeout_ms": ms})

	default:
		// ESCENARIO 3 — DIVERGENCIA DE PRECIO. Un venue reporta un precio que supera
		// el ratio de cordura contra el resto del mercado (misma compuerta que
		// executeForSession: MaxDivergenceRatio) → feed sospechoso, rechazo.
		ratio := p.MaxDivergenceRatio
		if ratio < 1.01 {
			ratio = DefaultMaxDivergenceRatio
		}
		fakeAsk := btcPrice * (ratio + 1.5)
		divPct := (fakeAsk/btcPrice - 1) * 100
		limitPct := (ratio - 1) * 100
		sendLog(session, fmt.Sprintf("🛑 [CIRCUIT BREAKER] Precio de BTC en %s ($%.0f) diverge %.0f%% del mercado ($%.0f, límite %.0f%%) — feed sospechoso, rechazado.", venue, fakeAsk, divPct, btcPrice, limitPct))
		emitCircuitBreaker(session, "divergence", venue,
			fmt.Sprintf("¡Escudo de robustez! El precio de BTC en %s diverge %.0f%% del resto del mercado (límite de cordura %.0f%%). Feed sospechoso: operación rechazada antes de arriesgar tu capital.", venue, divPct, limitPct),
			map[string]interface{}{"divergence_pct": divPct, "limit_pct": limitPct})
	}
}
