package daemon

import (
	"cmp"
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"agentbox/internal/acp"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// A Claude account's usage limits (D85). Nothing asks Anthropic for them: a
// subscription's limits are only reported in the responses a chat gets, and
// claude-agent-acp relays them on usage_update. The chat hands each reading
// here, and the newest per account is kept, so the app and `agentbox tokens`
// can say how much of the five-hour window is left — as of the last time any
// agent on that account talked to its model.

// claudeLimited keeps a reading under the account the agent's chat used: the
// agent's own when it has one, else the default, which is what its token was.
func (s *Server) claudeLimited(a state.Agent, reading acp.RateLimit) {
	if a.AI != "claude" {
		return
	}
	account, err := s.manager(nil).Creds.ClaudeAccountOf(a.ClaudeAccount)
	if err != nil || account == "" {
		return
	}
	raw, err := json.Marshal(reading)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.store.SetClaudeLimit(ctx, account, raw, time.Now()); err != nil {
		s.logf("recording %s's usage limits: %v", account, err)
	}
}

// limitWindows are the windows Anthropic names, in the order they are shown,
// and how to say each one. A window it adds later is shown after these, by its
// own name.
var limitWindows = []struct{ name, label string }{
	{"five_hour", "5-hour"},
	{"seven_day", "Weekly"},
	{"seven_day_opus", "Weekly (Opus)"},
	{"seven_day_sonnet", "Weekly (Sonnet)"},
}

// toClaudeLimit turns a stored reading into what the API answers with.
func toClaudeLimit(r state.ClaudeLimitReading, isDefault bool) (api.ClaudeLimit, bool) {
	var reading acp.RateLimit
	if json.Unmarshal(r.Reading, &reading) != nil {
		return api.ClaudeLimit{}, false
	}
	out := api.ClaudeLimit{
		Account: r.Account, Default: isDefault, At: r.At, Status: reading.Status,
		UsingOverage: reading.IsUsingOverage, Windows: []api.ClaudeLimitWindow{},
	}
	rank := func(name string) int {
		if i := slices.IndexFunc(limitWindows, func(w struct{ name, label string }) bool { return w.name == name }); i >= 0 {
			return i
		}
		return len(limitWindows)
	}
	for name, w := range reading.UnifiedWindows {
		label := strings.ReplaceAll(name, "_", " ")
		if i := rank(name); i < len(limitWindows) {
			label = limitWindows[i].label
		}
		out.Windows = append(out.Windows, api.ClaudeLimitWindow{
			Name: name, Label: label, Utilization: w.Utilization, ResetsAt: time.Unix(w.ResetsAt, 0),
		})
	}
	slices.SortFunc(out.Windows, func(a, b api.ClaudeLimitWindow) int {
		return cmp.Or(cmp.Compare(rank(a.Name), rank(b.Name)), cmp.Compare(a.Name, b.Name))
	})
	return out, true
}

// claudeLimits answers GET /v1/limits: the newest reading of each stored Claude
// account that has had one, the default account first. An account removed
// since is left out.
func (s *Server) claudeLimits(w http.ResponseWriter, r *http.Request) error {
	accounts, err := s.manager(nil).Creds.ClaudeAccounts()
	if err != nil {
		return err
	}
	isDefault := map[string]bool{}
	for _, a := range accounts {
		isDefault[a.Name] = a.Default
	}
	readings, err := s.store.ClaudeLimits(r.Context())
	if err != nil {
		return err
	}
	out := []api.ClaudeLimit{}
	for _, reading := range readings {
		def, stored := isDefault[reading.Account]
		if !stored {
			continue
		}
		if limit, ok := toClaudeLimit(reading, def); ok {
			out = append(out, limit)
		}
	}
	slices.SortStableFunc(out, func(a, b api.ClaudeLimit) int {
		switch {
		case a.Default && !b.Default:
			return -1
		case b.Default && !a.Default:
			return 1
		}
		return 0
	})
	return writeJSON(w, http.StatusOK, out)
}
