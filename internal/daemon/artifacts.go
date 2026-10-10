package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/hatch"
	"agentbox/internal/state"
)

// A project's Hatch artifacts (api.Artifacts): read out of its chats, and
// shown with the HTML the daemon fetches through the project's Hatch
// connector. Only a page one of the project's chats published or updated can
// be previewed: list_pages isn't asked, since it has every page of the Hatch
// account, whichever project made it.

// How long the daemon waits on Hatch, and the most HTML it takes: Hatch's
// Pro plan's limit.
const (
	hatchTimeout = 30 * time.Second
	maxPageBytes = 25 << 20
)

// pageLinkFor is how long a content_url get_page answered with is used
// again: it lasts 15 minutes.
const pageLinkFor = 10 * time.Minute

// pageLinks keeps the content_url of each artifact last previewed, by
// project and id, so opening one asks Hatch once for its details and once
// for its HTML rather than twice for the first.
type pageLinks struct {
	mu    sync.Mutex
	links map[string]pageLink
}

type pageLink struct {
	url string
	at  time.Time
}

func (p *pageLinks) get(key string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if l, ok := p.links[key]; ok && time.Since(l.at) < pageLinkFor {
		return l.url
	}
	return ""
}

func (p *pageLinks) set(key, link string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.links == nil {
		p.links = map[string]pageLink{}
	}
	for k, l := range p.links {
		if time.Since(l.at) >= pageLinkFor {
			delete(p.links, k)
		}
	}
	if link == "" {
		delete(p.links, key)
		return
	}
	p.links[key] = pageLink{url: link, at: time.Now()}
}

// hatchConnector is the Hatch connector a project's agents get, its own or
// an AgentBox-wide one it doesn't turn off, when it is on and signed in.
func (s *Server) hatchConnector(ctx context.Context, project string) (state.Connector, bool, error) {
	found, err := s.store.ProjectConnectors(ctx, project)
	if err != nil {
		return state.Connector{}, false, err
	}
	for _, c := range found {
		if !c.Enabled || !s.isHatch(c.URL) {
			continue
		}
		if status, _ := s.connectors.Status(ctx, c, ""); status == api.ConnectorConnected {
			return c, true, nil
		}
	}
	return state.Connector{}, false, nil
}

// isHatch is whether a connector's server is Hatch: hatch.IsHatch, or in a
// test on hatchHost.
func (s *Server) isHatch(server string) bool {
	if s.hatchHost == "" {
		return hatch.IsHatch(server)
	}
	u, err := url.Parse(server)
	return err == nil && u.Host == s.hatchHost
}

// projectArtifacts reads a project's artifacts out of its chats.
func (s *Server) projectArtifacts(ctx context.Context, project string, c state.Connector) ([]api.Artifact, error) {
	items, err := s.store.ToolCallsNamed(ctx, project, hatch.Publish, hatch.Update)
	if err != nil {
		return nil, err
	}
	calls := make([]hatch.Call, 0, len(items))
	for _, it := range items {
		var item api.ChatItem
		if json.Unmarshal(it.Data, &item) != nil || item.Tool == nil {
			continue
		}
		calls = append(calls, hatch.Call{Agent: project + "/" + it.Agent, Item: item.ID, At: item.CreatedAt, Tool: *item.Tool})
	}
	slices.SortStableFunc(calls, func(a, b hatch.Call) int { return a.At.Compare(b.At) })
	host := hatch.Host
	if u, err := url.Parse(c.URL); err == nil {
		host = u.Hostname()
	}
	return hatch.Collect(calls, host, time.Now()), nil
}

// listArtifacts is GET /v1/projects/{project}/artifacts[?agent=<name>].
func (s *Server) listArtifacts(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p, err := s.store.Project(ctx, r.PathValue("project"))
	if err != nil {
		return err
	}
	out := api.Artifacts{Artifacts: []api.Artifact{}}
	c, ok, err := s.hatchConnector(ctx, p.Name)
	if err != nil {
		return err
	}
	if !ok {
		return writeJSON(w, http.StatusOK, out)
	}
	out.Connector = c.Name
	list, err := s.projectArtifacts(ctx, p.Name, c)
	if err != nil {
		return err
	}
	if agent := r.URL.Query().Get("agent"); agent != "" {
		ref := p.Name + "/" + agent
		list = slices.DeleteFunc(list, func(a api.Artifact) bool { return !slices.Contains(a.Agents, ref) })
	}
	if list != nil {
		out.Artifacts = list
	}
	return writeJSON(w, http.StatusOK, out)
}

// artifactOf is one of a project's artifacts, with the connector to ask
// Hatch about it with: not found when none of the project's chats made it,
// or Hatch isn't connected.
func (s *Server) artifactOf(r *http.Request) (string, api.Artifact, state.Connector, error) {
	ctx := r.Context()
	p, err := s.store.Project(ctx, r.PathValue("project"))
	if err != nil {
		return "", api.Artifact{}, state.Connector{}, err
	}
	c, ok, err := s.hatchConnector(ctx, p.Name)
	if err != nil {
		return "", api.Artifact{}, state.Connector{}, err
	}
	if !ok {
		return "", api.Artifact{}, state.Connector{}, fmt.Errorf("%s has no Hatch connector signed in: %w", p.Name, state.ErrNotFound)
	}
	list, err := s.projectArtifacts(ctx, p.Name, c)
	if err != nil {
		return "", api.Artifact{}, state.Connector{}, err
	}
	id := r.PathValue("id")
	for _, a := range list {
		if a.ID == id {
			return p.Name, a, c, nil
		}
	}
	return "", api.Artifact{}, state.Connector{}, fmt.Errorf("no chat of %s published the page %q: %w", p.Name, id, state.ErrNotFound)
}

// hatchPage is what get_page answers with: the page's details, and since
// leciric/hatch#3 a short-lived link to its HTML.
type hatchPage struct {
	Artifact struct {
		Title          string  `json:"title"`
		Visibility     string  `json:"visibility"`
		CurrentVersion int     `json:"current_version"`
		ExpiresAt      *string `json:"expires_at"`
		URL            string  `json:"url"`
		Status         string  `json:"status"`
	} `json:"artifact"`
	ContentURL string `json:"content_url"`
}

// getPage asks Hatch about one of its pages. gone is Hatch saying it has no
// such page: it expired, or was deleted.
func (s *Server) getPage(ctx context.Context, c state.Connector, id string) (page hatchPage, gone bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, hatchTimeout)
	defer cancel()
	res, err := s.connectors.CallTool(ctx, c, "", "get_page", map[string]string{"id": id})
	if err != nil {
		return page, false, err
	}
	text := res.Text()
	if res.IsError {
		// Hatch's errors read "Error (<code>): <message>".
		return page, strings.Contains(text, "(not_found)"), errors.New(strings.TrimSpace(text))
	}
	raw := res.StructuredContent
	if len(raw) == 0 {
		// Without structured content, the JSON is the last text block.
		if i := strings.Index(text, "{"); i >= 0 {
			raw = json.RawMessage(text[i:])
		}
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		return page, false, fmt.Errorf("get_page answered with something else: %w", err)
	}
	return page, false, nil
}

// previewArtifact is GET /v1/projects/{project}/artifacts/{id}.
func (s *Server) previewArtifact(w http.ResponseWriter, r *http.Request) error {
	project, a, c, err := s.artifactOf(r)
	if err != nil {
		return err
	}
	out := api.ArtifactPreview{Artifact: a, Status: api.ArtifactActive}
	page, gone, err := s.getPage(r.Context(), c, a.ID)
	switch {
	case gone:
		out.Status, out.Error = api.ArtifactExpired, "Hatch no longer has this page: it expired, or was deleted."
		out.Artifact.Expired = true
		if a.ExpiresAt == nil || time.Now().Before(*a.ExpiresAt) {
			now := time.Now()
			out.Artifact.ExpiresAt, out.Artifact.Permanent = &now, false
		}
		s.pageLinks.set(project+"/"+a.ID, "")
		return writeJSON(w, http.StatusOK, out)
	case err != nil:
		out.Status, out.Error = api.ArtifactUnavailable, fmt.Sprintf("Couldn't ask Hatch about this page: %v", err)
		return writeJSON(w, http.StatusOK, out)
	}
	h := page.Artifact
	if h.Title != "" {
		out.Artifact.Title = h.Title
	}
	if h.URL != "" {
		out.Artifact.URL = h.URL
	}
	out.Artifact.Public = h.Visibility == "public"
	out.Artifact.Version = max(out.Artifact.Version, h.CurrentVersion)
	out.Artifact.ExpiresAt, out.Artifact.Permanent = nil, h.ExpiresAt == nil
	if h.ExpiresAt != nil {
		if at, err := time.Parse(time.RFC3339, *h.ExpiresAt); err == nil {
			out.Artifact.ExpiresAt = &at
		}
	}
	out.Artifact.Expired = out.Artifact.ExpiresAt != nil && !time.Now().Before(*out.Artifact.ExpiresAt)
	switch {
	case h.Status == "removed":
		out.Status, out.Error = api.ArtifactExpired, "Hatch took this page down."
	case out.Artifact.Expired:
		out.Status = api.ArtifactExpired
	case page.ContentURL == "":
		out.Error = "This version of Hatch doesn't hand its pages' HTML to AgentBox yet."
	default:
		out.Page = true
	}
	link := page.ContentURL
	if !out.Page {
		link = ""
	}
	s.pageLinks.set(project+"/"+a.ID, link)
	return writeJSON(w, http.StatusOK, out)
}

// artifactPage is GET /v1/projects/{project}/artifacts/{id}/page: the
// artifact's HTML, for the app's sandboxed frame. It goes out with Hatch's
// own Content-Security-Policy (its sandbox gives the page an opaque origin;
// it can't phone home), without the frame-ancestors that would keep it out
// of the app.
func (s *Server) artifactPage(w http.ResponseWriter, r *http.Request) error {
	project, a, c, err := s.artifactOf(r)
	if err != nil {
		return err
	}
	key := project + "/" + a.ID
	link := s.pageLinks.get(key)
	if link == "" {
		page, _, err := s.getPage(r.Context(), c, a.ID)
		if err != nil {
			return err
		}
		if page.ContentURL == "" {
			return fmt.Errorf("this version of Hatch doesn't hand its pages' HTML to AgentBox yet: %w", state.ErrNotFound)
		}
		link = page.ContentURL
		s.pageLinks.set(key, link)
	}
	ctx, cancel := context.WithTimeout(r.Context(), hatchTimeout)
	defer cancel()
	body, header, err := s.connectors.Fetch(ctx, link, maxPageBytes)
	if err != nil {
		s.pageLinks.set(key, "")
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", framedPolicy(header.Get("Content-Security-Policy")))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, err = w.Write(body)
	return err
}

// defaultPagePolicy is Hatch's policy for its pages, for an answer that
// came without one: inline scripts and styles and the CDNs Hatch allows, no
// fetching, and a sandbox.
const defaultPagePolicy = "default-src 'none'; script-src 'unsafe-inline' 'unsafe-eval' https:; style-src 'unsafe-inline' https:; " +
	"font-src data: https:; img-src data: blob: https:; media-src data: blob: https:; connect-src 'none'; form-action 'none'; " +
	"base-uri 'none'; object-src 'none'; sandbox allow-scripts allow-popups allow-popups-to-escape-sandbox allow-modals"

// framedPolicy is a page's Content-Security-Policy as the app frames it:
// Hatch's, without frame-ancestors (which names Hatch's own site, not the
// app), and always with a sandbox.
func framedPolicy(csp string) string {
	if strings.TrimSpace(csp) == "" {
		return defaultPagePolicy
	}
	var kept []string
	sandboxed := false
	for _, d := range strings.Split(csp, ";") {
		d = strings.TrimSpace(d)
		name, _, _ := strings.Cut(strings.ToLower(d), " ")
		switch name {
		case "", "frame-ancestors":
			continue
		case "sandbox":
			sandboxed = true
		}
		kept = append(kept, d)
	}
	if !sandboxed {
		kept = append(kept, "sandbox allow-scripts allow-popups allow-popups-to-escape-sandbox allow-modals")
	}
	return strings.Join(kept, "; ")
}
