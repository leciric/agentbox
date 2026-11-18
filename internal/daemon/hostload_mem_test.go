package daemon

import (
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/pressure"
)

// A fresh report that holds boots holds them; its ceiling lowers the cap
// boots and builds count the VM's growth against, never raises it; a stale
// report does neither.
func TestHostLoadHoldsBootsAndLowersTheCap(t *testing.T) {
	var h hostLoad
	now := time.Now()
	m := pressure.Mem{Total: 8 << 30, Cap: 14 << 30, Available: 3 << 30}
	if h.holdBoots(now) || h.withCeiling(m, now) != m {
		t.Fatal("no report: held or lowered")
	}
	h.last, h.got = api.HostLoad{HoldBoots: true, VMCeiling: 10 << 30}, now
	if !h.holdBoots(now) {
		t.Error("a fresh report holding boots didn't")
	}
	if got := h.withCeiling(m, now); got.Cap != 10<<30 {
		t.Errorf("cap %d, want the 10 GiB ceiling", got.Cap)
	}
	h.last.VMCeiling = 20 << 30
	if got := h.withCeiling(m, now); got.Cap != 14<<30 {
		t.Errorf("a ceiling over the cap raised it to %d", got.Cap)
	}
	stale := now.Add(api.HostLoadTTL + time.Second)
	if h.holdBoots(stale) || h.withCeiling(m, stale) != m {
		t.Error("a stale report still held boots or lowered the cap")
	}
}
