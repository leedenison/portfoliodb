package pricefetcher

import "sync"

// Registry holds price plugin implementations by plugin ID.
// The orchestrator loads enabled plugins (with precedence and config) from the
// DB, then looks them up here to invoke FetchPrices.
type Registry struct {
	mu   sync.RWMutex
	ids  []string
	byID map[string]Plugin
}

// NewRegistry returns a new price plugin registry.
func NewRegistry() *Registry {
	return &Registry{byID: make(map[string]Plugin)}
}

// Register adds a plugin for the given id. Idempotent for same id (replaces).
// Registration order is preserved for ListIDs (used when assigning default
// precedence on first insert).
func (r *Registry) Register(id string, p Plugin) {
	if p == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byID[id]; !ok {
		r.ids = append(r.ids, id)
	}
	r.byID[id] = p
}

// ListIDs returns registered plugin IDs in registration order.
func (r *Registry) ListIDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, len(r.ids))
	copy(out, r.ids)
	return out
}

// Get returns the plugin for id, or nil if not registered.
func (r *Registry) Get(id string) Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byID[id]
}

// GetDisplayName returns the plugin's display name for id, or id if not registered.
func (r *Registry) GetDisplayName(id string) string {
	p := r.Get(id)
	if p == nil {
		return id
	}
	return p.DisplayName()
}
