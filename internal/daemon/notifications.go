package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// The app's notifications are what an agent does that only the user can
// answer: a question, or a credential or connector it asks for. Each is
// stored as it happens, so the inbox still has the one missed yesterday, and
// published so every window's toast and OS notification shows it at once.
// What agents merely did — a finish, a screenshot kept — is not one: finished
// work is the review list's (review.go). A kept screenshot or recording is
// still stored, out of the list, as the Media views' unseen mark: one seen
// state serves the inbox's questions and those marks.

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

// notifyQuestion is a question that reached the user: one the lead passed
// on, or a credential or connector only they can give. Those the lead
// answers itself are none of the user's business until it says otherwise.
func (s *Server) notifyQuestion(ctx context.Context, q state.Question, title string) {
	s.notify(ctx, api.Notification{
		Kind: api.NotifyQuestion, Project: q.Project, Agent: q.Agent, Title: title,
		Text: cutNotice(q.Text), Question: q.ID, At: q.CreatedAt,
	})
	s.pushQuestion(q, title)
}

// notifyMedia marks a screenshot or recording an agent kept as unseen in the
// Media views. Notes, logs and reports are for reading in the Media tab, not
// for interrupting anyone.
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

// notificationsMax is the most notifications a page holds, whatever ?limit=
// asks.
const notificationsMax = 200

// notifications is the inbox's questions, newest first: a page of them with
// ?limit= (and ?cursor=), every one without. ?unseen=1 keeps those not seen.
func (s *Server) notifications(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	q := r.URL.Query()
	limit := 0
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return errors.New("limit must be a positive number")
		}
		limit = min(n, notificationsMax)
	}
	filter := state.NotificationFilter{Kind: api.NotifyQuestion, Unseen: q.Get("unseen") == "1" || q.Get("unseen") == "true"}
	rows, next, total, err := s.store.NotificationPage(ctx, time.Now(), filter, q.Get("cursor"), limit)
	if err != nil {
		return err
	}
	out := make([]api.Notification, 0, len(rows))
	for _, row := range rows {
		var n api.Notification
		if err := json.Unmarshal(row.Data, &n); err != nil {
			continue
		}
		n.Seen = !row.SeenAt.IsZero()
		out = append(out, n)
	}
	if limit == 0 {
		return writeJSON(w, http.StatusOK, out)
	}
	return writeJSON(w, http.StatusOK, api.NotificationPage{Items: out, Next: next, Total: total})
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
	if req.Kind != "" {
		more, err := s.store.SeeKind(r.Context(), req.Kind, time.Now())
		if err != nil {
			return err
		}
		n += more
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
