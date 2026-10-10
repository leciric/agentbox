// Package pressure decides, from the VM's measured memory pressure, when an
// agent's heavy commands (a test run, a build) may start and which of those
// already going to pause: the kernel's pressure stall information (PSI), not
// a guess at what anything will use.
//
// It holds no I/O but reading a PSI file: the daemon applies what it decides
// (internal/daemon/pressure.go) to the runs' cgroups.
package pressure

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// File is the VM's memory PSI.
const File = "/proc/pressure/memory"

// Stall is one line of a PSI file: the share of wall time, in percent, over
// the last 10, 60 and 300 seconds, that tasks stalled on memory, and the total
// in microseconds.
type Stall struct {
	Avg10, Avg60, Avg300 float64
	Total                uint64
}

// PSI is a PSI file: Some is time at least one task stalled, Full time every
// task that wasn't idle did, which is time the VM got nothing done.
type PSI struct {
	Some, Full Stall
}

// Read reads and parses a PSI file.
func Read(path string) (PSI, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return PSI{}, err
	}
	return Parse(string(b))
}

// Parse parses what a PSI file holds:
//
//	some avg10=1.36 avg60=0.52 avg300=0.22 total=46356041310
//	full avg10=1.35 avg60=0.50 avg300=0.21 total=40694621386
func Parse(text string) (PSI, error) {
	var out PSI
	seen := 0
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		var s *Stall
		switch fields[0] {
		case "some":
			s = &out.Some
		case "full":
			s = &out.Full
		default:
			continue
		}
		for _, f := range fields[1:] {
			key, value, ok := strings.Cut(f, "=")
			if !ok {
				return PSI{}, fmt.Errorf("pressure: %q isn't key=value", f)
			}
			var err error
			switch key {
			case "avg10":
				s.Avg10, err = strconv.ParseFloat(value, 64)
			case "avg60":
				s.Avg60, err = strconv.ParseFloat(value, 64)
			case "avg300":
				s.Avg300, err = strconv.ParseFloat(value, 64)
			case "total":
				s.Total, err = strconv.ParseUint(value, 10, 64)
			}
			if err != nil {
				return PSI{}, fmt.Errorf("pressure: %s: %w", f, err)
			}
		}
		seen++
	}
	if seen == 0 {
		return PSI{}, fmt.Errorf("pressure: no some or full line in %q", text)
	}
	return out, nil
}
