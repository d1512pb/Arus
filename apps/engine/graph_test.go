package main

import (
	"math"
	"testing"
	"time"
)

func closeTo(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

// TestGraphTopology: la topología nace del registro de venues — 2 nodos por venue,
// 2 aristas de libro por venue, paridad entre quotes y swap de inventario entre bases.
func TestGraphTopology(t *testing.T) {
	g := NewLiquidityGraph()

	if len(g.nodes) != 2*len(Venues) {
		t.Fatalf("nodos=%d, esperado %d", len(g.nodes), 2*len(Venues))
	}
	// Con 2 venues: 4 libro + 2 paridad (USDT↔USD) + 2 swap (BTC↔BTC) = 8.
	if len(g.edges) != 8 {
		t.Fatalf("aristas=%d, esperado 8", len(g.edges))
	}

	usdtBin := MarketNode{Asset: "USDT", Venue: "Binance"}
	usdBit := MarketNode{Asset: "USD", Venue: "Bitso"}
	if e := g.edges[edgeKey(usdtBin, usdBit)]; e == nil || e.Kind != EdgeParity || e.Rate != 1 {
		t.Fatal("falta la arista de paridad USDT@Binance→USD@Bitso (tasa 1)")
	}
	btcBin := MarketNode{Asset: "BTC", Venue: "Binance"}
	btcBit := MarketNode{Asset: "BTC", Venue: "Bitso"}
	if e := g.edges[edgeKey(btcBin, btcBit)]; e == nil || e.Kind != EdgeInventorySwap {
		t.Fatal("falta el swap de inventario BTC@Binance→BTC@Bitso")
	}
}

// TestGraphUpdateBook: las aristas de libro toman tasa, fee efectivo (taker +
// slippage de referencia), liquidez y peso w = −log(tasa·(1−fee)).
func TestGraphUpdateBook(t *testing.T) {
	g := NewLiquidityGraph()
	now := time.Now()
	g.UpdateBook("Binance", TopOfBook{Ask: 60_000, Bid: 59_990, AskQty: 2.0, BidQty: 1.5, UpdatedAt: now})

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
	g.UpdateBook("Binance", TopOfBook{Ask: 60_000, Bid: 59_990, AskQty: 2, BidQty: 2, UpdatedAt: now})
	g.UpdateBook("Bitso", TopOfBook{Ask: 60_010, Bid: 60_000, AskQty: 1, BidQty: 1, UpdatedAt: now})

	if c := g.FindBestCycle(now); c != nil {
		t.Fatalf("ciclo fantasma detectado: %s", DescribeCycle(c))
	}
}

// TestFindBestCycle_Profitable: un spread grande entre venues produce el ciclo
// comprar@Binance → swap → vender@Bitso → paridad, con la tasa neta exacta.
func TestFindBestCycle_Profitable(t *testing.T) {
	g := NewLiquidityGraph()
	now := time.Now()
	g.UpdateBook("Binance", TopOfBook{Ask: 60_000, Bid: 59_990, AskQty: 2.0, BidQty: 1.5, UpdatedAt: now})
	g.UpdateBook("Bitso", TopOfBook{Ask: 60_650, Bid: 60_600, AskQty: 0.8, BidQty: 0.4, UpdatedAt: now})

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

	// La liquidez la acota la pierna menos líquida de LIBRO (bid de Bitso: 0.4).
	if !closeTo(c.MaxVolumeBTC, 0.4, 1e-12) {
		t.Fatalf("volumen máx=%v, esperado 0.4", c.MaxVolumeBTC)
	}

	// El ciclo debe pasar por los 4 nodos y cerrarse sobre sí mismo.
	if len(c.Edges) != 4 {
		t.Fatalf("longitud del ciclo=%d aristas, esperado 4 (%s)", len(c.Edges), DescribeCycle(c))
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

// TestFindBestCycle_StaleBookExcluded: un libro congelado no participa — el mismo
// spread gigante que antes era ciclo deja de serlo si Bitso lleva >10s sin datos.
func TestFindBestCycle_StaleBookExcluded(t *testing.T) {
	g := NewLiquidityGraph()
	now := time.Now()
	g.UpdateBook("Binance", TopOfBook{Ask: 60_000, Bid: 59_990, AskQty: 2, BidQty: 2, UpdatedAt: now})
	g.UpdateBook("Bitso", TopOfBook{Ask: 60_650, Bid: 60_600, AskQty: 1, BidQty: 1, UpdatedAt: now.Add(-MaxBookStaleness - time.Second)})

	if c := g.FindBestCycle(now); c != nil {
		t.Fatalf("el radar usó un libro muerto: %s", DescribeCycle(c))
	}
}

// TestSnapshotFor: el snapshot superpone los saldos del usuario en cada nodo y
// expone el supuesto de paridad; los nodos base se valoran al mid de su venue.
func TestSnapshotFor(t *testing.T) {
	g := NewLiquidityGraph()
	now := time.Now()
	g.UpdateBook("Binance", TopOfBook{Ask: 60_010, Bid: 59_990, AskQty: 2, BidQty: 2, UpdatedAt: now})
	g.UpdateBook("Bitso", TopOfBook{Ask: 60_650, Bid: 60_600, AskQty: 1, BidQty: 0.4, UpdatedAt: now})

	wallets := map[string]Wallet{
		"Binance": {USD: 5_000, BTC: 0.5},
		"Bitso":   {USD: 7_000, BTC: 0.25},
	}
	cycle := g.FindBestCycle(now)
	snap := g.SnapshotFor(wallets, cycle, now)

	if !snap.ParityAssumed {
		t.Fatal("el snapshot debe declarar el supuesto de paridad USDT≈USD")
	}
	if len(snap.Nodes) != 4 {
		t.Fatalf("nodos en snapshot=%d, esperado 4", len(snap.Nodes))
	}

	byID := map[string]GraphNodeWire{}
	for _, n := range snap.Nodes {
		byID[n.ID] = n
	}
	if n := byID["BTC@Binance"]; n.Balance != 0.5 || !closeTo(n.PriceUSD, 60_000, 1e-9) || !closeTo(n.BalanceUSD, 30_000, 1e-6) {
		t.Fatalf("BTC@Binance mal poblado: %+v", n)
	}
	if n := byID["USDT@Binance"]; n.Balance != 5_000 || n.PriceUSD != 1 {
		t.Fatalf("USDT@Binance mal poblado: %+v", n)
	}
	if n := byID["USD@Bitso"]; n.Balance != 7_000 {
		t.Fatalf("USD@Bitso mal poblado: %+v", n)
	}

	if snap.BestCycle == nil || !snap.BestCycle.Viable {
		t.Fatal("el snapshot debería llevar el ciclo detectado")
	}
	if snap.BestCycle.Path[0] != snap.BestCycle.Path[len(snap.BestCycle.Path)-1] {
		t.Fatal("el path del ciclo debe cerrarse (primero == último)")
	}
}
