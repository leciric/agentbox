// Package hatch reads a project's Hatch artifacts out of its chats: the
// calls its lead and agents made to Hatch's publish_page and update_page,
// the arguments the chat kept of each (api.ChatPage) and what Hatch answered
// (the call's output). It holds no network code: the daemon asks Hatch
// itself, through the project's connector.
package hatch

import (
	"encoding/json"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"agentbox/internal/api"
)

// Host is where Hatch lives: a connector whose server is here is Hatch,
// whatever it is called.
const Host = "hatch.linting.dev"

// DefaultExpiry is how long a page lasts when publish_page doesn't say.
const DefaultExpiry = 24 * time.Hour

// IsHatch reports whether a connector's server URL is Hatch's.
func IsHatch(server string) bool {
	u, err := url.Parse(server)
	return err == nil && strings.EqualFold(u.Hostname(), Host)
}

// The two calls that make an artifact.
const (
	Publish = "publish_page"
	Update  = "update_page"
)

// pageTool finds publish_page or update_page as a whole name at the end of
// a tool's identifier, which each AI tool writes its own way:
// mcp__hatch__publish_page (Claude Code), hatch.publish_page, hatch_update_page.
var pageTool = regexp.MustCompile(`(?:^|[^a-z0-9])(publish_page|update_page)$`)

// Op is which of the two a tool call is, from its name or its title, or ""
// for any other call.
func Op(name, title string) string {
	for _, s := range []string{name, title} {
		if m := pageTool.FindStringSubmatch(strings.ToLower(strings.TrimSpace(s))); m != nil {
			return m[1]
		}
	}
	return ""
}

// Args is what a chat keeps of a call's arguments, raw as the AI tool sent
// them, leaving the HTML out; nil when they don't parse, which they don't
// while an AI tool is still streaming them.
func Args(raw json.RawMessage) *api.ChatPage {
	var in struct {
		ID      string          `json:"id"`
		Title   string          `json:"title"`
		Public  bool            `json:"public"`
		Expires json.RawMessage `json:"expires_in_hours"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &in) != nil {
		return nil
	}
	p := &api.ChatPage{ID: in.ID, Title: in.Title, Public: in.Public}
	switch e := strings.TrimSpace(string(in.Expires)); {
	case e == "null":
		p.ExpiresInHours = -1
	case e != "":
		if n, err := strconv.Atoi(e); err == nil && n > 0 {
			p.ExpiresInHours = n
		}
	}
	return p
}

// Call is one tool call of a project's chats, with where it was made.
type Call struct {
	Agent string // the chat's agent's ref
	Item  string
	At    time.Time
	Tool  api.ChatTool
}

// What Hatch answers a publish or an update with: a line saying what it did,
// then the same as JSON. The link is the page's; an update's JSON has the
// version's (?v=2).
var (
	pageLink   = regexp.MustCompile(`https?://[^\s"'<>()\\]+/p/([A-Za-z0-9_-]+)`)
	versionKey = regexp.MustCompile(`"version"\s*:\s*(\d+)`)
	versionWas = regexp.MustCompile(`(?i)\bversion (\d+)\b`)
	published  = regexp.MustCompile(`Published "(.*)" \(id `)
)

// Collect makes calls into artifacts, newest first: every successful
// publish_page and update_page whose answer links to a page on host, one
// artifact per page. calls are in the order they were made.
func Collect(calls []Call, host string, now time.Time) []api.Artifact {
	byID := map[string]*api.Artifact{}
	var order []string
	for _, c := range calls {
		op := Op(c.Tool.Name, c.Tool.Title)
		if op == "" || c.Tool.Status != "completed" {
			continue
		}
		args := c.Tool.Page
		if args == nil {
			args = &api.ChatPage{}
		}
		id, link := pageOf(c.Tool.Output, host)
		if id == "" || (args.ID != "" && args.ID != id) {
			continue
		}
		version := 0
		if m := versionKey.FindStringSubmatch(c.Tool.Output); m != nil {
			version, _ = strconv.Atoi(m[1])
		} else if m := versionWas.FindStringSubmatch(c.Tool.Output); m != nil {
			version, _ = strconv.Atoi(m[1])
		}
		a := byID[id]
		if a == nil {
			a = &api.Artifact{ID: id, URL: link, Agent: c.Agent, Item: c.Item, CreatedAt: c.At}
			byID[id] = a
			order = append(order, id)
		}
		if !slices.Contains(a.Agents, c.Agent) {
			a.Agents = append(a.Agents, c.Agent)
		}
		a.Version = max(a.Version, version, 1)
		if c.At.After(a.UpdatedAt) {
			a.UpdatedAt = c.At
		}
		if op != Publish {
			continue
		}
		// The publish is where the artifact was made, even when an update
		// of it was read first.
		if a.Agent != c.Agent || a.Item != c.Item {
			a.Agent, a.Item, a.CreatedAt = c.Agent, c.Item, c.At
			a.Agents = append([]string{c.Agent}, slices.DeleteFunc(a.Agents, func(r string) bool { return r == c.Agent })...)
		}
		a.Title = args.Title
		if a.Title == "" {
			if m := published.FindStringSubmatch(c.Tool.Output); m != nil {
				a.Title = m[1]
			}
		}
		a.Public = args.Public
		switch h := args.ExpiresInHours; {
		case h < 0:
			a.Permanent, a.ExpiresAt = true, nil
		default:
			d := DefaultExpiry
			if h > 0 {
				d = time.Duration(h) * time.Hour
			}
			at := c.At.Add(d)
			a.Permanent, a.ExpiresAt = false, &at
		}
	}
	out := make([]api.Artifact, 0, len(order))
	for _, id := range order {
		a := byID[id]
		if a.Title == "" {
			a.Title = id
		}
		a.Expired = a.ExpiresAt != nil && !now.Before(*a.ExpiresAt)
		out = append(out, *a)
	}
	slices.SortStableFunc(out, func(a, b api.Artifact) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	return out
}

// pageOf finds the page an answer links to on host: its id, and its link
// without a version.
func pageOf(output, host string) (id, link string) {
	for _, m := range pageLink.FindAllStringSubmatchIndex(output, -1) {
		raw := output[m[0]:m[1]]
		u, err := url.Parse(raw)
		if err != nil || !strings.EqualFold(u.Hostname(), host) {
			continue
		}
		return output[m[2]:m[3]], raw
	}
	return "", ""
}
