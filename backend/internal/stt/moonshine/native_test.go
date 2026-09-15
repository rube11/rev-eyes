//go:build moonshine && cgo

package moonshine

import (
	"encoding/binary"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNativeStreamIsolationAndCapacity(t *testing.T) {
	path := os.Getenv("MOONSHINE_TEST_MODEL_DIR")
	if path == "" {
		t.Skip("set MOONSHINE_TEST_MODEL_DIR for actual model inference")
	}
	f, err := New(path, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	a, err := f.Open()
	if err != nil {
		t.Fatal(err)
	}
	b, err := f.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if _, err = f.Open(); err == nil {
		t.Fatal("capacity exceeded")
	}
	for i := 0; i < 8; i++ {
		if err = a.Add(make([]float32, 4000)); err != nil {
			t.Fatal(err)
		}
		lines, err := a.Transcript()
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range lines {
			if l.Text != "" {
				t.Fatalf("silence recognized as %q", l.Text)
			}
		}
	}
	if lines, err := b.Transcript(); err != nil || len(lines) != 0 {
		t.Fatalf("second stream contaminated: %v %v", lines, err)
	}
	if err = a.Reset(); err != nil {
		t.Fatal(err)
	}
	a.Close()
	a.Close()
	c, err := f.Open()
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
}

func TestNativeSpeech(t *testing.T) {
	path := os.Getenv("MOONSHINE_TEST_MODEL_DIR")
	wav := os.Getenv("MOONSHINE_TEST_WAV")
	if path == "" || wav == "" {
		t.Skip("set model directory and MOONSHINE_TEST_WAV to the upstream two_cities_16k.wav fixture")
	}
	data, err := os.ReadFile(wav)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 12 || string(data[:4]) != "RIFF" {
		t.Fatal("expected WAV")
	}
	var pcm []byte
	for i := 12; i+8 <= len(data); {
		n := int(binary.LittleEndian.Uint32(data[i+4:]))
		if i+8+n > len(data) {
			t.Fatal("truncated WAV")
		}
		if string(data[i:i+4]) == "data" {
			pcm = data[i+8 : i+8+n]
			break
		}
		i += 8 + n + n%2
	}
	if len(pcm) == 0 {
		t.Fatal("no PCM")
	}
	f, err := New(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	stream, err := f.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	samples := make([]float32, len(pcm)/2+3*16000)
	for i := 0; i < len(pcm)/2; i++ {
		samples[i] = float32(int16(binary.LittleEndian.Uint16(pcm[i*2:]))) / 32768
	}
	found := false
	started := time.Now()
	for len(samples) > 0 {
		n := min(4000, len(samples))
		if err = stream.Add(samples[:n]); err != nil {
			t.Fatal(err)
		}
		samples = samples[n:]
		lines, err := stream.Transcript()
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range lines {
			if strings.Contains(strings.ToLower(line.Text), "best of times") {
				found = true
			}
		}
	}
	t.Logf("Processed %.1f seconds of speech plus silence in %s", float64(len(pcm))/32000, time.Since(started))
	if !found {
		t.Fatal("expected speech not recognized")
	}
}

func TestClosedNativeStreamCannotBeReused(t *testing.T) {
	path := os.Getenv("MOONSHINE_TEST_MODEL_DIR")
	if path == "" {
		t.Skip("set MOONSHINE_TEST_MODEL_DIR for native lifecycle checks")
	}
	factory, err := New(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer factory.Close()
	old, err := factory.Open()
	if err != nil {
		t.Fatal(err)
	}
	old.Close()
	current, err := factory.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	if err = old.Reset(); err == nil {
		t.Fatal("closed stream recreated native state outside admission limit")
	}
	if err = old.Add([]float32{0}); err == nil {
		t.Fatal("closed stream accepted audio")
	}
	if _, err = old.Transcript(); err == nil {
		t.Fatal("closed stream returned another stream's transcript")
	}
	factory.Close()
	if err = current.Reset(); err == nil {
		t.Fatal("stream used a closed model")
	}
}
