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
	// WorktreesDir, when set, is Worktrees: where that directory really is,
	// which the daemon finds once its data is in place (on a Cloud Hypervisor
	// VM, the data directory's worktrees is a symlink to the agents' disk).
	// Incus mounts, and git records, the path with no symlink in it.
	WorktreesDir string
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

// Customization is the user's customization of the app's look and layout
// (internal/customize): in their configuration, beside the credentials, and
// in the data directory when there is no configuration directory.
func (p Paths) Customization() string {
	if p.Config == "" {
		return filepath.Join(p.Data, "customization.json")
	}
	return filepath.Join(p.Config, "customization.json")
}

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
// downloaded (internal/imagecache). In a VM it's a symlink to the agents'
// disk (daemon.moveDataToPool), as are PackageCache and Tools.
func (p Paths) ImageCache() string { return filepath.Join(p.Data, "image-cache") }

// ImageCacheSocket is where the daemon serves the image cache; each agent
// reaches it through a proxy device (agent.ImageCachePort).
func (p Paths) ImageCacheSocket() string { return filepath.Join(p.Data, "run", "image-cache.sock") }

// PackageCache is where the package managers' caches every agent shares are
// kept (internal/pkgcache): in the VM, on one of its own disks, which are far
// faster than a shared folder for the many small files of a pnpm store or a
// Go build cache.
func (p Paths) PackageCache() string { return filepath.Join(p.Data, "package-cache") }

// NodeModules is where agents' node_modules are, and the snapshots new
// agents' are filled from (internal/nodemods): beside PackageCache, on a disk
// of the VM's rather than in worktrees in a shared folder.
func (p Paths) NodeModules() string { return filepath.Join(p.Data, "node-modules") }

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

// LeadSessionHome is the private HOME of one of a project's chat sessions,
// so its AI tool's session state and the lead socket its MCP server is given
// are its own. The session migrated from the chat before sessions ("main")
// keeps LeadHome.
func (p Paths) LeadSessionHome(project, session string) string {
	if session == "" || session == "main" {
		return p.LeadHome(project)
	}
	return filepath.Join(p.Data, "projects", project, "lead-sessions", session)
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

// Worktrees is where new agents' worktrees go: with the state, unless
// AGENTBOX_WORKTREES names another directory. In a VM that's the VM's own
// disk, where git and builds don't go through the folder it shares with the
// computer it runs on; agents made before that keep theirs on the share
// (SharedWorktrees).
func (p Paths) Worktrees() string {
	if p.WorktreesDir != "" {
		return p.WorktreesDir
	}
	if d := os.Getenv("AGENTBOX_WORKTREES"); filepath.IsAbs(d) {
		return d
	}
	return filepath.Join(p.Data, "worktrees")
}

// SharedWorktreesEnv names, in a VM, the worktrees directory on the host's
// home, which the VM shares at the same path: where agents' worktrees were
// made until they moved to the VM's own disk, and where those agents keep
// them (package hostvm sets it).
const SharedWorktreesEnv = "AGENTBOX_SHARED_WORKTREES"

// SharedWorktrees is SharedWorktreesEnv's directory, or "" outside a VM, or
// when it's Worktrees itself.
func (p Paths) SharedWorktrees() string {
	d := os.Getenv(SharedWorktreesEnv)
	if !filepath.IsAbs(d) || filepath.Clean(d) == filepath.Clean(p.Worktrees()) {
		return ""
	}
	return filepath.Clean(d)
}

// SharedWorktree is where an agent's worktree is in SharedWorktrees, or "".
func (p Paths) SharedWorktree(project, agent string) string {
	if d := p.SharedWorktrees(); d != "" {
		return filepath.Join(d, project, agent)
	}
	return ""
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

// WorktreeTrash is where destroyed agents' worktrees wait to be deleted:
// beside Worktrees, so moving one there is a rename on the same disk.
func (p Paths) WorktreeTrash() string { return trashBeside(p.Worktrees()) }

// WorktreeTrashes are the trashes of Worktrees and SharedWorktrees, each
// beside its own, with the worktrees directory each one is for.
func (p Paths) WorktreeTrashes() (trash, worktrees []string) {
	for _, d := range []string{p.Worktrees(), p.SharedWorktrees()} {
		if d != "" {
			trash, worktrees = append(trash, trashBeside(d)), append(worktrees, filepath.Clean(d))
		}
	}
	return trash, worktrees
}

func trashBeside(worktrees string) string {
	return filepath.Join(filepath.Dir(filepath.Clean(worktrees)), "worktrees-trash")
}

func (p Paths) Worktree(project, agent string) string {
	return filepath.Join(p.Worktrees(), project, agent)
}
