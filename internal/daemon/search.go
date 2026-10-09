package daemon

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"agentbox/internal/api"
	"agentbox/internal/memory"
	"agentbox/internal/notes"
	"agentbox/internal/state"
)

const (
	// searchLimit is how many of each kind a search returns unless asked
	// otherwise, and searchMaxLimit the most it will.
	searchLimit    = 5
	searchMaxLimit = 50
	// snippetRunes is about how much of a long text is shown around a match.
	snippetRunes = 140
)

// search is GET /v1/search, the app's search palette: one call that looks for
// the words typed so far through everything AgentBox keeps, in every project
// — projects, agents, memory, media, skills, connectors, notes and the pull
// requests it last read — and answers each kind as a group of its best.
//
// It is asked on every pause in the typing, so it reads only what is already
// here: the database, the notes files and the pull requests cache, never git
// or GitHub. Memory goes through its FTS5 indexes (memory.SearchAll); the
// rest is small enough to match in memory, a word at a time.
func (s *Server) search(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	limit := searchLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return errors.New("limit must be a positive number")
		}
		limit = min(n, searchMaxLimit)
	}
	out := api.SearchResults{Query: q, Groups: []api.SearchGroup{}}
	words := searchWordsOf(q)
	if len(words) == 0 {
		return writeJSON(w, http.StatusOK, out)
	}

	projects, err := s.store.Projects(ctx)
	if err != nil {
		return err
	}
	agents, err := s.store.Agents(ctx, "")
	if err != nil {
		return err
	}
	known := map[string]state.Project{}
	for _, p := range projects {
		known[p.Name] = p
	}

	// In the order the app shows them.
	add := func(kind string, found []searchFound, err error) error {
		if err != nil {
			return err
		}
		if g, ok := searchGroupOf(kind, found, limit); ok {
			out.Groups = append(out.Groups, g)
		}
		return nil
	}
	if err := add(api.SearchProjects, searchProjects(projects, words), nil); err != nil {
		return err
	}
	found, err := s.searchAgents(ctx, projects, agents, words)
	if err := add(api.SearchAgents, found, err); err != nil {
		return err
	}
	chats, more, err := s.searchChats(ctx, known, agents, q, limit)
	if err != nil {
		return err
	}
	if len(chats) > 0 {
		out.Groups = append(out.Groups, api.SearchGroup{Kind: api.SearchChats, Hits: chats, More: more})
	}
	memoryGroups, err := s.searchMemory(ctx, known, q, limit)
	if err != nil {
		return err
	}
	out.Groups = append(out.Groups, memoryGroups...)
	found, err = s.searchMedia(ctx, known, agents, words)
	if err := add(api.SearchMedia, found, err); err != nil {
		return err
	}
	found, err = s.searchSkills(ctx, words)
	if err := add(api.SearchSkills, found, err); err != nil {
		return err
	}
	found, err = s.searchConnectors(ctx, known, words)
	if err := add(api.SearchConnectors, found, err); err != nil {
		return err
	}
	if err := add(api.SearchNotes, s.searchNotes(projects, words), nil); err != nil {
		return err
	}
	if err := add(api.SearchPulls, s.searchPulls(known, words), nil); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

// searchFound is a hit with how well it matched, before the group is ranked.
type searchFound struct {
	hit   api.SearchHit
	score int
}

// searchGroupOf ranks what one kind found, best match first and newest among
// equals, and keeps the first limit of it.
func searchGroupOf(kind string, found []searchFound, limit int) (api.SearchGroup, bool) {
	if len(found) == 0 {
		return api.SearchGroup{}, false
	}
	slices.SortStableFunc(found, func(a, b searchFound) int {
		if c := cmp.Compare(b.score, a.score); c != 0 {
			return c
		}
		return cmp.Compare(timeOf(b.hit.At), timeOf(a.hit.At))
	})
	g := api.SearchGroup{Kind: kind, More: len(found) > limit}
	for _, f := range found[:min(limit, len(found))] {
		g.Hits = append(g.Hits, f.hit)
	}
	return g, true
}

func timeOf(at *time.Time) int64 {
	if at == nil {
		return 0
	}
	return at.UnixNano()
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// searchWords are the words of a query, lowercased: a match has every one of
// them, anywhere in its fields, in any order, as a whole word or a part of
// one, so a word still being typed already matches.
type searchWords []string

func searchWordsOf(q string) searchWords {
	return strings.Fields(strings.ToLower(q))
}

// score says how well fields match, the first of them being the thing's name:
// -1 when a word is in none of them, and otherwise more for each word in the
// name than elsewhere, and most for a name that starts with what was typed.
func (ws searchWords) score(fields ...string) int {
	lower := make([]string, len(fields))
	for i, f := range fields {
		lower[i] = strings.ToLower(f)
	}
	score := 0
	for _, w := range ws {
		switch {
		case len(lower) > 0 && strings.Contains(lower[0], w):
			score += 3
		case slices.ContainsFunc(lower[1:], func(f string) bool { return strings.Contains(f, w) }):
			score++
		default:
			return -1
		}
	}
	if len(lower) > 0 {
		whole := strings.Join(ws, " ")
		switch {
		case lower[0] == whole:
			score += 10
		case strings.HasPrefix(lower[0], whole):
			score += 5
		}
	}
	return score
}

// snippet is the part of text around the first of the words found in it, on
// one line, with an ellipsis where it was cut; text itself when it is short,
// and its start when none of the words is in it.
func (ws searchWords) snippet(text string) string {
	runes := []rune(strings.Join(strings.Fields(text), " "))
	if len(runes) <= snippetRunes {
		return string(runes)
	}
	// Lowered rune by rune, so an index into it is an index into runes.
	lower := make([]rune, len(runes))
	for i, r := range runes {
		lower[i] = unicode.ToLower(r)
	}
	at := -1
	for _, w := range ws {
		if i := runeIndex(lower, []rune(w)); i >= 0 && (at < 0 || i < at) {
			at = i
		}
	}
	start := max(0, at-snippetRunes/3)
	if at < 0 {
		start = 0
	}
	// Start and end on a word's edge where there's one nearby.
	for i := start; start > 0 && i < min(start+12, len(runes)); i++ {
		if unicode.IsSpace(runes[i]) {
			start = i + 1
			break
		}
	}
	end := min(len(runes), start+snippetRunes)
	for i := end; end < len(runes) && i > max(start, end-12); i-- {
		if unicode.IsSpace(runes[i]) {
			end = i
			break
		}
	}
	out := string(runes[start:end])
	if start > 0 {
		out = "…" + out
	}
	if end < len(runes) {
		out += "…"
	}
	return out
}

func runeIndex(s, sub []rune) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if slices.Equal(s[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}

func searchProjects(projects []state.Project, ws searchWords) []searchFound {
	var out []searchFound
	for _, p := range projects {
		name := cmp.Or(p.DisplayName, p.Name)
		if score := ws.score(name, p.Name, p.Root); score >= 0 {
			out = append(out, searchFound{api.SearchHit{
				ID: p.Name, Title: name, Detail: p.Root, Project: p.Name, At: timePtr(p.CreatedAt),
			}, score})
		}
	}
	return out
}

// searchAgents finds agents by what they're called, their branch, and the
// task the lead gave them (the newest one, when there were several).
func (s *Server) searchAgents(ctx context.Context, projects []state.Project, agents []state.Agent, ws searchWords) ([]searchFound, error) {
	tasks := map[string]string{}
	mem := s.memory()
	for _, p := range projects {
		list, err := mem.Tasks(ctx, p.Name, memory.TaskFilter{})
		if err != nil {
			return nil, err
		}
		newest := map[string]time.Time{}
		for _, t := range list {
			if t.Agent == "" || t.CreatedAt.Before(newest[t.Agent]) {
				continue
			}
			newest[t.Agent] = t.CreatedAt
			tasks[p.Name+"/"+t.Agent] = t.Goal
		}
	}
	var out []searchFound
	for _, a := range agents {
		if a.IsLead() {
			continue
		}
		ref := a.Project + "/" + a.Name
		task := tasks[ref]
		score := ws.score(cmp.Or(a.Title, a.Name), a.Name, a.Branch, task)
		if score < 0 {
			continue
		}
		out = append(out, searchFound{api.SearchHit{
			ID: ref, Title: cmp.Or(a.Title, a.Name), Detail: ws.snippet(task), Tag: a.Branch,
			Project: a.Project, Agent: a.Name, At: timePtr(a.CreatedAt),
		}, score})
	}
	return out, nil
}

// searchChatsMin is how long a query has to be before chats are searched:
// shorter than the chat index's trigrams, a search reads every message
// AgentBox holds, which is too slow to do on every keystroke.
const searchChatsMin = 3

// searchChats is what was said in every chat, through the chat index
// (state.SearchChats), best match first: the query found as typed, in one
// piece, the way a chat's own find bar finds it. A lead's messages are its
// project's chat (no Agent); the Home chat's are the project HomeProject.
func (s *Server) searchChats(ctx context.Context, known map[string]state.Project, agents []state.Agent, q string, limit int) ([]api.SearchHit, bool, error) {
	if utf8.RuneCountInString(q) < searchChatsMin {
		return nil, false, nil
	}
	found, more, err := s.store.SearchChats(ctx, state.ChatSearch{Query: q, Limit: limit})
	if err != nil {
		return nil, false, err
	}
	leads := map[string]bool{}
	for _, a := range agents {
		if a.IsLead() {
			leads[a.Project+"/"+a.Name] = true
		}
	}
	var out []api.SearchHit
	for _, h := range found {
		if _, ok := known[h.Project]; !ok && h.Project != state.HomeProject {
			continue
		}
		agent := h.Agent
		if leads[h.Project+"/"+h.Agent] || h.Project == state.HomeProject {
			agent = ""
		}
		var text strings.Builder
		for _, part := range h.Snippet {
			text.WriteString(part.Text)
		}
		out = append(out, api.SearchHit{ID: h.ID, Title: text.String(), Tag: h.Kind, Project: h.Project, Agent: agent})
	}
	return out, more, nil
}

// searchMemory is the memories, events and reports of every project, from
// memory's own indexes, each kind already ranked there. A row of a project
// that is gone, or of the Home chat, which has no Memory page, is left out.
func (s *Server) searchMemory(ctx context.Context, known map[string]state.Project, q string, limit int) ([]api.SearchGroup, error) {
	// One more than the limit, to know whether there were more.
	found, err := s.memory().SearchAll(ctx, q, limit+1)
	if err != nil {
		return nil, err
	}
	ws := searchWordsOf(q)
	var memories, events, reports []api.SearchHit
	for _, m := range found.Memories {
		if _, ok := known[m.Project]; ok {
			a := apiMemory(m)
			memories = append(memories, api.SearchHit{
				ID: m.ID, Title: m.Title, Detail: ws.snippet(m.Content), Tag: m.Kind,
				Project: m.Project, At: timePtr(m.UpdatedAt), Memory: &a,
			})
		}
	}
	for _, e := range found.Events {
		if _, ok := known[e.Project]; ok {
			a := apiEvent(e)
			events = append(events, api.SearchHit{
				ID: e.ID, Title: e.Type, Detail: ws.snippet(payloadText(e.Payload)),
				Project: e.Project, Agent: e.Agent, At: timePtr(e.At), Event: &a,
			})
		}
	}
	for _, rp := range found.Reports {
		if _, ok := known[rp.Project]; ok {
			a := apiReport(rp)
			text := strings.Join(slices.Concat([]string{rp.Summary}, rp.Discoveries, rp.Decisions, rp.RemainingIssues), " ")
			reports = append(reports, api.SearchHit{
				ID: rp.ID, Title: cmp.Or(rp.Task, rp.Summary), Detail: ws.snippet(text), Tag: rp.Status,
				Project: rp.Project, Agent: rp.Agent, At: timePtr(rp.CreatedAt), Report: &a,
			})
		}
	}
	var out []api.SearchGroup
	for _, g := range []struct {
		kind string
		hits []api.SearchHit
	}{{api.SearchMemories, memories}, {api.SearchEvents, events}, {api.SearchReports, reports}} {
		if len(g.hits) > 0 {
			out = append(out, api.SearchGroup{Kind: g.kind, Hits: g.hits[:min(limit, len(g.hits))], More: len(g.hits) > limit})
		}
	}
	return out, nil
}

// payloadText is the words of an event's JSON payload, its strings' values
// without the keys and punctuation around them.
func payloadText(payload json.RawMessage) string {
	var v any
	if json.Unmarshal(payload, &v) != nil {
		return string(payload)
	}
	var words []string
	var walk func(any)
	walk = func(v any) {
		switch v := v.(type) {
		case string:
			words = append(words, v)
		case []any:
			for _, x := range v {
				walk(x)
			}
		case map[string]any:
			keys := make([]string, 0, len(v))
			for k := range v {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			for _, k := range keys {
				walk(v[k])
			}
		}
	}
	walk(v)
	return strings.Join(words, " ")
}

func (s *Server) searchMedia(ctx context.Context, known map[string]state.Project, agents []state.Agent, ws searchWords) ([]searchFound, error) {
	items, err := s.store.AllMedia(ctx, nil)
	if err != nil {
		return nil, err
	}
	titles := map[string]string{}
	for _, a := range agents {
		titles[a.Project+"/"+a.Name] = cmp.Or(a.Title, a.Name)
	}
	m := s.manager(nil)
	var out []searchFound
	for _, it := range items {
		if _, ok := known[it.Project]; !ok {
			continue
		}
		score := ws.score(it.Name, it.Text, it.File, it.Kind)
		if score < 0 {
			continue
		}
		item := toAPIMedia(it, m.MediaPath(it))
		item.AgentName = it.Agent
		item.AgentTitle, item.AgentGone = titles[it.Ref()], titles[it.Ref()] == ""
		out = append(out, searchFound{api.SearchHit{
			ID: it.ID, Title: it.Name, Detail: ws.snippet(it.Text), Tag: it.Kind,
			Project: it.Project, Agent: it.Agent, At: timePtr(it.CreatedAt), Media: &item,
		}, score})
	}
	return out, nil
}

func (s *Server) searchSkills(ctx context.Context, ws searchWords) ([]searchFound, error) {
	skills, err := s.store.Skills(ctx)
	if err != nil {
		return nil, err
	}
	var out []searchFound
	for _, sk := range skills {
		if score := ws.score(sk.Name, sk.Description, sk.Source); score >= 0 {
			out = append(out, searchFound{api.SearchHit{
				ID: sk.Name, Title: sk.Name, Detail: ws.snippet(sk.Description), At: timePtr(sk.UpdatedAt),
			}, score})
		}
	}
	return out, nil
}

// searchConnectors finds connectors by name and URL, AgentBox-wide ones and
// each project's and agent's own.
func (s *Server) searchConnectors(ctx context.Context, known map[string]state.Project, ws searchWords) ([]searchFound, error) {
	connectors, err := s.store.AllConnectors(ctx)
	if err != nil {
		return nil, err
	}
	var out []searchFound
	for _, c := range connectors {
		if _, ok := known[c.Project]; c.Project != "" && !ok {
			continue
		}
		if score := ws.score(c.Name, c.URL); score >= 0 {
			out = append(out, searchFound{api.SearchHit{
				ID: c.Name, Title: c.Name, Detail: c.URL, Project: c.Project, Agent: c.Agent,
			}, score})
		}
	}
	return out, nil
}

// searchNotes finds the lines of each project's notes that have every word.
// A line is the unit because the notes are written a line per thing to know.
func (s *Server) searchNotes(projects []state.Project, ws searchWords) []searchFound {
	var out []searchFound
	for _, p := range projects {
		text, err := notes.Read(s.cfg.Paths.ProjectNotes(p.Name))
		if err != nil {
			continue
		}
		for i, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "-*#"))
			if line == "" {
				continue
			}
			if score := ws.score(line); score >= 0 {
				out = append(out, searchFound{api.SearchHit{
					ID: strconv.Itoa(i + 1), Title: ws.snippet(line), Project: p.Name,
				}, score})
			}
		}
	}
	return out
}

// searchPulls finds pull requests among those AgentBox last read from GitHub
// for each project (pullsCache), by title, number, branch and author.
func (s *Server) searchPulls(known map[string]state.Project, ws searchWords) []searchFound {
	var out []searchFound
	for project, prs := range s.pulls.known() {
		if _, ok := known[project]; !ok {
			continue
		}
		for _, pr := range prs {
			number := "#" + strconv.Itoa(pr.Number)
			if score := ws.score(pr.Title, number, strconv.Itoa(pr.Number), pr.HeadBranch, pr.Author); score >= 0 {
				out = append(out, searchFound{api.SearchHit{
					ID: strconv.Itoa(pr.Number), Title: pr.Title, Detail: number, Tag: pr.State,
					Project: project, Agent: pr.Agent, At: pr.UpdatedAt, URL: pr.URL,
				}, score})
			}
		}
	}
	return out
}
