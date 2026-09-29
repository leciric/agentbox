//go:build !darwin || !cgo

package chv

import (
	"errors"
	"runtime"
)

// errNoVZ is the vz driver where this agentbox can't run it: anywhere but a
// Mac, and on a Mac in a build without cgo, which the framework's bindings
// need.
func errNoVZ() error {
	if runtime.GOOS == "darwin" {
		return errors.New("this agentbox was built without cgo, so it can't run the vz driver's VM: build it with CGO_ENABLED=1")
	}
	return errors.New("the vz driver's VM only runs on a Mac")
}

func newVZMachine(Config, Layout, func(string, ...any)) (machine, error) { return nil, errNoVZ() }

// CheckVZ says why this machine can't run the vz driver's VM.
func CheckVZ(string) error { return errNoVZ() }
