package chv

import (
	"testing"

	"agentbox/internal/api"
)

func TestBackstopPausesARunningVMOnItsLastGigabytesAndResumesWithRoom(t *testing.T) {
	t.Parallel()
	const gb = int64(1) << 30
	for _, tc := range []struct {
		name          string
		free          int64
		state         string
		pausedForDisk bool
		pause, resume bool
	}{
		{"plenty free", 50 * gb, api.VMRunning, false, false, false},
		{"its last 1 GiB", 1 * gb, api.VMRunning, false, true, false},
		{"still starting", 1 * gb, api.VMStarting, false, true, false},
		{"already paused for it", 1 * gb, api.VMPaused, true, false, false},
		{"paused by the user", 1 * gb, api.VMPaused, false, false, false},
		{"stopping", 1 * gb, api.VMStopping, false, false, false},
		{"room, but not twice the backstop", 3 * gb, api.VMPaused, true, false, false},
		{"twice the backstop again", 4 * gb, api.VMPaused, true, false, true},
		{"room, paused by the user", 40 * gb, api.VMPaused, false, false, false},
		{"unreadable", 0, api.VMRunning, false, false, false},
	} {
		pause, resume := backstop(tc.free, tc.state, tc.pausedForDisk)
		if pause != tc.pause || resume != tc.resume {
			t.Errorf("%s: backstop = pause %v, resume %v; want %v, %v", tc.name, pause, resume, tc.pause, tc.resume)
		}
	}
}
