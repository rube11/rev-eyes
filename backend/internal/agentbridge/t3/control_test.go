package t3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"errors"
	"github.com/rube11/rev-eyes/backend/internal/agentbridge"
)

func TestSendDiscoversSchemaAndUsesQueueAndStableKey(t *testing.T) {
	for _, tc := range []struct {
		name, schema string
		fail         bool
		wantCalls    int
		unknown      bool
	}{
		{"compatible", `{"type":"object","properties":{"threadId":{"type":"string"},"message":{"type":"string"},"mode":{"type":"string","enum":["auto","queue"]},"clientRequestId":{"type":"string"}},"required":["threadId","message"]}`, false, 1, false},
		{"old server without idempotency", `{"type":"object","properties":{"threadId":{"type":"string"},"message":{"type":"string"},"mode":{"type":"string"}}}`, false, 0, false},
		{"response lost", `{"type":"object","properties":{"threadId":{"type":"string"},"message":{"type":"string"},"mode":{"type":"string"},"clientRequestId":{"type":"string"}}}`, true, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					ID     int    `json:"id"`
					Method string `json:"method"`
					Params struct {
						Name      string         `json:"name"`
						Arguments map[string]any `json:"arguments"`
					} `json:"params"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					return
				}
				var result string
				switch req.Method {
				case "initialize":
					result = `{"protocolVersion":"2025-03-26"}`
				case "notifications/initialized":
					w.WriteHeader(202)
					return
				case "tools/list":
					result = fmt.Sprintf(`{"tools":[{"name":"t3_thread_send","inputSchema":%s}]}`, tc.schema)
				case "tools/call":
					calls.Add(1)
					if req.Params.Name != "t3_thread_send" || req.Params.Arguments["mode"] != "queue" || req.Params.Arguments["clientRequestId"] != "stable-key" || req.Params.Arguments["threadId"] != "mcp:launch:target" || req.Params.Arguments["message"] != "fix it" {
						t.Errorf("wrong arguments %+v", req.Params)
					}
					if tc.fail {
						w.WriteHeader(502)
						return
					}
					result = `{"structuredContent":{"threadId":"mcp:launch:target","messageId":"m","runId":"r","delivery":"queued"}}`
				default:
					t.Errorf("unexpected %s", req.Method)
					w.WriteHeader(400)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":%s}`, req.ID, result)
			}))
			defer server.Close()
			client, _ := NewLoopback(server.URL+"/mcp", "test-token")
			if _, err := client.Send(context.Background(), "id", "message", "id"); err == nil || calls.Load() != 0 {
				t.Fatal("control enabled by default")
			}
			client.WithControl(true)
			_, err := client.Send(context.Background(), "mcp:launch:target", "fix it", "stable-key")
			if calls.Load() != int32(tc.wantCalls) || errors.Is(err, agentbridge.ErrDeliveryUnknown) != tc.unknown {
				t.Fatalf("calls=%d err=%v", calls.Load(), err)
			}
			if tc.name == "compatible" && err != nil {
				t.Fatal(err)
			}
			if tc.wantCalls == 0 && err == nil {
				t.Fatal("incompatible schema accepted")
			}
		})
	}
}
