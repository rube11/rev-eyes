package t3

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const listSchema = `{"type":"object","properties":{"allProjects":{"type":"boolean"},"limit":{"type":"integer"}}}`

func fakeServer(t *testing.T, sse bool, readOnly bool, schema string, result string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	return fakeServerProtocol(t, sse, readOnly, schema, result, legacyProtocolVersion)
}
func fakeServerProtocol(t *testing.T, sse bool, readOnly bool, schema string, result string, negotiated string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	calls := new(atomic.Int32)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mcp" || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("wrong request endpoint or auth")
		}
		if r.Method == http.MethodDelete {
			if r.Header.Get("Mcp-Session-Id") != "test-session" || r.Header.Get("MCP-Protocol-Version") != negotiated {
				t.Error("wrong closed session or protocol")
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var req struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		if req.Method != "initialize" && r.Header.Get("MCP-Protocol-Version") != negotiated {
			t.Error("request did not use the negotiated protocol")
		}
		var response string
		switch req.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "test-session")
			response = fmt.Sprintf(`{"protocolVersion":%q,"capabilities":{},"serverInfo":{"name":"mock","version":"test"}}`, negotiated)
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
			return
		case "tools/list":
			if r.Header.Get("Mcp-Session-Id") != "test-session" || r.Header.Get("MCP-Protocol-Version") != negotiated {
				t.Error("missing session/protocol")
			}
			response = fmt.Sprintf(`{"tools":[{"name":"t3_thread_list","inputSchema":%s,"annotations":{"readOnlyHint":%t}},{"name":"t3_thread_send"}]}`, schema, readOnly)
		case "tools/call":
			calls.Add(1)
			var p struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &p)
			if p.Name != "t3_thread_list" || p.Arguments["allProjects"] != true || p.Arguments["limit"] != float64(20) {
				t.Error("unexpected tool or arguments")
			}
			response = result
		default:
			t.Errorf("unexpected method %q", req.Method)
			w.WriteHeader(400)
			return
		}
		envelope := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":%s}`, req.ID, response)
		if sse {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", envelope)
		} else {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, envelope)
		}
	})), calls
}

func TestListJSONAndSSE(t *testing.T) {
	for _, sse := range []bool{false, true} {
		t.Run(fmt.Sprint(sse), func(t *testing.T) {
			s, calls := fakeServer(t, sse, true, listSchema, `{"structuredContent":{"threads":[{"threadId":"a","status":"working"}],"nextCursor":20}}`)
			defer s.Close()
			c, err := NewLoopback(s.URL+"/mcp", "test-token")
			if err != nil {
				t.Fatal(err)
			}
			raw, err := c.List(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(raw), `"nextCursor":20`) || calls.Load() != 1 {
				t.Fatalf("wrong result %s / calls %d", raw, calls.Load())
			}
			if c.Capabilities().Read || c.Capabilities().Send || c.Capabilities().Start || c.Capabilities().Interrupt {
				t.Fatal("unsafe advertised capability")
			}
			if _, err = c.Read(context.Background(), "a"); err != ErrUnavailable || calls.Load() != 1 {
				t.Fatal("read must fail without network")
			}
		})
	}
}

func TestDiscoveryFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name     string
		readonly bool
		schema   string
	}{
		{"nonreadonly", false, listSchema},
		{"unknownRequired", true, `{"type":"object","properties":{"allProjects":{"type":"boolean"},"limit":{"type":"integer"}},"required":["projectId"]}`},
		{"wrongType", true, `{"type":"object","properties":{"allProjects":{"type":"string"},"limit":{"type":"integer"}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, calls := fakeServer(t, false, tc.readonly, tc.schema, `{}`)
			defer s.Close()
			c, _ := NewLoopback(s.URL+"/mcp", "test-token")
			if _, err := c.List(context.Background()); err != ErrUnavailable || calls.Load() != 0 {
				t.Fatalf("must fail closed: %v calls %d", err, calls.Load())
			}
		})
	}
}

func TestTextResultAndToolError(t *testing.T) {
	for _, tc := range []struct {
		result string
		ok     bool
	}{
		{`{"content":[{"type":"text","text":"{\"threads\":[],\"nextCursor\":null}"}]}`, true},
		{`{"isError":true,"content":[{"type":"text","text":"SECRET upstream detail"}]}`, false},
		{`{"structuredContent":{"other":42}}`, false},
	} {
		s, _ := fakeServer(t, false, true, listSchema, tc.result)
		c, _ := NewLoopback(s.URL+"/mcp", "test-token")
		_, err := c.List(context.Background())
		s.Close()
		if (err == nil) != tc.ok {
			t.Fatalf("result %s: %v", tc.result, err)
		}
		if err != nil && strings.Contains(err.Error(), "SECRET") {
			t.Fatal("leaked upstream error")
		}
	}
}

func TestEndpointAndRedirectProtection(t *testing.T) {
	for _, u := range []string{"http://example.com/mcp", "https://example.com/rpc", "https://user:pass@example.com/mcp", "https://example.com/mcp?secret=x", "https://example.com/mcp#x"} {
		if _, err := New(u, "test-token"); err == nil {
			t.Errorf("accepted unsafe endpoint %s", u)
		}
	}
	if _, err := NewLoopback("http://localhost/mcp", "test-token"); err == nil {
		t.Fatal("DNS loopback not explicit")
	}
	if _, err := New("https://example.com/mcp", ""); err == nil {
		t.Fatal("missing token")
	}
	var leaked atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Store(true) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/mcp", http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	c, _ := NewLoopback(source.URL+"/mcp", "test-token")
	_, err := c.List(context.Background())
	if err == nil || leaked.Load() {
		t.Fatal("redirect must not forward credential")
	}
}

func TestCancellationAndResponseValidation(t *testing.T) {
	s, calls := fakeServer(t, false, true, listSchema, `{"structuredContent":{"threads":[]}}`)
	defer s.Close()
	c, _ := NewLoopback(s.URL+"/mcp", "test-token")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.List(ctx); err == nil || calls.Load() != 0 {
		t.Fatal("canceled request called provider")
	}
	for _, raw := range []string{`{"jsonrpc":"2.0","id":2,"result":{}}`, `{"jsonrpc":"2.0","id":1,"error":{"message":"secret"}}`, `not json`} {
		if _, err := decodeResponse([]byte(raw), 1); err == nil {
			t.Fatal("invalid response accepted")
		}
	}
}

func TestResponseSizeAndNoRetry(t *testing.T) {
	var requests atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, strings.Repeat("x", maxResponseBytes+1))
	}))
	defer s.Close()
	c, _ := NewLoopback(s.URL+"/mcp", "test-token")
	if _, err := c.List(context.Background()); err == nil || requests.Load() != 1 {
		t.Fatal("oversize response accepted or automatically retried")
	}
}

func TestReconnectRediscoversCapabilities(t *testing.T) {
	s, calls := fakeServer(t, false, true, listSchema, `{"structuredContent":{"threads":[]}}`)
	defer s.Close()
	c, _ := NewLoopback(s.URL+"/mcp", "test-token")
	for i := 0; i < 2; i++ {
		if _, err := c.List(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("unexpected provider call count")
	}
	// fakeServer requires a newly initialized session header for every tools/list.
	// List has no cached connection, capability or result state across invocations.
}

func TestInitializeFailureCleansAllocatedSession(t *testing.T) {
	for _, result := range []string{`{"protocolVersion":"unsupported"}`, `invalid JSON`} {
		var closed atomic.Bool
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodDelete {
				if r.Header.Get("Mcp-Session-Id") != "allocated" {
					t.Error("incorrect session cleanup")
				}
				closed.Store(true)
				w.WriteHeader(http.StatusNoContent)
				return
			}
			w.Header().Set("Mcp-Session-Id", "allocated")
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"result":%s}`, result)
		}))
		c, _ := NewLoopback(s.URL+"/mcp", "test-token")
		_, err := c.List(context.Background())
		s.Close()
		if err == nil || !closed.Load() {
			t.Fatalf("failed initialization not cleaned: %v / %v", err, closed.Load())
		}
	}
}

func TestDiscoveryPaginationAndCursorCycle(t *testing.T) {
	for _, cycle := range []bool{false, true} {
		t.Run(fmt.Sprint(cycle), func(t *testing.T) {
			var pages, calls atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodDelete {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				var req struct {
					ID     int    `json:"id"`
					Method string `json:"method"`
					Params struct {
						Cursor string `json:"cursor"`
					} `json:"params"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					return
				}
				result := ""
				switch req.Method {
				case "initialize":
					result = `{"protocolVersion":"2025-03-26"}`
				case "notifications/initialized":
					w.WriteHeader(http.StatusAccepted)
					return
				case "tools/list":
					n := pages.Add(1)
					if n == 1 {
						if req.Params.Cursor != "" {
							t.Error("unexpected first cursor")
						}
						result = `{"tools":[],"nextCursor":"a"}`
					} else if cycle {
						if n == 2 {
							result = `{"tools":[],"nextCursor":"b"}`
						} else {
							result = `{"tools":[],"nextCursor":"a"}`
						}
					} else {
						if req.Params.Cursor != "a" {
							t.Error("cursor not forwarded")
						}
						result = fmt.Sprintf(`{"tools":[{"name":"t3_thread_list","annotations":{"readOnlyHint":true},"inputSchema":%s}]}`, listSchema)
					}
				case "tools/call":
					calls.Add(1)
					result = `{"structuredContent":{"threads":[]}}`
				default:
					t.Error("unexpected method")
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":%s}`, req.ID, result)
			}))
			defer s.Close()
			c, _ := NewLoopback(s.URL+"/mcp", "test-token")
			_, err := c.List(context.Background())
			if cycle {
				if err == nil || pages.Load() != 3 || calls.Load() != 0 {
					t.Fatalf("cycle not rejected: %v pages %d calls %d", err, pages.Load(), calls.Load())
				}
			} else if err != nil || pages.Load() != 2 || calls.Load() != 1 {
				t.Fatalf("pagination failed: %v", err)
			}
		})
	}
}

func TestStalledHTTPAndSSEHonorDeadline(t *testing.T) {
	for _, sse := range []bool{false, true} {
		t.Run(fmt.Sprint(sse), func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if sse {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, ": heartbeat\n\n")
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
			}))
			defer s.Close()
			c, _ := NewLoopback(s.URL+"/mcp", "test-token")
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			start := time.Now()
			_, err := c.List(ctx)
			if err == nil || time.Since(start) > 2*time.Second {
				t.Fatalf("deadline not respected: %v", err)
			}
		})
	}
}

func TestNegotiatedProtocolUsedForEveryRequest(t *testing.T) {
	for _, version := range []string{legacyProtocolVersion, protocolVersion} {
		for _, sse := range []bool{false, true} {
			t.Run(version+fmt.Sprint(sse), func(t *testing.T) {
				server, calls := fakeServerProtocol(t, sse, true, listSchema, `{"structuredContent":{"threads":[],"nextCursor":null}}`, version)
				defer server.Close()
				client, err := NewLoopback(server.URL+"/mcp", "test-token")
				if err != nil {
					t.Fatal(err)
				}
				if _, err = client.List(context.Background()); err != nil {
					t.Fatal(err)
				}
				if calls.Load() != 1 {
					t.Fatal("list was not called exactly once")
				}
			})
		}
	}
}
