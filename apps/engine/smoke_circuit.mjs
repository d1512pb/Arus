// Smoke E2E: inject_fake ("Precio falso / error" → Circuit Breaker).
// Requiere el motor en :8080 con feeds reales. Node >= 21.
//
//   node smoke_circuit.mjs
//
// Verifica:
//   1. CIRCUIT_BREAKER por WS (alerta de UI).
//   2. Log de rechazo ([SPIKE BLOQUEADO] / [CIRCUIT BREAKER]).
//   3. Sin arbitrage_executed / omni_executed (cero luces verdes de trade).
//   4. Patrimonio sin cambio material tras el rechazo.

const WS = "ws://localhost:8080/ws";

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
let failures = 0;
const check = (ok, label) => {
  console.log(`${ok ? "✅" : "❌"} ${label}`);
  if (!ok) failures++;
};

const ws = new WebSocket(WS);
let sessionId = "";
const logs = [];
const events = [];
let wealth = null;
let pnl = null;

ws.onmessage = (ev) => {
  const data = JSON.parse(ev.data);
  events.push(data);
  if (data.type === "log") logs.push(data.message);
  if (data.type === "state_update" && data.session_id) sessionId = data.session_id;
  if (data.type === "wallet_update" || data.type === "state_update") {
    if (data.total_wealth !== undefined) wealth = data.total_wealth;
    if (data.total_net_profit !== undefined) pnl = data.total_net_profit;
  }
};

await new Promise((resolve, reject) => {
  ws.onopen = resolve;
  ws.onerror = reject;
});
ws.send(JSON.stringify({ action: "init_session", initial_usd: 100000, initial_btc: 2 }));
await sleep(2500);
check(sessionId !== "", `sesión inicializada (${sessionId.slice(0, 8)}…)`);
await sleep(1500); // feeds calientes

const wealthBefore = wealth;
const pnlBefore = pnl ?? 0;
logs.length = 0;
const before = events.length;

ws.send(JSON.stringify({ action: "inject_fake" }));
await sleep(1200);

const cb = events.slice(before).find((e) => e.type === "CIRCUIT_BREAKER");
check(cb !== undefined, `CIRCUIT_BREAKER recibido (scenario=${cb?.scenario ?? "?"})`);
check(typeof cb?.message === "string" && cb.message.length > 20, `mensaje de alerta usable (${(cb?.message ?? "").slice(0, 60)}…)`);

const rejectLog = logs.find(
  (m) =>
    m.includes("[SPIKE BLOQUEADO]") ||
    m.includes("[CIRCUIT BREAKER]") ||
    m.includes("DESCARTADO")
);
check(rejectLog !== undefined, `log de rechazo: ${rejectLog ?? "(nada)"}`);

const traded = events
  .slice(before)
  .some((e) => e.event === "arbitrage_executed" || e.event === "omni_executed" || e.type === "storm_trade");
check(!traded, "ningún trade ejecutado (sin luces verdes de operación)");

await sleep(400);
const wealthAfter = wealth;
const pnlAfter = pnl ?? 0;
check(
  wealthBefore === null || Math.abs((wealthAfter ?? 0) - wealthBefore) < 0.01,
  `patrimonio intacto (${wealthBefore} → ${wealthAfter})`
);
check(Math.abs(pnlAfter - pnlBefore) < 0.01, `PnL intacto (${pnlBefore} → ${pnlAfter})`);

// Segunda pulsación: debe seguir respondiendo (rota escenarios).
logs.length = 0;
const before2 = events.length;
ws.send(JSON.stringify({ action: "inject_fake" }));
await sleep(1200);
const cb2 = events.slice(before2).find((e) => e.type === "CIRCUIT_BREAKER");
check(cb2 !== undefined, `segunda pulsación también emite CIRCUIT_BREAKER (${cb2?.scenario ?? "?"})`);

ws.close();
console.log(failures === 0 ? "\n🎉 SMOKE CIRCUIT OK" : `\n💥 ${failures} fallos`);
process.exit(failures === 0 ? 0 : 1);
