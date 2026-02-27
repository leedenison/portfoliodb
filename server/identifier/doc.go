// Package identifier defines the instrument identification plugin API for PortfolioDB.
//
// # Canonical types
//
// Instrument holds security-master data (asset class, exchange, currency, etc.).
// Identifier is an opaque (Type, Value) pair; broker descriptions use Type = broker name ("IBKR", "SCHB"), Value = full instrument_description.
//
// # Plugin interface
//
// Implement the Plugin interface (Identify(ctx, broker, instrumentDescription)) and register in code.
// Add a row to the plugin config table (plugin_id, enabled, precedence, config JSONB). Precedence is required and unique; higher wins when merging results from multiple plugins.
//
// # Adding a new plugin
//
//  1. Create server/plugins/<datasource>/identifier (e.g. server/plugins/ibkr/identifier).
//  2. Implement identifier.Plugin (Identify method).
//  3. Register the plugin at init (e.g. in the service or a registry that the service uses).
//  4. Add a row to the plugin config table with a unique precedence.
//
// Plugin-specific migrations (e.g. reference tables) live in the plugin directory; the mechanism for applying them at datamodel creation time is out of scope (document that the operator applies them).
package identifier
