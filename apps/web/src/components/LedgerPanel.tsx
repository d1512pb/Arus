"use client";

import { useState, useCallback } from "react";
import { Database, RefreshCw, ChevronDown, ChevronRight, ShieldCheck } from "lucide-react";
import { ENGINE_HTTP_URL } from "../lib/config";

const LEDGER_URL = `${ENGINE_HTTP_URL}/api/ledger`;

interface TradeRecord {
  id: number;
  session_id: string;
  timestamp: string;
  buy_exchange: string;
  sell_exchange: string;
  volume_btc: number;
  spread_usd: number;
  net_profit_usd: number;
  is_credit_injection: boolean;
}

function formatTimestamp(ts: string): string {
  const d = new Date(ts);
  if (isNaN(d.getTime())) return ts;
  return d.toLocaleString("es-ES", { hour12: false });
}

export function LedgerPanel() {
  const [open, setOpen] = useState(false);
  const [records, setRecords] = useState<TradeRecord[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [loaded, setLoaded] = useState(false);

  const fetchLedger = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const res = await fetch(LEDGER_URL, { cache: "no-store" });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data = await res.json();
      setRecords(Array.isArray(data) ? data : []);
      setLoaded(true);
    } catch {
      setError("No se pudo leer el historial. Asegúrate de que el bot esté encendido.");
    } finally {
      setLoading(false);
    }
  }, []);

  const toggle = () => {
    const next = !open;
    setOpen(next);
    if (next && !loaded) fetchLedger();
  };

  return (
    <div className="mt-6 bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-800 rounded-xl shadow-sm overflow-hidden animate-fade-in-up" style={{ animationDelay: "0.5s" }}>
      {/* Cabecera / botón para colapsar */}
      <button
        onClick={toggle}
        className="w-full flex items-center justify-between gap-3 px-4 sm:px-6 py-4 hover:bg-gray-50 dark:hover:bg-gray-800/40 transition-colors"
      >
        <div className="flex items-center gap-3 text-left">
          <div className="w-9 h-9 rounded-lg bg-blue-600/10 flex items-center justify-center flex-shrink-0">
            <Database className="w-4 h-4 text-blue-600" />
          </div>
          <div>
            <h2 className="text-sm font-bold text-gray-900 dark:text-gray-100 tracking-widest uppercase">Historial / Auditoría</h2>
            <p className="text-[10px] text-gray-500 dark:text-gray-400 mt-0.5">
              Registro permanente de operaciones guardado en la base de datos
            </p>
          </div>
        </div>
        <div className="flex items-center gap-3 flex-shrink-0">
          {loaded && !error && (
            <span className="hidden sm:inline-flex items-center gap-1.5 text-[10px] font-bold text-emerald-600 bg-emerald-50 dark:bg-emerald-500/10 border border-emerald-200 dark:border-emerald-500/20 px-2.5 py-1 rounded-full">
              <ShieldCheck className="w-3 h-3" /> {records.length} guardados
            </span>
          )}
          {open ? <ChevronDown className="w-5 h-5 text-gray-400" /> : <ChevronRight className="w-5 h-5 text-gray-400" />}
        </div>
      </button>

      {/* Contenido colapsable */}
      {open && (
        <div className="border-t border-gray-100 dark:border-gray-800">
          <div className="px-4 sm:px-6 py-3 flex items-center justify-between gap-3 border-b border-gray-50 dark:border-gray-800/50 bg-gray-50/50 dark:bg-gray-950/30">
            <p className="text-[10px] sm:text-xs text-gray-500 dark:text-gray-400 leading-relaxed">
              Estos datos siguen aquí aunque reinicies el bot o cierres el navegador. Son la prueba de que el sistema guarda la información de forma permanente.
            </p>
            <button
              onClick={fetchLedger}
              disabled={loading}
              className="flex items-center gap-2 text-[10px] sm:text-xs font-bold uppercase tracking-widest text-blue-600 hover:text-blue-500 disabled:opacity-50 flex-shrink-0 transition-colors"
            >
              <RefreshCw className={`w-3.5 h-3.5 ${loading ? "animate-spin" : ""}`} />
              <span className="hidden sm:inline">Actualizar</span>
            </button>
          </div>

          <div className="max-h-[360px] overflow-y-auto overscroll-contain">
            {error ? (
              <div className="px-6 py-10 text-center text-sm text-red-500">{error}</div>
            ) : loading && records.length === 0 ? (
              <div className="px-6 py-10 text-center text-sm text-gray-400">Cargando historial...</div>
            ) : records.length === 0 ? (
              <div className="px-6 py-10 text-center text-sm text-gray-400 dark:text-gray-500">
                Todavía no hay operaciones guardadas. Ejecuta una prueba o deja que el bot opere y vuelve aquí.
              </div>
            ) : (
              <table className="w-full text-left border-collapse text-xs">
                <thead className="sticky top-0 bg-white dark:bg-gray-900 z-10">
                  <tr className="text-[10px] text-gray-500 dark:text-gray-400 font-bold tracking-widest uppercase border-b border-gray-100 dark:border-gray-800">
                    <th className="px-4 sm:px-6 py-3 font-bold whitespace-nowrap">Fecha y hora</th>
                    <th className="px-3 py-3 font-bold whitespace-nowrap">Tipo</th>
                    <th className="px-3 py-3 font-bold whitespace-nowrap">Ruta (compra ➔ venta)</th>
                    <th className="px-3 py-3 font-bold text-right whitespace-nowrap">Cantidad (BTC)</th>
                    <th className="px-4 sm:px-6 py-3 font-bold text-right whitespace-nowrap">Ganancia</th>
                  </tr>
                </thead>
                <tbody>
                  {records.map((r) => (
                    <tr key={r.id} className="border-b border-gray-50 dark:border-gray-800/50 hover:bg-gray-50/50 dark:hover:bg-gray-800/20 transition-colors">
                      <td className="px-4 sm:px-6 py-2.5 text-gray-500 dark:text-gray-400 whitespace-nowrap font-mono">{formatTimestamp(r.timestamp)}</td>
                      <td className="px-3 py-2.5 whitespace-nowrap">
                        {r.is_credit_injection ? (
                          <span className="px-2 py-0.5 rounded text-[10px] font-bold bg-blue-50 dark:bg-blue-500/10 text-blue-600 border border-blue-200 dark:border-blue-500/20">Préstamo / Reequilibrio</span>
                        ) : (
                          <span className="px-2 py-0.5 rounded text-[10px] font-bold bg-emerald-50 dark:bg-emerald-500/10 text-emerald-600 border border-emerald-200 dark:border-emerald-500/20">Operación</span>
                        )}
                      </td>
                      <td className="px-3 py-2.5 whitespace-nowrap text-gray-700 dark:text-gray-300 font-medium">
                        {r.buy_exchange} <span className="text-gray-300 dark:text-gray-600">➔</span> {r.sell_exchange}
                      </td>
                      <td className="px-3 py-2.5 text-right whitespace-nowrap font-mono text-gray-700 dark:text-gray-300">{r.volume_btc.toFixed(4)}</td>
                      <td className="px-4 sm:px-6 py-2.5 text-right whitespace-nowrap font-black font-mono">
                        <span className={r.net_profit_usd >= 0 ? "text-emerald-600" : "text-red-500"}>
                          {r.net_profit_usd >= 0 ? "+" : "-"}${Math.abs(r.net_profit_usd).toFixed(2)}
                        </span>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
