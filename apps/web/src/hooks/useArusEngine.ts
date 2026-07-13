import { useState, useRef, useCallback, useEffect } from "react";
import { ENGINE_WS_URL, ENGINE_HTTP_URL } from "../lib/config";

export interface Trade {
  event: string;
  exchange_buy: string;
  exchange_sell: string;
  net_profit_usd: number;
  new_total_usd: number;
  timestamp: string | number;
  volume?: number;
  credit_active?: boolean;
  // FASE 3: marca las operaciones de la ráfaga HFT. El radar les
  // dispara la luz verde (flare) pero NO el cobro flotante individual, para no
  // saturar de números la tormenta (el P&L trepa en la cabecera).
  storm?: boolean;
}

export interface LogEntry {
  type: string;
  level: string;
  timestamp: string;
  message: string;
  spread?: number;
  netProfit?: number;
}

// Parámetros de estrategia editables en vivo (espejo de TradingParameters en Go).
// El backend clampea cada campo a rangos sanos y devuelve lo APLICADO vía
// PARAMS_UPDATED — la UI siempre pinta lo aplicado, no lo solicitado.
export interface TradingParams {
  taker_fees: Record<string, number>;
  min_net_profit_usd: number;
  max_order_size_btc: number;
  slippage_rate: number;
  spike_tick_deviation: number;
  max_divergence_ratio: number;
  risk_multiplier: number;
  // Universo del usuario (hito 3): con qué exchanges y monedas juega.
  // Ausente/vacío = todos.
  enabled_venues?: string[];
  enabled_assets?: string[];
  // Autopiloto del radar: detección → ejecución del mejor ciclo del universo.
  radar_autopilot?: boolean;
  // Préstamo parametrizado: los términos del crédito los define el usuario.
  // Opcionales solo por compatibilidad con motores previos al bloque.
  credit_line_usd?: number;
  credit_line_btc?: number;
  credit_apr?: number; // fracción anual (0.10 = 10 %)
  credit_origination_fee?: number;
  credit_duration_min?: number;
  // Física del simulador: probabilidad de Fill-or-Kill fallido por orden.
  order_failure_prob?: number;
}

// --- Radar omnidireccional (Fase 2): espejo de GraphSnapshotWire en Go ---

export interface GraphNode {
  id: string;    // "BTC@Binance"
  asset: string; // "BTC" | "ETH" | "USDT" | "USD"
  venue: string;
  kind: "cash" | "crypto";
  balance: number;
  balance_usd: number;
  price_usd: number; // 1 para cash; 0 = aún sin dato de precio
  feed_stale: boolean;
}

export interface GraphEdge {
  from: string;
  to: string;
  kind: "book" | "parity" | "inventory" | "transfer";
  rate: number;
  fee_pct: number;
  liquidity: number; // en unidades de base_asset; 0 = sin dato
  base_asset?: string;
  stale: boolean;
}

export interface GraphCycle {
  path: string[]; // cerrado: primero == último
  net_return_pct: number;
  max_volume_btc: number; // 0 = piernas con bases mixtas (triangular)
  // Capacidad del ciclo en unidades de su nodo de inicio ("entrada hasta
  // 12 000 USDT"): cubre los triangulares, donde max_volume_btc no aplica.
  max_start_amount?: number;
  start_asset?: string;
  viable: boolean;
}

export interface GraphSnapshot {
  nodes: GraphNode[];
  edges: GraphEdge[];
  best_cycle?: GraphCycle;
  parity_assumed: boolean;
  updated_at: string;
}

// --- Arbitraje omnidireccional (FASE 1): ciclo triangular/espacial inyectado ---

// Una pierna del ciclo: convierte `in` unidades del nodo `from` en `out` del `to`.
export interface OmniLeg {
  from: string; // "USDT@Binance"
  to: string;   // "BTC@Binance"
  in: number;
  out: number;
  asset: string; // activo ganado (el de `to`)
  kind: "book" | "inventory" | "parity";
}

// omni_executed → el radar anima una luz verde recorriendo `path` secuencialmente.
// `id` es un contador local (cambia en cada inyección) que dispara la animación.
export interface OmniPulse {
  id: number;
  path: string[]; // IDs de nodo en orden, cerrado (primero == último)
  legs: OmniLeg[];
  route: string;
  net: number;
  /** Nodos cuyo inventario SÍ cambió (net delta ≠ 0). El radar solo los pulsa. */
  flashNodes?: string[];
}

/** Flash forense de un ciclo atómico: deltas por nodo durante ~400 ms. */
export interface InventoryFlash {
  id: number;
  /** "BTC@Binance" → delta firmado (excursión pico del recorrido). */
  deltas: Record<string, number>;
  /** venue → asset → delta (para las cards del dashboard). */
  byVenueAsset: Record<string, Record<string, number>>;
}

/** Inventario multi-venue / multi-asset (espejo de Balances en Go). */
export type VenueBalances = Record<string, Record<string, number>>;

export interface EngineState {
  sessionId: string;
  params: TradingParams | null;
  graph: GraphSnapshot | null;
  // Última inyección omnidireccional (FASE 2): el radar la anima como una luz
  // verde recorriendo el camino dinámico del ciclo ejecutado. null = ninguna aún.
  omniPulse: OmniPulse | null;
  /** Overlay HFT Flash tras un ciclo (null = inventario real). */
  inventoryFlash: InventoryFlash | null;
  // FASE 3 — ráfaga de volatilidad: stormActive mientras dura la tormenta;
  // stormResult trae el resumen (operaciones + ganancia) al terminar.
  stormActive: boolean;
  stormResult: { trades: number; profit: number } | null;
  // FASE 3 — escudo de robustez: alerta efímera del circuit breaker (spread
  // irreal, timeout o divergencia). La UI la muestra ~3 s y se limpia sola.
  circuitBreaker: { id: number; scenario: string; message: string } | null;
  // Distribución elegida en el onboarding (undefined = 50/50 clásico o sesión
  // reanudada): la usa la barra de salud de fondos para calibrar el 100 %.
  usdAllocation?: Allocation;
  // Rechazo del backend al init (INIT_REJECTED): el onboarding lo muestra.
  initError?: string;
  trades: Trade[];
  totalWealth: number;
  initialWealth: number;
  initialUsd: number;
  totalNetProfit: number;
  /** Fuente de verdad multi-asset (venue → asset → qty). */
  balances: VenueBalances;
  /** Compat dashboard legacy Binance/Bitso. */
  wallets: {
    binance: { usd: number; btc: number };
    bitso: { usd: number; btc: number };
  };
  borrowed: {
    binance: { usd: number; btc: number };
    bitso: { usd: number; btc: number };
  };
  borrowedBalances: VenueBalances;
  opsCount: number;
  uptimeSeconds: number;
  livePrices: { binance: number; bitso: number };
  ping: boolean;
  isRebalancing: boolean;
  rebalanceExpiresAt: Date | null;
  rebalanceMessage: string;
  rebalanceSuccessAmount: number | null;
  logs: LogEntry[];
  autoCreditMode: boolean;
  insufficientFundsModal: { open: boolean; profitPotential: number; creditCost: number; creditRequired: number };
  creditActiveState: { active: boolean; expiresAt: Date | null; depleted?: boolean };
  loanResults: { earnings: number; cost: number } | null;
  engineRunning: boolean;
}

function isVenueBalances(v: unknown): v is VenueBalances {
  return !!v && typeof v === "object" && !Array.isArray(v);
}

function walletsFromBalances(balances: VenueBalances) {
  return {
    binance: {
      usd: balances.Binance?.USDT ?? balances.Binance?.USD ?? 0,
      btc: balances.Binance?.BTC ?? 0,
    },
    bitso: {
      usd: balances.Bitso?.USD ?? balances.Bitso?.USDT ?? 0,
      btc: balances.Bitso?.BTC ?? 0,
    },
  };
}

function walletsFromData(data: Record<string, unknown>) {
  if (isVenueBalances(data.balances)) {
    return walletsFromBalances(data.balances);
  }
  if (data.binance_usd === undefined) return null;
  return {
    binance: { usd: data.binance_usd as number, btc: (data.binance_btc as number) ?? 0 },
    bitso: { usd: data.bitso_usd as number, btc: (data.bitso_btc as number) ?? 0 },
  };
}

function balancesFromData(data: Record<string, unknown>, prev: VenueBalances): VenueBalances {
  if (isVenueBalances(data.balances)) {
    return data.balances as VenueBalances;
  }
  const w = walletsFromData(data);
  if (!w) return prev;
  return {
    ...prev,
    Binance: { ...(prev.Binance ?? {}), USDT: w.binance.usd, BTC: w.binance.btc },
    Bitso: { ...(prev.Bitso ?? {}), USD: w.bitso.usd, BTC: w.bitso.btc },
  };
}

/** Extrae saldos de los nodos del radar (graph_update ya trae wallets de Go). */
function balancesFromGraph(graph: GraphSnapshot | null | undefined, prev: VenueBalances): VenueBalances {
  if (!graph?.nodes?.length) return prev;
  const next: VenueBalances = {};
  for (const n of graph.nodes) {
    if (!n.venue || !n.asset) continue;
    if (!next[n.venue]) next[n.venue] = {};
    next[n.venue][n.asset] = typeof n.balance === "number" ? n.balance : 0;
  }
  return Object.keys(next).length > 0 ? next : prev;
}

/** Escribe balances vivos sobre los nodos del grafo (el radar lee n.balance). */
function graphWithBalances(graph: GraphSnapshot | null, balances: VenueBalances): GraphSnapshot | null {
  if (!graph) return null;
  if (!balances || Object.keys(balances).length === 0) return graph;
  return {
    ...graph,
    nodes: graph.nodes.map(n => {
      const qty = balances[n.venue]?.[n.asset];
      if (typeof qty !== "number") return n;
      const price = n.price_usd > 0 ? n.price_usd : n.kind === "cash" ? 1 : 0;
      return { ...n, balance: qty, balance_usd: qty * price };
    }),
  };
}

function borrowedFromData(data: Record<string, unknown>) {
  return {
    binance: {
      usd: (data.borrowed_binance_usd as number) || 0,
      btc: (data.borrowed_binance_btc as number) || 0,
    },
    bitso: {
      usd: (data.borrowed_bitso_usd as number) || 0,
      btc: (data.borrowed_bitso_btc as number) || 0,
    },
  };
}

function borrowedBalancesFromData(data: Record<string, unknown>, prev: VenueBalances): VenueBalances {
  if (isVenueBalances(data.borrowed_balances)) {
    return data.borrowed_balances as VenueBalances;
  }
  if (data.credit_active === false && data.borrowed_binance_usd === undefined) {
    return {};
  }
  const flat = borrowedFromData(data);
  if (!flat.binance.usd && !flat.binance.btc && !flat.bitso.usd && !flat.bitso.btc) {
    return prev;
  }
  return {
    Binance: { USDT: flat.binance.usd, BTC: flat.binance.btc },
    Bitso: { USD: flat.bitso.usd, BTC: flat.bitso.btc },
  };
}

function flashFromOmni(data: Record<string, unknown>): InventoryFlash["byVenueAsset"] {
  const byVenueAsset: Record<string, Record<string, number>> = {};
  const arr = data.flash_deltas as Array<{ venue?: string; asset?: string; delta?: number; node?: string }> | undefined;
  if (Array.isArray(arr)) {
    for (const row of arr) {
      if (!row.venue || !row.asset || typeof row.delta !== "number") continue;
      if (!byVenueAsset[row.venue]) byVenueAsset[row.venue] = {};
      byVenueAsset[row.venue][row.asset] = (byVenueAsset[row.venue][row.asset] ?? 0) + row.delta;
    }
    return byVenueAsset;
  }
  const map = data.flash_delta_by_node as Record<string, number> | undefined;
  if (map) {
    for (const [node, delta] of Object.entries(map)) {
      const at = node.lastIndexOf("@");
      if (at <= 0) continue;
      const asset = node.slice(0, at);
      const venue = node.slice(at + 1);
      if (!byVenueAsset[venue]) byVenueAsset[venue] = {};
      byVenueAsset[venue][asset] = (byVenueAsset[venue][asset] ?? 0) + delta;
    }
  }
  return byVenueAsset;
}

// Distribución del capital por venue (porcentajes, suman 100).
export type Allocation = Record<string, number>;

// FASE 1 (refactor Probar Bot): escenario de "Crear tu propia prueba". Viaja
// por HTTP a POST /api/simulate/custom; el motor lo valida contra el universo
// del usuario y lo inyecta como MarketTicks REALES en su canal de ingesta.
export interface CustomSimPayload {
  exchangeA: string;
  assetA: string;
  bidA: number;
  askA: number;
  exchangeB: string;
  assetB: string;
  bidB: number;
  askB: number;
  liquidity?: number;
}

const HFT_FLASH_MS = 1800; // resalta celdas cuyo inventario SÍ cambió (sesgo real)

// Clave del token de sesión en localStorage: el UUID de la sesión persistida en
// el backend. Quien tiene el token, recupera SU sesión (saldos, estrategia,
// historial) tras cerrar el navegador o tras un reinicio del motor.
const SESSION_KEY = "arus_session_id";

// Estado limpio del hook: el arranque y el reset ("Borrar todo") parten del
// MISMO cero — nadie lo muta en sitio (el reducer siempre copia con spread).
const INITIAL_ENGINE_STATE: EngineState = {
  sessionId: "",
  params: null,
  graph: null,
  omniPulse: null,
  inventoryFlash: null,
  stormActive: false,
  stormResult: null,
  circuitBreaker: null,
  trades: [],
  totalWealth: 0,
  initialWealth: 0,
  initialUsd: 0,
  totalNetProfit: 0,
  balances: {},
  wallets: { binance: { usd: 0, btc: 0 }, bitso: { usd: 0, btc: 0 } },
  borrowed: { binance: { usd: 0, btc: 0 }, bitso: { usd: 0, btc: 0 } },
  borrowedBalances: {},
  opsCount: 0,
  uptimeSeconds: 0,
  livePrices: { binance: 0, bitso: 0 },
  ping: false,
  isRebalancing: false,
  rebalanceExpiresAt: null,
  rebalanceMessage: "",
  rebalanceSuccessAmount: null,
  logs: [],
  autoCreditMode: false,
  insufficientFundsModal: { open: false, profitPotential: 0, creditCost: 0, creditRequired: 0 },
  creditActiveState: { active: false, expiresAt: null, depleted: false },
  loanResults: null,
  engineRunning: true,
};

export function useArusEngine() {
  const [sessionReady, setSessionReady] = useState(false);
  // resuming: hay un token guardado y estamos recuperando la sesión del servidor
  // (la UI muestra "recuperando..." en vez del onboarding).
  const [resuming, setResuming] = useState(false);
  const sessionIdRef = useRef<string | null>(null);
  const [state, setState] = useState<EngineState>(INITIAL_ENGINE_STATE);

  const wsRef = useRef<WebSocket | null>(null);
  // Contador para la alerta del circuit breaker (FASE 3): identifica cada alerta
  // para que su auto-descarte a los 3 s no borre una alerta posterior.
  const cbCounterRef = useRef(0);
  // Generación del HFT Flash: evita que un setTimeout viejo borre un flash nuevo.
  const flashCounterRef = useRef(0);
  const configRef = useRef<{
    usd: number;
    btc: number;
    usdAlloc?: Allocation;
    btcAlloc?: Allocation;
    assetInventory?: Allocation;
  } | null>(null);
  const reconnectTimeoutRef = useRef<NodeJS.Timeout | null>(null);
  // Universo elegido en la checklist del onboarding (exchanges/monedas activos).
  // Se aplica vía set_params en cuanto la sesión existe (el primer state_update),
  // porque init_session no lleva el universo. null = sin poda (todo el catálogo).
  const pendingUniverseRef = useRef<{ enabledVenues?: string[]; enabledAssets?: string[] } | null>(null);

  // initMsg arma el payload de init_session con la distribución (si la hay) —
  // lo comparten el arranque normal y la re-inicialización tras reconexión.
  const initMsg = (cfg: {
    usd: number;
    btc: number;
    usdAlloc?: Allocation;
    btcAlloc?: Allocation;
    assetInventory?: Allocation;
  }) =>
    JSON.stringify({
      action: "init_session",
      initial_usd: cfg.usd,
      initial_btc: cfg.btc,
      usd_allocation: cfg.usdAlloc,
      btc_allocation: cfg.btcAlloc,
      asset_inventory: cfg.assetInventory,
    });

  // openSocket abre la conexión con los handlers compartidos. La reconexión
  // automática PREFIERE reanudar la sesión persistida (resume_session con el
  // token) en vez de re-inicializar — un parpadeo de red ya no borra el progreso.
  const openSocket = useCallback((onOpen: (ws: WebSocket) => void) => {
    if (wsRef.current && (wsRef.current.readyState === WebSocket.OPEN || wsRef.current.readyState === WebSocket.CONNECTING)) {
      wsRef.current.onclose = null; // el socket viejo no debe disparar reconexiones
      wsRef.current.close();
    }

    const ws = new WebSocket(ENGINE_WS_URL);
    wsRef.current = ws;

    ws.onopen = () => onOpen(ws);

    ws.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data);
        handleServerEvent(data);
      } catch (err) {
        console.error("WS Parse error:", err);
      }
    };

    ws.onclose = () => {
      setSessionReady(false);
      if (reconnectTimeoutRef.current) clearTimeout(reconnectTimeoutRef.current);
      reconnectTimeoutRef.current = setTimeout(() => {
        const token = sessionIdRef.current;
        if (token) {
          openSocket(s => s.send(JSON.stringify({ action: "resume_session", session_id: token })));
        } else if (configRef.current) {
          openSocket(s => s.send(initMsg(configRef.current!)));
        }
      }, 3000);
    };

    ws.onerror = () => ws.close();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const initSession = useCallback((
    usd: number,
    btc: number,
    usdAlloc?: Allocation,
    btcAlloc?: Allocation,
    enabledVenues?: string[],
    enabledAssets?: string[],
    assetInventory?: Allocation,
  ) => {
    configRef.current = { usd, btc, usdAlloc, btcAlloc, assetInventory };
    // La checklist del onboarding poda el universo: se guarda para aplicarlo en
    // cuanto la sesión exista (init_session solo lleva capital/distribución).
    pendingUniverseRef.current =
      (enabledVenues && enabledVenues.length > 0) || (enabledAssets && enabledAssets.length > 0)
        ? { enabledVenues, enabledAssets }
        : null;
    setState(prev => ({ ...prev, usdAllocation: usdAlloc, initError: undefined }));
    const msg = initMsg(configRef.current);
    if (wsRef.current?.readyState === WebSocket.OPEN) {
      wsRef.current.send(msg); // socket vivo (p. ej. tras RESUME_FAILED): reúsalo
      return;
    }
    openSocket(ws => ws.send(msg));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Reanudación al cargar la página: si hay token guardado, recuperamos la
  // sesión persistida en el backend en lugar de mostrar el onboarding.
  useEffect(() => {
    const token = typeof window !== "undefined" ? localStorage.getItem(SESSION_KEY) : null;
    if (!token) return;
    sessionIdRef.current = token;
    setResuming(true);
    openSocket(ws => ws.send(JSON.stringify({ action: "resume_session", session_id: token })));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // cancelResume: escape para el usuario si la recuperación tarda o falla —
  // olvida el token y vuelve al onboarding para empezar de cero.
  const cancelResume = useCallback(() => {
    if (typeof window !== "undefined") localStorage.removeItem(SESSION_KEY);
    sessionIdRef.current = null;
    setResuming(false);
  }, []);

  // resetSession — «Borrar todo y volver al inicio»: abandona la sesión actual
  // (token fuera, socket cerrado SIN reconexión — el motor la saca del Hub y
  // deja de operarla, igual que al cerrar la pestaña) y deja la UI en el
  // onboarding hasta que el usuario envíe capital nuevo; ese init abre una
  // conexión nueva → sesión con identidad fresca (saldos, historial y analítica
  // en cero). OJO: no enviar aquí la acción `reset_session` — su respuesta es un
  // `state_update` que re-activa sessionReady y expulsa al usuario del
  // onboarding antes de que pueda escribir.
  const resetSession = useCallback(() => {
    if (typeof window !== "undefined") localStorage.removeItem(SESSION_KEY);
    sessionIdRef.current = null;
    configRef.current = null;
    if (reconnectTimeoutRef.current) {
      clearTimeout(reconnectTimeoutRef.current);
      reconnectTimeoutRef.current = null;
    }
    if (wsRef.current) {
      wsRef.current.onclose = null; // el abandono es deliberado: sin reconexión
      wsRef.current.close();
      wsRef.current = null;
    }
    setState(INITIAL_ENGINE_STATE);
    setResuming(false);
    setSessionReady(false);
  }, []);

  const demoInject = useCallback((exchange: string, spread: number, liquidity: number) => {
    if (wsRef.current) {
      wsRef.current.send(JSON.stringify({ action: "demo_inject", exchange, spread, liquidity }));
    }
  }, []);

  // FASE 1 (refactor Probar Bot): "Crear tu propia prueba" → POST
  // /api/simulate/custom. El backend valida (sesión viva, universo activo,
  // libros del catálogo, coherencia) y, si todo cuadra, inyecta los ticks por
  // la MISMA tubería que los feeds reales; el veredicto llega por el WebSocket
  // (logs, arbitrage_executed, wallet_update). Devuelve el error del motor tal
  // cual para mostrarlo al usuario — nunca se corrige en silencio.
  const simulateCustom = useCallback(async (p: CustomSimPayload): Promise<{ ok: boolean; error?: string }> => {
    const sid = sessionIdRef.current;
    if (!sid) return { ok: false, error: "La sesión aún no está lista." };
    try {
      const res = await fetch(`${ENGINE_HTTP_URL}/api/simulate/custom`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          session_id: sid,
          exchange_a: p.exchangeA,
          asset_a: p.assetA,
          bid_a: p.bidA,
          ask_a: p.askA,
          exchange_b: p.exchangeB,
          asset_b: p.assetB,
          bid_b: p.bidB,
          ask_b: p.askB,
          liquidity: p.liquidity,
        }),
      });
      const data = (await res.json().catch(() => ({}))) as { error?: string };
      if (!res.ok) return { ok: false, error: data.error ?? `El motor respondió ${res.status}.` };
      return { ok: true };
    } catch {
      return { ok: false, error: "No se pudo contactar al motor." };
    }
  }, []);

  // FASE 2: "Oportunidad normal" → arbitraje omnidireccional dinámico. El backend
  // lee el universo activo, inyecta ineficiencia por la tubería real, el radar
  // descubre/ejecuta el ciclo y responde con omni_executed para animar el grafo.
  const injectOmni = useCallback(() => {
    if (wsRef.current) {
      wsRef.current.send(JSON.stringify({ action: "inject_omni" }));
    }
  }, []);

  // FASE 3: "Evento poco común" → ráfaga HFT. Micro-ticks reales + planCycle
  // concurrente; el radar pinta storm_started/trade/ended.
  const injectStorm = useCallback(() => {
    if (wsRef.current) {
      wsRef.current.send(JSON.stringify({ action: "inject_storm" }));
    }
  }, []);

  // FASE 3: "Precio falso / error" → escudo de robustez. El backend rechaza una
  // oportunidad envenenada y responde CIRCUIT_BREAKER (alerta efímera, no luces).
  const injectFake = useCallback(() => {
    if (wsRef.current) {
      wsRef.current.send(JSON.stringify({ action: "inject_fake" }));
    }
  }, []);

  const toggleAutoCredit = useCallback(() => {
    if (wsRef.current) {
      wsRef.current.send(JSON.stringify({ action: "toggle_auto_credit" }));
      setState(prev => ({ ...prev, autoCreditMode: !prev.autoCreditMode }));
    }
  }, []);

  const requestCredit = useCallback(() => {
    if (wsRef.current) {
      wsRef.current.send(JSON.stringify({ action: "request_credit" }));
    }
  }, []);

  const waitRebalance = useCallback(() => {
    if (wsRef.current) {
      wsRef.current.send(JSON.stringify({ action: "wait_rebalance" }));
    }
  }, []);

  // "Continuar sin rebalancear": ni préstamo ni reequilibrio. El backend limpia
  // el estado de falta de fondos y la inyección pendiente; aquí cerramos el modal
  // (no hay evento de servidor que lo cierre, a diferencia de crédito/reequilibrio).
  const dismissShortfall = useCallback(() => {
    if (wsRef.current) {
      wsRef.current.send(JSON.stringify({ action: "dismiss_shortfall" }));
    }
    setState(prev => ({ ...prev, insufficientFundsModal: { open: false, profitPotential: 0, creditCost: 0, creditRequired: 0 } }));
  }, []);

  // Depósito (amount > 0) o retiro (amount < 0) de cualquier activo del venue
  // (USD/USDT, BTC, ETH, SOL…). El backend valora en ≈USD y no altera el PnL.
  const adjustFunds = useCallback((exchange: string, currency: string, amount: number) => {
    if (wsRef.current && amount !== 0 && currency) {
      wsRef.current.send(JSON.stringify({ action: "adjust_funds", exchange, currency, amount }));
    }
  }, []);

  // Personalización de estrategia en vivo: envía el struct COMPLETO de parámetros.
  // El backend clampea y responde PARAMS_UPDATED con lo realmente aplicado.
  const setParams = useCallback((params: TradingParams) => {
    if (wsRef.current) {
      wsRef.current.send(JSON.stringify({ action: "set_params", params }));
    }
  }, []);

  const shutdownEngine = useCallback(() => {
    if (wsRef.current) {
      wsRef.current.send(JSON.stringify({ action: "shutdown_engine" }));
    }
  }, []);

  const handleServerEvent = useCallback((data: Record<string, unknown>) => {
    const applyWalletSync = (prev: EngineState): Partial<EngineState> => {
      const wallets = walletsFromData(data);
      const patch: Partial<EngineState> = {};
      if (wallets) patch.wallets = wallets;
      patch.balances = balancesFromData(data, prev.balances);
      patch.graph = graphWithBalances(prev.graph, patch.balances as VenueBalances);
      if (data.total_wealth !== undefined) patch.totalWealth = data.total_wealth as number;
      if (data.new_total_usd !== undefined) patch.totalWealth = data.new_total_usd as number;
      if (data.total_wealth_usd !== undefined) patch.totalWealth = data.total_wealth_usd as number;
      if (data.total_net_profit !== undefined) patch.totalNetProfit = data.total_net_profit as number;
      // Depósito/retiro mueve la base inicial junto con el total → el PnL no se distorsiona.
      if (data.initial_wealth !== undefined) patch.initialWealth = data.initial_wealth as number;
      if (data.initial_usd !== undefined) patch.initialUsd = data.initial_usd as number;
      if (data.borrowed_binance_usd !== undefined || data.borrowed_balances !== undefined || data.credit_active === false) {
        patch.borrowed = data.credit_active === false && data.borrowed_binance_usd === undefined && data.borrowed_balances === undefined
          ? { binance: { usd: 0, btc: 0 }, bitso: { usd: 0, btc: 0 } }
          : borrowedFromData(data);
        patch.borrowedBalances = borrowedBalancesFromData(data, prev.borrowedBalances);
        if (data.credit_active === false && data.borrowed_balances === undefined && data.borrowed_binance_usd === undefined) {
          patch.borrowedBalances = {};
        }
      }
      return patch;
    };

    if (data.type === "state_update") {
      // El session_id es el TOKEN de continuidad: se guarda para reanudar la
      // sesión tras cerrar el navegador o reiniciar el motor.
      const sid = data.session_id as string;
      if (sid && typeof window !== "undefined") {
        sessionIdRef.current = sid;
        localStorage.setItem(SESSION_KEY, sid);
      }
      setResuming(false);
      setSessionReady(true);
      // Aplica el universo elegido en la checklist del onboarding, ahora que la
      // sesión existe: set_params lo persiste y el radar se recalcula con él. Solo
      // en el init (no en resume: pendingUniverseRef es null al reanudar, la
      // sesión ya trae su universo persistido).
      if (pendingUniverseRef.current && data.params) {
        const u = pendingUniverseRef.current;
        pendingUniverseRef.current = null;
        const merged: TradingParams = {
          ...(data.params as TradingParams),
          enabled_venues: u.enabledVenues,
          enabled_assets: u.enabledAssets,
        };
        wsRef.current?.send(JSON.stringify({ action: "set_params", params: merged }));
      }
      setState(prev => ({
        ...prev,
        initError: undefined,
        sessionId: (data.session_id as string) || prev.sessionId,
        params: (data.params as TradingParams) ?? prev.params,
        totalWealth: (data.total_wealth as number) ?? prev.totalWealth,
        initialWealth: (data.initial_wealth as number) || prev.initialWealth,
        initialUsd: (data.initial_usd as number) || prev.initialUsd,
        totalNetProfit: (data.total_net_profit as number) || prev.totalNetProfit,
        balances: balancesFromData(data, {}),
        wallets: {
          binance: { usd: data.binance_usd as number, btc: data.binance_btc as number },
          bitso: { usd: data.bitso_usd as number, btc: data.bitso_btc as number },
        },
        borrowed: { binance: { usd: 0, btc: 0 }, bitso: { usd: 0, btc: 0 } },
        borrowedBalances: {},
        inventoryFlash: null,
        trades: [],
        opsCount: 0,
        uptimeSeconds: 0,
        logs: [],
        isRebalancing: false,
        rebalanceExpiresAt: null,
        creditActiveState: { active: false, expiresAt: null, depleted: false },
        insufficientFundsModal: { open: false, profitPotential: 0, creditCost: 0, creditRequired: 0 },
        loanResults: null,
      }));
    } else if (data.type === "INIT_REJECTED") {
      // El backend rechazó el capital o la distribución: el onboarding muestra
      // el motivo tal cual (a un experto no se le deja esperando en silencio).
      setState(prev => ({ ...prev, initError: (data.message as string) || "El motor rechazó la configuración inicial." }));
    } else if (data.type === "RESUME_FAILED") {
      // El token ya no corresponde a una sesión válida: se olvida y el usuario
      // pasa por el onboarding normal (el socket queda abierto para el init).
      if (typeof window !== "undefined") localStorage.removeItem(SESSION_KEY);
      sessionIdRef.current = null;
      setResuming(false);
    } else if (data.type === "PARAMS_UPDATED") {
      setState(prev => ({ ...prev, params: (data.params as TradingParams) ?? prev.params }));
    } else if (data.type === "graph_update") {
      // El snapshot ya trae saldos de sesión: sincronizar balances para que el
      // overlay del radar no pise nodos frescos con wallets viejos.
      setState(prev => {
        const graph = (data.graph as GraphSnapshot) ?? prev.graph;
        const balances = balancesFromGraph(graph, prev.balances);
        return {
          ...prev,
          graph,
          balances,
          wallets: walletsFromBalances(balances),
        };
      });
    } else if (data.type === "log") {
      setState(prev => {
        const next = [...prev.logs, data as unknown as LogEntry];
        return { ...prev, logs: next.slice(-200) };
      });
    } else if (data.type === "wallet_update") {
      setState(prev => ({ ...prev, ...applyWalletSync(prev) }));
    } else if (data.type === "INSUFFICIENT_FUNDS") {
      setState(prev => ({
        ...prev,
        insufficientFundsModal: {
          open: true,
          profitPotential: (data.profit_potential as number) || 0,
          creditCost: (data.credit_cost as number) || 0,
          // Umbral real de la decisión: costo × multiplicador de riesgo del usuario.
          creditRequired: (data.credit_required as number) || (data.credit_cost as number) || 0,
        },
      }));
    } else if (data.type === "CREDIT_APPROVED" || data.type === "CREDIT_AUTO_APPROVED") {
      setState(prev => ({
        ...prev,
        ...applyWalletSync(prev),
        insufficientFundsModal: { open: false, profitPotential: 0, creditCost: 0, creditRequired: 0 },
        creditActiveState: {
          active: true,
          expiresAt: data.expires_at ? new Date(data.expires_at as string) : null,
          depleted: false,
        },
        borrowed: borrowedFromData(data),
        borrowedBalances: borrowedBalancesFromData(data, {}),
        isRebalancing: false,
        rebalanceExpiresAt: null,
      }));
    } else if (data.type === "CREDIT_EXPIRED") {
      setState(prev => ({
        ...prev,
        ...applyWalletSync(prev),
        creditActiveState: { active: false, expiresAt: null, depleted: false },
        borrowed: { binance: { usd: 0, btc: 0 }, bitso: { usd: 0, btc: 0 } },
        borrowedBalances: {},
        rebalanceSuccessAmount: prev.totalWealth,
        loanResults: { earnings: (data.loan_earnings as number) || 0, cost: (data.loan_cost as number) || 0 },
      }));
      setTimeout(() => setState(prev => ({ ...prev, rebalanceSuccessAmount: null })), 6000);
      setTimeout(() => setState(prev => ({ ...prev, loanResults: null })), 10000);
    } else if (data.type === "CREDIT_DEPLETED") {
      setState(prev => ({
        ...prev,
        creditActiveState: { ...prev.creditActiveState, depleted: true }
      }));
    } else if (data.type === "REPLENISHING_STARTED") {
      setState(prev => ({
        ...prev,
        isRebalancing: true,
        rebalanceMessage: (data.message as string) || "Reequilibrando fondos entre exchanges...",
        rebalanceExpiresAt: data.replenish_expires_at ? new Date(data.replenish_expires_at as string) : null,
        insufficientFundsModal: { open: false, profitPotential: 0, creditCost: 0, creditRequired: 0 },
      }));
    } else if (data.type === "REPLENISHING_COMPLETE") {
      setState(prev => ({
        ...prev,
        ...applyWalletSync(prev),
        isRebalancing: false,
        rebalanceExpiresAt: null,
        rebalanceMessage: "",
        rebalanceSuccessAmount: prev.totalWealth,
      }));
      setTimeout(() => setState(prev => ({ ...prev, rebalanceSuccessAmount: null })), 6000);
    } else if (data.type === "AUTO_CREDIT_TOGGLED") {
      setState(prev => ({ ...prev, autoCreditMode: data.auto_mode === true }));
    } else if (data.type === "ENGINE_SHUTDOWN") {
      setState(prev => ({ ...prev, engineRunning: false }));
    } else if (data.type === "market_update") {
      setState(prev => ({
        ...prev,
        livePrices: { binance: data.binance_price as number, bitso: data.bitso_price as number },
        ping: true,
      }));
      setTimeout(() => setState(prev => ({ ...prev, ping: false })), 300);
    } else if (data.event === "market_rebalanced") {
      setState(prev => ({
        ...prev,
        ...applyWalletSync(prev),
        isRebalancing: false,
        rebalanceExpiresAt: null,
        creditActiveState: { active: false, expiresAt: null },
        borrowed: { binance: { usd: 0, btc: 0 }, bitso: { usd: 0, btc: 0 } },
        borrowedBalances: {},
        inventoryFlash: null,
      }));
    } else if (data.type === "CIRCUIT_BREAKER") {
      // FASE 3: rechazo por gestión de riesgo. Anuncio efímero centrado, sin luces
      // verdes (no hay operación). Se auto-descarta a los 3 s (id-guarded para no
      // borrar una alerta posterior si el usuario pulsa varias veces seguidas).
      const id = ++cbCounterRef.current;
      setState(prev => ({
        ...prev,
        circuitBreaker: { id, scenario: (data.scenario as string) ?? "spread", message: (data.message as string) ?? "Operación abortada por seguridad." },
      }));
      setTimeout(() => setState(prev => (prev.circuitBreaker?.id === id ? { ...prev, circuitBreaker: null } : prev)), 3000);
    } else if (data.type === "storm_started") {
      // FASE 3: comienza la ráfaga HFT (modo tormenta en el radar).
      setState(prev => ({ ...prev, stormActive: true, stormResult: null }));
    } else if (data.type === "storm_trade") {
      // Cada fill de la tormenta: luces + P&L + saldos (sesgo espacial en vivo).
      const flashId = ++flashCounterRef.current;
      setState(prev => {
        const nextBalances = balancesFromData(data, prev.balances);
        const byVenueAsset: InventoryFlash["byVenueAsset"] = {};
        for (const [venue, assets] of Object.entries(nextBalances)) {
          for (const [asset, qty] of Object.entries(assets)) {
            const before = prev.balances?.[venue]?.[asset] ?? 0;
            const delta = qty - before;
            if (Math.abs(delta) < 1e-12) continue;
            if (!byVenueAsset[venue]) byVenueAsset[venue] = {};
            byVenueAsset[venue][asset] = delta;
          }
        }
        const hasFlash = Object.keys(byVenueAsset).length > 0;
        const deltas: Record<string, number> = {};
        for (const [venue, assets] of Object.entries(byVenueAsset)) {
          for (const [asset, delta] of Object.entries(assets)) {
            deltas[`${asset}@${venue}`] = delta;
          }
        }
        return {
          ...prev,
          balances: nextBalances,
          wallets: walletsFromBalances(nextBalances),
          graph: graphWithBalances(prev.graph, nextBalances),
          totalWealth: (data.total_wealth as number) ?? prev.totalWealth,
          totalNetProfit: (data.total_net_profit as number) ?? prev.totalNetProfit,
          opsCount: prev.opsCount + 1,
          inventoryFlash: hasFlash ? { id: flashId, deltas, byVenueAsset } : prev.inventoryFlash,
          trades: [{
            event: "arbitrage_executed",
            exchange_buy: data.buy_venue as string,
            exchange_sell: data.sell_venue as string,
            net_profit_usd: (data.net_profit_usd as number) ?? 0,
            new_total_usd: (data.total_wealth as number) ?? prev.totalWealth,
            timestamp: (data.timestamp as string) ?? "",
            storm: true,
          } as Trade, ...prev.trades].slice(0, 15),
        };
      });
      setTimeout(() => {
        setState(prev =>
          prev.inventoryFlash?.id === flashId ? { ...prev, inventoryFlash: null } : prev,
        );
      }, HFT_FLASH_MS);
    } else if (data.type === "storm_ended") {
      // Fin de la tormenta: resumen efímero (operaciones + ganancia acumulada).
      setState(prev => ({
        ...prev,
        stormActive: false,
        stormResult: { trades: (data.trades as number) ?? 0, profit: (data.profit as number) ?? 0 },
      }));
      setTimeout(() => setState(prev => ({ ...prev, stormResult: null })), 8000);
    } else if (data.event === "omni_executed") {
      // Ciclo atómico real: el inventario YA quedó sesgado en Go (compra/venta).
      // Sync balances + wallets; flash solo resalta celdas con el delta neto.
      const flashId = ++flashCounterRef.current;
      const byVenueAsset = flashFromOmni(data);
      const deltas = (data.flash_delta_by_node as Record<string, number>) ?? {};
      const hasFlash = Object.keys(byVenueAsset).length > 0;
      setState(prev => {
        const nextBalances = balancesFromData(data, prev.balances);
        return {
          ...prev,
          balances: nextBalances,
          wallets: walletsFromBalances(nextBalances),
          graph: graphWithBalances(prev.graph, nextBalances),
          omniPulse: {
            id: (prev.omniPulse?.id ?? 0) + 1,
            path: (data.path as string[]) ?? (data.trade_route as string[]) ?? [],
            legs: (data.legs as OmniLeg[]) ?? [],
            route: (data.route as string) ?? "",
            net: (data.net_profit_usd as number) ?? 0,
            flashNodes: Object.keys(deltas),
          },
          inventoryFlash: hasFlash
            ? { id: flashId, deltas, byVenueAsset }
            : null,
        };
      });
      if (hasFlash) {
        setTimeout(() => {
          setState(prev =>
            prev.inventoryFlash?.id === flashId ? { ...prev, inventoryFlash: null } : prev,
          );
        }, HFT_FLASH_MS);
      }
    } else if (data.event === "arbitrage_executed") {
      setState(prev => ({
        ...prev,
        ...applyWalletSync(prev),
        opsCount: prev.opsCount + 1,
        trades: [data as unknown as Trade, ...prev.trades].slice(0, 15),
        creditActiveState: data.credit_active
          ? prev.creditActiveState
          : prev.creditActiveState,
      }));
    }
  }, []);

  useEffect(() => {
    const interval = setInterval(() => {
      setState(prev => {
        if (!sessionReady) return prev;
        return { ...prev, uptimeSeconds: prev.uptimeSeconds + 1 };
      });
    }, 1000);
    return () => clearInterval(interval);
  }, [sessionReady]);

  return {
    sessionReady,
    resuming,
    cancelResume,
    state,
    initSession,
    resetSession,
    demoInject,
    simulateCustom,
    injectOmni,
    injectStorm,
    injectFake,
    toggleAutoCredit,
    requestCredit,
    waitRebalance,
    dismissShortfall,
    adjustFunds,
    setParams,
    shutdownEngine,
  };
}
