//go:build !linux && !darwin && !windows

package hostload

import "errors"

type reader struct{}

func newReader() *reader { return &reader{} }

func (*reader) read() (Reading, error) { return Reading{}, errors.New("no host load on this OS") }
