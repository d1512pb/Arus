package main

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Wallet representa los fondos en un exchange específico
type Wallet struct {
	USD float64 `json:"usd"`
	BTC float64 `json:"btc"`
}

// DemoInjection define una simulación de liquidez para el order book
type DemoInjection struct {
	Exchange           string  `json:"exchange"`
	TargetSpread       float64 `json:"target_spread"`
	AvailableLiquidity float64 `json:"available_liquidity"`
}

const (
	DemoChunkSize     = 0.05
	DemoOrderLatency  = 50 * time.Millisecond

	CreditLineUSD            = 50000.0
	CreditLineBTC            = 1.0
	CreditOriginationFee     = 25.0
	CreditAPR                = 0.10
	CreditDurationMinutes    = 1.0
	RebalanceDurationMinutes = 1.0 // demo: en producción el traslado entre exchanges tarda ~30+ min
)

type CreditState struct {
	Active          bool
	AutoMode        bool
	ActivatedAt     time.Time
	ExpiresAt       time.Time
	TotalCostPaid   float64
	ActivationCount int
	BorrowedUSD           map[string]float64 // por exchange: liquidez prestada temporal
	BorrowedBTC           map[string]float64
	DepletedPending       bool
	NetProfitAtActivation float64
	LastCost              float64
}

type ClientSession struct {
	ID             string
	Conn           *websocket.Conn
	Mu             sync.Mutex
	ConnMu         sync.Mutex
	Wallets        map[string]*Wallet
	TotalWealth    float64
	TotalNetProfit float64
	Credit         CreditState
	InitialUSD     float64
	InitialBTC     float64
	InitialWealth  float64 // base en USD para el PnL; escalar estable (no se revalúa con el precio)
	IsReplenishing           bool
	ReplenishExpiresAt       time.Time
	InsufficientFundsPending bool
	LastTradeTime            time.Time
}

func (s *ClientSession) IsInitialized() bool {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	return s.Wallets != nil
}

func (s *ClientSession) WriteJSON(v interface{}) error {
	s.ConnMu.Lock()
	defer s.ConnMu.Unlock()
	if s.Conn != nil {
		return s.Conn.WriteJSON(v)
	}
	return nil
}

func (s *ClientSession) WriteMessage(messageType int, data []byte) error {
	s.ConnMu.Lock()
	defer s.ConnMu.Unlock()
	if s.Conn != nil {
		return s.Conn.WriteMessage(messageType, data)
	}
	return nil
}

type Hub struct {
	mu       sync.RWMutex
	sessions map[string]*ClientSession
}

func NewHub() *Hub {
	return &Hub{
		sessions: make(map[string]*ClientSession),
	}
}

func (h *Hub) Add(s *ClientSession) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sessions[s.ID] = s
}

func (h *Hub) Remove(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.sessions, id)
}

func (h *Hub) Snapshot() []*ClientSession {
	h.mu.RLock()
	defer h.mu.RUnlock()
	list := make([]*ClientSession, 0, len(h.sessions))
	for _, s := range h.sessions {
		list = append(list, s)
	}
	return list
}

type LogEvent struct {
	Type      string  `json:"type"`       // siempre "log"
	Level     string  `json:"level"`      // "waiting" | "opportunity" | "spike_warn" | "spike_block" | "arb"
	Timestamp string  `json:"timestamp"`
	Message   string  `json:"message"`
	Spread    float64 `json:"spread,omitempty"`
	NetProfit float64 `json:"net_profit,omitempty"`
}

func (h *Hub) BroadcastLog(event LogEvent) {
	for _, s := range h.Snapshot() {
		if s.IsInitialized() {
			s.WriteJSON(event)
		}
	}
}

type ClientMessage struct {
	Action     string  `json:"action"`
	InitialUSD float64 `json:"initial_usd,omitempty"`
	InitialBTC float64 `json:"initial_btc,omitempty"`
	Exchange   string  `json:"exchange,omitempty"`
	Spread     float64 `json:"spread,omitempty"`
	Liquidity  float64 `json:"liquidity,omitempty"`
	UseCredit  bool    `json:"use_credit,omitempty"`
	Currency   string  `json:"currency,omitempty"` // "USD" | "BTC" para depósito/retiro
	Amount     float64 `json:"amount,omitempty"`   // >0 deposita, <0 retira
}

type ServerEvent struct {
	Type               string  `json:"type"` // "state_update", "arb_executed", "spike_blocked", "waiting", "demo_log", "INSUFFICIENT_FUNDS", "CREDIT_PROCESSING", "ENGINE_SHUTDOWN", etc.
	SessionID          string  `json:"session_id"`
	BinanceUSD         float64 `json:"binance_usd"`
	BinanceBTC         float64 `json:"binance_btc"`
	BitsoUSD           float64 `json:"bitso_usd"`
	BitsoBTC           float64 `json:"bitso_btc"`
	BinancePrice       float64 `json:"binance_price,omitempty"`
	BitsoPrice         float64 `json:"bitso_price,omitempty"`
	TotalWealth        float64 `json:"total_wealth"`
	InitialWealth      float64 `json:"initial_wealth"`
	InitialUSD         float64 `json:"initial_usd"`
	TotalNetProfit     float64 `json:"total_net_profit"`
	NetProfit          float64 `json:"net_profit,omitempty"`
	Spread             float64 `json:"spread,omitempty"`
	SpikeFactor        float64 `json:"spike_factor,omitempty"`
	Message            string  `json:"message,omitempty"`
	RequiresManualAction bool  `json:"requiresManualAction,omitempty"`
	ProfitPotential    float64 `json:"profit_potential,omitempty"`
	CreditCost         float64 `json:"credit_cost,omitempty"`
	ExpiresAt            string  `json:"expires_at,omitempty"`
	ReplenishExpiresAt   string  `json:"replenish_expires_at,omitempty"`
	AutoMode             bool    `json:"auto_mode,omitempty"`
	IsReplenishing       bool    `json:"is_replenishing,omitempty"`
	CreditActive         bool    `json:"credit_active,omitempty"`
	BorrowedBinanceUSD   float64 `json:"borrowed_binance_usd,omitempty"`
	BorrowedBitsoUSD     float64 `json:"borrowed_bitso_usd,omitempty"`
	BorrowedBinanceBTC   float64 `json:"borrowed_binance_btc,omitempty"`
	BorrowedBitsoBTC     float64 `json:"borrowed_bitso_btc,omitempty"`
	LoanEarnings         float64 `json:"loan_earnings,omitempty"`
	LoanCost             float64 `json:"loan_cost,omitempty"`
}

// ArbitrageEvent es la estructura JSON que enviaremos al frontend
type ArbitrageEvent struct {
	Event          string  `json:"event"`
	BuyExchange    string  `json:"exchange_buy"`
	SellExchange   string  `json:"exchange_sell"`
	
	// Campos para compatibilidad con la UI existente en Next.js
	BuyExchangeUI  string  `json:"buy_exchange"`
	SellExchangeUI string  `json:"sell_exchange"`
	Volume         float64 `json:"volume"`
	NetProfitUSD   float64 `json:"net_profit_usd"`
	NetProfitUI    float64 `json:"net_profit"`
	
	NewTotalUSD    float64 `json:"new_total_usd"`
	Timestamp      string  `json:"timestamp"`
	
	// Fase 2: Línea de Crédito
	CreditActive   bool    `json:"credit_active"`
	InterestPaid   float64 `json:"interest_paid"`
}

// MarketUpdateEvent se usa para emitir el precio en vivo al frontend
type MarketUpdateEvent struct {
	Event        string  `json:"event"`
	BinancePrice float64 `json:"binance_price"`
	BitsoPrice   float64 `json:"bitso_price"`
	Spread       float64 `json:"spread"`
}

// PriceTick representa un evento de mercado simulado o real
type PriceTick struct {
	Exchange string
	Ask      float64 // Precio al que compramos (top of book)
	Bid      float64 // Precio al que vendemos (top of book)
}
