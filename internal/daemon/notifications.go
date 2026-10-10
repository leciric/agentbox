package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// The app's notifications are the three things an agent does that the user
// would want to hear about wherever they are in the app: it finished, it
// asks them something, or it kept a screenshot or recording. Each is stored
// as it happens, so the bell's history survives a restart and a missed one
// is still there the next day, and published so every window's toast and OS
// notification shows it at once. One seen state serves the bell and the
// Media view's unseen marks: a media notification is seen when either is.

// reportFresh is how recent an agent's last report must be to speak for a
// finish: older, and it's about an earlier task.
const reportFresh = 30 * time.Minute

// notify stores n and tells the app about it.
func (s *Server) notify(ctx context.Context, n api.Notification) {
	n.ID = newID()
	if n.At.IsZero() {
		n.At = time.Now()
	}
	n.Ref = n.Project + "/" + n.Agent
	data, err := json.Marshal(n)
	if err != nil {
		s.logf("notifying about %s: %v", n.Ref, err)
		return
	}
	row := state.Notification{ID: n.ID, Project: n.Project, Agent: n.Agent, Kind: n.Kind, CreatedAt: n.At, Data: data}
	if n.Media != nil {
		row.MediaID = n.Media.ID
	}
	if err := s.store.AddNotification(ctx, row); err != nil {
		s.logf("notifying about %s: %v", n.Ref, err)
	}
	s.events.publish(api.EventNotification, n)
}

// notifyFinished is an agent's finish, with what its last report said when
// it reported for this task.
func (s *Server) notifyFinished(ctx context.Context, ev api.AgentEvent) {
	n := api.Notification{Kind: api.NotifyFinished, Project: ev.Project, Agent: ev.Agent, Title: ev.Title, Text: ev.Summary, PR: ev.PR, At: ev.At}
	if reports, err := s.memory().Reports(ctx, ev.Project, ev.Agent); err == nil && len(reports) > 0 {
		if last := reports[0]; ev.At.Sub(last.CreatedAt) < reportFresh {
			n.Status = last.Status
			if last.Summary != "" {
				n.Text = last.Summary
			}
		}
	}
	n.Text = cutNotice(n.Text)
	s.notify(ctx, n)
}

// notifyQuestion is a question that reached the user: one the lead passed
// on, or a credential or connector only they can give. Those the lead
// answers itself are none of the user's business until it says otherwise.
func (s *Server) notifyQuestion(ctx context.Context, q state.Question, title string) {
	s.notify(ctx, api.Notification{
		Kind: api.NotifyQuestion, Project: q.Project, Agent: q.Agent, Title: title,
		Text: cutNotice(q.Text), Question: q.ID, At: q.CreatedAt,
	})
}

// notifyMedia is a screenshot or recording an agent kept. Notes, logs and
// reports are for reading in the Media tab, not for interrupting anyone.
func (s *Server) notifyMedia(ctx context.Context, a state.Agent, item api.MediaItem) {
	if item.Kind != "screenshot" && item.Kind != "recording" {
		return
	}
	item.AgentName, item.AgentTitle = a.Name, a.Title
	s.notify(ctx, api.Notification{
		Kind: api.NotifyMedia, Project: a.Project, Agent: a.Name, Title: a.Title,
		Text: item.Name, Media: &item, At: item.CreatedAt,
	})
}

// cutNotice keeps a notification's text to what a toast and a history row
// can show.
func cutNotice(text string) string {
	const limit = 400
	text = strings.TrimSpace(text)
	if r := []rune(text); len(r) > limit {
		return strings.TrimSpace(string(r[:limit])) + "…"
	}
	return text
}

// notifications is the bell's history: every project's, newest first.
func (s *Server) notifications(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	rows, err := s.store.Notifications(ctx, time.Now())
	if err != nil {
		return err
	}
	m := s.manager(nil)
	out := make([]api.Notification, 0, len(rows))
	for _, row := range rows {
		var n api.Notification
		if err := json.Unmarshal(row.Data, &n); err != nil {
			continue
		}
		n.Seen = !row.SeenAt.IsZero()
		if n.Media != nil {
			// The item may have been deleted since, or its path moved with the
			// data directory: what's stored is only what it was.
			item, err := s.store.MediaItem(ctx, n.Media.ID)
			if err != nil {
				n.Media.Removed = true
			} else {
				fresh := toAPIMedia(item, m.MediaPath(item))
				fresh.AgentName, fresh.AgentTitle = n.Media.AgentName, n.Media.AgentTitle
				n.Media = &fresh
			}
		}
		out = append(out, n)
	}
	return writeJSON(w, http.StatusOK, out)
}

// seeNotifications marks notifications seen, and tells every window.
func (s *Server) seeNotifications(w http.ResponseWriter, r *http.Request) error {
	var req api.SeeNotificationsRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	n, err := s.store.SeeNotifications(r.Context(), req.IDs, req.Media, req.All, req.AllMedia, time.Now())
	if err != nil {
		return err
	}
	if n > 0 {
		s.events.publish(api.EventNotificationsSeen, req)
	}
	return writeJSON(w, http.StatusOK, api.SeeNotificationsResult{Seen: n})
}

// allMedia is every project's media, newest first, for the app's Media view:
// each item labelled with its agent, as projectMedia does, and marked unseen
// while its notification is. ?kind= takes a comma-separated list, and ?limit=
// pages it (media_list.go).
func (s *Server) allMedia(w http.ResponseWriter, r *http.Request) error {
	return s.writeMediaList(w, r, state.MediaFilter{}, true)
}

// allMediaCounts is the counts on the Media view's filters.
func (s *Server) allMediaCounts(w http.ResponseWriter, r *http.Request) error {
	return s.writeMediaCounts(w, r, state.MediaFilter{})
}
