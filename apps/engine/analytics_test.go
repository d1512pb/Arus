package main

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// TestComputeSessionStats: agregados correctos sobre un historial mixto
// (operaciones + un evento de crédito que cuenta en el PnL pero no en el win rate).
func TestComputeSessionStats(t *testing.T) {
	base := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	recs := []TradeRecord{
		{Timestamp: base, NetProfitUSD: -25, IsCreditInjection: true}, // costo del préstamo
		{Timestamp: base.Add(10 * time.Minute), NetProfitUSD: 10, FeesUSD: 2, VolumeBTC: 0.01},
		{Timestamp: base.Add(20 * time.Minute), NetProfitUSD: -1, FeesUSD: 1, VolumeBTC: 0.02},
		{Timestamp: base.Add(30 * time.Minute), NetProfitUSD: 6, FeesUSD: 1.5, VolumeBTC: 0.03},
	}

	st := computeSessionStats(recs)

	if st.TotalOps != 3 || st.Wins != 2 || st.Losses != 1 {
		t.Fatalf("conteo: ops=%d wins=%d losses=%d, esperado 3/2/1", st.TotalOps, st.Wins, st.Losses)
	}
	if !almostEqual(st.WinRatePct, 200.0/3.0) {
		t.Fatalf("win rate=%v, esperado %v", st.WinRatePct, 200.0/3.0)
	}
	// PnL acumulado INCLUYE el costo del crédito: -25+10-1+6 = -10 (coincide con
	// la Ganancia Neta del dashboard, que también lo descuenta).
	if !almostEqual(st.NetProfitUSD, -10) {
		t.Fatalf("PnL=%v, esperado -10", st.NetProfitUSD)
	}
	if !almostEqual(st.FeesUSD, 4.5) || !almostEqual(st.VolumeBTC, 0.06) {
		t.Fatalf("fees=%v vol=%v, esperado 4.5 / 0.06", st.FeesUSD, st.VolumeBTC)
	}
	// 3 operaciones en 20 min de ventana (primera→última op) = 9/hora.
	if !almostEqual(st.OpsPerHour, 9) {
		t.Fatalf("ops/hora=%v, esperado 9", st.OpsPerHour)
	}
	if st.CreditEvents != 1 {
		t.Fatalf("eventos de crédito=%d, esperado 1", st.CreditEvents)
	}
	if len(st.Series) != 4 || !almostEqual(st.Series[len(st.Series)-1].Cum, -10) {
		t.Fatalf("serie mal construida: %+v", st.Series)
	}
	// La serie es acumulativa: el segundo punto ya absorbe el costo del crédito.
	if !almostEqual(st.Series[1].Cum, -15) {
		t.Fatalf("punto 2 de la serie=%v, esperado -15", st.Series[1].Cum)
	}
}

// TestComputeSessionStats_Empty: sin historial no hay división por cero ni nils.
func TestComputeSessionStats_Empty(t *testing.T) {
	st := computeSessionStats(nil)
	if st.TotalOps != 0 || st.WinRatePct != 0 || st.OpsPerHour != 0 || st.NetProfitUSD != 0 {
		t.Fatalf("stats vacíos con valores fantasma: %+v", st)
	}
	if st.Series == nil || len(st.Series) != 0 {
		t.Fatalf("la serie vacía debe ser [] (no nil): %+v", st.Series)
	}
}

// TestComputeSessionStats_SingleOp: una sola operación usa el piso de 1 minuto
// para el ritmo (no infinito, no división por cero).
func TestComputeSessionStats_SingleOp(t *testing.T) {
	st := computeSessionStats([]TradeRecord{
		{Timestamp: time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC), NetProfitUSD: 5, VolumeBTC: 0.01},
	})
	if st.TotalOps != 1 || !almostEqual(st.OpsPerHour, 60) {
		t.Fatalf("una op con piso de 1 min debe dar 60/h: ops=%d ritmo=%v", st.TotalOps, st.OpsPerHour)
	}
}

// TestDownsampleSeries: el muestreo conserva longitud objetivo y extremos.
func TestDownsampleSeries(t *testing.T) {
	pts := make([]StatsPoint, 1000)
	for i := range pts {
		pts[i] = StatsPoint{Cum: float64(i)}
	}
	out := downsampleSeries(pts, maxSeriesPoints)
	if len(out) != maxSeriesPoints {
		t.Fatalf("len=%d, esperado %d", len(out), maxSeriesPoints)
	}
	if out[0].Cum != 0 || out[len(out)-1].Cum != 999 {
		t.Fatalf("extremos perdidos: primero=%v último=%v", out[0].Cum, out[len(out)-1].Cum)
	}
	// Series cortas pasan intactas.
	short := pts[:50]
	if got := downsampleSeries(short, maxSeriesPoints); len(got) != 50 {
		t.Fatalf("serie corta alterada: len=%d", len(got))
	}
}

// newTestLedgerDB abre un SQLite temporal con el esquema del ledger y lo instala
// como ledgerDB global (restaurado al terminar el test).
func newTestLedgerDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "ledger-test.db"))
	if err != nil {
		t.Fatalf("no se pudo abrir SQLite de prueba: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if err := initLedgerSchema(db); err != nil {
		t.Fatalf("no se pudo crear el esquema del ledger: %v", err)
	}
	prev := ledgerDB
	ledgerDB = db
	t.Cleanup(func() { ledgerDB = prev })
	return db
}

// TestLedger_FeesRoundtrip: fees_usd sobrevive el viaje insert → select en ambas
// consultas (recientes y completas), y el esquema es idempotente.
func TestLedger_FeesRoundtrip(t *testing.T) {
	db := newTestLedgerDB(t)

	rec := TradeRecord{
		SessionID:    "sesion-fees",
		Timestamp:    time.Now(),
		BuyExchange:  "Binance",
		SellExchange: "Bitso",
		VolumeBTC:    0.005,
		SpreadUSD:    450,
		FeesUSD:      2.37,
		NetProfitUSD: 0.42,
	}
	if err := insertTradeRecord(rec); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := getTradesForSession("sesion-fees", 10)
	if err != nil || len(got) != 1 {
		t.Fatalf("select recientes: n=%d err=%v", len(got), err)
	}
	if !almostEqual(got[0].FeesUSD, 2.37) {
		t.Fatalf("fees perdidos en recientes: %v", got[0].FeesUSD)
	}
	all, err := getAllTradesForSession("sesion-fees")
	if err != nil || len(all) != 1 || !almostEqual(all[0].FeesUSD, 2.37) {
		t.Fatalf("fees perdidos en historial completo: %+v err=%v", all, err)
	}

	// Re-aplicar el esquema sobre una base ya migrada no debe fallar.
	if err := initLedgerSchema(db); err != nil {
		t.Fatalf("el esquema no es idempotente: %v", err)
	}
}

// TestLedger_MigratesOldSchema: una base creada ANTES de la analítica (sin
// fees_usd) se migra sola al arrancar y las filas viejas leen fricción 0.
func TestLedger_MigratesOldSchema(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "ledger-old.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	// Esquema HISTÓRICO (pre-Sprint D), con una fila existente.
	oldSchema := `
	CREATE TABLE trade_records (
		id                  INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id          TEXT    NOT NULL,
		timestamp           DATETIME NOT NULL,
		buy_exchange        TEXT    NOT NULL,
		sell_exchange       TEXT    NOT NULL,
		volume_btc          REAL    NOT NULL,
		spread_usd          REAL    NOT NULL,
		net_profit_usd      REAL    NOT NULL,
		is_credit_injection INTEGER NOT NULL DEFAULT 0
	);`
	if _, err := db.Exec(oldSchema); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO trade_records (session_id, timestamp, buy_exchange, sell_exchange, volume_btc, spread_usd, net_profit_usd, is_credit_injection)
		 VALUES ('vieja', ?, 'Binance', 'Bitso', 0.01, 100, 1.5, 0)`, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	if err := initLedgerSchema(db); err != nil {
		t.Fatalf("migración falló: %v", err)
	}

	prev := ledgerDB
	ledgerDB = db
	t.Cleanup(func() { ledgerDB = prev })

	got, err := getAllTradesForSession("vieja")
	if err != nil || len(got) != 1 {
		t.Fatalf("fila histórica ilegible tras migrar: n=%d err=%v", len(got), err)
	}
	if got[0].FeesUSD != 0 || !almostEqual(got[0].NetProfitUSD, 1.5) {
		t.Fatalf("fila histórica alterada: %+v", got[0])
	}
}
