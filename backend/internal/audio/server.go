package audio

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gorilla/websocket"
	"github.com/rube11/rev-eyes/backend/internal/stt"
)

type Server struct {
	Transcribe stt.Transcriber
}

// constructor
func NewServer(transcriber stt.Transcriber) *Server {
	return &Server{Transcribe: transcriber}
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
}

// opens the websocket connection and handles streaming data to an stt
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Println(err)
		return
	}
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
