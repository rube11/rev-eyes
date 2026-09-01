package memory

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/tool"
)

type extractorFunc func(context.Context, string) ([]Candidate, error)

func (f extractorFunc) Extract(ctx context.Context, text string) ([]Candidate, error) {
	return f(ctx, text)
}

type writerFunc func(context.Context, tool.Scope, string, []Candidate) (int, error)

func (f writerFunc) RememberCandidates(
	ctx context.Context,
	scope tool.Scope,
	sourceID string,
	candidates []Candidate,
) (int, error) {
	return f(ctx, scope, sourceID, candidates)
}

func TestRecorderCapturesInApplicationBackground(t *testing.T) {
	t.Parallel()

	wantScope := tool.Scope{UserID: "user-1", SessionID: "session-1"}
	wantCandidate := testRecorderCandidate().Normalize()
	written := make(chan []Candidate, 1)
	stored := make(chan string, 1)
	recorder, err := newRecorder(
		extractorFunc(func(_ context.Context, text string) ([]Candidate, error) {
			if text != "I need 130 grams of protein a day." {
				t.Fatalf("Extract() text = %q", text)
			}
			return []Candidate{wantCandidate}, nil
		}),
		writerFunc(func(
			_ context.Context,
			scope tool.Scope,
			sourceID string,
			candidates []Candidate,
		) (int, error) {
			if scope != wantScope {
				t.Fatalf("RememberCandidates() scope = %#v", scope)
			}
			if sourceID != "utterance-1" {
				t.Fatalf("RememberCandidates() source = %q", sourceID)
			}
			written <- candidates
			return len(candidates), nil
		}),
		2,
		time.Second,
	)
	if err != nil {
		t.Fatalf("newRecorder() error = %v", err)
	}
	recorder.SetOnStored(func(userID string) {
		stored <- userID
	})

	appCtx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		recorder.Run(appCtx)
		close(runDone)
	}()

	if !recorder.Capture(
		wantScope,
		" utterance-1 ",
		" I need 130 grams of protein a day. ",
	) {
		t.Fatal("Capture() = false")
	}

	select {
	case candidates := <-written:
		if len(candidates) != 1 || !reflect.DeepEqual(candidates[0], wantCandidate) {
			t.Fatalf("RememberCandidates() candidates = %#v", candidates)
		}
	case <-time.After(time.Second):
		t.Fatal("RememberCandidates() was not called")
	}
	select {
	case userID := <-stored:
		if userID != wantScope.UserID {
			t.Fatalf("stored callback user ID = %q", userID)
		}
	case <-time.After(time.Second):
		t.Fatal("stored callback was not called")
	}

	cancel()
	select {
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatal("Run() did not stop with the application context")
	}
	if recorder.Capture(wantScope, "utterance-2", "Another fact") {
		t.Fatal("Capture() succeeded after Run stopped")
	}
}

func TestRecorderDoesNotNotifyWhenWriterStoresNothingOrFails(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		stored int
		err    error
	}{
		{name: "nothing stored"},
		{name: "write failed", stored: 1, err: errors.New("database unavailable")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			notifications := 0
			recorder, err := newRecorder(
				extractorFunc(func(context.Context, string) ([]Candidate, error) {
					return []Candidate{testRecorderCandidate()}, nil
				}),
				writerFunc(func(context.Context, tool.Scope, string, []Candidate) (int, error) {
					return test.stored, test.err
				}),
				1,
				time.Second,
			)
			if err != nil {
				t.Fatalf("newRecorder() error = %v", err)
			}
			recorder.SetOnStored(func(string) { notifications++ })
			recorder.process(context.Background(), captureJob{
				scope:    tool.Scope{UserID: "user-1", SessionID: "session-1"},
				sourceID: "utterance-1",
				text:     "protein target",
			})
			if notifications != 0 {
				t.Fatalf("stored callback calls = %d, want 0", notifications)
			}
		})
	}
}

func TestRecorderRememberExplicitUsesAtomicKeyedPipeline(t *testing.T) {
	t.Parallel()

	wantScope := tool.Scope{UserID: "user-1", SessionID: "session-1"}
	wantCandidate := testRecorderCandidate().Normalize()
	wantCandidate.Card.Summary = "The user targets 150 grams of protein per day."
	recorder, err := newRecorder(
		extractorFunc(func(_ context.Context, text string) ([]Candidate, error) {
			if text != "Remember that my protein target is 150 grams." {
				t.Fatalf("Extract() text = %q", text)
			}
			return []Candidate{wantCandidate}, nil
		}),
		writerFunc(func(
			_ context.Context,
			scope tool.Scope,
			sourceID string,
			candidates []Candidate,
		) (int, error) {
			if scope != wantScope || sourceID != "utterance-1" {
				t.Fatalf("RememberCandidates(%#v, %q)", scope, sourceID)
			}
			if len(candidates) != 1 || !reflect.DeepEqual(candidates[0], wantCandidate) {
				t.Fatalf("explicit candidates = %#v", candidates)
			}
			return 1, nil
		}),
		1,
		time.Second,
	)
	if err != nil {
		t.Fatalf("newRecorder() error = %v", err)
	}
	recorder.SetOnStored(func(string) {
		t.Fatal("background refresh callback ran for explicit memory")
	})
	if err := recorder.RememberExplicit(
		context.Background(),
		wantScope,
		"utterance-1",
		"Remember that my protein target is 150 grams.",
	); err != nil {
		t.Fatalf("RememberExplicit() error = %v", err)
	}
}

func TestRecorderRememberExplicitRejectsEmptyExtraction(t *testing.T) {
	t.Parallel()

	writes := 0
	notifications := 0
	recorder, err := newRecorder(
		extractorFunc(func(context.Context, string) ([]Candidate, error) {
			return nil, nil
		}),
		writerFunc(func(context.Context, tool.Scope, string, []Candidate) (int, error) {
			writes++
			return 0, nil
		}),
		1,
		time.Second,
	)
	if err != nil {
		t.Fatalf("newRecorder() error = %v", err)
	}
	recorder.SetOnStored(func(string) { notifications++ })

	err = recorder.RememberExplicit(
		context.Background(),
		tool.Scope{UserID: "user-1", SessionID: "session-1"},
		"utterance-1",
		"Remember something.",
	)
	if !errors.Is(err, ErrNoMemoryCandidates) {
		t.Fatalf("RememberExplicit() error = %v, want %v", err, ErrNoMemoryCandidates)
	}
	if writes != 0 || notifications != 0 {
		t.Fatalf("writes = %d, notifications = %d; want zero", writes, notifications)
	}
}

func TestRecorderBasesTemporaryExpiryOnCaptureTime(t *testing.T) {
	t.Parallel()

	capturedAt := time.Date(2026, time.August, 30, 9, 0, 0, 0, time.UTC)
	extractorExpiry := capturedAt.Add(10 * time.Hour)
	recorder, err := newRecorder(
		extractorFunc(func(context.Context, string) ([]Candidate, error) {
			candidate := testRecorderCandidate()
			candidate.MemoryKey = "state.activity.current"
			candidate.Retention = RetentionTemporary
			candidate.ExpiresAt = &extractorExpiry
			return []Candidate{candidate}, nil
		}),
		writerFunc(func(
			_ context.Context,
			_ tool.Scope,
			_ string,
			candidates []Candidate,
		) (int, error) {
			want := capturedAt.Add(TemporaryMemoryLifetime)
			if len(candidates) != 1 ||
				candidates[0].ExpiresAt == nil ||
				!candidates[0].ExpiresAt.Equal(want) {
				t.Fatalf("temporary expiry = %#v, want %s", candidates, want)
			}
			return 1, nil
		}),
		1,
		time.Second,
	)
	if err != nil {
		t.Fatalf("newRecorder() error = %v", err)
	}
	recorder.now = func() time.Time { return capturedAt.Add(time.Hour) }
	recorder.process(context.Background(), captureJob{
		scope:      tool.Scope{UserID: "user-1", SessionID: "session-1"},
		sourceID:   "utterance-1",
		text:       "I am working out.",
		capturedAt: capturedAt,
	})
}

func TestRecorderDropsTemporaryMemoryAfterItsCaptureWindow(t *testing.T) {
	t.Parallel()

	capturedAt := time.Date(2026, time.August, 30, 9, 0, 0, 0, time.UTC)
	writes := 0
	recorder, err := newRecorder(
		extractorFunc(func(context.Context, string) ([]Candidate, error) {
			candidate := testRecorderCandidate()
			candidate.MemoryKey = "state.activity.current"
			candidate.Retention = RetentionTemporary
			return []Candidate{candidate}, nil
		}),
		writerFunc(func(context.Context, tool.Scope, string, []Candidate) (int, error) {
			writes++
			return 1, nil
		}),
		1,
		time.Second,
	)
	if err != nil {
		t.Fatalf("newRecorder() error = %v", err)
	}
	recorder.now = func() time.Time {
		return capturedAt.Add(TemporaryMemoryLifetime + time.Minute)
	}
	recorder.process(context.Background(), captureJob{
		scope:      tool.Scope{UserID: "user-1", SessionID: "session-1"},
		sourceID:   "utterance-1",
		text:       "I am working out.",
		capturedAt: capturedAt,
	})
	if writes != 0 {
		t.Fatalf("RememberCandidates() calls = %d, want 0", writes)
	}
}

func TestRecorderRejectsEntireInvalidExtractionBatch(t *testing.T) {
	t.Parallel()

	writes := 0
	recorder, err := newRecorder(
		extractorFunc(func(context.Context, string) ([]Candidate, error) {
			return []Candidate{testRecorderCandidate(), {}}, nil
		}),
		writerFunc(func(context.Context, tool.Scope, string, []Candidate) (int, error) {
			writes++
			return 0, nil
		}),
		1,
		time.Second,
	)
	if err != nil {
		t.Fatalf("newRecorder() error = %v", err)
	}

	recorder.process(context.Background(), captureJob{
		scope:    tool.Scope{UserID: "user-1", SessionID: "session-1"},
		sourceID: "utterance-1",
		text:     "protein target",
	})
	if writes != 0 {
		t.Fatalf("RememberCandidates() calls = %d, want 0", writes)
	}
}

func TestRecorderCaptureIsBoundedAndValidatesWork(t *testing.T) {
	t.Parallel()

	recorder, err := newRecorder(
		extractorFunc(func(context.Context, string) ([]Candidate, error) { return nil, nil }),
		writerFunc(func(context.Context, tool.Scope, string, []Candidate) (int, error) {
			return 0, nil
		}),
		1,
		time.Second,
	)
	if err != nil {
		t.Fatalf("newRecorder() error = %v", err)
	}
	scope := tool.Scope{UserID: "user-1", SessionID: "session-1"}
	if !recorder.Capture(scope, "utterance-1", "first") {
		t.Fatal("first Capture() = false")
	}
	if recorder.Capture(scope, "utterance-2", "second") {
		t.Fatal("Capture() succeeded with a full queue")
	}
	if recorder.Capture(tool.Scope{}, "utterance-3", "third") {
		t.Fatal("Capture() succeeded with an invalid scope")
	}
	if recorder.Capture(scope, "", "third") || recorder.Capture(scope, "utterance-3", " ") {
		t.Fatal("Capture() succeeded with an invalid source or text")
	}
}

func TestNewRecorderRequiresExtractorAndWriter(t *testing.T) {
	t.Parallel()

	writer := writerFunc(func(context.Context, tool.Scope, string, []Candidate) (int, error) {
		return 0, nil
	})
	extractor := extractorFunc(func(context.Context, string) ([]Candidate, error) {
		return nil, nil
	})
	if _, err := NewRecorder(nil, writer); !errors.Is(err, ErrExtractorRequired) {
		t.Fatalf("NewRecorder(nil, writer) error = %v", err)
	}
	if _, err := NewRecorder(extractor, nil); !errors.Is(err, ErrWriterRequired) {
		t.Fatalf("NewRecorder(extractor, nil) error = %v", err)
	}
	if _, err := NewRecorder(extractor, writer); err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
}

func testRecorderCandidate() Candidate {
	return Candidate{
		Card: Card{
			Topics:  []Topic{TopicHealth, TopicGoals},
			Kind:    KindGoal,
			Title:   "Daily protein target",
			Summary: "The user targets 130 grams of protein per day.",
		},
		MemoryKey: "profile.nutrition.daily_protein_target",
		Retention: RetentionDurable,
	}
}
