package main

import (
	"errors"
	"strings"

	"github.com/rube11/rev-eyes/backend/internal/agentbridge"
	"github.com/rube11/rev-eyes/backend/internal/agentbridge/t3"
)

// agentGatewayFromEnvironment configures one owner-bound connection. Credentials
// remain server-side; this does not pair a laptop or expose a local listener.
func agentGatewayFromEnvironment(getenv func(string) string) (*agentbridge.Gateway, error) {
	endpoint := strings.TrimSpace(getenv("AGENT_T3_MCP_URL"))
	token := strings.TrimSpace(getenv("AGENT_T3_MCP_TOKEN"))
	owner := strings.TrimSpace(getenv("AGENT_T3_OWNER_USER_ID"))
	if endpoint == "" && token == "" && owner == "" {
		return agentbridge.New(nil)
	}
	if endpoint == "" || token == "" || owner == "" {
		return nil, errors.New("agent T3 configuration requires URL, token, and owner user ID")
	}
	client, err := t3.New(endpoint, token)
	if err != nil {
		return nil, errors.New("invalid agent T3 configuration")
	}
	control := strings.TrimSpace(getenv("AGENT_T3_CONTROL_ENABLED"))
	if control != "" && control != "true" && control != "false" {
		return nil, errors.New("AGENT_T3_CONTROL_ENABLED must be true or false")
	}
	client.WithControl(control == "true")
	gateway, err := agentbridge.New([]agentbridge.Connection{{ID: "t3-laptop", OwnerID: owner, ProviderName: "t3", Provider: client}})
	if err != nil {
		return nil, err
	}
	return gateway, nil
}
