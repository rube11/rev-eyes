// Package t3 implements the owner-configured T3 MCP boundary.
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

const protocolVersion = "2025-06-18"
const legacyProtocolVersion = "2025-03-26"

type mcpSession struct {
	id       string
	protocol string
}

const maxResponseBytes = 1 << 20

var ErrUnavailable = errors.New("T3 capability unavailable or incompatible")

// Client uses only an operator-configured MCP endpoint; never a model-selected URL.
// Credentials are supplied by the host and are never included in returned errors.
// OAuth enrollment and refresh are deliberately outside this adapter.
type Client struct {
	endpoint, token string
	http            *http.Client
	control         bool
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

// WithControl opts in to explicit UI/gesture actions. It does not register model tools.
func (c *Client) WithControl(enabled bool) *Client { c.control = enabled; return c }
func (c *Client) Capabilities() agentbridge.Capabilities {
	return agentbridge.Capabilities{List: true, Send: c.control, Open: c.control}
}

// Read is unavailable: current upstream t3_thread_read marks child results as
// delivered and is explicitly annotated non-read-only. Do not silently weaken
// context-only speech protections to expose it.
func (*Client) Read(context.Context, string) (json.RawMessage, error) { return nil, ErrUnavailable }

// List returns the first bounded page, including provider status metadata and
// nextCursor. It does not claim the first page is the complete thread inventory.
func (c *Client) List(ctx context.Context) (json.RawMessage, error) { return c.ListPage(ctx, 0) }

func (c *Client) ListPage(ctx context.Context, cursor int) (json.RawMessage, error) {
	args := map[string]any{"allProjects": true, "limit": 20}
	if cursor > 0 {
		args["cursor"] = cursor
	}
	return c.call(ctx, "t3_thread_list", args, func(raw json.RawMessage, readOnly bool) bool {
		return readOnly && compatibleListSchema(raw) && (cursor == 0 || schemaAccepts(raw, map[string]string{"cursor": "number"}, nil))
	}, nil)
}

func (c *Client) Send(ctx context.Context, threadID, message, requestID string) (json.RawMessage, error) {
	if !c.control {
		return nil, ErrUnavailable
	}
	dispatched := false
	data, err := c.call(ctx, "t3_thread_send", map[string]any{"threadId": threadID, "message": message, "clientRequestId": requestID, "mode": "queue"}, func(raw json.RawMessage, _ bool) bool {
		return schemaAccepts(raw, map[string]string{"threadId": "string", "message": "string", "clientRequestId": "string", "mode": "string"}, []string{"threadId", "message", "clientRequestId", "mode"})
	}, &dispatched)
	if err != nil && dispatched {
		return nil, agentbridge.ErrDeliveryUnknown
	}
	return data, err
}

// Open is an explicit user action, never a context-only/read-only model tool:
// upstream reading may acknowledge delivery of a child result.
func (c *Client) Open(ctx context.Context, threadID string) (json.RawMessage, error) {
	return c.OpenPage(ctx, threadID, -1)
}
func (c *Client) OpenPage(ctx context.Context, threadID string, after int) (json.RawMessage, error) {
	if !c.control {
		return nil, ErrUnavailable
	}
	args := map[string]any{"threadId": threadID, "view": "messages", "limit": 20, "maxCharsPerItem": 2000}
	fields := map[string]string{"threadId": "string", "view": "string", "limit": "number", "maxCharsPerItem": "number"}
	if after >= 0 {
		args["afterPosition"] = after
		fields["afterPosition"] = "number"
	}
	return c.call(ctx, "t3_thread_read", args, func(raw json.RawMessage, _ bool) bool {
		return schemaAccepts(raw, fields, []string{"threadId", "view", "limit", "maxCharsPerItem", "afterPosition"})
	}, nil)
}

func schemaAccepts(raw json.RawMessage, fields map[string]string, allowedRequired []string) bool {
	var s struct {
		Type       string `json:"type"`
		Properties map[string]struct {
			Type string   `json:"type"`
			Enum []string `json:"enum"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if json.Unmarshal(raw, &s) != nil || s.Type != "object" {
		return false
	}
	for name, typ := range fields {
		p := s.Properties[name]
		if p.Type != typ && !(typ == "number" && p.Type == "integer") {
			return false
		}
		if name == "mode" && len(p.Enum) > 0 {
			found := false
			for _, value := range p.Enum {
				found = found || value == "queue"
			}
			if !found {
				return false
			}
		}
	}
	if allowedRequired != nil {
		for _, name := range s.Required {
			found := false
			for _, allowed := range allowedRequired {
				found = found || name == allowed
			}
			if !found {
				return false
			}
		}
	}
	return true
}

// Every invocation refreshes discovery and authorization. No automatic retries.
func (c *Client) call(ctx context.Context, name string, args map[string]any, compatible func(json.RawMessage, bool) bool, dispatched *bool) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	session := mcpSession{protocol: protocolVersion}
	defer func() { c.closeSession(ctx, session) }()
	init, err := c.rpc(ctx, &session, 1, "initialize", map[string]any{"protocolVersion": protocolVersion, "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "rev-eyes", "version": "0.2"}})
	if err != nil {
		return nil, err
	}
	var initialized struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if json.Unmarshal(init, &initialized) != nil || (initialized.ProtocolVersion != protocolVersion && initialized.ProtocolVersion != legacyProtocolVersion) {
		return nil, ErrUnavailable
	}
	session.protocol = initialized.ProtocolVersion
	if _, err = c.rpc(ctx, &session, 0, "notifications/initialized", nil); err != nil {
		return nil, err
	}
	cursor := ""
	seen := map[string]bool{}
	found := false
	for page := 0; page < 10; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, e := c.rpc(ctx, &session, 2+page, "tools/list", params)
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
		for _, tool := range catalog.Tools {
			if tool.Name == name {
				found = compatible(tool.InputSchema, tool.Annotations.ReadOnly)
			}
		}
		if found || catalog.NextCursor == "" {
			break
		}
		if seen[catalog.NextCursor] {
			return nil, ErrUnavailable
		}
		seen[catalog.NextCursor] = true
		cursor = catalog.NextCursor
	}
	if !found {
		return nil, ErrUnavailable
	}
	if dispatched != nil {
		*dispatched = true
	}
	raw, err := c.rpc(ctx, &session, 20, "tools/call", map[string]any{"name": name, "arguments": args})
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
		return nil, ErrUnavailable
	}
	payload := result.Structured
	if len(payload) == 0 || string(payload) == "null" {
		if len(result.Content) != 1 || result.Content[0].Type != "text" {
			return nil, ErrUnavailable
		}
		payload = json.RawMessage(result.Content[0].Text)
	}
	if !json.Valid(payload) || len(payload) > 128*1024 {
		return nil, ErrUnavailable
	}
	if name == "t3_thread_list" {
		var p struct {
			Threads json.RawMessage `json:"threads"`
		}
		if json.Unmarshal(payload, &p) != nil || len(p.Threads) == 0 || p.Threads[0] != '[' {
			return nil, ErrUnavailable
		}
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

func (c *Client) rpc(ctx context.Context, session *mcpSession, id int, method string, params any) (json.RawMessage, error) {
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
		req.Header.Set("MCP-Protocol-Version", session.protocol)
	}
	if session.id != "" {
		req.Header.Set("Mcp-Session-Id", session.id)
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
		session.id = s
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
func (c *Client) closeSession(ctx context.Context, session mcpSession) {
	if session.id == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.endpoint, nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Mcp-Session-Id", session.id)
	req.Header.Set("MCP-Protocol-Version", session.protocol)
	resp, err := c.http.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}
