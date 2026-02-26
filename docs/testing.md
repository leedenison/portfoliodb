# Testing Guidance

## Unit Testing

Unit tests should use mocks (either using a mocking library or as ephemeral structs) to limit dependencies on code that is not under test (except in the case of the database abstraction layer, see below).  Unit tests should focus on the behaviour of the code under test, not the behaviour of dependencies.

### Mocks

Prefer **gomock** for mocking interfaces (e.g. `db.DB`). Generate mocks via `go generate` (see `server/db/db.go` for the directive); do not maintain large hand-written mocks. Each test should set only the expectations it needs (e.g. `db.EXPECT().GetPortfolio(...).Return(...)`). The same generated mock can be reused across packages that depend on the interface.

Generated mocks follow the naming convention **`*_mock.go`** and are not checked in (they are ignored via the pattern `**/*_mock.go` in `.gitignore`). Run `make test` or `go generate ./server/db` (or the relevant package) to generate mocks before running tests.

### Verbosity

- Use **table-driven tests** when the same pattern repeats (e.g. unauthenticated, invalid argument, or not-found across several RPCs). One test with a slice of cases and `t.Run(tc.name, ...)` keeps coverage high without repetition.
- Use small **assertion helpers** (e.g. `requireGRPCCode(t, err, codes.NotFound)`) and **context helpers** (e.g. `authCtx(userID, authSub)`) instead of repeating setup and error checks. Helpers should call `t.Helper()` so failures report the correct line in the test.

### Database layer

Unit tests for the database abstraction layer should require a running postgres instance with an initialised datamodel.  These should be executed separately from regular unit tests via a make command.  The make command should create a docker test environment with a running postgres instance so that the tests can be run.  The datamodel should be reset after every test by rolling back a transaction.