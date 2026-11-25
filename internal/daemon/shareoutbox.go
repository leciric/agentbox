package daemon

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// A shared project's owner that can't be reached (shareremote.go) still gets
// what is written in its project here: a message in its chat, a prompt to one
// of its agents, an answer to an agent's question. The peer keeps each in
// share_outbox, answers that it is kept (api.ChatAsideAway), and sends them in
// order once the owner is back (remoteOutbox), each with a key the owner
// answers once (answerOnce), so one whose answer was lost on the way back is
// not done twice. What the owner refuses then (the project unshared, the peer's
// access lowered, the agent or the question gone) is kept with why, not sent
// again, until it is taken back. Everything else written in a project whose
// owner is away is refused, as before.
//
// Once anything waits for an owner, what is written in its projects after it
// waits too, whether or not the owner is back yet: they go in the order they
// were written.

// shareKeyHeader carries a kept write's key to its owner.
const shareKeyHeader = "Idempotency-Key"

// shareQueueable says whether a write of this kind is kept while its owner is
// away: the kinds shareActivityOf names.
func shareQueueable(kind string) bool {
	return kind == api.ShareDidMessage || kind == api.ShareDidPrompt || kind == api.ShareDidAnswer
}

// Sending what waits for an owner is tried again after outboxRetryFirst, each
// failure in a row waiting longer up to outboxRetryMax, and at once when its
// events are back. One try may take outboxSendTimeout: a message readies the
// chat it goes to first, which can take a while.
const (
	outboxRetryFirst  = 5 * time.Second
	outboxRetryMax    = time.Minute
	outboxSendTimeout = 2 * time.Minute
)

// maxQueuedBody is the most a kept write may hold, its pictures and files
// included: what proxyRemote reads of a request.
const maxQueuedBody = 96 << 20

// remoteWrite is a message, a prompt or an answer written in one of a
// remote's projects (local, its name here) at path, its owner's. It goes to
// the owner now if nothing waits for it and it isn't known to be away; it is
// kept otherwise, or if the owner turns out not to be reachable.
func (s *Server) remoteWrite(w http.ResponseWriter, r *http.Request, c *remoteConn, local, path string, act shareActivity) {
	ctx := r.Context()
	body, err := io.ReadAll(io.LimitReader(r.Body, maxQueuedBody+1))
	if err != nil {
		writeError(w, err)
		return
	}
	if len(body) > maxQueuedBody {
		writeError(w, errors.New("that is too big to send to a shared project"))
		return
	}
	if q := strings.ReplaceAll(r.URL.RawQuery, c.r.ID+remoteSep, ""); q != "" {
		path += "?" + q
	}
	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		writeError(w, err)
		return
	}
	q := state.ShareQueued{Remote: c.r.ID, Key: hex.EncodeToString(key), Kind: act.kind, Project: local, Path: path, Body: unrename(body, c.r.ID), Created: time.Now()}
	describeQueued(&q, act, body)

	if !s.holdingFor(ctx, c) {
		res := s.sendQueued(ctx, c, q)
		if ctx.Err() != nil {
			return // the app went: nothing to keep for it
		}
		if res.away == nil {
			// The owner answered: what it said, as it would have.
			for _, h := range []string{"Content-Type", "Retry-After"} {
				if v := res.header.Get(h); v != "" {
					w.Header().Set(h, v)
				}
			}
			out := fromRemote(res.body, c)
			w.Header().Set("Content-Length", strconv.Itoa(len(out)))
			w.WriteHeader(res.status)
			_, _ = w.Write(out)
			return
		}
	}
	// It would be refused when the owner is back: say so now.
	if remoteAccess(c, local) == state.ShareAccessView {
		_ = writeJSON(w, http.StatusForbidden, api.Error{Error: fmt.Sprintf("%s lets this AgentBox only look at %s: it can't write in its chats (its owner chooses, in Settings → Share)", c.r.Name, act.project)})
		return
	}
	q, err = s.store.AddShareQueued(ctx, q)
	if err != nil {
		writeError(w, err)
		return
	}
	s.logf("share: %s can't be reached; keeping a %s in %s to send when it's back", c.r.Name, q.Kind, local)
	s.publishOutbox(ctx)
	c.kickOutbox()
	if q.Chat != "" {
		_ = writeJSON(w, http.StatusAccepted, queuedChatItem(q))
		return
	}
	_ = writeJSON(w, http.StatusAccepted, s.queuedInfo(q))
}

// describeQueued fills in what the app shows of a kept write: the chat it is
// in, what it says, and the agent or the question it is for.
func describeQueued(q *state.ShareQueued, act shareActivity, body []byte) {
	switch act.kind {
	case api.ShareDidAnswer:
		var req api.AnswerQuestionRequest
		_ = json.Unmarshal(body, &req)
		q.Question, q.Text = act.detail, req.Answer
		return
	case api.ShareDidPrompt:
		q.Chat = q.Project + "/" + act.agent
	default:
		q.Chat = api.SessionRef(q.Project, act.detail)
	}
	var req api.ChatMessageRequest
	_ = json.Unmarshal(body, &req)
	q.Text, q.To, q.Attachments = strings.TrimSpace(req.Text), req.To, len(req.Images)+len(req.Files)
}

// queuedChatItem is what a message kept for its owner is answered with: the
// chat item it stands for until it is sent (api.ChatAsideAway).
func queuedChatItem(q state.ShareQueued) api.ChatItem {
	id := api.ShareQueuedPrefix + strconv.FormatInt(q.ID, 10)
	it := api.ChatItem{ID: id, Turn: id, Kind: "user", Text: q.Text, Delivery: api.ChatAsideAway, CreatedAt: q.Created, UpdatedAt: q.Created}
	if q.To != "" {
		it.Kind, it.To = "notice", q.To
	}
	return it
}

// remoteAccess is what a remote's project (by its name here) let this
// AgentBox do the last time the remote listed it, or "" if it didn't.
func remoteAccess(c *remoteConn, local string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, raw := range c.projects {
		var p struct {
			Name   string             `json:"name"`
			Remote *api.ProjectRemote `json:"remote"`
		}
		if json.Unmarshal(raw, &p) == nil && p.Name == local && p.Remote != nil {
			return p.Remote.Access
		}
	}
	return ""
}

// holdingFor says a write in one of c's projects is to be kept rather than
// sent: its owner's events say it is away, or something already waits for it.
func (s *Server) holdingFor(ctx context.Context, c *remoteConn) bool {
	c.mu.Lock()
	away := c.away
	c.mu.Unlock()
	if away {
		return true
	}
	waiting, err := s.store.ShareOutbox(ctx, c.r.ID, true)
	return err != nil || len(waiting) > 0
}

// sent is what an owner answered a kept write with; away says it didn't,
// and why: it can't be reached, or what is between says so.
type sent struct {
	status int
	header http.Header
	body   []byte
	away   error
}

// sendQueued sends a write to its owner once.
func (s *Server) sendQueued(ctx context.Context, c *remoteConn, q state.ShareQueued) sent {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base()+q.Path, bytes.NewReader(q.Body))
	if err != nil {
		return sent{away: err}
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(shareKeyHeader, q.Key)
	resp, err := s.peering.remotes.client.Do(req)
	if err != nil {
		return sent{away: fmt.Errorf("%s can't be reached: %w", c.r.Name, err)}
	}
	defer func() { _ = resp.Body.Close() }()
	// An answer cut off on its way back may still have been done: sent
	// again, the key has the owner answer as it did.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return sent{away: fmt.Errorf("%s's answer was cut off: %w", c.r.Name, err)}
	}
	if ownerAway(resp.StatusCode, raw) {
		return sent{away: fmt.Errorf("%s answered %s", c.r.Name, resp.Status)}
	}
	return sent{status: resp.StatusCode, header: resp.Header, body: raw}
}

// ownerAway says an answer is not the owner's own but from what stands
// between (a Cloudflare tunnel whose other end is gone answers 530), or one
// that says to try again later: busy (429), or unavailable for now (503).
// Any other answer is the owner's, which is final.
func ownerAway(status int, body []byte) bool {
	switch {
	case status == http.StatusTooManyRequests, status == http.StatusServiceUnavailable:
		return true
	case status == http.StatusBadGateway, status == http.StatusGatewayTimeout, status >= 520 && status <= 530:
		var e api.Error
		return json.Unmarshal(body, &e) != nil || e.Error == ""
	}
	return false
}

// refusedWhy is what the app says of a kept write its owner refused once
// back.
func refusedWhy(c *remoteConn, res sent) string {
	if res.status == http.StatusUnauthorized {
		return c.r.Name + " unpaired this AgentBox, so it wasn't sent"
	}
	msg := http.StatusText(res.status)
	var e api.Error
	if json.Unmarshal(res.body, &e) == nil && e.Error != "" {
		msg = e.Error
	}
	return fmt.Sprintf("%s refused it when it was back: %s", c.r.Name, msg)
}

func (c *remoteConn) kickOutbox() {
	select {
	case c.outbox <- struct{}{}:
	default:
	}
}

// remoteOutbox sends what waits for a remote whenever it may be back: at
// once (what the last daemon left), when its events connect again, when
// something more is kept, and after a while when a try failed.
func (s *Server) remoteOutbox(ctx context.Context, c *remoteConn) {
	var backoff time.Duration
	for {
		var retry <-chan time.Time
		if s.deliverOutbox(ctx, c) {
			backoff = min(max(2*backoff, outboxRetryFirst), outboxRetryMax)
			retry = time.After(backoff)
		} else {
			backoff = 0
		}
		select {
		case <-ctx.Done():
			return
		case <-c.outbox:
		case <-retry:
		}
	}
}

// deliverOutbox sends what waits for c, oldest first, until it is all sent or
// refused, or the owner turns out to be away: then it says something still
// waits.
func (s *Server) deliverOutbox(ctx context.Context, c *remoteConn) (waiting bool) {
	list, err := s.store.ShareOutbox(ctx, c.r.ID, true)
	if err != nil {
		if ctx.Err() == nil {
			s.logf("share: reading what waits for %s: %v", c.r.Name, err)
		}
		return true
	}
	for _, item := range list {
		q, ok := c.take(ctx, s.store, item.ID)
		if !ok {
			continue // taken back meanwhile
		}
		sctx, cancel := context.WithTimeout(ctx, outboxSendTimeout)
		res := s.sendQueued(sctx, c, q)
		cancel()
		c.mu.Lock()
		c.sending = 0
		c.mu.Unlock()
		if ctx.Err() != nil {
			return true
		}
		if res.away != nil {
			return true
		}
		if res.status/100 == 2 {
			if err := s.store.RemoveShareQueued(ctx, q.ID); err != nil && !errors.Is(err, state.ErrNotFound) {
				s.logf("share: forgetting a sent %s: %v", q.Kind, err)
			}
			s.logf("share: %s is back; sent the %s in %s kept for it", c.r.Name, q.Kind, q.Project)
		} else {
			why := refusedWhy(c, res)
			if err := s.store.RefuseShareQueued(ctx, q.ID, why); err != nil {
				s.logf("share: %v", err)
			}
			s.logf("share: the %s in %s kept for %s wasn't sent: %s", q.Kind, q.Project, c.r.Name, why)
		}
		s.publishOutbox(ctx)
	}
	return false
}

// take reads a kept write to send it, unless it was taken back or refused,
// and marks it being sent, which cancelShareQueued then refuses: one step,
// under c.mu, so a write taken back is never sent.
func (c *remoteConn) take(ctx context.Context, store *state.Store, id int64) (state.ShareQueued, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	q, err := store.ShareQueuedItem(ctx, id)
	if err != nil || q.Error != "" {
		return q, false
	}
	c.sending = id
	return q, true
}

func (s *Server) queuedInfo(q state.ShareQueued) api.ShareQueued {
	out := api.ShareQueued{
		ID: q.ID, Remote: q.Remote, Kind: q.Kind, Project: q.Project, Chat: q.Chat, Question: q.Question, To: q.To,
		Text: q.Text, Attachments: q.Attachments, CreatedAt: q.Created, Error: q.Error,
	}
	if c := s.remoteConn(q.Remote); c != nil {
		out.Owner = c.r.Name
	}
	return out
}

func (s *Server) shareOutbox(ctx context.Context) ([]api.ShareQueued, error) {
	list, err := s.store.ShareOutbox(ctx, "", false)
	if err != nil {
		return nil, err
	}
	out := make([]api.ShareQueued, 0, len(list))
	for _, q := range list {
		out = append(out, s.queuedInfo(q))
	}
	return out, nil
}

// publishOutbox tells the app what waits now. It names no project, so no
// peer of this AgentBox's own is sent it (shareEvents).
func (s *Server) publishOutbox(ctx context.Context) {
	out, err := s.shareOutbox(ctx)
	if err != nil {
		s.logf("share: %v", err)
		return
	}
	s.events.publish(api.EventShareOutbox, out)
}

func (s *Server) getShareOutbox(w http.ResponseWriter, r *http.Request) error {
	out, err := s.shareOutbox(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

// cancelShareQueued takes back a write that waits, or forgets one its owner
// refused; not one being sent this moment.
func (s *Server) cancelShareQueued(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return fmt.Errorf("outbox item %q: %w", r.PathValue("id"), state.ErrNotFound)
	}
	q, err := s.store.ShareQueuedItem(ctx, id)
	if err != nil {
		return err
	}
	if c := s.remoteConn(q.Remote); c != nil {
		c.mu.Lock()
		if c.sending == id {
			c.mu.Unlock()
			return fmt.Errorf("it's being sent to %s right now: %w", c.r.Name, state.ErrExists)
		}
		err = s.store.RemoveShareQueued(ctx, id)
		c.mu.Unlock()
	} else {
		err = s.store.RemoveShareQueued(ctx, id)
	}
	if err != nil {
		return err
	}
	s.publishOutbox(ctx)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// The owner's side: a peer's write that carries a key (shareKeyHeader) is
// done once. Sent again, because its answer never reached the peer, it is
// answered as it was the first time; one still being done is waited for.
// Kept for shareOnceFor, in memory: a peer sends again within minutes of
// being able to.
type shareOnce struct {
	mu   sync.Mutex
	done map[string]*onceAnswer // by peer id and key
}

type onceAnswer struct {
	ready  chan struct{}
	at     time.Time
	status int
	ctype  string
	body   []byte
}

const shareOnceFor = time.Hour

// answerOnce serves a peer's write through serve the first time its key
// arrives, and answers as then every other time. A write that failed is
// forgotten once its answer is given, so it can be tried again.
func (s *Server) answerOnce(w http.ResponseWriter, r *http.Request, key string, serve func(http.ResponseWriter)) {
	o := &s.peering.once
	o.mu.Lock()
	if o.done == nil {
		o.done = map[string]*onceAnswer{}
	}
	for k, a := range o.done {
		if time.Since(a.at) > shareOnceFor {
			delete(o.done, k)
		}
	}
	if a := o.done[key]; a != nil {
		o.mu.Unlock()
		select {
		case <-a.ready:
		case <-r.Context().Done():
			return
		}
		if a.ctype != "" {
			w.Header().Set("Content-Type", a.ctype)
		}
		w.WriteHeader(a.status)
		_, _ = w.Write(a.body)
		return
	}
	a := &onceAnswer{ready: make(chan struct{}), at: time.Now()}
	o.done[key] = a
	o.mu.Unlock()
	rec := &keptWriter{ResponseWriter: w}
	serve(rec)
	a.status, a.ctype, a.body = cmpStatus(rec.status), w.Header().Get("Content-Type"), rec.body.Bytes()
	if a.status/100 != 2 {
		o.mu.Lock()
		if o.done[key] == a {
			delete(o.done, key)
		}
		o.mu.Unlock()
	}
	close(a.ready)
}

func cmpStatus(status int) int {
	if status == 0 {
		return http.StatusOK
	}
	return status
}

// keptWriter remembers the status and body a handler answered with, a
// little of it: a write's answer is one item.
type keptWriter struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (w *keptWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *keptWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.body.Len() < 1<<20 {
		w.body.Write(b)
	}
	return w.ResponseWriter.Write(b)
}
