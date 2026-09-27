package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAgentMemoryLeavesOutCaches(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, "lxc.payload.ab-app-agent-01")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Numbers from a real agent: 4.4 GiB in memory.current, 2.5 GiB of it in use.
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("memory.current", "4673966080\n")
	write("memory.stat", "anon 2589249536\nfile 1938186240\nkernel 134979584\nshmem 10563584\n"+
		"active_file 707764224\ninactive_file 1219960832\nslab_reclaimable 81934336\nslab_unreclaimable 11429224\n")

	if got, want := agentMemory(root, "ab-app-agent-01", 1), int64(4673966080-707764224-1219960832-81934336); got != want {
		t.Errorf("agentMemory = %d, want %d", got, want)
	}
	if got := agentMemory(root, "ab-other-agent-01", 42); got != 42 {
		t.Errorf("without a cgroup: agentMemory = %d, want the fallback 42", got)
	}
}
