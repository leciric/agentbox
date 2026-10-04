package machinesweb

import (
	"cmp"
	"mime"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"agentbox/internal/machinesmedia"
)

// Sources an item can come from.
const (
	SourceMachines = "machines" // the central store, machinesmedia
	SourceAgentBox = "agentbox" // an AgentBox agent's media, through the daemon
)

// Item is one screenshot or recording as the page sees it, from either
// source. ID is unique across both: the source's own id behind a prefix.
type Item struct {
	ID       string    `json:"id"`
	Source   string    `json:"source"`
	Kind     string    `json:"kind"`
	Created  time.Time `json:"created"`
	Repo     string    `json:"repo"`
	Branch   string    `json:"branch"`
	Worktree string    `json:"worktree,omitempty"`
	// Session groups a session's (or an agent's) items under its branch;
	// SessionLabel is how the page names it.
	Session      string `json:"session"`
	SessionLabel string `json:"sessionLabel,omitempty"`
	Tool         string `json:"tool,omitempty"`
	Caption      string `json:"caption,omitempty"`
	Width        int    `json:"width,omitempty"`
	Height       int    `json:"height,omitempty"`
	DurationMs   int64  `json:"durationMs,omitempty"`
	Bytes        int64  `json:"bytes"`
	Mime         string `json:"mime,omitempty"`
	// Path is where the file is on this machine, for the page's Copy path;
	// empty when it isn't reachable here.
	Path string `json:"path,omitempty"`
}

const localPrefix, agentPrefix = "m-", "a-"

func fromEntry(e machinesmedia.Entry) Item {
	label := e.Session
	if e.Tool != "" && e.Session != "" {
		label = e.Tool + " · " + shorten(e.Session, 12)
	}
	return Item{
		ID: localPrefix + e.ID, Source: SourceMachines, Kind: e.Kind, Created: e.Created,
		Repo: e.Repo, Branch: e.Branch, Worktree: e.Worktree, Session: e.Session, SessionLabel: label,
		Tool: e.Tool, Caption: e.Caption, Width: e.Width, Height: e.Height, DurationMs: e.DurationMs,
		Bytes: e.Bytes, Mime: mimeOf(e.Path, e.Kind), Path: e.Path,
	}
}

// named gives an item without a repository, branch or session a name for
// its place in the tree, so that place can be filtered by too.
func (it Item) named() Item {
	it.Repo = cmp.Or(it.Repo, "(no repository)")
	it.Branch = cmp.Or(it.Branch, "(no branch)")
	it.Session = cmp.Or(it.Session, "(no session)")
	return it
}

func shorten(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func mimeOf(path, kind string) string {
	if t := mime.TypeByExtension(strings.ToLower(filepath.Ext(path))); t != "" {
		return t
	}
	if kind == machinesmedia.Recording {
		return "video/mp4"
	}
	return "image/png"
}

// Query is what the page asks for: filters, a grouping and a page.
type Query struct {
	Text    string // in the caption, branch, repo or session
	Kind    string
	Source  string
	Repo    string
	Branch  string
	Session string
	From    time.Time // inclusive
	To      time.Time // exclusive
	Group   string    // "", "day", "repo", "branch" or "session"
	Offset  int
	Limit   int
}

// Page is a page of items, Total being how many match.
type Page struct {
	Items  []Item `json:"items"`
	Total  int    `json:"total"`
	Offset int    `json:"offset"`
}

// matches reports whether it passes every filter; tree false leaves out the
// repo, branch and session ones, which Facets counts within.
func (q Query) matches(it Item, tree bool) bool {
	if q.Kind != "" && it.Kind != q.Kind || q.Source != "" && it.Source != q.Source {
		return false
	}
	if !q.From.IsZero() && it.Created.Before(q.From) || !q.To.IsZero() && !it.Created.Before(q.To) {
		return false
	}
	if tree {
		if q.Repo != "" && it.Repo != q.Repo || q.Branch != "" && it.Branch != q.Branch || q.Session != "" && it.Session != q.Session {
			return false
		}
	}
	if q.Text != "" {
		hay := strings.ToLower(strings.Join([]string{it.Caption, it.Branch, it.Repo, it.SessionLabel, it.Session}, "\n"))
		for _, w := range strings.Fields(strings.ToLower(q.Text)) {
			if !strings.Contains(hay, w) {
				return false
			}
		}
	}
	return true
}

func (q Query) groupKey(it Item) string {
	switch q.Group {
	case "repo":
		return it.Repo
	case "branch":
		return it.Repo + "\x00" + it.Branch
	case "session":
		return it.Repo + "\x00" + it.Branch + "\x00" + it.Session
	}
	return it.Created.Local().Format(time.DateOnly)
}

// Run filters, groups and pages items, which are newest first. Grouped by
// anything but day, a group's items are together, groups ordered by their
// newest item.
func (q Query) Run(items []Item) Page {
	out := []Item{} // [] rather than null to the page when nothing matches
	for _, it := range items {
		if q.matches(it, true) {
			out = append(out, it)
		}
	}
	if q.Group == "repo" || q.Group == "branch" || q.Group == "session" {
		newest := map[string]time.Time{}
		for _, it := range out {
			if k := q.groupKey(it); it.Created.After(newest[k]) {
				newest[k] = it.Created
			}
		}
		slices.SortStableFunc(out, func(a, b Item) int {
			ka, kb := q.groupKey(a), q.groupKey(b)
			if ka == kb {
				return 0 // already newest first
			}
			return cmp.Or(newest[kb].Compare(newest[ka]), strings.Compare(ka, kb))
		})
	}
	total := len(out)
	start := min(max(q.Offset, 0), total)
	end := total
	if q.Limit > 0 {
		end = min(start+q.Limit, total)
	}
	return Page{Items: slices.Clip(out[start:end]), Total: total, Offset: start}
}

// Facets is the tree the page's sidebar groups and filters by — repository,
// then branch, then session — with counts, and the counts by kind, all within
// the filters other than the tree's own.
type Facets struct {
	Total   int            `json:"total"`
	Kinds   map[string]int `json:"kinds"`
	Sources map[string]int `json:"sources"`
	Repos   []RepoFacet    `json:"repos"`
}

type RepoFacet struct {
	Repo     string        `json:"repo"`
	Count    int           `json:"count"`
	Branches []BranchFacet `json:"branches"`
}

type BranchFacet struct {
	Branch   string         `json:"branch"`
	Worktree string         `json:"worktree,omitempty"`
	Count    int            `json:"count"`
	Sessions []SessionFacet `json:"sessions"`
}

type SessionFacet struct {
	Session string    `json:"session"`
	Label   string    `json:"label,omitempty"`
	Count   int       `json:"count"`
	Newest  time.Time `json:"newest"`
}

// Facets counts items within q's filters other than repo, branch and session.
// Repositories and branches are ordered by name, sessions newest first.
func (q Query) Facets(items []Item) Facets {
	type branch struct {
		BranchFacet
		sessions map[string]*SessionFacet
	}
	type repo struct {
		RepoFacet
		branches map[string]*branch
	}
	f := Facets{Kinds: map[string]int{}, Sources: map[string]int{}, Repos: []RepoFacet{}}
	repos := map[string]*repo{}
	for _, it := range items {
		if !q.matches(it, false) {
			continue
		}
		f.Total++
		f.Kinds[it.Kind]++
		f.Sources[it.Source]++
		r := repos[it.Repo]
		if r == nil {
			r = &repo{RepoFacet{Repo: it.Repo}, map[string]*branch{}}
			repos[it.Repo] = r
		}
		r.Count++
		b := r.branches[it.Branch]
		if b == nil {
			b = &branch{BranchFacet{Branch: it.Branch, Worktree: it.Worktree}, map[string]*SessionFacet{}}
			r.branches[it.Branch] = b
		}
		b.Count++
		s := b.sessions[it.Session]
		if s == nil {
			s = &SessionFacet{Session: it.Session, Label: it.SessionLabel}
			b.sessions[it.Session] = s
		}
		s.Count++
		if it.Created.After(s.Newest) {
			s.Newest = it.Created
		}
	}
	for _, r := range repos {
		for _, b := range r.branches {
			for _, s := range b.sessions {
				b.Sessions = append(b.Sessions, *s)
			}
			slices.SortFunc(b.Sessions, func(x, y SessionFacet) int {
				return cmp.Or(y.Newest.Compare(x.Newest), strings.Compare(x.Session, y.Session))
			})
			r.Branches = append(r.Branches, b.BranchFacet)
		}
		slices.SortFunc(r.Branches, func(x, y BranchFacet) int { return strings.Compare(x.Branch, y.Branch) })
		f.Repos = append(f.Repos, r.RepoFacet)
	}
	slices.SortFunc(f.Repos, func(x, y RepoFacet) int { return strings.Compare(x.Repo, y.Repo) })
	return f
}

func atoi(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}
