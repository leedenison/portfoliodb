"use client";

import { useCallback, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";

export type PaginatedResult<T> = {
  items: T[];
  totalCount: number;
  nextPageToken: string | null;
};

export type UsePaginationOptions<T> = {
  /**
   * Identifies the filter set. The page token is appended internally, so each
   * page is its own cache entry and paging backwards is a cache hit.
   */
  queryKey: readonly unknown[];
  queryFn: (pageToken: string | null) => Promise<PaginatedResult<T>>;
  enabled?: boolean;
};

/**
 * Token-based (cursor) pagination over a TanStack Query cache.
 *
 * `pageTokens[i]` holds the token that fetches page `i`; index 0 is null, and
 * each fetched page writes the token for the page after it. That is what lets
 * "Previous" work by index without re-deriving a token.
 *
 * The hook does not reset itself when the filters change, on purpose. Give the
 * component that calls it a `key` built from the filter values so a change
 * remounts it -- state resets by construction, which is both the idiomatic React
 * answer and the one that avoids resetting state from an effect. Remounting
 * costs no requests: the query cache outlives the unmount.
 */
export function usePagination<T>({ queryKey, queryFn, enabled = true }: UsePaginationOptions<T>) {
  const queryClient = useQueryClient();
  const [pageIndex, setPageIndex] = useState(0);
  const [pageTokens, setPageTokens] = useState<(string | null)[]>([null]);

  const pageToken = pageTokens[pageIndex] ?? null;

  const { data, isPending, isFetching, error } = useQuery({
    queryKey: [...queryKey, pageToken],
    queryFn: () => queryFn(pageToken),
    enabled,
  });

  const goNext = useCallback(() => {
    const token = data?.nextPageToken;
    if (!token) return;
    setPageTokens((prev) => {
      const next = [...prev];
      next[pageIndex + 1] = token;
      return next;
    });
    setPageIndex(pageIndex + 1);
  }, [data?.nextPageToken, pageIndex]);

  const goPrev = useCallback(() => {
    setPageIndex((i) => (i > 0 ? i - 1 : i));
  }, []);

  // Prefix match, so every cached page of this filter set is refetched. Not
  // memoised: queryKey is a fresh array literal at every call site, so a
  // useCallback over it would need either a stringified dep or a disable
  // comment, and no caller memoises on refresh's identity.
  const refresh = () => queryClient.invalidateQueries({ queryKey });

  return {
    items: data?.items ?? [],
    totalCount: data?.totalCount ?? 0,
    // isPending covers the first load; isFetching keeps the indicator up while a
    // refresh replaces an already-rendered page.
    loading: isPending || isFetching,
    error: error ?? null,
    pageIndex,
    hasPrev: pageIndex > 0,
    hasNext: !!data?.nextPageToken,
    goNext,
    goPrev,
    refresh,
  };
}
