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

// TestAgentMemoryCountsTmpfs: a tmpfs's pages (/t) are shmem, which the
// kernel keeps on the anon lists, not active_file or inactive_file, so what
// an agent holds in /t counts in its memory, and so in admission, like any
// other memory it can't give back.
func TestAgentMemoryCountsTmpfs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, "lxc.payload.ab-app-agent-01")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Numbers from an agent before and after writing 300 MiB to /t: shmem,
	// file and active_anon each grew by it, and the file lists didn't.
	const tmpfs = 314572800
	if err := os.WriteFile(filepath.Join(dir, "memory.current"), []byte("1200000000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "memory.stat"), []byte(
		"anon 789454848\nfile 1038639104\nshmem 330018816\nactive_anon 746319872\ninactive_anon 43134976\n"+
			"active_file 668405760\ninactive_file 40292352\nslab_reclaimable 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := agentMemory(root, "ab-app-agent-01", 1)
	if want := int64(1200000000 - 668405760 - 40292352); got != want || got < tmpfs {
		t.Errorf("agentMemory = %d, want %d, with the %d in /t", got, want, int64(tmpfs))
	}
}
