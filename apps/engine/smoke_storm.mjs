// Smoke E2E de FASE 3 (refactor Probar Bot): inject_storm ("Evento poco común").
// Requiere el motor corriendo en :8080 con feeds reales. Node >= 21.
//
//   node smoke_storm.mjs
//
// Verifica:
//   1. storm_started → varios storm_trade → storm_ended.
//   2. ≥8 micro-operaciones en ~4 s (meta demo 15–20; umbral laxo ante feeds).
//   3. Log [TORMENTA] con tubería real / workers.
//   4. Ganancia acumulada ≥ 0.

const HTTP = "http://localhost:8080";
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

ws.onmessage = (ev) => {
  const data = JSON.parse(ev.data);
  events.push(data);
  if (data.type === "log") logs.push(data.message);
  if (data.type === "state_update" && data.session_id) sessionId = data.session_id;
};

await new Promise((resolve, reject) => {
  ws.onopen = resolve;
  ws.onerror = reject;
});
ws.send(JSON.stringify({ action: "init_session", initial_usd: 100000, initial_btc: 2 }));
await sleep(2500);
check(sessionId !== "", `sesión inicializada (${sessionId.slice(0, 8)}…)`);

ws.send(JSON.stringify({ action: "set_params", params: { min_net_profit_usd: 0.05 } }));
await sleep(500);

// Esperar a que los feeds llenen libros (triangular necesita ETH).
await sleep(2000);

logs.length = 0;
const before = events.length;
ws.send(JSON.stringify({ action: "inject_storm" }));
await sleep(6500);

const started = events.slice(before).find((e) => e.type === "storm_started");
const ended = events.slice(before).find((e) => e.type === "storm_ended");
const trades = events.slice(before).filter((e) => e.type === "storm_trade");

check(started !== undefined, `storm_started (${started?.duration_ms ?? "?"} ms)`);
check(logs.some((m) => m.includes("[TORMENTA]") && m.includes("workers")), "log de arranque con workers / tubería real");
check(ended !== undefined, `storm_ended: ${ended ? `${ended.trades} trades · +$${Number(ended.profit).toFixed(2)}` : "(nada)"}`);
check(trades.length >= 8, `≥8 storm_trade recibidos (${trades.length})`);
check((ended?.trades ?? 0) >= 8 && (ended?.trades ?? 0) <= 25, `resumen ~15–20 ops (±margen): ${ended?.trades ?? 0}`);
check((ended?.profit ?? -1) >= 0, `ganancia acumulada ≥ 0 (+$${Number(ended?.profit ?? 0).toFixed(2)})`);

ws.close();
console.log(failures === 0 ? "\n🎉 SMOKE STORM OK" : `\n💥 ${failures} fallos`);
process.exit(failures === 0 ? 0 : 1);
