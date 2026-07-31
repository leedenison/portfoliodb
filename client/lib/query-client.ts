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
 *   - staleTime 0: price fetching, corporate event discovery and split
 *     adjustment all change data behind the client's back, so treating a cached
 *     read as fresh for any window risks showing a stale table right after a
 *     worker has run. Mounting still paints the cached value immediately and
 *     revalidates behind it, so this is not a return to a blank loading state.
 *
 * gcTime is left at the library default and stated only for the record.
 */
export function createQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        retry: 0,
        refetchOnWindowFocus: false,
        refetchOnReconnect: false,
        staleTime: 0,
        gcTime: 5 * 60_000,
      },
    },
  });
}
