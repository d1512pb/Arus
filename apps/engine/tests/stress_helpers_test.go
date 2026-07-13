package main

import (
	"testing"
	"time"
)

func stressEngineFixture(t *testing.T) (*HFTEngine, *Hub, chan PriceTick, *ClientSession) {
	t.Helper()
	e := &HFTEngine{Tracker: NewSpreadTracker(), Graph: NewLiquidityGraph()}
	hub := NewHub()
	priceChan := make(chan PriceTick, 512)
	s := newClientSession("stress-"+t.Name(), nil)
	initSession(s, 100_000, 2.0, nil, nil, nil)
	hub.Add(s)
	go e.Start(priceChan, hub)
	t.Cleanup(func() { close(priceChan) })
	return e, hub, priceChan, s
}

func waitUntil(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timeout esperando condición de la prueba")
}
