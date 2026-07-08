package main

import (
	"encoding/json"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ws_real_market.go — IMPLEMENTACIONES de FeedAdapter (ver feed.go) para Binance y
// Bitso. Cada adaptador mantiene su conexión WebSocket viva (reconexión automática),
// valida la coherencia del libro en el origen y publica ticks normalizados con
// precio, CANTIDAD del top-of-book, timestamp e INSTRUMENTO (Fase 2: un venue
// puede publicar N libros; Binance emite el triángulo BTC/USDT·ETH/USDT·ETH/BTC
// por un único socket de streams combinados).

// TopOfBook es la mejor punta de UN libro: precios, cantidades y cuándo se actualizó.
// UpdatedAt habilita el control de staleness: un libro viejo no debe compararse
// contra uno fresco (produciría spreads fantasma si un feed se congela).
type TopOfBook struct {
	Ask, Bid       float64
	AskQty, BidQty float64
	UpdatedAt      time.Time
}

// LiveMarket guarda el último top-of-book conocido de cada INSTRUMENTO (clave:
// Instrument.Key(), p. ej. "Binance:BTC/USDT"), protegido por mutex (escriben las
// goroutines de ingesta, leen el motor y los handlers).
type LiveMarket struct {
	mu    sync.Mutex
	books map[string]TopOfBook
}

var currentMarket = &LiveMarket{books: make(map[string]TopOfBook)}

// Update fija el top-of-book de un instrumento (UpdatedAt = ahora si viene en cero).
func (m *LiveMarket) Update(instrKey string, t TopOfBook) {
	if t.UpdatedAt.IsZero() {
		t.UpdatedAt = time.Now()
	}
	m.mu.Lock()
	m.books[instrKey] = t
	m.mu.Unlock()
}

// Get devuelve el último top-of-book de un instrumento; ok=false si aún no hay datos.
func (m *LiveMarket) Get(instrKey string) (TopOfBook, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.books[instrKey]
	return t, ok
}

const (
	// Streams nativos en tiempo real (push), sin polling. Binance usa el endpoint
	// de STREAMS COMBINADOS: un solo socket transporta N libros.
	binanceCombinedURL = "wss://stream.binance.com:9443/stream?streams="
	bitsoStreamURL     = "wss://ws.bitso.com"
	krakenStreamURL    = "wss://ws.kraken.com/v2"

	wsReadTimeout   = 70 * time.Second // si no llega nada en este tiempo, reconectamos
	wsReconnectWait = 3 * time.Second  // espera entre intentos de reconexión
)

// publishTick registra el libro en LiveMarket y lo publica al canal del motor.
func publishTick(priceChan chan<- PriceTick, instr Instrument, ask, bid, askQty, bidQty float64) {
	now := time.Now()
	currentMarket.Update(instr.Key(), TopOfBook{Ask: ask, Bid: bid, AskQty: askQty, BidQty: bidQty, UpdatedAt: now})
	priceChan <- PriceTick{
		Exchange: instr.Venue,
		Base:     instr.Base,
		Quote:    instr.Quote,
		Ask:      ask,
		Bid:      bid,
		AskQty:   askQty,
		BidQty:   bidQty,
		Time:     now,
	}
}

// ---------------------------------------------------------------------------
// Binance — streams combinados (N libros, 1 socket)
// ---------------------------------------------------------------------------

// binanceFeed implementa FeedAdapter sobre <symbol>@bookTicker de Binance para
// TODOS los instrumentos del registro cuyo venue sea Binance.
type binanceFeed struct{}

func (binanceFeed) Name() string { return "Binance" }

// binanceBookTicker es el payload del stream <symbol>@bookTicker:
// mejor bid/ask del libro (precio Y cantidad), empujado por Binance en tiempo real.
//
// IMPORTANTE: el matching JSON de Go es case-insensitive. El payload trae tanto
// precios (b, a) como cantidades (B, A); si no declaramos B y A explícitamente,
// las cantidades sobrescriben a los precios (van después en el mensaje) y el motor
// leería ~2.2 en lugar de ~74000. Por eso mapeamos los cuatro campos.
type binanceBookTicker struct {
	Symbol   string `json:"s"`
	BidPrice string `json:"b"`
	BidQty   string `json:"B"`
	AskPrice string `json:"a"`
	AskQty   string `json:"A"`
}

// binanceCombinedMsg envuelve cada evento del endpoint de streams combinados:
// {"stream":"ethbtc@bookTicker","data":{...bookTicker...}}.
type binanceCombinedMsg struct {
	Stream string            `json:"stream"`
	Data   binanceBookTicker `json:"data"`
}

func (f binanceFeed) Run(priceChan chan<- PriceTick) {
	instruments := instrumentsForVenue(f.Name())
	if len(instruments) == 0 {
		log.Printf("[BINANCE] Sin instrumentos registrados — feed no iniciado")
		return
	}

	// URL combinada + índice stream→instrumento para despachar cada evento.
	streams := make([]string, 0, len(instruments))
	byStream := make(map[string]Instrument, len(instruments))
	for _, in := range instruments {
		s := in.StreamID + "@bookTicker"
		streams = append(streams, s)
		byStream[s] = in
	}
	url := binanceCombinedURL + strings.Join(streams, "/")

	for {
		conn, _, err := websocket.DefaultDialer.Dial(url, nil)
		if err != nil {
			log.Printf("[BINANCE] Error de conexión WS: %v — reintentando en %s", err, wsReconnectWait)
			time.Sleep(wsReconnectWait)
			continue
		}
		log.Printf("✅ Binance WebSocket conectado — %d libros por streams combinados (%s)", len(instruments), strings.Join(streams, ", "))

		readWSLoop(conn, "BINANCE", func(data []byte) {
			var m binanceCombinedMsg
			if err := json.Unmarshal(data, &m); err != nil {
				return
			}
			instr, ok := byStream[m.Stream]
			if !ok {
				return
			}
			ask, errA := strconv.ParseFloat(m.Data.AskPrice, 64)
			bid, errB := strconv.ParseFloat(m.Data.BidPrice, 64)
			if errA != nil || errB != nil || !coherentBook(ask, bid) {
				return
			}
			// Las cantidades son informativas: si faltan o vienen corruptas, el tick
			// sigue siendo válido (qty=0 se interpreta como "desconocida" aguas abajo).
			askQty, _ := strconv.ParseFloat(m.Data.AskQty, 64)
			bidQty, _ := strconv.ParseFloat(m.Data.BidQty, 64)

			publishTick(priceChan, instr, ask, bid, askQty, bidQty)
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
	Book    string `json:"book"`
	Payload struct {
		Bids []bitsoOrder `json:"bids"`
		Asks []bitsoOrder `json:"asks"`
	} `json:"payload"`
}

func (f bitsoFeed) Run(priceChan chan<- PriceTick) {
	instruments := instrumentsForVenue(f.Name())
	if len(instruments) == 0 {
		log.Printf("[BITSO] Sin instrumentos registrados — feed no iniciado")
		return
	}
	byBook := make(map[string]Instrument, len(instruments))
	for _, in := range instruments {
		byBook[in.StreamID] = in
	}

	for {
		conn, _, err := websocket.DefaultDialer.Dial(bitsoStreamURL, nil)
		if err != nil {
			log.Printf("[BITSO] Error de conexión WS: %v — reintentando en %s", err, wsReconnectWait)
			time.Sleep(wsReconnectWait)
			continue
		}

		// Bitso requiere suscribirse explícitamente a cada libro.
		subscribed := true
		for _, in := range instruments {
			sub := map[string]string{"action": "subscribe", "book": in.StreamID, "type": "orders"}
			if err := conn.WriteJSON(sub); err != nil {
				log.Printf("[BITSO] Error al suscribirse a %s: %v", in.StreamID, err)
				subscribed = false
				break
			}
		}
		if !subscribed {
			conn.Close()
			time.Sleep(wsReconnectWait)
			continue
		}
		log.Printf("✅ Bitso WebSocket conectado — %d libro(s) (canal orders)", len(instruments))

		readWSLoop(conn, "BITSO", func(data []byte) {
			var m bitsoWSMessage
			if err := json.Unmarshal(data, &m); err != nil {
				return
			}
			if m.Type != "orders" {
				return // confirmaciones de suscripción, keep-alive, etc.
			}
			instr, ok := byBook[m.Book]
			if !ok {
				// Compat: mensajes antiguos sin "book" cuando solo hay un libro.
				if len(instruments) == 1 && m.Book == "" {
					instr = instruments[0]
				} else {
					return
				}
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

			publishTick(priceChan, instr, ask, bid, askQty, bidQty)
		})

		conn.Close()
		log.Printf("[BITSO] Conexión cerrada — reconectando en %s", wsReconnectWait)
		time.Sleep(wsReconnectWait)
	}
}

// ---------------------------------------------------------------------------
// Kraken — WebSocket v2 (canal ticker, N libros por un socket)
// ---------------------------------------------------------------------------

// krakenFeed implementa FeedAdapter sobre el canal "ticker" del WS v2 de Kraken
// para todos los instrumentos del registro cuyo venue sea Kraken. Con
// event_trigger="bbo" Kraken empuja cada cambio del mejor bid/ask (no solo
// trades), que es exactamente el top-of-book que consume el motor.
type krakenFeed struct{}

func (krakenFeed) Name() string { return "Kraken" }

// krakenTicker es una entrada del canal ticker v2. A diferencia de Binance y
// Bitso (que serializan precios como strings), Kraken v2 envía NÚMEROS JSON —
// verificado contra el stream real; ver también docs.kraken.com/api.
type krakenTicker struct {
	Symbol string  `json:"symbol"` // símbolo normalizado ("BTC/USD") = StreamID del registro
	Bid    float64 `json:"bid"`
	BidQty float64 `json:"bid_qty"`
	Ask    float64 `json:"ask"`
	AskQty float64 `json:"ask_qty"`
}

// krakenWSMessage cubre los mensajes del canal ticker (snapshot y update).
// Los heartbeats, acks de suscripción y el canal status traen otro "channel"
// y se ignoran sin error.
type krakenWSMessage struct {
	Channel string         `json:"channel"`
	Type    string         `json:"type"`
	Data    []krakenTicker `json:"data"`
}

// krakenSubscription es la petición de suscripción del WS v2.
type krakenSubscription struct {
	Method string `json:"method"`
	Params struct {
		Channel      string   `json:"channel"`
		Symbol       []string `json:"symbol"`
		EventTrigger string   `json:"event_trigger"`
	} `json:"params"`
}

func (f krakenFeed) Run(priceChan chan<- PriceTick) {
	instruments := instrumentsForVenue(f.Name())
	if len(instruments) == 0 {
		log.Printf("[KRAKEN] Sin instrumentos registrados — feed no iniciado")
		return
	}
	symbols := make([]string, 0, len(instruments))
	bySymbol := make(map[string]Instrument, len(instruments))
	for _, in := range instruments {
		symbols = append(symbols, in.StreamID)
		bySymbol[in.StreamID] = in
	}

	for {
		conn, _, err := websocket.DefaultDialer.Dial(krakenStreamURL, nil)
		if err != nil {
			log.Printf("[KRAKEN] Error de conexión WS: %v — reintentando en %s", err, wsReconnectWait)
			time.Sleep(wsReconnectWait)
			continue
		}

		sub := krakenSubscription{Method: "subscribe"}
		sub.Params.Channel = "ticker"
		sub.Params.Symbol = symbols
		sub.Params.EventTrigger = "bbo"
		if err := conn.WriteJSON(sub); err != nil {
			log.Printf("[KRAKEN] Error al suscribirse: %v", err)
			conn.Close()
			time.Sleep(wsReconnectWait)
			continue
		}
		log.Printf("✅ Kraken WebSocket conectado — %d libro(s) (canal ticker v2: %s)", len(instruments), strings.Join(symbols, ", "))

		readWSLoop(conn, "KRAKEN", func(data []byte) {
			var m krakenWSMessage
			if err := json.Unmarshal(data, &m); err != nil {
				return
			}
			if m.Channel != "ticker" {
				return // heartbeat, status, ack de suscripción…
			}
			for _, t := range m.Data {
				instr, ok := bySymbol[t.Symbol]
				if !ok || !coherentBook(t.Ask, t.Bid) {
					continue
				}
				publishTick(priceChan, instr, t.Ask, t.Bid, t.AskQty, t.BidQty)
			}
		})

		conn.Close()
		log.Printf("[KRAKEN] Conexión cerrada — reconectando en %s", wsReconnectWait)
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
