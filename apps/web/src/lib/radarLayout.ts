// radarLayout.ts — MOTOR DE LAYOUT DEL RADAR (rediseño Radar-first, fase R0).
//
// Funciones PURAS: reciben los nodos del snapshot y devuelven geometría. Nada
// aquí asume "3 exchanges": el universo del usuario puede tener 1 venue o más
// de 4 y el lienzo se deriva de los datos (requisito del dueño, ver
// docs/REDISENO-RADAR.md). Al ser puro es testeable sin DOM (vitest).

export interface LayoutNode {
  id: string;    // "BTC@Binance"
  venue: string;
  asset: string;
  kind: "cash" | "crypto";
}

export interface LayoutEdge {
  from: string;
  to: string;
  kind: string; // "book" | "parity" | "inventory" | "transfer"
}

export interface Pt {
  x: number;
  y: number;
}

export interface RadarLayout {
  w: number;
  h: number;
  colW: number;
  offsetX: number; // margen izquierdo de la primera columna (columnas centradas)
  venues: string[]; // orden estable de aparición
  pos: Map<string, Pt>;
}

export const NODE_R = 38;
// Ancho por columna acotado: con 1 venue la columna no se estira a todo el
// lienzo (queda centrada y legible); con muchos venues no se comprime a nada.
export const MIN_COL_W = 260;
export const MAX_COL_W = 420;
export const MIN_W = 860;
export const TOP_Y = 140;
// Caja SVG por venue (RadarView): mismos márgenes que el stack de nodos, para
// que círculo + etiqueta "1 BTC = $…" no se salgan por debajo del rect.
export const VENUE_BOX_TOP = 52;
export const VENUE_BOX_BOTTOM = 20;
export const VENUE_BOX_X_INSET = 18;
/** Distancia bajo el centro del nodo hasta el baseline de la etiqueta de precio. */
export const NODE_PRICE_BELOW = NODE_R + 16;
/** Aire bajo la etiqueta hasta el borde interior de la caja. */
export const NODE_LABEL_GAP = 14;
// Centro del último nodo: deja sitio a radio + etiqueta + aire + margen del lienzo.
export const BOTTOM_PAD = NODE_PRICE_BELOW + NODE_LABEL_GAP + VENUE_BOX_BOTTOM;
export const MIN_H = 520;
// Separación vertical máxima entre nodos de una columna (que una columna de 2
// nodos no los mande a los polos del lienzo).
export const MAX_ROW_GAP = 190;

// venueOrderOf: orden ESTABLE de venues por primera aparición en los nodos
// (el backend emite en orden de registro — determinista entre snapshots).
export function venueOrderOf(nodes: LayoutNode[]): string[] {
  const venues: string[] = [];
  for (const n of nodes) if (!venues.includes(n.venue)) venues.push(n.venue);
  return venues;
}

// canvasSize: el lienzo crece con los venues (ancho) y con la columna más
// poblada (alto). Todo lo demás se deriva de aquí.
export function canvasSize(venueCount: number, maxAssetsPerVenue: number): { w: number; h: number } {
  const n = Math.max(1, venueCount);
  const w = Math.max(MIN_W, n * 300);
  // 148px por activo extra: deja aire entre nodos y la etiqueta de precio
  // sin forzar al stack a pegarse a yBot en columnas densas.
  const h = Math.max(MIN_H, TOP_Y + BOTTOM_PAD + Math.max(0, maxAssetsPerVenue - 1) * 148);
  return { w, h };
}

/** Rect de la caja del venue i (coordenadas del viewBox del radar). */
export function venueBoxRect(
  i: number,
  layout: Pick<RadarLayout, "colW" | "offsetX" | "h">,
): { x: number; y: number; width: number; height: number } {
  const { colW, offsetX, h } = layout;
  return {
    x: offsetX + colW * i + VENUE_BOX_X_INSET,
    y: VENUE_BOX_TOP,
    width: colW - VENUE_BOX_X_INSET * 2,
    height: h - VENUE_BOX_TOP - VENUE_BOX_BOTTOM,
  };
}

// layoutRadar: una columna por venue (centradas como grupo), efectivo arriba y
// criptos debajo en orden estable. Funciona para 1..N venues y 1..M activos.
export function layoutRadar(nodes: LayoutNode[]): RadarLayout {
  const venues = venueOrderOf(nodes);
  const byVenue = new Map<string, LayoutNode[]>();
  for (const v of venues) {
    const inV = nodes
      .filter((n) => n.venue === v)
      .sort((a, b) => (a.kind === b.kind ? 0 : a.kind === "cash" ? -1 : 1));
    byVenue.set(v, inV);
  }
  const maxAssets = Math.max(1, ...venues.map((v) => byVenue.get(v)!.length));
  const { w, h } = canvasSize(venues.length, maxAssets);

  const colW = Math.min(MAX_COL_W, Math.max(MIN_COL_W, w / Math.max(1, venues.length)));
  const offsetX = Math.max(0, (w - colW * venues.length) / 2);

  const pos = new Map<string, Pt>();
  const yTop = TOP_Y;
  const yBot = h - BOTTOM_PAD;
  venues.forEach((v, c) => {
    const inV = byVenue.get(v)!;
    const x = offsetX + colW * c + colW / 2;
    const span = Math.min(yBot - yTop, Math.max(0, inV.length - 1) * MAX_ROW_GAP);
    const start = yTop + ((yBot - yTop) - span) / 2;
    inV.forEach((n, i) => {
      const y = inV.length === 1 ? (yTop + yBot) / 2 : start + (i * span) / (inV.length - 1);
      pos.set(n.id, { x, y });
    });
  });

  return { w, h, colW, offsetX, venues, pos };
}

// ---------------------------------------------------------------------------
// Ruteo de aristas
// ---------------------------------------------------------------------------

// pairKey: identificador NO dirigido de una pareja de nodos (una arista visual
// por libro/paridad/inventario aunque el wire traiga ambas direcciones).
export function pairKey(a: string, b: string): string {
  return a < b ? `${a}|${b}` : `${b}|${a}`;
}

export interface RoutedEdge {
  key: string; // pairKey
  from: string; // extremo "canónico" (el primero en orden de pairKey)
  to: string;
  kind: string;
  d: string; // path SVG
  mid: Pt;   // punto medio de la curva (anclaje de tooltips)
  intra: boolean; // ¿misma columna (venue)?
}

const venueOf = (id: string) => id.split("@")[1] ?? "";

function quad(a: Pt, b: Pt, mx: number, my: number): string {
  return `M ${a.x.toFixed(1)} ${a.y.toFixed(1)} Q ${mx.toFixed(1)} ${my.toFixed(1)} ${b.x.toFixed(1)} ${b.y.toFixed(1)}`;
}

// routeEdges: convierte parejas únicas en curvas.
//  - CROSS-VENUE: curva suave con offset perpendicular fijo (aprobadas así).
//  - INTRA-VENUE (libros dentro de la columna): FAN-OUT — cada libro recibe un
//    arco horizontal proporcional al salto vertical entre sus nodos, alternando
//    lado izquierdo/derecho, para aprovechar el ancho de la caja del venue y
//    que ninguna arista se encime con otra (observación del dueño).
export function routeEdges(edges: LayoutEdge[], layout: RadarLayout): RoutedEdge[] {
  const { pos, colW } = layout;

  // Dedupe direcciones → una arista visual por pareja.
  const seen = new Map<string, LayoutEdge>();
  for (const e of edges) {
    const k = pairKey(e.from, e.to);
    if (!seen.has(k)) seen.set(k, e);
  }

  // Agrupa intra-venue por venue para asignar lados alternados de forma
  // determinista (orden por salto vertical y luego por clave).
  const intraByVenue = new Map<string, { k: string; e: LayoutEdge; span: number }[]>();
  const out: RoutedEdge[] = [];

  for (const [k, e] of seen) {
    const a = pos.get(e.from);
    const b = pos.get(e.to);
    if (!a || !b) continue;
    const intra = venueOf(e.from) === venueOf(e.to);
    if (intra) {
      const v = venueOf(e.from);
      if (!intraByVenue.has(v)) intraByVenue.set(v, []);
      intraByVenue.get(v)!.push({ k, e, span: Math.abs(a.y - b.y) });
    } else {
      // Cross-venue: offset perpendicular suave (como el mockup aprobado).
      const dx = b.x - a.x, dy = b.y - a.y;
      const len = Math.hypot(dx, dy) || 1;
      const off = 22;
      const mx = (a.x + b.x) / 2 - (dy / len) * off;
      const my = (a.y + b.y) / 2 + (dx / len) * off;
      const [cf, ct] = k.split("|");
      out.push({ key: k, from: cf, to: ct, kind: e.kind, d: quad(pos.get(cf)!, pos.get(ct)!, mx, my), mid: { x: mx, y: my }, intra: false });
    }
  }

  for (const [, group] of intraByVenue) {
    // Orden determinista: saltos cortos primero (arcos pegados), luego clave.
    group.sort((p, q) => p.span - q.span || (p.k < q.k ? -1 : 1));
    const maxOff = Math.max(40, colW / 2 - VENUE_BOX_X_INSET - 10);
    group.forEach(({ k, e }, i) => {
      const [cf, ct] = k.split("|");
      const a = pos.get(cf)!;
      const b = pos.get(ct)!;
      const rows = Math.max(1, Math.round(Math.abs(a.y - b.y) / MAX_ROW_GAP));
      // Arco proporcional al salto (adyacente = corto, salto largo = amplio),
      // lado alternado por índice dentro del venue; acotado al ancho de caja.
      const off = Math.min(maxOff, 26 + (rows - 1) * 34 + (i % 3) * 8);
      const side = i % 2 === 0 ? -1 : 1;
      const mx = (a.x + b.x) / 2 + side * off;
      const my = (a.y + b.y) / 2;
      out.push({ key: k, from: cf, to: ct, kind: e.kind, d: quad(a, b, mx, my), mid: { x: (a.x + mx * 2 + b.x) / 4, y: (a.y + my * 2 + b.y) / 4 }, intra: true });
    });
  }

  return out;
}

// pointOnQuad: punto en t∈[0,1] de la curva cuadrática (para partículas sin
// depender de getPointAtLength en tests; en runtime se usa el path real).
export function pointOnQuad(a: Pt, c: Pt, b: Pt, t: number): Pt {
  const u = 1 - t;
  return {
    x: u * u * a.x + 2 * u * t * c.x + t * t * b.x,
    y: u * u * a.y + 2 * u * t * c.y + t * t * b.y,
  };
}
