"use client";

import { useEffect, useState, useRef } from "react";
import { ShieldAlert, CheckCircle, Moon, Sun, Loader2, ArrowRight, HelpCircle, X, ArrowDown, Pencil, BookOpen } from "lucide-react";
import { useArusEngine, LogEntry } from "../hooks/useArusEngine";
import { OnboardingModal } from "../components/OnboardingModal";
import { LedgerPanel } from "../components/LedgerPanel";
import { TutorialModal } from "../components/TutorialModal";

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
  open, profitPotential, creditCost, onRequestCredit, onWaitRebalance, onShutdown 
}: {
  open: boolean;
  profitPotential: number;
  creditCost: number;
  onRequestCredit: () => void;
  onWaitRebalance: () => void;
  onShutdown: () => void;
}) {
  const [loading, setLoading] = useState<"credit" | "wait" | null>(null);

  useEffect(() => {
    if (open) setLoading(null);
  }, [open]);

  if (!open) return null;
  const isProfitable = profitPotential > creditCost;
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
          <div className="flex justify-between border-t border-gray-200 dark:border-gray-800 pt-2 mt-2">
            <span className="text-gray-900 dark:text-gray-100 font-bold">Te quedaría</span>
            <span className={`font-black ${isProfitable ? "text-emerald-500" : "text-red-500"}`}>
              ${(profitPotential - creditCost).toFixed(2)}
            </span>
          </div>
        </div>
        <div className="flex flex-col gap-3">
          <button 
            className="w-full bg-orange-500 hover:bg-orange-400 disabled:opacity-40 disabled:cursor-not-allowed text-white p-3 rounded-lg font-bold text-xs uppercase tracking-widest transition-all duration-300 flex justify-center items-center gap-2"
            onClick={handleRequestCredit}
            disabled={loading !== null || !isProfitable}
            title={!isProfitable ? "La ganancia no cubre el costo del préstamo" : undefined}
          >
            {loading === "credit" ? <><Loader2 className="w-4 h-4 animate-spin" /> Procesando...</> : "Pedir préstamo y seguir operando"}
          </button>
          <button
            className="w-full bg-blue-600 hover:bg-blue-500 text-white p-3 rounded-lg font-bold text-xs uppercase tracking-widest transition-all duration-300 flex justify-center items-center gap-2"
            onClick={handleWait}
            disabled={loading !== null}
          >
            {loading === "wait" ? <><Loader2 className="w-4 h-4 animate-spin" /> Iniciando...</> : "Esperar reequilibrio (1 min demo)"}
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
              <p className="text-xs font-bold uppercase tracking-widest text-emerald-600">La magia del Arbitraje</p>
            </div>
          </div>
          
          <p className="text-sm text-gray-600 dark:text-gray-400 mb-8 leading-relaxed">
            El bot aprovecha las <strong>diferencias de precio</strong> del mismo activo (como Bitcoin) en diferentes casas de cambio. Compra barato y vende caro <strong>al mismo tiempo</strong>.
          </p>

          <div className="bg-gray-50 dark:bg-gray-950 border border-gray-200 dark:border-gray-800 rounded-xl p-6 mb-8 relative pt-10">
            <div className="absolute top-0 left-1/2 -translate-x-1/2 -translate-y-1/2 bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-800 text-xs font-bold tracking-widest uppercase px-4 py-1.5 rounded-full shadow-sm text-gray-500 flex items-center gap-2">
              <span className="w-2 h-2 rounded-full bg-blue-500"></span>
              Ejemplo visual
            </div>
            
            <div className="flex flex-col sm:flex-row items-center justify-between gap-4 mt-2">
              {/* BINANCE */}
              <div className="bg-white dark:bg-gray-900 border-2 border-gray-200 dark:border-gray-800 rounded-lg p-5 w-full sm:w-[30%] text-center shadow-sm relative">
                <p className="text-xs font-bold text-gray-500 mb-1 tracking-widest uppercase">CASA 1</p>
                <p className="font-black text-lg text-gray-800 dark:text-gray-200">Binance</p>
                <div className="bg-emerald-50 dark:bg-emerald-900/20 text-emerald-600 dark:text-emerald-400 font-black text-base rounded py-2 mt-3 border border-emerald-100 dark:border-emerald-800/50 shadow-inner">
                  1 BTC = $60,000
                </div>
                <div className="absolute -bottom-3 left-1/2 -translate-x-1/2 bg-emerald-500 text-white text-[10px] font-bold uppercase tracking-widest px-3 py-1 rounded-full shadow-md whitespace-nowrap">
                  Compramos barato
                </div>
              </div>

              {/* FLECHA */}
              <div className="flex flex-col items-center flex-1 py-4 sm:py-0">
                <div className="hidden sm:flex items-center w-full justify-center gap-2">
                  <div className="h-0.5 bg-gray-300 dark:bg-gray-700 w-8"></div>
                  <span className="text-[10px] font-bold text-gray-500 dark:text-gray-400 uppercase tracking-widest bg-gray-100 dark:bg-gray-800 px-3 py-1 rounded-full border border-gray-200 dark:border-gray-700 whitespace-nowrap">
                    Al mismo tiempo
                  </span>
                  <div className="h-0.5 bg-gray-300 dark:bg-gray-700 w-8"></div>
                </div>
                
                <div className="flex sm:hidden items-center flex-col justify-center gap-2 my-2">
                  <div className="w-0.5 bg-gray-300 dark:bg-gray-700 h-4"></div>
                  <span className="text-[10px] font-bold text-gray-500 dark:text-gray-400 uppercase tracking-widest bg-gray-100 dark:bg-gray-800 px-3 py-1 rounded-full border border-gray-200 dark:border-gray-700 whitespace-nowrap">
                    Al mismo tiempo
                  </span>
                  <div className="w-0.5 bg-gray-300 dark:bg-gray-700 h-4"></div>
                </div>
              </div>

              {/* BITSO */}
              <div className="bg-white dark:bg-gray-900 border-2 border-gray-200 dark:border-gray-800 rounded-lg p-5 w-full sm:w-[30%] text-center shadow-sm relative">
                <p className="text-xs font-bold text-gray-500 mb-1 tracking-widest uppercase">CASA 2</p>
                <p className="font-black text-lg text-gray-800 dark:text-gray-200">Bitso</p>
                <div className="bg-blue-50 dark:bg-blue-900/20 text-blue-600 dark:text-blue-400 font-black text-base rounded py-2 mt-3 border border-blue-100 dark:border-blue-800/50 shadow-inner">
                  1 BTC = $61,000
                </div>
                <div className="absolute -bottom-3 left-1/2 -translate-x-1/2 bg-blue-500 text-white text-[10px] font-bold uppercase tracking-widest px-3 py-1 rounded-full shadow-md whitespace-nowrap">
                  Vendemos caro
                </div>
              </div>
            </div>

            <div className="mt-10 bg-emerald-50 dark:bg-emerald-900/20 border border-emerald-200 dark:border-emerald-800/50 rounded-xl p-5 text-center shadow-sm">
              <p className="text-sm font-bold text-emerald-800 dark:text-emerald-300 flex items-center justify-center gap-2">
                Resultado de la operación: 
                <span className="text-2xl font-black text-emerald-600 dark:text-emerald-400 bg-white dark:bg-emerald-950 px-3 py-1 rounded-lg border border-emerald-200 dark:border-emerald-800/50">
                  +$1,000 USD
                </span>
              </p>
              <p className="text-[11px] mt-3 text-emerald-700/80 dark:text-emerald-400/80 uppercase tracking-widest font-bold flex items-center justify-center gap-1.5">
                <ShieldAlert className="w-3.5 h-3.5" /> Ganancia sin riesgo de mercado
              </p>
            </div>
          </div>

          <div className="space-y-4 text-sm text-gray-600 dark:text-gray-400 border-t border-gray-100 dark:border-gray-800 pt-6">
            <h3 className="font-bold text-gray-900 dark:text-gray-100 uppercase tracking-widest text-xs mb-4">Condiciones para que funcione</h3>
            <div className="grid sm:grid-cols-3 gap-4">
              <div className="bg-gray-50 dark:bg-gray-950 p-4 rounded-lg border border-gray-100 dark:border-gray-800">
                <span className="text-xl mb-2 block">⚖️</span>
                <p className="text-xs font-bold text-gray-800 dark:text-gray-200 mb-1">1. Diferencia real</p>
                <p className="text-[11px] leading-relaxed">Debe existir un hueco (spread) entre los precios de ambas casas.</p>
              </div>
              <div className="bg-gray-50 dark:bg-gray-950 p-4 rounded-lg border border-gray-100 dark:border-gray-800">
                <span className="text-xl mb-2 block">📉</span>
                <p className="text-xs font-bold text-gray-800 dark:text-gray-200 mb-1">2. Superar comisiones</p>
                <p className="text-[11px] leading-relaxed">La ganancia debe ser mayor a las comisiones de compra/venta (~0.1%).</p>
              </div>
              <div className="bg-gray-50 dark:bg-gray-950 p-4 rounded-lg border border-gray-100 dark:border-gray-800">
                <span className="text-xl mb-2 block">💰</span>
                <p className="text-xs font-bold text-gray-800 dark:text-gray-200 mb-1">3. Liquidez en ambas</p>
                <p className="text-[11px] leading-relaxed">Necesitamos USD en una casa para comprar y BTC en la otra para vender.</p>
              </div>
            </div>
          </div>

          <div className="space-y-4 text-sm text-gray-600 dark:text-gray-400 border-t border-gray-100 dark:border-gray-800 pt-6 mt-6">
            <h3 className="font-bold text-blue-600 dark:text-blue-400 uppercase tracking-widest text-xs mb-4 flex items-center gap-2">
              <span className="w-2 h-2 rounded-full bg-blue-500 animate-pulse"></span>
              La ventaja de nuestro bot
            </h3>
            <div className="grid sm:grid-cols-2 gap-4">
              <div className="bg-blue-50 dark:bg-blue-900/10 p-4 rounded-lg border border-blue-100 dark:border-blue-900/30">
                <span className="text-xl mb-2 block">⚡</span>
                <p className="text-xs font-bold text-blue-900 dark:text-blue-200 mb-1">Cero tiempos muertos (Préstamos)</p>
                <p className="text-[11px] leading-relaxed text-blue-800/80 dark:text-blue-300/80">
                  Mover fondos de un exchange a otro suele tardar más de 30 minutos. Nuestro sistema elimina esa espera <strong>ofreciéndote crédito instantáneo</strong> si la oportunidad lo vale, asegurando que nunca dejes de ganar.
                </p>
              </div>
              <div className="bg-blue-50 dark:bg-blue-900/10 p-4 rounded-lg border border-blue-100 dark:border-blue-900/30">
                <span className="text-xl mb-2 block">🛡️</span>
                <p className="text-xs font-bold text-blue-900 dark:text-blue-200 mb-1">Filtro de seguridad avanzado</p>
                <p className="text-[11px] leading-relaxed text-blue-800/80 dark:text-blue-300/80">
                  Contamos con un mecanismo que detecta "precios falsos" o errores bruscos del mercado. Bloqueamos cualquier jugada sospechosa para proteger todo tu capital de riesgos innecesarios.
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

function FundsModal({ exchange, usd, btc, onClose, onSubmit }: {
  exchange: string;
  usd: number;
  btc: number;
  onClose: () => void;
  onSubmit: (currency: "USD" | "BTC", amount: number) => void;
}) {
  const [mode, setMode] = useState<"deposit" | "withdraw">("deposit");
  const [currency, setCurrency] = useState<"USD" | "BTC">("USD");
  const [amount, setAmount] = useState<number | "">("");

  const balance = currency === "USD" ? usd : btc;
  const amt = typeof amount === "number" ? amount : 0;
  const tooMuch = mode === "withdraw" && amt > balance;
  const valid = amt > 0 && !tooMuch;

  const handleConfirm = () => {
    if (!valid) return;
    onSubmit(currency, mode === "deposit" ? amt : -amt);
    onClose();
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
          Agrega o retira dinero de esta casa de cambio. No cuenta como ganancia ni pérdida del bot.
        </p>

        {/* Agregar / Retirar */}
        <div className="flex gap-2 mb-3">
          <button onClick={() => setMode("deposit")} className={tab(mode === "deposit", "bg-emerald-500")}>Agregar</button>
          <button onClick={() => setMode("withdraw")} className={tab(mode === "withdraw", "bg-amber-500")}>Retirar</button>
        </div>

        {/* USD / BTC */}
        <div className="flex gap-2 mb-4">
          <button onClick={() => { setCurrency("USD"); setAmount(""); }} className={tab(currency === "USD", "bg-gray-900 dark:bg-gray-200 dark:!text-gray-900")}>USD</button>
          <button onClick={() => { setCurrency("BTC"); setAmount(""); }} className={tab(currency === "BTC", "bg-gray-900 dark:bg-gray-200 dark:!text-gray-900")}>BTC</button>
        </div>

        <p className="text-[11px] text-gray-500 dark:text-gray-400 mb-1">
          Saldo actual: <span className="font-bold text-gray-700 dark:text-gray-200">
            {currency === "USD" ? `$${usd.toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 })}` : `${btc.toFixed(4)} ₿`}
          </span>
        </p>

        <input
          type="number"
          autoFocus
          value={amount}
          onChange={(e) => setAmount(e.target.value === "" ? "" : Math.abs(parseFloat(e.target.value)) || 0)}
          onKeyDown={(e) => { if (e.key === "Enter") handleConfirm(); }}
          placeholder={currency === "USD" ? "Ej. 5000" : "Ej. 0.25"}
          step={currency === "USD" ? "100" : "0.01"}
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

export default function Home() {
  const { sessionReady, state, initSession, resetSession, demoInject, toggleAutoCredit, requestCredit, waitRebalance, adjustFunds, shutdownEngine } = useArusEngine();
  
  const [showGuideModal, setShowGuideModal] = useState(false);
  const [showInjectionModal, setShowInjectionModal] = useState(false);
  const [injectionExchange, setInjectionExchange] = useState("Binance");
  const [injectionOffset, setInjectionOffset] = useState<number | "">("");
  const [injectionLiquidity, setInjectionLiquidity] = useState<number | "">("");
  const [injectionToastMessage, setInjectionToastMessage] = useState("");
  const [isDarkMode, setIsDarkMode] = useState(false);
  const [fundsModal, setFundsModal] = useState<string | null>(null);
  const [showTutorial, setShowTutorial] = useState(false);

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

  useEffect(() => {
    if (typeof window !== 'undefined') {
      const mediaQuery = window.matchMedia('(prefers-color-scheme: dark)');
      setIsDarkMode(mediaQuery.matches);

      const handleChange = (e: MediaQueryListEvent) => setIsDarkMode(e.matches);
      mediaQuery.addEventListener('change', handleChange);

      return () => mediaQuery.removeEventListener('change', handleChange);
    }
  }, []);

  useEffect(() => {
    if (!stickToBottomRef.current) return;
    const el = terminalScrollRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [state.logs]);

  const handleInjectSpread = (exchange?: string, offset?: number, liquidity?: number) => {
    setShowInjectionModal(false);
    
    const finalExchange = exchange || injectionExchange;
    const finalOffset = offset !== undefined ? offset : (typeof injectionOffset === 'number' ? injectionOffset : 0);
    const finalLiquidity = liquidity !== undefined ? liquidity : (typeof injectionLiquidity === 'number' ? injectionLiquidity : 1.0);

    demoInject(finalExchange, finalOffset, finalLiquidity);

    setInjectionToastMessage(`⚠️ Prueba iniciada: simulando el mercado de ${finalExchange} con ${finalLiquidity} BTC disponibles`);
    setTimeout(() => setInjectionToastMessage(""), 5000);
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

  const calculateHealth = (ownedUsd: number) => {
    const maxUSD = state.initialUsd ? state.initialUsd / 2 : 60000;
    if (maxUSD === 0) return 0;
    return Math.min(100, Math.max(0, (Math.max(0, ownedUsd) / maxUSD) * 100));
  };

  if (!sessionReady) {
    return <OnboardingModal onInit={initSession} />;
  }

  const { wallets, totalWealth, trades } = state;
  
  const actualPnl = state.initialWealth > 0 ? state.totalWealth - state.initialWealth : null;
  const pnlPercentage = state.initialWealth > 0 ? (actualPnl! / state.initialWealth) * 100 : null;

  const ownedBinanceUsd = Math.max(0, wallets.binance.usd - state.borrowed.binance.usd);
  const ownedBitsoUsd = Math.max(0, wallets.bitso.usd - state.borrowed.bitso.usd);
  const ownedBinanceBtc = Math.max(0, wallets.binance.btc - state.borrowed.binance.btc);
  const ownedBitsoBtc = Math.max(0, wallets.bitso.btc - state.borrowed.bitso.btc);

  return (
    <div className={`min-h-screen bg-gray-50 dark:bg-gray-950 text-gray-900 dark:text-gray-100 font-mono flex flex-col transition-all duration-700 relative overflow-x-hidden ${state.creditActiveState?.active ? 'border-t-4 border-blue-500' : state.isRebalancing ? 'border-t-4 border-amber-500' : ''} ${isDarkMode ? 'dark' : ''}`}>
      
      {!state.engineRunning && (
        <div className="fixed inset-0 z-[200] flex items-center justify-center bg-gray-900/90 backdrop-blur-md">
          <div className="text-center animate-modal-scale">
            <h1 className="text-4xl font-black text-red-500 mb-4 tracking-widest uppercase">BOT DETENIDO</h1>
            <p className="text-gray-300 mb-8 max-w-md mx-auto">El bot se detuvo. Recarga la página para empezar de nuevo.</p>
            <button onClick={() => window.location.reload()} className="px-6 py-3 bg-white text-gray-900 font-bold uppercase tracking-widest text-sm rounded-lg hover:bg-gray-200 transition-colors">Reiniciar</button>
          </div>
        </div>
      )}

      <InsufficientFundsModal 
        open={state.insufficientFundsModal?.open}
        profitPotential={state.insufficientFundsModal?.profitPotential}
        creditCost={state.insufficientFundsModal?.creditCost}
        onRequestCredit={requestCredit}
        onWaitRebalance={waitRebalance}
        onShutdown={shutdownEngine}
      />
      
      <StrategyGuideModal
        open={showGuideModal}
        onClose={() => setShowGuideModal(false)}
      />

      <TutorialModal open={showTutorial} onClose={closeTutorial} />

      {fundsModal && (
        <FundsModal
          exchange={fundsModal}
          usd={fundsModal === "Binance" ? wallets.binance.usd : wallets.bitso.usd}
          btc={fundsModal === "Binance" ? wallets.binance.btc : wallets.bitso.btc}
          onClose={() => setFundsModal(null)}
          onSubmit={(currency, amount) => adjustFunds(fundsModal, currency, amount)}
        />
      )}
      
      {/* Modal de Inyección de Spread */}
      {showInjectionModal && (
        <div className="fixed inset-0 z-[100] overflow-y-auto bg-gray-900/60 backdrop-blur-sm">
          <div className="flex min-h-full items-center justify-center p-2 sm:p-4">
            <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-800 rounded-xl max-w-5xl w-full my-4 max-h-[94vh] overflow-y-auto p-4 sm:p-6 lg:p-8 shadow-2xl transform transition-all animate-modal-scale">
            <h2 className="text-xl sm:text-2xl font-black text-gray-900 dark:text-gray-100 mb-2 flex items-center gap-3">
              <ShieldAlert className="text-red-600 w-6 h-6 sm:w-7 sm:h-7 animate-pulse" />
              Modo de pruebas del bot
            </h2>
            <p className="text-gray-600 dark:text-gray-400 text-xs sm:text-sm mb-6 leading-relaxed">
              Aquí puedes simular distintas situaciones del mercado y ver cómo reacciona el bot: una oportunidad normal, un evento poco común o un precio falso por error. Es 100% seguro, no se usa dinero real.
            </p>
            
            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4 sm:gap-6">
              
              {/* Columna Izquierda: Configuración Manual */}
              <div className="bg-gray-50 dark:bg-gray-950 p-4 sm:p-6 rounded-xl border border-gray-200 dark:border-gray-800 shadow-sm flex flex-col h-full">
                <h3 className="text-gray-800 dark:text-gray-200 font-bold tracking-widest text-xs mb-5 uppercase border-b border-gray-200 dark:border-gray-800 pb-2 flex-shrink-0">Crear tu propia prueba</h3>
                <div className="flex flex-col gap-4 flex-1">
                  <div>
                    <label className="text-[10px] font-bold tracking-widest text-gray-500 dark:text-gray-400 uppercase">Casa de cambio a simular</label>
                    <select 
                      value={injectionExchange}
                      onChange={e => setInjectionExchange(e.target.value)}
                      className="w-full bg-white dark:bg-gray-900 border border-gray-300 dark:border-gray-700 text-gray-900 dark:text-gray-100 p-3 rounded-lg outline-none focus:border-red-500 focus:ring-1 focus:ring-red-500 transition-colors mt-1 text-sm shadow-sm"
                    >
                      <option value="Binance">Binance</option>
                      <option value="Bitso">Bitso</option>
                    </select>
                  </div>
                  <div>
                    <label className="text-[10px] font-bold tracking-widest text-gray-500 dark:text-gray-400 uppercase">Diferencia de precio (USD)</label>
                    <input
                      type="number"
                      value={injectionOffset}
                      onChange={e => setInjectionOffset(e.target.value === "" ? "" : parseFloat(e.target.value) || 0)}
                      className="w-full bg-white dark:bg-gray-900 border border-gray-300 dark:border-gray-700 text-gray-900 dark:text-gray-100 p-3 rounded-lg outline-none focus:border-red-500 focus:ring-1 focus:ring-red-500 transition-colors mt-1 font-mono text-sm shadow-sm"
                      placeholder="Ej. 2000 o -1500"
                    />
                    <p className="text-[10px] text-gray-400 mt-1.5 leading-relaxed">Cuánto más caro (o barato) estará el bitcoin en esta casa de cambio frente a la otra. Más diferencia = más ganancia posible.</p>
                  </div>
                  <div>
                    <label className="text-[10px] font-bold tracking-widest text-gray-500 dark:text-gray-400 uppercase">Cantidad disponible (BTC)</label>
                    <input
                      type="number"
                      step="0.1"
                      value={injectionLiquidity}
                      onChange={e => setInjectionLiquidity(e.target.value === "" ? "" : parseFloat(e.target.value) || 0)}
                      className="w-full bg-white dark:bg-gray-900 border border-gray-300 dark:border-gray-700 text-gray-900 dark:text-gray-100 p-3 rounded-lg outline-none focus:border-red-500 focus:ring-1 focus:ring-red-500 transition-colors mt-1 font-mono text-sm shadow-sm"
                      placeholder="Ej. 1.5 o 0.1"
                      min="0.1"
                      max="10"
                    />
                    <div className="mt-3 bg-blue-50 dark:bg-blue-500/10 border border-blue-200 dark:border-blue-500/20 rounded-md p-3">
                      <p className="text-[10px] leading-relaxed text-blue-700 dark:text-blue-400 font-bold">
                        Es el volumen total de bitcoin disponible a este precio. El bot operará automáticamente dividiendo esta cantidad en múltiples transacciones pequeñas y seguras hasta agotarlo.
                      </p>
                    </div>
                  </div>
                  
                  <CreditToggle autoMode={state.autoCreditMode} onToggle={toggleAutoCredit} />

                  <div className="mt-auto pt-4">
                    <button 
                      onClick={() => handleInjectSpread()}
                      className="w-full bg-red-600 hover:bg-red-500 text-white p-4 rounded-lg font-black text-sm text-center shadow-md transition-all duration-300 hover:-translate-y-1 hover:shadow-xl hover:shadow-red-500/30 active:scale-95"
                    >
                      Iniciar esta prueba
                    </button>
                  </div>
                </div>
              </div>

              {/* Columna Centro: Acciones Rápidas */}
              <div className="bg-gray-50 dark:bg-gray-950 p-4 sm:p-6 rounded-xl border border-gray-200 dark:border-gray-800 shadow-sm flex flex-col h-full">
                <h3 className="text-gray-800 dark:text-gray-200 font-bold tracking-widest text-xs mb-5 uppercase border-b border-gray-200 dark:border-gray-800 pb-2 flex-shrink-0">Pruebas rápidas</h3>

                <div className="flex flex-col gap-4 flex-1">
                  <button
                    onClick={() => handleInjectSpread("Bitso", 800, 0.1)}
                    className="w-full bg-white dark:bg-gray-900 hover:bg-gray-50 dark:bg-gray-950 border border-gray-200 dark:border-gray-800 hover:border-emerald-400 p-4 rounded-lg text-left transition-all duration-300 hover:-translate-y-1 hover:shadow-lg group shadow-sm flex flex-col h-full"
                  >
                    <div className="flex justify-between items-center mb-1">
                      <span className="text-gray-900 dark:text-gray-100 font-bold text-sm flex items-center gap-2">
                        ⚡ Oportunidad normal
                      </span>
                    </div>
                    <div className="text-[10px] text-gray-500 dark:text-gray-400 font-mono mb-2 bg-gray-100 dark:bg-gray-800 p-1.5 rounded inline-block">Casa: Bitso | Diferencia: $800 | Vol: 0.1 BTC</div>
                    <p className="text-gray-600 dark:text-gray-400 text-xs mt-auto leading-relaxed">Una diferencia de precio razonable. El bot debería aprovecharla sin problema y ganar dinero.</p>
                  </button>

                  <button
                    onClick={() => handleInjectSpread("Bitso", 1300, 0.3)}
                    className="w-full bg-white dark:bg-gray-900 hover:bg-gray-50 dark:bg-gray-950 border border-gray-200 dark:border-gray-800 hover:border-amber-400 p-4 rounded-lg text-left transition-all duration-300 hover:-translate-y-1 hover:shadow-lg group shadow-sm flex flex-col h-full"
                  >
                    <div className="flex justify-between items-center mb-1">
                      <span className="text-amber-600 font-bold text-sm flex items-center gap-2">
                        ⚠️ Evento poco común
                      </span>
                    </div>
                    <div className="text-[10px] text-amber-700 dark:text-amber-400 font-mono mb-2 bg-amber-50 dark:bg-amber-900/30 p-1.5 rounded inline-block">Casa: Bitso | Diferencia: $1300 | Vol: 0.3 BTC</div>
                    <p className="text-gray-600 dark:text-gray-400 text-xs mt-auto leading-relaxed">Una diferencia grande pero todavía real. El bot opera, pero avisa de que es algo inusual.</p>
                  </button>

                  <button
                    onClick={() => handleInjectSpread("Binance", 10000, 0.1)}
                    className="w-full bg-white dark:bg-gray-900 hover:bg-gray-50 dark:bg-gray-950 border border-gray-200 dark:border-gray-800 hover:border-red-400 p-4 rounded-lg text-left transition-all duration-300 hover:-translate-y-1 hover:shadow-lg group shadow-sm flex flex-col h-full"
                  >
                    <div className="flex justify-between items-center mb-1">
                      <span className="text-red-600 font-bold text-sm flex items-center gap-2">
                        🛑 Precio falso (error)
                      </span>
                    </div>
                    <div className="text-[10px] text-red-700 dark:text-red-400 font-mono mb-2 bg-red-50 dark:bg-red-900/30 p-1.5 rounded inline-block">Casa: Binance | Diferencia: $10000 | Vol: 0.1 BTC</div>
                    <p className="text-gray-600 dark:text-gray-400 text-xs mt-auto leading-relaxed">Una diferencia enorme, casi seguro un error del mercado. El bot debe bloquearla para proteger tu dinero.</p>
                  </button>
                </div>
              </div>

              {/* Columna Derecha: Live Terminal Console Log */}
              <div className="relative flex flex-col h-full w-full min-h-[220px] sm:min-h-[300px] md:col-span-2 lg:col-span-1 lg:min-h-[400px]">
                <div className="absolute inset-0 flex flex-col bg-[#0a0e14] border border-gray-300 dark:border-gray-700 rounded-xl overflow-hidden shadow-md">
                  <div className="bg-gray-800 px-4 py-2 flex items-center gap-2 border-b border-gray-700 flex-shrink-0">
                    <div className="w-2.5 h-2.5 rounded-full bg-red-500"></div>
                    <div className="w-2.5 h-2.5 rounded-full bg-amber-500"></div>
                    <div className="w-2.5 h-2.5 rounded-full bg-emerald-500"></div>
                    <span className="ml-2 text-[10px] lg:text-xs font-bold text-gray-300 tracking-widest uppercase">Actividad del bot en vivo</span>
                  </div>
                  <div
                    ref={terminalScrollRef}
                    onScroll={handleTerminalScroll}
                    className="p-4 flex-1 min-h-0 overflow-y-auto overscroll-contain font-mono text-[11px] text-gray-300 leading-relaxed space-y-1"
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
            
            <button 
              onClick={() => setShowInjectionModal(false)}
              className="mt-6 text-gray-500 dark:text-gray-400 hover:text-gray-800 dark:text-gray-200 uppercase tracking-widest font-bold w-full text-center transition-colors text-xs"
            >
              Cerrar
            </button>
          </div>
        </div>
        </div>
      )}

      {/* Overlay de pausa por reequilibrio */}
      {state.isRebalancing && (
        <div className="fixed inset-0 z-[90] flex items-center justify-center bg-gray-900/40 backdrop-blur-[2px] pointer-events-none">
          <div className="flex flex-col items-center justify-center text-center max-w-lg p-8 pointer-events-auto">
            <div className="w-16 h-16 border-4 border-amber-200 border-t-amber-600 rounded-full animate-spin mb-6"></div>
            <h2 className="text-xl font-black text-white tracking-widest uppercase mb-3">Operaciones pausadas</h2>
            <p className="text-amber-100 font-bold text-sm bg-amber-900/60 border border-amber-700/50 px-6 py-3 rounded-lg">{state.rebalanceMessage}</p>
            <p className="text-gray-400 text-xs mt-4">Demo: 1 min · Producción: ~30+ min moviendo capital entre exchanges</p>
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

      {state.isRebalancing && (
        <ReplenishingBanner expiresAt={state.rebalanceExpiresAt} message="Traslado de capital en curso" />
      )}

      {state.creditActiveState?.active && (
        <CreditActiveBanner expiresAt={state.creditActiveState.expiresAt} depleted={state.creditActiveState.depleted} />
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

      {state.loanResults && (
        <div className={`fixed bottom-32 right-8 z-50 transition-all duration-500 transform translate-y-0 opacity-100`}>
          <div className="bg-emerald-50 dark:bg-emerald-950/40 border border-emerald-200 dark:border-emerald-500/20 text-emerald-900 dark:text-emerald-100 px-6 py-5 rounded-xl shadow-md flex items-start gap-4 max-w-md backdrop-blur-md">
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

      {/* Header Institucional Unificado */}
      <header className="relative z-50 px-4 sm:px-6 lg:px-8 py-4 border-b border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 flex flex-col sm:flex-row justify-between items-center sticky top-0 shadow-sm gap-4 sm:gap-0">
        <div className="flex items-center gap-4">
          <img src="/Logo-Arus.jpeg" alt="Logo Arus" className="w-8 h-8 rounded object-cover shadow-sm" />
          <h1 className="text-xl font-black text-gray-900 dark:text-gray-100 tracking-widest uppercase">ARUS</h1>
          <div className="ml-4 sm:ml-6 flex items-center gap-4 sm:gap-6 text-[10px] font-bold tracking-widest text-gray-500 dark:text-gray-400 border-l border-gray-200 dark:border-gray-800 pl-4 sm:pl-6 h-6">
            <span className="hidden sm:inline">UP {formatUptime(state.uptimeSeconds)}</span>
            <span className="text-gray-700 dark:text-gray-300 flex items-center gap-2"><span className="w-2 h-2 rounded-full bg-emerald-500 shadow-[0_0_8px_rgba(16,185,129,0.5)]"></span> EN LÍNEA</span>
          </div>
        </div>

        <div className="flex flex-wrap sm:flex-nowrap items-center justify-center gap-3">
          {/* Botón de regla de negocio: préstamo automático al quedarse sin fondos */}
          <button
            onClick={toggleAutoCredit}
            role="switch"
            aria-checked={state.autoCreditMode}
            title={state.autoCreditMode
              ? "Préstamo automático ACTIVADO: evita pausas de >30 min pidiendo crédito si la ganancia supera el costo."
              : "Préstamo automático DESACTIVADO: al quedarse sin fondos, reequilibrar tarda >30 min al mover capital entre exchanges."}
            className={`px-3 py-2 rounded-md border font-bold text-[10px] sm:text-xs uppercase tracking-widest flex items-center gap-2 shadow-sm transition-all duration-300 hover:-translate-y-0.5 active:scale-95 ${state.autoCreditMode ? 'bg-emerald-50 dark:bg-emerald-500/10 text-emerald-700 dark:text-emerald-400 border-emerald-200 dark:border-emerald-500/30 hover:bg-emerald-100 dark:hover:bg-emerald-500/20' : 'bg-white dark:bg-gray-900 text-gray-500 dark:text-gray-400 border-gray-200 dark:border-gray-800 hover:bg-gray-50 dark:hover:bg-gray-800'}`}
          >
            <div className={`w-6 h-3.5 sm:w-8 sm:h-4 rounded-full p-0.5 transition-colors duration-300 flex items-center ${state.autoCreditMode ? 'bg-emerald-500' : 'bg-gray-300 dark:bg-gray-600'}`}>
              <div className={`w-2.5 h-2.5 sm:w-3 sm:h-3 rounded-full bg-white transform transition-transform duration-300 ${state.autoCreditMode ? 'translate-x-2.5 sm:translate-x-4 shadow-sm' : 'translate-x-0'}`}></div>
            </div>
            <span className="hidden sm:inline">Préstamo Automático</span>
            <span className="sm:hidden">Préstamo</span>
          </button>

          <button 
            onClick={() => setShowInjectionModal(true)}
            className="px-4 py-2 bg-red-600 text-white rounded-md font-black text-[10px] sm:text-xs uppercase tracking-widest flex items-center gap-2 shadow-sm hover:bg-red-500 transition-all duration-300 hover:-translate-y-0.5 hover:shadow-lg hover:shadow-red-500/30 active:scale-95 border border-transparent"
          >
            <ShieldAlert className="w-4 h-4" />
            <span className="hidden sm:inline">Probar el bot</span>
            <span className="sm:hidden">Probar</span>
          </button>

          <button
            onClick={() => setShowTutorial(true)}
            className="px-3 py-2 bg-gray-100 dark:bg-gray-800 text-gray-600 dark:text-gray-300 border border-gray-200 dark:border-gray-700 rounded-md hover:bg-gray-200 dark:hover:bg-gray-700 transition-all duration-300 hover:-translate-y-0.5 active:scale-95 font-bold text-[10px] sm:text-xs uppercase tracking-widest flex items-center gap-1.5 shadow-sm"
            title="Ver el tutorial de uso"
          >
            <BookOpen className="w-4 h-4" />
            <span className="hidden sm:inline">Tutorial</span>
          </button>

          <button
            onClick={() => setIsDarkMode(!isDarkMode)}
            className="p-2 bg-gray-100 dark:bg-gray-800 text-gray-600 dark:text-gray-300 rounded-md hover:bg-gray-200 dark:hover:bg-gray-700 transition-all duration-300 hover:scale-110 active:scale-90 shadow-sm border border-gray-200 dark:border-gray-700"
            aria-label="Alternar modo oscuro"
          >
            {isDarkMode ? <Sun className="w-4 h-4 sm:w-5 sm:h-5" /> : <Moon className="w-4 h-4 sm:w-5 sm:h-5" />}
          </button>

          <button
            onClick={resetSession}
            className="px-4 py-2 bg-gray-100 dark:bg-gray-800 text-red-600 dark:text-red-400 border border-red-200 dark:border-red-900/50 rounded-md hover:bg-red-50 dark:hover:bg-red-950/30 transition-all duration-300 hover:-translate-y-0.5 hover:shadow-md active:scale-95 font-bold text-[10px] sm:text-xs uppercase tracking-widest shadow-sm flex items-center gap-1.5"
            title="Borrar todo y volver al inicio"
          >
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className="w-3.5 h-3.5 sm:w-4 sm:h-4"><path strokeLinecap="round" strokeLinejoin="round" d="M10 14l2-2m0 0l2-2m-2 2l-2-2m2 2l2 2m7-2a9 9 0 11-18 0 9 9 0 0118 0z"></path></svg>
            RESET
          </button>
        </div>
      </header>

      {/* Main Container Fluido y Centrado */}
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
            
            {/* Binance Wallet */}
            <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-800 rounded-xl p-5 relative overflow-hidden shadow-sm hover:shadow-lg transition-all duration-500 animate-fade-in-up group" style={{ animationDelay: '0.4s' }}>
              <div className="absolute inset-0 bg-yellow-400/0 group-hover:bg-yellow-400/5 transition-colors duration-500 pointer-events-none"></div>
              <div className="flex justify-between items-center mb-6">
                <div className="flex items-center gap-3">
                  <div className="w-6 h-6 bg-yellow-400 text-yellow-900 font-black flex items-center justify-center rounded-[4px] text-xs">
                    B
                  </div>
                  <h3 className="text-gray-900 dark:text-gray-100 font-bold tracking-widest">BINANCE</h3>
                </div>
                <div className="flex items-center gap-2">
                  <button
                    onClick={() => setFundsModal("Binance")}
                    className="flex items-center gap-1 text-[10px] font-bold uppercase tracking-widest text-blue-600 dark:text-blue-400 border border-blue-200 dark:border-blue-500/30 bg-blue-50 dark:bg-blue-500/10 hover:bg-blue-100 dark:hover:bg-blue-500/20 rounded-full px-2.5 py-1 transition-colors"
                    title="Agregar o retirar dinero de Binance"
                  >
                    <Pencil className="w-3 h-3" /> Editar fondos
                  </button>
                  <span className="text-[10px] border border-gray-200 dark:border-gray-800 bg-gray-50 dark:bg-gray-950 rounded-full px-3 py-1 text-gray-500 dark:text-gray-400 tracking-wider font-bold">GLOBAL</span>
                </div>
              </div>
              
              <div className="grid grid-cols-2 gap-4 mb-6">
                <div>
                  <p className="text-gray-500 dark:text-gray-400 text-[10px] font-bold tracking-widest mb-1">SALDO EN USD</p>
                  <p className="font-black text-gray-900 dark:text-gray-100 text-lg">
                    ${wallets.binance.usd.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}
                  </p>
                  {state.borrowed.binance.usd > 0 && (
                    <p className="text-[10px] text-blue-500 font-bold mt-1">
                      incl. ${state.borrowed.binance.usd.toLocaleString('en-US', { maximumFractionDigits: 0 })} prestado
                    </p>
                  )}
                  {state.borrowed.binance.usd > 0 && (
                    <p className="text-[10px] text-gray-400 mt-0.5">
                      Propio: ${ownedBinanceUsd.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}
                    </p>
                  )}
                </div>
                <div>
                  <p className="text-gray-500 dark:text-gray-400 text-[10px] font-bold tracking-widest mb-1">SALDO EN BITCOIN</p>
                  <p className="font-black text-yellow-600 text-lg">
                    {wallets.binance.btc.toFixed(4)} ₿
                  </p>
                  {state.borrowed.binance.btc > 0 && (
                    <p className="text-[10px] text-blue-500 font-bold mt-1">
                      incl. {state.borrowed.binance.btc.toFixed(4)} ₿ prestado
                    </p>
                  )}
                </div>
              </div>
              
              <div>
                <div className="flex justify-between text-[10px] font-bold tracking-widest text-gray-500 dark:text-gray-400 mb-2">
                  <span>NIVEL DE FONDOS (PROPIOS)</span>
                  <span className={calculateHealth(ownedBinanceUsd) < 20 ? 'text-red-500' : 'text-emerald-600'}>{Math.round(calculateHealth(ownedBinanceUsd))}%</span>
                </div>
                <div className="w-full bg-gray-100 h-1.5 rounded-full overflow-hidden">
                  <div 
                    className={`h-full rounded-full transition-all duration-500 ${calculateHealth(ownedBinanceUsd) < 20 ? 'bg-red-500' : 'bg-emerald-500'}`}
                    style={{ width: `${calculateHealth(ownedBinanceUsd)}%` }}
                  />
                </div>
              </div>
            </div>

            {/* Bitso Wallet */}
            <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-800 rounded-xl p-5 relative overflow-hidden shadow-sm hover:shadow-lg transition-all duration-500 animate-fade-in-up group" style={{ animationDelay: '0.5s' }}>
              <div className="absolute inset-0 bg-blue-600/0 group-hover:bg-blue-600/5 transition-colors duration-500 pointer-events-none"></div>
              <div className="flex justify-between items-center mb-6">
                <div className="flex items-center gap-3">
                  <div className="w-6 h-6 bg-blue-600 text-white font-black flex items-center justify-center rounded-[4px] text-xs">
                    S
                  </div>
                  <h3 className="text-gray-900 dark:text-gray-100 font-bold tracking-widest">BITSO</h3>
                </div>
                <div className="flex items-center gap-2">
                  <button
                    onClick={() => setFundsModal("Bitso")}
                    className="flex items-center gap-1 text-[10px] font-bold uppercase tracking-widest text-blue-600 dark:text-blue-400 border border-blue-200 dark:border-blue-500/30 bg-blue-50 dark:bg-blue-500/10 hover:bg-blue-100 dark:hover:bg-blue-500/20 rounded-full px-2.5 py-1 transition-colors"
                    title="Agregar o retirar dinero de Bitso"
                  >
                    <Pencil className="w-3 h-3" /> Editar fondos
                  </button>
                  <span className="text-[10px] border border-gray-200 dark:border-gray-800 bg-gray-50 dark:bg-gray-950 rounded-full px-3 py-1 text-gray-500 dark:text-gray-400 tracking-wider font-bold">MX</span>
                </div>
              </div>
              
              <div className="grid grid-cols-2 gap-4 mb-6">
                <div>
                  <p className="text-gray-500 dark:text-gray-400 text-[10px] font-bold tracking-widest mb-1">SALDO EN USD</p>
                  <p className="font-black text-gray-900 dark:text-gray-100 text-lg">
                    ${wallets.bitso.usd.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}
                  </p>
                  {state.borrowed.bitso.usd > 0 && (
                    <p className="text-[10px] text-blue-500 font-bold mt-1">
                      incl. ${state.borrowed.bitso.usd.toLocaleString('en-US', { maximumFractionDigits: 0 })} prestado
                    </p>
                  )}
                  {state.borrowed.bitso.usd > 0 && (
                    <p className="text-[10px] text-gray-400 mt-0.5">
                      Propio: ${ownedBitsoUsd.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}
                    </p>
                  )}
                </div>
                <div>
                  <p className="text-gray-500 dark:text-gray-400 text-[10px] font-bold tracking-widest mb-1">SALDO EN BITCOIN</p>
                  <p className="font-black text-blue-600 text-lg">
                    {wallets.bitso.btc.toFixed(4)} ₿
                  </p>
                  {state.borrowed.bitso.btc > 0 && (
                    <p className="text-[10px] text-blue-500 font-bold mt-1">
                      incl. {state.borrowed.bitso.btc.toFixed(4)} ₿ prestado
                    </p>
                  )}
                </div>
              </div>
              
              <div>
                <div className="flex justify-between text-[10px] font-bold tracking-widest text-gray-500 dark:text-gray-400 mb-2">
                  <span>NIVEL DE FONDOS (PROPIOS)</span>
                  <span className={calculateHealth(ownedBitsoUsd) < 20 ? 'text-red-500' : 'text-emerald-600'}>{Math.round(calculateHealth(ownedBitsoUsd))}%</span>
                </div>
                <div className="w-full bg-gray-100 h-1.5 rounded-full overflow-hidden">
                  <div 
                    className={`h-full rounded-full transition-all duration-500 ${calculateHealth(ownedBitsoUsd) < 20 ? 'bg-red-500' : 'bg-emerald-500'}`}
                    style={{ width: `${calculateHealth(ownedBitsoUsd)}%` }}
                  />
                </div>
              </div>
            </div>

            {/* Distribucion del capital */}
            {(() => {
              const pBinanceUSD = totalWealth > 0 ? (Math.max(0, wallets.binance.usd) / totalWealth) * 100 : 25;
              const pBitsoUSD = totalWealth > 0 ? (Math.max(0, wallets.bitso.usd) / totalWealth) * 100 : 25;
              const bncBtcVal = Math.max(0, wallets.binance.btc) * (state.livePrices.binance || 60000);
              // const bsoBtcVal = wallets.bitso.btc * (state.livePrices.bitso || 60000);
              const pBinanceBTC = totalWealth > 0 ? (bncBtcVal / totalWealth) * 100 : 25;
              
              const cp1 = pBinanceUSD;
              const cp2 = cp1 + pBitsoUSD;
              const cp3 = cp2 + pBinanceBTC;
              
              const dynamicConicGradient = `conic-gradient(#EAB308 0% ${cp1}%, #2563EB ${cp1}% ${cp2}%, #F97316 ${cp2}% ${cp3}%, #06B6D4 ${cp3}% 100%)`;

              return (
                <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-800 rounded-xl p-5 mt-2 shadow-sm animate-fade-in-up hover:shadow-lg transition-all duration-500 group" style={{ animationDelay: '0.6s' }}>
                  <p className="text-gray-500 dark:text-gray-400 text-[10px] font-bold tracking-widest mb-6">DÓNDE ESTÁ TU DINERO</p>
                  <div className="flex items-center gap-6">
                    <div className="relative w-24 h-24 rounded-full flex items-center justify-center shadow-sm transition-all duration-700 group-hover:scale-105" style={{ background: dynamicConicGradient }}>
                      <div className="w-20 h-20 bg-white dark:bg-gray-900 rounded-full flex items-center justify-center flex-col z-10 shadow-inner">
                        <p className="text-[10px] text-gray-400 font-bold">TOT</p>
                        <p className="text-gray-900 dark:text-gray-100 font-black text-xl">4</p>
                      </div>
                    </div>
                    <div className="flex-1 space-y-3">
                      <div className="flex justify-between items-center text-xs">
                        <div className="flex items-center gap-2"><div className="w-2 h-2 rounded-full bg-yellow-500"></div><span className="text-gray-600 dark:text-gray-400 font-bold">BNC - USD</span></div>
                        <span className="text-gray-900 dark:text-gray-100 font-bold">{pBinanceUSD.toFixed(1)}%</span>
                      </div>
                      <div className="flex justify-between items-center text-xs">
                        <div className="flex items-center gap-2"><div className="w-2 h-2 rounded-full bg-blue-600"></div><span className="text-gray-600 dark:text-gray-400 font-bold">BSO - USD</span></div>
                        <span className="text-gray-900 dark:text-gray-100 font-bold">{pBitsoUSD.toFixed(1)}%</span>
                      </div>
                      <div className="flex justify-between items-center text-xs">
                        <div className="flex items-center gap-2"><div className="w-2 h-2 rounded-full bg-orange-500"></div><span className="text-gray-600 dark:text-gray-400 font-bold">BNC - BTC</span></div>
                        <span className="text-gray-900 dark:text-gray-100 font-bold">{pBinanceBTC.toFixed(1)}%</span>
                      </div>
                      <div className="flex justify-between items-center text-xs">
                        <div className="flex items-center gap-2"><div className="w-2 h-2 rounded-full bg-cyan-500"></div><span className="text-gray-600 dark:text-gray-400 font-bold">BSO - BTC</span></div>
                        <span className="text-gray-900 dark:text-gray-100 font-bold">{Math.max(0, 100 - cp3).toFixed(1)}%</span>
                      </div>
                    </div>
                  </div>
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
                  <p className="text-[10px] mt-2 max-w-xs text-center opacity-70">El bot compara el precio del bitcoin entre Binance y Bitso en tiempo real. Operará solo si la ganancia supera los costos.</p>
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
                              {trade.exchange_buy === 'Binance' ? (
                                <span className="px-2 py-0.5 rounded text-[10px] font-bold bg-[#D4A000]/10 text-[#D4A000] border border-[#D4A000]/20">BINANCE</span>
                              ) : (
                                <span className="px-2 py-0.5 rounded text-[10px] font-bold bg-[#0088FF]/10 text-[#0088FF] border border-[#0088FF]/20">BITSO</span>
                              )}
                              <ArrowRight className="w-3 h-3 text-gray-300 dark:text-gray-600 transition-transform group-hover:translate-x-1" />
                              {trade.exchange_sell === 'Binance' ? (
                                <span className="px-2 py-0.5 rounded text-[10px] font-bold bg-[#D4A000]/10 text-[#D4A000] border border-[#D4A000]/20">BINANCE</span>
                              ) : (
                                <span className="px-2 py-0.5 rounded text-[10px] font-bold bg-[#0088FF]/10 text-[#0088FF] border border-[#0088FF]/20">BITSO</span>
                              )}
                            </div>
                          </td>
                          <td className="py-3 text-right font-black text-sm whitespace-nowrap">
                            {trade.volume?.toFixed(3) || "0.005"}
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

        {/* Ledger / Auditoría Institucional — demuestra la persistencia de datos */}
        <LedgerPanel />
      </main>
    </div>
  );
}
