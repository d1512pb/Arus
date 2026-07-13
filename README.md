<div align="center">

# ⬡ ARUS

### Motor de arbitraje omnidireccional de alta frecuencia · multi-exchange · multi-activo

> Plataforma **production-ready** de arbitraje cripto: grafo de liquidez en vivo, ejecución atómica, sesiones persistidas y terminal institucional conectada por WebSocket. Datos 100 % reales de **Binance, Bitso y Kraken**.

![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)
![Next.js](https://img.shields.io/badge/Next.js-16-000000?logo=nextdotjs)
![React](https://img.shields.io/badge/React-19-61DAFB?logo=react&logoColor=black)
![SQLite](https://img.shields.io/badge/SQLite-sesiones%20completas%20·%20CGO--free-003B57?logo=sqlite&logoColor=white)
![Detección](https://img.shields.io/badge/detección-~56ns%2Ftick-brightgreen)
![Tests](https://img.shields.io/badge/tests-90%2B%20·%20CI%20con%20--race-blue)
![Licencia](https://img.shields.io/badge/licencia-MIT-blue)

**Autor:** Daniel Peredo Borgonio · **Reto:** CODING_CHALLENGE_MEXICO

🔗 **Demo en vivo:** **[arus-snowy.vercel.app](https://arus-snowy.vercel.app)** · ⚙️ Motor (API): [arus-engine.fly.dev/api/ledger](https://arus-engine.fly.dev/api/ledger)

</div>

---

## Índice

1. [Resumen ejecutivo](#resumen-ejecutivo)
2. [Evolución del producto](#evolución-del-producto)
3. [Filosofía del producto — cuatro pilares](#filosofía-del-producto--cuatro-pilares)
4. [El problema y la solución](#el-problema-y-la-solución)
5. [Estrategia e inteligencia del bot](#estrategia-e-inteligencia-del-bot)
6. [El Radar Omnidireccional (grafo de liquidez)](#el-radar-omnidireccional-grafo-de-liquidez)
7. [Velocidad y eficiencia](#velocidad-y-eficiencia-detección-de-oportunidades)
8. [Precisión del cálculo de rentabilidad neta](#precisión-del-cálculo-de-rentabilidad-neta)
9. [Robustez y gestión de riesgo](#robustez-y-gestión-de-riesgo-circuit-breakers)
10. [Auditoría interna y pruebas de estrés](#auditoría-interna-y-pruebas-de-estrés)
11. [Persistencia y continuidad](#persistencia-y-continuidad-sesiones-completas--trade-ledger)
12. [Arquitectura y stack tecnológico](#arquitectura-y-stack-tecnológico)
13. [Interfaz y experiencia de usuario](#interfaz-y-experiencia-de-usuario)
14. [Parámetros y configuración — referencia completa](#parámetros-y-configuración--referencia-completa)
15. [Instalación y ejecución local](#instalación-y-ejecución-local)
16. [Despliegue](#despliegue)
17. [Capturas de pantalla](#capturas-de-pantalla)
18. [Licencia](#licencia)

---

## Resumen ejecutivo

**Arus** es un motor de **arbitraje omnidireccional**: modela el mercado como un **grafo de liquidez** donde cada nodo es un activo en un exchange (`BTC@Binance`, `USD@Bitso`, `ETH@Kraken`…) y cada arista una forma de convertirlo (libros de órdenes reales, paridad USDT≈USD, inventario pre-fondeado). Una oportunidad de arbitraje es un **ciclo rentable** en ese grafo — comprar barato y vender caro entre exchanges (espacial) o rotar tres pares dentro de uno (triangular) son el mismo problema matemático, detectado con Bellman-Ford sobre pesos `−log(tasa·(1−fee))`, cada segundo, con datos 100 % reales de **Binance, Bitso y Kraken** (9 libros de órdenes en vivo).

El sistema descuenta comisiones y slippage *antes* de decidir, dimensiona cada orden contra la liquidez visible del libro y deja al **usuario** la última palabra sobre riesgo, universo de mercados y crédito — con botones que automatizan tramos del flujo cuando el usuario lo autoriza. Todo es editable en vivo, validado por el backend y **persistido** en SQLite.

---

## Evolución del producto

Arus no nació como un grafo omnidireccional: **evolucionó** desde un bot de spread espacial (Binance ↔ Bitso, un par) hacia una plataforma multi-venue / multi-activo. Cada etapa resolvió una fricción real del arbitraje:

| Etapa | Qué aprendimos | Qué quedó en el producto |
|---|---|---|
| **Spread neto** | El bruto engaña: fees y slippage pueden volver negativa una “oportunidad” | Una sola fórmula (`computeNetProfit`) para detectar, ejecutar, simular y medir |
| **Inventario asimétrico** | Rebalancear on-chain tarda ~30+ min y se pierden ticks rentables | Crédito temporal vs. reequilibrio — **no dormir el capital** mientras hay ganancia neta |
| **Errores de mercado** | Spikes, feeds congelados y fallos FoK rompen bots ingenuos | Filtros y circuit breakers que **abortan antes** de mutar wallets |
| **Quién manda** | La automatización sin soberanía asusta al operador | El usuario **decide**; Arus propone y automatiza solo lo que él activa |
| **Universo personal** | No todos operan los mismos exchanges ni monedas | Catálogo ampliable + poda por sesión → **otro subgrafo = otras oportunidades** |
| **Omnidireccional** | Espacial y triangular son el mismo ciclo en un grafo | Radar + Bellman-Ford sobre el universo activo del usuario |

---

## Filosofía del producto — cuatro pilares

Arus no es un bot de spread bruto: es una plataforma de arbitraje con disciplina institucional. Descansa en cuatro pilares verificables en el código:

| Pilar | Principio | Implementación en el motor |
|---|---|---|
| **La Física Real del Dinero** | Solo opera cuando la ganancia **neta** supera el margen del usuario, tras fees y slippage en cada pierna | `computeNetProfit` (engine.go) — única fórmula para detección, ejecución del par, ciclos del radar, simulador y benchmarks |
| **Eficiencia de Capital** | El capital no duerme: si rebalancear implica perder la ventana, el crédito temporal permite **seguir capturando** la oportunidad cuando la matemática lo justifica | `creditWorthIt`, `cycleCreditProjection`, `handleLiquidityShortfall` — inecuación `ganancia > costo × RiskMultiplier` en modo clásico y radar |
| **Control Absoluto del Usuario** | La **última decisión** siempre es humana: riesgo, universo, crédito y autopiloto. Arus automatiza tramos (presets, auto-crédito, radar) solo cuando el usuario lo enciende | 16 parámetros por sesión (`set_params`), onboarding guiado/experto, `enabled_venues` / `enabled_assets`, presets Conservador/Balanceado/Agresivo, toggles de crédito y autopiloto |
| **Escudo de Robustez** | Ante fallas de exchange o precios absurdos, no queda exposición direccional | Fill-or-Kill atómico, Spike Filter, staleness, `MaxDivergenceRatio`, evento `CIRCUIT_BREAKER` + Emergency Unwind preventivo |

---

## El problema y la solución

El error clásico del arbitraje novato es operar sobre el **spread bruto** (`Ask < Bid`) ignorando que cada operación tiene costos que pueden volverla negativa. Arus parte de modelar esos costos — y de no abandonar oportunidades válidas por fricciones operativas.

| | Arbitraje ingenuo | **Arus** |
|---|---|---|
| Señal de entrada | Spread bruto positivo | Ciclo con tasa **neta** > margen del usuario, tras fees + slippage |
| Comisiones | Se asumen "despreciables" | Modeladas por exchange y **personalizables** (cuentas VIP pagan menos) |
| Slippage | Ignorado | Estimado por pierna (configurable) y descontado antes de decidir |
| Alcance | Un par fijo | **Grafo omnidireccional**: espacial + triangular con el mismo detector |
| Universo | Igual para todos | Cada usuario **poda** venues/activos → su propio mapa de oportunidades |
| Volumen | Fijo | `min(tope del usuario, liquidez real del libro en cada pierna)` |
| Anomalías de precio | Se opera sobre ellas | **Filtros** (Spike + staleness + divergencia) descartan el tick erróneo |
| Falta de fondos | Se detiene o falla | Crédito temporal vs. reequilibrio — **no perder** la ventana rentable por esperar el traslado |
| Autonomía | Todo automático o todo manual | El usuario decide; botones automatizan partes del flujo con opt-in |
| Estado | Volátil / en memoria | **Sesiones completas persistidas** + ledger inmutable de auditoría |

---

## Estrategia e inteligencia del bot

El motor opera con **dos estrategias conmutables por el usuario**:

- **Modo clásico (default):** arbitraje espacial del par BTC entre Binance y Bitso con fondos pre-posicionados en ambos lados — compra y venta **simultáneas**. Evalúa las **dos** direcciones cada tick y ejecuta la de **mayor neto**.
- **Modo radar / autopiloto (opt-in):** el grafo de liquidez completo. El motor busca cada segundo el mejor **ciclo** dentro del **universo del usuario** — espacial o **triangular** — con los fees de ESE usuario en los pesos, y lo ejecuta atómicamente si supera SU margen. Sin autopiloto, el radar **detecta y muestra**; no ejecuta solo.

### Crédito vs. reequilibrio — no perder la oportunidad

Cuando un exchange agota su inventario, reponerlo con un traslado on-chain implica **~30+ minutos** de capital idle. En ese intervalo el mercado sigue moviéndose y las ventanas netas se evaporan.

Arus modela una **línea de crédito temporal** cuyos términos define cada usuario y decide (o propone) según:

```
pedir préstamo ⇔ ganancia proyectada > costo del crédito × RiskMultiplier
```

- **Objetivo:** no sacrificar una oportunidad neta válida solo porque el inventario está desbalanceado.
- El préstamo es **siempre temporal**: al vencer se devuelve y el inventario del par vuelve a 50/50.
- Un crédito activo **jamás se persiste como capital del usuario**.
- Si el usuario prefiere esperar el reequilibrio, puede hacerlo: la **última decisión es suya** (pedir crédito, esperar redistribución o descartar el shortfall).

### Quién decide y qué se automatiza

| Decisión | Quién la toma | Automatización disponible |
|---|---|---|
| Margen mínimo, tamaño, fees, spike, divergencia | **Usuario** (`set_params`, panel de estrategia) | Presets Conservador / Balanceado / Agresivo |
| Universo (qué exchanges y monedas importan) | **Usuario** | Listas `enabled_venues` / `enabled_assets` |
| Ejecutar ciclos del radar sin confirmar cada uno | **Usuario** (opt-in) | Toggle `radar_autopilot` |
| Crédito ante shortfall | **Usuario** | Auto-crédito si cumple la inecuación; o botones Pedir crédito / Esperar reequilibrio / Descartar |
| Inyectar escenarios de prueba | **Usuario** | Banco **Probar el bot** (omni, tormenta, spike, custom) |

Arus **asiste y acelera**; no sustituye al operador.

---

## El Radar Omnidireccional (grafo de liquidez)

El mercado se modela como un **grafo dirigido** (diseño completo en [`docs/FASE2-GRAFO.md`](docs/FASE2-GRAFO.md)):

| Concepto | En el grafo |
|---|---|
| Un activo en un venue (`BTC@Binance`, `USD@Bitso`) | **Nodo** — con el saldo real del usuario superpuesto |
| Comprar/vender en un libro de órdenes | **Arista de libro** (tasa = top-of-book real, fee = taker del usuario + slippage) |
| USDT ≈ USD | **Arista de paridad** visible y etiquetada |
| Mismo activo en dos exchanges pre-fondeados | **Arista de inventario** |
| Oportunidad de arbitraje | **Ciclo de peso negativo** — detectado por Bellman-Ford |

**Topología actual:** **9 nodos** y **32 aristas** (Binance: triángulos BTC/ETH/SOL · Bitso: BTC/USD · Kraken: triángulo BTC/ETH).

### Extensible por diseño — un universo distinto por usuario

La lógica de decisión **no conoce** “Binance” ni “BTC” a fuego: lee un **registro / catálogo** (`venues.go`, opcionalmente `venues.json` vía `ARUS_CATALOG`).

- **Más exchanges o monedas** = entrada en el catálogo + adaptador de feed → nodos y aristas nuevos, **cero cambios** en Bellman-Ford ni en `computeNetProfit`.
- Cada sesión puede **podar** el grafo (`enabled_venues`, `enabled_assets`): un usuario solo Bitso+Kraken y otro con el catálogo completo ven **subgrafos distintos** y, por tanto, **oportunidades distintas** (espaciales, triangulares o ambas).
- Fees y slippage del usuario entran en los pesos: el mismo ciclo puede ser rentable para uno e inviable para otro.

Eso convierte a Arus en una plataforma de arbitraje **personalizable**, no en un bot de un solo par para todos.

---

## Velocidad y eficiencia (detección de oportunidades)

**Núcleo de detección — latencia medida, no estimada.** El hot path (mids, Spike Filter, fees, slippage, neto y decisión) es **O(1)**:

```
BenchmarkOpportunityDetection-12     ~56 ns/op                  tick completo: Spike Filter + ambas direcciones + decisión
BenchmarkNetProfitBothDirections-12  ~7.3 ns/op   0 allocs/op   decisión del par (lo que corre executeForSession)
BenchmarkNetProfitCalculation-12     ~3–21 ns/op  0 allocs/op   fórmula neta aislada
(Intel i5-12450H · go test -bench · tracker thread-safe · cifras del reporte de estrés)
```

≈ **18 millones de evaluaciones completas por segundo y por núcleo** (tick completo); la aritmética neta aislada corre **sin asignaciones**. Reproducible con:

```bash
cd apps/engine && go test -bench=Detection -benchmem -run=^$
cd apps/engine && go test -overlay tests/overlay.json -bench=BenchmarkNetProfit -benchmem -run=^$ .
```

**Ingesta 100 % WebSocket nativo, sin polling:**

| Plano | Mecanismo | Latencia |
|---|---|---|
| Exchanges → motor | WebSocket push (Binance streams combinados · Bitso `orders` · Kraken ticker v2) | Cada cambio del top-of-book |
| Motor → navegador | WebSocket `/ws` (gorilla) | Operaciones: inmediatas · Radar: coalescido ~1/s |

**Optimizaciones:** canal con búfer (1 000 eventos), Spike Filter O(1), una goroutine por sesión, persistencia **write-behind** (disco nunca frena el trading).

---

## Precisión del cálculo de rentabilidad neta

Una única fórmula gobierna TODAS las decisiones — `computeNetProfit` en engine.go:

```
Neto = (P_venta × V × (1 − fee_venta)) − (P_compra × V × (1 + fee_compra)) − Slippage
```

- **Fees por exchange** en ambas piernas, personalizables por sesión
- **Slippage estimado** descontado antes de decidir (default 5 bps/pierna)
- **Volumen dimensionado** contra liquidez real del libro
- **Umbral personal** por usuario — evita *fee bleeding*
- **P&L limpio:** `InitialWealth` se mueve con depósitos/retiros

---

## Robustez y gestión de riesgo (circuit breakers)

Arus asume que el mercado miente a veces. Los **filtros** existen para **evitar errores costosos**, no para decorar el UI:

| Filtro / compuerta | Qué evita |
|---|---|
| **Spike Filter** (ingesta 5 % + tolerancia por sesión) | Operar sobre un tick absurdo (+50 %, glitch, inyección de prueba) |
| **Staleness (>10 s)** | Usar un libro congelado como si fuera precio vivo |
| **`MaxDivergenceRatio`** | Arbitrar cuando dos casas “no hablan del mismo mercado” |
| **Fill-or-Kill atómico** | Quedarse a media pierna (exposición direccional) |
| **Doble hard block de fondos** | Commit con saldo insuficiente (plan + lock) |
| **Cooldown 3 s + `IsExecuting`** | Carreras TOCTOU entre ticks concurrentes |
| **Inecuación de crédito** | Pedir préstamo que no cubre `costo × RiskMultiplier` |

El botón **«Precio falso / error»** en *Probar el bot* (`inject_fake`, circuit.go) provoca anomalías reales (spike de ingesta, timeout FoK, divergencia) y el motor las **rechaza** emitiendo `CIRCUIT_BREAKER` — sin luces verdes ni mutación de capital.

---

## Auditoría interna y pruebas de estrés

Suite concentrada en `apps/engine/tests/` (compilada vía overlay sobre `package main` para acceso white-box). Reporte completo: [`apps/engine/tests/STRESS_TEST_REPORT.md`](apps/engine/tests/STRESS_TEST_REPORT.md).

### Ejecución

```bash
cd apps/engine/tests && ./run_tests.sh    # Linux/macOS/Git Bash
cd apps/engine/tests && .\run_tests.ps1   # Windows PowerShell

# Manual
cd apps/engine
go test -v -count=1 .                                                    # 90+ unit tests (raíz)
go test -overlay tests/overlay.json -v -count=1 -run 'TestStress|TestCredit|TestParams' .
go test -overlay tests/overlay.json -bench=BenchmarkNetProfit -benchmem -run=^$ .
```

### Resultados verificados (julio 2026)

| Categoría | Test | Resultado |
|---|---|---|
| **Benchmarks algorítmicos** | `BenchmarkNetProfitCalculation` (~3–21 ns/op) · `BenchmarkOpportunityDetection` (~56 ns/op) | PASS — fórmula neta 0 allocs; hot path O(1) |
| **Alta volatilidad** | `TestStressHighVolatility_SpikeRejection` | PASS — 100 ticks concurrentes; Spike Filter descarta; PnL = 0 |
| **Latencia / slippage** | `TestStressLatencyRisk_SlippageAbort` | PASS — re-cotización aborta; capital intacto |
| **Falla de exchange** | `TestStressExchangeFailure_CircuitBreaker` | PASS — FoK atómico; `PausedUntil` activo |
| **«La Pata Coja»** | `TestStressAdverse_PataCoja_EmergencyUnwind` | PASS — compra ACK en Binance + TIMEOUT en Bitso → `[CIRCUIT BREAKER]` + `[EMERGENCY UNWIND]`; delta neutral; pérdida $0 |
| **Eficiencia de capital** | `TestStressCapitalEfficiency_MXNBCreditRebalance` | PASS — auto-crédito cuando ganancia > costo × k |
| **Thread-safety** | `TestParamsConcurrency_AtomicHotSwap` · `TestParamsConcurrency_ExecuteWhileMutating` | PASS — `atomic.Pointer` bajo 64 lectores WS + escritor REST |
| **Presión de mutex** | `TestStressAdverse_ConcurrentExecutions_MutexPressure` | PASS — 32 goroutines; sin estado corrupto |

Frontend: `npm test` — 14 tests del layout paramétrico del radar (vitest). CI: `go vet` + `go test -race` + vitest + builds en cada push.

### Smoke E2E de *Probar el bot*

Scripts Node (≥ 21) validan el flujo WebSocket sin reiniciar el motor:

```bash
cd apps/engine && node smoke_omni.mjs      # Oportunidad normal → omni_executed
cd apps/engine && node smoke_storm.mjs     # Evento poco común → storm_trade
cd apps/engine && node smoke_circuit.mjs     # Precio falso → CIRCUIT_BREAKER
cd apps/engine && node smoke_custom_sim.mjs  # POST /api/simulate/custom
```

---

## Persistencia y continuidad (sesiones completas + Trade Ledger)

Arus persiste en SQLite **la sesión completa de cada usuario**: saldos multi-activo, estrategia, PnL y preferencias sobreviven a reinicios del motor **y** del navegador. Token UUID v4 en `localStorage` → `resume_session` recupera todo sin login.

- **Esquema multi-activo:** tabla `balances (session_id, venue, asset, amount)`
- **Patrón repositorio:** interfaz `SessionStore` — migrar a Postgres es escribir otro driver
- **Write-behind:** cada mutación dispara fotografía asíncrona vía `sendWalletUpdate`
- **Sin préstamos fantasma:** la fotografía persiste solo fondos PROPIOS

### Trade Ledger (auditoría inmutable)

| Endpoint | Qué devuelve |
|---|---|
| `GET /api/ledger?session_id=<uuid>` | Últimos 100 registros de TU sesión |
| `GET /api/stats?session_id=<uuid>` | P&L acumulado, win rate, ops/hora, fricción total |
| `GET /api/ledger.csv?session_id=<uuid>` | Historial completo como CSV |

### ¿Por qué SQLite embebida?

Un proceso, un escritor (write-behind), lecturas esporádicas — el patrón exacto de SQLite. Transaccional ACID, un archivo en el volumen de Fly.io, esquema auto-migrado al arrancar. La interfaz `SessionStore` deja la puerta abierta a Postgres si aparecen múltiples instancias del motor.

---

## Arquitectura y stack tecnológico

```mermaid
flowchart LR
    subgraph EXT["Mercados externos"]
        BIN["Binance · 5 libros"]
        BIT["Bitso · 1 libro"]
        KRK["Kraken · 3 libros"]
    end

    subgraph ENGINE["Motor · Go"]
        WS["FeedAdapters"]
        CH["Canal 1000 eventos"]
        EVAL["Detección PAR O(1)"]
        GRAPH["RADAR · Bellman-Ford"]
        EXEC["Ejecutores por sesión"]
        DB[("SQLite · WAL")]
    end

    subgraph WEB["Web · Next.js"]
        RADAR["Vista RADAR"]
        UI["Vista DASHBOARD"]
        SIM["Probar el bot"]
    end

    BIN & BIT & KRK -->|WebSocket| WS --> CH --> EVAL --> EXEC
    CH --> GRAPH --> EXEC
    EXEC -->|WebSocket| UI & RADAR
    SIM -->|inject_* / POST simulate| ENGINE
    EXEC -.->|write-behind| DB
```

| Capa | Tecnología | Justificación |
|---|---|---|
| Motor | **Go 1.26** | Goroutines + channels para bucle de eventos sin bloqueos |
| Comunicación | **WebSocket** (gorilla) | Tiempo real en ambos extremos |
| Persistencia | **SQLite** (modernc.org/sqlite) | CGO-free; detrás de `SessionStore` |
| Frontend | **Next.js 16 / React 19** | App Router, TypeScript end-to-end |
| Estilos | **Tailwind CSS 4** | Modo oscuro persistente, responsive |
| CI | **GitHub Actions** | `go test -race` + vitest + builds |

**Organización clave del motor:**

```
apps/engine/
├── engine.go      # computeNetProfit, bucle Start, ejecutor clásico, crédito
├── graph.go       # LiquidityGraph, Bellman-Ford
├── cycle.go       # planCycle + commitCycle + autopiloto
├── venues.go      # registro de venues/instrumentos (extensible)
├── simulate.go    # POST /api/simulate/custom (tubería real)
├── omni.go        # inject_omni — oportunidad omnidireccional
├── storm.go       # inject_storm — ráfaga HFT
├── circuit.go     # inject_fake — escudo de robustez
├── server.go      # WebSocket hub, set_params, handlers
├── store.go       # SessionStore + SQLite
└── tests/         # Suite de estrés + benchmarks (overlay)
```

---

## Interfaz y experiencia de usuario

Web app **Radar-first** ([`docs/REDISENO-RADAR.md`](docs/REDISENO-RADAR.md)): el grafo de liquidez es la pantalla principal; el dashboard concentra P&L, wallets, ledger y analítica.

### Terminal institucional en tiempo real

- **WebSocket persistente** (`useArusEngine.ts`): P&L, patrimonio animado, feed de operaciones y ledger por sesión
- **Vista RADAR:** grafo SVG a pantalla completa, revelación progresiva (hover → tooltip, click → card de nodo), partículas en el ciclo rentable, luz verde por trade, tematización claro/oscuro vía `--radar-*`
- **Vista DASHBOARD:** KPIs, salud de inventario por venue, donut de distribución de capital, historial/auditoría, analítica con curva de P&L y export CSV
- **Onboarding adaptativo:** modo Guiado (un número + presets) y Experto (matriz de % por exchange validada en UI y backend)

### El usuario manda; los botones aceleran

La UI está diseñada para que **ninguna automatización sea silenciosa**:

- Shortfall de liquidez → panel con **Pedir crédito**, **Esperar reequilibrio** o **Descartar** (más auto-crédito solo si el usuario lo habilitó).
- Radar → el ciclo se ve en el grafo; la ejecución en bucle requiere **autopiloto** explícito.
- Estrategia → presets y sliders; el motor **clampa y valida** en backend, no “adivina” riesgo.
- **Probar el bot** → el usuario dispara escenarios a voluntad para auditar el comportamiento bajo presión.

### Probar el bot — banco de pruebas sin reiniciar el motor

Accesible desde el header en **ambas vistas**. Inyecta escenarios por el **mismo pipeline** que los feeds reales — misma `computeNetProfit`, mismos circuit breakers, mismo ejecutor:

| Escenario | Acción | Comportamiento verificado |
|---|---|---|
| **Oportunidad normal** | `inject_omni` | Fabrica ineficiencia en libros reales → radar descubre ciclo → `planCycle`/`commitCycle` → `omni_executed` + animación |
| **Evento poco común** | `inject_storm` | Ráfaga concurrente (7.5 s, 2 workers) → `storm_trade` por fill → mutex estable |
| **Precio falso / error** | `inject_fake` | Spike / timeout FoK / divergencia → `CIRCUIT_BREAKER` efímero, capital intacto |
| **Crear tu propia prueba** | `POST /api/simulate/custom` | Dos libros definidos por el usuario → `publishTick` en canal de ingesta |

---

## Parámetros y configuración — referencia completa

Fuente de verdad viva: `GET /api/config` — cada parámetro con default, rango y catálogo de venues.

### Parámetros por sesión (editables EN VIVO)

| Parámetro (wire) | Qué controla | Default |
|---|---|---|
| `min_net_profit_usd` | Umbral de ganancia neta | $0.10 |
| `max_order_size_btc` | Tope de volumen | 0.005 BTC |
| `slippage_rate` | Slippage por pierna | 5 bps |
| `spike_tick_deviation` | Spike Filter por sesión | 5 % |
| `max_divergence_ratio` | Divergencia máxima entre casas | 1.20 |
| `risk_multiplier` | Inecuación del crédito | 1.0 |
| `order_failure_prob` | Prob. Fill-or-Kill (simulador) | 5 % |
| `enabled_venues` / `enabled_assets` | Universo (poda del grafo → oportunidades distintas) | todos |
| `radar_autopilot` | Detección → ejecución automática de ciclos | off |

Bloque completo de crédito (`credit_line_usd`, `credit_line_btc`, `credit_apr`, `credit_origination_fee`, `credit_duration_min`) y comisiones por venue — ver `GET /api/config`.

### Variables de entorno

| Variable | Default |
|---|---|
| `PORT` | `8080` |
| `ARUS_CATALOG` | `./venues.json` si existe |
| `ARUS_DB_PATH` | `data/ledger.db` |

Frontend: `NEXT_PUBLIC_ENGINE_WS_URL` y `NEXT_PUBLIC_ENGINE_HTTP_URL` (ver `apps/web/.env.example`).

---

## Instalación y ejecución local

> Sistema funcional en **menos de 2 minutos**. Sin Docker ni base de datos externa.

### Prerrequisitos

| Software | Versión |
|---|---|
| **Go** | 1.26+ |
| **Node.js** | 18+ (smokes E2E: 21+) |
| **npm** | 9+ |

### 1) Motor

```bash
cd apps/engine
go run .
# :8080 — crea data/ledger.db automáticamente
```

### 2) Dashboard

```bash
cd apps/web
npm install
npm run dev
# http://localhost:3000
```

### 3) Validar continuidad

1. Configura capital, opera con **Probar el bot**
2. Mata el motor, reinícialo, recarga el navegador → **«Recuperando tu sesión…»** → todo persiste

### Tests

```bash
cd apps/engine && go test ./...
cd apps/engine/tests && .\run_tests.ps1
cd apps/web && npm test
```

---

## Despliegue

| Componente | Plataforma | URL en producción |
|---|---|---|
| **Frontend** | **Vercel** (root: `apps/web`) | [arus-snowy.vercel.app](https://arus-snowy.vercel.app) |
| **Motor** | **Fly.io** (volumen persistente `data/`) | [arus-engine.fly.dev](https://arus-engine.fly.dev/api/ledger) |

```bash
flyctl auth login
cd apps/engine
flyctl deploy
```

Variables en Vercel (Production — requieren rebuild):

```
NEXT_PUBLIC_ENGINE_WS_URL=wss://arus-engine.fly.dev/ws
NEXT_PUBLIC_ENGINE_HTTP_URL=https://arus-engine.fly.dev
```

---

## Capturas de pantalla

### Vista RADAR — grafo de liquidez en acción

El radar es la pantalla principal: columnas por exchange, nodos con saldo en vivo, ciclo rentable resaltado y dinamismo HFT.

![Radar operando durante oportunidades](assets/Radar_operando_durante_oportunidades.png)

![Radar a máxima capacidad — ráfaga HFT](assets/Radar_operando_maxima_capacidad.png)

![Radar con línea de crédito activa](assets/Radar_operando_con_credito.png)

### Vista DASHBOARD — terminal institucional

P&L acumulado, salud de inventario, feed de operaciones y precios en vivo.

![Dashboard modo oscuro](assets/Dashboard_modo_oscuro.png)

![Dashboard modo claro](assets/Dashboard_modo_luminoso.png)

### Onboarding y configuración

<table>
<tr>
<td width="50%" valign="top"><img src="assets/Configuracion_Inicial_modo_guiado.png" alt="Onboarding modo guiado"><br><sub>Modo Guiado: un solo número, presets desde $100, reparto 50/50 explicado.</sub></td>
<td width="50%" valign="top"><img src="assets/Configuracion_Inicial_modo_experto.png" alt="Onboarding modo experto"><br><sub>Modo Experto: matriz de % por exchange, suma 100 validada en UI y backend.</sub></td>
</tr>
</table>

### Crédito, fondos y operaciones pausadas

<table>
<tr>
<td width="42%" valign="top"><img src="assets/Editar_fondos.png" alt="Editar fondos por exchange"><br><sub>Depósito/retiro por venue — ajusta la base del P&L sin distorsionar rendimiento.</sub></td>
<td width="58%" valign="top"><img src="assets/operaciones_detenidas_por_Redistribuir.png" alt="Pausa por reequilibrio"><br><sub>Decisión asistida: crédito vs. reequilibrio cuando faltan fondos.</sub></td>
</tr>
</table>

### Tutorial y guía

<table>
<tr>
<td width="50%" valign="top"><img src="assets/Tutorial.png" alt="Tutorial guiado"><br><sub>Tutorial radar-first de 8 pasos con chip de ubicación de cada elemento.</sub></td>
<td width="50%" valign="top"><img src="assets/Guia_funcionamiento_Arus.png" alt="Guía de funcionamiento"><br><sub>Guía de los cuatro pilares y flujo operativo del bot.</sub></td>
</tr>
</table>

---

## Licencia

Este proyecto se distribuye bajo la **licencia MIT** — eres libre de usar, copiar, modificar y distribuir el código, citando la autoría. El texto completo está en [`LICENSE`](LICENSE).

---

<div align="center">

**Arus** · CODING_CHALLENGE_MEXICO · Daniel Peredo Borgonio · Licencia MIT

*Motor de arbitraje omnidireccional — production-ready*

</div>
