package state

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"agentbox/internal/api"
)

// A Claude Code chat's context window is a choice of its own, beside the model
// and the effort, the way t3code offers one (D91). It is not a model: on
// Claude Code 2.1.280, Opus 5.5 and Sonnet 5 run with a 1M window whatever
// they are called, and "[1m]" on their names changes nothing that /context can
// see. What the choice really moves is where the chat compacts —
// autoCompactWindow in its settings.json, the knob D83 made installation-wide —
// so that is what it is here: the chat's own compact window, chosen from the
// windows its model has.

// ChatOptionContextWindow is the id, and the category, of the context window
// in a chat's options, and its key in the stored ones (state.Chat.Options).
// The value is a number of tokens, like "200000"; "" is the installation's
// compact window.
const ChatOptionContextWindow = "context_window"

// ClaudeFullWindow is the window of every Claude model with a long one.
const ClaudeFullWindow int64 = 1_000_000

// ClaudeShortWindow is the window of a model with no long one, like Haiku.
const ClaudeShortWindow int64 = 200_000

// SettingClaudeModelWindows is the window each Claude model value was seen to
// have, as a JSON object from the model's value to tokens: what usage_update
// reported for a session running on it. It is the account's own answer, and it
// overrides claudeModelWindowGuess.
const SettingClaudeModelWindows = "claude_model_windows"

const oneMSuffix = "[1m]"

// SplitClaudeModel takes the "[1m]" off a model's name: "opus[1m]" is "opus"
// and true.
func SplitClaudeModel(model string) (string, bool) {
	if base, ok := strings.CutSuffix(model, oneMSuffix); ok && base != "" {
		return base, true
	}
	return model, false
}

// claudeModelWindowGuess is the window of a model nobody has seen run yet,
// from its name. It is deliberately short: the aliases Claude Code resolves to
// a Claude 5 model, the Claude 5 ids, anything spelled with "[1m]", and Haiku.
// A name it doesn't know gets the short window, which only means the chat
// offers no second choice until a turn on it has reported the real one.
func claudeModelWindowGuess(model string) int64 {
	if _, oneM := SplitClaudeModel(model); oneM {
		return ClaudeFullWindow
	}
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "haiku"):
		return ClaudeShortWindow
	case m == "opus", m == "sonnet", m == "default", m == "best", m == "opusplan", strings.Contains(m, "fable"):
		return ClaudeFullWindow
	case strings.HasPrefix(m, "claude-opus-5"), strings.HasPrefix(m, "claude-sonnet-5"):
		return ClaudeFullWindow
	}
	return ClaudeShortWindow
}

// ClaudeWindows is what AgentBox knows about the windows of this account's
// Claude models: what their sessions reported, and which names the adapter's
// own menu offers a "[1m]" variant of.
type ClaudeWindows struct {
	Seen map[string]int64 // SettingClaudeModelWindows
	// OneM are the model values whose "[1m]" variant is on the remembered
	// menu: the only way to a long window for a model whose own is short.
	OneM map[string]bool
}

// ClaudeWindows reads what the store knows about Claude model windows.
func (s *Store) ClaudeWindows(ctx context.Context) (ClaudeWindows, error) {
	w := ClaudeWindows{Seen: map[string]int64{}, OneM: map[string]bool{}}
	raw, err := s.Setting(ctx, SettingClaudeModelWindows)
	if err != nil {
		return w, err
	}
	if raw != "" && json.Unmarshal([]byte(raw), &w.Seen) != nil {
		w.Seen = map[string]int64{}
	}
	menu, err := s.Setting(ctx, SettingClaudeModelChoices)
	if err != nil {
		return w, err
	}
	for _, v := range ChoiceValues(menu) {
		if base, ok := SplitClaudeModel(v); ok {
			w.OneM[base] = true
		}
	}
	return w, nil
}

// RememberClaudeModelWindow keeps the window a session on model reported, and
// is a no-op when it is already known.
func (s *Store) RememberClaudeModelWindow(ctx context.Context, model string, size int64) error {
	if model == "" || size <= 0 {
		return nil
	}
	w, err := s.ClaudeWindows(ctx)
	if err != nil {
		return err
	}
	if w.Seen[model] == size {
		return nil
	}
	w.Seen[model] = size
	raw, err := json.Marshal(w.Seen)
	if err != nil {
		return err
	}
	return s.SetSetting(ctx, SettingClaudeModelWindows, string(raw))
}

// Window is the whole window of a model: its own, or the one its "[1m]"
// variant reaches.
func (w ClaudeWindows) Window(model string) int64 {
	own := w.Seen[model]
	if own == 0 {
		own = claudeModelWindowGuess(model)
	}
	if own < ClaudeFullWindow && w.OneM[model] {
		return ClaudeFullWindow
	}
	return own
}

// Model is the name a chat on model should be started with, for a compact
// window of window tokens (0 for the whole). A model whose own window is short
// needs its "[1m]" variant to reach a long one; a model whose own window is
// already long is started under its own name, and so is every other.
func (w ClaudeWindows) Model(model string, window int64) string {
	if model == "" || model == "default" {
		return model
	}
	own := w.Seen[model]
	if own == 0 {
		own = claudeModelWindowGuess(model)
	}
	if own < ClaudeFullWindow && w.OneM[model] && (window == 0 || window > own) {
		return model + oneMSuffix
	}
	return model
}

// NormalizeClaudeModel is how a stored model reads now that the window is a
// choice of its own: "opus[1m]" is "opus", when "opus" already has the long
// window. It keeps the suffix only where it is still the way to a long window
// — a model whose own is short — and then the window it asks for is the whole.
//
// The window stays what it was. Before D91 "opus[1m]" compacted at the
// installation's window like everything else, so reading it as "the 1M
// choice" would move every existing agent to compacting five times later,
// which D83 exists to prevent.
func (w ClaudeWindows) NormalizeClaudeModel(model string) string {
	base, oneM := SplitClaudeModel(model)
	if !oneM {
		return model
	}
	if own := w.Seen[base]; own >= ClaudeFullWindow || (own == 0 && claudeModelWindowGuess(base) >= ClaudeFullWindow) {
		return base
	}
	return model
}

// ContextWindows are the windows a chat on model may choose, smallest first:
// the installation's compact window, and the model's whole window when that is
// longer. One choice is no choice, and the composer shows none.
func (w ClaudeWindows) ContextWindows(model string, installation int64) []int64 {
	whole := w.Window(model)
	standard := installation
	if standard <= 0 || standard > whole {
		standard = whole
	}
	if standard == whole {
		return []int64{whole}
	}
	return []int64{standard, whole}
}

// CompactWindow is what a chat's autoCompactWindow should be: the window
// chosen for it, when that is still one of its model's, else the
// installation's. 0 means leave the key out — the installation's own "the
// model's whole window".
func (w ClaudeWindows) CompactWindow(model, chosen string, installation int64) int64 {
	choices := w.ContextWindows(model, installation)
	window := choices[0]
	if n, err := ParseContextWindow(chosen); err == nil && n > 0 {
		if n >= choices[len(choices)-1] {
			window = choices[len(choices)-1]
		} else if slices.Contains(choices, n) {
			window = n
		}
	}
	if installation <= 0 && window == choices[len(choices)-1] {
		return 0
	}
	return window
}

// ParseContextWindow reads a context window the way people write one: "200k",
// "1m", "1M" or a number of tokens. "" is 0, for the installation's.
func ParseContextWindow(s string) (int64, error) {
	orig := strings.TrimSpace(s)
	s = strings.ToLower(orig)
	if s == "" {
		return 0, nil
	}
	mult := int64(1)
	switch {
	case strings.HasSuffix(s, "k"):
		s, mult = strings.TrimSuffix(s, "k"), 1_000
	case strings.HasSuffix(s, "m"):
		s, mult = strings.TrimSuffix(s, "m"), 1_000_000
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%q isn't a context window: write it like 200k or 1m", orig)
	}
	return int64(n * float64(mult)), nil
}

// FormatContextWindow names a window the way the composer does: "200k", "1M".
func FormatContextWindow(n int64) string {
	switch {
	case n >= 1_000_000 && n%1_000_000 == 0:
		return strconv.FormatInt(n/1_000_000, 10) + "M"
	case n >= 1_000_000:
		return strconv.FormatFloat(float64(n)/1_000_000, 'f', 1, 64) + "M"
	case n >= 1000:
		return strconv.FormatInt(n/1000, 10) + "k"
	}
	return strconv.FormatInt(n, 10)
}

// ContextWindowOption is the chat option for choosing among windows, with
// value the one chosen ("" for the installation's), or ok=false when there is
// only one to have.
func ContextWindowOption(windows []int64, value string) (api.ChatOption, bool) {
	if len(windows) < 2 {
		return api.ChatOption{}, false
	}
	current := windows[0]
	if n, err := ParseContextWindow(value); err == nil && n > 0 {
		if n >= windows[len(windows)-1] {
			current = windows[len(windows)-1]
		} else if slices.Contains(windows, n) {
			current = n
		}
	}
	o := api.ChatOption{
		ID: ChatOptionContextWindow, Name: "Context window",
		Description: "How much context the chat keeps before Claude Code compacts it",
		Category:    ChatOptionContextWindow, Type: "select", Value: strconv.FormatInt(current, 10),
	}
	for i, n := range windows {
		ch := api.ChatOptionChoice{Value: strconv.FormatInt(n, 10), Name: FormatContextWindow(n)}
		if i == 0 {
			ch.Description = "Compacts at " + FormatContextWindow(n) + ", the window every chat has in Settings → Agents."
		} else {
			ch.Description = "The model's whole window. Late in a long task every step resends up to " + FormatContextWindow(n) + " tokens."
		}
		o.Choices = append(o.Choices, ch)
	}
	return o, true
}

// ContextWindowChoice checks a window chosen for a model and gives the value
// to store: the chosen window in tokens, which must be one the model has.
func (w ClaudeWindows) ContextWindowChoice(model, chosen string, installation int64) (string, error) {
	n, err := ParseContextWindow(chosen)
	if err != nil {
		return "", err
	}
	if n == 0 {
		return "", fmt.Errorf("an empty context window isn't a choice: leave it out for the installation's")
	}
	windows := w.ContextWindows(model, installation)
	if !slices.Contains(windows, n) {
		names := make([]string, len(windows))
		for i, x := range windows {
			names[i] = FormatContextWindow(x)
		}
		return "", fmt.Errorf("%s has no %s context window here: it has %s", modelName(model), FormatContextWindow(n), strings.Join(names, " or "))
	}
	return strconv.FormatInt(n, 10), nil
}

func modelName(model string) string {
	if model == "" || model == "default" {
		return "the default model"
	}
	return model
}
