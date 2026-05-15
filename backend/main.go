package main

import (
	"github.com/rube11/rev-eyes/backend/internal/audio"
	"github.com/rube11/rev-eyes/backend/internal/stt"
)

func main() {
	transcriber := &stt.NoopTranscriber{}
	server := audio.NewServer(transcriber)
	_ = server
}
