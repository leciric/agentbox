package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"

	"github.com/spf13/cobra"
)

// Heavy phases inside an agent: a test run, a build, the browser, a
// recording take the agent's burst from the VM's burst pool as a lease, and
// wait for one when the pool is full (internal/daemon/burst.go). Claude Code
// agents take them by themselves through heavy-hook, which their settings
// run around tool calls (internal/agent/heavyhooks.go); `agentbox heavy`
// takes one around any other command.

// burstClient is the part of the in-agent API leases go through.
type burstClient interface {
	AcquireBurst(ctx context.Context, req api.BurstRequest) (api.BurstLease, error)
	ReleaseBurst(ctx context.Context, key string) (api.BurstLease, error)
}

// heavyRenew is how often `agentbox heavy` renews its key, well inside its
// TTL, so a lease outlives the command only by that TTL if it's killed.
const (
	heavyRenew = 30 * time.Second
	heavyTTL   = 2 * time.Minute
)

func newHeavyCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "heavy [--] <command> [args...]",
		Short: "Run a heavy command with memory from the VM's burst pool",
		Long: `Runs a command that needs a lot of memory for a while (a test suite, a big
build, an emulator) with this agent's burst from the VM's burst pool, and gives
it back when the command ends.

Run it inside an agent. When other agents' heavy phases hold the pool, it waits
for room, saying why once, and runs the command with its test runners'
parallelism held to the lease (GOFLAGS=-p, VITEST_MAX_THREADS, ...). Claude
Code agents take a lease by themselves around tests, builds and the browser;
use this for anything else.`,
		Example: `  agentbox heavy -- ./gradlew assembleDebug
  agentbox heavy make -j4`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var c burstClient
			if socket := inAgentSocket(); fileExists(socket) {
				c = api.NewClient(socket)
			}
			code, err := runHeavy(cmd.Context(), c, args, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			if code != 0 {
				return exitCodeError(code)
			}
			return nil
		},
	}
	cmd.Flags().SetInterspersed(false)
	return cmd
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// runHeavy runs args under a lease. With no in-agent API (c nil), outside
// an agent, it just runs them.
func runHeavy(ctx context.Context, c burstClient, args []string, stderr io.Writer) (int, error) {
	var env map[string]string
	if c != nil {
		key := "heavy-" + randomKey()
		lease, err := c.AcquireBurst(ctx, api.BurstRequest{Key: key, TTLSeconds: int(heavyTTL.Seconds()), WaitSeconds: 2})
		if err == nil && !lease.Granted {
			_, _ = fmt.Fprintf(stderr, "agentbox heavy: waiting for memory: %s\n", lease.Why)
			lease, err = c.AcquireBurst(ctx, api.BurstRequest{Key: key, TTLSeconds: int(heavyTTL.Seconds())})
		}
		switch {
		case err != nil:
			// The daemon is unreachable: the command still runs, unleased.
			_, _ = fmt.Fprintf(stderr, "agentbox heavy: no lease (%v); running anyway\n", err)
		case !lease.Granted:
			return 1, fmt.Errorf("no memory for it after 10 minutes: %s. Try again later", lease.Why)
		default:
			env = lease.Env
			stop := make(chan struct{})
			defer func() {
				close(stop)
				release, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				defer cancel()
				_, _ = c.ReleaseBurst(release, key)
			}()
			go func() {
				tick := time.NewTicker(heavyRenew)
				defer tick.Stop()
				for {
					select {
					case <-stop:
						return
					case <-tick.C:
						_, _ = c.AcquireBurst(ctx, api.BurstRequest{Key: key, TTLSeconds: int(heavyTTL.Seconds()), WaitSeconds: 1})
					}
				}
			}()
		}
	}
	if args[0] == "--" {
		args = args[1:]
	}
	if len(args) == 0 {
		return 0, errors.New("say which command to run")
	}
	child := exec.Command(args[0], args[1:]...)
	child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
	child.Env = withBurstEnv(os.Environ(), env)
	if err := child.Start(); err != nil {
		return 0, err
	}
	// Ctrl-C and a kill reach the command; this waits for it to end, to give
	// the lease back after.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(signals)
	go func() {
		for sig := range signals {
			_ = child.Process.Signal(sig)
		}
	}()
	err := child.Wait()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return max(exit.ExitCode(), 1), nil
	}
	return 0, err
}

func randomKey() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// withBurstEnv is environ with a lease's variables set: GOFLAGS added to,
// the rest replaced.
func withBurstEnv(environ []string, env map[string]string) []string {
	if len(env) == 0 {
		return environ
	}
	out := make([]string, 0, len(environ)+len(env))
	for _, kv := range environ {
		name, value, _ := strings.Cut(kv, "=")
		if v, ok := env[name]; ok {
			if name == "GOFLAGS" && value != "" {
				v = value + " " + v
			}
			out = append(out, name+"="+v)
			continue
		}
		out = append(out, kv)
	}
	for _, name := range sortedKeys(env) {
		if !hasVar(environ, name) {
			out = append(out, name+"="+env[name])
		}
	}
	return out
}

func hasVar(environ []string, name string) bool {
	for _, kv := range environ {
		if strings.HasPrefix(kv, name+"=") {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// The hook. Claude Code runs it before and after Bash and the browser's
// tools (agent.withHeavyHooks), with the call as JSON on stdin. It must cost
// the conversation nothing: it prints nothing and exits 0 whenever it gets a
// lease, isn't needed, or can't reach the daemon. Only when the wait for a
// lease runs out does it print one line, and exit 2, which keeps the tool
// from running and tells the model why.

// heavyCommand matches a Bash command that runs tests, builds or the app:
// the commands an agent's memory spikes in.
var heavyCommand = regexp.MustCompile(`(?:^|[\s;&|(/])(?:` +
	`go\s+(?:test|build|install|run|generate)\b|` +
	`(?:npm|pnpm|yarn|bun)\s+(?:--prefix[\s=]\S+\s+|-C\s+\S+\s+|--filter[\s=]\S+\s+)*(?:(?:run\s+)?(?:test|build|e2e|dist|start|dev|preview)\b|exec\s+(?:vitest|jest|playwright|electron))|` +
	`(?:npx|pnpx|bunx)\s+(?:vitest|jest|playwright|tsc|electron|next|vite)\b|` +
	`(?:vitest|jest|pytest|tox|tsc|electron|gradle|gradlew|mvn|make|cmake|ninja|bazel|dotnet)\b|` +
	`cargo\s+(?:test|build|run|bench|check|clippy)\b|` +
	`docker\s+(?:build|buildx|compose\s+(?:up|build))\b|` +
	`playwright\s+test\b` +
	`)`)

// hookCall is what Claude Code gives a hook on stdin.
type hookCall struct {
	Event     string `json:"hook_event_name"`
	Tool      string `json:"tool_name"`
	ToolUseID string `json:"tool_use_id"`
	Input     struct {
		Command string `json:"command"`
	} `json:"tool_input"`
}

// Bash commands hold their own key for as long as they run; the browser one
// key between them, renewed by each call and dropped when idle.
const (
	bashLeaseTTL    = 15 * time.Minute
	browserLeaseTTL = 5 * time.Minute
	browserKey      = "browser"
)

// leaseKey is the key a call takes its lease under, and for how long; "" for
// a call that needs none.
func leaseKey(call hookCall) (key string, ttl time.Duration, sticky bool) {
	switch {
	case call.Tool == "Bash":
		if !heavyCommand.MatchString(call.Input.Command) {
			return "", 0, false
		}
		id := call.ToolUseID
		if id == "" {
			id = randomKey()
		}
		return "bash-" + id, bashLeaseTTL, false
	case strings.HasPrefix(call.Tool, "mcp__playwright__"), strings.HasPrefix(call.Tool, "mcp__desktop__"):
		return browserKey, browserLeaseTTL, true
	}
	return "", 0, false
}

func newHeavyHookCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "heavy-hook",
		Short:  "Claude Code's hook for heavy phases (internal)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			socket := inAgentSocket()
			if !fileExists(socket) {
				return nil
			}
			home, _ := os.UserHomeDir()
			code := heavyHook(cmd.Context(), cmd.InOrStdin(), cmd.ErrOrStderr(), api.NewClient(socket), filepath.Join(home, agent.HeavyEnvFile))
			if code != 0 {
				return exitCodeError(code)
			}
			return nil
		},
		SilenceErrors: true,
		SilenceUsage:  true,
	}
}

// heavyHook handles one hook call, and answers its exit code.
func heavyHook(ctx context.Context, in io.Reader, stderr io.Writer, c burstClient, envFile string) int {
	var call hookCall
	if err := json.NewDecoder(io.LimitReader(in, 4<<20)).Decode(&call); err != nil {
		return 0
	}
	key, ttl, sticky := leaseKey(call)
	if key == "" {
		return 0
	}
	switch call.Event {
	case "PreToolUse":
		lease, err := c.AcquireBurst(ctx, api.BurstRequest{Key: key, TTLSeconds: int(ttl.Seconds())})
		if err != nil {
			return 0
		}
		if !lease.Granted {
			_, _ = fmt.Fprintf(stderr, "Not run: no memory for it after 10 minutes, %s. Try it again later.\n", lease.Why)
			return 2
		}
		writeBurstEnv(envFile, lease.Env)
	case "PostToolUse", "PostToolUseFailure":
		if sticky {
			return 0
		}
		left, err := c.ReleaseBurst(ctx, key)
		if err != nil {
			return 0
		}
		writeBurstEnv(envFile, left.Env)
	}
	return 0
}

// writeBurstEnv leaves env where BASH_ENV points, as a script the Bash tool's
// shell sources, or empties it. Written beside it and renamed over it, so a
// shell starting meanwhile reads all of it or none.
func writeBurstEnv(path string, env map[string]string) {
	var b strings.Builder
	for _, name := range sortedKeys(env) {
		value := strings.ReplaceAll(env[name], `"`, `\"`)
		if name == "GOFLAGS" {
			fmt.Fprintf(&b, "export GOFLAGS=\"${GOFLAGS:+$GOFLAGS }%s\"\n", value)
			continue
		}
		fmt.Fprintf(&b, "export %s=\"%s\"\n", name, value)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".heavy-*.env")
	if err != nil {
		return
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.WriteString(b.String()); err != nil || tmp.Close() != nil {
		return
	}
	_ = os.Rename(tmp.Name(), path)
}
