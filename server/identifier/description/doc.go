// Package description defines the description extraction plugin API for PortfolioDB.
//
// Description plugins extract identifier hints from raw broker instrument
// descriptions. They sit upstream of identifier plugins in the resolution
// pipeline: the caller runs description plugins first, then passes the
// extracted hints to identifier plugins for canonical resolution.
//
// # Plugin interface
//
// Implement [Plugin] (DisplayName, AcceptableSecurityTypes, ExtractBatch,
// DefaultConfig). Register the plugin at startup via [Registry.Register]. On
// first run the server calls DefaultConfig for each registered plugin that has
// no row in description_plugin_config and inserts the returned JSON with
// enabled=false and precedence assigned by registration order. The user edits
// config via the Admin UI.
//
// # ExtractBatch contract
//
// The caller invokes enabled plugins in series by descending precedence. The
// first plugin that returns at least one hint for any item wins; remaining
// plugins are not called. Each plugin receives config JSON, broker, source, and
// a slice of [BatchItem] (one per instrument description to extract).
//
// Return values:
//   - (map[BatchItem.ID][]Identifier, nil) on success. The map is keyed by
//     [BatchItem.ID]; items with no extractable hints may be absent or have an
//     empty slice.
//   - (nil, nil) or an empty map when nothing could be extracted.
//   - (nil, error) on failure (API error, timeout, etc.). The caller logs the
//     error, increments a plugin_error counter, and tries the next plugin.
//
// Returned identifiers must have a Type from [identifier.AllowedIdentifierTypes];
// the caller filters out invalid types at debug log level.
//
// # Differences from identifier plugins
//
// Description plugins extract hints; they do not produce canonical instrument
// data. They must not access the database. They support native batching via
// ExtractBatch (identifier plugins process one item at a time). Description
// plugins are called in series (first wins); identifier plugins are called
// concurrently with results merged by precedence.
//
// # Security type filtering
//
// AcceptableSecurityTypes returns the set of security type hints the plugin
// handles. Before calling ExtractBatch the caller filters the batch to only
// include items whose security type hint is in the acceptable set. Return nil
// or an empty map to accept all types.
//
// # Adding a new plugin
//
//  1. Create server/plugins/<datasource>/description.
//  2. Implement [Plugin]. DefaultConfig returns JSON with the config keys the
//     plugin uses and dummy/empty values.
//  3. Register the plugin at startup (e.g. in main, descRegistry.Register(pluginID, plugin)).
//  4. No migration or manual row is needed; the server creates the config row on first run.
package description
