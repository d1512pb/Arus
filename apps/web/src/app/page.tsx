"use client";

import { useCallback, useEffect, useMemo, useState, useRef } from "react";
import { ShieldAlert, CheckCircle, Loader2, ArrowRight, HelpCircle, X, Pencil, Zap, Landmark } from "lucide-react";
import { useArusEngine } from "../hooks/useArusEngine";
import { OnboardingModal } from "../components/OnboardingModal";
import { LedgerPanel } from "../components/LedgerPanel";
import { AnalyticsPanel } from "../components/AnalyticsPanel";
import { TutorialModal } from "../components/TutorialModal";
import { HeaderBar, AppView } from "../components/HeaderBar";
import { RadarView } from "../components/RadarView";
import { StrategyDrawer } from "../components/StrategyDrawer";
import { analyzeUniverseClient } from "../lib/universeCapability";

function logColor(level: string): string {
    switch(level) {
        case 'opportunity': return 'text-emerald-400';
        case 'arb':         return 'text-emerald-300 font-bold';
        case 'spike_warn':  return 'text-amber-400';
        case 'spike_block': return 'text-red-400';
        case 'waiting':     return 'text-gray-500';
        default:            return 'text-gray-300';
    }
}

// Colores de marca por casa de cambio para los badges del feed. Un venue fuera
// del mapa (recién agregado al registro del motor) cae al estilo neutro en vez
// de disfrazarse de otro exchange.
const VENUE_BADGE_STYLES: Record<string, string> = {
  Binance: "bg-[#D4A000]/10 text-[#D4A000] border-[#D4A000]/20",
  Bitso: "bg-[#0088FF]/10 text-[#0088FF] border-[#0088FF]/20",
  Kraken: "bg-[#5741D9]/10 text-[#5741D9] border-[#5741D9]/20",
};

function VenueBadge({ name }: { name: string }) {
  const style = VENUE_BADGE_STYLES[name] ?? "bg-gray-500/10 text-gray-500 border-gray-500/20";
  return (
    <span className={`px-2 py-0.5 rounded text-[10px] font-bold border ${style}`}>
      {name.toUpperCase()}
    </span>
  );
}

// ── FASE 1 (refactor Probar Bot): "Crear tu propia prueba" ──────────────────
// Una pierna del escenario custom: el top-of-book que el usuario define para un
// libro (casa + moneda + bid/ask). Se envía a POST /api/simulate/custom y el
// motor lo inyecta como tick REAL en su canal de ingesta.
type SimLeg = { venue: string; asset: string; bid: number | ""; ask: number | "" };

const simInputCls = "w-full bg-white dark:bg-gray-900 border border-gray-300 dark:border-gray-700 text-gray-900 dark:text-gray-100 p-2 rounded-lg outline-none focus:border-red-500 focus:ring-1 focus:ring-red-500 transition-colors font-mono text-xs shadow-sm";

// Editor de una pierna. Definido a nivel de módulo (no dentro de Home) para que
// los inputs no pierdan el foco en cada re-render del padre.
function SimLegEditor({ label, leg, venues, assets, inactive, onVenue, onAsset, onBid, onAsk }: {
  label: string;
  leg: SimLeg;
  venues: string[];
  assets: string[];
  inactive: boolean;
  onVenue: (v: string) => void;
  onAsset: (a: string) => void;
  onBid: (v: number | "") => void;
  onAsk: (v: number | "") => void;
}) {
  return (
    <div className={`bg-white dark:bg-gray-900 border rounded-lg p-3 ${inactive ? "border-amber-300 dark:border-amber-500/40" : "border-gray-200 dark:border-gray-800"}`}>
      <p className="text-[10px] font-black uppercase tracking-widest text-gray-500 dark:text-gray-400 mb-2">
        {label}
        {inactive && <span className="text-amber-600 dark:text-amber-400 tracking-normal"> · fuera de tu universo</span>}
      </p>
      <div className="grid grid-cols-2 gap-2 mb-2">
        <select value={leg.venue} onChange={e => onVenue(e.target.value)} className={simInputCls} aria-label={`${label}: casa de cambio`}>
          {venues.map(v => <option key={v} value={v}>{v}</option>)}
        </select>
        <select value={leg.asset} onChange={e => onAsset(e.target.value)} className={simInputCls} aria-label={`${label}: moneda`}>
          {assets.map(a => <option key={a} value={a}>{a}</option>)}
        </select>
      </div>
      <div className="grid grid-cols-2 gap-2">
        <div>
          <label className="text-[9px] font-bold tracking-widest text-gray-400 uppercase">Compra del mercado (bid)</label>
          <input
            type="number" step="any" value={leg.bid}
            onChange={e => onBid(e.target.value === "" ? "" : parseFloat(e.target.value) || 0)}
            className={simInputCls} placeholder="—"
          />
          <p className="text-[9px] text-gray-400 mt-0.5 leading-snug">Precio al que <em>tú vendes</em> en esta casa.</p>
        </div>
        <div>
          <label className="text-[9px] font-bold tracking-widest text-gray-400 uppercase">Venta del mercado (ask)</label>
          <input
            type="number" step="any" value={leg.ask}
            onChange={e => onAsk(e.target.value === "" ? "" : parseFloat(e.target.value) || 0)}
            className={simInputCls} placeholder="—"
          />
          <p className="text-[9px] text-gray-400 mt-0.5 leading-snug">Precio al que <em>tú compras</em> en esta casa.</p>
        </div>
      </div>
    </div>
  );
}

// Estado de una casa de cambio en el dashboard: INACTIVO (fuera del universo del
// usuario), SIN FONDOS (activa pero sin saldo propio) o ACTIVO. Antes las tarjetas
// no distinguían estos casos y una casa vacía/desactivada se veía como un bloque de
// ceros sin explicación.
// "hold" = fuera del universo PERO con fondos: el dinero está aquí en modo
// custodia, el bot no lo opera hasta que el usuario active la casa (FASE 7).
type VenueStatus = "active" | "inactive" | "empty" | "hold";
function VenueStatusPill({ status }: { status: VenueStatus }) {
  const map: Record<VenueStatus, { label: string; cls: string }> = {
    active: { label: "ACTIVO", cls: "border-emerald-300 dark:border-emerald-500/40 text-emerald-600 dark:text-emerald-400 bg-emerald-50 dark:bg-emerald-500/10" },
    inactive: { label: "INACTIVO", cls: "border-gray-300 dark:border-gray-600 text-gray-400 dark:text-gray-500 bg-gray-100 dark:bg-gray-800" },
    empty: { label: "SIN FONDOS", cls: "border-amber-300 dark:border-amber-500/40 text-amber-600 dark:text-amber-400 bg-amber-50 dark:bg-amber-500/10" },
    hold: { label: "SOLO HOLD", cls: "border-violet-300 dark:border-violet-500/40 text-violet-600 dark:text-violet-400 bg-violet-50 dark:bg-violet-500/10" },
  };
  const s = map[status];
  return (
    <span className={`text-[9px] font-bold uppercase tracking-widest rounded-full px-2 py-0.5 border ${s.cls}`}>
      {s.label}
    </span>
  );
}

// InactiveVenueCTA: bloque para una casa fuera del universo — explica el modo
// "Solo Hold" (el dinero está pero el bot no lo usa) y ofrece activarla en el
// motor de arbitraje con un click, sin ir hasta el drawer de Estrategia (FASE 7).
function InactiveVenueCTA({ onActivate }: { onActivate: () => void }) {
  return (
    <div className="mt-3 pt-3 border-t border-gray-100 dark:border-gray-800">
      <p className="text-[10px] text-gray-400 dark:text-gray-500 leading-relaxed mb-2">
        Tu dinero está aquí en modo <strong>Solo Hold</strong>: el bot no lo usa para arbitraje hasta que actives esta casa (o hazlo desde ⚙ Estrategia).
      </p>
      <button
        onClick={onActivate}
        className="w-full flex items-center justify-center gap-1.5 py-2 rounded-lg text-[10px] font-bold uppercase tracking-widest border border-emerald-300 dark:border-emerald-500/40 bg-emerald-50 dark:bg-emerald-500/10 text-emerald-700 dark:text-emerald-300 hover:bg-emerald-100 dark:hover:bg-emerald-500/20 transition-colors"
      >
        <Zap className="w-3 h-3" /> Activar en el Motor de Arbitraje
      </button>
    </div>
  );
}

const VENUE_CARD_ACCENT: Record<string, { badge: string; hover: string; crypto: string }> = {
  Binance: { badge: "bg-yellow-400 text-yellow-900", hover: "bg-yellow-400/0 group-hover:bg-yellow-400/5", crypto: "text-yellow-600" },
  Bitso: { badge: "bg-blue-600 text-white", hover: "bg-blue-600/0 group-hover:bg-blue-600/5", crypto: "text-blue-600" },
  Kraken: { badge: "bg-violet-600 text-white", hover: "bg-violet-600/0 group-hover:bg-violet-600/5", crypto: "text-violet-600" },
};

function formatAssetBalance(asset: string, amount: number): string {
  const cash = asset === "USD" || asset === "USDT";
  if (cash) return `$${amount.toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
  if (asset === "BTC") return `${amount.toFixed(4)} ₿`;
  return `${amount.toFixed(4)} ${asset}`;
}

function isCashAssetName(asset: string): boolean {
  return asset === "USD" || asset === "USDT";
}

function CreditToggle({ autoMode, onToggle }: { autoMode: boolean, onToggle: () => void }) {
  return (
    <div className="flex items-center gap-3 p-3 rounded-lg border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 shadow-sm mt-4">
      <div className="flex-1">
        <p className="text-sm font-bold text-gray-900 dark:text-gray-100">Préstamo Automático</p>
        <p className="text-[10px] text-gray-500 mt-1">
          {autoMode
            ? "Al quedarse sin fondos, usa un préstamo para evitar la demora de >30 min en reequilibrar, operando solo si es rentable."
            : "El bot se pausará >30 min si se queda sin fondos. Actívalo para usar préstamos instantáneos y evitar tiempos muertos."}
        </p>
      </div>
      <button 
        onClick={onToggle}
        className={`w-12 h-6 rounded-full transition-colors relative flex items-center px-1 ${autoMode ? 'bg-emerald-500' : 'bg-gray-300 dark:bg-gray-700'}`}
      >
        <div className={`w-4 h-4 bg-white rounded-full transition-transform transform ${autoMode ? 'translate-x-6' : 'translate-x-0'}`} />
      </button>
    </div>
  )
}

function useCountdown(expiresAt: Date | null) {
  const [remaining, setRemaining] = useState(0);
  useEffect(() => {
    if (!expiresAt) { setRemaining(0); return; }
    const tick = () => setRemaining(Math.max(0, Math.ceil((expiresAt.getTime() - Date.now()) / 1000)));
    tick();
    const id = setInterval(tick, 500);
    return () => clearInterval(id);
  }, [expiresAt]);
  return remaining;
}

function formatCountdown(seconds: number) {
  const m = Math.floor(seconds / 60);
  const s = seconds % 60;
  return `${m}:${s.toString().padStart(2, "0")}`;
}

function CreditActiveBanner({ expiresAt, depleted }: { expiresAt: Date | null, depleted?: boolean }) {
  const remaining = useCountdown(expiresAt);
  if (depleted) {
    return (
      <div className="relative z-10 transition-all duration-500 overflow-hidden opacity-100">
        <div className="bg-orange-600 text-white font-bold px-4 py-3 flex flex-col sm:flex-row items-center justify-center gap-2 sm:gap-4 shadow-md border-b border-orange-700">
          <div className="flex items-center gap-3">
            <div className="w-2.5 h-2.5 rounded-full bg-orange-300 animate-pulse shadow-[0_0_8px_rgba(253,186,116,0.8)]" />
            <span className="tracking-widest text-xs sm:text-sm uppercase text-center">
              FONDOS DEL PRÉSTAMO AGOTADOS — ESPERANDO FIN DE PLAZO
            </span>
          </div>
          {remaining > 0 && (
            <span className="text-orange-200 text-[10px] sm:text-xs font-mono bg-orange-700/50 px-3 py-1 rounded-full">
              Reequilibrio en {formatCountdown(remaining)}
            </span>
          )}
        </div>
      </div>
    );
  }
  return (
    <div className="relative z-10 transition-all duration-500 overflow-hidden opacity-100">
      <div className="bg-blue-600 text-white font-bold px-4 py-3 flex flex-col sm:flex-row items-center justify-center gap-2 sm:gap-4 shadow-md border-b border-blue-700">
        <div className="flex items-center gap-3">
          <div className="w-2.5 h-2.5 rounded-full bg-blue-300 animate-pulse shadow-[0_0_8px_rgba(147,197,253,0.8)]" />
          <span className="tracking-widest text-xs sm:text-sm uppercase text-center">
            PRÉSTAMO ACTIVO — OPERANDO CON FONDOS PRESTADOS
          </span>
        </div>
        {remaining > 0 && (
          <span className="text-blue-200 text-[10px] sm:text-xs font-mono bg-blue-700/50 px-3 py-1 rounded-full">
            Reequilibrio en curso · {formatCountdown(remaining)} restante (demo: 1 min · prod: ~30+ min)
          </span>
        )}
      </div>
    </div>
  );
}

function ReplenishingBanner({ expiresAt, message }: { expiresAt: Date | null; message: string }) {
  const remaining = useCountdown(expiresAt);
  return (
    <div className="relative z-10 transition-all duration-500 overflow-hidden opacity-100">
      <div className="bg-amber-600 text-white font-bold px-4 py-3 flex flex-col sm:flex-row items-center justify-center gap-2 sm:gap-4 shadow-md border-b border-amber-700">
        <div className="flex items-center gap-3">
          <div className="w-2.5 h-2.5 rounded-full bg-amber-300 animate-pulse" />
          <span className="tracking-widest text-xs sm:text-sm uppercase text-center">OPERACIONES PAUSADAS — REEQUILIBRANDO FONDOS</span>
        </div>
        {remaining > 0 && (
          <span className="text-amber-100 text-[10px] sm:text-xs font-mono bg-amber-700/50 px-3 py-1 rounded-full">
            {formatCountdown(remaining)} · {message}
          </span>
        )}
      </div>
    </div>
  );
}

function InsufficientFundsModal({
  open, profitPotential, creditCost, creditRequired, onRequestCredit, onWaitRebalance, onContinue, onShutdown
}: {
  open: boolean;
  profitPotential: number;
  creditCost: number;
  creditRequired: number;
  onRequestCredit: () => void;
  onWaitRebalance: () => void;
  onContinue: () => void;
  onShutdown: () => void;
}) {
  const [loading, setLoading] = useState<"credit" | "wait" | null>(null);

  useEffect(() => {
    if (open) setLoading(null);
  }, [open]);

  if (!open) return null;
  // Umbral real de la decisión: costo del préstamo × multiplicador de riesgo del
  // usuario (configurable en el panel de Estrategia). Con multiplicador 1x equivale
  // al costo a secas.
  const threshold = creditRequired > 0 ? creditRequired : creditCost;
  const isProfitable = profitPotential > threshold;
  const handleRequestCredit = () => { setLoading("credit"); onRequestCredit(); };
  const handleWait = () => { setLoading("wait"); onWaitRebalance(); };
  return (
    <div className="fixed inset-0 z-[110] flex items-center justify-center bg-gray-900/60 backdrop-blur-sm p-4">
      <div className="bg-white dark:bg-gray-900 border border-orange-500/50 rounded-xl max-w-md w-full p-6 shadow-2xl animate-modal-scale">
        <div className="flex items-center gap-3 mb-4">
          <div className="w-10 h-10 rounded-full bg-orange-500/10 flex items-center justify-center flex-shrink-0">
            <ShieldAlert className="w-5 h-5 text-orange-500 animate-pulse" />
          </div>
          <div>
            <h2 className="font-bold text-gray-900 dark:text-gray-100">Te quedaste sin fondos suficientes</h2>
            <p className="text-[10px] uppercase tracking-widest text-orange-600 font-bold">El bot está en pausa</p>
          </div>
        </div>
        <p className="text-xs text-gray-600 dark:text-gray-400 mb-4 leading-relaxed">
          Una casa de cambio se quedó sin saldo. En producción, mover capital entre exchanges tarda ~30+ minutos.
          En esta demo simulamos esa espera con <strong>1 minuto</strong>. Elige cómo continuar:
        </p>
        <div className="bg-gray-50 dark:bg-gray-950 border border-gray-200 dark:border-gray-800 rounded-lg p-4 mb-6 font-mono text-xs space-y-2">
          <div className="flex justify-between">
            <span className="text-gray-500 dark:text-gray-400">Ganancia posible</span>
            <span className="text-emerald-500 font-bold">+${profitPotential?.toFixed(2)}</span>
          </div>
          <div className="flex justify-between">
            <span className="text-gray-500 dark:text-gray-400">Costo del préstamo (con intereses)</span>
            <span className="text-red-500 font-bold">-${creditCost?.toFixed(2)}</span>
          </div>
          {threshold > creditCost && (
            <div className="flex justify-between">
              <span className="text-gray-500 dark:text-gray-400">Tu umbral de riesgo (costo × {(threshold / creditCost).toFixed(1)})</span>
              <span className="text-amber-500 font-bold">${threshold.toFixed(2)}</span>
            </div>
          )}
          <div className="flex justify-between border-t border-gray-200 dark:border-gray-800 pt-2 mt-2">
            <span className="text-gray-900 dark:text-gray-100 font-bold">Te quedaría</span>
            {/* El color sigue el SIGNO del neto mostrado (ganancia − costo), no el
                umbral de riesgo: un neto positivo pintado en rojo se contradecía con
                el propio número. El umbral vive en su fila ámbar y en el botón. */}
            <span className={`font-black ${profitPotential - creditCost >= 0 ? "text-emerald-500" : "text-red-500"}`}>
              ${(profitPotential - creditCost).toFixed(2)}
            </span>
          </div>
        </div>
        <div className="flex flex-col gap-3">
          {/* Préstamo: si no supera el umbral de riesgo, el botón lo DICE (texto
              dinámico), se atenúa a gris y queda explícitamente deshabilitado — ya
              no depende de un tooltip al pasar el cursor. */}
          <button
            className={`w-full p-3 rounded-lg font-bold text-xs uppercase tracking-widest transition-all duration-300 flex justify-center items-center gap-2 disabled:cursor-not-allowed ${
              isProfitable
                ? "bg-orange-500 hover:bg-orange-400 text-white"
                : "bg-gray-200 dark:bg-gray-800 text-gray-400 dark:text-gray-500"
            }`}
            onClick={handleRequestCredit}
            disabled={loading !== null || !isProfitable}
          >
            {loading === "credit"
              ? <><Loader2 className="w-4 h-4 animate-spin" /> Procesando...</>
              : isProfitable
                ? "Pedir préstamo y seguir operando"
                : "Préstamo No Rentable"}
          </button>
          {!isProfitable && (
            <p className="text-[10px] text-gray-500 dark:text-gray-400 text-center -mt-1.5 leading-relaxed">
              La ganancia (${profitPotential?.toFixed(2)}) no cubre tu umbral de riesgo (${threshold.toFixed(2)}). Baja el multiplicador de riesgo en <strong>Estrategia</strong> si quieres permitir este préstamo.
            </p>
          )}
          <button
            className="w-full bg-blue-600 hover:bg-blue-500 text-white p-3 rounded-lg font-bold text-xs uppercase tracking-widest transition-all duration-300 flex justify-center items-center gap-2"
            onClick={handleWait}
            disabled={loading !== null}
          >
            {loading === "wait" ? <><Loader2 className="w-4 h-4 animate-spin" /> Iniciando...</> : "Esperar reequilibrio (1 min demo)"}
          </button>
          {/* Decisión final del usuario (préstamo automático apagado): seguir sin
              endeudarse ni reequilibrar — el bot abandona ESTA oportunidad y sigue
              buscando otras con los fondos actuales. */}
          <button
            className="w-full bg-emerald-50 dark:bg-emerald-500/10 border border-emerald-300 dark:border-emerald-500/40 text-emerald-700 dark:text-emerald-300 hover:bg-emerald-100 dark:hover:bg-emerald-500/20 p-3 rounded-lg font-bold text-xs uppercase tracking-widest transition-all duration-300"
            onClick={onContinue}
            disabled={loading !== null}
          >
            Continuar sin rebalancear
          </button>
          <button
            className="w-full bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-800 text-gray-700 dark:text-gray-300 hover:bg-gray-50 dark:hover:bg-gray-800 p-3 rounded-lg font-bold text-xs uppercase tracking-widest transition-all duration-300"
            onClick={onShutdown}
            disabled={loading !== null}
          >
            Detener el bot
          </button>
        </div>
      </div>
    </div>
  );
}

function StrategyGuideModal({ open, onClose }: { open: boolean, onClose: () => void }) {
  if (!open) return null;
  return (
    <div className="fixed inset-0 z-[120] overflow-y-auto bg-gray-900/60 backdrop-blur-sm">
      <div className="flex min-h-full items-center justify-center p-4">
        <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-800 rounded-xl max-w-3xl w-full p-6 sm:p-8 shadow-2xl relative animate-modal-scale">
          <button onClick={onClose} className="absolute top-4 right-4 text-gray-400 hover:text-gray-600 dark:hover:text-gray-200 transition-colors">
            <X className="w-6 h-6" />
          </button>
          
          <div className="flex items-center gap-3 mb-6">
            <div className="w-10 h-10 rounded-full bg-emerald-500/10 flex items-center justify-center flex-shrink-0">
              <HelpCircle className="w-5 h-5 text-emerald-600" />
            </div>
            <div>
              <h2 className="text-xl font-black text-gray-900 dark:text-gray-100">¿Cómo funciona la estrategia?</h2>
              <p className="text-xs font-bold uppercase tracking-widest text-emerald-600">Arbitraje omnidireccional</p>
            </div>
          </div>
          
          <p className="text-sm text-gray-600 dark:text-gray-400 mb-4 leading-relaxed">
            Arus ya no persigue un solo par entre dos casas. Modelamos el mercado como un <strong>grafo de liquidez</strong>: cada nodo es un activo en un exchange (<code className="text-[11px] bg-gray-100 dark:bg-gray-800 px-1.5 py-0.5 rounded">BTC@Binance</code>, <code className="text-[11px] bg-gray-100 dark:bg-gray-800 px-1.5 py-0.5 rounded">USD@Bitso</code>, <code className="text-[11px] bg-gray-100 dark:bg-gray-800 px-1.5 py-0.5 rounded">ETH@Kraken</code>…) y cada arista una forma de convertirlo. Una oportunidad es un <strong>ciclo rentable</strong> en ese grafo — espacial entre casas o triangular dentro de una — y el motor busca la mejor ruta en todas las direcciones a la vez.
          </p>
          <p className="text-[12px] text-emerald-700 dark:text-emerald-400/90 mb-8 leading-relaxed border-l-2 border-emerald-500 pl-3 italic">
            &ldquo;El arbitraje no es una flecha entre dos casas: es un ciclo. Arus deja que el capital viaje por donde la ineficiencia realmente vive — entre exchanges o dentro de uno — y solo ejecuta cuando la ganancia neta, tras tus fees y fricciones, supera el margen que tú defines.&rdquo;
          </p>

          <div className="bg-gray-50 dark:bg-gray-950 border border-gray-200 dark:border-gray-800 rounded-xl p-6 mb-8 relative pt-10">
            <div className="absolute top-0 left-1/2 -translate-x-1/2 -translate-y-1/2 bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-800 text-xs font-bold tracking-widest uppercase px-4 py-1.5 rounded-full shadow-sm text-gray-500 flex items-center gap-2">
              <span className="w-2 h-2 rounded-full bg-emerald-500"></span>
              Dos caras del mismo ciclo
            </div>
            
            <div className="grid sm:grid-cols-2 gap-4 mt-2">
              {/* ESPACIAL */}
              <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-800 rounded-lg p-4 shadow-sm">
                <p className="text-[10px] font-bold text-emerald-600 tracking-widest uppercase mb-2">Espacial · entre casas</p>
                <p className="text-[11px] text-gray-500 dark:text-gray-400 mb-3 leading-relaxed">
                  El origen: comprar barato en una casa y vender caro en otra <strong className="text-gray-700 dark:text-gray-300">al mismo tiempo</strong>, con inventario pre-posicionado.
                </p>
                <div className="flex items-center justify-between gap-2 text-center">
                  <div className="flex-1 rounded-md bg-emerald-50 dark:bg-emerald-900/20 border border-emerald-100 dark:border-emerald-800/50 py-2 px-1">
                    <p className="text-[9px] font-bold text-gray-500 uppercase">Binance</p>
                    <p className="text-xs font-black text-emerald-600 dark:text-emerald-400">BTC barato</p>
                  </div>
                  <ArrowRight className="w-3.5 h-3.5 text-gray-400 flex-shrink-0" />
                  <div className="flex-1 rounded-md bg-blue-50 dark:bg-blue-900/20 border border-blue-100 dark:border-blue-800/50 py-2 px-1">
                    <p className="text-[9px] font-bold text-gray-500 uppercase">Bitso</p>
                    <p className="text-xs font-black text-blue-600 dark:text-blue-400">BTC caro</p>
                  </div>
                </div>
              </div>

              {/* TRIANGULAR */}
              <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-800 rounded-lg p-4 shadow-sm">
                <p className="text-[10px] font-bold text-blue-600 tracking-widest uppercase mb-2">Triangular · dentro de una casa</p>
                <p className="text-[11px] text-gray-500 dark:text-gray-400 mb-3 leading-relaxed">
                  La evolución: rotar tres pares en el mismo exchange hasta volver al cash con más de lo que salió.
                </p>
                <div className="flex items-center justify-center gap-1.5 text-[10px] font-black text-gray-700 dark:text-gray-300 flex-wrap">
                  <span className="bg-gray-100 dark:bg-gray-800 px-2 py-1 rounded">USDT</span>
                  <ArrowRight className="w-3 h-3 text-gray-400" />
                  <span className="bg-gray-100 dark:bg-gray-800 px-2 py-1 rounded">BTC</span>
                  <ArrowRight className="w-3 h-3 text-gray-400" />
                  <span className="bg-gray-100 dark:bg-gray-800 px-2 py-1 rounded">ETH</span>
                  <ArrowRight className="w-3 h-3 text-gray-400" />
                  <span className="bg-emerald-100 dark:bg-emerald-900/40 text-emerald-700 dark:text-emerald-400 px-2 py-1 rounded">USDT+</span>
                </div>
              </div>
            </div>

            <div className="mt-6 bg-emerald-50 dark:bg-emerald-900/20 border border-emerald-200 dark:border-emerald-800/50 rounded-xl p-5 text-center shadow-sm">
              <p className="text-sm font-bold text-emerald-800 dark:text-emerald-300">
                Mismo detector · misma decisión · ganancia <span className="text-emerald-600 dark:text-emerald-400">neta</span>
              </p>
              <p className="text-[11px] mt-2 text-emerald-700/80 dark:text-emerald-400/80 leading-relaxed max-w-lg mx-auto">
                Espacial y triangular son el mismo problema matemático. El radar elige el ciclo con mayor neto tras comisiones y slippage — y solo opera si supera <strong>tu</strong> margen mínimo.
              </p>
              <p className="text-[11px] mt-3 text-emerald-700/80 dark:text-emerald-400/80 uppercase tracking-widest font-bold flex items-center justify-center gap-1.5">
                <ShieldAlert className="w-3.5 h-3.5" /> Sin apostar a la dirección del mercado
              </p>
            </div>
          </div>

          <div className="space-y-4 text-sm text-gray-600 dark:text-gray-400 border-t border-gray-100 dark:border-gray-800 pt-6">
            <h3 className="font-bold text-gray-900 dark:text-gray-100 uppercase tracking-widest text-xs mb-4">Condiciones para que funcione</h3>
            <div className="grid sm:grid-cols-3 gap-4">
              <div className="bg-gray-50 dark:bg-gray-950 p-4 rounded-lg border border-gray-100 dark:border-gray-800">
                <span className="text-xl mb-2 block">🕸️</span>
                <p className="text-xs font-bold text-gray-800 dark:text-gray-200 mb-1">1. Ciclo en tu universo</p>
                <p className="text-[11px] leading-relaxed">Debe existir una ruta cerrada rentable entre las casas y monedas que <strong>tú</strong> activaste — el radar poda el grafo a tu universo.</p>
              </div>
              <div className="bg-gray-50 dark:bg-gray-950 p-4 rounded-lg border border-gray-100 dark:border-gray-800">
                <span className="text-xl mb-2 block">📉</span>
                <p className="text-xs font-bold text-gray-800 dark:text-gray-200 mb-1">2. Neto, no bruto</p>
                <p className="text-[11px] leading-relaxed">Arus descarta trades que parecen rentables en papel pero que las fricciones del mundo real vuelven negativos — con <strong>tus</strong> fees de Estrategia.</p>
              </div>
              <div className="bg-gray-50 dark:bg-gray-950 p-4 rounded-lg border border-gray-100 dark:border-gray-800">
                <span className="text-xl mb-2 block">💰</span>
                <p className="text-xs font-bold text-gray-800 dark:text-gray-200 mb-1">3. Capital posicionado</p>
                <p className="text-[11px] leading-relaxed">Cada pierna del ciclo necesita saldo (o crédito) en el nodo correcto. Mejor capital listo que ver pasar la oportunidad.</p>
              </div>
            </div>
          </div>

          <div className="space-y-4 text-sm text-gray-600 dark:text-gray-400 border-t border-gray-100 dark:border-gray-800 pt-6 mt-6">
            <h3 className="font-bold text-blue-600 dark:text-blue-400 uppercase tracking-widest text-xs mb-4 flex items-center gap-2">
              <span className="w-2 h-2 rounded-full bg-blue-500 animate-pulse"></span>
              La ventaja de Arus
            </h3>
            <div className="grid sm:grid-cols-2 gap-4">
              <div className="bg-blue-50 dark:bg-blue-900/10 p-4 rounded-lg border border-blue-100 dark:border-blue-900/30">
                <span className="text-xl mb-2 block">⚡</span>
                <p className="text-xs font-bold text-blue-900 dark:text-blue-200 mb-1">El dinero nunca duerme</p>
                <p className="text-[11px] leading-relaxed text-blue-800/80 dark:text-blue-300/80">
                  Mover fondos entre exchanges tarda ~30+ min. Si falta saldo en una pierna del ciclo, Arus puede ofrecer <strong>crédito instantáneo</strong> cuando la oportunidad lo justifica — convierte el tiempo muerto en oportunidad.
                </p>
              </div>
              <div className="bg-blue-50 dark:bg-blue-900/10 p-4 rounded-lg border border-blue-100 dark:border-blue-900/30">
                <span className="text-xl mb-2 block">🛡️</span>
                <p className="text-xs font-bold text-blue-900 dark:text-blue-200 mb-1">Escudo de robustez</p>
                <p className="text-[11px] leading-relaxed text-blue-800/80 dark:text-blue-300/80">
                  Spike filter contra precios rotos; si una pata pasa y la otra falla, el <strong>Emergency Unwind</strong> asume centavos de pérdida para salvar el capital de una caída.
                </p>
              </div>
              <div className="bg-blue-50 dark:bg-blue-900/10 p-4 rounded-lg border border-blue-100 dark:border-blue-900/30 sm:col-span-2">
                <span className="text-xl mb-2 block">🎯</span>
                <p className="text-xs font-bold text-blue-900 dark:text-blue-200 mb-1">Tú tienes el volante</p>
                <p className="text-[11px] leading-relaxed text-blue-800/80 dark:text-blue-300/80">
                  El sistema automatiza para no perder tiempo, pero la decisión final es tuya: margen, fees, universo, autopiloto y apetito de riesgo del crédito. Arus no juega a la velocidad bruta contra fondos gigantes — explota ineficiencias geográficas y estructurales dentro de las reglas que tú defines.
                </p>
              </div>
            </div>
          </div>

          <div className="mt-8 flex justify-end">
            <button
              onClick={onClose}
              className="bg-gray-900 dark:bg-white text-white dark:text-gray-900 hover:bg-gray-800 dark:hover:bg-gray-100 px-6 py-3 rounded-xl font-black text-sm transition-all duration-300 shadow-md hover:shadow-lg hover:-translate-y-0.5"
            >
              Cerrar guía
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}

function FundsModal({ exchange, asset, holdings, onClose, onSubmit }: {
  exchange: string;
  asset: string; // activo preseleccionado (el nodo del radar)
  holdings: { asset: string; balance: number; kind: "cash" | "crypto" }[];
  onClose: () => void;
  onSubmit: (currency: string, amount: number) => void;
}) {
  const assets = holdings.length > 0 ? holdings.map((h) => h.asset) : [asset];
  const [mode, setMode] = useState<"deposit" | "withdraw">("deposit");
  const [currency, setCurrency] = useState<string>(assets.includes(asset) ? asset : assets[0]);
  const [amount, setAmount] = useState<number | "">("");

  useEffect(() => {
    if (assets.includes(asset)) setCurrency(asset);
  }, [asset, exchange]); // eslint-disable-line react-hooks/exhaustive-deps

  const row = holdings.find((h) => h.asset === currency);
  const balance = row?.balance ?? 0;
  const isCash = row?.kind === "cash" || currency === "USD" || currency === "USDT";
  const amt = typeof amount === "number" ? amount : 0;
  const tooMuch = mode === "withdraw" && amt > balance;
  const valid = amt > 0 && !tooMuch;

  const handleConfirm = () => {
    if (!valid) return;
    onSubmit(currency, mode === "deposit" ? amt : -amt);
    onClose();
  };

  const fmtBal = (a: string, bal: number, kind?: string) => {
    if (kind === "cash" || a === "USD" || a === "USDT") {
      return `$${bal.toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
    }
    if (a === "BTC") return `${bal.toFixed(6)} ₿`;
    return `${bal.toFixed(6)} ${a}`;
  };

  const tab = (active: boolean, color: string) =>
    `flex-1 py-2 rounded-lg text-xs font-bold uppercase tracking-widest transition-all ${active ? `${color} text-white shadow-sm` : "bg-gray-100 dark:bg-gray-800 text-gray-500 dark:text-gray-400 hover:bg-gray-200 dark:hover:bg-gray-700"}`;

  return (
    <div className="fixed inset-0 z-[120] flex items-center justify-center bg-gray-900/60 backdrop-blur-sm p-4">
      <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-800 rounded-xl max-w-sm w-full p-6 shadow-2xl animate-modal-scale relative">
        <button onClick={onClose} className="absolute top-4 right-4 text-gray-400 hover:text-gray-600 dark:hover:text-gray-200 transition-colors">
          <X className="w-5 h-5" />
        </button>
        <h2 className="font-black text-lg text-gray-900 dark:text-gray-100 mb-1">Editar fondos · {exchange}</h2>
        <p className="text-xs text-gray-500 dark:text-gray-400 mb-5 leading-relaxed">
          Agrega o retira el activo de esta casa. No cuenta como ganancia ni pérdida del bot.
        </p>

        <div className="flex gap-2 mb-3">
          <button onClick={() => setMode("deposit")} className={tab(mode === "deposit", "bg-emerald-500")}>Agregar</button>
          <button onClick={() => setMode("withdraw")} className={tab(mode === "withdraw", "bg-amber-500")}>Retirar</button>
        </div>

        <div className="flex flex-wrap gap-2 mb-4">
          {assets.map((a) => (
            <button
              key={a}
              onClick={() => { setCurrency(a); setAmount(""); }}
              className={`px-3 py-2 rounded-lg text-xs font-bold uppercase tracking-widest transition-all ${currency === a ? "bg-gray-900 dark:bg-gray-200 text-white dark:!text-gray-900 shadow-sm" : "bg-gray-100 dark:bg-gray-800 text-gray-500 dark:text-gray-400 hover:bg-gray-200 dark:hover:bg-gray-700"}`}
            >
              {a}
            </button>
          ))}
        </div>

        <p className="text-[11px] text-gray-500 dark:text-gray-400 mb-1">
          Saldo actual de {currency}: <span className="font-bold text-gray-700 dark:text-gray-200">
            {fmtBal(currency, balance, row?.kind)}
          </span>
        </p>

        <input
          type="number"
          autoFocus
          value={amount}
          onChange={(e) => setAmount(e.target.value === "" ? "" : Math.abs(parseFloat(e.target.value)) || 0)}
          onKeyDown={(e) => { if (e.key === "Enter") handleConfirm(); }}
          placeholder={isCash ? "Ej. 5000" : currency === "BTC" ? "Ej. 0.25" : "Ej. 1.5"}
          step={isCash ? "100" : currency === "BTC" ? "0.01" : "0.1"}
          className="w-full bg-gray-50 dark:bg-gray-950 border border-gray-300 dark:border-gray-700 text-gray-900 dark:text-gray-100 p-3 rounded-lg outline-none focus:border-blue-500 focus:ring-1 focus:ring-blue-500 transition-colors font-mono text-sm shadow-sm mb-1"
        />
        {tooMuch && <p className="text-red-500 text-[11px] mb-1">No puedes retirar más de tu saldo.</p>}

        <button
          onClick={handleConfirm}
          disabled={!valid}
          className={`w-full mt-4 p-3 rounded-lg font-bold text-xs uppercase tracking-widest text-white transition-all duration-300 disabled:opacity-40 disabled:cursor-not-allowed active:scale-95 ${mode === "deposit" ? "bg-emerald-500 hover:bg-emerald-400" : "bg-amber-500 hover:bg-amber-400"}`}
        >
          {mode === "deposit" ? "Agregar" : "Retirar"} {currency}
        </button>
      </div>
    </div>
  );
}

// FASE 3 — anuncio efímero centrado del circuit breaker: aparece cuando Arus
// RECHAZA una oportunidad envenenada y se descarta solo a los 3 s (el hook limpia
// el estado). Rojo, con escudo: comunica que se protegió al usuario, sin luces
// verdes (no hubo operación). key={id} en el padre remonta y reanima cada alerta.
function CircuitBreakerAlert({ scenario, message }: { scenario: string; message: string }) {
  const label =
    scenario === "timeout" ? "Timeout de API bloqueado"
      : scenario === "divergence" ? "Divergencia de precio bloqueada"
        : "Spread irreal bloqueado";
  return (
    <div className="fixed inset-x-0 top-20 sm:top-24 z-[130] flex justify-center px-4 pointer-events-none">
      <div className="pointer-events-auto max-w-lg w-full bg-white dark:bg-gray-900 border-2 border-red-500 rounded-2xl shadow-2xl p-5 flex items-start gap-4 animate-modal-scale">
        <div className="w-11 h-11 rounded-full bg-red-500/10 flex items-center justify-center flex-shrink-0">
          <ShieldAlert className="w-6 h-6 text-red-500 animate-pulse" />
        </div>
        <div>
          <p className="text-[10px] font-black uppercase tracking-widest text-red-600 mb-1">🛡️ Escudo de robustez · {label}</p>
          <p className="text-sm font-bold text-gray-900 dark:text-gray-100 leading-relaxed">{message}</p>
        </div>
      </div>
    </div>
  );
}

export default function Home() {
  const { sessionReady, resuming, cancelResume, state, initSession, resetSession, simulateCustom, injectOmni, injectStorm, injectFake, toggleAutoCredit, requestCredit, waitRebalance, dismissShortfall, adjustFunds, setParams, shutdownEngine } = useArusEngine();
  
  const [showGuideModal, setShowGuideModal] = useState(false);
  const [showInjectionModal, setShowInjectionModal] = useState(false);
  // FASE 1 (refactor Probar Bot): las dos piernas del escenario custom + la
  // liquidez visible. Los precios se precargan con el mercado REAL del radar.
  const [simLegA, setSimLegA] = useState<SimLeg>({ venue: "Binance", asset: "BTC", bid: "", ask: "" });
  const [simLegB, setSimLegB] = useState<SimLeg>({ venue: "Bitso", asset: "BTC", bid: "", ask: "" });
  const [simLiquidity, setSimLiquidity] = useState<number | "">("");
  const [injectionToastMessage, setInjectionToastMessage] = useState("");
  const [isDarkMode, setIsDarkMode] = useState(false);
  const [fundsModal, setFundsModal] = useState<{ venue: string; asset: string } | null>(null);
  const [showTutorial, setShowTutorial] = useState(false);
  // Rediseño Radar-first: el radar es la vista principal; el dashboard clásico
  // vive en su propia pestaña. La estrategia se abre como drawer SOBRE el radar.
  const [view, setView] = useState<AppView>("radar");
  const [strategyOpen, setStrategyOpen] = useState(false);

  // Props estables para el RadarView memorizado: el lienzo no debe
  // re-renderizarse con cada log del feed (decenas por segundo).
  const lastSpike = useMemo(() => {
    for (let i = state.logs.length - 1; i >= 0; i--) {
      if (state.logs[i].level === "spike_block") return state.logs[i];
    }
    return null;
  }, [state.logs]);
  const openFundsModal = useCallback((venue: string, asset?: string) => {
    const nodes = state.graph?.nodes ?? [];
    const fallback =
      nodes.find((n) => n.venue === venue && n.kind === "cash")?.asset ??
      nodes.find((n) => n.venue === venue)?.asset ??
      "USD";
    setFundsModal({ venue, asset: asset ?? fallback });
  }, [state.graph]);

  // Catálogo de casas de cambio derivado de los nodos del radar (misma técnica
  // que el StrategyPanel): el simulador ofrece TODOS los venues del registro del
  // motor — un 4º exchange aparece aquí solo, sin tocar la UI.
  const catalogVenues = useMemo(() => {
    const venues: string[] = [];
    for (const n of state.graph?.nodes ?? []) {
      if (!venues.includes(n.venue)) venues.push(n.venue);
    }
    return venues.length > 0 ? venues : ["Binance", "Bitso"];
  }, [state.graph]);

  // Universo de Estrategia: la prueba custom SOLO ofrece casas/monedas activas
  // (misma regla que el backend — enabled vacío = todas).
  const simVenues = useMemo(() => {
    const ev = state.params?.enabled_venues;
    if (!ev || ev.length === 0) return catalogVenues;
    const filtered = catalogVenues.filter((v) => ev.includes(v));
    return filtered.length > 0 ? filtered : catalogVenues;
  }, [catalogVenues, state.params?.enabled_venues]);

  // ¿El universo actual admite al menos un ciclo? Si no, las pruebas rápidas
  // no tienen sentido (1 casa + solo BTC = imposible).
  const universeCap = useMemo(
    () => analyzeUniverseClient(state.params?.enabled_venues, state.params?.enabled_assets, catalogVenues),
    [state.params?.enabled_venues, state.params?.enabled_assets, catalogVenues],
  );

  // Monedas (cripto) que un venue publica, derivadas de los nodos del radar.
  const venueAssets = useCallback((venue: string) => {
    const assets: string[] = [];
    for (const n of state.graph?.nodes ?? []) {
      if (n.venue === venue && n.kind === "crypto" && !assets.includes(n.asset)) assets.push(n.asset);
    }
    const base = assets.length > 0 ? assets : ["BTC"];
    const ea = state.params?.enabled_assets;
    if (!ea || ea.length === 0) return base;
    const filtered = base.filter((a) => ea.includes(a));
    return filtered.length > 0 ? filtered : base;
  }, [state.graph, state.params?.enabled_assets]);

  // Precarga el bid/ask de una pierna con el precio REAL del libro elegido
  // (mid del radar ± 5 bps): la prueba parte del mercado vivo y el usuario solo
  // mueve lo que quiere probar. Sin dato de precio, deja los campos vacíos.
  const prefillSimLeg = useCallback((leg: SimLeg): SimLeg => {
    const node = state.graph?.nodes.find(n => n.venue === leg.venue && n.asset === leg.asset);
    const mid = node?.price_usd ?? 0;
    if (mid <= 0) return { ...leg, bid: "", ask: "" };
    const r = (x: number) => Math.round(x * 100) / 100;
    return { ...leg, bid: r(mid * 0.9995), ask: r(mid * 1.0005) };
  }, [state.graph]);

  // Venues con capital prestado mientras un préstamo está activo: el radar los
  // resalta con un anillo azul. Incluye crédito agnóstico multi-venue.
  const borrowedVenues = useMemo(() => {
    const set = new Set<string>();
    const b = state.borrowed;
    if (b.binance.usd > 0 || b.binance.btc > 0) set.add("Binance");
    if (b.bitso.usd > 0 || b.bitso.btc > 0) set.add("Bitso");
    for (const [venue, assets] of Object.entries(state.borrowedBalances ?? {})) {
      if (Object.values(assets).some(v => v > 0)) set.add(venue);
    }
    return [...set];
  }, [state.borrowed, state.borrowedBalances]);

  // Cards dinámicas: union de balances + nodos del grafo (Kraken/ETH/SOL aparecen solos).
  const dashboardVenues = useMemo(() => {
    const set = new Set<string>();
    for (const v of Object.keys(state.balances ?? {})) set.add(v);
    for (const n of state.graph?.nodes ?? []) set.add(n.venue);
    if (set.size === 0) {
      set.add("Binance");
      set.add("Bitso");
    }
    const order = ["Binance", "Bitso", "Kraken"];
    return [...set].sort((a, b) => {
      const ia = order.indexOf(a);
      const ib = order.indexOf(b);
      if (ia >= 0 || ib >= 0) return (ia < 0 ? 99 : ia) - (ib < 0 ? 99 : ib);
      return a.localeCompare(b);
    });
  }, [state.balances, state.graph?.nodes]);

  // Radar con saldos EN VIVO: graph_update llega ~1/s, pero tras un ciclo los
  // balances ya están en state — overlay para que BTC/ETH/SOL se muevan al instante.
  const radarGraph = useMemo(() => {
    if (!state.graph) return null;
    const bal = state.balances;
    if (!bal || Object.keys(bal).length === 0) return state.graph;
    return {
      ...state.graph,
      nodes: state.graph.nodes.map(n => {
        const qty = bal[n.venue]?.[n.asset];
        if (typeof qty !== "number") return n;
        const price = n.price_usd > 0 ? n.price_usd : n.kind === "cash" ? 1 : 0;
        return { ...n, balance: qty, balance_usd: qty * price };
      }),
    };
  }, [state.graph, state.balances]);

  // Tutorial automático en la primera visita (se recuerda con localStorage).
  useEffect(() => {
    if (sessionReady && typeof window !== "undefined" && !localStorage.getItem("arus_tutorial_seen")) {
      setShowTutorial(true);
    }
  }, [sessionReady]);

  const closeTutorial = () => {
    setShowTutorial(false);
    if (typeof window !== "undefined") localStorage.setItem("arus_tutorial_seen", "1");
  };

  const terminalScrollRef = useRef<HTMLDivElement>(null);
  // Solo seguimos el final de la terminal si el usuario ya estaba abajo.
  // Si subió a leer un log anterior, no lo arrastramos hacia abajo.
  const stickToBottomRef = useRef(true);

  const handleTerminalScroll = () => {
    const el = terminalScrollRef.current;
    if (!el) return;
    stickToBottomRef.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
  };

  // Tema: la elección MANUAL del usuario (persistida) siempre gana; el sistema
  // (prefers-color-scheme) solo decide mientras no haya preferencia guardada —
  // antes el listener del sistema podía pisar el toggle a mitad de una demo.
  useEffect(() => {
    if (typeof window !== 'undefined') {
      const stored = localStorage.getItem('arus_dark');
      const mediaQuery = window.matchMedia('(prefers-color-scheme: dark)');
      setIsDarkMode(stored !== null ? stored === '1' : mediaQuery.matches);

      const handleChange = (e: MediaQueryListEvent) => {
        if (localStorage.getItem('arus_dark') === null) setIsDarkMode(e.matches);
      };
      mediaQuery.addEventListener('change', handleChange);
      return () => mediaQuery.removeEventListener('change', handleChange);
    }
  }, []);

  const toggleDarkMode = () => {
    const next = !isDarkMode;
    if (typeof window !== 'undefined') localStorage.setItem('arus_dark', next ? '1' : '0');
    setIsDarkMode(next);
  };

  // La pestaña activa sobrevive recargas (se restaura en efecto para no
  // desalinear la hidratación de React).
  useEffect(() => {
    if (typeof window !== 'undefined' && localStorage.getItem('arus_view') === 'dashboard') {
      setView('dashboard');
    }
  }, []);

  const changeView = (v: AppView) => {
    setView(v);
    if (typeof window !== 'undefined') localStorage.setItem('arus_view', v);
  };

  // «Borrar todo y volver al inicio»: además de abandonar la sesión (hook),
  // cierra el estado LOCAL de la página — Home no se desmonta durante el
  // onboarding, así que un drawer/modal abierto reaparecería sobre la sesión
  // nueva. La vista vuelve a RADAR: la sesión fresca aterriza como la primera.
  // También olvida arus_tutorial_seen: tras rehacer el onboarding el tutorial
  // debe autoabrirse otra vez (mismo efecto que un usuario nuevo).
  const handleReset = () => {
    setStrategyOpen(false);
    setShowInjectionModal(false);
    setShowGuideModal(false);
    setShowTutorial(false);
    setFundsModal(null);
    changeView("radar");
    if (typeof window !== "undefined") localStorage.removeItem("arus_tutorial_seen");
    resetSession();
  };

  useEffect(() => {
    if (!stickToBottomRef.current) return;
    const el = terminalScrollRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [state.logs]);

  // Al abrir el modal, alinea las piernas al universo de Estrategia y precarga
  // precios reales (si el usuario ya editó bid/ask, se respeta lo suyo).
  useEffect(() => {
    if (!showInjectionModal) return;
    const align = (prev: SimLeg): SimLeg => {
      const venue = simVenues.includes(prev.venue) ? prev.venue : (simVenues[0] ?? prev.venue);
      const assets = venueAssets(venue);
      const asset = assets.includes(prev.asset) ? prev.asset : (assets[0] ?? prev.asset);
      const next = { ...prev, venue, asset };
      return prev.bid === "" && prev.ask === "" ? prefillSimLeg(next) : next;
    };
    setSimLegA(align);
    setSimLegB(align);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [showInjectionModal, simVenues]);

  // Cambiar casa/moneda re-precarga esa pierna con el precio real del libro
  // nuevo (si la moneda no existe en la casa nueva, cae a la primera disponible).
  const changeSimVenue = (which: "a" | "b", venue: string) => {
    const set = which === "a" ? setSimLegA : setSimLegB;
    set(prev => {
      const assets = venueAssets(venue);
      const asset = assets.includes(prev.asset) ? prev.asset : assets[0];
      return prefillSimLeg({ ...prev, venue, asset });
    });
  };
  const changeSimAsset = (which: "a" | "b", asset: string) => {
    const set = which === "a" ? setSimLegA : setSimLegB;
    set(prev => prefillSimLeg({ ...prev, asset }));
  };

  // FASE 1 — "Crear tu propia prueba": envía el escenario a POST
  // /api/simulate/custom. El motor lo valida (sesión, universo activo, libros
  // del catálogo, coherencia) y lo inyecta como ticks REALES en su canal de
  // ingesta; el veredicto se narra por el WebSocket. Un rechazo del backend se
  // muestra tal cual y el modal queda abierto para corregir.
  const handleCustomSim = async () => {
    if (simLegA.bid === "" || simLegA.ask === "" || simLegB.bid === "" || simLegB.ask === "") {
      setInjectionToastMessage("🛑 Completa el bid y el ask de ambos mercados.");
      setTimeout(() => setInjectionToastMessage(""), 5000);
      return;
    }
    const res = await simulateCustom({
      exchangeA: simLegA.venue, assetA: simLegA.asset, bidA: simLegA.bid, askA: simLegA.ask,
      exchangeB: simLegB.venue, assetB: simLegB.asset, bidB: simLegB.bid, askB: simLegB.ask,
      liquidity: typeof simLiquidity === "number" && simLiquidity > 0 ? simLiquidity : undefined,
    });
    if (!res.ok) {
      setInjectionToastMessage(`🛑 ${res.error}`);
      setTimeout(() => setInjectionToastMessage(""), 7000);
      return;
    }
    setShowInjectionModal(false);
    changeView("radar");
    setInjectionToastMessage(`⚠️ Ticks inyectados en el motor: ${simLegA.asset}@${simLegA.venue} vs ${simLegB.asset}@${simLegB.venue} — mira la actividad del bot`);
    setTimeout(() => setInjectionToastMessage(""), 5000);
  };

  // FASE 2 — "Oportunidad normal": inyecta ineficiencia omnidireccional dinámica
  // según el universo activo; el radar descubre el ciclo y anima la ruta real.
  const handleInjectOmni = () => {
    if (!universeCap.ok) {
      setInjectionToastMessage(`⚠️ ${universeCap.reason}`);
      setTimeout(() => setInjectionToastMessage(""), 6000);
      return;
    }
    setShowInjectionModal(false);
    changeView("radar");
    injectOmni();
    setInjectionToastMessage("🔺 Oportunidad omnidireccional inyectada — sigue la luz verde por el grafo");
    setTimeout(() => setInjectionToastMessage(""), 5000);
  };

  // FASE 3 — "Evento poco común": ráfaga HFT de micro-oportunidades por la
  // tubería real (ticks → radar → planCycle). Luces por todo el grafo.
  const handleInjectStorm = () => {
    if (!universeCap.ok) {
      setInjectionToastMessage(`⚠️ ${universeCap.reason}`);
      setTimeout(() => setInjectionToastMessage(""), 6000);
      return;
    }
    setShowInjectionModal(false);
    changeView("radar");
    injectStorm();
    setInjectionToastMessage("⚡ Tormenta de volatilidad iniciada — el motor ejecuta en paralelo por todo el grafo");
    setTimeout(() => setInjectionToastMessage(""), 5000);
  };

  // FASE 3 — "Precio falso / error": inyecta una oportunidad envenenada. El
  // backend la RECHAZA (circuit breaker) y responde con la alerta efímera; NO hay
  // luces verdes porque no hay operación. Se va al radar para que el contraste con
  // el flujo normal sea evidente. Sin toast rojo aquí: la alerta central es la voz.
  const handleInjectFake = () => {
    setShowInjectionModal(false);
    changeView("radar");
    injectFake();
  };

  const formatTime = (ts: string | number) => {
    if (typeof ts === 'number') {
      return new Date(ts * 1000).toLocaleTimeString('es-ES', { hour12: false });
    }
    return ts; 
  };

  const formatUptime = (seconds: number) => {
    const h = Math.floor(seconds / 3600);
    const m = Math.floor((seconds % 3600) / 60);
    const s = seconds % 60;
    return `${h.toString().padStart(2, '0')}:${m.toString().padStart(2, '0')}:${s.toString().padStart(2, '0')}`;
  };

  // Salud de fondos de un venue: saldo propio vs lo que ESE venue recibió al
  // inicio — con distribución (guiado o experto) el 100 % de cada casa es su
  // porcentaje real. Sin mapa de alloc (sesión antigua / nil), el par clásico
  // asume 50/50; el resto usa fallbackMaxUsd (valor actual) como referencia.
  const calculateHealth = (ownedUsd: number, venue: string, fallbackMaxUsd?: number) => {
    const isClassic = venue === "Binance" || venue === "Bitso";
    const pct = state.usdAllocation ? (state.usdAllocation[venue] ?? 0) : (isClassic ? 50 : 0);
    let maxUSD = state.initialUsd ? (state.initialUsd * pct) / 100 : (isClassic ? 60000 : 0);
    if (maxUSD <= 0) maxUSD = fallbackMaxUsd ?? 0;
    if (maxUSD <= 0) return 0;
    return Math.min(100, Math.max(0, (Math.max(0, ownedUsd) / maxUSD) * 100));
  };

  // ¿El venue está activo en el universo del usuario? enabled_venues vacío/undefined = todos.
  const isVenueActive = (venue: string) => {
    const ev = state.params?.enabled_venues;
    return !ev || ev.length === 0 || ev.includes(venue);
  };

  // Estado visual de una casa de cambio: fuera del universo → SOLO HOLD si tiene
  // fondos (custodia, sin operar) o INACTIVO si está vacía; dentro del universo →
  // ACTIVO con fondos o SIN FONDOS.
  const venueStatusOf = (venue: string, hasFunds: boolean): VenueStatus =>
    !isVenueActive(venue) ? (hasFunds ? "hold" : "inactive") : hasFunds ? "active" : "empty";

  // Activar una casa en el motor de arbitraje (FASE 7): la agrega a enabled_venues
  // y aplica los parámetros. Fondear una casa NO la activa sola (adjust_funds solo
  // toca la wallet) — el usuario decide explícitamente cuándo el bot puede usar esa
  // liquidez. Si enabled_venues está vacío ("todos"), no hay casas inactivas.
  const activateVenue = (venue: string) => {
    if (!state.params) return;
    const current = state.params.enabled_venues ?? [];
    if (current.length === 0 || current.includes(venue)) return;
    setParams({ ...state.params, enabled_venues: [...current, venue] });
  };

  if (!sessionReady) {
    // Continuidad: si hay una sesión persistida, se recupera de la base de datos
    // en lugar de pedir el capital de nuevo (saldos, estrategia e historial
    // sobreviven a reinicios del motor y del navegador).
    if (resuming) {
      return (
        <div className="min-h-screen bg-gray-50 dark:bg-gray-950 flex flex-col items-center justify-center gap-6 font-mono">
          <div className="w-14 h-14 border-4 border-gray-200 dark:border-gray-800 border-t-emerald-500 rounded-full animate-spin"></div>
          <div className="text-center">
            <p className="text-sm font-bold tracking-widest uppercase text-gray-700 dark:text-gray-300">Recuperando tu sesión…</p>
            <p className="text-xs text-gray-400 dark:text-gray-500 mt-2 max-w-xs">
              Tus saldos, tu estrategia y tu historial están guardados en la base de datos.
            </p>
          </div>
          <button
            onClick={cancelResume}
            className="text-[11px] uppercase tracking-widest font-bold text-gray-400 hover:text-red-500 transition-colors"
          >
            Empezar de cero en su lugar
          </button>
        </div>
      );
    }
    return <OnboardingModal onInit={initSession} initError={state.initError} />;
  }

  const { wallets, totalWealth, trades, balances, inventoryFlash, borrowedBalances } = state;
  
  const actualPnl = state.initialWealth > 0 ? state.totalWealth - state.initialWealth : null;
  const pnlPercentage = state.initialWealth > 0 ? (actualPnl! / state.initialWealth) * 100 : null;

  const displayAmount = (venue: string, asset: string, real: number) => {
    void venue;
    void asset;
    return real;
  };

  return (
    <div className={`${view === "radar" ? "h-dvh max-h-dvh overflow-hidden" : "min-h-screen overflow-x-hidden"} bg-gray-50 dark:bg-gray-950 text-gray-900 dark:text-gray-100 font-mono flex flex-col transition-all duration-700 relative ${state.creditActiveState?.active ? 'border-t-4 border-blue-500' : state.isRebalancing ? 'border-t-4 border-amber-500' : ''} ${isDarkMode ? 'dark' : ''}`}>
      
      {!state.engineRunning && (
        <div className="fixed inset-0 z-[200] flex items-center justify-center bg-gray-900/90 backdrop-blur-md">
          <div className="text-center animate-modal-scale">
            <h1 className="text-4xl font-black text-red-500 mb-4 tracking-widest uppercase">BOT DETENIDO</h1>
            <p className="text-gray-300 mb-8 max-w-md mx-auto">El bot se detuvo. Recarga la página para empezar de nuevo.</p>
            <button onClick={() => window.location.reload()} className="px-6 py-3 bg-white text-gray-900 font-bold uppercase tracking-widest text-sm rounded-lg hover:bg-gray-200 transition-colors">Reiniciar</button>
          </div>
        </div>
      )}

      {/* FASE 3 — anuncio efímero del circuit breaker (rechazo de precio falso) */}
      {state.circuitBreaker && (
        <CircuitBreakerAlert
          key={state.circuitBreaker.id}
          scenario={state.circuitBreaker.scenario}
          message={state.circuitBreaker.message}
        />
      )}

      <InsufficientFundsModal
        open={state.insufficientFundsModal?.open}
        profitPotential={state.insufficientFundsModal?.profitPotential}
        creditCost={state.insufficientFundsModal?.creditCost}
        creditRequired={state.insufficientFundsModal?.creditRequired}
        onRequestCredit={requestCredit}
        onWaitRebalance={waitRebalance}
        onContinue={dismissShortfall}
        onShutdown={shutdownEngine}
      />
      
      <StrategyGuideModal
        open={showGuideModal}
        onClose={() => setShowGuideModal(false)}
      />

      <TutorialModal open={showTutorial} onClose={closeTutorial} />

      {fundsModal && (
        <FundsModal
          exchange={fundsModal.venue}
          asset={fundsModal.asset}
          holdings={(state.graph?.nodes ?? [])
            .filter((n) => n.venue === fundsModal.venue)
            .map((n) => ({ asset: n.asset, balance: n.balance, kind: n.kind }))
            .sort((a, b) => (a.kind === b.kind ? a.asset.localeCompare(b.asset) : a.kind === "cash" ? -1 : 1))}
          onClose={() => setFundsModal(null)}
          onSubmit={(currency, amount) => adjustFunds(fundsModal.venue, currency, amount)}
        />
      )}
      
      {/* Modal de Inyección de Spread — acotado al viewport (como el radar):
          en pantallas normales se ve completo; solo columnas/terminal hacen
          scroll interno si hace falta. En móvil el cuerpo del modal scrollea. */}
      {showInjectionModal && (
        <div className="fixed inset-0 z-[100] flex items-center justify-center p-2 sm:p-3 bg-gray-900/60 backdrop-blur-sm">
          <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-800 rounded-xl max-w-5xl w-full max-h-[92dvh] lg:h-[min(820px,92dvh)] flex flex-col overflow-hidden shadow-2xl transform transition-all animate-modal-scale">
            <div className="flex-shrink-0 px-4 sm:px-6 pt-4 sm:pt-5 pb-3 border-b border-gray-100 dark:border-gray-800">
              <h2 className="text-lg sm:text-xl font-black text-gray-900 dark:text-gray-100 flex items-center gap-2.5">
                <ShieldAlert className="text-red-600 w-5 h-5 sm:w-6 sm:h-6 animate-pulse flex-shrink-0" />
                Modo de pruebas del bot
              </h2>
              <p className="text-gray-600 dark:text-gray-400 text-[11px] sm:text-xs mt-1.5 leading-relaxed">
                Inyecta cotizaciones de prueba en la misma tubería que las fuentes en vivo. El bot responde con <strong>tu estrategia</strong> (universo, comisiones, margen, deslizamiento y topes) — 100&nbsp;% seguro, sin dinero real.
              </p>
              {!universeCap.ok && (
                <div className="mt-2.5 rounded-lg border border-amber-300 dark:border-amber-500/40 bg-amber-50 dark:bg-amber-500/10 px-3 py-2 text-[11px] text-amber-800 dark:text-amber-200 leading-relaxed">
                  <strong>Universo demasiado estrecho.</strong> {universeCap.reason} Amplíalo en Estrategia → Tu universo.
                </div>
              )}
            </div>

            <div className="flex-1 min-h-0 overflow-y-auto lg:overflow-hidden px-4 sm:px-6 py-3">
              <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3 sm:gap-4 lg:h-full lg:min-h-0">

              {/* Columna Izquierda: Crear tu propia prueba (FASE 1 — tubería base).
                  Define el top-of-book de DOS libros; POST /api/simulate/custom los
                  valida contra tu universo y los inyecta como ticks REALES en el
                  canal de ingesta del motor (misma tubería que los feeds). */}
              <div className="bg-gray-50 dark:bg-gray-950 p-3 sm:p-4 rounded-xl border border-gray-200 dark:border-gray-800 shadow-sm flex flex-col lg:min-h-0 lg:overflow-y-auto">
                <h3 className="text-gray-800 dark:text-gray-200 font-bold tracking-widest text-[10px] mb-3 uppercase border-b border-gray-200 dark:border-gray-800 pb-2 flex-shrink-0">Crear tu propia prueba</h3>
                <div className="flex flex-col gap-2.5 flex-1 min-h-0">
                  <div className="text-[10px] text-gray-500 dark:text-gray-400 leading-relaxed space-y-1.5">
                    <p>
                      Arma la mejor oferta de dos casas — compra (bid) y venta (ask) — para el mismo activo. Al inyectar, esas <strong>cotizaciones</strong> pasan por el filtro anti-pico (spike), tus comisiones y tu deslizamiento (slippage) — igual que una fuente real.
                    </p>
                    <p className="rounded-md border border-emerald-200 dark:border-emerald-500/30 bg-emerald-50/80 dark:bg-emerald-500/10 px-2 py-1.5 text-emerald-900 dark:text-emerald-200">
                      <strong>Para ver ganancia:</strong> deja Mercado&nbsp;A cerca del precio vivo y sube la <strong>compra del mercado (bid)</strong> de Mercado&nbsp;B ~1.5&nbsp;%. El bot compra barato en A y vende caro en B. Si el salto supera ~5&nbsp;%, el escudo anti-pico lo descarta (también es una prueba válida).
                    </p>
                    <p>
                      En el radar busca nodos que laten, luz verde en la arista y <strong>+$</strong> en la última operación; en el registro: <span className="font-mono text-[9px]">[OPORTUNIDAD]</span> / <span className="font-mono text-[9px]">[ARBITRAJE]</span>.
                    </p>
                  </div>

                  <SimLegEditor
                    label="Mercado A · compra barata"
                    leg={simLegA}
                    venues={simVenues}
                    assets={venueAssets(simLegA.venue)}
                    inactive={!isVenueActive(simLegA.venue)}
                    onVenue={v => changeSimVenue("a", v)}
                    onAsset={a => changeSimAsset("a", a)}
                    onBid={v => setSimLegA(prev => ({ ...prev, bid: v }))}
                    onAsk={v => setSimLegA(prev => ({ ...prev, ask: v }))}
                  />
                  <SimLegEditor
                    label="Mercado B · venta cara"
                    leg={simLegB}
                    venues={simVenues}
                    assets={venueAssets(simLegB.venue)}
                    inactive={!isVenueActive(simLegB.venue)}
                    onVenue={v => changeSimVenue("b", v)}
                    onAsset={a => changeSimAsset("b", a)}
                    onBid={v => setSimLegB(prev => ({ ...prev, bid: v }))}
                    onAsk={v => setSimLegB(prev => ({ ...prev, ask: v }))}
                  />

                  <div>
                    <label className="text-[10px] font-bold tracking-widest text-gray-500 dark:text-gray-400 uppercase">Liquidez visible (unidades)</label>
                    <input
                      type="number"
                      step="any"
                      value={simLiquidity}
                      onChange={e => setSimLiquidity(e.target.value === "" ? "" : parseFloat(e.target.value) || 0)}
                      className="w-full bg-white dark:bg-gray-900 border border-gray-300 dark:border-gray-700 text-gray-900 dark:text-gray-100 p-2 rounded-lg outline-none focus:border-red-500 focus:ring-1 focus:ring-red-500 transition-colors mt-1 font-mono text-xs shadow-sm"
                      placeholder="1.0 (por defecto)"
                      min="0.001"
                      max="10"
                    />
                    <p className="text-[10px] text-gray-400 mt-1 leading-relaxed">Cantidad ofrecida en cada punta del libro. El bot ajusta el tamaño de la orden con esta liquidez y tu tope máximo de estrategia (deja 1.0 si no estás seguro).</p>
                  </div>

                  <div className="[&>div]:mt-0">
                    <CreditToggle autoMode={state.autoCreditMode} onToggle={toggleAutoCredit} />
                  </div>

                  <div className="mt-auto pt-2">
                    <button
                      onClick={handleCustomSim}
                      className="w-full bg-red-600 hover:bg-red-500 text-white p-3 rounded-lg font-black text-sm text-center shadow-md transition-all duration-300 hover:-translate-y-0.5 hover:shadow-xl hover:shadow-red-500/30 active:scale-95"
                    >
                      Inyectar en el motor
                    </button>
                  </div>
                </div>
              </div>

              {/* Columna Centro: Acciones Rápidas */}
              <div className="bg-gray-50 dark:bg-gray-950 p-3 sm:p-4 rounded-xl border border-gray-200 dark:border-gray-800 shadow-sm flex flex-col lg:min-h-0 lg:overflow-y-auto">
                <h3 className="text-gray-800 dark:text-gray-200 font-bold tracking-widest text-[10px] mb-3 uppercase border-b border-gray-200 dark:border-gray-800 pb-2 flex-shrink-0">Pruebas rápidas</h3>

                <div className="flex flex-col gap-2.5 flex-1 min-h-0">
                  <button
                    onClick={handleInjectOmni}
                    disabled={!universeCap.ok}
                    className="w-full bg-white dark:bg-gray-900 hover:bg-gray-50 dark:bg-gray-950 border border-gray-200 dark:border-gray-800 hover:border-emerald-400 p-3 rounded-lg text-left transition-all duration-300 hover:-translate-y-0.5 hover:shadow-lg group shadow-sm flex flex-col disabled:opacity-40 disabled:pointer-events-none disabled:hover:translate-y-0"
                  >
                    <div className="flex justify-between items-center mb-1 gap-2">
                      <span className="text-gray-900 dark:text-gray-100 font-bold text-sm flex flex-wrap items-center gap-2">
                        🔺 Oportunidad normal <span className="text-[9px] font-black uppercase tracking-widest text-emerald-600 bg-emerald-50 dark:bg-emerald-500/10 border border-emerald-200 dark:border-emerald-500/30 rounded-full px-2 py-0.5">Omnidireccional</span>
                      </span>
                    </div>
                    <div className="text-[10px] text-gray-500 dark:text-gray-400 font-mono mb-1.5 bg-gray-100 dark:bg-gray-800 p-1.5 rounded inline-block">Ciclo dinámico · triangular o espacial</div>
                    <p className="text-gray-600 dark:text-gray-400 text-[11px] leading-relaxed">Encuentra un <strong>camino completo</strong> en el grafo y lo recorre hasta volver con ganancia. Síguelo en el radar.</p>
                  </button>

                  <button
                    onClick={handleInjectStorm}
                    disabled={!universeCap.ok}
                    className="w-full bg-white dark:bg-gray-900 hover:bg-gray-50 dark:bg-gray-950 border border-gray-200 dark:border-gray-800 hover:border-amber-400 p-3 rounded-lg text-left transition-all duration-300 hover:-translate-y-0.5 hover:shadow-lg group shadow-sm flex flex-col disabled:opacity-40 disabled:pointer-events-none disabled:hover:translate-y-0"
                  >
                    <div className="flex justify-between items-center mb-1 gap-2">
                      <span className="text-amber-600 font-bold text-sm flex flex-wrap items-center gap-2">
                        ⚡ Evento poco común <span className="text-[9px] font-black uppercase tracking-widest text-amber-700 dark:text-amber-400 bg-amber-50 dark:bg-amber-500/10 border border-amber-200 dark:border-amber-500/30 rounded-full px-2 py-0.5">Tormenta HFT</span>
                      </span>
                    </div>
                    <div className="text-[10px] text-amber-700 dark:text-amber-400 font-mono mb-1.5 bg-amber-50 dark:bg-amber-900/30 p-1.5 rounded inline-block">~4 s · 15–20 micro-ops · paralelo</div>
                    <p className="text-gray-600 dark:text-gray-400 text-[11px] leading-relaxed">Ráfaga de <strong>micro-ineficiencias</strong> ejecutadas en paralelo. Luces por todo el grafo — Arus bajo caos HFT.</p>
                  </button>

                  <button
                    onClick={handleInjectFake}
                    className="w-full bg-white dark:bg-gray-900 hover:bg-gray-50 dark:bg-gray-950 border border-gray-200 dark:border-gray-800 hover:border-red-400 p-3 rounded-lg text-left transition-all duration-300 hover:-translate-y-0.5 hover:shadow-lg group shadow-sm flex flex-col"
                  >
                    <div className="flex justify-between items-center mb-1 gap-2">
                      <span className="text-red-600 font-bold text-sm flex flex-wrap items-center gap-2">
                        🛡️ Precio falso (error) <span className="text-[9px] font-black uppercase tracking-widest text-red-700 dark:text-red-400 bg-red-50 dark:bg-red-500/10 border border-red-200 dark:border-red-500/30 rounded-full px-2 py-0.5">Circuit Breaker</span>
                      </span>
                    </div>
                    <div className="text-[10px] text-red-700 dark:text-red-400 font-mono mb-1.5 bg-red-50 dark:bg-red-900/30 p-1.5 rounded inline-block">Tick falso · FoK/timeout · divergencia</div>
                    <p className="text-gray-600 dark:text-gray-400 text-[11px] leading-relaxed">Oportunidad envenenada: Arus la <strong>rechaza al instante</strong> y te dice por qué te protegió.</p>
                  </button>
                </div>
              </div>

              {/* Columna Derecha: Live Terminal Console Log */}
              <div className="relative flex flex-col w-full min-h-[200px] h-[240px] md:col-span-2 md:h-[260px] lg:col-span-1 lg:min-h-0 lg:h-full">
                <div className="absolute inset-0 flex flex-col bg-[#0a0e14] border border-gray-300 dark:border-gray-700 rounded-xl overflow-hidden shadow-md">
                  <div className="bg-gray-800 px-3 py-1.5 flex items-center gap-2 border-b border-gray-700 flex-shrink-0">
                    <div className="w-2.5 h-2.5 rounded-full bg-red-500"></div>
                    <div className="w-2.5 h-2.5 rounded-full bg-amber-500"></div>
                    <div className="w-2.5 h-2.5 rounded-full bg-emerald-500"></div>
                    <span className="ml-2 text-[10px] font-bold text-gray-300 tracking-widest uppercase">Actividad del bot en vivo</span>
                  </div>
                  <div
                    ref={terminalScrollRef}
                    onScroll={handleTerminalScroll}
                    className="p-3 flex-1 min-h-0 overflow-y-auto overscroll-contain font-mono text-[11px] text-gray-300 leading-relaxed space-y-1"
                  >
                    {state.logs.length === 0 ? (
                      <p className="text-gray-500 dark:text-gray-400 italic">Esperando a que el bot empiece a trabajar...</p>
                    ) : (
                      state.logs.map((log, i) => (
                        <div key={i} className={`break-all ${logColor(log.level)}`}>
                          <span className="text-gray-600 dark:text-gray-500 mr-2">[{log.timestamp}]</span>
                          {log.message}
                        </div>
                      ))
                    )}
                  </div>
                </div>
              </div>

              </div>
            </div>

            <button
              onClick={() => setShowInjectionModal(false)}
              className="flex-shrink-0 py-3 text-gray-500 dark:text-gray-400 hover:text-gray-800 dark:hover:text-gray-200 uppercase tracking-widest font-bold w-full text-center transition-colors text-[10px] border-t border-gray-100 dark:border-gray-800"
            >
              Cerrar
            </button>
          </div>
        </div>
      )}

      {/* Overlay de pausa por reequilibrio — aquí vive Pedir préstamo (no en el header) */}
      {state.isRebalancing && (
        <div className="fixed inset-0 z-[90] flex items-center justify-center bg-gray-900/40 backdrop-blur-[2px] pointer-events-none">
          <div className="flex flex-col items-center justify-center text-center max-w-lg p-8 pointer-events-auto">
            <div className="w-16 h-16 border-4 border-amber-200 border-t-amber-600 rounded-full animate-spin mb-6"></div>
            <h2 className="text-xl font-black text-white tracking-widest uppercase mb-3">Operaciones pausadas</h2>
            <p className="text-amber-100 font-bold text-sm bg-amber-900/60 border border-amber-700/50 px-6 py-3 rounded-lg">{state.rebalanceMessage}</p>
            <p className="text-gray-400 text-xs mt-4 mb-5">Demo: 1 min · Producción: ~30+ min moviendo capital entre exchanges</p>
            {!state.creditActiveState?.active && (
              <button
                type="button"
                onClick={requestCredit}
                className="inline-flex items-center gap-2 px-5 py-2.5 rounded-lg bg-blue-600 hover:bg-blue-500 text-white font-black text-xs uppercase tracking-widest shadow-lg shadow-blue-900/40 transition-all hover:-translate-y-0.5 active:scale-95"
                title="Cancela la espera del traslado e inyecta liquidez de inmediato"
              >
                <Landmark className="w-4 h-4" />
                Pedir préstamo
              </button>
            )}
            <p className="text-gray-500 text-[10px] mt-3 max-w-sm">
              ¿No quieres esperar? El préstamo inyecta capital ya y reanuda el bot (con costo de fee + interés).
            </p>
          </div>
        </div>
      )}

      {/* Alerta de Inyección de Spread */}
      {injectionToastMessage && (
        <div className={`fixed top-24 right-4 sm:right-8 z-[100] transition-all duration-500 transform translate-y-0 opacity-100`}>
          <div className="bg-red-50 dark:bg-red-950/40 border border-red-200 dark:border-red-500/20 text-red-900 dark:text-red-100 px-6 py-4 rounded-xl shadow-md flex items-center gap-3 backdrop-blur-md">
            <ShieldAlert className="w-6 h-6 text-red-600 dark:text-red-500 animate-pulse" />
            <p className="font-bold tracking-widest text-xs">{injectionToastMessage}</p>
          </div>
        </div>
      )}

      {/* FASE 2 — banner de tormenta (ráfaga de volatilidad en curso) */}
      {state.stormActive && (
        <div className="relative z-10 overflow-hidden">
          <div className="bg-amber-500 text-white font-bold px-4 py-3 flex items-center justify-center gap-3 shadow-md border-b border-amber-600">
            <span className="w-2.5 h-2.5 rounded-full bg-white animate-pulse shadow-[0_0_8px_rgba(255,255,255,0.85)]" />
            <span className="tracking-widest text-xs sm:text-sm uppercase text-center">
              ⚡ Tormenta de volatilidad — Arus ejecutando múltiples arbitrajes en paralelo
            </span>
          </div>
        </div>
      )}

      {state.isRebalancing && (
        <ReplenishingBanner expiresAt={state.rebalanceExpiresAt} message="Traslado de capital en curso" />
      )}

      {state.creditActiveState?.active && (
        <CreditActiveBanner expiresAt={state.creditActiveState.expiresAt} depleted={state.creditActiveState.depleted} />
      )}

      {/* FASE 2 — resumen efímero al terminar la tormenta */}
      {state.stormResult && (
        <div className="fixed bottom-8 right-8 z-50 transition-all duration-500 transform translate-y-0 opacity-100">
          <div className="bg-amber-50 dark:bg-amber-950/40 border border-amber-200 dark:border-amber-500/20 text-amber-900 dark:text-amber-100 px-6 py-5 rounded-xl shadow-md flex items-start gap-4 max-w-md backdrop-blur-md">
            <Zap className="w-6 h-6 text-amber-600 dark:text-amber-500 mt-0.5 flex-shrink-0" />
            <div>
              <p className="font-bold tracking-widest text-sm">⚡ Tormenta superada</p>
              <p className="text-amber-800 dark:text-amber-300 text-xs mt-2 leading-relaxed">
                Arus procesó <span className="font-black">{state.stormResult.trades} operaciones</span> concurrentes sin romperse.
                Ganancia acumulada: <span className="font-black text-emerald-600 dark:text-emerald-400">+${state.stormResult.profit.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}</span>.
              </p>
            </div>
          </div>
        </div>
      )}

      {state.rebalanceSuccessAmount !== null && (
        <div className={`fixed bottom-8 right-8 z-50 transition-all duration-500 transform translate-y-0 opacity-100`}>
          <div className="bg-blue-50 dark:bg-blue-950/40 border border-blue-200 dark:border-blue-500/20 text-blue-900 dark:text-blue-100 px-6 py-5 rounded-xl shadow-md flex items-start gap-4 max-w-md backdrop-blur-md">
            <CheckCircle className="w-6 h-6 text-blue-600 dark:text-blue-500 mt-0.5 flex-shrink-0" />
            <div>
              <p className="font-bold tracking-widest text-sm">✅ Inventario reequilibrado con éxito.</p>
              <p className="text-blue-800 dark:text-blue-300 text-xs mt-2 leading-relaxed">Tu dinero y bitcoin se repartieron 50/50 entre las dos casas de cambio, sobre un total de <span className="font-black">${state.rebalanceSuccessAmount.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })} USD</span>.</p>
            </div>
          </div>
        </div>
      )}

      {/* En la vista RADAR la ganancia del préstamo la comunica el burst centrado
          (LoanBurst); este toast queda para la vista DASHBOARD, que no tiene radar.
          Posición responsiva + h-auto: no se desborda ni se corta en móvil. */}
      {state.loanResults && view === "dashboard" && (
        <div className={`fixed bottom-4 sm:bottom-32 right-4 left-4 sm:left-auto sm:right-8 z-50 transition-all duration-500 transform translate-y-0 opacity-100`}>
          <div className="bg-emerald-50 dark:bg-emerald-950/40 border border-emerald-200 dark:border-emerald-500/20 text-emerald-900 dark:text-emerald-100 px-6 py-5 rounded-xl shadow-md flex items-start gap-4 w-full sm:max-w-md backdrop-blur-md">
            <CheckCircle className="w-6 h-6 text-emerald-600 dark:text-emerald-500 mt-0.5 flex-shrink-0" />
            <div>
              <p className="font-bold tracking-widest text-sm">✅ Resultados del Préstamo</p>
              <p className="text-emerald-800 dark:text-emerald-300 text-xs mt-2 leading-relaxed">
                Ganancia durante el préstamo: <span className="font-black">+${state.loanResults.earnings.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })} USD</span>.
                <br />
                Costo de intereses: <span className="font-black text-red-600 dark:text-red-400">-${state.loanResults.cost.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })} USD</span>.
                <br />
                <strong>Ganancia Neta Limpia: <span className={state.loanResults.earnings - state.loanResults.cost >= 0 ? 'text-emerald-600 dark:text-emerald-400' : 'text-red-600 dark:text-red-400'}>${(state.loanResults.earnings - state.loanResults.cost).toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}</span></strong>
              </p>
            </div>
          </div>
        </div>
      )}

      {/* Header compacto (Radar-first): tabs, patrimonio animado y acciones
          globales — Probar el bot vive aquí, disponible desde AMBAS vistas.
          El préstamo automático se movió al drawer de Estrategia. */}
      <HeaderBar
        view={view}
        onViewChange={changeView}
        totalWealth={totalWealth}
        pnl={actualPnl}
        uptime={formatUptime(state.uptimeSeconds)}
        autopilot={state.params?.radar_autopilot === true}
        isRebalancing={state.isRebalancing}
        creditActive={state.creditActiveState?.active === true}
        onProbar={() => setShowInjectionModal(true)}
        onRebalance={waitRebalance}
        onEstrategia={() => setStrategyOpen(true)}
        onTutorial={() => setShowTutorial(true)}
        onReset={handleReset}
        isDarkMode={isDarkMode}
        onToggleDark={toggleDarkMode}
      />

      {/* Estrategia como drawer SOBRE el radar: la poda del universo se ve en vivo */}
      <StrategyDrawer
        open={strategyOpen}
        onClose={() => setStrategyOpen(false)}
        params={state.params}
        graph={state.graph}
        onApply={setParams}
        autoCreditMode={state.autoCreditMode}
        onToggleAutoCredit={toggleAutoCredit}
      />

      {/* Vista principal: el RADAR a pantalla completa */}
      {view === "radar" && (
        <RadarView
          graph={radarGraph}
          trades={state.trades}
          spike={lastSpike}
          enabledVenues={state.params?.enabled_venues}
          enabledAssets={state.params?.enabled_assets}
          onEditFunds={openFundsModal}
          creditActive={state.creditActiveState?.active}
          borrowedVenues={borrowedVenues}
          loanResult={state.loanResults}
          omniPulse={state.omniPulse}
          stormActive={state.stormActive}
          inventoryFlash={state.inventoryFlash}
        />
      )}

      {/* Vista Dashboard: KPIs, wallets, feed de operaciones, ledger y analítica */}
      {view === "dashboard" && (
      <main className="relative z-10 flex-1 w-full max-w-[1600px] mx-auto px-4 sm:px-6 lg:px-8 py-6 sm:py-8 flex flex-col">
        
        {/* Encabezado (Top) - Tarjetas de KPIs */}
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4 mb-6">
          <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-800 rounded-xl p-5 shadow-sm hover:shadow-xl transition-all duration-500 hover:-translate-y-1 flex flex-col justify-center animate-fade-in-up" style={{ animationDelay: '0.1s' }}>
            <p className="text-gray-500 dark:text-gray-400 text-xs mb-1 tracking-widest font-bold">DINERO TOTAL (USD)</p>
            <p className="text-2xl sm:text-3xl font-black text-gray-900 dark:text-gray-100 tracking-tighter">
              ${totalWealth.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}
            </p>
          </div>
          <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-800 rounded-xl p-5 shadow-sm hover:shadow-xl transition-all duration-500 hover:-translate-y-1 flex flex-col justify-center animate-fade-in-up" style={{ animationDelay: '0.2s' }}>
            <p className="text-gray-500 dark:text-gray-400 text-xs mb-1 tracking-widest font-bold">GANANCIA NETA</p>
            <p className={`text-2xl sm:text-3xl font-black tracking-tighter ${actualPnl === null ? 'text-gray-500' : actualPnl >= 0 ? 'text-emerald-600' : 'text-red-600'}`}>
              {actualPnl === null ? '--' : `${actualPnl >= 0 ? '+' : '-'}$${Math.abs(actualPnl).toFixed(2)}`}
            </p>
          </div>
          <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-800 rounded-xl p-5 shadow-sm hover:shadow-xl transition-all duration-500 hover:-translate-y-1 flex flex-col justify-center animate-fade-in-up" style={{ animationDelay: '0.3s' }}>
            <p className="text-gray-500 dark:text-gray-400 text-xs mb-1 tracking-widest font-bold">RENDIMIENTO (%)</p>
            <p className={`text-2xl sm:text-3xl font-black tracking-tighter ${pnlPercentage === null ? 'text-gray-500' : pnlPercentage >= 0 ? 'text-emerald-600' : 'text-red-600'}`}>
              {pnlPercentage === null ? '--' : `${pnlPercentage >= 0 ? '+' : '-'}${Math.abs(pnlPercentage).toFixed(4)}%`}
            </p>
          </div>
          <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-800 rounded-xl p-5 shadow-sm hover:shadow-xl transition-all duration-500 hover:-translate-y-1 flex flex-col justify-center animate-fade-in-up" style={{ animationDelay: '0.4s' }}>
            <p className="text-gray-500 dark:text-gray-400 text-xs mb-1 tracking-widest font-bold">OPERACIONES</p>
            <p className="text-2xl sm:text-3xl font-black text-blue-600 tracking-tighter">
              {state.opsCount}
            </p>
          </div>
        </div>

        {/* Cuerpo Principal (Middle) - Dos Columnas en Desktop */}
        <div className="flex flex-col lg:flex-row gap-6 items-stretch">
          
          {/* Columna Izquierda (Billeteras) */}
          <div className="w-full lg:w-[400px] xl:w-[450px] flex flex-col gap-6 flex-shrink-0">
            <div className="flex justify-between items-end mb-2">
              <h2 className="text-gray-500 dark:text-gray-400 font-bold tracking-widest text-sm uppercase">CASAS DE CAMBIO</h2>
              {(state.livePrices.binance > 0 || state.livePrices.bitso > 0) && (
                <div className="text-right flex items-center gap-2">
                  <span className={`w-1.5 h-1.5 rounded-full transition-all duration-300 ${state.ping ? 'bg-emerald-500 scale-150' : 'bg-gray-300'}`}></span>
                  <p className="text-gray-400 font-mono text-[10px] tracking-widest font-bold hidden sm:block">
                    BNC: ${state.livePrices.binance.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })} | BSO: ${state.livePrices.bitso.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}
                  </p>
                </div>
              )}
            </div>
            
            {/* Wallets dinámicas: mapean balances de Go (Binance/Bitso/Kraken × USD/BTC/ETH/SOL). */}
            {dashboardVenues.map((venue, idx) => {
              const accent = VENUE_CARD_ACCENT[venue] ?? {
                badge: "bg-gray-600 text-white",
                hover: "bg-gray-600/0 group-hover:bg-gray-600/5",
                crypto: "text-emerald-600",
              };
              const venueBal = balances?.[venue] ?? {};
              const graphNodes = (state.graph?.nodes ?? []).filter(n => n.venue === venue);
              const assetSet = new Set<string>([
                ...Object.keys(venueBal),
                ...graphNodes.map(n => n.asset),
                ...(inventoryFlash?.byVenueAsset?.[venue] ? Object.keys(inventoryFlash.byVenueAsset[venue]) : []),
              ]);
              // Preferir orden cash → BTC → resto.
              const assets = [...assetSet].filter(a => (venueBal[a] ?? 0) !== 0 || (inventoryFlash?.byVenueAsset?.[venue]?.[a] ?? 0) !== 0 || graphNodes.some(n => n.asset === a && n.balance > 0))
                .sort((a, b) => {
                  const rank = (x: string) => (isCashAssetName(x) ? 0 : x === "BTC" ? 1 : 2);
                  const d = rank(a) - rank(b);
                  return d !== 0 ? d : a.localeCompare(b);
                });
              // Fallback: mostrar al menos cash + BTC aunque estén en 0.
              const displayAssets = assets.length > 0 ? assets : (venue === "Binance" ? ["USDT", "BTC"] : ["USD", "BTC"]);
              const borrowedVenue = borrowedBalances?.[venue] ?? {};
              let cashOwned = 0;
              let hasFunds = false;
              let totalVenueUSD = 0;
              for (const a of displayAssets) {
                const real = venueBal[a] ?? graphNodes.find(n => n.asset === a)?.balance ?? 0;
                const shown = displayAmount(venue, a, real);
                if (Math.abs(shown) > 1e-9) hasFunds = true;
                if (isCashAssetName(a)) cashOwned += Math.max(0, real - (borrowedVenue[a] ?? 0));
                const px = graphNodes.find(n => n.asset === a)?.price_usd;
                totalVenueUSD += shown * (px && px > 0 ? px : isCashAssetName(a) ? 1 : 0);
              }
              // Fallback cash from legacy wallets flat.
              if (venue === "Binance" && !venueBal.USDT && wallets.binance.usd) {
                cashOwned = Math.max(0, wallets.binance.usd - state.borrowed.binance.usd);
                hasFunds = hasFunds || wallets.binance.usd > 0.01 || wallets.binance.btc > 1e-6;
              }
              if (venue === "Bitso" && !venueBal.USD && wallets.bitso.usd) {
                cashOwned = Math.max(0, wallets.bitso.usd - state.borrowed.bitso.usd);
                hasFunds = hasFunds || wallets.bitso.usd > 0.01 || wallets.bitso.btc > 1e-6;
              }
              const active = isVenueActive(venue);
              const status = venueStatusOf(venue, hasFunds);
              const healthPct = calculateHealth(cashOwned, venue, totalVenueUSD);
              const flashActive = !!inventoryFlash?.byVenueAsset?.[venue];
              return (
                <div
                  key={venue}
                  className={`bg-white dark:bg-gray-900 border rounded-xl p-5 relative overflow-hidden shadow-sm hover:shadow-lg transition-all duration-500 animate-fade-in-up group ${!active ? "opacity-60" : ""} ${flashActive ? "border-emerald-400 dark:border-emerald-500 shadow-[0_0_20px_rgba(16,185,129,0.25)]" : "border-gray-200 dark:border-gray-800"}`}
                  style={{ animationDelay: `${0.4 + idx * 0.08}s` }}
                >
                  <div className={`absolute inset-0 ${accent.hover} transition-colors duration-500 pointer-events-none`} />
                  <div className="flex justify-between items-center mb-6">
                    <div className="flex items-center gap-3">
                      <div className={`w-6 h-6 ${accent.badge} font-black flex items-center justify-center rounded-[4px] text-xs`}>
                        {venue.charAt(0).toUpperCase()}
                      </div>
                      <h3 className="text-gray-900 dark:text-gray-100 font-bold tracking-widest uppercase">{venue}</h3>
                      <VenueStatusPill status={status} />
                    </div>
                    <div className="flex items-center gap-2">
                      <button
                        onClick={() => openFundsModal(venue)}
                        className="flex items-center gap-1 text-[10px] font-bold uppercase tracking-widest text-blue-600 dark:text-blue-400 border border-blue-200 dark:border-blue-500/30 bg-blue-50 dark:bg-blue-500/10 hover:bg-blue-100 dark:hover:bg-blue-500/20 rounded-full px-2.5 py-1 transition-colors"
                        title={`Agregar o retirar dinero de ${venue}`}
                      >
                        <Pencil className="w-3 h-3" /> Editar fondos
                      </button>
                      <span className="text-[10px] border border-gray-200 dark:border-gray-800 bg-gray-50 dark:bg-gray-950 rounded-full px-3 py-1 text-gray-500 dark:text-gray-400 tracking-wider font-bold">
                        {venue === "Bitso" ? "MX" : "GLOBAL"}
                      </span>
                    </div>
                  </div>

                  <div className="grid grid-cols-2 sm:grid-cols-3 gap-4 mb-6">
                    {displayAssets.map(asset => {
                      let real = venueBal[asset];
                      if (real === undefined) {
                        if (venue === "Binance" && asset === "USDT") real = wallets.binance.usd;
                        else if (venue === "Binance" && asset === "BTC") real = wallets.binance.btc;
                        else if (venue === "Bitso" && asset === "USD") real = wallets.bitso.usd;
                        else if (venue === "Bitso" && asset === "BTC") real = wallets.bitso.btc;
                        else real = graphNodes.find(n => n.asset === asset)?.balance ?? 0;
                      }
                      const flash = inventoryFlash?.byVenueAsset?.[venue]?.[asset] ?? 0;
                      const shown = real + flash;
                      const borrowedAmt = borrowedVenue[asset] ?? (venue === "Binance" && asset === "USDT" ? state.borrowed.binance.usd : venue === "Binance" && asset === "BTC" ? state.borrowed.binance.btc : venue === "Bitso" && asset === "USD" ? state.borrowed.bitso.usd : venue === "Bitso" && asset === "BTC" ? state.borrowed.bitso.btc : 0);
                      const owned = Math.max(0, real - borrowedAmt);
                      return (
                        <div key={asset} className={flash !== 0 ? "hft-flash-cell" : undefined}>
                          <p className="text-gray-500 dark:text-gray-400 text-[10px] font-bold tracking-widest mb-1">
                            SALDO EN {isCashAssetName(asset) ? "USD" : asset}
                          </p>
                          <p className={`font-black text-lg ${isCashAssetName(asset) ? "text-gray-900 dark:text-gray-100" : accent.crypto}`}>
                            {formatAssetBalance(asset, shown)}
                          </p>
                          {flash !== 0 && (
                            <p className={`text-[10px] font-bold mt-0.5 ${flash > 0 ? "text-emerald-600 dark:text-emerald-400" : "text-amber-600 dark:text-amber-400"}`}>
                              {flash > 0 ? "+" : ""}{isCashAssetName(asset) ? `$${flash.toFixed(2)}` : flash.toFixed(4)} en esta op
                            </p>
                          )}
                          {borrowedAmt > 0 && (
                            <p className="text-[10px] text-blue-500 font-bold mt-1">
                              incl. {formatAssetBalance(asset, borrowedAmt)} prestado
                            </p>
                          )}
                          {borrowedAmt > 0 && isCashAssetName(asset) && (
                            <p className="text-[10px] text-gray-400 mt-0.5">
                              Propio: {formatAssetBalance(asset, owned)}
                            </p>
                          )}
                        </div>
                      );
                    })}
                  </div>

                  <div>
                    <div className="flex justify-between text-[10px] font-bold tracking-widest text-gray-500 dark:text-gray-400 mb-2">
                      <span>NIVEL DE FONDOS (PROPIOS)</span>
                      <span className={healthPct < 20 ? "text-red-500" : "text-emerald-600"}>{Math.round(healthPct)}%</span>
                    </div>
                    <div className="w-full bg-gray-100 dark:bg-gray-800 h-1.5 rounded-full overflow-hidden">
                      <div
                        className={`h-full rounded-full transition-all duration-500 ${healthPct < 20 ? "bg-red-500" : "bg-emerald-500"}`}
                        style={{ width: `${healthPct}%` }}
                      />
                    </div>
                  </div>
                  {!active && <InactiveVenueCTA onActivate={() => activateVenue(venue)} />}
                </div>
              );
            })}

            {/* Distribución del capital — derivada de los NODOS DEL RADAR: todo el
                dinero real (todos los venues y activos, valorados por el motor),
                no un 4-buckets fijo de Binance/Bitso. El 40/40/20 de un experto y
                el ETH de un ciclo triangular aparecen aquí solos. */}
            {(() => {
              type Bucket = { label: string; usd: number; color: string };
              const PALETTE = ["#EAB308", "#2563EB", "#F97316", "#06B6D4", "#7C3AED", "#10B981", "#EC4899", "#64748B"];
              const nodes = (state.graph?.nodes ?? []).filter(n => n.balance_usd > 0.01);
              const totalUSD = nodes.reduce((s, n) => s + n.balance_usd, 0);

              // Un bucket por activo@venue con saldo, de mayor a menor; los que no
              // caben en la paleta se agrupan en "Otros" (nunca se oculta dinero).
              const sorted: Bucket[] = nodes
                .map(n => ({ label: `${n.venue} · ${n.asset}`, usd: n.balance_usd, color: "" }))
                .sort((a, b) => b.usd - a.usd);
              const buckets = sorted.slice(0, PALETTE.length - 1).map((b, i) => ({ ...b, color: PALETTE[i] }));
              const rest = sorted.slice(PALETTE.length - 1);
              if (rest.length > 0) {
                buckets.push({ label: "Otros", usd: rest.reduce((s, b) => s + b.usd, 0), color: PALETTE[PALETTE.length - 1] });
              }

              let acc = 0;
              const stops = buckets.map(b => {
                const from = acc;
                acc += totalUSD > 0 ? (b.usd / totalUSD) * 100 : 0;
                return `${b.color} ${from}% ${acc}%`;
              });
              const gradient = `conic-gradient(${stops.join(", ")})`;
              const compact = new Intl.NumberFormat("en-US", { notation: "compact", maximumFractionDigits: 1 });

              return (
                <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-800 rounded-xl p-5 mt-2 shadow-sm animate-fade-in-up hover:shadow-lg transition-all duration-500 group" style={{ animationDelay: '0.6s' }}>
                  <p className="text-gray-500 dark:text-gray-400 text-[10px] font-bold tracking-widest mb-6">DÓNDE ESTÁ TU DINERO</p>
                  {buckets.length === 0 ? (
                    <p className="text-xs text-gray-400 dark:text-gray-500 py-6 text-center">Esperando la primera lectura del radar…</p>
                  ) : (
                    <div className="flex items-center gap-6">
                      <div className="relative w-24 h-24 rounded-full flex items-center justify-center shadow-sm transition-all duration-700 group-hover:scale-105 flex-shrink-0" style={{ background: gradient }}>
                        <div className="w-20 h-20 bg-white dark:bg-gray-900 rounded-full flex items-center justify-center flex-col z-10 shadow-inner">
                          <p className="text-[10px] text-gray-400 font-bold">TOTAL</p>
                          <p className="text-gray-900 dark:text-gray-100 font-black text-lg">${compact.format(totalUSD)}</p>
                        </div>
                      </div>
                      <div className="flex-1 space-y-2.5 min-w-0">
                        {buckets.map(b => (
                          <div key={b.label} className="flex justify-between items-center gap-2 text-xs" title={`$${b.usd.toLocaleString("en-US", { maximumFractionDigits: 2 })}`}>
                            <div className="flex items-center gap-2 min-w-0">
                              <div className="w-2 h-2 rounded-full flex-shrink-0" style={{ background: b.color }}></div>
                              <span className="text-gray-600 dark:text-gray-400 font-bold truncate">{b.label}</span>
                            </div>
                            <span className="text-gray-900 dark:text-gray-100 font-bold flex-shrink-0">{totalUSD > 0 ? ((b.usd / totalUSD) * 100).toFixed(1) : "0.0"}%</span>
                          </div>
                        ))}
                      </div>
                    </div>
                  )}
                </div>
              );
            })()}
          </div>

          {/* Columna Derecha (Feed de Operaciones) */}
          <div className="w-full lg:flex-1 relative flex flex-col min-h-[500px] lg:min-h-0 animate-fade-in-up" style={{ animationDelay: '0.3s' }}>
            <div className="lg:absolute lg:inset-0 flex flex-col flex-1 bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-800 rounded-xl p-6 shadow-sm overflow-hidden hover:shadow-xl transition-all duration-500">
              <div className="flex justify-between items-start mb-4 border-b border-gray-100 dark:border-gray-800 pb-4 flex-shrink-0">
              <h2 className="text-gray-500 dark:text-gray-400 font-bold tracking-widest text-[11px] flex items-center justify-between w-full uppercase">
                <div className="flex items-center gap-2">
                  <span className="w-2 h-2 rounded-full bg-emerald-500 animate-pulse"></span>
                  OPERACIONES EN VIVO
                </div>
                <button
                  onClick={() => setShowGuideModal(true)}
                  className="w-6 h-6 rounded-full bg-gray-100 dark:bg-gray-800 text-gray-500 dark:text-gray-400 hover:bg-emerald-100 dark:hover:bg-emerald-900/30 hover:text-emerald-600 dark:hover:text-emerald-400 transition-colors flex items-center justify-center border border-gray-200 dark:border-gray-700 hover:border-emerald-300 dark:hover:border-emerald-500/50 shadow-sm"
                  title="¿Cómo funciona la estrategia?"
                >
                  <HelpCircle className="w-3.5 h-3.5" />
                </button>
              </h2>
            </div>
            
            <div className="flex-1 overflow-y-auto overflow-x-auto overscroll-contain pr-2">
              {trades.length === 0 ? (
                <div className="h-full flex flex-col items-center justify-center text-gray-400 dark:text-gray-500 min-h-[300px]">
                  <div className="w-16 h-16 border-4 border-gray-200 dark:border-gray-800 border-t-emerald-500 rounded-full animate-spin mb-4"></div>
                  <p className="text-sm font-bold tracking-widest uppercase">BUSCANDO OPORTUNIDADES...</p>
                  <p className="text-[10px] mt-2 max-w-xs text-center opacity-70">El bot compara precios entre tus casas de cambio activas en tiempo real. Operará solo si la ganancia neta supera tu margen mínimo.</p>
                </div>
              ) : (
                <div className="w-full min-w-[500px]">
                  <table className="w-full text-left border-collapse">
                    <thead>
                      <tr className="text-[10px] text-gray-500 dark:text-gray-400 font-bold tracking-widest uppercase border-b border-gray-100 dark:border-gray-800">
                        <th className="pb-3 pt-2 font-bold whitespace-nowrap">HORA</th>
                        <th className="pb-3 pt-2 font-bold whitespace-nowrap">RUTA (COMPRA ➔ VENTA)</th>
                        <th className="pb-3 pt-2 font-bold text-right whitespace-nowrap">CANTIDAD (BTC)</th>
                        <th className="pb-3 pt-2 font-bold text-right whitespace-nowrap">GANANCIA</th>
                      </tr>
                    </thead>
                    <tbody>
                      {trades.map((trade, i) => (
                        <tr key={`${trade.timestamp}-${i}`} className={`border-b border-gray-50 dark:border-gray-800/50 hover:bg-gray-50/50 dark:hover:bg-gray-800/20 transition-all duration-300 group ${i === 0 ? 'animate-new-trade' : ''}`}>
                          <td className="py-3 text-xs text-gray-500 dark:text-gray-400 whitespace-nowrap">{formatTime(trade.timestamp)}</td>
                          <td className="py-3 whitespace-nowrap">
                            <div className="flex items-center gap-2">
                              <VenueBadge name={trade.exchange_buy} />
                              <ArrowRight className="w-3 h-3 text-gray-300 dark:text-gray-600 transition-transform group-hover:translate-x-1" />
                              <VenueBadge name={trade.exchange_sell} />
                            </div>
                          </td>
                          <td className="py-3 text-right font-black text-sm whitespace-nowrap">
                            {trade.volume?.toFixed(4) ?? "—"}
                          </td>
                          <td className="py-3 text-right whitespace-nowrap">
                            <span className="inline-flex items-center gap-1.5 bg-emerald-50 dark:bg-emerald-500/10 text-emerald-600 border border-emerald-200 dark:border-emerald-500/20 px-3 py-1 rounded-md font-black shadow-sm group-hover:bg-emerald-100 dark:group-hover:bg-emerald-500/20 transition-colors">
                              +${trade.net_profit_usd.toFixed(2)}
                            </span>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </div>
            </div>
          </div>
        </div>

        {/* El radar vive ahora en su propia vista (tab RADAR); la estrategia,
            en el drawer global — aquí queda el resto del panel completo. */}

        {/* Ledger / Auditoría Institucional — demuestra la persistencia de datos */}
        <LedgerPanel sessionId={state.sessionId} />

        {/* Analítica del ledger (Sprint D) — P&L acumulado, win rate y export CSV */}
        <AnalyticsPanel sessionId={state.sessionId} />
      </main>
      )}
    </div>
  );
}
