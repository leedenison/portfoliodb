# Client data fetching: TanStack Query

We use TanStack Query for all client reads. Queries are keyed on the resource and
its parameters, so a filter or portfolio change refetches by changing the key
rather than by an effect, mutations refresh by invalidating a key prefix instead
of calling a loader back, and polling is `refetchInterval` rather than a
`setInterval` the component has to tear down. Keys hold primitives only: they are
compared structurally, so passing an object rebuilt with equal contents silently
refetches.

The hand-rolled alternative -- a `useEffect` calling a loader that sets `loading`,
`error` and the data -- does not survive repetition. Across twenty-odd sites it
drifts: cancellation applied in some places and not others, three refresh
mechanisms grown up alongside each other, two of them broken. It also cannot
satisfy `react-hooks/set-state-in-effect`, which the React team's recommended lint
config now enables.

The defaults in `client/lib/query-client.ts` deliberately reproduce the old
behaviour rather than taking the library's: `retry: 0` because this backend's
`INVALID_ARGUMENT` and `UNAUTHENTICATED` will not succeed on a second attempt, and
because retries would make the e2e suites issue requests their VCR cassettes have
no entry for; `refetchOnWindowFocus` and `refetchOnReconnect` off because
background refetching is new behaviour nobody asked for; and `staleTime` 0,
because price fetching, corporate event discovery and split adjustment all change
data behind the client's back, so treating a cached read as fresh for any window
risks showing a stale table just after a worker has run. Mounting still paints the
cached value and revalidates behind it, so nothing returns to a blank loading
state.

We did not add request cancellation. The old `cancelled` flags did not abort the
HTTP request either -- they suppressed the `setState` -- which is what the query
cache does structurally. Out-of-order responses, the bug they guarded against, are
prevented by the key instead: two searches are two cache entries and a late reply
cannot overwrite the current one. That leaves the gRPC-Web transport and the
`client/lib/*-api.ts` wrappers untouched.

The session itself is a query, so `enabled` can gate on it; sign-out drops every
non-session query, because a cache that outlives the session would otherwise show
one user's data to the next.
