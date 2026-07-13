package main

import (
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestStressHighVolatility_SpikeRejection inyecta 100 ticks concurrentes en ~1 s
// con un spread irreal (~$5 000 USD). El motor debe procesarlos sin panic ni data
// race (SpreadTracker + sesión bajo Mutex) y el Spike Filter debe bloquear la anomalía.
func TestStressHighVolatility_SpikeRejection(t *testing.T) {
	e, _, priceChan, s := stressEngineFixture(t)

	// Baseline de mercado normal (~$50 de spread) para poblar el tracker.
	for i := 0; i < 15; i++ {
		e.Tracker.Add(50 + float64(i%3))
	}

	binInstr, _ := primaryInstrument("Binance")
	bitInstr, _ := primaryInstrument("Bitso")
	base := 60_000.0

	// Establecer estado de ingesta con ticks normales antes del burst.
	publishTick(priceChan, binInstr, base, base-10, 1, 1)
	publishTick(priceChan, bitInstr, base+20, base+10, 1, 1)
	time.Sleep(50 * time.Millisecond)

	var wg sync.WaitGroup
	var panicked atomic.Bool
	start := time.Now()

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					panicked.Store(true)
					t.Errorf("panic en goroutine %d: %v", idx, r)
				}
			}()
			// Spread masivo: Bitso bid $5 000 por encima de Binance ask (~$5 000 de ganancia bruta).
			spikeBid := base + 5_000 + float64(idx%5)
			publishTick(priceChan, binInstr, base, base-10, 1, 1)
			publishTick(priceChan, bitInstr, spikeBid+10, spikeBid, 1, 1)
		}(i)
		// Distribuir ~100 ticks en ~1 s.
		time.Sleep(10 * time.Millisecond)
	}
	wg.Wait()

	if panicked.Load() {
		t.Fatal("el motor hizo panic bajo carga concurrente")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Logf("burst completado en %v (objetivo ~1 s)", elapsed)
	}

	// El Spike Filter debe haber bloqueado: ningún arbitraje ejecutado.
	waitUntil(t, 3*time.Second, func() bool {
		s.Mu.Lock()
		defer s.Mu.Unlock()
		return !s.IsExecuting
	})

	s.Mu.Lock()
	net := s.TotalNetProfit
	executing := s.IsExecuting
	s.Mu.Unlock()

	if net != 0 {
		t.Fatalf("se ejecutó trade pese al spike: PnL=%.2f", net)
	}
	if executing {
		t.Fatal("IsExecuting quedó bloqueado tras el burst")
	}

	avg := e.Tracker.Average()
	if avg <= 0 {
		t.Fatal("SpreadTracker corrupto tras acceso concurrente")
	}
	factor := 5_000.0 / avg
	if factor <= SpikeBlockMultiplier {
		t.Fatalf("factor de spike=%.1fx debería superar umbral %.0fx", factor, SpikeBlockMultiplier)
	}
}

// TestStressLatencyRisk_SlippageAbort configura tolerancia estricta (MinNetProfit alto)
// y simula 100 ms de latencia de ejecución mutando el mock de order book. La re-cotización
// con precios adversos debe abortar la operación (sin mover capital).
func TestStressLatencyRisk_SlippageAbort(t *testing.T) {
	e := &HFTEngine{Tracker: NewSpreadTracker()}
	s := newClientSession("slippage-latency", nil)
	initSession(s, 100_000, 2.0, nil, nil, nil)

	p := s.Params()
	p.MinNetProfitUSD = 25.0
	p.MaxOrderSizeBTC = 0.05
	p.SlippageRate = 0.0005
	p.OrderFailureProb = 0
	s.SetParams(p)

	mock := newMockExchangeBook()
	// Oportunidad viable: comprar Binance @60k, vender Bitso @61.2k (~$1.2k de spread).
	mock.setBinance(60_000, 59_990)
	mock.setBitso(61_210, 61_200)

	mkt0 := mock.snapshot()
	p0 := s.Params()
	vol := sizeOrder(p0.MaxOrderSizeBTC, mkt0.BinAskQty, mkt0.BitBidQty)
	_, _, _, net0 := computeNetProfit(mkt0.BinAsk, mkt0.BitBid, vol, p0.takerFee("Binance"), p0.takerFee("Bitso"), p0.SlippageRate)
	if net0 <= p0.MinNetProfitUSD {
		t.Fatalf("precondición: oportunidad inicial inviable net=%.2f umbral=%.2f", net0, p0.MinNetProfitUSD)
	}

	// Simular latencia de red (100 ms) y mutación adversa del libro mockeado.
	time.Sleep(100 * time.Millisecond)
	mock.setBitso(60_015, 60_005) // el spread colapsa: computeNetProfit ya no supera MinNetProfitUSD.

	mkt1 := mock.snapshot()
	_, _, _, net1 := computeNetProfit(mkt1.BinAsk, mkt1.BitBid, vol, p0.takerFee("Binance"), p0.takerFee("Bitso"), p0.SlippageRate)
	if net1 > p0.MinNetProfitUSD {
		t.Fatalf("precondición: mercado mutado aún viable net=%.2f", net1)
	}

	s.Mu.Lock()
	wealthBefore := s.TotalWealth
	s.LastTradeTime = time.Time{}
	s.Mu.Unlock()

	// Re-evaluación post-latencia: executeForSession usa el libro ACTUAL (mutado).
	e.executeForSession(s, mkt1)

	s.Mu.Lock()
	defer s.Mu.Unlock()
	if s.TotalNetProfit != 0 {
		t.Fatalf("operación no abortada tras cambio de precio: PnL=%.2f", s.TotalNetProfit)
	}
	if !almostEqual(s.TotalWealth, wealthBefore) {
		t.Fatalf("capital movido pese al aborto: antes=%.2f después=%.2f", wealthBefore, s.TotalWealth)
	}
	if s.IsExecuting {
		t.Fatal("Mutex de ejecución no liberado")
	}
}

// TestStressExchangeFailure_CircuitBreaker simula Fill-or-Kill fallido en la pierna remota
// (timeout / partial fill vía OrderFailureProb=100 %). El motor aborta ANTES de mover
// wallets (cero exposición direccional) y activa la pausa del circuit breaker.
func TestStressExchangeFailure_CircuitBreaker(t *testing.T) {
	e := &HFTEngine{Tracker: NewSpreadTracker()}
	s := newClientSession("exchange-failure", nil)
	initSession(s, 100_000, 2.0, nil, nil, nil)

	p := s.Params()
	p.OrderFailureProb = 1.0 // mock Bitso: timeout / partial fill determinista
	p.MinNetProfitUSD = 0.01
	s.SetParams(p)

	mock := newMockExchangeBook()
	mock.setBinance(60_000, 59_990)
	mock.setBitso(60_800, 60_790) // spread claramente rentable
	mkt := mock.snapshot()

	s.Mu.Lock()
	walletsBefore := s.Wallets.Clone()
	wealthBefore := s.TotalWealth
	pnlBefore := s.TotalNetProfit
	s.LastTradeTime = time.Time{}
	s.PausedUntil = time.Time{}
	s.Mu.Unlock()

	e.executeForSession(s, mkt)

	s.Mu.Lock()
	defer s.Mu.Unlock()

	if s.TotalNetProfit != pnlBefore {
		t.Fatalf("PnL cambió tras fallo de exchange: antes=%.2f después=%.2f", pnlBefore, s.TotalNetProfit)
	}
	if !almostEqual(s.TotalWealth, wealthBefore) {
		t.Fatalf("patrimonio alterado tras circuit breaker: %.2f → %.2f", wealthBefore, s.TotalWealth)
	}
	for venue, assets := range walletsBefore {
		for asset, amt := range assets {
			if !almostEqual(s.Wallets.Get(venue, asset), amt) {
				t.Fatalf("exposición direccional: %s/%s cambió de %.8f a %.8f",
					venue, asset, amt, s.Wallets.Get(venue, asset))
			}
		}
	}
	if !time.Now().Before(s.PausedUntil) {
		t.Fatal("circuit breaker no fijó PausedUntil")
	}
	if s.IsExecuting {
		t.Fatal("IsExecuting no liberado tras aborto atómico")
	}
}

// TestStressCapitalEfficiency_MXNBCreditRebalance fija USD en $0 en un exchange del par,
// presenta spread positivo real y verifica la inecuación creditWorthIt:
// Ganancia Neta > Costo del Préstamo × RiskMultiplier → auto-inyección de crédito.
func TestStressCapitalEfficiency_MXNBCreditRebalance(t *testing.T) {
	e := &HFTEngine{Tracker: NewSpreadTracker()}
	s := newClientSession("mxnb-credit", nil)
	initSession(s, 50_000, 1.0, nil, nil, nil)

	p := s.Params()
	p.MinNetProfitUSD = 0.10
	p.MaxOrderSizeBTC = 0.05
	p.RiskMultiplier = 1.0
	p.CreditOriginationFee = 10.0
	p.CreditAPR = 0.0
	p.CreditDurationMin = 1.0
	p.CreditLineUSD = DefaultCreditLineUSD
	p.OrderFailureProb = 0
	s.SetParams(p)

	// Wallet Bitso sin USD (MXN/USD cash agotado); Binance sin USDT para la pierna de compra.
	s.Mu.Lock()
	s.Wallets.Set("Bitso", "USD", 0)
	s.Wallets.Set("Binance", "USDT", 0)
	s.Credit.AutoMode = true
	s.Mu.Unlock()

	mock := newMockExchangeBook()
	mock.setBinance(60_000, 59_990)
	mock.setBitso(61_000, 60_990) // ~$990 de spread: neto >> costo del préstamo ($10)
	mkt := mock.snapshot()

	p = s.Params()
	vol := sizeOrder(p.MaxOrderSizeBTC, mkt.BinAskQty, mkt.BitBidQty)
	_, _, _, projected := computeNetProfit(mkt.BinAsk, mkt.BitBid, vol, p.takerFee("Binance"), p.takerFee("Bitso"), p.SlippageRate)
	creditCost := calculateCreditCost(p)
	required := creditCost * p.RiskMultiplier

	if !creditWorthIt(projected, creditCost, p.RiskMultiplier) {
		t.Fatalf("precondición matemática fallida: ganancia %.2f ≤ umbral %.2f (costo %.2f)",
			projected, required, creditCost)
	}

	s.Mu.Lock()
	s.LastTradeTime = time.Time{}
	s.Mu.Unlock()

	e.executeForSession(s, mkt)

	waitUntil(t, 2*time.Second, func() bool {
		s.Mu.Lock()
		defer s.Mu.Unlock()
		return s.Credit.Active
	})

	s.Mu.Lock()
	defer s.Mu.Unlock()

	if !s.Credit.Active {
		t.Fatal("el motor no activó la línea de crédito pese a spread > costo del préstamo")
	}
	if s.Credit.BorrowedUSD == nil || s.Credit.BorrowedUSD["Binance"] <= 0 {
		t.Fatalf("inyección USD ausente en Binance: %+v", s.Credit.BorrowedUSD)
	}
	if s.Wallets.Get("Binance", "USDT") <= 0 {
		t.Fatal("la wallet de Binance sigue sin liquidez tras el préstamo")
	}
	if math.Abs(s.Credit.LastCost-creditCost) > 1e-6 {
		t.Fatalf("costo del préstamo=%.2f, esperado %.2f", s.Credit.LastCost, creditCost)
	}
}
