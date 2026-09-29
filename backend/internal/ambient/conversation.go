package ambient

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/candidate"
	"github.com/rube11/rev-eyes/backend/internal/speech"
	"github.com/rube11/rev-eyes/backend/internal/stt"
)

// Conversation consumes owned PCM buffers and clears them after use. The bool
// marks an automatic wake, whose first utterance must pass the accurate gate.
// Pauses and manual endpoints keep this same stream open for follow-ups.
type Conversation func(context.Context, <-chan stt.AudioInput, bool) error

// Endpointing sees audio time, not wall-clock time. A microphone that stops
// delivering frames at a pause must explicitly flush its final partial block.
const conversationAudioGap = 1200 * time.Millisecond

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
	roles := make([]speech.Role, retention)
	defer clear(ring)
	block := make([]float32, 0, step)
	defer clear(block[:cap(block)])
	var end, base, floor int64
	// Only this loop starts/stops conversations. Workers report completion on a
	// fixed channel; channel nilness never selects the listening mode.
	finished := make(chan error, 1)
	var active *activeConversation
	gapTimer := time.NewTimer(conversationAudioGap)
	gapTimer.Stop()
	defer gapTimer.Stop()
	var audioGap <-chan time.Time
	stopGap := func() {
		if !gapTimer.Stop() {
			select {
			case <-gapTimer.C:
			default:
			}
		}
		audioGap = nil
	}
	armGap := func() {
		stopGap()
		gapTimer.Reset(conversationAudioGap)
		audioGap = gapTimer.C
	}
	defer func() {
		if active != nil {
			active.cancel()
			<-finished
			active.clearAudio()
		}
	}()
	// Called only after receiving completion, so no worker still owns PCM.
	reset := func() error {
		stopGap()
		active.cancel()
		active.clearAudio()
		active = nil
		base = end
		floor = end
		block = block[:0]
		clear(ring)
		clear(roles)
		return stream.Reset()
	}
	snapshot := func(start int64) stt.AudioInput {
		pcm := make([]byte, (end-start)*2)
		var spans []speech.Span
		for i := start; i < end; i++ {
			copy(pcm[(i-start)*2:], ring[(i%retention)*2:(i%retention)*2+2])
			role := roles[i%retention]
			if len(spans) == 0 || spans[len(spans)-1].Role != role {
				spans = append(spans, speech.Span{Start: i - start, End: i - start + 1, Role: role})
			} else {
				spans[len(spans)-1].End++
			}
		}
		return stt.AudioInput{PCM: pcm, Speakers: spans}
	}
	finishPendingAudio := func() error {
		stopGap()
		if active == nil {
			return nil
		}
		if len(block) > 0 {
			if err := active.send(ctx, snapshot(end-int64(len(block)))); err != nil {
				return err
			}
			clear(block)
			block = block[:0]
		}
		// Feed the observed pause or explicit end-of-turn through ordinary
		// endpointing. Immediately sending Finalize can freeze an earlier
		// interim result before Deepgram has decoded the last audio window.
		// One bounded silence window preserves the tail on the same stream.
		silence := make([]byte, int(conversationAudioGap*SampleRate/time.Second)*2)
		return active.send(ctx, stt.AudioInput{PCM: silence})
	}
	start := func(offset int64, automatic bool) error {
		conversationCtx, cancel := context.WithCancel(ctx)
		active = &activeConversation{audio: make(chan stt.AudioInput, 16), cancel: cancel}
		go func(audio <-chan stt.AudioInput) {
			finished <- converse(conversationCtx, audio, automatic)
		}(active.audio)
		if offset < end {
			armGap()
			return active.send(ctx, snapshot(offset))
		}
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-audioGap:
			if err := finishPendingAudio(); err != nil {
				return err
			}
		case conversationErr := <-finished:
			if conversationErr != nil && !errors.Is(conversationErr, context.Canceled) {
				active.cancel()
				active.clearAudio()
				active = nil
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
					if active == nil {
						if err := start(max(floor, end-SampleRate/4), false); err != nil {
							return err
						}
						block = block[:0]
					}
				case "conversation_finalize":
					if err := finishPendingAudio(); err != nil {
						return err
					}
				case "conversation_stop":
					if active != nil {
						active.cancel()
						<-finished
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
				roles[end%retention] = event.Role
				end++
				block = append(block, float32(int16(binary.LittleEndian.Uint16(event.PCM[i:])))/32768)
				if len(block) != step {
					continue
				}
				if active != nil {
					err = active.send(ctx, snapshot(end-step))
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
								// Fallible speaker labels inform downstream routing and
								// account permissions, but must not veto a wake phrase.
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
				if active == nil && end-base >= 60*SampleRate {
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
			if active != nil && len(event.PCM) > 0 {
				armGap()
			}
			clear(event.PCM)
		}
	}
}

// activeConversation owns the bounded handoff queue. Its listener cancels and
// joins the consumer before clearing any frames the consumer did not consume.
type activeConversation struct {
	audio  chan stt.AudioInput
	cancel context.CancelFunc
}

func (c *activeConversation) clearAudio() {
	for len(c.audio) > 0 {
		clear((<-c.audio).PCM)
	}
}

func (c *activeConversation) send(ctx context.Context, event stt.AudioInput) error {
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case c.audio <- event:
		return nil
	case <-ctx.Done():
		clear(event.PCM)
		return ctx.Err()
	case <-timer.C:
		clear(event.PCM)
		return errors.New("conversation audio stalled")
	}
}
