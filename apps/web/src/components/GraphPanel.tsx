"use client";

import { useState } from "react";
import { Radar, ChevronDown, ChevronRight, Snowflake } from "lucide-react";
import { GraphSnapshot, GraphNode, GraphEdge } from "../hooks/useArusEngine";

// GraphPanel — RADAR OMNIDIRECCIONAL (Fase 2).
// Dibuja el grafo de liquidez en vivo: dónde está el dinero del usuario (nodos =
// moneda@exchange con saldo y valor), por dónde puede fluir (aristas = libros,
// paridad, inventario) y qué ciclo rentable detectó el motor automáticamente
// (resaltado en verde). Es la información básica de tus exchanges y tus monedas
// en cada uno — actualizada ~1 vez por segundo desde el backend.

// El lienzo crece con el número de venues (Sprint C: 3 columnas con Kraken) y
// deja aire vertical para columnas de hasta 4 activos (Binance con SOL).
const COL_W = 300;
const MIN_W = 860;
const H = 480;

function canvasWidth(venueCount: number): number {
  return Math.max(MIN_W, venueCount * COL_W);
}

function fmtUSD(v: number): string {
  return "$" + v.toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 });
}

function fmtBalance(n: GraphNode): string {
  if (n.kind === "cash") return fmtUSD(n.balance);
  if (n.asset === "BTC") return `${n.balance.toFixed(4)} ₿`;
  return `${n.balance.toFixed(4)} ${n.asset}`;
}

// Posiciones: una columna por venue (en orden de aparición); dentro de cada
// columna, el efectivo (cash) arriba y las criptomonedas apiladas debajo en
// orden estable. Funciona para cualquier número de activos por venue.
function layout(nodes: GraphNode[], W: number): Map<string, { x: number; y: number }> {
  const venues: string[] = [];
  for (const n of nodes) if (!venues.includes(n.venue)) venues.push(n.venue);

  const pos = new Map<string, { x: number; y: number }>();
  const colW = W / Math.max(venues.length, 1);
  const yTop = 130, yBot = H - 100;

  venues.forEach((venue, col) => {
    const inVenue = nodes
      .filter(n => n.venue === venue)
      .sort((a, b) => (a.kind === b.kind ? a.asset.localeCompare(b.asset) : a.kind === "cash" ? -1 : 1));
    const x = colW * col + colW / 2;
    inVenue.forEach((n, i) => {
      const y = inVenue.length === 1 ? (yTop + yBot) / 2 : yTop + (i * (yBot - yTop)) / (inVenue.length - 1);
      pos.set(n.id, { x, y });
    });
  });
  return pos;
}

// ¿La arista from→to forma parte del ciclo detectado?
function inCycle(cyclePath: string[] | undefined, from: string, to: string): boolean {
  if (!cyclePath) return false;
  for (let i = 0; i < cyclePath.length - 1; i++) {
    if (cyclePath[i] === from && cyclePath[i + 1] === to) return true;
  }
  return false;
}

function EdgePath({ edge, pos, highlighted }: { edge: GraphEdge; pos: Map<string, { x: number; y: number }>; highlighted: boolean }) {
  const a = pos.get(edge.from);
  const b = pos.get(edge.to);
  if (!a || !b) return null;

  // Curvatura perpendicular para que ida y vuelta no se encimen.
  const dx = b.x - a.x, dy = b.y - a.y;
  const len = Math.hypot(dx, dy) || 1;
  const off = 16;
  const mx = (a.x + b.x) / 2 - (dy / len) * off;
  const my = (a.y + b.y) / 2 + (dx / len) * off;
  const d = `M ${a.x} ${a.y} Q ${mx} ${my} ${b.x} ${b.y}`;

  const isBook = edge.kind === "book";
  const stroke = highlighted
    ? "stroke-emerald-500"
    : edge.stale
      ? "stroke-red-300 dark:stroke-red-800"
      : isBook
        ? "stroke-gray-300 dark:stroke-gray-700"
        : "stroke-gray-200 dark:stroke-gray-800";

  // Etiqueta de libros: dirección por el activo base (comprar entra AL base,
  // vender sale DEL base) y precio expresado en el activo quote del libro.
  let label = "";
  if (isBook && edge.rate > 0 && edge.base_asset) {
    const isBuy = edge.to.startsWith(edge.base_asset + "@");
    const price = isBuy ? 1 / edge.rate : edge.rate;
    const quoteAsset = (isBuy ? edge.from : edge.to).split("@")[0];
    const priceTxt = quoteAsset === "USD" || quoteAsset === "USDT"
      ? fmtUSD(price)
      : `${price.toFixed(5)} ${quoteAsset}`;
    label = `${isBuy ? "compra" : "venta"} ${priceTxt} · fee ${edge.fee_pct.toFixed(2)}%`;
  } else if (edge.kind === "parity" && highlighted) {
    // Con 3 venues las etiquetas de paridad/inventario se enciman entre
    // columnas: solo se muestran cuando la arista participa del ciclo (la nota
    // al pie del panel ya declara ambos supuestos en todo momento).
    label = "≈ paridad 1:1 (supuesto declarado)";
  } else if (edge.kind === "inventory" && highlighted) {
    label = "inventario pre-fondeado";
  }

  return (
    <g>
      <path
        d={d}
        fill="none"
        strokeWidth={highlighted ? 3 : 1.5}
        strokeDasharray={isBook ? undefined : "6 5"}
        className={`${stroke} transition-all duration-500`}
        markerEnd={highlighted ? "url(#arrow-active)" : "url(#arrow)"}
      >
        {highlighted && <animate attributeName="stroke-dashoffset" from="22" to="0" dur="0.8s" repeatCount="indefinite" />}
      </path>
      {label && (
        <text x={mx} y={my - 5} textAnchor="middle" className={`text-[9px] font-mono ${highlighted ? "fill-emerald-600" : "fill-gray-400 dark:fill-gray-500"}`}>
          {label}
        </text>
      )}
    </g>
  );
}

function NodeCircle({ node, pos, inBestCycle }: { node: GraphNode; pos: { x: number; y: number }; inBestCycle: boolean }) {
  const isCash = node.kind === "cash";
  const assetColor = isCash ? "fill-blue-500" : node.asset === "BTC" ? "fill-amber-500" : "fill-violet-500";
  return (
    <g>
      <circle
        cx={pos.x}
        cy={pos.y}
        r={34}
        className={`${inBestCycle ? "stroke-emerald-500" : "stroke-gray-200 dark:stroke-gray-700"} fill-white dark:fill-gray-900 transition-all duration-500`}
        strokeWidth={inBestCycle ? 3 : 1.5}
      />
      <text x={pos.x} y={pos.y - 8} textAnchor="middle" className={`text-[13px] font-black ${assetColor}`}>
        {node.asset}
      </text>
      <text x={pos.x} y={pos.y + 8} textAnchor="middle" className="text-[10px] font-mono font-bold fill-gray-700 dark:fill-gray-300">
        {fmtBalance(node)}
      </text>
      {!isCash && node.balance_usd > 0 && (
        <text x={pos.x} y={pos.y + 21} textAnchor="middle" className="text-[8px] font-mono fill-gray-400 dark:fill-gray-500">
          ≈ {fmtUSD(node.balance_usd)}
        </text>
      )}
      {!isCash && node.price_usd > 0 && (
        <text x={pos.x} y={pos.y + 50} textAnchor="middle" className="text-[9px] font-mono fill-gray-400 dark:fill-gray-500">
          1 {node.asset} = {fmtUSD(node.price_usd)}
        </text>
      )}
    </g>
  );
}

export function GraphPanel({ graph }: { graph: GraphSnapshot | null }) {
  const [open, setOpen] = useState(true);

  if (!graph || graph.nodes.length === 0) return null;

  const venues: string[] = [];
  for (const n of graph.nodes) if (!venues.includes(n.venue)) venues.push(n.venue);

  const W = canvasWidth(venues.length);
  const pos = layout(graph.nodes, W);
  const cycle = graph.best_cycle;
  const cycleNodes = new Set(cycle?.path ?? []);

  const staleVenues = venues.filter(v => graph.nodes.some(n => n.venue === v && n.feed_stale));
  const colW = W / Math.max(venues.length, 1);

  return (
    <div className="mt-6 bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-800 rounded-xl shadow-sm overflow-hidden animate-fade-in-up" style={{ animationDelay: "0.4s" }}>
      <button
        onClick={() => setOpen(!open)}
        className="w-full flex items-center justify-between gap-3 px-4 sm:px-6 py-4 hover:bg-gray-50 dark:hover:bg-gray-800/40 transition-colors"
      >
        <div className="flex items-center gap-3 text-left">
          <div className="w-9 h-9 rounded-lg bg-emerald-600/10 flex items-center justify-center flex-shrink-0">
            <Radar className="w-4 h-4 text-emerald-600" />
          </div>
          <div>
            <h2 className="text-sm font-bold text-gray-900 dark:text-gray-100 tracking-widest uppercase">Radar omnidireccional</h2>
            <p className="text-[10px] text-gray-500 dark:text-gray-400 mt-0.5">
              Tu dinero en cada exchange y por dónde puede fluir — el motor busca ciclos rentables automáticamente
            </p>
          </div>
        </div>
        <div className="flex items-center gap-3 flex-shrink-0">
          {cycle?.viable ? (
            <span className="hidden sm:inline-flex items-center gap-1.5 text-[10px] font-bold text-emerald-600 bg-emerald-50 dark:bg-emerald-500/10 border border-emerald-200 dark:border-emerald-500/20 px-2.5 py-1 rounded-full animate-pulse">
              ● Ciclo rentable {cycle.net_return_pct >= 0 ? "+" : ""}{cycle.net_return_pct.toFixed(3)}%
            </span>
          ) : (
            <span className="hidden sm:inline-flex items-center gap-1.5 text-[10px] font-bold text-gray-500 bg-gray-50 dark:bg-gray-800 border border-gray-200 dark:border-gray-700 px-2.5 py-1 rounded-full">
              Explorando el mercado…
            </span>
          )}
          {open ? <ChevronDown className="w-5 h-5 text-gray-400" /> : <ChevronRight className="w-5 h-5 text-gray-400" />}
        </div>
      </button>

      {open && (
        <div className="border-t border-gray-100 dark:border-gray-800 px-2 sm:px-4 pb-4">
          <div className="overflow-x-auto">
            <svg viewBox={`0 0 ${W} ${H}`} className="w-full min-w-[640px]" role="img" aria-label="Grafo de liquidez en vivo">
              <defs>
                <marker id="arrow" markerWidth="8" markerHeight="8" refX="7" refY="3" orient="auto">
                  <path d="M0,0 L7,3 L0,6" fill="none" strokeWidth="1" className="stroke-gray-300 dark:stroke-gray-600" />
                </marker>
                <marker id="arrow-active" markerWidth="8" markerHeight="8" refX="7" refY="3" orient="auto">
                  <path d="M0,0 L7,3 L0,6" fill="none" strokeWidth="1.5" className="stroke-emerald-500" />
                </marker>
              </defs>

              {/* Cajas por venue */}
              {venues.map((v, i) => (
                <g key={v}>
                  <rect
                    x={colW * i + 24}
                    y={40}
                    width={colW - 48}
                    height={H - 70}
                    rx={14}
                    className="fill-gray-50 dark:fill-gray-950 stroke-gray-200 dark:stroke-gray-800"
                    strokeWidth={1}
                  />
                  <text x={colW * i + colW / 2} y={66} textAnchor="middle" className="text-[12px] font-black tracking-widest fill-gray-700 dark:fill-gray-300">
                    {v.toUpperCase()}
                  </text>
                  {staleVenues.includes(v) && (
                    <text x={colW * i + colW / 2} y={82} textAnchor="middle" className="text-[9px] font-bold fill-red-500">
                      ❄ feed congelado
                    </text>
                  )}
                </g>
              ))}

              {/* Aristas (las del ciclo detectado, resaltadas) */}
              {graph.edges.map((e, i) => (
                <EdgePath key={`${e.from}>${e.to}-${i}`} edge={e} pos={pos} highlighted={inCycle(cycle?.path, e.from, e.to)} />
              ))}

              {/* Nodos con el saldo del usuario */}
              {graph.nodes.map(n => {
                const p = pos.get(n.id);
                return p ? <NodeCircle key={n.id} node={n} pos={p} inBestCycle={cycleNodes.has(n.id)} /> : null;
              })}
            </svg>
          </div>

          {/* Narración del ciclo (o del mercado eficiente) */}
          {cycle?.viable ? (
            <div className="mx-2 sm:mx-2 mt-1 bg-emerald-50 dark:bg-emerald-500/10 border border-emerald-200 dark:border-emerald-500/20 rounded-lg px-4 py-3">
              <p className="text-[11px] font-bold text-emerald-700 dark:text-emerald-400 uppercase tracking-widest mb-1">📡 Ciclo rentable detectado</p>
              <p className="text-xs font-mono text-emerald-800 dark:text-emerald-300 break-all">
                {cycle.path.join(" → ")}
              </p>
              <p className="text-[11px] text-emerald-700/80 dark:text-emerald-400/80 mt-1.5">
                Retorno neto por vuelta: <strong>+{cycle.net_return_pct.toFixed(3)}%</strong> (fees y slippage ya descontados)
                {cycle.max_volume_btc > 0 && <> · liquidez disponible: <strong>{cycle.max_volume_btc.toFixed(4)} BTC</strong></>}
              </p>
            </div>
          ) : (
            <div className="mx-2 mt-1 bg-gray-50 dark:bg-gray-950 border border-gray-200 dark:border-gray-800 rounded-lg px-4 py-3">
              <p className="text-[11px] text-gray-500 dark:text-gray-400 leading-relaxed">
                <Snowflake className="w-3 h-3 inline mr-1 text-blue-400" />
                Sin ciclos rentables en este instante: el spread actual no supera las comisiones — exactamente lo esperado en un mercado eficiente.
                El radar recalcula <strong>cada segundo</strong> con los precios reales de {venues.join(" y ")}; cuando un ciclo supere los costos, se iluminará en verde y aparecerá en el feed como <span className="font-mono">[RADAR]</span>.
              </p>
            </div>
          )}

          {graph.parity_assumed && (
            <p className="mx-2 mt-2 text-[10px] text-gray-400 dark:text-gray-500 leading-relaxed">
              ⓘ Las líneas punteadas declaran supuestos del modelo: <strong>USDT ≈ USD</strong> (paridad 1:1) y el <strong>inventario pre-fondeado</strong> (hay capital en ambos exchanges, así que comprar y vender es simultáneo; el traslado real se difiere al reequilibrio).
            </p>
          )}
        </div>
      )}
    </div>
  );
}
