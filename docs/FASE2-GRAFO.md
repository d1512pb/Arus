# Fase 2 — Motor de arbitraje omnidireccional (grafo de liquidez)

> **Estado: RADAR IMPLEMENTADO (hito 1).** El grafo de liquidez vive en
> `apps/engine/graph.go`: se construye desde el registro de venues, se actualiza
> con cada tick real (O(1) por libro), detecta ciclos negativos con Bellman-Ford
> y se emite a la UI (~1/s) con los saldos de cada sesión superpuestos
> (`GraphPanel`). La **ejecución de ciclos** sigue a cargo del núcleo de dos
> venues probado — pasar la ejecución al grafo es el siguiente hito (sección 3).

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

## 2.5 Qué hay implementado hoy (modo radar)

- **Topología desde el registro:** 2 nodos por venue (base/quote), 2 aristas de
  libro por venue, paridad entre quotes (USDT≈USD **visible y etiquetada**) y
  swap de inventario pre-fondeado entre bases (tasa 1, costo 0 en demo).
- **Actualización O(1) por tick:** cada tick aceptado refresca las 2 aristas de su
  libro (tasa, fee efectivo = taker + slippage de referencia, liquidez, peso
  `−log(tasa·(1−fee))` precalculado).
- **Detección automática:** Bellman-Ford con fuente virtual cada barrido (~1/s);
  las aristas de libro congeladas (>10 s) se excluyen — mismo criterio de
  staleness de la Fase 1. Un ciclo nuevo se anuncia una sola vez en el feed
  (`[RADAR] Ciclo rentable detectado: USDT@Binance → … (+0.12 % neto)`).
- **Snapshot por sesión (`graph_update`):** el grafo global + los saldos del
  usuario en cada nodo — "tu dinero en cada exchange y por dónde puede fluir".
- **Aún NO:** ejecutar el ciclo detectado (radar ≠ gatillo), poda por Universe
  por sesión, y fees personalizados en los pesos (usa los de referencia y la UI
  lo declara).

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
