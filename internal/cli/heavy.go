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
	"strconv"
	"strings"
	"syscall"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"

	"github.com/spf13/cobra"
)

// Heavy commands inside an agent: a test run or a build asks the daemon
// before it starts, waits while the VM's memory is under pressure, and joins
// a cgroup of its own as it starts, so the daemon can pause it alone if
// pressure stays high (internal/daemon/pressure.go). Claude Code agents ask
// by themselves through heavy-hook, which their settings run around Bash
// tool calls (internal/agent/heavyhooks.go); `agentbox heavy` asks around any
// other command.

// heavyClient is the part of the in-agent API heavy commands go through.
type heavyClient interface {
	StartHeavy(ctx context.Context, req api.HeavyRequest) (api.HeavyStart, error)
	JoinHeavy(ctx context.Context, key string, pid int) error
	EndHeavy(ctx context.Context, key string) error
}

// heavyRenew is how often `agentbox heavy` asks again for its command while
// it runs, well inside heavyTTL, which only matters for a command that
// couldn't join a cgroup of its own: the daemon counts it as running for
// that long after it's killed.
const (
	heavyRenew = 30 * time.Second
	heavyTTL   = 2 * time.Minute
)

func newHeavyCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "heavy [--] <command> [args...]",
		Short: "Run a heavy command, waiting while the VM's memory is under pressure",
		Long: `Runs a command that needs a lot of memory for a while (a test suite, a big
build, an emulator) the way an agent's tests and builds run: it waits to start
while the VM's memory is under pressure, saying why once, starts in turn as it
eases, and may be paused, and resumed, if pressure stays high while it runs.

Run it inside an agent. Claude Code agents do this by themselves around tests
and builds; use it for anything else.`,
		Example: `  agentbox heavy -- ./gradlew assembleDebug
  agentbox heavy make -j4`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var c heavyClient
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

// runHeavy runs args once the daemon says it may start. With no in-agent API
// (c nil), outside an agent, it just runs them.
func runHeavy(ctx context.Context, c heavyClient, args []string, stderr io.Writer) (int, error) {
	if args[0] == "--" {
		args = args[1:]
	}
	if len(args) == 0 {
		return 0, errors.New("say which command to run")
	}
	if c != nil {
		key := "heavy-" + randomKey()
		req := api.HeavyRequest{Key: key, Command: strings.Join(args, " "), TTLSeconds: int(heavyTTL.Seconds())}
		start, err := c.StartHeavy(ctx, withWait(req, 2))
		if err == nil && !start.Started {
			_, _ = fmt.Fprintf(stderr, "agentbox heavy: waiting for memory: %s\n", start.Why)
			// Each ask waits up to 10 minutes; asking again keeps its place.
			for err == nil && !start.Started {
				start, err = c.StartHeavy(ctx, req)
			}
		}
		if err != nil {
			if ctx.Err() != nil {
				return 0, ctx.Err()
			}
			// The daemon is unreachable: the command still runs.
			_, _ = fmt.Fprintf(stderr, "agentbox heavy: couldn't ask the daemon (%v); running anyway\n", err)
		} else {
			// In its cgroup before the command starts, so all it starts is.
			_ = c.JoinHeavy(ctx, key, os.Getpid())
			stop := make(chan struct{})
			defer func() {
				close(stop)
				end, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				defer cancel()
				_ = c.EndHeavy(end, key)
			}()
			go func() {
				tick := time.NewTicker(heavyRenew)
				defer tick.Stop()
				for {
					select {
					case <-stop:
						return
					case <-tick.C:
						_, _ = c.StartHeavy(ctx, withWait(req, 1))
					}
				}
			}()
		}
	}
	child := exec.Command(args[0], args[1:]...)
	child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := child.Start(); err != nil {
		return 0, err
	}
	// Ctrl-C and a kill reach the command; this waits for it to end, to say
	// so after.
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

func withWait(req api.HeavyRequest, seconds int) api.HeavyRequest {
	req.WaitSeconds = seconds
	return req
}

func randomKey() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// The hook. Claude Code runs it before and after Bash tool calls
// (agent.withHeavyHooks), with the call as JSON on stdin. It must cost the
// conversation nothing: it prints nothing and exits 0 whenever the command
// may start, isn't heavy, or the daemon can't be reached. Only when the wait
// to start runs out does it print one line, and exit 2, which keeps the tool
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

// heavyKey is the key a call's command runs under, "" for a call that isn't
// a heavy command.
func heavyKey(call hookCall) string {
	if call.Tool != "Bash" || !heavyCommand.MatchString(call.Input.Command) {
		return ""
	}
	id := call.ToolUseID
	if id == "" {
		id = randomKey()
	}
	return "bash-" + id
}

// bashTTL is how long a Bash command that couldn't join a cgroup of its own
// counts as running, unless it ends first.
const bashTTL = 15 * time.Minute

func newHeavyHookCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "heavy-hook",
		Short:  "Claude Code's hook for heavy commands (internal)",
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
func heavyHook(ctx context.Context, in io.Reader, stderr io.Writer, c heavyClient, envFile string) int {
	var call hookCall
	if err := json.NewDecoder(io.LimitReader(in, 4<<20)).Decode(&call); err != nil {
		return 0
	}
	key := heavyKey(call)
	if key == "" {
		return 0
	}
	switch call.Event {
	case "PreToolUse":
		start, err := c.StartHeavy(ctx, api.HeavyRequest{Key: key, Command: call.Input.Command, TTLSeconds: int(bashTTL.Seconds())})
		if err != nil {
			return 0
		}
		if !start.Started {
			_, _ = fmt.Fprintf(stderr, "Not run: it waited 10 minutes to start, as %s. Try it again later, or with fewer packages or workers at once.\n", start.Why)
			return 2
		}
		writeJoin(envFile, key)
	case "PostToolUse", "PostToolUseFailure":
		clearJoin(envFile, key)
		_ = c.EndHeavy(ctx, key)
	}
	return 0
}

// joinLine is what the env file holds while a command that started hasn't
// joined its run yet: the Bash tool's next shell runs it as it starts
// (BASH_ENV), which puts that shell, and so the command, in the run's
// cgroup, and empties the file so no shell after it does.
func joinLine(key string) string {
	return fmt.Sprintf("%s heavy-join %s \"$$\" 2>/dev/null || :\n", agent.AgentBinaryPath, shellQuote(key))
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// writeJoin leaves key's join line where BASH_ENV points. Written beside it
// and renamed over it, so a shell starting meanwhile reads all of it or none.
func writeJoin(path, key string) { writeEnvFile(path, joinLine(key)) }

// clearJoin empties the env file if it still holds key's join line.
func clearJoin(path, key string) {
	if b, err := os.ReadFile(path); err == nil && string(b) == joinLine(key) {
		writeEnvFile(path, "")
	}
}

func writeEnvFile(path, text string) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".heavy-*.env")
	if err != nil {
		return
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.WriteString(text); err != nil || tmp.Close() != nil {
		return
	}
	_ = os.Rename(tmp.Name(), path)
}

func newHeavyJoinCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "heavy-join <key> <pid>",
		Short:  "Put a shell in its heavy command's cgroup (internal)",
		Hidden: true,
		Args:   cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			socket := inAgentSocket()
			pid, err := strconv.Atoi(args[1])
			if !fileExists(socket) || err != nil {
				return nil
			}
			home, _ := os.UserHomeDir()
			heavyJoin(cmd.Context(), api.NewClient(socket), filepath.Join(home, agent.HeavyEnvFile), args[0], pid)
			return nil
		},
		SilenceErrors: true,
		SilenceUsage:  true,
	}
}

// heavyJoin empties the env file first, so the shells the command starts
// don't join again, then puts pid in key's cgroup. It says nothing whatever
// happens: the command runs either way.
func heavyJoin(ctx context.Context, c heavyClient, envFile, key string, pid int) {
	clearJoin(envFile, key)
	_ = c.JoinHeavy(ctx, key, pid)
}
