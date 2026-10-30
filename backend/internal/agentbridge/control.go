package agentbridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrDeliveryUnknown = errors.New("delivery unknown; check the thread before sending again")

type Messenger interface {
	Send(context.Context, string, string, string) (json.RawMessage, error)
}
type ThreadOpener interface {
	Open(context.Context, string) (json.RawMessage, error)
}
type ThreadPageOpener interface {
	OpenPage(context.Context, string, int) (json.RawMessage, error)
}

type PageLister interface {
	ListPage(context.Context, int) (json.RawMessage, error)
}

func (g *Gateway) connection(owner, id string) *Connection {
	if owner == "" || !validID(id) {
		return nil
	}
	for i := range g.connections {
		c := &g.connections[i]
		if c.OwnerID == owner && c.ID == id {
			return c
		}
	}
	return nil
}

func (g *Gateway) CanSend(owner, id string) bool {
	c := g.connection(owner, id)
	return c != nil && c.Provider.Capabilities().Send
}

// Send is deliberately outside Tools(): only an explicit UI send or a bound
// tap-to-talk session can invoke it. The provider owns durable idempotency.
func (g *Gateway) Send(ctx context.Context, owner, connectionID, threadID, message, requestID string) (json.RawMessage, error) {
	c := g.connection(owner, connectionID)
	if c == nil || !c.Provider.Capabilities().Send || !validID(threadID) || !validID(requestID) || strings.TrimSpace(message) == "" || len(message) > 16000 || !utf8.ValidString(message) {
		return nil, ErrUnavailable
	}
	sender, ok := c.Provider.(Messenger)
	if !ok {
		return nil, ErrUnavailable
	}
	key := sha256.Sum256([]byte(owner + "\x00" + connectionID + "\x00" + requestID))
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	data, err := sender.Send(ctx, threadID, message, "eyes-"+hex.EncodeToString(key[:]))
	if err != nil {
		if errors.Is(err, ErrDeliveryUnknown) {
			return nil, ErrDeliveryUnknown
		}
		return nil, ErrUnavailable
	}
	var receipt struct {
		ThreadID  string `json:"threadId"`
		MessageID string `json:"messageId"`
		RunID     string `json:"runId"`
		Delivery  string `json:"delivery"`
	}
	if !validData(data) || json.Unmarshal(data, &receipt) != nil || receipt.ThreadID != threadID || receipt.MessageID == "" || receipt.RunID == "" || (receipt.Delivery != "queued" && receipt.Delivery != "started") {
		return nil, ErrDeliveryUnknown
	}
	return data, nil
}

func (g *Gateway) Open(ctx context.Context, owner, connectionID, threadID string) (json.RawMessage, error) {
	return g.OpenPage(ctx, owner, connectionID, threadID, -1)
}
func (g *Gateway) OpenPage(ctx context.Context, owner, connectionID, threadID string, after int) (json.RawMessage, error) {
	c := g.connection(owner, connectionID)
	if c == nil || !c.Provider.Capabilities().Open || !validID(threadID) {
		return nil, ErrUnavailable
	}
	opener, ok := c.Provider.(ThreadOpener)
	if !ok {
		return nil, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	var data json.RawMessage
	var err error
	if after >= 0 {
		pageOpener, ok := c.Provider.(ThreadPageOpener)
		if !ok || after > 10000000 {
			return nil, ErrUnavailable
		}
		data, err = pageOpener.OpenPage(ctx, threadID, after)
	} else {
		data, err = opener.Open(ctx, threadID)
	}
	var result struct {
		Thread struct {
			ID string `json:"threadId"`
		} `json:"thread"`
	}
	if err != nil || !validData(data) || json.Unmarshal(data, &result) != nil || result.Thread.ID != threadID {
		return nil, ErrUnavailable
	}
	return data, nil
}

func (g *Gateway) Page(ctx context.Context, owner, connectionID string, cursor int) ([]Envelope, error) {
	c := g.connection(owner, connectionID)
	if c == nil || !c.Provider.Capabilities().List || cursor < 0 || cursor > 1000000 {
		return nil, ErrUnavailable
	}
	pager, ok := c.Provider.(PageLister)
	if !ok {
		return nil, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	data, err := pager.ListPage(ctx, cursor)
	if err != nil || !validData(data) {
		return nil, ErrUnavailable
	}
	return []Envelope{{ConnectionID: c.ID, Provider: c.ProviderName, Capabilities: c.Provider.Capabilities(), Data: data}}, nil
}
