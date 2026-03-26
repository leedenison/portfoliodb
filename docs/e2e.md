# E2E Testing

End-to-end tests exercise the full stack (Next.js, Envoy, Go service, Postgres, Redis) using Playwright inside Docker containers.

## Key Design Decisions

### VCR-based HTTP mocking

External plugin HTTP calls (OpenAI, OpenFIGI, EODHD, Massive) are recorded and replayed using [go-vcr](https://github.com/dnaeon/go-vcr) v4. The Go server is built with `-tags e2e` which swaps real HTTP clients for VCR-backed transports.

- **Replay mode** (default): responses are played back from cassette files. No API keys needed, fast execution.
- **Record mode** (`VCR_MODE=record`): real HTTP requests are made and saved. Requires API keys in the environment.

Cassettes are YAML files in `e2e/cassettes/`. A `BeforeSaveHook` redacts API keys and sensitive headers before cassettes are committed.

### Per-suite cassette isolation

Each test suite loads its own cassette via the `E2eService` gRPC endpoint (defined in `proto/e2e/v1/e2e.proto`, only registered in e2e builds). Playwright calls `loadCassette("suite-name")` in `beforeAll` and `unloadCassette()` in `afterAll`.

This allows individual suites to be run and recorded in isolation. Suites that make no plugin HTTP calls (e.g. auth-flows) do not load a cassette; any unexpected plugin call hits a nil transport and fails immediately.

### Docker-based isolated environment

The E2E stack (`docker-compose.e2e.yml`) runs on isolated ports to avoid conflicts with development containers:

| Service | E2E Port | Dev Port |
|---------|----------|----------|
| Postgres | 5434 | 5432 |
| Redis | 6381 | 6379 |
| Envoy | 8081 | 8080 |
| gRPC | 50052 | 50051 |

### Serial execution

Playwright runs with `workers: 1`. Tests may share database state within a run and each spec file seeds its own data via the `db.ts` helper (`resetAndSeedBase()`).

### Fixture seeding

- `e2e/fixtures/seed.sql` -- base users and test portfolio
- `e2e/fixtures/instruments.sql` -- pre-identified instruments with prices (for price/performance tests)
- `e2e/fixtures/standard-3-stocks.csv` -- CSV upload fixture
- `e2e/fixtures/bad-format.csv` -- malformed CSV for error testing

The `db.ts` helper provides `resetAndSeedBase()` (truncates user data, loads seed, configures plugins) and `seedFixture(name)` for additional SQL fixtures.

### DOM selectors

E2E tests use `data-testid` attributes for stable element selection. These do not break with styling or structural changes. See the frontend skill (`.claude/skills/frontend-design/SKILL.md`) for guidance on adding `data-testid` to new UI elements.

## Running E2E tests

Generated protobuf bindings (`e2e/gen/`, `proto/**/*.pb.go`) are gitignored. Run `make generate` before the first `make e2e-test` in a fresh checkout or worktree.

```bash
# Replay mode (default, no API keys needed)
make e2e-test

# Record mode (requires API keys in environment)
make e2e-record

# Run a single suite in isolation (inside the Playwright container)
npx playwright test tests/auth-flows.spec.ts
```
