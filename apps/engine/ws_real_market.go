package main

import (
	"encoding/json"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ws_real_market.go — IMPLEMENTACIONES de FeedAdapter (ver feed.go) para Binance y
// Bitso. Cada adaptador mantiene su conexión WebSocket viva (reconexión automática),
// valida la coherencia del libro en el origen y publica ticks normalizados con
// precio, CANTIDAD del top-of-book y timestamp — los tres datos que la Fase 1 usa
// para dimensionar órdenes contra liquidez real y detectar feeds congelados.

// TopOfBook es la mejor punta de UN venue: precios, cantidades y cuándo se actualizó.
// UpdatedAt habilita el control de staleness: un libro viejo no debe compararse
// contra uno fresco (produciría spreads fantasma si un feed se congela).
type TopOfBook struct {
	Ask, Bid       float64
	AskQty, BidQty float64
	UpdatedAt      time.Time
}

// LiveMarket guarda el último top-of-book conocido de cada venue, protegido por
// mutex (escriben las goroutines de ingesta, leen el motor y los handlers).
type LiveMarket struct {
	mu    sync.Mutex
	books map[string]TopOfBook
}

var currentMarket = &LiveMarket{books: make(map[string]TopOfBook)}

// Update fija el top-of-book de un venue (marca UpdatedAt = ahora si viene en cero).
func (m *LiveMarket) Update(venue string, t TopOfBook) {
	if t.UpdatedAt.IsZero() {
		t.UpdatedAt = time.Now()
	}
	m.mu.Lock()
	m.books[venue] = t
	m.mu.Unlock()
}

// Get devuelve el último top-of-book de un venue; ok=false si aún no hay datos.
func (m *LiveMarket) Get(venue string) (TopOfBook, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.books[venue]
	return t, ok
}

const (
	// Streams nativos en tiempo real (push), sin polling.
	binanceStreamURL = "wss://stream.binance.com:9443/ws/btcusdt@bookTicker"
	bitsoStreamURL   = "wss://ws.bitso.com"

	wsReadTimeout   = 70 * time.Second // si no llega nada en este tiempo, reconectamos
	wsReconnectWait = 3 * time.Second  // espera entre intentos de reconexión
)

// ---------------------------------------------------------------------------
// Binance
// ---------------------------------------------------------------------------

// binanceFeed implementa FeedAdapter sobre el stream <symbol>@bookTicker de Binance.
type binanceFeed struct{}

func (binanceFeed) Name() string { return "Binance" }

// binanceBookTicker es el payload del stream <symbol>@bookTicker:
// mejor bid/ask del libro (precio Y cantidad), empujado por Binance en tiempo real.
//
// IMPORTANTE: el matching JSON de Go es case-insensitive. El payload trae tanto
// precios (b, a) como cantidades (B, A); si no declaramos B y A explícitamente,
// las cantidades sobrescriben a los precios (van después en el mensaje) y el motor
// leería ~2.2 en lugar de ~74000. Por eso mapeamos los cuatro campos — y desde la
// Fase 1 las cantidades ya no se descartan: alimentan el dimensionado de órdenes.
type binanceBookTicker struct {
	Symbol   string `json:"s"`
	BidPrice string `json:"b"`
	BidQty   string `json:"B"`
	AskPrice string `json:"a"`
	AskQty   string `json:"A"`
}

func (f binanceFeed) Run(priceChan chan<- PriceTick) {
	for {
		conn, _, err := websocket.DefaultDialer.Dial(binanceStreamURL, nil)
		if err != nil {
			log.Printf("[BINANCE] Error de conexión WS: %v — reintentando en %s", err, wsReconnectWait)
			time.Sleep(wsReconnectWait)
			continue
		}
		log.Println("✅ Binance WebSocket conectado — recibiendo BTC/USDT (bookTicker)")

		// El stream raw no requiere suscripción: empieza a empujar de inmediato.
		readWSLoop(conn, "BINANCE", func(data []byte) {
			var t binanceBookTicker
			if err := json.Unmarshal(data, &t); err != nil {
				return
			}
			ask, errA := strconv.ParseFloat(t.AskPrice, 64)
			bid, errB := strconv.ParseFloat(t.BidPrice, 64)
			if errA != nil || errB != nil || !coherentBook(ask, bid) {
				return
			}
			// Las cantidades son informativas: si faltan o vienen corruptas, el tick
			// sigue siendo válido (qty=0 se interpreta como "desconocida" aguas abajo).
			askQty, _ := strconv.ParseFloat(t.AskQty, 64)
			bidQty, _ := strconv.ParseFloat(t.BidQty, 64)

			now := time.Now()
			currentMarket.Update(f.Name(), TopOfBook{Ask: ask, Bid: bid, AskQty: askQty, BidQty: bidQty, UpdatedAt: now})

			priceChan <- PriceTick{Exchange: f.Name(), Ask: ask, Bid: bid, AskQty: askQty, BidQty: bidQty, Time: now}
		})

		conn.Close()
		log.Printf("[BINANCE] Conexión cerrada — reconectando en %s", wsReconnectWait)
		time.Sleep(wsReconnectWait)
	}
}

// ---------------------------------------------------------------------------
// Bitso
// ---------------------------------------------------------------------------

// bitsoFeed implementa FeedAdapter sobre el canal "orders" (libro) de Bitso.
type bitsoFeed struct{}

func (bitsoFeed) Name() string { return "Bitso" }

// bitsoOrder es una entrada del libro de órdenes (r = rate/precio, a = amount/cantidad).
type bitsoOrder struct {
	Rate   string `json:"r"`
	Amount string `json:"a"`
}

// bitsoWSMessage cubre tanto la confirmación de suscripción como los mensajes
// del canal "orders" (top del libro de órdenes con bids y asks).
type bitsoWSMessage struct {
	Type    string `json:"type"`
	Payload struct {
		Bids []bitsoOrder `json:"bids"`
		Asks []bitsoOrder `json:"asks"`
	} `json:"payload"`
}

func (f bitsoFeed) Run(priceChan chan<- PriceTick) {
	for {
		conn, _, err := websocket.DefaultDialer.Dial(bitsoStreamURL, nil)
		if err != nil {
			log.Printf("[BITSO] Error de conexión WS: %v — reintentando en %s", err, wsReconnectWait)
			time.Sleep(wsReconnectWait)
			continue
		}

		// Bitso requiere suscribirse explícitamente al canal del libro.
		sub := map[string]string{"action": "subscribe", "book": "btc_usd", "type": "orders"}
		if err := conn.WriteJSON(sub); err != nil {
			log.Printf("[BITSO] Error al suscribirse: %v", err)
			conn.Close()
			time.Sleep(wsReconnectWait)
			continue
		}
		log.Println("✅ Bitso WebSocket conectado — recibiendo BTC/USD (orders)")

		readWSLoop(conn, "BITSO", func(data []byte) {
			var m bitsoWSMessage
			if err := json.Unmarshal(data, &m); err != nil {
				return
			}
			if m.Type != "orders" {
				return // confirmaciones de suscripción, keep-alive, etc.
			}
			// Solo actuamos sobre snapshots completos (ambos lados presentes); así
			// evitamos fijar un top-of-book parcial/incoherente al arrancar.
			if len(m.Payload.Asks) == 0 || len(m.Payload.Bids) == 0 {
				return
			}

			// Mejor ask = menor precio de venta; mejor bid = mayor precio de compra.
			// Junto con el precio tomamos la CANTIDAD ofrecida en ese nivel: es la
			// liquidez real contra la que se dimensionan las órdenes.
			ask, askQty := bestAsk(m.Payload.Asks)
			bid, bidQty := bestBid(m.Payload.Bids)
			if !coherentBook(ask, bid) {
				return
			}

			now := time.Now()
			currentMarket.Update(f.Name(), TopOfBook{Ask: ask, Bid: bid, AskQty: askQty, BidQty: bidQty, UpdatedAt: now})

			priceChan <- PriceTick{Exchange: f.Name(), Ask: ask, Bid: bid, AskQty: askQty, BidQty: bidQty, Time: now}
		})

		conn.Close()
		log.Printf("[BITSO] Conexión cerrada — reconectando en %s", wsReconnectWait)
		time.Sleep(wsReconnectWait)
	}
}

// ---------------------------------------------------------------------------
// Utilidades compartidas de ingesta
// ---------------------------------------------------------------------------

// readWSLoop lee mensajes hasta que ocurra un error (con read deadline para
// detectar conexiones muertas). gorilla responde a los ping del servidor con
// pong automáticamente, manteniendo viva la conexión mientras llegan datos.
func readWSLoop(conn *websocket.Conn, tag string, onMessage func([]byte)) {
	for {
		_ = conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
		_, data, err := conn.ReadMessage()
		if err != nil {
			log.Printf("[%s] Error de lectura WS: %v", tag, err)
			return
		}
		onMessage(data)
	}
}

// coherentBook valida que un top-of-book sea sano antes de alimentar al motor:
// ambos lados positivos, no cruzado (ask >= bid) y con un spread interno realista
// (< 5 %). Filtra valores basura/transitorios que, de colarse, el Spike Filter
// "fijaría" y luego rechazaría a todos los ticks buenos (congelando el motor).
func coherentBook(ask, bid float64) bool {
	if ask <= 0 || bid <= 0 || ask < bid {
		return false
	}
	return (ask-bid)/ask < 0.05
}

// bestAsk devuelve el menor precio (>0) de una lista de órdenes y la cantidad
// ofrecida en ese nivel; (0, 0) si la lista está vacía o corrupta.
func bestAsk(orders []bitsoOrder) (price, qty float64) {
	for _, o := range orders {
		r, err := strconv.ParseFloat(o.Rate, 64)
		if err != nil || r <= 0 {
			continue
		}
		if price == 0 || r < price {
			price = r
			qty, _ = strconv.ParseFloat(o.Amount, 64)
		}
	}
	return price, qty
}

// bestBid devuelve el mayor precio (>0) de una lista de órdenes y la cantidad
// ofrecida en ese nivel; (0, 0) si la lista está vacía o corrupta.
func bestBid(orders []bitsoOrder) (price, qty float64) {
	for _, o := range orders {
		r, err := strconv.ParseFloat(o.Rate, 64)
		if err != nil || r <= 0 {
			continue
		}
		if r > price {
			price = r
			qty, _ = strconv.ParseFloat(o.Amount, 64)
		}
	}
	return price, qty
}
