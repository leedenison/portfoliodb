package identifier

import "github.com/leedenison/portfoliodb/server/pluginreg"

// Registry holds identifier plugin implementations by plugin ID, and is
// [pluginreg.Registry] specialised to this family's Plugin. The orchestrator
// loads the enabled plugins, with their precedence and config, from the DB and
// then looks them up here to invoke Identify.
type Registry = pluginreg.Registry[Plugin]

// NewRegistry returns a new identifier plugin registry.
func NewRegistry() *Registry {
	return pluginreg.New[Plugin]()
}
