// universeCapability.ts — espejo ligero de analyzeUniverse (Go): ¿el universo
// del usuario admite al menos un ciclo espacial o triangular?

export type UniverseCapability = {
  spatial: boolean;
  triangular: boolean;
  venueCount: number;
  ok: boolean;
  reason: string;
};

const TRIANGLE_MIDS = ["ETH", "SOL"] as const;

/** Catálogo mínimo alineado al registro del motor (venues.go). */
const VENUE_BOOKS: Record<string, { cash: string; cryptos: string[]; triangleMids: string[] }> = {
  Binance: { cash: "USDT", cryptos: ["BTC", "ETH", "SOL"], triangleMids: ["ETH", "SOL"] },
  Bitso: { cash: "USD", cryptos: ["BTC"], triangleMids: [] },
  Kraken: { cash: "USD", cryptos: ["BTC", "ETH"], triangleMids: ["ETH"] },
};

function allowed(list: string[] | undefined | null, item: string): boolean {
  if (!list || list.length === 0) return true;
  return list.includes(item);
}

export function analyzeUniverseClient(
  enabledVenues: string[] | undefined | null,
  enabledAssets: string[] | undefined | null,
  catalogVenues: string[] = Object.keys(VENUE_BOOKS),
): UniverseCapability {
  const venues = (enabledVenues && enabledVenues.length > 0 ? enabledVenues : catalogVenues)
    .filter((v) => VENUE_BOOKS[v]);

  const withBTC: { name: string; cash: string }[] = [];
  let triangular = false;

  for (const name of venues) {
    const meta = VENUE_BOOKS[name];
    if (!meta) continue;
    if (!allowed(enabledAssets, meta.cash)) continue;
    if (allowed(enabledAssets, "BTC") && meta.cryptos.includes("BTC")) {
      withBTC.push({ name, cash: meta.cash });
    }
    for (const mid of meta.triangleMids) {
      if (TRIANGLE_MIDS.includes(mid as "ETH" | "SOL") && allowed(enabledAssets, mid) && allowed(enabledAssets, "BTC")) {
        triangular = true;
      }
    }
  }

  let spatial = false;
  for (let i = 0; i < withBTC.length; i++) {
    for (let j = i + 1; j < withBTC.length; j++) {
      const a = withBTC[i], b = withBTC[j];
      if (a.cash === b.cash || (a.cash === "USDT" && b.cash === "USD") || (a.cash === "USD" && b.cash === "USDT")) {
        spatial = true;
      }
    }
  }

  const ok = spatial || triangular;
  let reason = "";
  if (!ok) {
    if (venues.length === 0) {
      reason = "No hay ninguna casa activa.";
    } else if (venues.length === 1 && withBTC.length <= 1 && !triangular) {
      reason = "Con una sola casa y solo BTC/efectivo no hay ciclo de arbitraje. Activa otra casa o ETH/SOL.";
    } else {
      reason = "Este universo no admite arbitraje espacial ni triangular. Amplía casas o monedas.";
    }
  }

  return { spatial, triangular, venueCount: venues.length, ok, reason };
}
