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

// The shared ceilings are shares of what the VM can reach: the cap, or the
// host's ceiling under it, never under what the VM has now (19 Nov 2026: a
// 14 GiB cap, the VM kept at 11.3 GiB).
func TestReachableMemory(t *testing.T) {
	t.Parallel()
	const gib = int64(1) << 30
	for _, c := range []struct {
		vmCap, ceiling, plugged, want int64
	}{
		// no ceiling reported
		{14 * gib, 0, 11 * gib, 14 * gib},
		// the host could spare more than the cap
		{14 * gib, 16 * gib, 11 * gib, 14 * gib},
		// the host keeps the VM under its cap
		{14 * gib, 12 * gib, 11 * gib, 12 * gib},
		// a dip under what's plugged doesn't count
		{14 * gib, 9 * gib, 11 * gib, 11 * gib},
		// an unknown cap stays unknown
		{0, 9 * gib, 11 * gib, 0},
	} {
		if got := reachable(c.vmCap, c.ceiling, c.plugged); got != c.want {
			t.Errorf("reachable(cap %d, ceiling %d, plugged %d) = %d GiB, want %d GiB", c.vmCap/gib, c.ceiling/gib, c.plugged/gib, got/gib, c.want/gib)
		}
	}
}
