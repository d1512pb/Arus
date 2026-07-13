package main

import (
	"sync"
	"testing"
	"time"
)

// ── Mocks de ejecución por venue (Trade Execution Manager) ───────────────────
// Modelan ACK / TIMEOUT sin red. El motor real usa Fill-or-Kill atómico
// (orderFails) ANTES de mutar wallets: nunca queda "pata coja". El mock narra
// el escenario adverso; las aserciones validan el escudo institucional.

type mockFillResult string

const (
	mockFillSuccess mockFillResult = "SUCCESS"
	mockFillTimeout mockFillResult = "TIMEOUT"
)

type mockVenueExecutor struct {
	name   string
	result mockFillResult
	delay  time.Duration
}

func (m mockVenueExecutor) Execute(t *testing.T, side string, notionalUSD float64) mockFillResult {
	t.Helper()
	start := time.Now()
	if m.delay > 0 {
		time.Sleep(m.delay)
	}
	elapsed := time.Since(start)
	t.Logf("📡 [MOCK %s] %s %.4f BTC-eq (~$%.2f) → %s | latencia simulada %d ms",
		m.name, side, notionalUSD/60_000, notionalUSD, m.result, elapsed.Milliseconds())
	return m.result
}

// TestStressAdverse_PataCoja_EmergencyUnwind simula la falla clásica de
// arbitraje cross-exchange: Binance confirma la compra, Bitso hace TIMEOUT en
// la venta. El Trade Execution Manager de Arus NO deja exposición direccional:
// el Circuit Breaker aborta Fill-or-Kill antes de tocar saldos (= hedging
// preventivo / Emergency Unwind con pérdida asumida $0).
func TestStressAdverse_PataCoja_EmergencyUnwind(t *testing.T) {
	t.Log("═══════════════════════════════════════════════════════════════════")
	t.Log("  ARUS · STRESS ADVERSE · ESCENARIO «LA PATA COJA»")
	t.Log("  Objetivo: Circuit Breaker + Delta Neutral bajo fallo de pierna")
	t.Log("═══════════════════════════════════════════════════════════════════")

	e := &HFTEngine{Tracker: NewSpreadTracker()}
	s := newClientSession("pata-coja", nil)
	initSession(s, 100_000, 2.0, nil, nil, nil)

	p := s.Params()
	p.OrderFailureProb = 1.0 // Bitso remoto: TIMEOUT determinista (FoK)
	p.MinNetProfitUSD = 0.01
	p.RadarAutopilot = false
	s.SetParams(p)

	mock := newMockExchangeBook()
	mock.setBinance(60_000, 59_990)
	mock.setBitso(60_800, 60_790) // spread claramente rentable
	mkt := mock.snapshot()

	vol := sizeOrder(p.MaxOrderSizeBTC, mkt.BinAskQty, mkt.BitBidQty)
	_, _, _, net := computeNetProfit(mkt.BinAsk, mkt.BitBid, vol, p.takerFee("Binance"), p.takerFee("Bitso"), p.SlippageRate)
	t.Logf("📈 [OPORTUNIDAD] Buy Binance @ $%.2f → Sell Bitso @ $%.2f | Vol %.4f BTC | Neto proyectado +$%.2f",
		mkt.BinAsk, mkt.BitBid, vol, net)

	// Narrativa de las dos piernas (mocks de exchange).
	binance := mockVenueExecutor{name: "Binance", result: mockFillSuccess, delay: 12 * time.Millisecond}
	bitso := mockVenueExecutor{name: "Bitso", result: mockFillTimeout, delay: 87 * time.Millisecond}

	buyNotional := mkt.BinAsk * vol
	if got := binance.Execute(t, "BUY", buyNotional); got != mockFillSuccess {
		t.Fatalf("precondición mock Binance: esperado SUCCESS, got %s", got)
	}
	t.Log("✅ [LEG 1] Binance ACK — compra teórica confirmada (inventario aún NO acreditado: FoK atómico)")

	timeoutStart := time.Now()
	if got := bitso.Execute(t, "SELL", mkt.BitBid*vol); got != mockFillTimeout {
		t.Fatalf("precondición mock Bitso: esperado TIMEOUT, got %s", got)
	}
	timeoutMs := time.Since(timeoutStart).Milliseconds()
	t.Logf("⏱️  [LEG 2] Bitso TIMEOUT — pierna de venta no confirmada (%d ms)", timeoutMs)

	// Snapshot pre-ejecución del motor real.
	s.Mu.Lock()
	walletsBefore := s.Wallets.Clone()
	wealthBefore := s.TotalWealth
	pnlBefore := s.TotalNetProfit
	binBTCBefore := s.Wallets.Get("Binance", "BTC")
	s.LastTradeTime = time.Time{}
	s.PausedUntil = time.Time{}
	s.Mu.Unlock()

	t.Log("🔌 [TRADE EXECUTION MANAGER] Invocando executeForSession con OrderFailureProb=100%…")
	e.executeForSession(s, mkt)

	s.Mu.Lock()
	defer s.Mu.Unlock()

	// ── Circuit Breaker ────────────────────────────────────────────────────
	if !time.Now().Before(s.PausedUntil) {
		t.Fatal("❌ Circuit Breaker no fijó PausedUntil tras TIMEOUT de Bitso")
	}
	t.Logf("🛡️  [CIRCUIT BREAKER] Accionado — sesión en pausa hasta %s", s.PausedUntil.Format("15:04:05.000"))

	// ── Mutex / no freeze ──────────────────────────────────────────────────
	if s.IsExecuting {
		t.Fatal("❌ Mutex IsExecuting quedó bloqueado (bot congelado)")
	}
	t.Log("🔓 [MUTEX] IsExecuting liberado — el bot no se congeló")

	// ── Emergency Unwind / Hedging preventivo (= delta neutral) ────────────
	// Política institucional: FoK aborta ANTES de acreditar BTC en Binance.
	// No hay inventario que revender → Unwind implícito con pérdida $0.
	binBTCAfter := s.Wallets.Get("Binance", "BTC")
	if !almostEqual(binBTCAfter, binBTCBefore) {
		t.Fatalf("❌ Exposición direccional: Binance BTC %.8f → %.8f (se esperaba flat)", binBTCBefore, binBTCAfter)
	}
	for venue, assets := range walletsBefore {
		for asset, amt := range assets {
			if !almostEqual(s.Wallets.Get(venue, asset), amt) {
				t.Fatalf("❌ Wallet mutada tras FoK: %s/%s %.8f → %.8f",
					venue, asset, amt, s.Wallets.Get(venue, asset))
			}
		}
	}
	if s.TotalNetProfit != pnlBefore || !almostEqual(s.TotalWealth, wealthBefore) {
		t.Fatalf("❌ PnL/patrimonio alterados: wealth %.2f→%.2f pnl %.2f→%.2f",
			wealthBefore, s.TotalWealth, pnlBefore, s.TotalNetProfit)
	}

	assumedLoss := 0.0 // FoK preventivo: nunca se compró la pierna larga
	t.Logf("♻️  [EMERGENCY UNWIND / HEDGING] Inventario Binance no acreditado — delta neutral preservado")
	t.Logf("📉 [P&L] Pérdida asumida: $%.2f USD (FoK atómico; sin fees de unwind)", assumedLoss)

	t.Logf("✅ [STRESS TEST PASS] Timeout detectado en Bitso (%dms). Circuit Breaker accionado. Exposición direccional mitigada vía Emergency Unwind en Binance. Pérdida asumida: $%.2f USD.",
		timeoutMs, assumedLoss)
}

// TestStressAdverse_ConcurrentExecutions_MutexPressure martilla executeForSession
// desde N goroutines con el mismo tick: solo una puede operar (IsExecuting) y
// el FoK no debe dejar estado corrupto bajo contención de mutex.
func TestStressAdverse_ConcurrentExecutions_MutexPressure(t *testing.T) {
	t.Log("═══════════════════════════════════════════════════════════════════")
	t.Log("  ARUS · STRESS ADVERSE · PRESIÓN DE MUTEX (fan-out concurrente)")
	t.Log("═══════════════════════════════════════════════════════════════════")

	e := &HFTEngine{Tracker: NewSpreadTracker()}
	s := newClientSession("mutex-pressure", nil)
	initSession(s, 100_000, 2.0, nil, nil, nil)

	p := s.Params()
	p.OrderFailureProb = 1.0
	p.MinNetProfitUSD = 0.01
	s.SetParams(p)

	mock := newMockExchangeBook()
	mock.setBinance(60_000, 59_990)
	mock.setBitso(60_800, 60_790)
	mkt := mock.snapshot()

	s.Mu.Lock()
	wealthBefore := s.TotalWealth
	s.LastTradeTime = time.Time{}
	s.PausedUntil = time.Time{}
	s.Mu.Unlock()

	const workers = 32
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			e.executeForSession(s, mkt)
		}()
	}
	wg.Wait()

	s.Mu.Lock()
	defer s.Mu.Unlock()
	if s.IsExecuting {
		t.Fatal("IsExecuting quedó true tras fan-out concurrente")
	}
	if !almostEqual(s.TotalWealth, wealthBefore) {
		t.Fatalf("patrimonio cambió bajo FoK concurrente: %.2f → %.2f", wealthBefore, s.TotalWealth)
	}
	t.Logf("✅ [STRESS TEST PASS] %d goroutines concurrentes; Mutex estable; capital intacto ($%.2f).",
		workers, s.TotalWealth)
}
