// Smoke E2E de FASE 1 (refactor Probar Bot): POST /api/simulate/custom.
// Requiere el motor corriendo en :8080 con feeds reales. Node >= 21.
//
//   node smoke_custom_sim.mjs
//
// Verifica:
//   1. Inyección válida → 200, log [PRUEBA PERSONALIZADA] y veredicto del motor
//      ([OPORTUNIDAD]/[ARBITRAJE] o descarte del Spike Filter) por el WebSocket.
//   2. Venue fuera del universo → 403 con error claro.
//   3. Libro incoherente (cruzado) → 400.
//   4. session_id desconocido → 404.

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
await sleep(1500);
check(sessionId !== "", `sesión inicializada (${sessionId.slice(0, 8)}…)`);

// Precio real de BTC para armar un escenario creíble (dentro del anti-spike).
const cfg = await (await fetch(`${HTTP}/api/config`)).json();
const btc = cfg?.reference?.btc_price_usd ?? 60000;
console.log(`   precio de referencia BTC: $${btc}`);

const post = (body) =>
  fetch(`${HTTP}/api/simulate/custom`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });

// ── 1. Inyección válida: Bitso paga ~1.5 % más que Binance ──────────────────
logs.length = 0;
let res = await post({
  session_id: sessionId,
  exchange_a: "Binance", asset_a: "BTC", bid_a: btc * 0.9995, ask_a: btc * 1.0005,
  exchange_b: "Bitso", asset_b: "BTC", bid_b: btc * 1.015, ask_b: btc * 1.016,
  liquidity: 1.0,
});
let body = await res.json();
check(res.ok && body.status === "injected", `inyección válida aceptada (${res.status}: ${JSON.stringify(body.injected)})`);
await sleep(2500);
check(logs.some((m) => m.includes("[PRUEBA PERSONALIZADA]")), "log [PRUEBA PERSONALIZADA] recibido por WS");
const verdict = logs.find((m) => m.includes("[ARBITRAJE]") || m.includes("[OPORTUNIDAD]") || m.includes("DESCARTADO") || m.includes("SPIKE"));
check(verdict !== undefined, `el motor emitió su veredicto: ${verdict ?? "(nada)"}`);
const executed = events.find((e) => e.event === "arbitrage_executed");
check(executed !== undefined, `trade ejecutado por la tubería real: ${executed ? `+$${executed.net_profit_usd?.toFixed(2)} (${executed.exchange_buy}→${executed.exchange_sell})` : "(no ejecutó)"}`);

// ── 2. Universo: podar Kraken y probarlo debe dar 403 ───────────────────────
const params = events.filter((e) => e.params).at(-1)?.params;
ws.send(JSON.stringify({ action: "set_params", params: { ...params, enabled_venues: ["Binance", "Bitso"] } }));
await sleep(600);
res = await post({
  session_id: sessionId,
  exchange_a: "Kraken", asset_a: "BTC", bid_a: btc * 0.999, ask_a: btc * 1.001,
  exchange_b: "Bitso", asset_b: "BTC", bid_b: btc * 1.01, ask_b: btc * 1.011,
});
body = await res.json();
check(res.status === 403 && /Kraken/.test(body.error ?? ""), `venue fuera del universo rechazado (${res.status}): ${body.error}`);

// ── 3. Libro incoherente (cruzado) → 400 ────────────────────────────────────
res = await post({
  session_id: sessionId,
  exchange_a: "Binance", asset_a: "BTC", bid_a: btc * 1.01, ask_a: btc * 0.99,
  exchange_b: "Bitso", asset_b: "BTC", bid_b: btc, ask_b: btc * 1.001,
});
body = await res.json();
check(res.status === 400, `libro cruzado rechazado (${res.status}): ${body.error}`);

// ── 4. Sesión desconocida → 404 ─────────────────────────────────────────────
res = await post({
  session_id: "00000000-0000-4000-8000-000000000000",
  exchange_a: "Binance", asset_a: "BTC", bid_a: btc, ask_a: btc * 1.001,
  exchange_b: "Bitso", asset_b: "BTC", bid_b: btc * 1.01, ask_b: btc * 1.011,
});
body = await res.json();
check(res.status === 404, `sesión desconocida rechazada (${res.status}): ${body.error}`);

ws.close();
console.log(failures === 0 ? "\n🎉 SMOKE OK" : `\n💥 ${failures} fallos`);
process.exit(failures === 0 ? 0 : 1);
