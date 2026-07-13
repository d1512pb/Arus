import React, { useEffect, useMemo, useState } from 'react';
import { ArrowRight, Sprout, SlidersHorizontal, Database, AlertTriangle, Check, ListChecks } from 'lucide-react';
import { ENGINE_HTTP_URL } from '../lib/config';
import { analyzeUniverseClient } from '../lib/universeCapability';

// OnboardingModal — la puerta de entrada se adapta a DOS clases de usuario:
//
//  · GUIADO (nuevo en esto, pocos recursos): un solo número — cuánto quiere
//    invertir en total. Arus deriva el resto y EXPLICA por qué: el bot compra y
//    vende al mismo tiempo, así que necesita inventario en ambos lados (mitad
//    efectivo, mitad BTC al precio de referencia) repartido por igual entre las
//    casas de cambio que el usuario marcó en el checklist.
//  · EXPERTO (sabe sus porcentajes): totales de USD y BTC + distribución por
//    exchange en porcentajes (suma 100, validada aquí Y en el backend), con
//    distribución separada para el BTC opcional. Sus números se respetan al
//    centavo o se rechazan — nunca se corrigen en silencio.
//
// El catálogo de exchanges y el precio de referencia vienen de GET /api/config
// (la fuente de verdad del motor): un 4º venue aparece aquí solo. La última
// configuración se recuerda en localStorage; la cuenta resultante (saldos,
// estrategia, historial) vive en la base local del motor (SQLite).

export type Allocation = Record<string, number>;

// AllocForm es la distribución MIENTRAS se edita: cada porcentaje puede quedar
// en '' (input vacío) sin forzar un 0 pegado. Al enviar se coacciona a número
// (pickSelected). Antes el estado era number puro y borrar dejaba un "0" fijo:
// el usuario tenía que escribir "020" y luego borrar el cero.
type AllocForm = Record<string, number | ''>;

interface OnboardingModalProps {
  onInit: (
    usd: number,
    btc: number,
    usdAlloc?: Allocation,
    btcAlloc?: Allocation,
    enabledVenues?: string[],
    enabledAssets?: string[],
    assetInventory?: Allocation,
  ) => void;
  // Rechazo del backend (INIT_REJECTED): se muestra tal cual — el experto
  // merece saber POR QUÉ no arrancó su sesión.
  initError?: string;
}

type Mode = 'guided' | 'expert';

// Presets del modo guiado: pensados para "pocos recursos" — se puede empezar
// desde $100; el grande existe para ver al bot mover volumen.
const GUIDED_PRESETS = [1_000, 10_000, 100_000];

const PREFS_KEY = 'arus_onboarding_v2';

interface StoredPrefs {
  mode: Mode;
  total?: number | '';
  usd?: number | '';
  btc?: number | '';
  usdAlloc?: AllocForm;
  btcAlloc?: AllocForm | null;
  selectedVenues?: string[];
  selectedAssets?: string[];
}

// toggleIn: agrega/quita un elemento de la selección conservando el ORDEN del
// catálogo (para que los chips y las columnas del radar no salten de sitio).
function toggleIn(selected: string[], catalog: string[], item: string): string[] {
  const next = selected.includes(item) ? selected.filter(x => x !== item) : [...selected, item];
  return catalog.filter(x => next.includes(x));
}

// equalAllocForm: reparte 100 % en partes iguales entre los venues dados.
// El último absorbe el residuo de redondeo para que la suma sea exactamente 100
// (el backend exige ±0.01). Antes defaultAlloc dejaba a Kraken en 0 mientras el
// checklist lo tenía marcado — capital "seleccionado" pero wallets vacíos.
function equalAllocForm(venues: string[]): AllocForm {
  const alloc: AllocForm = {};
  if (venues.length === 0) return alloc;
  const raw = 100 / venues.length;
  let assigned = 0;
  venues.forEach((v, i) => {
    if (i === venues.length - 1) {
      alloc[v] = Math.round((100 - assigned) * 100) / 100;
    } else {
      const p = Math.round(raw * 100) / 100;
      alloc[v] = p;
      assigned += p;
    }
  });
  return alloc;
}

function equalAlloc(venues: string[]): Allocation {
  return Object.fromEntries(
    Object.entries(equalAllocForm(venues)).map(([k, v]) => [k, Number(v) || 0]),
  ) as Allocation;
}

// renormalizeAlloc: al quitar un exchange, reescala los % restantes a 100.
// Si todos quedaban en 0, cae a partes iguales.
function renormalizeAlloc(prev: AllocForm, venues: string[]): AllocForm {
  if (venues.length === 0) return {};
  const weights = venues.map(v => {
    const n = Number(prev[v]);
    return Number.isFinite(n) && n > 0 ? n : 0;
  });
  const sum = weights.reduce((a, b) => a + b, 0);
  if (sum <= 0) return equalAllocForm(venues);
  const next: AllocForm = {};
  let assigned = 0;
  venues.forEach((v, i) => {
    if (i === venues.length - 1) {
      next[v] = Math.round((100 - assigned) * 100) / 100;
    } else {
      const p = Math.round((weights[i] / sum) * 100 * 100) / 100;
      next[v] = p;
      assigned += p;
    }
  });
  return next;
}

// sumOf/sumOk tratan '' como 0 (Number.isFinite('') es false; '' >= 0 es true):
// un input vacío no rompe la suma, solo no aporta hasta que se escriba un número.
const sumOf = (a: AllocForm) => Object.values(a).reduce<number>((s, v) => s + (typeof v === 'number' && Number.isFinite(v) ? v : 0), 0);
const sumOk = (a: AllocForm) => Math.abs(sumOf(a) - 100) < 0.01 && Object.values(a).every(v => v === '' || v >= 0);

// Monedas "cash": el medio de intercambio, no un activo a operar. Se mantienen
// SIEMPRE en el universo (sin efectivo no hay ciclos), así que en la checklist se
// muestran como base fija; solo las cripto son opt-in.
const CASH_ASSETS = new Set(['USD', 'USDT', 'USDC', 'DAI', 'BUSD']);
const isCashAsset = (a: string) => CASH_ASSETS.has(a);

export const OnboardingModal: React.FC<OnboardingModalProps> = ({ onInit, initError }) => {
  const [mode, setMode] = useState<Mode>('guided');

  // Catálogo real del motor (+ precio de referencia para derivar BTC).
  const [venues, setVenues] = useState<string[]>(['Binance', 'Bitso']);
  const [assets, setAssets] = useState<string[]>([]);
  const [btcPrice, setBtcPrice] = useState<number>(0);
  const [ethPrice, setEthPrice] = useState<number>(0);
  const [solPrice, setSolPrice] = useState<number>(0);
  // engineDown: /api/config no respondió. Sin el catálogo el formulario queda
  // incompleto (sin precio de referencia ni checklist de monedas) y el submit
  // no puede arrancar — se AVISA en vez de bloquear en silencio.
  const [engineDown, setEngineDown] = useState(false);

  // Checklist del universo: con qué exchanges y monedas quiere operar el usuario.
  // Vacío hasta que carga el catálogo; por defecto TODO seleccionado. Lo que quede
  // fuera no se renderiza en el radar y el bot no lo opera (enabled_venues/assets).
  const [selectedVenues, setSelectedVenues] = useState<string[]>(['Binance', 'Bitso']);
  const [selectedAssets, setSelectedAssets] = useState<string[]>([]);

  // Modo guiado: UN número.
  const [total, setTotal] = useState<number | ''>('');

  // Modo experto: totales + porcentajes (partes iguales entre los venues activos).
  const [usd, setUsd] = useState<number | ''>('');
  const [btc, setBtc] = useState<number | ''>('');
  const [usdAlloc, setUsdAlloc] = useState<AllocForm>(equalAllocForm(['Binance', 'Bitso']));
  // null = el BTC sigue a la distribución del cash (el caso común).
  const [btcAlloc, setBtcAlloc] = useState<AllocForm | null>(null);

  useEffect(() => {
    // Preferencias de la última vez (localStorage es del NAVEGADOR: solo la
    // comodidad del formulario; la cuenta vive en la base del motor).
    let stored: StoredPrefs | null = null;
    try {
      stored = JSON.parse(localStorage.getItem(PREFS_KEY) ?? 'null');
    } catch { /* preferencias corruptas: se ignoran */ }
    if (stored) {
      setMode(stored.mode === 'expert' ? 'expert' : 'guided');
      if (stored.total !== undefined) setTotal(stored.total);
      if (stored.usd !== undefined) setUsd(stored.usd);
      if (stored.btc !== undefined) setBtc(stored.btc);
      if (stored.usdAlloc) setUsdAlloc(stored.usdAlloc);
      if (stored.btcAlloc !== undefined) setBtcAlloc(stored.btcAlloc ?? null);
    }

    // Catálogo + referencia desde el motor; si no responde, el par clásico.
    fetch(`${ENGINE_HTTP_URL}/api/config`)
      .then(r => r.json())
      .then(cfg => {
        setEngineDown(false);
        const names: string[] = (cfg.venues ?? []).map((v: { name: string }) => v.name);
        if (names.length > 0) {
          setVenues(names);
          // Selección de exchanges: prefs guardadas (filtradas al catálogo real)
          // o TODO por defecto.
          const storedV = stored?.selectedVenues?.filter(v => names.includes(v));
          const nextSelected = storedV && storedV.length > 0 ? names.filter(v => storedV.includes(v)) : names;
          setSelectedVenues(nextSelected);
          setUsdAlloc(() => {
            // Prefs expertas: restaurar % por venue conocido. Si el checklist
            // incluye una casa con 0 % (prefs viejas: Kraken nacía en 0) o la
            // suma de lo seleccionado no es 100, se rebalancea en partes iguales
            // entre las casas activas — capital y checklist deben coincidir.
            if (stored?.usdAlloc) {
              const next: AllocForm = {};
              for (const n of names) next[n] = stored.usdAlloc![n] ?? 0;
              const selSum = nextSelected.reduce((s, v) => s + (Number(next[v]) || 0), 0);
              const hasEmptySelected = nextSelected.some(v => (Number(next[v]) || 0) <= 0);
              if (Math.abs(selSum - 100) > 0.01 || hasEmptySelected) {
                return { ...next, ...equalAllocForm(nextSelected) };
              }
              return next;
            }
            return equalAllocForm(names);
          });
          if (stored?.btcAlloc) {
            const next: AllocForm = {};
            for (const n of names) next[n] = stored.btcAlloc![n] ?? 0;
            const selSum = nextSelected.reduce((s, v) => s + (Number(next[v]) || 0), 0);
            const hasEmptySelected = nextSelected.some(v => (Number(next[v]) || 0) <= 0);
            if (Math.abs(selSum - 100) > 0.01 || hasEmptySelected) {
              setBtcAlloc({ ...next, ...equalAllocForm(nextSelected) });
            } else {
              setBtcAlloc(next);
            }
          }
        }
        const catalogAssets: string[] = Array.isArray(cfg.assets) ? cfg.assets : [];
        if (catalogAssets.length > 0) {
          setAssets(catalogAssets);
          const storedA = stored?.selectedAssets?.filter(a => catalogAssets.includes(a));
          setSelectedAssets(storedA && storedA.length > 0 ? catalogAssets.filter(a => storedA.includes(a)) : catalogAssets);
        }
        if (cfg.reference?.btc_price_usd > 0) setBtcPrice(cfg.reference.btc_price_usd);
        if (cfg.reference?.eth_price_usd > 0) setEthPrice(cfg.reference.eth_price_usd);
        if (cfg.reference?.sol_price_usd > 0) setSolPrice(cfg.reference.sol_price_usd);
      })
      .catch(() => setEngineDown(true)); // motor apagado o URL mal configurada: se avisa
  }, []);

  // ── Derivaciones del modo guiado ──────────────────────────────────────────
  const totalNum = Number(total) || 0;
  const guidedCash = totalNum / 2;
  const guidedCryptoBudget = totalNum / 2;
  // Cripto seleccionadas (BTC/ETH/SOL…): el presupuesto cripto se reparte en
  // valor USD entre ellas — así ETH@Binance y SOL@Binance ya no nacen en 0.
  const selectedCryptos = useMemo(
    () => selectedAssets.filter(a => !isCashAsset(a)),
    [selectedAssets],
  );
  const priceOf = (a: string) => {
    if (a === 'BTC') return btcPrice;
    if (a === 'ETH') return ethPrice > 0 ? ethPrice : 3000;
    if (a === 'SOL') return solPrice > 0 ? solPrice : 150;
    return 0;
  };
  const splitCryptoBudget = (budgetUsd: number): { btcQty: number; inventory: Allocation; ready: boolean } => {
    const cryptos = selectedCryptos.length > 0 ? selectedCryptos : ['BTC'];
    const priced = cryptos.filter(a => priceOf(a) > 0);
    if (priced.length === 0 || budgetUsd <= 0) return { btcQty: 0, inventory: {}, ready: false };
    const share = budgetUsd / priced.length;
    const inventory: Allocation = {};
    let btcQty = 0;
    for (const a of priced) {
      const qty = share / priceOf(a);
      if (a === 'BTC') btcQty = qty;
      else inventory[a] = qty;
    }
    return { btcQty, inventory, ready: true };
  };
  const guidedSplit = splitCryptoBudget(guidedCryptoBudget);
  const guidedOk = totalNum >= 100 && guidedSplit.ready && (guidedSplit.btcQty > 0 || Object.keys(guidedSplit.inventory).length > 0);

  // Universo operable: al menos un ciclo espacial o triangular (no basta 1 casa + BTC).
  const arbCap = useMemo(
    () => analyzeUniverseClient(selectedVenues, selectedAssets, venues),
    [selectedVenues, selectedAssets, venues],
  );
  const universeOk =
    selectedVenues.length >= 1 &&
    (assets.length === 0 || selectedAssets.some(a => !isCashAsset(a))) &&
    (assets.length === 0 || arbCap.ok);

  // ── Validación del modo experto ───────────────────────────────────────────
  const usdNum = Number(usd) || 0;
  const btcNum = Number(btc) || 0;
  const effectiveBtcAlloc = btcAlloc ?? usdAlloc;
  // El reparto se acota a los exchanges SELECCIONADOS: el capital solo va a los
  // venues activos, y su suma (100 %) se valida sobre ese subconjunto. Coacciona
  // '' → 0 (Number('') === 0) para que el input a medio escribir no rompa el tipo.
  const pickSelected = (a: AllocForm): Allocation =>
    Object.fromEntries(selectedVenues.map(v => [v, Number(a[v]) || 0])) as Allocation;
  const usdAllocSel = pickSelected(usdAlloc);
  const btcAllocSel = pickSelected(effectiveBtcAlloc);
  const usdSumOk = sumOk(usdAllocSel);
  const btcSumOk = sumOk(btcAllocSel);
  // Toda casa marcada en el checklist debe llevar capital (>0 %): si no, aparece
  // en el radar con wallets a 0 — el síntoma que el usuario reportó.
  const usdAllocOk = usdSumOk && selectedVenues.every(v => (usdAllocSel[v] ?? 0) > 0);
  const btcAllocOk = btcSumOk && selectedVenues.every(v => (btcAllocSel[v] ?? 0) > 0);
  const expertSplit = splitCryptoBudget(btcNum * (btcPrice > 0 ? btcPrice : 0));
  // Experto: el BTC tipeado es el presupuesto cripto en valor; se reparte entre
  // las monedas marcadas. Si solo hay BTC seleccionada, conserva el monto exacto.
  const expertBtcQty =
    selectedCryptos.length <= 1 && selectedCryptos[0] === 'BTC'
      ? btcNum
      : expertSplit.btcQty;
  const expertInventory =
    selectedCryptos.length <= 1 && selectedCryptos[0] === 'BTC'
      ? undefined
      : expertSplit.inventory;
  const expertCryptoOk =
    selectedCryptos.length <= 1 && selectedCryptos[0] === 'BTC'
      ? btcNum > 0
      : expertSplit.ready && (expertSplit.btcQty > 0 || Object.keys(expertSplit.inventory).length > 0);
  const expertOk = usdNum > 0 && expertCryptoOk && usdAllocOk && btcAllocOk && universeOk;

  const canSubmit = (mode === 'guided' ? guidedOk : expertOk) && universeOk;

  const persistPrefs = () => {
    try {
      const prefs: StoredPrefs = { mode, total, usd, btc, usdAlloc, btcAlloc, selectedVenues, selectedAssets };
      localStorage.setItem(PREFS_KEY, JSON.stringify(prefs));
    } catch { /* almacenamiento lleno/bloqueado: no es crítico */ }
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!canSubmit) return;
    persistPrefs();
    // Universo para el motor: se envía la lista solo cuando es un SUBCONJUNTO del
    // catálogo (undefined = todo, para no podar de más). Vacío nunca llega aquí:
    // universeOk exige ≥1 de cada.
    const univVenues = selectedVenues.length < venues.length ? selectedVenues : undefined;
    const univAssets = assets.length > 0 && selectedAssets.length < assets.length ? selectedAssets : undefined;
    if (mode === 'guided') {
      // Guiado: 50 % cash / 50 % cripto; el tramo cripto se parte en valor entre
      // BTC/ETH/SOL marcados y el cash+cripto se reparte entre las casas elegidas.
      const even = equalAlloc(selectedVenues);
      const inv = Object.keys(guidedSplit.inventory).length > 0 ? guidedSplit.inventory : undefined;
      onInit(guidedCash, guidedSplit.btcQty, even, even, univVenues, univAssets, inv);
    } else {
      const inv = expertInventory && Object.keys(expertInventory).length > 0 ? expertInventory : undefined;
      onInit(usdNum, expertBtcQty, usdAllocSel, btcAlloc ? btcAllocSel : undefined, univVenues, univAssets, inv);
    }
  };

  const editAlloc = (set: React.Dispatch<React.SetStateAction<AllocForm>>) =>
    (venue: string, value: string) =>
      // Vacío → '' (el input queda en blanco, no en 0); si no, parseFloat descarta
      // ceros a la izquierda ("020" → 20) y cae a 0 ante texto no numérico.
      set(prev => ({ ...prev, [venue]: value === '' ? '' : parseFloat(value) || 0 }));

  const inputCls = 'w-full bg-gray-50 dark:bg-gray-950 border border-gray-300 dark:border-gray-700 text-gray-900 dark:text-gray-100 p-3 rounded-lg outline-none focus:border-blue-500 focus:ring-1 focus:ring-blue-500 transition-colors font-mono font-bold shadow-sm';
  const labelCls = 'text-[10px] font-bold tracking-widest text-gray-500 dark:text-gray-400 uppercase';

  // allocEditor solo muestra los exchanges SELECCIONADOS en la checklist: el
  // capital se reparte entre los venues activos y su suma se valida sobre ellos.
  const allocEditor = (alloc: AllocForm, onEdit: (venue: string, value: string) => void, ok: boolean) => {
    const shown = pickSelected(alloc);
    return (
      <div>
        <div className="grid grid-cols-2 sm:grid-cols-3 gap-3 mt-2">
          {selectedVenues.map(v => (
            <div key={v}>
              <label className={labelCls}>{v} (%)</label>
              {/* step="any": la validación nativa ancla step a min y bloquearía
                  porcentajes legítimos (33.333); la suma exacta la valida sumOk.
                  value={alloc[v] ?? ''}: al borrar queda VACÍO (no un 0 pegado),
                  así el usuario escribe el número directo sin ceros a la izquierda. */}
              <input
                type="number" min="0" max="100" step="any"
                value={alloc[v] ?? ''}
                onChange={e => onEdit(v, e.target.value)}
                className={`${inputCls} mt-1 p-2.5 text-sm`}
              />
            </div>
          ))}
        </div>
        <p className={`text-[10px] mt-1.5 font-bold ${ok ? 'text-emerald-600' : 'text-red-500'}`}>
          {ok
            ? `Suma: ${sumOf(shown).toFixed(2)} % ✓`
            : Math.abs(sumOf(shown) - 100) >= 0.01
              ? `Suma: ${sumOf(shown).toFixed(2)} % — debe sumar exactamente 100`
              : `Suma: ${sumOf(shown).toFixed(2)} % — cada casa seleccionada necesita > 0 %`}
        </p>
      </div>
    );
  };

  return (
    // Contenedor de scroll (overflow-y-auto) SEPARADO del centrado: con
    // `flex items-center` directamente sobre el contenedor con scroll, un modal
    // más alto que el viewport se recorta por ARRIBA (el centrado empuja el tope
    // fuera del área desplazable). El wrapper `min-h-full` centra cuando cabe y
    // permite desplazar desde el borde superior cuando no cabe.
    <div className="fixed inset-0 z-[200] overflow-y-auto bg-gray-900/80 backdrop-blur-md font-mono">
      <div className="flex min-h-full items-center justify-center p-4">
        <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-800 rounded-xl max-w-xl w-full p-6 sm:p-8 shadow-2xl transform transition-all relative my-8">

        <div className="flex items-center gap-4 mb-5 border-b border-gray-100 dark:border-gray-800 pb-5">
          <div className="w-12 h-12 rounded-lg flex items-center justify-center shadow-lg overflow-hidden bg-white flex-shrink-0">
            <img src="/Logo-Arus.jpeg" alt="Logo Arus" className="w-full h-full object-cover" />
          </div>
          <div>
            <h2 className="text-xl font-black text-gray-900 dark:text-gray-100 tracking-widest uppercase">ARUS</h2>
            <p className="text-gray-500 dark:text-gray-400 text-xs font-bold tracking-widest mt-1">CONFIGURACIÓN INICIAL</p>
          </div>
        </div>

        {/* Motor inalcanzable: sin /api/config no hay precio de referencia ni
            checklist completa — el porqué del botón bloqueado debe ser VISIBLE. */}
        {engineDown && (
          <div className="flex items-start gap-2 bg-amber-50 dark:bg-amber-500/10 border border-amber-300 dark:border-amber-500/30 rounded-lg p-3 mb-5">
            <AlertTriangle className="w-4 h-4 text-amber-500 flex-shrink-0 mt-0.5" />
            <p className="text-[11px] text-amber-700 dark:text-amber-400 leading-relaxed font-bold">
              No se pudo contactar al motor de Arus. Verifica que esté corriendo (por defecto en :8080) y recarga la página — sin su catálogo no se puede configurar la sesión.
            </p>
          </div>
        )}

        {/* Selector de modo: la misma pantalla pregunta distinto según quién eres */}
        <div className="grid grid-cols-2 gap-3 mb-5">
          <button
            type="button"
            onClick={() => setMode('guided')}
            className={`p-3 rounded-lg border text-left transition-colors ${mode === 'guided' ? 'border-blue-500 bg-blue-50 dark:bg-blue-500/10' : 'border-gray-200 dark:border-gray-800 hover:border-gray-300 dark:hover:border-gray-700'}`}
          >
            <Sprout className={`w-4 h-4 mb-1 ${mode === 'guided' ? 'text-blue-600' : 'text-gray-400'}`} />
            <p className="text-xs font-black text-gray-900 dark:text-gray-100 uppercase tracking-widest">Guiado</p>
            <p className="text-[10px] text-gray-500 dark:text-gray-400 mt-0.5 leading-relaxed">Soy nuevo: díganme un solo número y expliquen el resto.</p>
          </button>
          <button
            type="button"
            onClick={() => setMode('expert')}
            className={`p-3 rounded-lg border text-left transition-colors ${mode === 'expert' ? 'border-violet-500 bg-violet-50 dark:bg-violet-500/10' : 'border-gray-200 dark:border-gray-800 hover:border-gray-300 dark:hover:border-gray-700'}`}
          >
            <SlidersHorizontal className={`w-4 h-4 mb-1 ${mode === 'expert' ? 'text-violet-600' : 'text-gray-400'}`} />
            <p className="text-xs font-black text-gray-900 dark:text-gray-100 uppercase tracking-widest">Experto</p>
            <p className="text-[10px] text-gray-500 dark:text-gray-400 mt-0.5 leading-relaxed">Sé mis porcentajes exactos por exchange.</p>
          </button>
        </div>

        <form onSubmit={handleSubmit} className="flex flex-col gap-5">
          {mode === 'guided' ? (
            <>
              <div>
                <label className={`${labelCls} flex justify-between`}>
                  <span>¿Cuánto quieres invertir en total?</span>
                  <span className="text-blue-500">(desde $100)</span>
                </label>
                <div className="relative mt-2">
                  <span className="absolute left-4 top-1/2 -translate-y-1/2 text-gray-400 font-bold">$</span>
                  {/* step="any": el step nativo se ancla a min y marcaría inválidos
                      montos legítimos ($10 050), bloqueando el submit del form. */}
                  <input
                    type="number"
                    value={total}
                    onChange={e => setTotal(e.target.value === '' ? '' : parseFloat(e.target.value) || 0)}
                    className={`${inputCls} p-4 pl-8`}
                    placeholder="Ej. 10000"
                    min="100"
                    step="any"
                  />
                </div>
                <div className="flex gap-2 mt-2">
                  {GUIDED_PRESETS.map(p => (
                    <button
                      key={p} type="button"
                      onClick={() => setTotal(p)}
                      className="px-3 py-1.5 rounded-full text-[11px] font-bold border border-blue-200 dark:border-blue-500/30 text-blue-600 bg-blue-50 dark:bg-blue-500/10 hover:bg-blue-600 hover:text-white transition-colors"
                    >
                      ${p.toLocaleString('en-US')}
                    </button>
                  ))}
                </div>
              </div>

              <div className="bg-gray-50 dark:bg-gray-950 border border-gray-200 dark:border-gray-800 rounded-lg p-4">
                <p className="text-[11px] font-bold text-gray-700 dark:text-gray-300 uppercase tracking-widest mb-2">Así se prepara tu dinero</p>
                <p className="text-[11px] text-gray-500 dark:text-gray-400 leading-relaxed">
                  Mitad en efectivo y mitad en cripto (repartida en valor entre las monedas que marcaste). Luego todo se distribuye entre tus casas seleccionadas.
                </p>
                <div className="grid grid-cols-2 gap-3 mt-3 text-center">
                  <div className="bg-white dark:bg-gray-900 rounded-lg p-3 border border-gray-200 dark:border-gray-800">
                    <p className="text-sm font-black text-gray-900 dark:text-gray-100">${guidedCash.toLocaleString('en-US', { maximumFractionDigits: 2 })}</p>
                    <p className="text-[10px] text-gray-400 mt-0.5">en efectivo</p>
                  </div>
                  <div className="bg-white dark:bg-gray-900 rounded-lg p-3 border border-gray-200 dark:border-gray-800">
                    <p className="text-sm font-black text-gray-900 dark:text-gray-100">${guidedCryptoBudget.toLocaleString('en-US', { maximumFractionDigits: 2 })}</p>
                    <p className="text-[10px] text-gray-400 mt-0.5">en cripto (valor)</p>
                  </div>
                </div>
                {guidedSplit.ready && (
                  <div className="mt-3 flex flex-wrap gap-1.5 justify-center">
                    {guidedSplit.btcQty > 0 && (
                      <span className="text-[10px] font-mono font-bold bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-800 rounded-full px-2.5 py-1 text-gray-700 dark:text-gray-300">
                        {guidedSplit.btcQty.toFixed(4)} BTC
                      </span>
                    )}
                    {Object.entries(guidedSplit.inventory).map(([a, q]) => (
                      <span key={a} className="text-[10px] font-mono font-bold bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-800 rounded-full px-2.5 py-1 text-gray-700 dark:text-gray-300">
                        {q.toFixed(4)} {a}
                      </span>
                    ))}
                  </div>
                )}
                <p className="text-[10px] text-gray-400 dark:text-gray-500 mt-3 leading-relaxed">
                  Casas: <strong>{selectedVenues.length > 0 ? selectedVenues.join(', ') : '—'}</strong>.
                  ETH/SOL solo en exchanges que los cotizan (p. ej. Binance; Bitso no tiene ETH/SOL).
                  Modo <strong>Experto</strong> si quieres porcentajes a mano.
                </p>
              </div>
            </>
          ) : (
            <>
              <div className="grid grid-cols-2 gap-4">
                <div>
                  <label className={labelCls}>Capital (USD)</label>
                  <div className="relative mt-2">
                    <span className="absolute left-4 top-1/2 -translate-y-1/2 text-gray-400 font-bold">$</span>
                    <input type="number" value={usd} min="1" step="any"
                      onChange={e => setUsd(e.target.value === '' ? '' : parseFloat(e.target.value) || 0)}
                      className={`${inputCls} pl-8`} placeholder="60000" />
                  </div>
                </div>
                <div>
                  <label className={labelCls}>Presupuesto cripto (en BTC)</label>
                  <div className="relative mt-2">
                    <span className="absolute left-4 top-1/2 -translate-y-1/2 text-gray-400 font-bold">₿</span>
                    <input type="number" value={btc} min="0.0001" step="any"
                      onChange={e => setBtc(e.target.value === '' ? '' : parseFloat(e.target.value) || 0)}
                      className={`${inputCls} pl-8`} placeholder="1.0" />
                  </div>
                  {selectedCryptos.length > 1 && expertSplit.ready && (
                    <p className="text-[10px] text-violet-600 dark:text-violet-400 mt-1.5 leading-relaxed">
                      Se reparte en valor entre {selectedCryptos.join('+')}:{' '}
                      {expertBtcQty > 0 && <span className="font-mono font-bold">{expertBtcQty.toFixed(4)} BTC </span>}
                      {Object.entries(expertSplit.inventory).map(([a, q]) => (
                        <span key={a} className="font-mono font-bold">{q.toFixed(4)} {a} </span>
                      ))}
                    </p>
                  )}
                </div>
              </div>

              <div>
                <p className={labelCls}>Distribución del capital por exchange</p>
                {allocEditor(usdAlloc, editAlloc(setUsdAlloc), usdAllocOk)}
              </div>

              <div className="flex items-center gap-3">
                <button
                  type="button"
                  role="switch"
                  aria-checked={btcAlloc !== null}
                  onClick={() => setBtcAlloc(prev => (prev === null ? { ...usdAlloc } : null))}
                  className={`w-10 h-5 rounded-full transition-colors relative flex items-center px-0.5 flex-shrink-0 ${btcAlloc !== null ? 'bg-violet-600' : 'bg-gray-300 dark:bg-gray-700'}`}
                >
                  <div className={`w-4 h-4 bg-white rounded-full transition-transform transform ${btcAlloc !== null ? 'translate-x-5' : 'translate-x-0'}`} />
                </button>
                <p className="text-[11px] text-gray-500 dark:text-gray-400 leading-relaxed">
                  Distribución <strong>distinta</strong> para el BTC {btcAlloc === null && '(apagado: el bitcoin sigue los mismos porcentajes que el efectivo)'}
                </p>
              </div>
              {btcAlloc !== null && allocEditor(btcAlloc, editAlloc(setBtcAlloc as React.Dispatch<React.SetStateAction<AllocForm>>), btcAllocOk)}

              <p className="text-[10px] text-gray-400 dark:text-gray-500 leading-relaxed">
                El motor valida esta distribución tal cual: casas de cambio registradas y suma exacta de 100. Ten presente que la línea de crédito y el reequilibrio automático operan sobre Binance+Bitso (el par clásico); lo asignado a otras casas lo opera el radar.
              </p>
            </>
          )}

          {/* Checklist del universo: con qué exchanges y monedas quiere operar el
              usuario. Solo lo marcado se dibuja en el radar y lo opera el bot. */}
          <div className="border-t border-gray-100 dark:border-gray-800 pt-5">
            <div className="flex items-center gap-2 mb-3">
              <ListChecks className="w-4 h-4 text-emerald-600" />
              <p className={labelCls}>¿Con qué exchanges y monedas quieres operar?</p>
            </div>

            <p className="text-[9px] font-bold uppercase tracking-widest text-gray-400 dark:text-gray-500 mb-2">Exchanges</p>
            <div className="flex flex-wrap gap-2">
              {venues.map(v => {
                const on = selectedVenues.includes(v);
                return (
                  <button
                    key={v} type="button" aria-pressed={on}
                    onClick={() => {
                      setSelectedVenues(prev => {
                        const next = toggleIn(prev, venues, v);
                        // El capital sigue al checklist: al agregar/quitar una casa
                        // se rebalancean los % del modo experto para que ninguna
                        // quede seleccionada con 0 % (o suma rota tras un borrado).
                        if (mode === 'expert') {
                          const added = next.includes(v) && !prev.includes(v);
                          setUsdAlloc(a => (added ? equalAllocForm(next) : renormalizeAlloc(a, next)));
                          setBtcAlloc(ba => {
                            if (ba === null) return null;
                            return added ? equalAllocForm(next) : renormalizeAlloc(ba, next);
                          });
                        }
                        return next;
                      });
                    }}
                    className={`flex items-center gap-1.5 px-3 py-1.5 rounded-full text-[11px] font-bold border transition-colors ${
                      on
                        ? 'border-emerald-400 bg-emerald-50 dark:bg-emerald-500/15 text-emerald-700 dark:text-emerald-300'
                        : 'border-gray-200 dark:border-gray-700 text-gray-400 dark:text-gray-500 hover:border-gray-300'
                    }`}
                  >
                    {on ? <Check className="w-3 h-3" /> : <span className="w-3 h-3 rounded-sm border border-current opacity-50" />}
                    {v}
                  </button>
                );
              })}
            </div>

            {assets.length > 0 && (
              <>
                <p className="text-[9px] font-bold uppercase tracking-widest text-gray-400 dark:text-gray-500 mt-3 mb-2">Monedas</p>
                <div className="flex flex-wrap gap-2">
                  {assets.map(a => {
                    // Cash = base fija (siempre activa, no toggleable): sin efectivo
                    // el bot no puede formar un ciclo. Solo las cripto son opt-in.
                    if (isCashAsset(a)) {
                      return (
                        <span
                          key={a}
                          title="Moneda base (efectivo): siempre activa — el bot la necesita para operar"
                          className="flex items-center gap-1.5 px-3 py-1.5 rounded-full text-[11px] font-bold border border-gray-200 dark:border-gray-700 bg-gray-50 dark:bg-gray-800/60 text-gray-400 dark:text-gray-500"
                        >
                          {a} <span className="text-[8px] uppercase tracking-widest opacity-70">base</span>
                        </span>
                      );
                    }
                    const on = selectedAssets.includes(a);
                    return (
                      <button
                        key={a} type="button" aria-pressed={on}
                        onClick={() => setSelectedAssets(prev => toggleIn(prev, assets, a))}
                        className={`flex items-center gap-1.5 px-3 py-1.5 rounded-full text-[11px] font-bold border transition-colors ${
                          on
                            ? 'border-blue-400 bg-blue-50 dark:bg-blue-500/15 text-blue-700 dark:text-blue-300'
                            : 'border-gray-200 dark:border-gray-700 text-gray-400 dark:text-gray-500 hover:border-gray-300'
                        }`}
                      >
                        {on ? <Check className="w-3 h-3" /> : <span className="w-3 h-3 rounded-sm border border-current opacity-50" />}
                        {a}
                      </button>
                    );
                  })}
                </div>
              </>
            )}

            {!universeOk ? (
              <p className="text-[10px] text-red-500 font-bold mt-2.5">
                {assets.length > 0 && !arbCap.ok
                  ? arbCap.reason
                  : "Selecciona al menos un exchange y una moneda cripto."}
              </p>
            ) : (
              <p className="text-[10px] text-gray-400 dark:text-gray-500 mt-2.5 leading-relaxed">
                Solo se dibujan y se operan los exchanges y monedas marcados. Al iniciar, el capital cripto se reparte en valor entre BTC/ETH/SOL seleccionados (ETH/SOL solo en casas que los cotizan). Puedes cambiar el universo luego en <strong>Estrategia</strong>.
              </p>
            )}
          </div>

          {initError && (
            <div className="flex items-start gap-2 bg-red-50 dark:bg-red-500/10 border border-red-200 dark:border-red-500/20 rounded-lg p-3">
              <AlertTriangle className="w-4 h-4 text-red-500 flex-shrink-0 mt-0.5" />
              <p className="text-[11px] text-red-600 dark:text-red-400 leading-relaxed font-bold">{initError}</p>
            </div>
          )}

          <button
            type="submit"
            disabled={!canSubmit}
            className={`w-full p-4 rounded-lg font-black text-sm uppercase tracking-widest flex justify-between items-center group shadow-md transition-all ${canSubmit ? 'bg-blue-600 hover:bg-blue-700 text-white' : 'bg-gray-300 dark:bg-gray-800 text-gray-500 cursor-not-allowed'}`}
          >
            <span>Empezar</span>
            <ArrowRight className="w-5 h-5 group-hover:translate-x-1 transition-transform" />
          </button>

          <p className="flex items-start gap-2 text-[10px] text-gray-400 dark:text-gray-500 leading-relaxed">
            <Database className="w-3.5 h-3.5 flex-shrink-0 mt-0.5" />
            <span>Tu cuenta (saldos, estrategia e historial) queda guardada en la base de datos local del motor: cierra el navegador o reinicia el servidor y sigues exactamente donde estabas. Dinero 100 % simulado.</span>
          </p>
        </form>
        </div>
      </div>
    </div>
  );
};
