package worker

import (
	"fmt"
	"sync"
)

// Registry holds the process's periodic lifecycles by kind. The admin panel reads their status and starts manual
// rounds through it; every lifecycle added gets the registry's record function, so the persisted history has one
// writer and no worker package needs to know about the jobs table.
type Registry struct {
	record RecordFunc

	mu     sync.Mutex
	byKind map[string]*Lifecycle
}

// NewRegistry builds an empty registry. record may be nil, in which case no round is persisted.
func NewRegistry(record RecordFunc) *Registry {
	return &Registry{record: record, byKind: make(map[string]*Lifecycle)}
}

// Add registers a lifecycle under its kind and installs the record function. Call it before Start, so the first
// round is already recorded. A kind outside Kinds or a kind registered twice is an assembly error.
func (r *Registry) Add(l *Lifecycle) error {
	if !IsKind(l.kind) {
		return fmt.Errorf("worker: unknown task kind %q", l.kind)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byKind[l.kind]; ok {
		return fmt.Errorf("worker: task kind %q registered twice", l.kind)
	}
	l.setRecorder(r.record)
	r.byKind[l.kind] = l
	return nil
}

// Get returns the lifecycle registered under kind.
func (r *Registry) Get(kind string) (*Lifecycle, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	l, ok := r.byKind[kind]
	return l, ok
}

// Statuses returns the status of every registered lifecycle, in the order of Kinds.
func (r *Registry) Statuses() []Status {
	r.mu.Lock()
	lifecycles := make([]*Lifecycle, 0, len(r.byKind))
	for _, kind := range Kinds {
		if l, ok := r.byKind[kind]; ok {
			lifecycles = append(lifecycles, l)
		}
	}
	r.mu.Unlock()
	out := make([]Status, 0, len(lifecycles))
	for _, l := range lifecycles {
		out = append(out, l.Status())
	}
	return out
}
