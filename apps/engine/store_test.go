package main

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// newTestStore abre un SQLite temporal con el esquema v2 (aislado por test).
func newTestStore(t *testing.T) *sqliteSessionStore {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("no se pudo abrir SQLite de prueba: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)

	prev := sessionStore
	t.Cleanup(func() { sessionStore = prev })
	if err := initSessionStore(db); err != nil {
		t.Fatalf("no se pudo crear el esquema: %v", err)
	}
	return sessionStore.(*sqliteSessionStore)
}

func sampleRecord(id string) SessionRecord {
	params := DefaultTradingParameters()
	params.MinNetProfitUSD = 7.5
	params.RiskMultiplier = 4
	return SessionRecord{
		ID:             id,
		CreatedAt:      time.Now(),
		LastSeen:       time.Now(),
		Params:         params,
		InitialUSD:     10_000,
		InitialBTC:     0.5,
		InitialWealth:  41_500,
		TotalWealth:    41_780.25,
		TotalNetProfit: 280.25,
		AutoCredit:     true,
		Initialized:    true,
		Balances: map[string]map[string]float64{
			"Binance": {"USDT": 4_900.10, "BTC": 0.26},
			"Bitso":   {"USD": 5_250.40, "BTC": 0.24},
		},
	}
}

// TestStore_SaveLoadRoundtrip: la fotografía completa sobrevive el viaje a disco —
// saldos multi-activo, parámetros de estrategia, base del PnL y preferencias.
func TestStore_SaveLoadRoundtrip(t *testing.T) {
	st := newTestStore(t)
	want := sampleRecord("sesion-roundtrip")

	if err := st.SaveSession(want); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	got, found, err := st.LoadSession(want.ID)
	if err != nil || !found {
		t.Fatalf("LoadSession: found=%v err=%v", found, err)
	}

	if got.TotalWealth != want.TotalWealth || got.TotalNetProfit != want.TotalNetProfit ||
		got.InitialWealth != want.InitialWealth || got.InitialUSD != want.InitialUSD ||
		got.InitialBTC != want.InitialBTC || !got.AutoCredit || !got.Initialized {
		t.Fatalf("escalares alterados: %+v", got)
	}
	if got.Params.MinNetProfitUSD != 7.5 || got.Params.RiskMultiplier != 4 ||
		got.Params.TakerFees["Bitso"] != want.Params.TakerFees["Bitso"] {
		t.Fatalf("parámetros alterados: %+v", got.Params)
	}
	if got.Balances["Binance"]["USDT"] != 4_900.10 || got.Balances["Binance"]["BTC"] != 0.26 ||
		got.Balances["Bitso"]["USD"] != 5_250.40 || got.Balances["Bitso"]["BTC"] != 0.24 {
		t.Fatalf("saldos alterados: %+v", got.Balances)
	}
}

// TestStore_UpsertReplacesSnapshot: guardar dos veces actualiza (no duplica) y
// el reemplazo de saldos es completo — sin filas huérfanas de activos viejos.
func TestStore_UpsertReplacesSnapshot(t *testing.T) {
	st := newTestStore(t)
	rec := sampleRecord("sesion-upsert")
	if err := st.SaveSession(rec); err != nil {
		t.Fatalf("primer save: %v", err)
	}

	rec.TotalWealth = 50_000
	rec.Balances = map[string]map[string]float64{
		"Binance": {"USDT": 100, "BTC": 1},
		// Bitso desaparece de la fotografía: sus filas viejas deben borrarse.
	}
	if err := st.SaveSession(rec); err != nil {
		t.Fatalf("segundo save: %v", err)
	}

	got, _, err := st.LoadSession(rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TotalWealth != 50_000 || got.Balances["Binance"]["USDT"] != 100 {
		t.Fatalf("upsert no aplicado: %+v", got)
	}
	if _, ok := got.Balances["Bitso"]; ok {
		t.Fatalf("filas huérfanas de un venue eliminado: %+v", got.Balances)
	}
}

// TestStore_LoadMissing: una sesión inexistente devuelve found=false sin error.
func TestStore_LoadMissing(t *testing.T) {
	st := newTestStore(t)
	_, found, err := st.LoadSession("no-existe")
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if found {
		t.Fatal("sesión fantasma encontrada")
	}
}

// TestSnapshotSessionRecord_ExcludesBorrowed: un préstamo activo NO se persiste
// como capital del usuario — al reanudar tras un reinicio no hay crédito fantasma.
func TestSnapshotSessionRecord_ExcludesBorrowed(t *testing.T) {
	s := newClientSession("con-credito", nil)
	initSession(s, 10_000, 0.5)

	s.Mu.Lock()
	s.Credit.Active = true
	s.Credit.BorrowedUSD = map[string]float64{"Binance": 25_000, "Bitso": 25_000}
	s.Credit.BorrowedBTC = map[string]float64{"Binance": 0.5, "Bitso": 0.5}
	s.Wallets["Binance"].USD += 25_000
	s.Wallets["Bitso"].USD += 25_000
	s.Wallets["Binance"].BTC += 0.5
	s.Wallets["Bitso"].BTC += 0.5
	s.Mu.Unlock()

	rec, ok := snapshotSessionRecord(s)
	if !ok {
		t.Fatal("snapshot rechazado en sesión inicializada")
	}
	// Fondos propios: 5 000 USD y 0.25 BTC por venue (lo prestado, excluido).
	if !almostEqual(rec.Balances["Binance"]["USDT"], 5_000) || !almostEqual(rec.Balances["Binance"]["BTC"], 0.25) {
		t.Fatalf("préstamo persistido como capital propio: %+v", rec.Balances)
	}
}

// TestSnapshotSessionRecord_Uninitialized: sin wallets no hay nada que persistir.
func TestSnapshotSessionRecord_Uninitialized(t *testing.T) {
	s := newClientSession("sin-init", nil)
	if _, ok := snapshotSessionRecord(s); ok {
		t.Fatal("snapshot de sesión sin inicializar")
	}
}

// TestApplySessionRecord_RestoresAndSanitizes: la reanudación restaura saldos y
// PnL, y los parámetros pasan por los clamps (una fila manipulada no inyecta
// valores fuera de rango).
func TestApplySessionRecord_RestoresAndSanitizes(t *testing.T) {
	rec := sampleRecord("restaurada")
	rec.Params.RiskMultiplier = 0.01 // manipulado a mano: endeudarse a pérdida

	s := newClientSession("efimera", nil)
	applySessionRecord(s, rec)

	s.Mu.Lock()
	binUSD := s.Wallets["Binance"].USD
	bitBTC := s.Wallets["Bitso"].BTC
	wealth := s.TotalWealth
	auto := s.Credit.AutoMode
	creditActive := s.Credit.Active
	s.Mu.Unlock()

	if !almostEqual(binUSD, 4_900.10) || !almostEqual(bitBTC, 0.24) {
		t.Fatalf("saldos no restaurados: binUSD=%v bitBTC=%v", binUSD, bitBTC)
	}
	if wealth != rec.TotalWealth || !auto || creditActive {
		t.Fatalf("estado no restaurado: wealth=%v auto=%v credit=%v", wealth, auto, creditActive)
	}
	if got := s.Params().RiskMultiplier; got != MinRiskMultiplier {
		t.Fatalf("parámetro manipulado no clampeado al reanudar: %v", got)
	}
}

// TestStore_RoundtripViaSessionHelpers: el ciclo completo snapshot → save →
// load → apply devuelve al usuario exactamente a donde estaba.
func TestStore_RoundtripViaSessionHelpers(t *testing.T) {
	st := newTestStore(t)

	orig := newClientSession("viaje-completo", nil)
	initSession(orig, 20_000, 1.0)
	orig.Mu.Lock()
	orig.Wallets["Binance"].USD = 8_123.45
	orig.TotalNetProfit = 77.7
	orig.TotalWealth += 77.7
	orig.Mu.Unlock()
	custom := DefaultTradingParameters()
	custom.MinNetProfitUSD = 12
	orig.SetParams(custom)

	rec, ok := snapshotSessionRecord(orig)
	if !ok {
		t.Fatal("snapshot rechazado")
	}
	if err := st.SaveSession(rec); err != nil {
		t.Fatal(err)
	}

	loaded, found, err := st.LoadSession("viaje-completo")
	if err != nil || !found {
		t.Fatalf("load: found=%v err=%v", found, err)
	}
	restored := newClientSession("otra-conexion", nil)
	applySessionRecord(restored, loaded)

	restored.Mu.Lock()
	defer restored.Mu.Unlock()
	if !almostEqual(restored.Wallets["Binance"].USD, 8_123.45) ||
		!almostEqual(restored.TotalNetProfit, 77.7) {
		t.Fatalf("el viaje completo perdió estado: %+v (pnl %v)", restored.Wallets["Binance"], restored.TotalNetProfit)
	}
	if restored.Params().MinNetProfitUSD != 12 {
		t.Fatalf("estrategia perdida en el viaje: %+v", restored.Params())
	}
}
