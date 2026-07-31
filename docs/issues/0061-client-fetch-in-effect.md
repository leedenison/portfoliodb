---
status: open
title: Stop loading client data with setState inside an effect
dependencies: [0060]
---

`react-hooks/set-state-in-effect` and `react-hooks/refs` are switched off in
`eslint.config.mjs`. Turning them on is the work.

## Motivation

Every page and most components load their data the same way: a `useEffect` that
calls a fetch helper which then calls `setState`. The rules flag 22 sites for
`set-state-in-effect` and 2 for `refs`, spread across the admin pages, the
holdings and performance pages, the upload and portfolio modals, and
`hooks/use-pagination.ts`.

That is a cascading render per load, and it is why the paginating hooks reach
for a ref to force a refetch -- which is what `refs` flags in turn. Neither is
a lint fix: they are the same pattern repeated, and clearing them means
changing how the client loads data.

## Design

Pick one way to fetch and apply it everywhere, rather than unpicking the sites
one at a time. Whatever it is has to cover what the current pattern does: an
initial load, a refetch when a filter or the selected portfolio changes,
cancellation when the inputs change mid-flight, and the paging that
`use-pagination.ts` layers on top.

Re-enable both rules in `eslint.config.mjs` as the last step, so the config
stops carrying the exception.
