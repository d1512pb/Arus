package main

import (
	"math"
	"testing"
	"time"
)

func closeTo(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

// TestGraphTopology: la topología nace del registro de INSTRUMENTOS — un nodo por
// (venue, activo), 2 aristas de libro por instrumento, swaps de inventario entre
// mismo activo cross-venue y paridad entre activos declarados equivalentes.
func TestGraphTopology(t *testing.T) {
	g := NewLiquidityGraph()

	// Con el registro actual (Sprint C): Binance {USDT, BTC, ETH, SOL} + Bitso
	// {USD, BTC} + Kraken {USD, BTC, ETH} = 9 nodos.
	if len(g.nodes) != 9 {
		t.Fatalf("nodos=%d, esperado 9", len(g.nodes))
	}
	// Aristas: 9 instrumentos × 2 (libro) = 18 + swaps de inventario (BTC entre
	// los 3 venues = 6, ETH Binance↔Kraken = 2, USD Bitso↔Kraken = 2) + paridad
	// USDT@Binance↔{USD@Bitso, USD@Kraken} = 4. Total 32.
	if len(g.edges) != 32 {
		t.Fatalf("aristas=%d, esperado 32", len(g.edges))
	}

	usdtBin := MarketNode{Asset: "USDT", Venue: "Binance"}
	usdBit := MarketNode{Asset: "USD", Venue: "Bitso"}
	usdKrk := MarketNode{Asset: "USD", Venue: "Kraken"}
	if e := g.edges[edgeKey(usdtBin, usdBit)]; e == nil || e.Kind != EdgeParity || e.Rate != 1 {
		t.Fatal("falta la arista de paridad USDT@Binance→USD@Bitso (tasa 1)")
	}
	if e := g.edges[edgeKey(usdtBin, usdKrk)]; e == nil || e.Kind != EdgeParity || e.Rate != 1 {
		t.Fatal("falta la arista de paridad USDT@Binance→USD@Kraken (tasa 1)")
	}
	// USD es el MISMO activo en Bitso y Kraken: swap de inventario, no paridad.
	if e := g.edges[edgeKey(usdBit, usdKrk)]; e == nil || e.Kind != EdgeInventorySwap {
		t.Fatal("falta el swap de inventario USD@Bitso→USD@Kraken")
	}
	btcBin := MarketNode{Asset: "BTC", Venue: "Binance"}
	btcBit := MarketNode{Asset: "BTC", Venue: "Bitso"}
	btcKrk := MarketNode{Asset: "BTC", Venue: "Kraken"}
	if e := g.edges[edgeKey(btcBin, btcBit)]; e == nil || e.Kind != EdgeInventorySwap {
		t.Fatal("falta el swap de inventario BTC@Binance→BTC@Bitso")
	}
	if e := g.edges[edgeKey(btcBin, btcKrk)]; e == nil || e.Kind != EdgeInventorySwap {
		t.Fatal("falta el swap de inventario BTC@Binance→BTC@Kraken")
	}
	// El triángulo de Binance: aristas de libro ETH/BTC dentro del mismo venue.
	ethBin := MarketNode{Asset: "ETH", Venue: "Binance"}
	if e := g.edges[edgeKey(btcBin, ethBin)]; e == nil || e.Kind != EdgeOrderBook || e.BaseAsset != "ETH" {
		t.Fatal("falta la arista de libro BTC@Binance→ETH@Binance (comprar ETH/BTC)")
	}
	// El segundo triángulo de Binance (Sprint C): SOL/USDT y SOL/BTC.
	solBin := MarketNode{Asset: "SOL", Venue: "Binance"}
	if e := g.edges[edgeKey(btcBin, solBin)]; e == nil || e.Kind != EdgeOrderBook || e.BaseAsset != "SOL" {
		t.Fatal("falta la arista de libro BTC@Binance→SOL@Binance (comprar SOL/BTC)")
	}
	// El triángulo de Kraken: ETH/BTC dentro de Kraken.
	ethKrk := MarketNode{Asset: "ETH", Venue: "Kraken"}
	if e := g.edges[edgeKey(btcKrk, ethKrk)]; e == nil || e.Kind != EdgeOrderBook || e.BaseAsset != "ETH" {
		t.Fatal("falta la arista de libro BTC@Kraken→ETH@Kraken (comprar ETH/BTC)")
	}
	// ETH no existe en Bitso: no debe haber swap ETH hacia allá.
	if _, ok := g.edges[edgeKey(ethBin, MarketNode{Asset: "ETH", Venue: "Bitso"})]; ok {
		t.Fatal("swap de inventario hacia un nodo inexistente (ETH@Bitso)")
	}
	// SOL solo existe en Binance: sin aristas cross-venue.
	if _, ok := g.edges[edgeKey(solBin, MarketNode{Asset: "SOL", Venue: "Kraken"})]; ok {
		t.Fatal("swap de inventario hacia un nodo inexistente (SOL@Kraken)")
	}
}

// TestGraphUpdateBook: las aristas de libro toman tasa, fee efectivo (taker +
// slippage de referencia), liquidez y peso w = −log(tasa·(1−fee)).
func TestGraphUpdateBook(t *testing.T) {
	g := NewLiquidityGraph()
	now := time.Now()
	g.UpdateBook("Binance:BTC/USDT", TopOfBook{Ask: 60_000, Bid: 59_990, AskQty: 2.0, BidQty: 1.5, UpdatedAt: now})

	quote := MarketNode{Asset: "USDT", Venue: "Binance"}
	base := MarketNode{Asset: "BTC", Venue: "Binance"}
	effFee := DefaultBinanceFeeForTest() + DefaultSlippageRate

	buy := g.edges[edgeKey(quote, base)]
	if buy == nil {
		t.Fatal("no existe la arista de compra USDT@Binance→BTC@Binance")
	}
	if !closeTo(buy.Rate, 1.0/60_000, 1e-15) || !closeTo(buy.Fee, effFee, 1e-12) || buy.Liquidity != 2.0 {
		t.Fatalf("arista de compra mal poblada: %+v", buy)
	}
	wantW := -math.Log(buy.Rate * (1 - buy.Fee))
	if !closeTo(buy.Weight, wantW, 1e-12) {
		t.Fatalf("peso=%v, esperado %v", buy.Weight, wantW)
	}

	sell := g.edges[edgeKey(base, quote)]
	if sell == nil || !closeTo(sell.Rate, 59_990, 1e-9) || sell.Liquidity != 1.5 {
		t.Fatalf("arista de venta mal poblada: %+v", sell)
	}

	// Clave desconocida: no debe tocar nada ni hacer panic.
	g.UpdateBook("OKX:BTC/USD", TopOfBook{Ask: 1, Bid: 1, UpdatedAt: now})
}

// DefaultBinanceFeeForTest evita acoplarse a literales: lee el registro.
func DefaultBinanceFeeForTest() float64 {
	v, _ := venueByName("Binance")
	return v.DefaultTakerFee
}

// TestFindBestCycle_NoArb: con precios realistas (spread que no cubre fees) el
// radar NO inventa ciclos.
func TestFindBestCycle_NoArb(t *testing.T) {
	g := NewLiquidityGraph()
	now := time.Now()
	g.UpdateBook("Binance:BTC/USDT", TopOfBook{Ask: 60_000, Bid: 59_990, AskQty: 2, BidQty: 2, UpdatedAt: now})
	g.UpdateBook("Bitso:BTC/USD", TopOfBook{Ask: 60_010, Bid: 60_000, AskQty: 1, BidQty: 1, UpdatedAt: now})
	// Triángulo coherente con esos precios (ETH ≈ $3 000, ETH/BTC ≈ 0.05): sin arb.
	g.UpdateBook("Binance:ETH/USDT", TopOfBook{Ask: 3_000.3, Bid: 3_000, AskQty: 10, BidQty: 10, UpdatedAt: now})
	g.UpdateBook("Binance:ETH/BTC", TopOfBook{Ask: 0.05001, Bid: 0.05, AskQty: 10, BidQty: 10, UpdatedAt: now})

	if c := g.FindBestCycle(now); c != nil {
		t.Fatalf("ciclo fantasma detectado: %s", DescribeCycle(c))
	}
}

// TestFindBestCycle_Profitable: un spread grande entre venues produce el ciclo
// espacial comprar@Binance → swap → vender@Bitso → paridad, con la tasa neta exacta.
func TestFindBestCycle_Profitable(t *testing.T) {
	g := NewLiquidityGraph()
	now := time.Now()
	g.UpdateBook("Binance:BTC/USDT", TopOfBook{Ask: 60_000, Bid: 59_990, AskQty: 2.0, BidQty: 1.5, UpdatedAt: now})
	g.UpdateBook("Bitso:BTC/USD", TopOfBook{Ask: 60_650, Bid: 60_600, AskQty: 0.8, BidQty: 0.4, UpdatedAt: now})

	c := g.FindBestCycle(now)
	if c == nil {
		t.Fatal("el radar no detectó un ciclo claramente rentable")
	}

	binFee := DefaultBinanceFeeForTest() + DefaultSlippageRate
	bitV, _ := venueByName("Bitso")
	bitFee := bitV.DefaultTakerFee + DefaultSlippageRate
	wantNet := (60_600.0/60_000.0)*(1-binFee)*(1-bitFee) - 1
	if !closeTo(c.NetReturn, wantNet, 1e-9) {
		t.Fatalf("tasa neta=%v, esperada %v", c.NetReturn, wantNet)
	}

	// Piernas de libro homogéneas (base BTC): la liquidez la acota el bid de Bitso.
	if !closeTo(c.MaxVolumeBTC, 0.4, 1e-12) {
		t.Fatalf("volumen máx=%v, esperado 0.4", c.MaxVolumeBTC)
	}

	// Con Kraken en el grafo, Bellman-Ford puede rutear el swap BTC por un venue
	// de tránsito (BTC@Binance→BTC@Kraken→BTC@Bitso, ambas a costo cero): el neto
	// y el volumen no cambian. Exigimos las DOS piernas de libro del arbitraje y
	// que cualquier pierna extra sea de tránsito gratuito (tasa 1, fee 0).
	books := 0
	for _, e := range c.Edges {
		if e.Kind == EdgeOrderBook {
			books++
		} else if e.Rate != 1 || e.Fee != 0 {
			t.Fatalf("pierna de tránsito con costo: %+v (%s)", e, DescribeCycle(c))
		}
	}
	if books != 2 {
		t.Fatalf("piernas de libro=%d, esperado 2 (%s)", books, DescribeCycle(c))
	}
	seen := map[string]bool{}
	for _, e := range c.Edges {
		seen[e.From.ID()] = true
	}
	for _, want := range []string{"USDT@Binance", "BTC@Binance", "BTC@Bitso", "USD@Bitso"} {
		if !seen[want] {
			t.Fatalf("el ciclo no pasa por %s: %s", want, DescribeCycle(c))
		}
	}
	if c.Edges[0].From.ID() != c.Edges[len(c.Edges)-1].To.ID() {
		t.Fatalf("el ciclo no está cerrado: %s", DescribeCycle(c))
	}
}

// TestFindBestCycle_Triangular: el hito 2 en acción — un desalineamiento entre los
// TRES libros de Binance produce un ciclo triangular DENTRO del exchange, sin que
// Bitso participe (sus libros ni siquiera tienen datos).
func TestFindBestCycle_Triangular(t *testing.T) {
	g := NewLiquidityGraph()
	now := time.Now()
	// ETH/BTC cotiza "caro" respecto a los otros dos libros:
	// (1/askETHUSDT) · bidETHBTC · bidBTCUSDT = (1/3000)·0.0515·59990 ≈ 1.0298 > 1.
	g.UpdateBook("Binance:BTC/USDT", TopOfBook{Ask: 60_000, Bid: 59_990, AskQty: 2, BidQty: 2, UpdatedAt: now})
	g.UpdateBook("Binance:ETH/USDT", TopOfBook{Ask: 3_000, Bid: 2_999, AskQty: 10, BidQty: 10, UpdatedAt: now})
	g.UpdateBook("Binance:ETH/BTC", TopOfBook{Ask: 0.0516, Bid: 0.0515, AskQty: 5, BidQty: 5, UpdatedAt: now})

	c := g.FindBestCycle(now)
	if c == nil {
		t.Fatal("el radar no detectó el ciclo triangular")
	}

	// Todas las piernas deben ser de LIBRO y vivir en Binance.
	if len(c.Edges) != 3 {
		t.Fatalf("longitud del ciclo=%d, esperado 3 (%s)", len(c.Edges), DescribeCycle(c))
	}
	for _, e := range c.Edges {
		if e.Kind != EdgeOrderBook || e.From.Venue != "Binance" || e.To.Venue != "Binance" {
			t.Fatalf("pierna fuera del triángulo de Binance: %+v (%s)", e, DescribeCycle(c))
		}
	}

	// Tasa neta exacta: producto de tasas × (1−fee)³ − 1.
	fee := DefaultBinanceFeeForTest() + DefaultSlippageRate
	wantNet := (1.0/3_000.0)*0.0515*59_990.0*math.Pow(1-fee, 3) - 1
	if !closeTo(c.NetReturn, wantNet, 1e-9) {
		t.Fatalf("tasa neta=%v, esperada %v (%s)", c.NetReturn, wantNet, DescribeCycle(c))
	}

	// Bases mixtas (ETH y BTC): el volumen homogéneo no aplica → 0, pero la
	// capacidad en unidades del nodo de inicio SÍ se reporta (misma matemática
	// de mapeo por producto de tasas que planRotation). La pierna que acota es
	// ETH/BTC: 5 ETH de bid → 5/eff1 = 15 000/(1−fee) USDT de entrada.
	if c.MaxVolumeBTC != 0 {
		t.Fatalf("volumen=%v, esperado 0 (bases mixtas)", c.MaxVolumeBTC)
	}
	if c.StartAsset != "USDT" {
		t.Fatalf("StartAsset=%q, esperado USDT (nodo cash de inicio)", c.StartAsset)
	}
	wantCap := 15_000.0 / (1 - fee)
	if !closeTo(c.MaxStartAmount, wantCap, 1e-6) {
		t.Fatalf("capacidad de entrada=%v, esperada %v", c.MaxStartAmount, wantCap)
	}
}

// TestFindBestCycle_StaleBookExcluded: un libro congelado no participa — el mismo
// spread gigante que antes era ciclo deja de serlo si Bitso lleva >10s sin datos.
func TestFindBestCycle_StaleBookExcluded(t *testing.T) {
	g := NewLiquidityGraph()
	now := time.Now()
	g.UpdateBook("Binance:BTC/USDT", TopOfBook{Ask: 60_000, Bid: 59_990, AskQty: 2, BidQty: 2, UpdatedAt: now})
	g.UpdateBook("Bitso:BTC/USD", TopOfBook{Ask: 60_650, Bid: 60_600, AskQty: 1, BidQty: 1, UpdatedAt: now.Add(-MaxBookStaleness - time.Second)})

	if c := g.FindBestCycle(now); c != nil {
		t.Fatalf("el radar usó un libro muerto: %s", DescribeCycle(c))
	}
}

// TestSnapshotFor: el snapshot superpone los saldos del usuario en cada nodo,
// clasifica cash/crypto, expone el supuesto de paridad y valora al mid.
func TestSnapshotFor(t *testing.T) {
	g := NewLiquidityGraph()
	now := time.Now()
	g.UpdateBook("Binance:BTC/USDT", TopOfBook{Ask: 60_010, Bid: 59_990, AskQty: 2, BidQty: 2, UpdatedAt: now})
	g.UpdateBook("Binance:ETH/USDT", TopOfBook{Ask: 3_001, Bid: 2_999, AskQty: 10, BidQty: 10, UpdatedAt: now})
	g.UpdateBook("Bitso:BTC/USD", TopOfBook{Ask: 60_650, Bid: 60_600, AskQty: 1, BidQty: 0.4, UpdatedAt: now})

	wallets := Balances{
		"Binance": {"USDT": 5_000, "BTC": 0.5, "ETH": 1.5},
		"Bitso":   {"USD": 7_000, "BTC": 0.25},
	}
	cycle := g.FindBestCycle(now)
	snap := g.SnapshotFor(wallets, DefaultTradingParameters(), cycle, now)

	if !snap.ParityAssumed {
		t.Fatal("el snapshot debe declarar el supuesto de paridad USDT≈USD")
	}
	if len(snap.Nodes) != 9 {
		t.Fatalf("nodos en snapshot=%d, esperado 9 (3 venues)", len(snap.Nodes))
	}

	byID := map[string]GraphNodeWire{}
	for _, n := range snap.Nodes {
		byID[n.ID] = n
	}
	if n := byID["BTC@Binance"]; n.Balance != 0.5 || n.Kind != "crypto" || !closeTo(n.PriceUSD, 60_000, 1e-9) || !closeTo(n.BalanceUSD, 30_000, 1e-6) {
		t.Fatalf("BTC@Binance mal poblado: %+v", n)
	}
	if n := byID["USDT@Binance"]; n.Balance != 5_000 || n.Kind != "cash" || n.PriceUSD != 1 {
		t.Fatalf("USDT@Binance mal poblado: %+v", n)
	}
	if n := byID["USD@Bitso"]; n.Balance != 7_000 || n.Kind != "cash" {
		t.Fatalf("USD@Bitso mal poblado: %+v", n)
	}
	// ETH con saldo real (hito 3: wallets multi-activo) y precio de mercado.
	if n := byID["ETH@Binance"]; n.Balance != 1.5 || n.Kind != "crypto" || !closeTo(n.PriceUSD, 3_000, 1e-9) || !closeTo(n.BalanceUSD, 4_500, 1e-6) {
		t.Fatalf("ETH@Binance mal poblado: %+v", n)
	}

	if snap.BestCycle == nil || !snap.BestCycle.Viable {
		t.Fatal("el snapshot debería llevar el ciclo detectado")
	}
	if snap.BestCycle.Path[0] != snap.BestCycle.Path[len(snap.BestCycle.Path)-1] {
		t.Fatal("el path del ciclo debe cerrarse (primero == último)")
	}
}
