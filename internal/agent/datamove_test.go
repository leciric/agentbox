package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/datamove"
	"agentbox/internal/image"
	"agentbox/internal/state"
)

var vmMove = datamove.Marker{
	From: "/home/u.linux/.local/share/agentbox", To: "/home/u.linux/.agentbox",
	Also: [][2]string{{"/home/u/.local/share/agentbox", "/home/u/.agentbox"}},
}

func TestMovedDevices(t *testing.T) {
	devices := map[string]map[string]string{
		"worktree": {"type": "disk", "source": "/home/u/.local/share/agentbox/worktrees/shop/agent-01", "path": "/home/u/.local/share/agentbox/worktrees/shop/agent-01"},
		"gitdir":   {"type": "disk", "source": "/home/u/src/shop/.git", "path": "/home/u/src/shop/.git"},
		"agentapi": {"type": "proxy", "connect": "unix:/home/u.linux/.local/share/agentbox/run/agents/ab-shop-agent-01.sock", "listen": "unix:/run/agentbox.sock", "bind": "instance"},
		"browser":  {"type": "proxy", "listen": "unix:/home/u.linux/.local/share/agentbox/run/browser/x.sock", "connect": "tcp:127.0.0.1:9222", "bind": "host"},
		"gpu":      {"type": "gpu"},
	}
	got := movedDevices(devices, "/home/u/.agentbox/worktrees/shop/agent-01", vmMove)
	want := map[string]map[string]string{
		"worktree": {"type": "disk", "source": "/home/u/.agentbox/worktrees/shop/agent-01", "path": "/home/u/.agentbox/worktrees/shop/agent-01"},
		"agentapi": {"type": "proxy", "connect": "unix:/home/u.linux/.agentbox/run/agents/ab-shop-agent-01.sock", "listen": "unix:/run/agentbox.sock", "bind": "instance"},
		"browser":  {"type": "proxy", "listen": "unix:/home/u.linux/.agentbox/run/browser/x.sock", "connect": "tcp:127.0.0.1:9222", "bind": "host"},
	}
	if len(got) != len(want) {
		t.Fatalf("changed %v, want %v", got, want)
	}
	for name, dev := range want {
		for k, v := range dev {
			if got[name][k] != v {
				t.Errorf("%s %s = %q, want %q", name, k, got[name][k], v)
			}
		}
	}
	if again := movedDevices(want, "/home/u/.agentbox/worktrees/shop/agent-01", vmMove); len(again) != 0 {
		t.Errorf("moved devices move again: %v", again)
	}
}

// A machine Incus failed to start with the VM, its worktree gone from the old
// path, gets its devices moved, is started, and its Claude Code sessions are
// renamed for the worktree's new path.
func TestMoveDevicesStartsWhatWasToRun(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	home := filepath.Join(dir, "home")
	old := "/home/u/.local/share/agentbox/worktrees/shop/agent-01"
	sessions := filepath.Join(home, ".claude", "projects")
	for _, d := range []string{datamove.ClaudeProjectName(old), datamove.ClaudeProjectName(old + "/desktop"), "-home-u-src-other"} {
		if err := os.MkdirAll(filepath.Join(sessions, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	inc := fakeIncus(t, `echo "$*" >> `+log+`
case "$1" in
list) echo '[{"name":"ab-shop-agent-01","status":"Stopped","config":{"volatile.last_state.power":"RUNNING"}}]' ;;
query) echo '{"devices":{"worktree":{"type":"disk","source":"`+old+`","path":"`+old+`"}}}' ;;
exec) HOME=`+home+` sh -c "$8" ;;
esac
`)
	m := &Manager{Incus: inc, User: image.User{Name: "u"}}
	a := state.Agent{Project: "shop", Name: "agent-01", Instance: "ab-shop-agent-01", Worktree: "/home/u/.agentbox/worktrees/shop/agent-01"}
	if err := m.MoveDevices(context.Background(), a, vmMove); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	calls := string(b)
	for _, want := range []string{
		"config device remove ab-shop-agent-01 worktree",
		"config device add ab-shop-agent-01 worktree disk",
		"source=/home/u/.agentbox/worktrees/shop/agent-01",
		"start ab-shop-agent-01",
	} {
		if !strings.Contains(calls, want) {
			t.Errorf("no %q in the calls:\n%s", want, calls)
		}
	}
	if strings.Contains(calls, "stop ") {
		t.Errorf("stopped a machine that wasn't running:\n%s", calls)
	}
	for _, d := range []string{"-home-u--agentbox-worktrees-shop-agent-01", "-home-u--agentbox-worktrees-shop-agent-01-desktop", "-home-u-src-other"} {
		if _, err := os.Stat(filepath.Join(sessions, d)); err != nil {
			t.Errorf("sessions %s: %v", d, err)
		}
	}
}

// A running machine whose worktree moves is stopped around the change; one
// whose devices are already where they belong isn't touched.
func TestMoveDevicesRestartsARunningMachine(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	old := "/home/u/.local/share/agentbox/worktrees/shop/agent-01"
	inc := fakeIncus(t, `echo "$*" >> `+log+`
case "$1" in
list) echo '[{"name":"ab-shop-agent-01","status":"Running"}]' ;;
query) echo '{"devices":{"worktree":{"type":"disk","source":"`+old+`","path":"`+old+`"}}}' ;;
esac
`)
	m := &Manager{Incus: inc, User: image.User{Name: "u"}}
	a := state.Agent{Instance: "ab-shop-agent-01", Worktree: "/home/u/.agentbox/worktrees/shop/agent-01"}
	if err := m.MoveDevices(context.Background(), a, vmMove); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(log)
	calls := string(b)
	stop, remove, start := strings.Index(calls, "stop ab-shop"), strings.Index(calls, "device remove"), strings.Index(calls, "start ab-shop")
	if stop < 0 || remove < stop || start < remove {
		t.Errorf("want stop, change, start; calls:\n%s", calls)
	}

	a.Worktree = old // already where it belongs
	_ = os.Remove(log)
	if err := m.MoveDevices(context.Background(), a, vmMove); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(log)
	if strings.Contains(string(b), "device") || strings.Contains(string(b), "stop") {
		t.Errorf("changed a machine with nothing to move:\n%s", b)
	}
}
