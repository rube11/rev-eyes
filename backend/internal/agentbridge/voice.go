package agentbridge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/rube11/rev-eyes/backend/internal/realtime"
	"github.com/rube11/rev-eyes/backend/internal/stt"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

func readControlBody(w http.ResponseWriter, r *http.Request) (json.RawMessage, error) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 20000))
	return json.RawMessage(data), err
}
func controlError(w http.ResponseWriter, status int, code string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

// VoiceHandler binds the exact target for the lifetime of one authenticated
// socket. It has no ambient wake listener and cannot route speech through Eyes.
// It shares the shutdown hub, but never sends T3 credentials to the client.
func (g *Gateway) VoiceHandler(transcriber stt.Transcriber, hub *realtime.Hub, authenticate realtime.Authenticator, checkOrigin func(*http.Request) bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connectionID, threadID := r.URL.Query().Get("connection_id"), r.URL.Query().Get("thread_id")
		if !validID(connectionID) || !validID(threadID) {
			http.Error(w, "invalid target", http.StatusBadRequest)
			return
		}
		server := realtime.NewServerWithHub(transcriber, hub, realtime.Handlers{
			DisableRepeat:   true,
			SingleUtterance: true,
			Authenticate: func(ticket string) (tool.Scope, error) {
				scope, err := authenticate(ticket)
				if err != nil || !g.CanSend(scope.UserID, connectionID) {
					return tool.Scope{}, ErrUnavailable
				}
				return scope, nil
			},
			CheckOrigin: checkOrigin,
			Utterance: func(ctx context.Context, scope tool.Scope, text string) (realtime.UtteranceResult, error) {
				if scope.Speech.ContextOnly() {
					return realtime.UtteranceResult{Text: "Speech was not sent. Tap and speak your own message."}, nil
				}
				var nonce [16]byte
				if _, err := rand.Read(nonce[:]); err != nil {
					return realtime.UtteranceResult{}, err
				}
				_, err := g.Send(ctx, scope.UserID, connectionID, threadID, text, hex.EncodeToString(nonce[:]))
				message := "Message queued in your T3 thread. Open the thread to check its reply."
				if errors.Is(err, ErrDeliveryUnknown) {
					message = "Delivery is uncertain. Check the T3 thread before sending again."
				} else if err != nil {
					message = "Message was not sent. Check the T3 connection and its permissions."
				}
				return realtime.UtteranceResult{Text: message}, nil
			},
		})
		server.ServeHTTP(w, r)
	})
}
