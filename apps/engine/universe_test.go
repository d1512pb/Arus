package main

import "testing"

func TestAnalyzeUniverseFullCatalogOK(t *testing.T) {
	cap := analyzeUniverse(DefaultTradingParameters())
	if !cap.OK {
		t.Fatalf("catálogo completo debía ser operable: %s", cap.Reason)
	}
	if !cap.Spatial || !cap.Triangular {
		t.Fatalf("esperado espacial+triangular, got spatial=%v triangular=%v", cap.Spatial, cap.Triangular)
	}
}

func TestAnalyzeUniverseSingleVenueBTCOnlyBlocked(t *testing.T) {
	p := DefaultTradingParameters()
	p.EnabledVenues = []string{"Binance"}
	p.EnabledAssets = []string{"USDT", "BTC"} // sin ETH/SOL → sin triangular; 1 casa → sin espacial
	cap := analyzeUniverse(p)
	if cap.OK {
		t.Fatal("1 casa + solo BTC no debía admitir arbitraje")
	}
	if cap.Reason == "" {
		t.Fatal("debía explicar el motivo")
	}
}

func TestAnalyzeUniverseSingleVenueWithETHTriangular(t *testing.T) {
	p := DefaultTradingParameters()
	p.EnabledVenues = []string{"Binance"}
	p.EnabledAssets = []string{"USDT", "BTC", "ETH"}
	cap := analyzeUniverse(p)
	if !cap.OK || !cap.Triangular {
		t.Fatalf("Binance+ETH debía permitir triangular: ok=%v tri=%v (%s)", cap.OK, cap.Triangular, cap.Reason)
	}
	if cap.Spatial {
		t.Fatal("una sola casa no es espacial")
	}
}

func TestAnalyzeUniverseTwoVenuesSpatial(t *testing.T) {
	p := DefaultTradingParameters()
	p.EnabledVenues = []string{"Binance", "Bitso"}
	p.EnabledAssets = []string{"USDT", "USD", "BTC"}
	cap := analyzeUniverse(p)
	if !cap.OK || !cap.Spatial {
		t.Fatalf("Binance+Bitso+BTC debía permitir espacial: %s", cap.Reason)
	}
}

func TestVenueHasAsset(t *testing.T) {
	if !venueHasAsset("Binance", "ETH") {
		t.Fatal("Binance debe tener ETH")
	}
	if !venueHasAsset("Binance", "USDT") {
		t.Fatal("Binance debe tener USDT")
	}
	if venueHasAsset("Bitso", "ETH") {
		t.Fatal("Bitso no publica ETH")
	}
}
