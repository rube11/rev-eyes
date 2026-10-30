package main

import "testing"

func TestAgentToolsConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name      string
		values    map[string]string
		wantError bool
		wantTools int
	}{
		{name: "disabled"},
		{name: "partial", values: map[string]string{"AGENT_T3_MCP_URL": "https://example.test/mcp"}, wantError: true},
		{name: "insecure", values: map[string]string{"AGENT_T3_MCP_URL": "http://example.test/mcp", "AGENT_T3_MCP_TOKEN": "test-only", "AGENT_T3_OWNER_USER_ID": "owner"}, wantError: true},
		{name: "explicit control still registers only a status tool", values: map[string]string{"AGENT_T3_MCP_URL": "https://example.test/mcp", "AGENT_T3_MCP_TOKEN": "test-only", "AGENT_T3_OWNER_USER_ID": "owner", "AGENT_T3_CONTROL_ENABLED": "true"}, wantTools: 1},
		{name: "invalid control switch", values: map[string]string{"AGENT_T3_MCP_URL": "https://example.test/mcp", "AGENT_T3_MCP_TOKEN": "test-only", "AGENT_T3_OWNER_USER_ID": "owner", "AGENT_T3_CONTROL_ENABLED": "yes"}, wantError: true},
		{name: "configured without contacting provider", values: map[string]string{"AGENT_T3_MCP_URL": "https://example.test/mcp", "AGENT_T3_MCP_TOKEN": "test-only", "AGENT_T3_OWNER_USER_ID": "owner"}, wantTools: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gateway, err := agentGatewayFromEnvironment(func(key string) string { return tc.values[key] })
			if (err != nil) != tc.wantError {
				t.Fatalf("error=%v", err)
			}
			if err != nil {
				return
			}
			tools := gateway.Tools()
			if len(tools) != tc.wantTools {
				t.Fatalf("tools=%d", len(tools))
			}
			for _, tool := range tools {
				if !tool.Spec().ReadOnly || tool.Spec().Name != "list_agent_threads" {
					t.Fatalf("unexpected capability: %+v", tool.Spec())
				}
			}
		})
	}
}
