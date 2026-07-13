"use client";

import { useCallback, useRef, useState } from "react";
import { BarChart3, ChevronDown, ChevronRight, Download, RefreshCw } from "lucide-react";
import { ENGINE_HTTP_URL } from "../lib/config";

// AnalyticsPanel — ANALÍTICA DEL LEDGER (Sprint D).
// Lee /api/stats (agregados por sesión calculados en el backend desde el Trade
// Ledger persistido) y lo presenta como tiles de estadísticas + la curva de P&L
// acumulado. La serie es ÚNICA (sin leyenda: el título la nombra); el color de
// la línea (emerald-600) está validado contra las superficies clara y oscura.
// La vista de tabla accesible es el propio panel de Historial/Auditoría.

interface StatsPoint {
  t: string;
  cum: number;
}

interface SessionStats {
  total_ops: number;
  wins: number;
  losses: number;
  win_rate_pct: number;
  net_profit_usd: number;
  fees_usd: number;
  volume_btc: number;
  ops_per_hour: number;
  credit_events: number;
  first_op_at?: string;
  last_op_at?: string;
  series: StatsPoint[];
}

function fmtUSD(v: number): string {
  const sign = v < 0 ? "-" : "";
  return `${sign}$${Math.abs(v).toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
}

function fmtTime(iso: string, withDate: boolean): string {
  const d = new Date(iso);
  if (isNaN(d.getTime())) return iso;
  const hm = d.toLocaleTimeString("es-ES", { hour: "2-digit", minute: "2-digit", hour12: false });
  return withDate ? `${d.toLocaleDateString("es-ES", { day: "2-digit", month: "2-digit" })} ${hm}` : hm;
}

// Geometría del gráfico (viewBox fijo; el SVG escala con el contenedor).
const CW = 720, CH = 210;
const PAD = { l: 56, r: 16, t: 14, b: 26 };

function PnLChart({ series }: { series: StatsPoint[] }) {
  const [hover, setHover] = useState<number | null>(null);
  const svgRef = useRef<SVGSVGElement | null>(null);

  if (series.length < 2) {
    return (
      <p className="px-4 py-6 text-center text-xs text-gray-400 dark:text-gray-500">
        La curva de P&L aparecerá cuando haya al menos dos operaciones registradas.
      </p>
    );
  }

  const cums = series.map(p => p.cum);
  // El dominio SIEMPRE incluye el cero: la línea base es la referencia de
  // "¿voy ganando o perdiendo?" y no debe quedar fuera del encuadre.
  let lo = Math.min(0, ...cums);
  let hi = Math.max(0, ...cums);
  if (hi - lo < 1e-9) { hi = lo + 1; }
  const padY = (hi - lo) * 0.08;
  lo -= padY; hi += padY;

  const x = (i: number) => PAD.l + (i * (CW - PAD.l - PAD.r)) / (series.length - 1);
  const y = (v: number) => PAD.t + ((hi - v) * (CH - PAD.t - PAD.b)) / (hi - lo);

  const path = series.map((p, i) => `${i === 0 ? "M" : "L"} ${x(i).toFixed(2)} ${y(p.cum).toFixed(2)}`).join(" ");
  const area = `${path} L ${x(series.length - 1).toFixed(2)} ${y(0).toFixed(2)} L ${x(0).toFixed(2)} ${y(0).toFixed(2)} Z`;

  // Ticks del eje Y: 4 divisiones redondeadas, rejilla recesiva.
  const ticks: number[] = [];
  for (let i = 0; i <= 3; i++) ticks.push(lo + ((hi - lo) * i) / 3);

  const spanMs = new Date(series[series.length - 1].t).getTime() - new Date(series[0].t).getTime();
  const withDate = spanMs > 24 * 3600 * 1000;
  const last = series[series.length - 1];

  const onMove = (e: React.MouseEvent<SVGSVGElement>) => {
    const rect = svgRef.current?.getBoundingClientRect();
    if (!rect) return;
    const px = ((e.clientX - rect.left) / rect.width) * CW;
    const frac = (px - PAD.l) / (CW - PAD.l - PAD.r);
    const i = Math.round(frac * (series.length - 1));
    setHover(Math.max(0, Math.min(series.length - 1, i)));
  };

  return (
    <div className="relative">
      <svg
        ref={svgRef}
        viewBox={`0 0 ${CW} ${CH}`}
        className="w-full"
        role="img"
        aria-label="P&L acumulado de la sesión a lo largo del tiempo"
        onMouseMove={onMove}
        onMouseLeave={() => setHover(null)}
      >
        {/* Rejilla recesiva + etiquetas del eje Y (tokens de texto, no color de serie) */}
        {ticks.map((v, i) => (
          <g key={i}>
            <line x1={PAD.l} x2={CW - PAD.r} y1={y(v)} y2={y(v)} className="stroke-gray-100 dark:stroke-gray-800" strokeWidth={1} />
            <text x={PAD.l - 6} y={y(v) + 3} textAnchor="end" className="text-[9px] font-mono fill-gray-400 dark:fill-gray-500">
              {fmtUSD(v)}
            </text>
          </g>
        ))}
        {/* Línea base cero: referencia de polaridad */}
        <line x1={PAD.l} x2={CW - PAD.r} y1={y(0)} y2={y(0)} strokeDasharray="4 4" className="stroke-gray-300 dark:stroke-gray-600" strokeWidth={1} />

        {/* Área y línea de la serie (única): emerald-600 validado en ambos modos */}
        <path d={area} className="fill-emerald-600/10" />
        <path d={path} fill="none" strokeWidth={2} strokeLinejoin="round" strokeLinecap="round" className="stroke-emerald-600" />

        {/* Etiqueta directa SOLO en el último punto (selectiva, no en todos) */}
        <circle cx={x(series.length - 1)} cy={y(last.cum)} r={3.5} className="fill-emerald-600 stroke-white dark:stroke-gray-900" strokeWidth={2} />
        <text
          x={Math.min(x(series.length - 1) + 6, CW - PAD.r)}
          y={y(last.cum) - 8}
          textAnchor="end"
          className="text-[10px] font-mono font-bold fill-gray-700 dark:fill-gray-300"
        >
          {fmtUSD(last.cum)}
        </text>

        {/* Eje X: primera y última marca temporal */}
        <text x={PAD.l} y={CH - 8} className="text-[9px] font-mono fill-gray-400 dark:fill-gray-500">
          {fmtTime(series[0].t, withDate)}
        </text>
        <text x={CW - PAD.r} y={CH - 8} textAnchor="end" className="text-[9px] font-mono fill-gray-400 dark:fill-gray-500">
          {fmtTime(last.t, withDate)}
        </text>

        {/* Capa de hover: crosshair + punto activo */}
        {hover !== null && (
          <g>
            <line x1={x(hover)} x2={x(hover)} y1={PAD.t} y2={CH - PAD.b} className="stroke-gray-300 dark:stroke-gray-600" strokeWidth={1} />
            <circle cx={x(hover)} cy={y(series[hover].cum)} r={4} className="fill-emerald-600 stroke-white dark:stroke-gray-900" strokeWidth={2} />
          </g>
        )}
      </svg>

      {hover !== null && (
        <div
          className="pointer-events-none absolute -translate-x-1/2 -translate-y-full bg-gray-900 dark:bg-gray-800 text-white rounded-md px-2.5 py-1.5 shadow-lg border border-gray-700"
          style={{
            left: `${(x(hover) / CW) * 100}%`,
            top: `${(y(series[hover].cum) / CH) * 100 - 4}%`,
          }}
        >
          <p className="text-[10px] font-mono whitespace-nowrap text-gray-300">{fmtTime(series[hover].t, true)}</p>
          <p className="text-xs font-mono font-bold whitespace-nowrap">{fmtUSD(series[hover].cum)} acumulado</p>
        </div>
      )}
    </div>
  );
}

function Tile({ label, value, hint, tone }: { label: string; value: string; hint?: string; tone?: "pos" | "neg" }) {
  const valueCls = tone === "pos" ? "text-emerald-600" : tone === "neg" ? "text-red-500" : "text-gray-900 dark:text-gray-100";
  return (
    <div className="bg-gray-50 dark:bg-gray-950 border border-gray-200 dark:border-gray-800 rounded-lg px-3 py-2.5">
      <p className="text-[9px] font-bold tracking-widest uppercase text-gray-500 dark:text-gray-400">{label}</p>
      <p className={`text-base font-black font-mono mt-0.5 ${valueCls}`}>{value}</p>
      {hint && <p className="text-[9px] text-gray-400 dark:text-gray-500 mt-0.5">{hint}</p>}
    </div>
  );
}

export function AnalyticsPanel({ sessionId }: { sessionId: string }) {
  const [open, setOpen] = useState(false);
  const [stats, setStats] = useState<SessionStats | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [loaded, setLoaded] = useState(false);

  const fetchStats = useCallback(async () => {
    if (!sessionId) {
      setError("Sesión aún no establecida — intenta de nuevo en unos segundos.");
      return;
    }
    setLoading(true);
    setError("");
    try {
      const url = `${ENGINE_HTTP_URL}/api/stats?session_id=${encodeURIComponent(sessionId)}`;
      const res = await fetch(url, { cache: "no-store" });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      setStats(await res.json());
      setLoaded(true);
    } catch {
      setError("No se pudo leer la analítica. Asegúrate de que el bot esté encendido.");
    } finally {
      setLoading(false);
    }
  }, [sessionId]);

  const toggle = () => {
    const next = !open;
    setOpen(next);
    if (next && !loaded) fetchStats();
  };

  const csvUrl = sessionId
    ? `${ENGINE_HTTP_URL}/api/ledger.csv?session_id=${encodeURIComponent(sessionId)}`
    : undefined;

  return (
    <div className="mt-6 bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-800 rounded-xl shadow-sm overflow-hidden animate-fade-in-up" style={{ animationDelay: "0.55s" }}>
      <button
        onClick={toggle}
        className="w-full flex items-center justify-between gap-3 px-4 sm:px-6 py-4 hover:bg-gray-50 dark:hover:bg-gray-800/40 transition-colors"
      >
        <div className="flex items-center gap-3 text-left">
          <div className="w-9 h-9 rounded-lg bg-emerald-600/10 flex items-center justify-center flex-shrink-0">
            <BarChart3 className="w-4 h-4 text-emerald-600" />
          </div>
          <div>
            <h2 className="text-sm font-bold text-gray-900 dark:text-gray-100 tracking-widest uppercase">Analítica / Rendimiento</h2>
            <p className="text-[10px] text-gray-500 dark:text-gray-400 mt-0.5">
              P&L acumulado, win rate y costos — calculados desde tu historial persistido
            </p>
          </div>
        </div>
        <div className="flex items-center gap-3 flex-shrink-0">
          {loaded && stats && !error && (
            <span className={`hidden sm:inline-flex items-center gap-1.5 text-[10px] font-bold px-2.5 py-1 rounded-full border ${stats.net_profit_usd >= 0 ? "text-emerald-600 bg-emerald-50 dark:bg-emerald-500/10 border-emerald-200 dark:border-emerald-500/20" : "text-red-500 bg-red-50 dark:bg-red-500/10 border-red-200 dark:border-red-500/20"}`}>
              {fmtUSD(stats.net_profit_usd)} · {stats.total_ops} ops
            </span>
          )}
          {open ? <ChevronDown className="w-5 h-5 text-gray-400" /> : <ChevronRight className="w-5 h-5 text-gray-400" />}
        </div>
      </button>

      {open && (
        <div className="border-t border-gray-100 dark:border-gray-800">
          <div className="px-4 sm:px-6 py-3 flex items-center justify-between gap-3 border-b border-gray-50 dark:border-gray-800/50 bg-gray-50/50 dark:bg-gray-950/30">
            <p className="text-[10px] sm:text-xs text-gray-500 dark:text-gray-400 leading-relaxed">
              Todo sale del Trade Ledger inmutable: los mismos datos que ves en Historial/Auditoría, agregados.
            </p>
            <div className="flex items-center gap-4 flex-shrink-0">
              <button
                onClick={fetchStats}
                disabled={loading}
                className="flex items-center gap-2 text-[10px] sm:text-xs font-bold uppercase tracking-widest text-emerald-600 hover:text-emerald-500 disabled:opacity-50 transition-colors"
              >
                <RefreshCw className={`w-3.5 h-3.5 ${loading ? "animate-spin" : ""}`} />
                <span className="hidden sm:inline">Actualizar</span>
              </button>
              {csvUrl && (
                <a
                  href={csvUrl}
                  download
                  className="flex items-center gap-2 text-[10px] sm:text-xs font-bold uppercase tracking-widest text-blue-600 hover:text-blue-500 transition-colors"
                  title="Descargar el historial completo como CSV"
                >
                  <Download className="w-3.5 h-3.5" />
                  <span className="hidden sm:inline">CSV</span>
                </a>
              )}
            </div>
          </div>

          {error ? (
            <div className="px-6 py-10 text-center text-sm text-red-500">{error}</div>
          ) : !stats ? (
            <div className="px-6 py-10 text-center text-sm text-gray-400">Cargando analítica...</div>
          ) : stats.total_ops === 0 && stats.credit_events === 0 ? (
            <div className="px-6 py-10 text-center text-sm text-gray-400 dark:text-gray-500">
              Todavía no hay operaciones que analizar. Deja operar al bot (o usa el modo de pruebas) y vuelve aquí.
            </div>
          ) : (
            <div className="px-4 sm:px-6 py-4">
              <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-6 gap-3">
                <Tile
                  label="Ganancia neta"
                  value={fmtUSD(stats.net_profit_usd)}
                  hint="crédito incluido"
                  tone={stats.net_profit_usd >= 0 ? "pos" : "neg"}
                />
                <Tile
                  label="Win rate"
                  value={`${stats.win_rate_pct.toFixed(1)}%`}
                  hint={`${stats.wins} ganadas · ${stats.losses} perdidas`}
                />
                <Tile label="Ritmo" value={stats.ops_per_hour.toFixed(1)} hint="operaciones / hora" />
                <Tile label="Fricción pagada" value={fmtUSD(stats.fees_usd)} hint="fees + slippage" />
                <Tile label="Volumen" value={`${stats.volume_btc.toFixed(4)} ₿`} hint="BTC-equivalente" />
                <Tile
                  label="Operaciones"
                  value={String(stats.total_ops)}
                  hint={stats.credit_events > 0 ? `+ ${stats.credit_events} eventos de crédito` : "sin eventos de crédito"}
                />
              </div>

              <div className="mt-4">
                <p className="text-[10px] font-bold tracking-widest uppercase text-gray-500 dark:text-gray-400 mb-1">
                  P&L acumulado (USD)
                </p>
                <PnLChart series={stats.series ?? []} />
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
