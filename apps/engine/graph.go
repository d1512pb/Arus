package main

import (
	"fmt"
	"math"
	"sync"
	"time"
)

// graph.go — FASE 2: MOTOR DE ARBITRAJE OMNIDIRECCIONAL (grafo de liquidez).
//
// El mercado se modela como un grafo dirigido (ver docs/FASE2-GRAFO.md):
//   - un NODO es un activo en un lugar concreto: BTC@Binance, ETH@Binance, USD@Bitso…
//   - una ARISTA es una forma de convertir un activo en otro (libro de órdenes,
//     paridad entre stablecoins, swap de inventario pre-fondeado).
//   - una OPORTUNIDAD es un ciclo cuyo producto de tasas efectivas supera 1 —
//     equivalentemente, un ciclo de peso NEGATIVO con w = −log(tasa × (1 − fee)),
//     detectable con Bellman-Ford.
//
// Desde el hito 2, la topología nace del registro de INSTRUMENTOS (venues.go): un
// venue puede publicar N libros. Con el triángulo de Binance (BTC/USDT · ETH/USDT
// · ETH/BTC) el radar detecta arbitraje TRIANGULAR dentro de un solo exchange,
// además del espacial entre exchanges — con datos 100 % reales.
//
// El grafo opera en MODO RADAR: detecta y narra; la EJECUCIÓN sigue a cargo del
// núcleo de dos venues probado (executeForSession). Ejecutar ciclos arbitrarios
// exige wallets multi-activo (hito 3, ver doc).

// Asset identifica una moneda o token ("BTC", "USD", "USDT", "ETH"…).
// USDT y USD son activos DISTINTOS en el grafo: el basis entre ellos deja de ser
// una suposición implícita y pasa a ser una arista visible y etiquetada.
type Asset string

// MarketNode es un nodo del grafo: un activo custodiado en un venue concreto.
type MarketNode struct {
	Asset Asset
	Venue string // Venue.Name del registro (venues.go)
}

// ID es el identificador estable del nodo en el wire y en los mapas ("BTC@Binance").
func (n MarketNode) ID() string { return string(n.Asset) + "@" + n.Venue }

// EdgeKind clasifica las conversiones posibles entre nodos.
type EdgeKind int

const (
	// EdgeOrderBook convierte activos DENTRO de un venue vía un libro de órdenes
	// (p. ej. USDT@Binance → BTC@Binance comprando al ask). Su tasa viene del
	// top-of-book del FeedAdapter; su fee combina taker de referencia + slippage.
	EdgeOrderBook EdgeKind = iota
	// EdgeParity conecta stablecoins/monedas declaradas equivalentes entre venues
	// (USDT@Binance ↔ USD@Bitso) a tasa 1. Hace VISIBLE el supuesto AssumeUSDTParity;
	// cuando exista un libro USDT/USD real, esta arista tomará su precio.
	EdgeParity
	// EdgeInventorySwap conecta el MISMO activo entre venues a tasa 1 y costo 0:
	// modela el arbitraje PRE-FONDEADO (hay inventario en ambos lados, la compra y
	// la venta son simultáneas y el traslado real se difiere al reequilibrio).
	// En producción esta arista cargará el costo amortizado de reequilibrar.
	EdgeInventorySwap
	// EdgeTransfer (reservado): traslado on-chain real con fee de retiro + fee de
	// red + ~30 min de latencia. Se incorporará cuando el radar modele rutas lentas.
	EdgeTransfer
)

// wireKind es la etiqueta legible del tipo de arista para la UI.
func (k EdgeKind) wireKind() string {
	switch k {
	case EdgeOrderBook:
		return "book"
	case EdgeParity:
		return "parity"
	case EdgeInventorySwap:
		return "inventory"
	default:
		return "transfer"
	}
}

// Edge es una conversión dirigida entre dos nodos del grafo con su costo total.
type Edge struct {
	Kind EdgeKind
	From MarketNode
	To   MarketNode

	// Rate: unidades de To obtenidas por 1 unidad de From, ANTES de fees.
	Rate float64
	// Fee: fracción cobrada por usar la arista (taker + slippage estimado de referencia).
	Fee float64
	// Liquidity acota el volumen que soporta la arista al Rate visible, en unidades
	// de BaseAsset; 0 = sin dato/sin límite práctico (paridad, swaps de inventario).
	Liquidity float64
	// BaseAsset es el activo en que se expresa Liquidity (el base del libro).
	// Vacío en aristas que no son de libro.
	BaseAsset string
	// Latency estima cuánto tarda la conversión (≈0 salvo transfers on-chain).
	Latency time.Duration
	// UpdatedAt habilita el control de staleness: una arista de libro vieja no
	// participa en la búsqueda de ciclos (mismo principio que la Fase 1).
	UpdatedAt time.Time

	// Weight = −log(Rate × (1 − Fee)). Un ciclo con suma de pesos < 0 equivale a
	// un producto de tasas efectivas > 1: arbitraje. Se precalcula al actualizar
	// la arista para que la búsqueda sea aritmética pura (sin log en el hot path).
	Weight float64
}

// recomputeWeight actualiza el peso tras cambiar Rate/Fee.
func (e *Edge) recomputeWeight() {
	eff := e.Rate * (1 - e.Fee)
	if eff <= 0 {
		e.Weight = math.Inf(1) // arista inutilizable hasta tener datos
		return
	}
	e.Weight = -math.Log(eff)
}

// Cycle es una oportunidad detectada: la secuencia de aristas que sale de un nodo
// y regresa a él con tasa neta positiva tras todos los fees.
type Cycle struct {
	Edges []Edge
	// NetReturn es la tasa neta del ciclo (0.001 = +0.1 % por vuelta de capital).
	NetReturn float64
	// MaxVolumeBTC es el volumen ejecutable acotado por la arista de libro menos
	// líquida, SOLO cuando todas las piernas de libro comparten el mismo activo
	// base (p. ej. el ciclo espacial BTC). En ciclos con bases mixtas vale 0;
	// la capacidad real viaja en MaxStartAmount/StartAsset.
	MaxVolumeBTC float64
	// MaxStartAmount es la capacidad del ciclo expresada en unidades de su nodo
	// de inicio (StartAsset): cuánto puede ENTRAR al ciclo sin exceder la
	// liquidez de ninguna pierna, con la misma matemática de mapeo por producto
	// de tasas que usa planRotation. Cubre los ciclos triangulares (bases
	// mixtas), donde MaxVolumeBTC no aplica. 0 = sin dato de liquidez.
	MaxStartAmount float64
	StartAsset     string
}

// Universe es el subgrafo PERSONAL de una sesión: los venues y activos con los que
// el usuario decidió jugar. La búsqueda de ciclos corre solo sobre aristas cuyo
// From y To pertenecen al universo — la personalización es una poda del grafo.
// (El radar actual usa el universo completo; la poda por sesión llega con la
// ejecución de ciclos.)
type Universe struct {
	Venues []string
	Assets []Asset
}

// Contains informa si un nodo pertenece al universo del usuario.
func (u Universe) Contains(n MarketNode) bool {
	venueOK := false
	for _, v := range u.Venues {
		if v == n.Venue {
			venueOK = true
			break
		}
	}
	if !venueOK {
		return false
	}
	for _, a := range u.Assets {
		if a == n.Asset {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// LiquidityGraph — el grafo vivo
// ---------------------------------------------------------------------------

// LiquidityGraph mantiene el grafo global del mercado. Lo escriben las goroutines
// de ingesta (UpdateBook por tick, O(1): solo las 2 aristas del libro afectado) y
// lo leen el detector de ciclos y los snapshots hacia la UI — todo bajo mutex.
type LiquidityGraph struct {
	mu    sync.Mutex
	nodes []MarketNode
	edges map[string]*Edge // clave: From.ID()+">"+To.ID()
}

func edgeKey(from, to MarketNode) string { return from.ID() + ">" + to.ID() }

// NewLiquidityGraph construye la topología desde el registro de INSTRUMENTOS:
//   - 1 nodo por (venue, activo) que aparezca en algún libro,
//   - 2 aristas de libro por instrumento (comprar: quote→base; vender: base→quote),
//   - swaps de inventario entre el MISMO activo en venues distintos (pre-fondeado),
//   - paridad entre activos declarados equivalentes (USDT≈USD, visible).
//
// Agregar un instrumento o un venue al registro agrega sus nodos y aristas aquí
// sin tocar nada más.
func NewLiquidityGraph() *LiquidityGraph {
	g := &LiquidityGraph{edges: make(map[string]*Edge)}

	seen := map[string]bool{}
	addNode := func(n MarketNode) {
		if !seen[n.ID()] {
			seen[n.ID()] = true
			g.nodes = append(g.nodes, n)
		}
	}

	// Nodos y aristas de libro, por instrumento.
	for _, in := range Instruments {
		base := MarketNode{Asset: Asset(in.Base), Venue: in.Venue}
		quote := MarketNode{Asset: Asset(in.Quote), Venue: in.Venue}
		addNode(quote)
		addNode(base)

		buy := &Edge{Kind: EdgeOrderBook, From: quote, To: base, BaseAsset: in.Base}
		sell := &Edge{Kind: EdgeOrderBook, From: base, To: quote, BaseAsset: in.Base}
		buy.recomputeWeight()
		sell.recomputeWeight()
		g.edges[edgeKey(quote, base)] = buy
		g.edges[edgeKey(base, quote)] = sell
	}

	// Conexiones entre venues (ambas direcciones): mismo activo → swap de
	// inventario; activos en paridad declarada → arista de paridad.
	for _, from := range g.nodes {
		for _, to := range g.nodes {
			if from.Venue == to.Venue || g.edges[edgeKey(from, to)] != nil {
				continue
			}
			var kind EdgeKind
			switch {
			case from.Asset == to.Asset:
				kind = EdgeInventorySwap
			case assetsParity(string(from.Asset), string(to.Asset)):
				kind = EdgeParity
			default:
				continue
			}
			e := &Edge{Kind: kind, From: from, To: to, Rate: 1}
			e.recomputeWeight()
			g.edges[edgeKey(from, to)] = e
		}
	}

	return g
}

// UpdateBook refresca las 2 aristas del instrumento (clave "Venue:BASE/QUOTE")
// con su top-of-book. O(1) por tick. El fee efectivo de referencia = taker del
// registro + slippage estimado por defecto (la vista personalizada por sesión
// llega con la ejecución de ciclos; el radar usa referencia y lo declara en la UI).
func (g *LiquidityGraph) UpdateBook(instrKey string, book TopOfBook) {
	in, ok := instrumentByKey(instrKey)
	if !ok || book.Ask <= 0 || book.Bid <= 0 {
		return
	}
	v, ok := venueByName(in.Venue)
	if !ok {
		return
	}
	base := MarketNode{Asset: Asset(in.Base), Venue: in.Venue}
	quote := MarketNode{Asset: Asset(in.Quote), Venue: in.Venue}
	effFee := v.DefaultTakerFee + DefaultSlippageRate

	g.mu.Lock()
	defer g.mu.Unlock()

	if buy := g.edges[edgeKey(quote, base)]; buy != nil {
		buy.Rate = 1 / book.Ask // 1 quote compra 1/ask unidades de base
		buy.Fee = effFee
		buy.Liquidity = book.AskQty
		buy.UpdatedAt = book.UpdatedAt
		buy.recomputeWeight()
	}
	if sell := g.edges[edgeKey(base, quote)]; sell != nil {
		sell.Rate = book.Bid // 1 base vende a bid unidades de quote
		sell.Fee = effFee
		sell.Liquidity = book.BidQty
		sell.UpdatedAt = book.UpdatedAt
		sell.recomputeWeight()
	}
}

// activeEdgesLocked devuelve las aristas utilizables en este instante: libros
// frescos (staleness) y con datos; paridad/inventario siempre. Con g.mu tomado.
func (g *LiquidityGraph) activeEdgesLocked(now time.Time) []Edge {
	active := make([]Edge, 0, len(g.edges))
	for _, e := range g.edges {
		if math.IsInf(e.Weight, 1) {
			continue // sin datos aún
		}
		if e.Kind == EdgeOrderBook && now.Sub(e.UpdatedAt) > MaxBookStaleness {
			continue // libro muerto: no participa (mismo criterio que la Fase 1)
		}
		active = append(active, *e)
	}
	return active
}

// cycleEpsilon evita reportar "ciclos" nacidos del ruido de punto flotante.
const cycleEpsilon = 1e-9

// FindBestCycle busca un ciclo de peso negativo (arbitraje) con Bellman-Ford
// sobre las aristas activas, con los FEES DE REFERENCIA del registro. Es la
// vista global del radar. Devuelve nil si no hay ciclo rentable ahora mismo.
func (g *LiquidityGraph) FindBestCycle(now time.Time) *Cycle {
	g.mu.Lock()
	edges := g.activeEdgesLocked(now)
	nodeCount := len(g.nodes)
	g.mu.Unlock()
	return findNegativeCycle(edges, nodeCount)
}

// FindBestCycleFor es la detección PERSONALIZADA del hito 3: mismas aristas
// activas, pero con los fees DEL USUARIO en los pesos y el grafo PODADO a su
// universo (venues/activos habilitados). Dos usuarios ven ciclos distintos en
// el mismo mercado — la personalización es literalmente la forma del espacio
// de búsqueda.
func (g *LiquidityGraph) FindBestCycleFor(p TradingParameters, now time.Time) *Cycle {
	g.mu.Lock()
	all := g.activeEdgesLocked(now)
	nodeCount := len(g.nodes)
	g.mu.Unlock()

	edges := make([]Edge, 0, len(all))
	for _, e := range all {
		if !p.universeAllows(e.From) || !p.universeAllows(e.To) {
			continue // poda: fuera del universo del usuario
		}
		if e.Kind == EdgeOrderBook {
			// Pesos con el fee de ESTE usuario (+ su slippage estimado).
			e.Fee = p.takerFee(e.From.Venue) + p.SlippageRate
			e.recomputeWeight()
		}
		edges = append(edges, e)
	}
	return findNegativeCycle(edges, nodeCount)
}

// findNegativeCycle es el núcleo compartido: Bellman-Ford con fuente virtual +
// extracción del ciclo por predecesores + volumen homogéneo. Con grafos de este
// tamaño (≤ 8 nodos) la búsqueda completa es de microsegundos; si el universo
// crece, el doc de Fase 2 contempla SPFA incremental.
func findNegativeCycle(edges []Edge, nodeCount int) *Cycle {
	if len(edges) == 0 || nodeCount == 0 {
		return nil
	}

	// Bellman-Ford con "fuente virtual": dist 0 en todos los nodos detecta
	// cualquier ciclo negativo alcanzable en el grafo.
	dist := make(map[string]float64, nodeCount)
	pred := make(map[string]Edge, nodeCount)

	var lastRelaxed string
	for i := 0; i < nodeCount; i++ {
		lastRelaxed = ""
		for _, e := range edges {
			from, to := e.From.ID(), e.To.ID()
			if dist[from]+e.Weight < dist[to]-cycleEpsilon {
				dist[to] = dist[from] + e.Weight
				pred[to] = e
				lastRelaxed = to
			}
		}
		if lastRelaxed == "" {
			return nil // convergió: no hay ciclo negativo
		}
	}

	// lastRelaxed es alcanzable desde un ciclo negativo; retrocedemos nodeCount
	// pasos para garantizar estar DENTRO del ciclo y luego lo recolectamos.
	v := lastRelaxed
	for i := 0; i < nodeCount; i++ {
		v = pred[v].From.ID()
	}

	var cycleEdges []Edge
	sum := 0.0
	u := v
	for {
		e, ok := pred[u]
		if !ok {
			return nil // defensa: cadena de predecesores rota
		}
		cycleEdges = append([]Edge{e}, cycleEdges...)
		sum += e.Weight
		u = e.From.ID()
		if u == v {
			break
		}
		if len(cycleEdges) > nodeCount {
			return nil // defensa: no debería ocurrir
		}
	}

	net := math.Exp(-sum) - 1
	if net <= cycleEpsilon {
		return nil
	}

	// Volumen máximo: solo homogéneo si todas las piernas de libro comparten base.
	maxVol := 0.0
	baseAsset := ""
	homogeneous := true
	for _, e := range cycleEdges {
		if e.Kind != EdgeOrderBook {
			continue
		}
		if baseAsset == "" {
			baseAsset = e.BaseAsset
		} else if e.BaseAsset != baseAsset {
			homogeneous = false
		}
		if e.Liquidity > 0 && (maxVol == 0 || e.Liquidity < maxVol) {
			maxVol = e.Liquidity
		}
	}
	if !homogeneous {
		maxVol = 0 // bases mixtas (triangular): el volumen homogéneo no aplica
	}

	start, capacity := cycleStartCapacity(cycleEdges)
	return &Cycle{
		Edges: cycleEdges, NetReturn: net, MaxVolumeBTC: maxVol,
		MaxStartAmount: capacity, StartAsset: start,
	}
}

// cycleStartCapacity calcula cuánto puede ENTRAR al ciclo (en unidades de su nodo
// de inicio) sin exceder la liquidez visible de ninguna pierna de libro — la misma
// matemática de mapeo por producto de tasas que planRotation (cycle.go), pero sin
// saldos ni parámetros: es capacidad PURA de mercado. El ciclo se rota a su primer
// nodo cash (USD/USDT) si lo tiene, para que la cifra se lea como "entrada ≈ USD";
// un ciclo sin nodo cash (p. ej. ETH↔BTC entre venues) reporta en su primer activo.
func cycleStartCapacity(edges []Edge) (startAsset string, capacity float64) {
	if len(edges) == 0 {
		return "", 0
	}
	rot := 0
	for i, e := range edges {
		if isCashAsset(e.From.Asset) {
			rot = i
			break
		}
	}
	rotated := append(append([]Edge{}, edges[rot:]...), edges[:rot]...)

	maxStart := math.Inf(1)
	prod := 1.0
	for _, e := range rotated {
		eff := e.Rate * (1 - e.Fee)
		if eff <= 0 {
			return string(rotated[0].From.Asset), 0
		}
		if e.Kind == EdgeOrderBook && e.Liquidity > 0 {
			var limit float64
			if string(e.To.Asset) == e.BaseAsset {
				// Compra: el volumen en base SALE de la pierna → X·prod·eff ≤ liq.
				limit = e.Liquidity / (prod * eff)
			} else {
				// Venta: el volumen en base ENTRA a la pierna → X·prod ≤ liq.
				limit = e.Liquidity / prod
			}
			if limit < maxStart {
				maxStart = limit
			}
		}
		prod *= eff
	}
	if math.IsInf(maxStart, 1) {
		maxStart = 0 // ninguna pierna reportó liquidez: sin dato, no "infinito"
	}
	return string(rotated[0].From.Asset), maxStart
}

// ---------------------------------------------------------------------------
// Snapshot hacia la UI (GraphPanel)
// ---------------------------------------------------------------------------

// GraphNodeWire es un nodo del radar para la UI: el activo, dónde vive, cuánto
// tiene el usuario ahí y cuánto vale — "la información básica de tu dinero".
type GraphNodeWire struct {
	ID    string `json:"id"`    // "BTC@Binance"
	Asset string `json:"asset"` // "BTC" | "ETH" | "USDT" | "USD"
	Venue string `json:"venue"`
	// Kind: "cash" para USD y equivalentes declarados (fila superior del panel);
	// "crypto" para el resto.
	Kind       string  `json:"kind"`
	Balance    float64 `json:"balance"`     // saldo del usuario en unidades del activo
	BalanceUSD float64 `json:"balance_usd"` // valor aproximado en USD
	PriceUSD   float64 `json:"price_usd"`   // precio del activo (mid) — 1 para cash, 0 = sin dato
	FeedStale  bool    `json:"feed_stale"`  // true si el libro principal del venue está congelado
}

// GraphEdgeWire es una arista del radar para la UI.
type GraphEdgeWire struct {
	From      string  `json:"from"`
	To        string  `json:"to"`
	Kind      string  `json:"kind"` // "book" | "parity" | "inventory" | "transfer"
	Rate      float64 `json:"rate"`
	FeePct    float64 `json:"fee_pct"`   // fee efectivo en % (taker + slippage)
	Liquidity float64 `json:"liquidity"` // en unidades de BaseAsset; 0 = sin dato
	BaseAsset string  `json:"base_asset,omitempty"`
	Stale     bool    `json:"stale"`
}

// GraphCycleWire describe el mejor ciclo detectado, listo para narrar.
type GraphCycleWire struct {
	Path         []string `json:"path"` // IDs de nodos en orden, cerrado (primero == último)
	NetReturnPct float64  `json:"net_return_pct"`
	MaxVolumeBTC float64  `json:"max_volume_btc"` // 0 = piernas con bases mixtas (triangular)
	// MaxStartAmount/StartAsset: capacidad del ciclo en unidades de su nodo de
	// inicio ("hasta 12 000 USDT de entrada") — cubre los triangulares, donde
	// max_volume_btc no aplica.
	MaxStartAmount float64 `json:"max_start_amount,omitempty"`
	StartAsset     string  `json:"start_asset,omitempty"`
	Viable         bool    `json:"viable"`
}

// GraphSnapshotWire es el paquete completo que consume el GraphPanel.
type GraphSnapshotWire struct {
	Nodes []GraphNodeWire `json:"nodes"`
	Edges []GraphEdgeWire `json:"edges"`
	// BestCycle es nil casi siempre (el mercado real rara vez regala ciclos netos
	// positivos tras fees — y el radar lo muestra tal cual, sin maquillaje).
	BestCycle *GraphCycleWire `json:"best_cycle,omitempty"`
	// ParityAssumed recuerda a la UI que USDT≈USD es un supuesto declarado.
	ParityAssumed bool   `json:"parity_assumed"`
	UpdatedAt     string `json:"updated_at"`
}

// isCashAsset: USD o cualquier activo declarado en paridad con USD (USDT).
func isCashAsset(a Asset) bool {
	return a == "USD" || assetsParity(string(a), "USD")
}

// SnapshotFor arma la vista del radar para UNA sesión: el grafo global de mercado
// con los SALDOS MULTI-ACTIVO de esa sesión superpuestos en cada nodo (hito 3:
// el ETH de un ciclo triangular aparece con su saldo real) y las aristas de libro
// valoradas con LOS FEES DEL USUARIO (p.takerFee + p.SlippageRate): mover un
// slider en el panel cambia los números del radar en el siguiente barrido. El
// ciclo llega ya resuelto (FindBestCycleFor por sesión, ver Start).
func (g *LiquidityGraph) SnapshotFor(wallets Balances, p TradingParameters, cycle *Cycle, now time.Time) *GraphSnapshotWire {
	g.mu.Lock()
	defer g.mu.Unlock()

	snap := &GraphSnapshotWire{
		ParityAssumed: AssumeUSDTParity,
		UpdatedAt:     now.Format(time.RFC3339),
	}

	// Precio USD por nodo: cash = 1; cripto = mid de su libro contra un quote
	// cash del mismo venue (BTC/USDT, ETH/USDT, BTC/USD…).
	priceUSD := make(map[string]float64, len(g.nodes))
	for _, in := range Instruments {
		if !isCashAsset(Asset(in.Quote)) {
			continue // ETH/BTC no da precio USD directo
		}
		base := MarketNode{Asset: Asset(in.Base), Venue: in.Venue}
		quote := MarketNode{Asset: Asset(in.Quote), Venue: in.Venue}
		sell := g.edges[edgeKey(base, quote)]
		buy := g.edges[edgeKey(quote, base)]
		if sell != nil && buy != nil && sell.Rate > 0 && buy.Rate > 0 {
			priceUSD[base.ID()] = (sell.Rate + 1/buy.Rate) / 2 // (bid + ask) / 2
		}
	}

	// Staleness por venue: el libro PRINCIPAL (respaldado por wallets) manda para
	// el chip de la UI; cada arista lleva además su propio flag de staleness.
	staleVenue := make(map[string]bool, len(Venues))
	for _, v := range Venues {
		staleVenue[v.Name] = true
		if in, ok := primaryInstrument(v.Name); ok {
			base := MarketNode{Asset: Asset(in.Base), Venue: in.Venue}
			quote := MarketNode{Asset: Asset(in.Quote), Venue: in.Venue}
			if sell := g.edges[edgeKey(base, quote)]; sell != nil && sell.Rate > 0 {
				staleVenue[v.Name] = now.Sub(sell.UpdatedAt) > MaxBookStaleness
			}
		}
	}

	for _, n := range g.nodes {
		node := GraphNodeWire{
			ID:        n.ID(),
			Asset:     string(n.Asset),
			Venue:     n.Venue,
			Kind:      "crypto",
			Balance:   wallets.Get(n.Venue, string(n.Asset)),
			FeedStale: staleVenue[n.Venue],
		}
		if isCashAsset(n.Asset) {
			node.Kind = "cash"
			node.PriceUSD = 1
		} else {
			node.PriceUSD = priceUSD[n.ID()]
		}
		node.BalanceUSD = node.Balance * node.PriceUSD
		snap.Nodes = append(snap.Nodes, node)
	}

	for _, e := range g.edges {
		if math.IsInf(e.Weight, 1) && e.Kind == EdgeOrderBook {
			continue // libro sin datos: no pintar una arista vacía
		}
		feePct := e.Fee * 100
		if e.Kind == EdgeOrderBook {
			// La arista se pinta con el costo que ESTE usuario pagaría por usarla,
			// no con el de referencia: mismo cálculo que FindBestCycleFor.
			feePct = (p.takerFee(e.From.Venue) + p.SlippageRate) * 100
		}
		snap.Edges = append(snap.Edges, GraphEdgeWire{
			From:      e.From.ID(),
			To:        e.To.ID(),
			Kind:      e.Kind.wireKind(),
			Rate:      e.Rate,
			FeePct:    feePct,
			Liquidity: e.Liquidity,
			BaseAsset: e.BaseAsset,
			Stale:     e.Kind == EdgeOrderBook && now.Sub(e.UpdatedAt) > MaxBookStaleness,
		})
	}

	if cycle != nil && len(cycle.Edges) > 0 {
		path := make([]string, 0, len(cycle.Edges)+1)
		path = append(path, cycle.Edges[0].From.ID())
		for _, e := range cycle.Edges {
			path = append(path, e.To.ID())
		}
		snap.BestCycle = &GraphCycleWire{
			Path:           path,
			NetReturnPct:   cycle.NetReturn * 100,
			MaxVolumeBTC:   cycle.MaxVolumeBTC,
			MaxStartAmount: cycle.MaxStartAmount,
			StartAsset:     cycle.StartAsset,
			Viable:         cycle.NetReturn > 0,
		}
	}

	return snap
}

// DescribeCycle produce la narración humana de un ciclo para el feed de logs:
// "USDT@Binance → BTC@Binance → BTC@Bitso → USD@Bitso → USDT@Binance (+0.12 %)".
func DescribeCycle(c *Cycle) string {
	if c == nil || len(c.Edges) == 0 {
		return ""
	}
	s := c.Edges[0].From.ID()
	for _, e := range c.Edges {
		s += " → " + e.To.ID()
	}
	if c.MaxVolumeBTC > 0 {
		return fmt.Sprintf("%s (%+.3f %% neto, hasta %.4f BTC)", s, c.NetReturn*100, c.MaxVolumeBTC)
	}
	if c.MaxStartAmount > 0 {
		// Ciclo de bases mixtas (triangular): la capacidad se narra en unidades
		// del nodo de inicio ("entrada hasta 12 000 USDT").
		return fmt.Sprintf("%s (%+.3f %% neto, entrada hasta %.2f %s)", s, c.NetReturn*100, c.MaxStartAmount, c.StartAsset)
	}
	return fmt.Sprintf("%s (%+.3f %% neto)", s, c.NetReturn*100)
}
