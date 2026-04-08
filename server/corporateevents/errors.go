package corporateevents

import "errors"

// ErrTransient indicates a recoverable failure (network error, rate limit,
// 5xx). The orchestrator leaves the missing interval untouched so the next
// cycle retries. Use this rather than returning a nil result + nil error.
type ErrTransient struct{ Reason string }

func (e *ErrTransient) Error() string { return "transient: " + e.Reason }

// ErrPermanent indicates a permanent failure for an (instrument, plugin)
// pair (e.g. HTTP 403, 404). The orchestrator creates a fetch block so this
// combination is never retried.
type ErrPermanent struct{ Reason string }

func (e *ErrPermanent) Error() string { return "permanent: " + e.Reason }

// ErrNoData indicates the plugin cannot answer for this instrument at all
// (e.g. unsupported identifier domain, instrument outside coverage). The
// orchestrator tries the next plugin in precedence order without recording
// coverage. Distinct from a successful empty fetch, which DOES record
// coverage and stops the precedence walk.
var ErrNoData = errors.New("no corporate event data available")
