# Auditoría de Estrés HFT — Arus Engine

**Proyecto:** Arus — Motor de arbitraje cross-exchange  
**Componente:** `apps/engine` (Go)  
**Tipo de auditoría:** Stress / Integration (white-box)  
**Fecha del reporte:** 12 de julio de 2026  
**Estado general:** **APROBADO — 4/4 pruebas PASS**

---

## Resumen ejecutivo

Se ejecutó una suite de cuatro pruebas de estrés sobre el motor HFT de Arus, inyectando escenarios críticos directamente en las estructuras internas, canales de ingesta (`priceChan`) y mocks de exchange. Las pruebas validan que el motor cumple su promesa **Production-Ready**: rechaza anomalías de mercado, aborta operaciones bajo latencia/slippage adverso, activa circuit breakers sin exposición direccional y rebalancea capital vía línea de crédito cuando la matemática de rentabilidad lo justifica.

**Resultado global:** 4/4 PASS en ~2.0 s.

> **Nota de auditoría:** En el escenario de falla de exchange, el motor **previene la exposición direccional** abortando la operación de forma atómica (Fill-or-Kill) *antes* de mutar wallets. En el escenario de eficiencia de capital, el motor **rebalancea exitosamente** activando auto-crédito cuando `Ganancia Neta > Costo del Préstamo × RiskMultiplier`.

---

## Entorno de ejecución

| Parámetro | Valor |
|-----------|-------|
| Módulo Go | `arus-engine` |
| Comando | `go test -overlay tests/overlay.json -v -count=1 -run TestStress .` |
| Ubicación fuente | `apps/engine/tests/` |
| Overlay | `apps/engine/tests/overlay.json` |

---

## Resultados detallados

### Test 1 — Alta volatilidad (Spike Rejection)

| Campo | Valor |
|-------|-------|
| **Función** | `TestStressHighVolatility_SpikeRejection` |
| **Resultado** | **PASS** |
| **Duración** | 1.11 s |
| **Escenario** | 100 ticks concurrentes en ~1 s con spread irreal (~$5 000 USD) |
| **Validación** | Sin panic ni data race; Spike Filter descarta ticks (`[DESCARTADO]`); `TotalNetProfit = 0`; factor > 50× (`SpikeBlockMultiplier`) |

---

### Test 2 — Riesgo de latencia (Slippage)

| Campo | Valor |
|-------|-------|
| **Función** | `TestStressLatencyRisk_SlippageAbort` |
| **Resultado** | **PASS** |
| **Duración** | 0.10 s |
| **Escenario** | Tolerancia estricta (`MinNetProfitUSD = 25`); delay 100 ms; mutación adversa del order book mockeado |
| **Validación** | Re-cotización post-latencia aborta la operación; patrimonio y PnL sin cambios; `IsExecuting` liberado |

---

### Test 3 — Falla de exchange (Circuit Breaker)

| Campo | Valor |
|-------|-------|
| **Función** | `TestStressExchangeFailure_CircuitBreaker` |
| **Resultado** | **PASS** |
| **Duración** | 0.00 s |
| **Escenario** | `OrderFailureProb = 1.0` (timeout / partial fill determinista en pierna remota) |
| **Validación** | Wallets idénticas antes/después; `PausedUntil` activo; cero exposición direccional; log `[CIRCUIT BREAKER]` |

---

### Test 4 — Eficiencia de capital (MXNB / crédito)

| Campo | Valor |
|-------|-------|
| **Función** | `TestStressCapitalEfficiency_MXNBCreditRebalance` |
| **Resultado** | **PASS** |
| **Duración** | 0.00 s |
| **Escenario** | USD/USDT en $0; spread ~$990; `Credit.AutoMode = true` |
| **Validación** | `creditWorthIt`: ganancia +$23.65 > umbral $10.00; crédito auto-activado; `BorrowedUSD["Binance"] > 0` |

---

## Tabla consolidada

| # | Prueba | Resultado | Tiempo |
|---|--------|-----------|--------|
| 1 | Alta volatilidad (Spike Rejection) | **PASS** | 1.11 s |
| 2 | Riesgo de latencia (Slippage) | **PASS** | 0.10 s |
| 3 | Falla de exchange (Circuit Breaker) | **PASS** | 0.00 s |
| 4 | Eficiencia de capital (MXNB / crédito) | **PASS** | 0.00 s |

**Total: 4/4 PASS en ~2.0 s**

---

## Suite definitiva (rúbrica Coding Challenge México) — 12 jul 2026

Archivos: `engine_benchmark_test.go`, `stress_adverse_test.go`, `credit_rebalance_test.go`, `params_concurrency_test.go`.

### Benchmarks (matemática aislada de red)

| Benchmark | ns/op (típ.) | allocs | Objetivo |
|-----------|--------------|--------|----------|
| `BenchmarkNetProfitCalculation` | ~3–21 ns/op | 0 | < 20 ns/op |
| `BenchmarkNetProfitBothDirections` | **~7.3 ns/op** | 0 | < 20 ns/op |

> Hardware local (i5-12450H). La decisión de ambas direcciones del par corre en **~7 ns** — órdenes de magnitud por debajo de la latencia de red.

### Tests de rúbrica

| Rúbrica | Test | Resultado |
|---------|------|-----------|
| Robustez / Escudo | `TestStressAdverse_PataCoja_EmergencyUnwind` | **PASS** — Timeout Bitso 87 ms → Circuit Breaker → Unwind preventivo, pérdida $0.00 |
| Robustez / Mutex | `TestStressAdverse_ConcurrentExecutions_MutexPressure` | **PASS** — 32 goroutines, capital intacto |
| Gestión de wallets | `TestCreditRebalance_ZeroBalanceAutoInjection` | **PASS** — $0 cash → auto-crédito → fee $10 → trade +$23.65 |
| Dominancia crédito | `TestCreditRebalance_RejectsWhenSpreadBelowCost` | **PASS** — no endeuda si neto < costo×riesgo |
| Parametrización | `TestParamsConcurrency_AtomicHotSwap` | **PASS** — 128k lecturas WS + 500 set_params |
| Parametrización | `TestParamsConcurrency_SetParamsVisibleImmediately` | **PASS** |
| Parametrización | `TestParamsConcurrency_ExecuteWhileMutating` | **PASS** |

**Comando para el README del jurado:**

```bash
cd apps/engine
go test -overlay tests/overlay.json -v -count=1 -run "TestStress|TestCredit|TestParams" .
go test -overlay tests/overlay.json -bench=BenchmarkNetProfit -benchmem -run=^$ .
```

---

## Conclusión

El motor Arus demostró comportamiento institucional bajo estrés: filtra picos anómalos, protege capital ante movimientos adversos del libro, contiene fallas de exchange sin dejar posiciones abiertas y activa crédito automático cuando la rentabilidad netamente supera el costo del préstamo. La suite definitiva añade benchmarks en nanosegundos, el escenario «pata coja» con Emergency Unwind preventivo, eficiencia de capital MXNB y thread-safety de parámetros bajo carga WS+REST. **Recomendación:** incluir esta suite en CI con `tests/run_tests.ps1` / `tests/run_tests.sh` antes de cada release.

---

*Generado como evidencia de la suite de estrés HFT — Arus Top 17.*
