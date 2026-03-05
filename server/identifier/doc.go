// Package identifier defines the instrument identification plugin API for PortfolioDB.
//
// # Canonical types
//
// Instrument holds security-master data (asset class, exchange, currency, etc.).
// Identifier is an opaque (Type, Value) pair; broker descriptions use Type = broker name ("IBKR", "SCHB"), Value = full instrument_description.
//
// # Plugin interface
//
// Implement the Plugin interface (Identify and DefaultConfig). Register the plugin in code. On startup the server calls DefaultConfig for each registered plugin that has no row in identifier_plugin_config and inserts the returned JSON (with enabled=false and precedence assigned by registration order). The user edits config via the Admin UI.
//
// # Adding a new plugin
//
//  1. Create server/plugins/<datasource>/identifier (e.g. server/plugins/ibkr/identifier).
//  2. Implement identifier.Plugin (Identify and DefaultConfig). DefaultConfig returns JSON with the config keys your plugin uses and dummy/empty values.
//  3. Register the plugin at startup (e.g. in main, registry.Register(pluginID, plugin)).
//  4. No migration or manual row is needed; the server creates the config row on first run.
//
// Plugin-specific migrations (e.g. reference tables) live in the plugin directory; the mechanism for applying them at datamodel creation time is out of scope (document that the operator applies them).
package identifier
