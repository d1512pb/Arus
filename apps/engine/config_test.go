package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestValidateCatalog: las reglas que impiden que un JSON externo deje al motor
// sin mercado o sin el par clásico (del que dependen capital inicial y crédito).
func TestValidateCatalog(t *testing.T) {
	valid := CatalogFile{
		Venues: []Venue{
			{Name: "Binance", DefaultTakerFee: 0.001, BaseAsset: "BTC", QuoteAsset: "USDT"},
			{Name: "Bitso", DefaultTakerFee: 0.0065, BaseAsset: "BTC", QuoteAsset: "USD"},
		},
		Instruments: []Instrument{
			{Venue: "Binance", Base: "BTC", Quote: "USDT", StreamID: "btcusdt"},
			{Venue: "Bitso", Base: "BTC", Quote: "USD", StreamID: "btc_usd"},
		},
		ParityPairs: [][2]string{{"USDT", "USD"}},
	}
	if err := validateCatalog(valid); err != nil {
		t.Fatalf("catálogo válido rechazado: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(cf *CatalogFile)
	}{
		{"vacío", func(cf *CatalogFile) { cf.Venues = nil }},
		{"venue duplicado", func(cf *CatalogFile) { cf.Venues = append(cf.Venues, cf.Venues[0]) }},
		{"fee absurdo", func(cf *CatalogFile) { cf.Venues[0].DefaultTakerFee = 0.5 }},
		{"sin el par clásico", func(cf *CatalogFile) { cf.Venues[1].Name = "OKX"; cf.Instruments[1].Venue = "OKX" }},
		{"instrumento de venue no declarado", func(cf *CatalogFile) { cf.Instruments[0].Venue = "Fantasma" }},
		{"instrumento sin stream_id", func(cf *CatalogFile) { cf.Instruments[0].StreamID = "" }},
		{"instrumento base==quote", func(cf *CatalogFile) { cf.Instruments[0].Quote = "BTC" }},
		{"instrumento duplicado", func(cf *CatalogFile) { cf.Instruments = append(cf.Instruments, cf.Instruments[0]) }},
		{"paridad mal formada", func(cf *CatalogFile) { cf.ParityPairs = [][2]string{{"USD", "USD"}} }},
	}
	for _, c := range cases {
		cf := CatalogFile{
			Venues:      append([]Venue{}, valid.Venues...),
			Instruments: append([]Instrument{}, valid.Instruments...),
			ParityPairs: append([][2]string{}, valid.ParityPairs...),
		}
		c.mutate(&cf)
		if err := validateCatalog(cf); err == nil {
			t.Errorf("%s: catálogo inválido aceptado", c.name)
		}
	}
}

// TestLoadCatalog_FromFile: un catálogo externo válido sustituye a los registros
// compilados (agregar un libro = editar JSON, sin recompilar); uno inválido los
// conserva intactos.
func TestLoadCatalog_FromFile(t *testing.T) {
	// Los registros son globales del paquete: se restauran al terminar.
	oldVenues, oldInstruments, oldParity := Venues, Instruments, parityPairs
	defer func() { Venues, Instruments, parityPairs = oldVenues, oldInstruments, oldParity }()

	dir := t.TempDir()
	path := filepath.Join(dir, "venues.json")
	catalog := `{
	  "venues": [
	    {"name": "Binance", "default_taker_fee": 0.001, "base_asset": "BTC", "quote_asset": "USDT"},
	    {"name": "Bitso", "default_taker_fee": 0.0065, "base_asset": "BTC", "quote_asset": "USD"},
	    {"name": "Kraken", "default_taker_fee": 0.004, "base_asset": "BTC", "quote_asset": "USD"}
	  ],
	  "instruments": [
	    {"venue": "Binance", "base": "BTC", "quote": "USDT", "stream_id": "btcusdt"},
	    {"venue": "Bitso", "base": "BTC", "quote": "USD", "stream_id": "btc_usd"},
	    {"venue": "Kraken", "base": "BTC", "quote": "USD", "stream_id": "BTC/USD"},
	    {"venue": "Kraken", "base": "SOL", "quote": "USD", "stream_id": "SOL/USD"}
	  ],
	  "parity_pairs": [["USDT", "USD"]]
	}`
	if err := os.WriteFile(path, []byte(catalog), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ARUS_CATALOG", path)

	LoadCatalog()

	if len(Venues) != 3 || len(Instruments) != 4 {
		t.Fatalf("catálogo no cargado: %d venues, %d instrumentos", len(Venues), len(Instruments))
	}
	if _, ok := instrumentByKey("Kraken:SOL/USD"); !ok {
		t.Fatal("el libro agregado por JSON (Kraken:SOL/USD) no está en el registro")
	}
	if !isKnownAsset("SOL") {
		t.Fatal("SOL debería derivarse del catálogo cargado")
	}

	// Un archivo inválido NO debe tocar el registro vigente.
	if err := os.WriteFile(path, []byte(`{"venues": []}`), 0o644); err != nil {
		t.Fatal(err)
	}
	LoadCatalog()
	if len(Venues) != 3 {
		t.Fatalf("un catálogo inválido pisó el registro: %d venues", len(Venues))
	}
}
