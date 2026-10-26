package cli

import (
	"strings"
	"testing"
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
