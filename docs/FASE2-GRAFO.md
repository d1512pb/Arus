# Fase 2 — Motor de arbitraje omnidireccional (grafo de liquidez)

> **Estado: HITOS 1, 2 Y 3 IMPLEMENTADOS.** El grafo de liquidez
> (`apps/engine/graph.go`) nace del registro de INSTRUMENTOS, se actualiza con
> cada tick real y detecta ciclos negativos (espaciales y triangulares) con
> Bellman-Ford. Desde el **hito 3** el radar además EJECUTA: wallets
> **multi-activo en memoria** (`Balances`, mismo esquema que persiste el store),
> ejecutor de ciclos (`cycle.go`: planificación pura + commit atómico con
> Fill-or-Kill antes de tocar saldos), **Universe por sesión** (poda del grafo a
> los venues/monedas del usuario), **fees del usuario en los pesos**
> (`FindBestCycleFor`) y el **autopiloto opt-in** (`RadarAutopilot`) que
> sustituye al ejecutor clásico del par cuando está activo.

## 1. Idea central

El mercado se modela como un **grafo dirigido**:

| Concepto | En el grafo |
|---|---|
| Un activo en un venue (`BTC@Binance`, `USD@Bitso`) | **Nodo** (`MarketNode`) |
| Comprar/vender en un libro de órdenes | **Arista** `EdgeOrderBook` (tasa = top-of-book, fee = taker del usuario) |
| Transferir un activo entre venues | **Arista** `EdgeTransfer` (fee retiro + fee de red + ~30 min de latencia) |
| Oportunidad de arbitraje | **Ciclo de peso negativo** con `w = −log(tasa × (1 − fee))` |

Un ciclo que sale de `USD@Bitso` y regresa con más de lo que salió —después de todos
los fees— tiene suma de pesos `< 0`. Detectarlo es **Bellman-Ford/SPFA**, el mismo
formalismo que usan los desks institucionales de arbitraje cross-venue.

**Lo que ya tenemos es un caso particular:** Binance↔Bitso con BTC es un ciclo de
2 aristas. El motor de grafos no reemplaza el producto actual: lo generaliza.

## 2. Por qué este diseño cumple la visión del producto

- **El usuario elige con qué jugar.** Su `Universe` (venues + activos habilitados)
  **poda el grafo**: la búsqueda corre solo sobre su subgrafo, con sus fees, su
  `MaxOrderSizeBTC`, su `RiskMultiplier`. La personalización de la Fase 0 se vuelve
  literalmente la forma del espacio de búsqueda.
- **Expansión sin reescrituras.** Agregar exchange/moneda = 1 entrada en el registro
  (`venues.go`) + 1 `FeedAdapter` (`feed.go`). El detector no cambia.
- **Corrige el basis USDT/USD por diseño.** Hoy `AssumeUSDTParity` documenta la
  simplificación; en el grafo USDT y USD son nodos distintos y el basis es una arista
  con precio real.
- **El costo de rebalancear se vuelve honesto.** `EdgeTransfer` carga fee de retiro,
  fee de red y latencia: la decisión crédito-vs-reequilibrio de la Fase 0 pasa a
  comparar contra el costo REAL de mover inventario.

## 2.5 Qué hay implementado hoy (modo radar multi-instrumento)

- **Instrumentos como datos:** `Instruments` en venues.go registra N libros por
  venue; agregar un par = 1 entrada (el FeedAdapter se suscribe solo, el grafo
  gana nodos/aristas, el radar lo cubre). Binance corre 3 libros por un único
  socket de streams combinados; Bitso 1.
- **Topología desde instrumentos:** 1 nodo por (venue, activo); 2 aristas de
  libro por instrumento; paridad entre activos declarados equivalentes
  (USDT≈USD **visible y etiquetada**) y swap de inventario pre-fondeado entre el
  mismo activo cross-venue (tasa 1, costo 0 en demo).
- **Actualización O(1) por tick:** cada tick refresca las 2 aristas de su libro
  (tasa, fee efectivo = taker + slippage de referencia, liquidez en unidades del
  base, peso `−log(tasa·(1−fee))` precalculado). Spike Filter y staleness POR
  LIBRO (no por venue).
- **Detección automática:** Bellman-Ford con fuente virtual cada barrido (~1/s)
  encuentra ciclos espaciales (2 libros + swap + paridad) **y triangulares**
  (3 libros dentro de Binance) con el mismo algoritmo; libros congelados (>10 s)
  excluidos. Un ciclo nuevo se anuncia una sola vez en el feed (`[RADAR] …`).
- **Snapshot por sesión (`graph_update`):** el grafo global + los saldos del
  usuario en cada nodo (clasificados cash/crypto, con precio USD real); los
  activos sin wallet respaldada (ETH) aparecen con saldo 0 y precio real.
- **Volumen de ciclo:** homogéneo (min de liquidez) solo si todas las piernas de
  libro comparten activo base; en triangulares (bases mixtas) se reporta "no
  homogéneo" hasta el ejecutor del hito 3.
- **Hito 3 (hecho):** ejecución de ciclos vía autopiloto opt-in — el plan rota
  el ciclo a un inicio CASH, dimensiona contra saldo + tope del usuario +
  liquidez por pierna (mapeada a unidades de inicio) y solo ejecuta si el neto
  supera el margen DEL usuario; commit atómico bajo el lock de sesión con
  re-verificación de fondos (hard block) y Fill-or-Kill previo (cero exposición).
- **Aún NO:** crédito automático para ciclos (el shortfall de ciclos se omite en
  silencio, sin ofrecer préstamo), liquidez compartida entre sesiones, y el wire
  plano 2-venue convive con el campo `balances` multi-activo (la UI de wallets
  clásicas sigue leyendo el plano).

## 3. Primer hito demostrable (el más barato)

**Triangular intra-Binance con datos 100 % reales:** suscribir 3 streams del mismo
`FeedAdapter` de Binance ya probado — `BTC/USDT`, `ETH/USDT`, `ETH/BTC` — y detectar
ciclos de 3 aristas dentro de un solo venue. Cero exchanges nuevos, cero riesgo de
integración, y el caso Binance↔Bitso sigue operando como ciclo de 2 en paralelo.

Pasos:
1. `FeedAdapter` paramétrico por par (hoy Binance está fijado a `btcusdt@bookTicker`;
   generalizar a N suscripciones combinadas en un solo socket con streams combinados).
2. `PriceTick` ya normalizado (Fase 1) gana el campo `Pair` (base/quote) para poblar
   aristas de libros distintos.
3. Grafo global actualizado por tick: recalcular el `Weight` de las 2 aristas del
   libro afectado es O(1).
4. Búsqueda de ciclos: con grafos de este tamaño (≤ 20 nodos), Bellman-Ford completo
   por tick sigue siendo microsegundos; optimizar a SPFA incremental solo si el
   universo crece.
5. Ejecución: reutilizar el pipeline por sesión (cooldown, `IsExecuting`,
   Fill-or-Kill, hard block de fondos) aplicado a cada pierna del ciclo en orden.

## 4. Cambios de contrato pendientes (deuda consciente de la Fase 1)

- **Wire motor↔UI aún es 2-venue:** `ServerEvent` expone `binance_usd`, `bitso_usd`…
  como campos fijos. La Fase 2 necesita `wallets: {venue: {asset: amount}}` genérico
  y su migración en `useArusEngine.ts`. Se pospuso a propósito para no romper el
  dashboard desplegado durante la Fase 1.
- **El feed global de referencia** (`Start`) evalúa con parámetros default. Con el
  grafo, el "feed de mercado" pasa a narrar ciclos detectados (con su desglose de
  aristas) y la evaluación personalizada vive 100 % por sesión.
- **`pairView` desaparece:** `executeForSession` se convierte en `executeCycle`.
- **Slippage por profundidad:** con streams `depth` (no solo `bookTicker`), el
  slippage estimado configurable pasa a slippage CALCULADO, y `SlippageRate` del
  usuario se reinterpreta como tolerancia máxima (rechazar ciclo si excede).

## 5. Decisiones de producto abiertas

1. **Liquidez compartida entre sesiones** (¿la oportunidad se la lleva quien llega
   primero?): pool de liquidez por arista consumible entre tenants. Espectacular
   para demo multiusuario; complejidad media. Pendiente de decisión.
2. **Préstamo dimensionado a la oportunidad** (hoy: línea fija de $50k + 1 BTC).
   Con originación proporcional, la inecuación `neto > costo × riesgo` cobra vida
   en oportunidades chicas.
3. **Persistencia de sesión** (wallets sobreviven reinicios del motor): ya hay
   SQLite; falta decidir el modelo de identidad (session_id en localStorage vs
   cuentas).

## 6. Criterios para dar luz verde a la Fase 2

- [ ] Fase 1 desplegada y estable (Fly.io + Vercel) ≥ 1 semana sin regresiones.
- [ ] `go test -race ./...` verde en CI.
- [ ] Panel de estrategia usado end-to-end (set_params → PARAMS_UPDATED → trades
      con parámetros personalizados visibles en el ledger).
- [ ] Benchmark del hot path re-medido y README actualizado (el tracker ahora es
      thread-safe; el número anterior de ~21 ns/op debe re-verificarse).
