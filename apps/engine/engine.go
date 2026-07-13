package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	SpikeWarnMultiplier  = 15.0
	SpikeBlockMultiplier = 50.0

	// OrderFailurePause es cuánto se pausa la sesión tras un Fill-or-Kill fallido, para no
	// reintentar a ciegas contra un libro que está rechazando liquidez en ese instante.
	OrderFailurePause = 2 * time.Second
)

// orderFails simula una Falla de Orden Parcial (Fill-or-Kill): con probabilidad
// `prob` la orden remota NO se llena (TradingParameters.OrderFailureProb — cada
// sesión define la "física" de su simulador). Se evalúa JUSTO antes de mover
// wallets, de modo que un fallo aborta sin dejar estado a medias (sin exposición
// direccional).
func orderFails(prob float64) bool {
	return rand.Float64() < prob
}

// computeNetProfit es LA fórmula institucional de rentabilidad — la única fuente de
// verdad del motor. Toda decisión de ejecutar (bucle de referencia, ejecución por
// sesión, simulador y benchmark) pasa por aquí:
//
//	Neto = (P_venta × V × (1 − fee_venta)) − (P_compra × V × (1 + fee_compra)) − Slippage
//
// que equivale a: bruto − fees − slippage, con el fee de compra ENCARECIENDO el
// costo y el de venta REDUCIENDO el ingreso, tal como cobra cada exchange.
// Devuelve el desglose completo para que cada evaluación sea auditable en la UI.
func computeNetProfit(buyPrice, sellPrice, volume, feeBuy, feeSell, slippageRate float64) (gross, fees, slippage, net float64) {
	gross = (sellPrice - buyPrice) * volume
	fees = (buyPrice*feeBuy + sellPrice*feeSell) * volume
	slippage = estimateSlippage(buyPrice, sellPrice, volume, slippageRate)
	net = gross - fees - slippage
	return gross, fees, slippage, net
}

// estimateSlippage devuelve el costo estimado de deslizamiento para una operación
// que compra y vende `volume` BTC. En un libro real, una orden de mercado no se
// llena íntegra en el top-of-book: consume varios niveles y empeora el precio
// promedio. Lo modelamos como slippageRate (fracción por pierna, configurable por
// el usuario desde el panel de estrategia) sobre el notional de CADA pierna, y se
// descuenta ANTES de decidir.
func estimateSlippage(buyPrice, sellPrice, volume, slippageRate float64) float64 {
	return (buyPrice + sellPrice) * volume * slippageRate
}

// creditWorthIt es la inecuación de dominancia del crédito: endeudarse solo si la
// ganancia proyectada supera el costo del préstamo multiplicado por el apetito de
// riesgo del usuario (1 = punto de equilibrio; 5 = exigir cubrir 5× el costo).
func creditWorthIt(projectedProfit, creditCost, riskMultiplier float64) bool {
	return projectedProfit > creditCost*riskMultiplier
}

// SpreadTracker mantiene la media móvil del spread para el Spike Filter.
// Protegido con mutex: lo escribe el bucle de detección y lo leen las goroutines
// del simulador (runDemoInjection) concurrentemente.
type SpreadTracker struct {
	mu         sync.Mutex
	recent     []float64
	maxSamples int
}

func NewSpreadTracker() *SpreadTracker {
	return &SpreadTracker{maxSamples: 20}
}

func (s *SpreadTracker) Add(spread float64) {
	s.mu.Lock()
	s.recent = append(s.recent, spread)
	if len(s.recent) > s.maxSamples {
		s.recent = s.recent[1:]
	}
	s.mu.Unlock()
}

func (s *SpreadTracker) Average() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.recent) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range s.recent {
		sum += v
	}
	return sum / float64(len(s.recent))
}

type HFTEngine struct {
	Tracker *SpreadTracker
	// Graph es el radar omnidireccional (Fase 2): el grafo de liquidez que se
	// actualiza con cada tick y detecta ciclos de arbitraje automáticamente.
	Graph *LiquidityGraph
	// Ticks es el canal de INGESTA del motor — el mismo que alimentan los
	// FeedAdapters reales. El simulador omnidireccional (omni.go) inyecta por
	// aquí sus MarketTicks fabricados: no existe un camino de evaluación
	// paralelo, todo entra por la misma tubería.
	Ticks chan<- PriceTick
}

func getLevel(msg string) string {
	if strings.Contains(msg, "[OPORTUNIDAD]") {
		return "opportunity"
	}
	if strings.Contains(msg, "[ARBITRAJE]") {
		return "arb"
	}
	if strings.Contains(msg, "[RADAR]") {
		return "opportunity"
	}
	if strings.Contains(msg, "SPIKE ALERTA") {
		return "spike_warn"
	}
	if strings.Contains(msg, "FEED CONGELADO") {
		return "spike_warn"
	}
	if strings.Contains(msg, "SPIKE BLOQUEADO") {
		return "spike_block"
	}
	if strings.Contains(msg, "CIRCUIT BREAKER") {
		return "spike_block"
	}
	if strings.Contains(msg, "[EN ESPERA]") {
		return "waiting"
	}
	return "info"
}

func sendLog(s *ClientSession, msg string) {
	log.Println(msg)
	if s != nil {
		s.WriteJSON(LogEvent{
			Type:      "log",
			Level:     getLevel(msg),
			Timestamp: time.Now().Format("15:04:05.000"),
			Message:   msg,
		})
	}
}

func broadcastLog(hub *Hub, msg string, spread float64, netProfit float64) {
	log.Println(msg)
	hub.BroadcastLog(LogEvent{
		Type:      "log",
		Level:     getLevel(msg),
		Timestamp: time.Now().Format("15:04:05.000"),
		Message:   msg,
		Spread:    spread,
		NetProfit: netProfit,
	})
}

// isValidTick descarta un tick cuya variación respecto al anterior supere maxDeviation
// (fracción, p. ej. 0.05 = 5 %). Se aplica en DOS capas: el bucle compartido de ingesta
// filtra con el default del motor (protege el grafo y el tracker globales), y
// executeForSession vuelve a filtrar con la tolerancia de CADA sesión
// (TradingParameters.SpikeTickDeviation) sobre los mids del par.
func isValidTick(newPrice, lastPrice, maxDeviation float64) bool {
	if lastPrice == 0 {
		return true
	}
	deviation := math.Abs(newPrice-lastPrice) / lastPrice
	return deviation <= maxDeviation
}

func getBTCPrice() float64 {
	if book, ok := currentMarket.Get("Binance:BTC/USDT"); ok && book.Ask > 0 {
		return book.Ask
	}
	return DefaultBTCPriceFallback
}

// assetPriceUSD valora un activo en USD con el mid de su libro contra un quote
// cash (BTC vía Binance:BTC/USDT, ETH vía Binance:ETH/USDT…); cash = 1.
func assetPriceUSD(asset string) float64 {
	if asset == "USD" || assetsParity(asset, "USD") {
		return 1
	}
	for _, in := range Instruments {
		if in.Base == asset && (in.Quote == "USD" || assetsParity(in.Quote, "USD")) {
			if book, ok := currentMarket.Get(in.Key()); ok && book.Ask > 0 && book.Bid > 0 {
				return (book.Ask + book.Bid) / 2
			}
		}
	}
	switch asset {
	case "BTC":
		return DefaultBTCPriceFallback
	case "ETH":
		return DefaultETHPriceFallback
	case "SOL":
		return DefaultSOLPriceFallback
	}
	return 0
}

func (e *HFTEngine) sessionHasFundsForTrade(session *ClientSession, buyEx, sellEx string, volume, buyPrice float64) bool {
	p := session.Params()
	requiredUSD := buyPrice * volume * (1 + p.takerFee(buyEx))

	session.Mu.Lock()
	defer session.Mu.Unlock()
	return session.Wallets.Get(buyEx, quoteOf(buyEx)) >= requiredUSD &&
		session.Wallets.Get(sellEx, baseOf(sellEx)) >= volume
}

// adjustFunds permite depositar (amount > 0) o retirar (amount < 0) CUALQUIER
// activo que el venue publique (USD/USDT, BTC, ETH, SOL…). Para que el movimiento
// NO se contabilice como PnL del bot, ajustamos por igual TotalWealth e
// InitialWealth (valorados a precio de mercado en ≈USD).
func (e *HFTEngine) adjustFunds(session *ClientSession, exchange, currency string, amount float64) {
	if amount == 0 {
		return
	}

	session.Mu.Lock()
	if session.Wallets == nil || !isKnownVenue(exchange) {
		session.Mu.Unlock()
		return
	}

	asset := strings.ToUpper(strings.TrimSpace(currency))
	if asset == "" {
		session.Mu.Unlock()
		return
	}
	// Atajo UI: "USD" / "CASH" → efectivo nativo del venue (USDT en Binance, USD en Bitso).
	if asset == "USD" || asset == "CASH" {
		asset = quoteOf(exchange)
	}
	if isCashAsset(Asset(asset)) && assetsParity(asset, quoteOf(exchange)) {
		asset = quoteOf(exchange)
	}
	if !venueHasAsset(exchange, asset) {
		session.Mu.Unlock()
		sendLog(session, fmt.Sprintf("💤 [FONDOS] %s no opera %s — elige un activo que exista en esa casa.", exchange, asset))
		return
	}

	px := assetPriceUSD(asset)
	if px <= 0 {
		session.Mu.Unlock()
		sendLog(session, fmt.Sprintf("🧊 [FONDOS] Sin precio de mercado para %s — no se puede valorar el movimiento.", asset))
		return
	}

	applied := amount
	if cur := session.Wallets.Get(exchange, asset); amount < 0 && cur+amount < 0 {
		applied = -cur
	}
	if applied == 0 {
		session.Mu.Unlock()
		return
	}

	session.Wallets.Add(exchange, asset, applied)
	usdDelta := applied * px
	session.TotalWealth += usdDelta
	session.InitialWealth += usdDelta
	if isCashAsset(Asset(asset)) {
		session.InitialUSD += applied
	} else if asset == "BTC" {
		session.InitialBTC += applied
	}
	session.Mu.Unlock()

	action := "Depósito"
	if applied < 0 {
		action = "Retiro"
	}
	if isCashAsset(Asset(asset)) {
		sendLog(session, fmt.Sprintf("🏦 [FONDOS] %s en %s: $%.2f %s", action, exchange, math.Abs(applied), asset))
	} else {
		sendLog(session, fmt.Sprintf("🏦 [FONDOS] %s en %s: %.6f %s (≈$%.2f)", action, exchange, math.Abs(applied), asset, math.Abs(usdDelta)))
	}
	sendWalletUpdate(session)
}

func sendWalletUpdate(s *ClientSession) {
	s.Mu.Lock()
	ev := ServerEvent{
		Type:      "wallet_update",
		SessionID: s.ID,
		// Wire plano 2-venue (compatibilidad con el dashboard desplegado): el
		// quote del venue viaja como *_usd y el base como *_btc. El estado
		// COMPLETO multi-activo viaja además en Balances.
		BinanceUSD:     s.Wallets.Get("Binance", quoteOf("Binance")),
		BinanceBTC:     s.Wallets.Get("Binance", baseOf("Binance")),
		BitsoUSD:       s.Wallets.Get("Bitso", quoteOf("Bitso")),
		BitsoBTC:       s.Wallets.Get("Bitso", baseOf("Bitso")),
		Balances:       s.Wallets.Clone(),
		TotalWealth:    s.TotalWealth,
		TotalNetProfit: s.TotalNetProfit,
		IsReplenishing: s.IsReplenishing,
		CreditActive:   s.Credit.Active,
		AutoMode:       s.Credit.AutoMode,
	}
	if s.Credit.Active {
		if s.Credit.Borrowed != nil {
			ev.BorrowedBalances = s.Credit.Borrowed.Clone()
			ev.BorrowedBinanceUSD = s.Credit.Borrowed.Get("Binance", quoteOf("Binance"))
			ev.BorrowedBitsoUSD = s.Credit.Borrowed.Get("Bitso", quoteOf("Bitso"))
			ev.BorrowedBinanceBTC = s.Credit.Borrowed.Get("Binance", "BTC")
			ev.BorrowedBitsoBTC = s.Credit.Borrowed.Get("Bitso", "BTC")
		} else if s.Credit.BorrowedUSD != nil {
			ev.BorrowedBinanceUSD = s.Credit.BorrowedUSD["Binance"]
			ev.BorrowedBitsoUSD = s.Credit.BorrowedUSD["Bitso"]
			ev.BorrowedBinanceBTC = s.Credit.BorrowedBTC["Binance"]
			ev.BorrowedBitsoBTC = s.Credit.BorrowedBTC["Bitso"]
		}
	}
	if s.Credit.Active && !s.Credit.ExpiresAt.IsZero() {
		ev.ExpiresAt = s.Credit.ExpiresAt.Format(time.RFC3339)
	}
	if s.IsReplenishing && !s.ReplenishExpiresAt.IsZero() {
		ev.ReplenishExpiresAt = s.ReplenishExpiresAt.Format(time.RFC3339)
	}
	s.Mu.Unlock()
	sendEvent(s, ev)

	// Persistencia write-behind (Sprint A): sendWalletUpdate es el punto de paso
	// de TODA mutación de saldos (trades, depósitos/retiros, crédito, rebalanceo),
	// así que aquí la fotografía de la sesión llega a SQLite sin frenar el trading.
	persistSessionAsync(s)
}

// sendArbExecuted emite el evento arbitrage_executed con el estado de wallets al
// momento (única definición del payload: la usan el camino real y el simulador).
func sendArbExecuted(session *ClientSession, buyEx, sellEx string, volume, netProfit float64) {
	payload := map[string]interface{}{
		"event":          "arbitrage_executed",
		"exchange_buy":   buyEx,
		"exchange_sell":  sellEx,
		"buy_exchange":   buyEx,
		"sell_exchange":  sellEx,
		"volume":         volume,
		"net_profit_usd": netProfit,
		"net_profit":     netProfit,
		"timestamp":      time.Now().Format("15:04:05.000"),
	}
	session.Mu.Lock()
	payload["new_total_usd"] = session.TotalWealth
	payload["credit_active"] = session.Credit.Active
	payload["binance_usd"] = session.Wallets.Get("Binance", quoteOf("Binance"))
	payload["binance_btc"] = session.Wallets.Get("Binance", baseOf("Binance"))
	payload["bitso_usd"] = session.Wallets.Get("Bitso", quoteOf("Bitso"))
	payload["bitso_btc"] = session.Wallets.Get("Bitso", baseOf("Bitso"))
	payload["balances"] = session.Wallets.Clone()
	session.Mu.Unlock()

	b, _ := json.Marshal(payload)
	session.WriteMessage(websocket.TextMessage, b)
	sendWalletUpdate(session)
}

// pairView es la instantánea del par de libros (precios + liquidez top-of-book) que
// recibe cada ejecución por sesión. Sigue siendo explícitamente de DOS venues: es el
// núcleo pre-grafo. La Fase 2 lo sustituye por ciclos sobre el grafo de liquidez
// (ver graph.go y docs/FASE2-GRAFO.md) sin tocar la capa de ingesta ni los params.
type pairView struct {
	BinAsk, BinBid, BitAsk, BitBid             float64
	BinAskQty, BinBidQty, BitAskQty, BitBidQty float64
}

// venueTickState acumula el último estado aceptado de UN venue en el bucle de
// detección: precios previos (para el Spike Filter tick-a-tick) y el top-of-book
// vigente con su hora (para el control de staleness).
type venueTickState struct {
	lastAsk, lastBid float64
	book             TopOfBook
	hasData          bool
}

func (e *HFTEngine) Start(priceChan <-chan PriceTick, hub *Hub) {
	// states lleva un estado por INSTRUMENTO (clave InstrKey): desde el hito 2 un
	// venue puede publicar N libros (Binance emite el triángulo BTC/USDT ·
	// ETH/USDT · ETH/BTC) y cada uno tiene su propio Spike Filter y staleness.
	states := make(map[string]*venueTickState)
	lastLogTime := time.Now()
	lastStaleLog := time.Time{}
	// radarCycleActive evita repetir el log del radar en cada barrido: se anuncia
	// al APARECER un ciclo rentable, no mientras persiste.
	radarCycleActive := false
	// Fees de referencia del registro, resueltos UNA vez fuera del hot loop
	// (defaultTakerFees construye un mapa nuevo por llamada — no per-tick).
	refFees := defaultTakerFees()
	refBinanceFee, refBitsoFee := refFees["Binance"], refFees["Bitso"]

	// Los dos libros RESPALDADOS POR WALLETS que ejecuta el motor de dos venues.
	// Los demás instrumentos alimentan solo al radar (detección, no ejecución).
	binInstr, _ := primaryInstrument("Binance")
	bitInstr, _ := primaryInstrument("Bitso")
	binKey, bitKey := binInstr.Key(), bitInstr.Key()

	for tick := range priceChan {
		key := tick.InstrKey()
		st, ok := states[key]
		if !ok {
			st = &venueTickState{}
			states[key] = st
		}

		if !isValidTick(tick.Ask, st.lastAsk, DefaultSpikeTickDeviation) || !isValidTick(tick.Bid, st.lastBid, DefaultSpikeTickDeviation) {
			broadcastLog(hub, fmt.Sprintf("[DESCARTADO] 🚨 Spike Filter Activado en %s: Variación anómala detectada. Ignorando tick para proteger capital.", key), 0, 0)
			continue
		}

		st.lastAsk = tick.Ask
		st.lastBid = tick.Bid
		when := tick.Time
		if when.IsZero() {
			when = time.Now()
		}
		st.book = TopOfBook{Ask: tick.Ask, Bid: tick.Bid, AskQty: tick.AskQty, BidQty: tick.BidQty, UpdatedAt: when}
		st.hasData = true

		// Radar omnidireccional: el grafo refleja cada tick aceptado (O(1): solo
		// las 2 aristas del libro afectado).
		if e.Graph != nil {
			e.Graph.UpdateBook(key, st.book)
		}

		now := time.Now()
		bin, bit := states[binKey], states[bitKey]
		pairReady := bin != nil && bit != nil && bin.hasData && bit.hasData
		pairFresh := pairReady &&
			now.Sub(bin.book.UpdatedAt) <= MaxBookStaleness &&
			now.Sub(bit.book.UpdatedAt) <= MaxBookStaleness

		var binanceMid, bitsoMid, spread float64
		if pairReady {
			binanceMid = (bin.book.Ask + bin.book.Bid) / 2
			bitsoMid = (bit.book.Ask + bit.book.Bid) / 2
			spread = math.Abs(binanceMid - bitsoMid)
		}

		// Evaluación del PAR (solo cuando el tick pertenece a uno de los dos libros
		// respaldados por wallets — los ticks de ETH no re-evalúan el par).
		isPairTick := key == binKey || key == bitKey
		if isPairTick && pairReady {
			if !pairFresh {
				// Control de staleness: si el feed de un lado lleva demasiado sin
				// publicar, su último precio es un dato muerto — compararlo contra el
				// precio fresco del otro produciría spreads fantasma.
				if now.Sub(lastStaleLog) >= 5*time.Second {
					lastStaleLog = now
					staleSide := binKey
					if now.Sub(bit.book.UpdatedAt) > MaxBookStaleness {
						staleSide = bitKey
					}
					broadcastLog(hub, fmt.Sprintf("🧊 [FEED CONGELADO] El libro %s lleva >%s sin datos — evaluación pausada para no operar contra precios muertos.", staleSide, MaxBookStaleness), 0, 0)
				}
			} else {
				grossSpread1 := bit.book.Bid - bin.book.Ask // comprar Binance → vender Bitso
				grossSpread2 := bin.book.Bid - bit.book.Ask // comprar Bitso → vender Binance
				grossSpread := math.Max(grossSpread1, grossSpread2)

				e.Tracker.Add(spread)

				avg := e.Tracker.Average()
				factor := 0.0
				if avg > 0 {
					factor = spread / avg
				}

				// Vista de mercado de REFERENCIA: se evalúa con los parámetros por
				// defecto (el bucle corre pre-fan-out, sin sesión). La decisión
				// personalizada de cada usuario ocurre en executeForSession.
				baseVolume := DefaultMaxOrderSizeBTC
				var grossOp, fees, slippageOp, netOp float64
				if grossSpread1 > grossSpread2 {
					grossOp, fees, slippageOp, netOp = computeNetProfit(bin.book.Ask, bit.book.Bid, baseVolume, refBinanceFee, refBitsoFee, DefaultSlippageRate)
				} else {
					grossOp, fees, slippageOp, netOp = computeNetProfit(bit.book.Ask, bin.book.Bid, baseVolume, refBitsoFee, refBinanceFee, DefaultSlippageRate)
				}

				isSpiked := false
				switch {
				case factor > SpikeBlockMultiplier:
					broadcastLog(hub, fmt.Sprintf("⚠️  [SPIKE BLOQUEADO] Spread: $%.2f | Promedio: $%.2f | Factor: %.1fx — posible error de API", spread, avg, factor), spread, 0)
					isSpiked = true
				case factor > SpikeWarnMultiplier:
					broadcastLog(hub, fmt.Sprintf("⚡ [SPIKE ALERTA] Spread: $%.2f | Factor: %.1fx — evento extremo, ejecutando", spread, factor), spread, 0)
				default:
					if netOp > DefaultMinNetProfitUSD {
						broadcastLog(hub, fmt.Sprintf("✅ [OPORTUNIDAD] Spread (1 BTC): $%.2f | Vol: %.3f BTC | Bruto: $%.2f | Fees: $%.2f | Slippage: $%.2f | Neto: +$%.2f", grossSpread, baseVolume, grossOp, fees, slippageOp, netOp), grossSpread, netOp)
					} else {
						broadcastLog(hub, fmt.Sprintf("⏳ [EN ESPERA] Spread (1 BTC): $%.2f | Volumen: %.3f BTC | Bruto Op: $%.2f | Fees: $%.2f | Slippage: $%.2f | Neto: $%.2f (Inviable)", grossSpread, baseVolume, grossOp, fees, slippageOp, netOp), grossSpread, netOp)
					}
				}

				if !isSpiked {
					mkt := pairView{
						BinAsk: bin.book.Ask, BinBid: bin.book.Bid,
						BitAsk: bit.book.Ask, BitBid: bit.book.Bid,
						BinAskQty: bin.book.AskQty, BinBidQty: bin.book.BidQty,
						BitAskQty: bit.book.AskQty, BitBidQty: bit.book.BidQty,
					}
					for _, session := range hub.Snapshot() {
						if session.IsInitialized() {
							go e.executeForSession(session, mkt)
						}
					}
				}
			}
		}

		// Bloque de emisión ~1/s: corre con CUALQUIER tick (el radar cubre todos
		// los libros, no solo el par ejecutable).
		if time.Since(lastLogTime) >= time.Second {
			lastLogTime = time.Now()

			// Radar (Fase 2): la vista GLOBAL de referencia se calcula una vez por
			// barrido y alimenta el log compartido del feed.
			if e.Graph != nil {
				refCycle := e.Graph.FindBestCycle(now)
				if refCycle != nil && !radarCycleActive {
					broadcastLog(hub, fmt.Sprintf("📡 [RADAR] Ciclo rentable detectado: %s", DescribeCycle(refCycle)), 0, refCycle.NetReturn)
				}
				radarCycleActive = refCycle != nil
			}

			for _, s := range hub.Snapshot() {
				if pairFresh {
					sendEvent(s, ServerEvent{
						Type:         "market_update",
						BinancePrice: binanceMid,
						BitsoPrice:   bitsoMid,
						Spread:       spread,
					})
				}
				if e.Graph != nil && s.IsInitialized() {
					s.Mu.Lock()
					wallets := s.Wallets.Clone()
					s.Mu.Unlock()

					// Radar PERSONALIZADO (hito 3): el ciclo que ve cada sesión es el
					// de SU subgrafo — sus fees, su slippage y su universo. Dos usuarios
					// con el mismo mercado ven radares distintos; ajustar un slider en
					// el panel cambia este dibujo en el siguiente barrido. Con ≤ 9
					// nodos el Bellman-Ford extra por sesión cuesta microsegundos.
					p := s.Params()
					userCycle := e.Graph.FindBestCycleFor(p, now)
					snap := e.Graph.SnapshotFor(wallets, p, userCycle, now)
					sendEvent(s, ServerEvent{Type: "graph_update", SessionID: s.ID, Graph: snap})

					// AUTOPILOTO DEL RADAR: si el usuario lo activó, ese mismo ciclo
					// (ya calculado para su vista) se ejecuta.
					if p.RadarAutopilot && userCycle != nil {
						go e.executeCycleForSession(s, userCycle, p)
					}
				}
			}
		}
	}
}

// sizeOrder devuelve el volumen ejecutable de una dirección: el TOPE del usuario
// acotado por la liquidez visible en ambas piernas (qty=0 significa "sin dato" y
// no acota). Aquí nace la personalización real: con la misma ineficiencia de
// 0.05 BTC, el usuario con tope 0.01 entra y el que exige bloques de 1.0 la ignora.
func sizeOrder(maxOrder, buySideQty, sellSideQty float64) float64 {
	vol := maxOrder
	if buySideQty > 0 && buySideQty < vol {
		vol = buySideQty
	}
	if sellSideQty > 0 && sellSideQty < vol {
		vol = sellSideQty
	}
	return vol
}

func (e *HFTEngine) executeForSession(session *ClientSession, mkt pairView) {
	// Snapshot de los parámetros de ESTA sesión: vista consistente para toda la
	// función aunque el usuario los edite a mitad de la evaluación (set_params).
	p := session.Params()

	// Con el autopiloto del radar activo, la ejecución la gobierna el ciclo
	// detectado en el subgrafo del usuario (executeCycleForSession): el ejecutor
	// clásico del par se apaga para no operar dos veces la misma oportunidad.
	if p.RadarAutopilot {
		return
	}

	// El UNIVERSO del usuario gobierna también al ejecutor clásico: si deshabilitó
	// cualquiera de los dos venues del par en el panel, el par no opera — el toggle
	// de "exchanges activos" apaga el trading de verdad, no solo el radar.
	for _, venue := range classicPair {
		if !p.venueEnabled(venue) {
			return
		}
	}

	if mkt.BitAsk > mkt.BinAsk*p.MaxDivergenceRatio || mkt.BinAsk > mkt.BitAsk*p.MaxDivergenceRatio {
		sendLog(session, fmt.Sprintf("🛑 [CIRCUIT BREAKER] Divergencia de precio entre venues supera TU límite de cordura (%.0f %%) — oportunidad rechazada.", (p.MaxDivergenceRatio-1)*100))
		return
	}

	session.Mu.Lock()
	// Spike Filter POR SESIÓN: la tolerancia tick-a-tick DEL USUARIO
	// (SpikeTickDeviation) se aplica sobre los mids del par, encima del filtro
	// global de ingesta (que protege el grafo compartido con el default). Los
	// últimos mids se actualizan SIEMPRE —incluso si después corta el cooldown—
	// para que la continuidad de la serie no invente spikes fantasma.
	binMid := (mkt.BinAsk + mkt.BinBid) / 2
	bitMid := (mkt.BitAsk + mkt.BitBid) / 2
	prevBin, prevBit := session.lastBinMid, session.lastBitMid
	session.lastBinMid, session.lastBitMid = binMid, bitMid
	if !isValidTick(binMid, prevBin, p.SpikeTickDeviation) || !isValidTick(bitMid, prevBit, p.SpikeTickDeviation) {
		shouldLog := time.Since(session.lastSpikeLogAt) >= 5*time.Second
		if shouldLog {
			session.lastSpikeLogAt = time.Now()
		}
		session.Mu.Unlock()
		if shouldLog {
			sendLog(session, fmt.Sprintf("🚨 [SPIKE BLOQUEADO] Variación del par supera TU tolerancia (%.1f %%) — tick descartado para proteger capital.", p.SpikeTickDeviation*100))
		}
		return
	}
	if session.IsReplenishing {
		session.Mu.Unlock()
		return
	}
	// Serialización por sesión: el flag IsExecuting se evalúa y se fija bajo el MISMO
	// lock que valida el cooldown. Esto cierra la ventana TOCTOU que permitía a dos
	// goroutines del mismo tick pasar ambas el cooldown y ejecutar dos trades: mientras
	// una ejecución está en curso, los ticks siguientes de esta sesión se descartan.
	if session.IsExecuting {
		session.Mu.Unlock()
		return
	}
	// Pausa activa tras un circuit breaker (Fill-or-Kill fallido): no operar hasta vencer.
	if time.Now().Before(session.PausedUntil) {
		session.Mu.Unlock()
		return
	}
	if time.Since(session.LastTradeTime) < 3*time.Second {
		session.Mu.Unlock()
		return
	}
	session.IsExecuting = true
	session.Mu.Unlock()

	// Liberamos el "candado lógico" pase lo que pase (trade ejecutado, fondos
	// insuficientes, circuit breaker o early-return): garantiza que un fallo no deje
	// la sesión bloqueada para siempre.
	defer func() {
		session.Mu.Lock()
		session.IsExecuting = false
		session.Mu.Unlock()
	}()

	binanceFee := p.takerFee("Binance")
	bitsoFee := p.takerFee("Bitso")

	// Dirección 1: comprar en Binance (ask) → vender en Bitso (bid).
	// El volumen se dimensiona contra la liquidez REAL visible en ambas piernas.
	vol1 := sizeOrder(p.MaxOrderSizeBTC, mkt.BinAskQty, mkt.BitBidQty)
	gross1, fees1, slip1, netProfit1 := computeNetProfit(mkt.BinAsk, mkt.BitBid, vol1, binanceFee, bitsoFee, p.SlippageRate)

	// Dirección 2: comprar en Bitso (ask) → vender en Binance (bid).
	vol2 := sizeOrder(p.MaxOrderSizeBTC, mkt.BitAskQty, mkt.BinBidQty)
	gross2, fees2, slip2, netProfit2 := computeNetProfit(mkt.BitAsk, mkt.BinBid, vol2, bitsoFee, binanceFee, p.SlippageRate)

	viable1 := vol1 >= MinExecutableVolumeBTC && netProfit1 > p.MinNetProfitUSD
	viable2 := vol2 >= MinExecutableVolumeBTC && netProfit2 > p.MinNetProfitUSD

	// Se ejecuta la dirección de mayor NETO (no la de mayor bruto ni la primera).
	var buyEx, sellEx string
	var buyPrice, sellPrice, volume, gross, fees, friction, net float64
	switch {
	case viable1 && (!viable2 || netProfit1 >= netProfit2):
		buyEx, sellEx = "Binance", "Bitso"
		buyPrice, sellPrice, volume, gross, fees, friction, net = mkt.BinAsk, mkt.BitBid, vol1, gross1, fees1, fees1+slip1, netProfit1
	case viable2:
		buyEx, sellEx = "Bitso", "Binance"
		buyPrice, sellPrice, volume, gross, fees, friction, net = mkt.BitAsk, mkt.BinBid, vol2, gross2, fees2, fees2+slip2, netProfit2
	default:
		return
	}

	session.Mu.Lock()
	hasFunds := session.Wallets.Get(buyEx, quoteOf(buyEx)) >= (volume*buyPrice*(1+p.takerFee(buyEx))) &&
		session.Wallets.Get(sellEx, baseOf(sellEx)) >= volume
	session.Mu.Unlock()

	if !hasFunds {
		e.handleLiquidityShortfall(session, net)
		return
	}

	sendLog(session, fmt.Sprintf("✅ [OPORTUNIDAD] Bruto Op: $%.2f | Fees Combinados: $%.2f | Ganancia Neta Limpia: +$%.2f | Ejecutando %.4f BTC...", gross, fees, net, volume))
	e.executeTradeForSession(session, buyEx, sellEx, buyPrice, sellPrice, volume, net, friction)
}

// executeTradeForSession ejecuta el par clásico; feesUSD es la fricción total
// (fees + slippage estimado) ya calculada por computeNetProfit, para el ledger.
func (e *HFTEngine) executeTradeForSession(session *ClientSession, buyEx, sellEx string, buyPrice, sellPrice, volume, netProfit, feesUSD float64) {
	if !e.sessionHasFundsForTrade(session, buyEx, sellEx, volume, buyPrice) {
		sendLog(session, fmt.Sprintf("🛑 [BLOQUEADO] Fondos insuficientes en %s/%s — operación rechazada (sin saldos negativos).", buyEx, sellEx))
		e.handleLiquidityShortfall(session, netProfit)
		return
	}

	p := session.Params()

	// Circuit breaker (Fill-or-Kill): con la probabilidad de fallo DE LA SESIÓN la
	// orden remota no se llena. Como todavía NO hemos tocado ninguna wallet, abortar
	// aquí es atómico (cero exposición direccional: nunca quedamos comprados en una
	// pierna sin vender la otra). Pausamos para no martillar un libro roto.
	if orderFails(p.OrderFailureProb) {
		session.Mu.Lock()
		session.PausedUntil = time.Now().Add(OrderFailurePause)
		session.Mu.Unlock()
		sendLog(session, "🔌 [CIRCUIT BREAKER] Fallo de liquidez en exchange remoto, abortando para evitar exposición direccional")
		return
	}

	session.Mu.Lock()

	session.LastTradeTime = time.Now()

	// Movimiento de saldos genérico por venue: la pierna de compra paga
	// precio × (1 + fee) y la de venta ingresa precio × (1 − fee).
	session.Wallets.Add(buyEx, quoteOf(buyEx), -(buyPrice*volume)*(1+p.takerFee(buyEx)))
	session.Wallets.Add(buyEx, baseOf(buyEx), volume)
	session.Wallets.Add(sellEx, baseOf(sellEx), -volume)
	session.Wallets.Add(sellEx, quoteOf(sellEx), (sellPrice*volume)*(1-p.takerFee(sellEx)))

	session.TotalWealth += netProfit
	session.TotalNetProfit += netProfit

	creditStatus := "INACTIVE"
	if session.Credit.Active {
		creditStatus = "ACTIVE"
	}
	totalWealth := session.TotalWealth
	session.Mu.Unlock()

	sendLog(session, fmt.Sprintf("⚡ [ARBITRAJE] Executed %.4f BTC | Net Profit: +$%.2f USD | Total: $%.2f | Credit: %s", volume, netProfit, totalWealth, creditStatus))
	sendArbExecuted(session, buyEx, sellEx, volume, netProfit)

	e.pushSessionGraph(session)

	recordTradeAsync(TradeRecord{
		SessionID:    session.ID,
		Timestamp:    time.Now(),
		BuyExchange:  buyEx,
		SellExchange: sellEx,
		VolumeBTC:    volume,
		SpreadUSD:    sellPrice - buyPrice,
		FeesUSD:      feesUSD,
		NetProfitUSD: netProfit,
	})
}

func (e *HFTEngine) startReplenishing(session *ClientSession, message string) {
	session.Mu.Lock()
	if session.IsReplenishing || session.Credit.Active {
		session.Mu.Unlock()
		return
	}
	session.IsReplenishing = true
	session.InsufficientFundsPending = false
	session.ReplenishExpiresAt = time.Now().Add(RebalanceDurationMinutes * time.Minute)
	expiresAt := session.ReplenishExpiresAt
	session.Mu.Unlock()

	sendLog(session, fmt.Sprintf("⏸️ [PAUSA] Operaciones detenidas %.0f min (demo). En producción el traslado entre exchanges tarda ~30+ min.", RebalanceDurationMinutes))
	sendEvent(session, ServerEvent{
		Type:               "REPLENISHING_STARTED",
		SessionID:          session.ID,
		Message:            message,
		ReplenishExpiresAt: expiresAt.Format(time.RFC3339),
		IsReplenishing:     true,
	})

	go func() {
		time.Sleep(time.Duration(RebalanceDurationMinutes * float64(time.Minute)))
		e.completeReplenishing(session)
	}()
}

func (e *HFTEngine) completeReplenishing(session *ClientSession) {
	session.Mu.Lock()
	if !session.IsReplenishing {
		session.Mu.Unlock()
		return
	}
	session.Mu.Unlock()

	e.rebalanceWallets50_50(session)

	session.Mu.Lock()
	session.IsReplenishing = false
	session.ReplenishExpiresAt = time.Time{}
	session.InsufficientFundsPending = false
	session.Mu.Unlock()

	sendEvent(session, ServerEvent{
		Type:      "REPLENISHING_COMPLETE",
		SessionID: session.ID,
		Message:   "Inventario reequilibrado. El bot puede volver a operar.",
	})
	sendLog(session, "✅ [LISTO] Reequilibrio completado — operaciones reanudadas.")

	// Si había una inyección del simulador pausada por falta de fondos (el usuario
	// eligió "esperar reequilibrio"), ahora que el inventario está 50/50 se reanuda
	// el consumo de la liquidez restante en vez de dejar la oportunidad abandonada.
	e.resumePendingInjection(session)
}

func (e *HFTEngine) handleLiquidityShortfall(session *ClientSession, projectedProfit float64) {
	session.Mu.Lock()
	if session.IsReplenishing {
		session.Mu.Unlock()
		return
	}
	if session.Credit.Active {
		if !session.Credit.DepletedPending {
			session.Credit.DepletedPending = true
			session.Mu.Unlock()
			sendLog(session, "⚠️ [CRÉDITO AGOTADO] El bot consumió los fondos del préstamo. En pausa hasta que termine el plazo y reequilibre.")
			sendEvent(session, ServerEvent{
				Type:      "CREDIT_DEPLETED",
				SessionID: session.ID,
				Message:   "Se ha agotado el saldo del préstamo. El bot esperará a que termine el plazo para reequilibrar.",
			})
			return
		}
		session.Mu.Unlock()
		return
	}
	autoMode := session.Credit.AutoMode
	session.Mu.Unlock()

	// Inecuación de dominancia del crédito con el apetito de riesgo del usuario:
	// endeudarse solo si la ganancia proyectada supera costo × RiskMultiplier.
	// El costo sale de los TÉRMINOS DE LA SESIÓN (línea, APR, fee, plazo).
	p := session.Params()
	creditCost := calculateCreditCost(p)
	required := creditCost * p.RiskMultiplier

	if autoMode && creditWorthIt(projectedProfit, creditCost, p.RiskMultiplier) {
		sendLog(session, fmt.Sprintf("🏦 [AUTO-CRÉDITO] Ganancia +$%.2f > umbral $%.2f (costo $%.2f × riesgo %.1fx) — activando línea de crédito...", projectedProfit, required, creditCost, p.RiskMultiplier))
		e.activateCreditSession(session, true)
		return
	}

	if autoMode {
		sendLog(session, fmt.Sprintf("⚖️ [REEQUILIBRIO] Ganancia +$%.2f ≤ umbral $%.2f (costo $%.2f × riesgo %.1fx) — pausando operaciones 1 min...", projectedProfit, required, creditCost, p.RiskMultiplier))
		e.startReplenishing(session, "La ganancia no cubre el costo del préstamo. Reequilibrando fondos entre exchanges.")
		return
	}

	session.Mu.Lock()
	alreadySent := session.InsufficientFundsPending
	if !alreadySent {
		session.InsufficientFundsPending = true
	}
	session.Mu.Unlock()

	if alreadySent {
		return
	}

	sendLog(session, "⚠️ [SIN FONDOS] Inventario agotado. Elige: pedir préstamo, esperar reequilibrio (1 min) o detener el bot.")
	sendEvent(session, ServerEvent{
		Type:                 "INSUFFICIENT_FUNDS",
		SessionID:            session.ID,
		RequiresManualAction: true,
		ProfitPotential:      projectedProfit,
		CreditCost:           creditCost,
		CreditRequired:       required,
	})
}

// calculateCreditCost valora el préstamo con los TÉRMINOS DE LA SESIÓN:
// comisión de originación + interés simple prorrateado al plazo en minutos
// sobre la línea en USD. Antes eran constantes; ahora cada usuario define
// cuánto pide, a qué tasa y por cuánto tiempo — y el costo que RiskMultiplier
// multiplica se mueve con ellos.
func calculateCreditCost(p TradingParameters) float64 {
	minuteRate := p.CreditAPR / 365.0 / 24.0 / 60.0
	interest := minuteRate * p.CreditDurationMin * p.CreditLineUSD
	return p.CreditOriginationFee + interest
}

func (e *HFTEngine) activateCreditSession(s *ClientSession, auto bool) {
	// Términos del préstamo de ESTA sesión (línea, APR, fee, plazo): snapshot
	// consistente para toda la activación aunque el usuario edite a mitad.
	p := s.Params()

	s.Mu.Lock()
	if s.Credit.Active {
		s.Mu.Unlock()
		return
	}
	// Guarda defensiva: request_credit antes de init_session no debe tocar wallets
	// inexistentes (el frontend legítimo nunca lo envía, pero el backend no confía).
	if s.Wallets == nil {
		s.Mu.Unlock()
		return
	}

	cryptoExtra := append([]string(nil), s.Credit.PendingCrypto...)
	plan := buildCreditInjection(p, cryptoExtra)
	cost := plan.cost
	venues := enabledVenuesForSession(p)
	if len(venues) == 0 {
		venues = append([]Venue(nil), Venues...)
	}
	n := float64(len(venues))
	if n == 0 {
		s.Mu.Unlock()
		return
	}
	costPerVenue := cost / n

	s.Credit.Borrowed = plan.borrowed.Clone()
	s.Credit.BorrowedUSD = make(map[string]float64, len(venues))
	s.Credit.BorrowedBTC = make(map[string]float64, len(venues))

	for venue, assets := range plan.borrowed {
		for asset, amt := range assets {
			s.Wallets.Add(venue, asset, amt)
			if asset == quoteOf(venue) {
				s.Credit.BorrowedUSD[venue] = amt
			}
			if asset == "BTC" {
				s.Credit.BorrowedBTC[venue] = amt
			}
		}
	}
	// El costo del crédito (fee + interés) se cobra del cash de cada venue —
	// no solo de TotalWealth (si no, el reequilibrio "devolvería" el costo).
	for _, v := range venues {
		s.Wallets.Add(v.Name, v.QuoteAsset, -costPerVenue)
	}

	s.TotalNetProfit -= cost
	s.TotalWealth -= cost
	s.Credit.Active = true
	s.Credit.ActivatedAt = time.Now()
	s.Credit.ExpiresAt = time.Now().Add(time.Duration(p.CreditDurationMin * float64(time.Minute)))
	s.Credit.TotalCostPaid += cost
	s.Credit.ActivationCount++
	s.Credit.DepletedPending = false
	s.Credit.NetProfitAtActivation = s.TotalNetProfit
	s.Credit.LastCost = cost
	s.Credit.PendingCrypto = nil
	s.IsReplenishing = false
	s.InsufficientFundsPending = false

	expiresAt := s.Credit.ExpiresAt
	borrowedSnap := s.Credit.Borrowed.Clone()
	binUSD := s.Credit.BorrowedUSD["Binance"]
	bitUSD := s.Credit.BorrowedUSD["Bitso"]
	binBTC := s.Credit.BorrowedBTC["Binance"]
	bitBTC := s.Credit.BorrowedBTC["Bitso"]
	s.Mu.Unlock()

	extraNote := ""
	if plan.cryptoNote != "" {
		extraNote = " + " + plan.cryptoNote
	}
	sendLog(s, fmt.Sprintf("🏦 [CRÉDITO %s] +$%.0f USD +%.1f BTC%s prestados en %d venues | Costo: $%.2f (APR %.1f %% · fee $%.2f) | Plazo: %.2g min",
		map[bool]string{true: "AUTO-ACTIVADO", false: "ACTIVADO"}[auto],
		p.CreditLineUSD, p.CreditLineBTC, extraNote, len(venues), cost, p.CreditAPR*100, p.CreditOriginationFee, p.CreditDurationMin))

	approvedType := "CREDIT_APPROVED"
	if auto {
		approvedType = "CREDIT_AUTO_APPROVED"
	}
	sendEvent(s, ServerEvent{
		Type:               approvedType,
		SessionID:          s.ID,
		Message:            "Línea de crédito activa — operando con fondos prestados",
		ExpiresAt:          expiresAt.Format(time.RFC3339),
		CreditActive:       true,
		BorrowedBinanceUSD: binUSD,
		BorrowedBitsoUSD:   bitUSD,
		BorrowedBinanceBTC: binBTC,
		BorrowedBitsoBTC:   bitBTC,
		BorrowedBalances:   borrowedSnap,
	})
	sendWalletUpdate(s)

	recordTradeAsync(TradeRecord{
		SessionID:         s.ID,
		Timestamp:         time.Now(),
		BuyExchange:       "Préstamo",
		SellExchange:      "Línea de Crédito",
		VolumeBTC:         p.CreditLineBTC,
		SpreadUSD:         0,
		NetProfitUSD:      -cost,
		IsCreditInjection: true,
	})

	go func() {
		time.Sleep(time.Duration(p.CreditDurationMin * float64(time.Minute)))
		s.Mu.Lock()
		stillActive := s.Credit.Active
		var earnings float64
		var costPaid float64
		if stillActive {
			earnings = s.TotalNetProfit - s.Credit.NetProfitAtActivation
			costPaid = s.Credit.LastCost
		}
		s.Mu.Unlock()
		if stillActive {
			sendLog(s, fmt.Sprintf("⏰ [CRÉDITO VENCIDO] Devolviendo préstamo. Ganancia con préstamo: +$%.2f USD. Intereses: $%.2f USD.", earnings, costPaid))
			e.rebalanceGlobalInventory(s)
			sendEvent(s, ServerEvent{
				Type:         "CREDIT_EXPIRED",
				SessionID:    s.ID,
				Message:      "Préstamo devuelto. Inventario reequilibrado.",
				LoanEarnings: earnings,
				LoanCost:     costPaid,
			})
			sendWalletUpdate(s)
		}
	}()
}

func (e *HFTEngine) executeChunkMirrorSession(session *ClientSession, buyEx, sellEx string, chunkSize, buyPrice, sellPrice, netProfit float64) error {
	if !e.sessionHasFundsForTrade(session, buyEx, sellEx, chunkSize, buyPrice) {
		return fmt.Errorf("fondos insuficientes en %s/%s", buyEx, sellEx)
	}

	p := session.Params()

	session.Mu.Lock()
	defer session.Mu.Unlock()

	session.Wallets.Add(buyEx, quoteOf(buyEx), -buyPrice*chunkSize*(1+p.takerFee(buyEx)))
	session.Wallets.Add(buyEx, baseOf(buyEx), chunkSize)
	session.Wallets.Add(sellEx, baseOf(sellEx), -chunkSize)
	session.Wallets.Add(sellEx, quoteOf(sellEx), sellPrice*chunkSize*(1-p.takerFee(sellEx)))

	session.TotalWealth += netProfit
	session.TotalNetProfit += netProfit
	return nil
}

func (e *HFTEngine) runDemoInjection(session *ClientSession, exchange string, targetSpread, liquidity float64) {
	session.Mu.Lock()
	initialized := session.Wallets != nil && len(session.Wallets) > 0
	replenishing := session.IsReplenishing
	session.Mu.Unlock()

	if !initialized {
		fmt.Printf("⚠️ [DEMO] Sesión %s no inicializada — ignorando inyección\n", session.ID)
		return
	}
	if replenishing {
		sendLog(session, "⏸️ [DEMO] Bot en pausa por reequilibrio — inyección ignorada hasta que termine la espera.")
		return
	}

	// El simulador respeta los parámetros de la sesión (fees y slippage del usuario);
	// el tamaño de chunk es propio del escenario (modela consumo de un bloque de
	// liquidez), independiente del tope de orden del modo en vivo.
	p := session.Params()

	var buyEx, sellEx string
	if targetSpread > 0 {
		sellEx = exchange
		if exchange == "Binance" {
			buyEx = "Bitso"
		} else {
			buyEx = "Binance"
		}
	} else {
		buyEx = exchange
		if exchange == "Binance" {
			sellEx = "Bitso"
		} else {
			sellEx = "Binance"
		}
	}

	absSpread := math.Abs(targetSpread)

	sendLog(session, fmt.Sprintf("🧪 [DEMO] INYECCIÓN ACTIVA: %s Spread: +$%.2f | Liquidez: %.4f BTC", exchange, absSpread, liquidity))

	avg := e.Tracker.Average()
	if avg <= 0 {
		// Sin histórico de spread (p. ej. un exchange sin feed en la nube): usamos un
		// baseline ~0.1% del precio (≈ el spread real Binance↔Bitso), para que el
		// Spike Filter conserve sus umbrales: una oportunidad normal ejecuta, un evento
		// extremo avisa y un precio falso se bloquea, aunque el tracker esté vacío.
		avg = getBTCPrice() * 0.001
	}
	factor := 0.0
	if avg > 0 {
		factor = absSpread / avg
	}

	switch {
	case factor > SpikeBlockMultiplier:
		sendLog(session, fmt.Sprintf("⚠️  [SPIKE BLOQUEADO] Spread: $%.2f | Promedio: $%.2f | Factor: %.1fx — posible error de API", absSpread, avg, factor))
		sendLog(session, "✅ [DEMO] Order book consumido. Inyección completada.")
		return
	case factor > SpikeWarnMultiplier:
		sendLog(session, fmt.Sprintf("⚡ [SPIKE ALERTA] Spread: $%.2f | Factor: %.1fx — evento extremo, ejecutando", absSpread, factor))
	}

	for liquidity > 0 {
		chunkSize := DemoChunkSize
		if liquidity < chunkSize {
			chunkSize = liquidity
		}

		price := getBTCPrice()

		if !e.sessionHasFundsForTrade(session, buyEx, sellEx, chunkSize, price) {
			liquidezRestante := liquidity
			_, _, _, gananciaPotencial := computeNetProfit(price, price+absSpread, liquidezRestante, p.takerFee(buyEx), p.takerFee(sellEx), p.SlippageRate)

			session.Mu.Lock()
			wasReplenishing := session.IsReplenishing
			wasCredit := session.Credit.Active
			session.Mu.Unlock()

			if wasReplenishing || wasCredit {
				break
			}

			e.handleLiquidityShortfall(session, gananciaPotencial)

			session.Mu.Lock()
			nowCredit := session.Credit.Active
			session.Mu.Unlock()

			if nowCredit {
				continue // auto-crédito activado en el acto: se sigue consumiendo aquí mismo
			}
			// La goroutine va a terminar: o el bot pausó para reequilibrar (auto sin
			// margen), o pidió la decisión al usuario (INSUFFICIENT_FUNDS). Guardamos
			// la liquidez restante para REANUDAR esta misma oportunidad cuando haya
			// fondos nuevos (crédito manual o fin del reequilibrio). Sin esto, "Pedir
			// préstamo" agregaba fondos pero no volvía a operar → el préstamo parecía
			// roto y solo cobraba su costo. Ver resumePendingInjection.
			session.Mu.Lock()
			session.PendingInjection = &PendingInjection{Exchange: exchange, Spread: targetSpread, Liquidity: liquidezRestante}
			session.Mu.Unlock()
			break
		}

		sellPrice := price + absSpread
		_, chunkFees, chunkSlip, netProfit := computeNetProfit(price, sellPrice, chunkSize, p.takerFee(buyEx), p.takerFee(sellEx), p.SlippageRate)

		if netProfit <= 0 {
			sendLog(session, fmt.Sprintf("⚠️ [DEMO] Spread insuficiente para chunk de %.4f BTC. Abortando.", chunkSize))
			break
		}

		// Mismo circuit breaker que en el camino real: Fill-or-Kill por chunk con la
		// probabilidad de fallo de la sesión. Aún no se ha movido ninguna wallet, así
		// que abortar es atómico; pausamos y cortamos la inyección.
		if orderFails(p.OrderFailureProb) {
			session.Mu.Lock()
			session.PausedUntil = time.Now().Add(OrderFailurePause)
			session.Mu.Unlock()
			sendLog(session, "🔌 [CIRCUIT BREAKER] Fallo de liquidez en exchange remoto, abortando para evitar exposición direccional")
			break
		}

		if err := e.executeChunkMirrorSession(session, buyEx, sellEx, chunkSize, price, sellPrice, netProfit); err != nil {
			sendLog(session, fmt.Sprintf("❌ [ERROR] %v", err))
			e.handleLiquidityShortfall(session, netProfit)
			break
		}

		liquidity -= chunkSize
		if liquidity < 0.0001 {
			liquidity = 0
		}

		sendLog(session, fmt.Sprintf("⚡ [ARBITRAJE] Executed %.4f BTC | Faltan %.4f BTC en Order Book | Profit: +$%.2f USD",
			chunkSize, liquidity, netProfit))

		sendArbExecuted(session, buyEx, sellEx, chunkSize, netProfit)

		// Write-behind: persistimos cada chunk rentable sin frenar el siguiente.
		recordTradeAsync(TradeRecord{
			SessionID:    session.ID,
			Timestamp:    time.Now(),
			BuyExchange:  buyEx,
			SellExchange: sellEx,
			VolumeBTC:    chunkSize,
			SpreadUSD:    absSpread,
			FeesUSD:      chunkFees + chunkSlip,
			NetProfitUSD: netProfit,
		})

		time.Sleep(DemoOrderLatency)
	}

	// Solo declaramos "completada" en la salida NATURAL del loop (liquidity==0).
	// Los demás cortes (sin fondos, reequilibrio, spread insuficiente, circuit
	// breaker) ya emiten su propio log; antes este mensaje verde salía SIEMPRE, y
	// aparecía "Inyección completada" al mismo tiempo que el diálogo de fondos
	// insuficientes — feedback contradictorio.
	if liquidity <= 0 {
		sendLog(session, "✅ [DEMO] Order book consumido. Inyección completada.")
	}
}

// resumePendingInjection reanuda una inyección del simulador que se pausó por
// falta de fondos, una vez que la sesión consiguió liquidez (crédito manual o fin
// del reequilibrio). Toma-y-limpia PendingInjection de forma atómica, así que si
// crédito y reequilibrio compitieran, solo uno reanuda (nunca doble ejecución).
// Sin esto, "Pedir préstamo" / "Esperar reequilibrio" añadían fondos pero no
// volvían a operar la oportunidad pendiente — el fuerte de Arus (no perder
// oportunidades) quedaba roto en el camino manual.
func (e *HFTEngine) resumePendingInjection(session *ClientSession) {
	session.Mu.Lock()
	pending := session.PendingInjection
	session.PendingInjection = nil
	session.Mu.Unlock()
	if pending == nil {
		return
	}
	sendLog(session, fmt.Sprintf("▶️ [DEMO] Reanudando la oportunidad pendiente con los fondos nuevos: %s · %.4f BTC restantes.", pending.Exchange, pending.Liquidity))
	e.runDemoInjection(session, pending.Exchange, pending.Spread, pending.Liquidity)
}
