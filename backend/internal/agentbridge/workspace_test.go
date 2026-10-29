package agentbridge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/web"
)

func TestWorkspaceStatusOwnershipAndStates(t *testing.T) {
	for _, tc := range []struct {
		name, token, method, data            string
		disabled, unavailable                bool
		wantCode, wantCalls, wantConnections int
	}{
		{name: "missing authentication", method: "GET", wantCode: 401},
		{name: "rejected authentication", token: "rejected", method: "GET", wantCode: 401},
		{name: "empty authenticated identity", token: "empty", method: "GET", wantCode: 401},
		{name: "owner isolation", token: "bob", method: "GET", wantCode: 200},
		{name: "feature off", token: "alice", method: "GET", disabled: true, wantCode: 200},
		{name: "ready", token: "alice", method: "GET", data: `{"threads":[{"threadId":"one","title":"<script>"}],"nextCursor":"page2"}`, wantCode: 200, wantCalls: 1, wantConnections: 1},
		{name: "empty", token: "alice", method: "GET", data: `{"threads":[]}`, wantCode: 200, wantCalls: 1, wantConnections: 1},
		{name: "unavailable", token: "alice", method: "GET", unavailable: true, wantCode: 200, wantCalls: 1, wantConnections: 1},
		{name: "mutation rejected", token: "alice", method: "POST", wantCode: 405},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &fakeProvider{caps: Capabilities{List: true}, list: func(ctx context.Context) (json.RawMessage, error) {
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 3*time.Second {
					t.Error("missing bounded deadline")
				}
				if tc.unavailable {
					return nil, errors.New("secret token and endpoint must not leak")
				}
				return json.RawMessage(tc.data), nil
			}}
			gateway := testGateway(t, provider)
			if tc.disabled {
				gateway, _ = New(nil)
			}
			handler, err := NewWorkspaceHandler(func(_ context.Context, token string) (string, error) {
				if token == "rejected" {
					return "", errors.New("secret auth detail")
				}
				if token == "empty" {
					return "", nil
				}
				return token, nil
			}, gateway)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(tc.method, "/workspace/agents?owner=alice&cursor=ignored&url=https://forbidden.test", strings.NewReader(`{"owner":"alice"}`))
			if tc.token != "" {
				request.Header.Set("Authorization", "Bearer "+tc.token)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tc.wantCode || provider.lists != tc.wantCalls || provider.reads != 0 {
				t.Fatalf("code=%d lists=%d reads=%d body=%s", response.Code, provider.lists, provider.reads, response.Body)
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("response must not be cached")
			}
			if strings.Contains(response.Body.String(), "secret") || strings.Contains(response.Body.String(), "forbidden.test") {
				t.Fatal("leaked credentials/configuration")
			}
			if response.Code == 200 {
				var result struct {
					Connections []Envelope `json:"connections"`
					Limit       int        `json:"limit"`
				}
				if json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Connections == nil || len(result.Connections) != tc.wantConnections || result.Limit != 20 {
					t.Fatalf("invalid response: %s", response.Body)
				}
				if len(result.Connections) > 0 {
					entry := result.Connections[0]
					if entry.Capabilities.Read || entry.Capabilities.Send || entry.Capabilities.Start || entry.Capabilities.Interrupt {
						t.Fatal("unsupported capability")
					}
					if (entry.Error != "") != tc.unavailable {
						t.Fatalf("unavailable=%v, entry=%+v", tc.unavailable, entry)
					}
				}
			}
		})
	}
}

func TestWorkspaceStatusCancellationAndCORS(t *testing.T) {
	provider := &fakeProvider{caps: Capabilities{List: true}, list: func(ctx context.Context) (json.RawMessage, error) { <-ctx.Done(); return nil, ctx.Err() }}
	handler, _ := NewWorkspaceHandler(func(context.Context, string) (string, error) { return "alice", nil }, testGateway(t, provider))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest("GET", "/workspace/agents", nil).WithContext(ctx)
	request.Header.Set("Authorization", "Bearer test")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "unavailable") {
		t.Fatalf("canceled read: %s", response.Body)
	}
	origins, _ := web.NewOriginPolicy("https://eyes.example")
	request = httptest.NewRequest("OPTIONS", "/workspace/agents", nil)
	request.Header.Set("Origin", "https://eyes.example")
	request.Header.Set("Access-Control-Request-Method", "GET")
	response = httptest.NewRecorder()
	origins.Handler(handler).ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || !strings.Contains(response.Header().Get("Access-Control-Allow-Methods"), "GET") {
		t.Fatalf("GET preflight failed: %+v", response)
	}
	request.Header.Set("Origin", "https://untrusted.example")
	response = httptest.NewRecorder()
	origins.Handler(handler).ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatal("untrusted origin permitted")
	}
	if provider.lists != 1 {
		t.Fatal("preflight contacted provider")
	}
}

func TestWorkspaceStatusRequiresDependencies(t *testing.T) {
	gateway, _ := New(nil)
	if _, err := NewWorkspaceHandler(nil, gateway); err == nil {
		t.Fatal("nil verifier accepted")
	}
	if _, err := NewWorkspaceHandler(func(context.Context, string) (string, error) { return "alice", nil }, nil); err == nil {
		t.Fatal("nil gateway accepted")
	}
}
