package daemon

import (
	"net/http"
	"sync"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/gitrepo"
)

// filesLimit caps how many paths a worktree's file listing sends, so an
// enormous repository can't turn every keystroke in the composer's @ mentions
// into a huge response.
const filesLimit = 20000

// filesTTL is how long a worktree's file listing is cached, keyed by agent:
// long enough that the composer's autocomplete doesn't shell out to git on
// every keystroke, short enough that a file made moments ago shows up without
// restarting anything.
const filesTTL = 5 * time.Second

// filesCache remembers each agent's worktree file listing briefly.
type filesCache struct {
	mu      sync.Mutex
	entries map[string]filesEntry
	now     func() time.Time
}

type filesEntry struct {
	files     []string
	truncated bool
	err       error
	at        time.Time
}

func newFilesCache() *filesCache {
	return &filesCache{entries: map[string]filesEntry{}, now: time.Now}
}

func (c *filesCache) list(ref, worktree string) ([]string, bool, error) {
	c.mu.Lock()
	if e, ok := c.entries[ref]; ok && c.now().Sub(e.at) < filesTTL {
		c.mu.Unlock()
		return e.files, e.truncated, e.err
	}
	c.mu.Unlock()

	files, truncated, err := gitrepo.ListFiles(worktree, filesLimit)

	c.mu.Lock()
	c.entries[ref] = filesEntry{files: files, truncated: truncated, err: err, at: c.now()}
	c.mu.Unlock()
	return files, truncated, err
}

// listFiles serves a worktree's files, for @ mentions in the composer. from
// resolves the agent the same way the chat routes do, so the project's lead
// and its agents share one handler: agentFromPath for the route under
// /v1/agents/{project}/{agent}, leadFromPath for the one under
// /v1/projects/{project}.
func (s *Server) listFiles(from agentFrom) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		a, err := from(r)
		if err != nil {
			return err
		}
		root := a.Worktree
		if root == "" {
			// The project's chat hasn't started yet, so it has no worktree of its
			// own: its files are the main checkout's, same as what its first
			// message will stand on.
			p, err := s.store.Project(r.Context(), a.Project)
			if err != nil {
				return err
			}
			root = p.Root
		}
		files, truncated, err := s.files.list(a.Ref(), root)
		if err != nil {
			return err
		}
		return writeJSON(w, http.StatusOK, api.WorktreeFiles{Files: files, Truncated: truncated})
	}
}
