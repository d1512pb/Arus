"use client";

import { useEffect } from "react";
import { SlidersHorizontal, X } from "lucide-react";
import { StrategyPanel } from "./StrategyPanel";
import { TradingParams, GraphSnapshot } from "../hooks/useArusEngine";

// StrategyDrawer — la estrategia UNIDA al radar (rediseño Radar-first, fase R1).
// Se abre SOBRE el grafo: al podar el universo o cambiar fees, el usuario ve al
// radar reaccionar en vivo detrás del panel. Reutiliza el formulario completo
// de StrategyPanel (modo embedded) y suma aquí el préstamo automático — es
// parte del apetito de riesgo del usuario, no un botón suelto del header.

interface Props {
  open: boolean;
  onClose: () => void;
  params: TradingParams | null;
  graph: GraphSnapshot | null;
  onApply: (p: TradingParams) => void;
  autoCreditMode: boolean;
  onToggleAutoCredit: () => void;
}

export function StrategyDrawer({ open, onClose, params, graph, onApply, autoCreditMode, onToggleAutoCredit }: Props) {
  // Cerrar con Escape (accesibilidad básica del drawer).
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") onClose(); };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  return (
    <>
      {/* Velo: el grafo sigue visible (y reaccionando) detrás */}
      <div
        className={`fixed inset-0 z-[94] bg-gray-950/40 backdrop-blur-[1px] transition-opacity duration-300 ${open ? "opacity-100" : "opacity-0 pointer-events-none"}`}
        onClick={onClose}
        aria-hidden="true"
      />
      <aside
        role="dialog"
        aria-label="Estrategia"
        aria-hidden={!open}
        className={`fixed top-0 right-0 z-[95] h-full w-full sm:w-[400px] bg-white dark:bg-gray-900 border-l border-gray-200 dark:border-gray-800 shadow-2xl transition-transform duration-300 ease-out flex flex-col ${open ? "translate-x-0" : "translate-x-full"}`}
      >
        <div className="flex items-center justify-between gap-3 px-4 sm:px-5 py-4 border-b border-gray-200 dark:border-gray-800 flex-shrink-0">
          <div className="flex items-center gap-3">
            <div className="w-9 h-9 rounded-lg bg-violet-600/10 flex items-center justify-center flex-shrink-0">
              <SlidersHorizontal className="w-4 h-4 text-violet-600" />
            </div>
            <div>
              <h2 className="text-sm font-bold text-gray-900 dark:text-gray-100 tracking-widest uppercase">Estrategia / Tu configuración</h2>
              <p className="text-[10px] text-gray-500 dark:text-gray-400 mt-0.5">
                El radar reacciona en vivo a lo que cambies aquí
              </p>
            </div>
          </div>
          <button
            onClick={onClose}
            aria-label="Cerrar estrategia"
            className="p-2 text-gray-400 hover:text-gray-700 dark:hover:text-gray-200 transition-colors"
          >
            <X className="w-5 h-5" />
          </button>
        </div>

        <div className="flex-1 overflow-y-auto overscroll-contain px-4 sm:px-5 py-4">
          {/* Préstamo automático: parte del apetito de riesgo del usuario */}
          <div className="flex items-center gap-3 p-3 rounded-lg border border-gray-200 dark:border-gray-800 bg-gray-50 dark:bg-gray-950 mb-4">
            <div className="flex-1">
              <p className="text-sm font-bold text-gray-900 dark:text-gray-100">Préstamo automático</p>
              <p className="text-[10px] text-gray-500 dark:text-gray-400 mt-0.5 leading-relaxed">
                {autoCreditMode
                  ? "Al quedarse sin fondos, pide crédito instantáneo si la ganancia supera tu umbral (costo × riesgo) — sin tiempos muertos."
                  : "Apagado: al quedarse sin fondos el bot te preguntará (o pausará ~30 min en producción para reequilibrar)."}
              </p>
            </div>
            <button
              onClick={onToggleAutoCredit}
              role="switch"
              aria-checked={autoCreditMode}
              aria-label="Préstamo automático"
              className={`w-12 h-6 rounded-full transition-colors relative flex items-center px-1 flex-shrink-0 ${autoCreditMode ? "bg-emerald-500" : "bg-gray-300 dark:bg-gray-700"}`}
            >
              <div className={`w-4 h-4 bg-white rounded-full transition-transform transform ${autoCreditMode ? "translate-x-6" : "translate-x-0"}`} />
            </button>
          </div>

          <StrategyPanel params={params} graph={graph} onApply={onApply} embedded />
        </div>
      </aside>
    </>
  );
}
