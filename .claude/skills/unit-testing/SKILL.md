---
name: unit-testing
description: Conventions for writing and running unit tests in PortfolioDB (Go backend and Next.js client). Use when adding or changing unit tests, mocking dependencies, or deciding how to structure test cases. For DB-layer and plugin tests see integration-testing; for full-stack Playwright see e2e-testing.
---

# Unit Testing

Unit tests focus on the behaviour of the code under test, not the behaviour of its
dependencies. Use mocks (a mocking library or ephemeral structs) to limit
dependencies on code that is not under test. The one exception is the database
abstraction layer, which is tested against a real postgres instance -- see the
`integration-testing` skill.

## Running

- `make server-test` -- Go unit tests (`go test ./server/...`).
- `make client-test` -- client unit tests (Vitest).
- `make test` -- runs the whole matrix (server, client, db, integration).

Run tests via the make targets, not `go test` / `docker` directly.

## Mocks

Prefer **gomock** (`go.uber.org/mock/gomock`) for mocking interfaces (e.g. `db.DB`).
Generate mocks via `go generate` -- the sole directive lives at `server/db/db.go`
and produces `server/db/mock/db_mock.go`. Do not maintain large hand-written mocks.

Generated mocks follow the `*_mock.go` naming convention and are not checked in
(ignored via `**/*_mock.go` in `.gitignore`). `make test` (or `go generate
./server/db`) regenerates them before running. Each test sets only the expectations
it needs, e.g. `db.EXPECT().GetPortfolio(...).Return(...)`. The same generated mock
is reused across packages that depend on the interface.

Go-specific gomock idioms (controller setup, `DoAndReturn`, matchers) live in the
`go` skill.

For the client, mock the gRPC-Web transport rather than the network:
`vi.mock("./grpc-web")` and assert on decoded protobuf results.

## Keeping tests terse

- Use **table-driven tests** when the same pattern repeats (e.g. unauthenticated,
  invalid argument, or not-found across several RPCs). One test with a slice of
  cases and `t.Run(tc.name, ...)` keeps coverage high without repetition.
- Use small **assertion helpers** (e.g. `testutil.RequireGRPCCode(t, err,
  codes.NotFound)` in `server/testutil/grpc.go`) and **context helpers** (e.g.
  `authCtx(userID)` wrapping `auth.WithUser`) instead of repeating setup and error
  checks. Helpers must call `t.Helper()` so failures report the caller's line.

## Naming and layout

- Go tests are colocated as `*_test.go`; generated mocks are `*_mock.go` (gitignored).
- Shared Go test helpers live under `server/testutil`.
- Client tests are colocated `*.test.ts(x)` (e.g. under `client/lib/`).
- Front-end elements carry `data-testid` attributes for stable selection (used by
  e2e tests too -- see the `frontend-design` skill).

## Coverage and CI

CI runs the make-target matrix; tests must pass before a PR is merged.
