package machinesweb

import (
	"context"
	"io"
	"strings"

	"agentbox/internal/api"
)

// Daemon is AgentBox's own agents' media, through the daemon's API. A
// Server works without one, and keeps working when its calls fail.
type Daemon interface {
	// Media lists every agent's screenshots and recordings, newest first.
	Media(ctx context.Context) ([]Item, error)
	// Open opens one's file; id is the daemon's, without the page's prefix.
	Open(ctx context.Context, id string) (io.ReadCloser, error)
	Delete(ctx context.Context, id string) error
	// Watch calls changed whenever an agent's media changes, until ctx ends
	// or the connection fails.
	Watch(ctx context.Context, changed func()) error
}

// APIDaemon is Daemon over an api.Client.
type APIDaemon struct{ Client *api.Client }

func (d APIDaemon) Media(ctx context.Context) ([]Item, error) {
	projects, err := d.Client.Projects(ctx)
	if err != nil {
		return nil, err
	}
	var out []Item
	for _, p := range projects {
		media, err := d.Client.ProjectMedia(ctx, p.Name, "", "")
		if err != nil {
			continue
		}
		agents := map[string]api.Agent{}
		if list, err := d.Client.Agents(ctx, p.Name); err == nil {
			for _, a := range list {
				agents[a.Name] = a
			}
		}
		for _, m := range media {
			if m.Kind != "screenshot" && m.Kind != "recording" {
				continue
			}
			out = append(out, fromAPI(p, agents, m))
		}
	}
	sortNewest(out)
	return out, nil
}

func fromAPI(p api.Project, agents map[string]api.Agent, m api.MediaItem) Item {
	name := m.AgentName
	if name == "" {
		_, name, _ = strings.Cut(m.Agent, "/")
	}
	a := agents[name]
	repo := p.DisplayName
	if repo == "" {
		repo = p.Name
	}
	label := name
	if m.AgentTitle != "" {
		label += " · " + m.AgentTitle
	}
	branch := a.Branch
	if branch == "" && m.AgentGone {
		branch = "(destroyed agents)"
	}
	caption := m.Name
	if m.Text != "" {
		caption += " — " + m.Text
	}
	return Item{
		ID: agentPrefix + m.ID, Source: SourceAgentBox, Kind: m.Kind, Created: m.CreatedAt,
		Repo: repo, Branch: branch, Worktree: a.Worktree, Session: name, SessionLabel: label,
		Tool: "agentbox", Caption: caption, Width: m.Meta.Width, Height: m.Meta.Height,
		DurationMs: int64(m.Meta.Duration * 1000), Bytes: m.Size, Mime: m.Mime, Path: m.Path,
	}
}

func (d APIDaemon) Open(ctx context.Context, id string) (io.ReadCloser, error) {
	return d.Client.MediaFile(ctx, "-", id)
}

func (d APIDaemon) Delete(ctx context.Context, id string) error {
	return d.Client.DeleteMedia(ctx, id)
}

func (d APIDaemon) Watch(ctx context.Context, changed func()) error {
	return d.Client.Events(ctx, func(e api.Event) error {
		if e.Type == api.EventMedia {
			changed()
		}
		return nil
	})
}
