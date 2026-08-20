// Package identifier defines the instrument identification plugin API for PortfolioDB.
//
// # Canonical types
//
// [Instrument] holds security-master data (asset class, exchange, currency, etc.).
// [Identifier] is an opaque (Type, Domain, Value) triple. Type must be from
// [AllowedIdentifierTypes] (e.g. "MIC_TICKER", "OPENFIGI_TICKER", "ISIN", "CUSIP", "OCC"). Domain is
// optional context: ISO 10383 MIC for MIC_TICKER, Bloomberg exchange code for OPENFIGI_TICKER. Broker descriptions use
// Type = "BROKER_DESCRIPTION", Domain = source, Value = full instrument_description.
//
// [Hints] carries optional resolution context (currency, security type hint).
// Plugins may use hints to narrow API queries. Only what the upstream service
// confirmed is canonical, so a plugin must not return a hint value the service
// said nothing about.
//
// Confirming is not the same as returning. A provider that filters on a value
// and answers at all has confirmed it, whether or not the value appears in the
// response body -- OpenFIGI Mapping answers "No identifier found" for a currency
// the security has no listing in, and returns zero results for an identifier
// that did not match. A plugin may put such a value on the Instrument it
// returns, and must say at the site which request made it a confirmation, so
// that removing the filter cannot quietly turn a fact back into a guess.
//
// Where a provider returns the field, use what it returned and do not lean on
// the filter -- EODHD and Massive both return currency and exchange, and the
// EODHD plugin re-checks them against the response rather than trusting the
// query. OpenFIGI is the exception the rule is written for: it filters on the
// currency but does not return one.
//
// The resolver relies on this: it counts what a result confirmed when deciding
// whether to trust an identifier nobody stated. See adr/0059, which records what
// each provider was measured doing.
//
// # Plugin interface
//
// Implement [Plugin] (DisplayName, AcceptableSecurityTypes, Identify, DefaultConfig).
// Register the plugin at startup via [Registry.Register]. On first run the
// server calls DefaultConfig for each registered plugin that has no row in
// identifier_plugin_config and inserts the returned JSON with enabled=false and
// precedence assigned by registration order. The user edits config via the Admin
// UI.
//
// # Identify contract
//
// The caller invokes enabled plugins concurrently. Each plugin receives the same
// context (with a per-plugin timeout, default 30 s), config JSON, broker, source,
// instrumentDescription, [Hints], and identifier hints extracted by description
// plugins or supplied by the client.
//
// Return values:
//   - ([Result] with Instrument and Identifiers, nil) when resolved.
//   - ([Result], [ErrNotIdentified]) when the plugin cannot resolve. The caller
//     falls back to a broker-description-only instrument.
//   - ([Result], error) on transient failure (timeout, rate limit, API error).
//     The caller retries once with a 2 s backoff before recording the failure.
//
// # Telemetry
//
// Set [Result.Telemetry] on every return path, errors included. [Outcome] says
// how the call went on the wire; the caller composes it with what only it knows
// -- whether an identifying plugin won, was superseded by a better hint match,
// or was discarded as inconsistent with the winner -- into the row it writes.
// Do not count retries or elapsed time: the retry loop and the clock are the
// caller's. A plugin never writes telemetry itself and never depends on the
// telemetry backend.
//
// When multiple plugins succeed, the caller merges identifiers: for each
// identifier type the first occurrence (in precedence-descending order) wins.
// The highest-precedence plugin's Instrument data is used.
//
// # Identifier normalisation
//
// Plugins are responsible for normalising identifiers to the form required by
// their upstream service before making API calls. For example, OCC option
// symbols have two common forms (space-padded 21-char and compact). The Massive
// plugin converts to compact via derivative.OCCCompact; the OpenFIGI plugin
// converts to padded via derivative.OCCPadded. See server/derivative/parse.go
// for helpers.
//
// # Derivatives
//
// When the resolved instrument is a derivative (option, future), the plugin
// should populate Instrument.UnderlyingIdentifiers with identifier hints for
// the underlying. The resolution layer resolves the underlying through the
// full plugin pipeline using these hints.
//
// # Security type filtering
//
// AcceptableSecurityTypes returns the set of security type hints the plugin
// handles. The caller skips plugins whose acceptable set does not include the
// current hint. Return nil or an empty map to accept all types.
//
// # Adding a new plugin
//
//  1. Create server/plugins/<datasource>/identifier.
//  2. Implement [Plugin]. DefaultConfig returns JSON with the config keys the
//     plugin uses and dummy/empty values.
//  3. Register the plugin at startup (e.g. in main, registry.Register(pluginID, plugin)).
//  4. No migration or manual row is needed; the server creates the config row on first run.
package identifier
