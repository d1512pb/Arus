package main

// simulate.go — FASE 1 del refactor de "Probar Bot": la TUBERÍA BASE.
//
// POST /api/simulate/custom recibe un escenario de mercado definido por el
// usuario (dos libros con su bid/ask) y lo inyecta como MarketTicks REALES en
// el canal de ingesta del motor — publishTick, el MISMO punto de entrada que
// usan los WebSockets de Binance/Bitso/Kraken. No existe un camino "de demo"
// divergente: el tick pasa por el Spike Filter de ingesta, el control de
// staleness, la actualización del grafo, computeNetProfit (fees + slippage DEL
// USUARIO) y el fan-out multi-tenant, y el veredicto se emite por el WebSocket
// de la sesión (logs [OPORTUNIDAD]/[ARBITRAJE]/[DESCARTADO], wallet_update…).
//
// Validación ANTES de inyectar (el backend nunca corrige en silencio):
//   1. La sesión debe existir (viva en el Hub) y estar inicializada.
//   2. Cada libro debe existir en el registro de instrumentos (venue + moneda
//      contra el efectivo del venue).
//   3. Venue y moneda deben pertenecer al universo ACTIVO del usuario
//      (enabled_venues / enabled_assets) — si no, error claro y NO se inyecta.
//   4. Cada libro debe ser coherente (coherentBook: mismos escudos que la
//      ingesta real) — la ineficiencia se crea ENTRE casas, no cruzando un libro.

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
)

// customSimRequest es el payload de POST /api/simulate/custom.
type customSimRequest struct {
	// SessionID es el token de la sesión (el mismo UUID de resume_session): la
	// validación de universo se hace contra los parámetros de ESA sesión.
	SessionID string `json:"session_id"`

	ExchangeA string  `json:"exchange_a"`
	AssetA    string  `json:"asset_a"`
	BidA      float64 `json:"bid_a"`
	AskA      float64 `json:"ask_a"`

	ExchangeB string  `json:"exchange_b"`
	AssetB    string  `json:"asset_b"`
	BidB      float64 `json:"bid_b"`
	AskB      float64 `json:"ask_b"`

	// Liquidity: cantidad visible en cada punta (unidades del activo base).
	// Opcional; ausente = 1.0. Se clampea a MaxDemoLiquidity como el resto del
	// simulador (nunca un order book absurdo que cuelgue el motor).
	Liquidity float64 `json:"liquidity,omitempty"`
}

// simError responde un error del simulador como JSON {"error": "..."}.
func simError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// resolveCashBook busca en el registro el libro moneda/efectivo de un venue
// (p. ej. Binance+BTC → Binance:BTC/USDT). Los libros cripto-cripto (ETH/BTC)
// no son inyectables desde la prueba custom: el escenario del usuario se define
// en precios de efectivo, igual que el top-of-book que ve en la UI.
func resolveCashBook(venue, asset string) (Instrument, bool) {
	for _, in := range Instruments {
		if in.Venue == venue && in.Base == asset && isCashAsset(Asset(in.Quote)) {
			return in, true
		}
	}
	return Instrument{}, false
}

// validateSimLeg valida UNA pierna del escenario contra el registro, el universo
// del usuario y la cordura del libro. Devuelve el instrumento resuelto, o el
// status HTTP y el motivo EXACTO del rechazo (400 = payload/catálogo inválido,
// 403 = el nodo existe pero está fuera del universo del usuario).
func validateSimLeg(p TradingParameters, label, venue, asset string, bid, ask float64) (Instrument, int, string) {
	if !isKnownVenue(venue) {
		return Instrument{}, http.StatusBadRequest, fmt.Sprintf("%s: la casa %q no existe en el catálogo del motor.", label, venue)
	}
	if !isKnownAsset(asset) {
		return Instrument{}, http.StatusBadRequest, fmt.Sprintf("%s: la moneda %q no existe en el catálogo del motor.", label, asset)
	}
	if !p.venueEnabled(venue) {
		return Instrument{}, http.StatusForbidden, fmt.Sprintf("%s: %s no está activo en tu configuración — actívalo en Estrategia → Tu universo antes de probarlo.", label, venue)
	}
	if !p.universeAllows(MarketNode{Asset: Asset(asset), Venue: venue}) {
		return Instrument{}, http.StatusForbidden, fmt.Sprintf("%s: la moneda %s no está activa en tu configuración — actívala en Estrategia → Tu universo antes de probarla.", label, asset)
	}
	instr, ok := resolveCashBook(venue, asset)
	if !ok {
		return Instrument{}, http.StatusBadRequest, fmt.Sprintf("%s: %s no publica un libro %s/efectivo — elige otra combinación.", label, venue, asset)
	}
	for _, v := range []float64{bid, ask} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
			return Instrument{}, http.StatusBadRequest, fmt.Sprintf("%s: precios inválidos — usa números positivos y finitos.", label)
		}
	}
	// Mismo escudo que la ingesta real (coherentBook): lados positivos, libro no
	// cruzado y spread interno < 5 %. La oportunidad de arbitraje se crea con la
	// DIFERENCIA entre casas, no cruzando el bid/ask de un solo libro.
	if !coherentBook(ask, bid) {
		return Instrument{}, http.StatusBadRequest, fmt.Sprintf("%s: libro incoherente — el ask debe ser ≥ bid con spread interno < 5 %%. Crea la oportunidad subiendo el precio de UNA casa frente a la otra.", label)
	}
	return instr, 0, ""
}

// customSimHandler construye el handler de POST /api/simulate/custom con acceso
// al Hub (para resolver la sesión) y al canal de ingesta (para inyectar los
// ticks por la MISMA tubería que los feeds reales).
func customSimHandler(hub *Hub, priceChan chan<- PriceTick) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// CORS: el frontend corre en otro origen (Vercel en prod, :3000 en dev).
		// El POST con JSON dispara preflight, así que se permiten sus headers.
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			simError(w, http.StatusMethodNotAllowed, "Usa POST.")
			return
		}

		var req customSimRequest
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			simError(w, http.StatusBadRequest, "Payload inválido: se espera JSON con exchange_a/asset_a/bid_a/ask_a, exchange_b/… y session_id.")
			return
		}

		if req.SessionID == "" || len(req.SessionID) > 64 {
			simError(w, http.StatusBadRequest, "Falta el session_id de tu sesión.")
			return
		}
		session := hub.Get(req.SessionID)
		if session == nil {
			simError(w, http.StatusNotFound, "Sesión no encontrada o desconectada — recarga la página e inténtalo de nuevo.")
			return
		}
		if !session.IsInitialized() {
			simError(w, http.StatusConflict, "Tu sesión aún no tiene capital configurado.")
			return
		}

		// Snapshot de parámetros de ESTA sesión: la validación de universo usa la
		// configuración vigente del usuario (enabled_venues / enabled_assets).
		p := session.Params()

		instrA, code, msg := validateSimLeg(p, "Mercado A", req.ExchangeA, req.AssetA, req.BidA, req.AskA)
		if msg != "" {
			simError(w, code, msg)
			return
		}
		instrB, code, msg := validateSimLeg(p, "Mercado B", req.ExchangeB, req.AssetB, req.BidB, req.AskB)
		if msg != "" {
			simError(w, code, msg)
			return
		}
		if instrA.Key() == instrB.Key() {
			simError(w, http.StatusBadRequest, "Elige dos libros distintos: la prueba compara dos mercados.")
			return
		}

		liq := req.Liquidity
		if liq == 0 {
			liq = 1.0
		}
		liq = clampFloat(liq, MinExecutableVolumeBTC, MaxDemoLiquidity, 1.0)

		// INYECCIÓN por la tubería real: publishTick registra el top-of-book en
		// LiveMarket y publica el PriceTick en el canal del motor — exactamente lo
		// que hace cada FeedAdapter al parsear un mensaje del exchange. A partir de
		// aquí deciden los MISMOS escudos y la MISMA matemática que en producción:
		// Spike Filter (si el precio se aleja >5 % del último real, se descarta y
		// se narra), staleness, computeNetProfit con los fees del usuario, etc.
		publishTick(priceChan, instrA, req.AskA, req.BidA, liq, liq)
		publishTick(priceChan, instrB, req.AskB, req.BidB, liq, liq)

		sendLog(session, fmt.Sprintf(
			"🧪 [PRUEBA PERSONALIZADA] Ticks inyectados en la tubería real: %s (bid $%.2f / ask $%.2f) vs %s (bid $%.2f / ask $%.2f) · liquidez %.4f — el motor los evalúa con TUS fees, tu slippage y tus filtros.",
			instrA.Key(), req.BidA, req.AskA, instrB.Key(), req.BidB, req.AskB, liq))

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":   "injected",
			"injected": []string{instrA.Key(), instrB.Key()},
			"message":  "Ticks inyectados en el pipeline del motor. El veredicto (oportunidad, spike o rechazo) llega por tu WebSocket en la actividad del bot.",
		})
	}
}
