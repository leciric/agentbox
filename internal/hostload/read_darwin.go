package hostload

import (
	"errors"

	"golang.org/x/sys/unix"
)

type reader struct{}

func newReader() *reader { return &reader{} }

// The levels kern.memorystatus_vm_pressure_level reports (sys/event.h's
// NOTE_MEMORYSTATUS_PRESSURE_*).
const (
	levelNormal   = 1
	levelWarn     = 2
	levelCritical = 4
)

func (*reader) read() (Reading, error) {
	r := Reading{OS: "darwin", Memory: -1, CPU: -1, IO: -1, CPUShare: -1}
	level, err := unix.SysctlUint32("kern.memorystatus_vm_pressure_level")
	if err != nil {
		return r, err
	}
	switch level {
	case levelNormal:
		r.Level = "normal"
	case levelWarn:
		r.Level = "warn"
	case levelCritical:
		r.Level = "critical"
	default:
		return r, errors.New("unknown memory pressure level")
	}
	if n, err := unix.SysctlUint64("hw.memsize"); err == nil {
		r.MemoryTotal = int64(n)
	}
	return r, nil
}
