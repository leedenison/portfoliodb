package inflationfetcher

import "github.com/leedenison/portfoliodb/server/pluginreg"

// Registry holds inflation plugin implementations by plugin ID, and is
// [pluginreg.Registry] specialised to this family's Plugin. The orchestrator
// loads the enabled plugins, with their precedence and config, from the DB and
// then looks them up here to invoke FetchInflation.
type Registry = pluginreg.Registry[Plugin]

// NewRegistry returns a new inflation plugin registry.
func NewRegistry() *Registry {
	return pluginreg.New[Plugin]()
}
