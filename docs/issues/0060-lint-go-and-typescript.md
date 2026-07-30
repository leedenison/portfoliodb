---
status: open
title: Lint and typecheck Go and TypeScript in CI
dependencies: [0059]
---

There is no working linter for either language, and the client does not
typecheck.

## Motivation

Nothing currently reports a lint or type problem in either tree:

- **Go** -- no `.golangci.yml` and no linter in the toolchain.
- **Client** -- `npm run lint` is `next lint`, which newer Next.js removed. It
  fails with `Invalid project directory provided, no such directory:
  /app/client/lint`, so it has been silently doing nothing. There is no eslint
  config either.
- **Extension** -- has `typecheck` but no lint and no eslint config.
- **Client typecheck** -- there is no `typecheck` script at all, and
  `npx tsc --noEmit` currently reports 5 errors, all in
  `client/lib/csv/prices.test.ts`: a `create()` argument mismatch and four
  BigInt literals under an ES2019 target. Tests still run because vitest
  transpiles without typechecking, so the errors are invisible in normal use.

The style conventions the `go` and `typescript` skills describe are enforced by
review alone. A linter would carry the mechanical part of that.

## Design

Three separate pieces of work, in rough order of value:

1. **Fix the client typecheck.** Add a `typecheck` script, resolve the 5
   errors (the BigInt literals need the target raised or `BigInt(...)` calls),
   and gate it in CI. The extension already has the script and only needs the
   gate.
2. **Replace `next lint`.** Adopt eslint directly with a flat config, shared
   between `client/` and `extension/` since the extension imports client
   modules across two npm workspaces.
3. **Add golangci-lint** with a conservative starting set, once 0059 has
   formatted the tree -- otherwise the first run buries real findings under
   formatting noise.

Each gets a make target and a CI matrix entry, matching how the test targets
already work.
