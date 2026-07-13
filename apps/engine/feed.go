package main

import "log"

// feed.go — CAPA DE INGESTA DESACOPLADA (Fase 1 · arquitectura omnidireccional).
//
// FeedAdapter es el contrato que separa "de dónde vienen los precios" de "qué hace
// el motor con ellos". Cada exchange implementa su propio adaptador (conexión,
// suscripción, parseo, reconexión) y publica ticks NORMALIZADOS (PriceTick con
// bid/ask, cantidades del top-of-book y timestamp) en el canal compartido.
//
// Agregar un exchange nuevo = 1 entrada en venues.go + 1 FeedAdapter aquí registrado.
// El bucle de detección no cambia. En la Fase 2, estos mismos ticks alimentarán las
// aristas del grafo de liquidez.

// FeedAdapter es una fuente de precios en tiempo real de UN venue. Run debe
// bloquear para siempre: mantiene la conexión viva (reconexión incluida) y publica
// cada actualización del top-of-book en priceChan.
type FeedAdapter interface {
	// Name devuelve el nombre del venue del registro (venues.go) al que alimenta.
	Name() string
	// Run conecta, se suscribe y publica ticks normalizados. No retorna (se invoca
	// en su propia goroutine); ante errores reintenta con espera, jamás hace panic.
	Run(priceChan chan<- PriceTick)
}

// feedAdapters registra las fuentes activas. El nombre de cada adaptador debe
// existir en Venues (venues.go): el registro es la fuente de verdad de los venues.
var feedAdapters = []FeedAdapter{
	binanceFeed{},
	bitsoFeed{},
	krakenFeed{},
}

// StartFeeds lanza todos los adaptadores registrados, cada uno en su goroutine.
// Sustituye al antiguo StartRealMarketWS acoplado a dos exchanges concretos.
func StartFeeds(priceChan chan<- PriceTick) {
	for _, adapter := range feedAdapters {
		if !isKnownVenue(adapter.Name()) {
			log.Printf("⚠️ [FEEDS] Adaptador %q no está en el registro de venues — omitido", adapter.Name())
			continue
		}
		log.Printf("🔗 [FEEDS] Iniciando feed de %s...", adapter.Name())
		go adapter.Run(priceChan)
	}
}
