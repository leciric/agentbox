package paths

import (
	"path/filepath"
	"testing"
)

func TestDefault(t *testing.T) {
	t.Setenv("HOME", "/home/alice")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	p, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := p.Config, "/home/alice/.config/agentbox"; got != want {
		t.Errorf("Config = %s, want %s", got, want)
	}
	if got, want := p.Data, "/home/alice/.local/share/agentbox"; got != want {
		t.Errorf("Data = %s, want %s", got, want)
	}

	// XDG_CONFIG_HOME and XDG_DATA_HOME, when absolute, win over the fallback.
	t.Setenv("XDG_CONFIG_HOME", "/etc/xdg-config")
	t.Setenv("XDG_DATA_HOME", "/var/xdg-data")
	p, err = Default()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := p.Config, "/etc/xdg-config/agentbox"; got != want {
		t.Errorf("Config with XDG_CONFIG_HOME = %s, want %s", got, want)
	}
	if got, want := p.Data, "/var/xdg-data/agentbox"; got != want {
		t.Errorf("Data with XDG_DATA_HOME = %s, want %s", got, want)
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
	p := Paths{Config: "/home/alice/.config/agentbox", Data: "/home/alice/.local/share/agentbox"}
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"StateDB", p.StateDB(), "/home/alice/.local/share/agentbox/state.db"},
		{"Credentials", p.Credentials(), "/home/alice/.config/agentbox/credentials"},
		{"DaemonLog", p.DaemonLog(), "/home/alice/.local/share/agentbox/daemon.log"},
		{"SecretsKey", p.SecretsKey(), "/home/alice/.config/agentbox/secrets.key"},
		{"AgentSockets", p.AgentSockets(), "/home/alice/.local/share/agentbox/run/agents"},
		{"Tools", p.Tools(), "/home/alice/.local/share/agentbox/tools"},
		{"LeadHome", p.LeadHome("app"), "/home/alice/.local/share/agentbox/projects/app/lead-home"},
		{"ProjectNotes", p.ProjectNotes("app"), "/home/alice/.local/share/agentbox/projects/app/notes.md"},
		{"ChatImages", p.ChatImages("app", "agent-01"), "/home/alice/.local/share/agentbox/projects/app/chat-images/agent-01"},
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
	p := Paths{Data: "/home/alice/.local/share/agentbox"}
	t.Setenv("AGENTBOX_SOCKET", "")
	if got, want := p.Socket(), "/home/alice/.local/share/agentbox/run/agentbox.sock"; got != want {
		t.Errorf("by default: got %s, want %s", got, want)
	}
	t.Setenv("AGENTBOX_SOCKET", "/tmp/custom.sock")
	if got, want := p.Socket(), "/tmp/custom.sock"; got != want {
		t.Errorf("with AGENTBOX_SOCKET: got %s, want %s", got, want)
	}
}

func TestWorktrees(t *testing.T) {
	p := Paths{Data: "/home/alice/.local/share/agentbox"}
	t.Setenv("AGENTBOX_WORKTREES", "")
	if got, want := p.Worktree("app", "agent-01"), "/home/alice/.local/share/agentbox/worktrees/app/agent-01"; got != want {
		t.Errorf("by default: got %s, want %s", got, want)
	}
	// On a Mac the daemon's state is in the VM and the worktrees on the Mac's share.
	t.Setenv("AGENTBOX_WORKTREES", "/Users/alice/.local/share/agentbox/worktrees")
	if got, want := p.Worktree("app", "agent-01"), filepath.Join("/Users/alice/.local/share/agentbox/worktrees", "app", "agent-01"); got != want {
		t.Errorf("with AGENTBOX_WORKTREES: got %s, want %s", got, want)
	}
	t.Setenv("AGENTBOX_WORKTREES", "relative/path")
	if got := p.Worktrees(); got != "/home/alice/.local/share/agentbox/worktrees" {
		t.Errorf("a relative AGENTBOX_WORKTREES was used: %s", got)
	}
}
