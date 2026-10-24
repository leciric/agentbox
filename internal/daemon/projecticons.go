package daemon

import (
	"context"
	"errors"
	"net/http"
	"time"

	"golang.org/x/sync/singleflight"

	"agentbox/internal/api"
	"agentbox/internal/projecticon"
	"agentbox/internal/state"
)

// redetectAfter is how long a look that found no icon is trusted before
// GET looks again: an icon added to the repository shows up within a day,
// without the repository being walked on every read.
const redetectAfter = 24 * time.Hour

// projectIcons is what the icon routes share: one look per project at a
// time, however many reads are waiting on it.
type projectIcons struct {
	detecting singleflight.Group
}

func newProjectIcons() *projectIcons { return &projectIcons{} }

// projectActivity answers GET /v1/project-activity.
func (s *Server) projectActivity(w http.ResponseWriter, r *http.Request) error {
	rows, err := s.store.ProjectsActivity(r.Context())
	if err != nil {
		return err
	}
	out := make([]api.ProjectActivity, 0, len(rows))
	for _, row := range rows {
		out = append(out, api.ProjectActivity{Project: row.Project, LastActivity: parseAt(row.LastActivity), LastMessage: parseAt(row.LastMessage)})
	}
	return writeJSON(w, http.StatusOK, out)
}

// parseAt reads a chat item's createdAt, nil for none or one that doesn't
// parse.
func parseAt(at string) *time.Time {
	if at == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return nil
	}
	return &t
}

func (s *Server) getProjectIcon(w http.ResponseWriter, r *http.Request) error {
	p, err := s.store.Project(r.Context(), r.PathValue("project"))
	if err != nil {
		return err
	}
	ic, ok, err := s.store.ProjectIcon(r.Context(), p.Name)
	if err != nil {
		return err
	}
	stale := !ok || ic.DetectedAt.IsZero() || (ic.Detected == nil && time.Since(ic.DetectedAt) > redetectAfter)
	if ic.Chosen == "" && stale {
		if ic, err = s.detectProjectIcon(r.Context(), p); err != nil {
			return err
		}
	}
	return writeJSON(w, http.StatusOK, projectIconOf(ic))
}

func (s *Server) setProjectIcon(w http.ResponseWriter, r *http.Request) error {
	var req api.SetProjectIconRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	p, err := s.store.Project(ctx, r.PathValue("project"))
	if err != nil {
		return err
	}
	switch {
	case (req.Emoji == "") == (req.Image == ""):
		return errors.New("choose an emoji or an image, one of them")
	case req.Emoji != "":
		emoji, err := projecticon.ParseEmoji(req.Emoji)
		if err != nil {
			return err
		}
		err = s.store.ChooseProjectIcon(ctx, p.Name, "emoji", emoji, nil, "", time.Now())
		if err != nil {
			return err
		}
	default:
		data, mime, err := projecticon.ParseImage(req.Image)
		if err != nil {
			return err
		}
		if err := s.store.ChooseProjectIcon(ctx, p.Name, "image", "", data, mime, time.Now()); err != nil {
			return err
		}
	}
	return s.writeProjectIcon(w, r, p.Name)
}

// deleteProjectIcon goes back to the detected icon, looking for it again:
// whoever resets it has likely just changed the repository's.
func (s *Server) deleteProjectIcon(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	p, err := s.store.Project(ctx, r.PathValue("project"))
	if err != nil {
		return err
	}
	if err := s.store.ChooseProjectIcon(ctx, p.Name, "", "", nil, "", time.Now()); err != nil {
		return err
	}
	ic, err := s.detectProjectIcon(ctx, p)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, projectIconOf(ic))
}

func (s *Server) writeProjectIcon(w http.ResponseWriter, r *http.Request, project string) error {
	ic, _, err := s.store.ProjectIcon(r.Context(), project)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, projectIconOf(ic))
}

// detectProjectIcon looks for a project's icon, keeps what it found (or
// that it found nothing) and returns the row as it now is. The look reads
// the repository, so it holds no transaction and
// runs once per project however many reads ask at once; other projects'
// looks run beside it. The look outlives a read that gives up on it, so
// what it finds is kept for the next.
func (s *Server) detectProjectIcon(ctx context.Context, p state.Project) (state.ProjectIcon, error) {
	icons := s.icons
	ch := icons.detecting.DoChan(p.Name, func() (any, error) {
		lookCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		icon, found := projecticon.FindInRepo(p.Root)
		if !found {
			icon = projecticon.Icon{}
		}
		return nil, s.store.SaveDetectedProjectIcon(lookCtx, p.Name, icon.Data, icon.MIME, icon.From, time.Now())
	})
	select {
	case res := <-ch:
		if res.Err != nil {
			return state.ProjectIcon{}, res.Err
		}
	case <-ctx.Done():
		return state.ProjectIcon{}, ctx.Err()
	}
	ic, _, err := s.store.ProjectIcon(ctx, p.Name)
	return ic, err
}

// projectIconOf is what a project shows, from its row.
func projectIconOf(ic state.ProjectIcon) api.ProjectIcon {
	out := api.ProjectIcon{Source: "none", HasDetected: ic.Detected != nil, DetectedFrom: ic.DetectedFrom}
	switch {
	case ic.Chosen == "emoji":
		out.Source, out.Emoji = "chosen", ic.Emoji
	case ic.Chosen == "image":
		out.Source, out.Image = "chosen", projecticon.DataURL(ic.MIME, ic.Image)
	case ic.Detected != nil:
		out.Source, out.Image = "detected", projecticon.DataURL(ic.DetectedMIME, ic.Detected)
	}
	return out
}
