package agent

import (
	"context"
	_ "embed"
	"io"
	"strings"

	"agentbox/internal/state"
)

// Whether an agent's commits and pull requests credit the AI tool that wrote
// them is the installation's choice (state.SettingCreditAITool), off unless
// the user turned it on. Off reaches each tool in what it reads:
//
//   - Claude Code: "attribution" with an empty commit and pr in its
//     settings.json, the form the pinned version reads, and the older
//     includeCoAuthoredBy: false beside it (withClaudeAttribution).
//   - Codex: nothing to set. Its attribution is the ChatGPT workspace's
//     policy, sent with the account; the config.toml key it once read is gone.
//   - Cursor: nothing either. Its SDK always asks for attribution; only the
//     Cursor app's own settings could turn it off.
//   - OpenCode adds none of its own.
//
// And for every tool, a commit-msg hook in the agent's git (githooks/) takes
// the AI tools' co-author trailers out of each commit as it is made. A pull
// request's body has no such hook: Claude Code's setting covers its own, and
// the brief asks the others not to add one.
//
// On leaves every tool's default: the keys are taken back out and the hook is
// no longer pointed at.

// gitHooksDir is where, under the agent user's home, the hooks go, and what
// core.hooksPath in its ~/.gitconfig names while the setting is off.
const gitHooksDir = ".config/agentbox/git-hooks"

var (
	//go:embed githooks/commit-msg
	commitMsgHook string
	//go:embed githooks/chain
	chainHook string
)

// chainedHooks are the client-side hooks a repository may have of its own,
// which core.hooksPath would otherwise switch off: each runs the repository's
// own through chainHook, linked under its name. reference-transaction is left out on purpose — it
// runs on every ref update, and a script there would cost a shell and a git on
// each one for a hook hardly any repository has.
var chainedHooks = []string{
	"applypatch-msg", "pre-applypatch", "post-applypatch",
	"pre-commit", "pre-merge-commit", "prepare-commit-msg", "post-commit",
	"pre-rebase", "post-checkout", "post-merge", "pre-push", "post-rewrite",
	"pre-auto-gc", "push-to-checkout", "sendemail-validate",
}

// creditAITool reads the installation's setting. An error reads as off, which
// is the default: a store that can't answer must not start crediting.
func (m *Manager) creditAITool(ctx context.Context) bool {
	on, err := m.Store.Flag(ctx, state.SettingCreditAITool)
	if err != nil {
		m.logf("reading whether to credit the AI tool: %v", err)
		return false
	}
	return on
}

// claudeAttributionOff is what settings.json's "attribution" says while the
// setting is off: no commit trailer, no line in a pull request, and no link
// to the session either.
var claudeAttributionOff = map[string]any{"commit": "", "pr": "", "sessionUrl": false}

// withClaudeAttribution merges the setting into Claude Code's settings.json:
// both keys when credit is off, neither when it is on, so Claude Code's own
// default applies. nil when the file already says that.
func withClaudeAttribution(credit bool) func([]byte) ([]byte, error) {
	return func(existing []byte) ([]byte, error) {
		return withClaudeSettings(existing,
			func(b []byte) ([]byte, error) { return withClaudeKey(b, "attribution", claudeAttributionOff, !credit) },
			func(b []byte) ([]byte, error) { return withClaudeKey(b, "includeCoAuthoredBy", false, !credit) },
		)
	}
}

// writeGitHooks writes the hooks into the agent's machine when the setting is
// off, and says where they are for its ~/.gitconfig, or "" when it is on.
func (m *Manager) writeGitHooks(ctx context.Context, a state.Agent) (string, error) {
	if m.creditAITool(ctx) {
		return "", nil
	}
	dir := "/home/" + m.User.Name + "/" + gitHooksDir
	if err := m.Incus.WriteFile(ctx, a.Instance, dir+"/commit-msg", []byte(commitMsgHook), m.User.UID, m.User.GID, 0o755); err != nil {
		return "", err
	}
	if err := m.Incus.WriteFile(ctx, a.Instance, dir+"/chain", []byte(chainHook), m.User.UID, m.User.GID, 0o755); err != nil {
		return "", err
	}
	// The rest are links to chain, made in one exec rather than a write each.
	link := "cd " + shellQuote(dir) + " && for h in " + strings.Join(chainedHooks, " ") + "; do ln -sfn chain \"$h\"; done"
	if err := m.Incus.UserExec(ctx, a.Instance, m.User.Name, link, nil, io.Discard, io.Discard); err != nil {
		return "", err
	}
	return dir, nil
}
