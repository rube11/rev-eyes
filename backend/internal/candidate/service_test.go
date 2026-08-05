package candidate

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/stt"
)

type clipTranscriberFunc func(context.Context, []byte, stt.AudioFormat) (string, error)

var validAudioFormat = stt.AudioFormat{
	Encoding:   stt.EncodingLinear16,
	SampleRate: 16_000,
	Channels:   1,
}

func (f clipTranscriberFunc) TranscribeClip(
	ctx context.Context,
	audio []byte,
	format stt.AudioFormat,
) (string, error) {
	return f(ctx, audio, format)
}

func TestServiceClearsAudioAfterSuccessAndFailure(t *testing.T) {
	tests := []struct {
		name        string
		transcriber clipTranscriberFunc
		want        string
		wantErr     error
	}{
		{
			name: "success",
			transcriber: func(context.Context, []byte, stt.AudioFormat) (string, error) {
				return "  accurate transcript  ", nil
			},
			want: "accurate transcript",
		},
		{
			name: "failure",
			transcriber: func(context.Context, []byte, stt.AudioFormat) (string, error) {
				return "", errors.New("deepgram unavailable")
			},
			wantErr: errors.New("deepgram unavailable"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, err := NewService(test.transcriber)
			if err != nil {
				t.Fatalf("NewService() error = %v", err)
			}
			audio := []byte{1, 2, 3, 4}
			got, err := service.Process(context.Background(), audio, validAudioFormat)
			if test.wantErr == nil && err != nil {
				t.Fatalf("Process() error = %v", err)
			}
			if test.wantErr != nil && !stringsContain(err, test.wantErr.Error()) {
				t.Fatalf("Process() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("Process() = %q, want %q", got, test.want)
			}
			assertCleared(t, audio)
		})
	}
}

func TestServiceRejectsInvalidFormatAndClearsAudio(t *testing.T) {
	service, err := NewService(clipTranscriberFunc(func(
		context.Context,
		[]byte,
		stt.AudioFormat,
	) (string, error) {
		t.Fatal("transcriber called with an invalid format")
		return "", nil
	}))
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	audio := []byte{1, 2, 3, 4}
	if _, err := service.Process(context.Background(), audio, stt.AudioFormat{}); !errors.Is(
		err,
		stt.ErrAudioFormatInvalid,
	) {
		t.Fatalf("Process() error = %v, want ErrAudioFormatInvalid", err)
	}
	assertCleared(t, audio)
}

func TestServiceTimesOutAndClearsAudio(t *testing.T) {
	service, err := NewService(clipTranscriberFunc(func(
		ctx context.Context,
		_ []byte,
		_ stt.AudioFormat,
	) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}))
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	service.timeout = 20 * time.Millisecond
	audio := []byte{1, 2, 3, 4}
	if _, err := service.Process(context.Background(), audio, validAudioFormat); !errors.Is(
		err,
		context.DeadlineExceeded,
	) {
		t.Fatalf("Process() error = %v, want context.DeadlineExceeded", err)
	}
	assertCleared(t, audio)
}

func assertCleared(t *testing.T, audio []byte) {
	t.Helper()
	for index, value := range audio {
		if value != 0 {
			t.Fatalf("audio[%d] = %d, want 0", index, value)
		}
	}
}

func stringsContain(err error, text string) bool {
	return err != nil && strings.Contains(err.Error(), text)
}
