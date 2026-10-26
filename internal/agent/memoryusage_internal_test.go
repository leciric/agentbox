package agent

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestAgentSwap(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "lxc.payload.ab-app-agent-01")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "memory.swap.current"), []byte("18790481920\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, want := agentSwap(root, "ab-app-agent-01"), int64(18790481920); got != want {
		t.Errorf("agentSwap = %d, want %d", got, want)
	}
	if got := agentSwap(root, "ab-other-agent-01"); got != 0 {
		t.Errorf("without a cgroup: agentSwap = %d, want 0", got)
	}
}

func TestZramTotal(t *testing.T) {
	root := t.TempDir()

	// zram0 is sized and backing swap.
	zram0 := filepath.Join(root, "zram0")
	if err := os.MkdirAll(zram0, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(zram0, "disksize"), []byte("17179869184\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// orig_data_size compr_data_size mem_used_total mem_limit mem_used_max same_pages pages_compacted huge_pages huge_pages_since
	if err := os.WriteFile(filepath.Join(zram0, "mm_stat"), []byte("18790481920 3758096384 3958096384 0 0 0 0 0 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// zram1 is loaded but never sized, the way zramctl skips it too.
	zram1 := filepath.Join(root, "zram1")
	if err := os.MkdirAll(zram1, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(zram1, "disksize"), []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, ok := zramTotal(root)
	if !ok {
		t.Fatal("zramTotal ok = false, want true")
	}
	if got.SwapBytes != 18790481920 || got.RealBytes != 3958096384 {
		t.Errorf("zramTotal = %+v, want SwapBytes 18790481920, RealBytes 3958096384", got)
	}

	if _, ok := zramTotal(t.TempDir()); ok {
		t.Error("zramTotal on an empty directory: ok = true, want false")
	}
}

func TestOutsideMemory(t *testing.T) {
	root := t.TempDir()
	proc := func(pid, cgroup, cmdline, status string) {
		dir := filepath.Join(root, pid)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, body := range map[string]string{"cgroup": cgroup, "cmdline": cmdline, "status": status} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	rss := func(anon, shmem int) string {
		return "Name:\tx\nRssAnon:\t" + strconv.Itoa(anon) + " kB\nRssFile:\t999 kB\nRssShmem:\t" + strconv.Itoa(shmem) + " kB\n"
	}
	user := "0::/user.slice/user-1000.slice/user@1000.service/app.slice/agentbox.service\n"
	proc("10", user, "/usr/local/bin/agentbox\x00daemon\x00", rss(100, 0))
	proc("11", user, "/usr/lib/chromium/chromium\x00--type=renderer\x00", rss(200, 50))
	proc("12", user, "node\x00/x/claude-agent-acp/index.js\x00--mcp\x00playwright\x00", rss(300, 0))
	proc("13", user, "/x/claude-agent-sdk-linux-x64/claude\x00", rss(400, 0))
	proc("14", user, "/usr/sbin/sshd\x00", rss(500, 0))
	proc("15", "0::/lxc.payload.ab-app-agent-01/x\n", "/usr/lib/chromium/chromium\x00", rss(600, 0))
	proc("16", user, "", rss(700, 0)) // a kernel thread
	leads, box := outsideMemory(root)
	if want := int64(700 * 1024); leads != want {
		t.Errorf("leads = %d, want %d", leads, want)
	}
	if want := int64(350 * 1024); box != want {
		t.Errorf("agentbox = %d, want %d", box, want)
	}
}

func TestAgentCache(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "lxc.payload.ab-app-agent-01")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	stat := "anon 5\nactive_file 100\ninactive_file 20\nslab_reclaimable 3\nfile 999\n"
	if err := os.WriteFile(filepath.Join(dir, "memory.stat"), []byte(stat), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := agentCache(root, "ab-app-agent-01"); got != 123 {
		t.Errorf("agentCache = %d, want 123", got)
	}
	if got := agentCache(root, "missing"); got != 0 {
		t.Errorf("without a cgroup: agentCache = %d, want 0", got)
	}
}
