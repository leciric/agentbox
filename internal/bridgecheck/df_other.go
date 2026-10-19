//go:build !linux

package bridgecheck

import "errors"

// The bridge only ever runs on Linux.
func setDontFragment(uintptr) error { return errors.New("only on Linux") }
