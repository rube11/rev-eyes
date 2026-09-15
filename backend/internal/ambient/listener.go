// Package ambient gates continuous audio locally before paid clip transcription.
package ambient

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/candidate"
)

const SampleRate = 16000
const retention = 30 * SampleRate
const preRoll = 10 * SampleRate
const postRoll = 2 * SampleRate
const step = SampleRate / 4

// Line timestamps are relative to the current recognizer stream.
type Line struct {
	ID              uint64
	Text            string
	Start, Duration float64
	Complete        bool
}
type Stream interface {
	Add([]float32) error
	Transcript() ([]Line, error)
	Reset() error
	Close()
}
type Factory interface{ Open() (Stream, error) }
type Clip struct {
	Audio      []byte
	Start, End int64
	Reason     candidate.WakeReason
}

type Input struct {
	PCM     []byte
	Control string
}

// Listener is immutable; each Run owns its recognizer and audio state.
type Listener struct{ Factory Factory }

func (l *Listener) Run(ctx context.Context, input <-chan Input, emit func(Clip)) error {
	return l.RunObserved(ctx, input, emit, Observer{})
}

// Observer is optional, session-local presentation for the authenticated live
// test. Regular listening never exposes rough transcripts.
type Observer struct {
	Ready      func() error
	Transcript func(Line, int64) error
	Audio      func(int64) error
}

func (l *Listener) RunObserved(ctx context.Context, input <-chan Input, emit func(Clip), observer Observer) error {
	stream, err := l.Factory.Open()
	if err != nil {
		return err
	}
	defer stream.Close()
	if observer.Ready != nil {
		if err := observer.Ready(); err != nil {
			return err
		}
	}
	d := detector{stream: stream, emit: emit, seen: make(map[uint64]bool), observer: observer}
	defer clear(d.ring[:])
	// Process fixed blocks, independent of WebSocket frame size.
	block := make([]float32, 0, step)
	defer clear(block[:cap(block)])
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-input:
			if !ok {
				return nil
			}
			if event.Control != "" {
				switch event.Control {
				case "ambient_reply_arm":
					d.replyUntil = time.Now().Add(30 * time.Second)
					d.replyStart = d.end
				case "ambient_reply_disarm":
					d.replyUntil = time.Time{}
				case "listening_start":
					d.start = d.end
					d.pending = true
					d.reason = candidate.WakeManual
					d.finish = 0
				case "listening_stop":
					if !d.pending && time.Now().Before(d.replyUntil) {
						// A tap authorizes the buffered reply even if recognition
						// has not produced its first hypothesis yet.
						d.pending = true
						d.start = max(d.replyStart, d.lastEnd)
						d.reason = candidate.WakeManual
					}
					if d.pending && d.reason == candidate.WakeManual {
						d.flush()
					}
				}
				continue
			}
			pcm := event.PCM
			if len(pcm)%2 != 0 {
				clear(pcm)
				return errors.New("ambient PCM is not sample aligned")
			}
			for i := 0; i < len(pcm); i += 2 {
				if err := ctx.Err(); err != nil {
					clear(pcm)
					return err
				}
				sample := int16(binary.LittleEndian.Uint16(pcm[i:]))
				d.ring[d.end%retention] = sample
				d.end++
				block = append(block, float32(sample)/32768)
				if len(block) == step {
					err = d.process(block)
					clear(block)
					block = block[:0]
					if err != nil {
						clear(pcm)
						return err
					}
				}
			}
			clear(pcm)
		}
	}
}

type detector struct {
	observer                          Observer
	acknowledged                      int64
	replyUntil                        time.Time
	replyStart                        int64
	stream                            Stream
	emit                              func(Clip)
	ring                              [retention]int16
	end, base, start, finish, lastEnd int64
	pending                           bool
	reason                            candidate.WakeReason
	seen                              map[uint64]bool
}

func (d *detector) process(block []float32) error {
	if err := d.stream.Add(block); err != nil {
		return err
	}
	lines, err := d.stream.Transcript()
	if err != nil {
		return err
	}
	for _, line := range lines {
		if math.IsNaN(line.Start) || math.IsNaN(line.Duration) || math.IsInf(line.Start, 0) || math.IsInf(line.Duration, 0) || line.Start < 0 || line.Duration < 0 {
			continue
		}
		if d.observer.Transcript != nil {
			if err := d.observer.Transcript(line, d.base); err != nil {
				return err
			}
		}
		end := d.base + int64((line.Start+line.Duration)*SampleRate)
		if end > d.end {
			end = d.end
		}
		if end <= d.lastEnd || d.seen[line.ID] {
			continue
		}
		reason, match := candidate.MatchWakePhrase(line.Text)
		match = match && d.base+int64(line.Start*SampleRate) >= d.lastEnd
		reply := time.Now().Before(d.replyUntil) && d.base+int64(line.Start*SampleRate) >= d.replyStart && strings.TrimSpace(line.Text) != ""
		if !d.pending && (match || reply) {
			if reply {
				reason = candidate.WakeManual
				d.replyUntil = time.Time{}
			}
			d.pending = true
			d.reason = reason
			d.start = max(d.end-retention, max(d.lastEnd, d.base+int64(line.Start*SampleRate)-preRoll))
			if reply {
				d.start = max(d.lastEnd, max(d.replyStart, d.base+int64(line.Start*SampleRate)-SampleRate/4))
			}
			d.start = max(0, d.start)
		}
		if d.pending && end > d.start {
			if line.Complete {
				d.finish = max(d.finish, end+postRoll)
			} else {
				d.finish = 0
			}
		}
		if line.Complete {
			d.seen[line.ID] = true
		}
	}
	if d.pending && ((d.finish > 0 && d.end >= d.finish) || d.end-d.start >= retention) {
		d.flush()
	}
	if d.observer.Audio != nil && d.end-d.acknowledged >= SampleRate {
		if err := d.observer.Audio(d.end * 2); err != nil {
			return err
		}
		d.acknowledged = d.end
	}
	// Moonshine retains transcript lines. Recreate streams periodically so long
	// listening sessions remain bounded, replaying context across the boundary.
	if d.end-d.base >= 60*SampleRate {
		if err := d.stream.Reset(); err != nil {
			return err
		}
		d.base = d.end - 5*SampleRate
		clear(d.seen)
		overlap := make([]float32, 5*SampleRate)
		defer clear(overlap)
		for i := range overlap {
			overlap[i] = float32(d.ring[(d.base+int64(i))%retention]) / 32768
		}
		if err := d.stream.Add(overlap); err != nil {
			return err
		}
	}
	return nil
}

func (d *detector) flush() {
	start := max(d.start, max(0, d.end-retention))
	if start < d.end {
		pcm := make([]byte, (d.end-start)*2)
		for offset := start; offset < d.end; offset++ {
			binary.LittleEndian.PutUint16(pcm[(offset-start)*2:], uint16(d.ring[offset%retention]))
		}
		d.emit(Clip{Audio: pcm, Start: start, End: d.end, Reason: d.reason})
	} else if d.reason == candidate.WakeManual {
		d.emit(Clip{Start: start, End: d.end, Reason: d.reason})
	}
	d.lastEnd = d.end
	d.replyUntil = time.Time{}
	d.pending = false
	d.finish = 0
}
