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

export interface EngineState {
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
  insufficientFundsModal: { open: boolean; profitPotential: number; creditCost: number };
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

export function useArusEngine() {
  const [sessionReady, setSessionReady] = useState(false);
  const [state, setState] = useState<EngineState>({
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
    insufficientFundsModal: { open: false, profitPotential: 0, creditCost: 0 },
    creditActiveState: { active: false, expiresAt: null, depleted: false },
    loanResults: null,
    engineRunning: true,
  });

  const wsRef = useRef<WebSocket | null>(null);
  const configRef = useRef<{ usd: number; btc: number } | null>(null);
  const reconnectTimeoutRef = useRef<NodeJS.Timeout | null>(null);

  const initSession = useCallback((usd: number, btc: number) => {
    if (wsRef.current?.readyState === WebSocket.OPEN) {
      wsRef.current.close();
    }

    configRef.current = { usd, btc };
    const ws = new WebSocket(ENGINE_WS_URL);
    wsRef.current = ws;

    ws.onopen = () => {
      ws.send(JSON.stringify({ action: "init_session", initial_usd: usd, initial_btc: btc }));
    };

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
        if (configRef.current) {
          initSession(configRef.current.usd, configRef.current.btc);
        }
      }, 3000);
    };

    ws.onerror = () => ws.close();
  }, []);

  const resetSession = useCallback(() => {
    if (wsRef.current && configRef.current) {
      wsRef.current.send(JSON.stringify({
        action: "reset_session",
        initial_usd: configRef.current.usd,
        initial_btc: configRef.current.btc,
      }));
    }
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
      setSessionReady(true);
      setState(prev => ({
        ...prev,
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
        insufficientFundsModal: { open: false, profitPotential: 0, creditCost: 0 },
        loanResults: null,
      }));
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
        },
      }));
    } else if (data.type === "CREDIT_APPROVED" || data.type === "CREDIT_AUTO_APPROVED") {
      setState(prev => ({
        ...prev,
        ...applyWalletSync(prev),
        insufficientFundsModal: { open: false, profitPotential: 0, creditCost: 0 },
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
        insufficientFundsModal: { open: false, profitPotential: 0, creditCost: 0 },
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
    state,
    initSession,
    resetSession,
    demoInject,
    toggleAutoCredit,
    requestCredit,
    waitRebalance,
    adjustFunds,
    shutdownEngine,
  };
}
