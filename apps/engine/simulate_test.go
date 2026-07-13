package main

import (
	"net/http"
	"strings"
	"testing"
)

// Tests de validateSimLeg (FASE 1 del refactor de "Probar Bot"): la validación
// que protege POST /api/simulate/custom antes de inyectar ticks en la tubería
// real. Función pura sobre TradingParameters — sin red, sin sesiones.

func TestValidateSimLegHappyPath(t *testing.T) {
	p := DefaultTradingParameters()
	instr, code, msg := validateSimLeg(p, "Mercado A", "Binance", "BTC", 60_000, 60_050)
	if msg != "" || code != 0 {
		t.Fatalf("pierna válida rechazada: code=%d msg=%q", code, msg)
	}
	if instr.Key() != "Binance:BTC/USDT" {
		t.Fatalf("instrumento resuelto incorrecto: %s", instr.Key())
	}
}

func TestValidateSimLegResolvesCashBook(t *testing.T) {
	p := DefaultTradingParameters()
	// ETH en Kraken debe resolver contra su libro de efectivo (ETH/USD), no ETH/BTC.
	instr, code, msg := validateSimLeg(p, "Mercado B", "Kraken", "ETH", 3_000, 3_002)
	if msg != "" || code != 0 {
		t.Fatalf("pierna válida rechazada: code=%d msg=%q", code, msg)
	}
	if instr.Key() != "Kraken:ETH/USD" {
		t.Fatalf("instrumento resuelto incorrecto: %s", instr.Key())
	}
}

func TestValidateSimLegUnknownVenue(t *testing.T) {
	p := DefaultTradingParameters()
	_, code, msg := validateSimLeg(p, "Mercado A", "MtGox", "BTC", 60_000, 60_050)
	if code != http.StatusBadRequest || msg == "" {
		t.Fatalf("venue inexistente debía rechazarse con 400: code=%d msg=%q", code, msg)
	}
}

func TestValidateSimLegVenueOutsideUniverse(t *testing.T) {
	p := DefaultTradingParameters()
	p.EnabledVenues = []string{"Binance", "Bitso"} // Kraken fuera del universo
	_, code, msg := validateSimLeg(p, "Mercado A", "Kraken", "BTC", 60_000, 60_050)
	if code != http.StatusForbidden {
		t.Fatalf("venue fuera del universo debía rechazarse con 403: code=%d msg=%q", code, msg)
	}
	if !strings.Contains(msg, "Kraken") || !strings.Contains(msg, "no está activo") {
		t.Fatalf("el error debe nombrar el venue inactivo y cómo corregirlo: %q", msg)
	}
}

func TestValidateSimLegAssetOutsideUniverse(t *testing.T) {
	p := DefaultTradingParameters()
	p.EnabledAssets = []string{"USD", "USDT", "BTC"} // sin ETH
	_, code, msg := validateSimLeg(p, "Mercado B", "Binance", "ETH", 3_000, 3_002)
	if code != http.StatusForbidden {
		t.Fatalf("activo fuera del universo debía rechazarse con 403: code=%d msg=%q", code, msg)
	}
	if !strings.Contains(msg, "ETH") {
		t.Fatalf("el error debe nombrar la moneda inactiva: %q", msg)
	}
}

func TestValidateSimLegIncoherentBook(t *testing.T) {
	p := DefaultTradingParameters()
	// Libro cruzado (ask < bid): mismo escudo que la ingesta real.
	if _, code, _ := validateSimLeg(p, "Mercado A", "Binance", "BTC", 61_000, 60_000); code != http.StatusBadRequest {
		t.Fatalf("libro cruzado debía rechazarse con 400: code=%d", code)
	}
	// Spread interno > 5 %: valor basura que el Spike Filter "fijaría".
	if _, code, _ := validateSimLeg(p, "Mercado A", "Binance", "BTC", 50_000, 60_000); code != http.StatusBadRequest {
		t.Fatalf("spread interno >5%% debía rechazarse con 400: code=%d", code)
	}
	// Precios no positivos / no finitos.
	if _, code, _ := validateSimLeg(p, "Mercado A", "Binance", "BTC", -1, 60_000); code != http.StatusBadRequest {
		t.Fatalf("precio negativo debía rechazarse con 400: code=%d", code)
	}
}

func TestValidateSimLegNoCashBook(t *testing.T) {
	p := DefaultTradingParameters()
	// SOL existe en el catálogo (Binance) pero Kraken no publica SOL/efectivo.
	_, code, msg := validateSimLeg(p, "Mercado A", "Kraken", "SOL", 150, 150.2)
	if code != http.StatusBadRequest || msg == "" {
		t.Fatalf("libro inexistente debía rechazarse con 400: code=%d msg=%q", code, msg)
	}
}
