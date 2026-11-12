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
	"runtime"
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
	TestPolicy(ctx context.Context) (api.TestPolicy, error)
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
	return runHeavyCommand(ctx, c, heavyCommandRun{path: args[0], argv: args, command: strings.Join(args, " ")}, stderr)
}

// heavyCommandRun is one heavy command to run: the executable, its argv
// (argv[0] the name it is run by), and how it is shown.
type heavyCommandRun struct {
	path    string
	argv    []string
	command string
}

// workersFor is the worker counts a heavy command starts with: the
// machine's cores, halved when the VM's memory is short or the command had
// to wait for it. A variable for tests.
var workersFor = func(env []string, waited bool) []string {
	short := waited
	if b, err := os.ReadFile(agent.MemoryShortFile); err == nil && len(strings.TrimSpace(string(b))) > 0 {
		short = true
	}
	return agent.WorkerEnv(env, machineCPUs("/sys/fs/cgroup/cpu.max"), memAvailable(), short)
}

// machineCPUs is the cores this machine may use, as nproc counts them: the
// CPUs it may run on, capped by its cgroup's cpu.max quota, which is how
// AgentBox gives each agent its share of the VM's cores.
func machineCPUs(cpuMax string) int {
	n := runtime.NumCPU()
	b, err := os.ReadFile(cpuMax)
	if err != nil {
		return n
	}
	quota, period, ok := strings.Cut(strings.TrimSpace(string(b)), " ")
	q, err1 := strconv.ParseInt(quota, 10, 64)
	p, err2 := strconv.ParseInt(period, 10, 64)
	if !ok || err1 != nil || err2 != nil || q <= 0 || p <= 0 {
		return n // "max": no quota
	}
	return max(1, min(n, int((q+p-1)/p)))
}

// memAvailable is MemAvailable from /proc/meminfo, in bytes; 0 unread.
func memAvailable() int64 {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(line, "MemAvailable:"); ok {
			kb, _ := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(rest), " kB"), 10, 64)
			return kb << 10
		}
	}
	return 0
}

// runHeavyCommand asks the daemon before run starts, waiting its turn,
// starts it in a cgroup of its own with the worker counts the VM's memory
// allows, and says it ended once it does.
func runHeavyCommand(ctx context.Context, c heavyClient, run heavyCommandRun, stderr io.Writer) (int, error) {
	env := os.Environ()
	waited := false
	key := "heavy-" + randomKey()
	if c != nil {
		req := api.HeavyRequest{Key: key, Command: run.command, TTLSeconds: int(heavyTTL.Seconds())}
		start, err := c.StartHeavy(ctx, withWait(req, 2))
		if err == nil && !start.Started {
			waited = true
			_, _ = fmt.Fprintf(stderr, "agentbox: %s waits for memory: %s. It starts by itself in turn.\n", headWordsCLI(run.command, 60), start.Why)
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
		env = workersFor(env, waited)
	}
	env = append(env, agent.HeavyEnv+"="+key)
	child := exec.Command(run.path)
	child.Args = run.argv
	child.Env = env
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

// headWordsCLI is s cut to n characters on a word, with an ellipsis.
func headWordsCLI(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	if i := strings.LastIndexByte(s[:n], ' '); i > n/2 {
		return s[:i] + "…"
	}
	return s[:n] + "…"
}

// The shims (agent/heavyshims.go): go, cargo, npm, make… are first in an
// agent's PATH, and run `agentbox heavy-shim <name> <shim dir> args…`, which
// runs the real command at once unless it is heavy, and otherwise as
// `agentbox heavy` would.

func newHeavyShimCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "heavy-shim <name> <shim dir> [args...]",
		Short:  "Run a shimmed command, waiting for memory when it is heavy (internal)",
		Hidden: true,
		Args:   cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			code := heavyShim(cmd.Context(), args[0], args[1], args[2:], cmd.ErrOrStderr())
			if code != 0 {
				return exitCodeError(code)
			}
			return nil
		},
		SilenceErrors:      true,
		SilenceUsage:       true,
		DisableFlagParsing: true,
	}
	return cmd
}

// execReal replaces this process with the real command: a variable for
// tests.
var execReal = func(path string, argv, env []string) error { return syscall.Exec(path, argv, env) }

// heavyShim runs name, the real one past dir in PATH, with args.
func heavyShim(ctx context.Context, name, dir string, args []string, stderr io.Writer) int {
	real, ok := agent.FindReal(name, dir, os.Getenv("PATH"))
	if !ok {
		_, _ = fmt.Fprintf(stderr, "%s: command not found\n", name)
		return 127
	}
	argv := append([]string{name}, args...)
	command := shellJoin(argv)
	socket := inAgentSocket()
	if os.Getenv(agent.HeavyEnv) != "" || !isHeavy(command) || !fileExists(socket) {
		if err := execReal(real, argv, os.Environ()); err != nil {
			_, _ = fmt.Fprintf(stderr, "%s: %v\n", name, err)
			return 126
		}
		return 0
	}
	code, err := runHeavyCommand(ctx, api.NewClient(socket), heavyCommandRun{path: real, argv: argv, command: command}, stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return max(code, 1)
	}
	return code
}

// shellJoin is argv as a command line isHeavy reads: words with spaces
// quoted.
func shellJoin(argv []string) string {
	out := make([]string, len(argv))
	for i, a := range argv {
		if a == "" || strings.ContainsAny(a, " \t\n'\"\\$`;&|<>()") {
			out[i] = shellQuote(a)
		} else {
			out[i] = a
		}
	}
	return strings.Join(out, " ")
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
// may start, isn't heavy, or the daemon can't be reached. Only when the
// command is a whole test suite the agent may not run (fullsuite.go) does it
// print, and exit 2, which keeps the tool from running and tells the model
// why. Waiting for memory is the shims' (heavyShim), for every tool alike.

// heavyCommand matches a Bash command that runs tests, builds or the app:
// the commands an agent's memory spikes in. Only a command's own words count,
// where a command starts (after ;, &&, |, a newline, $( or bash -c ", past
// VAR=x, sudo, time, timeout 60 and a path): `git commit -m "fix make"`, an
// `echo jest` or a comment saying "make sure" isn't one. See isHeavy.
var heavyCommand = regexp.MustCompile(`(?m)(?:^|[;&|(\x60]|\$\(|-c\s+["']|\b(?:then|do|else)\s)\s*` +
	`(?:(?:[A-Za-z_][A-Za-z0-9_]*=\S*|sudo|time|nice|nohup|env|exec|command|timeout\s+\S+|agentbox\s+heavy\s+--)\s+)*` +
	`(?:\S*/)?(?:` +
	`go\s+(?:test|build|install|run|generate)\b|` +
	`(?:npm|pnpm|yarn|bun)\s+(?:--prefix[\s=]\S+\s+|-C\s+\S+\s+|--filter[\s=]\S+\s+)*(?:(?:run\s+)?(?:test|build|e2e|dist|start|dev|preview)\b|exec\s+(?:vitest|jest|playwright|electron))|` +
	`(?:npx|pnpx|bunx)\s+(?:vitest|jest|playwright|tsc|electron|next|vite)\b|` +
	`(?:(?:npx|pnpx|bunx)\s+)?vp\s+(?:test|build|dev|preview)\b|` +
	`vite(?:\s+(?:build|dev|serve|preview|--host|--port|--config|--mode)\b|\s*(?:$|[;&|)]))|` +
	`node\s+(?:-\S+\s+)*(?:\S*/)?build[\w.-]*\.[cm]?js\b|` +
	`(?:vitest|jest|pytest|tox|tsc|electron|gradle|gradlew|mvn|make|cmake|ninja|bazel|dotnet)\b|` +
	`(?:cargo|cross)\s+(?:\+\S+\s+|(?:--(?:manifest-path|config|color|target-dir|target|features|package)|-[CZp])(?:\s+|=)\S+\s+|-\S+\s+)*(?:test|build|run|bench|check|clippy|nextest|install|xtask|doc|miri)\b|` +
	`(?:rustc|just)\b|` +
	`docker\s+(?:build|buildx|compose\s+(?:up|build))\b|` +
	`playwright\s+test\b` +
	`)`)

// heredoc is where a here-document starts: <<WORD, <<-'WORD' or <<"WORD",
// not a here-string's <<<.
var heredoc = regexp.MustCompile(`(?:^|[^<])<<(-?)\s*['"]?([A-Za-z_][A-Za-z0-9_]*)['"]?`)

// isHeavy reports whether a Bash command is heavy. A here-document's body
// is data, not commands, and is left out: on 2026-10-12 a `python3 - <<'EOF'`
// edit whose script said "make sure" waited six minutes for memory as if it
// were `make`.
func isHeavy(command string) bool {
	return heavyCommand.MatchString(withoutHeredocs(command))
}

// withoutHeredocs is command without its here-documents' bodies.
func withoutHeredocs(command string) string {
	lines := strings.Split(command, "\n")
	var out []string
	var ends []string // the words that end the here-documents still open
	var tabs []bool
	for _, line := range lines {
		if len(ends) > 0 {
			l := line
			if tabs[0] {
				l = strings.TrimLeft(l, "\t")
			}
			if l == ends[0] {
				ends, tabs = ends[1:], tabs[1:]
			}
			continue
		}
		out = append(out, line)
		for _, m := range heredoc.FindAllStringSubmatch(line, -1) {
			ends = append(ends, m[2])
			tabs = append(tabs, m[1] == "-")
		}
	}
	return strings.Join(out, "\n")
}

// hookCall is what Claude Code gives a hook on stdin.
type hookCall struct {
	Event     string `json:"hook_event_name"`
	Tool      string `json:"tool_name"`
	ToolUseID string `json:"tool_use_id"`
	Cwd       string `json:"cwd"`
	Input     struct {
		Command string `json:"command"`
	} `json:"tool_input"`
}

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
	if call.Event == "PreToolUse" && call.Tool == "Bash" {
		if run, ok := fullSuite(call.Input.Command, call.Cwd); ok {
			// Asked only now, so a command that isn't one costs nothing.
			if policy, err := c.TestPolicy(ctx); err == nil && policy.BlockFullSuite {
				_, _ = fmt.Fprint(stderr, fullSuiteMessage(ctx, run, policy.Base))
				return 2
			}
		}
	}
	// A heavy command waits its turn in its shim (agent/heavyshims.go),
	// whichever tool runs it: the hook no longer asks for it as well. An
	// earlier hook's join line, if one is left, goes.
	if b, err := os.ReadFile(envFile); err == nil && len(b) > 0 {
		writeEnvFile(envFile, "")
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
