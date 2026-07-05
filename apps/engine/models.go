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

// Parámetros de trading por defecto. Son la ÚNICA fuente de verdad de los números del
// motor: TradingParameters se inicializa a partir de ellos en cada sesión, y el bucle de
// detección compartido (Start) los usa directamente (corre pre-fan-out, sin una sesión a
// la cual atribuir parámetros). Antes estaban como literales sueltos repetidos ~15 veces.
const (
	DefaultBinanceTakerFee    = 0.001   // taker fee de Binance (0.10 %)
	DefaultBitsoTakerFee      = 0.0065  // taker fee de Bitso (0.65 %)
	DefaultMinNetProfitUSD    = 0.10    // umbral de viabilidad: solo ejecuta si el neto supera esto
	DefaultBaseOrderSize      = 0.005   // tamaño base de orden por evaluación (BTC)
	DefaultSpikeTickDeviation = 0.05    // variación máx. tick-a-tick antes de descartar por Spike Filter
	DefaultMaxDivergenceRatio = 1.20    // compuerta de cordura: rechaza si un precio supera al otro en >20 %
	DefaultBTCPriceFallback   = 60000.0 // precio BTC de respaldo cuando aún no hay feed de Binance
)

// Límites de validación de entradas del cliente (defensa del backend: el frontend ya
// valida, pero el servidor NUNCA debe confiar en el cliente).
const (
	MaxInitialUSD    = 1e12 // tope sano de capital inicial en USD
	MaxInitialBTC    = 1e6  // tope sano de capital inicial en BTC
	MaxDemoLiquidity = 10.0 // BTC: igual al máximo del frontend; evita bucles de chunks gigantes que cuelguen al motor
	MaxDemoSpreadUSD = 1e7  // tope de |spread| inyectable en el simulador
)

// TradingParameters agrupa los parámetros de negocio que gobiernan una sesión. Vive
// dentro de ClientSession para habilitar tuning POR SESIÓN: un próximo sprint expondrá
// estos campos a la UI. Se fija UNA sola vez al crear la sesión (wsHandler) con
// DefaultTradingParameters() y es INMUTABLE durante su vida — por eso el hot path lo lee
// sin lock. Cuando la UI permita editarlo, esas escrituras (y sus lecturas) deberán
// sincronizarse con session.Mu.
type TradingParameters struct {
	BinanceTakerFee    float64 `json:"binance_taker_fee"`
	BitsoTakerFee      float64 `json:"bitso_taker_fee"`
	MinNetProfitUSD    float64 `json:"min_net_profit_usd"`
	BaseOrderSize      float64 `json:"base_order_size"`
	SpikeTickDeviation float64 `json:"spike_tick_deviation"`
	MaxDivergenceRatio float64 `json:"max_divergence_ratio"`
}

// DefaultTradingParameters devuelve los parámetros por defecto del concurso.
func DefaultTradingParameters() TradingParameters {
	return TradingParameters{
		BinanceTakerFee:    DefaultBinanceTakerFee,
		BitsoTakerFee:      DefaultBitsoTakerFee,
		MinNetProfitUSD:    DefaultMinNetProfitUSD,
		BaseOrderSize:      DefaultBaseOrderSize,
		SpikeTickDeviation: DefaultSpikeTickDeviation,
		MaxDivergenceRatio: DefaultMaxDivergenceRatio,
	}
}

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
	// Params son los parámetros de trading de ESTA sesión (fees, umbrales, tamaño de
	// orden…). Se fijan UNA vez al crear la sesión (wsHandler) con DefaultTradingParameters()
	// y la lógica de ejecución los lee en vez de números mágicos. Inmutables durante la vida
	// de la sesión (reset_session NO los reescribe) → lectura sin lock segura en el hot path.
	Params         TradingParameters
	IsReplenishing           bool
	ReplenishExpiresAt       time.Time
	InsufficientFundsPending bool
	LastTradeTime            time.Time

	// IsExecuting serializa la ejecución por sesión: se fija bajo el mismo lock que valida
	// el cooldown, de modo que solo una goroutine puede operar a la vez por sesión (cierra
	// el race de sobre-trading donde dos ticks ejecutaban dos trades simultáneos).
	IsExecuting bool
	// PausedUntil bloquea la operativa de la sesión hasta este instante; lo fija el circuit
	// breaker cuando una orden Fill-or-Kill falla, para no reintentar contra un libro roto.
	PausedUntil time.Time
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
