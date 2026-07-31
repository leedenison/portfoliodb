import React from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

/**
 * A QueryClient for tests: no retries, so a rejecting queryFn surfaces as an
 * error immediately rather than after backoff, and nothing goes stale on its
 * own, so the only refetches are the ones a test asks for. `invalidateQueries`
 * still refetches regardless of staleTime.
 */
export function createTestQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: Infinity, gcTime: Infinity },
    },
  });
}

/**
 * Wrapper for renderHook/render. Each call gets its own client so cache state
 * cannot leak between tests.
 */
export function queryWrapper(client: QueryClient = createTestQueryClient()) {
  return function Wrapper({ children }: { children: React.ReactNode }) {
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  };
}
