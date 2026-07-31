import { QueryClient } from "@tanstack/react-query";

/**
 * The app's QueryClient defaults.
 *
 * These deliberately reproduce how the client behaved when every page hand-rolled
 * its own fetch-in-effect, rather than taking the library defaults:
 *
 *   - retry: nothing retried before. Retrying is also wrong for most of what this
 *     backend returns -- INVALID_ARGUMENT and UNAUTHENTICATED will not succeed on
 *     a second attempt -- and it would make the e2e suites issue requests the VCR
 *     cassettes have no entry for.
 *   - refetchOnWindowFocus / refetchOnReconnect: both default to true and would
 *     be background refetches this app has never done.
 *   - staleTime: reactStrictMode double-mounts components in dev, so a non-zero
 *     staleTime makes the second mount a cache hit instead of a second request.
 */
export function createQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        retry: 0,
        refetchOnWindowFocus: false,
        refetchOnReconnect: false,
        staleTime: 30_000,
        gcTime: 5 * 60_000,
      },
    },
  });
}
