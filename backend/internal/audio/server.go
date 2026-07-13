package audio

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/rube11/rev-eyes/backend/internal/stt"
)

type Server struct {
	Transcribe stt.Transcriber

	mu          sync.Mutex
	connections map[*websocket.Conn]struct{}
	closing     bool
	handlers    sync.WaitGroup
}

// constructor
func NewServer(transcriber stt.Transcriber) *Server {
	return &Server{
		Transcribe:  transcriber,
		connections: make(map[*websocket.Conn]struct{}),
	}
}

// Shutdown closes active audio connections and waits for their handlers to exit.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.closing = true
	connections := make([]*websocket.Conn, 0, len(s.connections))
	for conn := range s.connections {
		connections = append(connections, conn)
	}
	s.mu.Unlock()

	for _, conn := range connections {
		_ = conn.Close()
	}

	done := make(chan struct{})
	go func() {
		s.handlers.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Server) registerConnection(conn *websocket.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closing {
		return false
	}

	s.connections[conn] = struct{}{}
	s.handlers.Add(1)
	return true
}

func (s *Server) unregisterConnection(conn *websocket.Conn) {
	s.mu.Lock()
	delete(s.connections, conn)
	s.mu.Unlock()
	s.handlers.Done()
}

/*
function to receive audio from the connection and store it inside
a []byte channel so other go routines can access audio data and send
it to an stt provider
*/
func readAudio(ctx context.Context, conn *websocket.Conn, audioChan chan<- []byte) {
	defer close(audioChan)

	for {
		message, data, err := conn.ReadMessage()

		if err != nil {
			fmt.Println(err)
			return
		}

		// skip message types that arent binary since our channel is only a byte arr
		if message != websocket.BinaryMessage {
			continue
		}

		select {
		case audioChan <- data:

		// check if the context was cancelled by other go routines
		case <-ctx.Done():
			return
		}
	}
}

// upgrader to upgrade http request to a socket connection
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     isLocalOrigin,
}

func isLocalOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}

	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}

	switch parsed.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

// Handler returns the HTTP handler for the audio WebSocket endpoint.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleWS)
	return mux
}

// opens the websocket connection and handles streaming data to an stt
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Println(err)
		return
	}
	if !s.registerConnection(conn) {
		_ = conn.Close()
		return
	}
	defer s.unregisterConnection(conn)
	defer conn.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	audioChan := make(chan []byte, 100)

	/*
	    received message is a byte slice becasue we are receiving data
	   from our connection in the form of a uint8array
	*/
	go readAudio(ctx, conn, audioChan)

	if err := s.Transcribe.Transcribe(ctx, audioChan); err != nil {
		fmt.Println("transcription error:", err)
	}
}
