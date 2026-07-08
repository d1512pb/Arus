package main

import (
	"encoding/json"
	"math"
	"sync"
	"testing"
)

// almostEqual compara floats con tolerancia absoluta (aritmética financiera de demo).
func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

// TestComputeNetProfit_MatchesInstitutionalFormula verifica que la fórmula única del
// motor equivale EXACTAMENTE a la forma institucional:
//
//	Neto = (P_venta × V × (1 − fee_venta)) − (P_compra × V × (1 + fee_compra)) − Slippage
func TestComputeNetProfit_MatchesInstitutionalFormula(t *testing.T) {
	buyPrice, sellPrice := 60_000.0, 60_450.0
	volume := 0.25
	feeBuy, feeSell := 0.001, 0.0065
	slipRate := 0.0005

	_, _, slippage, net := computeNetProfit(buyPrice, sellPrice, volume, feeBuy, feeSell, slipRate)

	institutional := (sellPrice * volume * (1 - feeSell)) - (buyPrice * volume * (1 + feeBuy)) - slippage
	if !almostEqual(net, institutional) {
		t.Fatalf("neto=%.10f difiere de la forma institucional=%.10f", net, institutional)
	}
}

// TestComputeNetProfit_Breakdown valida el desglose auditable (bruto/fees/slippage).
func TestComputeNetProfit_Breakdown(t *testing.T) {
	gross, fees, slippage, net := computeNetProfit(100.0, 110.0, 2.0, 0.01, 0.02, 0.001)

	if !almostEqual(gross, 20.0) { // (110-100)*2
		t.Errorf("bruto=%.6f, esperado 20", gross)
	}
	if !almostEqual(fees, (100*0.01+110*0.02)*2.0) { // compra 1%, venta 2%
		t.Errorf("fees=%.6f, esperado %.6f", fees, (100*0.01+110*0.02)*2.0)
	}
	if !almostEqual(slippage, (100.0+110.0)*2.0*0.001) {
		t.Errorf("slippage=%.6f, esperado %.6f", slippage, (100.0+110.0)*2.0*0.001)
	}
	if !almostEqual(net, gross-fees-slippage) {
		t.Errorf("neto=%.6f no es bruto-fees-slippage", net)
	}
}

// TestComputeNetProfit_LiquidityTrap: una oportunidad rentable en BRUTO pero negativa
// en NETO (la "trampa de liquidez" que el motor debe rechazar).
func TestComputeNetProfit_LiquidityTrap(t *testing.T) {
	// Spread bruto de $30 sobre 0.005 BTC = $0.15 de bruto…
	gross, _, _, net := computeNetProfit(60_000.0, 60_030.0, 0.005, 0.001, 0.0065, 0.0005)
	if gross <= 0 {
		t.Fatalf("el bruto debería ser positivo, fue %.6f", gross)
	}
	// …pero los fees (~$2.25) lo devoran: el neto debe ser negativo.
	if net >= 0 {
		t.Fatalf("trampa de liquidez no detectada: neto=%.6f debería ser negativo", net)
	}
}

// TestCreditWorthIt cubre la inecuación de dominancia con el multiplicador de riesgo.
func TestCreditWorthIt(t *testing.T) {
	cost := calculateCreditCost(CreditLineUSD)

	cases := []struct {
		name       string
		projected  float64
		multiplier float64
		want       bool
	}{
		{"agresivo 1x: apenas sobre el costo → sí", cost + 0.01, 1.0, true},
		{"agresivo 1x: exactamente el costo → no (estricto)", cost, 1.0, false},
		{"conservador 5x: cubre 4x → no", cost * 4, 5.0, false},
		{"conservador 5x: cubre 6x → sí", cost * 6, 5.0, true},
		{"ganancia cero → nunca", 0, 1.0, false},
	}
	for _, c := range cases {
		if got := creditWorthIt(c.projected, cost, c.multiplier); got != c.want {
			t.Errorf("%s: creditWorthIt(%.2f, %.2f, %.1f)=%v, esperado %v",
				c.name, c.projected, cost, c.multiplier, got, c.want)
		}
	}
}

// TestCalculateCreditCost: originación + interés prorrateado por minuto.
func TestCalculateCreditCost(t *testing.T) {
	got := calculateCreditCost(CreditLineUSD)
	interest := (CreditAPR / 365.0 / 24.0 / 60.0) * CreditDurationMinutes * CreditLineUSD
	want := CreditOriginationFee + interest
	if !almostEqual(got, want) {
		t.Fatalf("costo=%.10f, esperado %.10f", got, want)
	}
}

// TestSizeOrder: el volumen es min(tope del usuario, liquidez de ambas piernas),
// y qty=0 significa "sin dato" (no acota).
func TestSizeOrder(t *testing.T) {
	cases := []struct {
		name                  string
		maxOrder, buyQ, sellQ float64
		want                  float64
	}{
		{"liquidez sobrada → tope del usuario", 0.01, 5.0, 3.0, 0.01},
		{"pierna de compra acota", 1.0, 0.05, 3.0, 0.05},
		{"pierna de venta acota", 1.0, 5.0, 0.02, 0.02},
		{"sin datos de liquidez → tope", 0.5, 0, 0, 0.5},
		{"solo un lado con dato", 0.5, 0.1, 0, 0.1},
	}
	for _, c := range cases {
		if got := sizeOrder(c.maxOrder, c.buyQ, c.sellQ); !almostEqual(got, c.want) {
			t.Errorf("%s: sizeOrder(%v,%v,%v)=%v, esperado %v", c.name, c.maxOrder, c.buyQ, c.sellQ, got, c.want)
		}
	}
}

// TestSanitizeTradingParams: los clamps de backend garantizan que ningún mensaje
// (malicioso o corrupto) deje la sesión con parámetros absurdos.
func TestSanitizeTradingParams(t *testing.T) {
	nan := math.NaN()

	p := sanitizeTradingParams(TradingParameters{
		TakerFees: map[string]float64{
			"Binance":   0.9,   // fuera de rango → clamp a MaxTakerFee
			"Bitso":     -0.5,  // negativo → clamp a 0
			"Inventado": 0.001, // venue desconocido → descartado
		},
		MinNetProfitUSD:    -10, // → 0
		MaxOrderSizeBTC:    999, // → MaxOrderSizeCapBTC
		SlippageRate:       nan, // NaN → default
		SpikeTickDeviation: 0,   // 0 (ausente) → clamp a mínimo
		MaxDivergenceRatio: 50,  // → MaxDivergenceRatioLimit
		RiskMultiplier:     0.2, // <1 (endeudarse a pérdida) → clamp a 1
	})

	if p.TakerFees["Binance"] != MaxTakerFee {
		t.Errorf("fee Binance=%v, esperado clamp a %v", p.TakerFees["Binance"], MaxTakerFee)
	}
	if p.TakerFees["Bitso"] != MinTakerFee {
		t.Errorf("fee Bitso=%v, esperado clamp a %v", p.TakerFees["Bitso"], MinTakerFee)
	}
	if _, ok := p.TakerFees["Inventado"]; ok {
		t.Error("venue desconocido no debe aceptarse en TakerFees")
	}
	if p.MinNetProfitUSD != MinNetProfitFloor {
		t.Errorf("MinNetProfitUSD=%v, esperado %v", p.MinNetProfitUSD, MinNetProfitFloor)
	}
	if p.MaxOrderSizeBTC != MaxOrderSizeCapBTC {
		t.Errorf("MaxOrderSizeBTC=%v, esperado %v", p.MaxOrderSizeBTC, MaxOrderSizeCapBTC)
	}
	if p.SlippageRate != DefaultSlippageRate {
		t.Errorf("SlippageRate=%v, esperado default %v ante NaN", p.SlippageRate, DefaultSlippageRate)
	}
	if p.SpikeTickDeviation != MinSpikeDeviation {
		t.Errorf("SpikeTickDeviation=%v, esperado clamp a %v", p.SpikeTickDeviation, MinSpikeDeviation)
	}
	if p.MaxDivergenceRatio != MaxDivergenceRatioLimit {
		t.Errorf("MaxDivergenceRatio=%v, esperado %v", p.MaxDivergenceRatio, MaxDivergenceRatioLimit)
	}
	if p.RiskMultiplier != MinRiskMultiplier {
		t.Errorf("RiskMultiplier=%v, esperado clamp a %v", p.RiskMultiplier, MinRiskMultiplier)
	}
}

// TestSanitizeTradingParams_ValidPassesThrough: valores legítimos no se alteran.
func TestSanitizeTradingParams_ValidPassesThrough(t *testing.T) {
	in := TradingParameters{
		TakerFees:          map[string]float64{"Binance": 0.002, "Bitso": 0.004},
		MinNetProfitUSD:    10.0,
		MaxOrderSizeBTC:    0.01,
		SlippageRate:       0.001,
		SpikeTickDeviation: 0.08,
		MaxDivergenceRatio: 1.15,
		RiskMultiplier:     5.0,
	}
	p := sanitizeTradingParams(in)
	if p.TakerFees["Binance"] != 0.002 || p.TakerFees["Bitso"] != 0.004 ||
		p.MinNetProfitUSD != 10.0 || p.MaxOrderSizeBTC != 0.01 ||
		p.SlippageRate != 0.001 || p.SpikeTickDeviation != 0.08 ||
		p.MaxDivergenceRatio != 1.15 || p.RiskMultiplier != 5.0 {
		t.Fatalf("parámetros válidos alterados: %+v", p)
	}
}

// TestSessionParams_AtomicSwap: el snapshot es consistente y el swap es visible.
func TestSessionParams_AtomicSwap(t *testing.T) {
	s := newClientSession("test", nil)

	p := s.Params()
	if p.MinNetProfitUSD != DefaultMinNetProfitUSD {
		t.Fatalf("params iniciales incorrectos: %+v", p)
	}

	updated := DefaultTradingParameters()
	updated.MinNetProfitUSD = 42.0
	s.SetParams(updated)

	if got := s.Params().MinNetProfitUSD; got != 42.0 {
		t.Fatalf("SetParams no visible: MinNetProfitUSD=%v", got)
	}
}

// TestSpreadTracker_ConcurrentAccess: el tracker es compartido entre el bucle de
// detección (Add) y las goroutines del simulador (Average). Con -race este test
// detecta cualquier regresión de sincronización.
func TestSpreadTracker_ConcurrentAccess(t *testing.T) {
	tr := NewSpreadTracker()
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				tr.Add(float64(j))
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				_ = tr.Average()
			}
		}()
	}
	wg.Wait()

	if avg := tr.Average(); avg <= 0 {
		t.Fatalf("promedio inesperado tras escrituras: %v", avg)
	}
}

// TestCoherentBook: la validación de libros en el origen.
func TestCoherentBook(t *testing.T) {
	cases := []struct {
		name     string
		ask, bid float64
		want     bool
	}{
		{"libro sano", 60_010, 60_000, true},
		{"cruzado (ask<bid)", 59_000, 60_000, false},
		{"lado en cero", 0, 60_000, false},
		{"negativo", 60_000, -1, false},
		{"spread interno absurdo ≥5%", 63_200, 60_000, false},
	}
	for _, c := range cases {
		if got := coherentBook(c.ask, c.bid); got != c.want {
			t.Errorf("%s: coherentBook(%v,%v)=%v, esperado %v", c.name, c.ask, c.bid, got, c.want)
		}
	}
}

// TestBestAskBid: mejor punta del libro de Bitso CON su cantidad (Fase 1: la
// liquidez ya no se descarta).
func TestBestAskBid(t *testing.T) {
	asks := []bitsoOrder{{Rate: "60100", Amount: "0.8"}, {Rate: "60050", Amount: "0.3"}, {Rate: "corrupto", Amount: "9"}}
	bids := []bitsoOrder{{Rate: "59900", Amount: "1.2"}, {Rate: "59950", Amount: "0.5"}}

	if price, qty := bestAsk(asks); price != 60050 || qty != 0.3 {
		t.Errorf("bestAsk=(%v,%v), esperado (60050, 0.3)", price, qty)
	}
	if price, qty := bestBid(bids); price != 59950 || qty != 0.5 {
		t.Errorf("bestBid=(%v,%v), esperado (59950, 0.5)", price, qty)
	}
	if price, _ := bestAsk(nil); price != 0 {
		t.Errorf("bestAsk(nil) debería ser 0, fue %v", price)
	}
}

// TestIsValidTick: el Spike Filter tick-a-tick.
func TestIsValidTick(t *testing.T) {
	if !isValidTick(60_000, 0, DefaultSpikeTickDeviation) {
		t.Error("el primer tick (lastPrice=0) siempre es válido")
	}
	if !isValidTick(60_500, 60_000, DefaultSpikeTickDeviation) {
		t.Error("variación de 0.83% debe pasar con umbral de 5%")
	}
	if isValidTick(66_100, 60_000, DefaultSpikeTickDeviation) {
		t.Error("variación >5% debe descartarse")
	}
}

// TestSanitizeDemoInject: validación/clamps del simulador.
func TestSanitizeDemoInject(t *testing.T) {
	if _, _, _, ok := sanitizeDemoInject("OKX", 100, 1); ok {
		t.Error("venue no registrado debe rechazarse")
	}
	if _, _, _, ok := sanitizeDemoInject("Binance", math.Inf(1), 1); ok {
		t.Error("spread infinito debe rechazarse")
	}
	if _, _, _, ok := sanitizeDemoInject("Binance", 100, 0); ok {
		t.Error("liquidez 0 debe rechazarse")
	}
	if _, _, liq, ok := sanitizeDemoInject("Bitso", 100, 99); !ok || liq != MaxDemoLiquidity {
		t.Errorf("liquidez gigante debe clamparse a %v (liq=%v ok=%v)", MaxDemoLiquidity, liq, ok)
	}
	if _, spread, _, ok := sanitizeDemoInject("Bitso", -1e9, 1); !ok || spread != -MaxDemoSpreadUSD {
		t.Errorf("spread negativo gigante debe clamparse a %v (spread=%v)", -MaxDemoSpreadUSD, spread)
	}
}

// TestTakerFee_FallbackToRegistry: fee ausente en el snapshot → default del registro.
func TestTakerFee_FallbackToRegistry(t *testing.T) {
	p := TradingParameters{TakerFees: map[string]float64{"Binance": 0.002}}
	if got := p.takerFee("Binance"); got != 0.002 {
		t.Errorf("fee override=%v, esperado 0.002", got)
	}
	if got := p.takerFee("Bitso"); got != 0.0065 {
		t.Errorf("fee fallback=%v, esperado 0.0065 (registro)", got)
	}
	if got := p.takerFee("Desconocido"); got != 0 {
		t.Errorf("venue desconocido=%v, esperado 0", got)
	}
}

// TestKrakenTickerParsing: los structs del adaptador de Kraken contra un payload
// REAL capturado del WS v2 (números JSON, no strings — a diferencia de Binance).
func TestKrakenTickerParsing(t *testing.T) {
	raw := `{"channel":"ticker","type":"snapshot","data":[{"symbol":"BTC/USD","bid":62703.4,"bid_qty":0.0318498,"ask":62710.3,"ask_qty":0.00191335,"last":62702.9,"volume":1947.83637005,"vwap":63392.2,"low":62476.1,"high":64196.6,"change":-239.4,"change_pct":-0.38,"timestamp":"2026-07-08T05:24:41.204829Z"}]}`

	var m krakenWSMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("payload real de Kraken no parsea: %v", err)
	}
	if m.Channel != "ticker" || len(m.Data) != 1 {
		t.Fatalf("mensaje mal mapeado: %+v", m)
	}
	d := m.Data[0]
	if d.Symbol != "BTC/USD" || !almostEqual(d.Bid, 62703.4) || !almostEqual(d.Ask, 62710.3) ||
		!almostEqual(d.BidQty, 0.0318498) || !almostEqual(d.AskQty, 0.00191335) {
		t.Fatalf("ticker mal mapeado: %+v", d)
	}
	if !coherentBook(d.Ask, d.Bid) {
		t.Fatal("el libro real de Kraken debe pasar la validación de coherencia")
	}

	// Heartbeats y acks de suscripción llegan por el mismo socket: se ignoran
	// porque su channel no es "ticker" (el ack ni siquiera trae channel).
	for _, other := range []string{
		`{"channel":"heartbeat"}`,
		`{"method":"subscribe","result":{"channel":"ticker","symbol":"BTC/USD"},"success":true}`,
	} {
		var o krakenWSMessage
		if err := json.Unmarshal([]byte(other), &o); err != nil {
			t.Fatalf("mensaje auxiliar no parsea: %v", err)
		}
		if o.Channel == "ticker" {
			t.Fatalf("mensaje auxiliar clasificado como ticker: %s", other)
		}
	}
}

// TestInitSession_ClassicPairOnly: el capital inicial se reparte 50/50 SOLO en el
// par clásico. Con 3 venues registrados la suma sigue siendo el capital EXACTO
// (antes del Sprint C, usd/2 por venue habría inflado el capital 1.5×).
func TestInitSession_ClassicPairOnly(t *testing.T) {
	s := newClientSession("init-3-venues", nil)
	initSession(s, 10_000, 0.5)

	s.Mu.Lock()
	defer s.Mu.Unlock()
	sumQuote, sumBase := 0.0, 0.0
	for _, v := range Venues {
		sumQuote += s.Wallets.Get(v.Name, v.QuoteAsset)
		sumBase += s.Wallets.Get(v.Name, v.BaseAsset)
	}
	if !almostEqual(sumQuote, 10_000) || !almostEqual(sumBase, 0.5) {
		t.Fatalf("capital total=%v USD / %v BTC, esperado 10000 / 0.5 exactos", sumQuote, sumBase)
	}
	if !almostEqual(s.Wallets.Get("Binance", "USDT"), 5_000) || !almostEqual(s.Wallets.Get("Bitso", "USD"), 5_000) {
		t.Fatalf("el par clásico no recibió el 50/50: %+v", s.Wallets)
	}
	if s.Wallets.Get("Kraken", "USD") != 0 || s.Wallets.Get("Kraken", "BTC") != 0 {
		t.Fatalf("Kraken debe nacer en cero: %+v", s.Wallets["Kraken"])
	}
}

// TestRebalance_PreservesNonClassicVenues: el reequilibrio 50/50 opera SOLO sobre
// el par clásico; los saldos en Kraken (u otros activos) se quedan donde están.
func TestRebalance_PreservesNonClassicVenues(t *testing.T) {
	e := &HFTEngine{Tracker: NewSpreadTracker()}
	s := newClientSession("rebal-3-venues", nil)
	initSession(s, 10_000, 0.5)

	s.Mu.Lock()
	s.Wallets.Set("Binance", "USDT", 9_000) // par desbalanceado a propósito
	s.Wallets.Set("Bitso", "USD", 100)
	s.Wallets.Set("Kraken", "USD", 1_234.5) // fuera del par: intocable
	s.Wallets.Set("Kraken", "ETH", 2.0)
	s.Mu.Unlock()

	e.rebalanceWallets50_50(s)

	s.Mu.Lock()
	defer s.Mu.Unlock()
	if !almostEqual(s.Wallets.Get("Kraken", "USD"), 1_234.5) || !almostEqual(s.Wallets.Get("Kraken", "ETH"), 2.0) {
		t.Fatalf("el reequilibrio tocó un venue fuera del par: %+v", s.Wallets["Kraken"])
	}
	if !almostEqual(s.Wallets.Get("Binance", "USDT"), s.Wallets.Get("Bitso", "USD")) ||
		!almostEqual(s.Wallets.Get("Binance", "BTC"), s.Wallets.Get("Bitso", "BTC")) {
		t.Fatalf("el par clásico no quedó simétrico: %+v", s.Wallets)
	}
	// El patrimonio del PAR se conserva: quote + base×precio antes == después.
	btcPrice := getBTCPrice()
	pairWealth := s.Wallets.Get("Binance", "USDT") + s.Wallets.Get("Bitso", "USD") +
		(s.Wallets.Get("Binance", "BTC")+s.Wallets.Get("Bitso", "BTC"))*btcPrice
	wantPair := 9_000.0 + 100.0 + 0.5*btcPrice
	if !closeTo(pairWealth, wantPair, 1e-6) {
		t.Fatalf("patrimonio del par=%v, esperado %v", pairWealth, wantPair)
	}
}

// TestActivateCredit_ClassicPairOnly: la línea de crédito llega SOLO al par
// clásico (Kraken no recibe fondos prestados) y se devuelve completa al vencer.
func TestActivateCredit_ClassicPairOnly(t *testing.T) {
	e := &HFTEngine{Tracker: NewSpreadTracker()}
	s := newClientSession("credito-3-venues", nil)
	initSession(s, 10_000, 0.5)

	e.activateCreditSession(s)

	s.Mu.Lock()
	defer s.Mu.Unlock()
	if !s.Credit.Active {
		t.Fatal("crédito no activado")
	}
	if got := s.Credit.BorrowedUSD["Kraken"]; got != 0 {
		t.Fatalf("Kraken recibió préstamo: %v", got)
	}
	if !almostEqual(s.Credit.BorrowedUSD["Binance"], CreditLineUSD/2) ||
		!almostEqual(s.Credit.BorrowedUSD["Bitso"], CreditLineUSD/2) ||
		!almostEqual(s.Credit.BorrowedBTC["Binance"], CreditLineBTC/2) {
		t.Fatalf("préstamo mal repartido: USD=%+v BTC=%+v", s.Credit.BorrowedUSD, s.Credit.BorrowedBTC)
	}
	if s.Wallets.Get("Kraken", "USD") != 0 {
		t.Fatalf("el préstamo tocó la wallet de Kraken: %v", s.Wallets.Get("Kraken", "USD"))
	}
}

// TestVenueRegistry: el registro es la fuente de verdad.
func TestVenueRegistry(t *testing.T) {
	if !isKnownVenue("Binance") || !isKnownVenue("Bitso") {
		t.Fatal("los venues base deben estar registrados")
	}
	if isKnownVenue("FTX") {
		t.Fatal("venue inexistente reportado como conocido")
	}
	fees := defaultTakerFees()
	if len(fees) != len(Venues) {
		t.Fatalf("defaultTakerFees devolvió %d entradas, esperado %d", len(fees), len(Venues))
	}
	// Cada llamada debe devolver un mapa NUEVO (los snapshots no comparten mutables).
	fees["Binance"] = 0.99
	if defaultTakerFees()["Binance"] == 0.99 {
		t.Fatal("defaultTakerFees comparte el mapa entre llamadas")
	}
}
