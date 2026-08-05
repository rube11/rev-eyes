package realtime

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/candidate"
	"github.com/rube11/rev-eyes/backend/internal/stt"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

const defaultCandidateProcessingTimeout = 60 * time.Second

type candidateJob struct {
	header        candidateAudioHeader
	audio         []byte
	acceptedAt    time.Time
	admissionHeld bool
}

func candidateDoneMessage(id string) serverMessage {
	if !validCandidateID(id) {
		id = ""
	}
	return serverMessage{Type: candidateAssistantDoneFallback, ID: id}
}

func (s *Server) runCandidateWorker(
	ctx context.Context,
	scope tool.Scope,
	writer jsonWriter,
	jobs <-chan candidateJob,
	done chan<- struct{},
) {
	defer func() {
		for {
			select {
			case job, ok := <-jobs:
				if !ok {
					close(done)
					return
				}
				s.releaseCandidateAdmission(job)
				clearCandidateAudio(job.audio)
			default:
				close(done)
				return
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case job, ok := <-jobs:
			if !ok {
				return
			}
			s.processCandidateBeforeDeadline(ctx, scope, writer, job)
		}
	}
}

func (s *Server) processCandidateBeforeDeadline(
	ctx context.Context,
	scope tool.Scope,
	writer jsonWriter,
	job candidateJob,
) {
	timeout := s.candidateTimeout
	if timeout <= 0 {
		timeout = defaultCandidateProcessingTimeout
	}
	acceptedAt := job.acceptedAt
	if acceptedAt.IsZero() {
		acceptedAt = time.Now()
	}
	jobCtx, cancel := context.WithDeadline(ctx, acceptedAt.Add(timeout))
	terminalSent := s.processCandidate(jobCtx, scope, writer, job)
	timedOut := errors.Is(jobCtx.Err(), context.DeadlineExceeded)
	cancel()
	if !timedOut || terminalSent || ctx.Err() != nil {
		return
	}
	slog.WarnContext(
		ctx,
		"candidate audio processing timed out",
		"candidate_id", job.header.ID,
	)
	_ = writer.WriteJSON(candidateDoneMessage(job.header.ID))
}

func (s *Server) tryAdmitCandidate(job *candidateJob) bool {
	if job == nil || s.candidateAdmissions == nil {
		return job != nil
	}
	select {
	case s.candidateAdmissions <- struct{}{}:
		job.admissionHeld = true
		return true
	default:
		return false
	}
}

func (s *Server) releaseCandidateAdmission(job candidateJob) {
	if !job.admissionHeld || s.candidateAdmissions == nil {
		return
	}
	<-s.candidateAdmissions
}

func (s *Server) processCandidate(
	ctx context.Context,
	scope tool.Scope,
	writer jsonWriter,
	job candidateJob,
) bool {
	defer clearCandidateAudio(job.audio)
	releaseAdmission := func() {
		if !job.admissionHeld {
			return
		}
		s.releaseCandidateAdmission(job)
		job.admissionHeld = false
	}
	defer releaseAdmission()
	if s.candidatePermits != nil {
		select {
		case s.candidatePermits <- struct{}{}:
			defer func() { <-s.candidatePermits }()
		case <-ctx.Done():
			return false
		}
	}
	if ctx.Err() != nil {
		return false
	}
	if s.handlers.CandidateAudio == nil {
		slog.WarnContext(ctx, "candidate audio processing is disabled")
		_ = writer.WriteJSON(candidateDoneMessage(job.header.ID))
		return true
	}
	format := job.header.format()
	metrics, metricsErr := stt.MeasureLinear16PCM(job.audio, format)
	if metricsErr != nil {
		slog.WarnContext(
			ctx,
			"candidate audio metrics unavailable",
			"candidate_id", job.header.ID,
			"error", metricsErr,
		)
	} else {
		slog.InfoContext(
			ctx,
			"candidate audio signal",
			"candidate_id", job.header.ID,
			"duration_ms", metrics.Duration.Milliseconds(),
			"peak_pcm", metrics.PeakAmplitude,
			"rms_pcm", metrics.RMSAmplitude,
		)
	}

	transcript, err := s.handlers.CandidateAudio(ctx, job.audio, format)
	clearCandidateAudio(job.audio)
	releaseAdmission()
	if err != nil {
		if ctx.Err() == nil {
			slog.ErrorContext(
				ctx,
				"failed to transcribe candidate audio",
				"candidate_id", job.header.ID,
				"error", err,
			)
			_ = writer.WriteJSON(candidateDoneMessage(job.header.ID))
			return true
		}
		return false
	}
	transcript = strings.TrimSpace(transcript)
	if transcript == "" {
		slog.InfoContext(ctx, "candidate audio produced no transcript", "candidate_id", job.header.ID)
		_ = writer.WriteJSON(candidateDoneMessage(job.header.ID))
		return true
	}
	wakeReason := candidate.WakeManual
	if job.header.GateCategory != string(candidate.WakeManual) {
		var matched bool
		wakeReason, matched = candidate.MatchWakePhrase(transcript)
		if !matched {
			slog.InfoContext(
				ctx,
				"candidate transcript rejected by wake policy",
				"candidate_id", job.header.ID,
			)
			_ = writer.WriteJSON(candidateDoneMessage(job.header.ID))
			return true
		}
	}
	slog.InfoContext(
		ctx,
		"candidate audio transcribed",
		"candidate_id", job.header.ID,
		"transcript_characters", len(transcript),
		"wake_reason", wakeReason,
	)
	err = s.handleCompletedUtterance(
		ctx,
		scope,
		writer,
		transcript,
		utteranceDelivery{messageID: job.header.ID},
	)
	if err != nil {
		if ctx.Err() == nil {
			slog.ErrorContext(ctx, "failed to handle candidate transcript", "error", err)
		}
		return false
	}
	return true
}
