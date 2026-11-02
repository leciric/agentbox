package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"
	_ "time/tzdata" // a phone's quiet hours are in its own time zone, which the machine may have no file for

	"agentbox/internal/api"
	"agentbox/internal/push"
	"agentbox/internal/state"
)

// Push notifications to the paired phones' app (mobile/), for what the
// desktop would have the user look at: an agent finished, an agent or the
// lead needs them, a pull request is ready to review, a pull request's checks
// turned red. The app registers its Expo push token on the network port
// (PUT /lan/push, only the phone itself), with which kinds it wants and its
// quiet hours; the daemon posts to Expo's push service itself
// (internal/push), which reaches the phone wherever it is. Nothing goes
// through the hub, which has no push relay: Expo holds the app's APNs and FCM
// credentials, so none is needed.
//
// What is pushed stays within the phone's scopes: only a phone that may read
// gets anything, and only while phones are on. A push carries the project,
// the agent and the pull request, never more than its title and a line, and
// the app opens that agent's chat, or the project's review, on a tap. Quiet
// hours drop what happens in them rather than hold it: the app shows it all
// when opened, and a pile of notifications at 7:00 helps nobody.

// phonePushState is the daemon's side of pushing. The zero value posts to
// Expo.
type phonePushState struct {
	client push.Client
	// now is the clock quiet hours are read against; nil is time.Now.
	now func() time.Time
	// sent is what a single push was already sent for, by key, such as a
	// permission request the chat reports again as it changes.
	mu   sync.Mutex
	sent map[string]time.Time
}

// pushNote is one thing to push, to every phone that wants its kind.
type pushNote struct {
	kind, title, body string
	project, agent    string
	pr                int
}

// data is what the notification carries for the app: what to open.
func (n pushNote) data() map[string]string {
	ref := n.project
	if n.agent != "" {
		ref += "/" + n.agent
	}
	d := map[string]string{"kind": n.kind, "ref": ref}
	if n.pr > 0 {
		d["pr"] = strconv.Itoa(n.pr)
	}
	return d
}

func (n pushNote) message(token string) push.Message {
	ref := n.data()["ref"]
	return push.Message{
		To: token, Title: n.title, Body: n.body, Data: n.data(),
		Sound: "default", Priority: "high", ChannelID: "default", ThreadID: ref,
	}
}

// pushPhones sends n to the phones that want it, in the background: a push
// is never what the thing it reports waits on.
func (s *Server) pushPhones(n pushNote) {
	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(s.runCtx), 30*time.Second)
		defer cancel()
		s.sendPushes(ctx, n)
	}()
}

// pushOnce is pushPhones for what may be reported more than once, by key:
// it pushes only the first time in a day.
func (s *Server) pushOnce(key string, n pushNote) {
	p := &s.phonePush
	p.mu.Lock()
	now := time.Now()
	if p.sent == nil {
		p.sent = map[string]time.Time{}
	}
	for k, at := range p.sent {
		if now.Sub(at) > 24*time.Hour {
			delete(p.sent, k)
		}
	}
	_, seen := p.sent[key]
	p.sent[key] = now
	p.mu.Unlock()
	if !seen {
		s.pushPhones(n)
	}
}

// sendPushes sends n to every phone that wants it now, and forgets the
// tokens Expo says reach nobody. It returns how many it sent.
func (s *Server) sendPushes(ctx context.Context, n pushNote) int {
	if on, err := s.store.Flag(ctx, state.SettingLAN); err != nil || !on {
		return 0
	}
	phones, err := s.store.PhonePushes(ctx)
	if err != nil {
		s.logf("push: %v", err)
		return 0
	}
	now := time.Now()
	if s.phonePush.now != nil {
		now = s.phonePush.now()
	}
	var msgs []push.Message
	for _, p := range phones {
		if p.Token != "" && p.Phone.Can(state.PhoneScopeRead) && slices.Contains(p.Kinds, n.kind) && !quietAt(p, now) {
			msgs = append(msgs, n.message(p.Token))
		}
	}
	if len(msgs) == 0 {
		return 0
	}
	return s.deliverPushes(ctx, msgs)
}

// deliverPushes posts msgs to Expo and returns how many it took.
func (s *Server) deliverPushes(ctx context.Context, msgs []push.Message) int {
	tickets, err := s.phonePush.client.Send(ctx, msgs)
	if err != nil {
		s.logf("push: %v", err)
	}
	sent := 0
	for i, t := range tickets {
		switch {
		case t.Status == "ok":
			sent++
		case t.Gone():
			if err := s.store.DropPushToken(ctx, msgs[i].To); err != nil {
				s.logf("push: forgetting a phone's token: %v", err)
			}
		default:
			s.logf("push: Expo refused a notification: %s %s", t.Details.Error, t.Message)
		}
	}
	return sent
}

// quietAt says now is in the phone's quiet hours, read in its time zone.
func quietAt(p state.PhonePush, now time.Time) bool {
	if p.QuietFrom == p.QuietTo {
		return false
	}
	if loc, err := time.LoadLocation(p.TimeZone); err == nil && p.TimeZone != "" {
		now = now.In(loc)
	}
	m := now.Hour()*60 + now.Minute()
	if p.QuietFrom < p.QuietTo {
		return m >= p.QuietFrom && m < p.QuietTo
	}
	return m >= p.QuietFrom || m < p.QuietTo
}

// who is how a push names an agent: its title, or its name without one.
func who(name, title string) string {
	if title != "" {
		return title
	}
	return name
}

// pushFinished is an agent's finish: its pull request ready to review when
// it has one open, or just the finish.
func (s *Server) pushFinished(a state.Agent, ev api.AgentEvent) {
	n := pushNote{kind: api.PushFinished, project: a.Project, agent: a.Name, title: who(a.Name, a.Title) + " finished", body: headWords(ev.Summary, 160)}
	if ev.PR != nil && ev.PR.State == "open" {
		n.kind, n.pr = api.PushPRReady, ev.PR.Number
		n.title = fmt.Sprintf("PR #%d is ready to review", ev.PR.Number)
		n.body = who(a.Name, a.Title)
		if ev.PR.Title != "" {
			n.body += ": " + ev.PR.Title
		}
	}
	s.pushPhones(n)
}

// pushFailed is an agent that stopped on an error.
func (s *Server) pushFailed(a state.Agent, note string) {
	s.pushPhones(pushNote{kind: api.PushFinished, project: a.Project, agent: a.Name, title: who(a.Name, a.Title) + " stopped on an error", body: headWords(note, 160)})
}

// pushQuestion is a question that reached the user (notifyQuestion).
func (s *Server) pushQuestion(q state.Question, title string) {
	s.pushOnce("question/"+q.ID, pushNote{kind: api.PushNeedsYou, project: q.Project, agent: q.Agent, title: who(q.Agent, title) + " needs you", body: headWords(q.Text, 160)})
}

// pushPermission is an agent's permission request, while it waits: the chat
// reports it again as it changes, and once answered.
func (s *Server) pushPermission(a state.Agent, p api.ChatAgentPermission) {
	if p.Permission.Outcome != "" {
		return
	}
	body := p.Permission.Title
	if p.Permission.Detail != "" {
		body = p.Permission.Detail
	}
	s.pushOnce("permission/"+p.Ref+"/"+p.Item, pushNote{kind: api.PushNeedsYou, project: a.Project, agent: a.Name, title: who(a.Name, a.Title) + " asks for permission", body: headWords(body, 160)})
}

// pushChecksFailed is a pull request whose checks just turned red, or are
// red again on a new head. An agent that is gone has no chat to open: the
// tap opens the project's.
func (s *Server) pushChecksFailed(project, agent, title string, here bool, number int, failing string) {
	body := who(agent, title)
	if failing != "" {
		body += ": " + failing
	}
	n := pushNote{kind: api.PushCIFailed, project: project, pr: number, title: fmt.Sprintf("Checks failed on PR #%d", number), body: body}
	if here {
		n.agent = agent
	}
	s.pushPhones(n)
}

// lanPush is GET, PUT and DELETE /lan/push and POST /lan/push/test on the
// network port: the phone's own push notifications.
func (s *Server) lanPush(w http.ResponseWriter, r *http.Request, addr string) {
	p, ok := s.lanPhoneOf(w, r, addr)
	if !ok {
		return
	}
	ctx := r.Context()
	if r.URL.Path == "/lan/push/test" {
		if r.Method != http.MethodPost {
			writeLANError(w, http.StatusMethodNotAllowed, "POST only")
			return
		}
		s.lanPushTest(w, r, p)
		return
	}
	switch r.Method {
	case http.MethodGet:
	case http.MethodPut:
		var req api.PhonePush
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if err := readJSON(r, &req); err != nil {
			writeLANError(w, http.StatusBadRequest, err.Error())
			return
		}
		row, err := phonePushRow(req)
		if err == nil && row.Token == "" {
			// Settings changed without the token: the one registered, if
			// any, stays.
			if cur, cerr := s.store.PhonePushOf(ctx, p.ID); cerr == nil {
				row.Token = cur.Token
			}
		}
		if err != nil {
			writeLANError(w, http.StatusBadRequest, err.Error())
			return
		}
		row.PhoneID = p.ID
		if err := s.store.SetPhonePush(ctx, row); err != nil {
			writeLANError(w, http.StatusInternalServerError, err.Error())
			return
		}
	case http.MethodDelete:
		if err := s.store.RemovePhonePush(ctx, p.ID); err != nil {
			writeLANError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	default:
		writeLANError(w, http.StatusMethodNotAllowed, "GET, PUT or DELETE")
		return
	}
	out := api.PhonePush{Kinds: slices.Clone(api.PushKinds)}
	row, err := s.store.PhonePushOf(ctx, p.ID)
	switch {
	case err == nil:
		out = api.PhonePush{Registered: row.Token != "", Kinds: row.Kinds, QuietFrom: row.QuietFrom, QuietTo: row.QuietTo, TimeZone: row.TimeZone}
	case !errors.Is(err, state.ErrNotFound):
		writeLANError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = writeJSON(w, http.StatusOK, out)
}

// phonePushRow checks what a phone asks for.
func phonePushRow(req api.PhonePush) (state.PhonePush, error) {
	if req.Token != "" && !push.IsToken(req.Token) {
		return state.PhonePush{}, fmt.Errorf("%q isn't an Expo push token", req.Token)
	}
	kinds := []string{}
	for _, k := range api.PushKinds {
		if slices.Contains(req.Kinds, k) {
			kinds = append(kinds, k)
		}
	}
	for _, k := range req.Kinds {
		if !slices.Contains(api.PushKinds, k) {
			return state.PhonePush{}, fmt.Errorf("%q isn't something a phone can be notified about", k)
		}
	}
	const day = 24 * 60
	if req.QuietFrom < 0 || req.QuietFrom >= day || req.QuietTo < 0 || req.QuietTo >= day {
		return state.PhonePush{}, errors.New("quiet hours are minutes after midnight, 0 to 1439")
	}
	if req.TimeZone != "" {
		if _, err := time.LoadLocation(req.TimeZone); err != nil {
			return state.PhonePush{}, fmt.Errorf("%q isn't a time zone", req.TimeZone)
		}
	}
	return state.PhonePush{Token: req.Token, Kinds: kinds, QuietFrom: req.QuietFrom, QuietTo: req.QuietTo, TimeZone: req.TimeZone}, nil
}

// lanPushTest sends the phone a notification now, whatever its kinds and
// quiet hours, and says what it was: the app shows it itself when it couldn't
// be pushed, a build with no EAS project among them.
func (s *Server) lanPushTest(w http.ResponseWriter, r *http.Request, p state.Phone) {
	n := pushNote{kind: "test", title: "AgentBox", body: "Notifications from AgentBox on your computer reach this phone."}
	out := api.PhonePushTest{Title: n.title, Body: n.body, Data: n.data()}
	row, err := s.store.PhonePushOf(r.Context(), p.ID)
	switch {
	case errors.Is(err, state.ErrNotFound) || err == nil && row.Token == "":
		out.Error = "this phone has no push token registered"
	case err != nil:
		writeLANError(w, http.StatusInternalServerError, err.Error())
		return
	default:
		out.Sent = s.deliverPushes(r.Context(), []push.Message{n.message(row.Token)}) == 1
		if !out.Sent {
			out.Error = "Expo's push service didn't take it: see the daemon's log"
		}
	}
	_ = writeJSON(w, http.StatusOK, out)
}
