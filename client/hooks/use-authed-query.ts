"use client";

import { useQuery, type UseQueryOptions, type UseQueryResult } from "@tanstack/react-query";
import { useAuth } from "@/contexts/auth-context";

/**
 * A query that does not run until the session is known to be good.
 *
 * The session starts as `{ status: "loading" }` while getSession() is in flight,
 * and firing a request in that window gets a 401 -- which the transport turns
 * into a session-lost redirect. Every call site used to open with
 * `if (state.status !== "authenticated") return;` inside its effect; this is that
 * gate, once.
 *
 * The user is deliberately not part of the query key. Sign-out drops the cached
 * queries instead (see AuthProvider), so keys stay readable and a signed-out
 * cache holds nothing to leak.
 */
export function useAuthedQuery<TData, TError = Error>(
  options: UseQueryOptions<TData, TError> & { queryKey: readonly unknown[] }
): UseQueryResult<TData, TError> {
  const { state } = useAuth();
  return useQuery({
    ...options,
    enabled: state.status === "authenticated" && (options.enabled ?? true),
  });
}
