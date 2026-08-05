package realtime

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/stt"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

type channelJSONWriter chan serverMessage

func (writer channelJSONWriter) WriteJSON(value any) error {
	message, ok := value.(serverMessage)
	if !ok {
		return fmt.Errorf("unexpected message type %T", value)
	}
	writer <- message
	return nil
}

func TestCandidateAdmissionBoundsRetainedAudio(t *testing.T) {
	server := NewServer(echoTranscriber{}, Handlers{
		CandidateMaxConcurrent: 1,
		CandidateAudio: func(
			context.Context,
			[]byte,
			stt.AudioFormat,
		) (string, error) {
			return "", nil
		},
	})

	first := testCandidateJob("candidate-1", []byte{1, 2})
	second := testCandidateJob("candidate-2", []byte{3, 4})
	third := testCandidateJob("candidate-3", []byte{5, 6})
	if !server.tryAdmitCandidate(&first) || !server.tryAdmitCandidate(&second) {
		t.Fatal("candidate admission rejected work below the global bound")
	}
	if server.tryAdmitCandidate(&third) {
		t.Fatal("candidate admission exceeded the global retained-audio bound")
	}

	server.releaseCandidateAdmission(first)
	if !server.tryAdmitCandidate(&third) {
		t.Fatal("candidate admission did not reopen after release")
	}
	server.releaseCandidateAdmission(second)
	server.releaseCandidateAdmission(third)
	if got := len(server.candidateAdmissions); got != 0 {
		t.Fatalf("candidate admissions retained %d slots", got)
	}
}

func TestCandidateProcessingDeadlineCancelsWorkAndSendsDone(t *testing.T) {
	handlerCanceled := make(chan struct{})
	server := NewServer(echoTranscriber{}, Handlers{
		CandidateMaxConcurrent: 1,
		CandidateAudio: func(
			ctx context.Context,
			_ []byte,
			_ stt.AudioFormat,
		) (string, error) {
			<-ctx.Done()
			close(handlerCanceled)
			return "", ctx.Err()
		},
	})
	server.candidateTimeout = 20 * time.Millisecond

	audio := []byte{1, 2, 3, 4}
	job := testCandidateJob("candidate-timeout", audio)
	job.acceptedAt = time.Now()
	if !server.tryAdmitCandidate(&job) {
		t.Fatal("candidate admission failed")
	}
	writer := make(channelJSONWriter, 1)
	server.processCandidateBeforeDeadline(
		context.Background(),
		tool.Scope{},
		writer,
		job,
	)

	receive(t, handlerCanceled)
	message := receive(t, writer)
	if message.Type != assistantDoneMessageType || message.ID != job.header.ID {
		t.Fatalf("timeout message = %+v", message)
	}
	select {
	case duplicate := <-writer:
		t.Fatalf("candidate timeout sent duplicate terminal message: %+v", duplicate)
	default:
	}
	if !allZero(audio) {
		t.Fatal("timed-out candidate audio was not cleared")
	}
	if got := len(server.candidateAdmissions); got != 0 {
		t.Fatalf("candidate timeout retained %d admission slots", got)
	}
}

func TestExpiredQueuedCandidateNeverReachesHandler(t *testing.T) {
	called := make(chan struct{}, 1)
	server := NewServer(echoTranscriber{}, Handlers{
		CandidateMaxConcurrent: 1,
		CandidateAudio: func(
			context.Context,
			[]byte,
			stt.AudioFormat,
		) (string, error) {
			called <- struct{}{}
			return "", nil
		},
	})
	server.candidateTimeout = 20 * time.Millisecond

	audio := []byte{1, 2}
	job := testCandidateJob("candidate-expired", audio)
	job.acceptedAt = time.Now().Add(-time.Second)
	writer := make(channelJSONWriter, 1)
	server.processCandidateBeforeDeadline(
		context.Background(),
		tool.Scope{},
		writer,
		job,
	)

	message := receive(t, writer)
	if message.Type != assistantDoneMessageType || message.ID != job.header.ID {
		t.Fatalf("expired candidate message = %+v", message)
	}
	select {
	case <-called:
		t.Fatal("expired candidate reached the audio handler")
	default:
	}
	if !allZero(audio) {
		t.Fatal("expired candidate audio was not cleared")
	}
}

func TestCandidateConcurrencyCoversTranscriptionAndUtteranceHandling(t *testing.T) {
	transcriptions := make(chan byte, 2)
	utterances := make(chan struct{}, 2)
	releaseUtterances := make(chan struct{})
	server := NewServer(echoTranscriber{}, Handlers{
		CandidateMaxConcurrent: 1,
		CandidateAudio: func(
			_ context.Context,
			audio []byte,
			_ stt.AudioFormat,
		) (string, error) {
			transcriptions <- audio[0]
			return "candidate transcript", nil
		},
		Utterance: func(
			_ context.Context,
			_ tool.Scope,
			_ string,
		) (UtteranceResult, error) {
			utterances <- struct{}{}
			<-releaseUtterances
			return UtteranceResult{}, nil
		},
	})

	firstAudio := []byte{1, 2}
	secondAudio := []byte{2, 3}
	done := make(chan struct{}, 2)
	go func() {
		server.processCandidate(
			context.Background(),
			tool.Scope{},
			discardJSONWriter{},
			testCandidateJob("candidate-1", firstAudio),
		)
		done <- struct{}{}
	}()
	if got := receive(t, transcriptions); got != 1 {
		t.Fatalf("first transcription audio marker = %d, want 1", got)
	}
	receive(t, utterances)

	go func() {
		server.processCandidate(
			context.Background(),
			tool.Scope{},
			discardJSONWriter{},
			testCandidateJob("candidate-2", secondAudio),
		)
		done <- struct{}{}
	}()
	select {
	case marker := <-transcriptions:
		t.Fatalf("second transcription (%d) started while the first utterance was active", marker)
	case <-time.After(30 * time.Millisecond):
	}

	close(releaseUtterances)
	if got := receive(t, transcriptions); got != 2 {
		t.Fatalf("second transcription audio marker = %d, want 2", got)
	}
	receive(t, utterances)
	receive(t, done)
	receive(t, done)
	if !allZero(firstAudio) || !allZero(secondAudio) {
		t.Fatal("candidate audio was not cleared after processing")
	}
}

func TestCandidateCanceledWhileWaitingForPermitClearsAudio(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	server := NewServer(echoTranscriber{}, Handlers{
		CandidateMaxConcurrent: 1,
		CandidateAudio: func(
			ctx context.Context,
			_ []byte,
			_ stt.AudioFormat,
		) (string, error) {
			started <- struct{}{}
			select {
			case <-release:
				return "", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		},
	})

	firstDone := make(chan struct{})
	go func() {
		server.processCandidate(
			context.Background(),
			tool.Scope{},
			discardJSONWriter{},
			testCandidateJob("candidate-1", []byte{1, 2}),
		)
		close(firstDone)
	}()
	receive(t, started)

	ctx, cancel := context.WithCancel(context.Background())
	secondAudio := []byte{3, 4}
	secondDone := make(chan struct{})
	go func() {
		server.processCandidate(
			ctx,
			tool.Scope{},
			discardJSONWriter{},
			testCandidateJob("candidate-2", secondAudio),
		)
		close(secondDone)
	}()
	cancel()
	receive(t, secondDone)
	if !allZero(secondAudio) {
		t.Fatal("candidate audio was not cleared after cancellation")
	}
	select {
	case <-started:
		t.Fatal("canceled candidate reached the audio handler")
	default:
	}

	close(release)
	receive(t, firstDone)
}

func testCandidateJob(id string, audio []byte) candidateJob {
	return candidateJob{
		header: candidateAudioHeader{
			ID:           id,
			Encoding:     candidateEncoding,
			SampleRate:   candidateSampleRate,
			Channels:     candidateChannels,
			GateCategory: "manual",
		},
		audio: audio,
	}
}
