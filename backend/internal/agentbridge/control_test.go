package agentbridge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type controlProvider struct {
	fakeProvider
	sends                         int
	opens, recentOpens, pageOpens int
	target, text, key             string
	sendErr                       error
	receipt                       string
}

func newControlProvider() *controlProvider {
	return &controlProvider{fakeProvider: fakeProvider{caps: Capabilities{List: true, Send: true, Open: true}}}
}
func (p *controlProvider) Send(_ context.Context, id, text, key string) (json.RawMessage, error) {
	p.sends++
	p.target = id
	p.text = text
	p.key = key
	if p.sendErr != nil {
		return nil, p.sendErr
	}
	if p.receipt != "" {
		return json.RawMessage(p.receipt), nil
	}
	data, _ := json.Marshal(map[string]string{"threadId": id, "messageId": "msg-1", "runId": "run-1", "delivery": "queued"})
	return data, nil
}
func (p *controlProvider) Open(_ context.Context, id string) (json.RawMessage, error) {
	p.opens++
	data, _ := json.Marshal(map[string]any{"thread": map[string]string{"threadId": id}, "items": []any{}})
	return data, nil
}
func (p *controlProvider) OpenRecent(ctx context.Context, id string) (json.RawMessage, error) {
	p.recentOpens++
	data, _ := json.Marshal(map[string]any{"thread": map[string]string{"threadId": id}, "items": []any{}})
	return data, nil
}
func (p *controlProvider) OpenPage(ctx context.Context, id string, after int) (json.RawMessage, error) {
	p.pageOpens++
	data, _ := json.Marshal(map[string]any{"thread": map[string]string{"threadId": id}, "items": []any{}})
	return data, nil
}

func (p *controlProvider) ListPage(_ context.Context, cursor int) (json.RawMessage, error) {
	return json.RawMessage(`{"threads":[],"nextCursor":null}`), nil
}

func TestExplicitControlOwnerTargetAndIdempotency(t *testing.T) {
	p := newControlProvider()
	g := testGateway(t, p)
	for _, owner := range []string{"bob", "", " alice"} {
		if _, err := g.Send(context.Background(), owner, "laptop", "mcp:launch:thread", "fix it", "key"); !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"../bad", "https://evil", ""} {
		if _, err := g.Send(context.Background(), "alice", "laptop", id, "fix it", "key"); err == nil {
			t.Fatal("invalid target accepted")
		}
	}
	if p.sends != 0 {
		t.Fatal("unauthorized provider call")
	}
	for range 2 {
		if _, err := g.Send(context.Background(), "alice", "laptop", "mcp:launch:thread", "fix it", "key"); err != nil {
			t.Fatal(err)
		}
	}
	firstKey := p.key
	if p.target != "mcp:launch:thread" || p.text != "fix it" || !strings.HasPrefix(firstKey, "eyes-") {
		t.Fatalf("wrong target/payload/key: %+v", p)
	}
	if _, err := g.Send(context.Background(), "alice", "laptop", "mcp:launch:thread", "fix it", "key"); err != nil || p.key != firstKey {
		t.Fatal("retry key changed")
	}
	for _, candidate := range g.Tools() {
		if !candidate.Spec().ReadOnly || candidate.Spec().Name != "list_agent_threads" {
			t.Fatal("control exposed to model tools")
		}
	}
	p.sendErr = errors.New("secret token")
	if _, err := g.Send(context.Background(), "alice", "laptop", "id", "fix it", "key"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unsafe error %v", err)
	}
	p.sendErr = ErrDeliveryUnknown
	if _, err := g.Send(context.Background(), "alice", "laptop", "id", "fix it", "key"); !errors.Is(err, ErrDeliveryUnknown) {
		t.Fatal(err)
	}
	p.sendErr = nil
	p.receipt = `{"threadId":"wrong","messageId":"m","runId":"r","delivery":"queued"}`
	if _, err := g.Send(context.Background(), "alice", "laptop", "id", "fix it", "key"); !errors.Is(err, ErrDeliveryUnknown) {
		t.Fatal("uncorrelated receipt accepted")
	}
}

func TestControlHTTPUsesVerifiedOwnerAndStrictPayload(t *testing.T) {
	p := newControlProvider()
	h, _ := NewWorkspaceHandler(func(_ context.Context, token string) (string, error) { return token, nil }, testGateway(t, p))
	mux := http.NewServeMux()
	mux.Handle("POST /workspace/agents/{action}", h)
	for _, tc := range []struct {
		token, body string
		code        int
	}{
		{"", `{}`, 401},
		{"bob", `{"connection_id":"laptop","thread_id":"id","text":"hello","request_id":"r"}`, 503},
		{"alice", `{"connection_id":"laptop","thread_id":"id","text":"hello","request_id":"r","owner":"bob"}`, 400},
		{"alice", `{"connection_id":"laptop","thread_id":"id","thread_id":"other","text":"hello","request_id":"r"}`, 400},
		{"alice", `{"connection_id":"laptop","thread_id":"id","text":"hello","request_id":"r"}`, 200},
	} {
		r := httptest.NewRequest("POST", "/workspace/agents/messages", strings.NewReader(tc.body))
		if tc.token != "" {
			r.Header.Set("Authorization", "Bearer "+tc.token)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != tc.code || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("status %d body %s", w.Code, w.Body)
		}
	}
	if p.sends != 1 {
		t.Fatalf("sent %d times", p.sends)
	}
}

func TestThreadHTTPSeparatesRecentAndHistoryAndRejectsMixedModes(t *testing.T) {
	p := newControlProvider()
	handler, _ := NewWorkspaceHandler(func(context.Context, string) (string, error) { return "alice", nil }, testGateway(t, p))
	mux := http.NewServeMux()
	mux.Handle("POST /workspace/agents/{action}", handler)
	for _, test := range []struct {
		body   string
		status int
	}{
		{`{"connection_id":"laptop","thread_id":"id"}`, 200},
		{`{"connection_id":"laptop","thread_id":"id","from_start":true}`, 200},
		{`{"connection_id":"laptop","thread_id":"id","after_position":20}`, 200},
		{`{"connection_id":"laptop","thread_id":"id","from_start":true,"after_position":20}`, 400},
		{`{"connection_id":"laptop","thread_id":"id","from_start":"yes"}`, 400},
		{`{"connection_id":"laptop","thread_id":"id","from_start":true,"owner":"bob"}`, 400},
	} {
		request := httptest.NewRequest("POST", "/workspace/agents/thread", strings.NewReader(test.body))
		request.Header.Set("Authorization", "Bearer verified")
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("status=%d body=%s", response.Code, response.Body)
		}
	}
	if p.opens != 1 || p.recentOpens != 1 || p.pageOpens != 1 {
		t.Fatalf("wrong read operations: first=%d recent=%d page=%d", p.opens, p.recentOpens, p.pageOpens)
	}
}
