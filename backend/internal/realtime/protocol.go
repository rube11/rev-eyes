package realtime

import (
	"context"
	"net/http"

	"github.com/rube11/rev-eyes/backend/internal/tool"
)

const (
	locationMessageType       = "location"
	listeningStartMessageType = "listening_start"
	listeningStopMessageType  = "listening_stop"

	assistantDoneMessageType     = "assistant_done"
	assistantRepeatMessageType   = "assistant_repeat"
	assistantResponseMessageType = "assistant_response"
	assistantThinkingMessageType = "assistant_thinking"
	listeningStoppedMessageType  = "listening_stopped"
	notificationMessageType      = "notification"
	notificationAckMessageType   = "notification_ack"
	userTranscriptMessageType    = "user_transcript"
	workspaceChangedMessageType  = "workspace_changed"
)

type WorkspaceResource string

const (
	WorkspaceConversations WorkspaceResource = "conversations"
	WorkspaceMemories      WorkspaceResource = "memories"
	WorkspaceWatches       WorkspaceResource = "watches"
	WorkspaceTasks         WorkspaceResource = "tasks"
)

type Authenticator func(ticket string) (tool.Scope, error)
type UtteranceResult struct {
	Text                 string
	AwaitingConfirmation bool
	WorkspaceResources   []WorkspaceResource
}

type UtteranceHandler func(
	ctx context.Context,
	scope tool.Scope,
	utterance string,
) (UtteranceResult, error)
type LocationHandler func(ctx context.Context, scope tool.Scope, update LocationUpdate) error
type NotificationAckHandler func(ctx context.Context, scope tool.Scope, notificationID string) error
type Handlers struct {
	Authenticate    Authenticator
	CheckOrigin     func(r *http.Request) bool
	Connect         func(ctx context.Context, scope tool.Scope) error
	PrepareSession  func(ctx context.Context, scope tool.Scope) error
	Utterance       UtteranceHandler
	Location        LocationHandler
	NotificationAck NotificationAckHandler
	Disconnect      func(scope tool.Scope)
}

type LocationUpdate struct {
	Latitude       float64 `json:"latitude"`
	Longitude      float64 `json:"longitude"`
	AccuracyMeters float64 `json:"accuracy_meters,omitempty"`
}

type clientMessage struct {
	Type string `json:"type"`
	ID   string `json:"id,omitempty"`
	LocationUpdate
}

type serverMessage struct {
	Type                 string              `json:"type"`
	ID                   string              `json:"id,omitempty"`
	Text                 string              `json:"text,omitempty"`
	Error                string              `json:"error,omitempty"`
	AwaitingConfirmation bool                `json:"awaiting_confirmation,omitempty"`
	Resources            []WorkspaceResource `json:"resources,omitempty"`
}

type jsonWriter interface {
	WriteJSON(value any) error
}
