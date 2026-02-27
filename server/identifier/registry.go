package identifier

import "sync"

// Registry holds plugin implementations by plugin ID.
// The service loads enabled plugins (with precedence and config) from the DB,
// then looks them up here to invoke Identify.
type Registry struct {
	mu   sync.RWMutex
	byID map[string]Plugin
}

// NewRegistry returns a new plugin registry.
func NewRegistry() *Registry {
	return &Registry{byID: make(map[string]Plugin)}
}

// Register adds a plugin for the given id. Idempotent for same id (replaces).
func (r *Registry) Register(id string, p Plugin) {
	if p == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID[id] = p
}

// Get returns the plugin for id, or nil if not registered.
func (r *Registry) Get(id string) Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byID[id]
}
