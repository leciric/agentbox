package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// mediaRoutes are an agent's media endpoints, under its path on the host API
// and under /v1/self/media on its own socket (all but export).
var mediaRoutes = []struct{ method, path, action string }{
	{"GET", "", "list"},
	{"POST", "/screenshot", "screenshot"},
	{"GET", "/record", "record-status"},
	{"POST", "/record/start", "record-start"},
	{"POST", "/record/stop", "record-stop"},
	{"POST", "/add", "add"},
	{"POST", "/note", "note"},
	{"POST", "/logs", "logs"},
	{"POST", "/export", "export"},
}

func toAPIMedia(item state.Media, hostPath string) api.MediaItem {
	var meta api.MediaMeta
	_ = json.Unmarshal([]byte(item.Meta), &meta)
	out := api.MediaItem{
		ID:        item.ID,
		Agent:     item.Ref(),
		Kind:      item.Kind,
		Name:      item.Name,
		Path:      hostPath,
		Mime:      item.Mime,
		Size:      item.Size,
		SHA256:    item.SHA256,
		Source:    item.Source,
		Text:      item.Text,
		Meta:      meta,
		CreatedAt: item.CreatedAt,
	}
	if item.File != "" {
		out.File = filepath.Base(item.File)
	}
	return out
}

func toAPIRecording(st agent.RecordingStatus) api.RecordingStatus {
	if !st.Recording {
		return api.RecordingStatus{}
	}
	started := st.StartedAt
	return api.RecordingStatus{Recording: true, Target: st.Target, Input: st.Input, Name: st.Name, Source: st.Source, StartedAt: &started, LimitSeconds: int(st.Limit.Seconds())}
}

// media serves an agent's media endpoints. source is who adds items: "user" on
// the host API, "agent" on an agent's own socket, which never sees host paths.
func (s *Server) media(action string, agentOf func(*http.Request) (state.Agent, error), source string) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		a, err := agentOf(r)
		if err != nil {
			return err
		}
		ctx, m := r.Context(), s.manager(s.cfg.Log)
		hostPath := func(item state.Media) string {
			if source == "user" {
				return m.MediaPath(item)
			}
			return ""
		}

		var item state.Media
		switch action {
		case "list":
			items, err := s.store.Media(ctx, a.Project, a.Name)
			if err != nil {
				return err
			}
			out := make([]api.MediaItem, 0, len(items))
			for _, it := range items {
				out = append(out, toAPIMedia(it, hostPath(it)))
			}
			return writeJSON(w, http.StatusOK, out)
		case "record-status":
			status, err := m.Recording(ctx, a)
			if err != nil {
				return err
			}
			return writeJSON(w, http.StatusOK, toAPIRecording(status))
		case "record-start":
			var req api.RecordRequest
			if err := readJSON(r, &req); err != nil {
				return err
			}
			status, err := m.StartRecording(ctx, a, req.Target, req.Input, req.Name, time.Duration(req.LimitSeconds)*time.Second, source)
			if err != nil {
				return err
			}
			return writeJSON(w, http.StatusOK, toAPIRecording(status))
		case "export":
			var req api.ExportRequest
			if err := readJSON(r, &req); err != nil {
				return err
			}
			dir := req.Dir
			if dir == "" {
				home, err := os.UserHomeDir()
				if err != nil {
					return err
				}
				dir = filepath.Join(home, "AgentBox", "exports")
			}
			out, n, err := m.ExportMedia(ctx, a, dir)
			if err != nil {
				return err
			}
			return writeJSON(w, http.StatusOK, api.ExportResult{Dir: out, Items: n})
		case "record-stop":
			item, err = m.StopRecording(ctx, a)
		case "screenshot":
			var req api.ScreenshotRequest
			if err = readJSON(r, &req); err == nil {
				item, err = m.Screenshot(ctx, a, agent.ScreenshotOptions{Target: req.Target, FullPage: req.FullPage, Name: req.Name, Source: source})
			}
		case "add":
			var req api.AddMediaRequest
			if err = readJSON(r, &req); err == nil {
				item, err = m.AddMedia(ctx, a, agent.AddMediaOptions{Path: req.Path, Kind: req.Kind, Name: req.Name, Source: source})
			}
		case "note":
			var req api.NoteRequest
			if err = readJSON(r, &req); err == nil {
				item, err = m.AddNote(ctx, a, req.Text, req.Name, source)
			}
		case "logs":
			var req api.LogsRequest
			if err = readJSON(r, &req); err == nil {
				item, err = m.AddLogs(ctx, a, agent.LogsOptions{Service: req.Service, Since: req.Since, Terminal: req.Terminal, Window: req.Window,
					Android: req.Android, Package: req.Package, Name: req.Name, Source: source})
			}
		default:
			return fmt.Errorf("unknown media action %q", action)
		}
		if err != nil {
			return err
		}
		s.events.publish(api.EventMedia, toAPIMedia(item, m.MediaPath(item)))
		if art, ok := s.captureArtifact(ctx, a.Project, a.Name, "media", "/v1/media/"+item.ID, map[string]any{
			"mediaId": item.ID, "kind": item.Kind, "name": item.Name, "mime": item.Mime, "size": item.Size, "source": item.Source,
		}); ok {
			s.captureEvent(ctx, a.Project, a.Name, "artifact_created", map[string]any{
				"artifactId": art.ID, "kind": item.Kind, "name": item.Name,
			}, art.ID)
		}
		return writeJSON(w, http.StatusCreated, toAPIMedia(item, hostPath(item)))
	}
}

func (s *Server) mediaItem(w http.ResponseWriter, r *http.Request) error {
	item, err := s.store.MediaItem(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, toAPIMedia(item, s.manager(nil).MediaPath(item)))
}

// mediaFile serves an item's content, with range requests for video. For a
// directory, ?path= picks a file inside it; the default is its entry file.
func (s *Server) mediaFile(w http.ResponseWriter, r *http.Request) error {
	item, err := s.store.MediaItem(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	root := s.manager(nil).MediaPath(item)
	if root == "" {
		return fmt.Errorf("media %s is a note, with no file: %w", item.ID, state.ErrNotFound)
	}
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	file := root
	if info.IsDir() {
		rel := r.URL.Query().Get("path")
		if rel == "" {
			var meta api.MediaMeta
			_ = json.Unmarshal([]byte(item.Meta), &meta)
			rel = meta.Entry
		}
		if rel == "" {
			return errors.New("this item is a directory: choose a file in it with ?path=")
		}
		file = filepath.Join(root, filepath.FromSlash(rel))
		if inside, err := filepath.Rel(root, file); err != nil || inside == ".." || strings.HasPrefix(inside, "../") {
			return errors.New("that path is outside the item")
		}
	}
	f, err := os.Open(file)
	if err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(file), state.ErrNotFound)
	}
	defer func() { _ = f.Close() }()
	stat, err := f.Stat()
	if err != nil {
		return err
	}
	if stat.IsDir() {
		return fmt.Errorf("%s is a directory", filepath.Base(file))
	}
	if file == root && item.Mime != "" {
		w.Header().Set("Content-Type", item.Mime)
	}
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	http.ServeContent(w, r, filepath.Base(file), stat.ModTime(), f)
	return nil
}

func (s *Server) deleteMedia(w http.ResponseWriter, r *http.Request) error {
	item, err := s.store.MediaItem(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	if err := s.manager(nil).DeleteMedia(r.Context(), item); err != nil {
		return err
	}
	s.events.publish(api.EventMedia, api.MediaItem{ID: item.ID, Agent: item.Ref(), Kind: item.Kind, Name: item.Name, Removed: true})
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// deleteProjectMedia deletes many of a project's items at once, across its
// agents. Both bulk routes are on the host API only, like every other delete:
// they are deliberately not in mediaRoutes, which an agent's own socket serves.
func (s *Server) deleteProjectMedia(w http.ResponseWriter, r *http.Request) error {
	var req api.DeleteMediaRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	project := r.PathValue("project")
	if _, err := s.store.Project(r.Context(), project); err != nil {
		return err
	}
	out, err := s.deleteMediaBulk(r.Context(), project, "", req)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

// deleteAgentMedia is the same, pinned to one agent's gallery.
func (s *Server) deleteAgentMedia(w http.ResponseWriter, r *http.Request) error {
	a, err := s.agentFromPath(r)
	if err != nil {
		return err
	}
	var req api.DeleteMediaRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	out, err := s.deleteMediaBulk(r.Context(), a.Project, a.Name, req)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

// deleteMediaBulk removes the items a request names, row and files together,
// and says how many went and how much that freed. An empty agent means the
// whole project; a set one pins it, whatever the request's own agent filter says.
func (s *Server) deleteMediaBulk(ctx context.Context, project, agent string, req api.DeleteMediaRequest) (api.DeleteMediaResult, error) {
	var out api.DeleteMediaResult
	switch {
	case req.All && len(req.IDs) > 0:
		return out, errors.New("name the items to delete, or pass all, not both")
	case !req.All && len(req.IDs) == 0:
		return out, errors.New("name the items to delete, or pass all")
	}
	items, err := s.mediaToDelete(ctx, project, agent, req)
	if err != nil {
		return out, err
	}
	m := s.manager(s.cfg.Log)
	for _, item := range items {
		if err := m.DeleteMedia(ctx, item); err != nil {
			if errors.Is(err, state.ErrNotFound) {
				continue // deleted while we worked through the list
			}
			return out, err
		}
		out.Deleted++
		out.Bytes += item.Size
		s.events.publish(api.EventMedia, api.MediaItem{ID: item.ID, Agent: item.Ref(), Kind: item.Kind, Name: item.Name, Removed: true})
	}
	return out, nil
}

// mediaToDelete is what a bulk request comes to: the rows behind its IDs, or
// everything its filters match. An ID that's already gone is skipped, since
// this is a delete; one from somewhere else is refused, so a wrong project or
// agent in the path can't quietly take another's media.
func (s *Server) mediaToDelete(ctx context.Context, project, agent string, req api.DeleteMediaRequest) ([]state.Media, error) {
	if req.All {
		items, err := s.store.ProjectMedia(ctx, project)
		if err != nil {
			return nil, err
		}
		wanted := agent
		if wanted == "" {
			wanted = req.Agent
		}
		return slices.DeleteFunc(items, func(it state.Media) bool {
			return (wanted != "" && it.Agent != wanted) || (req.Kind != "" && it.Kind != req.Kind)
		}), nil
	}
	where := project
	if agent != "" {
		where = project + "/" + agent
	}
	items := make([]state.Media, 0, len(req.IDs))
	for _, id := range req.IDs {
		item, err := s.store.MediaItem(ctx, id)
		if errors.Is(err, state.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if item.Project != project || (agent != "" && item.Agent != agent) {
			return nil, fmt.Errorf("media %s belongs to %s, not %s", id, item.Ref(), where)
		}
		items = append(items, item)
	}
	return items, nil
}
