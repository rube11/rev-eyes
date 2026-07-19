package audio

import (
	"context"
	"sort"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/stt"
)

type echoTranscriber struct{}

func (echoTranscriber) Transcribe(
	ctx context.Context,
	audio <-chan []byte,
	completed chan<- string,
) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case chunk, ok := <-audio:
			if !ok {
				return nil
			}
			select {
			case completed <- string(chunk):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

func TestTranscribeConnectionKeepsUtterancesWithConnectionContext(t *testing.T) {
	t.Parallel()

	type connectionKey struct{}
	received := make(chan string, 4)
	server := NewServer(echoTranscriber{}, func(ctx context.Context, utterance string) {
		connection := ctx.Value(connectionKey{}).(string)
		received <- connection + ":" + utterance
	})

	audioA := make(chan []byte, 2)
	audioA <- []byte("first-a")
	audioA <- []byte("second-a")
	close(audioA)

	audioB := make(chan []byte, 2)
	audioB <- []byte("first-b")
	audioB <- []byte("second-b")
	close(audioB)

	errors := make(chan error, 2)
	go func() {
		ctx := context.WithValue(context.Background(), connectionKey{}, "a")
		errors <- server.transcribeConnection(ctx, audioA)
	}()
	go func() {
		ctx := context.WithValue(context.Background(), connectionKey{}, "b")
		errors <- server.transcribeConnection(ctx, audioB)
	}()

	for range 2 {
		if err := <-errors; err != nil {
			t.Fatalf("transcribeConnection() error = %v", err)
		}
	}

	utterances := make([]string, 0, 4)
	for range 4 {
		utterances = append(utterances, <-received)
	}
	sort.Strings(utterances)

	want := []string{"a:first-a", "a:second-a", "b:first-b", "b:second-b"}
	for i := range want {
		if utterances[i] != want[i] {
			t.Fatalf("utterances[%d] = %q, want %q", i, utterances[i], want[i])
		}
	}
}

var _ stt.Transcriber = echoTranscriber{}
