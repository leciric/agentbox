// Package machines gives an AI tool running on the user's own machine — Claude
// Code or Codex, inside t3code or on their own — a desktop machine to test its
// app in: `agentbox machines mcp`, a stdio MCP server registered at user scope
// (`agentbox machines install`).
//
// A machine belongs to a worktree: the directory the AI tool's session runs in.
// It has the worktree mounted at the same path, a virtual display with
// Chromium on it (the agents' own browser.sh), ffmpeg and Playwright's MCP
// server, and nothing else of the user's: no GitHub or Claude tokens, only the
// environment the project's .env gives it.
//
// The machine comes from a Backend. The only one so far is Docker's (Podman's
// when it is installed), which needs neither the daemon nor AgentBox's VM; the
// VM's Incus machines are meant to be a second one.
package machines

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Backend runs machines, one per worktree.
type Backend interface {
	// Name is what machine_status calls it: docker, podman.
	Name() string
	// Start makes the worktree's machine and starts it, or starts the one
	// there is. A machine made with another Config is made again.
	Start(ctx context.Context, worktree string, c Config, progress func(string)) (Status, error)
	// Stop stops it. What it keeps is the backend's: its next Start is
	// quicker for it.
	Stop(ctx context.Context, worktree string) error
	// Remove deletes it and whatever it kept.
	Remove(ctx context.Context, worktree string) error
	// Status is the worktree's machine, which may not exist.
	Status(ctx context.Context, worktree string) (Status, error)
	// List is every machine there is.
	List(ctx context.Context) ([]Status, error)
	// Command is name with args, run in the worktree's machine as its user,
	// in the worktree, with DISPLAY set. stdin is kept open when the caller
	// sets cmd.Stdin.
	Command(ctx context.Context, worktree string, name string, args ...string) *exec.Cmd
}

// Status is one machine.
type Status struct {
	Backend  string `json:"backend"`
	Name     string `json:"name"`
	Worktree string `json:"worktree"`
	Exists   bool   `json:"exists"`
	Running  bool   `json:"running"`
	// Ports are the published ones: the port in the machine to the address
	// on this one.
	Ports        map[int]string `json:"ports,omitempty"`
	DockerInside bool           `json:"docker_inside,omitempty"`
	Memory       string         `json:"memory,omitempty"`
	// Started is when a running machine started.
	Started time.Time `json:"started,omitzero"`

	// config is the Config.Hash it was made with.
	config string
}

// ConfigFile is a project's machine configuration, in its repository:
// committed, every worktree of it has the same.
const ConfigFile = ".agentbox/machine.json"

// Config is how a project's machines are made.
type Config struct {
	// DockerInside runs a Docker daemon in the machine, for kind and
	// docker compose: the container is privileged, with raised inotify and
	// open-file limits. Off, it is an ordinary unprivileged container.
	DockerInside bool `json:"docker_inside,omitempty"`
	// Memory caps the machine, as Docker writes it: 4g, 512m.
	Memory string `json:"memory,omitempty"`
	// Ports are published on 127.0.0.1, each on a port of its own, for
	// preview_url. Docker can't add one to a running container.
	Ports []int `json:"ports,omitempty"`
	// EnvFiles are the worktree's files whose variables the machine gets,
	// .env by default. A file that isn't there is skipped.
	EnvFiles []string `json:"env_files,omitempty"`
	// Image replaces the machine image `agentbox machines build` makes.
	Image string `json:"image,omitempty"`
	// BrowserTools gives the session Playwright's browser_* tools, which read
	// a page's DOM. Off, the desktop tools are the only ones that operate the
	// app, which is what the user sees.
	BrowserTools bool `json:"browser_tools,omitempty"`

	// env is what EnvFiles held, read by Load.
	env map[string]string
}

// Defaults for what a project's file doesn't say.
var (
	DefaultMemory   = "4g"
	DefaultPorts    = []int{3000, 4173, 5173, 8000, 8080}
	DefaultEnvFiles = []string{".env"}
)

// Load reads the worktree's machine configuration: its own .agentbox/machine.json,
// or the main checkout's when it is a linked worktree without one, then the
// env files it names.
func Load(worktree string) (Config, error) {
	var c Config
	for _, dir := range []string{worktree, mainCheckout(worktree)} {
		if dir == "" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, ConfigFile))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return c, err
		}
		if err := json.Unmarshal(data, &c); err != nil {
			return c, fmt.Errorf("%s: %w", filepath.Join(dir, ConfigFile), err)
		}
		break
	}
	if c.Memory == "" {
		c.Memory = DefaultMemory
	}
	if c.Ports == nil {
		c.Ports = DefaultPorts
	}
	if c.EnvFiles == nil {
		c.EnvFiles = DefaultEnvFiles
	}
	c.env = map[string]string{}
	for _, f := range c.EnvFiles {
		path := f
		if !filepath.IsAbs(path) {
			path = filepath.Join(worktree, f)
		}
		vars, err := readEnvFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return c, err
		}
		for k, v := range vars {
			c.env[k] = v
		}
	}
	return c, nil
}

// Env is the machine's environment, from the env files.
func (c Config) Env() map[string]string { return c.env }

// Hash names a configuration: a machine made with another one is made again.
// The environment's values count, so a changed secret reaches the machine.
func (c Config) Hash(image string) string {
	h := sha256.New()
	keys := make([]string, 0, len(c.env))
	for k := range c.env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	_, _ = fmt.Fprintf(h, "%s\n%t\n%s\n%v\n", image, c.DockerInside, c.Memory, c.Ports)
	for _, k := range keys {
		_, _ = fmt.Fprintf(h, "%s=%s\n", k, c.env[k])
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

var envKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// readEnvFile reads a dotenv file: KEY=value lines, an optional `export `,
// single or double quotes around a value, and # comments. It doesn't expand
// variables, which Docker's own --env-file doesn't either.
func readEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	vars := map[string]string{}
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for n := 1; s.Scan(); n++ {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		k, v, ok := strings.Cut(line, "=")
		k = strings.TrimSpace(k)
		if !ok || !envKey.MatchString(k) {
			return nil, fmt.Errorf("%s:%d: not KEY=value", path, n)
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			quote := v[0]
			v = v[1 : len(v)-1]
			if quote == '"' {
				v = strings.NewReplacer(`\n`, "\n", `\"`, `"`, `\\`, `\`).Replace(v)
			}
		} else if i := strings.Index(v, " #"); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		vars[k] = v
	}
	return vars, s.Err()
}

// Worktree is the machine's worktree for a session started in dir: the top
// of its git checkout, or dir itself outside one. The home directory and /
// are refused: the machine mounts its worktree, and those hold everything.
func Worktree(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output(); err == nil {
		if top := strings.TrimSpace(string(out)); top != "" {
			dir = top
		}
	}
	home, _ := os.UserHomeDir()
	if dir == "/" || (home != "" && filepath.Clean(dir) == filepath.Clean(home)) {
		return "", fmt.Errorf("%s isn't a project: start the session in the project's directory, which the machine mounts", dir)
	}
	return dir, nil
}

// mainCheckout is the main checkout of a linked worktree, or "" for one that
// isn't: where a project's configuration is when a worktree doesn't have it,
// because it isn't committed yet.
func mainCheckout(worktree string) string {
	common := gitCommonDir(worktree)
	if common == "" || filepath.Base(common) != ".git" {
		return ""
	}
	if main := filepath.Dir(common); main != filepath.Clean(worktree) {
		return main
	}
	return ""
}

// gitCommonDir is the repository a linked worktree's .git file points into,
// when it is outside the worktree: the machine mounts it too, so git works in
// there. "" for a worktree with its own .git directory, or none.
func gitCommonDir(worktree string) string {
	data, err := os.ReadFile(filepath.Join(worktree, ".git"))
	if err != nil {
		return ""
	}
	gitdir, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
	if !ok {
		return ""
	}
	gitdir = strings.TrimSpace(gitdir)
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(worktree, gitdir)
	}
	common := filepath.Dir(filepath.Dir(gitdir)) // <repo>/.git/worktrees/<name>
	if b, err := os.ReadFile(filepath.Join(gitdir, "commondir")); err == nil {
		c := strings.TrimSpace(string(b))
		if !filepath.IsAbs(c) {
			c = filepath.Join(gitdir, c)
		}
		common = c
	}
	common = filepath.Clean(common)
	if strings.HasPrefix(common, filepath.Clean(worktree)+string(filepath.Separator)) {
		return ""
	}
	return common
}

// containerName is the worktree's machine's name: readable, and unique to
// its path.
func containerName(worktree string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(worktree)))
	base := strings.ToLower(filepath.Base(worktree))
	base = regexp.MustCompile(`[^a-z0-9_.-]+`).ReplaceAllString(base, "-")
	base = strings.Trim(base, "-.")
	if len(base) > 32 {
		base = base[:32]
	}
	if base == "" {
		base = "worktree"
	}
	return "agentbox-machine-" + base + "-" + hex.EncodeToString(sum[:])[:8]
}
