"use client";

import { useEffect, useRef } from "react";
import { Radar, LayoutDashboard, ShieldAlert, SlidersHorizontal, BookOpen, Moon, Sun, RotateCcw } from "lucide-react";

// HeaderBar — cabecera compacta del rediseño Radar-first (fase R1).
// Una sola línea con lo vital: marca, estado, patrimonio+PnL (con contador
// animado), tabs RADAR|DASHBOARD, y las acciones globales. "Probar el bot" vive
// aquí porque inyectar un escenario y VER al radar reaccionar es la demo
// central de Arus — disponible desde ambas vistas.

export type AppView = "radar" | "dashboard";

interface Props {
  view: AppView;
  onViewChange: (v: AppView) => void;
  totalWealth: number;
  pnl: number | null;
  uptime: string;
  autopilot: boolean;
  onProbar: () => void;
  onEstrategia: () => void;
  onTutorial: () => void;
  onReset: () => void;
  isDarkMode: boolean;
  onToggleDark: () => void;
}

const fmtUSD = (v: number) =>
  "$" + v.toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 });

// useCountUp: React SIEMPRE pinta el valor objetivo (correcto por construcción);
// la animación de "contar hacia arriba" es una mutación imperativa del span por
// encima — si un re-render intermedio la pisa, simplemente salta al final.
function useCountUp(target: number) {
  const ref = useRef<HTMLSpanElement>(null);
  const prevRef = useRef(target);

  useEffect(() => {
    const el = ref.current;
    const from = prevRef.current;
    prevRef.current = target;
    const reduced = typeof window !== "undefined" && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    if (!el || reduced || Math.abs(target - from) < 0.005) return;
    const t0 = performance.now();
    const DUR = 800;
    let raf = requestAnimationFrame(function step(t: number) {
      const k = Math.min(1, (t - t0) / DUR);
      const eased = 1 - Math.pow(1 - k, 3);
      el.textContent = fmtUSD(from + (target - from) * eased);
      if (k < 1) raf = requestAnimationFrame(step);
    });
    return () => cancelAnimationFrame(raf);
  }, [target]);

  return ref;
}

export function HeaderBar({
  view, onViewChange, totalWealth, pnl, uptime, autopilot,
  onProbar, onEstrategia, onTutorial, onReset, isDarkMode, onToggleDark,
}: Props) {
  const wealthRef = useCountUp(totalWealth);

  const tabCls = (active: boolean) =>
    `flex items-center gap-1.5 px-3 sm:px-4 py-2 text-[10px] font-bold uppercase tracking-widest transition-colors ${
      active
        ? "bg-emerald-500/10 text-emerald-600 dark:text-emerald-400"
        : "text-gray-500 dark:text-gray-400 hover:text-gray-800 dark:hover:text-gray-200"
    }`;

  return (
    <header className="relative z-50 px-3 sm:px-5 py-2.5 border-b border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 sticky top-0 shadow-sm flex flex-wrap items-center gap-x-4 gap-y-2">
      {/* Marca + estado */}
      <div className="flex items-center gap-3">
        <img src="/Logo-Arus.jpeg" alt="Logo Arus" className="w-7 h-7 rounded object-cover shadow-sm" />
        <h1 className="text-base font-black text-gray-900 dark:text-gray-100 tracking-[0.25em] uppercase">ARUS</h1>
        <div className="hidden md:flex items-center gap-3 text-[9px] font-bold tracking-widest text-gray-400 dark:text-gray-500 border-l border-gray-200 dark:border-gray-800 pl-3">
          <span>UP {uptime}</span>
          <span className="text-gray-600 dark:text-gray-300 flex items-center gap-1.5">
            <span className="w-1.5 h-1.5 rounded-full bg-emerald-500 shadow-[0_0_6px_rgba(16,185,129,0.6)] animate-pulse"></span>
            EN LÍNEA
          </span>
        </div>
      </div>

      {/* Modo del radar */}
      <span
        className={`hidden lg:inline-flex text-[9px] font-bold tracking-[0.18em] px-2.5 py-1 rounded-full border ${
          autopilot
            ? "border-emerald-300 dark:border-emerald-500/40 text-emerald-600 dark:text-emerald-400 bg-emerald-50 dark:bg-emerald-500/10"
            : "border-gray-200 dark:border-gray-700 text-gray-400 dark:text-gray-500"
        }`}
        title={autopilot ? "El radar detecta Y ejecuta el mejor ciclo de tu universo" : "El radar solo detecta; ejecuta el modo clásico del par"}
      >
        {autopilot ? "AUTOPILOTO ON" : "SOLO DETECTA"}
      </span>

      {/* Patrimonio + PnL — el contador anima al ganar */}
      <div className="ml-auto flex items-baseline gap-2.5 font-mono" style={{ fontVariantNumeric: "tabular-nums" }}>
        <span ref={wealthRef} className="text-base sm:text-lg font-black text-gray-900 dark:text-gray-100">{fmtUSD(totalWealth)}</span>
        {pnl !== null && (
          <span className={`text-[11px] font-bold ${pnl >= 0 ? "text-emerald-600 dark:text-emerald-400" : "text-red-500"}`}>
            {pnl >= 0 ? "+" : "-"}{fmtUSD(Math.abs(pnl)).slice(0)}
          </span>
        )}
      </div>

      {/* Tabs de vista */}
      <nav className="flex border border-gray-200 dark:border-gray-700 rounded-lg overflow-hidden" role="tablist" aria-label="Vista">
        <button role="tab" aria-selected={view === "radar"} onClick={() => onViewChange("radar")} className={tabCls(view === "radar")}>
          <Radar className="w-3.5 h-3.5" /> <span className="hidden sm:inline">Radar</span>
        </button>
        <button role="tab" aria-selected={view === "dashboard"} onClick={() => onViewChange("dashboard")} className={tabCls(view === "dashboard")}>
          <LayoutDashboard className="w-3.5 h-3.5" /> <span className="hidden sm:inline">Dashboard</span>
        </button>
      </nav>

      {/* Acciones globales */}
      <div className="flex items-center gap-2">
        <button
          onClick={onProbar}
          className="px-3 py-2 bg-red-600 text-white rounded-md font-black text-[10px] uppercase tracking-widest flex items-center gap-1.5 shadow-sm hover:bg-red-500 transition-all duration-300 hover:-translate-y-0.5 hover:shadow-lg hover:shadow-red-500/30 active:scale-95"
        >
          <ShieldAlert className="w-3.5 h-3.5" />
          <span className="hidden sm:inline">Probar el bot</span>
          <span className="sm:hidden">Probar</span>
        </button>
        <button
          onClick={onEstrategia}
          className="px-3 py-2 rounded-md font-bold text-[10px] uppercase tracking-widest flex items-center gap-1.5 shadow-sm border border-violet-200 dark:border-violet-500/30 bg-violet-50 dark:bg-violet-500/10 text-violet-700 dark:text-violet-300 hover:bg-violet-100 dark:hover:bg-violet-500/20 transition-all duration-300 hover:-translate-y-0.5 active:scale-95"
        >
          <SlidersHorizontal className="w-3.5 h-3.5" />
          <span className="hidden sm:inline">Estrategia</span>
        </button>
        <button
          onClick={onTutorial}
          title="Ver el tutorial de uso"
          className="p-2 bg-gray-100 dark:bg-gray-800 text-gray-600 dark:text-gray-300 rounded-md hover:bg-gray-200 dark:hover:bg-gray-700 transition-colors border border-gray-200 dark:border-gray-700"
        >
          <BookOpen className="w-4 h-4" />
        </button>
        <button
          onClick={onToggleDark}
          aria-label="Alternar modo oscuro"
          className="p-2 bg-gray-100 dark:bg-gray-800 text-gray-600 dark:text-gray-300 rounded-md hover:bg-gray-200 dark:hover:bg-gray-700 transition-colors border border-gray-200 dark:border-gray-700"
        >
          {isDarkMode ? <Sun className="w-4 h-4" /> : <Moon className="w-4 h-4" />}
        </button>
        <button
          onClick={onReset}
          title="Borrar todo y volver al inicio"
          className="p-2 bg-gray-100 dark:bg-gray-800 text-red-500 dark:text-red-400 rounded-md hover:bg-red-50 dark:hover:bg-red-950/40 transition-colors border border-red-200 dark:border-red-900/50"
        >
          <RotateCcw className="w-4 h-4" />
        </button>
      </div>
    </header>
  );
}
