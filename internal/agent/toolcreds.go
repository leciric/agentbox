package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"path"
	"slices"
	"strconv"
	"strings"

	"agentbox/internal/state"
	"agentbox/internal/toolcreds"
)

// An agent's secrets are exported in its env file, which every login shell in
// its machine sources, so they reach the AI tool too, and the tools take a
// credential from the environment over the account they were signed in with
// (an ANTHROPIC_API_KEY secret had every Claude Code of a project billing the
// key instead of its subscription). The tools are therefore started with the
// variables they choose a credential by removed, and those come back only in
// the shells the tool starts to run a command, through BASH_ENV:
//
//   - The variables are removed by `env -u`, which then execs mise, which
//     execs the tool: no shell runs between the removal and the tool, so
//     nothing (BASH_ENV, a profile, a .bashrc) can source them back into the
//     tool's own process. Subagents are the same process, and whatever the tool
//     spawns without a shell (its MCP servers) inherits the clean environment.
//   - BASH_ENV names bash_env, which sources the secrets file again only when
//     bash was started to run a command string (bash -c, BASH_EXECUTION_STRING),
//     as the Bash tool, Codex and OpenCode run commands. A script that bash
//     runs by its shebang doesn't get them, so a tool launcher written in bash
//     can't put them back either.
//
// What still gets them is what the agent's code starts from such a shell, a
// `claude -p` in a test included: that is the agent's own doing, and the reason
// it was given the secret.

// toolHides are the variables an agent's AI tool must start without: its
// credential variables, when the tool runs on an account AgentBox signed it in
// with. A tool with no such account keeps them, since then they are how it
// signs in.
func (m *Manager) toolHides(a state.Agent) ([]string, error) {
	switch a.AI {
	case "claude":
		token, err := m.Creds.ClaudeToken(a.ClaudeAccount)
		if err != nil || token == "" {
			return nil, err
		}
	case "codex":
		if !m.Creds.HasCodexLogin(a.CodexAccount) {
			return nil, nil
		}
	case "opencode":
		if !m.Creds.HasOpenCodeLogin(a.OpenCodeAccounts) {
			return nil, nil
		}
	default:
		return nil, nil
	}
	return toolcreds.Variables[a.AI], nil
}

// withoutCredentials is the start of a command that runs the AI tool with
// hide removed from its environment and BASH_ENV pointing at bash_env, or ""
// when there is nothing to remove. What follows it must be the program to exec,
// never a shell (see above).
func (m *Manager) withoutCredentials(hide []string) string {
	if len(hide) == 0 {
		return ""
	}
	s := "env"
	for _, name := range hide {
		s += " -u " + name
	}
	return s + " BASH_ENV=" + shellQuote(m.BashEnvPath()) + " "
}

// bashEnvSecrets is the part of bash_env that gives a command's shell back
// the secrets its AI tool was started without.
func (m *Manager) bashEnvSecrets() string {
	return "# The secrets, for the shells the AI tool starts to run a command: it was\n" +
		"# started without the ones that would replace its account (toolcreds).\n" +
		"if [ -n \"${BASH_EXECUTION_STRING-}\" ] && [ -r " + shellQuote(m.SecretsPath()) + " ]; then . " + shellQuote(m.SecretsPath()) + "; fi\n"
}

// ToolCredential is what an agent's AI tool really runs on, read from the
// environment of its running process rather than from settings.
type ToolCredential struct {
	Tool    string
	Running bool
	// Variable is the environment variable it authenticates with, or "" when
	// it has none and uses its own stored sign-in.
	Variable string
	// Account is the AgentBox account it signs in with: for Claude Code the
	// one whose token is in its environment, whether or not Variable is that
	// token; for Codex and OpenCode the one whose login file it has
	// (OpenCode's as "provider: account, …"), "" when the file matches no
	// stored account (the tool refreshed it, or it signed in by itself).
	Account string
	// Others are its other credential, provider and endpoint variables.
	Others []string
}

// toolProcess is one process of the agent's user, as credentialScript reports it.
type toolProcess struct {
	pid, ppid int
	argv      []string
	env       map[string]string // a watched variable's name → the SHA-256 of its value
}

// credentialScript lists the agent user's processes, with the credential
// variables in the environment of those that may be an AI tool. Values never
// leave the machine: only their SHA-256 does, to tell which account a token
// is; and of a command line, only the program and its first argument, which
// is all isTool reads, and only for a process that may be a tool.
func credentialScript(uid int, names []string, authFile string) string {
	return `uid=` + strconv.Itoa(uid) + `
names=' ` + strings.Join(names, " ") + ` '
for d in /proc/[0-9]*; do
  [ "$(stat -c %u "$d" 2>/dev/null)" = "$uid" ] || continue
  cmd=$(tr '\000\n' '\001 ' <"$d/cmdline" 2>/dev/null | cut -d "$(printf '\001')" -f1,2) || continue
  ppid=$(awk '/^PPid:/ {print $2}' "$d/status" 2>/dev/null)
  case "$cmd" in *claude*|*codex*|*opencode*) ;; *) printf 'P %s %s -\n' "${d#/proc/}" "$ppid"; continue ;; esac
  printf 'P %s %s %s\n' "${d#/proc/}" "$ppid" "$cmd"
  while IFS= read -r -d '' kv; do
    n=${kv%%=*}
    case "$names" in *" $n "*) printf 'E %s %s\n' "$n" "$(printf %s "${kv#*=}" | sha256sum | cut -c1-64)" ;; esac
  done <"$d/environ" 2>/dev/null
done
f=` + shellQuote(authFile) + `
if [ -n "$f" ] && [ -r "$f" ]; then printf 'F %s\n' "$(sha256sum <"$f" | cut -c1-64)"; fi
`
}

// authFileSum is the SHA-256 of the tool's login file credentialScript
// reported, "" when it has none.
func authFileSum(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if sum, ok := strings.CutPrefix(line, "F "); ok {
			return strings.TrimSpace(sum)
		}
	}
	return ""
}

func parseToolProcesses(out string) []toolProcess {
	var procs []toolProcess
	for _, line := range strings.Split(out, "\n") {
		kind, rest, _ := strings.Cut(line, " ")
		switch kind {
		case "P":
			f := strings.SplitN(rest, " ", 3)
			if len(f) < 3 {
				continue
			}
			pid, err1 := strconv.Atoi(f[0])
			ppid, err2 := strconv.Atoi(f[1])
			if err1 != nil || err2 != nil {
				continue
			}
			procs = append(procs, toolProcess{pid: pid, ppid: ppid, argv: strings.Split(strings.TrimSuffix(f[2], "\x01"), "\x01"), env: map[string]string{}})
		case "E":
			if name, sum, ok := strings.Cut(rest, " "); ok && len(procs) > 0 {
				procs[len(procs)-1].env[name] = sum
			}
		}
	}
	return procs
}

// isTool reports whether a process is the AI tool itself: its binary, or the
// script node runs for it. The ACP adapters around Claude Code and Codex
// aren't: the tool is their child.
func isTool(tool string, argv []string) bool {
	names := map[string][]string{"claude": {"claude", "cli.js"}, "codex": {"codex", "codex-acp"}, "opencode": {"opencode"}}[tool]
	for i, arg := range argv {
		if i > 1 || (i == 1 && path.Base(argv[0]) != "node") {
			break
		}
		base := path.Base(arg)
		if slices.Contains(names, base) && (base != "cli.js" || strings.Contains(arg, "claude")) {
			return true
		}
	}
	return false
}

// ToolCredential reads what an agent's AI tool runs on now from its running
// process, the topmost one when the tool started others of itself (a
// `claude` the agent runs in its shell is its code, not its AI tool).
func (m *Manager) ToolCredential(ctx context.Context, a state.Agent) (ToolCredential, error) {
	cred := ToolCredential{Tool: a.AI}
	watched := toolcreds.Variables[a.AI]
	if len(watched) == 0 {
		return cred, nil
	}
	if inst, err := m.Incus.Instance(ctx, a.Instance); err != nil || inst.Status != "Running" {
		return cred, err
	}
	if a.AI == "claude" {
		watched = append(slices.Clone(watched), "CLAUDE_CODE_OAUTH_TOKEN")
	}
	authFile := ""
	if a.AI == "codex" || a.AI == "opencode" {
		authFile = "/home/" + m.User.Name + "/" + toolAuthFile(a.AI)
	}
	out, err := m.Incus.Exec(ctx, a.Instance, "bash", "-c", credentialScript(m.User.UID, watched, authFile))
	if err != nil {
		return cred, fmt.Errorf("reading %s's processes: %w", a.Ref(), err)
	}
	procs := parseToolProcesses(out)
	byPID := map[int]toolProcess{}
	for _, p := range procs {
		byPID[p.pid] = p
	}
	var top *toolProcess
	for i, p := range procs {
		if !isTool(a.AI, p.argv) {
			continue
		}
		nested := false
		for seen, parent := 0, byPID[p.ppid]; parent.pid != 0 && seen < 64; seen, parent = seen+1, byPID[parent.ppid] {
			if isTool(a.AI, parent.argv) {
				nested = true
				break
			}
		}
		if !nested && (top == nil || p.pid < top.pid) {
			top = &procs[i]
		}
	}
	if top == nil {
		return cred, nil
	}
	cred.Running = true
	cred.Variable, cred.Others = credentialOf(a.AI, top.env)
	if sum, ok := top.env["CLAUDE_CODE_OAUTH_TOKEN"]; ok {
		cred.Account = m.claudeAccountOf(sum)
	}
	if sum := authFileSum(out); sum != "" {
		cred.Account = m.toolAccountOf(a, sum)
	}
	return cred, nil
}

// credentialOf picks the variable a tool authenticates with from those in its
// environment, in the order the tool itself prefers them, and lists the rest.
func credentialOf(tool string, env map[string]string) (string, []string) {
	order := map[string][]string{
		// Claude Code's documented precedence: a cloud provider, then
		// ANTHROPIC_AUTH_TOKEN, ANTHROPIC_API_KEY, and the OAuth token.
		"claude": {"AWS_BEARER_TOKEN_BEDROCK", "ANTHROPIC_AWS_API_KEY", "ANTHROPIC_FOUNDRY_API_KEY", "ANTHROPIC_FOUNDRY_AUTH_TOKEN",
			"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"},
		"codex": {"CODEX_API_KEY", "OPENAI_API_KEY"},
	}[tool]
	var variable string
	for _, name := range order {
		if _, ok := env[name]; ok {
			variable = name
			break
		}
	}
	if variable == "" && tool == "opencode" {
		for _, name := range toolcreds.Variables[tool] {
			if _, ok := env[name]; ok && toolcreds.Keys[name] {
				variable = name
				break
			}
		}
	}
	var others []string
	for name := range env {
		if name != variable && name != "CLAUDE_CODE_OAUTH_TOKEN" {
			others = append(others, name)
		}
	}
	slices.Sort(others)
	return variable, others
}

// claudeAccountOf names the stored Claude account whose token hashes to sum.
func (m *Manager) claudeAccountOf(sum string) string {
	accounts, err := m.Creds.ClaudeAccounts()
	if err != nil {
		return ""
	}
	for _, acc := range accounts {
		token, err := m.Creds.ClaudeToken(acc.Name)
		if err != nil || token == "" {
			continue
		}
		if sha256Hex(token) == sum {
			return acc.Name
		}
	}
	return ""
}

// toolAccountOf names the stored Codex account, or the OpenCode accounts,
// whose login file hashes to sum: the agent's own first, then any other.
func (m *Manager) toolAccountOf(a state.Agent, sum string) string {
	if a.AI == "opencode" {
		for _, chosen := range []state.AccountMap{a.OpenCodeAccounts, nil} {
			auth, used, err := m.Creds.OpenCodeAuth(chosen)
			if err == nil && auth != nil && sha256Hex(string(auth)) == sum {
				return formatAccounts(used)
			}
		}
		return ""
	}
	accounts, err := m.Creds.ToolAccounts(a.AI)
	if err != nil {
		return ""
	}
	for _, acc := range accounts {
		auth, _, err := m.Creds.ToolAuth(a.AI, "", acc.Name)
		if err == nil && auth != nil && sha256Hex(string(auth)) == sum {
			return acc.Name
		}
	}
	return ""
}

// formatAccounts says which account each OpenCode provider is on.
func formatAccounts(m state.AccountMap) string {
	var parts []string
	for _, p := range slices.Sorted(maps.Keys(m)) {
		parts = append(parts, p+": "+m[p])
	}
	return strings.Join(parts, ", ")
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
