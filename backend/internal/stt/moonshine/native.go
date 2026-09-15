//go:build moonshine && cgo

package moonshine

/*
#cgo LDFLAGS: -lmoonshine
#include "moonshine-c-api.h"
#include <stdlib.h>
*/
import "C"

import (
	"errors"
	"fmt"
	"sync"
	"unsafe"

	"github.com/rube11/rev-eyes/backend/internal/ambient"
)

// Factory serializes native calls, including copying borrowed result pointers.
// Streams keep independent state but share model weights. Admission is bounded.
type Factory struct {
	mu      sync.Mutex
	handle  C.int32_t
	permits chan struct{}
	closed  bool
}

func New(path string, concurrency int) (*Factory, error) {
	if path == "" || concurrency < 1 || concurrency > 32 {
		return nil, errors.New("Moonshine needs a model directory and concurrency from 1 to 32")
	}
	p := C.CString(path)
	defer C.free(unsafe.Pointer(p))
	h := C.moonshine_load_transcriber_from_files(p, C.MOONSHINE_MODEL_ARCH_TINY_STREAMING, nil, 0, C.MOONSHINE_HEADER_VERSION)
	if h < 0 {
		return nil, nativeError("load model", h)
	}
	return &Factory{handle: h, permits: make(chan struct{}, concurrency)}, nil
}
func nativeError(op string, code C.int32_t) error {
	return fmt.Errorf("Moonshine %s: %s (%d)", op, C.GoString(C.moonshine_error_to_string(code)), int(code))
}
func (f *Factory) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		C.moonshine_free_transcriber(f.handle)
		f.closed = true
	}
}
func (f *Factory) Open() (ambient.Stream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil, errors.New("Moonshine closed")
	}
	select {
	case f.permits <- struct{}{}:
	default:
		return nil, errors.New("Moonshine listening capacity exhausted")
	}
	s := &stream{factory: f, handle: -1}
	if err := s.reset(); err != nil {
		<-f.permits
		return nil, err
	}
	return s, nil
}

type stream struct {
	factory *Factory
	handle  C.int32_t
	closed  bool
}

func (s *stream) reset() error {
	if s.closed || s.factory.closed {
		return errors.New("Moonshine stream is closed")
	}
	f := s.factory
	if s.handle >= 0 {
		C.moonshine_free_stream(f.handle, s.handle)
		s.handle = -1
	}
	h := C.moonshine_create_stream(f.handle, 0)
	if h < 0 {
		return nativeError("create stream", h)
	}
	s.handle = h
	if code := C.moonshine_start_stream(f.handle, h); code != 0 {
		C.moonshine_free_stream(f.handle, h)
		s.handle = -1
		return nativeError("start stream", code)
	}
	return nil
}
func (s *stream) Reset() error {
	s.factory.mu.Lock()
	defer s.factory.mu.Unlock()
	return s.reset()
}
func (s *stream) Add(audio []float32) error {
	if len(audio) == 0 {
		return nil
	}
	s.factory.mu.Lock()
	defer s.factory.mu.Unlock()
	if s.closed || s.factory.closed {
		return errors.New("Moonshine stream is closed")
	}
	code := C.moonshine_transcribe_add_audio_to_stream(s.factory.handle, s.handle, (*C.float)(unsafe.Pointer(&audio[0])), C.uint64_t(len(audio)), ambient.SampleRate, 0)
	if code != 0 {
		return nativeError("add audio", code)
	}
	return nil
}
func (s *stream) Transcript() ([]ambient.Line, error) {
	s.factory.mu.Lock()
	defer s.factory.mu.Unlock()
	if s.closed || s.factory.closed {
		return nil, errors.New("Moonshine stream is closed")
	}
	var result *C.struct_transcript_t
	code := C.moonshine_transcribe_stream(s.factory.handle, s.handle, 0, &result)
	if code != 0 {
		return nil, nativeError("transcribe", code)
	}
	if result == nil {
		return nil, nil
	}
	lines := make([]ambient.Line, 0, int(result.line_count))
	for _, line := range unsafe.Slice(result.lines, int(result.line_count)) {
		if line.is_updated == 0 && line.is_new == 0 {
			continue
		}
		lines = append(lines, ambient.Line{ID: uint64(line.id), Text: C.GoString(line.text), Start: float64(line.start_time), Duration: float64(line.duration), Complete: line.is_complete != 0})
	}
	return lines, nil
}
func (s *stream) Close() {
	s.factory.mu.Lock()
	defer s.factory.mu.Unlock()
	if !s.closed {
		if !s.factory.closed && s.handle >= 0 {
			C.moonshine_free_stream(s.factory.handle, s.handle)
		}
		<-s.factory.permits
		s.closed = true
	}
}

// ModelManifest returns the release's English tiny-streaming download manifest.
func ModelManifest() (string, error) {
	language := C.CString("en")
	defer C.free(unsafe.Pointer(language))
	name := C.CString("model_arch")
	defer C.free(unsafe.Pointer(name))
	value := C.CString("2")
	defer C.free(unsafe.Pointer(value))
	option := C.struct_moonshine_option_t{name: name, value: value}
	var manifest *C.char
	code := C.moonshine_get_stt_dependencies(language, &option, 1, &manifest)
	if code != 0 {
		return "", nativeError("model manifest", code)
	}
	defer C.moonshine_free_buffer(unsafe.Pointer(manifest))
	return C.GoString(manifest), nil
}
