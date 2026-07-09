# Rediseño "Radar primero" — plan de implementación

> **Estado: IMPLEMENTADO (fases R0–R4).** El mockup de referencia vive en
> [`docs/mockups/arus-radar-mockup.html`](mockups/arus-radar-mockup.html). La
> implementación real: `lib/radarLayout.ts` (+13 tests), `HeaderBar.tsx`,
> `RadarView.tsx`, `StrategyDrawer.tsx` (con StrategyPanel `embedded`), y
> `page.tsx` dividido en vistas Radar/Dashboard. Verificado E2E con el motor y
> feeds reales. Todo fue **frontend puro**: el motor y su wire no se tocaron.
>
> Notas de implementación que difieren del plan (por rendimiento, descubiertas
> en verificación): el barrido del sonar es un overlay HTML (no SVG) para que
> el compositor lo anime sin repintar el lienzo; `RadarView` está memoizado y
> recibe solo el último log de spike (el feed completo re-renderizaba el canvas
> decenas de veces por segundo); el contador del patrimonio pinta SIEMPRE el
> valor real y anima por mutación imperativa encima (correcto por construcción).

## La tesis

El radar omnidireccional es el diferenciador del producto y hoy vive enterrado a
mitad del dashboard, gritando todos sus números a la vez. Se invierte la
jerarquía: **el grafo es la pantalla principal** y cada dato aparece en el nivel
de interacción donde se necesita (revelación progresiva):

| Nivel | Cuándo | Qué |
|---|---|---|
| 0 · siempre | vista base | columnas por venue, nodos con activo+saldo(+≈USD), aristas mudas, ciclo animado, narración de 1 línea |
| 1 · hover | arista | tooltip: dirección, precio, fee del usuario, liquidez |
| 2 · click | nodo | card flotante (desktop) / sheet (móvil): libros que lo tocan, frescura del feed, depositar/retirar |
| 3 · botón | drawer / vista | Estrategia (drawer sobre el grafo) · Dashboard (todo lo demás) |

Decisiones ya tomadas con el dueño:
- Tabs persistentes `RADAR | DASHBOARD` en el header; el onboarding aterriza en el Radar.
- **Probar el bot es pilar** (así evalúa un juez): vive en el header, disponible
  desde ambas vistas; inyectar y VER al radar reaccionar es la demo central.
- Card de nodo flotante en desktop, sheet inferior en móvil.
- Ticker de última operación al pie del Radar; feed completo en el Dashboard.
- Estética del mockup: fondo `#060a14`, acento emerald `#10d98e` (validado sobre
  ambas superficies), números en mono tabular, hues por activo (cash azul, BTC
  ámbar, ETH/SOL violeta, peligro `#ff5470`).

## Requisitos estructurales (observaciones del dueño, 2026-07-09)

### 1 · Layout paramétrico: 1, 3 o N exchanges sin romper nada

El universo del usuario puede tener **un solo exchange o más de tres**. Nada del
lienzo puede asumir "3 columnas":

- La geometría se **deriva del snapshot** (`graph.nodes`): venues presentes →
  columnas; activos por venue → filas. Cero constantes de topología.
- Ancho de lienzo `W = max(Wmin, nVenues × colW)`; con 1 venue la columna se
  centra y ensancha (los triángulos internos se siguen leyendo); con >4 venues,
  scroll horizontal suave con cabeceras de venue sticky.
- **Las animaciones nunca usan coordenadas duras**: pulsos, partículas y floats
  se calculan sobre los paths/nodos reales (`getPointAtLength`,
  `getBoundingClientRect`) y se recalculan con `ResizeObserver` — así el conteo
  de venues, el zoom o el resize no rompen nada.
- La poda del universo atenúa columna+nodos (clase por capa de venue, como en el
  mockup tras el fix de capas); si el registro del motor crece, el frontend no
  cambia.

### 2 · Ruteo de aristas: desahogar las intra-venue

Las aristas **entre venues** están bien. Las de **libros dentro de una columna**
(USDT→BTC→ETH→SOL apiladas) se amontonan pegadas al eje vertical:

- **Fan-out por salto**: cada libro intra-venue recibe un arco horizontal
  proporcional a la distancia entre sus nodos (adyacentes = arco corto; saltos
  largos tipo USDT→SOL = arco amplio hacia el borde de la caja), **alternando
  lado** izquierdo/derecho para repartir el ancho completo de la caja del venue
  — que para eso está.
- Las hit-areas anchas (16px) se conservan para hover cómodo.
- Criterio de listo: con Binance a 4 activos (5 libros internos), ninguna
  arista se superpone a otra ni cruza un nodo.

## Fases

### R0 — Motor de layout (base de todo)
`apps/web/src/lib/radarLayout.ts` — funciones puras y testeables:
`layoutColumns(nodes) → posiciones`, `routeEdge(edge, pos) → path d` (con el
fan-out), `canvasSize(nVenues)`. Tests unitarios con 1, 2, 3 y 5 venues
sintéticos (sin DOM). **Done:** tests en verde; ningún supuesto de "3 columnas".

### R1 — Navegación y estructura
- `page.tsx` se divide en `RadarView` (principal) y `DashboardView` (todo lo
  actual, intacto); conmutación por estado local — **sin** router nuevo, para no
  tocar el ciclo de vida del WebSocket de `useArusEngine`.
- `HeaderBar.tsx` nuevo: brand, estado, patrimonio+PnL, tabs, **⚡ Probar el
  bot** (el modal del simulador se eleva a nivel page para abrirse desde ambas
  vistas), **⚙ Estrategia**, modo oscuro.
- `StrategyPanel` → presentación **drawer** (mismos props y submit; cero lógica
  nueva).
**Done:** todas las funciones actuales alcanzables desde ambas vistas; smoke
manual de onboarding → radar → dashboard → volver.

### R2 — RadarCanvas (el grafo del mockup, con datos reales)
- Reescritura del SVG: capas (cajas → aristas → nodos), sin etiquetas
  permanentes; tooltip de arista; `NodeCard` flotante/sheet con libros del nodo
  (datos de `graph.edges`), frescura y accesos a Editar fondos.
- Narración de 1 línea + ticker de última operación al pie.
- Mismo `graph_update` de siempre: **cero cambios de wire**.
**Done:** paridad funcional con el mockup usando datos vivos; sin números
encimados a 1/3/N venues.

### R3 — Dinamismo
- **Pulso de tick:** diff de `rate/updated_at` entre snapshots consecutivos →
  pulso en los nodos cuyo libro cambió.
- **Ciclo:** `best_cycle` presente → highlight + partículas (rAF sobre paths
  reales); `arbitrage_executed` → float `+$X` desde el nodo de inicio + contador
  animado del patrimonio en el header.
- **Seguridad visible:** logs `spike_block`/`CIRCUIT BREAKER` → flash rojo de la
  arista implicada + narración; feed congelado → nodo desaturado + ❄ (además del
  chip).
- Rendimiento: animaciones por mutación directa de refs (sin re-render React por
  frame); pausa total con `document.hidden`; respeto a `prefers-reduced-motion`.
**Done:** demo del juez de punta a punta: Probar el bot → radar reacciona
(oportunidad ejecuta con flujo y cobro; precio falso rebota en rojo).

### R4 — Pulido y cierre
- Móvil (sheet, header envolvente), accesibilidad (nodos enfocables por teclado,
  roles/aria, foco visible).
- **Modo claro:** propuesta — el lienzo del radar conserva SIEMPRE el fondo
  profundo (es un terminal), y header/cards/drawer sí tematizan; a validar con
  el dueño con un vistazo antes de cerrar.
- Estado de carga del radar (skeleton hasta el primer `graph_update`).
- `npm run build`, smoke con preview, capturas nuevas del README y bitácora.

## Mejoras adicionales aceptadas ("si ves algo, mejóralo")

- Tooltip de arista con la **razón de inviabilidad** cuando no hay ciclo
  (spread actual vs fees) — hace visible la honestidad del modelo. (Stretch,
  puede caer a backlog si alarga R3.)
- Orden estable de nodos/venues en el snapshot para layout determinista.
- El mockup queda versionado en `docs/mockups/` como referencia de diseño.

## Qué NO cambia

Motor Go completo, wire de eventos, persistencia, endpoints, StrategyPanel
(lógica), simulador (lógica), Analítica/Ledger (viven en la vista Dashboard).
La revisión adversarial multi-agente sigue **diferida** a una sesión dedicada,
después de este rediseño.
