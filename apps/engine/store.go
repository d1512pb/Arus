package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"time"
)

// store.go — PERSISTENCIA DE SESIÓN (Sprint A · continuidad).
//
// Evoluciona el SQLite del ledger a una base de datos de SESIONES COMPLETAS:
// saldos, parámetros de estrategia, base del PnL y preferencias sobreviven a
// reinicios del motor y del navegador. El demo deja de ser volátil.
//
// Diseño:
//   - SessionStore es una INTERFAZ (patrón repositorio): el motor no sabe que
//     detrás hay SQLite. Migrar a Postgres = escribir otro driver, cero cambios
//     en la lógica. Los disparadores para migrar están en docs/FASE2-GRAFO.md.
//   - El esquema de saldos nace MULTI-ACTIVO (venue, asset, amount): es el
//     prerequisito de las wallets multi-activo del hito 3 — cuando el tipo en
//     memoria se generalice, los datos ya estarán en la forma correcta y no
//     habrá migración. Hoy el motor persiste los dos activos respaldados por
//     wallet de cada venue (quote y base del registro).
//   - Escritura write-behind (mismo patrón que el ledger): persistir nunca
//     frena el hot path. La identidad de sesión (UUID v4 no enumerable) actúa
//     como token portador — quien lo tiene, reanuda esa sesión.

// SessionRecord es la fotografía persistible de una sesión: todo lo necesario
// para que el usuario "vuelva y encuentre su cuenta".
type SessionRecord struct {
	ID        string
	CreatedAt time.Time
	LastSeen  time.Time

	Params TradingParameters

	InitialUSD     float64
	InitialBTC     float64
	InitialWealth  float64
	TotalWealth    float64
	TotalNetProfit float64
	AutoCredit     bool
	Initialized    bool

	// Balances es multi-activo por diseño: venue → asset → cantidad.
	// IMPORTANTE: son los fondos PROPIOS del usuario (excluyendo préstamos
	// activos): un crédito vivo no debe sobrevivir a un reinicio como si fuera
	// capital del usuario.
	Balances map[string]map[string]float64
}

// SessionStore es el contrato de persistencia de sesiones.
type SessionStore interface {
	// SaveSession inserta o actualiza la fotografía completa (upsert atómico).
	SaveSession(rec SessionRecord) error
	// LoadSession devuelve la sesión y ok=true si existe.
	LoadSession(id string) (SessionRecord, bool, error)
	// TouchSession actualiza last_seen (reconexiones, desconexiones).
	TouchSession(id string, at time.Time) error
}

// sessionStore es la instancia global (nil ⇒ persistencia deshabilitada: el
// motor sigue operando en memoria, igual que cuando falla el ledger).
var sessionStore SessionStore

// sqliteSessionStore implementa SessionStore sobre la MISMA base del ledger
// (un archivo, un volumen en Fly.io, un backup).
type sqliteSessionStore struct {
	db *sql.DB
}

// initSessionStore crea el esquema v2 y activa la persistencia de sesiones.
func initSessionStore(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS sessions (
		id               TEXT PRIMARY KEY,
		created_at       DATETIME NOT NULL,
		last_seen        DATETIME NOT NULL,
		params_json      TEXT NOT NULL,
		initial_usd      REAL NOT NULL,
		initial_btc      REAL NOT NULL,
		initial_wealth   REAL NOT NULL,
		total_wealth     REAL NOT NULL,
		total_net_profit REAL NOT NULL,
		auto_credit      INTEGER NOT NULL DEFAULT 0,
		initialized      INTEGER NOT NULL DEFAULT 0
	);
	CREATE TABLE IF NOT EXISTS balances (
		session_id TEXT NOT NULL,
		venue      TEXT NOT NULL,
		asset      TEXT NOT NULL,
		amount     REAL NOT NULL,
		PRIMARY KEY (session_id, venue, asset)
	);
	CREATE INDEX IF NOT EXISTS idx_sessions_last_seen ON sessions(last_seen DESC);
	`
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	sessionStore = &sqliteSessionStore{db: db}
	log.Printf("💾 [STORE] Persistencia de sesiones activa (SQLite, esquema multi-activo)")
	return nil
}

func (s *sqliteSessionStore) SaveSession(rec SessionRecord) error {
	paramsJSON, err := json.Marshal(rec.Params)
	if err != nil {
		return err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck — no-op tras Commit

	// Upsert: created_at se conserva en actualizaciones.
	if _, err := tx.Exec(`
		INSERT INTO sessions
			(id, created_at, last_seen, params_json, initial_usd, initial_btc,
			 initial_wealth, total_wealth, total_net_profit, auto_credit, initialized)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			last_seen        = excluded.last_seen,
			params_json      = excluded.params_json,
			initial_usd      = excluded.initial_usd,
			initial_btc      = excluded.initial_btc,
			initial_wealth   = excluded.initial_wealth,
			total_wealth     = excluded.total_wealth,
			total_net_profit = excluded.total_net_profit,
			auto_credit      = excluded.auto_credit,
			initialized      = excluded.initialized`,
		rec.ID, rec.CreatedAt.UTC(), rec.LastSeen.UTC(), string(paramsJSON),
		rec.InitialUSD, rec.InitialBTC, rec.InitialWealth,
		rec.TotalWealth, rec.TotalNetProfit, rec.AutoCredit, rec.Initialized,
	); err != nil {
		return err
	}

	// Saldos: reemplazo completo de la fotografía (multi-activo).
	if _, err := tx.Exec(`DELETE FROM balances WHERE session_id = ?`, rec.ID); err != nil {
		return err
	}
	for venue, assets := range rec.Balances {
		for asset, amount := range assets {
			if _, err := tx.Exec(
				`INSERT INTO balances (session_id, venue, asset, amount) VALUES (?, ?, ?, ?)`,
				rec.ID, venue, asset, amount,
			); err != nil {
				return err
			}
		}
	}

	return tx.Commit()
}

func (s *sqliteSessionStore) LoadSession(id string) (SessionRecord, bool, error) {
	var rec SessionRecord
	var paramsJSON string

	err := s.db.QueryRow(`
		SELECT id, created_at, last_seen, params_json, initial_usd, initial_btc,
		       initial_wealth, total_wealth, total_net_profit, auto_credit, initialized
		FROM sessions WHERE id = ?`, id,
	).Scan(&rec.ID, &rec.CreatedAt, &rec.LastSeen, &paramsJSON,
		&rec.InitialUSD, &rec.InitialBTC, &rec.InitialWealth,
		&rec.TotalWealth, &rec.TotalNetProfit, &rec.AutoCredit, &rec.Initialized)
	if err == sql.ErrNoRows {
		return SessionRecord{}, false, nil
	}
	if err != nil {
		return SessionRecord{}, false, err
	}

	if err := json.Unmarshal([]byte(paramsJSON), &rec.Params); err != nil {
		// Params corruptos no invalidan la sesión: se reanuda con defaults y el
		// usuario los reconfigura desde el panel.
		log.Printf("⚠️ [STORE] params_json corrupto en sesión %s — usando defaults: %v", id, err)
		rec.Params = DefaultTradingParameters()
	}

	rec.Balances = make(map[string]map[string]float64)
	rows, err := s.db.Query(`SELECT venue, asset, amount FROM balances WHERE session_id = ?`, id)
	if err != nil {
		return SessionRecord{}, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var venue, asset string
		var amount float64
		if err := rows.Scan(&venue, &asset, &amount); err != nil {
			return SessionRecord{}, false, err
		}
		if rec.Balances[venue] == nil {
			rec.Balances[venue] = make(map[string]float64)
		}
		rec.Balances[venue][asset] = amount
	}
	return rec, true, rows.Err()
}

func (s *sqliteSessionStore) TouchSession(id string, at time.Time) error {
	_, err := s.db.Exec(`UPDATE sessions SET last_seen = ? WHERE id = ?`, at.UTC(), id)
	return err
}

// ---------------------------------------------------------------------------
// Puente motor ↔ store (fotografía + write-behind)
// ---------------------------------------------------------------------------

// snapshotSessionRecord toma la fotografía persistible de una sesión EN VIVO.
// Excluye los fondos prestados (un crédito activo muere con el proceso: al
// reanudar, el usuario recupera SUS fondos, sin préstamo fantasma) y expresa
// los saldos en el esquema multi-activo usando el registro de venues.
func snapshotSessionRecord(s *ClientSession) (SessionRecord, bool) {
	params := s.Params()

	s.Mu.Lock()
	defer s.Mu.Unlock()

	if s.Wallets == nil {
		return SessionRecord{}, false // sesión sin inicializar: nada que persistir
	}

	now := time.Now()
	rec := SessionRecord{
		ID:             s.ID,
		CreatedAt:      now, // solo se usa en el INSERT; el upsert conserva el original
		LastSeen:       now,
		Params:         params,
		InitialUSD:     s.InitialUSD,
		InitialBTC:     s.InitialBTC,
		InitialWealth:  s.InitialWealth,
		TotalWealth:    s.TotalWealth,
		TotalNetProfit: s.TotalNetProfit,
		AutoCredit:     s.Credit.AutoMode,
		Initialized:    true,
		Balances:       make(map[string]map[string]float64, len(Venues)),
	}

	// Fotografía multi-activo COMPLETA (hito 3): todos los activos de todos los
	// venues; el préstamo activo (que vive solo en quote/base) se descuenta.
	for venue, assets := range s.Wallets {
		inner := make(map[string]float64, len(assets))
		for asset, amt := range assets {
			own := amt
			if s.Credit.Active {
				if asset == quoteOf(venue) && s.Credit.BorrowedUSD != nil {
					own -= s.Credit.BorrowedUSD[venue]
				}
				if asset == baseOf(venue) && s.Credit.BorrowedBTC != nil {
					own -= s.Credit.BorrowedBTC[venue]
				}
				if own < 0 {
					own = 0 // el bot consumió parte del préstamo: lo propio nunca es negativo
				}
			}
			inner[asset] = own
		}
		rec.Balances[venue] = inner
	}

	return rec, true
}

// persistSessionAsync guarda la fotografía de la sesión sin bloquear el hot
// path (write-behind, mismo patrón que el ledger). Un fallo de persistencia
// jamás detiene el trading: se registra y se seguirá intentando en la próxima
// mutación.
func persistSessionAsync(s *ClientSession) {
	if sessionStore == nil || s == nil {
		return
	}
	rec, ok := snapshotSessionRecord(s)
	if !ok {
		return
	}
	go func() {
		if err := sessionStore.SaveSession(rec); err != nil {
			log.Printf("⚠️ [STORE] Error al persistir sesión %s: %v", rec.ID, err)
		}
	}()
}

// applySessionRecord restaura una fotografía sobre una sesión recién conectada
// (reanudación). Los parámetros pasan por los clamps de backend — una fila
// manipulada a mano no puede inyectar parámetros fuera de rango.
func applySessionRecord(s *ClientSession, rec SessionRecord) {
	s.SetParams(sanitizeTradingParams(rec.Params))

	s.Mu.Lock()
	defer s.Mu.Unlock()

	// Restauración multi-activo COMPLETA (hito 3): cada activo persistido vuelve
	// a su venue — incluidos los que no son quote/base (ETH de un triangular).
	s.Wallets = make(Balances, len(Venues))
	for _, v := range Venues {
		s.Wallets.Set(v.Name, v.QuoteAsset, 0)
		s.Wallets.Set(v.Name, v.BaseAsset, 0)
	}
	for venue, assets := range rec.Balances {
		if !isKnownVenue(venue) {
			continue
		}
		for asset, amt := range assets {
			s.Wallets.Set(venue, asset, amt)
		}
	}

	s.InitialUSD = rec.InitialUSD
	s.InitialBTC = rec.InitialBTC
	s.InitialWealth = rec.InitialWealth
	s.TotalWealth = rec.TotalWealth
	s.TotalNetProfit = rec.TotalNetProfit
	s.Credit = CreditState{AutoMode: rec.AutoCredit} // sin préstamos fantasma
	s.IsReplenishing = false
	s.ReplenishExpiresAt = time.Time{}
	s.InsufficientFundsPending = false
}
