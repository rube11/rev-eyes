package main

import (
	"os"

	"github.com/rube11/rev-eyes/backend/internal/audio"
	"github.com/rube11/rev-eyes/backend/internal/stt"
)

func main() {
	transcriber := stt.NewDeepGramTranscriber(os.Getenv("DEEPGRAM_API_KEY"))
	server := audio.NewServer(transcriber)
	_ = server
}
