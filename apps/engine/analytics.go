package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"
)

// analytics.go — ANALÍTICA DERIVADA DEL LEDGER (Sprint D).
//
// Los datos ya se persisten (Trade Ledger inmutable); esta capa solo los LEE y
// los agrega: P&L acumulado en serie temporal, win rate, fricción total pagada
// (fees + slippage), ops/hora y export CSV. Igual que el ledger, es por sesión:
// el session_id (UUID no enumerable) es el token de acceso, y todas las
// consultas usan placeholders parametrizados.

// StatsPoint es un punto de la serie de P&L acumulado.
type StatsPoint struct {
	T   string  `json:"t"`   // timestamp RFC3339 de la operación
	Cum float64 `json:"cum"` // P&L acumulado (USD) hasta ese punto, crédito incluido
}

// SessionStats es el resumen analítico de una sesión, calculado desde el ledger.
type SessionStats struct {
	// TotalOps cuenta solo OPERACIONES (excluye filas de crédito/reequilibrio).
	TotalOps   int     `json:"total_ops"`
	Wins       int     `json:"wins"`
	Losses     int     `json:"losses"`
	WinRatePct float64 `json:"win_rate_pct"`
	// NetProfitUSD es el P&L acumulado del historial completo, incluidos los
	// costos de crédito (coincide con la "Ganancia Neta" del dashboard).
	NetProfitUSD float64 `json:"net_profit_usd"`
	// FeesUSD es la fricción total pagada en operaciones (fees + slippage).
	// Filas anteriores a la migración de fees_usd cuentan 0 (no se inventa).
	FeesUSD      float64      `json:"fees_usd"`
	VolumeBTC    float64      `json:"volume_btc"`
	OpsPerHour   float64      `json:"ops_per_hour"`
	CreditEvents int          `json:"credit_events"`
	FirstOpAt    string       `json:"first_op_at,omitempty"`
	LastOpAt     string       `json:"last_op_at,omitempty"`
	Series       []StatsPoint `json:"series"`
}

// maxSeriesPoints acota la serie que viaja a la UI: con historiales largos se
// muestrea por paso constante (el último punto siempre se conserva).
const maxSeriesPoints = 400

// computeSessionStats agrega el historial completo de una sesión. Función de
// una sola pasada sobre las filas en orden cronológico, separada del transporte
// para poder testearla con registros sintéticos.
func computeSessionStats(records []TradeRecord) SessionStats {
	stats := SessionStats{Series: []StatsPoint{}}
	cum := 0.0
	var firstOp, lastOp time.Time

	for _, r := range records {
		cum += r.NetProfitUSD
		stats.Series = append(stats.Series, StatsPoint{T: r.Timestamp.UTC().Format(time.RFC3339), Cum: cum})

		if r.IsCreditInjection {
			stats.CreditEvents++
			continue
		}
		stats.TotalOps++
		if r.NetProfitUSD > 0 {
			stats.Wins++
		} else {
			stats.Losses++
		}
		stats.FeesUSD += r.FeesUSD
		stats.VolumeBTC += r.VolumeBTC
		if firstOp.IsZero() {
			firstOp = r.Timestamp
		}
		lastOp = r.Timestamp
	}
	stats.NetProfitUSD = cum

	if stats.TotalOps > 0 {
		stats.WinRatePct = float64(stats.Wins) / float64(stats.TotalOps) * 100
		stats.FirstOpAt = firstOp.UTC().Format(time.RFC3339)
		stats.LastOpAt = lastOp.UTC().Format(time.RFC3339)
		// Ritmo: operaciones por hora sobre la ventana observada, con piso de
		// 1 minuto para que dos trades en el mismo segundo no reporten infinito.
		hours := lastOp.Sub(firstOp).Hours()
		if hours < 1.0/60 {
			hours = 1.0 / 60
		}
		stats.OpsPerHour = float64(stats.TotalOps) / hours
	}

	stats.Series = downsampleSeries(stats.Series, maxSeriesPoints)
	return stats
}

// downsampleSeries reduce la serie a ≤ maxPts conservando el primer y el último
// punto (muestreo por paso constante: la forma de la curva se preserva).
func downsampleSeries(pts []StatsPoint, maxPts int) []StatsPoint {
	if len(pts) <= maxPts || maxPts < 2 {
		return pts
	}
	out := make([]StatsPoint, 0, maxPts)
	step := float64(len(pts)-1) / float64(maxPts-1)
	for i := 0; i < maxPts; i++ {
		out = append(out, pts[int(float64(i)*step+0.5)])
	}
	out[len(out)-1] = pts[len(pts)-1]
	return out
}

// writeAnalyticsCORS replica la política CORS del ledger (Vercel↔Fly).
func writeAnalyticsCORS(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return false
	}
	return true
}

// statsHandler expone GET /api/stats?session_id=<uuid>: el resumen analítico de
// UNA sesión. Sin session_id responde el struct vacío (misma respuesta neutra
// que el ledger: no hay forma de listar datos ajenos).
func statsHandler(w http.ResponseWriter, r *http.Request) {
	if !writeAnalyticsCORS(w, r) {
		return
	}

	stats := SessionStats{Series: []StatsPoint{}}
	if sessionID := r.URL.Query().Get("session_id"); sessionID != "" {
		records, err := getAllTradesForSession(sessionID)
		if err != nil {
			http.Error(w, `{"error":"no se pudo leer el ledger"}`, http.StatusInternalServerError)
			log.Printf("⚠️ [STATS] Error al leer registros: %v", err)
			return
		}
		stats = computeSessionStats(records)
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(stats); err != nil {
		log.Printf("⚠️ [STATS] Error al serializar: %v", err)
	}
}

// ledgerCSVHandler expone GET /api/ledger.csv?session_id=<uuid>: el historial
// completo de la sesión como archivo CSV descargable (auditoría portátil).
func ledgerCSVHandler(w http.ResponseWriter, r *http.Request) {
	if !writeAnalyticsCORS(w, r) {
		return
	}

	sessionID := r.URL.Query().Get("session_id")
	records := []TradeRecord{}
	if sessionID != "" {
		var err error
		records, err = getAllTradesForSession(sessionID)
		if err != nil {
			http.Error(w, "no se pudo leer el ledger", http.StatusInternalServerError)
			log.Printf("⚠️ [CSV] Error al leer registros: %v", err)
			return
		}
	}

	name := "arus-ledger.csv"
	if len(sessionID) >= 8 {
		name = "arus-ledger-" + sessionID[:8] + ".csv"
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))

	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"id", "timestamp_utc", "tipo", "compra", "venta_o_ruta", "volumen_btc", "spread_usd", "fees_usd", "neto_usd"})
	for _, rec := range records {
		tipo := "operacion"
		if rec.IsCreditInjection {
			tipo = "credito_reequilibrio"
		}
		_ = cw.Write([]string{
			strconv.FormatInt(rec.ID, 10),
			rec.Timestamp.UTC().Format(time.RFC3339),
			tipo,
			rec.BuyExchange,
			rec.SellExchange,
			strconv.FormatFloat(rec.VolumeBTC, 'f', 8, 64),
			strconv.FormatFloat(rec.SpreadUSD, 'f', 2, 64),
			strconv.FormatFloat(rec.FeesUSD, 'f', 4, 64),
			strconv.FormatFloat(rec.NetProfitUSD, 'f', 4, 64),
		})
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		log.Printf("⚠️ [CSV] Error al escribir CSV: %v", err)
	}
}
