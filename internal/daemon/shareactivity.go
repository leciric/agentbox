package daemon

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// What each paired AgentBox did on this machine, for Settings → Share
// (sharepeers.go): what its requests did that changed something, recorded
// when they succeed (shareActivityOf), the agents made for it, whether it
// asked or its message had the project's chat make one (createAgent), and
// the desktops and terminals it opened (sharepeople.go). And what it spent:
// tokens are read from the ledger (state.SharePeerUsage); the minutes its
// agents run are counted here, a minute at a time (shareMeter).

// recordShareActivity records something a peer did, and tells the app.
func (s *Server) recordShareActivity(ctx context.Context, peer, kind, project, agent, detail string) {
	if peer == "" {
		return
	}
	if _, err := s.store.AddShareActivity(context.WithoutCancel(ctx), state.ShareActivity{Peer: peer, Kind: kind, Project: project, Agent: agent, Detail: detail}); err != nil {
		s.logf("share: recording what %s did: %v", peer, err)
		return
	}
	s.events.publish(api.EventShareActivity, api.ShareActivityEvent{Peer: peer})
}

func (s *Server) sharePeerActivity(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p, err := s.store.SharePeer(ctx, r.PathValue("id"))
	if err != nil {
		return err
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 100
	}
	acts, err := s.store.ShareActivities(ctx, p.ID, limit)
	if err != nil {
		return err
	}
	out := make([]api.ShareActivity, 0, len(acts))
	for _, a := range acts {
		out = append(out, api.ShareActivity{ID: a.ID, At: a.At, Kind: a.Kind, Project: a.Project, Agent: a.Agent, Detail: a.Detail})
	}
	return writeJSON(w, http.StatusOK, out)
}

// shareActivityRoutes are the requests whose success is something a peer
// did, matched against "METHOD /path" once the gateway has named the project
// by its slug: the kind, and which of the match's groups are the project,
// the agent and the detail (0 for none).
var shareActivityRoutes = []struct {
	re                     *regexp.Regexp
	kind                   string
	project, agent, detail int
}{
	{regexp.MustCompile(`^POST /v1/projects/([^/]+)(?:/sessions/([^/]+))?/chat/messages$`), api.ShareDidMessage, 1, 0, 2},
	{regexp.MustCompile(`^POST /v1/agents/([^/]+)/([^/]+)/chat/messages$`), api.ShareDidPrompt, 1, 2, 0},
	{regexp.MustCompile(`^POST /v1/projects/([^/]+)/questions/([^/]+)/answer$`), api.ShareDidAnswer, 1, 0, 2},
	{regexp.MustCompile(`^POST /v1/agents/([^/]+)/([^/]+)/merge$`), api.ShareDidMerge, 1, 2, 0},
	{regexp.MustCompile(`^POST /v1/projects/([^/]+)/pulls/([0-9]+)/merge(?:-when-green)?$`), api.ShareDidMerge, 1, 0, 2},
	{regexp.MustCompile(`^POST /v1/agents/([^/]+)/([^/]+)/(?:start|resume)$`), api.ShareDidStartAgent, 1, 2, 0},
	{regexp.MustCompile(`^POST /v1/agents/([^/]+)/([^/]+)/(?:stop|pause)$`), api.ShareDidStopAgent, 1, 2, 0},
	{regexp.MustCompile(`^DELETE /v1/agents/([^/]+)/([^/]+)$`), api.ShareDidRemoveAgent, 1, 2, 0},
	{regexp.MustCompile(`^POST /v1/agents/([^/]+)/([^/]+)/(?:review|request-changes)$`), api.ShareDidReview, 1, 2, 0},
}

// shareActivity is what a peer's request does, when it succeeds.
type shareActivity struct {
	kind, project, agent, detail string
}

// shareActivityOf says what a request of a peer does that its activity
// shows, if anything.
func shareActivityOf(method, path string) (shareActivity, bool) {
	line := method + " " + strings.TrimSuffix(path, "/")
	for _, r := range shareActivityRoutes {
		m := r.re.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		group := func(i int) string {
			if i == 0 {
				return ""
			}
			v, err := url.PathUnescape(m[i])
			if err != nil {
				return m[i]
			}
			return v
		}
		return shareActivity{kind: r.kind, project: group(r.project), agent: group(r.agent), detail: group(r.detail)}, true
	}
	return shareActivity{}, false
}

// recordShareRequest records what a peer's request did, if it succeeded. An
// answer names the agent whose question it was.
func (s *Server) recordShareRequest(ctx context.Context, p state.SharePeer, a shareActivity, status int) {
	if status < 200 || status >= 300 {
		return
	}
	if a.kind == api.ShareDidAnswer {
		if q, err := s.store.Question(ctx, a.detail); err == nil {
			a.agent, a.detail = q.Agent, ""
		}
	}
	if a.kind == api.ShareDidMessage && a.detail == "" {
		a.detail = state.MigratedSession
	}
	s.recordShareActivity(ctx, p.ID, a.kind, a.project, a.agent, a.detail)
}

// statusWriter remembers the status a handler answered with.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// shareMeterEvery is how often the minutes peers' agents run are counted.
const shareMeterEvery = time.Minute

// shareMeter counts, every shareMeterEvery, the time each agent a peer made
// has run since the last count. A count that comes late (the computer slept)
// adds at most two intervals.
func (s *Server) shareMeter(ctx context.Context) {
	t := time.NewTicker(shareMeterEvery)
	defer t.Stop()
	last := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			elapsed := min(now.Sub(last), 2*shareMeterEvery)
			last = now
			s.meterShareAgents(ctx, elapsed)
		}
	}
}

func (s *Server) meterShareAgents(ctx context.Context, elapsed time.Duration) {
	peers, err := s.store.SharePeers(ctx)
	if err != nil {
		return
	}
	byProject := map[string][]string{}
	for _, p := range peers {
		refs, _ := s.store.SharePeerAgents(ctx, p.ID)
		for _, ref := range refs {
			project, name, _ := strings.Cut(ref, "/")
			byProject[project] = append(byProject[project], name)
		}
	}
	for project, names := range byProject {
		// A machine whose state can't be read isn't counted: unlike the
		// slots, which count it as running not to let a peer past them.
		statuses, err := s.manager(nil).List(ctx, project)
		if err != nil {
			continue
		}
		for _, st := range statuses {
			if !slices.Contains(names, st.Name) || st.State == "Stopped" || st.State == "stopped" {
				continue
			}
			if err := s.store.AddShareRunSeconds(ctx, project, st.Name, int64(elapsed/time.Second)); err != nil {
				s.logf("share: counting %s/%s's time: %v", project, st.Name, err)
			}
		}
	}
}
