package realtime

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
)

type connection struct {
	socket  *websocket.Conn
	userID  string
	writeMu sync.Mutex
}

func (c *connection) WriteJSON(value any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.socket.WriteJSON(value)
}

// Hub tracks active connections for user-targeted realtime delivery.
type Hub struct {
	mu          sync.Mutex
	connections map[string]map[*connection]struct{}
	closing     bool
	active      sync.WaitGroup
}

func NewHub() *Hub {
	return &Hub{connections: make(map[string]map[*connection]struct{})}
}

// Send delivers a notification and reports whether any active connection received it.
func (h *Hub) Send(userID, notificationID, text string) bool {
	userID = strings.TrimSpace(userID)
	notificationID = strings.TrimSpace(notificationID)
	text = strings.TrimSpace(text)
	if userID == "" || notificationID == "" || text == "" {
		return false
	}

	h.mu.Lock()
	if h.closing {
		h.mu.Unlock()
		return false
	}
	connections := make([]*connection, 0, len(h.connections[userID]))
	for conn := range h.connections[userID] {
		connections = append(connections, conn)
	}
	h.mu.Unlock()

	delivered := false
	for _, conn := range connections {
		if err := conn.WriteJSON(serverMessage{
			Type: notificationMessageType,
			ID:   notificationID,
			Text: text,
		}); err != nil {
			slog.Debug("failed to send realtime message", "user_id", userID, "error", err)
			_ = conn.socket.Close()
			continue
		}
		delivered = true
	}
	return delivered
}

func (h *Hub) register(conn *connection) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closing {
		return false
	}
	if h.connections[conn.userID] == nil {
		h.connections[conn.userID] = make(map[*connection]struct{})
	}
	h.connections[conn.userID][conn] = struct{}{}
	h.active.Add(1)
	return true
}

func (h *Hub) unregister(conn *connection) {
	h.mu.Lock()
	delete(h.connections[conn.userID], conn)
	if len(h.connections[conn.userID]) == 0 {
		delete(h.connections, conn.userID)
	}
	h.mu.Unlock()
	h.active.Done()
}

// Shutdown closes active connections and waits for their handlers to exit.
func (h *Hub) Shutdown(ctx context.Context) error {
	h.mu.Lock()
	h.closing = true
	var connections []*connection
	for _, userConnections := range h.connections {
		for conn := range userConnections {
			connections = append(connections, conn)
		}
	}
	h.mu.Unlock()

	for _, conn := range connections {
		_ = conn.socket.Close()
	}

	done := make(chan struct{})
	go func() {
		h.active.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
