package main

// venues.go — REGISTRO DE VENUES (Fase 1 · arquitectura omnidireccional).
//
// Antes, "Binance" y "Bitso" eran strings quemados por todo el motor. Este registro
// convierte los exchanges en DATOS: agregar un venue nuevo (Kraken, OKX…) es añadir
// una entrada aquí + un FeedAdapter (ver feed.go), sin tocar la lógica de negocio.
// Es el prerequisito del motor de grafos de la Fase 2, donde cada venue aporta nodos
// (activo@venue) y aristas (libros de órdenes) al grafo global.

// Venue describe una casa de cambio soportada por el motor.
type Venue struct {
	Name string // identificador único, usado como clave de wallets/fees ("Binance")

	// DefaultTakerFee es la comisión taker por defecto del venue. El usuario puede
	// sobrescribirla por sesión desde el panel de estrategia (TradingParameters.TakerFees).
	DefaultTakerFee float64

	// BaseAsset / QuoteAsset del instrumento que el feed de este venue publica hoy.
	// NOTA IMPORTANTE (basis USDT/USD): Binance opera BTC/USDT y Bitso BTC/USD.
	// USDT y USD NO son el mismo activo; hoy el motor los trata como equivalentes
	// (AssumeUSDTParity) para el arbitraje de demo, y lo documenta en vez de ocultarlo.
	// En la Fase 2 serán nodos distintos del grafo y el basis será una arista más.
	BaseAsset  string
	QuoteAsset string
}

// AssumeUSDTParity documenta la simplificación vigente: tratamos 1 USDT = 1 USD al
// comparar Binance (BTC/USDT) contra Bitso (BTC/USD). Es una aproximación razonable
// (el basis típico es de pocos puntos base) pero es una DECISIÓN explícita, no un
// descuido. La Fase 2 la elimina modelando USDT y USD como activos separados.
const AssumeUSDTParity = true

// Venues es la fuente de verdad de los exchanges activos. El orden es estable
// (se usa para iterar de forma determinista en logs y reequilibrios).
var Venues = []Venue{
	{Name: "Binance", DefaultTakerFee: 0.001, BaseAsset: "BTC", QuoteAsset: "USDT"},
	{Name: "Bitso", DefaultTakerFee: 0.0065, BaseAsset: "BTC", QuoteAsset: "USD"},
}

// VenueNames devuelve los nombres de los venues activos, en orden estable.
func VenueNames() []string {
	names := make([]string, 0, len(Venues))
	for _, v := range Venues {
		names = append(names, v.Name)
	}
	return names
}

// venueByName busca un venue por nombre; ok=false si no está registrado.
func venueByName(name string) (Venue, bool) {
	for _, v := range Venues {
		if v.Name == name {
			return v, true
		}
	}
	return Venue{}, false
}

// isKnownVenue valida que un nombre de venue exista en el registro (defensa de
// entradas del cliente: nunca crear wallets/fees para venues inventados).
func isKnownVenue(name string) bool {
	_, ok := venueByName(name)
	return ok
}

// defaultTakerFees construye el mapa de fees por venue a partir del registro.
// Cada llamada devuelve un mapa NUEVO: los TradingParameters se comparten entre
// goroutines como snapshots de solo lectura y jamás deben compartir un mapa mutable.
func defaultTakerFees() map[string]float64 {
	fees := make(map[string]float64, len(Venues))
	for _, v := range Venues {
		fees[v.Name] = v.DefaultTakerFee
	}
	return fees
}
