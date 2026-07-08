package main

import (
	"log"
	"net/http"
	"os"
)

func main() {
	log.Println("Iniciando Arus HFT Engine - Fase 1 (Arbitraje Simultáneo Pre-fondeado) [Multi-Tenant]")

	// Persistencia: Trade Ledger en SQLite (registro de auditoría inmutable).
	if err := InitLedger(); err != nil {
		log.Printf("⚠️ [LEDGER] No se pudo inicializar SQLite, el trading continúa sin persistencia: %v", err)
	}

	// Crear el flujo de eventos temprano para que esté disponible en las rutas
	priceChan := make(chan PriceTick, 1000) // Buffer de 1000 eventos

	hub := NewHub()

	engine := &HFTEngine{
		Tracker: NewSpreadTracker(),
		// Radar omnidireccional (Fase 2): grafo de liquidez construido desde el
		// registro de venues, actualizado por tick y con detección automática de
		// ciclos de arbitraje (Bellman-Ford sobre pesos -log(tasa·(1-fee))).
		Graph: NewLiquidityGraph(),
	}

	http.HandleFunc("/ws", wsHandler(hub, engine))
	http.HandleFunc("/api/ledger", ledgerHandler)
	// Analítica derivada del ledger (Sprint D): resumen por sesión y export CSV.
	http.HandleFunc("/api/stats", statsHandler)
	http.HandleFunc("/api/ledger.csv", ledgerCSVHandler)

	// El puerto se toma de la variable de entorno PORT (Railway, Render, Cloud Run
	// la inyectan al desplegar); por defecto 8080 para desarrollo local.
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Servidor levantado en :%s (WebSocket en /ws, ledger en /api/ledger)", port)

	go engine.Start(priceChan, hub)

	// Ingesta desacoplada: cada venue registrado (venues.go) arranca su FeedAdapter
	// (feed.go) y publica ticks normalizados en el mismo canal.
	StartFeeds(priceChan)

	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("Error crítico al levantar servidor: %v", err)
	}
}
