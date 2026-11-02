package daemon

import (
	"context"
	"sync"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// An agent's permission request shows in its project's lead chat too, as a
// card the user answers there on the agent's own route (chat's
// leadpermission.go). The chat reports each change to a request under the
// agent's lock, so it is queued here and handed to the lead's chat in the
// order it happened: a request answered at once must not reach the lead
// answered and then asked.

// leadPermissions is the queue of changes on their way to leads' chats.
type leadPermissions struct {
	mu      sync.Mutex
	queue   []queuedPermission
	running bool
}

type queuedPermission struct {
	project, session string
	p                api.ChatAgentPermission
}

// agentPermission is the chat's PermissionChanged: it queues the change and
// returns.
func (s *Server) agentPermission(a state.Agent, p api.ChatAgentPermission) {
	s.pushPermission(a, p)
	q := &s.leadPermissions
	q.mu.Lock()
	defer q.mu.Unlock()
	q.queue = append(q.queue, queuedPermission{a.Project, a.Session(), p})
	if !q.running {
		q.running = true
		go s.showAgentPermissions()
	}
}

// showAgentPermissions empties the queue into the leads' chats, one change at
// a time, and ends when it is empty.
func (s *Server) showAgentPermissions() {
	q := &s.leadPermissions
	for {
		q.mu.Lock()
		if len(q.queue) == 0 {
			q.running = false
			q.mu.Unlock()
			return
		}
		next := q.queue[0]
		q.queue = q.queue[1:]
		q.mu.Unlock()
		s.showAgentPermission(context.Background(), next.project, next.session, next.p)
	}
}

// showAgentPermission puts p in the chat of the session that made the agent,
// or of the one most recently active. A project with no session has no chat
// to show it in: the agent's own chat and the inbox still do.
func (s *Server) showAgentPermission(ctx context.Context, project, session string, p api.ChatAgentPermission) {
	lead, err := s.leadOf(ctx, project, session, "", false)
	if err != nil {
		return
	}
	if err := s.chat.ShowAgentPermission(lead, p); err != nil {
		s.logf("showing %s's permission request in its lead: %v", p.Ref, err)
	}
}
