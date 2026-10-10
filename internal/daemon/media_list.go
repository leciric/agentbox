package daemon

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// The Media views (the app's agent, project and all-projects galleries) read
// a page at a time: GET /v1/media, /v1/projects/{project}/media and an
// agent's media take ?limit= and ?cursor=, with their filters and search
// applied here, so every page is right however far down it is. Their filter
// counts come from .../media/counts, which counts every item rather than the
// pages loaded. Without ?limit= the same routes answer every item at once,
// which is what the CLI and the agents' own API read.

// mediaPageMax is the most items a page holds, whatever ?limit= asks.
const mediaPageMax = 200

// mediaList is what a request for a Media list asks for.
type mediaList struct {
	filter state.MediaFilter // what the database narrows
	kinds  []string          // ?kind=, before ?only= narrows it
	only   string
	terms  []string // the search, folded
	limit  int      // 0: every item, as a plain list
	cursor string
	// all is GET /v1/media: items of a project that's gone are left out, as
	// that project can't be opened.
	all bool
}

// parseMediaList reads a list's filters from its URL. base is what the route
// pins: a project, or a project and an agent.
func parseMediaList(r *http.Request, base state.MediaFilter) (mediaList, error) {
	q := r.URL.Query()
	l := mediaList{filter: base, all: base.Project == "", cursor: q.Get("cursor"), only: q.Get("only")}
	if l.all {
		l.filter.Project = q.Get("project")
	}
	if base.Agent == "" && l.filter.Project != "" {
		l.filter.Agent = q.Get("agent")
	}
	for k := range strings.SplitSeq(q.Get("kind"), ",") {
		if k = strings.TrimSpace(k); k != "" {
			l.kinds = append(l.kinds, k)
		}
	}
	l.filter.Kinds = l.kinds
	if l.only != "" {
		if len(l.kinds) > 0 && !slices.Contains(l.kinds, l.only) {
			l.filter.Kinds = []string{""} // no item has no kind: nothing
		} else {
			l.filter.Kinds = []string{l.only}
		}
	}
	l.filter.Favorite = q.Get("favorite") == "1" || q.Get("favorite") == "true"
	l.filter.Unseen = q.Get("unseen") == "1" || q.Get("unseen") == "true"
	l.terms = mediaSearchTerms(q.Get("q"))
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return l, errors.New("limit must be a positive number")
		}
		l.limit = min(n, mediaPageMax)
	}
	return l, nil
}

// mediaLabeller turns stored items into the API's, labelled with their
// agent, when they expire and whether they're unseen, looking each project's
// agents up once.
type mediaLabeller struct {
	s        *Server
	ctx      context.Context
	withPath bool // on the host API, where an item's path on disk is shown
	period   time.Duration
	forever  bool
	unseen   map[string]bool
	projects map[string]bool // nil: every project is known
	titles   map[string]map[string]string
}

func (s *Server) mediaLabeller(ctx context.Context, withPath, all bool) (*mediaLabeller, error) {
	retention, err := s.store.MediaRetention(ctx)
	if err != nil {
		return nil, err
	}
	l := &mediaLabeller{s: s, ctx: ctx, withPath: withPath, titles: map[string]map[string]string{}}
	l.period, l.forever, _ = state.MediaRetentionPeriod(retention)
	if l.unseen, err = s.store.UnseenMedia(ctx); err != nil {
		return nil, err
	}
	if all {
		projects, err := s.store.Projects(ctx)
		if err != nil {
			return nil, err
		}
		l.projects = map[string]bool{}
		for _, p := range projects {
			l.projects[p.Name] = true
		}
	}
	return l, nil
}

// known is whether an item's project still exists.
func (l *mediaLabeller) known(project string) bool {
	return l.projects == nil || l.projects[project]
}

// agent is an agent's title, and whether it's gone.
func (l *mediaLabeller) agent(project, name string) (string, bool, error) {
	titles, ok := l.titles[project]
	if !ok {
		var err error
		if titles, err = l.s.agentTitles(l.ctx, project); err != nil {
			return "", false, err
		}
		l.titles[project] = titles
	}
	title, ok := titles[name]
	return title, !ok, nil
}

func (l *mediaLabeller) label(it state.Media) (api.MediaItem, error) {
	path := ""
	if l.withPath {
		path = l.s.manager(nil).MediaPath(it)
	}
	item := toAPIMedia(it, path)
	item.AgentName = it.Agent
	title, gone, err := l.agent(it.Project, it.Agent)
	if err != nil {
		return item, err
	}
	// A gone agent's item was kept, not deleted, when it was destroyed.
	item.AgentTitle, item.AgentGone = title, gone
	if !it.OrphanedAt.IsZero() && !l.forever && !it.Favorite {
		// Matches Store.ExpiredMedia's own arithmetic, so what's shown here
		// is exactly when the sweeper will remove the item.
		expires := it.OrphanedAt.Add(l.period)
		item.ExpiresAt = &expires
	}
	item.Unseen = l.unseen[it.ID]
	return item, nil
}

// eachMedia calls fn with every item the list keeps, labelled, newest first
// from its cursor, until fn returns false.
func (s *Server) eachMedia(ctx context.Context, l mediaList, labels *mediaLabeller, fn func(api.MediaItem) bool) error {
	var failed error
	err := s.store.EachMedia(ctx, l.filter, l.cursor, func(it state.Media) bool {
		if !labels.known(it.Project) {
			return true
		}
		item, err := labels.label(it)
		if err != nil {
			failed = err
			return false
		}
		if !mediaMatches(item, l.terms) {
			return true
		}
		return fn(item)
	})
	if failed != nil {
		return failed
	}
	return err
}

// writeMediaList answers a Media list: a page with ?limit=, every item
// without.
func (s *Server) writeMediaList(w http.ResponseWriter, r *http.Request, base state.MediaFilter, withPath bool) error {
	ctx := r.Context()
	l, err := parseMediaList(r, base)
	if err != nil {
		return err
	}
	labels, err := s.mediaLabeller(ctx, withPath, l.all)
	if err != nil {
		return err
	}
	items := []api.MediaItem{}
	more := false
	err = s.eachMedia(ctx, l, labels, func(item api.MediaItem) bool {
		if l.limit > 0 && len(items) == l.limit {
			more = true
			return false
		}
		items = append(items, item)
		return true
	})
	if err != nil {
		return err
	}
	if l.limit == 0 {
		return writeJSON(w, http.StatusOK, items)
	}
	page := api.MediaPage{Items: items}
	if more {
		last := items[len(items)-1]
		page.Next = state.MediaCursor(state.Media{ID: last.ID, CreatedAt: last.CreatedAt})
	}
	return writeJSON(w, http.StatusOK, page)
}

// writeMediaCounts answers .../media/counts, for a list with the same URL.
func (s *Server) writeMediaCounts(w http.ResponseWriter, r *http.Request, base state.MediaFilter) error {
	ctx := r.Context()
	l, err := parseMediaList(r, base)
	if err != nil {
		return err
	}
	labels, err := s.mediaLabeller(ctx, false, l.all)
	if err != nil {
		return err
	}
	// One tally of everything the projects' filter could show, by project,
	// agent and kind; each count is a sum of some of it.
	scope := state.MediaFilter{Kinds: l.kinds}
	if !l.all {
		scope.Project = base.Project
	}
	if base.Agent != "" {
		scope.Agent = base.Agent
	}
	tallies, err := s.store.MediaTallies(ctx, scope)
	if err != nil {
		return err
	}
	out := api.MediaCounts{Kinds: map[string]int{}, Agents: []api.MediaAgentCount{}, Projects: []api.MediaProjectCount{}}
	projects := map[string]int{}
	agents := map[string]int{}
	for _, t := range tallies {
		if !labels.known(t.Project) {
			continue
		}
		if i, ok := projects[t.Project]; ok {
			out.Projects[i].Count += t.Count
		} else {
			projects[t.Project] = len(out.Projects)
			out.Projects = append(out.Projects, api.MediaProjectCount{Project: t.Project, Count: t.Count})
		}
		if l.filter.Project != "" && t.Project != l.filter.Project {
			continue
		}
		ref := t.Project + "/" + t.Agent
		if i, ok := agents[ref]; ok {
			out.Agents[i].Count += t.Count
			out.Agents[i].Bytes += t.Bytes
		} else {
			title, gone, err := labels.agent(t.Project, t.Agent)
			if err != nil {
				return err
			}
			agents[ref] = len(out.Agents)
			out.Agents = append(out.Agents, api.MediaAgentCount{Agent: ref, Name: t.Agent, Title: title, Gone: gone, Count: t.Count, Bytes: t.Bytes})
		}
		if l.filter.Agent != "" && t.Agent != l.filter.Agent {
			continue
		}
		out.Total += t.Count
		out.Bytes += t.Bytes
		out.Kinds[t.Kind] += t.Count
		out.Favorites += t.Favorites
		out.Unseen += t.Unseen
		if !l.filter.Favorite && !l.filter.Unseen && len(l.terms) == 0 && (l.only == "" || l.only == t.Kind) {
			out.Matching += t.Count
			out.MatchingBytes += t.Bytes
		}
	}
	if l.filter.Favorite || l.filter.Unseen || len(l.terms) > 0 {
		// Only reading the items can tell what these keep.
		l.cursor = ""
		err := s.eachMedia(ctx, l, labels, func(item api.MediaItem) bool {
			out.Matching++
			out.MatchingBytes += item.Size
			return true
		})
		if err != nil {
			return err
		}
	}
	return writeJSON(w, http.StatusOK, out)
}

// mediaKindWords are the other words a search finds each kind by, beyond its
// own name: "image" finds the screenshots, "video" the recordings. Mirrors
// kindWords in desktop/src/renderer/lib/media.ts, which also adds each kind's
// label in the app's language.
var mediaKindWords = map[string]string{
	"screenshot": "screenshots image picture",
	"recording":  "recordings video screencast",
	"report":     "reports test",
	"log":        "logs",
	"note":       "notes text",
	"file":       "files",
}

var mediaTermPattern = regexp.MustCompile(`"([^"]*)"|(\S+)`)

// mediaSearchTerms splits a search into what every match has to contain: its
// words, or a "quoted phrase" kept whole, folded.
func mediaSearchTerms(q string) []string {
	var terms []string
	for _, m := range mediaTermPattern.FindAllStringSubmatch(foldMedia(q), -1) {
		term := m[2]
		if strings.HasPrefix(m[0], `"`) {
			term = m[1]
		}
		if term = strings.TrimSpace(term); term != "" {
			terms = append(terms, term)
		}
	}
	return terms
}

// foldMedia is how a search and what it searches are compared: case and
// accents ignored, so "resume" finds "Résumé".
func foldMedia(s string) string {
	return strings.ToLower(strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Mn, r) {
			return -1
		}
		return r
	}, norm.NFD.String(s)))
}

// mediaMatches is whether an item has every term of a search in its name, file,
// kind, type, agent, page URL or, for a note, its text: the same fields as the
// app's own search (searchMedia in lib/media.ts). A log's or a report's text
// is a file, so they're found by name and type.
func mediaMatches(item api.MediaItem, terms []string) bool {
	if len(terms) == 0 {
		return true
	}
	fields := []string{item.Name, item.File, item.Kind, mediaKindWords[item.Kind], item.Mime, item.Meta.URL, item.AgentName, item.AgentTitle}
	if item.Kind == "note" {
		fields = append(fields, item.Text)
	}
	text := foldMedia(strings.Join(fields, "\n"))
	for _, term := range terms {
		if !strings.Contains(text, term) {
			return false
		}
	}
	return true
}
