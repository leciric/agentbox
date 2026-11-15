package cli

import (
	"strings"
	"testing"

	"agentbox/internal/api"
)

func TestWriteHeapSummary(t *testing.T) {
	t.Parallel()
	profile := "heap profile: 1: 2 [3: 4] @ heap/1048576\n# runtime.MemStats\n# Alloc = 1\n# HeapAlloc = 3145728\n# Sys = 20971520\n# NumGC = 12\n"
	var out strings.Builder
	writeHeapSummary(&out, strings.NewReader(profile))
	got := out.String()
	for _, want := range []string{"HeapAlloc         3.0 MiB", "Sys              20.0 MiB", "NumGC              12"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "HeapIdle") {
		t.Errorf("summary shows a stat the profile didn't have:\n%s", got)
	}
}

func TestWriteDebugStats(t *testing.T) {
	t.Parallel()
	var out strings.Builder
	writeDebugStats(&out, api.DebugStats{UptimeSeconds: 3600, CPUSeconds: 36, Goroutines: 63, Threads: 12, RSS: 64 << 20, Locked: 30 << 20,
		HeapAlloc: 8 << 20, MemoryLimit: 256 << 20, DBOpen: 2, Chats: 3, ChatsLoaded: 1, ChatItems: 120, Jobs: 4, EventSubscribers: 1})
	got := out.String()
	for _, want := range []string{"1h0m0s, 36 s of CPU (1.0% of a core on average)", "goroutines     63", "resident       64.0 MiB, of it locked 30.0 MiB",
		"8.0 MiB live", "soft limit 256.0 MiB", "3 in memory, 1 with their transcript loaded (120 items)", "jobs           4 in memory"} {
		if !strings.Contains(got, want) {
			t.Errorf("stats lack %q:\n%s", want, got)
		}
	}
}
