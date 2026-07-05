package main

import "time"

// graph.go — PREPARACIÓN DE LA FASE 2 (motor de arbitraje omnidireccional).
//
// ⚠️ SOLO TIPOS Y CONTRATOS: aquí NO hay lógica de detección todavía. La Fase 2
// (ver docs/FASE2-GRAFO.md) implementará la búsqueda de ciclos negativos sobre
// estos tipos. Se definen ahora para que la Fase 1 (ingesta normalizada, venues
// como datos, parámetros por sesión) quede comprobadamente alineada con el
// modelo de grafo que la sustituirá al núcleo de dos venues.
//
// El modelo: el mercado es un grafo dirigido donde
//   - un NODO es un activo en un lugar concreto: BTC@Binance, USDT@Binance, USD@Bitso…
//   - una ARISTA es una forma de convertir un activo en otro: un libro de órdenes
//     (comprar/vender con fee) o una transferencia entre venues (con fee de retiro
//     + fee de red + tiempo).
//   - una OPORTUNIDAD DE ARBITRAJE es un ciclo cuyo producto de tasas efectivas
//     supera 1 — equivalentemente, un ciclo de peso NEGATIVO usando el peso
//     w = −log(tasa × (1 − fee)), detectable con Bellman-Ford/SPFA.
//
// El arbitraje actual Binance↔Bitso es el caso particular de un ciclo de 2 aristas;
// el triangular intra-exchange (BTC/USDT + ETH/USDT + ETH/BTC) es un ciclo de 3
// aristas en un solo venue y será el primer hito demostrable de la Fase 2.

// Asset identifica una moneda o token ("BTC", "USD", "USDT", "ETH"…).
// USDT y USD son activos DISTINTOS en el grafo: el basis entre ellos deja de ser
// una suposición (AssumeUSDTParity) y pasa a ser una arista con precio real.
type Asset string

// MarketNode es un nodo del grafo: un activo custodiado en un venue concreto.
// El mismo activo en dos venues son dos nodos distintos (moverlo cuesta tiempo y
// fees — exactamente lo que modelan las aristas de transferencia).
type MarketNode struct {
	Asset Asset
	Venue string // Venue.Name del registro (venues.go)
}

// EdgeKind clasifica las conversiones posibles entre nodos.
type EdgeKind int

const (
	// EdgeOrderBook convierte activos DENTRO de un venue vía un libro de órdenes
	// (p. ej. USDT@Binance → BTC@Binance comprando al ask). Su tasa viene del
	// top-of-book del FeedAdapter y su fee del TradingParameters de la sesión.
	EdgeOrderBook EdgeKind = iota
	// EdgeTransfer mueve el MISMO activo entre venues (BTC@Binance → BTC@Bitso).
	// Modela fee de retiro + fee de red + tiempo de confirmación: es la arista que
	// hace honesto el costo real de reequilibrar inventario.
	EdgeTransfer
)

// Edge es una conversión dirigida entre dos nodos del grafo con su costo total.
type Edge struct {
	Kind EdgeKind
	From MarketNode
	To   MarketNode

	// Rate es cuántas unidades de To se obtienen por 1 unidad de From, ANTES de
	// fees (para un libro: el precio top-of-book en la dirección correspondiente).
	Rate float64
	// Fee es la fracción cobrada por usar la arista (taker fee o fee de retiro).
	Fee float64
	// Liquidity acota cuánto volumen soporta la arista al Rate visible
	// (cantidad del top-of-book para libros; sin límite práctico para transfers).
	Liquidity float64
	// Latency estima cuánto tarda la conversión (≈0 para órdenes de mercado,
	// ~30+ min para transfers on-chain). La Fase 2 la usa para descartar ciclos
	// cuyo riesgo temporal excede el apetito del usuario.
	Latency time.Duration
	// UpdatedAt habilita el mismo control de staleness que la Fase 1 aplica a los
	// libros: una arista vieja no participa en la búsqueda de ciclos.
	UpdatedAt time.Time

	// Weight es el peso para la búsqueda de ciclos negativos:
	//   Weight = −log(Rate × (1 − Fee))
	// Un ciclo con suma de pesos < 0 equivale a un producto de tasas efectivas > 1:
	// arbitraje. Se precalcula al actualizar la arista para que la búsqueda sea
	// aritmética pura sobre floats (sin logaritmos en el hot path).
	Weight float64
}

// Cycle es una oportunidad detectada: la secuencia de aristas que sale de un nodo
// y regresa a él con ganancia neta positiva tras todos los fees y el slippage.
type Cycle struct {
	Edges []Edge
	// NetReturn es la tasa neta del ciclo (0.001 = +0.1 % por vuelta) ANTES de
	// aplicar el volumen; el volumen ejecutable lo acota la arista menos líquida
	// (idéntico principio que sizeOrder en el núcleo de dos venues).
	NetReturn float64
	// MaxVolume es el volumen máximo ejecutable limitado por Liquidity de las aristas.
	MaxVolume float64
}

// Universe es el subgrafo PERSONAL de una sesión: los venues y activos con los que
// el usuario decidió jugar (y para los que cumple requisitos). La búsqueda de
// ciclos de la Fase 2 se ejecuta solo sobre las aristas cuyo From y To pertenecen
// al universo — la personalización es literalmente una poda del grafo global.
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
