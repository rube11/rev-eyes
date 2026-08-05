package proposal

import (
	"context"
	"errors"
	"testing"

	"github.com/rube11/rev-eyes/backend/internal/tool"
)

type resolverFunc func(context.Context, tool.Scope, Status) (Resolution, bool, error)

func (f resolverFunc) ResolvePending(
	ctx context.Context,
	scope tool.Scope,
	status Status,
) (Resolution, bool, error) {
	return f(ctx, scope, status)
}

func TestConfirmerResolvesKnownProposalKinds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		utterance  string
		kind       Kind
		wantStatus Status
		want       string
	}{
		{"accept reminder", "Yes please!", KindReminder, StatusAccepted, "Okay, I saved that reminder."},
		{"save reminder", "Save that.", KindReminder, StatusAccepted, "Okay, I saved that reminder."},
		{"reject reminder", "Nope.", KindReminder, StatusRejected, "Okay, I won't create that reminder."},
		{"accept watch", "Go ahead", KindWatch, StatusAccepted, "Okay, I'll watch for that."},
		{"reject watch", "Never mind", KindWatch, StatusRejected, "Okay, I won't create that watch."},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			confirmer, err := NewConfirmer(resolverFunc(func(
				_ context.Context,
				_ tool.Scope,
				status Status,
			) (Resolution, bool, error) {
				if status != test.wantStatus {
					t.Fatalf("status = %q", status)
				}
				return Resolution{Kind: test.kind, Status: status}, true, nil
			}), func() {})
			if err != nil {
				t.Fatalf("NewConfirmer() error = %v", err)
			}

			response, handled, err := confirmer.Confirm(context.Background(), tool.Scope{}, test.utterance)
			if err != nil || !handled || response != test.want {
				t.Fatalf("Confirm() = %q, %v, %v", response, handled, err)
			}
		})
	}
}

func TestConfirmerIgnoresAmbiguousOrUnmatchedSpeech(t *testing.T) {
	t.Parallel()

	called := false
	confirmer, _ := NewConfirmer(resolverFunc(func(
		context.Context,
		tool.Scope,
		Status,
	) (Resolution, bool, error) {
		called = true
		return Resolution{}, false, nil
	}), func() {})

	response, handled, err := confirmer.Confirm(context.Background(), tool.Scope{}, "Yes, but check weekly")
	if err != nil || handled || response != "" || called {
		t.Fatalf("Confirm() = %q, %v, %v; called = %v", response, handled, err, called)
	}

	response, handled, err = confirmer.Confirm(context.Background(), tool.Scope{}, "yes")
	if err != nil || handled || response != "" || !called {
		t.Fatalf("Confirm() = %q, %v, %v; called = %v", response, handled, err, called)
	}
}

func TestNewConfirmerRequiresResolver(t *testing.T) {
	t.Parallel()

	if _, err := NewConfirmer(nil, func() {}); !errors.Is(err, ErrResolverRequired) {
		t.Fatalf("NewConfirmer(nil) error = %v", err)
	}
	if _, err := NewConfirmer(resolverFunc(func(
		context.Context,
		tool.Scope,
		Status,
	) (Resolution, bool, error) {
		return Resolution{}, false, nil
	}), nil); !errors.Is(err, ErrScheduleTriggerRequired) {
		t.Fatalf("NewConfirmer(nil trigger) error = %v", err)
	}
}

func TestConfirmerTriggersAcceptedScheduleRegistration(t *testing.T) {
	t.Parallel()

	triggered := false
	confirmer, _ := NewConfirmer(resolverFunc(func(
		context.Context,
		tool.Scope,
		Status,
	) (Resolution, bool, error) {
		return Resolution{Kind: KindReminder, Status: StatusAccepted}, true, nil
	}), func() {
		triggered = true
	})

	_, handled, err := confirmer.Confirm(context.Background(), tool.Scope{}, "yes")
	if err != nil || !handled || !triggered {
		t.Fatalf("handled = %v, triggered = %v, error = %v", handled, triggered, err)
	}
}

func TestConfirmerExplainsActiveWatchLimitWithoutScheduling(t *testing.T) {
	t.Parallel()

	triggered := false
	confirmer, _ := NewConfirmer(resolverFunc(func(
		context.Context,
		tool.Scope,
		Status,
	) (Resolution, bool, error) {
		return Resolution{
			Kind:                    KindWatch,
			Status:                  StatusAccepted,
			ActiveWatchLimitReached: true,
		}, true, nil
	}), func() {
		triggered = true
	})

	response, handled, err := confirmer.Confirm(context.Background(), tool.Scope{}, "yes")
	want := "You already have 5 active watches. Let one expire before starting another."
	if err != nil || !handled || response != want || triggered {
		t.Fatalf(
			"Confirm() = %q, %v, %v; triggered = %v",
			response,
			handled,
			err,
			triggered,
		)
	}
}
