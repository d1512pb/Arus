package main

import (
	"database/sql"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// TradeRecord representa una fila inmutable del Trade Ledger (registro de auditoría).
// Cada operación rentable ejecutada por una sesión in-memory se persiste aquí.
type TradeRecord struct {
	ID           int64     `json:"id"`
	SessionID    string    `json:"session_id"`
	Timestamp    time.Time `json:"timestamp"`
	BuyExchange  string    `json:"buy_exchange"`
	SellExchange string    `json:"sell_exchange"`
	VolumeBTC    float64   `json:"volume_btc"`
	SpreadUSD    float64   `json:"spread_usd"`
	// FeesUSD es la FRICCIÓN total pagada por la operación (comisiones de ambas
	// piernas + slippage estimado), en USD. Alimenta la analítica ("fees totales
	// pagados"); en filas de crédito/reequilibrio vale 0.
	FeesUSD           float64 `json:"fees_usd"`
	NetProfitUSD      float64 `json:"net_profit_usd"`
	IsCreditInjection bool    `json:"is_credit_injection"`
}

// ledgerDB es la conexión global a SQLite. database/sql ya es seguro para uso
// concurrente (mantiene un pool interno), así que puede compartirse entre goroutines.
var ledgerDB *sql.DB

// ledgerWG permite (si se quisiera) esperar a que terminen las escrituras pendientes.
var ledgerWG sync.WaitGroup

// InitLedger abre/crea el archivo SQLite y asegura el esquema de la tabla.
// Se llama una sola vez al arrancar el motor.
func InitLedger() error {
	// Ruta de la BD: data/ledger.db por defecto; ARUS_DB_PATH la sobrescribe
	// (necesario en despliegues con volumen montado en otra ruta, p. ej. Fly.io).
	dbPath := os.Getenv("ARUS_DB_PATH")
	if dbPath == "" {
		dbPath = filepath.Join("data", "ledger.db")
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return err
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return err
	}

	// SQLite con un único escritor: limitamos a 1 conexión para evitar
	// errores "database is locked" bajo escrituras concurrentes (write-behind).
	db.SetMaxOpenConns(1)

	// WAL mejora la concurrencia lectura/escritura; busy_timeout evita locks duros.
	if _, err := db.Exec(`PRAGMA journal_mode=WAL;`); err != nil {
		return err
	}
	if _, err := db.Exec(`PRAGMA busy_timeout=5000;`); err != nil {
		return err
	}

	if err := initLedgerSchema(db); err != nil {
		return err
	}

	ledgerDB = db
	log.Printf("📒 [LEDGER] SQLite inicializado en %s", dbPath)

	// Sprint A: la MISMA base persiste también las sesiones completas (saldos
	// multi-activo, parámetros, PnL) — ver store.go. Si el esquema falla, el
	// trading continúa con sesiones volátiles, igual que sin ledger.
	if err := initSessionStore(db); err != nil {
		log.Printf("⚠️ [STORE] No se pudo inicializar la persistencia de sesiones: %v", err)
	}
	return nil
}

// recordTradeAsync persiste un TradeRecord SIN bloquear el bucle de ejecución.
// Lanza una goroutine write-behind: el siguiente chunk de liquidez se procesa
// inmediatamente mientras la fila se escribe en disco en segundo plano.
func recordTradeAsync(rec TradeRecord) {
	if ledgerDB == nil {
		return
	}
	if rec.Timestamp.IsZero() {
		rec.Timestamp = time.Now()
	}

	ledgerWG.Add(1)
	go func() {
		defer ledgerWG.Done()
		if err := insertTradeRecord(rec); err != nil {
			// El ledger es auxiliar: un fallo de escritura no debe afectar al trading.
			log.Printf("⚠️ [LEDGER] Error al persistir trade (sesión %s): %v", rec.SessionID, err)
		}
	}()
}

// initLedgerSchema crea el esquema del ledger y aplica las migraciones aditivas.
// Separado de InitLedger (que fija rutas y la conexión global) para poder
// verificarlo con bases temporales en los tests.
func initLedgerSchema(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS trade_records (
		id                  INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id          TEXT    NOT NULL,
		timestamp           DATETIME NOT NULL,
		buy_exchange        TEXT    NOT NULL,
		sell_exchange       TEXT    NOT NULL,
		volume_btc          REAL    NOT NULL,
		spread_usd          REAL    NOT NULL,
		fees_usd            REAL    NOT NULL DEFAULT 0,
		net_profit_usd      REAL    NOT NULL,
		is_credit_injection INTEGER NOT NULL DEFAULT 0
	);
	CREATE INDEX IF NOT EXISTS idx_trade_records_ts ON trade_records(timestamp DESC);
	`
	if _, err := db.Exec(schema); err != nil {
		return err
	}

	// Migración in situ (Sprint D): bases creadas antes de la analítica no tienen
	// fees_usd; se agrega con DEFAULT 0 (las filas históricas simplemente no
	// conocen su fricción). Idempotente: si la columna ya existe, no hace nada.
	return ensureLedgerColumn(db, "trade_records", "fees_usd", "REAL NOT NULL DEFAULT 0")
}

// ensureLedgerColumn agrega una columna si no existe (migración aditiva simple:
// el esquema se auto-crea/migra al arrancar, sin pasos manuales — igual en Fly).
func ensureLedgerColumn(db *sql.DB, table, column, decl string) error {
	var count int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, table, column,
	).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	_, err := db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + column + ` ` + decl)
	if err == nil {
		log.Printf("📒 [LEDGER] Migración: columna %s.%s agregada", table, column)
	}
	return err
}

func insertTradeRecord(rec TradeRecord) error {
	_, err := ledgerDB.Exec(
		`INSERT INTO trade_records
			(session_id, timestamp, buy_exchange, sell_exchange, volume_btc, spread_usd, fees_usd, net_profit_usd, is_credit_injection)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.SessionID, rec.Timestamp.UTC(), rec.BuyExchange, rec.SellExchange,
		rec.VolumeBTC, rec.SpreadUSD, rec.FeesUSD, rec.NetProfitUSD, rec.IsCreditInjection,
	)
	return err
}

// getTradesForSession devuelve los últimos `limit` registros de UNA sesión, del más
// reciente al más antiguo. El filtro WHERE session_id = ? usa un placeholder
// parametrizado de database/sql (no concatenación de strings): inmune a inyección SQL.
//
// Sustituye al antiguo getRecentTrades (que devolvía TODAS las sesiones): así no queda
// ningún camino de código capaz de filtrar trades entre sesiones (Hallazgo #1).
func getTradesForSession(sessionID string, limit int) ([]TradeRecord, error) {
	if ledgerDB == nil {
		return []TradeRecord{}, nil
	}
	if limit <= 0 {
		limit = 100
	}

	rows, err := ledgerDB.Query(
		`SELECT id, session_id, timestamp, buy_exchange, sell_exchange,
		        volume_btc, spread_usd, fees_usd, net_profit_usd, is_credit_injection
		 FROM trade_records
		 WHERE session_id = ?
		 ORDER BY id DESC
		 LIMIT ?`, sessionID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records := make([]TradeRecord, 0, limit)
	for rows.Next() {
		var r TradeRecord
		if err := rows.Scan(
			&r.ID, &r.SessionID, &r.Timestamp, &r.BuyExchange, &r.SellExchange,
			&r.VolumeBTC, &r.SpreadUSD, &r.FeesUSD, &r.NetProfitUSD, &r.IsCreditInjection,
		); err != nil {
			return nil, err
		}
		records = append(records, r)
	}
	return records, rows.Err()
}

// getAllTradesForSession devuelve TODO el historial de una sesión en orden
// cronológico (id ASC): es la base de la analítica y del export CSV. El mismo
// placeholder parametrizado que el resto del ledger (inmune a inyección).
func getAllTradesForSession(sessionID string) ([]TradeRecord, error) {
	if ledgerDB == nil {
		return []TradeRecord{}, nil
	}
	rows, err := ledgerDB.Query(
		`SELECT id, session_id, timestamp, buy_exchange, sell_exchange,
		        volume_btc, spread_usd, fees_usd, net_profit_usd, is_credit_injection
		 FROM trade_records
		 WHERE session_id = ?
		 ORDER BY id ASC`, sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []TradeRecord
	for rows.Next() {
		var r TradeRecord
		if err := rows.Scan(
			&r.ID, &r.SessionID, &r.Timestamp, &r.BuyExchange, &r.SellExchange,
			&r.VolumeBTC, &r.SpreadUSD, &r.FeesUSD, &r.NetProfitUSD, &r.IsCreditInjection,
		); err != nil {
			return nil, err
		}
		records = append(records, r)
	}
	return records, rows.Err()
}
