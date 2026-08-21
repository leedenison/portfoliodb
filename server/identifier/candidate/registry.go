package candidate

import "github.com/leedenison/portfoliodb/server/pluginreg"

// Registry holds candidate plugin implementations by plugin ID, and is
// [pluginreg.Registry] specialised to this family's Plugin. The orchestrator
// loads the enabled plugins, with their precedence and config, from the DB and
// then looks them up here to invoke Extract.
type Registry = pluginreg.Registry[Plugin]

// NewRegistry returns a new candidate plugin registry.
func NewRegistry() *Registry {
	return pluginreg.New[Plugin]()
}
