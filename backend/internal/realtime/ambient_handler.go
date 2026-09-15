package realtime

import (
	"context"
	"fmt"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/ambient"
	"github.com/rube11/rev-eyes/backend/internal/candidate"
)

// DiagnosticsServer shares native listening and paid-clip capacity with the
// application, but has no assistant, notification, or history handlers.
func (s *Server) DiagnosticsServer(observed func(context.Context, <-chan ambient.Input, func(ambient.Clip), ambient.Observer) error) *Server {
	d := NewServer(s.transcriber, Handlers{
		Diagnostics: true, Ambient: s.handlers.Ambient, AmbientObserved: observed,
		CandidateAudio: s.handlers.CandidateAudio,
		Authenticate:   s.handlers.Authenticate, CheckOrigin: s.handlers.CheckOrigin,
	})
	d.candidateAdmissions = s.candidateAdmissions
	d.candidatePermits = s.candidatePermits
	return d
}

func (s *Server) listenAmbient(ctx context.Context, writer jsonWriter, input <-chan ambient.Input, jobs chan<- candidateJob) error {
	defer func() {
		for {
			select {
			case event, ok := <-input:
				if !ok {
					return
				}
				clearCandidateAudio(event.PCM)
			default:
				return
			}
		}
	}()
	listener := s.handlers.Ambient
	if s.handlers.Diagnostics {
		if s.handlers.AmbientObserved == nil {
			return fmt.Errorf("observed server listening unavailable")
		}
		listener = func(ctx context.Context, input <-chan ambient.Input, emit func(ambient.Clip)) error {
			return s.handlers.AmbientObserved(ctx, input, emit, ambient.Observer{
				Ready: func() error { return writer.WriteJSON(serverMessage{Type: "ready"}) },
				Transcript: func(line ambient.Line, base int64) error {
					return writer.WriteJSON(serverMessage{Type: "moonshine_transcript", ID: fmt.Sprintf("%d-%d", base, line.ID), Text: line.Text, Final: line.Complete})
				},
				Audio: func(bytes int64) error {
					return writer.WriteJSON(serverMessage{Type: "audio_received", ReceivedBytes: bytes})
				},
			})
		}
	}
	return listener(ctx, input, func(clip ambient.Clip) {
		if ctx.Err() != nil {
			clearCandidateAudio(clip.Audio)
			return
		}
		job := candidateJob{
			sessionContext: ctx,
			header:         candidateAudioHeader{ID: fmt.Sprintf("ambient-%d-%d", time.Now().UnixNano(), clip.End), Encoding: candidateEncoding, SampleRate: candidateSampleRate, Channels: 1, ByteLength: len(clip.Audio), StartSampleOffset: clip.Start, EndSampleOffset: clip.End, GateCategory: string(clip.Reason), GateConfidence: 1},
			audio:          clip.Audio, acceptedAt: time.Now(),
		}
		// Announce manual clips before their terminal message so the client can
		// associate completions with the active tap interaction, including rejection.
		if clip.Reason == candidate.WakeManual {
			if err := writer.WriteJSON(serverMessage{Type: "ambient_candidate", ID: job.header.ID}); err != nil {
				clearCandidateAudio(clip.Audio)
				return
			}
		}
		if err := job.header.validate(); err != nil {
			clearCandidateAudio(clip.Audio)
			_ = writer.WriteJSON(candidateDoneMessage(job.header.ID))
			return
		}
		if !s.tryAdmitCandidate(&job) {
			clearCandidateAudio(clip.Audio)
			_ = writer.WriteJSON(candidateDoneMessage(job.header.ID))
			return
		}
		select {
		case jobs <- job:
			if s.handlers.Diagnostics {
				_ = writer.WriteJSON(serverMessage{Type: "keyword_detected", ID: job.header.ID, Text: string(clip.Reason)})
			}
		default:
			s.releaseCandidateAdmission(job)
			clearCandidateAudio(clip.Audio)
			_ = writer.WriteJSON(candidateDoneMessage(job.header.ID))
		}
	})
}
