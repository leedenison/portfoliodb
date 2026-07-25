---
name: go
description: Go style and conventions for the PortfolioDB backend (server/) -- formatting, error handling, logging/telemetry, naming/terseness, and Go-specific test idioms. Use when writing or reviewing Go code in this repo.
---

# Go Style

Prefer language-specific, idiomatic Go solutions. This covers Go-specific
conventions; for cross-cutting test guidance see the `unit-testing`,
`integration-testing`, and `e2e-testing` skills.

## Formatting

Go code follows the gofmt standard for layout. Always run gofmt on any Go code
before committing.

## Error handling

- At the service (gRPC) boundary, return gRPC status errors:
  `status.Error(codes.X, msg)` (or `status.Errorf`). Use the right code --
  `InvalidArgument`, `NotFound`, `Unauthenticated`, `PermissionDenied`,
  `FailedPrecondition`, `Unavailable`, `Internal`.
- In lower layers (db, plugins, clients), wrap errors with `%w` to preserve the
  chain: `fmt.Errorf("get portfolio: %w", err)`.

## Logging and telemetry

Use the standard library `log/slog` only (no zap/zerolog/logrus). Log through the
category-based handler in `server/logger`:

- `logger.WithCategory(ctx, "...")` attaches a `category` attribute.
- `LOG_LEVEL` is either a global level or a JSON map of path-prefix -> level (e.g.
  `{"server/plugins":"debug","default":"info"}`).
- gRPC requests are logged by the interceptor in `server/logger/interceptor.go`;
  avoid scattering ad-hoc `slog.Info` calls.

There is no OpenTelemetry/tracing in the codebase. Auth is propagated through
`context` via `auth.WithUser` / `auth.FromContext`.

## Naming

Prefer terse names for functions and variables. In particular, financial
transactions are named `Tx` (not `Transaction`); reserve `Transaction` for database
transactions. Use short receiver names and idiomatic interface names.

## Testing idioms

Go unit tests use the slice-of-test-cases (table-driven) approach:

```go
func TestAdd(t *testing.T) {
	tests := []struct {
		name string
		a, b int
		want int
	}{
		{name: "zero", a: 0, b: 0, want: 0},
		{name: "positive", a: 2, b: 3, want: 5},
		{name: "negative", a: -1, b: -2, want: -3},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Add(tc.a, tc.b)
			if got != tc.want {
				t.Fatalf("Add(%d, %d) = %d, want %d", tc.a, tc.b, got, tc.want)
			}
		})
	}
}
```

Mock interfaces with gomock (`go.uber.org/mock/gomock`). Construct a controller per
test and clean it up:

```go
ctrl := gomock.NewController(t)
t.Cleanup(ctrl.Finish)
db := mock.NewMockDB(ctrl)
db.EXPECT().
	CreateJob(gomock.Any(), gomock.AssignableToTypeOf(&Job{})).
	DoAndReturn(func(ctx context.Context, j *Job) (int64, error) { ... })
```

Use matchers (`gomock.Any`, `gomock.AssignableToTypeOf`) and `DoAndReturn` to
inspect arguments. Give shared assertion/context helpers a `t.Helper()` call; the
canonical gRPC assertion is `testutil.RequireGRPCCode(t, err, codes.X)`. See the
`unit-testing` skill for the rest.

## Linting

gofmt and `go vet` are the enforced baseline today. (golangci-lint is the intended
linter but is not yet configured in the repo -- there is no `.golangci.yml` and no
`make lint` target. Do not assume it runs in CI.)
