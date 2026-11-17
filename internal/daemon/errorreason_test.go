package daemon

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/incus"
	"agentbox/internal/state"
)

func TestErrorClass(t *testing.T) {
	sigkill := exec.Command("sh", "-c", "kill -9 $$").Run()
	for _, c := range []struct {
		err  error
		text string
		want string
	}{
		{err: fmt.Errorf("writing: %w", syscall.ENOSPC), want: classNoSpace},
		{err: fmt.Errorf("copy: %w", context.Canceled), want: classCancelled},
		{err: fmt.Errorf("waiting: %w", context.DeadlineExceeded), want: classTimeout},
		{err: fmt.Errorf("%w: hung", incus.ErrNotAnswering), want: classIncusDown},
		{err: sigkill, want: classKilled},
		{text: "Claude Code exited (exit status 137): ", want: classKilled},
		{text: "Claude Code exited (exit status 1): Session x: query stream error: ACP connection closed", want: classACPClosed},
		{text: "Codex exited (exit status 2)", want: classExitCode},
		{text: "incus copy: Failed creating instance: no space left on device", want: classNoSpace},
		{text: `Get "https://example.com/x": dial tcp: lookup example.com: no such host`, want: classNetwork},
		{text: "Invalid API key · Please run /login", want: classAuth},
		{text: "the base image isn't built: run agentbox image build", want: classNoImage},
		{text: "image part 3 doesn't match its sha256", want: classChecksum},
		{text: "mkdir /x: permission denied", want: classPermission},
		{text: "something odd", want: classOther},
	} {
		if got := errorClass(c.err, c.text); got != c.want {
			t.Errorf("errorClass(%v, %q) = %s, want %s", c.err, c.text, got, c.want)
		}
	}
	// A failed create's step is AgentBox's words, and kept.
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	err := &agent.CreateError{Ref: "shop/agent-3", Step: "starting the machine", Err: errors.New(`incus start shop-agent-3: Failed to start "shop-agent-3": exit status 1`)}
	class, reason := d.srv.createReason(err, "shop", "agent-3")
	if class != classExitCode || reason != `starting the machine: incus start [name]-[name]: Failed to start "[text]": exit status 1` {
		t.Errorf("createReason = %s, %q", class, reason)
	}
}

func TestScrubReasonLeavesNothingPersonal(t *testing.T) {
	in := `Leandro's "fix the login page" failed at /home/leandro/src/shop/.git on api.acme-corp.com ` +
		`(10.1.2.3:8443, fe80::1:2:3) for leandro@acme.com, C:\Users\leandro\shop, see https://acme.com/x?token=1, ` +
		`branch agentbox/fix-login, id 0123456789abcdef0123, notes.md and package.json, ação ✓ `
	got := scrubReason(in, "Leandro", "shop")
	for _, bad := range []string{"leandro", "Leandro", "shop", "fix the login", "acme", "10.1.2.3", "fe80", "/", "\\", "@", "fix-login", "0123456789abcdef", "notes", "ação"} {
		if strings.Contains(got, bad) {
			t.Errorf("reason keeps %q: %s", bad, got)
		}
	}
	if len(got) > maxReason || !strings.HasSuffix(got, "...") {
		t.Errorf("reason = %q (%d bytes)", got, len(got))
	}
	if got := scrubReason("npm ci: notes.md and package.json, ação ✓"); got != "npm ci: [name] and package.json, ? ?" {
		t.Errorf("reason = %q", got)
	}
	for _, r := range got {
		if r < 0x20 || r >= 0x7f {
			t.Fatalf("reason has %q: %s", r, got)
		}
	}
	// Another user's home, as the Redactor leaves it, goes whole.
	if got := scrubReason("open /home/[user]/src/p/.git: no such file"); got != "open [path]: no such file" {
		t.Errorf("a redacted home: %q", got)
	}
	if got := scrubReason("Claude Code exited (exit status 137): Killed"); got != "Claude Code exited (exit status 137): Killed" {
		t.Errorf("plain wording changed: %q", got)
	}
}

func TestLostClassUsesWhatTheDaemonSaw(t *testing.T) {
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	a := state.Agent{Project: "p", Name: "a", Instance: "p-a", AI: "claude"}
	why := "Claude Code exited (exit status 1): Session s: query stream error: ACP connection closed"
	if got := d.srv.lostClass(a, why); got != classACPClosed {
		t.Errorf("with nothing seen, %s", got)
	}
	// Incus restarted a moment ago: that cut the link.
	d.srv.incus.mu.Lock()
	d.srv.incus.lastRestart = time.Now()
	d.srv.incus.mu.Unlock()
	if got := d.srv.lostClass(a, why); got != classIncusDown {
		t.Errorf("after an Incus restart, %s", got)
	}
	// A kill for memory in its machine says more than either.
	d.srv.mu.Lock()
	d.srv.oomAt[a.Instance] = time.Now()
	d.srv.mu.Unlock()
	if got := d.srv.lostClass(a, "Claude Code exited (exit status 137)"); got != classOOMKilled {
		t.Errorf("after an OOM kill, %s", got)
	}
	// One the watch hasn't seen yet counts too.
	d.srv.mu.Lock()
	delete(d.srv.oomAt, a.Instance)
	d.srv.oomSeen[a.Instance] = agent.MemoryCounts{Kills: 1}
	d.srv.mu.Unlock()
	d.srv.memoryEvents = func(string) (agent.MemoryCounts, bool) { return agent.MemoryCounts{Kills: 2}, true }
	if got := d.srv.lostClass(a, "Claude Code exited (exit status 137)"); got != classOOMKilled {
		t.Errorf("with a kill not yet seen, %s", got)
	}
}

func TestUTCOffset(t *testing.T) {
	for zone, want := range map[string]string{
		"America/Sao_Paulo": "-3", "Asia/Kolkata": "6", "America/St_Johns": "-3", "Pacific/Kiritimati": "14", "UTC": "0",
	} {
		loc, err := time.LoadLocation(zone)
		if err != nil {
			t.Skip(err)
		}
		// In July, St. John's is -2:30, which rounds away from zero.
		if got := utcOffset(time.Date(2026, 7, 1, 12, 0, 0, 0, loc)); got != want {
			t.Errorf("%s: %s, want %s", zone, got, want)
		}
	}
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	if err := d.srv.store.SetSetting(ctx, state.SettingTimezone, "America/Sao_Paulo"); err != nil {
		t.Fatal(err)
	}
	if got := d.srv.usageUTCOffset(ctx, time.Now()); got != "-3" {
		t.Errorf("with the app's timezone, %s", got)
	}
}
