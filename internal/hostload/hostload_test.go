package hostload

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

func calm() Reading {
	return Reading{OS: "linux", Memory: 0, CPU: 5, IO: 3, CPUShare: -1, MemoryTotal: 32 << 30, Available: 20 << 30}
}

func TestJudgeThresholds(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Reading)
		why  string
	}{
		{"calm", func(*Reading) {}, ""},
		{"memory stall", func(r *Reading) { r.Memory = 12 }, "memory"},
		{"below the reserve", func(r *Reading) { r.Available = 3 << 30 }, "memory"},
		// 7.5 GiB of 32 is over the reserve: it was under a quarter's.
		{"over the reserve", func(r *Reading) { r.Available = 15 << 29 }, ""},
		{"cpu", func(r *Reading) { r.CPU = 60 }, "cpu"},
		{"io", func(r *Reading) { r.IO = 45 }, "io"},
		{"windows cpu", func(r *Reading) { r.CPU, r.CPUShare = -1, 95 }, "cpu"},
		{"mac warn", func(r *Reading) { r.Memory, r.Available, r.Level = -1, 0, "warn" }, "memory"},
		{"mac normal", func(r *Reading) { r.Memory, r.Available, r.Level = -1, 0, "normal" }, ""},
	} {
		r := calm()
		tc.edit(&r)
		got := Judge(r, t0)
		if got.Why != tc.why || got.Reclaim != (tc.why == "memory") {
			t.Errorf("%s: %+v", tc.name, got)
		}
	}
}

// The reserve is a small fixed 4 GiB, never under the host's hard floor,
// however big the host.
func TestReserve(t *testing.T) {
	share := func(total int64) int64 { return int64(float64(total) * FloorShare) }
	for _, tc := range []struct{ total, reserve, floor int64 }{
		{8 << 30, 4 << 30, 2 << 30},
		{16 << 30, 4 << 30, 2 << 30},
		{61 << 29, 4 << 30, 2 << 30}, // 30.5 GiB: was 7.6
		{64 << 30, 4 << 30, share(64 << 30)},
		{128 << 30, share(128 << 30), share(128 << 30)},
	} {
		if got := Reserve(tc.total); got != tc.reserve {
			t.Errorf("Reserve(%d GiB) = %d, want %d", tc.total>>30, got, tc.reserve)
		}
		if got := Floor(tc.total); got != tc.floor {
			t.Errorf("Floor(%d GiB) = %d, want %d", tc.total>>30, got, tc.floor)
		}
		if Reserve(tc.total) < Floor(tc.total) {
			t.Errorf("Reserve(%d GiB) is under the floor", tc.total>>30)
		}
	}
}
