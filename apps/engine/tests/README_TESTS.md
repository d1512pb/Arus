# Guía de pruebas — Arus Engine

Esta carpeta (`apps/engine/tests/`) concentra las **pruebas de estrés HFT**, mocks asociados, reportes de auditoría y scripts de ejecución.

---

## Estructura

```
apps/engine/
├── tests/
│   ├── engine_stress_test.go       # Suite original de estrés (4 escenarios)
│   ├── engine_benchmark_test.go    # Benchmarks ns/op (computeNetProfit)
│   ├── stress_adverse_test.go      # Pata coja / Circuit Breaker / Unwind
│   ├── credit_rebalance_test.go    # Crédito MXNB + dominancia
│   ├── params_concurrency_test.go  # Thread-safety de TradingParameters
│   ├── mock_exchange_book_test.go  # Mock de order books Binance/Bitso
│   ├── stress_helpers_test.go      # Fixtures compartidos
│   ├── overlay.json                # Mapeo Go overlay
│   ├── run_tests.sh / run_tests.ps1
│   ├── STRESS_TEST_REPORT.md
│   └── README_TESTS.md
├── engine_test.go                  # Unit tests (raíz)
└── ...
```

### Comandos rápidos (para el README del jurado)

```bash
cd apps/engine

# Suite de rúbrica (stress + credit + params + adverse)
go test -overlay tests/overlay.json -v -count=1 \
  -run 'TestStress|TestCredit|TestParams' .

# Benchmark de decisión matemática (aislado de red)
go test -overlay tests/overlay.json -bench=BenchmarkNetProfit -benchmem -run=^$ .
```

---

## ¿Por qué los tests viven aquí pero se compilan desde la raíz?

El motor es `package main`. En Go, los tests white-box que acceden a símbolos internos (`executeForSession`, `computeNetProfit`, etc.) deben compilarse **en el mismo paquete** que el código fuente.

Para organizar los archivos en `tests/` sin romper el acceso interno, usamos el flag **`-overlay`** de Go (`tests/overlay.json`), que mapea los archivos de esta carpeta como si estuvieran en `apps/engine/` durante `go test`.

---

## Cómo ejecutar las pruebas

### Opción 1 — Script (recomendado)

**Linux / macOS / Git Bash:**

```bash
cd apps/engine/tests
chmod +x run_tests.sh
./run_tests.sh
```

**Windows (PowerShell):**

```powershell
cd apps/engine/tests
.\run_tests.ps1
```

### Opción 2 — Manual

```bash
cd apps/engine

# Suite completa del motor (unit + integración en raíz)
go test -v -count=1 .

# Solo pruebas de estrés (fuentes en tests/)
go test -overlay tests/overlay.json -v -count=1 -run TestStress .
```

### Opción 3 — Detector de carreras (Linux / CI)

```bash
cd apps/engine
go test -race -overlay tests/overlay.json -count=1 -run TestStressHighVolatility .
```

> En Windows nativo, `-race` puede fallar si CGO no está configurado. Usa WSL o CI Linux.

---

## Cómo añadir nuevos tests en esta carpeta

### Pruebas de estrés (white-box, acceso interno)

1. Crea un archivo `*_test.go` en `apps/engine/tests/` con `package main`.
2. Añade una entrada en `overlay.json`:

   ```json
   {
     "Replace": {
       "mi_nuevo_test.go": "tests/mi_nuevo_test.go"
     }
   }
   ```

3. Ejecuta:

   ```bash
   cd apps/engine
   go test -overlay tests/overlay.json -v -run TestMiNuevo .
   ```

4. Actualiza `STRESS_TEST_REPORT.md` si es una prueba de auditoría para el jurado.

### Unit tests simples (sin acceso interno)

Si el test solo usa API exportada o funciones puras ya testeadas en `engine_test.go`, puedes dejarlo en la **raíz** de `apps/engine/` (`engine_test.go`, `graph_test.go`, etc.) — es el patrón estándar de Go para `package main`.

### Mocks

- Coloca mocks reutilizables en archivos dedicados (`mock_*_test.go`).
- Regístralos en `overlay.json` igual que los tests.
- Documenta en un comentario de cabecera qué escenario simula cada mock.

---

## Convenciones de nomenclatura

| Prefijo / patrón | Uso |
|------------------|-----|
| `TestStress*` | Pruebas de estrés HFT (auditoría) |
| `mock*` | Mocks de exchange / fixtures |
| `*_test.go` | Obligatorio para que Go reconozca tests |

---

## Checklist antes de demo / CI

- [ ] `cd apps/engine/tests && ./run_tests.sh` → todo PASS
- [ ] Revisar `STRESS_TEST_REPORT.md` actualizado
- [ ] Sin cambios en lógica de negocio ni UI (solo tests)

---

## Contacto / mantenimiento

Para ampliar la suite, sigue el patrón de las 4 pruebas existentes en `engine_stress_test.go`: inyectar escenario → validar invariantes (PnL, wallets, mutex, circuit breaker, crédito).
