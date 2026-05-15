package audio

import (
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

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)

	if err != nil {
		fmt.Println(err)
		return
	}
	defer conn.Close()
	receivedMsg := []int{}
	for {
		message, data, err := conn.ReadMessage()
		if len(receivedMsg) == 0 {
			receivedMsg = append(receivedMsg, message)
		}
	}

}
