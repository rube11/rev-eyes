package t3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func labelServer(t *testing.T, tools string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var result string
		switch req.Method {
		case "initialize":
			result = `{"protocolVersion":"2025-03-26"}`
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
			return
		case "tools/list":
			result = tools
		case "tools/call":
			calls.Add(1)
			result = `{"structuredContent":{"environmentId":"env","label":"Dubs","serverVersion":"0.1.5"}}`
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":%s}`, req.ID, result)
	})), &calls
}

func TestLabelReadsAndCachesEnvironmentName(t *testing.T) {
	server, calls := labelServer(t, `{"tools":[{"name":"t3_environment_read","inputSchema":{"type":"object","properties":{"environmentId":{"type":"string"}}}}]}`)
	defer server.Close()
	c, err := NewLoopback(server.URL+"/mcp", "test-token")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if label, err := c.Label(context.Background()); err != nil || label != "Dubs" {
			t.Fatalf("label = %q, %v", label, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("label was not cached: %d calls", calls.Load())
	}
}

func TestLabelIsEmptyWhenToolMissingOrNeedsArguments(t *testing.T) {
	for name, tools := range map[string]string{
		"missing":  `{"tools":[]}`,
		"required": `{"tools":[{"name":"t3_environment_read","inputSchema":{"type":"object","properties":{"x":{"type":"string"}},"required":["x"]}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			server, calls := labelServer(t, tools)
			defer server.Close()
			c, _ := NewLoopback(server.URL+"/mcp", "test-token")
			if label, _ := c.Label(context.Background()); label != "" || calls.Load() != 0 {
				t.Fatalf("label = %q after %d calls", label, calls.Load())
			}
		})
	}
}
