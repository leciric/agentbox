package daemon

import (
	"cmp"
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// searchSessionMessages is how many messages of the chats' sessions a search
// reads to find the sessions they were said in: a session counts once, at its
// best match, so a word every session uses still reaches most of them.
const searchSessionMessages = 1000

// searchSessions finds the sessions of the chats, open and closed, the way
// the Ctrl+K palette jumps to one: by topic, by what was said in it (the chat
// index, state.SearchChats with Leads, the query in one piece as a chat's
// find bar takes it), and by the agents it made, their names, branches and
// pull requests. A session found by what was said opens at its best match
// (SearchHit.Item); a closed one is Archived, and opens read-only in its
// chat's history. With no project it is every project's and the Home chat's.
//
// The agents are the ones still there: an archived agent keeps no session.
func (s *Server) searchSessions(ctx context.Context, known map[string]state.Project, agents []state.Agent, filter searchFilter, q string, ws searchWords) ([]searchFound, error) {
	projects := []string{filter.project}
	if filter.project == "" {
		projects = []string{state.HomeProject}
		for name := range known {
			projects = append(projects, name)
		}
	}
	type key struct{ project, session string }
	type found struct {
		hit   api.SearchHit
		score int
	}
	byKey := map[key]*found{}
	var order []key
	for _, project := range projects {
		sessions, err := s.store.AllLeadSessions(ctx, project)
		if err != nil {
			return nil, err
		}
		if len(sessions) == 0 {
			continue
		}
		open, err := s.store.LeadSessionActivity(ctx, project)
		if err != nil {
			return nil, err
		}
		closed, err := s.store.ClosedLeadSessionActivity(ctx, project)
		if err != nil {
			return nil, err
		}
		for _, ls := range sessions {
			at := cmp.Or(open[ls.ID], closed[ls.ID], ls.CreatedAt)
			k := key{project, ls.ID}
			byKey[k] = &found{hit: api.SearchHit{
				ID: ls.ID, Title: ls.Topic, Project: project, Session: ls.ID, Archived: ls.Closed(), At: timePtr(at),
			}, score: -1}
			order = append(order, k)
			if score := ws.score(ls.Topic); score >= 0 {
				byKey[k].score = 10 + score
			}
		}
	}
	if len(byKey) == 0 {
		return nil, nil
	}

	// What was said: each session at its best match, ranked by the index.
	if utf8.RuneCountInString(q) >= searchChatsMin {
		hits, _, err := s.store.SearchChats(ctx, state.ChatSearch{Query: q, Project: filter.project, Leads: true, Since: filter.since, Limit: searchSessionMessages})
		if err != nil {
			return nil, err
		}
		said := map[key]int{}
		for _, h := range hits {
			id, ok := state.SessionOfLead(h.Agent)
			if !ok {
				continue
			}
			f := byKey[key{h.Project, id}]
			if f == nil {
				continue
			}
			k := key{h.Project, id}
			said[k]++
			if said[k] > 1 {
				continue
			}
			var text strings.Builder
			for _, part := range h.Snippet {
				text.WriteString(part.Text)
			}
			f.hit.Item, f.hit.Detail = h.ID, text.String()
			f.score = max(f.score, 0) + 6
		}
	}

	// The agents each made, by name, title, branch and pull request.
	number, isNumber := state.PRNumberQuery(strings.Join(ws, " "))
	prs := map[string]api.PullRequest{}
	for project, list := range s.pulls.known() {
		for _, pr := range list {
			if pr.Agent != "" {
				prs[project+"/"+pr.Agent] = pr
			}
		}
	}
	for _, a := range agents {
		if a.IsLead() || a.Session() == "" {
			continue
		}
		f := byKey[key{a.Project, a.Session()}]
		if f == nil || f.hit.Tag != "" {
			continue
		}
		pr, hasPR := prs[a.Ref()]
		fields := []string{a.Name, a.Title, a.Branch}
		if hasPR {
			fields = append(fields, pr.Title, fmt.Sprintf("#%d", pr.Number))
		}
		score := ws.score(fields...)
		if isNumber && hasPR && pr.Number == number {
			score = max(score, 0) + 20
		}
		if score < 0 {
			continue
		}
		f.hit.Tag = a.Branch
		if hasPR {
			f.hit.Tag = fmt.Sprintf("#%d", pr.Number)
		}
		if f.hit.Detail == "" {
			f.hit.Detail = strings.Join(nonEmpty(cmp.Or(a.Title, a.Name), a.Branch, pr.Title), " · ")
		}
		f.score = max(f.score, 0) + 4 + score
	}

	var out []searchFound
	for _, k := range order {
		if f := byKey[k]; f.score >= 0 {
			out = append(out, searchFound{f.hit, f.score})
		}
	}
	return out, nil
}

func nonEmpty(parts ...string) []string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
