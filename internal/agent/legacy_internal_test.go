package agent

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// What the budget used to set on the disk, and its memory.high, is put back
// on daemon start; a QoS the host set itself, and files already at their
// defaults, are left alone.
func TestResetLegacyBudget(t *testing.T) {
	root := budgetRoot(t)
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	read := func(name string) string {
		b, _ := os.ReadFile(filepath.Join(root, name))
		return string(b)
	}
	// cgroupfs files take one line a write and read back every line; a
	// plain file keeps only the last write, which is enough to see what was
	// written, one device at a time.
	write("io.cost.qos", "259:4 enable=1 ctrl=user rpct=95.00 rlat=5000 wpct=95.00 wlat=10000 min=5.00 max=150.00\n")
	write("agentbox/io.max", "253:0 rbps=max wbps=16777216 riops=max wiops=max\n")
	write("agentbox/io.weight", "default 10\n")
	write("agentbox/memory.high", "15461882265\n")
	reset, err := ResetLegacyBudget()
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(reset)
	if want := []string{"io.cost on 259:4", "the budget's io.max on 253:0", "the budget's io.weight", "the budget's memory.high"}; !slices.Equal(reset, want) {
		t.Errorf("reset %q, want %q", reset, want)
	}
	for name, want := range map[string]string{
		"io.cost.qos": "259:4 enable=0", "agentbox/io.max": "253:0 rbps=max wbps=max riops=max wiops=max",
		"agentbox/io.weight": "default 100", "agentbox/memory.high": "max",
	} {
		if got := read(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}

	// The host's own io.cost, and what's at its defaults, stay.
	own := "259:4 enable=1 ctrl=user rpct=90.00 rlat=2000 wpct=90.00 wlat=4000 min=50.00 max=100.00\n"
	write("io.cost.qos", own)
	write("agentbox/io.max", "253:0 rbps=max wbps=max riops=max wiops=max\n")
	write("agentbox/io.weight", "default 100\n")
	write("agentbox/memory.high", "max\n")
	if reset, err := ResetLegacyBudget(); err != nil || len(reset) != 0 {
		t.Errorf("reset %q (%v) on a host with nothing of the budget's", reset, err)
	}
	if got := read("io.cost.qos"); got != own {
		t.Errorf("the host's own io.cost.qos became %q", got)
	}

	// The hard disk targets are the budget's too.
	if !legacyQoS(nestedKeyed("8:0 enable=1 ctrl=user rpct=95.00 rlat=75000 wpct=95.00 wlat=150000 min=5.00 max=150.00")["8:0"]) {
		t.Error("the budget's hard disk QoS isn't recognised")
	}
	if legacyQoS(nestedKeyed("8:0 enable=0 ctrl=user rpct=95.00 rlat=75000 wpct=95.00 wlat=150000 min=5.00 max=150.00")["8:0"]) {
		t.Error("io.cost already off is reset again")
	}
	if strings.Contains(read("agentbox/io.max"), "16777216") {
		t.Error("io.max still capped")
	}
}
