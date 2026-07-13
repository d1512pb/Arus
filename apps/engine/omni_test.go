package main

import (
	"strings"
	"testing"
	"time"
)

// Tests de FASE 2 (refactor Probar Bot): pickOmniTarget y omniShiftNeeded eligen
// y dimensionan la ineficiencia sobre el universo ACTIVO del usuario. Funciones
// puras sobre TradingParameters + libros frescos en currentMarket.

func seedFreshBook(key string, ask, bid float64) {
	currentMarket.Update(key, TopOfBook{
		Ask: ask, Bid: bid, AskQty: 5, BidQty: 5, UpdatedAt: time.Now(),
	})
}

func seedBinanceTriangleBooks(t *testing.T) {
	t.Helper()
	seedFreshBook("Binance:BTC/USDT", 60_050, 60_000)
	seedFreshBook("Binance:ETH/BTC", 0.051, 0.0508)
	seedFreshBook("Binance:ETH/USDT", 3_060, 3_055)
}

func TestOmniNodeAllowedRespectsUniverse(t *testing.T) {
	p := DefaultTradingParameters()
	p.EnabledVenues = []string{"Binance", "Bitso"}
	p.EnabledAssets = []string{"USDT", "USD", "BTC"}

	if !omniNodeAllowed(p, "BTC", "Binance") {
		t.Fatal("BTC@Binance debía estar permitido")
	}
	if omniNodeAllowed(p, "ETH", "Binance") {
		t.Fatal("ETH fuera del universo debía rechazarse")
	}
	if omniNodeAllowed(p, "BTC", "Kraken") {
		t.Fatal("Kraken fuera del universo debía rechazarse")
	}
}

func TestPickOmniTargetTriangularOnBinance(t *testing.T) {
	seedBinanceTriangleBooks(t)
	p := DefaultTradingParameters()
	// Una sola casa: sin espacial posible; debe caer a triangular.
	p.EnabledVenues = []string{"Binance"}
	p.EnabledAssets = []string{"USDT", "BTC", "ETH"}
	wallets := Balances{"Binance": {"USDT": 50_000, "BTC": 1, "ETH": 2}}

	target, ok := pickOmniTarget(p, wallets)
	if !ok {
		t.Fatal("debía elegir un escenario triangular con libros frescos")
	}
	if target.kind != "triangular" {
		t.Fatalf("kind=%q, esperado triangular", target.kind)
	}
	if target.startVenue != "Binance" || target.startCash != "USDT" {
		t.Fatalf("inicio inesperado: %s@%s", target.startCash, target.startVenue)
	}
	if target.buyBook.Key() != "Binance:ETH/BTC" || target.sellBook.Key() != "Binance:ETH/USDT" {
		t.Fatalf("libros inyectados incorrectos: buy=%s sell=%s", target.buyBook.Key(), target.sellBook.Key())
	}
	if len(target.legFees) != 3 {
		t.Fatalf("triangular debía tener 3 fees, tiene %d", len(target.legFees))
	}
}

func TestCycleMatchesOmniTargetRejectsForeignBooks(t *testing.T) {
	now := time.Now()
	g := NewLiquidityGraph()
	g.UpdateBook("Binance:BTC/USDT", TopOfBook{Ask: 59_000, Bid: 58_900, AskQty: 2, BidQty: 2, UpdatedAt: now})
	g.UpdateBook("Bitso:BTC/USD", TopOfBook{Ask: 61_000, Bid: 60_900, AskQty: 2, BidQty: 2, UpdatedAt: now})
	seedFreshBook("Binance:BTC/USDT", 59_000, 58_900)
	seedFreshBook("Bitso:BTC/USD", 61_000, 60_900)

	p := DefaultTradingParameters()
	p.EnabledVenues = []string{"Binance", "Bitso"}
	p.EnabledAssets = []string{"USDT", "USD", "BTC", "ETH"}
	cycle := g.FindBestCycleFor(p, now)
	if cycle == nil {
		t.Fatal("esperado ciclo espacial BTC")
	}
	spatial, ok := pickOmniTarget(p, Balances{
		"Binance": {"USDT": 50_000, "BTC": 1},
		"Bitso":   {"USD": 40_000, "BTC": 1},
	})
	if !ok || spatial.kind != "espacial" {
		t.Fatal("setup espacial")
	}
	if !cycleMatchesOmniTarget(cycle, spatial) {
		t.Fatal("el ciclo espacial BTC debía matchear el target espacial")
	}
	// Target triangular de Binance no debe matchear un ciclo solo BTC entre casas.
	tri := omniTarget{
		kind:     "triangular",
		buyBook:  mustInstrument(t, "Binance:ETH/BTC"),
		sellBook: mustInstrument(t, "Binance:ETH/USDT"),
	}
	if cycleMatchesOmniTarget(cycle, tri) {
		t.Fatal("un ciclo BTC espacial no debe matchear un target triangular ETH")
	}
}

func mustInstrument(t *testing.T, key string) Instrument {
	t.Helper()
	in, ok := instrumentByKey(key)
	if !ok {
		t.Fatalf("instrumento %s no registrado", key)
	}
	return in
}

func TestPickOmniTargetPrefersSpatialWhenAvailable(t *testing.T) {
	seedBinanceTriangleBooks(t)
	seedFreshBook("Bitso:BTC/USD", 60_100, 60_050)

	p := DefaultTradingParameters()
	p.EnabledVenues = []string{"Binance", "Bitso"}
	p.EnabledAssets = []string{"USDT", "USD", "BTC", "ETH"}
	wallets := Balances{
		"Binance": {"USDT": 50_000, "BTC": 1, "ETH": 2},
		"Bitso":   {"USD": 40_000, "BTC": 1},
	}

	target, ok := pickOmniTarget(p, wallets)
	if !ok {
		t.Fatal("debía elegir escenario con libros frescos")
	}
	if target.kind != "espacial" {
		t.Fatalf("con ambas casas disponibles debe preferir espacial (got %q) para sesgar inventario", target.kind)
	}
}

func TestLockOmniTargetsOneWay(t *testing.T) {
	seedBinanceTriangleBooks(t)
	seedFreshBook("Bitso:BTC/USD", 60_100, 60_050)

	p := DefaultTradingParameters()
	p.EnabledVenues = []string{"Binance", "Bitso"}
	p.EnabledAssets = []string{"USDT", "USD", "BTC", "ETH"}
	wallets := Balances{
		"Binance": {"USDT": 50_000, "BTC": 1, "ETH": 2},
		"Bitso":   {"USD": 40_000, "BTC": 1},
	}
	all := preferSpatialOmniTargets(listOmniTargets(p, wallets))
	locked := lockOmniTargetsOneWay(all)
	if len(locked) == 0 {
		t.Fatal("lock no debe vaciar la lista")
	}
	buyV, sellV := locked[0].buyBook.Venue, locked[0].sellBook.Venue
	for _, tgt := range locked {
		if tgt.buyBook.Venue != buyV || tgt.sellBook.Venue != sellV {
			t.Fatalf("dirección mezclada: want %s→%s got %s→%s", buyV, sellV, tgt.buyBook.Venue, tgt.sellBook.Venue)
		}
	}
	// Sin lock habría ambas direcciones Binance↔Bitso.
	var dirs int
	seen := map[string]bool{}
	for _, tgt := range all {
		k := tgt.buyBook.Venue + "->" + tgt.sellBook.Venue
		if !seen[k] {
			seen[k] = true
			dirs++
		}
	}
	if dirs < 2 {
		t.Fatalf("setup: esperaba ≥2 direcciones espaciales antes del lock, got %d", dirs)
	}
}

func TestPickOmniTargetSpatialWhenNoMidAsset(t *testing.T) {
	seedFreshBook("Binance:BTC/USDT", 60_050, 60_000)
	seedFreshBook("Bitso:BTC/USD", 60_100, 60_050)

	p := DefaultTradingParameters()
	p.EnabledAssets = []string{"USDT", "USD", "BTC"} // sin ETH/SOL → sin triangular
	wallets := Balances{"Binance": {"USDT": 40_000}, "Bitso": {"USD": 30_000, "BTC": 1}}

	target, ok := pickOmniTarget(p, wallets)
	if !ok {
		t.Fatal("debía elegir escenario espacial Binance↔Bitso")
	}
	if target.kind != "espacial" {
		t.Fatalf("kind=%q, esperado espacial", target.kind)
	}
	if target.buyBook.Venue == target.sellBook.Venue {
		t.Fatal("espacial debe usar dos venues distintos")
	}
	if !strings.Contains(target.route, "Binance") || !strings.Contains(target.route, "Bitso") {
		t.Fatalf("ruta narrada debe nombrar ambas casas: %q", target.route)
	}
}

func TestPickOmniTargetFailsWithoutFreshBooks(t *testing.T) {
	// Libros presentes pero VIEJOS: pickOmniTarget exige frescura (staleness).
	stale := time.Now().Add(-MaxBookStaleness - time.Second)
	currentMarket.Update("Binance:BTC/USDT", TopOfBook{Ask: 60_050, Bid: 60_000, AskQty: 1, BidQty: 1, UpdatedAt: stale})
	currentMarket.Update("Binance:ETH/BTC", TopOfBook{Ask: 0.051, Bid: 0.0508, AskQty: 1, BidQty: 1, UpdatedAt: stale})
	currentMarket.Update("Binance:ETH/USDT", TopOfBook{Ask: 3_060, Bid: 3_055, AskQty: 1, BidQty: 1, UpdatedAt: stale})

	p := DefaultTradingParameters()
	wallets := Balances{"Binance": {"USDT": 50_000}}
	if _, ok := pickOmniTarget(p, wallets); ok {
		t.Fatal("libros congelados no deben producir escenario")
	}
}

func TestOmniShiftNeededPositiveWhenMarketAligned(t *testing.T) {
	seedBinanceTriangleBooks(t)
	p := DefaultTradingParameters()
	wallets := Balances{"Binance": {"USDT": 50_000}}
	target, ok := pickOmniTarget(p, wallets)
	if !ok {
		t.Fatal("setup triangular")
	}
	anchors := map[string]TopOfBook{}
	for _, in := range append([]Instrument{target.buyBook, target.sellBook}, target.support...) {
		b, _ := freshTopOfBook(in.Key())
		anchors[in.Key()] = b
	}
	shift := omniShiftNeeded(target, anchors, 0.002)
	if shift <= 0 {
		t.Fatalf("mercado alineado debe exigir desplazamiento positivo, got %v", shift)
	}
	if shift > 2*omniMaxShift {
		t.Fatalf("shift=%v fuera de rango razonable", shift)
	}
}

func TestOmniShiftNeededZeroWhenAlreadyProfitable(t *testing.T) {
	seedBinanceTriangleBooks(t)
	p := DefaultTradingParameters()
	wallets := Balances{"Binance": {"USDT": 50_000}}
	target, ok := pickOmniTarget(p, wallets)
	if !ok {
		t.Fatal("setup triangular")
	}
	anchors := map[string]TopOfBook{}
	for _, in := range append([]Instrument{target.buyBook, target.sellBook}, target.support...) {
		b, _ := freshTopOfBook(in.Key())
		anchors[in.Key()] = b
	}
	// Margen ridículamente bajo: si el mercado ya regala la ruta, shift=0.
	if shift := omniShiftNeeded(target, anchors, -0.5); shift != 0 {
		t.Fatalf("retorno negativo enorme debía dar shift=0, got %v", shift)
	}
}
