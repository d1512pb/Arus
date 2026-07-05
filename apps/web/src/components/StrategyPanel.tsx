"use client";

import { useEffect, useState } from "react";
import { SlidersHorizontal, ChevronDown, ChevronRight, Check } from "lucide-react";
import { TradingParams } from "../hooks/useArusEngine";

// StrategyPanel — personalización de la estrategia EN VIVO (Fase 0).
// El usuario define su apetito de riesgo: margen mínimo, tamaño máximo de orden,
// slippage estimado, comisiones por casa de cambio y el multiplicador de riesgo
// del crédito. El backend clampea todo a rangos sanos y responde con lo APLICADO,
// que es lo que este panel vuelve a pintar (nunca se muestra un valor no vigente).

interface Props {
  params: TradingParams | null;
  onApply: (p: TradingParams) => void;
}

// Campos numéricos editables como strings (permiten borrar/escribir libremente);
// se convierten al aplicar.
interface FormState {
  minNet: string;
  maxOrder: string;
  slippageBps: string;
  risk: string;
  fees: Record<string, string>;
}

function fromParams(p: TradingParams): FormState {
  const fees: Record<string, string> = {};
  for (const [venue, fee] of Object.entries(p.taker_fees ?? {})) {
    fees[venue] = (fee * 100).toString(); // fracción → %
  }
  return {
    minNet: p.min_net_profit_usd.toString(),
    maxOrder: p.max_order_size_btc.toString(),
    slippageBps: (p.slippage_rate * 10000).toString(), // fracción → bps
    risk: p.risk_multiplier.toString(),
    fees,
  };
}

const num = (s: string) => {
  const v = parseFloat(s);
  return Number.isFinite(v) ? v : 0;
};

export function StrategyPanel({ params, onApply }: Props) {
  const [open, setOpen] = useState(false);
  const [form, setForm] = useState<FormState | null>(null);
  const [dirty, setDirty] = useState(false);
  const [justApplied, setJustApplied] = useState(false);

  // Sincroniza el formulario con los parámetros VIGENTES del servidor mientras el
  // usuario no esté editando (dirty). Tras aplicar, el PARAMS_UPDATED del backend
  // refresca params y aquí se pintan los valores post-clamp.
  useEffect(() => {
    if (params && !dirty) {
      setForm(fromParams(params));
    }
  }, [params, dirty]);

  if (!params || !form) return null;

  const edit = (patch: Partial<FormState>) => {
    setForm(prev => (prev ? { ...prev, ...patch } : prev));
    setDirty(true);
    setJustApplied(false);
  };

  const handleApply = () => {
    const fees: Record<string, number> = {};
    for (const [venue, pct] of Object.entries(form.fees)) {
      fees[venue] = num(pct) / 100; // % → fracción
    }
    onApply({
      ...params,
      taker_fees: fees,
      min_net_profit_usd: num(form.minNet),
      max_order_size_btc: num(form.maxOrder),
      slippage_rate: num(form.slippageBps) / 10000, // bps → fracción
      risk_multiplier: num(form.risk),
    });
    setDirty(false);
    setJustApplied(true);
    setTimeout(() => setJustApplied(false), 3000);
  };

  const inputCls =
    "w-full bg-gray-50 dark:bg-gray-950 border border-gray-300 dark:border-gray-700 text-gray-900 dark:text-gray-100 p-2.5 rounded-lg outline-none focus:border-violet-500 focus:ring-1 focus:ring-violet-500 transition-colors font-mono text-sm shadow-sm";
  const labelCls = "text-[10px] font-bold tracking-widest text-gray-500 dark:text-gray-400 uppercase";
  const hintCls = "text-[10px] text-gray-400 dark:text-gray-500 mt-1 leading-relaxed";

  return (
    <div className="mt-6 bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-800 rounded-xl shadow-sm overflow-hidden animate-fade-in-up" style={{ animationDelay: "0.45s" }}>
      {/* Cabecera / botón para colapsar */}
      <button
        onClick={() => setOpen(!open)}
        className="w-full flex items-center justify-between gap-3 px-4 sm:px-6 py-4 hover:bg-gray-50 dark:hover:bg-gray-800/40 transition-colors"
      >
        <div className="flex items-center gap-3 text-left">
          <div className="w-9 h-9 rounded-lg bg-violet-600/10 flex items-center justify-center flex-shrink-0">
            <SlidersHorizontal className="w-4 h-4 text-violet-600" />
          </div>
          <div>
            <h2 className="text-sm font-bold text-gray-900 dark:text-gray-100 tracking-widest uppercase">Estrategia / Tu configuración</h2>
            <p className="text-[10px] text-gray-500 dark:text-gray-400 mt-0.5">
              Define tus propios umbrales: el bot decide con TUS reglas, no con las de otros
            </p>
          </div>
        </div>
        <div className="flex items-center gap-3 flex-shrink-0">
          <span className="hidden sm:inline-flex items-center gap-1.5 text-[10px] font-bold text-violet-600 bg-violet-50 dark:bg-violet-500/10 border border-violet-200 dark:border-violet-500/20 px-2.5 py-1 rounded-full">
            mín. ${params.min_net_profit_usd.toFixed(2)} · máx. {params.max_order_size_btc} BTC · riesgo {params.risk_multiplier}x
          </span>
          {open ? <ChevronDown className="w-5 h-5 text-gray-400" /> : <ChevronRight className="w-5 h-5 text-gray-400" />}
        </div>
      </button>

      {open && (
        <div className="border-t border-gray-100 dark:border-gray-800 px-4 sm:px-6 py-5">
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
            <div>
              <label className={labelCls}>Margen mínimo de ganancia (USD)</label>
              <input type="number" step="0.1" min="0" value={form.minNet}
                onChange={e => edit({ minNet: e.target.value })} className={`${inputCls} mt-1`} />
              <p className={hintCls}>Solo opera si la ganancia neta supera esto. Bajo = captura oportunidades pequeñas antes que nadie; alto = solo jugadas grandes.</p>
            </div>
            <div>
              <label className={labelCls}>Orden máxima (BTC)</label>
              <input type="number" step="0.001" min="0.0005" max="10" value={form.maxOrder}
                onChange={e => edit({ maxOrder: e.target.value })} className={`${inputCls} mt-1`} />
              <p className={hintCls}>Tope por operación. El bot además se ajusta a la liquidez real disponible en cada casa de cambio.</p>
            </div>
            <div>
              <label className={labelCls}>Slippage estimado (bps)</label>
              <input type="number" step="1" min="0" max="100" value={form.slippageBps}
                onChange={e => edit({ slippageBps: e.target.value })} className={`${inputCls} mt-1`} />
              <p className={hintCls}>Deslizamiento de precio que asumes por pierna (100 bps = 1%). Se descuenta ANTES de decidir si operar.</p>
            </div>
            <div>
              <label className={labelCls}>Riesgo del crédito (multiplicador)</label>
              <input type="number" step="0.5" min="1" max="100" value={form.risk}
                onChange={e => edit({ risk: e.target.value })} className={`${inputCls} mt-1`} />
              <p className={hintCls}>Pedir préstamo solo si la ganancia cubre el costo × este factor. 1 = agresivo (al límite); 5 = conservador.</p>
            </div>
          </div>

          <div className="mt-5 pt-4 border-t border-gray-100 dark:border-gray-800">
            <p className={`${labelCls} mb-3`}>Comisiones taker por casa de cambio (%)</p>
            <div className="grid grid-cols-2 sm:grid-cols-4 gap-4">
              {Object.keys(form.fees).sort().map(venue => (
                <div key={venue}>
                  <label className={labelCls}>{venue}</label>
                  <input type="number" step="0.01" min="0" max="5" value={form.fees[venue]}
                    onChange={e => edit({ fees: { ...form.fees, [venue]: e.target.value } })}
                    className={`${inputCls} mt-1`} />
                </div>
              ))}
            </div>
            <p className={hintCls}>Ajústalas si tu nivel de cuenta paga comisiones distintas a las estándar.</p>
          </div>

          <div className="mt-5 flex items-center justify-between gap-3">
            <p className="text-[10px] text-gray-400 dark:text-gray-500 leading-relaxed max-w-md">
              El servidor valida y acota cada valor a rangos seguros; lo que ves aquí después de aplicar es lo que realmente quedó vigente.
            </p>
            <button
              onClick={handleApply}
              disabled={!dirty}
              className={`flex items-center gap-2 px-5 py-2.5 rounded-lg font-bold text-xs uppercase tracking-widest text-white transition-all duration-300 shadow-sm disabled:opacity-40 disabled:cursor-not-allowed active:scale-95 ${justApplied ? "bg-emerald-500" : "bg-violet-600 hover:bg-violet-500"}`}
            >
              {justApplied ? (<><Check className="w-4 h-4" /> Aplicado</>) : "Aplicar estrategia"}
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
