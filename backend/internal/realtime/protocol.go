package realtime

import (
	"context"
	"net/http"

	"github.com/rube11/rev-eyes/backend/internal/ambient"
	"github.com/rube11/rev-eyes/backend/internal/stt"
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
type CandidateAudioHandler func(
	ctx context.Context,
	audio []byte,
	format stt.AudioFormat,
) (string, error)

type AmbientListener func(context.Context, <-chan ambient.Input, func(ambient.Clip)) error

type Handlers struct {
	ConversationTranscriber stt.ConversationTranscriber
	Ambient                 AmbientListener
	AmbientStreaming        func(context.Context, <-chan ambient.Input, ambient.Conversation) error
	CandidateAudio          CandidateAudioHandler
	CandidateMaxConcurrent  int
	Authenticate            Authenticator
	CheckOrigin             func(r *http.Request) bool
	Connect                 func(ctx context.Context, scope tool.Scope) error
	PrepareSession          func(ctx context.Context, scope tool.Scope) error
	Utterance               UtteranceHandler
	Location                LocationHandler
	NotificationAck         NotificationAckHandler
	Disconnect              func(scope tool.Scope)
}

type LocationUpdate struct {
	Latitude       float64 `json:"latitude"`
	Longitude      float64 `json:"longitude"`
	AccuracyMeters float64 `json:"accuracy_meters,omitempty"`
}

type clientMessage struct {
	Type              string  `json:"type"`
	ID                string  `json:"id,omitempty"`
	Encoding          string  `json:"encoding,omitempty"`
	SampleRate        int     `json:"sample_rate,omitempty"`
	Channels          int     `json:"channels,omitempty"`
	ByteLength        int     `json:"byte_length,omitempty"`
	StartSampleOffset int64   `json:"start_sample_offset,omitempty"`
	EndSampleOffset   int64   `json:"end_sample_offset,omitempty"`
	GateCategory      string  `json:"gate_category,omitempty"`
	GateConfidence    float64 `json:"gate_confidence,omitempty"`
	LocationUpdate
}

func (message clientMessage) candidateHeader() candidateAudioHeader {
	return candidateAudioHeader{
		ID:                message.ID,
		Encoding:          message.Encoding,
		SampleRate:        message.SampleRate,
		Channels:          message.Channels,
		ByteLength:        message.ByteLength,
		StartSampleOffset: message.StartSampleOffset,
		EndSampleOffset:   message.EndSampleOffset,
		GateCategory:      message.GateCategory,
		GateConfidence:    message.GateConfidence,
	}
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
