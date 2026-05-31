package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const (
	SpikeWarnMultiplier  = 15.0
	SpikeBlockMultiplier = 50.0

	// SlippageRate modela el deslizamiento de precio (slippage) estimado por pierna.
	// En un libro de órdenes real, una orden de mercado no se llena por completo en el
	// top-of-book: consume varios niveles, empeorando el precio promedio de ejecución.
	// Lo estimamos como un % (puntos base) sobre el notional ejecutado en CADA exchange.
	SlippageRate = 0.0005 // 5 bps por pierna
)

// estimateSlippage devuelve el costo estimado de slippage para una operación que
// compra `volume` BTC a buyPrice y vende `volume` BTC a sellPrice. Se descuenta del
// neto ANTES de decidir, para no ejecutar oportunidades que son rentables en bruto
// pero que el deslizamiento vuelve negativas.
func estimateSlippage(buyPrice, sellPrice, volume float64) float64 {
	return (buyPrice + sellPrice) * volume * SlippageRate
}

type SpreadTracker struct {
	recent     []float64
	maxSamples int
}

func NewSpreadTracker() *SpreadTracker {
	return &SpreadTracker{maxSamples: 20}
}

func (s *SpreadTracker) Add(spread float64) {
	s.recent = append(s.recent, spread)
	if len(s.recent) > s.maxSamples {
		s.recent = s.recent[1:]
	}
}

func (s *SpreadTracker) Average() float64 {
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
}

func getLevel(msg string) string {
	if strings.Contains(msg, "[OPORTUNIDAD]") { return "opportunity" }
	if strings.Contains(msg, "[ARBITRAJE]") { return "arb" }
	if strings.Contains(msg, "SPIKE ALERTA") { return "spike_warn" }
	if strings.Contains(msg, "SPIKE BLOQUEADO") { return "spike_block" }
	if strings.Contains(msg, "[EN ESPERA]") { return "waiting" }
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

func isValidTick(newPrice, lastPrice float64) bool {
	if lastPrice == 0 {
		return true
	}
	deviation := math.Abs(newPrice-lastPrice) / lastPrice
	return deviation <= 0.05
}

func getBTCPrice() float64 {
	btcPrice := 60000.0
	currentMarket.mu.Lock()
	if currentMarket.BinanceAsk > 0 {
		btcPrice = currentMarket.BinanceAsk
	}
	currentMarket.mu.Unlock()
	return btcPrice
}

func clampWallet(w *Wallet) {
	if w.USD < 0 && w.USD > -1e-8 {
		w.USD = 0
	}
	if w.BTC < 0 && w.BTC > -1e-8 {
		w.BTC = 0
	}
}

func (e *HFTEngine) sessionHasFundsForTrade(session *ClientSession, buyEx, sellEx string, volume, buyPrice float64) bool {
	binanceTakerFee := 0.001
	bitsoTakerFee := 0.0065

	var requiredUSD float64
	if buyEx == "Binance" {
		requiredUSD = buyPrice * volume * (1 + binanceTakerFee)
	} else {
		requiredUSD = buyPrice * volume * (1 + bitsoTakerFee)
	}

	session.Mu.Lock()
	defer session.Mu.Unlock()
	return session.Wallets[buyEx].USD >= requiredUSD && session.Wallets[sellEx].BTC >= volume
}

// adjustFunds permite al usuario depositar (amount > 0) o retirar (amount < 0) USD o
// BTC de un exchange concreto. Para que un depósito/retiro NO se contabilice como
// ganancia o pérdida del bot, ajustamos por igual el patrimonio total y la base
// inicial (initial), dejando el PnL ("Ganancia Neta") intacto en el momento del ajuste.
func (e *HFTEngine) adjustFunds(session *ClientSession, exchange, currency string, amount float64) {
	if amount == 0 {
		return
	}

	session.Mu.Lock()
	if session.Wallets == nil {
		session.Mu.Unlock()
		return
	}
	w, ok := session.Wallets[exchange]
	if !ok {
		session.Mu.Unlock()
		return
	}

	btcPrice := getBTCPrice()
	applied := amount

	switch strings.ToUpper(currency) {
	case "USD":
		if amount < 0 && w.USD+amount < 0 {
			applied = -w.USD // no permitir saldo negativo: retira como máximo lo disponible
		}
		w.USD += applied
		session.TotalWealth += applied
		session.InitialWealth += applied // base sube/baja igual → no se contabiliza como PnL
		session.InitialUSD += applied
	case "BTC":
		if amount < 0 && w.BTC+amount < 0 {
			applied = -w.BTC
		}
		w.BTC += applied
		session.TotalWealth += applied * btcPrice
		session.InitialWealth += applied * btcPrice
		session.InitialBTC += applied
	default:
		session.Mu.Unlock()
		return
	}
	clampWallet(w)
	session.Mu.Unlock()

	action := "Depósito"
	if applied < 0 {
		action = "Retiro"
	}
	if strings.ToUpper(currency) == "BTC" {
		sendLog(session, fmt.Sprintf("🏦 [FONDOS] %s en %s: %.6f BTC", action, exchange, math.Abs(applied)))
	} else {
		sendLog(session, fmt.Sprintf("🏦 [FONDOS] %s en %s: $%.2f USD", action, exchange, math.Abs(applied)))
	}
	sendWalletUpdate(session)
}

func sendWalletUpdate(s *ClientSession) {
	s.Mu.Lock()
	ev := ServerEvent{
		Type:           "wallet_update",
		SessionID:      s.ID,
		BinanceUSD:     s.Wallets["Binance"].USD,
		BinanceBTC:     s.Wallets["Binance"].BTC,
		BitsoUSD:       s.Wallets["Bitso"].USD,
		BitsoBTC:       s.Wallets["Bitso"].BTC,
		TotalWealth:    s.TotalWealth,
		TotalNetProfit: s.TotalNetProfit,
		IsReplenishing: s.IsReplenishing,
		CreditActive:   s.Credit.Active,
		AutoMode:       s.Credit.AutoMode,
	}
	if s.Credit.Active && s.Credit.BorrowedUSD != nil {
		ev.BorrowedBinanceUSD = s.Credit.BorrowedUSD["Binance"]
		ev.BorrowedBitsoUSD = s.Credit.BorrowedUSD["Bitso"]
		ev.BorrowedBinanceBTC = s.Credit.BorrowedBTC["Binance"]
		ev.BorrowedBitsoBTC = s.Credit.BorrowedBTC["Bitso"]
	}
	if s.Credit.Active && !s.Credit.ExpiresAt.IsZero() {
		ev.ExpiresAt = s.Credit.ExpiresAt.Format(time.RFC3339)
	}
	if s.IsReplenishing && !s.ReplenishExpiresAt.IsZero() {
		ev.ReplenishExpiresAt = s.ReplenishExpiresAt.Format(time.RFC3339)
	}
	s.Mu.Unlock()
	sendEvent(s, ev)
}

func (e *HFTEngine) Start(priceChan <-chan PriceTick, hub *Hub) {
	var lastBinanceAsk, lastBinanceBid float64
	var lastBitsoAsk, lastBitsoBid float64
	lastLogTime := time.Now()

	for tick := range priceChan {
		var currentLastAsk, currentLastBid float64
		if tick.Exchange == "Binance" {
			currentLastAsk = lastBinanceAsk
			currentLastBid = lastBinanceBid
		} else {
			currentLastAsk = lastBitsoAsk
			currentLastBid = lastBitsoBid
		}

		if !isValidTick(tick.Ask, currentLastAsk) || !isValidTick(tick.Bid, currentLastBid) {
			broadcastLog(hub, "[DESCARTADO] 🚨 Spike Filter Activado: Variación anómala detectada. Ignorando tick para proteger capital.", 0, 0)
			continue
		}

		if tick.Exchange == "Binance" {
			lastBinanceAsk = tick.Ask
			lastBinanceBid = tick.Bid
		} else if tick.Exchange == "Bitso" {
			lastBitsoAsk = tick.Ask
			lastBitsoBid = tick.Bid
		}

		effectiveBinanceAsk := lastBinanceAsk
		effectiveBinanceBid := lastBinanceBid
		effectiveBitsoAsk := lastBitsoAsk
		effectiveBitsoBid := lastBitsoBid

		if effectiveBinanceAsk > 0 && lastBitsoAsk > 0 {
			binanceMid := (effectiveBinanceAsk + effectiveBinanceBid) / 2
			bitsoMid := (effectiveBitsoAsk + effectiveBitsoBid) / 2
			
			grossSpread1 := effectiveBitsoBid - effectiveBinanceAsk
			grossSpread2 := effectiveBinanceBid - effectiveBitsoAsk
			grossSpread := math.Max(grossSpread1, grossSpread2)
			spread := math.Abs(binanceMid - bitsoMid)
			
			e.Tracker.Add(spread)
			
			avg := e.Tracker.Average()
			factor := 0.0
			if avg > 0 {
				factor = spread / avg
			}

			baseVolume := 0.005
			var fees float64
			if grossSpread1 > grossSpread2 {
				fees = (effectiveBinanceAsk*0.001 + effectiveBitsoBid*0.0065) * baseVolume
			} else {
				fees = (effectiveBitsoAsk*0.0065 + effectiveBinanceBid*0.001) * baseVolume
			}
			grossOp := grossSpread * baseVolume
			slippageOp := estimateSlippage(binanceMid, bitsoMid, baseVolume)
			netOp := grossOp - fees - slippageOp

			isSpiked := false
			switch {
			case factor > SpikeBlockMultiplier:
				broadcastLog(hub, fmt.Sprintf("⚠️  [SPIKE BLOQUEADO] Spread: $%.2f | Promedio: $%.2f | Factor: %.1fx — posible error de API", spread, avg, factor), spread, 0)
				isSpiked = true
			case factor > SpikeWarnMultiplier:
				broadcastLog(hub, fmt.Sprintf("⚡ [SPIKE ALERTA] Spread: $%.2f | Factor: %.1fx — evento extremo, ejecutando", spread, factor), spread, 0)
			default:
				if netOp > 0.10 {
					broadcastLog(hub, fmt.Sprintf("✅ [OPORTUNIDAD] Spread (1 BTC): $%.2f | Vol: %.3f BTC | Bruto: $%.2f | Fees: $%.2f | Slippage: $%.2f | Neto: +$%.2f", grossSpread, baseVolume, grossOp, fees, slippageOp, netOp), grossSpread, netOp)
				} else {
					broadcastLog(hub, fmt.Sprintf("⏳ [EN ESPERA] Spread (1 BTC): $%.2f | Volumen: %.3f BTC | Bruto Op: $%.2f | Fees: $%.2f | Slippage: $%.2f | Neto: $%.2f (Inviable)", grossSpread, baseVolume, grossOp, fees, slippageOp, netOp), grossSpread, netOp)
				}
			}

			if isSpiked {
				continue
			}

			if time.Since(lastLogTime) >= time.Second {
				lastLogTime = time.Now()
				for _, s := range hub.Snapshot() {
					sendEvent(s, ServerEvent{
						Type:         "market_update",
						BinancePrice: binanceMid,
						BitsoPrice:   bitsoMid,
						Spread:       spread,
					})
				}
			}

			for _, session := range hub.Snapshot() {
				if session.IsInitialized() {
					go e.executeForSession(session, effectiveBinanceAsk, effectiveBinanceBid, effectiveBitsoAsk, effectiveBitsoBid)
				}
			}
		}
	}
}

func (e *HFTEngine) executeForSession(session *ClientSession, binAsk, binBid, bitAsk, bitBid float64) {
	orderSize := 0.005 

	if bitAsk > binAsk*1.20 || binAsk > bitAsk*1.20 {
		return
	}

	session.Mu.Lock()
	if session.IsReplenishing {
		session.Mu.Unlock()
		return
	}
	if time.Since(session.LastTradeTime) < 3*time.Second {
		session.Mu.Unlock()
		return
	}

	session.Mu.Unlock()

	binanceTakerFee := 0.001
	bitsoTakerFee := 0.0065

	grossSpread1 := bitBid - binAsk
	feeEstimate1 := (binAsk*binanceTakerFee + bitBid*bitsoTakerFee) * orderSize
	slippage1 := estimateSlippage(binAsk, bitBid, orderSize)
	netProfit1 := (grossSpread1 * orderSize) - feeEstimate1 - slippage1

	grossSpread2 := binBid - bitAsk
	feeEstimate2 := (bitAsk*bitsoTakerFee + binBid*binanceTakerFee) * orderSize
	slippage2 := estimateSlippage(bitAsk, binBid, orderSize)
	netProfit2 := (grossSpread2 * orderSize) - feeEstimate2 - slippage2

	var bestProfit float64
	var needBinanceUSD, needBitsoBTC, needBitsoUSD, needBinanceBTC bool
	
	if grossSpread1 > grossSpread2 && netProfit1 > 0.10 {
		bestProfit = netProfit1
		needBinanceUSD = true
		needBitsoBTC = true
	} else if grossSpread2 > grossSpread1 && netProfit2 > 0.10 {
		bestProfit = netProfit2
		needBitsoUSD = true
		needBinanceBTC = true
	} else {
		return
	}

	session.Mu.Lock()
	hasFunds := true
	if needBinanceUSD && session.Wallets["Binance"].USD < (orderSize * binAsk * (1 + binanceTakerFee)) { hasFunds = false }
	if needBitsoBTC && session.Wallets["Bitso"].BTC < orderSize { hasFunds = false }
	if needBitsoUSD && session.Wallets["Bitso"].USD < (orderSize * bitAsk * (1 + bitsoTakerFee)) { hasFunds = false }
	if needBinanceBTC && session.Wallets["Binance"].BTC < orderSize { hasFunds = false }
	
	if !hasFunds {
		session.Mu.Unlock()
		e.handleLiquidityShortfall(session, bestProfit)
		return
	}

	if needBinanceUSD {
		sendLog(session, fmt.Sprintf("✅ [OPORTUNIDAD] Bruto Op: $%.2f | Fees Combinados: $%.2f | Ganancia Neta Limpia: +$%.2f | Ejecutando %.3f BTC...", grossSpread1*orderSize, feeEstimate1, netProfit1, orderSize))
		session.Mu.Unlock()
		e.executeTradeForSession(session, "Binance", "Bitso", binAsk, bitBid, orderSize, netProfit1)
	} else {
		sendLog(session, fmt.Sprintf("✅ [OPORTUNIDAD] Bruto Op: $%.2f | Fees Combinados: $%.2f | Ganancia Neta Limpia: +$%.2f | Ejecutando %.3f BTC...", grossSpread2*orderSize, feeEstimate2, netProfit2, orderSize))
		session.Mu.Unlock()
		e.executeTradeForSession(session, "Bitso", "Binance", bitAsk, binBid, orderSize, netProfit2)
	}
}

func (e *HFTEngine) executeTradeForSession(session *ClientSession, buyEx, sellEx string, buyPrice, sellPrice, volume, netProfit float64) {
	if !e.sessionHasFundsForTrade(session, buyEx, sellEx, volume, buyPrice) {
		sendLog(session, fmt.Sprintf("🛑 [BLOQUEADO] Fondos insuficientes en %s/%s — operación rechazada (sin saldos negativos).", buyEx, sellEx))
		e.handleLiquidityShortfall(session, netProfit)
		return
	}

	session.Mu.Lock()

	session.LastTradeTime = time.Now()

	binanceTakerFee := 0.001
	bitsoTakerFee := 0.0065

	if buyEx == "Binance" {
		session.Wallets["Binance"].USD -= (buyPrice * volume) * (1 + binanceTakerFee)
		session.Wallets["Binance"].BTC += volume
		session.Wallets["Bitso"].BTC -= volume
		session.Wallets["Bitso"].USD += (sellPrice * volume) * (1 - bitsoTakerFee)
	} else {
		session.Wallets["Bitso"].USD -= (buyPrice * volume) * (1 + bitsoTakerFee)
		session.Wallets["Bitso"].BTC += volume
		session.Wallets["Binance"].BTC -= volume
		session.Wallets["Binance"].USD += (sellPrice * volume) * (1 - binanceTakerFee)
	}

	clampWallet(session.Wallets["Binance"])
	clampWallet(session.Wallets["Bitso"])

	session.TotalWealth += netProfit
	session.TotalNetProfit += netProfit

	creditStatus := "INACTIVE"
	if session.Credit.Active {
		creditStatus = "ACTIVE"
	}

	sendLog(session, fmt.Sprintf("⚡ [ARBITRAJE] Executed %.3f BTC | Net Profit: +$%.2f USD | Total: $%.2f | Credit: %s", volume, netProfit, session.TotalWealth, creditStatus))

	payload := map[string]interface{}{
		"event":          "arbitrage_executed",
		"exchange_buy":   buyEx,
		"exchange_sell":  sellEx,
		"buy_exchange":   buyEx,
		"sell_exchange":  sellEx,
		"volume":         volume,
		"net_profit_usd": netProfit,
		"net_profit":     netProfit,
		"new_total_usd":  session.TotalWealth,
		"timestamp":      time.Now().Format("15:04:05.000"),
		"credit_active":  session.Credit.Active,
		"binance_usd":    session.Wallets["Binance"].USD,
		"binance_btc":    session.Wallets["Binance"].BTC,
		"bitso_usd":      session.Wallets["Bitso"].USD,
		"bitso_btc":      session.Wallets["Bitso"].BTC,
	}
	session.Mu.Unlock()

	b, _ := json.Marshal(payload)
	session.WriteMessage(websocket.TextMessage, b)
	sendWalletUpdate(session)

	recordTradeAsync(TradeRecord{
		SessionID:    session.ID,
		Timestamp:    time.Now(),
		BuyExchange:  buyEx,
		SellExchange: sellEx,
		VolumeBTC:    volume,
		SpreadUSD:    sellPrice - buyPrice,
		NetProfitUSD: netProfit,
	})
}

func (e *HFTEngine) rebalanceWallets50_50(session *ClientSession) {
	session.Mu.Lock()

	if session.Credit.Active && session.Credit.BorrowedUSD != nil {
		for ex, amt := range session.Credit.BorrowedUSD {
			session.Wallets[ex].USD -= amt
		}
		for ex, amt := range session.Credit.BorrowedBTC {
			session.Wallets[ex].BTC -= amt
		}
		session.Credit.Active = false
		session.Credit.BorrowedUSD = nil
		session.Credit.BorrowedBTC = nil
	}

	btcPrice := getBTCPrice()
	totalUSD := session.Wallets["Binance"].USD + session.Wallets["Bitso"].USD
	totalBTC := session.Wallets["Binance"].BTC + session.Wallets["Bitso"].BTC
	totalWealthUSD := totalUSD + (totalBTC * btcPrice)

	targetUSD := totalWealthUSD / 4.0
	targetBTC := (totalWealthUSD / 4.0) / btcPrice

	session.Wallets["Binance"] = &Wallet{USD: targetUSD, BTC: targetBTC}
	session.Wallets["Bitso"] = &Wallet{USD: targetUSD, BTC: targetBTC}
	session.TotalWealth = totalWealthUSD
	session.Mu.Unlock()

	sendLog(session, fmt.Sprintf("🔄 [REEQUILIBRIO] Fondos repartidos 50/50. Riqueza total: $%.2f USD", totalWealthUSD))

	payload := map[string]interface{}{
		"event":            "market_rebalanced",
		"total_wealth_usd": totalWealthUSD,
		"target_usd":       targetUSD,
		"target_btc":       targetBTC,
		"binance_usd":      targetUSD,
		"binance_btc":      targetBTC,
		"bitso_usd":        targetUSD,
		"bitso_btc":        targetBTC,
	}
	b, _ := json.Marshal(payload)
	session.WriteMessage(websocket.TextMessage, b)
	sendWalletUpdate(session)

	recordTradeAsync(TradeRecord{
		SessionID:         session.ID,
		Timestamp:         time.Now(),
		BuyExchange:       "Sistema",
		SellExchange:      "Rebalanceo",
		VolumeBTC:         totalBTC,
		SpreadUSD:         0,
		NetProfitUSD:      0,
		IsCreditInjection: true,
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

	creditCost := calculateCreditCost(CreditLineUSD)

	if autoMode && projectedProfit > creditCost {
		sendLog(session, fmt.Sprintf("🏦 [AUTO-CRÉDITO] Ganancia +$%.2f > costo préstamo $%.2f — activando línea de crédito...", projectedProfit, creditCost))
		e.activateCreditSession(session)
		return
	}

	if autoMode {
		sendLog(session, fmt.Sprintf("⚖️ [REEQUILIBRIO] Ganancia +$%.2f ≤ costo préstamo $%.2f — pausando operaciones 1 min...", projectedProfit, creditCost))
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
	})
}

// ExecuteRebalanceForSession queda como alias interno del reequilibrio instantáneo
// (solo se invoca al completar la pausa de 1 min o al vencer el crédito).
func (e *HFTEngine) ExecuteRebalanceForSession(session *ClientSession, _ bool) {
	e.rebalanceWallets50_50(session)
}

func (e *HFTEngine) hasInventoryForChunkSession(session *ClientSession, buyEx, sellEx string, chunkSize, price float64) bool {
	return e.sessionHasFundsForTrade(session, buyEx, sellEx, chunkSize, price)
}

func calculateCreditCost(usdBorrowed float64) float64 {
	minuteRate := CreditAPR / 365.0 / 24.0 / 60.0
	interest := minuteRate * CreditDurationMinutes * usdBorrowed
	return CreditOriginationFee + interest
}

func (e *HFTEngine) activateCreditSession(s *ClientSession) {
	s.Mu.Lock()
	if s.Credit.Active {
		s.Mu.Unlock()
		return
	}
	cost := calculateCreditCost(CreditLineUSD)

	halfUSD := CreditLineUSD / 2
	halfBTC := CreditLineBTC / 2

	s.Wallets["Binance"].USD += halfUSD
	s.Wallets["Bitso"].USD += halfUSD
	s.Wallets["Binance"].BTC += halfBTC
	s.Wallets["Bitso"].BTC += halfBTC

	s.Credit.BorrowedUSD = map[string]float64{"Binance": halfUSD, "Bitso": halfUSD}
	s.Credit.BorrowedBTC = map[string]float64{"Binance": halfBTC, "Bitso": halfBTC}

	s.TotalNetProfit -= cost
	s.TotalWealth -= cost
	s.Credit.Active = true
	s.Credit.ActivatedAt = time.Now()
	s.Credit.ExpiresAt = time.Now().Add(CreditDurationMinutes * time.Minute)
	s.Credit.TotalCostPaid += cost
	s.Credit.ActivationCount++
	s.Credit.DepletedPending = false
	s.Credit.NetProfitAtActivation = s.TotalNetProfit
	s.Credit.LastCost = cost
	s.IsReplenishing = false
	s.InsufficientFundsPending = false

	expiresAt := s.Credit.ExpiresAt
	s.Mu.Unlock()

	sendLog(s, fmt.Sprintf("🏦 [CRÉDITO ACTIVADO] +$%.0f USD +%.1f BTC prestados | Costo: $%.2f | Operando con crédito mientras se reequilibran fondos (%.0f min demo)",
		CreditLineUSD, CreditLineBTC, cost, CreditDurationMinutes))

	sendEvent(s, ServerEvent{
		Type:               "CREDIT_APPROVED",
		SessionID:          s.ID,
		Message:            "Línea de crédito activa — operando con fondos prestados",
		ExpiresAt:          expiresAt.Format(time.RFC3339),
		CreditActive:       true,
		BorrowedBinanceUSD: halfUSD,
		BorrowedBitsoUSD:   halfUSD,
		BorrowedBinanceBTC: halfBTC,
		BorrowedBitsoBTC:   halfBTC,
	})
	sendWalletUpdate(s)

	recordTradeAsync(TradeRecord{
		SessionID:         s.ID,
		Timestamp:         time.Now(),
		BuyExchange:       "Préstamo",
		SellExchange:      "Línea de Crédito",
		VolumeBTC:         CreditLineBTC,
		SpreadUSD:         0,
		NetProfitUSD:      -cost,
		IsCreditInjection: true,
	})

	go func() {
		time.Sleep(time.Duration(CreditDurationMinutes * float64(time.Minute)))
		s.Mu.Lock()
		stillActive := s.Credit.Active
		var earnings float64
		var cost float64
		if stillActive {
			earnings = s.TotalNetProfit - s.Credit.NetProfitAtActivation
			cost = s.Credit.LastCost
		}
		s.Mu.Unlock()
		if stillActive {
			sendLog(s, fmt.Sprintf("⏰ [CRÉDITO VENCIDO] Devolviendo préstamo. Ganancia con préstamo: +$%.2f USD. Intereses: $%.2f USD.", earnings, cost))
			e.rebalanceWallets50_50(s)
			sendEvent(s, ServerEvent{
				Type:         "CREDIT_EXPIRED",
				SessionID:    s.ID,
				Message:      "Préstamo devuelto. Inventario reequilibrado.",
				LoanEarnings: earnings,
				LoanCost:     cost,
			})
			sendWalletUpdate(s)
		}
	}()
}

func (e *HFTEngine) calculateFeesSession(buyEx, sellEx string, chunkSize, price float64) float64 {
	binanceTakerFee := 0.001
	bitsoTakerFee := 0.0065

	var feeEstimate float64
	if buyEx == "Binance" {
		feeEstimate += price * binanceTakerFee * chunkSize
	} else {
		feeEstimate += price * bitsoTakerFee * chunkSize
	}

	if sellEx == "Binance" {
		feeEstimate += price * binanceTakerFee * chunkSize
	} else {
		feeEstimate += price * bitsoTakerFee * chunkSize
	}

	return feeEstimate
}

func (e *HFTEngine) executeChunkMirrorSession(session *ClientSession, buyEx, sellEx string, chunkSize, buyPrice, sellPrice, netProfit float64) error {
	if !e.sessionHasFundsForTrade(session, buyEx, sellEx, chunkSize, buyPrice) {
		return fmt.Errorf("fondos insuficientes en %s/%s", buyEx, sellEx)
	}

	session.Mu.Lock()
	defer session.Mu.Unlock()

	binanceTakerFee := 0.001
	bitsoTakerFee := 0.0065

	if buyEx == "Binance" {
		session.Wallets["Binance"].USD -= buyPrice * chunkSize * (1 + binanceTakerFee)
		session.Wallets["Binance"].BTC += chunkSize
		session.Wallets["Bitso"].BTC -= chunkSize
		session.Wallets["Bitso"].USD += sellPrice * chunkSize * (1 - bitsoTakerFee)
	} else {
		session.Wallets["Bitso"].USD -= buyPrice * chunkSize * (1 + bitsoTakerFee)
		session.Wallets["Bitso"].BTC += chunkSize
		session.Wallets["Binance"].BTC -= chunkSize
		session.Wallets["Binance"].USD += sellPrice * chunkSize * (1 - binanceTakerFee)
	}

	clampWallet(session.Wallets["Binance"])
	clampWallet(session.Wallets["Bitso"])

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

		currentMarket.mu.Lock()
		price := currentMarket.BinanceAsk
		if price == 0 {
			price = 60000.0 // fallback
		}
		currentMarket.mu.Unlock()

		if !e.hasInventoryForChunkSession(session, buyEx, sellEx, chunkSize, price) {
			liquidezRestante := liquidity
			gananciaPotencial := absSpread*liquidezRestante*(1-0.0075) - estimateSlippage(price, price+absSpread, liquidezRestante)

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
				continue
			}
			break
		}

		sellPrice := price + absSpread
		grossProfit := absSpread * chunkSize
		fees := e.calculateFeesSession(buyEx, sellEx, chunkSize, price)
		slippage := estimateSlippage(price, sellPrice, chunkSize)
		netProfit := grossProfit - fees - slippage

		if netProfit <= 0 {
			sendLog(session, fmt.Sprintf("⚠️ [DEMO] Spread insuficiente para chunk de %.4f BTC. Abortando.", chunkSize))
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

		payload := map[string]interface{}{
			"event":          "arbitrage_executed",
			"exchange_buy":   buyEx,
			"exchange_sell":  sellEx,
			"buy_exchange":   buyEx,
			"sell_exchange":  sellEx,
			"volume":         chunkSize,
			"net_profit_usd": netProfit,
			"net_profit":     netProfit,
			"timestamp":      time.Now().Format("15:04:05.000"),
		}
		session.Mu.Lock()
		payload["new_total_usd"] = session.TotalWealth
		payload["credit_active"] = session.Credit.Active
		payload["binance_usd"] = session.Wallets["Binance"].USD
		payload["binance_btc"] = session.Wallets["Binance"].BTC
		payload["bitso_usd"] = session.Wallets["Bitso"].USD
		payload["bitso_btc"] = session.Wallets["Bitso"].BTC
		session.Mu.Unlock()

		b, _ := json.Marshal(payload)
		session.WriteMessage(websocket.TextMessage, b)
		sendWalletUpdate(session)

		// Write-behind: persistimos cada chunk rentable sin frenar el siguiente.
		recordTradeAsync(TradeRecord{
			SessionID:    session.ID,
			Timestamp:    time.Now(),
			BuyExchange:  buyEx,
			SellExchange: sellEx,
			VolumeBTC:    chunkSize,
			SpreadUSD:    absSpread,
			NetProfitUSD: netProfit,
		})

		time.Sleep(DemoOrderLatency)
	}

	sendLog(session, "✅ [DEMO] Order book consumido. Inyección completada.")
}
