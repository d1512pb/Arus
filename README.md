<div align="center">

# ⬡ ARUS

### Motor de arbitraje omnidireccional de alta frecuencia · multi-exchange · multi-activo

> ### 🚧 TRABAJO EN CURSO — pendiente a continuación
> Rama `feat/arbitraje-omnidireccional`. Este README es la **referencia completa del proyecto** (documento de handoff: se puede retomar el trabajo leyendo solo esto): qué hay construido, cómo funciona y qué sigue. Los Sprints C y D (Kraken, SOL, crédito para ciclos, analítica) **y el rediseño Radar-first de la UI** (el grafo es ahora la pantalla principal) ya están integrados y verificados. Queda: desplegar la rama, **actualizar las capturas** (las actuales son de la versión anterior) y la sesión de revisión multi-agente. Ver [Estado de la rama](#-estado-de-la-rama-bitácora) y [Qué sigue](#-qué-sigue--plan-de-evolución).

*Un grafo de liquidez en vivo detecta ciclos de arbitraje —espaciales entre exchanges y triangulares dentro de uno— con datos 100 % reales de **Binance, Bitso y Kraken**, descuenta cada fricción (fees + slippage) y ejecuta solo cuando la ganancia neta supera el margen que **cada usuario** define. Sesiones completas persistidas: el bot te recuerda.*

![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)
![Next.js](https://img.shields.io/badge/Next.js-16-000000?logo=nextdotjs)
![React](https://img.shields.io/badge/React-19-61DAFB?logo=react&logoColor=black)
![SQLite](https://img.shields.io/badge/SQLite-sesiones%20completas%20·%20CGO--free-003B57?logo=sqlite&logoColor=white)
![Detección](https://img.shields.io/badge/detección-~50ns%2Ftick-brightgreen)
![Tests](https://img.shields.io/badge/tests-60%2B%20·%20CI%20con%20--race-blue)
![Licencia](https://img.shields.io/badge/licencia-MIT-blue)

**Autor:** Daniel Peredo Borgonio · **Reto:** CODING_CHALLENGE_MEXICO

🔗 **Demo en vivo:** **[arus-snowy.vercel.app](https://arus-snowy.vercel.app)** *(⚠️ el deploy corre la versión de `main`; esta rama aún no se despliega)* · ⚙️ Motor (API): [arus-engine.fly.dev/api/ledger](https://arus-engine.fly.dev/api/ledger)

</div>

---

## 📑 Índice

1. [Estado de la rama (bitácora)](#-estado-de-la-rama-bitácora)
2. [Resumen ejecutivo](#-resumen-ejecutivo)
3. [El problema y la solución](#-el-problema-y-la-solución)
4. [Estrategia e inteligencia del bot](#-estrategia-e-inteligencia-del-bot)
5. [El Radar Omnidireccional (grafo de liquidez)](#-el-radar-omnidireccional-grafo-de-liquidez)
6. [Velocidad y eficiencia](#-velocidad-y-eficiencia-detección-de-oportunidades)
7. [Precisión del cálculo de rentabilidad neta](#-precisión-del-cálculo-de-rentabilidad-neta)
8. [Robustez y gestión de riesgo](#-robustez-y-gestión-de-riesgo-circuit-breakers)
9. [Persistencia y continuidad](#-persistencia-y-continuidad-sesiones-completas--trade-ledger)
10. [Arquitectura y stack tecnológico](#-arquitectura-y-stack-tecnológico)
11. [Interfaz y experiencia de usuario](#-interfaz-y-experiencia-de-usuario)
12. [Instalación y ejecución local](#-instalación-y-ejecución-local)
13. [Despliegue](#-despliegue)
14. [Qué sigue — plan de evolución](#-qué-sigue--plan-de-evolución)
15. [Capturas de pantalla](#-capturas-de-pantalla)
16. [Licencia](#-licencia)

---

## 🚧 Estado de la rama (bitácora)

> Esta sección existe para retomar el proyecto **sin más contexto que este README**. Resume qué se construyó en esta rama, en qué orden y por qué. El diseño detallado del grafo vive en [`docs/FASE2-GRAFO.md`](docs/FASE2-GRAFO.md); el del rediseño de la UI (decisiones, mockup aprobado y notas de rendimiento), en [`docs/REDISENO-RADAR.md`](docs/REDISENO-RADAR.md).

El proyecto partió de un bot de arbitraje del par BTC entre Binance y Bitso (rama `main`, lo que muestra el deploy actual) y evolucionó en esta rama hacia una **plataforma omnidireccional personalizable** con el radar como pantalla principal. Todo lo siguiente está implementado, testeado (50+ tests de Go y 13 de frontend; CI en GitHub Actions con `go test -race` + `vitest` + builds) y verificado end-to-end contra feeds reales:

| Etapa | Qué se construyó | Piezas clave |
|---|---|---|
| **Fase 0 — Estandarización y personalización** | Fórmula institucional única de rentabilidad; parámetros de estrategia editables **en vivo** por el usuario con validación de backend; multiplicador de riesgo en la decisión de crédito | `computeNetProfit` (engine.go) · acción `set_params` + `sanitizeTradingParams` (server.go) · `StrategyPanel.tsx` · `creditWorthIt` |
| **Fase 1 — Arquitectura para expansión** | Exchanges e instrumentos como **datos** (registro); adaptadores de feed con liquidez y timestamp por tick; volumen dimensionado contra liquidez real; detección de feeds congelados (staleness >10 s); ledger por sesión | `venues.go` (Venues/Instruments) · `feed.go` (interface FeedAdapter) · `ws_real_market.go` · `sizeOrder` |
| **Fase 2 · hito 1 — Radar** | Grafo de liquidez en vivo: nodos activo@venue, aristas de libro/paridad/inventario con pesos `−log(tasa·(1−fee))`, Bellman-Ford detecta ciclos automáticamente, visualización SVG por sesión (~1/s) | `graph.go` · `GraphPanel.tsx` · evento `graph_update` |
| **Fase 2 · hito 2 — Multi-instrumento** | N libros por venue: Binance emite el **triángulo BTC/USDT · ETH/USDT · ETH/BTC** por un solo socket de streams combinados → el radar detecta arbitraje **triangular** además del espacial, con datos reales | `Instruments` en venues.go · streams combinados en ws_real_market.go · CI (`.github/workflows/ci.yml`) |
| **Sprint A — Continuidad** | Sesiones **completas** persistidas en SQLite (interfaz `SessionStore`, esquema `balances` multi-activo): saldos, estrategia, PnL y preferencias sobreviven a reinicios del motor y del navegador; token en `localStorage` + `resume_session` con takeover entre pestañas; la reconexión ya no resetea el progreso | `store.go` · tablas `sessions`/`balances` · flujo resume en `useArusEngine.ts` |
| **Sprint B / hito 3 — Ejecución omnidireccional** | Wallets **multi-activo en memoria** (`Balances`: venue→asset→cantidad, mismo esquema que persiste el store); **ejecutor de ciclos** (planificación pura + commit atómico con Fill-or-Kill antes de tocar saldos); **Universe por sesión** (el usuario elige exchanges/monedas = poda del grafo); **fees del usuario en los pesos** (`FindBestCycleFor`); **autopiloto del radar** opt-in que sustituye al ejecutor clásico del par | `cycle.go` (planCycle/commitCycle) · `Balances` (models.go) · `RadarAutopilot`/`EnabledVenues`/`EnabledAssets` en TradingParameters |
| **Sprint C — Más exchanges y monedas** | **Kraken como tercer venue** (WebSocket v2, canal `ticker` con `event_trigger=bbo`: BTC/USD · ETH/USD · ETH/BTC) y **SOL/USDT + SOL/BTC** en Binance (segundo triángulo) → radar de **9 nodos / 32 aristas**; **par clásico explícito** (`classicPair` = Binance+Bitso): capital inicial, crédito y reequilibrio operan solo sobre el par (un tercer venue ya no infla el capital ni diluye el crédito); `planCycle` prueba **todas las rotaciones cash** del ciclo → un ciclo vía Kraken es ejecutable sin fondos en Kraken | `krakenFeed` (ws_real_market.go) · `classicPair`/`classicVenues` (venues.go) · `planRotation` (cycle.go) |
| **Sprint D — Crédito para ciclos + analítica** | El autopiloto **ya no omite en silencio** oportunidades sin fondos: `cycleCreditProjection` re-planifica con la línea de crédito hipotética y la decisión pasa por la misma inecuación `ganancia > costo × k` del modo clásico (auto-crédito / reequilibrio / diálogo asistido); **analítica desde el ledger**: columna `fees_usd` (fricción total, con migración aditiva automática), `GET /api/stats` (P&L acumulado en serie, win rate, ops/hora), `GET /api/ledger.csv` (export) y **panel de Analítica** en el dashboard (tiles + curva de P&L con hover) | `cycleCreditProjection` (cycle.go) · `analytics.go` (stats/CSV) · `AnalyticsPanel.tsx` |
| **Rediseño Radar-first (UI)** | **El radar es ahora la pantalla principal** (revelación progresiva: nodos con activo+saldo; precio/fee/liquidez al hover de cada arista; detalle completo al click con card flotante/sheet); vistas `RADAR \| DASHBOARD` con header compacto (patrimonio con contador animado, **Probar el bot** global); **Estrategia como drawer sobre el grafo** (préstamo automático incluido; la poda del universo se VE: los venues excluidos se atenúan); dinamismo: pulso por tick, partículas recorriendo el ciclo, cobro flotante `+$X`, spike en rojo, barrido de sonar; **layout paramétrico** (1..N venues sin tocar código, fan-out de aristas intra-venue) con 13 tests puros | `radarLayout.ts` · `RadarView.tsx` · `HeaderBar.tsx` · `StrategyDrawer.tsx` · plan y mockup en `docs/REDISENO-RADAR.md` |

**Decisiones de diseño que hay que conocer para seguir trabajando:**

- **Dos modos de ejecución conviven.** Modo clásico: el par BTC entre Binance↔Bitso (`executeForSession`, el camino original probado). Modo radar (autopiloto ON): ejecuta el mejor ciclo del subgrafo del usuario (`executeCycleForSession`) y **apaga** el clásico para esa sesión — nunca operan los dos a la vez.
- **El par clásico es explícito (`classicPair` = Binance+Bitso).** El reparto inicial 50/50, la línea de crédito y el reequilibrio operan SOLO sobre esos dos venues. Los demás (Kraken) son venues *solo-radar*: nacen con saldo cero y se fondean por depósitos del usuario o por ciclos del autopiloto. Sin esta distinción, cada venue nuevo inflaría el capital inicial (usd/2 por venue) y diluiría el crédito.
- **Los ciclos eligen su nodo de inicio.** `planCycle` prueba todas las rotaciones que empiezan en un nodo cash y ejecuta la de mayor neto viable: los swaps de inventario permiten que un ciclo que pasa por Kraken arranque desde USD@Bitso o USDT@Binance.
- **El wire motor↔UI es doble.** Los campos planos (`binance_usd`, `bitso_btc`…) se mantienen por compatibilidad con el dashboard desplegado; `wallet_update` además lleva `balances` (multi-activo completo) y `graph_update` lleva el radar. La UI de wallets clásicas todavía lee el plano.
- **USDT ≠ USD, declarado.** Binance opera BTC/USDT; Bitso y Kraken operan BTC/USD; la equivalencia 1:1 es una arista `EdgeParity` **visible** en el grafo (`AssumeUSDTParity`, `parityPairs` en venues.go), no un supuesto escondido.
- **El crédito TAMBIÉN aplica a ciclos del radar (Sprint D).** Cuando el plan de un ciclo muere por saldo, `cycleCreditProjection` calcula si la línea de crédito lo volvería viable y la decisión pasa por `handleLiquidityShortfall` — la misma inecuación `ganancia > costo × k`, el mismo diálogo asistido. La línea sigue llegando al par clásico (los ciclos arrancan desde sus nodos cash).
- **La UI es Radar-first (rediseño).** Dos vistas conmutadas por tabs: `RADAR` (principal, el grafo a pantalla completa con revelación progresiva) y `DASHBOARD` (todo lo clásico). La **Estrategia es un drawer sobre el grafo** (incluye el préstamo automático) y **Probar el bot vive en el header**, accesible desde ambas vistas — es la demo central para un juez. El plan de diseño con TODAS las decisiones (y el mockup aprobado) vive en [`docs/REDISENO-RADAR.md`](docs/REDISENO-RADAR.md).
- **El lienzo del radar está comprometido al modo oscuro** (es un terminal; colores explícitos, no tematiza), mientras header/cards/drawer sí siguen el tema claro/oscuro de la app. Validar esa decisión con el dueño mirando el modo claro sigue abierto (backlog).
- **Todo lo simulado sigue simulado.** Las órdenes no tocan APIs privadas de exchanges: los fills son instantáneos al top-of-book con slippage estimado, y el Fill-or-Kill es probabilístico (5 %). El puente a ejecución real (testnet) está en el plan (ver [Qué sigue](#-qué-sigue--plan-de-evolución)).

**Notas de entorno de desarrollo (gotchas reales):**

- `go test -race` **no corre en la máquina de desarrollo Windows** (requiere gcc de 64 bits); el CI de GitHub Actions lo cubre en cada push.
- En pruebas locales pueden quedar **procesos zombi en el puerto 8080** (el bind falla en silencio y te conectas a un motor viejo). Liberar con PowerShell: `Get-NetTCPConnection -LocalPort 8080 -State Listen | % { Stop-Process -Id $_.OwningProcess -Force }`.
- Smoke E2E por WebSocket: Node ≥ 21 trae `WebSocket` global — un script `.mjs` de ~40 líneas conecta a `ws://localhost:8080/ws`, envía acciones y valida eventos (patrón usado para verificar cada sprint).
- El esquema SQLite **se auto-crea/migra al arrancar** (`InitLedger` → `initSessionStore`); en Fly.io el volumen montado en `data/` conserva la base entre deploys sin pasos manuales.
- **Rendimiento del radar (aprendido en la verificación E2E, no regresionar):** `RadarView` está **memoizado** y recibe solo el último log de spike — pasarle el feed de logs completo re-renderiza TODO el lienzo con cada log (decenas por segundo con feeds reales). Las animaciones continuas (barrido) van en **overlays HTML**, no dentro del SVG (animar un `<g>` repinta el canvas entero por frame). El contador del patrimonio pinta SIEMPRE el valor real de React y anima por mutación imperativa encima.
- Frontend: `npm test` corre los tests de **vitest** (layout paramétrico del radar); el CI también los corre.
- Nota para sesiones con Claude Code: en la sesión del rediseño, la herramienta de screenshots del preview **no pudo capturar esta app** (timeouts con la página sana) — verificar por snapshot de accesibilidad + `eval` sobre el DOM, y dejar la validación visual al dueño en su navegador.

---

## 🎯 Resumen ejecutivo

**Arus** es un motor de **arbitraje omnidireccional**: modela el mercado como un **grafo de liquidez** donde cada nodo es un activo en un exchange (`BTC@Binance`, `USD@Bitso`, `ETH@Kraken`…) y cada arista una forma de convertirlo (libros de órdenes reales, paridad USDT≈USD, inventario pre-fondeado). Una oportunidad de arbitraje es un **ciclo rentable** en ese grafo — comprar barato y vender caro entre exchanges (espacial) o rotar tres pares dentro de uno (triangular) son el mismo problema matemático, y el motor los detecta con el mismo algoritmo (Bellman-Ford sobre pesos `−log(tasa·(1−fee))`), cada segundo, con datos 100 % reales de **Binance, Bitso y Kraken** (9 libros de órdenes en vivo).

Su diferenciador es doble. Primero, **modela la física real del dinero**: descuenta comisiones y slippage *antes* de decidir, rechaza las "trampas de liquidez" (rentables en bruto, negativas en neto) y dimensiona cada orden contra la liquidez visible del libro. Segundo, **el usuario tiene el control**: margen mínimo, tamaño de orden, fees, apetito de riesgo del crédito, con qué exchanges y monedas jugar, y si el radar solo detecta o también **ejecuta** (autopiloto) — todo editable en vivo, validado por el backend, y **persistido**: cierras el navegador, reinicia el servidor, y tu sesión (saldos, estrategia, historial) sigue ahí.

---

## 💡 El problema y la solución

El error clásico del arbitraje novato es operar sobre el **spread bruto** (`Ask < Bid`) ignorando que cada operación tiene costos que pueden volverla negativa. Arus parte de modelar esos costos.

| | Arbitraje ingenuo | **Arus** |
|---|---|---|
| Señal de entrada | Spread bruto positivo | Ciclo con tasa **neta** > margen del usuario, tras fees + slippage |
| Comisiones | Se asumen "despreciables" | Modeladas por exchange y **personalizables** (cuentas VIP pagan menos) |
| Slippage | Ignorado | Estimado por pierna (configurable) y descontado antes de decidir |
| Alcance | Un par fijo | **Grafo omnidireccional**: espacial + triangular con el mismo detector |
| Volumen | Fijo | `min(tope del usuario, liquidez real del libro en cada pierna)` |
| Anomalías de precio | Se opera sobre ellas | **Spike Filter** + staleness + compuertas de divergencia |
| Falta de fondos | Se detiene o falla | Decisión crédito-vs-reequilibrio con análisis de rentabilidad |
| Estado | Volátil / en memoria | **Sesiones completas persistidas** + ledger inmutable de auditoría |

---

## 🧠 Estrategia e inteligencia del bot

> **¿El bot detecta la primera oportunidad que aparece, o las prioriza y razona con una estrategia más sofisticada?**

El motor opera con **dos estrategias conmutables por el usuario**:

- **Modo clásico (default):** arbitraje espacial del par BTC entre Binance y Bitso con fondos pre-posicionados en ambos lados — compra y venta **simultáneas**, sin esperar confirmaciones on-chain. Evalúa las **dos** direcciones cada tick y ejecuta la de **mayor neto**, no la primera que aparece.
- **Modo radar / autopiloto (opt-in):** el grafo de liquidez completo. El motor busca cada segundo el mejor **ciclo** dentro del universo del usuario — espacial (libros + swaps de inventario + paridad, ahora también vía Kraken) o **triangular** (los dos triángulos de Binance o el de Kraken) — con los fees de ESE usuario en los pesos, y lo ejecuta atómicamente si supera SU margen. Si el plan muere por falta de saldo, la **línea de crédito** entra a la misma decisión que en el modo clásico. Ver [El Radar Omnidireccional](#-el-radar-omnidireccional-grafo-de-liquidez).

En ambos modos, la inteligencia está en resolver los sub-problemas que convierten un arbitraje *aparentemente obvio* en una pérdida real: ¿es rentable de verdad? (neto estricto), ¿el dato está roto? (spike filter + staleness), ¿alcanza la liquidez? (dimensionado por pierna), ¿qué hacer sin inventario? (crédito vs. reequilibrio).

### 🎛️ Personalización de estrategia: el usuario tiene el control

Los parámetros que gobiernan al bot son **del usuario, no del sistema** — editables **en vivo** desde el panel de Estrategia, por sesión, y **persistidos** con ella:

| Parámetro | Qué controla | Ejemplo de personalización |
|---|---|---|
| **Margen mínimo (USD)** | Umbral de ganancia neta para ejecutar | El usuario A exige $10; el usuario B opera desde $1 y captura las oportunidades pequeñas que A ignora |
| **Orden máxima (BTC)** | Tope de volumen por operación | Si el mercado ofrece 0.05 BTC de ineficiencia, quien configuró 0.01 entra; quien exige bloques de 1.0 la deja pasar |
| **Slippage estimado (bps)** | Deslizamiento asumido por pierna | Un perfil agresivo asume menos fricción y ejecuta más |
| **Comisiones por exchange** | Taker fee de cada casa | La misma oportunidad existe para quien paga 0.1 % y desaparece para quien paga 2 % — con sus fees en los pesos del grafo |
| **Multiplicador de riesgo** | Inecuación del crédito: `ganancia > costo × k` | Conservador `k=5` (solo endeudarse si cubre 5× el costo); agresivo `k=1` |
| **Tu universo** | Con qué exchanges y monedas juega el bot (poda del grafo) | Solo Binance → el radar busca únicamente ciclos triangulares internos |
| **Autopiloto del radar** | Detección → ejecución del mejor ciclo del universo | ON: ejecuta ciclos espaciales o triangulares; OFF: modo clásico del par |

El backend **valida y acota** cada valor a rangos sanos (`sanitizeTradingParams`) y responde con lo realmente aplicado: un mensaje malicioso no puede corromper una sesión (ni siquiera una fila de la base de datos manipulada a mano — la reanudación también sanea). Los cambios son atómicos (snapshot por operación): si editas a mitad de un trade, ese trade termina con los parámetros con los que empezó.

### ⭐ La capa de inteligencia financiera (crédito vs. reequilibrio)

En el arbitraje real existe un enemigo silencioso: el **tiempo muerto**. Cuando un exchange agota su inventario, reponerlo exige una transferencia on-chain de **~30+ minutos**, y durante esa espera el capital queda ocioso mientras las oportunidades —que viven milisegundos— se evaporan. La mayoría de los bots simplemente **se detienen**.

Arus no se detiene: **razona**. Modela una línea de crédito instantánea (`$50 000 USD` + `1 BTC`, con comisión de originación `$25` y APR `10 %` prorrateado al plazo, vía `calculateCreditCost`) y decide según la **inecuación de dominancia** con el apetito de riesgo del usuario:

```
pedir préstamo ⇔ ganancia proyectada > costo del crédito × RiskMultiplier
```

- **Préstamo automático ON:** si la desigualdad se cumple, pide la línea al instante y sigue operando *mientras* se reequilibra el inventario en segundo plano; si no, pausa y reequilibra 50/50 — **nunca se endeuda a pérdida** (el multiplicador nunca baja de 1).
- **Préstamo automático OFF:** el bot cede la decisión al usuario con los números sobre la mesa: ganancia posible, costo del crédito, el umbral personal (costo × k) y el resultado neto.
- **También en el radar (Sprint D):** cuando el plan de un **ciclo** del autopiloto muere por falta de saldo, `cycleCreditProjection` re-planifica con la línea hipotética y la misma inecuación decide — auto-crédito, reequilibrio o diálogo asistido. Ningún modo del bot omite ya en silencio una oportunidad por falta de fondos.

El préstamo es **siempre temporal**: al vencer el plazo se devuelve y el inventario del par vuelve a 50/50. Un préstamo activo **jamás se persiste como capital del usuario**: si el motor se reinicia a mitad de un crédito, la sesión reanuda con sus fondos propios, sin apalancamiento fantasma.

---

## 📡 El Radar Omnidireccional (grafo de liquidez)

> El corazón de la Fase 2 — diseño completo en [`docs/FASE2-GRAFO.md`](docs/FASE2-GRAFO.md).

El mercado se modela como un **grafo dirigido**:

| Concepto | En el grafo |
|---|---|
| Un activo en un venue (`BTC@Binance`, `USD@Bitso`, `ETH@Binance`) | **Nodo** — con el saldo real del usuario superpuesto |
| Comprar/vender en un libro de órdenes | **Arista de libro** (tasa = top-of-book real, fee = taker del usuario + slippage, liquidez = cantidad visible) |
| USDT ≈ USD (supuesto declarado) | **Arista de paridad** a tasa 1 — visible y etiquetada, no escondida |
| Mismo activo en dos exchanges pre-fondeados | **Arista de inventario** (compra y venta simultáneas; el traslado real se difiere al reequilibrio) |
| Oportunidad de arbitraje | **Ciclo de peso negativo** con `w = −log(tasa × (1 − fee))` — detectado por Bellman-Ford |

**Lo que hace hoy, con datos 100 % reales:**

- **Topología desde datos:** los registros `Venues` e `Instruments` (venues.go) generan nodos y aristas; hoy: **9 nodos** (USDT/BTC/ETH/SOL en Binance + USD/BTC en Bitso + USD/BTC/ETH en Kraken) y **32 aristas** (18 de libro: dos triángulos de Binance + el par de Bitso + el triángulo de Kraken; 10 swaps de inventario; 4 de paridad). Agregar un exchange o un par = entradas en el registro + adaptador de feed, **cero cambios en la lógica** — Kraken entró exactamente así.
- **Actualización O(1) por tick** y detección cada ~1 s: la vista global usa fees de referencia; la del usuario (`FindBestCycleFor`) usa SUS fees y SU universo — dos usuarios ven ciclos distintos en el mismo mercado.
- **Ejecución (autopiloto):** el ciclo se planifica en una función pura — rota a un inicio en efectivo, dimensiona contra saldo + tope del usuario + liquidez de **cada** pierna (mapeada a unidades de inicio) y exige que el neto supere el margen del usuario — y se ejecuta con **commit atómico**: re-verificación de fondos bajo lock (hard block) y Fill-or-Kill evaluado *antes* de tocar saldos (cero exposición direccional, sin necesidad de deshacer).
- **Visualización (rediseño Radar-first):** el radar ES la pantalla principal (`RadarView`, SVG a pantalla completa) con **revelación progresiva**: los nodos muestran solo activo + saldo (+≈USD); el precio/fee/liquidez de cada libro aparece al **hover** de su arista; el detalle completo de un nodo (sus libros, frescura del feed, editar fondos) al **click**. El ciclo detectado se resalta en verde con partículas recorriéndolo y su narración vive en una línea al pie; cuando no hay ciclo, lo dice honestamente: *"mercado eficiente — los fees superan al spread"*, con un barrido de sonar que comunica "sigo buscando". El layout es **paramétrico** (`lib/radarLayout.ts`): 1 venue o 5, el lienzo se deriva de los datos.
- **Honestidad del modelo:** el radar rara vez encuentra ciclos netos positivos en el mercado real — y eso es lo esperado. No maquilla: muestra el desglose de por qué cada ruta es (in)viable.

---

## ⚡ Velocidad y eficiencia (detección de oportunidades)

> **¿Con qué latencia identifico una divergencia? ¿WebSockets o polling? ¿Cómo optimizo el tiempo real?**

**Núcleo de detección — latencia medida, no estimada.** El cálculo que identifica y evalúa una oportunidad por cada tick (mids, spreads en ambas direcciones, media móvil del Spike Filter, fees, slippage, neto y decisión de viabilidad — todo vía la fórmula única `computeNetProfit`) es **O(1)** y está libre de asignaciones en el *hot path*:

```
BenchmarkOpportunityDetection-12   ~50 ns/op   16 B/op   0 allocs/op
(Intel i5-12450H · go test -bench · tracker del Spike Filter thread-safe)
```

≈ **20 millones de evaluaciones por segundo y por núcleo**. (El número anterior de ~21 ns/op correspondía al tracker sin sincronizar; el actual incluye el mutex que lo hace seguro entre goroutines — preferimos un número honesto a uno bonito.) Reproducible con:

```bash
cd apps/engine && go test -bench=Detection -benchmem -run=^$
```

**Ingesta de datos — 100 % WebSocket nativo, sin polling.** Cada venue tiene su `FeedAdapter`; Binance transporta sus **5 libros por un solo socket** de streams combinados y Kraken sus 3 por el canal `ticker` v2:

| Plano | Mecanismo | Endpoint / canal | Latencia |
|---|---|---|---|
| Exchanges → motor | **WebSocket nativo** | Binance `wss://stream.binance.com:9443/stream?streams=btcusdt@bookTicker/…/solbtc@bookTicker` · Bitso `wss://ws.bitso.com` (canal `orders`) · Kraken `wss://ws.kraken.com/v2` (canal `ticker`, `event_trigger=bbo`) | Push en cada cambio del *top-of-book* |
| Motor → navegador | **WebSocket** | `/ws` (gorilla) | Operaciones y alertas: **inmediatas**. Precio y radar: coalescidos a ~1/s |

> Cada tick trae **precio, cantidad y timestamp**. Las conexiones se reconectan solas (3 s entre intentos, *read-deadline* de 70 s) y validan la coherencia del libro (`coherentBook`: lados positivos, no cruzado, spread interno < 5 %) *antes* de alimentar la detección.

**Optimizaciones de tiempo real (verificables en el código):**
- **Canal con búfer (1 000 eventos)** entre ingesta y procesamiento → *zero-sampling-loss* en picos de volatilidad.
- **Media móvil del Spike Filter O(1)** sobre ventana fija de 20 muestras, protegida con mutex.
- **Una goroutine por sesión** para la ejecución; un solo Bellman-Ford por barrido del radar (el snapshot por sesión solo superpone saldos).
- **Todo lo que toca disco es *write-behind*** (ledger y fotografías de sesión): persistir nunca frena la siguiente operación.

---

## 🎯 Precisión del cálculo de rentabilidad neta

> **¿Consideras los fees, el slippage y el riesgo de ejecución antes de decidir? ¿Evitas operaciones rentables en bruto pero negativas en neto?**

Una única fórmula gobierna TODAS las decisiones (detección de referencia, ejecución del par, ciclos del radar, simulador y benchmark) — `computeNetProfit` en engine.go, con tests que verifican equivalencia exacta con la forma institucional:

```
Neto = (P_venta × V × (1 − fee_venta)) − (P_compra × V × (1 + fee_compra)) − Slippage
```

- **Fees por exchange, en las dos piernas, personalizables:** la pierna de **compra** encarece el costo y la de **venta** reduce el ingreso, tal como cobra cada exchange. En ciclos de N piernas, cada una descuenta su fee.
- **Slippage estimado:** `5 bps por pierna` por defecto, **configurable por el usuario**. Se descuenta antes de decidir — incluso en la proyección que dispara la solicitud de préstamo.
- **Volumen dimensionado contra liquidez real:** `min(tope del usuario, cantidad del top-of-book en cada pierna)`. En ciclos, la restricción de cada pierna se mapea matemáticamente a unidades del nodo de inicio.
- **Umbral de viabilidad personal:** cada usuario define su margen; el motor clasifica `[EN ESPERA] (Inviable)` todo lo que no lo supere, evitando el *fee bleeding*.
- **Cálculo auditable, no caja negra:** cada evaluación se emite al feed con su desglose `Bruto | Fees | Slippage | Neto`; cada ciclo ejecutado registra su ruta completa en el ledger.
- **P&L limpio:** la base (`InitialWealth`) se mueve junto con depósitos/retiros — agregar o quitar capital no distorsiona el rendimiento.

---

## 🛡️ Robustez y gestión de riesgo (circuit breakers)

> **¿Cómo manejas baja liquidez, órdenes parciales y movimientos bruscos? ¿Hay circuit breaker?**

- **Detección de feed congelado (staleness):** si un libro lleva **>10 s** sin publicar, es **dato muerto**: la evaluación del par se pausa (`[FEED CONGELADO]`) y sus aristas salen de la búsqueda de ciclos. El motor prefiere no operar a operar contra precios fantasma.
- **Spike Filter (circuit breaker de precio), POR LIBRO:** un tick cuya variación supere el **5 %** respecto al anterior se descarta como corrupto. Además, el spread se mide contra su media móvil: factor **>15×** → `[SPIKE ALERTA]` (opera con aviso); **>50×** → `[SPIKE BLOQUEADO]` (probable error de API → se rechaza).
- **Doble compuerta de cordura:** divergencia entre exchanges >20 % → se aborta antes de tocar saldos.
- **Fill-or-Kill atómico (5 % simulado):** tanto en el par como en cada ciclo, el fallo de orden se evalúa **antes** de mover saldos — abortar es atómico, cero exposición direccional, y la sesión pausa 2 s para no martillar un libro roto.
- **Doble *hard block* de fondos:** el saldo se valida al planificar y **otra vez** bajo el lock justo antes del commit; si el mundo cambió en ese microinstante, la operación se rechaza. El motor jamás permite saldos negativos (clamp de *dust* de redondeo en `Balances.Add`).
- **Ritmo anti-*overtrading*:** cooldown de **3 s** por sesión + flag `IsExecuting` que serializa (cierra la ventana TOCTOU de doble ejecución en el mismo tick).
- **Apalancamiento disciplinado:** nunca se pide un préstamo que no cubra `costo × RiskMultiplier`; el crédito agotado pausa hasta el vencimiento y se devuelve solo.
- **Validación de entradas SIEMPRE en backend:** capital inicial, inyecciones del simulador, parámetros de estrategia y universo — todo se sanea contra NaN/Inf, rangos y registros conocidos, aunque el frontend ya valide.

---

## 💾 Persistencia y continuidad (sesiones completas + Trade Ledger)

Arus persiste en SQLite **la sesión completa de cada usuario**, no solo sus trades: el demo dejó de ser volátil.

- **Continuidad real:** saldos **multi-activo**, parámetros de estrategia (incluidos universo y autopiloto), base del PnL y preferencias sobreviven a reinicios del motor **y** del navegador. El token de sesión (UUID v4 no enumerable) vive en `localStorage`: al volver, `resume_session` recupera todo — sin login, sin fricción. Verificado E2E: *kill* del motor → restart → resume → saldos al centavo.
- **Esquema multi-activo end-to-end:** la tabla `balances` es `(session_id, venue, asset, amount)` y el tipo en memoria (`Balances`) tiene la misma forma — el ETH que deja un ciclo triangular se persiste y restaura igual que el BTC.
- **Patrón repositorio:** el motor habla con la interfaz `SessionStore`, no con SQLite. Migrar a Postgres (si algún día hay múltiples instancias del motor o cuentas con login) es escribir otro driver, cero cambios de lógica.
- **Reconexión sin pérdidas:** un parpadeo de red reanuda la sesión persistida (antes ¡re-inicializaba y borraba el progreso!). Si otra pestaña reclama el mismo token, la vieja se desconecta limpiamente (takeover identity-aware en el Hub).
- **Sin préstamos fantasma:** la fotografía persiste solo fondos PROPIOS (préstamo activo excluido).
- **Write-behind siempre:** cada mutación de saldos dispara la fotografía asíncrona (el punto de paso es `sendWalletUpdate`); si el disco falla, el motor sigue operando en memoria.

El **Trade Ledger** sigue siendo el registro de auditoría **inmutable**: cada operación (del par o ciclo completo con su ruta) con timestamp, volumen, **fricción pagada** (`fees_usd`, fees + slippage — Sprint D, con migración automática de bases anteriores), neto y flag de préstamo/reequilibrio. Y desde el Sprint D, el ledger alimenta la **analítica por sesión**:

| Endpoint | Qué devuelve |
|---|---|
| `GET /api/ledger?session_id=<uuid>` | Últimos 100 registros de TU sesión |
| `GET /api/stats?session_id=<uuid>` | P&L acumulado en serie temporal, win rate, ops/hora, fricción total, volumen |
| `GET /api/ledger.csv?session_id=<uuid>` | Historial completo como CSV descargable |

Los tres comparten la misma política de privacidad: el `session_id` (UUID v4 no enumerable) es el token de acceso; sin él la respuesta es vacía.

---

## 🏗️ Arquitectura y stack tecnológico

> **¿El sistema está bien estructurado, es mantenible y escalable? ¿El código es legible y sigue buenas prácticas?**

Separación de responsabilidades por archivo y **pipeline orientado a eventos**: adaptadores de ingesta → canal con búfer → bucle de detección (par + radar) → ejecución por sesión → persistencia write-behind.

```mermaid
flowchart LR
    subgraph EXT["Mercados externos"]
        BIN["Binance · 5 libros<br/>streams combinados"]
        BIT["Bitso · 1 libro<br/>canal orders"]
        KRK["Kraken · 3 libros<br/>ticker v2 (bbo)"]
    end

    subgraph ENGINE["Motor · Go"]
        WS["FeedAdapters<br/>(reconexión + coherentBook<br/>precio + cantidad + timestamp)"]
        CH["Canal con búfer<br/>1000 eventos"]
        EVAL["Detección del PAR<br/>O(1) · Spike Filter · staleness"]
        GRAPH["RADAR: grafo de liquidez<br/>Bellman-Ford ~1/s<br/>global + por usuario (fees/universo)"]
        EXEC["Ejecutores por sesión<br/>clásico (par) ⊕ autopiloto (ciclos)"]
        DB[("SQLite · WAL<br/>sessions + balances + ledger")]
    end

    subgraph WEB["Web · Next.js (Radar-first)"]
        RADAR["Vista RADAR (principal)<br/>RadarView: lienzo + card + tooltip + dinamismo"]
        UI["Vista DASHBOARD<br/>P&L · wallets · feed"]
        STRAT["StrategyDrawer sobre el grafo<br/>(params + universo + autopiloto + préstamo auto)"]
        AUDIT["Historial / Auditoría"]
        ANLT["Analítica<br/>(P&L · win rate · CSV)"]
    end

    BIN & BIT & KRK -->|WebSocket push| WS --> CH --> EVAL --> EXEC
    CH --> GRAPH --> EXEC
    EXEC -->|WebSocket push| UI & RADAR
    STRAT -->|set_params| ENGINE
    EXEC -.->|write-behind| DB
    AUDIT -->|GET /api/ledger?session_id| DB
    ANLT -->|GET /api/stats · /api/ledger.csv| DB
```

**¿Por qué este stack?**

| Capa | Tecnología | Justificación |
|---|---|---|
| Motor | **Go 1.26** | Concurrencia nativa (goroutines + channels) para un bucle de eventos sin bloqueos; binarios estáticos triviales de desplegar. |
| Comunicación | **WebSocket** (`gorilla/websocket`) | Tiempo real en ambos extremos, sin polling. |
| Persistencia | **SQLite** (`modernc.org/sqlite`) | Driver **CGO-free**: sesiones completas + ledger sin Postgres ni contenedores; detrás de la interfaz `SessionStore` para migrar sin reescribir. |
| Frontend | **Next.js 16 / React 19** | App Router, SSR y DX moderna. |
| Estilos | **Tailwind CSS 4** + `lucide-react` | UI consistente, responsive, modo oscuro. |
| Tipado | **TypeScript** | Contratos motor↔UI espejados (interfaces de `useArusEngine.ts`). |
| CI | **GitHub Actions** | `go vet` + `go test -race` + `vitest` (layout del radar) + builds en cada push (el `-race` no corre en la máquina de desarrollo). |

**Organización del repositorio:**

```
Arus/
├─ .github/workflows/ci.yml   # CI: vet + test -race + build (motor y web)
├─ apps/
│  ├─ engine/                 # Motor en Go — capas separadas por archivo
│  │  ├─ main.go              # Composition root: canal, Hub, motor+grafo, rutas /ws y /api/ledger
│  │  ├─ venues.go            # REGISTRO (datos): Venues, Instruments (N libros/venue), classicPair, parityPairs, catálogos
│  │  ├─ feed.go              # CONTRATO de ingesta: interface FeedAdapter
│  │  ├─ ws_real_market.go    # ADAPTADORES: Binance (streams combinados), Bitso y Kraken (ticker v2); LiveMarket por instrumento
│  │  ├─ engine.go            # DOMINIO: computeNetProfit, bucle Start (par + emisión radar), ejecutor clásico, crédito/reequilibrio
│  │  ├─ graph.go             # RADAR: LiquidityGraph, Bellman-Ford (global y por usuario), snapshot para la UI
│  │  ├─ cycle.go             # EJECUTOR omnidireccional: planCycle (rotaciones cash, puro) + commitCycle (atómico) + autopiloto + crédito de ciclos
│  │  ├─ server.go            # TRANSPORTE: wsHandler (init/resume/set_params/…), sanitizadores, ledger HTTP
│  │  ├─ store.go             # PERSISTENCIA de sesiones: interfaz SessionStore + SQLite (sessions/balances)
│  │  ├─ ledger.go            # PERSISTENCIA del ledger: esquema + migración aditiva, write-behind, consultas por sesión
│  │  ├─ analytics.go         # ANALÍTICA: agregados del ledger (/api/stats) + export CSV (/api/ledger.csv)
│  │  ├─ models.go            # ESTADO y WIRE: Balances multi-activo, TradingParameters, ClientSession, Hub, eventos
│  │  └─ *_test.go            # 50+ tests: fórmula, grafo, ciclos, crédito, store, analítica, clamps, concurrencia + benchmark
│  └─ web/src/
│     ├─ app/page.tsx         # Contenedor: vistas RADAR | DASHBOARD, modales (crédito/fondos/simulador/guía)
│     ├─ components/
│     │   ├─ RadarView.tsx    # LA PANTALLA PRINCIPAL: lienzo SVG, tooltip, card de nodo, dinamismo (memoizado)
│     │   ├─ HeaderBar.tsx    # Header compacto: patrimonio animado, tabs, Probar el bot, Estrategia
│     │   ├─ StrategyDrawer.tsx  # Drawer sobre el grafo (usa StrategyPanel `embedded` + préstamo automático)
│     │   ├─ StrategyPanel.tsx   # Formulario de estrategia (modo tarjeta clásico y modo embedded)
│     │   ├─ AnalyticsPanel.tsx  # P&L acumulado + win rate + CSV (vista Dashboard)
│     │   ├─ LedgerPanel.tsx     # Historial/Auditoría (vista Dashboard)
│     │   ├─ GraphPanel.tsx      # Radar ANTERIOR (ya no montado; referencia hasta borrar)
│     │   └─ OnboardingModal · TutorialModal
│     ├─ hooks/useArusEngine.ts  # Única fuente de verdad: WebSocket + resume + reducer de eventos
│     └─ lib/
│         ├─ radarLayout.ts   # LAYOUT PARAMÉTRICO del radar (1..N venues, fan-out de aristas) — 13 tests
│         └─ config.ts        # Endpoints del motor por variable de entorno
├─ docs/
│  ├─ FASE2-GRAFO.md          # Diseño del motor omnidireccional + estado por hitos
│  ├─ REDISENO-RADAR.md       # Plan del rediseño Radar-first (implementado) + notas de rendimiento
│  └─ mockups/arus-radar-mockup.html  # Mockup interactivo aprobado (abrir en el navegador)
└─ README.md
```

**Principios de diseño y convenciones:**

- **Separación por capas, no por conveniencia.** El dominio no sabe de HTTP; el transporte no toma decisiones de negocio; la persistencia es auxiliar y su fallo **nunca** detiene el trading.
- **Los mercados son datos.** Exchanges, instrumentos y paridades viven en registros (`venues.go`); agregar un venue/par no toca la lógica — el grafo, los feeds y las wallets se adaptan solos.
- **Multi-tenant desde el diseño.** El bucle de detección evalúa el mercado una vez por tick; la ejecución es por sesión en goroutines independientes, cada una con sus `Balances` y su estrategia.
- **Disciplina de concurrencia explícita.** Estado de sesión bajo `session.Mu`; sockets serializados con `ConnMu`; parámetros como snapshot atómico (`atomic.Pointer` — lectores sin lock, sin estados a medias); grafo bajo su propio mutex; verificado con `-race` en CI.
- **Funciones puras donde importa.** `computeNetProfit`, `planCycle`, `sanitizeTradingParams`, `findNegativeCycle` — la matemática financiera es testeable sin red, sin reloj y sin aleatoriedad.
- **Contratos tipados de extremo a extremo** y **comentarios que explican el porqué** (gotchas reales documentados en el código: el JSON case-insensitive de Binance, el `SetMaxOpenConns(1)` de SQLite, el happens-before de los params).
- **Observabilidad por tags:** `[OPORTUNIDAD]`, `[ARBITRAJE]`, `[RADAR]`, `[CICLO EJECUTADO]`, `[SPIKE BLOQUEADO]`, `[FEED CONGELADO]`, `[CRÉDITO ACTIVADO]`… — los mismos logs narran al usuario y sirven para depurar.

---

## 🖥️ Interfaz y experiencia de usuario

Web app **Radar-first** (rediseño completo, plan y mockup en [`docs/REDISENO-RADAR.md`](docs/REDISENO-RADAR.md)) pensada para que **cualquiera** entienda lo que ocurre. La filosofía: *la complejidad por dentro* — cada dato aparece en el nivel de interacción donde se necesita.

**Vista RADAR (pantalla principal — aterriza aquí tras el onboarding):**

- **El grafo de liquidez a pantalla completa:** una columna por exchange, tus monedas como nodos (activo + saldo + valor), las conversiones posibles como líneas mudas. Los supuestos del modelo (paridad USDT≈USD, inventario pre-fondeado) aparecen al hover de sus aristas punteadas.
- **Hover en una arista** → tooltip con compra/venta, TU fee + slippage y la liquidez visible del libro.
- **Click en un nodo** → card de detalle (flotante en desktop, hoja inferior en móvil): saldo, precio, los libros que tocan ese nodo, frescura del feed, y **Editar fondos** de ese exchange (funciona también para venues fuera del par clásico, como Kraken).
- **Dinamismo:** pulso en cada nodo cuyo libro recibió tick; el ciclo rentable se ilumina en verde con **partículas recorriendo la ruta del dinero**; al ejecutarse, un `+$X` flota desde el nodo de origen y el patrimonio del header **cuenta hacia arriba**; un spike bloqueado tiñe la narración de rojo; sin ciclo, un barrido de sonar dice "sigo buscando". Respeta `prefers-reduced-motion`.
- **Narración de 1 línea + ticker** al pie: qué ve el radar ahora mismo y la última operación.
- **La poda del universo se VE:** los exchanges/monedas que saques de tu universo (drawer de Estrategia) se desvanecen del lienzo.

**Header (ambas vistas):** patrimonio con contador animado + PnL, tabs `RADAR | DASHBOARD`, **⚡ Probar el bot** (el simulador es un pilar: es como un juez evalúa el sistema — inyectar escenarios y VER al radar reaccionar), **⚙ Estrategia**, tutorial, modo oscuro y reset.

**Drawer de Estrategia (se abre SOBRE el grafo):** margen mínimo, orden máxima, slippage, comisiones por exchange, multiplicador de riesgo, **préstamo automático**, **tu universo** (chips) y el **toggle del autopiloto**. Lo que ves tras aplicar es lo que el backend dejó vigente.

**Vista DASHBOARD (todo lo demás):**

- **P&L acumulado en tiempo real**, precios en vivo con ping, feed de operaciones (ruta compra→venta), salud de inventario por exchange y distribución del capital (wallets del par clásico).
- **Panel de Historial / Auditoría:** el ledger persistido de TU sesión.
- **Panel de Analítica / Rendimiento (Sprint D):** curva de P&L acumulado con hover, win rate, ritmo, fricción total pagada (fees + slippage) y volumen — todo desde el ledger — más **export CSV**.

**Transversal:** continuidad sin fricción ("Recuperando tu sesión…" con token en `localStorage`), **modo de pruebas** con 3 escenarios (oportunidad normal / evento extremo / precio falso), **decisión asistida sin fondos** (ganancia posible vs costo del crédito × tu riesgo), tutorial guiado de 8 pasos, configuración inicial guiada, banners de crédito/reequilibrio con cuenta regresiva, modo oscuro y responsive.

---

## 🚀 Instalación y ejecución local

> Objetivo: sistema funcional en **menos de 2 minutos**. Sin Docker ni base de datos externa — SQLite embebido se auto-crea al arrancar.

### Prerrequisitos

| Software | Versión | Verificar |
|---|---|---|
| **Go** | 1.26+ | `go version` |
| **Node.js** | 18+ (WebSocket global para smokes: 21+) | `node -v` |
| **npm** | 9+ | `npm -v` |

### 1) Motor (backend en Go)

```bash
cd apps/engine
go run .
# Servidor en :8080 — crea data/ledger.db con las tablas de sesiones y ledger
```

### 2) Dashboard (frontend en Next.js)

```bash
cd apps/web
npm install
npm run dev
# Abre http://localhost:3000
```

### 3) Probar la continuidad (opcional)

1. Configura capital, cambia la estrategia y genera trades (**Probar el bot**).
2. Mata el motor (`Ctrl+C`), reinícialo (`go run .`) y recarga el navegador: **"Recuperando tu sesión…" → todo sigue ahí** (saldos, estrategia, historial).

### Tests y benchmark

```bash
cd apps/engine
go test ./...                                  # 50+ tests unitarios del motor
go test -bench=Detection -benchmem -run=^$     # benchmark del hot path
# go test -race corre en el CI (requiere gcc de 64 bits, ausente en la máquina de desarrollo)

cd ../web
npm test                                       # 13 tests del layout paramétrico del radar (vitest)
```

---

## ☁️ Despliegue

Arquitectura de dos servicios, ambos en tiers gratuitos (la infraestructura del proyecto es **Fly.io + Vercel** — decisión fija):

| Componente | Plataforma | Por qué |
|---|---|---|
| **Frontend** (Next.js) | **Vercel** | Soporte nativo de Next.js, CI/CD desde Git, HTTPS automático. |
| **Motor** (Go + SQLite) | **Fly.io** | Proceso de larga vida ideal para WebSocket, TLS (`wss://`) y **volumen persistente** para `data/` (sesiones + ledger sobreviven deploys). |

El motor se despliega con un `Dockerfile` multi-etapa que compila un binario **estático** (`CGO_ENABLED=0`, gracias al SQLite en Go puro) sobre `distroless/static` (~5 MB). Config en [`apps/engine/fly.toml`](apps/engine/fly.toml). El esquema de base de datos **se auto-crea/migra al arrancar** — no hay pasos manuales de migración.

### 1) Motor en Fly.io

```bash
flyctl auth login
cd apps/engine
flyctl apps create arus-engine                              # nombre único global
flyctl volumes create arus_data --size 1 --region dfw --yes # volumen de data/
flyctl deploy
```

Verifica: `https://<tu-app>.fly.dev/api/ledger` → debe devolver `[]`, y `https://<tu-app>.fly.dev/api/stats` → el resumen vacío (`{"total_ops":0,…}`).

### 2) Frontend en Vercel

1. *Add New → Project* → importar el repo. **Root Directory → `apps/web`** (crítico: monorepo).
2. Variables de entorno (ver [`apps/web/.env.example`](apps/web/.env.example)):
   ```
   NEXT_PUBLIC_ENGINE_WS_URL=wss://arus-engine.fly.dev/ws
   NEXT_PUBLIC_ENGINE_HTTP_URL=https://arus-engine.fly.dev
   ```
3. **Deploy**.

> ⚠️ **Pendiente de esta rama:** el deploy actual corre `main`. Al desplegar esta rama, validar end-to-end en producción: panel de estrategia, radar, autopiloto y `resume_session` (el volumen conserva la base; el esquema nuevo se crea solo).

---

## 🧭 Qué sigue — plan de evolución

> Los sprints A, B, **C y D** y el **rediseño Radar-first** ya están hechos (ver [bitácora](#-estado-de-la-rama-bitácora)). Queda el cierre:

### Cierre de la rama

1. **Desplegar esta rama** (Fly + Vercel) y validar end-to-end en producción: vista Radar con 3 venues, drawer de estrategia, autopiloto, `resume_session`, `/api/stats` y el export CSV. El esquema nuevo (columna `fees_usd`) se migra solo al arrancar — verificado contra una base existente. **Bloqueado por:** `flyctl auth login` es interactivo (lo corre el dueño); Vercel se maneja desde su dashboard (cambiar la production branch o hacer merge a `main`).
2. **Actualizar las capturas del README** (checklist detallado en la [sección de capturas](#-capturas-de-pantalla)) — las toma el dueño con la app corriendo en local.
3. **Sesión de revisión profunda:** correr la revisión adversarial multi-agente sobre la rama completa (los intentos previos murieron por límites de tokens del plan — el dueño la pedirá EXPLÍCITAMENTE en una sesión dedicada; no lanzarla sin que la pida). Buen candidato a limpiar en esa sesión: `GraphPanel.tsx` (el radar viejo, ya sin montar).

### Backlog (diseño listo, sin fecha)

- **Slippage por profundidad real** (streams `depth`): el `SlippageRate` del usuario pasa de estimación a **tolerancia máxima**.
- **Liquidez compartida entre sesiones** (la oportunidad se la lleva quien llega primero — decisión de producto pendiente del dueño).
- **Préstamo dimensionado a la oportunidad** (hoy la línea es fija $50k + 1 BTC).
- **Puente a ejecución real:** interface `ExchangeAdapter` (libro/órdenes/balances) con implementación simulada actual + Binance **Testnet** — el paso de demo a sistema real.
- **Postgres** solo si aparecen múltiples instancias del motor o cuentas con login (la interfaz `SessionStore` ya lo permite sin reescribir).
- Wire multi-venue completo en la UI de wallets clásicas (hoy leen el plano 2-venue; el radar ya usa `balances`).
- **Validar el modo claro del radar** con el dueño: hoy el lienzo está comprometido al oscuro (decisión de diseño tipo terminal); si no convence en modo claro, tematizarlo.
- Tooltip de arista con la **razón de inviabilidad** cuando no hay ciclo (spread actual vs fees) — hace visible la honestidad del modelo (quedó fuera de R3 por alcance).

---

## 📸 Capturas de pantalla

> ### ⚠️ PENDIENTE: ACTUALIZAR LAS CAPTURAS (las toma el dueño)
> Las capturas siguientes corresponden a la **versión anterior** (rama `main`, pre-rediseño): siguen siendo útiles para los flujos que no cambiaron (crédito, onboarding, tutorial, fondos, simulador), pero **ya no reflejan la pantalla principal**. Checklist de capturas nuevas (app corriendo en local, modo oscuro):
>
> 1. **Vista RADAR completa** — las 3 columnas (Binance/Bitso/Kraken) con precios vivos, header con tabs y patrimonio. *La captura estrella.*
> 2. **Card de nodo abierta** (click en BTC@Bitso, por ejemplo): libros, frescura, Editar fondos.
> 3. **Tooltip de arista** (hover sobre un libro): compra/venta + fee + liquidez.
> 4. **Drawer de Estrategia abierto sobre el grafo**, idealmente con un venue podado (atenuado en el lienzo detrás).
> 5. **El ciclo en acción**: inyectar "Oportunidad normal" desde ⚡ Probar el bot y capturar el grafo con la ruta en verde + partículas (y si se puede, el `+$X` flotando).
> 6. **Spike bloqueado**: inyectar "Precio falso" y capturar la narración en rojo.
> 7. **Vista DASHBOARD** (tab): KPIs + wallets + feed.
> 8. **Panel de Analítica** abierto con la curva de P&L y los tiles.
> 9. **"Recuperando tu sesión…"** (recargar la página con sesión activa).
>
> Al reemplazar: mantener los nombres descriptivos en `assets/` y actualizar los `<img>`/rutas de abajo.

### Panel principal en tiempo real

El dashboard reúne el P&L acumulado (dinero total, ganancia neta y rendimiento), la salud de inventario por exchange y el feed de operaciones en vivo, con precios de Binance/Bitso e indicador de *ping*.

![Dashboard de Arus en modo oscuro](assets/Pantalla_inicial_modo_oscuro.png)

<sub>Modo claro disponible con un clic — diseño responsive y *dark mode* nativo:</sub>

![Dashboard de Arus en modo claro](assets/Pantalla_inicial_modo_luminoso.png)

### ⭐ Crédito vs. tiempo muerto

Con la línea de crédito activa, el bot **sigue operando** (banner superior con cuenta regresiva) mientras el inventario se reequilibra en segundo plano; cada wallet muestra cuánto capital es propio y cuánto prestado.

![Préstamo activo: el bot opera con fondos prestados mientras se reequilibra](assets/Prestamo_activo_operando_con_fondos_prestados.png)

Cuando un exchange se queda sin saldo y el préstamo automático está apagado, el bot **cede la decisión al usuario** con los números sobre la mesa.

<p align="center">
  <img src="assets/Interfaz_Bot_en_pausa_por_fondos_insuficientes.png" alt="Diálogo de decisión al quedarse sin fondos" width="60%">
</p>

### Onboarding y tutorial guiado

<table>
<tr>
<td width="50%" valign="top"><img src="assets/Configuracion_Inicial.png" alt="Configuración inicial del capital"><br><sub>Configuración inicial: el usuario elige capital de arranque (mín. $1 000 y 0.1 BTC) y ve cómo se repartirá 50/50.</sub></td>
<td width="50%" valign="top"><img src="assets/Tutorial.png" alt="Tutorial guiado de 8 pasos"><br><sub>Tutorial guiado de 8 pasos en lenguaje sencillo, con chip de ubicación de cada elemento.</sub></td>
</tr>
</table>

### Gestión de fondos y modo de pruebas

<table>
<tr>
<td width="42%" valign="top"><img src="assets/Editar_fondos.png" alt="Modal de depósito y retiro de fondos"><br><sub>Depósito/retiro por exchange (USD o BTC): ajusta la base del P&L para que no cuente como ganancia ni pérdida.</sub></td>
<td width="58%" valign="top"><img src="assets/Modo_de_pruebas_del_bot.png" alt="Modo de pruebas / simulador"><br><sub>Simulador: inyecta escenarios (oportunidad normal, evento extremo, precio falso) para ver al Spike Filter y a la lógica de crédito en acción.</sub></td>
</tr>
</table>

---

## 📄 Licencia

Este proyecto se distribuye bajo la **licencia MIT** — eres libre de usar, copiar, modificar y distribuir el código, citando la autoría. El texto completo está en el archivo [`LICENSE`](LICENSE).

---

<div align="center">

**Arus** · CODING_CHALLENGE_MEXICO · Daniel Peredo Borgonio · Licencia MIT

🚧 *Rama `feat/arbitraje-omnidireccional` — trabajo en curso; ver [Qué sigue](#-qué-sigue--plan-de-evolución)*

</div>
