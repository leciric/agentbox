package daemon

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
)

// tailLines is how many lines of a terminal a row's thumbnail gets, unless
// it asks for more (its hover preview does), up to tailMost, and tailFresh
// how long a tail read answers again: the agent list polls every
// two seconds for the rows on screen, and two windows of the app (or the
// phone) asking for the same agent share one exec.
const (
	tailLines = 4
	tailMost  = 40
	tailFresh = 1500 * time.Millisecond
)

type tailCache struct {
	sync.Mutex
	at map[string]cachedTail
}

type cachedTail struct {
	at   time.Time
	tail api.TerminalTail
	ok   bool
}

// terminalTails serves GET /v1/projects/{project}/terminals?agents=a,b&lines=n:
// the last n lines (tailLines by default) of each named agent's terminal
// (agent.Manager.TerminalTail), read in parallel and kept for tailFresh, all
// tailMost of them, so a thumbnail and its preview share the read. An agent that can't be read (stopped, no
// tmux yet) is left out rather than failing the rest.
func (s *Server) terminalTails(w http.ResponseWriter, r *http.Request) error {
	project := r.PathValue("project")
	n := tailLines
	if v := r.URL.Query().Get("lines"); v != "" {
		got, err := strconv.Atoi(v)
		if err != nil || got < 1 || got > tailMost {
			return fmt.Errorf("invalid lines %q: use 1 to %d", v, tailMost)
		}
		n = got
	}
	var names []string
	for _, name := range strings.Split(r.URL.Query().Get("agents"), ",") {
		if name = strings.TrimSpace(name); name != "" && len(names) < 50 {
			names = append(names, name)
		}
	}
	m := s.manager(nil)
	tails := make([]cachedTail, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		ref := project + "/" + name
		s.tails.Lock()
		hit, found := s.tails.at[ref]
		s.tails.Unlock()
		if found && time.Since(hit.at) < tailFresh {
			tails[i] = hit
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			got := cachedTail{at: time.Now()}
			if a, err := s.store.Agent(r.Context(), project, name); err == nil && !a.IsLead() {
				if t, err := m.TerminalTail(r.Context(), a, tailMost); err == nil {
					got.tail = toAPITerminalTail(a.Ref(), t)
					got.ok = true
				}
			}
			if r.Context().Err() != nil {
				return
			}
			s.tails.Lock()
			if s.tails.at == nil {
				s.tails.at = map[string]cachedTail{}
			}
			for k, v := range s.tails.at { // drop what no one asked for in a while
				if time.Since(v.at) > time.Minute {
					delete(s.tails.at, k)
				}
			}
			s.tails.at[ref] = got
			s.tails.Unlock()
			tails[i] = got
		}()
	}
	wg.Wait()
	out := api.TerminalTails{Agents: []api.TerminalTail{}}
	for _, t := range tails {
		if t.ok {
			tail := t.tail
			tail.Lines = tail.Lines[max(0, len(tail.Lines)-n):]
			out.Agents = append(out.Agents, tail)
		}
	}
	return writeJSON(w, http.StatusOK, out)
}

func toAPITerminalTail(ref string, t agent.TerminalTail) api.TerminalTail {
	lines := t.Lines
	if lines == nil {
		lines = []string{}
	}
	return api.TerminalTail{Ref: ref, Window: t.Window, Command: t.Command, Busy: t.Busy, Lines: lines}
}
