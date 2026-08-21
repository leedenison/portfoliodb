// Package pluginreg holds the registry every plugin family keeps its
// implementations in.
//
// The five families -- identifier, candidate, price, corporate event and
// inflation -- differ in what their Plugin interface can do and not at all in
// how an implementation is registered and looked up, so the registry is generic
// over the plugin type and each family aliases it.
package pluginreg

import "sync"

// Named is what the registry needs of a plugin: a human-readable name for the
// admin UI. Every plugin interface in the server satisfies it.
type Named interface {
	DisplayName() string
}

// Registry holds plugin implementations by plugin ID. A family's orchestrator
// loads the enabled plugins, with their precedence and config, from the DB and
// then looks them up here to invoke them.
//
// Safe for concurrent use.
type Registry[P Named] struct {
	mu   sync.RWMutex
	ids  []string
	byID map[string]P
}

// New returns a new plugin registry.
func New[P Named]() *Registry[P] {
	return &Registry[P]{byID: make(map[string]P)}
}

// Register adds a plugin for the given id, replacing any plugin already
// registered under it. A nil plugin is ignored. Registration order is preserved
// for ListIDs, which is what assigns default precedence on first insert.
func (r *Registry[P]) Register(id string, p P) {
	var zero P
	if any(p) == any(zero) {
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
func (r *Registry[P]) ListIDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, len(r.ids))
	copy(out, r.ids)
	return out
}

// Get returns the plugin for id, or the zero plugin if not registered.
func (r *Registry[P]) Get(id string) P {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byID[id]
}

// GetDisplayName returns the plugin's display name for id, or id itself if not
// registered.
func (r *Registry[P]) GetDisplayName(id string) string {
	p := r.Get(id)
	var zero P
	if any(p) == any(zero) {
		return id
	}
	return p.DisplayName()
}
