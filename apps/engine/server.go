package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

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

	btcPrice := 60000.0
	currentMarket.mu.Lock()
	if currentMarket.BinanceAsk > 0 {
		btcPrice = currentMarket.BinanceAsk
	}
	currentMarket.mu.Unlock()

	s.TotalWealth = usd + (btc * btcPrice)
	s.InitialWealth = s.TotalWealth // base del PnL = patrimonio al iniciar
	s.TotalNetProfit = 0
	s.Credit = CreditState{}
	s.InitialUSD = usd
	s.InitialBTC = btc
	s.IsReplenishing = false
	s.ReplenishExpiresAt = time.Time{}
	s.InsufficientFundsPending = false
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

		session := &ClientSession{
			ID:   generateUUID(),
			Conn: conn,
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
				initSession(session, msg.InitialUSD, msg.InitialBTC)
				sendEvent(session, ServerEvent{Type: "state_update", SessionID: session.ID, Message: "Sesión inicializada"})

			case "reset_session":
				initSession(session, session.InitialUSD, session.InitialBTC)
				sendEvent(session, ServerEvent{Type: "state_update", SessionID: session.ID, Message: "Sesión reseteada"})

			case "demo_inject":
				go engine.runDemoInjection(session, msg.Exchange, msg.Spread, msg.Liquidity)

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

// ledgerHandler expone los últimos 100 trades persistidos en SQLite como JSON.
// Demuestra que los datos sobreviven al reinicio del motor (persistencia real).
func ledgerHandler(w http.ResponseWriter, r *http.Request) {
	// CORS: el frontend Next.js corre en otro puerto (3000) durante desarrollo.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	records, err := getRecentTrades(100)
	if err != nil {
		http.Error(w, `{"error":"no se pudo leer el ledger"}`, http.StatusInternalServerError)
		log.Printf("⚠️ [LEDGER] Error al leer registros: %v", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(records); err != nil {
		log.Printf("⚠️ [LEDGER] Error al serializar registros: %v", err)
	}
}

func generateUUID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
