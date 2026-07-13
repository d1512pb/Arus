package main

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestParamsConcurrency_AtomicHotSwap demuestra que TradingParameters es
// thread-safe bajo carga tipo producción:
//
//   - N goroutines "WebSocket" leen Params() en cada tick (hot path).
//   - 1 goroutine "REST set_params" publica snapshots nuevos con SetParams.
//
// El motor usa atomic.Pointer[TradingParameters] (no RWMutex en params): cada
// lector obtiene un snapshot consistente; el escritor publica un struct NUEVO.
// Este test falla con -race si hubiera data race, y valida coherencia de valores.
func TestParamsConcurrency_AtomicHotSwap(t *testing.T) {
	t.Log("═══════════════════════════════════════════════════════════════════")
	t.Log("  ARUS · PARAMS CONCURRENCY · THREAD-SAFETY (atomic.Pointer)")
	t.Log("  Lectores WS masivos + escritor REST set_params en caliente")
	t.Log("═══════════════════════════════════════════════════════════════════")

	s := newClientSession("params-race", nil)
	initSession(s, 50_000, 1.0, nil, nil, nil)

	const (
		readers      = 64
		ticksPerRead = 2_000
		writerEdits  = 500
	)

	var (
		wg           sync.WaitGroup
		inconsistent atomic.Uint64
		reads        atomic.Uint64
		writes       atomic.Uint64
		stop         atomic.Bool
	)

	// Escritor: simula el panel de estrategia (REST) actualizando fees/umbrales.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < writerEdits; i++ {
			p := DefaultTradingParameters()
			p.MinNetProfitUSD = 0.10 + float64(i%50)*0.01
			p.MaxOrderSizeBTC = 0.005 + float64(i%10)*0.001
			p.SlippageRate = 0.0003 + float64(i%5)*0.0001
			p.SpikeTickDeviation = 0.03 + float64(i%7)*0.01
			p.RiskMultiplier = 1.0 + float64(i%9)
			if p.TakerFees == nil {
				p.TakerFees = defaultTakerFees()
			}
			p.TakerFees["Binance"] = 0.001 + float64(i%3)*0.0001
			p.TakerFees["Bitso"] = 0.0065
			s.SetParams(p)
			writes.Add(1)
			time.Sleep(50 * time.Microsecond)
		}
		stop.Store(true)
	}()

	// Lectores: simulan fan-out de ticks WebSocket leyendo params en el hot path.
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < ticksPerRead; i++ {
				p := s.Params()
				reads.Add(1)

				// Invariantes de un snapshot coherente (post-sanitize / defaults sanos).
				if p.MinNetProfitUSD < 0 || p.MaxOrderSizeBTC < MinOrderSizeBTC ||
					p.SlippageRate < 0 || p.SlippageRate > MaxSlippageRate ||
					p.RiskMultiplier < MinRiskMultiplier ||
					p.SpikeTickDeviation < 0 {
					inconsistent.Add(1)
					t.Errorf("snapshot incoherente en reader=%d: %+v", id, p)
					return
				}
				// Fees del mapa no deben ser basura si existen.
				if p.TakerFees != nil {
					if f, ok := p.TakerFees["Binance"]; ok && (f < 0 || f > MaxTakerFee) {
						inconsistent.Add(1)
						t.Errorf("fee Binance fuera de rango: %v", f)
						return
					}
				}
				if stop.Load() && i > ticksPerRead/2 {
					// Drenar un poco más tras el escritor, luego salir.
					continue
				}
			}
		}(r)
	}

	wg.Wait()

	if n := inconsistent.Load(); n > 0 {
		t.Fatalf("❌ %d snapshots incoherentes bajo concurrencia", n)
	}

	final := s.Params()
	t.Logf("📊 [METRICS] Lecturas WS=%d | Escrituras REST=%d | Readers=%d",
		reads.Load(), writes.Load(), readers)
	t.Logf("📌 [SNAPSHOT FINAL] MinNet=$%.2f | MaxOrder=%.4f BTC | Slip=%.4f | Risk=%.1fx | FeeBin=%.4f",
		final.MinNetProfitUSD, final.MaxOrderSizeBTC, final.SlippageRate,
		final.RiskMultiplier, final.takerFee("Binance"))
	t.Log("✅ [PARAMS TEST PASS] atomic.Pointer hot-swap bajo carga WS+REST: cero races lógicos, snapshots siempre consistentes.")
}

// TestParamsConcurrency_SetParamsVisibleImmediately: un SetParams debe ser
// visible de forma atómica para el siguiente Params() (sin estado a medias).
func TestParamsConcurrency_SetParamsVisibleImmediately(t *testing.T) {
	s := newClientSession("params-visibility", nil)

	var ready, done sync.WaitGroup
	ready.Add(1)
	done.Add(1)

	go func() {
		defer done.Done()
		ready.Wait()
		for i := 0; i < 10_000; i++ {
			p := DefaultTradingParameters()
			p.MinNetProfitUSD = 42.0
			s.SetParams(p)
		}
	}()

	ready.Done()
	seen := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s.Params().MinNetProfitUSD == 42.0 {
			seen = true
			break
		}
	}
	done.Wait()

	if !seen {
		t.Fatal("SetParams no fue visible para los lectores en 2s")
	}
	if got := s.Params().MinNetProfitUSD; got != 42.0 {
		t.Fatalf("valor final=%v, esperado 42", got)
	}
	t.Log("✅ [PARAMS TEST PASS] Visibilidad atómica de SetParams confirmada (MinNetProfitUSD=42).")
}

// TestParamsConcurrency_ExecuteWhileMutating: executeForSession lee Params()
// una vez al inicio; mutaciones concurrentes no deben causar panic ni corromper
// wallets aunque el usuario mueva sliders a mitad de la evaluación.
func TestParamsConcurrency_ExecuteWhileMutating(t *testing.T) {
	t.Log("═══════════════════════════════════════════════════════════════════")
	t.Log("  ARUS · PARAMS · executeForSession bajo set_params concurrente")
	t.Log("═══════════════════════════════════════════════════════════════════")

	e := &HFTEngine{Tracker: NewSpreadTracker()}
	s := newClientSession("params-exec", nil)
	initSession(s, 100_000, 2.0, nil, nil, nil)

	mock := newMockExchangeBook()
	mock.setBinance(60_000, 59_990)
	mock.setBitso(60_400, 60_390)
	mkt := mock.snapshot()

	var stop atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		i := 0
		for !stop.Load() {
			p := DefaultTradingParameters()
			p.OrderFailureProb = 0
			p.MinNetProfitUSD = float64(i%20) * 0.05
			p.MaxOrderSizeBTC = 0.005
			s.SetParams(p)
			i++
		}
	}()

	for i := 0; i < 40; i++ {
		s.Mu.Lock()
		s.LastTradeTime = time.Time{}
		s.PausedUntil = time.Time{}
		s.Mu.Unlock()
		e.executeForSession(s, mkt)
	}
	stop.Store(true)
	wg.Wait()

	s.Mu.Lock()
	defer s.Mu.Unlock()
	if s.IsExecuting {
		t.Fatal("IsExecuting quedó bloqueado")
	}
	t.Logf("✅ [PARAMS TEST PASS] 40 ejecuciones con set_params en paralelo — sin panic; PnL=$%.2f; Mutex libre.",
		s.TotalNetProfit)
}
