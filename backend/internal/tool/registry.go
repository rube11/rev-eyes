package tool

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

var (
	ErrNilTool       = errors.New("tool is required")
	ErrEmptyToolName = errors.New("tool name is required")
)

// Registry stores the tools available to the agent.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

// Register adds a tool and rejects duplicate or empty names.
func (r *Registry) Register(t Tool) error {
	if t == nil {
		return ErrNilTool
	}

	spec := t.Spec()
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		return ErrEmptyToolName
	}
	if name != spec.Name {
		return fmt.Errorf("tool name %q must not have surrounding whitespace", spec.Name)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("tool %q is already registered", name)
	}

	if r.tools == nil {
		r.tools = make(map[string]Tool)
	}
	r.tools[name] = t
	return nil
}

// Get returns a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	t, ok := r.tools[name]
	return t, ok
}

// Specs returns tool specifications sorted by name.
func (r *Registry) Specs() []Spec {
	r.mu.RLock()
	defer r.mu.RUnlock()

	specs := make([]Spec, 0, len(r.tools))
	for _, t := range r.tools {
		specs = append(specs, t.Spec())
	}

	sort.Slice(specs, func(i, j int) bool {
		return specs[i].Name < specs[j].Name
	})

	return specs
}
