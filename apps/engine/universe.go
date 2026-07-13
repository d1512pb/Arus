package main

import "fmt"

// universe.go — capacidad de arbitraje del universo del usuario.
//
// Un universo demasiado estrecho (p. ej. 1 casa + solo BTC/efectivo) no admite
// ningún ciclo: no hay espacial (hace falta otra casa) ni triangular (hace falta
// ETH/SOL). Las pruebas y el radar quedarían muertos. analyzeUniverse detecta
// eso ANTES de aplicar la estrategia o de disparar omni/tormenta.

// UniverseCapability resume qué formas de arbitraje admite el universo.
type UniverseCapability struct {
	Spatial     bool
	Triangular  bool
	VenueCount  int
	CryptoCount int
	OK          bool
	Reason      string
}

// analyzeUniverse inspecciona el catálogo (no los libros en vivo): ¿el universo
// APLICADO puede sostener al menos un ciclo espacial o triangular?
func analyzeUniverse(p TradingParameters) UniverseCapability {
	type venueBTC struct {
		name, cash string
	}
	var withBTC []venueBTC
	cryptos := map[string]bool{}
	venuesSeen := map[string]bool{}

	for _, v := range Venues {
		if !p.venueEnabled(v.Name) {
			continue
		}
		venuesSeen[v.Name] = true
		cash := v.QuoteAsset
		if !isCashAsset(Asset(cash)) || !omniNodeAllowed(p, cash, v.Name) {
			continue
		}
		if omniNodeAllowed(p, "BTC", v.Name) && hasInstrument(v.Name, "BTC", cash) {
			withBTC = append(withBTC, venueBTC{v.Name, cash})
			cryptos["BTC"] = true
		}
		for _, mid := range []string{"ETH", "SOL"} {
			if !omniNodeAllowed(p, mid, v.Name) {
				continue
			}
			if !hasInstrument(v.Name, "BTC", cash) ||
				!hasInstrument(v.Name, mid, "BTC") ||
				!hasInstrument(v.Name, mid, cash) {
				continue
			}
			cryptos[mid] = true
		}
	}

	cap := UniverseCapability{
		VenueCount:  len(venuesSeen),
		CryptoCount: len(cryptos),
	}

	// Espacial: ≥2 casas con BTC/cash y retorno de cash (mismo quote o paridad).
	for i := 0; i < len(withBTC); i++ {
		for j := i + 1; j < len(withBTC); j++ {
			a, b := withBTC[i], withBTC[j]
			if a.cash == b.cash || assetsParity(a.cash, b.cash) {
				cap.Spatial = true
				break
			}
		}
		if cap.Spatial {
			break
		}
	}

	// Triangular: al menos un venue con triángulo cash↔BTC↔mid completo y mid activo.
	for _, v := range Venues {
		if !p.venueEnabled(v.Name) || !omniNodeAllowed(p, "BTC", v.Name) {
			continue
		}
		cash := v.QuoteAsset
		if !isCashAsset(Asset(cash)) || !omniNodeAllowed(p, cash, v.Name) {
			continue
		}
		for _, mid := range []string{"ETH", "SOL"} {
			if !omniNodeAllowed(p, mid, v.Name) {
				continue
			}
			if hasInstrument(v.Name, "BTC", cash) &&
				hasInstrument(v.Name, mid, "BTC") &&
				hasInstrument(v.Name, mid, cash) {
				cap.Triangular = true
				break
			}
		}
		if cap.Triangular {
			break
		}
	}

	cap.OK = cap.Spatial || cap.Triangular
	if cap.OK {
		return cap
	}

	switch {
	case cap.VenueCount == 0:
		cap.Reason = "No hay ninguna casa de cambio activa. Activa al menos una en Estrategia → Tu universo."
	case cap.VenueCount == 1 && cap.CryptoCount <= 1:
		cap.Reason = "Con una sola casa y solo BTC/efectivo no existe ciclo de arbitraje: el bot no puede ganar comprando y vendiendo el mismo libro. Activa otra casa (arbitraje espacial) o una moneda intermedia como ETH o SOL (triangular)."
	case cap.VenueCount == 1:
		cap.Reason = "Tu única casa no tiene un triángulo operable (BTC + ETH/SOL + efectivo). Activa ETH o SOL en esa casa, o añade una segunda casa de cambio."
	case len(withBTC) < 2 && !cap.Triangular:
		cap.Reason = "Ninguna ruta cierra: hace falta BTC en al menos dos casas (con efectivo compatible) o un triangular BTC↔ETH/SOL en una casa. Revisa casas y monedas en Estrategia."
	default:
		cap.Reason = "Este universo no admite arbitraje espacial ni triangular. Amplía casas o monedas en Estrategia → Tu universo."
	}
	return cap
}

// venueHasAsset informa si el venue publica ese activo (cash del venue o base/quote de algún libro).
func venueHasAsset(venue, asset string) bool {
	if !isKnownVenue(venue) {
		return false
	}
	q := quoteOf(venue)
	if asset == q || assetsParity(asset, q) {
		return true
	}
	for _, in := range Instruments {
		if in.Venue == venue && (in.Base == asset || in.Quote == asset) {
			return true
		}
	}
	return false
}

// describeUniverseCap mensaje corto para logs / UI.
func describeUniverseCap(c UniverseCapability) string {
	if c.OK {
		parts := []string{}
		if c.Spatial {
			parts = append(parts, "espacial")
		}
		if c.Triangular {
			parts = append(parts, "triangular")
		}
		return fmt.Sprintf("universo operable (%s)", joinComma(parts))
	}
	return c.Reason
}

func joinComma(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	default:
		return parts[0] + " + " + parts[1]
	}
}
