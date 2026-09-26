package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"agentbox/internal/api"
)

// Settings that belong to the installation rather than to one project. Unlike
// a project's Claude Code account or media retention, the model new agents
// start on says what you want your agents to cost and be capable of, which
// doesn't change from one repository to the next — and the control for it
// lives on the overview, a page that spans every project.
const (
	// SettingDefaultClaudeModel is the "model" chat option new Claude Code
	// agents are seeded with. Empty means DefaultClaudeModel. It is the
	// agents' half of Settings: the lead's chat has its own,
	// SettingDefaultLeadModel. The key keeps its old name because it has
	// always been the agents' — the lead never read it — so splitting the two
	// moved nothing.
	SettingDefaultClaudeModel = "default_claude_model"
	// SettingDefaultAgentContextWindow is the context window (D91) new Claude
	// Code agents start with: "" for the installation's compact window, which
	// is what every agent had before this setting, or ClaudeFullWindow as a
	// count of tokens for the model's whole window. Like the model it is
	// applied once, at creation.
	SettingDefaultAgentContextWindow = "default_agent_context_window"
	// SettingDefaultLeadModel is the model a project's lead chats on when its
	// composer hasn't chosen one. Empty means Claude Code's own default, which
	// is what every lead ran on before this setting existed. Unlike the
	// agents' default it isn't copied into the chat: a lead is made once and
	// lives as long as its project, so a change here reaches it the next time
	// its adapter starts.
	SettingDefaultLeadModel = "default_lead_model"
	// SettingDefaultLeadContextWindow is SettingDefaultAgentContextWindow for
	// the lead's chat, read the way SettingDefaultLeadModel is.
	SettingDefaultLeadContextWindow = "default_lead_context_window"
	// SettingClaudeModelChoices is the model menu the Claude Code adapter last
	// advertised, as JSON. AgentBox never invents a model list here — it
	// arrives over ACP, per account — so this remembers the real one between
	// sessions and gives the overview something true to offer. A reader of
	// this setting sees only what the adapter sent; MergePinnedClaudeModels
	// adds AgentBox's small pinned list (D69) on top, at every place the menu
	// is shown.
	SettingClaudeModelChoices = "claude_model_choices"
	// SettingDefaultClaudeEffort is the "effort" chat option new Claude Code
	// agents are seeded with. Empty means DefaultClaudeEffort.
	SettingDefaultClaudeEffort = "default_claude_effort"
	// SettingClaudeEffortChoices is the effort menu the Claude Code adapter
	// last advertised, as JSON, remembered for the same reason as the model
	// menu and with one caveat the model list doesn't have: the adapter sends
	// the levels "available for this model", and a model can have none at all
	// (Haiku 4.5 sends no effort option). So this is the levels Claude Code
	// has been seen to name, not a promise about one model.
	SettingClaudeEffortChoices = "claude_effort_choices"
	// SettingDefaultCPU is how many cores a new agent's machine gets, as a
	// count like "4". Unlike the settings above, "" is not "AgentBox's own
	// default" but a real choice — every core, no limit — so this key is read
	// with SettingValue, which says whether it was ever set at all. An
	// installation that has never chosen is seeded with 2 cores, or fewer on a
	// one-core host, when the daemon starts (see agent.DefaultLimits).
	SettingDefaultCPU = "default_cpu"
	// SettingDefaultCPUAllowance is the share of the CPUs a new agent gets
	// (Incus limits.cpu.allowance): "50%", or "25ms/100ms". "" is all of it.
	SettingDefaultCPUAllowance = "default_cpu_allowance"
	// SettingDefaultMemory is the memory ceiling on a new agent's machine,
	// like "8GiB". "" is no ceiling, and read with SettingValue like the CPU
	// default. An installation that has never chosen is seeded with 8GiB, or
	// half the host's memory when that is less (see agent.DefaultMemory).
	SettingDefaultMemory = "default_memory"
	// SettingDefaultMemorySeeded records that SettingDefaultMemory was seeded
	// with a ceiling. Before it was, every installation was seeded with "",
	// and a stored "" can't say whether the user chose it or the daemon wrote
	// it; this key is how the daemon gives those installations the ceiling
	// once, and never again.
	SettingDefaultMemorySeeded = "default_memory_seeded"
	// SettingOpenCodeModelChoices is the model menu OpenCode last advertised,
	// as JSON, remembered for the same reason as the Claude Code menus: which
	// models OpenCode can run depends on which providers the login has keys
	// for, so the only honest list is one OpenCode itself named. It arrives
	// either from an OpenCode chat's ACP session or from `opencode models`
	// (internal/opencode), and its values are OpenCode's "provider/model" ids.
	SettingOpenCodeModelChoices = "opencode_model_choices"
	// SettingImageAndroid, SettingImageCodex, SettingImageOpenCode and
	// SettingImageDevCaches are the
	// optional components the base image is built with, stored as flags. They
	// belong to the installation for the same reason the model does: there is
	// one base image, and every project's agents are copied from it. All are
	// off until someone turns them on, so a first build is the small one.
	SettingImageAndroid   = "image_android"
	SettingImageCodex     = "image_codex"
	SettingImageOpenCode  = "image_opencode"
	SettingImageDevCaches = "image_dev_caches"
	// SettingAppearance is what AgentBox wears: api.AppearanceFollow (the
	// desktop theme this machine is running, in its own window and on every
	// agent's desktop), or api.AppearanceLight or api.AppearanceDark for its
	// own colours one way round or the other. Unlike the image flags above it
	// has a default that isn't the zero value, which is why it is read with
	// Appearance rather than Setting: a machine running Omarchy should match
	// the desktop around it without anyone having found a switch, and a
	// machine running anything else sees no difference, because there is then
	// no theme to follow.
	//
	// The key is still follow_host_theme, and so are its two old values: this
	// used to be a yes/no flag, and "0" — the switch turned off — is exactly
	// what api.AppearanceDark means. An installation that turned it off before
	// this setting grew keeps the look it chose, with nothing to migrate.
	SettingAppearance = "follow_host_theme"
	// SettingResumeAfterLimit says whether a chat whose turn was cut short by
	// a Claude usage limit carries on by itself once the limit resets. It
	// belongs to the installation because the limit does: it is the account's,
	// shared by every agent on it, so waiting for it is not a per-project
	// choice. On until somebody turns it off, so it is read with FlagOn rather
	// than Flag.
	SettingResumeAfterLimit = "resume_after_limit"
	// SettingClaudeCompactWindow is how full a chat's context may get before
	// its AI tool compacts it (D83), as a count of tokens. Empty means
	// DefaultClaudeCompactWindow and "0" means the model's whole window. Named
	// for Claude Code, which it governed first, it now drives Codex's
	// model_auto_compact_token_limit the same way; OpenCode has no absolute
	// compact window to set at all, only knobs relative to the model's own
	// context, so this doesn't reach it. It belongs to the installation
	// because what it saves is the account's usage limit, which every project
	// shares; unlike the model and the effort it applies to agents that
	// already exist, from their next chat.
	SettingClaudeCompactWindow = "claude_compact_window"
	// SettingUpdateCheck says whether the daemon asks once a day whether a
	// newer AgentBox is out. On until somebody turns it off (FlagOn).
	SettingUpdateCheck = "update_check"
	// SettingUsageStats says whether the update check also sends the
	// feature_usage counts. On until somebody turns it off (FlagOn), and
	// never sent while SettingUpdateCheck is off.
	SettingUsageStats = "usage_stats"
	// SettingMediaRetention is how long a removed agent's media is kept: one
	// of the api.MediaRetention values, empty meaning
	// DefaultMediaRetention. It belongs to the installation rather than to a
	// project, and it replaced projects.media_retention_days, which is no
	// longer read.
	SettingMediaRetention = "media_retention"
	// SettingInstallID is the random UUID the update check sends, so the
	// server can count installations without anything that identifies the
	// machine. Made on the first check, so an installation that never checks
	// never has one.
	SettingInstallID = "install_id"
)

// DefaultMediaRetention is how long a removed agent's media is kept when
// nobody chose: long enough to look at what it did the day after, short
// enough that a busy installation doesn't pile up recordings.
const DefaultMediaRetention = api.MediaRetentionDay

// MediaRetention reads SettingMediaRetention. Anything that isn't one of the
// choices — never written, or written by a build with other ones — is the
// default.
func (s *Store) MediaRetention(ctx context.Context) (string, error) {
	value, err := s.Setting(ctx, SettingMediaRetention)
	if err != nil {
		return DefaultMediaRetention, err
	}
	if _, _, ok := MediaRetentionPeriod(value); !ok {
		return DefaultMediaRetention, nil
	}
	return value, nil
}

// MediaRetentionPeriod is how long a retention choice keeps media after its
// agent is gone, and whether it keeps it forever. ok is false for anything
// that isn't a choice. Immediately is a zero period: the media goes with the
// agent.
func MediaRetentionPeriod(value string) (period time.Duration, forever, ok bool) {
	const day = 24 * time.Hour
	switch value {
	case api.MediaRetentionImmediately:
		return 0, false, true
	case api.MediaRetentionDay:
		return day, false, true
	case api.MediaRetentionWeek:
		return 7 * day, false, true
	case api.MediaRetentionMonth:
		return 30 * day, false, true
	case api.MediaRetentionForever:
		return 0, true, true
	}
	return 0, false, false
}

// DefaultClaudeCompactWindow is the context a Claude Code or Codex chat
// compacts at when nobody chose (D83): the window the models had before their
// 1M-context variants. Every model call sends the whole context again, so a
// session that is left to fill a 1M window costs up to five times as much per
// call as one held here — for an agent working alone for hours, that is most
// of what it spends.
const DefaultClaudeCompactWindow int64 = 200_000

// MinClaudeCompactWindow and MaxClaudeCompactWindow are the bounds Claude Code
// itself holds CLAUDE_CODE_AUTO_COMPACT_WINDOW to. A setting outside them
// would be clamped or ignored behind AgentBox's back, so it is refused here —
// the same bounds are asked of Codex's model_auto_compact_token_limit, which
// has no bounds of its own to violate.
const (
	MinClaudeCompactWindow int64 = 100_000
	MaxClaudeCompactWindow int64 = 1_000_000
)

// ClaudeCompactWindow is the compact window Claude Code and Codex chats run
// with: the setting, or the default when nobody chose. 0 is the model's whole
// window. A value that doesn't parse — written by hand, or by a build that
// stored it differently — is the default rather than an error: a chat must
// still start.
func (s *Store) ClaudeCompactWindow(ctx context.Context) (int64, error) {
	raw, err := s.Setting(ctx, SettingClaudeCompactWindow)
	if err != nil {
		return 0, err
	}
	if raw == "" {
		return DefaultClaudeCompactWindow, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return DefaultClaudeCompactWindow, nil
	}
	return n, nil
}

// ValidClaudeCompactWindow says why a compact window can't be used, or nil.
func ValidClaudeCompactWindow(n int64) error {
	if n == 0 || (n >= MinClaudeCompactWindow && n <= MaxClaudeCompactWindow) {
		return nil
	}
	return fmt.Errorf("a compact window is 0, for the model's whole window, or between %d and %d tokens; %d isn't", MinClaudeCompactWindow, MaxClaudeCompactWindow, n)
}

// DefaultClaudeModel is the model a new Claude Code agent starts on when you
// haven't chosen one: Opus. It was "opus[1m]" until the context window became
// a choice of its own (D91); a stored "opus[1m]" still works, and reads as
// "opus" (ClaudeWindows.NormalizeClaudeModel).
const DefaultClaudeModel = "opus"

// DefaultContextWindow checks a context window chosen as a default in
// Settings, for the chats of a role whose default model is model, and gives
// the value to store: "" for the installation's compact window — "200k", as
// people write it — or ClaudeFullWindow for the model's whole window, which a
// model without one, like Haiku, can't have. Anything else isn't a default a
// role can hold: the windows between are the compact window's own setting.
func (w ClaudeWindows) DefaultContextWindow(model, value string, installation int64) (string, error) {
	n, err := ParseContextWindow(value)
	if err != nil {
		return "", err
	}
	windows := w.ContextWindows(model, installation)
	switch n {
	case 0, windows[0], ClaudeShortWindow:
		return "", nil
	case ClaudeFullWindow:
		if windows[len(windows)-1] < ClaudeFullWindow {
			return "", fmt.Errorf("%s has no 1M context window: it only has %s", modelName(model), FormatContextWindow(windows[len(windows)-1]))
		}
		return strconv.FormatInt(ClaudeFullWindow, 10), nil
	}
	return "", fmt.Errorf("a default context window is 200k or 1m; %s isn't", FormatContextWindow(n))
}

// DefaultClaudeEffort is how hard a new Claude Code agent thinks when you
// haven't chosen: high, one step below the most (D83). Thinking is billed as
// output, on every call of every agent, and "xhigh" everywhere was the most
// expensive setting AgentBox could have picked for someone who never looked;
// a task that needs more is one agent's choice to make. Unlike the model, the
// adapter resolves no aliases here — an effort it doesn't advertise is refused
// outright — so this is a value the tool really offers, not a preference it
// works out.
const DefaultClaudeEffort = "high"

// ChoiceValues reads the values out of a remembered menu, as stored under
// SettingClaudeModelChoices, SettingClaudeEffortChoices or
// SettingOpenCodeModelChoices. Only the values are decoded, so this stays out of the way of whatever else a choice carries. A
// menu written by another build that can't be read is treated as no menu at
// all: there is then nothing true to check against, which is the same position
// as an installation whose chat has never started.
func ChoiceValues(raw string) []string {
	if raw == "" {
		return nil
	}
	var choices []struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal([]byte(raw), &choices); err != nil {
		return nil
	}
	values := make([]string, 0, len(choices))
	for _, c := range choices {
		if c.Value != "" {
			values = append(values, c.Value)
		}
	}
	return values
}

// pinnedClaudeModel is a model AgentBox knows about and offers on every
// Claude model menu, whether or not a Claude Code adapter has ever
// advertised it for this account. Unlike the remembered menu — otherwise the
// only list AgentBox composes (D45) — this one is fixed and small: adding an
// entry here is a commitment to keep offering it, not a guess at what an
// account can run.
//
// Fable is the model this exists for: the account gates it, so it can never
// earn a place on the remembered menu the way an ordinary model does before
// someone has already typed it once (see ModelByName.tsx). opus[1m] was
// pinned too, until the window became a choice of its own (D91).
type pinnedClaudeModel struct{ Value, Name string }

var pinnedClaudeModels = []pinnedClaudeModel{
	{Value: "claude-fable-5-1", Name: "Fable 5.1"},
}

// pinnedClaudeModelNote marks a pinned choice in the UI: pinning one here is
// not a promise it runs, and the account still decides that.
const pinnedClaudeModelNote = "may not be enabled for this account"

// pinnedClaudeChoice is the api.ChatOptionChoice AgentBox itself contributes
// for a pinned model, or ok=false when value isn't one.
func pinnedClaudeChoice(value string) (choice api.ChatOptionChoice, ok bool) {
	i := slices.IndexFunc(pinnedClaudeModels, func(p pinnedClaudeModel) bool { return p.Value == value })
	if i < 0 {
		return api.ChatOptionChoice{}, false
	}
	p := pinnedClaudeModels[i]
	return api.ChatOptionChoice{Value: p.Value, Name: p.Name, Description: pinnedClaudeModelNote}, true
}

// IsPinnedClaudeChoice reports whether choice is exactly the entry
// MergePinnedClaudeModels would add for its value — AgentBox's own, not one a
// Claude Code adapter sent. rememberChoices (internal/chat) uses this to keep
// the remembered menu the adapter's alone: a pinned entry earns a real place
// on it only once the adapter actually advertises it, same as any model
// named outside the menu (D46).
func IsPinnedClaudeChoice(choice api.ChatOptionChoice) bool {
	pinned, ok := pinnedClaudeChoice(choice.Value)
	return ok && pinned == choice
}

// MergePinnedClaudeModels adds AgentBox's pinned models to a menu a Claude
// Code adapter really advertised, skipping any value already on it — so an
// account that does offer Fable, or its own 1M Opus, shows the adapter's own
// entry rather than AgentBox's placeholder for it.
//
// It also folds a "[1m]" variant into its model when the menu has both, like
// "opus[1m]" beside "opus": the window is chosen on its own (D91), and two
// entries for one model would be the duplicate that replaced.
func MergePinnedClaudeModels(choices []api.ChatOptionChoice) []api.ChatOptionChoice {
	out := slices.DeleteFunc(slices.Clone(choices), func(c api.ChatOptionChoice) bool {
		base, oneM := SplitClaudeModel(c.Value)
		return oneM && slices.ContainsFunc(choices, func(o api.ChatOptionChoice) bool { return o.Value == base })
	})
	for _, p := range pinnedClaudeModels {
		if slices.ContainsFunc(choices, func(c api.ChatOptionChoice) bool { return c.Value == p.Value }) {
			continue
		}
		choice, _ := pinnedClaudeChoice(p.Value)
		out = append(out, choice)
	}
	return out
}

// MergePinnedClaudeModelValues is MergePinnedClaudeModels for a bare list of
// values, like ChoiceValues returns — used where there's no choice metadata
// to carry, such as the lead's brief (internal/agent/lead.go).
func MergePinnedClaudeModelValues(values []string) []string {
	out := slices.Clone(values)
	for _, p := range pinnedClaudeModels {
		if !slices.Contains(values, p.Value) {
			out = append(out, p.Value)
		}
	}
	return out
}

// Setting returns a setting's value, or "" when it was never set.
func (s *Store) Setting(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}

// SettingValue returns a setting's value and whether it is stored at all.
// Most settings use "" for "nobody chose"; the resource limits can't, because
// "" is how you say "no limit", so they need the difference.
func (s *Store) SettingValue(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return value, err == nil, err
}

// SetSetting stores a setting's value, replacing whatever was there.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// Appearance reads SettingAppearance: api.AppearanceFollow, AppearanceLight or
// AppearanceDark. Anything else — never written, or the "1" this setting used
// to be stored as — is following, which is the default. See the constant for
// why "0" reads as dark.
func (s *Store) Appearance(ctx context.Context) (string, error) {
	value, err := s.Setting(ctx, SettingAppearance)
	switch value {
	case api.AppearanceLight, api.AppearanceDark:
		return value, err
	case "0":
		return api.AppearanceDark, err
	default:
		return api.AppearanceFollow, err
	}
}

// Flag reads a setting stored as a yes/no choice. Anything but "1" is off,
// so a setting never written yet is off — which is what the image options
// want: a build nobody has configured is the small one.
func (s *Store) Flag(ctx context.Context, key string) (bool, error) {
	value, err := s.Setting(ctx, key)
	return value == "1", err
}

// FlagOn reads a yes/no setting that is on unless it was turned off: "0" is
// off, and everything else — including a setting nobody has ever written — is
// on. It is the opposite default to Flag, for settings whose useful state is
// the one you get without choosing: an image nobody has configured should be
// the small one, but a chat nobody has configured should still pick itself up
// after a usage limit, and a desktop nobody has configured should still match
// the one around it.
func (s *Store) FlagOn(ctx context.Context, key string) (bool, error) {
	value, err := s.Setting(ctx, key)
	return value != "0", err
}

// SetFlag stores a yes/no setting.
func (s *Store) SetFlag(ctx context.Context, key string, on bool) error {
	value := "0"
	if on {
		value = "1"
	}
	return s.SetSetting(ctx, key, value)
}
