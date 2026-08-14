/**
 * The gate that keeps a query from firing before the session is known.
 *
 * Firing one in that window gets a 401, which the transport turns into a
 * session-lost redirect -- so the bug this prevents is not a failed request but a
 * user bounced to the sign-in page on every reload. That makes the loading state,
 * rather than the authenticated one, the case worth pinning.
 */

import React from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { useAuthedQuery } from "./use-authed-query";

type AuthState =
  | { status: "loading" }
  | { status: "unauthenticated" }
  | { status: "authenticated"; user: unknown; email: string; role: string };

// The hook reads state.status off the auth context and nothing else, so the
// context is mocked rather than driven through AuthProvider's getSession call.
const authState = vi.hoisted(() => ({ current: { status: "loading" } as AuthState }));
vi.mock("@/contexts/auth-context", () => ({
  useAuth: () => ({ state: authState.current }),
}));

function wrapper({ children }: { children: React.ReactNode }) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

/** Renders the hook against a query function that records whether it ran. */
function renderQuery(state: AuthState, enabled?: boolean) {
  authState.current = state;
  const queryFn = vi.fn().mockResolvedValue("data");
  const view = renderHook(
    () => useAuthedQuery({ queryKey: ["thing"], queryFn, ...(enabled === undefined ? {} : { enabled }) }),
    { wrapper }
  );
  return { queryFn, ...view };
}

describe("useAuthedQuery", () => {
  it("does not fetch while the session is still loading", async () => {
    const { queryFn, result } = renderQuery({ status: "loading" });
    // Nothing to wait for, so give the query a chance to fire if it were going to.
    await Promise.resolve();
    expect(queryFn).not.toHaveBeenCalled();
    expect(result.current.fetchStatus).toBe("idle");
  });

  it("does not fetch when the session is known to be absent", async () => {
    const { queryFn } = renderQuery({ status: "unauthenticated" });
    await Promise.resolve();
    expect(queryFn).not.toHaveBeenCalled();
  });

  it("fetches once the session is good", async () => {
    const { queryFn, result } = renderQuery({
      status: "authenticated",
      user: {},
      email: "u@example.com",
      role: "user",
    });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(queryFn).toHaveBeenCalled();
    expect(result.current.data).toBe("data");
  });

  it("fetches when the caller says enabled and the session is good", async () => {
    const { queryFn, result } = renderQuery(
      { status: "authenticated", user: {}, email: "u@example.com", role: "user" },
      true
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(queryFn).toHaveBeenCalled();
  });

  it("does not fetch when the caller says disabled, session or no session", async () => {
    // The caller's own enabled is combined with the gate rather than replaced by
    // it: a query waiting on an id it does not have yet must stay waiting.
    const { queryFn } = renderQuery(
      { status: "authenticated", user: {}, email: "u@example.com", role: "user" },
      false
    );
    await Promise.resolve();
    expect(queryFn).not.toHaveBeenCalled();
  });

  it("starts fetching when the session arrives", async () => {
    const { queryFn, result, rerender } = renderQuery({ status: "loading" });
    await Promise.resolve();
    expect(queryFn).not.toHaveBeenCalled();

    authState.current = { status: "authenticated", user: {}, email: "u@example.com", role: "user" };
    rerender();

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(queryFn).toHaveBeenCalledTimes(1);
  });

  it("keys the query on what the caller gave and nothing else", async () => {
    // The user is deliberately not part of the key: sign-out drops the cached
    // queries instead, so keys stay readable and a signed-out cache holds nothing.
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
    authState.current = { status: "authenticated", user: {}, email: "u@example.com", role: "user" };
    const { result } = renderHook(
      () => useAuthedQuery({ queryKey: ["thing", 7], queryFn: () => Promise.resolve("data") }),
      {
        wrapper: ({ children }: { children: React.ReactNode }) => (
          <QueryClientProvider client={client}>{children}</QueryClientProvider>
        ),
      }
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(client.getQueryData(["thing", 7])).toBe("data");
  });
});
