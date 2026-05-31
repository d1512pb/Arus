package main

import (
	"encoding/json"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type LiveMarket struct {
	mu         sync.Mutex
	BinanceAsk float64
	BinanceBid float64
	BitsoAsk   float64
	BitsoBid   float64
}

var currentMarket = &LiveMarket{}

const (
	// Streams nativos en tiempo real (push), sin polling.
	binanceStreamURL = "wss://stream.binance.com:9443/ws/btcusdt@bookTicker"
	bitsoStreamURL   = "wss://ws.bitso.com"

	wsReadTimeout   = 70 * time.Second // si no llega nada en este tiempo, reconectamos
	wsReconnectWait = 3 * time.Second  // espera entre intentos de reconexión
)

// StartRealMarketWS abre las conexiones WebSocket a ambos exchanges y mantiene
// vivas las suscripciones (con reconexión automática). Cada actualización de
// precio se publica en priceChan, igual que hacía la versión con polling.
func StartRealMarketWS(priceChan chan<- PriceTick) {
	log.Println("🔗 Conectando a Binance (WebSocket bookTicker)...")
	go streamBinance(priceChan)

	log.Println("🔗 Conectando a Bitso (WebSocket orders)...")
	go streamBitso(priceChan)
}

// binanceBookTicker es el payload del stream <symbol>@bookTicker:
// mejor bid/ask del libro, empujado por Binance en tiempo real.
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

func streamBinance(priceChan chan<- PriceTick) {
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

			currentMarket.mu.Lock()
			currentMarket.BinanceAsk = ask
			currentMarket.BinanceBid = bid
			currentMarket.mu.Unlock()

			priceChan <- PriceTick{Exchange: "Binance", Ask: ask, Bid: bid}
		})

		conn.Close()
		log.Printf("[BINANCE] Conexión cerrada — reconectando en %s", wsReconnectWait)
		time.Sleep(wsReconnectWait)
	}
}

// bitsoOrder es una entrada del libro de órdenes (r = rate/precio).
type bitsoOrder struct {
	Rate string `json:"r"`
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

func streamBitso(priceChan chan<- PriceTick) {
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
			ask := minRate(m.Payload.Asks)
			bid := maxRate(m.Payload.Bids)
			if !coherentBook(ask, bid) {
				return
			}

			currentMarket.mu.Lock()
			currentMarket.BitsoAsk = ask
			currentMarket.BitsoBid = bid
			currentMarket.mu.Unlock()

			priceChan <- PriceTick{Exchange: "Bitso", Ask: ask, Bid: bid}
		})

		conn.Close()
		log.Printf("[BITSO] Conexión cerrada — reconectando en %s", wsReconnectWait)
		time.Sleep(wsReconnectWait)
	}
}

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

// minRate devuelve el menor precio (>0) de una lista de órdenes; 0 si está vacía.
func minRate(orders []bitsoOrder) float64 {
	best := 0.0
	for _, o := range orders {
		r, err := strconv.ParseFloat(o.Rate, 64)
		if err != nil || r <= 0 {
			continue
		}
		if best == 0 || r < best {
			best = r
		}
	}
	return best
}

// maxRate devuelve el mayor precio (>0) de una lista de órdenes; 0 si está vacía.
func maxRate(orders []bitsoOrder) float64 {
	best := 0.0
	for _, o := range orders {
		r, err := strconv.ParseFloat(o.Rate, 64)
		if err != nil || r <= 0 {
			continue
		}
		if r > best {
			best = r
		}
	}
	return best
}
