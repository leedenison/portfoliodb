import { beforeEach, describe, expect, it, vi } from "vitest";
import { DEFAULT_CONFIG, loadConfig, missingRequired, saveConfig } from "./config";

/** Minimal chrome.storage.local stand-in; the extension only uses get and set. */
function fakeStorage() {
  let store: Record<string, unknown> = {};
  return {
    reset: () => {
      store = {};
    },
    api: {
      get: vi.fn(async (key: string) => (key in store ? { [key]: store[key] } : {})),
      set: vi.fn(async (items: Record<string, unknown>) => {
        store = { ...store, ...items };
      }),
    },
  };
}

const storage = fakeStorage();
vi.stubGlobal("chrome", { storage: { local: storage.api } });

describe("config", () => {
  beforeEach(() => {
    storage.reset();
    vi.clearAllMocks();
  });

  it("returns defaults when nothing is stored", async () => {
    expect(await loadConfig()).toEqual(DEFAULT_CONFIG);
  });

  it("defaults the lookback to 14 days", async () => {
    // The lookback must exceed the broker's order-to-completion lag; changing the
    // default silently changes how far back every sync reaches.
    expect(DEFAULT_CONFIG.lookbackDays).toBe(14);
  });

  it("merges a partial update over stored values", async () => {
    await saveConfig({ portfoliodbOrigin: "http://localhost:8080" });
    await saveConfig({ currency: "GBP" });

    const config = await loadConfig();
    expect(config.portfoliodbOrigin).toBe("http://localhost:8080");
    expect(config.currency).toBe("GBP");
    expect(config.lookbackDays).toBe(DEFAULT_CONFIG.lookbackDays);
  });

  it("fills in defaults for keys absent from stored config", async () => {
    // A config written before a setting existed must not come back undefined.
    await storage.api.set({ config: { currency: "GBP" } });

    const config = await loadConfig();
    expect(config.currency).toBe("GBP");
    expect(config.timeZone).toBe(DEFAULT_CONFIG.timeZone);
  });

  it("reports the settings a sync cannot run without", async () => {
    expect(missingRequired(DEFAULT_CONFIG)).toEqual(["portfoliodbOrigin", "currency"]);
    expect(
      missingRequired({ ...DEFAULT_CONFIG, portfoliodbOrigin: "http://x", currency: "GBP" })
    ).toEqual([]);
  });
});
