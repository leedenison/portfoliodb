import { describe, expect, it, vi } from "vitest";
import { act, renderHook, waitFor } from "@testing-library/react";
import { usePagination, type PaginatedResult } from "./use-pagination";
import { queryWrapper } from "@/test-utils";

type Row = { id: string };

/**
 * A fetcher over fixed pages, keyed by the token that reaches it. Records every
 * call so tests can assert on how many requests a navigation actually made --
 * which is the point of most of these cases.
 */
function pagedFetcher(pages: PaginatedResult<Row>[]) {
  const calls: (string | null)[] = [];
  const byToken = new Map<string | null, PaginatedResult<Row>>();
  let token: string | null = null;
  for (const page of pages) {
    byToken.set(token, page);
    token = page.nextPageToken;
  }
  const fn = vi.fn(async (pageToken: string | null) => {
    calls.push(pageToken);
    const page = byToken.get(pageToken);
    if (!page) throw new Error(`no page for token ${String(pageToken)}`);
    return page;
  });
  return { fn, calls };
}

const THREE_PAGES: PaginatedResult<Row>[] = [
  { items: [{ id: "a" }], totalCount: 3, nextPageToken: "t1" },
  { items: [{ id: "b" }], totalCount: 3, nextPageToken: "t2" },
  { items: [{ id: "c" }], totalCount: 3, nextPageToken: null },
];

function render(fetchFn: (t: string | null) => Promise<PaginatedResult<Row>>, queryKey: readonly unknown[] = ["rows"]) {
  return renderHook(() => usePagination<Row>({ queryKey, queryFn: fetchFn }), {
    wrapper: queryWrapper(),
  });
}

describe("usePagination", () => {
  it("loads the first page with a null token", async () => {
    const { fn, calls } = pagedFetcher(THREE_PAGES);
    const { result } = render(fn);

    await waitFor(() => expect(result.current.loading).toBe(false));

    expect(result.current.items).toEqual([{ id: "a" }]);
    expect(result.current.totalCount).toBe(3);
    expect(result.current.pageIndex).toBe(0);
    expect(result.current.hasPrev).toBe(false);
    expect(result.current.hasNext).toBe(true);
    expect(calls).toEqual([null]);
  });

  it("goNext fetches with the token the previous page returned", async () => {
    const { fn, calls } = pagedFetcher(THREE_PAGES);
    const { result } = render(fn);
    await waitFor(() => expect(result.current.loading).toBe(false));

    act(() => result.current.goNext());
    await waitFor(() => expect(result.current.items).toEqual([{ id: "b" }]));

    expect(result.current.pageIndex).toBe(1);
    expect(result.current.hasPrev).toBe(true);
    expect(calls).toEqual([null, "t1"]);
  });

  it("hasNext is false on the last page", async () => {
    const { fn } = pagedFetcher(THREE_PAGES);
    const { result } = render(fn);
    await waitFor(() => expect(result.current.loading).toBe(false));

    // Wait for each page to settle: goNext needs the current page's
    // nextPageToken, so it is a no-op while a fetch is still in flight.
    act(() => result.current.goNext());
    await waitFor(() => expect(result.current.items).toEqual([{ id: "b" }]));
    act(() => result.current.goNext());
    await waitFor(() => expect(result.current.items).toEqual([{ id: "c" }]));

    expect(result.current.pageIndex).toBe(2);
    expect(result.current.hasNext).toBe(false);
  });

  it("goPrev returns to a page already fetched without refetching it", async () => {
    const { fn, calls } = pagedFetcher(THREE_PAGES);
    const { result } = render(fn);
    await waitFor(() => expect(result.current.loading).toBe(false));

    act(() => result.current.goNext());
    await waitFor(() => expect(result.current.items).toEqual([{ id: "b" }]));

    act(() => result.current.goPrev());
    await waitFor(() => expect(result.current.items).toEqual([{ id: "a" }]));

    // Page 0 is served from cache: still just the two requests.
    expect(calls).toEqual([null, "t1"]);
    expect(result.current.pageIndex).toBe(0);
  });

  it("starts at page 0 with a single request when remounted under a new key", async () => {
    // The key-prop reset path: a filter change remounts the component that owns
    // this hook. Regression test for the old hook, which fired two requests --
    // one for the stale pageIndex, then another after resetting to page 0.
    const { fn, calls } = pagedFetcher(THREE_PAGES);
    const first = render(fn, ["rows", "search-a"]);
    await waitFor(() => expect(first.result.current.loading).toBe(false));

    act(() => first.result.current.goNext());
    await waitFor(() => expect(first.result.current.pageIndex).toBe(1));
    first.unmount();
    calls.length = 0;

    const second = render(fn, ["rows", "search-b"]);
    await waitFor(() => expect(second.result.current.loading).toBe(false));

    expect(second.result.current.pageIndex).toBe(0);
    expect(second.result.current.items).toEqual([{ id: "a" }]);
    expect(calls).toEqual([null]);
  });

  it("refresh refetches with no other render trigger", async () => {
    // Regression test for the old hook, where refresh() bumped a ref. Mutating a
    // ref does not re-render, so it only ever worked when something else
    // happened to re-render the component.
    const { fn, calls } = pagedFetcher(THREE_PAGES);
    const { result } = render(fn);
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(calls).toEqual([null]);

    await act(async () => {
      await result.current.refresh();
    });

    await waitFor(() => expect(calls).toEqual([null, null]));
  });

  it("surfaces a rejection and leaves items empty", async () => {
    const fn = vi.fn(async () => {
      throw new Error("boom");
    });
    const { result } = render(fn);

    await waitFor(() => expect(result.current.error).not.toBeNull());

    expect(result.current.error?.message).toBe("boom");
    expect(result.current.items).toEqual([]);
    expect(result.current.totalCount).toBe(0);
  });

  it("does not fetch while disabled", async () => {
    const { fn, calls } = pagedFetcher(THREE_PAGES);
    const { result } = renderHook(
      () => usePagination<Row>({ queryKey: ["rows"], queryFn: fn, enabled: false }),
      { wrapper: queryWrapper() }
    );

    expect(calls).toEqual([]);
    expect(result.current.items).toEqual([]);
  });
});
