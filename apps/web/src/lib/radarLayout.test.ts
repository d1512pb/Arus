import { describe, it, expect } from "vitest";
import {
  layoutRadar, routeEdges, canvasSize, pairKey, venueOrderOf, venueBoxRect,
  LayoutNode, LayoutEdge, MIN_W, NODE_R, NODE_PRICE_BELOW,
} from "./radarLayout";

// Fábrica de universos sintéticos: n venues, cada uno con cash + criptos.
function universe(venueNames: string[], cryptosPerVenue = 2): LayoutNode[] {
  const nodes: LayoutNode[] = [];
  for (const v of venueNames) {
    // El cash se agrega AL FINAL a propósito: el layout debe reordenarlo arriba.
    for (let i = 0; i < cryptosPerVenue; i++) {
      nodes.push({ id: `C${i}@${v}`, venue: v, asset: `C${i}`, kind: "crypto" });
    }
    nodes.push({ id: `USD@${v}`, venue: v, asset: "USD", kind: "cash" });
  }
  return nodes;
}

describe("layoutRadar — paramétrico en número de venues", () => {
  for (const names of [["Solo"], ["A", "B"], ["A", "B", "C"], ["A", "B", "C", "D", "E"]]) {
    it(`${names.length} venue(s): columnas dentro del lienzo y sin solapes`, () => {
      const nodes = universe(names, 3);
      const L = layoutRadar(nodes);

      expect(L.venues).toEqual(names);
      expect(L.w).toBeGreaterThanOrEqual(MIN_W);
      // Todas las posiciones existen y caen dentro del lienzo.
      for (const n of nodes) {
        const p = L.pos.get(n.id)!;
        expect(p).toBeDefined();
        expect(p.x).toBeGreaterThanOrEqual(NODE_R);
        expect(p.x).toBeLessThanOrEqual(L.w - NODE_R);
        expect(p.y).toBeGreaterThanOrEqual(NODE_R);
        expect(p.y).toBeLessThanOrEqual(L.h - NODE_R);
      }
      // Centros de columna estrictamente crecientes (una columna por venue).
      const xs = names.map((v) => L.pos.get(`USD@${v}`)!.x);
      for (let i = 1; i < xs.length; i++) expect(xs[i]).toBeGreaterThan(xs[i - 1]);
      // Nodos del mismo venue comparten x; nodos distintos no se enciman en y.
      for (const v of names) {
        const inV = nodes.filter((n) => n.venue === v).map((n) => L.pos.get(n.id)!);
        const uniqueX = new Set(inV.map((p) => p.x.toFixed(3)));
        expect(uniqueX.size).toBe(1);
        const ys = inV.map((p) => p.y).sort((a, b) => a - b);
        for (let i = 1; i < ys.length; i++) expect(ys[i] - ys[i - 1]).toBeGreaterThanOrEqual(NODE_R * 2);
      }
    });
  }

  it("el cash queda ARRIBA de las criptos en cada columna", () => {
    const L = layoutRadar(universe(["A", "B", "C"], 3));
    for (const v of ["A", "B", "C"]) {
      const cashY = L.pos.get(`USD@${v}`)!.y;
      for (let i = 0; i < 3; i++) {
        expect(cashY).toBeLessThan(L.pos.get(`C${i}@${v}`)!.y);
      }
    }
  });

  it("nodos densos: círculo + etiqueta de precio caben DENTRO de la caja del venue", () => {
    // 4 activos (cash+3) fuerza el stack a yBot — el caso que se salía por debajo.
    const L = layoutRadar(universe(["Dense"], 3));
    const box = venueBoxRect(0, L);
    const boxBottom = box.y + box.height;
    for (const n of universe(["Dense"], 3)) {
      const p = L.pos.get(n.id)!;
      expect(p.y - NODE_R).toBeGreaterThanOrEqual(box.y + 8);
      // Precio bajo el círculo (crypto) + un poco de descent tipográfico.
      expect(p.y + NODE_PRICE_BELOW + 4).toBeLessThanOrEqual(boxBottom);
    }
  });

  it("1 venue: columna centrada (no estirada a todo el lienzo)", () => {
    const L = layoutRadar(universe(["Solo"], 3));
    const x = L.pos.get("USD@Solo")!.x;
    expect(Math.abs(x - L.w / 2)).toBeLessThan(1);
    expect(L.colW).toBeLessThanOrEqual(420);
  });

  it("el ancho crece con los venues; el alto con la columna más poblada", () => {
    expect(canvasSize(5, 3).w).toBeGreaterThan(canvasSize(3, 3).w);
    expect(canvasSize(3, 6).h).toBeGreaterThan(canvasSize(3, 2).h);
  });

  it("venueOrderOf conserva el orden de aparición (determinista)", () => {
    const nodes = universe(["Z", "A", "M"]);
    expect(venueOrderOf(nodes)).toEqual(["Z", "A", "M"]);
  });
});

describe("routeEdges — fan-out intra-venue y dedupe de direcciones", () => {
  const nodes = universe(["A", "B"], 3); // A: USD,C0,C1,C2 · B: igual
  const L = layoutRadar(nodes);

  it("ambas direcciones de un libro colapsan en UNA arista visual", () => {
    const edges: LayoutEdge[] = [
      { from: "USD@A", to: "C0@A", kind: "book" },
      { from: "C0@A", to: "USD@A", kind: "book" },
    ];
    const routed = routeEdges(edges, L);
    expect(routed).toHaveLength(1);
    expect(routed[0].key).toBe(pairKey("USD@A", "C0@A"));
  });

  it("intra-venue: arcos con offsets DISTINTOS (nada encimado) y dentro de la caja", () => {
    // Los 5 libros internos posibles del venue A (como Binance con 4 activos).
    const edges: LayoutEdge[] = [
      { from: "USD@A", to: "C0@A", kind: "book" },
      { from: "USD@A", to: "C1@A", kind: "book" },
      { from: "USD@A", to: "C2@A", kind: "book" },
      { from: "C0@A", to: "C1@A", kind: "book" },
      { from: "C0@A", to: "C2@A", kind: "book" },
    ];
    const routed = routeEdges(edges, L);
    expect(routed).toHaveLength(5);
    const colX = L.pos.get("USD@A")!.x;
    const mids = routed.map((r) => r.mid.x - colX);
    // Ningún par de arcos comparte el mismo offset lateral (con signo).
    const unique = new Set(mids.map((m) => m.toFixed(1)));
    expect(unique.size).toBe(mids.length);
    // Hay arcos a AMBOS lados (se aprovecha todo el ancho de la caja).
    expect(mids.some((m) => m < 0)).toBe(true);
    expect(mids.some((m) => m > 0)).toBe(true);
    // Y ninguno se sale de la caja del venue.
    for (const m of mids) expect(Math.abs(m)).toBeLessThanOrEqual(L.colW / 2);
  });

  it("cross-venue: curvas suaves sin fan-out (aprobadas como están)", () => {
    const edges: LayoutEdge[] = [
      { from: "USD@A", to: "USD@B", kind: "parity" },
      { from: "C0@A", to: "C0@B", kind: "inventory" },
    ];
    const routed = routeEdges(edges, L);
    expect(routed).toHaveLength(2);
    for (const r of routed) {
      expect(r.intra).toBe(false);
      expect(r.d).toMatch(/^M .+ Q .+/);
    }
  });

  it("aristas hacia nodos sin posición se descartan sin romper", () => {
    const routed = routeEdges([{ from: "USD@A", to: "X@NoExiste", kind: "book" }], L);
    expect(routed).toHaveLength(0);
  });

  it("determinista: mismo input, mismas curvas", () => {
    const edges: LayoutEdge[] = [
      { from: "USD@A", to: "C0@A", kind: "book" },
      { from: "USD@A", to: "C1@A", kind: "book" },
      { from: "C0@A", to: "C0@B", kind: "inventory" },
    ];
    const a = routeEdges(edges, L).map((r) => r.d).join(";");
    const b = routeEdges(edges, L).map((r) => r.d).join(";");
    expect(a).toBe(b);
  });
});
