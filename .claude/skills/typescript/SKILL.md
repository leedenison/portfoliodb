---
name: typescript
description: TypeScript and Next.js conventions for the PortfolioDB client (client/) -- using generated protobuf-es types, App Router/React patterns, TS strictness, gRPC-Web data fetching, and lint/format. Use when writing or reviewing client-side TypeScript.
---

# TypeScript Style

Conventions for the Next.js (TypeScript) front end in `client/`. Prefer idiomatic,
modern TypeScript. For component design and aesthetics see the `frontend-design`
skill; for client tests see the `unit-testing` skill.

## Generated protobuf types

Backend types come from generated protobuf-es v2 code (`@bufbuild/protobuf`) under
`client/gen`; regenerate with `make generate`.

- Import from the `@/gen/...` path alias, e.g.
  `import { AssetClass, PortfolioSchema } from "@/gen/api/v1/api_pb"`.
- Import enum values and `*Schema` descriptors as normal imports; import message
  **types** with `import type`.
- Build/serialize with `create(Schema, {...})`, `toBinary(Schema, msg)`,
  `fromBinary(Schema, bytes)`. Use WKT helpers (`timestampDate`,
  `timestampFromDate`) from `@bufbuild/protobuf/wkt`.
- protobuf-es strips the enum-name prefix: proto `ASSET_CLASS_STOCK` becomes
  `AssetClass.STOCK`, and `*_UNSPECIFIED` becomes `.UNSPECIFIED`. Enum label maps
  and string<->enum converters live in `client/lib/` (e.g. `asset-class.ts`).

## Decimal fields

Quantities, prices and money arrive as `string`, not `number` -- a TypeScript
`number` is a float64 and cannot hold them exactly. See the `protobuf` skill and
adr/0027-decimal-values-cross-the-wire-as-strings.md.

- **Display: render the string.** Do not round-trip through `Number` to format
  it. The `parseFloat(x.toFixed(n))` idiom exists only to hide float artifacts
  and is unnecessary on a value that never had any.
- **Arithmetic: only in the converters.** Code under `client/lib/csv/` and
  `client/lib/ofx/` authors facts -- deriving counter-legs, splitting netted
  totals -- and uses a decimal library so the postings it emits balance exactly.
  Nothing else on the client computes with these values. Keep the dependency out
  of component code; the extension shares these modules and carries the bundle
  cost. The library is picked in 0042.
- **Charts take `number`.** Series values (`ValuationPoint.total_value` and any
  later return metric) are `double` on the wire and feed Recharts directly.
- Sorting and comparison need a numeric or decimal comparator; lexicographic
  string ordering is wrong for these fields.

## Next.js / React

- App Router (`client/app/`). Default to client components: interactive files
  declare `"use client"`. Keep server components (like `app/layout.tsx`) for
  metadata/layout only.
- Shared state uses React hooks plus Context (`client/contexts/*`); each context
  exposes a `useX()` hook that throws when used outside its provider. There is no
  Redux/Zustand. Server state is TanStack Query, not context -- see Data fetching.
- Styling is Tailwind (`^3.4`) with CSS-variable semantic tokens (`text-text-muted`,
  `bg-accent-soft/50`); `darkMode: 'media'`.
- Component filenames are kebab-case (`app-shell.tsx`). Add `data-testid`
  attributes to key elements for stable e2e/unit selection.

## Data fetching

Call the backend through the `client/lib/*-api.ts` wrapper modules, which build
requests with `create/toBinary`, send them via the hand-written gRPC-Web transport
in `client/lib/grpc-web.ts` (`unaryFetch` / `streamingFetch`), and decode with
`fromBinary`. Those throw `GrpcError` / `SessionLostError`; a lost session
triggers `notifySessionLost()` from inside the transport, so no call site handles
it.

Reads go through TanStack Query -- **never fetch in a `useEffect`** (see
docs/adr/0019). Use `useAuthedQuery` from `client/hooks/use-authed-query.ts`,
which gates on the session being resolved, and take keys from the `qk` factories
in `client/lib/query-keys.ts`. Keys hold primitives only: they are compared
structurally, so an object rebuilt with equal contents is a new key and silently
refetches -- pass `portfolio.id`, not `portfolio`. Render errors with
`errorMessage()` from `client/lib/errors.ts`.

Refresh after a mutation with `queryClient.invalidateQueries({ queryKey })`,
which prefix-matches. Poll with `refetchInterval`, not `setInterval`. Use
`client/hooks/use-pagination.ts` for token pagination; it does not reset itself,
so give the paginated child a `key` built from the filter values and let the
remount clear the page.

The same `key` trick is how to reset any state when an input changes -- both
`react-hooks/set-state-in-effect` and `set-state-in-render` are on at error, so
neither an effect nor a guarded render-time `setState` is available. Derive
during render where you can, remount where you cannot.

## Strictness and idioms

- `tsconfig.json` sets `strict: true`, `moduleResolution: "bundler"`, and the `@/*`
  path alias. Respect strict null/any checks; avoid `any`.
- Use `import type` for type-only imports (required under `isolatedModules`).

## Lint and format

Lint with `make lint-ts` (`make lint-ts-fix` to autofix) and typecheck with
`make client-typecheck` / `make extension-typecheck`. All three gate CI.

`eslint.config.mjs` sits at the repo root, not in either project, and covers
`client/` and `extension/` together -- the extension imports client modules
through a path alias, so the rules applying to shared source cannot be allowed
to drift. Rules are typescript-eslint's non-type-aware `recommended` for both
trees, plus react, react-hooks and `@next/next` core-web-vitals for the client
only.

There is no Prettier config -- follow the observed style: 2-space indent, double
quotes, semicolons, and trailing commas.

## Testing

Client tests run on Vitest 4 with `@testing-library/react` (`client/vitest.config.ts`,
`vitest.setup.ts`). Colocate `*.test.ts(x)` files; mock the transport with
`vi.mock("./grpc-web")` and assert on decoded protobuf results. See the
`unit-testing` skill.
