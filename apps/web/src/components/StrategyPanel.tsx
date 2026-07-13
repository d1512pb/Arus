"use client";

import { useEffect, useMemo, useState } from "react";
import { SlidersHorizontal, ChevronDown, ChevronRight, Check, Radar } from "lucide-react";
import { TradingParams, GraphSnapshot } from "../hooks/useArusEngine";
import { analyzeUniverseClient } from "../lib/universeCapability";

// StrategyPanel — personalización de la estrategia EN VIVO (Fase 0).
// El usuario define su apetito de riesgo: margen mínimo, tamaño máximo de orden,
// slippage estimado, comisiones por casa de cambio y el multiplicador de riesgo
// del crédito. El backend clampea todo a rangos sanos y responde con lo APLICADO,
// que es lo que este panel vuelve a pintar (nunca se muestra un valor no vigente).

interface Props {
  params: TradingParams | null;
  // graph aporta el CATÁLOGO de venues/activos disponibles (los nodos del radar)
  // para pintar el universo del usuario.
  graph: GraphSnapshot | null;
  onApply: (p: TradingParams) => void;
  // embedded (rediseño Radar-first): sin tarjeta colapsable propia — el
  // contenido se monta directo dentro del StrategyDrawer, siempre visible.
  embedded?: boolean;
}

// Campos numéricos editables como strings (permiten borrar/escribir libremente);
// se convierten al aplicar.
interface FormState {
  minNet: string;
  maxOrder: string;
  slippageBps: string;
  risk: string;
  // Filtros de seguridad (circuit breakers), en % para legibilidad.
  spikePct: string;      // spike_tick_deviation × 100
  divergencePct: string; // (max_divergence_ratio − 1) × 100
  // Préstamo parametrizado: los términos del crédito los define el usuario.
  creditLineUsd: string;
  creditLineBtc: string;
  creditAprPct: string; // credit_apr × 100
  creditFee: string;
  creditDurationMin: string;
  // Física del simulador: probabilidad de fallo de orden, en %.
  failureProbPct: string;
  fees: Record<string, string>;
  autopilot: boolean;
  venues: string[]; // universo seleccionado (vacío antes de conocer catálogo)
  assets: string[];
}

function fromParams(p: TradingParams, catalogVenues: string[], catalogAssets: string[]): FormState {
  const fees: Record<string, string> = {};
  for (const [venue, fee] of Object.entries(p.taker_fees ?? {})) {
    fees[venue] = (fee * 100).toString(); // fracción → %
  }
  return {
    minNet: p.min_net_profit_usd.toString(),
    maxOrder: p.max_order_size_btc.toString(),
    slippageBps: (p.slippage_rate * 10000).toString(), // fracción → bps
    risk: p.risk_multiplier.toString(),
    spikePct: (p.spike_tick_deviation * 100).toString(),
    divergencePct: ((p.max_divergence_ratio - 1) * 100).toFixed(0),
    creditLineUsd: (p.credit_line_usd ?? 50000).toString(),
    creditLineBtc: (p.credit_line_btc ?? 1).toString(),
    creditAprPct: ((p.credit_apr ?? 0.1) * 100).toString(),
    creditFee: (p.credit_origination_fee ?? 25).toString(),
    creditDurationMin: (p.credit_duration_min ?? 1).toString(),
    failureProbPct: ((p.order_failure_prob ?? 0.05) * 100).toString(),
    fees,
    autopilot: p.radar_autopilot === true,
    // Lista vacía en el servidor = "todos": se materializa con el catálogo.
    venues: p.enabled_venues?.length ? p.enabled_venues : catalogVenues,
    assets: p.enabled_assets?.length ? p.enabled_assets : catalogAssets,
  };
}

// Presets de estrategia: rellenan el formulario (SIN aplicar — el usuario revisa
// y pulsa Aplicar). No tocan fees, universo ni crédito: son perfiles de apetito
// de riesgo, no de mercado.
const PRESETS: Record<string, Partial<FormState> & { hint: string }> = {
  Conservador: {
    minNet: "5", maxOrder: "0.002", slippageBps: "10", risk: "5",
    spikePct: "2", divergencePct: "10",
    hint: "Solo jugadas claras: margen alto, órdenes chicas y filtros estrictos.",
  },
  Balanceado: {
    minNet: "0.1", maxOrder: "0.005", slippageBps: "5", risk: "2",
    spikePct: "5", divergencePct: "20",
    hint: "El punto medio: los defaults del motor con un crédito prudente.",
  },
  Agresivo: {
    minNet: "0.01", maxOrder: "0.05", slippageBps: "3", risk: "1",
    spikePct: "10", divergencePct: "50",
    hint: "Captura hasta lo mínimo: margen simbólico, órdenes grandes, crédito al límite.",
  },
};

const num = (s: string) => {
  const v = parseFloat(s);
  return Number.isFinite(v) ? v : 0;
};

export function StrategyPanel({ params, graph, onApply, embedded = false }: Props) {
  const [open, setOpen] = useState(false);
  const [form, setForm] = useState<FormState | null>(null);
  const [dirty, setDirty] = useState(false);
  const [justApplied, setJustApplied] = useState(false);
  const [universeError, setUniverseError] = useState<string | null>(null);

  // Catálogo de venues/activos disponibles, derivado de los nodos del radar.
  const catalogVenues: string[] = [];
  const catalogAssets: string[] = [];
  for (const n of graph?.nodes ?? []) {
    if (!catalogVenues.includes(n.venue)) catalogVenues.push(n.venue);
    if (!catalogAssets.includes(n.asset)) catalogAssets.push(n.asset);
  }

  // Sincroniza el formulario con los parámetros VIGENTES del servidor mientras el
  // usuario no esté editando (dirty). Tras aplicar, el PARAMS_UPDATED del backend
  // refresca params y aquí se pintan los valores post-clamp.
  useEffect(() => {
    if (params && !dirty) {
      setForm(fromParams(params, catalogVenues, catalogAssets));
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [params, dirty, graph === null]);

  // Antes del early return: el orden de hooks no puede depender de params/form.
  const draftCap = useMemo(() => {
    if (!form) return null;
    const allV = catalogVenues.length > 0 && form.venues.length === catalogVenues.length;
    const allA = catalogAssets.length > 0 && form.assets.length === catalogAssets.length;
    return analyzeUniverseClient(allV ? [] : form.venues, allA ? [] : form.assets, catalogVenues);
  }, [form, catalogVenues, catalogAssets]);

  if (!params || !form) return null;

  const edit = (patch: Partial<FormState>) => {
    setForm(prev => (prev ? { ...prev, ...patch } : prev));
    setDirty(true);
    setJustApplied(false);
    setUniverseError(null);
  };

  const toggleIn = (list: string[], item: string) =>
    list.includes(item) ? list.filter(x => x !== item) : [...list, item];

  const handleApply = () => {
    const fees: Record<string, number> = {};
    for (const [venue, pct] of Object.entries(form.fees)) {
      fees[venue] = num(pct) / 100; // % → fracción
    }
    const allV = catalogVenues.length > 0 && form.venues.length === catalogVenues.length;
    const allA = catalogAssets.length > 0 && form.assets.length === catalogAssets.length;
    const enabledVenues = allV ? [] : form.venues;
    const enabledAssets = allA ? [] : form.assets;
    const cap = analyzeUniverseClient(enabledVenues, enabledAssets, catalogVenues);
    if (!cap.ok) {
      setUniverseError(cap.reason);
      return;
    }
    setUniverseError(null);
    onApply({
      ...params,
      taker_fees: fees,
      min_net_profit_usd: num(form.minNet),
      max_order_size_btc: num(form.maxOrder),
      slippage_rate: num(form.slippageBps) / 10000,
      risk_multiplier: num(form.risk),
      spike_tick_deviation: num(form.spikePct) / 100,
      max_divergence_ratio: 1 + num(form.divergencePct) / 100,
      credit_line_usd: num(form.creditLineUsd),
      credit_line_btc: num(form.creditLineBtc),
      credit_apr: num(form.creditAprPct) / 100,
      credit_origination_fee: num(form.creditFee),
      credit_duration_min: num(form.creditDurationMin),
      order_failure_prob: num(form.failureProbPct) / 100,
      enabled_venues: enabledVenues,
      enabled_assets: enabledAssets,
      radar_autopilot: form.autopilot,
    });
    setDirty(false);
    setJustApplied(true);
    setTimeout(() => setJustApplied(false), 3000);
  };

  const inputCls =
    "w-full bg-gray-50 dark:bg-gray-950 border border-gray-300 dark:border-gray-700 text-gray-900 dark:text-gray-100 p-2.5 rounded-lg outline-none focus:border-violet-500 focus:ring-1 focus:ring-violet-500 transition-colors font-mono text-sm shadow-sm";
  const labelCls = "text-[10px] font-bold tracking-widest text-gray-500 dark:text-gray-400 uppercase";
  const hintCls = "text-[10px] text-gray-400 dark:text-gray-500 mt-1 leading-relaxed";

  // Cuerpo del formulario — compartido entre la tarjeta clásica del dashboard
  // y el StrategyDrawer del radar (embedded).
  const body = (
    <div className={embedded ? "" : "border-t border-gray-100 dark:border-gray-800 px-4 sm:px-6 py-5"}>
          {/* Presets: el rango completo de la parametrización en un click.
              Solo rellenan el formulario — nada se aplica sin revisar. */}
          <div className="mb-5 flex flex-wrap items-center gap-2">
            <span className={labelCls}>Perfiles rápidos</span>
            {Object.entries(PRESETS).map(([name, preset]) => (
              <button
                key={name}
                onClick={() => {
                  const { hint: _hint, ...fields } = preset;
                  edit(fields);
                }}
                title={preset.hint}
                className="px-3 py-1.5 rounded-full text-[11px] font-bold border border-violet-200 dark:border-violet-500/30 text-violet-600 bg-violet-50 dark:bg-violet-500/10 hover:bg-violet-600 hover:text-white transition-colors"
              >
                {name}
              </button>
            ))}
            <span className={`${hintCls} mt-0 basis-full sm:basis-auto`}>Rellenan el formulario; revisa y pulsa Aplicar.</span>
          </div>

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

          {/* Filtros de seguridad (circuit breakers): antes existían en el motor
              sin control en la UI — ahora el usuario ajusta sus propias compuertas. */}
          <div className="mt-5 pt-4 border-t border-gray-100 dark:border-gray-800">
            <p className={`${labelCls} mb-3`}>Filtros de seguridad (circuit breakers)</p>
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
              <div>
                <label className={labelCls}>Spike Filter: salto máx. por tick (%)</label>
                <input type="number" step="0.5" min="0.5" max="50" value={form.spikePct}
                  onChange={e => edit({ spikePct: e.target.value })} className={`${inputCls} mt-1`} />
                <p className={hintCls}>Si el precio salta más que esto entre dos ticks, el dato se descarta como corrupto. Estricto = opera solo con datos suaves.</p>
              </div>
              <div>
                <label className={labelCls}>Divergencia máx. entre casas (%)</label>
                <input type="number" step="1" min="1" max="100" value={form.divergencePct}
                  onChange={e => edit({ divergencePct: e.target.value })} className={`${inputCls} mt-1`} />
                <p className={hintCls}>Compuerta de cordura: si un exchange se aleja del otro más que esto, algo está roto y no se opera.</p>
              </div>
            </div>
          </div>

          {/* Préstamo parametrizado: los términos del crédito los define el usuario.
              El multiplicador de riesgo (arriba) decide CUÁNDO endeudarse; esto
              define QUÉ préstamo se pide. */}
          <div className="mt-5 pt-4 border-t border-gray-100 dark:border-gray-800">
            <p className={`${labelCls} mb-3`}>Tu línea de crédito (términos del préstamo)</p>
            <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-5 gap-4">
              <div>
                <label className={labelCls}>Línea (USD)</label>
                <input type="number" step="1000" min="1000" max="1000000" value={form.creditLineUsd}
                  onChange={e => edit({ creditLineUsd: e.target.value })} className={`${inputCls} mt-1`} />
              </div>
              <div>
                <label className={labelCls}>Línea (BTC)</label>
                <input type="number" step="0.1" min="0" max="100" value={form.creditLineBtc}
                  onChange={e => edit({ creditLineBtc: e.target.value })} className={`${inputCls} mt-1`} />
              </div>
              <div>
                <label className={labelCls}>Tasa anual (%)</label>
                <input type="number" step="0.5" min="0" max="100" value={form.creditAprPct}
                  onChange={e => edit({ creditAprPct: e.target.value })} className={`${inputCls} mt-1`} />
              </div>
              <div>
                <label className={labelCls}>Fee de apertura (USD)</label>
                <input type="number" step="5" min="0" max="1000" value={form.creditFee}
                  onChange={e => edit({ creditFee: e.target.value })} className={`${inputCls} mt-1`} />
              </div>
              <div>
                <label className={labelCls}>Plazo (min)</label>
                <input type="number" step="0.25" min="0.25" max="60" value={form.creditDurationMin}
                  onChange={e => edit({ creditDurationMin: e.target.value })} className={`${inputCls} mt-1`} />
              </div>
            </div>
            <p className={hintCls}>
              Costo por activación = fee de apertura + interés prorrateado al plazo. El bot solo se endeuda si la ganancia proyectada supera ese costo × tu multiplicador de riesgo.
            </p>
          </div>

          {/* Física del simulador: qué tan hostil es el mercado simulado. */}
          <div className="mt-5 pt-4 border-t border-gray-100 dark:border-gray-800">
            <p className={`${labelCls} mb-3`}>Simulador</p>
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
              <div>
                <label className={labelCls}>Prob. de fallo de orden (%)</label>
                <input type="number" step="1" min="0" max="50" value={form.failureProbPct}
                  onChange={e => edit({ failureProbPct: e.target.value })} className={`${inputCls} mt-1`} />
                <p className={hintCls}>Probabilidad de que una orden no se llene (Fill-or-Kill) y salte el circuit breaker. 0 = corrida limpia; alto = estrés máximo.</p>
              </div>
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

          {/* Universo del usuario (hito 3): con qué exchanges y monedas juega */}
          {catalogVenues.length > 0 && (
            <div className="mt-5 pt-4 border-t border-gray-100 dark:border-gray-800">
              <p className={`${labelCls} mb-3`}>Tu universo: elige con qué jugar</p>
              <div className="flex flex-wrap gap-4">
                <div>
                  <p className="text-[10px] text-gray-400 dark:text-gray-500 mb-1.5">Casas de cambio</p>
                  <div className="flex flex-wrap gap-2">
                    {catalogVenues.map(v => (
                      <button
                        key={v}
                        onClick={() => edit({ venues: toggleIn(form.venues, v) })}
                        className={`px-3 py-1.5 rounded-full text-[11px] font-bold border transition-colors ${form.venues.includes(v) ? "bg-violet-600 text-white border-violet-600" : "bg-gray-50 dark:bg-gray-950 text-gray-400 border-gray-200 dark:border-gray-700 line-through"}`}
                      >
                        {v}
                      </button>
                    ))}
                  </div>
                </div>
                <div>
                  <p className="text-[10px] text-gray-400 dark:text-gray-500 mb-1.5">Monedas</p>
                  <div className="flex flex-wrap gap-2">
                    {catalogAssets.map(a => (
                      <button
                        key={a}
                        onClick={() => edit({ assets: toggleIn(form.assets, a) })}
                        className={`px-3 py-1.5 rounded-full text-[11px] font-bold border transition-colors ${form.assets.includes(a) ? "bg-violet-600 text-white border-violet-600" : "bg-gray-50 dark:bg-gray-950 text-gray-400 border-gray-200 dark:border-gray-700 line-through"}`}
                      >
                        {a}
                      </button>
                    ))}
                  </div>
                </div>
              </div>
              <p className={hintCls}>El radar solo busca ciclos dentro de tu universo. Deseleccionar todo equivale a permitir todo. Hace falta al menos un ciclo espacial (2 casas + BTC) o triangular (1 casa + BTC + ETH/SOL).</p>
              {(universeError || (draftCap && !draftCap.ok)) && (
                <p className="mt-2 text-[11px] text-amber-700 dark:text-amber-300 bg-amber-50 dark:bg-amber-500/10 border border-amber-200 dark:border-amber-500/30 rounded-lg px-3 py-2 leading-relaxed">
                  {universeError ?? draftCap?.reason}
                </p>
              )}
            </div>
          )}

          {/* Autopiloto del radar (hito 3): detección → ejecución */}
          <div className="mt-5 pt-4 border-t border-gray-100 dark:border-gray-800">
            <div className="flex items-center gap-3 p-3 rounded-lg border border-gray-200 dark:border-gray-800 bg-gray-50 dark:bg-gray-950">
              <Radar className={`w-5 h-5 flex-shrink-0 ${form.autopilot ? "text-emerald-500" : "text-gray-400"}`} />
              <div className="flex-1">
                <p className="text-sm font-bold text-gray-900 dark:text-gray-100">Autopiloto del radar</p>
                <p className="text-[10px] text-gray-500 dark:text-gray-400 mt-0.5 leading-relaxed">
                  {form.autopilot
                    ? "El bot EJECUTA automáticamente el mejor ciclo de tu universo (espacial o triangular), con tus comisiones y tu margen. Sustituye al modo clásico del par."
                    : "Apagado: el radar solo detecta y muestra los ciclos; la ejecución sigue en el modo clásico (par BTC entre Binance y Bitso)."}
                </p>
              </div>
              <button
                onClick={() => edit({ autopilot: !form.autopilot })}
                role="switch"
                aria-checked={form.autopilot}
                className={`w-12 h-6 rounded-full transition-colors relative flex items-center px-1 flex-shrink-0 ${form.autopilot ? "bg-emerald-500" : "bg-gray-300 dark:bg-gray-700"}`}
              >
                <div className={`w-4 h-4 bg-white rounded-full transition-transform transform ${form.autopilot ? "translate-x-6" : "translate-x-0"}`} />
              </button>
            </div>
          </div>

          <div className="mt-5 flex items-center justify-between gap-3">
            <p className="text-[10px] text-gray-400 dark:text-gray-500 leading-relaxed max-w-md">
              El servidor valida y acota cada valor a rangos seguros; lo que ves aquí después de aplicar es lo que realmente quedó vigente.
            </p>
            <button
              onClick={handleApply}
              disabled={!dirty || (draftCap !== null && !draftCap.ok)}
              className={`flex items-center gap-2 px-5 py-2.5 rounded-lg font-bold text-xs uppercase tracking-widest text-white transition-all duration-300 shadow-sm disabled:opacity-40 disabled:cursor-not-allowed active:scale-95 ${justApplied ? "bg-emerald-500" : "bg-violet-600 hover:bg-violet-500"}`}
              title={draftCap && !draftCap.ok ? draftCap.reason : undefined}
            >
              {justApplied ? (<><Check className="w-4 h-4" /> Aplicado</>) : "Aplicar estrategia"}
            </button>
          </div>
    </div>
  );

  // Modo drawer (rediseño Radar-first): el contenido va directo, sin tarjeta.
  if (embedded) return body;

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
          {params.radar_autopilot && (
            <span className="hidden sm:inline-flex items-center gap-1 text-[10px] font-bold text-emerald-600 bg-emerald-50 dark:bg-emerald-500/10 border border-emerald-200 dark:border-emerald-500/20 px-2.5 py-1 rounded-full">
              <Radar className="w-3 h-3" /> Autopiloto
            </span>
          )}
          {open ? <ChevronDown className="w-5 h-5 text-gray-400" /> : <ChevronRight className="w-5 h-5 text-gray-400" />}
        </div>
      </button>

      {open && body}
    </div>
  );
}
