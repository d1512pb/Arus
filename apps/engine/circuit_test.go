package main

import "testing"

func TestIsValidTickRejectsFakeSpikeJump(t *testing.T) {
	anchor := 60_000.0
	poison := anchor * (1 + fakeSpikeJump)
	if isValidTick(poison, anchor, DefaultSpikeTickDeviation) {
		t.Fatalf("salto +%.0f%% debía rechazarse con tolerancia %.1f%%", fakeSpikeJump*100, DefaultSpikeTickDeviation*100)
	}
	if !isValidTick(anchor*1.04, anchor, DefaultSpikeTickDeviation) {
		t.Fatal("salto +4% dentro del Spike Filter debía aceptarse")
	}
}

func TestClassicPairEnabledRespectsUniverse(t *testing.T) {
	p := DefaultTradingParameters()
	if !classicPairEnabled(p) {
		t.Fatal("universo completo debía habilitar el par clásico")
	}
	p.EnabledVenues = []string{"Kraken"}
	if classicPairEnabled(p) {
		t.Fatal("solo Kraken no debe habilitar Binance/Bitso")
	}
}

func TestWealthSnapshotUnchangedHelper(t *testing.T) {
	s := &ClientSession{ID: "cb-test"}
	s.Wallets = Balances{"Binance": {"USDT": 1000}}
	s.TotalWealth = 1000
	s.TotalNetProfit = 5
	w, p := snapshotWealth(s)
	if !wealthUnchanged(s, w, p) {
		t.Fatal("sin cambios debía reportar intacto")
	}
	s.Mu.Lock()
	s.TotalNetProfit = 6
	s.Mu.Unlock()
	if wealthUnchanged(s, w, p) {
		t.Fatal("PnL movido debía detectarse")
	}
}
