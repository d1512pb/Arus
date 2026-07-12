import { useState, useRef, useCallback, useEffect } from "react";
import { ENGINE_WS_URL } from "../lib/config";

export interface Trade {
  event: string;
  exchange_buy: string;
  exchange_sell: string;
  net_profit_usd: number;
  new_total_usd: number;
  timestamp: string | number;
  volume?: number;
  credit_active?: boolean;
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

// Distribución del capital por venue (porcentajes, suman 100).
export type Allocation = Record<string, number>;

export interface EngineState {
  sessionId: string;
  params: TradingParams | null;
  graph: GraphSnapshot | null;
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
  wallets: {
    binance: { usd: number; btc: number };
    bitso: { usd: number; btc: number };
  };
  borrowed: {
    binance: { usd: number; btc: number };
    bitso: { usd: number; btc: number };
  };
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

function walletsFromData(data: Record<string, unknown>) {
  if (data.binance_usd === undefined) return null;
  return {
    binance: { usd: data.binance_usd as number, btc: (data.binance_btc as number) ?? 0 },
    bitso: { usd: data.bitso_usd as number, btc: (data.bitso_btc as number) ?? 0 },
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
  trades: [],
  totalWealth: 0,
  initialWealth: 0,
  initialUsd: 0,
  totalNetProfit: 0,
  wallets: { binance: { usd: 0, btc: 0 }, bitso: { usd: 0, btc: 0 } },
  borrowed: { binance: { usd: 0, btc: 0 }, bitso: { usd: 0, btc: 0 } },
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
  const configRef = useRef<{ usd: number; btc: number; usdAlloc?: Allocation; btcAlloc?: Allocation } | null>(null);
  const reconnectTimeoutRef = useRef<NodeJS.Timeout | null>(null);
  // Universo elegido en la checklist del onboarding (exchanges/monedas activos).
  // Se aplica vía set_params en cuanto la sesión existe (el primer state_update),
  // porque init_session no lleva el universo. null = sin poda (todo el catálogo).
  const pendingUniverseRef = useRef<{ enabledVenues?: string[]; enabledAssets?: string[] } | null>(null);

  // initMsg arma el payload de init_session con la distribución (si la hay) —
  // lo comparten el arranque normal y la re-inicialización tras reconexión.
  const initMsg = (cfg: { usd: number; btc: number; usdAlloc?: Allocation; btcAlloc?: Allocation }) =>
    JSON.stringify({
      action: "init_session",
      initial_usd: cfg.usd,
      initial_btc: cfg.btc,
      usd_allocation: cfg.usdAlloc,
      btc_allocation: cfg.btcAlloc,
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

  const initSession = useCallback((usd: number, btc: number, usdAlloc?: Allocation, btcAlloc?: Allocation, enabledVenues?: string[], enabledAssets?: string[]) => {
    configRef.current = { usd, btc, usdAlloc, btcAlloc };
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

  // Depósito (amount > 0) o retiro (amount < 0) de USD/BTC en un exchange.
  const adjustFunds = useCallback((exchange: string, currency: "USD" | "BTC", amount: number) => {
    if (wsRef.current && amount !== 0) {
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
      if (data.total_wealth !== undefined) patch.totalWealth = data.total_wealth as number;
      if (data.new_total_usd !== undefined) patch.totalWealth = data.new_total_usd as number;
      if (data.total_wealth_usd !== undefined) patch.totalWealth = data.total_wealth_usd as number;
      // Depósito/retiro mueve la base inicial junto con el total → el PnL no se distorsiona.
      if (data.initial_wealth !== undefined) patch.initialWealth = data.initial_wealth as number;
      if (data.initial_usd !== undefined) patch.initialUsd = data.initial_usd as number;
      if (data.borrowed_binance_usd !== undefined || data.credit_active === false) {
        patch.borrowed = data.credit_active === false && data.borrowed_binance_usd === undefined
          ? { binance: { usd: 0, btc: 0 }, bitso: { usd: 0, btc: 0 } }
          : borrowedFromData(data);
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
        wallets: {
          binance: { usd: data.binance_usd as number, btc: data.binance_btc as number },
          bitso: { usd: data.bitso_usd as number, btc: data.bitso_btc as number },
        },
        borrowed: { binance: { usd: 0, btc: 0 }, bitso: { usd: 0, btc: 0 } },
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
      setState(prev => ({ ...prev, graph: (data.graph as GraphSnapshot) ?? prev.graph }));
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
        isRebalancing: false,
        rebalanceExpiresAt: null,
      }));
    } else if (data.type === "CREDIT_EXPIRED") {
      setState(prev => ({
        ...prev,
        ...applyWalletSync(prev),
        creditActiveState: { active: false, expiresAt: null, depleted: false },
        borrowed: { binance: { usd: 0, btc: 0 }, bitso: { usd: 0, btc: 0 } },
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
      }));
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
    toggleAutoCredit,
    requestCredit,
    waitRebalance,
    adjustFunds,
    setParams,
    shutdownEngine,
  };
}
