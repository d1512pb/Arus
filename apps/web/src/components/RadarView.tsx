"use client";

import { memo, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Pencil, X } from "lucide-react";
import { GraphSnapshot, GraphEdge, Trade, LogEntry } from "../hooks/useArusEngine";
import { layoutRadar, routeEdges, pairKey, LayoutNode, LayoutEdge, NODE_R } from "../lib/radarLayout";

// RadarView — LA PANTALLA PRINCIPAL del rediseño Radar-first (fases R2+R3).
//
// Revelación progresiva: el lienzo muestra solo activo+saldo por nodo y líneas
// mudas; el precio/fee/liquidez vive en el hover de cada arista; el detalle
// completo de un nodo, en su card al click. El dinamismo (pulsos de tick,
// partículas del ciclo, cobro flotante, barrido) se anima por mutación directa
// de refs — React solo re-renderiza al llegar cada graph_update (~1/s).
//
// El lienzo TEMATIZA con la app vía variables CSS (--radar-*, globals.css):
// la clase .dark del contenedor raíz conmuta la paleta completa — SVG incluido,
// porque las custom properties se heredan. Ojo: en ATRIBUTOS de presentación
// SVG var() no es válido — los colores dinámicos van por style, no setAttribute.

interface Props {
  graph: GraphSnapshot | null;
  trades: Trade[];
  // Último log de nivel spike_block (calculado en page): pasar el feed completo
  // haría re-renderizar TODO el lienzo con cada log (decenas por segundo).
  spike: LogEntry | null;
  // Universo APLICADO del usuario (params vigentes): lo que quede fuera se
  // atenúa en el lienzo — la poda del grafo, visible. Vacío/undefined = todo.
  enabledVenues?: string[];
  enabledAssets?: string[];
  onEditFunds: (venue: string) => void;
  // Crédito: mientras un préstamo está activo, los nodos de los venues con capital
  // prestado laten en azul. Al vencer, loanResult trae la ganancia neta que rindió
  // esa inyección de capital → burst centrado en el radar (el usuario ve DE UN
  // VISTAZO cuánto ganó gracias al préstamo). loanResult cambia de referencia en
  // cada vencimiento (null entre préstamos).
  creditActive?: boolean;
  borrowedVenues?: string[];
  loanResult?: { earnings: number; cost: number } | null;
}

const INK = "var(--radar-ink)", INK2 = "var(--radar-ink2)", INK3 = "var(--radar-ink3)";
const ACCENT = "var(--radar-accent)", DANGER = "var(--radar-danger)";
const assetFill = (kind: string, asset: string) =>
  kind === "cash" ? "var(--radar-cash)" : asset === "BTC" ? "var(--radar-btc)" : "var(--radar-alt)";

const fmtUSD = (v: number) =>
  "$" + v.toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 });

function fmtBalance(kind: string, asset: string, balance: number): string {
  if (kind === "cash") return fmtUSD(balance);
  if (asset === "BTC") return `${balance.toFixed(4)} ₿`;
  return `${balance.toFixed(4)} ${asset}`;
}

// Precio legible de una arista de libro para tooltips/cards: la arista de
// compra (quote→base) trae rate = 1/ask; la de venta (base→quote) rate = bid.
function fmtQuote(price: number, quote: string): string {
  if (quote === "USD" || quote === "USDT") return fmtUSD(price);
  return `${price.toFixed(6)} ${quote}`;
}

interface TipState {
  x: number;
  y: number;
  title: string;
  rows: [string, string][];
}

interface FloatProfit {
  id: number;
  x: number;
  y: number;
  amount: number;
}

// memo: el lienzo solo se re-renderiza cuando cambian SUS datos (graph ~1/s,
// trades, spike, universo) — no con cada log o ping del feed de mercado.
export const RadarView = memo(function RadarView({ graph, trades, spike, enabledVenues, enabledAssets, onEditFunds, creditActive, borrowedVenues, loanResult }: Props) {
  const venueAllowed = useCallback(
    (v: string) => !enabledVenues || enabledVenues.length === 0 || enabledVenues.includes(v),
    [enabledVenues]
  );
  const nodeAllowed = useCallback(
    (venue: string, asset: string) =>
      venueAllowed(venue) && (!enabledAssets || enabledAssets.length === 0 || enabledAssets.includes(asset)),
    [venueAllowed, enabledAssets]
  );
  const wrapRef = useRef<HTMLDivElement>(null);
  const innerRef = useRef<HTMLDivElement>(null);
  const pathRefs = useRef(new Map<string, SVGPathElement>());
  const nodeRefs = useRef(new Map<string, SVGGElement>());
  const pulseRefs = useRef(new Map<string, SVGCircleElement>());
  const particlesRef = useRef<SVGGElement>(null);

  const [tip, setTip] = useState<TipState | null>(null);
  const [selNode, setSelNode] = useState<string | null>(null);
  const [cardPos, setCardPos] = useState<{ left: number; top: number } | null>(null);
  const [floats, setFloats] = useState<FloatProfit[]>([]);
  const [spikeMsg, setSpikeMsg] = useState<string | null>(null);
  // Burst de ganancia por préstamo (se limpia solo a los 6 s, independiente del
  // parent que limpia loanResult a los 10 s).
  const [loanBurst, setLoanBurst] = useState<{ earnings: number; cost: number; net: number } | null>(null);

  // Venues con capital prestado (lookup O(1) para el anillo azul de los nodos).
  const borrowedSet = useMemo(() => new Set(borrowedVenues ?? []), [borrowedVenues]);

  const reduced = useMemo(
    () => typeof window !== "undefined" && window.matchMedia("(prefers-reduced-motion: reduce)").matches,
    []
  );

  // ── Geometría (solo cambia si cambia la TOPOLOGÍA, no con cada precio) ──
  const topoKey = graph ? graph.nodes.map((n) => n.id).join(",") : "";
  const layout = useMemo(() => {
    if (!graph) return null;
    const nodes: LayoutNode[] = graph.nodes.map((n) => ({
      id: n.id, venue: n.venue, asset: n.asset, kind: n.kind,
    }));
    return layoutRadar(nodes);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [topoKey]);

  const edgesKey = graph ? graph.edges.map((e) => pairKey(e.from, e.to)).sort().join(",") : "";
  const routed = useMemo(() => {
    if (!graph || !layout) return [];
    const edges: LayoutEdge[] = graph.edges.map((e) => ({ from: e.from, to: e.to, kind: e.kind }));
    return routeEdges(edges, layout);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [layout, edgesKey]);

  // Índice de libros por pareja (se refresca con cada snapshot: trae precios).
  const books = useMemo(() => {
    const m = new Map<string, { venue: string; base: string; quote: string; buy?: GraphEdge; sell?: GraphEdge }>();
    if (!graph) return m;
    for (const e of graph.edges) {
      if (e.kind !== "book" || !e.base_asset) continue;
      const k = pairKey(e.from, e.to);
      const venue = e.from.split("@")[1];
      const isBuy = e.to.startsWith(e.base_asset + "@"); // quote→base
      const quote = (isBuy ? e.from : e.to).split("@")[0];
      const cur = m.get(k) ?? { venue, base: e.base_asset, quote };
      if (isBuy) cur.buy = e; else cur.sell = e;
      m.set(k, cur);
    }
    return m;
  }, [graph]);

  const staleVenues = useMemo(() => {
    const s = new Set<string>();
    for (const n of graph?.nodes ?? []) if (n.feed_stale) s.add(n.venue);
    return s;
  }, [graph]);

  // ── Ciclo detectado ───────────────────────────────────────────────────────
  const cyclePath = graph?.best_cycle?.viable ? graph.best_cycle.path : null;
  const cycleKey = cyclePath?.join(">") ?? "";
  const cyclePairs = useMemo(() => {
    const s = new Set<string>();
    if (cyclePath) for (let i = 0; i < cyclePath.length - 1; i++) s.add(pairKey(cyclePath[i], cyclePath[i + 1]));
    return s;
  }, [cycleKey]); // eslint-disable-line react-hooks/exhaustive-deps
  const cycleNodes = useMemo(() => new Set(cyclePath ?? []), [cycleKey]); // eslint-disable-line react-hooks/exhaustive-deps

  // Partículas recorriendo el ciclo (rAF sobre los paths REALES, dirección
  // respetada aunque la arista visual esté dibujada al revés).
  useEffect(() => {
    const g = particlesRef.current;
    if (!g || !cyclePath || reduced) return;
    const segs: { el: SVGPathElement; rev: boolean }[] = [];
    for (let i = 0; i < cyclePath.length - 1; i++) {
      const a = cyclePath[i], b = cyclePath[i + 1];
      const k = pairKey(a, b);
      const el = pathRefs.current.get(k);
      if (!el) return;
      segs.push({ el, rev: !(k.split("|")[0] === a) });
    }
    const N = 5;
    const parts: SVGCircleElement[] = [];
    for (let i = 0; i < N; i++) {
      const c = document.createElementNS("http://www.w3.org/2000/svg", "circle");
      c.setAttribute("r", "4.5");
      // var() no es válido como ATRIBUTO de presentación SVG: el color va por style.
      c.style.fill = ACCENT;
      c.style.filter = `drop-shadow(0 0 6px ${ACCENT})`;
      c.style.pointerEvents = "none";
      g.appendChild(c);
      parts.push(c);
    }
    const lens = segs.map(({ el }) => el.getTotalLength());
    const total = lens.reduce((s, l) => s + l, 0);
    let raf = 0;
    const t0 = performance.now();
    const DUR = 2600;
    const step = (t: number) => {
      raf = requestAnimationFrame(step);
      if (document.hidden || total <= 0) return;
      const base = ((t - t0) % DUR) / DUR;
      parts.forEach((c, i) => {
        let d = ((base + i / N) % 1) * total;
        for (let s = 0; s < segs.length; s++) {
          if (d <= lens[s]) {
            const pt = segs[s].el.getPointAtLength(segs[s].rev ? lens[s] - d : d);
            c.setAttribute("cx", String(pt.x));
            c.setAttribute("cy", String(pt.y));
            break;
          }
          d -= lens[s];
        }
      });
    };
    raf = requestAnimationFrame(step);
    return () => {
      cancelAnimationFrame(raf);
      parts.forEach((c) => c.remove());
    };
  }, [cycleKey, reduced]); // eslint-disable-line react-hooks/exhaustive-deps

  // ── Pulso de tick: nodos cuyo libro cambió entre snapshots ───────────────
  const prevRatesRef = useRef(new Map<string, number>());
  useEffect(() => {
    if (!graph) return;
    const next = new Map<string, number>();
    const changed = new Set<string>();
    for (const e of graph.edges) {
      if (e.kind !== "book") continue;
      const k = `${e.from}>${e.to}`;
      next.set(k, e.rate);
      const prev = prevRatesRef.current.get(k);
      if (prev !== undefined && prev !== e.rate) {
        changed.add(e.from);
        changed.add(e.to);
      }
    }
    prevRatesRef.current = next;
    if (reduced || document.hidden) return;
    for (const id of changed) {
      pulseRefs.current.get(id)?.animate(
        [{ opacity: 0.8, transform: "scale(1)" }, { opacity: 0, transform: "scale(1.45)" }],
        { duration: 650, easing: "ease-out" }
      );
    }
  }, [graph, reduced]);

  // ── Cobro: al llegar una operación nueva, "+$X" flota desde su origen ─────
  const prevTradeRef = useRef<string | null>(null);
  useEffect(() => {
    const t = trades[0];
    if (!t || !graph) return;
    const sig = `${t.timestamp}|${t.net_profit_usd}`;
    if (prevTradeRef.current === sig) return;
    const isFirst = prevTradeRef.current === null;
    prevTradeRef.current = sig;
    if (isFirst || reduced) return; // no animar el histórico al montar

    // Anclaje: inicio del ciclo si hay; si no, el nodo cash del venue de compra.
    const anchorId =
      cyclePath?.[0] ??
      graph.nodes.find((n) => n.venue === t.exchange_buy && n.kind === "cash")?.id ??
      graph.nodes.find((n) => n.kind === "cash")?.id;
    const el = anchorId ? nodeRefs.current.get(anchorId) : null;
    const inner = innerRef.current;
    if (!el || !inner) return;
    const nb = el.getBoundingClientRect();
    const ib = inner.getBoundingClientRect();
    const id = Date.now() + Math.random();
    setFloats((f) => [...f, {
      id,
      x: nb.left - ib.left + nb.width / 2,
      y: nb.top - ib.top - 10,
      amount: t.net_profit_usd,
    }]);
    setTimeout(() => setFloats((f) => f.filter((p) => p.id !== id)), 2400);
  }, [trades, graph, cyclePath, reduced]);

  // ── Ganancia por préstamo: burst centrado al vencer el crédito ───────────
  // loanResult cambia de referencia en cada CREDIT_EXPIRED (null entre préstamos);
  // el ref evita re-disparar por re-renders con el mismo resultado.
  const lastLoanRef = useRef<{ earnings: number; cost: number } | null>(null);
  useEffect(() => {
    if (!loanResult || lastLoanRef.current === loanResult) return;
    lastLoanRef.current = loanResult;
    setLoanBurst({
      earnings: loanResult.earnings,
      cost: loanResult.cost,
      net: loanResult.earnings - loanResult.cost,
    });
    const to = setTimeout(() => setLoanBurst(null), 6000);
    return () => clearTimeout(to);
  }, [loanResult]);

  // ── Seguridad visible: SPIKE/CIRCUIT BREAKER en rojo por unos segundos ───
  const lastSpikeRef = useRef<string | null>(null);
  useEffect(() => {
    if (!spike) return;
    const sig = `${spike.timestamp}|${spike.message.slice(0, 40)}`;
    if (lastSpikeRef.current === sig) return;
    const isFirst = lastSpikeRef.current === null;
    lastSpikeRef.current = sig;
    if (isFirst) return; // no re-narrar historial al montar
    setSpikeMsg(spike.message);
    const to = setTimeout(() => setSpikeMsg(null), 4500);
    return () => clearTimeout(to);
  }, [spike]);

  // ── Interacciones ─────────────────────────────────────────────────────────
  const showEdgeTip = useCallback((k: string, kind: string, ev: React.MouseEvent) => {
    const inner = innerRef.current;
    if (!inner) return;
    const ib = inner.getBoundingClientRect();
    const x = ev.clientX - ib.left, y = ev.clientY - ib.top;
    const bk = books.get(k);
    if (kind === "book" && bk) {
      const rows: [string, string][] = [];
      if (bk.buy && bk.buy.rate > 0) rows.push([`compra ${bk.base}`, fmtQuote(1 / bk.buy.rate, bk.quote)]);
      if (bk.sell && bk.sell.rate > 0) rows.push([`venta ${bk.base}`, fmtQuote(bk.sell.rate, bk.quote)]);
      const fee = bk.buy?.fee_pct ?? bk.sell?.fee_pct;
      if (fee !== undefined) rows.push(["tu fee + slippage", `${fee.toFixed(2)} %`]);
      const liqA = bk.buy?.liquidity ?? 0, liqB = bk.sell?.liquidity ?? 0;
      rows.push(["liquidez visible", `${liqA > 0 ? liqA.toFixed(4) : "–"} / ${liqB > 0 ? liqB.toFixed(4) : "–"} ${bk.base}`]);
      setTip({ x, y, title: `LIBRO ${bk.base}/${bk.quote} · ${bk.venue.toUpperCase()}`, rows });
    } else {
      const [a, b] = k.split("|");
      setTip({
        x, y,
        title: "SUPUESTO DEL MODELO",
        rows: [[`${a} ⇄ ${b}`, ""], [kind === "parity" ? "paridad 1:1 (declarada)" : "inventario pre-fondeado", ""]],
      });
    }
  }, [books]);

  const openNodeCard = useCallback((id: string, ev?: React.MouseEvent | React.KeyboardEvent) => {
    ev?.stopPropagation();
    setSelNode(id);
    const el = nodeRefs.current.get(id);
    const inner = innerRef.current;
    if (el && inner && typeof window !== "undefined" && window.innerWidth >= 768) {
      const nb = el.getBoundingClientRect();
      const ib = inner.getBoundingClientRect();
      let left = nb.right - ib.left + 14;
      if (left + 312 > ib.width) left = Math.max(8, nb.left - ib.left - 316);
      const top = Math.max(8, Math.min(nb.top - ib.top - 40, ib.height - 340));
      setCardPos({ left, top });
    } else {
      setCardPos(null); // móvil: sheet inferior fija
    }
  }, []);

  // ── Estado vacío (primer graph_update aún en camino) ─────────────────────
  if (!graph || !layout || graph.nodes.length === 0) {
    return (
      <div className="flex-1 min-h-[520px] flex flex-col items-center justify-center gap-5 bg-[var(--radar-bg)]">
        <div className="w-12 h-12 border-4 border-[var(--radar-border)] border-t-[var(--radar-accent)] rounded-full animate-spin" />
        <p className="text-[11px] font-mono font-bold tracking-[0.2em] uppercase text-[var(--radar-ink2)]">
          Conectando con el radar…
        </p>
      </div>
    );
  }

  const { w, h, colW, offsetX, venues, pos } = layout;
  const sel = selNode ? graph.nodes.find((n) => n.id === selNode) : null;
  const selBooks = sel
    ? [...books.entries()].filter(([, b]) => {
        const baseId = `${b.base}@${b.venue}`, quoteId = `${b.quote}@${b.venue}`;
        return baseId === sel.id || quoteId === sel.id;
      })
    : [];
  const lastTrade = trades[0];
  const netPct = graph.best_cycle?.net_return_pct ?? 0;

  return (
    <div className="flex-1 min-h-[520px] flex flex-col min-h-0 bg-[var(--radar-bg)]" onClick={() => setSelNode(null)}>
      {/* Lienzo con scroll horizontal si el universo no cabe */}
      <div ref={wrapRef} className="flex-1 min-h-0 overflow-auto">
        <div ref={innerRef} className="relative h-full" style={{ minWidth: w }}>
          <svg
            viewBox={`0 0 ${w} ${h}`}
            className="w-full h-full block"
            role="img"
            aria-label="Radar omnidireccional: tu dinero en cada exchange y por dónde puede fluir"
          >
            {/* Cajas por venue (atenuadas si el usuario las podó de su universo) */}
            {venues.map((v, i) => (
              <g key={v} opacity={!venueAllowed(v) ? 0.16 : staleVenues.has(v) ? 0.75 : 1}
                 style={{ transition: "opacity .4s" }}>
                <rect
                  x={offsetX + colW * i + 18} y={52} width={colW - 36} height={h - 108} rx={16}
                  style={{ fill: "var(--radar-panel)", stroke: "var(--radar-border)" }} strokeWidth={1}
                />
                <text x={offsetX + colW * i + colW / 2} y={84} textAnchor="middle"
                  className="text-[12px] font-black tracking-[0.3em]" style={{ fill: INK2 }}>
                  {v.toUpperCase()}
                </text>
                {!venueAllowed(v) ? (
                  <text x={offsetX + colW * i + colW / 2} y={100} textAnchor="middle"
                    className="text-[9px] font-bold" style={{ fill: INK3 }}>
                    fuera de tu universo
                  </text>
                ) : staleVenues.has(v) && (
                  <text x={offsetX + colW * i + colW / 2} y={100} textAnchor="middle"
                    className="text-[9px] font-bold" style={{ fill: DANGER }}>
                    ❄ feed congelado
                  </text>
                )}
              </g>
            ))}

            {/* Aristas mudas (libros sólidos, supuestos punteados) + hit areas */}
            <g>
              {routed.map((r) => {
                const inCycle = cyclePairs.has(r.key);
                const isBook = r.kind === "book";
                return (
                  <g key={r.key}>
                    <path
                      ref={(el) => { if (el) pathRefs.current.set(r.key, el); else pathRefs.current.delete(r.key); }}
                      d={r.d}
                      fill="none"
                      strokeWidth={inCycle ? 2.6 : isBook ? 1.6 : 1.1}
                      strokeDasharray={isBook ? undefined : "5 6"}
                      style={{
                        stroke: inCycle ? ACCENT : isBook ? "var(--radar-edge-book)" : "var(--radar-edge-assume)",
                        ...(inCycle ? { filter: `drop-shadow(0 0 7px ${ACCENT})` } : null),
                      }}
                    />
                    <path
                      d={r.d}
                      fill="none"
                      stroke="transparent"
                      strokeWidth={16}
                      style={{ cursor: "pointer" }}
                      onMouseMove={(ev) => showEdgeTip(r.key, r.kind, ev)}
                      onMouseLeave={() => setTip(null)}
                    />
                  </g>
                );
              })}
            </g>

            {/* Partículas del ciclo (imperativas) */}
            <g ref={particlesRef} />

            {/* Nodos: activo + saldo (+≈USD). El detalle espera al click. */}
            {graph.nodes.map((n) => {
              const p = pos.get(n.id);
              if (!p) return null;
              const inCycle = cycleNodes.has(n.id);
              const seld = selNode === n.id;
              const allowed = nodeAllowed(n.venue, n.asset);
              return (
                <g
                  key={n.id}
                  ref={(el) => { if (el) nodeRefs.current.set(n.id, el); else nodeRefs.current.delete(n.id); }}
                  tabIndex={0}
                  role="button"
                  aria-label={`${n.asset} en ${n.venue}: ${fmtBalance(n.kind, n.asset, n.balance)}`}
                  opacity={allowed ? 1 : 0.16}
                  style={{ cursor: "pointer", outline: "none", transition: "opacity .4s" }}
                  onClick={(ev) => openNodeCard(n.id, ev)}
                  onKeyDown={(ev) => { if (ev.key === "Enter" || ev.key === " ") { ev.preventDefault(); openNodeCard(n.id, ev); } }}
                >
                  {/* Anillo azul: este venue sostiene capital prestado (préstamo
                      activo). Late suave mientras dura el crédito. */}
                  {creditActive && borrowedSet.has(n.venue) && (
                    <circle
                      cx={p.x} cy={p.y} r={NODE_R + 5}
                      fill="none" strokeWidth={2}
                      className="animate-credit-ring"
                      style={{ stroke: "var(--radar-credit)", filter: "drop-shadow(0 0 6px var(--radar-credit-glow))", pointerEvents: "none" }}
                    />
                  )}
                  <circle
                    ref={(el) => { if (el) pulseRefs.current.set(n.id, el); else pulseRefs.current.delete(n.id); }}
                    cx={p.x} cy={p.y} r={NODE_R}
                    fill="none" opacity={0}
                    style={{ stroke: ACCENT, transformBox: "fill-box", transformOrigin: "center", pointerEvents: "none" }}
                  />
                  <circle
                    cx={p.x} cy={p.y} r={NODE_R}
                    strokeWidth={inCycle ? 2.4 : seld ? 2 : 1.4}
                    style={{
                      fill: "var(--radar-node)",
                      stroke: inCycle || seld ? ACCENT : "var(--radar-border)",
                      ...(inCycle ? { filter: `drop-shadow(0 0 8px var(--radar-accent-glow))` } : null),
                    }}
                  />
                  <text x={p.x} y={p.y - 8} textAnchor="middle" className="text-[13px] font-black font-mono"
                    style={{ fill: assetFill(n.kind, n.asset) }}>
                    {n.asset}
                  </text>
                  <text x={p.x} y={p.y + 9} textAnchor="middle" className="text-[11px] font-bold font-mono" style={{ fill: INK }}>
                    {fmtBalance(n.kind, n.asset, n.balance)}
                  </text>
                  {n.kind !== "cash" && n.balance_usd > 0 && (
                    <text x={p.x} y={p.y + 23} textAnchor="middle" className="text-[9px] font-mono" style={{ fill: INK3 }}>
                      ≈ {fmtUSD(n.balance_usd)}
                    </text>
                  )}
                  {n.kind !== "cash" && n.price_usd > 0 && (
                    <text x={p.x} y={p.y + NODE_R + 16} textAnchor="middle" className="text-[9px] font-mono" style={{ fill: INK3 }}>
                      1 {n.asset} = {fmtUSD(n.price_usd)}
                    </text>
                  )}
                </g>
              );
            })}
          </svg>

          {/* Barrido tipo sonar cuando el mercado está eficiente. Es un overlay
              HTML (no SVG) a propósito: el compositor lo anima en su propia capa
              sin repintar el lienzo completo en cada frame. */}
          {!cyclePath && !reduced && (
            <div
              aria-hidden="true"
              className="absolute inset-y-3 left-0 w-[130px] pointer-events-none animate-radar-sweep"
              style={{
                background: "linear-gradient(90deg, transparent, var(--radar-sweep-mid) 60%, var(--radar-sweep-end))",
                willChange: "transform",
                ["--sweep-dist" as string]: `${w + 140}px`,
              }}
            />
          )}

          {/* Tooltip de arista (nivel hover) */}
          {tip && (
            <div
              className="absolute z-30 pointer-events-none bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-lg px-3 py-2 shadow-xl font-mono text-[11px] min-w-[210px]"
              style={{ left: Math.min(tip.x + 14, (innerRef.current?.clientWidth ?? w) - 240), top: tip.y + 14 }}
            >
              <p className="text-[9px] font-sans font-bold tracking-[0.18em] text-gray-500 dark:text-gray-400 mb-1.5">{tip.title}</p>
              {tip.rows.map(([k2, v2], i) => (
                <div key={i} className="flex justify-between gap-5 py-0.5">
                  <span className="text-gray-400 dark:text-gray-500">{k2}</span>
                  <span className="text-gray-800 dark:text-gray-200 font-bold">{v2}</span>
                </div>
              ))}
            </div>
          )}

          {/* Cobros flotantes (+$X) */}
          {floats.map((f) => (
            <div
              key={f.id}
              className="absolute z-30 pointer-events-none font-mono font-black text-xl animate-profit-rise"
              style={{ left: f.x, top: f.y, color: f.amount >= 0 ? ACCENT : DANGER, textShadow: `0 0 14px ${f.amount >= 0 ? ACCENT : DANGER}` }}
            >
              {f.amount >= 0 ? "+" : "-"}{fmtUSD(Math.abs(f.amount))}
            </div>
          ))}

          {/* Burst: ganancia neta que rindió el préstamo (centrado en el radar) */}
          {loanBurst && <LoanBurst earnings={loanBurst.earnings} cost={loanBurst.cost} net={loanBurst.net} reduced={reduced} />}

          {/* Card de nodo: flotante en desktop, sheet inferior en móvil */}
          {sel && (
            <div
              className={`z-40 bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 shadow-2xl animate-card-pop overflow-hidden ${
                cardPos ? "absolute w-[300px] rounded-xl" : "fixed inset-x-0 bottom-0 rounded-t-2xl"
              }`}
              style={cardPos ?? undefined}
              onClick={(ev) => ev.stopPropagation()}
            >
              <div className="flex items-center gap-2.5 px-4 py-3 border-b border-gray-100 dark:border-gray-800">
                <span className="font-mono font-black text-[15px]" style={{ color: assetFill(sel.kind, sel.asset) }}>
                  {sel.asset}
                </span>
                <span className="text-[9px] font-bold tracking-[0.2em] text-gray-400 dark:text-gray-500">{sel.venue.toUpperCase()}</span>
                <span className={`ml-auto text-[9px] font-bold flex items-center gap-1 ${sel.feed_stale ? "text-red-500" : "text-emerald-500"}`}>
                  {sel.feed_stale ? "❄ feed congelado" : "● en vivo"}
                </span>
                <button
                  onClick={() => setSelNode(null)}
                  aria-label="Cerrar detalle"
                  className="p-1 text-gray-400 hover:text-gray-700 dark:hover:text-gray-200 transition-colors"
                >
                  <X className="w-4 h-4" />
                </button>
              </div>
              <div className="px-4 py-3 font-mono" style={{ fontVariantNumeric: "tabular-nums" }}>
                <p className="text-xl font-black text-gray-900 dark:text-gray-100">{fmtBalance(sel.kind, sel.asset, sel.balance)}</p>
                <p className="text-[11px] text-gray-400 dark:text-gray-500 mt-0.5">
                  {sel.kind !== "cash" && sel.balance_usd > 0 ? `≈ ${fmtUSD(sel.balance_usd)} · ` : ""}
                  1 {sel.asset} = {sel.kind === "cash" ? "$1.00 (cash)" : sel.price_usd > 0 ? fmtUSD(sel.price_usd) : "sin dato"}
                </p>
                <div className="mt-3 pt-2.5 border-t border-gray-100 dark:border-gray-800">
                  <p className="text-[9px] font-sans font-bold tracking-[0.18em] uppercase text-gray-500 dark:text-gray-400 mb-1.5">
                    Libros que tocan este nodo
                  </p>
                  {selBooks.length === 0 ? (
                    <p className="text-[11px] text-gray-400 dark:text-gray-500">— nodo cash: convierte vía paridad/inventario</p>
                  ) : (
                    selBooks.map(([k2, b]) => {
                      const isQuoteSide = `${b.quote}@${b.venue}` === sel.id;
                      const price = isQuoteSide
                        ? b.buy && b.buy.rate > 0 ? fmtQuote(1 / b.buy.rate, b.quote) : "–"
                        : b.sell && b.sell.rate > 0 ? fmtQuote(b.sell.rate, b.quote) : "–";
                      const fee = b.buy?.fee_pct ?? b.sell?.fee_pct ?? 0;
                      const liq = b.sell?.liquidity ?? b.buy?.liquidity ?? 0;
                      return (
                        <div key={k2} className="flex justify-between gap-3 py-1 text-[11px]">
                          <span className="text-gray-500 dark:text-gray-400">
                            {isQuoteSide ? `→ compra ${b.base}` : `⇄ libro ${b.base}/${b.quote}`}
                          </span>
                          <span className="text-gray-800 dark:text-gray-200 text-right">
                            {price} · fee {fee.toFixed(2)}%{liq > 0 ? ` · liq ${liq.toFixed(4)}` : ""}
                          </span>
                        </div>
                      );
                    })
                  )}
                </div>
              </div>
              <div className="flex gap-2 px-4 pb-4">
                <button
                  onClick={() => { onEditFunds(sel.venue); setSelNode(null); }}
                  className="flex-1 flex items-center justify-center gap-1.5 py-2 rounded-lg border border-blue-200 dark:border-blue-500/30 bg-blue-50 dark:bg-blue-500/10 text-blue-600 dark:text-blue-400 text-[10px] font-bold uppercase tracking-widest hover:bg-blue-100 dark:hover:bg-blue-500/20 transition-colors"
                >
                  <Pencil className="w-3 h-3" /> Editar fondos de {sel.venue}
                </button>
              </div>
            </div>
          )}
        </div>
      </div>

      {/* Narración de 1 línea + ticker de última operación */}
      <div className="flex items-center gap-4 px-4 sm:px-5 h-10 border-t border-[var(--radar-border)] bg-[var(--radar-bar)] font-mono text-[11px] flex-shrink-0">
        <p
          className="truncate"
          style={{ color: spikeMsg ? DANGER : cyclePath ? ACCENT : INK2 }}
          aria-live="polite"
        >
          {spikeMsg
            ? `⚠️ ${spikeMsg}`
            : cyclePath
              ? `📡 Ciclo rentable: ${cyclePath.join(" → ")} (${netPct >= 0 ? "+" : ""}${netPct.toFixed(3)} % neto por vuelta${
                  (graph.best_cycle?.max_volume_btc ?? 0) > 0
                    ? ` · hasta ${graph.best_cycle!.max_volume_btc.toFixed(4)} BTC`
                    : (graph.best_cycle?.max_start_amount ?? 0) > 0
                      ? ` · entrada hasta ${graph.best_cycle!.max_start_amount!.toLocaleString("en-US", { maximumFractionDigits: 0 })} ${graph.best_cycle!.start_asset}`
                      : ""
                })`
              : "◉ Mercado eficiente — los fees superan al spread. El radar recalcula cada segundo…"}
        </p>
        {lastTrade && (
          <p className="ml-auto flex-shrink-0 text-[10px]" style={{ color: INK3 }}>
            ÚLTIMA OP: <span style={{ color: lastTrade.net_profit_usd >= 0 ? ACCENT : DANGER }} className="font-bold">
              {lastTrade.net_profit_usd >= 0 ? "+" : "-"}{fmtUSD(Math.abs(lastTrade.net_profit_usd))}
            </span>
            {" · "}{lastTrade.exchange_buy}→{lastTrade.exchange_sell}
          </p>
        )}
      </div>
    </div>
  );
});

// LoanBurst — destello centrado que, al vencer un préstamo, resume DE UN VISTAZO
// cuánto NETO rindió la inyección de capital (el fuerte de Arus: no perder
// oportunidades por falta de fondos). El neto cuenta hacia arriba (imán visual) y
// debajo va el desglose operado/interés. Color por signo: honesto — si el préstamo
// no rindió, se ve en rojo, sin maquillar. Overlay HTML: no repinta el lienzo SVG.
function LoanBurst({ earnings, cost, net, reduced }: { earnings: number; cost: number; net: number; reduced: boolean }) {
  const netRef = useRef<HTMLSpanElement>(null);
  const positive = net >= 0;
  const color = positive ? ACCENT : DANGER;
  const signed = (v: number) => `${v >= 0 ? "+" : "-"}${fmtUSD(Math.abs(v))}`;

  useEffect(() => {
    const el = netRef.current;
    if (!el || reduced) return;
    const t0 = performance.now();
    const DUR = 1100;
    let raf = requestAnimationFrame(function step(t: number) {
      const k = Math.min(1, (t - t0) / DUR);
      const eased = 1 - Math.pow(1 - k, 3);
      el.textContent = signed(net * eased);
      if (k < 1) raf = requestAnimationFrame(step);
    });
    return () => cancelAnimationFrame(raf);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [net, reduced]);

  return (
    <div className="absolute z-40 pointer-events-none animate-loan-burst" style={{ left: "50%", top: "42%" }}>
      <div
        className="flex flex-col items-center gap-1.5 px-7 py-5 rounded-2xl backdrop-blur-md"
        style={{
          background: "var(--radar-panel)",
          border: `1.5px solid ${color}`,
          boxShadow: `0 0 44px ${positive ? "var(--radar-accent-glow)" : "rgba(220,38,38,0.4)"}`,
        }}
      >
        <span className="text-[10px] font-sans font-black tracking-[0.22em] uppercase" style={{ color: INK2 }}>
          🏦 {positive ? "Ganancia con el préstamo" : "Resultado del préstamo"}
        </span>
        <span ref={netRef} className="font-mono font-black text-4xl leading-none" style={{ color, textShadow: `0 0 24px ${color}` }}>
          {signed(reduced ? net : 0)}
        </span>
        <span className="text-[11px] font-mono" style={{ color: INK3 }}>
          <span style={{ color: ACCENT }}>+{fmtUSD(earnings)}</span> generados · <span style={{ color: DANGER }}>−{fmtUSD(cost)}</span> interés
        </span>
      </div>
    </div>
  );
}
