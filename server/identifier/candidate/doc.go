// Package candidate defines the candidate plugin API for PortfolioDB.
//
// Candidate plugins extract identifier hints from raw broker instrument
// descriptions. They sit upstream of identifier plugins in the resolution
// pipeline: the caller runs candidate plugins first, then passes the
// extracted hints to identifier plugins for canonical resolution.
//
// # Plugin interface
//
// Implement [Plugin] (DisplayName, AcceptableSecurityTypes, ProposeBatch,
// DefaultConfig). Register the plugin at startup via [Registry.Register]. On
// first run the server calls DefaultConfig for each registered plugin that has
// no row in candidate plugin config and inserts the returned JSON with
// enabled=false and precedence assigned by registration order. The user edits
// config via the Admin UI.
//
// # ProposeBatch contract
//
// The caller invokes enabled plugins in series by descending precedence. Each
// plugin sees only the items its predecessors failed to extract hints for, so
// a later plugin is called with the remainder rather than not at all. Each
// plugin receives config JSON, broker, source, and a slice of [BatchItem] (one
// per instrument description to extract).
//
// Return values:
//   - ([Result] with Hints, nil) on success. Hints is keyed by [BatchItem.ID];
//     items with no extractable hints may be absent or have an empty slice.
//   - ([Result] with empty Hints, nil) when nothing could be extracted.
//   - ([Result], error) on failure (API error, timeout, etc.). The caller logs
//     the error and tries the next plugin.
//
// # Telemetry
//
// Set [Result.Telemetry] on every return path, errors included. [Outcome] says
// how the call went, and [Telemetry.Tokens] carries what the call cost, which
// is what makes the cost of one import answerable. A failure the plugin
// absorbs -- returning no hints and no error -- must still be reported as the
// failure it was, or it is indistinguishable from finding nothing. Leave the
// batch size and the count of items with hints to the caller: it knows both. A
// plugin never writes telemetry itself and never depends on the telemetry
// backend.
//
// Returned identifiers must have a Type in the controlled vocabulary ([identifier.Known]);
// the caller filters out invalid types at debug log level.
//
// # Differences from identifier plugins
//
// Candidate plugins extract hints; they do not produce canonical instrument
// data. They must not access the database. They support native batching via
// ProposeBatch (identifier plugins process one item at a time). Description
// plugins are called in series, each on what the previous ones left
// unresolved; identifier plugins are called concurrently with results merged
// by precedence.
//
// # Security type filtering
//
// AcceptableSecurityTypes returns the set of security type hints the plugin
// handles. Before calling ProposeBatch the caller filters the batch to only
// include items whose security type hint is in the acceptable set. Return nil
// or an empty map to accept all types.
//
// # Adding a new plugin
//
//  1. Create server/plugins/<datasource>/description.
//  2. Implement [Plugin]. DefaultConfig returns JSON with the config keys the
//     plugin uses and dummy/empty values.
//  3. Register the plugin at startup (e.g. in main, candRegistry.Register(pluginID, plugin)).
//  4. No migration or manual row is needed; the server creates the config row on first run.
package candidate
