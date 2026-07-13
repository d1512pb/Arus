package main

import (
	"math"
	"testing"
	"time"
)

// TestCreditRebalance_ZeroBalanceAutoInjection demuestra eficiencia de capital
// estilo Gravity / MXNB: wallet de origen en $0, spread altísimo, AutoMode ON.
// El bot NO aborta por "Insufficient Funds": evalúa creditWorthIt, inyecta la
// línea, descuenta el fee del préstamo del PnL y deja la sesión lista para
// ejecutar el arbitraje en el siguiente ciclo.
func TestCreditRebalance_ZeroBalanceAutoInjection(t *testing.T) {
	t.Log("═══════════════════════════════════════════════════════════════════")
	t.Log("  ARUS · CREDIT / REBALANCE · EFICIENCIA DE CAPITAL (MXNB)")
	t.Log("  Objetivo: $0 en origen + spread → auto-crédito, no Insufficient Funds")
	t.Log("═══════════════════════════════════════════════════════════════════")

	e := &HFTEngine{Tracker: NewSpreadTracker()}
	s := newClientSession("credit-mxnb", nil)
	initSession(s, 50_000, 1.0, nil, nil, nil)

	p := s.Params()
	p.MinNetProfitUSD = 0.10
	p.MaxOrderSizeBTC = 0.05
	p.RiskMultiplier = 1.0
	p.CreditOriginationFee = 10.0
	p.CreditAPR = 0.0 // costo = solo originación (auditable)
	p.CreditDurationMin = 1.0
	p.CreditLineUSD = DefaultCreditLineUSD
	p.CreditLineBTC = DefaultCreditLineBTC
	p.OrderFailureProb = 0
	p.RadarAutopilot = false
	s.SetParams(p)

	// Estado adverso: cash agotado en el par (sin USDT/USD para comprar).
	s.Mu.Lock()
	s.Wallets.Set("Binance", "USDT", 0)
	s.Wallets.Set("Bitso", "USD", 0)
	// Conservamos BTC en Bitso para que, tras el crédito, la pierna de venta exista.
	bitsoBTC := s.Wallets.Get("Bitso", "BTC")
	s.Credit.AutoMode = true
	pnlBefore := s.TotalNetProfit
	s.Mu.Unlock()

	t.Logf("💼 [WALLET] Binance USDT=$0 | Bitso USD=$0 | Bitso BTC=%.4f (inventario residual)", bitsoBTC)
	t.Log("⚙️  [CREDIT] AutoMode=ON | Fee originación=$10 | APR=0% | RiskMultiplier=1.0x")

	mock := newMockExchangeBook()
	mock.setBinance(60_000, 59_990)
	mock.setBitso(61_000, 60_990) // ~$990 de spread bruto
	mkt := mock.snapshot()

	p = s.Params()
	vol := sizeOrder(p.MaxOrderSizeBTC, mkt.BinAskQty, mkt.BitBidQty)
	_, fees, slip, projected := computeNetProfit(
		mkt.BinAsk, mkt.BitBid, vol,
		p.takerFee("Binance"), p.takerFee("Bitso"), p.SlippageRate,
	)
	creditCost := calculateCreditCost(p)
	required := creditCost * p.RiskMultiplier

	t.Logf("📈 [SPREAD] Buy $%.2f → Sell $%.2f | Vol %.4f BTC", mkt.BinAsk, mkt.BitBid, vol)
	t.Logf("🧮 [MATH] Neto proyectado +$%.2f | Fees $%.2f | Slippage $%.2f | Costo crédito $%.2f | Umbral $%.2f",
		projected, fees, slip, creditCost, required)

	if !creditWorthIt(projected, creditCost, p.RiskMultiplier) {
		t.Fatalf("precondición: ganancia %.2f ≤ umbral %.2f — el escenario debe justificar el préstamo",
			projected, required)
	}
	t.Logf("✅ [INECUACIÓN] Ganancia +$%.2f > Costo×Riesgo $%.2f → crédito DOMINA al reequilibrio pasivo",
		projected, required)

	s.Mu.Lock()
	s.LastTradeTime = time.Time{}
	s.PausedUntil = time.Time{}
	s.Mu.Unlock()

	// Ciclo 1: shortfall → auto-crédito (NO diálogo Insufficient Funds).
	t.Log("▶️  [CICLO 1] executeForSession con cash=$0…")
	e.executeForSession(s, mkt)

	waitUntil(t, 2*time.Second, func() bool {
		s.Mu.Lock()
		defer s.Mu.Unlock()
		return s.Credit.Active
	})

	s.Mu.Lock()
	if s.InsufficientFundsPending {
		s.Mu.Unlock()
		t.Fatal("❌ El bot emitió Insufficient Funds con AutoMode ON — debía auto-inyectar crédito")
	}
	if !s.Credit.Active {
		s.Mu.Unlock()
		t.Fatal("❌ Crédito no activado pese a creditWorthIt=true")
	}
	costPaid := s.Credit.LastCost
	binUSDT := s.Wallets.Get("Binance", "USDT")
	bitUSD := s.Wallets.Get("Bitso", "USD")
	pnlAfterCredit := s.TotalNetProfit
	s.Mu.Unlock()

	if math.Abs(costPaid-creditCost) > 1e-6 {
		t.Fatalf("fee del préstamo=%.4f, esperado %.4f", costPaid, creditCost)
	}
	if binUSDT <= 0 || bitUSD <= 0 {
		t.Fatalf("inyección incompleta: Binance USDT=%.2f Bitso USD=%.2f", binUSDT, bitUSD)
	}
	if !almostEqual(pnlAfterCredit, pnlBefore-costPaid) {
		t.Fatalf("el costo del crédito no se descontó del PnL: antes=%.2f después=%.2f costo=%.2f",
			pnlBefore, pnlAfterCredit, costPaid)
	}

	t.Logf("🏦 [CRÉDITO AUTO-ACTIVADO] +$%.0f USD + %.1f BTC | Fee cobrado $%.2f | PnL %.2f → %.2f",
		p.CreditLineUSD, p.CreditLineBTC, costPaid, pnlBefore, pnlAfterCredit)
	t.Logf("💼 [WALLET POST-CRÉDITO] Binance USDT=$%.2f | Bitso USD=$%.2f", binUSDT, bitUSD)

	// Ciclo 2: con liquidez inyectada, el trade debe ejecutarse.
	s.Mu.Lock()
	s.LastTradeTime = time.Time{}
	s.PausedUntil = time.Time{}
	pnlPreTrade := s.TotalNetProfit
	s.Mu.Unlock()

	t.Log("▶️  [CICLO 2] Reintento post-inyección (OrderFailureProb=0)…")
	var traded bool
	for i := 0; i < 8 && !traded; i++ {
		e.executeForSession(s, mkt)
		s.Mu.Lock()
		traded = s.TotalNetProfit > pnlPreTrade
		s.LastTradeTime = time.Time{}
		s.PausedUntil = time.Time{}
		s.Mu.Unlock()
	}

	s.Mu.Lock()
	defer s.Mu.Unlock()
	if !traded {
		t.Fatal("❌ Tras el crédito el motor no ejecutó el arbitraje")
	}
	t.Logf("⚡ [ARBITRAJE] Ejecutado | PnL sesión $%.2f (incluye −$%.2f de fee de crédito)",
		s.TotalNetProfit, costPaid)
	t.Logf("✅ [CREDIT TEST PASS] Cash=$0 no bloqueó al bot. Inecuación creditWorthIt OK. Fee $%.2f descontado. Orden ejecutada post-inyección. Delta de capital restaurada.",
		costPaid)
}

// TestCreditRebalance_RejectsWhenSpreadBelowCost: si el neto no cubre
// costo × RiskMultiplier, AutoMode elige REEQUILIBRIO (no endeudarse a pérdida).
func TestCreditRebalance_RejectsWhenSpreadBelowCost(t *testing.T) {
	t.Log("═══════════════════════════════════════════════════════════════════")
	t.Log("  ARUS · CREDIT · DOMINANCIA NEGATIVA → REEQUILIBRIO (no préstamo)")
	t.Log("═══════════════════════════════════════════════════════════════════")

	e := &HFTEngine{Tracker: NewSpreadTracker()}
	s := newClientSession("credit-reject", nil)
	initSession(s, 10_000, 0.2, nil, nil, nil)

	p := s.Params()
	p.RiskMultiplier = 5.0 // conservador: exigir 5× el costo
	p.CreditOriginationFee = 25.0
	p.CreditAPR = 0.10
	p.OrderFailureProb = 0
	s.SetParams(p)

	s.Mu.Lock()
	s.Wallets.Set("Binance", "USDT", 0)
	s.Wallets.Set("Bitso", "USD", 0)
	s.Credit.AutoMode = true
	s.Mu.Unlock()

	cost := calculateCreditCost(p)
	// Ganancia positiva pero insuficiente frente a umbral 5× (no justifica endeudarse).
	projected := cost * 2 // cubre 2× el costo, el usuario exige 5×
	t.Logf("🧮 [MATH] Neto +$%.2f | Costo $%.2f | Umbral (×5) $%.2f → creditWorthIt=%v",
		projected, cost, cost*p.RiskMultiplier, creditWorthIt(projected, cost, p.RiskMultiplier))

	if creditWorthIt(projected, cost, p.RiskMultiplier) {
		t.Fatal("precondición rota: el escenario debía fallar creditWorthIt")
	}

	e.handleLiquidityShortfall(s, projected)

	s.Mu.Lock()
	defer s.Mu.Unlock()
	if s.Credit.Active {
		t.Fatal("no debía activar crédito cuando la ganancia no cubre costo×riesgo")
	}
	if !s.IsReplenishing {
		t.Fatal("esperado startReplenishing (reequilibrio) cuando auto-crédito no domina")
	}
	t.Log("✅ [CREDIT TEST PASS] Dominancia negativa → reequilibrio 50/50, sin endeudamiento.")
}
