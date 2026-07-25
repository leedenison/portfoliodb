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

## Next.js / React

- App Router (`client/app/`). Default to client components: interactive files
  declare `"use client"`. Keep server components (like `app/layout.tsx`) for
  metadata/layout only.
- Shared state uses React hooks plus Context (`client/contexts/*`); each context
  exposes a `useX()` hook that throws when used outside its provider. There is no
  Redux/Zustand and no React Query/SWR.
- Styling is Tailwind (`^3.4`) with CSS-variable semantic tokens (`text-text-muted`,
  `bg-accent-soft/50`); `darkMode: 'media'`.
- Component filenames are kebab-case (`app-shell.tsx`). Add `data-testid`
  attributes to key elements for stable e2e/unit selection.

## Data fetching

Call the backend through the `client/lib/*-api.ts` wrapper modules, which build
requests with `create/toBinary`, send them via the hand-written gRPC-Web transport
in `client/lib/grpc-web.ts` (`unaryFetch` / `streamingFetch`), and decode with
`fromBinary`. Handle `GrpcError` / `SessionLostError`; a lost session triggers
`notifySessionLost()`. Fetch in `useEffect` with manual loading/error state; use
`client/hooks/use-pagination.ts` for token pagination.

## Strictness and idioms

- `tsconfig.json` sets `strict: true`, `moduleResolution: "bundler"`, and the `@/*`
  path alias. Respect strict null/any checks; avoid `any`.
- Use `import type` for type-only imports (required under `isolatedModules`).

## Lint and format

Lint with `next lint` (`eslint-config-next`); there is no standalone eslint config.
There is no Prettier config -- follow the observed style: 2-space indent, double
quotes, semicolons, and trailing commas.

## Testing

Client tests run on Vitest 4 with `@testing-library/react` (`client/vitest.config.ts`,
`vitest.setup.ts`). Colocate `*.test.ts(x)` files; mock the transport with
`vi.mock("./grpc-web")` and assert on decoded protobuf results. See the
`unit-testing` skill.
