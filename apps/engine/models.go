package main

import (
	"sync"
	"sync/atomic"
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
	DemoChunkSize    = 0.05
	DemoOrderLatency = 50 * time.Millisecond

	CreditLineUSD            = 50000.0
	CreditLineBTC            = 1.0
	CreditOriginationFee     = 25.0
	CreditAPR                = 0.10
	CreditDurationMinutes    = 1.0
	RebalanceDurationMinutes = 1.0 // demo: en producción el traslado entre exchanges tarda ~30+ min
)

// Parámetros de trading por defecto. Son la fuente de los VALORES INICIALES de cada
// sesión: TradingParameters se construye a partir de ellos (y del registro de venues)
// al crear la sesión, y desde la Fase 0 el usuario puede modificarlos EN VIVO desde
// el panel de estrategia (acción set_params). El bucle de detección compartido (Start)
// sigue usando los defaults directamente: corre pre-fan-out, sin una sesión concreta a
// la cual atribuir parámetros — es la "vista de mercado de referencia".
const (
	DefaultMinNetProfitUSD    = 0.10    // umbral de viabilidad: solo ejecuta si el neto supera esto
	DefaultMaxOrderSizeBTC    = 0.005   // TOPE de orden por evaluación (BTC); el volumen real = min(tope, liquidez top-of-book)
	DefaultSlippageRate       = 0.0005  // slippage ESTIMADO por pierna (fracción; 0.0005 = 5 bps sobre el notional)
	DefaultSpikeTickDeviation = 0.05    // variación máx. tick-a-tick antes de descartar por Spike Filter
	DefaultMaxDivergenceRatio = 1.20    // compuerta de cordura: rechaza si un precio supera al otro en >20 %
	DefaultRiskMultiplier     = 1.0     // crédito: exigir ganancia > costo × multiplicador (1 = punto de equilibrio)
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

// Rangos permitidos para los parámetros de estrategia editables (set_params).
// Todo valor fuera de rango (incl. 0 por campo ausente, NaN o Inf) se CLAMPEA:
// un mensaje malicioso o corrupto nunca puede dejar la sesión con parámetros
// absurdos (fee negativo, orden gigante, multiplicador que endeuda a pérdida…).
const (
	MinTakerFee             = 0.0  // un venue puede ser fee-free
	MaxTakerFee             = 0.05 // 5 %: por encima de esto es un error, no una comisión
	MinNetProfitFloor       = 0.0
	MaxNetProfitCeil        = 1e6
	MinOrderSizeBTC         = 0.0005 // = MinExecutableVolumeBTC: un tope menor jamás ejecutaría
	MaxOrderSizeCapBTC      = 10.0   // mismo tope que el simulador
	MinSlippageRate         = 0.0
	MaxSlippageRate         = 0.01 // 100 bps por pierna: más que eso no es slippage, es un mercado roto
	MinSpikeDeviation       = 0.005
	MaxSpikeDeviation       = 0.50
	MinDivergenceRatioLimit = 1.01
	MaxDivergenceRatioLimit = 2.00
	MinRiskMultiplier       = 1.0 // <1 significaría endeudarse aceptando pérdida esperada: prohibido
	MaxRiskMultiplier       = 100.0
)

// MinExecutableVolumeBTC: si la liquidez disponible deja el volumen por debajo de
// esto, la operación se omite (el dust no paga ni sus propios fees).
const MinExecutableVolumeBTC = 0.0005

// MaxBookStaleness es la antigüedad máxima que puede tener el top-of-book de un
// venue para participar en una evaluación. Si el feed de un exchange se congela,
// comparar su último precio (viejo) contra el precio fresco del otro produciría
// spreads fantasma — el motor prefiere no operar a operar contra datos muertos.
const MaxBookStaleness = 10 * time.Second

// TradingParameters agrupa los parámetros de negocio que gobiernan una sesión.
// Desde la Fase 0 son EDITABLES EN VIVO vía la acción set_params (panel de
// estrategia de la UI): cada usuario define su propio apetito de riesgo, y dos
// sesiones con el mismo mercado pueden tomar decisiones distintas.
//
// Concurrencia: la sesión guarda los parámetros en un atomic.Pointer. El escritor
// (set_params) publica un struct NUEVO completo con SetParams; los lectores toman
// un snapshot inmutable con Params() y trabajan sobre esa vista consistente. El
// mapa TakerFees se construye fresco en cada publicación y NUNCA se muta después:
// compartirlo entre lectores es seguro por convención de solo-lectura.
type TradingParameters struct {
	// TakerFees es la comisión taker por venue (clave = Venue.Name). Extensible a
	// N exchanges sin tocar el struct: agregar un venue solo agrega una clave.
	TakerFees map[string]float64 `json:"taker_fees"`

	// MinNetProfitUSD: margen mínimo de ganancia neta para ejecutar. El usuario
	// conservador exige $10; el agresivo captura oportunidades desde $0.10.
	MinNetProfitUSD float64 `json:"min_net_profit_usd"`

	// MaxOrderSizeBTC es el TOPE de volumen por operación. El volumen ejecutado es
	// min(tope, liquidez disponible en el top-of-book de ambas piernas): quien opera
	// bloques chicos entra en ineficiencias que el que exige bloques grandes ignora.
	MaxOrderSizeBTC float64 `json:"max_order_size_btc"`

	// SlippageRate es el deslizamiento ESTIMADO por pierna (fracción del notional)
	// que el modelo de costos descuenta antes de decidir. Cuando la Fase 2 calcule
	// el slippage real por profundidad de libro, este campo pasará a ser un TOPE
	// de tolerancia (rechazar si slippage calculado > tolerancia del usuario).
	SlippageRate float64 `json:"slippage_rate"`

	// SpikeTickDeviation: variación máx. tick-a-tick tolerada por el Spike Filter.
	SpikeTickDeviation float64 `json:"spike_tick_deviation"`

	// MaxDivergenceRatio: compuerta de cordura entre precios de ambos venues.
	MaxDivergenceRatio float64 `json:"max_divergence_ratio"`

	// RiskMultiplier gobierna la inecuación de dominancia del crédito:
	//   pedir préstamo ⇔ ganancia proyectada > costo del crédito × RiskMultiplier
	// Conservador = alto (solo endeudarse si la ganancia cubre 5× el costo);
	// agresivo = 1.0 (al límite del punto de equilibrio). Nunca < 1.
	RiskMultiplier float64 `json:"risk_multiplier"`
}

// DefaultTradingParameters devuelve los parámetros iniciales de una sesión,
// con los fees tomados del registro de venues.
func DefaultTradingParameters() TradingParameters {
	return TradingParameters{
		TakerFees:          defaultTakerFees(),
		MinNetProfitUSD:    DefaultMinNetProfitUSD,
		MaxOrderSizeBTC:    DefaultMaxOrderSizeBTC,
		SlippageRate:       DefaultSlippageRate,
		SpikeTickDeviation: DefaultSpikeTickDeviation,
		MaxDivergenceRatio: DefaultMaxDivergenceRatio,
		RiskMultiplier:     DefaultRiskMultiplier,
	}
}

// takerFee devuelve la comisión taker del venue según ESTE snapshot de parámetros,
// con fallback al default del registro si el mapa no la trae (sesión antigua o
// venue recién agregado).
func (p TradingParameters) takerFee(venue string) float64 {
	if p.TakerFees != nil {
		if fee, ok := p.TakerFees[venue]; ok {
			return fee
		}
	}
	if v, ok := venueByName(venue); ok {
		return v.DefaultTakerFee
	}
	return 0
}

type CreditState struct {
	Active                bool
	AutoMode              bool
	ActivatedAt           time.Time
	ExpiresAt             time.Time
	TotalCostPaid         float64
	ActivationCount       int
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

	// params son los parámetros de trading de ESTA sesión (fees, umbrales, tamaño
	// de orden, multiplicador de riesgo…). Editables EN VIVO vía set_params: el
	// escritor publica un snapshot nuevo con SetParams y los lectores del hot path
	// leen la vista consistente con Params() — sin locks y sin estados a medias.
	params atomic.Pointer[TradingParameters]

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

// newClientSession construye una sesión con los parámetros por defecto ya
// publicados (Params() es válido desde el primer instante, antes de hub.Add).
func newClientSession(id string, conn *websocket.Conn) *ClientSession {
	s := &ClientSession{ID: id, Conn: conn}
	s.SetParams(DefaultTradingParameters())
	return s
}

// Params devuelve el snapshot vigente de parámetros de la sesión. La copia por
// valor da una vista consistente para toda la operación en curso: si el usuario
// cambia los parámetros a mitad de un trade, el trade termina con los que empezó.
func (s *ClientSession) Params() TradingParameters {
	return *s.params.Load()
}

// SetParams publica un snapshot nuevo de parámetros (visible atómicamente para
// todos los lectores). El struct que se pasa no debe mutarse después de llamar.
func (s *ClientSession) SetParams(p TradingParameters) {
	s.params.Store(&p)
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

// CloseConn cierra el socket de la sesión (usado en el takeover de reanudación:
// la pestaña vieja se desconecta limpiamente cuando otra reclama el mismo token).
func (s *ClientSession) CloseConn() {
	s.ConnMu.Lock()
	defer s.ConnMu.Unlock()
	if s.Conn != nil {
		s.Conn.Close()
	}
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

// Remove elimina la sesión SOLO si sigue siendo la registrada bajo su ID
// (identity-aware): tras un takeover por reanudación, el defer de la conexión
// vieja no debe expulsar del Hub a la conexión nueva que heredó el mismo ID.
func (h *Hub) Remove(s *ClientSession) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if cur, ok := h.sessions[s.ID]; ok && cur == s {
		delete(h.sessions, s.ID)
	}
}

// Get devuelve la sesión viva registrada bajo un ID (nil si no hay).
func (h *Hub) Get(id string) *ClientSession {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.sessions[id]
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
	Type      string  `json:"type"`  // siempre "log"
	Level     string  `json:"level"` // "waiting" | "opportunity" | "spike_warn" | "spike_block" | "arb"
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

	// SessionID acompaña a resume_session: el token (UUID no enumerable) que el
	// navegador guarda en localStorage para recuperar SU sesión persistida.
	SessionID string  `json:"session_id,omitempty"`
	Exchange  string  `json:"exchange,omitempty"`
	Spread    float64 `json:"spread,omitempty"`
	Liquidity float64 `json:"liquidity,omitempty"`
	UseCredit bool    `json:"use_credit,omitempty"`
	Currency  string  `json:"currency,omitempty"` // "USD" | "BTC" para depósito/retiro
	Amount    float64 `json:"amount,omitempty"`   // >0 deposita, <0 retira

	// Params acompaña a la acción set_params: la UI envía el struct COMPLETO
	// (no parches parciales) y el backend clampea cada campo a rangos sanos.
	Params *TradingParameters `json:"params,omitempty"`
}

type ServerEvent struct {
	Type                 string  `json:"type"` // "state_update", "arb_executed", "spike_blocked", "waiting", "demo_log", "INSUFFICIENT_FUNDS", "CREDIT_PROCESSING", "ENGINE_SHUTDOWN", etc.
	SessionID            string  `json:"session_id"`
	BinanceUSD           float64 `json:"binance_usd"`
	BinanceBTC           float64 `json:"binance_btc"`
	BitsoUSD             float64 `json:"bitso_usd"`
	BitsoBTC             float64 `json:"bitso_btc"`
	BinancePrice         float64 `json:"binance_price,omitempty"`
	BitsoPrice           float64 `json:"bitso_price,omitempty"`
	TotalWealth          float64 `json:"total_wealth"`
	InitialWealth        float64 `json:"initial_wealth"`
	InitialUSD           float64 `json:"initial_usd"`
	TotalNetProfit       float64 `json:"total_net_profit"`
	NetProfit            float64 `json:"net_profit,omitempty"`
	Spread               float64 `json:"spread,omitempty"`
	SpikeFactor          float64 `json:"spike_factor,omitempty"`
	Message              string  `json:"message,omitempty"`
	RequiresManualAction bool    `json:"requiresManualAction,omitempty"`
	ProfitPotential      float64 `json:"profit_potential,omitempty"`
	CreditCost           float64 `json:"credit_cost,omitempty"`
	// CreditRequired = CreditCost × RiskMultiplier de la sesión: el umbral REAL que
	// la ganancia debe superar para que endeudarse sea aceptable para este usuario.
	CreditRequired     float64 `json:"credit_required,omitempty"`
	ExpiresAt          string  `json:"expires_at,omitempty"`
	ReplenishExpiresAt string  `json:"replenish_expires_at,omitempty"`
	AutoMode           bool    `json:"auto_mode,omitempty"`
	IsReplenishing     bool    `json:"is_replenishing,omitempty"`
	CreditActive       bool    `json:"credit_active,omitempty"`
	BorrowedBinanceUSD float64 `json:"borrowed_binance_usd,omitempty"`
	BorrowedBitsoUSD   float64 `json:"borrowed_bitso_usd,omitempty"`
	BorrowedBinanceBTC float64 `json:"borrowed_binance_btc,omitempty"`
	BorrowedBitsoBTC   float64 `json:"borrowed_bitso_btc,omitempty"`
	LoanEarnings       float64 `json:"loan_earnings,omitempty"`
	LoanCost           float64 `json:"loan_cost,omitempty"`

	// Params viaja en state_update y PARAMS_UPDATED para que la UI siempre refleje
	// los parámetros APLICADOS (tras clamps del backend), no los que pidió.
	Params *TradingParameters `json:"params,omitempty"`

	// Graph viaja en los eventos graph_update (~1/s): el radar omnidireccional
	// con los saldos de ESTA sesión superpuestos en cada nodo (ver graph.go).
	Graph *GraphSnapshotWire `json:"graph,omitempty"`

	// Resumed marca el state_update de una sesión RECUPERADA de la base de datos
	// (el frontend salta el onboarding y no resetea la configuración local).
	Resumed bool `json:"resumed,omitempty"`
}

// PriceTick representa un evento de mercado normalizado que un FeedAdapter publica
// hacia el motor: la mejor punta de UN LIBRO (venue + par), con su liquidez y su hora.
type PriceTick struct {
	Exchange string
	Base     string  // activo base del libro ("BTC", "ETH")
	Quote    string  // activo quote del libro ("USDT", "USD", "BTC")
	Ask      float64 // Precio al que compramos (top of book)
	Bid      float64 // Precio al que vendemos (top of book)
	// AskQty/BidQty: cantidad (en Base) ofrecida en cada punta. 0 = desconocida;
	// el motor la interpreta como "sin dato" y dimensiona solo con MaxOrderSizeBTC.
	AskQty float64
	BidQty float64
	// Time habilita el control de staleness (no comparar libros muertos).
	Time time.Time
}

// InstrKey identifica el libro del tick ("Binance:ETH/BTC") — misma convención
// que Instrument.Key().
func (t PriceTick) InstrKey() string { return t.Exchange + ":" + t.Base + "/" + t.Quote }
