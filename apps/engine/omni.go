package main

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"time"
)

// omni.go — FASE 2 del refactor de "Probar Bot": OPORTUNIDAD OMNIDIRECCIONAL REAL.
//
// El botón "Oportunidad normal" ya NO fabrica ni ejecuta un ciclo a mano (antes
// buildTriangle/commitOmni movían wallets directamente, sin fees, con una
// ganancia inventada). Ahora el botón solo fabrica una INEFICIENCIA DE MERCADO
// y el motor hace todo lo demás por su tubería real:
//
//  1. Se lee el universo ACTIVO del usuario (enabled_venues/enabled_assets) y se
//     elige la ruta más ilustrativa posible: un TRIANGULAR intra-venue
//     (cash → BTC → ETH/SOL → cash) o, si el universo no lo permite, un
//     ESPACIAL entre dos casas activas (cash@A → BTC@A → BTC@B → cash@B → …).
//  2. La ineficiencia se fabrica desplazando DOS libros reales en direcciones
//     opuestas (el de compra se abarata, el de venta se encarece), anclada a los
//     precios REALES vigentes y dentro del Spike Filter de ingesta (<5 %). Se
//     desplaza el libro COMPLETO (bid y ask por igual): ningún libro queda
//     cruzado ni rentable por sí solo — la ganancia solo existe para quien
//     recorre el CAMINO completo, como en un desalineamiento real.
//  3. Los ticks se publican con publishTick — el MISMO punto de entrada que los
//     WebSockets de Binance/Bitso/Kraken. Pasan el Spike Filter de ingesta,
//     actualizan el grafo, y el radar DEBE descubrir el ciclo por sí mismo:
//     FindBestCycleFor (Bellman-Ford con LOS FEES DEL USUARIO sobre SU subgrafo
//     podado) y executeCycleForSession (planCycle: saldo + tope + liquidez +
//     margen; Fill-or-Kill; commit atómico) — exactamente el camino del
//     autopiloto del radar.
//  4. La animación de la luz verde (omni_executed) nace del plan REALMENTE
//     ejecutado (ver emitCycleExecuted en cycle.go), no de un plan fabricado.
//
// Los feeds reales siguen publicando: el siguiente tick real (≤4 % de distancia)
// pasa el Spike Filter y RESTAURA el mercado — la ineficiencia es transitoria
// por diseño, y por eso la inyección persigue al radar con reintentos.

// Parámetros del escenario omnidireccional (FASE 2).
const (
	// omniMaxShift limita el desplazamiento por libro: la ingesta descarta ticks
	// que se desvían >5 % del último aceptado (DefaultSpikeTickDeviation), así
	// que la ineficiencia fabricada debe caber DENTRO de lo que un mercado real
	// toleraría. 4 % deja margen para el ruido del propio feed.
	omniMaxShift = 0.04
	// omniExtraReturn es el colchón de retorno neto (fracción) que se suma al
	// margen del usuario al dimensionar la ineficiencia: que el ciclo no pase
	// "raspando" el umbral y muera por el redondeo o la liquidez de una pierna.
	omniExtraReturn = 0.0012
	// omniAttempts × omniSettle es la ventana de persecución: los feeds reales
	// pueden sobrescribir la ineficiencia antes de que el radar barra el grafo
	// (Binance publica en milisegundos) — se reinyecta y se reintenta.
	omniAttempts = 12
	omniSettle   = 60 * time.Millisecond
)

// applyOmniShift publica el tick en la tubería real Y refleja el libro en el
// grafo de inmediato. El canal de ingesta está bufferizado (1000 eventos):
// publishTick retorna sin esperar a la goroutine de ingesta, y los feeds reales
// (milisegundos) pueden pisar la ineficiencia antes del barrido del radar. El
// reflejo en grafo replica lo que la ingesta haría tras ACEPTAR el tick — que
// pasa el Spike Filter por construcción (ancla + acotado a omniMaxShift).
func applyOmniShift(e *HFTEngine, in Instrument, ask, bid, qty float64) {
	publishTick(e.Ticks, in, ask, bid, qty, qty)
	if e.Graph != nil {
		e.Graph.UpdateBook(in.Key(), TopOfBook{
			Ask: ask, Bid: bid, AskQty: qty, BidQty: qty, UpdatedAt: time.Now(),
		})
	}
}

// omniTarget es el ESCENARIO elegido sobre el universo activo del usuario.
type omniTarget struct {
	kind string // "triangular" | "espacial"
	// buyBook se desplaza HACIA ABAJO (ahí se compra más barato) y sellBook
	// HACIA ARRIBA (ahí se vende más caro). Triangular: buyBook es el libro
	// cruzado (ETH/BTC) y sellBook el libro mid/cash (ETH/USDT).
	buyBook  Instrument
	sellBook Instrument
	// support: libros de la ruta que NO se tocan pero deben estar FRESCOS (el
	// ciclo usa sus precios reales). Triangular: el libro BTC/cash del venue.
	support []Instrument
	// startVenue/startCash: nodo cash donde arranca la ruta (donde el plan
	// necesitará saldo del usuario).
	startVenue, startCash string
	// legFees: fee+slippage DEL USUARIO por cada pierna de libro de la ruta.
	legFees []float64
	route   string
}

// freshTopOfBook devuelve el top-of-book vigente de un instrumento solo si está
// FRESCO (mismo criterio de staleness que la evaluación real): la ineficiencia
// se ancla a precios vivos, nunca a libros muertos.
func freshTopOfBook(key string) (TopOfBook, bool) {
	b, ok := currentMarket.Get(key)
	if !ok || b.Ask <= 0 || b.Bid <= 0 || time.Since(b.UpdatedAt) > MaxBookStaleness {
		return TopOfBook{}, false
	}
	return b, true
}

// listOmniTargets enumera TODOS los escenarios omnidireccionales viables sobre
// el universo ACTIVO (libros frescos): triangulares intra-venue y espaciales
// entre casas, en ambas direcciones. FASE 2 elige el primero (mejor casa por
// efectivo); FASE 3 (tormenta) reparte la ráfaga entre todos.
func listOmniTargets(p TradingParameters, wallets Balances) []omniTarget {
	type homeCand struct {
		v    Venue
		cash float64
	}
	var homes []homeCand
	for _, v := range Venues {
		if !p.venueEnabled(v.Name) || !isCashAsset(Asset(v.QuoteAsset)) || !omniNodeAllowed(p, v.QuoteAsset, v.Name) {
			continue
		}
		homes = append(homes, homeCand{v, wallets.Get(v.Name, v.QuoteAsset)})
	}
	sort.SliceStable(homes, func(i, j int) bool { return homes[i].cash > homes[j].cash })

	var out []omniTarget

	// 1) TRIANGULAR intra-venue: cash → BTC → mid → cash.
	for _, h := range homes {
		v := h.v
		if !omniNodeAllowed(p, "BTC", v.Name) {
			continue
		}
		for _, mid := range []string{"ETH", "SOL"} {
			if !omniNodeAllowed(p, mid, v.Name) {
				continue
			}
			baseIn, ok1 := instrumentByKey(v.Name + ":BTC/" + v.QuoteAsset)
			crossIn, ok2 := instrumentByKey(v.Name + ":" + mid + "/BTC")
			midIn, ok3 := instrumentByKey(v.Name + ":" + mid + "/" + v.QuoteAsset)
			if !ok1 || !ok2 || !ok3 {
				continue
			}
			if _, ok := freshTopOfBook(baseIn.Key()); !ok {
				continue
			}
			if _, ok := freshTopOfBook(crossIn.Key()); !ok {
				continue
			}
			if _, ok := freshTopOfBook(midIn.Key()); !ok {
				continue
			}
			f := p.takerFee(v.Name) + p.SlippageRate
			out = append(out, omniTarget{
				kind:       "triangular",
				buyBook:    crossIn,
				sellBook:   midIn,
				support:    []Instrument{baseIn},
				startVenue: v.Name,
				startCash:  v.QuoteAsset,
				legFees:    []float64{f, f, f},
				route: fmt.Sprintf("%s@%s → BTC@%s → %s@%s → %s@%s",
					v.QuoteAsset, v.Name, v.Name, mid, v.Name, v.QuoteAsset, v.Name),
			})
		}
	}

	// 2) ESPACIAL entre exchanges (ambas direcciones para luces cruzadas).
	for _, ha := range homes {
		a := ha.v
		if !omniNodeAllowed(p, "BTC", a.Name) {
			continue
		}
		aIn, ok := instrumentByKey(a.Name + ":BTC/" + a.QuoteAsset)
		if !ok {
			continue
		}
		if _, fresh := freshTopOfBook(aIn.Key()); !fresh {
			continue
		}
		for _, hb := range homes {
			b := hb.v
			if b.Name == a.Name || !omniNodeAllowed(p, "BTC", b.Name) {
				continue
			}
			if b.QuoteAsset != a.QuoteAsset && !assetsParity(b.QuoteAsset, a.QuoteAsset) {
				continue
			}
			bIn, ok := instrumentByKey(b.Name + ":BTC/" + b.QuoteAsset)
			if !ok {
				continue
			}
			if _, fresh := freshTopOfBook(bIn.Key()); !fresh {
				continue
			}
			out = append(out, omniTarget{
				kind:       "espacial",
				buyBook:    aIn,
				sellBook:   bIn,
				startVenue: a.Name,
				startCash:  a.QuoteAsset,
				legFees: []float64{
					p.takerFee(a.Name) + p.SlippageRate,
					p.takerFee(b.Name) + p.SlippageRate,
				},
				route: fmt.Sprintf("%s@%s → BTC@%s → BTC@%s → %s@%s → %s@%s",
					a.QuoteAsset, a.Name, a.Name, b.Name, b.QuoteAsset, b.Name, a.QuoteAsset, a.Name),
			})
		}
	}
	return out
}

// preferSpatialOmniTargets prioriza rutas espaciales en demos rápidas: el
// triangular cierra el loop de cripto (inventario casi plano); el espacial deja
// el sesgo buy@A / sell@B visible en el radar y en las wallets.
func preferSpatialOmniTargets(all []omniTarget) []omniTarget {
	var spatial []omniTarget
	for _, t := range all {
		if t.kind == "espacial" {
			spatial = append(spatial, t)
		}
	}
	if len(spatial) > 0 {
		return spatial
	}
	return all
}

// lockOmniTargetsOneWay fija UNA dirección espacial (mismo buyVenue→sellVenue).
// Mezclar A→B y B→A en una ráfaga cancela el BTC y solo deja el neto en cash.
func lockOmniTargetsOneWay(all []omniTarget) []omniTarget {
	if len(all) == 0 || all[0].kind != "espacial" {
		return all
	}
	buyV, sellV := all[0].buyBook.Venue, all[0].sellBook.Venue
	var out []omniTarget
	for _, t := range all {
		if t.kind == "espacial" && t.buyBook.Venue == buyV && t.sellBook.Venue == sellV {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return all
	}
	return out
}

// cycleIsSpatial: el ciclo cruza al menos dos venues en piernas de libro
// (compra en A / venta en B). Un triangular intra-venue no cuenta.
func cycleIsSpatial(c *Cycle) bool {
	if c == nil {
		return false
	}
	venues := map[string]struct{}{}
	for _, e := range c.Edges {
		if e.Kind != EdgeOrderBook {
			continue
		}
		venues[e.From.Venue] = struct{}{}
		venues[e.To.Venue] = struct{}{}
	}
	return len(venues) >= 2
}

// cycleUsesInstrument: el ciclo tiene una pierna de libro del instrumento dado
// (en cualquiera de las dos direcciones del par).
func cycleUsesInstrument(c *Cycle, in Instrument) bool {
	if c == nil {
		return false
	}
	baseID := MarketNode{Asset: Asset(in.Base), Venue: in.Venue}.ID()
	quoteID := MarketNode{Asset: Asset(in.Quote), Venue: in.Venue}.ID()
	for _, e := range c.Edges {
		if e.Kind != EdgeOrderBook {
			continue
		}
		from, to := e.From.ID(), e.To.ID()
		if (from == quoteID && to == baseID) || (from == baseID && to == quoteID) {
			return true
		}
	}
	return false
}

// cycleMatchesOmniTarget: solo ejecutar el ciclo que aprovecha los libros que
// acabamos de desplazar. Evita que un triangular/ETH residual "robe" la demo
// (ilumina ETH en el path y deja el saldo igual).
func cycleMatchesOmniTarget(c *Cycle, t omniTarget) bool {
	if c == nil {
		return false
	}
	if t.kind == "espacial" && !cycleIsSpatial(c) {
		return false
	}
	return cycleUsesInstrument(c, t.buyBook) && cycleUsesInstrument(c, t.sellBook)
}

// pickOmniTarget elige el escenario más ilustrativo: espacial si el universo lo
// permite (para que el inventario se sesgue), si no triangular intra-venue.
func pickOmniTarget(p TradingParameters, wallets Balances) (omniTarget, bool) {
	all := preferSpatialOmniTargets(listOmniTargets(p, wallets))
	if len(all) == 0 {
		return omniTarget{}, false
	}
	return all[0], true
}

// omniShiftNeeded calcula el desplazamiento TOTAL (fracción) que la ruta del
// escenario necesita para que su producto de tasas NETAS —con los fees del
// usuario— supere 1 + netReturn, partiendo de los precios reales anclados.
// El resultado se reparte a partes iguales entre buyBook (hacia abajo) y
// sellBook (hacia arriba).
func omniShiftNeeded(t omniTarget, anchors map[string]TopOfBook, netReturn float64) float64 {
	buy, sell := anchors[t.buyBook.Key()], anchors[t.sellBook.Key()]
	if buy.Ask <= 0 || sell.Bid <= 0 {
		return 2 * omniMaxShift
	}
	var prod float64
	if t.kind == "triangular" {
		// cash → BTC (ask real del libro base) → mid (ask del cross) → cash
		// (bid del libro mid). Producto bruto ≈ 1 en un mercado alineado.
		base := anchors[t.support[0].Key()]
		if base.Ask <= 0 {
			return 2 * omniMaxShift
		}
		prod = sell.Bid / (base.Ask * buy.Ask)
	} else {
		// cash@A → BTC@A (ask de A) → BTC@B (swap) → cash@B (bid de B) → cash@A.
		prod = sell.Bid / buy.Ask
	}
	for _, f := range t.legFees {
		prod *= 1 - f
	}
	if prod <= 0 {
		return 2 * omniMaxShift
	}
	need := (1 + netReturn) / prod
	if need <= 1 {
		return 0 // el mercado real ya regala la ruta (raro, pero honesto)
	}
	return need - 1
}

// runOmniInjection es el gatillo del botón "Oportunidad normal" (acción
// inject_omni): fabrica la ineficiencia, la inyecta por la tubería real y
// PERSIGUE la detección/ejecución del radar — sin atajos ni caminos paralelos.
func (e *HFTEngine) runOmniInjection(session *ClientSession) {
	session.Mu.Lock()
	initialized := session.Wallets != nil && len(session.Wallets) > 0
	replenishing := session.IsReplenishing
	lastTrade := session.LastTradeTime
	netBefore := session.TotalNetProfit
	fundsDialogBefore := session.InsufficientFundsPending
	creditBefore := session.Credit.Active
	var wallets Balances
	if initialized {
		wallets = session.Wallets.Clone()
	}
	session.Mu.Unlock()

	if !initialized {
		return
	}
	if replenishing {
		sendLog(session, "⏸️ [OMNI] Bot en pausa por reequilibrio — inyección omnidireccional ignorada.")
		return
	}
	if e.Ticks == nil || e.Graph == nil {
		return // motor sin canal de ingesta o sin grafo (solo posible en tests)
	}

	p := session.Params()
	if cap := analyzeUniverse(p); !cap.OK {
		sendLog(session, "💤 [OMNI] "+cap.Reason)
		return
	}
	target, ok := pickOmniTarget(p, wallets)
	if !ok {
		sendLog(session, "💤 [OMNI] No hay ruta omnidireccional posible con tu universo actual: se necesita al menos una casa activa con BTC habilitado y sus libros publicando en vivo. Activa más casas/monedas o espera a que los feeds despierten.")
		return
	}

	// ANCLAS: el top-of-book REAL de cada libro de la ruta, capturado UNA sola
	// vez. Todas las (re)inyecciones se calculan desde aquí — idempotentes:
	// reintentar no acumula desplazamiento y el libro nunca deriva lejos del
	// mercado (el siguiente tick real lo restaura pasando el Spike Filter).
	anchors := make(map[string]TopOfBook)
	for _, in := range append([]Instrument{target.buyBook, target.sellBook}, target.support...) {
		b, ok := freshTopOfBook(in.Key())
		if !ok {
			sendLog(session, fmt.Sprintf("🧊 [OMNI] El libro %s se congeló justo antes de inyectar — inténtalo de nuevo.", in.Key()))
			return
		}
		anchors[in.Key()] = b
	}

	// Dimensionado: cuánta entrada cabe (saldo del nodo cash de inicio vs tope
	// del usuario) y qué retorno neto necesita para superar SU margen con
	// colchón. El desplazamiento se reparte entre ambos libros y se acota al
	// Spike Filter: si los fees/margen del usuario exigen más de lo que un
	// mercado real toleraría, se inyecta el máximo y el motor decide (y narra).
	btcPrice := getBTCPrice()
	entry := p.MaxOrderSizeBTC * btcPrice
	if bal := wallets.Get(target.startVenue, target.startCash); bal > 0 && bal < entry {
		entry = bal
	}
	if entry < 1 {
		entry = p.MaxOrderSizeBTC * btcPrice // sin saldo: el flujo de crédito decidirá
	}
	netReturn := omniExtraReturn + p.MinNetProfitUSD*1.5/entry
	shift := omniShiftNeeded(target, anchors, netReturn)
	half := shift / 2
	capped := half > omniMaxShift
	if capped {
		half = omniMaxShift
	}

	// Liquidez visible de los libros inyectados: generosa (3× la entrada) para
	// que el tamaño del plan lo acoten el SALDO y el TOPE del usuario, no un
	// qty artificial. En unidades del activo base de cada libro.
	qtyFor := func(in Instrument, b TopOfBook) float64 {
		q := 1.0
		if px := assetPriceUSD(in.Base); px > 0 {
			q = 3 * entry / px
		}
		if q < b.AskQty {
			q = b.AskQty
		}
		if q < b.BidQty {
			q = b.BidQty
		}
		return q
	}

	kindLabel := "triangular"
	if target.kind == "espacial" {
		kindLabel = "espacial (paralela)"
	}
	sendLog(session, fmt.Sprintf(
		"🔺 [OMNI] Ineficiencia %s inyectada en la tubería REAL: %s se abarata %.2f %% y %s se encarece %.2f %%. Ningún libro es rentable por sí solo — el radar debe descubrir la ruta completa (%s) con TUS fees y ejecutarla.",
		kindLabel, target.buyBook.Key(), half*100, target.sellBook.Key(), half*100, target.route))
	if capped {
		sendLog(session, fmt.Sprintf(
			"⚠️ [OMNI] Tu margen mínimo ($%.2f) y tus fees exigen una ineficiencia del %.2f %% — más de lo que el Spike Filter de ingesta admite. Se inyecta el máximo realista (%.1f %% por libro); si el neto no supera tu margen, el motor lo dirá.",
			p.MinNetProfitUSD, shift*100, omniMaxShift*100))
	}

	// Cooldown anti-sobretrading del ejecutor (3 s entre trades): si la sesión
	// acaba de operar, esperar el remanente en vez de morir en esa compuerta.
	if rem := 3*time.Second - time.Since(lastTrade); rem > 0 {
		time.Sleep(rem + 50*time.Millisecond)
	}

	buyAnchor := anchors[target.buyBook.Key()]
	sellAnchor := anchors[target.sellBook.Key()]
	qtyBuy := qtyFor(target.buyBook, buyAnchor)
	qtySell := qtyFor(target.sellBook, sellAnchor)

	var lastCycle *Cycle
	announced := false
	for attempt := 0; attempt < omniAttempts; attempt++ {
		// (Re)inyección idempotente desde las anclas: los feeds reales pueden
		// haberse comido la ineficiencia entre intentos.
		applyOmniShift(e, target.buyBook, buyAnchor.Ask*(1-half), buyAnchor.Bid*(1-half), qtyBuy)
		applyOmniShift(e, target.sellBook, sellAnchor.Ask*(1+half), sellAnchor.Bid*(1+half), qtySell)

		// Barrido inmediato tras reflejar en grafo; el sleep corto solo da tiempo
		// al ejecutor si otra goroutine tenía el lock de serialización.
		cycle := e.Graph.FindBestCycleFor(p, time.Now())
		if cycle == nil {
			continue
		}
		// Solo el ciclo que usa los libros inyectados (no un triangular/ETH ajeno).
		if !cycleMatchesOmniTarget(cycle, target) {
			continue
		}
		lastCycle = cycle
		if !announced {
			announced = true
			sendLog(session, fmt.Sprintf("📡 [RADAR] Ciclo detectado en TU subgrafo: %s", DescribeCycle(cycle)))
		}

		// Ejecución por la MISMA vía que el autopiloto del radar: planCycle
		// (saldo + tope + liquidez + margen del usuario), Fill-or-Kill y commit
		// atómico. La animación (omni_executed) la emite el propio ejecutor.
		e.executeCycleForSession(session, cycle, p)

		session.Mu.Lock()
		executed := session.TotalNetProfit != netBefore
		engaged := (session.InsufficientFundsPending && !fundsDialogBefore) ||
			session.IsReplenishing || (session.Credit.Active && !creditBefore)
		session.Mu.Unlock()
		if executed {
			return // ejecutado: cycle.go ya narró, animó y persistió
		}
		if engaged {
			// El motor tomó su camino real de liquidez (crédito automático,
			// reequilibrio o diálogo asistido): la decisión sigue su flujo normal.
			return
		}
		time.Sleep(omniSettle)
	}

	// Sin ejecución tras la ventana de persecución: narrar el motivo HONESTO.
	if lastCycle == nil {
		sendLog(session, "🌊 [OMNI] El mercado real sobrescribió la ineficiencia antes de que el radar barriera el grafo (los feeds publican en milisegundos). Inténtalo de nuevo.")
		return
	}
	session.Mu.Lock()
	balancesNow := session.Wallets.Clone()
	session.Mu.Unlock()
	if _, err := planCycle(lastCycle, balancesNow, p, getBTCPrice()); err != nil {
		switch err {
		case errBelowMargin:
			sendLog(session, fmt.Sprintf("💤 [OMNI] El radar detectó el ciclo, pero con tu tope de orden el neto no supera TU margen mínimo ($%.2f). Baja el margen o sube el tope en Estrategia.", p.MinNetProfitUSD))
		case errNoFunds:
			sendLog(session, fmt.Sprintf("🛑 [OMNI] Ciclo detectado pero sin saldo suficiente en %s@%s para el mínimo ejecutable — deposita fondos ahí.", target.startCash, target.startVenue))
		case errNoCashStart:
			sendLog(session, "💤 [OMNI] El ciclo detectado no pasa por ningún nodo de efectivo — no es fondeable.")
		default:
			sendLog(session, "💤 [OMNI] El ciclo detectado dejó de ser rentable al dimensionarlo con tus fees — el mercado se movió. Inténtalo de nuevo.")
		}
		return
	}
	sendLog(session, "💤 [OMNI] El ciclo era viable pero otra compuerta lo frenó (cooldown, pausa o ejecución en curso). Inténtalo de nuevo en unos segundos.")
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers de nodos / IDs compartidos con la tormenta (storm.go) y utilidades
// de grafo. commitOmni/demoNet/omniPlan quedan solo como legado inerte: la
// FASE 3 ya no los usa (la tormenta pasa por planCycle/commitCycle).
// ─────────────────────────────────────────────────────────────────────────────

// omniLeg es una pierna ya dimensionada de un ciclo fabricado (legado).
type omniLeg struct {
	From, To string
	In, Out  float64
	Asset    string // activo GANADO (el activo de To)
	Kind     string // "book" | "inventory" | "parity"
}

// omniPlan es un ciclo fabricado listo para ejecutar (legado).
type omniPlan struct {
	StartVenue string
	StartAsset string
	Path       []string // IDs de nodo en orden, cerrado (primero == último)
	Legs       []omniLeg
	Net        float64
	VolumeBTC  float64
	Route      string
}

// demoNet legado: ineficiencia neta inventada (ya no usa la tormenta).
func demoNet() float64 {
	return 0.0015 + rand.Float64()*0.0012
}

// hasInstrument informa si existe un libro (venue, base, quote) en el registro.
func hasInstrument(venue, base, quote string) bool {
	_, ok := instrumentByKey(venue + ":" + base + "/" + quote)
	return ok
}

// omniNodeAllowed informa si un nodo (activo@venue) pertenece al universo del
// usuario (params.EnabledVenues/EnabledAssets; vacío = todos).
func omniNodeAllowed(p TradingParameters, asset, venue string) bool {
	return p.universeAllows(MarketNode{Asset: Asset(asset), Venue: venue})
}

// splitNodeID parte un ID de nodo "BTC@Binance" en (asset, venue).
func splitNodeID(id string) (asset, venue string) {
	if i := strings.Index(id, "@"); i >= 0 {
		return id[:i], id[i+1:]
	}
	return id, ""
}

// commitOmni aplica un plan fabricado (legado; no usado por FASE 2/3).
func commitOmni(session *ClientSession, pl *omniPlan) bool {
	session.Mu.Lock()
	defer session.Mu.Unlock()

	if session.Wallets == nil || len(pl.Legs) == 0 {
		return false
	}
	startAsset, startVenue := splitNodeID(pl.Path[0])
	if session.Wallets.Get(startVenue, startAsset) < pl.Legs[0].In*(1-1e-9) {
		return false
	}

	for _, l := range pl.Legs {
		fa, fv := splitNodeID(l.From)
		ta, tv := splitNodeID(l.To)
		session.Wallets.Add(fv, fa, -l.In)
		session.Wallets.Add(tv, ta, l.Out)
	}
	session.TotalWealth += pl.Net
	session.TotalNetProfit += pl.Net
	session.LastTradeTime = time.Now()
	return true
}
