package features

import (
	"strings"
	"testing"
)

func TestEveryFeatureIsComplete(t *testing.T) {
	seen := map[string]bool{}
	for _, f := range All() {
		if f.Name == "" || f.What == "" {
			t.Errorf("%q has no name or no what", f.Name)
		}
		if seen[strings.ToLower(f.Name)] {
			t.Errorf("%q is in the catalogue twice", f.Name)
		}
		seen[strings.ToLower(f.Name)] = true
		if f.Where == "" && len(f.CLI) == 0 {
			t.Errorf("%q says neither where it is nor its command", f.Name)
		}
		for _, c := range f.CLI {
			if !strings.HasPrefix(c, "agentbox ") {
				t.Errorf("%q's command %q doesn't start with agentbox", f.Name, c)
			}
		}
		if !strings.HasSuffix(f.What, ".") {
			t.Errorf("%q's what isn't a sentence: %q", f.Name, f.What)
		}
	}
}

// TestSearchFindsWhatUsersAsk holds the search to the questions users ask in
// their own words, which rarely name the feature.
func TestSearchFindsWhatUsersAsk(t *testing.T) {
	for query, want := range map[string]string{
		"can AgentBox record a video of the change?":       "Recordings and screenshots as proof",
		"how do I try the same task on opus and sonnet?":   "Compare models or tools on one task",
		"can it triage new issues every Monday at 8?":      "Schedules",
		"can I chat with my agents from my phone":          "Phone",
		"give agents my Stripe API key":                    "Secrets",
		"undo the agent's last turn":                       "Checkpoints, rollback and fork",
		"how much did this project cost in tokens?":        "Tokens and cost",
		"can agents use Notion?":                           "Connectors",
		"run agents on my VPS":                             "Other machines through a hub",
		"does it have a dark theme":                        "Appearance and language",
		"move everything to my new laptop":                 "Export and import",
		"is there an Android emulator":                     "Android emulator",
		"the disk is full":                                 "Disk space",
		"let the lead decide product questions on its own": "Autonomy",
	} {
		got := Search(query, 3)
		var names []string
		for _, f := range got {
			names = append(names, f.Name)
		}
		if len(got) == 0 || got[0].Name != want {
			t.Errorf("Search(%q) = %q, want %q first", query, names, want)
		}
	}
}

func TestSearchMatchesNothingForNothing(t *testing.T) {
	if got := Search("can AgentBox", 3); len(got) != 0 {
		t.Errorf("a query of stop words matched %d features", len(got))
	}
	if got := Search("zxqv", 3); len(got) != 0 {
		t.Errorf("a word in no feature matched %d", len(got))
	}
}

func TestNamed(t *testing.T) {
	if f, ok := Named("schedules"); !ok || f.Name != "Schedules" {
		t.Errorf("Named(schedules) = %+v, %v", f, ok)
	}
	if _, ok := Named("nothing like it"); ok {
		t.Error("found a feature that isn't there")
	}
}
