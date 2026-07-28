import { beforeEach, describe, expect, it, vi } from "vitest";
import { clearSessionId, getSessionId, setSessionId } from "./session";

let store: Record<string, unknown> = {};

vi.stubGlobal("chrome", {
  storage: {
    session: {
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

describe("session store", () => {
  beforeEach(() => {
    store = {};
  });

  it("returns an empty string when no session is stored", async () => {
    expect(await getSessionId()).toBe("");
  });

  it("round-trips a session id", async () => {
    await setSessionId("abc123");
    expect(await getSessionId()).toBe("abc123");
  });

  it("clears the stored session", async () => {
    await setSessionId("abc123");
    await clearSessionId();
    expect(await getSessionId()).toBe("");
  });

  it("treats a non-string stored value as absent", async () => {
    store = { sessionId: 42 };
    expect(await getSessionId()).toBe("");
  });
});
