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
	ID                int64     `json:"id"`
	SessionID         string    `json:"session_id"`
	Timestamp         time.Time `json:"timestamp"`
	BuyExchange       string    `json:"buy_exchange"`
	SellExchange      string    `json:"sell_exchange"`
	VolumeBTC         float64   `json:"volume_btc"`
	SpreadUSD         float64   `json:"spread_usd"`
	NetProfitUSD      float64   `json:"net_profit_usd"`
	IsCreditInjection bool      `json:"is_credit_injection"`
}

// ledgerDB es la conexión global a SQLite. database/sql ya es seguro para uso
// concurrente (mantiene un pool interno), así que puede compartirse entre goroutines.
var ledgerDB *sql.DB

// ledgerWG permite (si se quisiera) esperar a que terminen las escrituras pendientes.
var ledgerWG sync.WaitGroup

// InitLedger abre/crea el archivo SQLite y asegura el esquema de la tabla.
// Se llama una sola vez al arrancar el motor.
func InitLedger() error {
	// Guardamos la BD en data/ledger.db (se crea la carpeta si no existe).
	dataDir := "data"
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	dbPath := filepath.Join(dataDir, "ledger.db")

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

	schema := `
	CREATE TABLE IF NOT EXISTS trade_records (
		id                  INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id          TEXT    NOT NULL,
		timestamp           DATETIME NOT NULL,
		buy_exchange        TEXT    NOT NULL,
		sell_exchange       TEXT    NOT NULL,
		volume_btc          REAL    NOT NULL,
		spread_usd          REAL    NOT NULL,
		net_profit_usd      REAL    NOT NULL,
		is_credit_injection INTEGER NOT NULL DEFAULT 0
	);
	CREATE INDEX IF NOT EXISTS idx_trade_records_ts ON trade_records(timestamp DESC);
	`
	if _, err := db.Exec(schema); err != nil {
		return err
	}

	ledgerDB = db
	log.Printf("📒 [LEDGER] SQLite inicializado en %s", dbPath)
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

func insertTradeRecord(rec TradeRecord) error {
	_, err := ledgerDB.Exec(
		`INSERT INTO trade_records
			(session_id, timestamp, buy_exchange, sell_exchange, volume_btc, spread_usd, net_profit_usd, is_credit_injection)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.SessionID, rec.Timestamp.UTC(), rec.BuyExchange, rec.SellExchange,
		rec.VolumeBTC, rec.SpreadUSD, rec.NetProfitUSD, rec.IsCreditInjection,
	)
	return err
}

// getRecentTrades devuelve los últimos `limit` registros, del más reciente al más antiguo.
func getRecentTrades(limit int) ([]TradeRecord, error) {
	if ledgerDB == nil {
		return []TradeRecord{}, nil
	}
	if limit <= 0 {
		limit = 100
	}

	rows, err := ledgerDB.Query(
		`SELECT id, session_id, timestamp, buy_exchange, sell_exchange,
		        volume_btc, spread_usd, net_profit_usd, is_credit_injection
		 FROM trade_records
		 ORDER BY id DESC
		 LIMIT ?`, limit,
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
			&r.VolumeBTC, &r.SpreadUSD, &r.NetProfitUSD, &r.IsCreditInjection,
		); err != nil {
			return nil, err
		}
		records = append(records, r)
	}
	return records, rows.Err()
}
