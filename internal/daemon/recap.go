package daemon

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/gitrepo"
	"agentbox/internal/state"
)

// The weekly recap: the user's week with AgentBox, for its page and the cards
// they share (desktop's RecapView). Everything comes from state.db: the token
// ledger says when agents worked, chat items and verdicts when the user was
// there, and the Work page's entries (work.go) which agents had pull requests
// and what their branches changed. Nothing is kept: it is read again each
// time the page opens.

// recapStreakDays is how far back a streak is looked for.
const recapStreakDays = 366

// recap is GET /v1/recap: ?offset= is the user's time zone, minutes east of
// UTC, and ?weeks= how many weeks before this one.
func (s *Server) recap(w http.ResponseWriter, r *http.Request) error {
	v := r.URL.Query()
	var offset, weeks int
	if o := v.Get("offset"); o != "" {
		n, err := strconv.Atoi(o)
		if err != nil || n < -14*60 || n > 14*60 {
			return errors.New("offset must be minutes east of UTC, from -840 to 840")
		}
		offset = n
	}
	if wk := v.Get("weeks"); wk != "" {
		n, err := strconv.Atoi(wk)
		if err != nil || n < 0 || n > 520 {
			return errors.New("weeks must be a number from 0 to 520")
		}
		weeks = n
	}
	out, err := s.buildRecap(r.Context(), time.Now(), time.Duration(offset)*time.Minute, weeks)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

// recapAgent is what the recap knows of one agent of the week.
type recapAgent struct {
	project, name string
	title, ai     string
	work          time.Duration
	created       time.Time
	entry         *workEntry
}

// buildRecap is the week ending today, weeks weeks ago, in the time zone
// offset east of UTC, as of now.
func (s *Server) buildRecap(ctx context.Context, now time.Time, offset time.Duration, weeks int) (api.Recap, error) {
	zone := time.FixedZone("", int(offset.Seconds()))
	local := now.In(zone)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, zone)
	start := today.AddDate(0, 0, -6-7*weeks)
	end := today.AddDate(0, 0, 1-7*weeks)
	out := api.Recap{Start: start, End: end, Days: make([]api.RecapDay, 7), Hours: make([]int64, 24), Models: []api.RecapModel{}}
	for i := range out.Days {
		out.Days[i].Date = start.AddDate(0, 0, i).Format(time.DateOnly)
	}

	spans, err := s.store.TurnSpans(ctx, start, end)
	if err != nil {
		return out, err
	}
	seen, err := s.store.UserPresence(ctx, start.Add(-api.RecapPresence), end)
	if err != nil {
		return out, err
	}
	present := presenceOf(seen)

	agents := map[string]*recapAgent{}
	agentOf := func(project, name string) *recapAgent {
		ref := project + "/" + name
		if a, ok := agents[ref]; ok {
			return a
		}
		a := &recapAgent{project: project, name: name}
		agents[ref] = a
		return a
	}
	projectWork := map[string]time.Duration{}
	dayAgents := make([]map[string]bool, 7)
	var work, away time.Duration
	var dayWork, dayAway [7]time.Duration
	var hourWork [24]time.Duration
	for _, sp := range spans {
		a := agentOf(sp.Project, sp.Agent)
		from, to := later(sp.Start, start), sp.End
		// The turn in pieces at each hour it crossed, for the days and hours.
		for cur := from; cur.Before(to); {
			lt := cur.In(zone)
			next := earlier(time.Date(lt.Year(), lt.Month(), lt.Day(), lt.Hour()+1, 0, 0, 0, zone), to)
			d := next.Sub(cur)
			gone := d - present.overlap(cur, next)
			day := int(time.Date(lt.Year(), lt.Month(), lt.Day(), 0, 0, 0, 0, zone).Sub(start).Hours() / 24)
			if day >= 0 && day < 7 {
				dayWork[day] += d
				dayAway[day] += gone
				if dayAgents[day] == nil {
					dayAgents[day] = map[string]bool{}
				}
				dayAgents[day][sp.Project+"/"+sp.Agent] = true
			}
			hourWork[lt.Hour()] += d
			work += d
			away += gone
			a.work += d
			projectWork[sp.Project] += d
			cur = next
		}
	}
	out.WorkSeconds, out.AwaySeconds = int64(work.Seconds()), int64(away.Seconds())
	for i := range out.Days {
		out.Days[i].WorkSeconds = int64(dayWork[i].Seconds())
		out.Days[i].AwaySeconds = int64(dayAway[i].Seconds())
		out.Days[i].Agents = len(dayAgents[i])
	}
	for i := range out.Hours {
		out.Hours[i] = int64(hourWork[i].Seconds())
	}

	// Who those agents were, and every worker created in the week besides,
	// from the Work page's entries: live agents and past ones.
	projects, err := s.store.Projects(ctx)
	if err != nil {
		return out, err
	}
	live, err := s.store.Agents(ctx, "")
	if err != nil {
		return out, err
	}
	created := map[string]time.Time{}
	for _, a := range live {
		created[a.Ref()] = a.CreatedAt
	}
	for _, p := range projects {
		if p.Name == state.HomeProject {
			continue
		}
		entries, err := s.projectWorkEntries(ctx, p)
		if err != nil {
			return out, err
		}
		for i := range entries {
			e := &entries[i]
			ref := e.item.Item.Ref
			at := created[ref]
			if e.past != nil {
				at = e.past.CreatedAt
			}
			if _, worked := agents[ref]; !worked && (at.Before(start) || !at.Before(end)) {
				continue
			}
			a := agentOf(e.item.Item.Project, e.item.Item.Agent)
			a.title, a.ai, a.created, a.entry = e.item.Item.Title, e.item.Item.AI, at, e
		}
	}

	byProject := map[string]*api.RecapProject{}
	for _, a := range agents {
		out.Agents++
		p := byProject[a.project]
		if p == nil {
			p = &api.RecapProject{Project: a.project, WorkSeconds: int64(projectWork[a.project].Seconds())}
			byProject[a.project] = p
		}
		p.Agents++
		if out.Longest == nil || int64(a.work.Seconds()) > out.Longest.WorkSeconds {
			out.Longest = &api.RecapAgent{Project: a.project, Agent: a.name, Title: a.title, AI: a.ai, WorkSeconds: int64(a.work.Seconds())}
		}
		if a.entry == nil {
			continue
		}
		item := a.entry.item.Item
		if pr := item.PR; pr != nil && pr.Number > 0 {
			out.PRsOpened++
			if pr.State == "merged" {
				out.PRsMerged++
			}
		}
		changes := item.Changes
		if changes == nil && a.entry.past != nil {
			if commit, base, err := archivedRange(a.entry.root, *a.entry.past); err == nil && base != "" {
				if stat, err := gitrepo.DiffCommits(a.entry.root, base, commit, true); err == nil {
					c := statOf(stat)
					changes = &c
				}
			}
		}
		if changes != nil {
			out.Insertions += changes.Insertions
			out.Deletions += changes.Deletions
			out.Files += changes.Files
		}
	}
	if out.Longest != nil && out.Longest.WorkSeconds == 0 {
		out.Longest = nil
	}
	out.Projects = len(byProject)
	for _, p := range byProject {
		if out.Busiest == nil || cmp.Or(cmp.Compare(p.WorkSeconds, out.Busiest.WorkSeconds), cmp.Compare(p.Agents, out.Busiest.Agents)) > 0 {
			out.Busiest = p
		}
	}

	totals, err := s.store.TokenTotals(ctx, state.TokenFilter{Since: start, Until: end})
	if err != nil {
		return out, err
	}
	models := map[string]*api.RecapModel{}
	for _, t := range totals {
		m := models[t.Model]
		if m == nil {
			m = &api.RecapModel{Model: t.Model}
			models[t.Model] = m
		}
		m.Tokens += t.Input + t.Output + t.CacheRead + t.CacheWrite
		m.Output += t.Output
		out.CostUSD += t.CostUSD
	}
	for _, m := range models {
		if m.Tokens == 0 {
			continue
		}
		out.Models = append(out.Models, *m)
		out.Tokens += m.Tokens
	}
	slices.SortFunc(out.Models, func(a, b api.RecapModel) int {
		return cmp.Or(cmp.Compare(b.Tokens, a.Tokens), cmp.Compare(a.Model, b.Model))
	})

	days, err := s.store.TurnDays(ctx, today.AddDate(0, 0, -recapStreakDays), offset)
	if err != nil {
		return out, err
	}
	out.Streak, out.BestStreak = streaks(days, (now.UnixMilli()+offset.Milliseconds())/86400000)
	return out, nil
}

// streaks is the run of days up to today (or yesterday, when today has
// nothing yet) and the longest run, in days, ascending, since the epoch.
func streaks(days []int64, today int64) (current, best int) {
	has := map[int64]bool{}
	run := 0
	for i, d := range days {
		has[d] = true
		if i > 0 && days[i-1] == d-1 {
			run++
		} else {
			run = 1
		}
		best = max(best, run)
	}
	d := today
	if !has[d] {
		d--
	}
	for has[d] {
		current++
		d--
	}
	return current, best
}

// presence is the stretches the user was there, sorted and merged.
type presence [][2]time.Time

// presenceOf is the stretches of RecapPresence after each of seen.
func presenceOf(seen []time.Time) presence {
	slices.SortFunc(seen, func(a, b time.Time) int { return a.Compare(b) })
	var out presence
	for _, t := range seen {
		if n := len(out); n > 0 && !t.After(out[n-1][1]) {
			out[n-1][1] = t.Add(api.RecapPresence)
			continue
		}
		out = append(out, [2]time.Time{t, t.Add(api.RecapPresence)})
	}
	return out
}

// overlap is how much of [from, to) the user was there for.
func (p presence) overlap(from, to time.Time) time.Duration {
	var d time.Duration
	for _, s := range p {
		if !s[0].Before(to) {
			break
		}
		if lo, hi := later(s[0], from), earlier(s[1], to); lo.Before(hi) {
			d += hi.Sub(lo)
		}
	}
	return d
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func earlier(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
