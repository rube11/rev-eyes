package ambient

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/candidate"
)

// Conversation consumes owned PCM buffers and clears them after use. The bool
// marks an automatic wake, whose first utterance must pass the accurate gate.
// A nil buffer requests an utterance finalization without closing the stream.
type Conversation func(context.Context, <-chan []byte, bool) error

// RunStreaming owns the rolling buffer and the handoff. Moonshine runs only
// while idle. The triggering block is included in the replay exactly once;
// subsequent blocks go directly to the same conversation until it ends.
func (l *Listener) RunStreaming(ctx context.Context, input <-chan Input, converse Conversation) error {
	defer func() {
		for {
			select {
			case event, ok := <-input:
				if !ok {
					return
				}
				clear(event.PCM)
			default:
				return
			}
		}
	}()
	stream, err := l.Factory.Open()
	if err != nil {
		return err
	}
	defer stream.Close()
	ring := make([]byte, retention*2)
	defer clear(ring)
	block := make([]float32, 0, step)
	defer clear(block[:cap(block)])
	var end, base, floor int64
	var audio chan []byte
	var done chan struct{}
	var conversationErr error
	var cancel context.CancelFunc
	drain := func() {
		for len(audio) > 0 {
			clear(<-audio)
		}
	}
	defer func() {
		if cancel != nil {
			cancel()
			<-done
			drain()
		}
	}()
	reset := func() error {
		cancel()
		drain()
		audio = nil
		done = nil
		cancel = nil
		base = end
		floor = end
		block = block[:0]
		clear(ring)
		return stream.Reset()
	}
	send := func(pcm []byte) error {
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		select {
		case audio <- pcm:
			return nil
		case <-ctx.Done():
			clear(pcm)
			return ctx.Err()
		case <-timer.C:
			clear(pcm)
			return errors.New("conversation audio stalled")
		}
	}
	snapshot := func(start int64) []byte {
		pcm := make([]byte, (end-start)*2)
		for i := start; i < end; i++ {
			copy(pcm[(i-start)*2:], ring[(i%retention)*2:(i%retention)*2+2])
		}
		return pcm
	}
	start := func(offset int64, automatic bool) error {
		conversationCtx, stop := context.WithCancel(ctx)
		cancel = stop
		audio = make(chan []byte, 16)
		done = make(chan struct{})
		go func(ch <-chan []byte, finished chan<- struct{}) {
			conversationErr = converse(conversationCtx, ch, automatic)
			close(finished)
		}(audio, done)
		if offset < end {
			return send(snapshot(offset))
		}
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
			if conversationErr != nil && !errors.Is(conversationErr, context.Canceled) {
				return conversationErr
			}
			if err := reset(); err != nil {
				return err
			}
		case event, ok := <-input:
			if !ok {
				return nil
			}
			if event.Control != "" {
				switch event.Control {
				case "listening_start":
					if audio == nil {
						if err := start(max(floor, end-SampleRate/4), false); err != nil {
							return err
						}
						block = block[:0]
					}
				case "conversation_finalize":
					if audio != nil {
						if len(block) > 0 {
							if err := send(snapshot(end - int64(len(block)))); err != nil {
								return err
							}
							clear(block)
							block = block[:0]
						}
						if err := send(nil); err != nil {
							return err
						}
					}
				case "conversation_stop":
					if cancel != nil {
						cancel()
						<-done
						if err := reset(); err != nil {
							return err
						}
					}
				}
				continue
			}
			if len(event.PCM)%2 != 0 {
				clear(event.PCM)
				return errors.New("ambient PCM is not sample aligned")
			}
			for i := 0; i < len(event.PCM); i += 2 {
				if err := ctx.Err(); err != nil {
					clear(event.PCM)
					return err
				}
				copy(ring[(end%retention)*2:], event.PCM[i:i+2])
				end++
				block = append(block, float32(int16(binary.LittleEndian.Uint16(event.PCM[i:])))/32768)
				if len(block) != step {
					continue
				}
				if audio != nil {
					err = send(snapshot(end - step))
				} else {
					err = stream.Add(block)
					if err == nil {
						var lines []Line
						lines, err = stream.Transcript()
						for _, line := range lines {
							if math.IsNaN(line.Start) || math.IsInf(line.Start, 0) || line.Start < 0 {
								continue
							}
							if _, matched := candidate.MatchWakePhrase(line.Text); matched {
								offset := max(floor, max(end-retention, base+int64(line.Start*SampleRate)-preRoll))
								if offset > end {
									continue
								}
								err = start(offset, true)
								break
							}
						}
					}
				}
				clear(block)
				block = block[:0]
				if err != nil {
					clear(event.PCM)
					return err
				}
				if audio == nil && end-base >= 60*SampleRate {
					if err := stream.Reset(); err != nil {
						clear(event.PCM)
						return err
					}
					base = end - 5*SampleRate
					overlap := make([]float32, 5*SampleRate)
					for n := range overlap {
						offset := ((base + int64(n)) % retention) * 2
						overlap[n] = float32(int16(binary.LittleEndian.Uint16(ring[offset:]))) / 32768
					}
					err = stream.Add(overlap)
					clear(overlap)
					if err != nil {
						clear(event.PCM)
						return err
					}
				}
			}
			clear(event.PCM)
		}
	}
}
