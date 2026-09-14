package realtime

import (
	"context"
	"fmt"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/ambient"
	"github.com/rube11/rev-eyes/backend/internal/candidate"
)

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
	return s.handlers.Ambient(ctx, input, func(clip ambient.Clip) {
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
		default:
			s.releaseCandidateAdmission(job)
			clearCandidateAudio(clip.Audio)
			_ = writer.WriteJSON(candidateDoneMessage(job.header.ID))
		}
	})
}
