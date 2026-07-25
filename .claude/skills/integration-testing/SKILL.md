---
name: integration-testing
description: Conventions for PortfolioDB integration tests -- the database abstraction layer (run against real postgres) and plugin tests (run against recorded external HTTP via VCR cassettes). Use when adding or changing db-layer or plugin integration tests. For pure unit tests see unit-testing; for full-stack Playwright see e2e-testing.
---

# Integration Testing

Integration tests exercise code against real dependencies that unit tests mock out:
the database abstraction layer against a live postgres, and plugins against recorded
external HTTP. They run under their own make targets, separate from unit tests.

## Database abstraction layer

Unit tests for the database abstraction layer require a running postgres instance
with the datamodel loaded. Run them with:

- `make db-test` -- `go test -v ./server/db/postgres/...` inside an isolated docker
  compose stack that provides postgres; the stack is torn down afterwards.

The datamodel is reset after every test by rolling back a transaction, so tests do
not leak state into each other. Fixture helpers (`setupUser`, `setupInstrument`,
etc.) live alongside the db tests in `server/db/postgres/`.

## Plugin integration tests (VCR)

Plugin tests that call external services (OpenAI, OpenFIGI, EODHD, Massive) replay
recorded HTTP using VCR cassettes instead of hitting the network. They are gated
behind the `integration` build tag.

- `make integration-test` -- `go test -tags integration -v ./server/plugins/...`
  in replay mode (no API keys needed).
- `make integration-test-list` -- list available suites.
- `make integration-test-record VCR_SUITES=...` -- make real HTTP calls and
  re-record cassettes (requires API keys); sets `VCR_MODE`.

The VCR helper is `server/testutil/vcr/vcr.go`: `vcr.New(t, cassettePath, sanitize,
suite)` returns a recorder and `*http.Client`, defaults to replay-only unless the
suite is named in `VCR_MODE`, and stops the recorder via `t.Cleanup`. Cassettes are
YAML under `server/plugins/*/*/testdata/cassettes/`, named descriptively (e.g.
`stock_aapl.yaml`, `not_found.yaml`). Built-in sanitizers redact API keys, bearer
tokens, and cookies before cassettes are committed -- never commit a cassette
containing a live secret.

See also the `unit-testing` and `e2e-testing` skills.
