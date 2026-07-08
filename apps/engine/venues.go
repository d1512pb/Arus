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
// BaseAsset/QuoteAsset señalan los activos RESPALDADOS POR WALLET del venue (el
// par principal que el motor de dos venues ejecuta); los demás instrumentos del
// registro de abajo alimentan al radar aunque el usuario aún no tenga saldo ahí.
var Venues = []Venue{
	{Name: "Binance", DefaultTakerFee: 0.001, BaseAsset: "BTC", QuoteAsset: "USDT"},
	{Name: "Bitso", DefaultTakerFee: 0.0065, BaseAsset: "BTC", QuoteAsset: "USD"},
	// Kraken (Sprint C): taker 0.40 % = tier base de Kraken Pro. Tercer venue del
	// radar; NO forma parte del par clásico (ver classicPair) — sus saldos llegan
	// por depósitos del usuario o por ciclos del autopiloto, no por el 50/50.
	{Name: "Kraken", DefaultTakerFee: 0.0040, BaseAsset: "BTC", QuoteAsset: "USD"},
}

// classicPair son los DOS venues del modo clásico: el ejecutor del par BTC
// (executeForSession), el reparto inicial 50/50, la línea de crédito y el
// reequilibrio operan SOLO sobre ellos. Los demás venues del registro (Kraken…)
// participan en el radar y en el autopiloto, pero no reciben capital automático:
// sin esta distinción, agregar un venue inflaría el capital inicial (usd/2 por
// venue) y diluiría el crédito entre exchanges que el par clásico nunca opera.
var classicPair = [2]string{"Binance", "Bitso"}

// isClassicVenue informa si un venue pertenece al par clásico.
func isClassicVenue(name string) bool {
	return name == classicPair[0] || name == classicPair[1]
}

// classicVenues devuelve los Venue del par clásico, en orden estable.
func classicVenues() []Venue {
	out := make([]Venue, 0, len(classicPair))
	for _, name := range classicPair {
		if v, ok := venueByName(name); ok {
			out = append(out, v)
		}
	}
	return out
}

// Instrument es un libro de órdenes concreto de un venue (Fase 2 · hito 2: un
// venue puede publicar N libros). Agregar un instrumento = 1 entrada aquí; el
// FeedAdapter del venue se suscribe solo y el grafo gana sus nodos y aristas.
type Instrument struct {
	Venue string
	Base  string // activo que se compra/vende ("BTC", "ETH")
	Quote string // activo con el que se paga ("USDT", "USD", "BTC")
	// StreamID identifica el libro dentro del venue (Binance: símbolo del stream
	// combinado en minúsculas; Bitso: nombre del book del canal orders).
	StreamID string
}

// Key es el identificador estable del instrumento ("Binance:ETH/BTC").
func (i Instrument) Key() string { return i.Venue + ":" + i.Base + "/" + i.Quote }

// Instruments registra los libros activos. Binance publica DOS triángulos
// (BTC/USDT · ETH/USDT · ETH/BTC y BTC/USDT · SOL/USDT · SOL/BTC): con ellos el
// radar detecta arbitraje triangular DENTRO de un solo exchange, con datos
// reales. Kraken (Sprint C) aporta su propio triángulo BTC/USD · ETH/USD ·
// ETH/BTC y habilita ciclos espaciales contra Bitso (mismo quote USD) y contra
// Binance (vía la paridad USDT≈USD declarada).
var Instruments = []Instrument{
	{Venue: "Binance", Base: "BTC", Quote: "USDT", StreamID: "btcusdt"},
	{Venue: "Binance", Base: "ETH", Quote: "USDT", StreamID: "ethusdt"},
	{Venue: "Binance", Base: "ETH", Quote: "BTC", StreamID: "ethbtc"},
	{Venue: "Binance", Base: "SOL", Quote: "USDT", StreamID: "solusdt"},
	{Venue: "Binance", Base: "SOL", Quote: "BTC", StreamID: "solbtc"},
	{Venue: "Bitso", Base: "BTC", Quote: "USD", StreamID: "btc_usd"},
	// Kraken WS v2 identifica los libros por su símbolo normalizado ("BTC/USD").
	{Venue: "Kraken", Base: "BTC", Quote: "USD", StreamID: "BTC/USD"},
	{Venue: "Kraken", Base: "ETH", Quote: "USD", StreamID: "ETH/USD"},
	{Venue: "Kraken", Base: "ETH", Quote: "BTC", StreamID: "ETH/BTC"},
}

// instrumentByKey busca un instrumento por su clave ("Binance:ETH/BTC").
func instrumentByKey(key string) (Instrument, bool) {
	for _, i := range Instruments {
		if i.Key() == key {
			return i, true
		}
	}
	return Instrument{}, false
}

// instrumentsForVenue devuelve los libros de un venue, en orden estable.
func instrumentsForVenue(venue string) []Instrument {
	list := make([]Instrument, 0, len(Instruments))
	for _, i := range Instruments {
		if i.Venue == venue {
			list = append(list, i)
		}
	}
	return list
}

// primaryInstrument es el libro RESPALDADO POR WALLETS de un venue (Base/Quote
// del registro de venues): el que ejecuta el motor de dos venues.
func primaryInstrument(venue string) (Instrument, bool) {
	v, ok := venueByName(venue)
	if !ok {
		return Instrument{}, false
	}
	for _, i := range Instruments {
		if i.Venue == venue && i.Base == v.BaseAsset && i.Quote == v.QuoteAsset {
			return i, true
		}
	}
	return Instrument{}, false
}

// quoteOf / baseOf devuelven los activos respaldados por wallet de un venue
// (el par principal del registro); "" si el venue no existe.
func quoteOf(venue string) string {
	if v, ok := venueByName(venue); ok {
		return v.QuoteAsset
	}
	return ""
}

func baseOf(venue string) string {
	if v, ok := venueByName(venue); ok {
		return v.BaseAsset
	}
	return ""
}

// knownAssets devuelve el catálogo de activos que aparecen en algún instrumento
// (para validar el universo del usuario), en orden estable.
func knownAssets() []string {
	seen := map[string]bool{}
	var out []string
	for _, in := range Instruments {
		for _, a := range []string{in.Quote, in.Base} {
			if !seen[a] {
				seen[a] = true
				out = append(out, a)
			}
		}
	}
	return out
}

// isKnownAsset valida que un activo exista en el catálogo de instrumentos.
func isKnownAsset(a string) bool {
	for _, k := range knownAssets() {
		if k == a {
			return true
		}
	}
	return false
}

// parityPairs declara qué activos distintos se tratan como equivalentes 1:1
// (supuesto visible en el grafo como aristas EdgeParity). Hoy: USDT ≈ USD.
var parityPairs = [][2]string{{"USDT", "USD"}}

// assetsParity informa si dos activos distintos están declarados equivalentes.
func assetsParity(a, b string) bool {
	for _, p := range parityPairs {
		if (p[0] == a && p[1] == b) || (p[0] == b && p[1] == a) {
			return true
		}
	}
	return false
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
