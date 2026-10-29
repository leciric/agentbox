package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"agentbox/internal/credentials"
	"agentbox/internal/image"
	"agentbox/internal/opencode"
	"agentbox/internal/state"
)

// Logins are the stored accounts an agent's AI tool signs in with: one per
// tool, OpenCode's one per provider. Only the agent's own tool's is set on a
// resolved one.
type Logins struct {
	Claude, Codex, Cursor string
	OpenCode              state.AccountMap
}

// The files each tool reads its login from, under the agent's (or the lead's)
// home.
const (
	codexAuthFile    = ".codex/auth.json"
	openCodeAuthFile = ".local/share/opencode/auth.json"
)

// CheckLogin reports that AgentBox can log the new agent in to its AI tool,
// and returns the account it will use: want's, else the project's, else the
// machine's default. An account named for a tool the agent doesn't run is
// refused, as is one that doesn't exist. Exported so a caller that starts a
// job for Create can run this check first, synchronously, rather than let the
// job fail after it has already answered that the agent is on its way (see
// createAgentFrom).
func (m *Manager) CheckLogin(ai string, p state.Project, want Logins) (Logins, error) {
	for tool, named := range map[string]bool{"claude": want.Claude != "", "codex": want.Codex != "", "cursor": want.Cursor != "", "opencode": len(want.OpenCode.Clean()) > 0} {
		if named && tool != ai {
			return Logins{}, fmt.Errorf("a %s account only applies to agents that run %s, and this one runs %s", ChatAdapters[tool].Tool, ChatAdapters[tool].Tool, ai)
		}
	}
	switch ai {
	case "claude":
		account, err := m.ClaudeAccountFor(p, want.Claude)
		return Logins{Claude: account}, err
	case "codex", "cursor":
		named := map[string]string{"codex": want.Codex, "cursor": want.Cursor}[ai]
		account, err := m.toolAccountFor(ai, cmpOr(named, projectToolAccount(p, ai)))
		if err != nil {
			return Logins{}, err
		}
		if ai == "codex" {
			return Logins{Codex: account}, nil
		}
		return Logins{Cursor: account}, nil
	case "opencode":
		used, err := m.openCodeAccountsFor(p, want.OpenCode)
		return Logins{OpenCode: used}, err
	}
	return Logins{}, nil
}

// projectToolAccount is the account a project names for a tool, "" for the
// machine's default.
func projectToolAccount(p state.Project, tool string) string {
	switch tool {
	case "codex":
		return p.CodexAccount
	case "cursor":
		return p.CursorAccount
	}
	return ""
}

// toolAccountFor resolves a Codex or Cursor account: the one named, which
// must exist (and for Cursor, hold a key that hasn't expired), else the
// machine's default.
func (m *Manager) toolAccountFor(tool, name string) (string, error) {
	b, resolved, err := m.Creds.ToolLogin(tool, name)
	if err != nil {
		return "", err
	}
	if b != nil {
		return resolved, nil
	}
	if name != "" {
		if ok, _ := m.Creds.HasToolAccount(tool, "", name); ok {
			return "", fmt.Errorf("the Cursor account %q has expired: sign in to it again under Settings → Accounts, or run agentbox auth cursor --account %s", name, name)
		}
		return "", fmt.Errorf("no %s account named %q: %s", credentials.ToolName(tool), name, m.toolAccountsHint(tool))
	}
	if tool == "codex" {
		return "", errors.New("agents have no Codex login: run agentbox auth codex (or use --ai none)")
	}
	return "", errors.New("agents aren't signed in to Cursor: run agentbox auth cursor, or sign in under Settings → Accounts (or use --ai claude)")
}

// openCodeAccountsFor resolves OpenCode's account for each provider: want's,
// else the project's, else the provider's default.
func (m *Manager) openCodeAccountsFor(p state.Project, want state.AccountMap) (state.AccountMap, error) {
	chosen := p.OpenCodeAccounts.Clean()
	maps.Copy(chosen, want.Clean())
	auth, used, err := m.Creds.OpenCodeAuth(chosen)
	if err != nil {
		return nil, err
	}
	if auth == nil {
		return nil, errors.New("agents have no OpenCode login: run agentbox auth opencode (or use --ai claude)")
	}
	return used, nil
}

// toolAccountsHint says what to do about an unknown account name.
func (m *Manager) toolAccountsHint(tool string) string {
	accounts, err := m.Creds.ToolAccounts(tool)
	if err != nil || len(accounts) == 0 {
		return fmt.Sprintf("add it with agentbox auth %s --account <name>", tool)
	}
	names := make([]string, len(accounts))
	for i, a := range accounts {
		names[i] = a.Name
	}
	return "this machine has " + strings.Join(names, ", ")
}

// toolLoginFiles are the other tools' login files an agent's home gets,
// relative to it: its own tool's from the accounts it was created with, and
// the others' from its project's (the machine's defaults when that fails),
// so a command the agent runs finds them as the old single logins were found.
func (m *Manager) toolLoginFiles(ctx context.Context, a state.Agent) (map[string][]byte, error) {
	var p state.Project
	if !a.IsHome() {
		var err error
		if p, err = m.Store.Project(ctx, a.Project); err != nil && !errors.Is(err, state.ErrNotFound) {
			return nil, err
		}
	}
	files := map[string][]byte{}
	for _, tool := range credentials.AccountTools {
		auth, err := m.toolLoginFile(p, a, tool)
		if err != nil {
			if tool == a.AI {
				return nil, err
			}
			auth = nil
		}
		if auth != nil {
			files[toolAuthFile(tool)] = auth
		}
	}
	return files, nil
}

func toolAuthFile(tool string) string {
	switch tool {
	case "codex":
		return codexAuthFile
	case "opencode":
		return openCodeAuthFile
	}
	return cursorAuthFile
}

// openCodeChosen is the account each OpenCode provider's login comes from:
// the agent's own accounts when it has any, else its project's.
func openCodeChosen(p state.Project, a state.Agent) state.AccountMap {
	chosen := p.OpenCodeAccounts.Clean()
	if len(a.OpenCodeAccounts) > 0 {
		chosen = a.OpenCodeAccounts.Clean()
	}
	return chosen
}

// openCodeImportMarker is where a machine keeps the accounts last imported
// into it: a hash of the export, the label each provider was imported under,
// and a hash of each provider's credential. It holds no credential, only what
// the next sync needs to take the accounts this one no longer wants back out.
const openCodeImportMarker = ".config/opencode/.agentbox-accounts.json"

// openCodeImported is the marker's content.
type openCodeImported struct {
	Hash     string            `json:"hash"`
	Accounts map[string]string `json:"accounts"`
	Values   map[string]string `json:"values,omitempty"`
}

// syncOpenCodeImport gives OpenCode 2 in one machine exactly the accounts in
// export, keeping them out of the way while it changes. `auth import` only
// adds credentials: it skips an id it already holds and never deactivates one
// it no longer has, so a renamed or removed account would stay behind and keep
// answering. The labels this same file imported last time are logged out after
// the import (which leaves the new ones active), and the marker is rewritten.
// An account kept under the same label whose credential changed (a rotated
// key, a new sign-in) has the same id, which the import would skip, so it is
// logged out first and imported afresh. One that didn't change is left alone:
// OpenCode refreshes an OAuth token in place, and the machine's copy may be
// newer than the stored one.
func syncOpenCodeImport(ctx context.Context, read func() ([]byte, error), write func([]byte) error, run func(context.Context, []string, io.Reader) error, export []byte, used map[string]string) error {
	sum := fmt.Sprintf("%x", sha256.Sum256(export))
	var was openCodeImported
	if b, err := read(); err == nil && len(b) > 0 {
		_ = json.Unmarshal(b, &was)
	}
	if was.Hash == sum {
		return nil
	}
	values := openCodeValueHashes(export)
	for provider, label := range was.Accounts {
		if used[provider] == label && was.Values[provider] != values[provider] {
			// Best effort, as below: the import that follows is what matters.
			_ = run(ctx, []string{"auth", "logout", "--standalone", provider, label}, nil)
		}
	}
	if len(export) > 0 {
		if err := run(ctx, []string{"auth", "import", "--standalone"}, bytes.NewReader(export)); err != nil {
			return err
		}
	}
	for provider, label := range was.Accounts {
		if used[provider] == label {
			continue
		}
		// Best effort: an account the user already logged out of the machine,
		// or one whose provider is gone, makes this fail, and that is not a
		// reason to fail the whole sync.
		_ = run(ctx, []string{"auth", "logout", "--standalone", provider, label}, nil)
	}
	now, err := json.Marshal(openCodeImported{Hash: sum, Accounts: used, Values: values})
	if err != nil {
		return err
	}
	return write(now)
}

// openCodeValueHashes is a hash of each provider's credential in an export, so
// that the next sync can tell which ones changed.
func openCodeValueHashes(export []byte) map[string]string {
	var creds []struct {
		IntegrationID string          `json:"integrationID"`
		Value         json.RawMessage `json:"value"`
	}
	_ = json.Unmarshal(export, &creds)
	out := map[string]string{}
	for _, c := range creds {
		out[c.IntegrationID] = fmt.Sprintf("%x", sha256.Sum256(c.Value))
	}
	return out
}

// importOpenCodeAccounts gives an OpenCode 2 agent its logins. OpenCode 2
// keeps credentials in a database of its own, not in the auth.json OpenCode 1
// read, so the accounts go in through its own `auth import` rather than as a
// file. An OpenCode 1 agent, or one with no login, has nothing to do here.
func (m *Manager) importOpenCodeAccounts(ctx context.Context, a state.Agent, version opencode.Release) error {
	if a.AI != "opencode" || version != opencode.V2 {
		return nil
	}
	var p state.Project
	if !a.IsHome() {
		var err error
		if p, err = m.Store.Project(ctx, a.Project); err != nil && !errors.Is(err, state.ErrNotFound) {
			return err
		}
	}
	export, used, err := m.Creds.OpenCodeImport(openCodeChosen(p, a))
	if err != nil {
		return err
	}
	if export == nil {
		// No accounts at all: an empty import adds nothing, and the logouts
		// below are what take the old ones out.
		export = []byte("[]")
		used = map[string]string{}
	}
	if err := m.ensureOpenCode2InAgent(ctx, a); err != nil {
		return err
	}
	home := "/home/" + m.User.Name
	marker := home + "/" + openCodeImportMarker
	run := func(ctx context.Context, args []string, stdin io.Reader) error {
		quoted := make([]string, 0, len(args))
		for _, arg := range args {
			quoted = append(quoted, shellQuote(arg))
		}
		command := strings.Join(append([]string{opencode.Command2}, quoted...), " ")
		var out bytes.Buffer
		if err := m.Incus.UserExec(ctx, a.Instance, m.User.Name, command, stdin, &out, &out); err != nil {
			return fmt.Errorf("%s: %w: %s", command, err, tail(out.String()))
		}
		return nil
	}
	read := func() ([]byte, error) { return m.readAgentFile(ctx, a, marker), nil }
	write := func(b []byte) error {
		return m.Incus.WriteFile(ctx, a.Instance, marker, b, m.User.UID, m.User.GID, 0o600)
	}
	if err := syncOpenCodeImport(ctx, read, write, run, export, used); err != nil {
		return fmt.Errorf("giving %s its OpenCode login: %w", a.Ref(), err)
	}
	return nil
}

// ensureOpenCode2InAgent installs OpenCode 2's platform package in an agent
// the first time it runs it, linking it as opencode2 where the login shell
// finds it. The base image doesn't carry it, so that choosing OpenCode 2 never
// costs an installation the published image: the price is that this first
// start needs the network. npm retries a registry it can't reach for minutes
// without a word, so it is held to one retry and its errors are kept, and a
// machine that can't reach the registry is told so rather than given npm's
// log. A machine that already has the pinned version does nothing.
func (m *Manager) ensureOpenCode2InAgent(ctx context.Context, a state.Agent) error {
	pkg, err := opencode.Package2()
	if err != nil {
		return err
	}
	// Installed at the pinned version: a bump of image.OpenCode2Version
	// reaches agents that already have an older one.
	have := fmt.Sprintf("command -v %[1]s >/dev/null && grep -Fq %[2]s ~/.local/share/opencode2/node_modules/%[3]s/package.json",
		opencode.Command2, shellQuote(`"version": "`+image.OpenCode2Version+`"`), pkg)
	if m.Incus.UserExec(ctx, a.Instance, m.User.Name, have, nil, io.Discard, io.Discard) == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, installTimeout)
	defer cancel()
	spec := shellQuote(pkg + "@" + image.OpenCode2Version)
	// Under a lock, checking again inside it: a terminal opening while a chat
	// starts must not run two installs into the same directory.
	install := fmt.Sprintf("grep -Fq %[3]s ~/.local/share/opencode2/node_modules/%[2]s/package.json 2>/dev/null && [ -e ~/.local/bin/opencode2 ] && exit 0; npm install --loglevel=error --fetch-retries=1 --no-fund --no-audit --prefix ~/.local/share/opencode2 %[1]s && ln -sfn ../share/opencode2/node_modules/%[2]s/bin/opencode ~/.local/bin/opencode2",
		spec, pkg, shellQuote(`"version": "`+image.OpenCode2Version+`"`))
	script := "mkdir -p ~/.local/bin ~/.local/share/opencode2 && flock ~/.local/share/opencode2.lock sh -c " + shellQuote(install)
	var out bytes.Buffer
	if err := m.Incus.UserExec(ctx, a.Instance, m.User.Name, script, nil, &out, &out); err != nil {
		if reason := npmNetworkError(out.String()); reason != "" || ctx.Err() != nil {
			return fmt.Errorf("%s has no OpenCode 2 yet, and installing it needs the network, which its machine can't reach (%s): connect it and start it again, or switch back to OpenCode 1", a.Ref(), cmpOr(reason, "timed out"))
		}
		return fmt.Errorf("installing OpenCode 2 in %s: %w: %s", a.Ref(), err, tail(out.String()))
	}
	return nil
}

// npmNetworkError is the error code npm gave up with when it couldn't reach
// the registry at all, "" for any other failure.
func npmNetworkError(out string) string {
	for _, line := range strings.Split(out, "\n") {
		code, ok := strings.CutPrefix(strings.TrimSpace(line), "npm error code ")
		if !ok {
			continue
		}
		switch code = strings.TrimSpace(code); code {
		case "ENOTFOUND", "EAI_AGAIN", "ECONNREFUSED", "ECONNRESET", "ETIMEDOUT", "ENETUNREACH", "EHOSTUNREACH", "ECONNABORTED":
			return code
		}
	}
	return ""
}

// toolLoginFile is one tool's login file for an agent, nil when there is
// none: the agent's own account for its tool, else the project's.
func (m *Manager) toolLoginFile(p state.Project, a state.Agent, tool string) ([]byte, error) {
	own := tool == a.AI
	switch tool {
	case "opencode":
		chosen := p.OpenCodeAccounts.Clean()
		if own {
			chosen = openCodeChosen(p, a)
		}
		auth, _, err := m.Creds.OpenCodeAuth(chosen)
		return auth, err
	default:
		name := projectToolAccount(p, tool)
		if own {
			name = cmpOr(agentToolAccount(a, tool), name)
		}
		auth, _, err := m.Creds.ToolLogin(tool, name)
		return auth, err
	}
}

// agentToolAccount is the Codex or Cursor account an agent holds.
func agentToolAccount(a state.Agent, tool string) string {
	switch tool {
	case "codex":
		return a.CodexAccount
	case "cursor":
		return a.CursorAccount
	}
	return ""
}

// LeadLogins are the accounts a lead on a tool other than Claude Code runs on:
// the one its composer chose for that tool while it still exists, else the
// project's. OpenCode's are the project's, since the composer doesn't choose
// them.
func (m *Manager) LeadLogins(ctx context.Context, a state.Agent) (Logins, error) {
	p := homeProject
	if !a.IsHome() {
		var err error
		if p, err = m.Store.Project(ctx, a.Project); err != nil {
			return Logins{}, err
		}
	}
	choice, err := m.LeadChoiceOf(ctx, a)
	if err != nil {
		return Logins{}, err
	}
	return m.leadLogins(p, a.AI, choice)
}

func (m *Manager) leadLogins(p state.Project, tool string, choice LeadChoice) (Logins, error) {
	switch tool {
	case "codex", "cursor":
		if chosen := choice.ToolAccounts[tool]; chosen != "" {
			want := Logins{Cursor: chosen}
			if tool == "codex" {
				want = Logins{Codex: chosen}
			}
			logins, err := m.CheckLogin(tool, p, want)
			if err == nil {
				return logins, nil
			}
			// Removed since: the project's own account rather than no chat.
			m.logf("The %s chat's chosen %s account: %v; using the project's", p.Name, ChatAdapters[tool].Tool, err)
		}
	}
	return m.CheckLogin(tool, p, Logins{})
}

// writeLeadLogin puts the login a lead's tool reads into its HOME, for the
// accounts it runs on, and records them on its row so the app can say which.
func (m *Manager) writeLeadLogin(ctx context.Context, a state.Agent, home string, version opencode.Release, command, nodeBin string) error {
	if !slices.Contains(credentials.AccountTools, a.AI) {
		return nil
	}
	logins, err := m.LeadLogins(ctx, a)
	if err != nil {
		return err
	}
	a.CodexAccount, a.OpenCodeAccounts, a.CursorAccount = logins.Codex, logins.OpenCode, logins.Cursor
	if a.AI == "opencode" && version == opencode.V2 {
		// OpenCode 2 keeps credentials in a database of its own, not in the
		// auth.json OpenCode 1 read: its own import is how the lead gets them.
		export, used, err := m.Creds.OpenCodeImport(a.OpenCodeAccounts.Clean())
		if err != nil {
			return err
		}
		if export == nil {
			export, used = []byte("[]"), map[string]string{}
		}
		ctx, cancel := context.WithTimeout(ctx, installTimeout)
		defer cancel()
		marker := filepath.Join(home, openCodeImportMarker)
		run := func(ctx context.Context, args []string, stdin io.Reader) error {
			cmd := exec.CommandContext(ctx, command, args...)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "PATH=" + nodeBin + string(os.PathListSeparator) + os.Getenv("PATH"), "LANG=C.UTF-8"}
			cmd.Stdin = stdin
			var out bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &out
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("opencode %s: %w: %s", strings.Join(args, " "), err, tail(out.String()))
			}
			return nil
		}
		read := func() ([]byte, error) { return os.ReadFile(marker) }
		write := func(b []byte) error { return writePrivate(home, openCodeImportMarker, b) }
		if err := syncOpenCodeImport(ctx, read, write, run, export, used); err != nil {
			return fmt.Errorf("giving the project's OpenCode chat its login: %w", err)
		}
	} else {
		auth, err := m.toolLoginFile(state.Project{}, a, a.AI)
		if err != nil {
			return err
		}
		if err := writePrivate(home, toolAuthFile(a.AI), auth); err != nil {
			return err
		}
	}
	if a.IsHome() || a.Status == "" {
		return nil
	}
	if err := m.Store.SetAgentToolAccounts(ctx, a.Project, a.Name, a.CodexAccount, a.OpenCodeAccounts, a.CursorAccount); err != nil && !errors.Is(err, state.ErrNotFound) {
		return err
	}
	return nil
}

// writePrivate writes a file under dir readable only by its owner, making the
// directories it needs.
func writePrivate(dir, rel string, b []byte) error {
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// SetAccount moves an agent to another stored account of its AI tool, keeping
// the tool: Claude Code's goes through SetClaudeAccount; Codex's and Cursor's
// one account, and OpenCode's per provider (laid over the ones it has), are
// resolved as Create resolves them (CheckLogin: "" is the project's, else the
// machine's default). Their login is a file in the machine's home, which the
// tool rewrites as it refreshes its token, and which Start leaves alone for
// that reason, so the machine must be up to be given the new one. The tool
// reads it only as it starts, so the caller restarts the chat on it
// (chat.Manager.AccountChanged).
func (m *Manager) SetAccount(ctx context.Context, a state.Agent, want Logins) (state.Agent, error) {
	if a.IsLead() {
		return a, errors.New("a project's chat chooses its account in its composer")
	}
	if a.AI == "claude" {
		if want.Codex != "" || want.Cursor != "" || len(want.OpenCode.Clean()) > 0 {
			return a, fmt.Errorf("%s runs Claude Code: name a Claude Code account", a.Ref())
		}
		return m.SetClaudeAccount(ctx, a, want.Claude)
	}
	if !slices.Contains(credentials.AccountTools, a.AI) {
		return a, fmt.Errorf("%s runs %s, which has no accounts to choose from", a.Ref(), cmpOr(ChatAdapters[a.AI].Tool, "no AI tool"))
	}
	p, err := m.Store.Project(ctx, a.Project)
	if err != nil {
		return a, err
	}
	if a.AI == "opencode" && len(want.OpenCode.Clean()) > 0 {
		merged := a.OpenCodeAccounts.Clean()
		maps.Copy(merged, want.OpenCode.Clean())
		want.OpenCode = merged
	}
	logins, err := m.CheckLogin(a.AI, p, want)
	if err != nil {
		return a, err
	}
	a.CodexAccount, a.OpenCodeAccounts, a.CursorAccount = logins.Codex, logins.OpenCode, logins.Cursor
	inst, err := m.Incus.Instance(ctx, a.Instance)
	if err != nil {
		return a, err
	}
	if inst.Status != "Running" {
		return a, fmt.Errorf("%s is %s: start it first, since %s's login is a file in its machine", a.Ref(), strings.ToLower(inst.Status), ChatAdapters[a.AI].Tool)
	}
	auth, err := m.toolLoginFile(p, a, a.AI)
	if err != nil {
		return a, err
	}
	home := "/home/" + m.User.Name
	if err := m.Incus.WriteFile(ctx, a.Instance, home+"/"+toolAuthFile(a.AI), auth, m.User.UID, m.User.GID, 0o600); err != nil {
		return a, err
	}
	if a.AI == "opencode" {
		version, err := m.openCodeVersion(ctx)
		if err != nil {
			return a, err
		}
		if err := m.importOpenCodeAccounts(ctx, a, version); err != nil {
			return a, err
		}
	}
	if err := m.Store.SetAgentToolAccounts(ctx, a.Project, a.Name, a.CodexAccount, a.OpenCodeAccounts, a.CursorAccount); err != nil {
		return a, err
	}
	return a, nil
}
