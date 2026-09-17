//go:build !moonshine || !cgo

package moonshine

import (
	"errors"
	"github.com/rube11/rev-eyes/backend/internal/ambient"
)

type Factory struct{}

func New(_ string, _ int) (*Factory, error) {
	return nil, errors.New("server Moonshine requires a CGO-enabled build with -tags moonshine")
}
func (*Factory) Open() (ambient.Stream, error) {
	return nil, errors.New("Moonshine native runtime unavailable")
}
func (*Factory) Close() {}
