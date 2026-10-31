// Package features is the catalogue of what AgentBox does for its user, one
// entry per feature: what it is, where it is in the app, and the lead tool or
// command that does it. A project's lead searches it (list_resources
// features) when the user asks whether or how AgentBox can do something, so
// the answer comes from here rather than from what the model guesses.
//
// The entries are features.json, which the desktop app's tests read too: a
// tab or a Settings section of the app with no entry naming it in places
// fails desktop/src/renderer/lib/features.test.ts, and a CLI command with no
// entry naming it in cli fails internal/cli's TestEveryCommandIsAFeature.
package features

import (
	_ "embed"
	"encoding/json"
	"math"
	"sort"
	"strings"
	"unicode"
)

// OfferedEvent is the type of the project memory event that says the lead
// suggested a feature to the user (list_resources features' offer), with the
// feature's name as the payload's feature. It is AgentBox's bookkeeping, so a
// distillation never reads it (internal/memory's distillTypes).
const OfferedEvent = "feature_offered"

// Feature is one thing the user can do with AgentBox.
type Feature struct {
	Name string `json:"name"`
	// What it does, in a sentence.
	What string `json:"what"`
	// Where it is in the app, for the user to find it; empty for a feature
	// only the CLI has.
	Where string `json:"where,omitempty"`
	// The lead tool that does it, with the parameter that does when it's
	// one of several (create_agent with issue).
	Tool string `json:"tool,omitempty"`
	// The agentbox commands that do it, each starting with "agentbox ".
	CLI []string `json:"cli,omitempty"`
	// The app's tabs and Settings sections it is on, as
	// desktop/src/renderer/lib/features.test.ts names them: project:<tab>,
	// project:settings/<section>, agent:<tab>, agent:settings/<section>,
	// lead:<panel tab>, panel:<view> and settings/<section>.
	Places []string `json:"places,omitempty"`
	// Words a user might ask with that the rest doesn't say.
	Keywords string `json:"keywords,omitempty"`
}

//go:embed features.json
var catalogue []byte

var all = func() []Feature {
	var list []Feature
	if err := json.Unmarshal(catalogue, &list); err != nil {
		panic("features.json: " + err.Error())
	}
	return list
}()

// All is every feature, in the catalogue's order.
func All() []Feature { return all }

// Named is the feature called name, ignoring case.
func Named(name string) (Feature, bool) {
	for _, f := range all {
		if strings.EqualFold(f.Name, name) {
			return f, true
		}
	}
	return Feature{}, false
}

// Search is the n features that best match query, best first. A word of the
// query in a feature's name counts most, then in its keywords, then in what
// it does and where it is, and each counts by how few features have it: "vps"
// says more than "agents". A word matches another it starts, when it has four
// letters or more ("record" matches "recordings"). None comes back when no
// word matches.
func Search(query string, n int) []Feature {
	words := terms(query)
	type scored struct {
		i     int
		score float64
	}
	var hits []scored
	for i, f := range indexed {
		score := 0.0
		for _, w := range words {
			switch {
			case matches(w, f.name):
				score += 3 * rarity(w)
			case matches(w, f.keywords):
				score += 2 * rarity(w)
			case matches(w, f.rest):
				score += rarity(w)
			}
		}
		if score > 0 {
			hits = append(hits, scored{i, score})
		}
	}
	sort.SliceStable(hits, func(a, b int) bool { return hits[a].score > hits[b].score })
	if len(hits) > n {
		hits = hits[:n]
	}
	out := make([]Feature, len(hits))
	for i, h := range hits {
		out[i] = all[h.i]
	}
	return out
}

// indexed is each feature's words, by how much a match in them counts.
var indexed = func() []struct{ name, keywords, rest []string } {
	out := make([]struct{ name, keywords, rest []string }, len(all))
	for i, f := range all {
		out[i].name = terms(f.Name)
		out[i].keywords = terms(f.Keywords)
		out[i].rest = terms(f.What + " " + f.Where + " " + f.Tool + " " + strings.Join(f.CLI, " "))
	}
	return out
}()

// rarity is how much a match of word says: the fewer features have it, the
// more.
func rarity(word string) float64 {
	have := 0
	for _, f := range indexed {
		if matches(word, f.name) || matches(word, f.keywords) || matches(word, f.rest) {
			have++
		}
	}
	return math.Log(1 + float64(len(indexed))/float64(max(have, 1)))
}

func matches(word string, in []string) bool {
	for _, t := range in {
		if t == word || (len(word) >= 4 && strings.HasPrefix(t, word)) || (len(t) >= 4 && strings.HasPrefix(word, t)) {
			return true
		}
	}
	return false
}

// stop are words a question about AgentBox is made of that say nothing about
// which feature it is after.
var stop = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "agentbox": true, "be": true, "can": true, "do": true,
	"does": true, "for": true, "from": true, "get": true, "how": true, "i": true, "in": true, "is": true,
	"it": true, "its": true, "me": true, "my": true, "of": true, "on": true, "or": true, "the": true,
	"there": true, "to": true, "use": true, "what": true, "when": true, "where": true, "with": true,
	"you": true, "your": true, "way": true, "want": true, "make": true, "able": true, "any": true,
}

// terms is text's words, lower-cased, without the stop words and with a
// plural's s taken off.
func terms(text string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if len(w) < 2 || stop[w] {
			continue
		}
		if len(w) > 3 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") {
			w = w[:len(w)-1]
		}
		out = append(out, w)
	}
	return out
}
