// Package t3 implements the deliberately narrow, read-only T3 MCP boundary.
package t3

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/agentbridge"
)

const protocolVersion = "2025-03-26"
const maxResponseBytes = 1 << 20

var ErrUnavailable = errors.New("T3 capability unavailable or incompatible")

// Client uses only an operator-configured MCP endpoint; never a model-selected URL.
// Credentials are supplied by the host and are never included in returned errors.
// OAuth enrollment and refresh are deliberately outside this adapter.
type Client struct {
	endpoint, token string
	http            *http.Client
}

func New(endpoint, token string) (*Client, error) { return newClient(endpoint, token, false) }

// NewLoopback allows plain HTTP only to a literal loopback IP, for a connector
// running on the same machine as T3. It does not expose a listener or tunnel.
func NewLoopback(endpoint, token string) (*Client, error) { return newClient(endpoint, token, true) }
func newClient(endpoint, token string, loopback bool) (*Client, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/mcp" {
		return nil, errors.New("T3 endpoint must be an absolute /mcp URL without credentials, query or fragment")
	}
	local := net.ParseIP(u.Hostname())
	if u.Scheme != "https" && !(loopback && u.Scheme == "http" && local != nil && local.IsLoopback()) {
		return nil, errors.New("T3 endpoint requires HTTPS (or explicit literal loopback HTTP)")
	}
	if strings.TrimSpace(token) == "" || strings.ContainsAny(token, "\r\n") {
		return nil, errors.New("T3 bearer token required")
	}
	return &Client{endpoint: endpoint, token: token, http: &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (*Client) Capabilities() agentbridge.Capabilities { return agentbridge.Capabilities{List: true} }

// Read is unavailable: current upstream t3_thread_read marks child results as
// delivered and is explicitly annotated non-read-only. Do not silently weaken
// context-only speech protections to expose it.
func (*Client) Read(context.Context, string) (json.RawMessage, error) { return nil, ErrUnavailable }

// List returns the first bounded page, including provider status metadata and
// nextCursor. It does not claim the first page is the complete thread inventory.
func (c *Client) List(ctx context.Context) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	session := ""
	defer func() { c.closeSession(ctx, session) }()
	init, err := c.rpc(ctx, &session, 1, "initialize", map[string]any{"protocolVersion": protocolVersion, "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "rev-eyes", "version": "0.1"}})
	if err != nil {
		return nil, err
	}
	var initialized struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if json.Unmarshal(init, &initialized) != nil || initialized.ProtocolVersion != protocolVersion {
		return nil, ErrUnavailable
	}
	if _, err = c.rpc(ctx, &session, 0, "notifications/initialized", nil); err != nil {
		return nil, err
	}
	// Discovery is bounded and repeated per request, so stale permissions/schemas
	// are never assumed after a reconnect. No mutation is retried or attempted.
	cursor := ""
	seenCursors := map[string]bool{}
	compatible := false
	for page := 0; page < 10; page++ {
		args := map[string]any{}
		if cursor != "" {
			args["cursor"] = cursor
		}
		raw, e := c.rpc(ctx, &session, 2+page, "tools/list", args)
		if e != nil {
			return nil, e
		}
		var catalog struct {
			Tools []struct {
				Name        string          `json:"name"`
				InputSchema json.RawMessage `json:"inputSchema"`
				Annotations struct {
					ReadOnly bool `json:"readOnlyHint"`
				} `json:"annotations"`
			} `json:"tools"`
			NextCursor string `json:"nextCursor"`
		}
		if json.Unmarshal(raw, &catalog) != nil {
			return nil, ErrUnavailable
		}
		for _, t := range catalog.Tools {
			if t.Name == "t3_thread_list" {
				compatible = t.Annotations.ReadOnly && compatibleListSchema(t.InputSchema)
			}
		}
		if compatible || catalog.NextCursor == "" {
			break
		}
		if seenCursors[catalog.NextCursor] {
			return nil, ErrUnavailable
		}
		seenCursors[catalog.NextCursor] = true
		cursor = catalog.NextCursor
	}
	if !compatible {
		return nil, ErrUnavailable
	}
	raw, err := c.rpc(ctx, &session, 20, "tools/call", map[string]any{"name": "t3_thread_list", "arguments": map[string]any{"allProjects": true, "limit": 20}})
	if err != nil {
		return nil, err
	}
	var result struct {
		IsError    bool            `json:"isError"`
		Structured json.RawMessage `json:"structuredContent"`
		Content    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &result) != nil || result.IsError {
		return nil, errors.New("T3 list failed")
	}
	payload := result.Structured
	if len(payload) == 0 || string(payload) == "null" {
		if len(result.Content) != 1 || result.Content[0].Type != "text" {
			return nil, ErrUnavailable
		}
		payload = json.RawMessage(result.Content[0].Text)
	}
	var page struct {
		Threads json.RawMessage `json:"threads"`
	}
	if json.Unmarshal(payload, &page) != nil || len(page.Threads) == 0 || page.Threads[0] != '[' {
		return nil, ErrUnavailable
	}
	return payload, nil
}

// Fail closed on unknown required parameters and on changed known field types.
// This is compatibility checking, not a general-purpose JSON Schema validator.
func compatibleListSchema(raw json.RawMessage) bool {
	var s struct {
		Type       string `json:"type"`
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if json.Unmarshal(raw, &s) != nil || s.Type != "object" || s.Properties["allProjects"].Type != "boolean" {
		return false
	}
	if typ := s.Properties["limit"].Type; typ != "integer" && typ != "number" {
		return false
	}
	for _, k := range s.Required {
		if k != "allProjects" && k != "limit" {
			return false
		}
	}
	return true
}

func (c *Client) rpc(ctx context.Context, session *string, id int, method string, params any) (json.RawMessage, error) {
	envelope := map[string]any{"jsonrpc": "2.0", "method": method}
	if id != 0 {
		envelope["id"] = id
	}
	if params != nil {
		envelope["params"] = params
	}
	body, _ := json.Marshal(envelope)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("T3 request failed")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if method != "initialize" {
		req.Header.Set("MCP-Protocol-Version", protocolVersion)
	}
	if *session != "" {
		req.Header.Set("Mcp-Session-Id", *session)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, errors.New("T3 transport unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("T3 HTTP status %d", resp.StatusCode)
	}
	if method == "initialize" {
		s := resp.Header.Get("Mcp-Session-Id")
		if len(s) > 256 || strings.ContainsAny(s, "\r\n") {
			return nil, ErrUnavailable
		}
		*session = s
	}
	if id == 0 {
		return nil, nil
	}
	media, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	reader := io.LimitReader(resp.Body, maxResponseBytes+1)
	if media == "application/json" {
		raw, e := io.ReadAll(reader)
		if e != nil || len(raw) > maxResponseBytes {
			return nil, errors.New("T3 response too large or unreadable")
		}
		return decodeResponse(raw, id)
	}
	if media != "text/event-stream" {
		return nil, ErrUnavailable
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), maxResponseBytes)
	var data strings.Builder
	total := 0
	for scanner.Scan() {
		line := scanner.Text()
		total += len(line) + 1
		if total > maxResponseBytes {
			return nil, errors.New("T3 response too large")
		}
		if line == "" {
			if data.Len() > 0 {
				raw := []byte(data.String())
				data.Reset()
				var header struct {
					ID *int `json:"id"`
				}
				if json.Unmarshal(raw, &header) != nil {
					return nil, ErrUnavailable
				}
				if header.ID != nil {
					return decodeResponse(raw, id)
				}
			}
		} else if strings.HasPrefix(line, "data:") {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	return nil, errors.New("T3 event stream ended without response")
}
func decodeResponse(raw []byte, id int) (json.RawMessage, error) {
	var r struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      *int            `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   json.RawMessage `json:"error"`
	}
	if json.Unmarshal(raw, &r) != nil || r.JSONRPC != "2.0" || r.ID == nil || *r.ID != id || len(r.Result) == 0 || (len(r.Error) > 0 && string(r.Error) != "null") {
		return nil, errors.New("T3 RPC failed or incompatible response")
	}
	return r.Result, nil
}

// closeSession releases server-side MCP transport state when supported. A
// failed close never changes the result of a completed read. The parent request
// deadline still applies; no background goroutine survives this request.
func (c *Client) closeSession(ctx context.Context, session string) {
	if session == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.endpoint, nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Mcp-Session-Id", session)
	req.Header.Set("MCP-Protocol-Version", protocolVersion)
	resp, err := c.http.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}
