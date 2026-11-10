package hostload

import (
	"os"
	"strconv"
	"strings"
)

// procRoot is where /proc is; tests point it at a fixture.
var procRoot = "/proc"

type reader struct{}

func newReader() *reader { return &reader{} }

func (*reader) read() (Reading, error) {
	r := Reading{OS: "linux", CPUShare: -1}
	var err error
	if r.Memory, err = psiFile(procRoot + "/pressure/memory"); err != nil {
		return r, err
	}
	r.CPU, _ = psiFile(procRoot + "/pressure/cpu")
	r.IO, _ = psiFile(procRoot + "/pressure/io")
	if b, err := os.ReadFile(procRoot + "/meminfo"); err == nil {
		r.MemoryTotal, r.Available = memInfo(string(b))
	}
	return r, nil
}

// psiFile is a /proc/pressure file's "some avg10", -1 when it can't be read.
func psiFile(path string) (float64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return -1, err
	}
	return PSISome(string(b)), nil
}

// PSISome is the "some avg10" percentage of a /proc/pressure file, 0 when it
// hasn't one.
func PSISome(psi string) float64 {
	for line := range strings.Lines(psi) {
		f := strings.Fields(line)
		if len(f) == 0 || f[0] != "some" {
			continue
		}
		for _, kv := range f[1:] {
			if v, ok := strings.CutPrefix(kv, "avg10="); ok {
				n, _ := strconv.ParseFloat(v, 64)
				return n
			}
		}
	}
	return 0
}

// memInfo is /proc/meminfo's MemTotal and MemAvailable, in bytes.
func memInfo(s string) (total, available int64) {
	for line := range strings.Lines(s) {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		kb, _ := strconv.ParseInt(f[1], 10, 64)
		switch f[0] {
		case "MemTotal:":
			total = kb << 10
		case "MemAvailable:":
			available = kb << 10
		}
	}
	return total, available
}
