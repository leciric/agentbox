package daemon

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/chat"
	"agentbox/internal/gitrepo"
	"agentbox/internal/hostos"
	"agentbox/internal/share"
	"agentbox/internal/state"
	"agentbox/internal/timeline"
)

// agentShare is GET /v1/agents/{project}/{agent}/share: what the agent did as
// one HTML page to send anyone (internal/share), a live agent's or a past
// one's. The page carries its screenshots and a frame of each recording, and
// is redacted here, where the agent's secrets are known: their values, the
// project's GitHub token, its worktree's path and the user's home don't
// leave in it.
func (s *Server) agentShare(w http.ResponseWriter, r *http.Request) error {
	a, err := s.agentOrArchivedFromPath(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	var items []api.ChatItem
	if _, ok := chat.ToolNames[a.AI]; ok {
		thread, err := s.chat.Page(a, "", 0)
		if err != nil {
			return err
		}
		items = thread.Items
	}
	kept, err := s.store.Media(ctx, a.Project, a.Name)
	if err != nil {
		return err
	}
	media := make([]api.MediaItem, 0, len(kept))
	files := map[string]state.Media{}
	for _, m := range kept {
		media = append(media, toAPIMedia(m, ""))
		files[m.ID] = m
	}
	run := share.Run{
		Project: a.Project, Agent: a.Name, Title: a.Title, AI: a.AI, Branch: a.Branch,
		Task:     share.Task(items),
		Timeline: timeline.Build(items, media),
		Messages: share.Messages(items),
		Exported: time.Now(),
	}
	if past, err := s.archivedFromPath(r); err == nil && a.Worktree == "" {
		if run.Task == "" {
			run.Task = past.Task
		}
		if past.PR.Number > 0 {
			run.PR = &api.PullRequest{Number: past.PR.Number, Title: past.PR.Title, URL: past.PR.URL, State: past.PR.State}
		}
	} else {
		run.PR = s.prFor(ctx, a)
	}
	if stats, err := gitrepo.NumstatWorktree(a.Worktree, a.BaseCommit); err == nil && a.Worktree != "" {
		for _, f := range stats {
			run.Files = append(run.Files, share.File{Path: f.Path, Added: f.Added, Removed: f.Removed, Binary: f.Binary})
		}
	} else {
		run.Files, run.FilesUncounted = share.EditedPaths(run.Timeline), true
		if len(run.Files) == 0 {
			run.FilesUncounted = false
		}
	}
	var b bytes.Buffer
	err = share.Write(&b, run, share.Options{
		Redactor: s.shareRedactor(ctx, a),
		Picture: func(m api.MediaItem) (share.Picture, bool) {
			return s.sharePicture(ctx, files[m.ID], m)
		},
	})
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+share.FileName(a.Project, a.Name)+`"`)
	w.Header().Set("Content-Length", strconv.Itoa(b.Len()))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b.Bytes())
	return nil
}

// shareRedactor knows what's secret about a: the values of the secrets it
// is given, and its project's GitHub token, besides the shapes and names
// internal/redact knows; and where its worktree and the user's home are.
func (s *Server) shareRedactor(ctx context.Context, a state.Agent) share.Redactor {
	var values []string
	if given, err := s.secrets().ForAgent(ctx, a.Project, a.Name); err == nil {
		for _, v := range given {
			values = append(values, v.Value)
		}
	}
	if p, err := s.store.Project(ctx, a.Project); err == nil {
		m := s.manager(nil)
		if account, err := m.GitHubAccountFor(p, a.GitHubAccount); err == nil {
			if token, err := m.Creds.GitHubToken(account); err == nil && token != "" {
				values = append(values, token)
			}
		}
	}
	home, _ := os.UserHomeDir()
	return share.NewRedactor(values, []string{a.Worktree}, home, os.Getenv("HOME"), hostos.Home())
}

// A screenshot bigger than maxPictureFile isn't put in a page, and a frame
// that takes longer than frameTimeout to read is left out.
const (
	maxPictureFile = 8 << 20
	frameTimeout   = 15 * time.Second
)

// sharePicture is a screenshot's file, or a frame from the middle of a
// recording, made smaller, when ffmpeg is here to make one.
func (s *Server) sharePicture(ctx context.Context, item state.Media, m api.MediaItem) (share.Picture, bool) {
	path := s.manager(nil).MediaPath(item)
	if path == "" {
		return share.Picture{}, false
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return share.Picture{}, false
	}
	switch m.Kind {
	case "screenshot":
		if info.Size() > maxPictureFile {
			return share.Picture{}, false
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return share.Picture{}, false
		}
		// The type of what's in the file, not the name it was given: it goes
		// in a data URL.
		mime := http.DetectContentType(data)
		if !strings.HasPrefix(mime, "image/") {
			return share.Picture{}, false
		}
		return share.Picture{Mime: mime, Data: data}, true
	case "recording":
		ffmpeg, err := exec.LookPath("ffmpeg")
		if err != nil {
			return share.Picture{}, false
		}
		ctx, cancel := context.WithTimeout(ctx, frameTimeout)
		defer cancel()
		at := m.Meta.Duration / 2
		// -ss before -i seeks by keyframe, fast however long the video.
		cmd := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-loglevel", "error", "-nostdin",
			"-ss", fmt.Sprintf("%.2f", at), "-i", path, "-frames:v", "1",
			"-vf", "scale='min(1280,iw)':-2", "-q:v", "4", "-f", "image2", "-c:v", "mjpeg", "pipe:1")
		data, err := cmd.Output()
		if err != nil || len(data) == 0 {
			return share.Picture{}, false
		}
		return share.Picture{Mime: "image/jpeg", Data: data}, true
	}
	return share.Picture{}, false
}
