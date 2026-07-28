import { beforeEach, describe, expect, it, vi } from "vitest";
import { clearRuns, listRuns, recordRun, type RunLogEntry } from "./run-log";

let store: Record<string, unknown> = {};

vi.stubGlobal("chrome", {
  storage: {
    local: {
      get: vi.fn(async (key: string) => (key in store ? { [key]: store[key] } : {})),
      set: vi.fn(async (items: Record<string, unknown>) => {
        store = { ...store, ...items };
      }),
      remove: vi.fn(async (key: string) => {
        delete store[key];
      }),
    },
  },
});

const entry = (startedAt: string): RunLogEntry => ({
  startedAt,
  recipeId: "fidelity-uk",
  status: "success",
});

describe("run log", () => {
  beforeEach(() => {
    store = {};
  });

  it("is empty before anything runs", async () => {
    expect(await listRuns()).toEqual([]);
  });

  it("puts the most recent run first", async () => {
    await recordRun(entry("2026-07-26T10:00:00Z"));
    await recordRun(entry("2026-07-27T10:00:00Z"));
    expect((await listRuns()).map((r) => r.startedAt)).toEqual([
      "2026-07-27T10:00:00Z",
      "2026-07-26T10:00:00Z",
    ]);
  });

  it("keeps at most 20 runs", async () => {
    for (let i = 0; i < 25; i++) await recordRun(entry(`2026-07-${String(i + 1).padStart(2, "0")}`));
    const runs = await listRuns();
    expect(runs).toHaveLength(20);
    expect(runs[0]!.startedAt).toBe("2026-07-25");
  });

  it("survives a stored value that is not a list", async () => {
    store = { runLog: "corrupt" };
    expect(await listRuns()).toEqual([]);
  });

  it("clears", async () => {
    await recordRun(entry("2026-07-27T10:00:00Z"));
    await clearRuns();
    expect(await listRuns()).toEqual([]);
  });
});
