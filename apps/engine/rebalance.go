package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/gorilla/websocket"
)

// rebalance.go — GESTIÓN DE CAPITAL AGNÓSTICA (multi-venue · multi-asset).
//
// Sustituye el hardcode Binance/Bitso × USD/BTC: el reequilibrio reparte CADA
// activo entre los venues del universo del usuario que lo soportan, y el crédito
// inyecta liquidez dinámica (cash + cripto que bloquea la oportunidad).

// enabledVenuesForSession: venues del registro filtrados por el universo del
// usuario (lista vacía = todos). Orden estable del registro.
func enabledVenuesForSession(p TradingParameters) []Venue {
	out := make([]Venue, 0, len(Venues))
	for _, v := range Venues {
		if p.venueEnabled(v.Name) {
			out = append(out, v)
		}
	}
	return out
}

// venuesSupportingAsset: subset de `venues` que cotizan `asset` (cash nativo o
// base/quote de algún libro). Orden preservado.
func venuesSupportingAsset(venues []Venue, asset string) []Venue {
	out := make([]Venue, 0, len(venues))
	for _, v := range venues {
		if venueHasAsset(v.Name, asset) {
			out = append(out, v)
		}
	}
	return out
}

// assetEnabledInUniverse: EnabledAssets vacío = todos los conocidos.
func assetEnabledInUniverse(p TradingParameters, asset string) bool {
	if len(p.EnabledAssets) == 0 {
		return isKnownAsset(asset) || isCashAsset(Asset(asset))
	}
	for _, a := range p.EnabledAssets {
		if a == asset || (isCashAsset(Asset(asset)) && (a == "USD" || a == "USDT" || assetsParity(a, asset))) {
			return true
		}
	}
	// Cash nativo del venue siempre permitido si el usuario habilitó "USD"/"USDT".
	if isCashAsset(Asset(asset)) {
		for _, a := range p.EnabledAssets {
			if isCashAsset(Asset(a)) {
				return true
			}
		}
	}
	return false
}

// rebalanceWallets50_50 es el nombre histórico (wire + tests). Delega al
// reequilibrio global agnóstico.
func (e *HFTEngine) rebalanceWallets50_50(session *ClientSession) {
	e.rebalanceGlobalInventory(session)
}

// rebalanceGlobalInventory reparte el inventario de la sesión:
//  1. Si hay crédito activo, devuelve el principal prestado (todos los activos).
//  2. Pool de CASH (USD/USDT a paridad 1:1) → se redistribuye como quote nativo
//     de cada venue habilitado (Target = totalCash / N).
//  3. Por cada cripto (BTC, ETH, SOL…): Total / N entre venues que la soportan
//     y están en el universo.
// Conserva el patrimonio mark-to-market (salvo dust de redondeo).
func (e *HFTEngine) rebalanceGlobalInventory(session *ClientSession) {
	session.Mu.Lock()
	if session.Wallets == nil {
		session.Mu.Unlock()
		return
	}

	repayCreditLocked(session)

	p := session.Params()
	venues := enabledVenuesForSession(p)
	if len(venues) == 0 {
		session.Mu.Unlock()
		return
	}

	// ── 1) Pool de cash (USD-equivalente) ──────────────────────────────────
	cashTotal := 0.0
	for venueName, assets := range session.Wallets {
		for asset, amt := range assets {
			if amt == 0 || !isCashAsset(Asset(asset)) {
				continue
			}
			cashTotal += amt
			session.Wallets.Set(venueName, asset, 0)
		}
	}
	cashVenues := make([]Venue, 0, len(venues))
	for _, v := range venues {
		if v.QuoteAsset != "" {
			cashVenues = append(cashVenues, v)
		}
	}
	if n := len(cashVenues); n > 0 && cashTotal > 0 {
		per := cashTotal / float64(n)
		for _, v := range cashVenues {
			session.Wallets.Set(v.Name, v.QuoteAsset, per)
		}
	}

	// ── 2) Cada cripto por separado ────────────────────────────────────────
	cryptoTotals := map[string]float64{}
	for venueName, assets := range session.Wallets {
		for asset, amt := range assets {
			if amt == 0 || isCashAsset(Asset(asset)) {
				continue
			}
			cryptoTotals[asset] += amt
			session.Wallets.Set(venueName, asset, 0)
		}
	}
	cryptoKeys := make([]string, 0, len(cryptoTotals))
	for a := range cryptoTotals {
		cryptoKeys = append(cryptoKeys, a)
	}
	sort.Strings(cryptoKeys)

	targets := map[string]map[string]float64{} // asset → venue → qty (para el wire)
	for _, asset := range cryptoKeys {
		total := cryptoTotals[asset]
		if total <= 0 || !assetEnabledInUniverse(p, asset) {
			// Activo fuera del universo: se deja en el primer venue que lo
			// soportaba (no destruir capital); aquí total ya se zeró — reponer
			// en el primer venue habilitado que lo soporte, o en el primero del registro.
			hosts := venuesSupportingAsset(venues, asset)
			if len(hosts) == 0 {
				hosts = venuesSupportingAsset(Venues, asset)
			}
			if len(hosts) > 0 && total > 0 {
				session.Wallets.Set(hosts[0].Name, asset, total)
			}
			continue
		}
		hosts := venuesSupportingAsset(venues, asset)
		if len(hosts) == 0 {
			continue
		}
		per := total / float64(len(hosts))
		targets[asset] = make(map[string]float64, len(hosts))
		for _, v := range hosts {
			session.Wallets.Set(v.Name, asset, per)
			targets[asset][v.Name] = per
		}
	}

	// ── 3) Patrimonio mark-to-market ────────────────────────────────────────
	totalWealth := 0.0
	for venueName, assets := range session.Wallets {
		_ = venueName
		for asset, amt := range assets {
			totalWealth += amt * assetPriceUSD(asset)
		}
	}
	session.TotalWealth = totalWealth

	// Snapshot para el payload (fuera del lock se serializa).
	binUSD := session.Wallets.Get("Binance", quoteOf("Binance"))
	binBTC := session.Wallets.Get("Binance", baseOf("Binance"))
	bitUSD := session.Wallets.Get("Bitso", quoteOf("Bitso"))
	bitBTC := session.Wallets.Get("Bitso", baseOf("Bitso"))
	balances := session.Wallets.Clone()
	session.Mu.Unlock()

	sendLog(session, fmt.Sprintf("🔄 [REEQUILIBRIO GLOBAL] Inventario repartido entre %d venues. Riqueza total: $%.2f USD", len(venues), totalWealth))

	payload := map[string]interface{}{
		"event":            "market_rebalanced",
		"total_wealth_usd": totalWealth,
		"balances":         balances,
		// Compat dashboard 2-venue:
		"binance_usd": binUSD,
		"binance_btc": binBTC,
		"bitso_usd":   bitUSD,
		"bitso_btc":   bitBTC,
		"targets":     targets,
	}
	b, _ := json.Marshal(payload)
	session.WriteMessage(websocket.TextMessage, b)
	sendWalletUpdate(session)
	e.pushSessionGraph(session)

	recordTradeAsync(TradeRecord{
		SessionID:         session.ID,
		Timestamp:         time.Now(),
		BuyExchange:       "Sistema",
		SellExchange:      "Rebalanceo",
		VolumeBTC:         cryptoTotals["BTC"],
		SpreadUSD:         0,
		NetProfitUSD:      0,
		IsCreditInjection: true,
	})
}

// repayCreditLocked asume session.Mu sostenido: resta el principal prestado de
// cada venue/activo y limpia el estado de crédito.
func repayCreditLocked(session *ClientSession) {
	if !session.Credit.Active {
		return
	}
	if session.Credit.Borrowed != nil {
		for venue, assets := range session.Credit.Borrowed {
			for asset, amt := range assets {
				session.Wallets.Add(venue, asset, -amt)
			}
		}
	} else {
		// Compat sesiones / tests antiguos con mapas USD/BTC.
		for ex, amt := range session.Credit.BorrowedUSD {
			session.Wallets.Add(ex, quoteOf(ex), -amt)
		}
		for ex, amt := range session.Credit.BorrowedBTC {
			session.Wallets.Add(ex, baseOf(ex), -amt)
		}
	}
	session.Credit.Active = false
	session.Credit.Borrowed = nil
	session.Credit.BorrowedUSD = nil
	session.Credit.BorrowedBTC = nil
}

// applyHypotheticalCredit inyecta en `w` (copia) la misma liquidez que
// activateCreditSession — sin cobrar fee ni mutar CreditState. cryptoExtra son
// activos adicionales a fondear (p. ej. ETH/SOL del ciclo que bloquea).
func applyHypotheticalCredit(w Balances, p TradingParameters, cryptoExtra []string) {
	if w == nil {
		return
	}
	venues := enabledVenuesForSession(p)
	if len(venues) == 0 {
		venues = append([]Venue(nil), Venues...)
	}
	n := float64(len(venues))
	if n == 0 {
		return
	}
	perCash := p.CreditLineUSD / n
	for _, v := range venues {
		w.Add(v.Name, v.QuoteAsset, perCash)
	}

	// BTC: línea explícita repartida entre venues que lo soportan.
	btcHosts := venuesSupportingAsset(venues, "BTC")
	if len(btcHosts) > 0 && p.CreditLineBTC > 0 {
		perBTC := p.CreditLineBTC / float64(len(btcHosts))
		for _, v := range btcHosts {
			w.Add(v.Name, "BTC", perBTC)
		}
	}

	// Cripto dinámica: convierte una fracción de la línea USD al activo bloqueante.
	injectDynamicCrypto(w, venues, p.CreditLineUSD, cryptoExtra)
}

// injectDynamicCrypto reparte ~30 % de la línea USD (en notional) entre los
// activos cripto extra (ETH/SOL…), convertidos a unidades al precio de mercado.
func injectDynamicCrypto(w Balances, venues []Venue, creditLineUSD float64, assets []string) {
	seen := map[string]bool{"BTC": true} // BTC ya va por CreditLineBTC
	var list []string
	for _, a := range assets {
		if a == "" || isCashAsset(Asset(a)) || seen[a] {
			continue
		}
		seen[a] = true
		list = append(list, a)
	}
	if len(list) == 0 || creditLineUSD <= 0 {
		return
	}
	budget := creditLineUSD * 0.30 / float64(len(list))
	for _, asset := range list {
		px := assetPriceUSD(asset)
		if px <= 0 {
			continue
		}
		hosts := venuesSupportingAsset(venues, asset)
		if len(hosts) == 0 {
			continue
		}
		qty := (budget / px) / float64(len(hosts))
		for _, v := range hosts {
			w.Add(v.Name, asset, qty)
		}
	}
}

// creditInjectionPlan calcula el reparto real del préstamo (cash + BTC + extras).
type creditInjectionPlan struct {
	borrowed   Balances
	cost       float64
	perCash    float64
	btcTotal   float64
	cryptoNote string
}

func buildCreditInjection(p TradingParameters, cryptoExtra []string) creditInjectionPlan {
	venues := enabledVenuesForSession(p)
	if len(venues) == 0 {
		venues = append([]Venue(nil), Venues...)
	}
	n := len(venues)
	plan := creditInjectionPlan{
		borrowed: make(Balances),
		cost:     calculateCreditCost(p),
	}
	if n == 0 {
		return plan
	}
	plan.perCash = p.CreditLineUSD / float64(n)
	for _, v := range venues {
		plan.borrowed.Add(v.Name, v.QuoteAsset, plan.perCash)
	}
	btcHosts := venuesSupportingAsset(venues, "BTC")
	if len(btcHosts) > 0 && p.CreditLineBTC > 0 {
		perBTC := p.CreditLineBTC / float64(len(btcHosts))
		plan.btcTotal = p.CreditLineBTC
		for _, v := range btcHosts {
			plan.borrowed.Add(v.Name, "BTC", perBTC)
		}
	}
	// Copia temporal para medir extras.
	tmp := make(Balances)
	injectDynamicCrypto(tmp, venues, p.CreditLineUSD, cryptoExtra)
	for venue, assets := range tmp {
		for asset, amt := range assets {
			plan.borrowed.Add(venue, asset, amt)
			if plan.cryptoNote == "" {
				plan.cryptoNote = asset
			} else if asset != "BTC" && asset != plan.cryptoNote {
				plan.cryptoNote += "+" + asset
			}
		}
	}
	return plan
}
