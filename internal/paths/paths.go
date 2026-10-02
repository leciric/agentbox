// Package paths resolves where AgentBox keeps its files, following the XDG
// base directory spec.
package paths

import (
	"os"
	"path/filepath"
)

type Paths struct {
	Config string // ~/.config/agentbox
	Data   string // ~/.local/share/agentbox
	// Home is ~/.agentbox, the Home chat's working directory (HomeChat).
	Home string
}

func Default() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	return Paths{
		Config: filepath.Join(xdg("XDG_CONFIG_HOME", filepath.Join(home, ".config")), "agentbox"),
		Data:   filepath.Join(xdg("XDG_DATA_HOME", filepath.Join(home, ".local", "share")), "agentbox"),
		Home:   filepath.Join(home, ".agentbox"),
	}, nil
}

func xdg(env, fallback string) string {
	if v := os.Getenv(env); filepath.IsAbs(v) {
		return v
	}
	return fallback
}

func (p Paths) StateDB() string     { return filepath.Join(p.Data, "state.db") }
func (p Paths) Credentials() string { return filepath.Join(p.Config, "credentials") }
func (p Paths) DaemonLog() string   { return filepath.Join(p.Data, "daemon.log") }

// SecretsKey is the AES key the secrets you hand to agents are sealed under
// (package secrets). It is created on first use, mode 0600, and lives beside
// the logins rather than beside the state, so a copy of state.db is a copy of
// ciphertext only.
func (p Paths) SecretsKey() string { return filepath.Join(p.Config, "secrets.key") }

// Socket is the daemon's API socket; AGENTBOX_SOCKET overrides it. It lives
// with the state rather than in XDG_RUNTIME_DIR, so separate state
// directories get separate daemons.
func (p Paths) Socket() string {
	if s := os.Getenv("AGENTBOX_SOCKET"); s != "" {
		return s
	}
	return filepath.Join(p.Data, "run", "agentbox.sock")
}

// VM is where AgentBox keeps the Linux VM it runs in when this machine is its
// front end (internal/hostvm/chv): the VM's disks, the programs that run it
// and their sockets. Nothing in it belongs to the daemon, which is in the VM.
func (p Paths) VM() string { return filepath.Join(p.Data, "vm") }

// VMSocket is where the VM's supervisor serves the VM's state and controls
// (api.VMStatus), while it runs.
func (p Paths) VMSocket() string { return filepath.Join(p.Data, "run", "vm.sock") }

// ImageCache is where the image cache agents' Docker shares keeps what it
// downloaded (internal/imagecache), on the disk the daemon's state is on.
func (p Paths) ImageCache() string { return filepath.Join(p.Data, "image-cache") }

// ImageCacheSocket is where the daemon serves the image cache; each agent
// reaches it through a proxy device (agent.ImageCachePort).
func (p Paths) ImageCacheSocket() string { return filepath.Join(p.Data, "run", "image-cache.sock") }

// PackageCache is where the package managers' caches every agent shares are
// kept (internal/pkgcache), on the disk the daemon's state is on: in the VM,
// its own disk, which is far faster than a shared folder for the many small
// files of a pnpm store or a Go build cache.
func (p Paths) PackageCache() string { return filepath.Join(p.Data, "package-cache") }

// AgentSockets is the directory of in-agent API sockets, one per agent.
func (p Paths) AgentSockets() string { return filepath.Join(p.Data, "run", "agents") }

// Tools is where AgentBox keeps the AI tools a project's lead runs on the
// host: Claude Code and its ACP adapter, installed on first use. They never go
// on the user's PATH, and never near the host's own ~/.claude (D6).
func (p Paths) Tools() string { return filepath.Join(p.Data, "tools") }

// LeadHome is the private HOME of a project's lead, holding its settings, its
// brief and its AI tool's session state.
func (p Paths) LeadHome(project string) string {
	return filepath.Join(p.Data, "projects", project, "lead-home")
}

// HomeChat is the Home chat's working directory: ~/.agentbox, made the first
// time the chat is opened. Paths made without Home (a test's) keep it under
// Data instead.
func (p Paths) HomeChat() string {
	if p.Home != "" {
		return p.Home
	}
	return filepath.Join(p.Data, "home")
}

// ProjectNotes is a project's notes for its agents: one markdown file, written
// by the user in the app and by the project's lead, folded into every brief.
// A plain file rather than a column, so it reads and diffs by hand.
func (p Paths) ProjectNotes(project string) string {
	return filepath.Join(p.Data, "projects", project, "notes.md")
}

// ChatImages holds the pictures sent in an agent's chat, or in a project's
// lead's, one file each: kept out of state.db, where the conversation is.
func (p Paths) ChatImages(project, agent string) string {
	return filepath.Join(p.Data, "projects", project, "chat-images", agent)
}

// Worktrees is where agents' worktrees live: with the state, unless
// AGENTBOX_WORKTREES names another directory. On a Mac the daemon runs in a
// Linux VM whose state is on the VM's own disk, and its worktrees go on the
// Mac's home directory, which the VM shares at the same path, so the Mac's
// editors can open them (package hostvm).
func (p Paths) Worktrees() string {
	if d := os.Getenv("AGENTBOX_WORKTREES"); filepath.IsAbs(d) {
		return d
	}
	return filepath.Join(p.Data, "worktrees")
}

// Media is where agents' media is kept, one directory per agent: with the
// state, unless AGENTBOX_MEDIA names another directory. In a VM, the front end
// names one on the host's home, which the VM shares at the same path, so the
// app on the host can open an item with its default app or show it in its
// folder (package hostvm).
func (p Paths) Media() string {
	if d := os.Getenv("AGENTBOX_MEDIA"); filepath.IsAbs(d) {
		return d
	}
	return filepath.Join(p.Data, "media")
}

func (p Paths) Worktree(project, agent string) string {
	return filepath.Join(p.Worktrees(), project, agent)
}
