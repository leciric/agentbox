// Package pricing is what models cost at API list prices, for estimating
// what a turn cost when its AI tool didn't say.
//
// Of the four AI tools, Claude Code (claude-agent-acp) and OpenCode report a
// cost with every model result (usage_update's running total, read by
// internal/chat). Codex (codex-acp) and Cursor (AgentBox's own adapter over
// Cursor's SDK) report tokens only, and OpenCode reports 0 for a model it
// has no price for or reaches through a subscription. Their turns are priced
// from the table below.
//
// # Updating the table
//
// Prices are US dollars per million tokens, from each vendor's pricing page:
//
//   - Anthropic: https://platform.claude.com/docs/en/about-claude/pricing.
//     A cache write is the 5-minute one, 1.25× input.
//   - OpenAI: https://developers.openai.com/api/docs/pricing. OpenAI doesn't
//     charge for writing the cache, and Codex reports no cache writes.
//   - Cursor: https://cursor.com/docs/models.
//
// Add a model under the name its tool reports, without a date suffix
// (claude-haiku-4-5, not claude-haiku-4-5-20251001): Lookup strips those,
// and a provider prefix (anthropic/claude-sonnet-4-5) or reasoning effort
// (gpt-5.3-codex/high). A model that isn't here stays unpriced, and the Week
// page says how many tokens it left out, rather than guessing from a
// similar name. Change an entry when its vendor changes the price; turns
// already in the ledger are priced again each time, at the new price.
package pricing

import (
	"regexp"
	"strings"
)

// Price is a model's list price in US dollars per million tokens of each
// kind: input not read from the cache, output, cache reads, cache writes.
type Price struct {
	Input, Output, CacheRead, CacheWrite float64
}

// Cost is what the tokens cost at p.
func (p Price) Cost(input, output, cacheRead, cacheWrite int64) float64 {
	return (float64(input)*p.Input + float64(output)*p.Output +
		float64(cacheRead)*p.CacheRead + float64(cacheWrite)*p.CacheWrite) / 1e6
}

// anthropic is a Claude model's price from its input and output prices and
// what a cache read costs: cache writes are always 1.25× input.
func anthropic(input, output, cacheRead float64) Price {
	return Price{Input: input, Output: output, CacheRead: cacheRead, CacheWrite: input * 1.25}
}

// table is every model with a price, by the name its tool reports.
// Checked 26 October 2026.
var table = map[string]Price{
	// Anthropic.
	"claude-fable-5-1":  anthropic(10, 50, 0.25),
	"claude-mythos-5-1": anthropic(10, 50, 0.25),
	"claude-fable-5":    anthropic(10, 50, 1),
	"claude-mythos-5":   anthropic(10, 50, 1),
	"claude-opus-5-5":   anthropic(4, 20, 0.20),
	"claude-opus-5":     anthropic(5, 25, 0.50),
	"claude-opus-4-8":   anthropic(5, 25, 0.50),
	"claude-opus-4-7":   anthropic(5, 25, 0.50),
	"claude-opus-4-6":   anthropic(5, 25, 0.50),
	"claude-opus-4-5":   anthropic(5, 25, 0.50),
	"claude-opus-4-1":   anthropic(15, 75, 1.50),
	"claude-opus-4":     anthropic(15, 75, 1.50),
	"claude-sonnet-5-5": anthropic(2, 10, 0.20),
	"claude-sonnet-5":   anthropic(2, 10, 0.20),
	"claude-sonnet-4-6": anthropic(3, 15, 0.30),
	"claude-sonnet-4-5": anthropic(3, 15, 0.30),
	"claude-sonnet-4":   anthropic(3, 15, 0.30),
	// Haiku 5.5's price for prompts up to 100K tokens; beyond, $0.50/$2.50.
	"claude-haiku-5-5": anthropic(0.10, 0.50, 0.01),
	"claude-haiku-4-5": anthropic(1, 5, 0.10),
	"claude-3-5-haiku": anthropic(0.80, 4, 0.08),

	// OpenAI.
	"gpt-5.5":            {Input: 5, Output: 30, CacheRead: 0.50},
	"gpt-5.4":            {Input: 2.50, Output: 15, CacheRead: 0.25},
	"gpt-5.3-codex":      {Input: 1.75, Output: 14, CacheRead: 0.175},
	"gpt-5.2":            {Input: 1.75, Output: 14, CacheRead: 0.175},
	"gpt-5.2-codex":      {Input: 1.75, Output: 14, CacheRead: 0.175},
	"gpt-5.1":            {Input: 1.25, Output: 10, CacheRead: 0.125},
	"gpt-5.1-codex":      {Input: 1.25, Output: 10, CacheRead: 0.125},
	"gpt-5.1-codex-max":  {Input: 1.25, Output: 10, CacheRead: 0.125},
	"gpt-5.1-codex-mini": {Input: 0.25, Output: 2, CacheRead: 0.025},
	"gpt-5":              {Input: 1.25, Output: 10, CacheRead: 0.125},
	"gpt-5-codex":        {Input: 1.25, Output: 10, CacheRead: 0.125},
	"gpt-5-mini":         {Input: 0.25, Output: 2, CacheRead: 0.025},
	"gpt-5-nano":         {Input: 0.05, Output: 0.40, CacheRead: 0.005},

	// Cursor. Composer 2's cache-read price isn't published; it is taken as
	// a tenth of input, as most vendors charge.
	"composer-2": {Input: 0.50, Output: 2.50, CacheRead: 0.05},
	"composer-1": {Input: 1.25, Output: 10, CacheRead: 0.125},
}

var (
	// dated is a snapshot's date (claude-opus-4-1-20250805) or Vertex's
	// version (claude-opus-4-5@20251101).
	dated = regexp.MustCompile(`(-\d{8}|@.*)$`)
	// cursorClaude is how Cursor names Claude models, version first:
	// claude-4.5-sonnet, claude-4.6-opus-thinking.
	cursorClaude = regexp.MustCompile(`^claude-(\d+)(?:\.(\d+))?-(opus|sonnet|haiku)(?:-.*)?$`)
)

// Lookup is the price of model as an AI tool named it, and whether there is
// one.
func Lookup(model string) (Price, bool) {
	// A provider before the name, or an effort after it: try each part.
	for part := range strings.SplitSeq(strings.ToLower(strings.TrimSpace(model)), "/") {
		if p, ok := table[canonical(part)]; ok {
			return p, true
		}
	}
	return Price{}, false
}

// canonical is one part of a model's name as the table spells it.
func canonical(name string) string {
	// Claude Code's 1M-context suffix: claude-sonnet-4-5[1m].
	if i := strings.IndexByte(name, '['); i >= 0 {
		name = name[:i]
	}
	name = dated.ReplaceAllString(name, "")
	if m := cursorClaude.FindStringSubmatch(name); m != nil {
		name = "claude-" + m[3] + "-" + m[1]
		if m[2] != "" {
			name += "-" + m[2]
		}
	}
	return name
}
