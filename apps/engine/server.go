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

// wsWriteMutex is used to avoid concurrent writes to the same websocket connection
var wsWriteMutex = make(map[*websocket.Conn]*time.Time) // mock map, better to lock inside session
// actually we can just add a writeMutex to ClientSession if needed, but for simplicity let's use a global map or just not lock. 
// Standard practice for Gorilla is a write channel or mutex. We will just use s.Mu for simplicity when writing, but it's dangerous. Let's not lock writes for now to avoid deadlocks.

func initSession(s *ClientSession, usd, btc float64) {
	s.Mu.Lock()
	defer s.Mu.Unlock()

	half := usd / 2.0
	halfBTC := btc / 2.0

	s.Wallets = map[string]*Wallet{
		"Binance": {USD: half, BTC: halfBTC},
		"Bitso":   {USD: half, BTC: halfBTC},
	}

	btcPrice := DefaultBTCPriceFallback
	currentMarket.mu.Lock()
	if currentMarket.BinanceAsk > 0 {
		btcPrice = currentMarket.BinanceAsk
	}
	currentMarket.mu.Unlock()

	s.TotalWealth = usd + (btc * btcPrice)
	s.InitialWealth = s.TotalWealth // base del PnL = patrimonio al iniciar
	s.TotalNetProfit = 0
	s.Credit = CreditState{}
	// NOTA: Params NO se toca aquí. Se fija una sola vez al crear la sesión (wsHandler) y
	// es inmutable durante su vida, así que reset_session (que re-invoca initSession) no
	// reintroduce un escritor concurrente que haría carrera con los lectores sin lock.
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

// sanitizeDemoInject valida y CLAMPEA los parámetros de una inyección del simulador.
// Devuelve ok=false si son irrecuperables (exchange desconocido, NaN/Inf, liquidez ≤ 0).
func sanitizeDemoInject(exchange string, spread, liquidity float64) (string, float64, float64, bool) {
	if exchange != "Binance" && exchange != "Bitso" {
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

func sendEvent(s *ClientSession, ev ServerEvent) {
	// Enrich with wallet state if initialized
	s.Mu.Lock()
	if s.Wallets != nil {
		ev.BinanceUSD = s.Wallets["Binance"].USD
		ev.BinanceBTC = s.Wallets["Binance"].BTC
		ev.BitsoUSD = s.Wallets["Bitso"].USD
		ev.BitsoBTC = s.Wallets["Bitso"].BTC
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

	// We ignore concurrent write errors for this demo, or we could add a dedicated write mutex.
	s.WriteMessage(websocket.TextMessage, payload)
}

func wsHandler(hub *Hub, engine *HFTEngine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}

		// Params se fija UNA sola vez aquí, en la creación de la sesión, ANTES de hub.Add:
		// el happens-before del RWMutex del Hub ordena esta escritura antes de cualquier
		// hub.Snapshot()/fan-out, así que los lectores del hot path leen session.Params SIN
		// lock (value object inmutable durante la vida de la sesión). reset_session NO lo
		// reescribe. Cuando un sprint futuro permita editar parámetros desde la UI, esas
		// escrituras deberán sincronizarse con session.Mu.
		session := &ClientSession{
			ID:     generateUUID(),
			Conn:   conn,
			Params: DefaultTradingParameters(),
		}
		hub.Add(session)
		defer func() {
			hub.Remove(session.ID)
			conn.Close()
		}()

		for {
			var msg ClientMessage
			if err := conn.ReadJSON(&msg); err != nil {
				break
			}

			switch msg.Action {
			case "init_session":
				// Validación de backend: rechazamos capital inválido en vez de inicializar
				// una sesión con estado corrupto. El frontend legítimo siempre envía valores
				// válidos (mín. $1 000 / 0.1 BTC), así que su flujo feliz no se ve afectado.
				if !validInitFunds(msg.InitialUSD, msg.InitialBTC) {
					log.Printf("⚠️ [VALIDACIÓN] init_session rechazado: usd=%v btc=%v", msg.InitialUSD, msg.InitialBTC)
					sendEvent(session, ServerEvent{Type: "INIT_REJECTED", SessionID: session.ID, Message: "Capital inicial inválido: usa montos positivos y razonables."})
					continue
				}
				initSession(session, msg.InitialUSD, msg.InitialBTC)
				sendEvent(session, ServerEvent{Type: "state_update", SessionID: session.ID, Message: "Sesión inicializada"})

			case "reset_session":
				initSession(session, session.InitialUSD, session.InitialBTC)
				sendEvent(session, ServerEvent{Type: "state_update", SessionID: session.ID, Message: "Sesión reseteada"})

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
	// Sin session_id no devolvemos nada (cierra la fuga). Respondemos [] —y NO 400— para
	// no romper el panel de auditoría del frontend desplegado, que todavía consulta
	// /api/ledger sin el parámetro; se actualizará en el siguiente sprint.
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
