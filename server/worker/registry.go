package worker

import (
	"sync"
	"time"
)

// State represents the current state of a worker.
type State string

const (
	Idle    State = "IDLE"
	Running State = "RUNNING"
)

// Status holds the current status of a named worker.
type Status struct {
	Name       string
	State      State
	Summary    string
	QueueDepth int
	UpdatedAt  time.Time
}

// Registry tracks the runtime status of background workers.
// It is safe for concurrent use.
type Registry struct {
	mu      sync.RWMutex
	workers map[string]*Status
}

// NewRegistry creates an empty worker registry.
func NewRegistry() *Registry {
	return &Registry{workers: make(map[string]*Status)}
}

// SetRunning marks the named worker as running with a summary of its activity.
func (r *Registry) SetRunning(name, summary string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.getOrCreate(name)
	s.State = Running
	s.Summary = summary
	s.UpdatedAt = time.Now()
}

// SetIdle marks the named worker as idle.
func (r *Registry) SetIdle(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.getOrCreate(name)
	s.State = Idle
	s.Summary = ""
	s.UpdatedAt = time.Now()
}

// SetQueueDepth updates the queue depth for the named worker.
func (r *Registry) SetQueueDepth(name string, depth int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.getOrCreate(name)
	s.QueueDepth = depth
	s.UpdatedAt = time.Now()
}

// List returns a snapshot of all registered workers sorted by name.
func (r *Registry) List() []Status {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Status, 0, len(r.workers))
	for _, s := range r.workers {
		out = append(out, *s)
	}
	return out
}

// getOrCreate returns the status entry for name, creating it if needed.
// Caller must hold r.mu.
func (r *Registry) getOrCreate(name string) *Status {
	s, ok := r.workers[name]
	if !ok {
		s = &Status{Name: name, State: Idle}
		r.workers[name] = s
	}
	return s
}
