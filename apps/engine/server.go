package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// initSession inicializa (o resetea) los fondos de una sesión. usdAlloc/btcAlloc
// son la distribución por venue elegida por el usuario (porcentajes ya validados
// por validAllocation, o nil = reparto clásico 50/50 entre el par).
func initSession(s *ClientSession, usd, btc float64, usdAlloc, btcAlloc map[string]float64) {
	s.Mu.Lock()
	defer s.Mu.Unlock()

	// Saldos multi-activo creados desde el registro de venues, todos en cero.
	s.Wallets = make(Balances, len(Venues))
	for _, v := range Venues {
		s.Wallets.Set(v.Name, v.QuoteAsset, 0)
		s.Wallets.Set(v.Name, v.BaseAsset, 0)
	}

	if usdAlloc != nil {
		// Distribución del usuario (modo experto): cada venue recibe su
		// porcentaje del capital — cash al quote del venue, BTC a su base.
		// Kraken (o cualquier venue fuera del par) puede nacer fondeado.
		for venue, pct := range usdAlloc {
			if v, ok := venueByName(venue); ok {
				s.Wallets.Add(v.Name, v.QuoteAsset, usd*pct/100)
			}
		}
		for venue, pct := range btcAlloc {
			if v, ok := venueByName(venue); ok {
				s.Wallets.Add(v.Name, v.BaseAsset, btc*pct/100)
			}
		}
	} else {
		// Reparto clásico: 50/50 SOLO entre el par (repartir usd/2 a cada venue
		// del registro inflaría el capital al agregar un tercero). Los demás
		// venues nacen en cero y se fondean por depósitos o por el autopiloto.
		for _, v := range classicVenues() {
			s.Wallets.Set(v.Name, v.QuoteAsset, usd/2.0)
			s.Wallets.Set(v.Name, v.BaseAsset, btc/2.0)
		}
	}
	s.UsdAlloc = usdAlloc
	s.BtcAlloc = btcAlloc

	btcPrice := DefaultBTCPriceFallback
	if book, ok := currentMarket.Get("Binance:BTC/USDT"); ok && book.Ask > 0 {
		btcPrice = book.Ask
	}

	s.TotalWealth = usd + (btc * btcPrice)
	s.InitialWealth = s.TotalWealth // base del PnL = patrimonio al iniciar
	s.TotalNetProfit = 0
	s.Credit = CreditState{}
	// NOTA: los parámetros de estrategia NO se tocan aquí: un reset de fondos no
	// borra la configuración de riesgo que el usuario eligió (set_params).
	s.InitialUSD = usd
	s.InitialBTC = btc
	s.IsReplenishing = false
	s.ReplenishExpiresAt = time.Time{}
	s.InsufficientFundsPending = false
}

// --- Validación de entradas del cliente (Hallazgo #5) -----------------------------
// El frontend ya valida, pero el backend NUNCA debe confiar en el cliente: un mensaje
// malicioso o corrupto (negativos, NaN/Inf, liquidez gigante) no debe corromper el
// estado ni colgar el motor con un bucle de chunks casi infinito.

func isFinitePositive(x float64) bool {
	return !math.IsNaN(x) && !math.IsInf(x, 0) && x > 0
}

// validInitFunds acepta solo capital inicial finito, positivo y por debajo de topes sanos.
func validInitFunds(usd, btc float64) bool {
	return isFinitePositive(usd) && isFinitePositive(btc) && usd <= MaxInitialUSD && btc <= MaxInitialBTC
}

// validAllocation valida una distribución de capital por venue (modo experto del
// onboarding): solo venues registrados, porcentajes finitos no negativos y suma
// exacta de 100 (tolerancia de redondeo flotante). El backend NO corrige una
// distribución inválida — la rechaza: los números de un experto se respetan al
// centavo o no se aceptan.
func validAllocation(alloc map[string]float64) bool {
	if len(alloc) == 0 {
		return false
	}
	sum := 0.0
	for venue, pct := range alloc {
		if !isKnownVenue(venue) || math.IsNaN(pct) || math.IsInf(pct, 0) || pct < 0 {
			return false
		}
		sum += pct
	}
	return math.Abs(sum-100) < 0.01
}

// sanitizeDemoInject valida y CLAMPEA los parámetros de una inyección del simulador.
// Devuelve ok=false si son irrecuperables (exchange desconocido, NaN/Inf, liquidez ≤ 0).
func sanitizeDemoInject(exchange string, spread, liquidity float64) (string, float64, float64, bool) {
	if !isKnownVenue(exchange) {
		return "", 0, 0, false
	}
	if math.IsNaN(spread) || math.IsInf(spread, 0) || math.IsNaN(liquidity) || math.IsInf(liquidity, 0) {
		return "", 0, 0, false
	}
	if liquidity <= 0 {
		return "", 0, 0, false
	}
	if liquidity > MaxDemoLiquidity {
		liquidity = MaxDemoLiquidity // clamp: nunca un order book absurdo que cuelgue el motor
	}
	if spread > MaxDemoSpreadUSD {
		spread = MaxDemoSpreadUSD
	} else if spread < -MaxDemoSpreadUSD {
		spread = -MaxDemoSpreadUSD
	}
	return exchange, spread, liquidity, true
}

// clampFloat acota v a [lo, hi]; NaN/Inf caen al valor de respaldo fallback.
func clampFloat(v, lo, hi, fallback float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return fallback
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// sanitizeTradingParams construye los parámetros APLICABLES a partir de lo que pidió
// el cliente: cada campo se clampea a su rango sano (models.go) y los fees solo se
// aceptan para venues registrados (claves desconocidas se descartan; venues ausentes
// reciben su fee por defecto). El resultado es siempre un snapshot completo y válido:
// no existe forma de dejar una sesión con parámetros corruptos.
func sanitizeTradingParams(requested TradingParameters) TradingParameters {
	defaults := DefaultTradingParameters()

	fees := make(map[string]float64, len(Venues))
	for _, v := range Venues {
		fee := v.DefaultTakerFee
		if requested.TakerFees != nil {
			if f, ok := requested.TakerFees[v.Name]; ok {
				fee = clampFloat(f, MinTakerFee, MaxTakerFee, v.DefaultTakerFee)
			}
		}
		fees[v.Name] = fee
	}

	// Universo (hito 3): solo venues y activos REGISTRADOS; duplicados fuera.
	// Si tras el filtrado queda vacío, se interpreta como "todos" (nil) — el
	// usuario no puede dejarse a sí mismo sin mercado por accidente.
	var venuesU []string
	seenV := map[string]bool{}
	for _, v := range requested.EnabledVenues {
		if isKnownVenue(v) && !seenV[v] {
			seenV[v] = true
			venuesU = append(venuesU, v)
		}
	}
	var assetsU []string
	seenA := map[string]bool{}
	for _, a := range requested.EnabledAssets {
		if isKnownAsset(a) && !seenA[a] {
			seenA[a] = true
			assetsU = append(assetsU, a)
		}
	}

	applied := TradingParameters{
		TakerFees:          fees,
		MinNetProfitUSD:    clampFloat(requested.MinNetProfitUSD, MinNetProfitFloor, MaxNetProfitCeil, defaults.MinNetProfitUSD),
		MaxOrderSizeBTC:    clampFloat(requested.MaxOrderSizeBTC, MinOrderSizeBTC, MaxOrderSizeCapBTC, defaults.MaxOrderSizeBTC),
		SlippageRate:       clampFloat(requested.SlippageRate, MinSlippageRate, MaxSlippageRate, defaults.SlippageRate),
		SpikeTickDeviation: clampFloat(requested.SpikeTickDeviation, MinSpikeDeviation, MaxSpikeDeviation, defaults.SpikeTickDeviation),
		MaxDivergenceRatio: clampFloat(requested.MaxDivergenceRatio, MinDivergenceRatioLimit, MaxDivergenceRatioLimit, defaults.MaxDivergenceRatio),
		RiskMultiplier:     clampFloat(requested.RiskMultiplier, MinRiskMultiplier, MaxRiskMultiplier, defaults.RiskMultiplier),
		EnabledVenues:      venuesU,
		EnabledAssets:      assetsU,
		RadarAutopilot:     requested.RadarAutopilot,

		// Bloque nuevo (préstamo + simulador): defaults salvo payload v2, abajo.
		CreditLineUSD:        defaults.CreditLineUSD,
		CreditLineBTC:        defaults.CreditLineBTC,
		CreditAPR:            defaults.CreditAPR,
		CreditOriginationFee: defaults.CreditOriginationFee,
		CreditDurationMin:    defaults.CreditDurationMin,
		OrderFailureProb:     defaults.OrderFailureProb,
	}

	// Versionado del wire: un payload SIN el bloque del préstamo (sesión persistida
	// o cliente anteriores a la parametrización del crédito) llega con
	// CreditLineUSD == 0 — todo el bloque conserva los defaults de arriba, en vez
	// de degradar la línea al mínimo o dejar APR/fee en cero por accidente. Un
	// payload v2 SIEMPRE trae la línea (la UI envía el struct completo) y ahí sí
	// se respetan valores explícitos como APR 0 o fee 0.
	if requested.CreditLineUSD != 0 {
		applied.CreditLineUSD = clampFloat(requested.CreditLineUSD, MinCreditLineUSDParam, MaxCreditLineUSDParam, defaults.CreditLineUSD)
		applied.CreditLineBTC = clampFloat(requested.CreditLineBTC, MinCreditLineBTCParam, MaxCreditLineBTCParam, defaults.CreditLineBTC)
		applied.CreditAPR = clampFloat(requested.CreditAPR, 0, MaxCreditAPRParam, defaults.CreditAPR)
		applied.CreditOriginationFee = clampFloat(requested.CreditOriginationFee, 0, MaxCreditFeeParam, defaults.CreditOriginationFee)
		applied.CreditDurationMin = clampFloat(requested.CreditDurationMin, MinCreditDurationMin, MaxCreditDurationMin, defaults.CreditDurationMin)
		applied.OrderFailureProb = clampFloat(requested.OrderFailureProb, 0, MaxOrderFailureProb, defaults.OrderFailureProb)
	}
	return applied
}

func sendEvent(s *ClientSession, ev ServerEvent) {
	// Enrich with wallet state if initialized
	s.Mu.Lock()
	if s.Wallets != nil {
		ev.BinanceUSD = s.Wallets.Get("Binance", quoteOf("Binance"))
		ev.BinanceBTC = s.Wallets.Get("Binance", baseOf("Binance"))
		ev.BitsoUSD = s.Wallets.Get("Bitso", quoteOf("Bitso"))
		ev.BitsoBTC = s.Wallets.Get("Bitso", baseOf("Bitso"))
		ev.TotalWealth = s.TotalWealth
		ev.TotalNetProfit = s.TotalNetProfit
		ev.InitialWealth = s.InitialWealth
		ev.InitialUSD = s.InitialUSD
	}
	s.Mu.Unlock()

	payload, err := json.Marshal(ev)
	if err != nil {
		return
	}

	s.WriteMessage(websocket.TextMessage, payload)
}

// sendParamsUpdate notifica a la sesión sus parámetros VIGENTES (post-clamps).
// La UI pinta siempre lo aplicado, nunca lo solicitado.
func sendParamsUpdate(s *ClientSession, eventType string, msg string) {
	p := s.Params()
	sendEvent(s, ServerEvent{
		Type:      eventType,
		SessionID: s.ID,
		Message:   msg,
		Params:    &p,
	})
}

func wsHandler(hub *Hub, engine *HFTEngine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}

		// La sesión nace con los parámetros por defecto ya publicados (snapshot
		// atómico): cualquier lector del hot path ve parámetros válidos desde el
		// primer instante, y set_params puede reemplazarlos en vivo sin locks.
		session := newClientSession(generateUUID(), conn)
		hub.Add(session)
		defer func() {
			hub.Remove(session)
			conn.Close()
			// Última fotografía + last_seen al desconectar: el usuario puede
			// cerrar el navegador y volver mañana.
			persistSessionAsync(session)
			if sessionStore != nil {
				if err := sessionStore.TouchSession(session.ID, time.Now()); err != nil {
					log.Printf("⚠️ [STORE] touch al desconectar: %v", err)
				}
			}
		}()

		for {
			var msg ClientMessage
			if err := conn.ReadJSON(&msg); err != nil {
				break
			}

			switch msg.Action {
			case "init_session":
				// Validación de backend: rechazamos capital inválido en vez de inicializar
				// una sesión con estado corrupto. El frontend legítimo siempre envía
				// valores válidos, así que su flujo feliz no se ve afectado.
				if !validInitFunds(msg.InitialUSD, msg.InitialBTC) {
					log.Printf("⚠️ [VALIDACIÓN] init_session rechazado: usd=%v btc=%v", msg.InitialUSD, msg.InitialBTC)
					sendEvent(session, ServerEvent{Type: "INIT_REJECTED", SessionID: session.ID, Message: "Capital inicial inválido: usa montos positivos y razonables."})
					continue
				}
				// Distribución por venue (modo experto): opcional; si viene, se valida
				// estricta. BTC sin distribución propia sigue a la de USD.
				usdAlloc, btcAlloc := msg.UsdAllocation, msg.BtcAllocation
				if usdAlloc != nil || btcAlloc != nil {
					if usdAlloc == nil {
						usdAlloc = btcAlloc
					}
					if btcAlloc == nil {
						btcAlloc = usdAlloc
					}
					if !validAllocation(usdAlloc) || !validAllocation(btcAlloc) {
						log.Printf("⚠️ [VALIDACIÓN] init_session rechazado: distribución inválida usd=%v btc=%v", msg.UsdAllocation, msg.BtcAllocation)
						sendEvent(session, ServerEvent{Type: "INIT_REJECTED", SessionID: session.ID, Message: "Distribución inválida: usa casas de cambio registradas y porcentajes que sumen 100."})
						continue
					}
				}
				initSession(session, msg.InitialUSD, msg.InitialBTC, usdAlloc, btcAlloc)
				persistSessionAsync(session)
				p := session.Params()
				sendEvent(session, ServerEvent{Type: "state_update", SessionID: session.ID, Message: "Sesión inicializada", Params: &p})

			case "resume_session":
				// Continuidad (Sprint A): el navegador presenta su token (UUID de la
				// sesión persistida) y recupera saldos, parámetros y base del PnL.
				// El historial del ledger vuelve a ser SUYO porque el ID se conserva.
				id := msg.SessionID
				if sessionStore == nil || id == "" || len(id) > 64 {
					sendEvent(session, ServerEvent{Type: "RESUME_FAILED", SessionID: session.ID, Message: "Sesión no recuperable."})
					continue
				}
				rec, found, err := sessionStore.LoadSession(id)
				if err != nil || !found || !rec.Initialized {
					if err != nil {
						log.Printf("⚠️ [STORE] resume_session %s: %v", id, err)
					}
					sendEvent(session, ServerEvent{Type: "RESUME_FAILED", SessionID: session.ID, Message: "Sesión no encontrada — configura tu capital para empezar de nuevo."})
					continue
				}
				// Takeover: si otra pestaña vive con este token, se desconecta.
				// Hub.Remove es identity-aware: su defer no expulsará a esta sesión.
				if old := hub.Get(id); old != nil && old != session {
					log.Printf("🔁 [RESUME] Takeover de sesión %s (conexión previa desconectada)", id)
					old.CloseConn()
				}
				hub.Remove(session) // suelta el ID efímero de esta conexión
				applySessionRecord(session, rec)
				session.ID = id
				hub.Add(session)
				if err := sessionStore.TouchSession(id, time.Now()); err != nil {
					log.Printf("⚠️ [STORE] touch al reanudar: %v", err)
				}
				log.Printf("💾 [RESUME] Sesión %s recuperada (patrimonio $%.2f, PnL $%.4f)", id, rec.TotalWealth, rec.TotalNetProfit)
				p := session.Params()
				sendEvent(session, ServerEvent{Type: "state_update", SessionID: session.ID, Message: "Sesión recuperada — tus fondos y tu estrategia siguen aquí.", Params: &p, Resumed: true})
				sendLog(session, "💾 [SESIÓN] Bienvenido de vuelta: saldos, estrategia e historial recuperados de la base de datos.")

			case "reset_session":
				// El reset conserva la DISTRIBUCIÓN elegida en el onboarding: el
				// 40/40/20 de un experto no se degrada al 50/50 clásico.
				initSession(session, session.InitialUSD, session.InitialBTC, session.UsdAlloc, session.BtcAlloc)
				persistSessionAsync(session)
				p := session.Params()
				sendEvent(session, ServerEvent{Type: "state_update", SessionID: session.ID, Message: "Sesión reseteada", Params: &p})

			case "set_params":
				// Personalización de estrategia EN VIVO: el usuario define su apetito
				// de riesgo (margen mínimo, tamaño de orden, slippage estimado, fees,
				// multiplicador de crédito). El backend clampea todo a rangos sanos.
				if msg.Params == nil {
					sendLog(session, "🛑 [VALIDACIÓN] set_params sin parámetros — ignorado.")
					continue
				}
				applied := sanitizeTradingParams(*msg.Params)
				session.SetParams(applied)
				persistSessionAsync(session) // la estrategia del usuario sobrevive reinicios
				log.Printf("🎛️ [PARAMS] Sesión %s: minNet=$%.2f maxOrden=%.4f BTC slip=%.1f bps riesgo=%.1fx",
					session.ID, applied.MinNetProfitUSD, applied.MaxOrderSizeBTC, applied.SlippageRate*10000, applied.RiskMultiplier)
				sendLog(session, fmt.Sprintf("🎛️ [ESTRATEGIA] Parámetros actualizados: margen mín. $%.2f | orden máx. %.4f BTC | slippage %.1f bps | riesgo crédito %.1fx",
					applied.MinNetProfitUSD, applied.MaxOrderSizeBTC, applied.SlippageRate*10000, applied.RiskMultiplier))
				sendParamsUpdate(session, "PARAMS_UPDATED", "Parámetros de estrategia aplicados")

			case "demo_inject":
				// Validación + clamp de backend: exchange conocido, sin NaN/Inf, liquidez > 0
				// y acotada (evita un bucle de chunks gigante que cuelgue el motor).
				ex, spread, liq, ok := sanitizeDemoInject(msg.Exchange, msg.Spread, msg.Liquidity)
				if !ok {
					sendLog(session, "🛑 [VALIDACIÓN] Parámetros de simulación inválidos — inyección ignorada.")
					continue
				}
				go engine.runDemoInjection(session, ex, spread, liq)

			case "adjust_funds":
				go engine.adjustFunds(session, msg.Exchange, msg.Currency, msg.Amount)

			case "wait_rebalance":
				go engine.startReplenishing(session, "Esperando traslado de capital entre exchanges (1 min en demo; ~30+ min en producción).")

			case "request_credit":
				go func() {
					sendEvent(session, ServerEvent{Type: "CREDIT_PROCESSING", Message: "Procesando solicitud de préstamo..."})
					time.Sleep(2 * time.Second)
					engine.activateCreditSession(session)
				}()

			case "toggle_auto_credit":
				session.Mu.Lock()
				session.Credit.AutoMode = !session.Credit.AutoMode
				autoMode := session.Credit.AutoMode
				session.Mu.Unlock()
				log.Printf("🔁 [TOGGLE] Préstamo automático: %v", autoMode)
				persistSessionAsync(session)
				// Evento dedicado: NO usar "state_update" para no reiniciar el feed/logs del dashboard.
				sendEvent(session, ServerEvent{Type: "AUTO_CREDIT_TOGGLED", SessionID: session.ID, AutoMode: autoMode, Message: fmt.Sprintf("Préstamo automático: %v", autoMode)})

			case "shutdown_engine":
				log.Printf("🛑 [APAGADO] Usuario apagó el motor")
				sendEvent(session, ServerEvent{Type: "ENGINE_SHUTDOWN"})
			}
		}
	}
}

// ledgerHandler expone los últimos 100 trades de UNA sesión persistidos en SQLite como
// JSON. Demuestra que los datos sobreviven al reinicio del motor (persistencia real).
//
// Seguridad (Hallazgo #1): el ledger es por-sesión. Antes /api/ledger devolvía los trades
// de TODAS las sesiones (fuga de privacidad cross-sesión). Ahora exige ?session_id=<id> y
// filtra por él. El session_id es un UUID v4 no enumerable, así que aun con CORS abierto
// (necesario para Vercel↔Fly) un tercero no puede listar las operaciones de otra sesión.
func ledgerHandler(w http.ResponseWriter, r *http.Request) {
	// CORS: el frontend Next.js corre en otro origen (Vercel en prod, :3000 en dev).
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	records := []TradeRecord{}
	// Sin session_id no devolvemos nada (cierra la fuga). Respondemos [] —y NO 400—
	// como respuesta neutra para clientes viejos; el panel de auditoría actual ya
	// envía siempre su session_id.
	if sessionID := r.URL.Query().Get("session_id"); sessionID != "" {
		var err error
		records, err = getTradesForSession(sessionID, 100)
		if err != nil {
			http.Error(w, `{"error":"no se pudo leer el ledger"}`, http.StatusInternalServerError)
			log.Printf("⚠️ [LEDGER] Error al leer registros: %v", err)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(records); err != nil {
		log.Printf("⚠️ [LEDGER] Error al serializar registros: %v", err)
	}
}

// generateUUID devuelve un identificador de sesión único. Antes usaba
// time.Now().UnixNano(), que colisiona si dos clientes conectan en el mismo
// nanosegundo (una sesión pisaría a la otra en el Hub). uuid.NewString() (UUID v4,
// aleatorio) elimina ese riesgo de colisión bajo conexiones simultáneas.
func generateUUID() string {
	return uuid.NewString()
}
