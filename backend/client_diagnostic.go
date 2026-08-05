package main

import (
	"context"
	"log/slog"

	"github.com/rube11/rev-eyes/backend/internal/realtime"
)

func logClientDiagnostic(
	ctx context.Context,
	diagnostic realtime.ClientDiagnostic,
) {
	switch diagnostic.Event {
	case "transcript":
		slog.InfoContext(
			ctx,
			"moonshine local transcript",
			"kind", diagnostic.Kind,
			"text", diagnostic.Text,
		)
	case "lifecycle":
		slog.InfoContext(
			ctx,
			"moonshine local lifecycle",
			"event", diagnostic.Name,
		)
	case "candidate_trigger":
		slog.InfoContext(
			ctx,
			"moonshine local gate triggered",
			"category", diagnostic.Category,
			"confidence", diagnostic.Confidence,
			"sample_offset", diagnostic.SampleOffset,
		)
	case "candidate_finalized":
		slog.InfoContext(
			ctx,
			"moonshine local candidate finalized",
			"reason", diagnostic.Reason,
			"category", diagnostic.Category,
			"bytes", diagnostic.ByteLength,
			"start_sample_offset", diagnostic.StartSampleOffset,
			"end_sample_offset", diagnostic.EndSampleOffset,
			"submitted", diagnostic.Submitted,
		)
	}
}
