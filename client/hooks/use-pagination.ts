"use client";

import { useCallback, useEffect, useRef, useState } from "react";

export type PaginatedResult<T> = {
  items: T[];
  totalCount: number;
  nextPageToken: string | null;
};

/**
 * Manages token-based pagination state. The caller provides a fetch function
 * that accepts a page token and returns a page of results. The hook tracks
 * page tokens, loading/error state, and exposes navigation helpers.
 *
 * `fetchFn` should be wrapped in useCallback by the caller so that changes
 * to filter/search params naturally trigger a re-fetch via the dependency
 * array. When fetchFn identity changes the hook resets to page 0.
 */
export function usePagination<T>(fetchFn: (pageToken: string | null) => Promise<PaginatedResult<T>>) {
  const [items, setItems] = useState<T[]>([]);
  const [totalCount, setTotalCount] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [nextPageToken, setNextPageToken] = useState<string | null>(null);
  const [pageTokens, setPageTokens] = useState<(string | null)[]>([null]);
  const [pageIndex, setPageIndex] = useState(0);
  const refreshRef = useRef(0);

  // Reset to page 0 when fetchFn identity changes (e.g. filters changed).
  const fetchFnRef = useRef(fetchFn);
  useEffect(() => {
    if (fetchFnRef.current !== fetchFn) {
      fetchFnRef.current = fetchFn;
      setPageIndex(0);
      setPageTokens([null]);
    }
  }, [fetchFn]);

  const fetchPage = useCallback(
    async (pageToken: string | null, forPageIndex: number) => {
      setLoading(true);
      setError(null);
      try {
        const result = await fetchFn(pageToken);
        setItems(result.items);
        setTotalCount(result.totalCount);
        setNextPageToken(result.nextPageToken);
        if (result.nextPageToken) {
          setPageTokens((prev) => {
            const next = [...prev];
            next[forPageIndex + 1] = result.nextPageToken!;
            return next;
          });
        }
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
        setItems([]);
        setTotalCount(0);
      } finally {
        setLoading(false);
      }
    },
    [fetchFn]
  );

  useEffect(() => {
    const token = pageTokens[pageIndex] ?? null;
    fetchPage(token, pageIndex);
  }, [pageIndex, fetchPage, refreshRef.current]); // eslint-disable-line react-hooks/exhaustive-deps

  const goNext = useCallback(() => {
    if (nextPageToken) setPageIndex((i) => i + 1);
  }, [nextPageToken]);

  const goPrev = useCallback(() => {
    if (pageIndex > 0) setPageIndex((i) => i - 1);
  }, [pageIndex]);

  const reset = useCallback(() => {
    setPageIndex(0);
    setPageTokens([null]);
  }, []);

  const refresh = useCallback(() => {
    refreshRef.current += 1;
  }, []);

  return {
    items,
    totalCount,
    loading,
    error,
    pageIndex,
    hasPrev: pageIndex > 0,
    hasNext: !!nextPageToken,
    goNext,
    goPrev,
    reset,
    refresh,
  };
}
