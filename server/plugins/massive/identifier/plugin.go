package identifier

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/leedenison/portfoliodb/server/clock"
	"github.com/leedenison/portfoliodb/server/derivative"
	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/leedenison/portfoliodb/server/plugins/massive/client"
)

// PluginID is the stable plugin_id for registration and identifier_plugin_config.
const PluginID = "massive"

// defaultExpiredDerivativeHorizon is the default number of days after which an
// expired derivative is skipped without hitting the API.
const defaultExpiredDerivativeHorizon = 180

type configJSON struct {
	MassiveAPIKey            string `json:"massive_api_key"`
	MassiveBaseURL           string `json:"massive_base_url"`           // for testing
	CallsPerMin              *int   `json:"massive_calls_per_min"`      // nil or absent = unlimited
	ExpiredDerivativeHorizon *int   `json:"expired_derivative_horizon"` // days; nil = default (180)
}

// Plugin implements identifier.Plugin using the Massive REST API.
// The client and rate limiter are shared across concurrent Identify calls
// and rebuilt only when the config JSON changes.
type Plugin struct {
	log        *slog.Logger
	httpClient *http.Client
	timer      *clock.Timer

	mu            sync.Mutex
	client        *client.Client
	lastConfig    string // raw config JSON used to detect changes
	expiryHorizon time.Duration
}

// NewPlugin returns a plugin. log and timer are optional (nil for tests). A nil
// timer delegates to time.Now().
func NewPlugin(log *slog.Logger, httpClient *http.Client, timer *clock.Timer) *Plugin {
	return &Plugin{log: log, httpClient: httpClient, timer: timer}
}

func (p *Plugin) DisplayName() string { return "Massive" }

func (p *Plugin) DefaultConfig() []byte {
	horizon := defaultExpiredDerivativeHorizon
	cfg := configJSON{ExpiredDerivativeHorizon: &horizon}
	out, _ := json.Marshal(cfg)
	return out
}

func (p *Plugin) AcceptableInstrumentKinds() map[string]bool {
	return map[string]bool{identifier.InstrumentKindSecurity: true}
}

func (p *Plugin) AcceptableSecurityTypes() map[string]bool {
	return map[string]bool{
		identifier.SecurityTypeHintStock:  true,
		identifier.SecurityTypeHintOption: true,
	}
}

func (p *Plugin) Identify(ctx context.Context, config []byte, broker, source, instrumentDescription string, hints identifier.Hints, identifierHints []identifier.Identifier) (identifier.Result, error) {
	if len(identifierHints) == 0 {
		return result(nil, nil, identifier.ErrNotIdentified)
	}

	c, err := p.getClient(config)
	if err != nil {
		return result(nil, nil, err)
	}

	var inst *identifier.Instrument
	var ids []identifier.Identifier
	switch hints.SecurityTypeHint {
	case identifier.SecurityTypeHintOption:
		inst, ids, err = p.identifyOption(ctx, c, identifierHints)
	default:
		inst, ids, err = p.identifyStock(ctx, c, identifierHints)
	}

	return result(inst, ids, err)
}

// errExpiredSkipped is returned when an expired option is skipped without an
// API call. It wraps ErrNotIdentified so the resolver treats it as any other
// non-identification, while the outcome the plugin reports says why.
var errExpiredSkipped = fmt.Errorf("expired derivative beyond horizon: %w", identifier.ErrNotIdentified)

// result pairs the identification with the outcome the resolver records for
// this call.
func result(inst *identifier.Instrument, ids []identifier.Identifier, err error) (identifier.Result, error) {
	return identifier.Result{
		Instrument:  inst,
		Identifiers: ids,
		Telemetry:   identifier.Telemetry{Outcome: outcome(err)},
	}, err
}

// outcome classifies an Identify error into the vocabulary the resolver records.
func outcome(err error) identifier.Outcome {
	switch {
	case err == nil:
		return identifier.OutcomeIdentified
	case errors.Is(err, errExpiredSkipped):
		return identifier.OutcomeSkippedExpired
	case errors.Is(err, identifier.ErrNotIdentified):
		return identifier.OutcomeNotIdentified
	case errors.Is(err, context.DeadlineExceeded):
		return identifier.OutcomeTimeout
	default:
		var rl *client.ErrRateLimit
		if errors.As(err, &rl) {
			return identifier.OutcomeRateLimited
		}
		return identifier.OutcomeError
	}
}

// getClient returns the shared client, rebuilding it only when config changes.
func (p *Plugin) getClient(config []byte) (*client.Client, error) {
	raw := string(config)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.client != nil && p.lastConfig == raw {
		return p.client, nil
	}
	var cfg configJSON
	if len(config) > 0 {
		if err := json.Unmarshal(config, &cfg); err != nil {
			return nil, err
		}
	}
	perMin := 0
	if cfg.CallsPerMin != nil {
		perMin = *cfg.CallsPerMin
	}
	horizon := defaultExpiredDerivativeHorizon
	if cfg.ExpiredDerivativeHorizon != nil {
		horizon = *cfg.ExpiredDerivativeHorizon
	}
	limiter := client.NewRateLimiter(perMin)
	p.client = client.New(cfg.MassiveAPIKey, cfg.MassiveBaseURL, limiter, p.log, p.httpClient)
	p.expiryHorizon = time.Duration(horizon) * 24 * time.Hour
	p.lastConfig = raw
	return p.client, nil
}

// identifyStock looks up a stock via MIC_TICKER/OPENFIGI_TICKER hint and the ticker overview API.
func (p *Plugin) identifyStock(ctx context.Context, c *client.Client, hints []identifier.Identifier) (*identifier.Instrument, []identifier.Identifier, error) {
	ticker := findHint(hints, "MIC_TICKER")
	if ticker == "" {
		ticker = findHint(hints, "OPENFIGI_TICKER")
	}
	if ticker == "" {
		return nil, nil, identifier.ErrNotIdentified
	}
	ticker = identifier.NormalizeSplitTicker(ticker, ".")
	overview, err := c.TickerOverview(ctx, ticker)
	if err != nil {
		var nf *client.ErrNotFound
		if errors.As(err, &nf) {
			return nil, nil, identifier.ErrNotIdentified
		}
		return nil, nil, err
	}
	inst, ids := stockFromTicker(overview)
	if inst == nil {
		return nil, nil, identifier.ErrNotIdentified
	}
	return inst, ids, nil
}

// identifyOption looks up an option via OCC hint, falling back to TICKER.
// Options whose expiry (from the OCC symbol) is older than expiryHorizon are
// skipped without an API call.
func (p *Plugin) identifyOption(ctx context.Context, c *client.Client, hints []identifier.Identifier) (*identifier.Instrument, []identifier.Identifier, error) {
	raw := findHint(hints, "OCC")
	if raw == "" {
		return nil, nil, identifier.ErrNotIdentified
	}
	compact, ok := derivative.OCCCompact(raw)
	if !ok {
		return nil, nil, identifier.ErrNotIdentified
	}
	if p.expiryHorizon > 0 {
		if expiry, ok := derivative.OCCExpiry(compact); ok {
			if p.timer.Now().Sub(expiry) > p.expiryHorizon {
				if p.log != nil {
					p.log.InfoContext(ctx, "massive: skipping expired option beyond horizon",
						"occ", compact, "expiry", expiry.Format("2006-01-02"),
						"horizon_days", int(p.expiryHorizon.Hours()/24))
				}
				return nil, nil, errExpiredSkipped
			}
		}
	}
	return p.identifyOptionByOCC(ctx, c, "O:"+compact)
}

// identifyOptionByOCC calls the options contract API and returns the option instrument
// with UnderlyingIdentifiers for the resolution layer to resolve.
func (p *Plugin) identifyOptionByOCC(ctx context.Context, c *client.Client, occ string) (*identifier.Instrument, []identifier.Identifier, error) {
	contract, err := c.OptionsContract(ctx, occ)
	if err != nil {
		var nf *client.ErrNotFound
		if errors.As(err, &nf) {
			return nil, nil, identifier.ErrNotIdentified
		}
		return nil, nil, err
	}
	if contract.UnderlyingTicker == "" {
		if p.log != nil {
			p.log.WarnContext(ctx, "massive: option contract has no underlying_ticker", "occ", occ)
		}
		return nil, nil, identifier.ErrNotIdentified
	}
	inst, ids := optionFromContract(contract)
	return inst, ids, nil
}

// findHint returns the Value of the first hint with the given Type, or "".
func findHint(hints []identifier.Identifier, typ string) string {
	for _, h := range hints {
		if h.Type == typ && h.Value != "" {
			return h.Value
		}
	}
	return ""
}
