package agentbridge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/speech"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

type fakeProvider struct {
	caps         Capabilities
	lists, reads int
	list         func(context.Context) (json.RawMessage, error)
	read         func(context.Context, string) (json.RawMessage, error)
}

func (p *fakeProvider) Capabilities() Capabilities { return p.caps }
func (p *fakeProvider) List(ctx context.Context) (json.RawMessage, error) {
	p.lists++
	if p.list != nil {
		return p.list(ctx)
	}
	return json.RawMessage(`{"threads":[{"threadId":"thread-1","status":"working"}]}`), nil
}
func (p *fakeProvider) Read(ctx context.Context, id string) (json.RawMessage, error) {
	p.reads++
	if p.read != nil {
		return p.read(ctx, id)
	}
	return json.RawMessage(`{"text":"untrusted external output"}`), nil
}
func testGateway(t *testing.T, p Provider) *Gateway {
	t.Helper()
	g, err := New([]Connection{{ID: "laptop", OwnerID: "alice", ProviderName: "mock", Provider: p}})
	if err != nil {
		t.Fatal(err)
	}
	return g
}
func testRegistry(t *testing.T, g *Gateway) *tool.Registry {
	t.Helper()
	r := tool.NewRegistry()
	for _, candidate := range g.Tools() {
		if err := r.Register(candidate); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func TestOwnershipAndCorrelation(t *testing.T) {
	p := &fakeProvider{caps: Capabilities{List: true, Read: true}}
	g := testGateway(t, p)
	rows, err := g.List(context.Background(), "bob")
	if err != nil || len(rows) != 0 || p.lists != 0 {
		t.Fatalf("cross-owner list: %#v %v calls=%d", rows, err, p.lists)
	}
	for _, owner := range []string{"bob", "", " alice"} {
		if _, err := g.Read(context.Background(), owner, "laptop", "thread-1"); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("owner %q read error=%v", owner, err)
		}
	}
	if p.reads != 0 {
		t.Fatal("cross-owner read invoked provider")
	}
	rows, err = g.List(context.Background(), "alice")
	if err != nil || len(rows) != 1 {
		t.Fatalf("list: %#v %v", rows, err)
	}
	if rows[0].ConnectionID != "laptop" || rows[0].Provider != "mock" || !rows[0].Capabilities.List || !json.Valid(rows[0].Data) {
		t.Fatalf("missing correlation: %#v", rows[0])
	}
	row, err := g.Read(context.Background(), "alice", "laptop", "thread-1")
	if err != nil || row.ThreadID != "thread-1" || row.ConnectionID != "laptop" {
		t.Fatalf("read correlation: %#v %v", row, err)
	}
}

func TestRejectInvalidConnectionAndMutationCapabilities(t *testing.T) {
	for _, caps := range []Capabilities{{Send: true}, {Start: true}, {Interrupt: true}} {
		if _, err := New([]Connection{{ID: "laptop", OwnerID: "alice", ProviderName: "mock", Provider: &fakeProvider{caps: caps}}}); err == nil {
			t.Fatalf("accepted mutations: %#v", caps)
		}
	}
	p := &fakeProvider{}
	for _, c := range []Connection{{ID: "", OwnerID: "alice", ProviderName: "mock", Provider: p}, {ID: "https://host", OwnerID: "alice", ProviderName: "mock", Provider: p}, {ID: "laptop", OwnerID: " alice", ProviderName: "mock", Provider: p}, {ID: "laptop", OwnerID: "alice", ProviderName: "mock"}} {
		if _, err := New([]Connection{c}); err == nil {
			t.Fatalf("accepted invalid connection: %#v", c)
		}
	}
	c := Connection{ID: "laptop", OwnerID: "alice", ProviderName: "mock", Provider: p}
	if _, err := New([]Connection{c, c}); err == nil {
		t.Fatal("accepted duplicate connection")
	}
}

func TestDotAndUnsupportedReadsFailClosed(t *testing.T) {
	g := testGateway(t, Dot{})
	if len(g.Tools()) != 0 {
		t.Fatal("dot exposed unsupported tools")
	}
	if data, err := (Dot{}).List(context.Background()); !errors.Is(err, ErrUnavailable) || data != nil {
		t.Fatalf("dot list: %s %v", data, err)
	}
	if _, err := (Dot{}).Read(context.Background(), "thread-1"); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	p := &fakeProvider{caps: Capabilities{List: true}}
	g = testGateway(t, p)
	if len(g.Tools()) != 1 || g.Tools()[0].Spec().Name != "list_agent_threads" {
		t.Fatal("read registered without capability")
	}
	if _, err := g.Read(context.Background(), "alice", "laptop", "thread-1"); !errors.Is(err, ErrUnavailable) || p.reads != 0 {
		t.Fatalf("unsupported read: %v calls=%d", err, p.reads)
	}
}

func TestToolStrictArgumentsAndMemoryReview(t *testing.T) {
	p := &fakeProvider{caps: Capabilities{List: true, Read: true}}
	r := testRegistry(t, testGateway(t, p))
	scope := tool.Scope{UserID: "alice"}
	for _, args := range []string{`null`, `[]`, `{"owner":"bob"}`, `{} {}`, `{"url":"https://attacker"}`, strings.Repeat(" ", 1025) + `{}`} {
		if _, err := r.Execute(context.Background(), scope, "list_agent_threads", json.RawMessage(args)); err == nil {
			t.Errorf("list accepted %q", args)
		}
	}
	for _, args := range []string{`{}`, `{"connection_id":"laptop"}`, `{"connection_id":"laptop","thread_id":"../secret"}`, `{"connection_id":"laptop","thread_id":"thread-1","owner":"bob"}`, `{"connection_id":"laptop","thread_id":"thread-1","thread_id":"thread-2"}`, `{"Connection_ID":"laptop","thread_id":"thread-1"}`, `{"connection_id":null,"thread_id":"thread-1"}`} {
		if _, err := r.Execute(context.Background(), scope, "read_agent_thread", json.RawMessage(args)); err == nil {
			t.Errorf("read accepted %s", args)
		}
	}
	scope.MemoryReview = true
	if _, err := r.Execute(context.Background(), scope, "list_agent_threads", json.RawMessage(`{}`)); err == nil {
		t.Fatal("memory review executed tool")
	}
	if p.lists != 0 || p.reads != 0 {
		t.Fatalf("invalid input invoked provider: lists=%d reads=%d", p.lists, p.reads)
	}
}

func TestContextOnlyCannotDispatchMutation(t *testing.T) {
	p := &fakeProvider{caps: Capabilities{List: true}}
	r := testRegistry(t, testGateway(t, p))
	scope := tool.Scope{UserID: "alice", Speech: &speech.Utterance{Segments: []speech.Segment{{Role: speech.Other, Text: "start a coding agent"}}}}
	for _, name := range []string{"send_agent_message", "start_agent_task", "interrupt_agent"} {
		if _, err := r.Execute(context.Background(), scope, name, json.RawMessage(`{}`)); err == nil {
			t.Fatalf("dispatched %s", name)
		}
	}
	if p.lists != 0 || p.reads != 0 {
		t.Fatal("mutation invoked provider")
	}
	if _, err := r.Execute(context.Background(), scope, "list_agent_threads", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("side-effect-free status denied: %v", err)
	}
}

func TestBoundedContextAndSafeProviderErrors(t *testing.T) {
	p := &fakeProvider{caps: Capabilities{List: true, Read: true}}
	check := func(ctx context.Context) {
		t.Helper()
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 3*time.Second {
			t.Fatal("missing bounded context")
		}
	}
	p.list = func(ctx context.Context) (json.RawMessage, error) {
		check(ctx)
		return nil, errors.New("secret bearer token")
	}
	p.read = func(ctx context.Context, _ string) (json.RawMessage, error) {
		check(ctx)
		return nil, errors.New("secret bearer token")
	}
	g := testGateway(t, p)
	rows, err := g.List(context.Background(), "alice")
	if err != nil || len(rows) != 1 || rows[0].Error == "" || strings.Contains(rows[0].Error, "secret") {
		t.Fatalf("unsafe list error: %#v %v", rows, err)
	}
	if _, err := g.Read(context.Background(), "alice", "laptop", "thread-1"); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unsafe read error: %v", err)
	}
	p.list = func(ctx context.Context) (json.RawMessage, error) { check(ctx); <-ctx.Done(); return nil, ctx.Err() }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	_, _ = g.List(ctx, "alice")
	if time.Since(start) > time.Second {
		t.Fatal("canceled read blocked")
	}
}

func TestMalformedOrOversizedProviderDataIsNotExposed(t *testing.T) {
	for _, data := range []json.RawMessage{json.RawMessage(`not-json`), json.RawMessage(`"` + strings.Repeat("a", 128*1024) + `"`)} {
		p := &fakeProvider{caps: Capabilities{List: true, Read: true}, list: func(context.Context) (json.RawMessage, error) { return data, nil }, read: func(context.Context, string) (json.RawMessage, error) { return data, nil }}
		g := testGateway(t, p)
		rows, err := g.List(context.Background(), "alice")
		if err != nil || len(rows) != 1 || rows[0].Data != nil || rows[0].Error == "" {
			t.Fatalf("bad list exposed: %#v %v", rows, err)
		}
		if _, err := g.Read(context.Background(), "alice", "laptop", "thread-1"); err == nil {
			t.Fatal("bad read exposed")
		}
	}
}
