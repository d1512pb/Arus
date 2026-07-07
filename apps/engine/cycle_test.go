package main

import (
	"testing"
	"time"
)

// profitableSpatialGraph arma el escenario clásico: comprar en Binance, vender
// en Bitso con spread que sí cubre fees.
func profitableSpatialGraph(now time.Time) *LiquidityGraph {
	g := NewLiquidityGraph()
	g.UpdateBook("Binance:BTC/USDT", TopOfBook{Ask: 60_000, Bid: 59_990, AskQty: 2.0, BidQty: 1.5, UpdatedAt: now})
	g.UpdateBook("Bitso:BTC/USD", TopOfBook{Ask: 60_650, Bid: 60_600, AskQty: 0.8, BidQty: 0.4, UpdatedAt: now})
	return g
}

// TestPlanCycle_Spatial: el plan rota a un inicio CASH, respeta el tope del
// usuario en BTC-equivalente y su neto coincide con la tasa del ciclo.
func TestPlanCycle_Spatial(t *testing.T) {
	now := time.Now()
	g := profitableSpatialGraph(now)
	cycle := g.FindBestCycle(now)
	if cycle == nil {
		t.Fatal("sin ciclo espacial de partida")
	}

	balances := Balances{
		"Binance": {"USDT": 5_000, "BTC": 0.25},
		"Bitso":   {"USD": 5_000, "BTC": 0.25},
	}
	p := DefaultTradingParameters()
	const btcPrice = 60_000.0

	plan, err := planCycle(cycle, balances, p, btcPrice)
	if err != nil {
		t.Fatalf("plan rechazado: %v", err)
	}

	if !isCashAsset(plan.Start.Asset) {
		t.Fatalf("el ciclo no rotó a un inicio cash: %s", plan.Start.ID())
	}
	// Tope del usuario: 0.005 BTC × $60,000 = $300 de entrada (saldo y liquidez sobran).
	if !almostEqual(plan.StartAmount, p.MaxOrderSizeBTC*btcPrice) {
		t.Fatalf("entrada=%v, esperado el tope del usuario %v", plan.StartAmount, p.MaxOrderSizeBTC*btcPrice)
	}
	if !almostEqual(plan.VolumeBTCEquiv, p.MaxOrderSizeBTC) {
		t.Fatalf("volumen BTC-eq=%v, esperado %v", plan.VolumeBTCEquiv, p.MaxOrderSizeBTC)
	}
	// Neto = entrada × (tasa del ciclo): misma matemática que el radar.
	wantNet := plan.StartAmount * cycle.NetReturn
	if !almostEqual(plan.NetProfit, wantNet) {
		t.Fatalf("neto=%v, esperado %v", plan.NetProfit, wantNet)
	}
	// Consistencia de la cadena: lo que sale de la última pierna = entrada + neto.
	last := plan.Legs[len(plan.Legs)-1]
	if !almostEqual(last.Out, plan.StartAmount+plan.NetProfit) {
		t.Fatalf("cadena rota: out final=%v, esperado %v", last.Out, plan.StartAmount+plan.NetProfit)
	}
	if last.To != plan.Start {
		t.Fatalf("el ciclo no regresa al inicio: %s ≠ %s", last.To.ID(), plan.Start.ID())
	}
}

// TestPlanCycle_LiquidityBinds: cuando el bid de Bitso solo soporta 0.001 BTC,
// esa pierna acota la entrada (mapeada a unidades cash del inicio).
func TestPlanCycle_LiquidityBinds(t *testing.T) {
	now := time.Now()
	g := NewLiquidityGraph()
	g.UpdateBook("Binance:BTC/USDT", TopOfBook{Ask: 60_000, Bid: 59_990, AskQty: 2.0, BidQty: 1.5, UpdatedAt: now})
	g.UpdateBook("Bitso:BTC/USD", TopOfBook{Ask: 60_650, Bid: 60_600, AskQty: 0.8, BidQty: 0.001, UpdatedAt: now})
	cycle := g.FindBestCycle(now)
	if cycle == nil {
		t.Fatal("sin ciclo de partida")
	}

	balances := Balances{"Binance": {"USDT": 1e6}, "Bitso": {"USD": 1e6, "BTC": 1}}
	p := DefaultTradingParameters()
	p.MaxOrderSizeBTC = 10  // tope enorme: debe mandar la liquidez, no el usuario
	p.MinNetProfitUSD = 0.0 // margen fuera de la ecuación: aislamos la liquidez

	plan, err := planCycle(cycle, balances, p, 60_000)
	if err != nil {
		t.Fatalf("plan rechazado: %v", err)
	}
	// La pierna de venta en Bitso admite 0.001 BTC: la entrada X debe ser tal que
	// X·prod_hasta_esa_pierna = 0.001 BTC. Verificamos por la propia cadena:
	for _, leg := range plan.Legs {
		if leg.Kind == EdgeOrderBook && leg.From.ID() == "BTC@Bitso" {
			if !closeTo(leg.In, 0.001, 1e-12) {
				t.Fatalf("la pierna acotada usa %v BTC, esperado 0.001", leg.In)
			}
		}
	}
}

// TestPlanCycle_Inviable: sin saldo, o con un margen de usuario imposible, no hay plan.
func TestPlanCycle_Inviable(t *testing.T) {
	now := time.Now()
	g := profitableSpatialGraph(now)
	cycle := g.FindBestCycle(now)

	p := DefaultTradingParameters()

	// Sin saldo cash en el nodo de inicio.
	if _, err := planCycle(cycle, Balances{"Binance": {"USDT": 0}}, p, 60_000); err == nil {
		t.Fatal("plan aceptado sin fondos")
	}

	// Margen del usuario mayor que lo que el ciclo puede rendir.
	rich := Balances{"Binance": {"USDT": 5_000}, "Bitso": {"BTC": 1}}
	p.MinNetProfitUSD = 10_000
	if _, err := planCycle(cycle, rich, p, 60_000); err == nil {
		t.Fatal("plan aceptado por debajo del margen del usuario")
	}
}

// TestCommitCycle_ConservesBalances: tras ejecutar, el nodo de inicio gana
// exactamente el neto; los nodos intermedios quedan como estaban (todo lo que
// entra sale) y el PnL de la sesión sube por el neto.
func TestCommitCycle_ConservesBalances(t *testing.T) {
	now := time.Now()
	g := profitableSpatialGraph(now)
	cycle := g.FindBestCycle(now)

	s := newClientSession("ciclo", nil)
	initSession(s, 10_000, 0.5) // 5 000 quote + 0.25 BTC por venue

	s.Mu.Lock()
	before := s.Wallets.Clone()
	wealthBefore := s.TotalWealth
	s.Mu.Unlock()

	plan, err := planCycle(cycle, before, DefaultTradingParameters(), 60_000)
	if err != nil {
		t.Fatalf("plan rechazado: %v", err)
	}
	if !commitCycle(s, plan) {
		t.Fatal("commit rechazado con fondos suficientes")
	}

	s.Mu.Lock()
	defer s.Mu.Unlock()
	start := plan.Start
	gotStart := s.Wallets.Get(start.Venue, string(start.Asset))
	wantStart := before.Get(start.Venue, string(start.Asset)) + plan.NetProfit
	if !closeTo(gotStart, wantStart, 1e-6) {
		t.Fatalf("inicio: %v, esperado %v (+neto)", gotStart, wantStart)
	}
	// Nodos intermedios sin residuo (BTC en ambos venues, USD en Bitso).
	for _, check := range []struct{ venue, asset string }{
		{"Binance", "BTC"}, {"Bitso", "BTC"}, {"Bitso", "USD"},
	} {
		got := s.Wallets.Get(check.venue, check.asset)
		want := before.Get(check.venue, check.asset)
		if !closeTo(got, want, 1e-9) {
			t.Fatalf("residuo en %s@%s: %v, esperado %v", check.asset, check.venue, got, want)
		}
	}
	if !closeTo(s.TotalWealth, wealthBefore+plan.NetProfit, 1e-9) {
		t.Fatalf("TotalWealth=%v, esperado %v", s.TotalWealth, wealthBefore+plan.NetProfit)
	}
}

// TestCommitCycle_HardBlock: si los fondos cambiaron entre plan y commit, el
// commit se rechaza sin tocar nada.
func TestCommitCycle_HardBlock(t *testing.T) {
	now := time.Now()
	g := profitableSpatialGraph(now)
	cycle := g.FindBestCycle(now)

	s := newClientSession("ciclo-block", nil)
	initSession(s, 10_000, 0.5)

	s.Mu.Lock()
	snapshot := s.Wallets.Clone()
	s.Mu.Unlock()

	plan, err := planCycle(cycle, snapshot, DefaultTradingParameters(), 60_000)
	if err != nil {
		t.Fatalf("plan rechazado: %v", err)
	}

	// El mundo cambió: alguien vació el nodo de inicio.
	s.Mu.Lock()
	s.Wallets.Set(plan.Start.Venue, string(plan.Start.Asset), 0)
	wealth := s.TotalWealth
	s.Mu.Unlock()

	if commitCycle(s, plan) {
		t.Fatal("commit aceptado sin fondos (hard block roto)")
	}
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if s.TotalWealth != wealth {
		t.Fatal("el commit rechazado alteró el patrimonio")
	}
}

// TestFindBestCycleFor_UserFees: la MISMA oportunidad existe para un usuario con
// fees estándar y desaparece para uno cuyas comisiones (2 %) se la comen — la
// detección personalizada usa los pesos DEL usuario.
func TestFindBestCycleFor_UserFees(t *testing.T) {
	now := time.Now()
	g := profitableSpatialGraph(now)

	standard := DefaultTradingParameters()
	if g.FindBestCycleFor(standard, now) == nil {
		t.Fatal("el usuario con fees estándar debería ver el ciclo")
	}

	expensive := DefaultTradingParameters()
	expensive.TakerFees = map[string]float64{"Binance": 0.02, "Bitso": 0.02}
	if c := g.FindBestCycleFor(expensive, now); c != nil {
		t.Fatalf("con fees del 2%% el ciclo no debería existir: %s", DescribeCycle(c))
	}
}

// TestFindBestCycleFor_UniversePruning: la poda del universo.
func TestFindBestCycleFor_UniversePruning(t *testing.T) {
	now := time.Now()
	// Espacial rentable Y triángulo de Binance rentable, ambos activos.
	g := profitableSpatialGraph(now)
	g.UpdateBook("Binance:ETH/USDT", TopOfBook{Ask: 3_000, Bid: 2_999, AskQty: 10, BidQty: 10, UpdatedAt: now})
	g.UpdateBook("Binance:ETH/BTC", TopOfBook{Ask: 0.0516, Bid: 0.0515, AskQty: 5, BidQty: 5, UpdatedAt: now})

	// Universo restringido a Binance: el ciclo espacial (que necesita Bitso)
	// queda podado; el que aparezca debe vivir 100 % en Binance.
	p := DefaultTradingParameters()
	p.EnabledVenues = []string{"Binance"}
	c := g.FindBestCycleFor(p, now)
	if c == nil {
		t.Fatal("el triángulo de Binance debería sobrevivir a la poda")
	}
	for _, e := range c.Edges {
		if e.From.Venue != "Binance" || e.To.Venue != "Binance" {
			t.Fatalf("la poda dejó pasar un nodo fuera del universo: %s", DescribeCycle(c))
		}
	}

	// Universo sin ETH: el triángulo muere; con ambos venues, el espacial vive.
	p2 := DefaultTradingParameters()
	p2.EnabledAssets = []string{"USDT", "USD", "BTC"}
	c2 := g.FindBestCycleFor(p2, now)
	if c2 == nil {
		t.Fatal("el ciclo espacial debería sobrevivir sin ETH")
	}
	for _, e := range c2.Edges {
		if e.From.Asset == "ETH" || e.To.Asset == "ETH" {
			t.Fatalf("la poda dejó pasar ETH: %s", DescribeCycle(c2))
		}
	}
}

// TestSanitizeTradingParams_Universe: venues/activos desconocidos fuera,
// duplicados fuera, autopiloto pasa tal cual.
func TestSanitizeTradingParams_Universe(t *testing.T) {
	p := sanitizeTradingParams(TradingParameters{
		EnabledVenues:  []string{"Binance", "FTX", "Binance"},
		EnabledAssets:  []string{"BTC", "DOGE", "BTC", "USDT"},
		RadarAutopilot: true,
	})
	if len(p.EnabledVenues) != 1 || p.EnabledVenues[0] != "Binance" {
		t.Fatalf("venues mal saneados: %v", p.EnabledVenues)
	}
	if len(p.EnabledAssets) != 2 || p.EnabledAssets[0] != "BTC" || p.EnabledAssets[1] != "USDT" {
		t.Fatalf("activos mal saneados: %v", p.EnabledAssets)
	}
	if !p.RadarAutopilot {
		t.Fatal("autopiloto perdido en la sanitización")
	}
}
