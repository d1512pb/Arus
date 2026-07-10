package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
)

// config.go — EL CATÁLOGO COMO CONFIGURACIÓN (no solo como dato compilado).
//
// Dos piezas que hacen AUDITABLE y DEMOSTRABLE la parametrización del motor:
//
//  1. LoadCatalog: al arrancar, el registro de venues/instrumentos/paridades
//     puede venir de un archivo JSON (env ARUS_CATALOG, o ./venues.json si
//     existe). Agregar un libro a un venue ya conectado (p. ej. SOL/USD en
//     Kraken) pasa a ser un cambio de DATOS sin recompilar: el FeedAdapter se
//     suscribe solo (instrumentsForVenue) y el grafo gana sus nodos y aristas.
//     Limitación honesta: un venue NUEVO sigue necesitando su FeedAdapter en
//     Go (la ingesta es código); mientras no lo tenga, aparece en el radar sin
//     datos de precio.
//
//  2. GET /api/config: el motor DECLARA todo lo que controla — defaults de la
//     estrategia, rangos de clamp, catálogo, términos del crédito, guardrails
//     y variables de entorno. Un solo curl enseña la superficie completa de
//     parametrización, y el frontend puede construir sus formularios desde
//     aquí en vez de duplicar rangos a mano.

// CatalogFile es el contrato del catálogo externo (mismo shape que /api/config
// expone en "venues"/"instruments"/"parity_pairs").
type CatalogFile struct {
	Venues      []Venue      `json:"venues"`
	Instruments []Instrument `json:"instruments"`
	// ParityPairs declara equivalencias 1:1 entre activos ("USDT" ≈ "USD").
	// Opcional: ausente = se conservan las paridades compiladas.
	ParityPairs [][2]string `json:"parity_pairs,omitempty"`
}

// LoadCatalog puebla los registros desde el archivo externo si existe. DEBE
// correr antes de NewLiquidityGraph y StartFeeds (main.go): ambos leen los
// registros al construirse. Cualquier defecto de validación conserva el
// catálogo compilado — un JSON roto jamás deja al motor sin mercado.
func LoadCatalog() {
	path := os.Getenv("ARUS_CATALOG")
	if path == "" {
		if _, err := os.Stat("venues.json"); err == nil {
			path = "venues.json"
		} else {
			return // sin catálogo externo: registros compilados (el caso normal)
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		log.Printf("⚠️ [CATÁLOGO] No se pudo leer %s — usando el registro compilado: %v", path, err)
		return
	}
	var cf CatalogFile
	if err := json.Unmarshal(data, &cf); err != nil {
		log.Printf("⚠️ [CATÁLOGO] JSON inválido en %s — usando el registro compilado: %v", path, err)
		return
	}
	if err := validateCatalog(cf); err != nil {
		log.Printf("⚠️ [CATÁLOGO] %s rechazado — usando el registro compilado: %v", path, err)
		return
	}

	Venues = cf.Venues
	Instruments = cf.Instruments
	if cf.ParityPairs != nil {
		parityPairs = cf.ParityPairs
	}
	log.Printf("🗂️ [CATÁLOGO] Registro cargado desde %s: %d venues, %d instrumentos, %d paridades",
		path, len(Venues), len(Instruments), len(parityPairs))
}

// validateCatalog aplica las reglas que mantienen al motor operable: nombres
// únicos, fees en rango, libros bien formados y el PAR CLÁSICO presente (el
// capital inicial, el crédito y el reequilibrio operan sobre él).
func validateCatalog(cf CatalogFile) error {
	if len(cf.Venues) == 0 || len(cf.Instruments) == 0 {
		return fmt.Errorf("el catálogo necesita al menos 1 venue y 1 instrumento")
	}
	seen := map[string]bool{}
	for _, v := range cf.Venues {
		switch {
		case v.Name == "":
			return fmt.Errorf("venue sin nombre")
		case seen[v.Name]:
			return fmt.Errorf("venue duplicado: %q", v.Name)
		case v.DefaultTakerFee < 0 || v.DefaultTakerFee > MaxTakerFee:
			return fmt.Errorf("fee de %q fuera de rango [0, %v]: %v", v.Name, MaxTakerFee, v.DefaultTakerFee)
		case v.BaseAsset == "" || v.QuoteAsset == "":
			return fmt.Errorf("venue %q sin base/quote", v.Name)
		}
		seen[v.Name] = true
	}
	for _, name := range classicPair {
		if !seen[name] {
			return fmt.Errorf("falta el venue %q del par clásico (capital inicial, crédito y reequilibrio dependen de él)", name)
		}
	}
	keys := map[string]bool{}
	for _, in := range cf.Instruments {
		switch {
		case !seen[in.Venue]:
			return fmt.Errorf("instrumento %s/%s referencia un venue no declarado: %q", in.Base, in.Quote, in.Venue)
		case in.Base == "" || in.Quote == "" || in.Base == in.Quote:
			return fmt.Errorf("instrumento mal formado en %q: base=%q quote=%q", in.Venue, in.Base, in.Quote)
		case in.StreamID == "":
			return fmt.Errorf("instrumento %s sin stream_id", in.Key())
		case keys[in.Key()]:
			return fmt.Errorf("instrumento duplicado: %s", in.Key())
		}
		keys[in.Key()] = true
	}
	for _, p := range cf.ParityPairs {
		if p[0] == "" || p[1] == "" || p[0] == p[1] {
			return fmt.Errorf("paridad mal formada: %v", p)
		}
	}
	return nil
}

// paramRange describe el rango de clamp de un parámetro editable, con su default.
type paramRange struct {
	Min     float64 `json:"min"`
	Max     float64 `json:"max"`
	Default float64 `json:"default"`
	Unit    string  `json:"unit"`
}

// configHandler expone GET /api/config: la declaración completa de lo que el
// motor controla. Sin estado ni autenticación: solo configuración, nunca datos
// de sesiones.
func configHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	defaults := DefaultTradingParameters()
	resp := map[string]interface{}{
		// Los parámetros por sesión (acción WS set_params), con default y rango
		// de clamp: la fuente de verdad que la UI refleja en sus formularios.
		"session_params": map[string]paramRange{
			"taker_fees[venue]":      {MinTakerFee, MaxTakerFee, 0, "fracción (0.001 = 0.1 %); default por venue, ver venues"},
			"min_net_profit_usd":     {MinNetProfitFloor, MaxNetProfitCeil, defaults.MinNetProfitUSD, "USD"},
			"max_order_size_btc":     {MinOrderSizeBTC, MaxOrderSizeCapBTC, defaults.MaxOrderSizeBTC, "BTC"},
			"slippage_rate":          {MinSlippageRate, MaxSlippageRate, defaults.SlippageRate, "fracción por pierna (0.0005 = 5 bps)"},
			"spike_tick_deviation":   {MinSpikeDeviation, MaxSpikeDeviation, defaults.SpikeTickDeviation, "fracción tick-a-tick"},
			"max_divergence_ratio":   {MinDivergenceRatioLimit, MaxDivergenceRatioLimit, defaults.MaxDivergenceRatio, "ratio entre venues"},
			"risk_multiplier":        {MinRiskMultiplier, MaxRiskMultiplier, defaults.RiskMultiplier, "× costo del crédito"},
			"credit_line_usd":        {MinCreditLineUSDParam, MaxCreditLineUSDParam, defaults.CreditLineUSD, "USD"},
			"credit_line_btc":        {MinCreditLineBTCParam, MaxCreditLineBTCParam, defaults.CreditLineBTC, "BTC"},
			"credit_apr":             {0, MaxCreditAPRParam, defaults.CreditAPR, "fracción anual"},
			"credit_origination_fee": {0, MaxCreditFeeParam, defaults.CreditOriginationFee, "USD"},
			"credit_duration_min":    {MinCreditDurationMin, MaxCreditDurationMin, defaults.CreditDurationMin, "minutos"},
			"order_failure_prob":     {0, MaxOrderFailureProb, defaults.OrderFailureProb, "fracción por orden"},
		},
		// enabled_venues / enabled_assets / radar_autopilot no son numéricos:
		// listas del universo (subconjunto del catálogo) y el opt-in del autopiloto.
		"session_params_other": map[string]string{
			"enabled_venues":  "subconjunto de venues; vacío = todos",
			"enabled_assets":  "subconjunto de assets; vacío = todos",
			"radar_autopilot": "bool: ejecutar el mejor ciclo del universo (apaga el ejecutor clásico)",
		},

		// El catálogo vigente (compilado o cargado de ARUS_CATALOG).
		"venues":         Venues,
		"instruments":    Instruments,
		"assets":         knownAssets(),
		"parity_pairs":   parityPairs,
		"parity_assumed": AssumeUSDTParity,
		"classic_pair":   classicPair,

		// Guardrails del motor (constantes de esta build).
		"guardrails": map[string]interface{}{
			"spike_warn_multiplier":     SpikeWarnMultiplier,
			"spike_block_multiplier":    SpikeBlockMultiplier,
			"max_book_staleness_sec":    MaxBookStaleness / time.Second,
			"min_executable_volume_btc": MinExecutableVolumeBTC,
			"trade_cooldown_sec":        3,
			"rebalance_duration_min":    RebalanceDurationMinutes,
			"order_failure_pause_sec":   OrderFailurePause / time.Second,
			"max_demo_liquidity_btc":    MaxDemoLiquidity,
			"max_demo_spread_usd":       MaxDemoSpreadUSD,
		},

		// Variables de entorno que el motor honra.
		"env": map[string]string{
			"PORT":         "puerto HTTP (default 8080)",
			"ARUS_CATALOG": "ruta a un catálogo JSON de venues/instrumentos/paridades (default: ./venues.json si existe)",
			"ARUS_DB_PATH": "ruta del SQLite de ledger+sesiones (default: data/ledger.db)",
		},
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("⚠️ [CONFIG] Error al serializar configuración: %v", err)
	}
}
