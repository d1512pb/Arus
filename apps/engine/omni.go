package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// omni.go — FASE 1: INYECCIÓN DE ARBITRAJE OMNIDIRECCIONAL (triangular/espacial).
//
// El botón "Oportunidad normal" del simulador ya no inyecta un simple spread de
// dos venues: construye y ejecuta un CICLO que atraviesa varios nodos del grafo
// del usuario (cash → BTC → ETH → cash dentro de un exchange, o cash → BTC →
// BTC@otro → cash@otro → cash de vuelta entre exchanges). Demuestra que Arus no
// solo hace arbitraje simple, sino que encuentra CAMINOS COMPLEJOS.
//
// Como el resto del simulador (demo_inject), la OPORTUNIDAD se fabrica: el
// mercado real rara vez regala un ciclo neto positivo tras fees, así que aquí se
// inyecta una pequeña ineficiencia (demoNet) para la demo — pero las piernas se
// dimensionan con precios REALES y los saldos multi-activo se mueven de verdad,
// así que el patrimonio sube y el ledger registra la operación.
//
// El frontend anima una luz verde que viaja SECUENCIALMENTE por las aristas del
// camino (evento omni_executed → RadarView), ilustrando el flujo del dinero.

// Parámetros del escenario omnidireccional.
const (
	omniMinCash       = 100.0   // efectivo mínimo en el nodo de inicio para que valga la pena
	omniEntryFraction = 0.4     // fracción del efectivo del nodo de inicio que entra al ciclo
	omniMaxEntry      = 80000.0 // tope de entrada: un ciclo enorme no debe descuadrar la demo
)

// omniLeg es una pierna ya dimensionada del ciclo: convierte In unidades del nodo
// From en Out unidades del nodo To. From/To son IDs de nodo ("USDT@Binance").
type omniLeg struct {
	From, To string
	In, Out  float64
	Asset    string // activo GANADO (el activo de To)
	Kind     string // "book" | "inventory" | "parity"
}

// omniPlan es un ciclo omnidireccional listo para ejecutar y animar.
type omniPlan struct {
	StartVenue string
	StartAsset string
	Path       []string // IDs de nodo en orden, cerrado (primero == último)
	Legs       []omniLeg
	Net        float64
	VolumeBTC  float64
	Route      string
}

// demoNet devuelve la ineficiencia neta inyectada por vuelta de capital
// (0.15 %–0.27 %): pequeña y creíble, positiva siempre para que la demo gane.
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

// planOmni construye el mejor ciclo omnidireccional EJECUTABLE sobre el universo
// y los fondos del usuario. Prioriza el camino más ilustrativo disponible:
// triangular intra-venue (cash→BTC→ETH→cash) sobre el venue con más efectivo;
// si el universo no lo permite, un ciclo espacial entre exchanges; y como último
// recurso un ida-y-vuelta simple. Función PURA sobre una copia de saldos.
func (e *HFTEngine) planOmni(wallets Balances, p TradingParameters) (*omniPlan, bool) {
	if wallets == nil {
		return nil, false
	}
	btcPrice := getBTCPrice()
	if btcPrice <= 0 {
		return nil, false
	}

	// Nodos cash candidatos como inicio: venue activo, activo cash permitido, con
	// el mayor efectivo (empieza donde está el dinero).
	type homeCand struct {
		venue, cash string
		bal         float64
	}
	var homes []homeCand
	for _, v := range Venues {
		if !p.venueEnabled(v.Name) {
			continue
		}
		cash := v.QuoteAsset
		if !isCashAsset(Asset(cash)) || !omniNodeAllowed(p, cash, v.Name) {
			continue
		}
		homes = append(homes, homeCand{v.Name, cash, wallets.Get(v.Name, cash)})
	}
	sort.SliceStable(homes, func(i, j int) bool { return homes[i].bal > homes[j].bal })

	for _, hc := range homes {
		if hc.bal < omniMinCash {
			continue
		}
		entry := hc.bal * omniEntryFraction
		if entry > omniMaxEntry {
			entry = omniMaxEntry
		}
		if entry < omniMinCash {
			entry = hc.bal // wallet chica: usa lo que hay (sigue siendo ≥ mínimo)
		}

		// Camino más rico primero, degradando con gracia.
		if pl := buildTriangle(hc.venue, hc.cash, "ETH", entry, btcPrice, p); pl != nil {
			return pl, true
		}
		if pl := buildTriangle(hc.venue, hc.cash, "SOL", entry, btcPrice, p); pl != nil {
			return pl, true
		}
		if pl := buildSpatial(hc.venue, hc.cash, entry, btcPrice, p); pl != nil {
			return pl, true
		}
		if pl := buildRoundTrip(hc.venue, hc.cash, entry, btcPrice); pl != nil {
			return pl, true
		}
	}
	return nil, false
}

// buildTriangle arma un ciclo triangular DENTRO de un venue:
// cash → BTC → mid → cash (mid = ETH o SOL). Requiere los tres libros y que el
// activo intermedio tenga precio y esté en el universo del usuario.
func buildTriangle(venue, cash, mid string, entry, btcPrice float64, p TradingParameters) *omniPlan {
	if !omniNodeAllowed(p, "BTC", venue) || !omniNodeAllowed(p, mid, venue) {
		return nil
	}
	if !hasInstrument(venue, "BTC", cash) || !hasInstrument(venue, mid, "BTC") || !hasInstrument(venue, mid, cash) {
		return nil
	}
	midPrice := assetPriceUSD(mid)
	if midPrice <= 0 {
		return nil
	}

	btc := entry / btcPrice
	midAmt := entry / midPrice // = btc × (btcPrice/midPrice)
	net := entry * demoNet()
	cashOut := entry + net

	cashNode := cash + "@" + venue
	btcNode := "BTC@" + venue
	midNode := mid + "@" + venue
	return &omniPlan{
		StartVenue: venue, StartAsset: cash,
		Path: []string{cashNode, btcNode, midNode, cashNode},
		Legs: []omniLeg{
			{From: cashNode, To: btcNode, In: entry, Out: btc, Asset: "BTC", Kind: "book"},
			{From: btcNode, To: midNode, In: btc, Out: midAmt, Asset: mid, Kind: "book"},
			{From: midNode, To: cashNode, In: midAmt, Out: cashOut, Asset: cash, Kind: "book"},
		},
		Net: net, VolumeBTC: btc,
		Route: strings.Join([]string{cashNode, btcNode, midNode, cashNode}, " → "),
	}
}

// buildSpatial arma un ciclo ESPACIAL entre exchanges (arbitraje "paralelo"):
// cash@home → BTC@home → BTC@otro → cash@otro → cash@home. Usa un swap de
// inventario (BTC entre venues) y paridad/inventario para el retorno del cash.
func buildSpatial(home, cash string, entry, btcPrice float64, p TradingParameters) *omniPlan {
	if !omniNodeAllowed(p, "BTC", home) || !hasInstrument(home, "BTC", cash) {
		return nil
	}
	for _, v := range Venues {
		if v.Name == home || !p.venueEnabled(v.Name) || !omniNodeAllowed(p, "BTC", v.Name) {
			continue
		}
		ocash := v.QuoteAsset
		if !isCashAsset(Asset(ocash)) || !omniNodeAllowed(p, ocash, v.Name) || !hasInstrument(v.Name, "BTC", ocash) {
			continue
		}
		// El retorno del cash es swap de inventario si es el mismo activo, o
		// paridad si son equivalentes declarados (USD ↔ USDT).
		returnKind := "inventory"
		if ocash != cash {
			if !assetsParity(ocash, cash) {
				continue // sin arista de retorno del cash: este venue no sirve
			}
			returnKind = "parity"
		}

		btc := entry / btcPrice
		net := entry * demoNet()
		cashOut := entry + net // se vende en el otro venue a precio favorable (demo)

		cashHome := cash + "@" + home
		btcHome := "BTC@" + home
		btcOther := "BTC@" + v.Name
		cashOther := ocash + "@" + v.Name
		path := []string{cashHome, btcHome, btcOther, cashOther, cashHome}
		return &omniPlan{
			StartVenue: home, StartAsset: cash,
			Path: path,
			Legs: []omniLeg{
				{From: cashHome, To: btcHome, In: entry, Out: btc, Asset: "BTC", Kind: "book"},
				{From: btcHome, To: btcOther, In: btc, Out: btc, Asset: "BTC", Kind: "inventory"},
				{From: btcOther, To: cashOther, In: btc, Out: cashOut, Asset: ocash, Kind: "book"},
				{From: cashOther, To: cashHome, In: cashOut, Out: cashOut, Asset: cash, Kind: returnKind},
			},
			Net: net, VolumeBTC: btc,
			Route: strings.Join(path, " → "),
		}
	}
	return nil
}

// buildRoundTrip es el último recurso: un ida-y-vuelta cash → BTC → cash en el
// mismo libro. No es un ciclo "complejo", pero garantiza que el botón siempre
// haga ALGO visible cuando el universo se podó a un solo venue sin criptos alt.
func buildRoundTrip(home, cash string, entry, btcPrice float64) *omniPlan {
	if !hasInstrument(home, "BTC", cash) {
		return nil
	}
	btc := entry / btcPrice
	net := entry * demoNet()
	cashOut := entry + net
	cashNode := cash + "@" + home
	btcNode := "BTC@" + home
	return &omniPlan{
		StartVenue: home, StartAsset: cash,
		Path: []string{cashNode, btcNode, cashNode},
		Legs: []omniLeg{
			{From: cashNode, To: btcNode, In: entry, Out: btc, Asset: "BTC", Kind: "book"},
			{From: btcNode, To: cashNode, In: btc, Out: cashOut, Asset: cash, Kind: "book"},
		},
		Net: net, VolumeBTC: btc,
		Route: strings.Join([]string{cashNode, btcNode, cashNode}, " → "),
	}
}

// commitOmni aplica el plan sobre la sesión: re-verifica el saldo de inicio bajo
// el lock (hard block — el mundo pudo cambiar) y mueve todas las piernas de una
// vez. Como cada pierna acredita el activo que la siguiente consume, los saldos
// nunca quedan negativos. Devuelve false si ya no hay fondos.
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

// emitOmniExecuted envía el evento omni_executed con el camino completo para que
// el frontend anime la luz verde recorriendo las aristas secuencialmente. Sigue
// el patrón de sendArbExecuted: payload crudo por el socket (no ServerEvent).
func emitOmniExecuted(session *ClientSession, pl *omniPlan) {
	legs := make([]map[string]interface{}, 0, len(pl.Legs))
	for _, l := range pl.Legs {
		legs = append(legs, map[string]interface{}{
			"from": l.From, "to": l.To, "in": l.In, "out": l.Out, "asset": l.Asset, "kind": l.Kind,
		})
	}
	payload := map[string]interface{}{
		"event":          "omni_executed",
		"route":          pl.Route,
		"path":           pl.Path,
		"legs":           legs,
		"net_profit_usd": pl.Net,
		"start_venue":    pl.StartVenue,
		"volume_btc":     pl.VolumeBTC,
		"timestamp":      time.Now().Format("15:04:05.000"),
	}
	if b, err := json.Marshal(payload); err == nil {
		session.WriteMessage(websocket.TextMessage, b)
	}
}

// runOmniInjection es el gatillo del botón "Oportunidad normal" (acción
// inject_omni): planifica, ejecuta y anuncia un ciclo omnidireccional.
func (e *HFTEngine) runOmniInjection(session *ClientSession) {
	session.Mu.Lock()
	initialized := session.Wallets != nil && len(session.Wallets) > 0
	replenishing := session.IsReplenishing
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

	p := session.Params()
	pl, ok := e.planOmni(wallets, p)
	if !ok {
		sendLog(session, "💤 [OMNI] No hay un camino omnidireccional ejecutable con tu universo y fondos actuales. Deja algo de efectivo en una casa activa (y BTC/ETH habilitados).")
		return
	}
	if !commitOmni(session, pl) {
		sendLog(session, "🛑 [OMNI] Los fondos cambiaron antes de ejecutar el ciclo — inténtalo de nuevo.")
		return
	}

	sendLog(session, fmt.Sprintf("🔺 [ARBITRAJE OMNIDIRECCIONAL] %s | Entrada: $%.2f | Neto: +$%.2f | Vol: %.4f BTC-eq",
		pl.Route, pl.Legs[0].In, pl.Net, pl.VolumeBTC))

	emitOmniExecuted(session, pl)
	sendWalletUpdate(session) // saldos + patrimonio + persistencia write-behind

	recordTradeAsync(TradeRecord{
		SessionID:    session.ID,
		Timestamp:    time.Now(),
		BuyExchange:  "Omnidireccional",
		SellExchange: pl.Route,
		VolumeBTC:    pl.VolumeBTC,
		SpreadUSD:    0,
		FeesUSD:      0,
		NetProfitUSD: pl.Net,
	})
}
