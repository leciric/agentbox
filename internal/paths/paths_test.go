package paths

import (
	"path/filepath"
	"testing"
)

func TestDefault(t *testing.T) {
	t.Setenv("HOME", "/home/alice")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("AGENTBOX_HOME", "")
	p, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := p.Config, "/home/alice/.config/agentbox"; got != want {
		t.Errorf("Config = %s, want %s", got, want)
	}
	if got, want := p.Data, "/home/alice/.agentbox"; got != want {
		t.Errorf("Data = %s, want %s", got, want)
	}

	// XDG_CONFIG_HOME, when absolute, wins over the fallback; XDG_DATA_HOME
	// no longer moves the data.
	t.Setenv("XDG_CONFIG_HOME", "/etc/xdg-config")
	t.Setenv("XDG_DATA_HOME", "/var/xdg-data")
	p, err = Default()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := p.Config, "/etc/xdg-config/agentbox"; got != want {
		t.Errorf("Config with XDG_CONFIG_HOME = %s, want %s", got, want)
	}
	if got, want := p.Data, "/home/alice/.agentbox"; got != want {
		t.Errorf("Data with XDG_DATA_HOME = %s, want %s", got, want)
	}
	if got, err := Legacy(); err != nil || got != "/var/xdg-data/agentbox" {
		t.Errorf("Legacy with XDG_DATA_HOME = %s, %v, want /var/xdg-data/agentbox", got, err)
	}

	// AGENTBOX_HOME, when absolute, moves it.
	t.Setenv("AGENTBOX_HOME", "/srv/agentbox/")
	if p, err = Default(); err != nil || p.Data != "/srv/agentbox" {
		t.Errorf("Data with AGENTBOX_HOME = %s, %v, want /srv/agentbox", p.Data, err)
	}
	if got, err := Home(); err != nil || got != "/srv/agentbox" {
		t.Errorf("Home with AGENTBOX_HOME = %s, %v, want /srv/agentbox", got, err)
	}
	t.Setenv("AGENTBOX_HOME", "relative")
	if got, _ := Home(); got != "/home/alice/.agentbox" {
		t.Errorf("Home with a relative AGENTBOX_HOME = %s, want /home/alice/.agentbox", got)
	}

	// A relative XDG_CONFIG_HOME is ignored, as xdg() requires an absolute path.
	t.Setenv("XDG_CONFIG_HOME", "relative/xdg-config")
	p, err = Default()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := p.Config, "/home/alice/.config/agentbox"; got != want {
		t.Errorf("Config with relative XDG_CONFIG_HOME = %s, want %s", got, want)
	}
}

func TestDefaultErrorsWithoutAHome(t *testing.T) {
	t.Setenv("HOME", "")
	if _, err := Default(); err == nil {
		t.Error("Default() with no $HOME: want an error")
	}
}

func TestSimplePaths(t *testing.T) {
	p := Paths{Config: "/home/alice/.config/agentbox", Data: "/home/alice/.agentbox"}
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"StateDB", p.StateDB(), "/home/alice/.agentbox/state.db"},
		{"Credentials", p.Credentials(), "/home/alice/.config/agentbox/credentials"},
		{"DaemonLog", p.DaemonLog(), "/home/alice/.agentbox/daemon.log"},
		{"SecretsKey", p.SecretsKey(), "/home/alice/.config/agentbox/secrets.key"},
		{"AgentSockets", p.AgentSockets(), "/home/alice/.agentbox/run/agents"},
		{"Tools", p.Tools(), "/home/alice/.agentbox/tools"},
		{"LeadHome", p.LeadHome("app"), "/home/alice/.agentbox/projects/app/lead-home"},
		{"ProjectNotes", p.ProjectNotes("app"), "/home/alice/.agentbox/projects/app/notes.md"},
		{"ChatImages", p.ChatImages("app", "agent-01"), "/home/alice/.agentbox/projects/app/chat-images/agent-01"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("got %s, want %s", c.got, c.want)
			}
		})
	}
}

func TestSocket(t *testing.T) {
	p := Paths{Data: "/home/alice/.agentbox"}
	t.Setenv("AGENTBOX_SOCKET", "")
	if got, want := p.Socket(), "/home/alice/.agentbox/run/agentbox.sock"; got != want {
		t.Errorf("by default: got %s, want %s", got, want)
	}
	t.Setenv("AGENTBOX_SOCKET", "/tmp/custom.sock")
	if got, want := p.Socket(), "/tmp/custom.sock"; got != want {
		t.Errorf("with AGENTBOX_SOCKET: got %s, want %s", got, want)
	}
}

func TestWorktrees(t *testing.T) {
	p := Paths{Data: "/home/alice/.agentbox"}
	t.Setenv("AGENTBOX_WORKTREES", "")
	if got, want := p.Worktree("app", "agent-01"), "/home/alice/.agentbox/worktrees/app/agent-01"; got != want {
		t.Errorf("by default: got %s, want %s", got, want)
	}
	// On a Mac the daemon's state is in the VM and the worktrees on the Mac's share.
	t.Setenv("AGENTBOX_WORKTREES", "/Users/alice/.agentbox/worktrees")
	if got, want := p.Worktree("app", "agent-01"), filepath.Join("/Users/alice/.agentbox/worktrees", "app", "agent-01"); got != want {
		t.Errorf("with AGENTBOX_WORKTREES: got %s, want %s", got, want)
	}
	t.Setenv("AGENTBOX_WORKTREES", "relative/path")
	if got := p.Worktrees(); got != "/home/alice/.agentbox/worktrees" {
		t.Errorf("a relative AGENTBOX_WORKTREES was used: %s", got)
	}
}
