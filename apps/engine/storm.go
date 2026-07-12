package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// storm.go — FASE 2: PRUEBA DE ESTRÉS / RÁFAGA DE VOLATILIDAD ("Evento poco común").
//
// El botón dispara una TORMENTA: durante ~4 s el motor lanza varias goroutines
// que ejecutan CONCURRENTEMENTE decenas de mini-arbitrajes espaciales entre las
// casas activas ("comprando y vendiendo por doquier"). Demuestra que Arus es un
// motor de alta frecuencia capaz de manejar el caos: ingesta masiva + ejecución
// concurrente serializada por el mutex de la sesión (cero data races).
//
// Cada unidad de la tormenta es un CICLO ESPACIAL cerrado (cash@A → BTC@A →
// BTC@B → cash@B → cash@A): vuelve al efectivo con una ganancia neta pequeña, así
// que NO drena inventario (repetible sin fin) pero el patrimonio sube rápido. El
// frontend lo pinta como una tormenta de luces verdes cruzando el grafo (evento
// storm_trade → flare por operación) con el P&L trepando (ver useArusEngine).

const (
	stormDurationMs    = 4200 // duración de la ráfaga (3–5 s como pide la fase)
	stormWorkers       = 4    // goroutines concurrentes que operan en paralelo
	stormMinCash       = 50.0 // efectivo mínimo en el nodo de inicio para operar
	stormEntryFraction = 0.05 // fracción del efectivo por operación (muchas caben)
	stormMaxEntry      = 6000.0
	stormTickMinMs     = 90  // cadencia por worker (jitter) — controla la densidad
	stormTickMaxMs     = 190
)

// stormPair es una dirección concreta del ciclo espacial de la tormenta:
// se compra BTC en `home` (con su cash) y se vende en `other` (por su ocash),
// cerrando el cash de vuelta por swap de inventario (mismo activo) o paridad.
type stormPair struct {
	home, cash, other, ocash, returnKind string
}

// stormPairs enumera las direcciones de ciclo espacial ejecutables sobre el
// universo del usuario (ambos venues activos, libros BTC/cash presentes y una
// arista de retorno del cash entre ambos). Incluye ambas direcciones de cada par
// para que las luces crucen el grafo en todos los sentidos.
func stormPairs(p TradingParameters) []stormPair {
	var out []stormPair
	for _, a := range Venues {
		if !p.venueEnabled(a.Name) || !omniNodeAllowed(p, "BTC", a.Name) {
			continue
		}
		cash := a.QuoteAsset
		if !isCashAsset(Asset(cash)) || !omniNodeAllowed(p, cash, a.Name) || !hasInstrument(a.Name, "BTC", cash) {
			continue
		}
		for _, b := range Venues {
			if b.Name == a.Name || !p.venueEnabled(b.Name) || !omniNodeAllowed(p, "BTC", b.Name) {
				continue
			}
			ocash := b.QuoteAsset
			if !isCashAsset(Asset(ocash)) || !omniNodeAllowed(p, ocash, b.Name) || !hasInstrument(b.Name, "BTC", ocash) {
				continue
			}
			returnKind := "inventory"
			if ocash != cash {
				if !assetsParity(ocash, cash) {
					continue
				}
				returnKind = "parity"
			}
			out = append(out, stormPair{home: a.Name, cash: cash, other: b.Name, ocash: ocash, returnKind: returnKind})
		}
	}
	return out
}

// stormSpatialPlan construye el ciclo espacial de una dirección concreta con la
// entrada dada (mismo esquema que buildSpatial pero para un par ya elegido).
func stormSpatialPlan(sp stormPair, entry, btcPrice float64) *omniPlan {
	btc := entry / btcPrice
	net := entry * demoNet()
	cashOut := entry + net

	cashHome := sp.cash + "@" + sp.home
	btcHome := "BTC@" + sp.home
	btcOther := "BTC@" + sp.other
	cashOther := sp.ocash + "@" + sp.other
	path := []string{cashHome, btcHome, btcOther, cashOther, cashHome}
	return &omniPlan{
		StartVenue: sp.home, StartAsset: sp.cash,
		Path: path,
		Legs: []omniLeg{
			{From: cashHome, To: btcHome, In: entry, Out: btc, Asset: "BTC", Kind: "book"},
			{From: btcHome, To: btcOther, In: btc, Out: btc, Asset: "BTC", Kind: "inventory"},
			{From: btcOther, To: cashOther, In: btc, Out: cashOut, Asset: sp.ocash, Kind: "book"},
			{From: cashOther, To: cashHome, In: cashOut, Out: cashOut, Asset: sp.cash, Kind: sp.returnKind},
		},
		Net: net, VolumeBTC: btc,
		Route: cashHome + " → " + btcHome + " → " + btcOther + " → " + cashOther,
	}
}

// emitStormEvent envía un evento crudo de la tormenta por el socket (patrón de
// sendArbExecuted: no ServerEvent). Todos usan el campo `type`.
func emitStormEvent(session *ClientSession, payload map[string]interface{}) {
	if b, err := json.Marshal(payload); err == nil {
		session.WriteMessage(websocket.TextMessage, b)
	}
}

// emitStormTrade anuncia UNA operación de la tormenta: el frontend dispara una
// luz verde de `buyVenue`→`sellVenue` y actualiza el P&L en vivo.
func emitStormTrade(session *ClientSession, buyVenue, sellVenue string, net float64) {
	session.Mu.Lock()
	total := session.TotalWealth
	netProfit := session.TotalNetProfit
	session.Mu.Unlock()
	emitStormEvent(session, map[string]interface{}{
		"type":             "storm_trade",
		"buy_venue":        buyVenue,
		"sell_venue":       sellVenue,
		"net_profit_usd":   net,
		"total_wealth":     total,
		"total_net_profit": netProfit,
		"timestamp":        time.Now().Format("15:04:05.000"),
	})
}

// runStormInjection es el gatillo del botón "Evento poco común" (acción
// inject_storm): lanza la ráfaga concurrente durante stormDurationMs.
func (e *HFTEngine) runStormInjection(session *ClientSession) {
	session.Mu.Lock()
	initialized := session.Wallets != nil && len(session.Wallets) > 0
	replenishing := session.IsReplenishing
	already := session.StormActive
	startWealth := session.TotalWealth
	if initialized && !replenishing && !already {
		session.StormActive = true
	}
	session.Mu.Unlock()

	if !initialized {
		return
	}
	if replenishing {
		sendLog(session, "⏸️ [TORMENTA] Bot en pausa por reequilibrio — la ráfaga de volatilidad se ignora hasta que termine.")
		return
	}
	if already {
		sendLog(session, "⚡ [TORMENTA] Ya hay una ráfaga en curso — espera a que termine.")
		return
	}

	p := session.Params()
	pairs := stormPairs(p)
	if len(pairs) == 0 {
		// Sin par cruzado (universo de un solo venue): la tormenta no puede pintar
		// luces entre casas. Se cancela con un aviso claro en vez de girar en vacío.
		session.Mu.Lock()
		session.StormActive = false
		session.Mu.Unlock()
		sendLog(session, "💤 [TORMENTA] Se necesitan al menos dos casas activas con efectivo para una ráfaga entre exchanges. Activa otra casa e inténtalo.")
		return
	}

	sendLog(session, fmt.Sprintf("⚡ [TORMENTA] Ráfaga de volatilidad iniciada (%.1f s): %d workers ejecutando arbitrajes en paralelo por todo el grafo…", stormDurationMs/1000.0, stormWorkers))
	emitStormEvent(session, map[string]interface{}{"type": "storm_started", "duration_ms": stormDurationMs})

	end := time.Now().Add(stormDurationMs * time.Millisecond)
	var trades int64

	var wg sync.WaitGroup
	for w := 0; w < stormWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for time.Now().Before(end) {
				sp := pairs[rand.Intn(len(pairs))]

				session.Mu.Lock()
				homeCash := session.Wallets.Get(sp.home, sp.cash)
				session.Mu.Unlock()
				if homeCash >= stormMinCash {
					entry := homeCash * stormEntryFraction
					if entry > stormMaxEntry {
						entry = stormMaxEntry
					}
					pl := stormSpatialPlan(sp, entry, getBTCPrice())
					if commitOmni(session, pl) {
						atomic.AddInt64(&trades, 1)
						emitStormTrade(session, sp.home, sp.other, pl.Net)
						recordTradeAsync(TradeRecord{
							SessionID:    session.ID,
							Timestamp:    time.Now(),
							BuyExchange:  sp.home,
							SellExchange: sp.other,
							VolumeBTC:    pl.VolumeBTC,
							SpreadUSD:    0,
							FeesUSD:      0,
							NetProfitUSD: pl.Net,
						})
					}
				}

				time.Sleep(time.Duration(stormTickMinMs+rand.Intn(stormTickMaxMs-stormTickMinMs)) * time.Millisecond)
			}
		}()
	}
	wg.Wait()

	session.Mu.Lock()
	session.StormActive = false
	endWealth := session.TotalWealth
	session.Mu.Unlock()

	profit := endWealth - startWealth
	n := atomic.LoadInt64(&trades)
	sendLog(session, fmt.Sprintf("✅ [TORMENTA] Ráfaga superada: %d operaciones ejecutadas en %.1f s | Ganancia acumulada: +$%.2f. El motor absorbió el caos sin romperse.", n, stormDurationMs/1000.0, profit))
	emitStormEvent(session, map[string]interface{}{
		"type":        "storm_ended",
		"trades":      n,
		"profit":      profit,
		"duration_ms": stormDurationMs,
	})
	sendWalletUpdate(session) // sincroniza saldos exactos + persiste (write-behind)
}
