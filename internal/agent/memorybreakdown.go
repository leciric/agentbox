package agent

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Where the VM's used memory goes beyond the agents' machines: the project
// chats' AI tools, which run in the VM outside any container, AgentBox's own
// processes, and the file cache the agents' machines hold. Worked out when
// the memory popover opens, like the rest of MemoryUsage.

// leadMarks are what a chat's AI tool and its ACP adapter have in their
// command lines.
var leadMarks = []string{"claude-agent-acp", "claude-agent-sdk", "codex-acp", "opencode"}

// agentboxMarks are what AgentBox's own helpers have in theirs: the browser
// the desktop tools drive, and Playwright's.
var agentboxMarks = []string{"playwright", "chromium", "chrome"}

// outsideMemory adds up what the processes outside every agent's machine
// hold, by kind: leads are the chats' AI tools and adapters, agentbox is the
// daemon, its connectors and its browsers. Anything else (systemd, Incus,
// sshd) is left to the kernel-and-other remainder. A process counts its
// anonymous and shared memory, which is what memory.current charges it
// without the file cache. procRoot is /proc.
func outsideMemory(procRoot string) (leads, agentbox int64) {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return 0, 0
	}
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		dir := filepath.Join(procRoot, e.Name())
		cgroup, err := os.ReadFile(filepath.Join(dir, "cgroup"))
		if err != nil || bytes.Contains(cgroup, []byte("lxc.payload.")) || bytes.HasPrefix(cgroup, []byte("0::/"+oldBudgetCgroup+"/")) {
			continue
		}
		cmdline, err := os.ReadFile(filepath.Join(dir, "cmdline"))
		if err != nil || len(cmdline) == 0 {
			continue // a kernel thread, or gone
		}
		args := strings.ReplaceAll(string(cmdline), "\x00", " ")
		var kind *int64
		switch {
		case filepath.Base(strings.SplitN(args, " ", 2)[0]) == "agentbox":
			kind = &agentbox
		case containsAny(args, leadMarks): // before the browser: a chat's MCP config may name Playwright
			kind = &leads
		case containsAny(args, agentboxMarks):
			kind = &agentbox
		default:
			continue
		}
		*kind += processMemory(filepath.Join(dir, "status"))
	}
	return leads, agentbox
}

func containsAny(s string, marks []string) bool {
	for _, m := range marks {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// processMemory is RssAnon plus RssShmem from a /proc/<pid>/status, in bytes.
func processMemory(status string) int64 {
	b, err := os.ReadFile(status)
	if err != nil {
		return 0
	}
	var total int64
	for _, line := range strings.Split(string(b), "\n") {
		key, value, _ := strings.Cut(line, ":")
		if key != "RssAnon" && key != "RssShmem" {
			continue
		}
		if f := strings.Fields(value); len(f) > 0 {
			kb, _ := strconv.ParseInt(f[0], 10, 64)
			total += kb * 1024
		}
	}
	return total
}

// agentCache is the file cache and reclaimable slab an agent's cgroup holds:
// what agentMemory leaves out, and the kernel gives back when memory runs
// short. 0 for a stopped agent.
func agentCache(root, instance string) int64 {
	b, err := os.ReadFile(filepath.Join(agentCgroup(root, instance), "memory.stat"))
	if err != nil {
		return 0
	}
	var total int64
	for _, line := range strings.Split(string(b), "\n") {
		key, value, _ := strings.Cut(line, " ")
		switch key {
		case "active_file", "inactive_file", "slab_reclaimable":
			n, _ := strconv.ParseInt(value, 10, 64)
			total += n
		}
	}
	return total
}
