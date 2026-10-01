package daemon

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/incus"
	"agentbox/internal/state"
)

// recreate gives an agent whose machine is gone a new one (agent.Recreate):
// how `agentbox vm migrate` moves each agent into AgentBox's VM, after the
// VM's daemon has taken over the state.db its agents are recorded in.
func (s *Server) recreate(w http.ResponseWriter, r *http.Request) error {
	a, err := s.agentFromPath(r)
	if err != nil {
		return err
	}
	var req api.RecreateRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if req.Home != "" && !filepath.IsAbs(req.Home) {
		return fmt.Errorf("home must be an absolute path, not %q", req.Home)
	}
	if err := s.diskRefusal("making " + a.Ref() + " a new machine"); err != nil {
		return err
	}
	return s.startJob(w, "recreate", a.Ref(), func(ctx context.Context, log io.Writer) (any, error) {
		// A chat started before the machine was there has nothing to talk to.
		s.chat.Stop(a.Ref(), "the agent's machine is being made")
		err := s.manager(log).Recreate(ctx, a, agent.RecreateOptions{
			Home:    req.Home,
			Stopped: req.Stopped,
		})
		if err != nil {
			return nil, err
		}
		a, err := s.store.Agent(ctx, a.Project, a.Name)
		if err != nil {
			return nil, err
		}
		return s.agentReady(ctx, a)
	})
}

// checkMigration compares this daemon's state with the copy of the state.db it
// was made from, which `agentbox vm migrate` kept: every project and agent,
// each agent's worktree and machine, and the rows of what a user would miss
// (state.Compare). What it can't find is a problem, and a migration with
// problems isn't verified, so nothing of the old installation is removed.
func (s *Server) checkMigration(w http.ResponseWriter, r *http.Request) error {
	var req api.MigrationCheckRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if !filepath.IsAbs(req.Backup) {
		return fmt.Errorf("backup must be an absolute path, not %q", req.Backup)
	}
	if _, err := os.Stat(req.Backup); err != nil {
		return fmt.Errorf("the state.db to compare with: %v", err)
	}
	from, err := state.OpenReadOnly(req.Backup)
	if err != nil {
		return err
	}
	defer func() { _ = from.Close() }()
	check, err := s.compareWith(r.Context(), from)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, check)
}

func (s *Server) compareWith(ctx context.Context, from *sql.DB) (api.MigrationCheck, error) {
	var out api.MigrationCheck
	problem := func(format string, args ...any) { out.Problems = append(out.Problems, fmt.Sprintf(format, args...)) }

	projects, err := names(ctx, from, "SELECT name FROM projects ORDER BY name")
	if err != nil {
		return out, err
	}
	for _, p := range projects {
		if _, err := s.store.Project(ctx, p); err != nil {
			problem("project %s isn't here: %v", p, err)
		}
	}
	out.Found = append(out.Found, fmt.Sprintf("%d project(s): %s", len(projects), listOr(projects, "none")))

	refs, err := names(ctx, from, "SELECT project || '/' || name FROM agents WHERE name != '"+state.LeadName+"' ORDER BY project, name")
	if err != nil {
		return out, err
	}
	m := s.manager(io.Discard)
	machines := 0
	for _, ref := range refs {
		project, name, _ := strings.Cut(ref, "/")
		a, err := s.store.Agent(ctx, project, name)
		if err != nil {
			problem("agent %s isn't here: %v", ref, err)
			continue
		}
		if _, err := os.Stat(a.Worktree); err != nil {
			problem("%s's worktree isn't there: %v", ref, err)
		}
		switch _, err := m.Incus.Instance(ctx, a.Instance); {
		case errors.Is(err, incus.ErrNotFound):
			problem("%s has no machine here yet", ref)
		case err != nil:
			problem("%s's machine: %v", ref, err)
		case a.Status != state.AgentReady:
			problem("%s's machine is still being made", ref)
		default:
			machines++
		}
	}
	out.Found = append(out.Found, fmt.Sprintf("%d agent(s), %d of them with their worktree and a machine here: %s", len(refs), machines, listOr(refs, "none")))

	if ok, err := state.HasTable(ctx, from, "chat_items"); err != nil {
		return out, err
	} else if ok {
		lost, total, err := s.compareChats(ctx, from)
		if err != nil {
			return out, err
		}
		out.Problems = append(out.Problems, lost...)
		out.Found = append(out.Found, fmt.Sprintf("%d chat item(s), across every project's chat and every agent's", total))
	}

	carried, err := state.Compare(ctx, from, s.store.DB())
	if err != nil {
		return out, err
	}
	var counts []string
	for _, c := range carried {
		switch c.Table {
		case "projects", "agents", "chat_items":
			continue // above, by name
		}
		if c.To < c.From {
			problem("%s: %d here, of the %d there were", strings.ReplaceAll(c.Table, "_", " "), c.To, c.From)
		}
		if c.From > 0 {
			counts = append(counts, fmt.Sprintf("%d %s", c.From, strings.ReplaceAll(c.Table, "_", " ")))
		}
	}
	if len(counts) > 0 {
		out.Found = append(out.Found, "and "+strings.Join(counts, ", "))
	}

	if err := s.compareMedia(ctx, from, &out); err != nil {
		return out, err
	}
	out.OK = len(out.Problems) == 0
	return out, nil
}

// compareChats reports every chat that has fewer items here than it had.
func (s *Server) compareChats(ctx context.Context, from *sql.DB) (lost []string, total int, err error) {
	rows, err := from.QueryContext(ctx, "SELECT project, agent, count(*) FROM chat_items GROUP BY project, agent ORDER BY project, agent")
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var project, agentName string
		var n int
		if err := rows.Scan(&project, &agentName, &n); err != nil {
			return nil, 0, err
		}
		total += n
		var here int
		if err := s.store.DB().QueryRowContext(ctx, "SELECT count(*) FROM chat_items WHERE project = ? AND agent = ?", project, agentName).Scan(&here); err != nil {
			return nil, 0, err
		}
		if here < n {
			lost = append(lost, fmt.Sprintf("%s/%s's chat has %d item(s) here, of the %d it had", project, agentName, here, n))
		}
	}
	return lost, total, rows.Err()
}

// compareMedia checks the file of every media item there was is here, with
// the size it had.
func (s *Server) compareMedia(ctx context.Context, from *sql.DB, out *api.MigrationCheck) error {
	if ok, err := state.HasTable(ctx, from, "media"); err != nil || !ok {
		return err
	}
	rows, err := from.QueryContext(ctx, "SELECT project, agent, file, size FROM media WHERE file != ''")
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	m := s.manager(io.Discard)
	files := 0
	for rows.Next() {
		var item state.Media
		var size int64
		if err := rows.Scan(&item.Project, &item.Agent, &item.File, &size); err != nil {
			return err
		}
		files++
		path := m.MediaPath(item)
		info, err := os.Stat(path)
		switch {
		case err != nil:
			out.Problems = append(out.Problems, fmt.Sprintf("media file %s isn't here", path))
		case info.Mode().IsRegular() && size > 0 && info.Size() != size:
			out.Problems = append(out.Problems, fmt.Sprintf("media file %s has %d bytes here, of %d", path, info.Size(), size))
		}
	}
	if files > 0 {
		out.Found = append(out.Found, fmt.Sprintf("%d media file(s)", files))
	}
	return rows.Err()
}

// names runs a query of one text column.
func names(ctx context.Context, db *sql.DB, query string) ([]string, error) {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out, rows.Err()
}

func listOr(items []string, none string) string {
	if len(items) == 0 {
		return none
	}
	return strings.Join(items, ", ")
}
