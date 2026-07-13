package main

import "testing"

func TestListOmniTargetsIncludesSpatialAndTriangular(t *testing.T) {
	seedBinanceTriangleBooks(t)
	seedFreshBook("Bitso:BTC/USD", 60_100, 60_050)

	p := DefaultTradingParameters()
	wallets := Balances{
		"Binance": {"USDT": 50_000, "BTC": 1, "ETH": 2},
		"Bitso":   {"USD": 30_000, "BTC": 1},
	}
	all := listOmniTargets(p, wallets)
	if len(all) < 2 {
		t.Fatalf("esperado ≥2 rutas (triangular+espacial), got %d", len(all))
	}
	var tri, esp int
	for _, tg := range all {
		switch tg.kind {
		case "triangular":
			tri++
		case "espacial":
			esp++
		}
	}
	if tri == 0 || esp == 0 {
		t.Fatalf("faltan tipos: triangular=%d espacial=%d", tri, esp)
	}
}

func TestStormParamsCapsOrderAndDisablesFoK(t *testing.T) {
	p := DefaultTradingParameters()
	p.MaxOrderSizeBTC = 0.05
	p.OrderFailureProb = 0.2
	sp := stormParams(p)
	if sp.MaxOrderSizeBTC != stormMaxOrderBTC {
		t.Fatalf("tope storm=%v, esperado %v", sp.MaxOrderSizeBTC, stormMaxOrderBTC)
	}
	if sp.OrderFailureProb != 0 {
		t.Fatal("FoK debía apagarse durante la tormenta")
	}
	if sp.MinNetProfitUSD != p.MinNetProfitUSD || sp.SlippageRate != p.SlippageRate {
		t.Fatal("margen/slippage del usuario no deben alterarse")
	}
}
