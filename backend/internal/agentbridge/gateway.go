// Package agentbridge owns the provider boundary for external coding agents.
// Provider output is evidence, never authorization to execute another action.
package agentbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrUnavailable = errors.New("agent connection or capability unavailable")

// Capabilities are explicit: adding a provider never implicitly enables writes.
type Capabilities struct {
	List      bool `json:"list"`
	Open      bool `json:"open"`
	Read      bool `json:"read"`
	Send      bool `json:"send"`
	Start     bool `json:"start"`
	Interrupt bool `json:"interrupt"`
}

// Provider is the model-visible read boundary. Read MUST be side-effect free;
// acknowledging delivery or approving anything is not a read for this purpose.
// Implementations must honor context cancellation and bound response sizes.
type Provider interface {
	Capabilities() Capabilities
	List(context.Context) (json.RawMessage, error)
	Read(context.Context, string) (json.RawMessage, error)
}

// Connection is trusted server configuration, never model-supplied credentials.
// A provider credential must belong to OwnerID and respect that owner's ceilings.
type Connection struct {
	ID           string
	OwnerID      string
	ProviderName string
	Provider     Provider
}

type Gateway struct{ connections []Connection }

func New(connections []Connection) (*Gateway, error) {
	seen := map[string]bool{}
	for _, c := range connections {
		if !validID(c.ID) || strings.TrimSpace(c.OwnerID) == "" || c.OwnerID != strings.TrimSpace(c.OwnerID) || c.ProviderName == "" || c.Provider == nil || seen[c.ID] {
			return nil, errors.New("invalid or duplicate agent connection configuration")
		}
		seen[c.ID] = true
		caps := c.Provider.Capabilities()
		_, canSend := c.Provider.(Messenger)
		_, canOpen := c.Provider.(ThreadOpener)
		if (caps.Send && !canSend) || (caps.Open && !canOpen) || caps.Start || caps.Interrupt {
			return nil, errors.New("agent capability has no implementation")
		}
	}
	return &Gateway{connections: append([]Connection(nil), connections...)}, nil
}

// Envelope preserves correlation independently of any final-composer prose.
type Envelope struct {
	ConnectionID string          `json:"connection_id"`
	Provider     string          `json:"provider"`
	ThreadID     string          `json:"thread_id,omitempty"`
	Capabilities Capabilities    `json:"capabilities"`
	Data         json.RawMessage `json:"untrusted_provider_data,omitempty"`
	Error        string          `json:"error,omitempty"`
}

// List performs bounded status reads, not background execution or completion waits.
func (g *Gateway) List(ctx context.Context, owner string) ([]Envelope, error) {
	if strings.TrimSpace(owner) == "" {
		return nil, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	result := []Envelope{}
	for _, c := range g.connections {
		if c.OwnerID != owner || !c.Provider.Capabilities().List {
			continue
		}
		entry := Envelope{ConnectionID: c.ID, Provider: c.ProviderName, Capabilities: c.Provider.Capabilities()}
		data, err := c.Provider.List(ctx)
		if err != nil || !validData(data) {
			entry.Error = "agent status unavailable; no action was taken"
		} else {
			entry.Data = data
		}
		result = append(result, entry)
	}
	return result, nil
}

func (g *Gateway) Read(ctx context.Context, owner, connectionID, threadID string) (Envelope, error) {
	if !validID(connectionID) || !validID(threadID) || owner == "" {
		return Envelope{}, ErrUnavailable
	}
	for _, c := range g.connections {
		if c.ID != connectionID || c.OwnerID != owner || !c.Provider.Capabilities().Read {
			continue
		}
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		data, err := c.Provider.Read(ctx, threadID)
		if err != nil || !validData(data) {
			return Envelope{}, fmt.Errorf("agent thread unavailable")
		}
		return Envelope{ConnectionID: c.ID, Provider: c.ProviderName, ThreadID: threadID, Capabilities: c.Provider.Capabilities(), Data: data}, nil
	}
	return Envelope{}, ErrUnavailable
}

func validData(data json.RawMessage) bool { return len(data) <= 128*1024 && json.Valid(data) }
func validID(id string) bool {
	if len(id) == 0 || len(id) > 256 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' || c == ':') {
			return false
		}
	}
	return true
}

// Dot is intentionally capability-disabled: no public inbound API is assumed.
type Dot struct{}

func (Dot) Capabilities() Capabilities                            { return Capabilities{} }
func (Dot) List(context.Context) (json.RawMessage, error)         { return nil, ErrUnavailable }
func (Dot) Read(context.Context, string) (json.RawMessage, error) { return nil, ErrUnavailable }
