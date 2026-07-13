// Smoke E2E de FASE 2 (refactor Probar Bot): inject_omni ("Oportunidad normal").
// Requiere el motor corriendo en :8080 con feeds reales. Node >= 21.
//
//   node smoke_omni.mjs
//
// Verifica:
//   1. Inyección omnidireccional → log [OMNI] + detección [RADAR] + ejecución real.
//   2. Evento omni_executed con path de ≥3 nodos (ciclo dinámico, no par clásico).
//   3. Universo podado (solo BTC, sin ETH) → escenario espacial o narración honesta.

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
await sleep(2000);
check(sessionId !== "", `sesión inicializada (${sessionId.slice(0, 8)}…)`);

// Margen bajo para que el demo omnidireccional no muera por umbral del usuario.
ws.send(JSON.stringify({ action: "set_params", params: { min_net_profit_usd: 0.5 } }));
await sleep(500);

const cfg = await (await fetch(`${HTTP}/api/config`)).json();
const btc = cfg?.reference?.btc_price_usd ?? 60000;
console.log(`   precio de referencia BTC: $${btc}`);

// ── 1. Omnidireccional con universo completo (triangular si ETH está activo) ─
logs.length = 0;
events.length = 0;
const profitBefore = events.filter((e) => e.total_net_profit).at(-1)?.total_net_profit ?? 0;
ws.send(JSON.stringify({ action: "inject_omni" }));
await sleep(5000);

check(logs.some((m) => m.includes("[OMNI]")), "log [OMNI] — ineficiencia inyectada en tubería real");
const radar = logs.find((m) => m.includes("[RADAR]") || m.includes("[CICLO EJECUTADO]"));
check(radar !== undefined, `radar/ejecutor respondió: ${radar ?? "(nada)"}`);

const omni = events.find((e) => e.event === "omni_executed");
check(omni !== undefined, `omni_executed recibido: ${omni ? omni.route : "(no ejecutó)"}`);
if (omni) {
  const path = omni.path ?? [];
  check(path.length >= 3, `path dinámico con ≥3 nodos (${path.length}): ${path.join(" → ")}`);
  check((omni.net_profit_usd ?? 0) > 0, `ganancia neta positiva: +$${(omni.net_profit_usd ?? 0).toFixed(2)}`);
  check((omni.legs?.length ?? 0) >= 2, `≥2 piernas en el plan real (${omni.legs?.length ?? 0})`);
}

// ── 2. Universo podado: solo Binance+Bitso con BTC (sin ETH/SOL) ─────────────
const params = events.filter((e) => e.params).at(-1)?.params ?? {};
ws.send(JSON.stringify({
  action: "set_params",
  params: {
    ...params,
    enabled_venues: ["Binance", "Bitso"],
    enabled_assets: ["USDT", "USD", "BTC"],
    min_net_profit_usd: 0.5,
  },
}));
await sleep(800);
logs.length = 0;
ws.send(JSON.stringify({ action: "inject_omni" }));
await sleep(5000);

const omniSpatial = events.filter((e) => e.event === "omni_executed").at(-1);
const spatialLog = logs.find((m) => m.includes("[OMNI]") && (m.includes("espacial") || m.includes("paralela")));
const noRoute = logs.find((m) => m.includes("No hay ruta omnidireccional"));
check(
  omniSpatial !== undefined || spatialLog !== undefined || noRoute !== undefined,
  omniSpatial
    ? `universo podado ejecutó ciclo: ${omniSpatial.route}`
    : spatialLog
      ? `universo podado eligió espacial: ${spatialLog.slice(0, 80)}…`
      : `narración honesta sin ruta: ${noRoute ?? "(sin respuesta)"}`
);

ws.close();
console.log(failures === 0 ? "\n🎉 SMOKE OMNI OK" : `\n💥 ${failures} fallos`);
process.exit(failures === 0 ? 0 : 1);
