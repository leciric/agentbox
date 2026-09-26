package chat

import (
	"context"
	"slices"
	"strings"
	"time"

	"agentbox/internal/acp"
	"agentbox/internal/state"
)

// The token ledger's writer (D83).
//
// Every model call a chat makes goes through an adapter this package runs, so
// this is the one place that sees all of them: turns, the results a session
// produces by itself between turns, and the hidden prompts that compact and
// consolidate a project's chat. What each spent is written to the ledger as
// it ends (state.AddTokenRows), and the ledger is never rewritten.
//
// Tokens come from the prompt response (acp.PromptResponse.ByModel). Cost
// comes from usage_update, which carries the adapter's running total rather
// than a figure per result, so a turn costs whatever that total moved by
// since the ledger last heard from this adapter.

// minCost is the smallest cost worth a row of its own: below it the running
// total only moved by rounding.
const minCost = 1e-9

// spend is what one adapter process has cost, as far as the ledger knows.
type spend struct {
	cost   float64 // the adapter's running total, as last reported
	booked float64 // how much of it the ledger already has
}

// observe takes a usage_update's cost. The running total only goes down when
// the tool started counting again (a /clear), and then all of it is new.
func (s *spend) observe(cost *acp.Cost) {
	if cost == nil {
		return
	}
	if cost.Amount < s.cost {
		s.booked = 0
	}
	s.cost = cost.Amount
}

// take hands over what the ledger hasn't had yet.
func (s *spend) take() float64 {
	delta := s.cost - s.booked
	s.booked = s.cost
	if delta < minCost {
		return 0
	}
	return delta
}

// book writes what the adapter spent on one turn, or on one result of its
// own: the response's tokens — none, for a turn that failed or a result no
// prompt asked for — and the cost since the last booking. Nothing is written
// when there is neither. generationMS is how long it took to generate, 0 when
// nothing timed it (state.TokenRow.GenerationMS). The conversation is locked;
// the write happens off the lock.
func (c *conversation) book(ad *adapter, kind, turn string, res *acp.PromptResponse, generationMS int64) {
	model := optionValueOf(c.session.Options, "model")
	var spent map[string]acp.Spent
	if res != nil {
		spent = res.ByModel(model)
	}
	cost := ad.spend.take()
	rows := tokenRows(state.TokenRow{
		Project: c.agent.Project,
		Agent:   c.agent.Name,
		AI:      c.agent.AI,
		Session: ad.sessionID,
		Turn:    turn,
		Kind:    kind,
		At:      c.m.now(),
		Context: c.session.ContextUsed,
	}, spent, model, cost, generationMS)
	if len(rows) > 0 {
		go c.m.recordTokens(rows)
	}
}

// tokenRows makes a turn's ledger rows: one per model, the busiest first,
// carrying the turn's whole cost and generation time. A turn that reported a
// cost and no tokens — a failed turn, or a result the session made by itself —
// is one row under the model the session was on.
func tokenRows(base state.TokenRow, spent map[string]acp.Spent, fallback string, cost float64, generationMS int64) []state.TokenRow {
	if len(spent) == 0 {
		if cost <= 0 {
			return nil
		}
		row := base
		row.Model, row.CostUSD, row.GenerationMS = fallback, cost, generationMS
		return []state.TokenRow{row}
	}
	models := make([]string, 0, len(spent))
	for model := range spent {
		models = append(models, model)
	}
	// The package has a cmp of its own (chat.go), so the busiest-first order
	// is spelled out rather than taken from the standard library's.
	slices.SortFunc(models, func(a, b string) int {
		if x, y := spent[a].Total(), spent[b].Total(); x != y {
			if x > y {
				return -1
			}
			return 1
		}
		return strings.Compare(a, b)
	})
	rows := make([]state.TokenRow, 0, len(models))
	for i, model := range models {
		s := spent[model]
		row := base
		row.Model = model
		row.Input, row.Output, row.CacheRead, row.CacheWrite = s.Input, s.Output, s.CacheRead, s.CacheWrite
		if i == 0 {
			row.CostUSD, row.GenerationMS = cost, generationMS
		}
		rows = append(rows, row)
	}
	return rows
}

// recordTokens writes rows to the ledger. A row that can't be written is
// logged and lost: the turn it describes has already happened, and holding
// the chat up to retry would cost more than the line is worth.
func (m *Manager) recordTokens(rows []state.TokenRow) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := m.Store.AddTokenRows(ctx, rows); err != nil {
		m.logf("chat %s/%s: recording what the turn spent: %v", rows[0].Project, rows[0].Agent, err)
	}
}
