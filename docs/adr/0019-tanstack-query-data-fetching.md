# Client data fetching: TanStack Query

Every page and most components used to load data the same way: a `useEffect`
calling a loader that set `loading`, `error` and the data itself. Repeated across
twenty-odd sites it drifted -- some cancelled with a `let cancelled` closure and
some did not, three different refresh mechanisms grew up alongside each other,
and two of them did not work (a `useRef` bumped to force a refetch does not
re-render, and a filter change while off page one fired two requests). It also
cannot satisfy `react-hooks/set-state-in-effect`, which the React team's
recommended lint config now enables.

We use TanStack Query for all client reads. Queries are keyed on the resource and
its parameters, so a filter or portfolio change refetches by changing the key
rather than by an effect, mutations refresh by invalidating a key prefix instead
of calling a loader back, and polling is `refetchInterval` rather than a
`setInterval` the component has to tear down. Keys hold primitives only: they are
compared structurally, so passing an object that is rebuilt with equal contents
silently refetches.

The defaults in `client/lib/query-client.ts` deliberately reproduce the old
behaviour rather than taking the library's: `retry: 0` because nothing retried
before, because this backend's `INVALID_ARGUMENT` and `UNAUTHENTICATED` will not
succeed on a second attempt, and because retries would make the e2e suites issue
requests their VCR cassettes have no entry for; `refetchOnWindowFocus` and
`refetchOnReconnect` off because background refetching is new behaviour nobody
asked for; and `staleTime` 0, because price fetching, corporate event discovery
and split adjustment all change data behind the client's back, so treating a
cached read as fresh for any window risks showing a stale table just after a
worker has run. Mounting still paints the cached value and revalidates behind
it, so nothing returns to a blank loading state.

We did not add request cancellation. The old `cancelled` flags did not abort the
HTTP request either -- they suppressed the `setState` -- which is what the query
cache does structurally for every query, so dropping them changes nothing.
Out-of-order responses, the bug the flags guarded against, are prevented by the
key instead: two searches are two cache entries and a late reply cannot overwrite
the current one. That leaves the gRPC-Web transport and the `client/lib/*-api.ts`
wrappers untouched, which is worth more than a cancellation nothing needs yet.

The session itself is a query, so `enabled` can gate on it; sign-out drops every
non-session query, because a cache that outlives the session would otherwise show
one user's data to the next.
