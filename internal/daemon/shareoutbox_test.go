package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/chat"
	"agentbox/internal/state"
)

// pairSharedChat is pairShared with a project whose chat takes messages: a
// repository with its lead, whose AI tool never starts (the turn fails, the
// message stands).
func pairSharedChat(t *testing.T) sharedPair {
	t.Helper()
	return pairShared(t, func(owner testDaemon) string {
		if _, err := owner.client.AddProject(context.Background(), api.AddProjectRequest{Path: owner.fixtureRepo(t, "hello-stack")}); err != nil {
			t.Fatal(err)
		}
		leadReadyToChat(t, owner, "hello-stack")
		owner.srv.chat.Launch = func(context.Context, state.Agent, func(string)) (*chat.Process, error) {
			return nil, errors.New("this test starts no AI tool")
		}
		return "hello-stack"
	})
}

// setOwnerReachable opens or closes the owner's phone page, which its share
// gateway is on: closed, the peer can't reach it at all.
func (sp sharedPair) setOwnerReachable(t *testing.T, on bool) {
	t.Helper()
	port := sp.port
	if _, err := sp.owner.client.UpdateLAN(context.Background(), api.UpdateLANRequest{Enabled: &on, Port: &port}); err != nil {
		t.Fatal(err)
	}
}

func remoteAway(d testDaemon, id string) bool {
	c := d.srv.remoteConn(id)
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.away
}

func userTexts(t *testing.T, d testDaemon, ref string) []string {
	t.Helper()
	th, err := d.client.Chat(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, it := range th.Items {
		if it.Kind == "user" || it.Kind == "aside" {
			out = append(out, it.Text)
		}
	}
	return out
}

// Two daemons: the owner goes away; what the peer writes in its project
// meanwhile is kept, shown as waiting, survives the peer's restart, and
// reaches the owner once, in order, when it is back. What the owner refuses
// then stays, with why; what was taken back never goes.
func TestShareOutboxWhileTheOwnerIsAway(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sp := pairSharedChat(t)
	if err := sp.owner.srv.store.AddQuestion(ctx, state.Question{ID: "q-1", Project: "hello-stack", Agent: "agent-01", Kind: state.QuestionDecision, Text: "Which colour?", Status: state.QuestionEscalated, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	main := api.SessionRef(sp.local, state.MigratedSession)
	ownerMain := api.SessionRef("hello-stack", state.MigratedSession)

	sp.setOwnerReachable(t, false)
	waitFor(t, "the peer to see the owner away", func() bool { return remoteAway(sp.peer, sp.remote.ID) })

	app := newPhone(t, "http://agentbox", sp.peer.client.HTTPClient().Transport)
	chatPath := "/v1/projects/" + sp.local + "/sessions/" + state.MigratedSession + "/chat/messages"
	send := func(path, body string) api.ChatItem {
		t.Helper()
		code, got := app.do("POST", path, body)
		var it api.ChatItem
		if code != http.StatusAccepted || json.Unmarshal([]byte(got), &it) != nil {
			t.Fatalf("POST %s with the owner away = %d %s; want it kept", path, code, got)
		}
		return it
	}
	first := send(chatPath, `{"text":"First, the footer."}`)
	if first.Delivery != api.ChatAsideAway || !strings.HasPrefix(first.ID, api.ShareQueuedPrefix) || first.Kind != "user" || first.Text != "First, the footer." {
		t.Errorf("a message kept for the owner = %+v; want a user item, away", first)
	}
	send("/v1/agents/"+sp.local+"/agent-09/chat/messages", `{"text":"Are you there?"}`) // no such agent on the owner
	if code, got := app.do("POST", "/v1/projects/"+sp.local+"/questions/q-1/answer", `{"answer":"Blue."}`); code != http.StatusAccepted || !strings.Contains(got, `"kind":"answer"`) {
		t.Fatalf("an answer with the owner away = %d %s; want it kept", code, got)
	}
	gone := send(chatPath, `{"text":"Never mind."}`)
	send(chatPath, `{"text":"Then the header."}`)
	id, _ := strconv.ParseInt(strings.TrimPrefix(gone.ID, api.ShareQueuedPrefix), 10, 64)
	if err := sp.peer.client.CancelShareQueued(ctx, id); err != nil {
		t.Fatalf("taking back a kept message: %v", err)
	}
	// What isn't a message, a prompt or an answer is refused, as before.
	if code, _ := app.do("POST", "/v1/projects/"+sp.local+"/sessions/"+state.MigratedSession+"/chat/cancel", "{}"); code == http.StatusAccepted || code/100 == 2 {
		t.Errorf("stopping a turn with the owner away = %d; want refused", code)
	}

	box, err := sp.peer.client.ShareOutbox(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, q := range box {
		texts = append(texts, q.Kind+":"+q.Text)
		if q.Owner != "Alice" || q.Error != "" || q.Project != sp.local {
			t.Errorf("kept %+v; want waiting for Alice, in %s", q, sp.local)
		}
	}
	want := []string{"message:First, the footer.", "prompt:Are you there?", "answer:Blue.", "message:Then the header."}
	if strings.Join(texts, "|") != strings.Join(want, "|") {
		t.Errorf("the outbox = %q; want %q", texts, want)
	}
	if box[0].Chat != main || box[1].Chat != sp.local+"/agent-09" || box[2].Question != "q-1" || box[2].Chat != "" {
		t.Errorf("where each shows = %+v", box)
	}

	// The peer stops and starts again while the owner is still away: what
	// it kept is still there.
	sp.peer.stop()
	sp.peer = startTestDaemon(t, sp.peer.root, fakeIncus)
	if again, err := sp.peer.client.ShareOutbox(ctx); err != nil || len(again) != len(want) {
		t.Fatalf("the outbox after the peer's restart = %+v, %v; want the %d kept", again, err, len(want))
	}
	waitFor(t, "the restarted peer to see the owner away", func() bool { return remoteAway(sp.peer, sp.remote.ID) })
	if left := userTexts(t, sp.owner, ownerMain); len(left) != 0 {
		t.Fatalf("the owner's chat with the owner away = %q; want nothing yet", left)
	}

	// The owner is back: everything goes, once, in order; the prompt to an
	// agent it doesn't have is refused, and stays with why.
	sp.setOwnerReachable(t, true)
	var left []api.ShareQueued
	waitFor(t, "the outbox to be sent", func() bool {
		left, err = sp.peer.client.ShareOutbox(ctx)
		return err == nil && len(left) == 1 && left[0].Error != ""
	})
	if left[0].Kind != api.ShareDidPrompt || !strings.Contains(left[0].Error, "Alice refused it") || !strings.Contains(left[0].Error, "agent-09") {
		t.Errorf("what is left = %+v; want the prompt to agent-09, refused with why", left[0])
	}
	got := userTexts(t, sp.owner, ownerMain)
	if strings.Join(got, "|") != "First, the footer.|Then the header." {
		t.Errorf("the owner's chat = %q; want the two messages, once each, in order, and not the one taken back", got)
	}
	q, err := sp.owner.srv.store.Question(ctx, "q-1")
	if err != nil || q.Status != state.QuestionAnswered || q.Answer != "Blue." || q.AuthorID != sp.peerID {
		t.Errorf("the question on the owner = %+v, %v; want answered by the peer", q, err)
	}
	// Taking back what was refused forgets it.
	if err := sp.peer.client.CancelShareQueued(ctx, left[0].ID); err != nil {
		t.Fatal(err)
	}
	if box, _ := sp.peer.client.ShareOutbox(ctx); len(box) != 0 {
		t.Errorf("the outbox after forgetting the refused one = %+v", box)
	}

	// With the owner here, a message goes at once, as it always did.
	code, body := app.do("POST", chatPath, `{"text":"And the sidebar."}`)
	var now api.ChatItem
	if code != http.StatusAccepted || json.Unmarshal([]byte(body), &now) != nil || now.Delivery == api.ChatAsideAway || strings.HasPrefix(now.ID, api.ShareQueuedPrefix) {
		t.Errorf("a message with the owner here = %d %s; want sent", code, body)
	}
	if got := userTexts(t, sp.owner, ownerMain); len(got) != 3 || got[2] != "And the sidebar." {
		t.Errorf("the owner's chat = %q", got)
	}
}

// A peer that may only look has nothing kept: it would be refused.
func TestShareOutboxKeepsNothingAViewerCantSend(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sp := pairShared(t)
	view := state.ShareAccessView
	if _, err := sp.owner.client.UpdateSharePeer(ctx, sp.peerID, api.UpdateSharePeerRequest{Access: &view}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the peer to see it may only look", func() bool {
		_, _ = sp.peer.client.Projects(ctx) // lists the owner's again
		return remoteAccess(sp.peer.srv.remoteConn(sp.remote.ID), sp.local) == view
	})
	sp.setOwnerReachable(t, false)
	waitFor(t, "the peer to see the owner away", func() bool { return remoteAway(sp.peer, sp.remote.ID) })
	app := newPhone(t, "http://agentbox", sp.peer.client.HTTPClient().Transport)
	if code, body := app.do("POST", "/v1/projects/"+sp.local+"/chat/messages", `{"text":"Hi"}`); code != http.StatusForbidden || !strings.Contains(body, "only look") {
		t.Errorf("a message from a peer that may only look, owner away = %d %s; want refused", code, body)
	}
	if box, _ := sp.peer.client.ShareOutbox(ctx); len(box) != 0 {
		t.Errorf("the outbox = %+v; want nothing kept", box)
	}
}

// The owner does a kept write once however often it arrives: sent again, it
// is answered as the first time.
func TestShareKeyedWriteIsDoneOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sp := pairShared(t)
	if err := sp.owner.srv.store.AddQuestion(ctx, state.Question{ID: "q-2", Project: "shared", Agent: "agent-01", Kind: state.QuestionDecision, Text: "Which?", Status: state.QuestionEscalated, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	path := "/share/api/v1/projects/shared/questions/q-2/answer"
	keyed := append([]string{shareKeyHeader, "k-1"}, sp.bearer...)
	code1, body1 := sp.gw.do("POST", path, `{"answer":"the blue one"}`, keyed...)
	code2, body2 := sp.gw.do("POST", path, `{"answer":"the blue one"}`, keyed...)
	if code1 != http.StatusOK || code2 != code1 || body2 != body1 {
		t.Errorf("an answer sent twice with one key = %d %s, then %d %s; want the same answer", code1, body1, code2, body2)
	}
	// Without the key, or with another, it is a write of its own: the
	// question is answered already.
	if code, body := sp.gw.do("POST", path, `{"answer":"the red one"}`, append([]string{shareKeyHeader, "k-2"}, sp.bearer...)...); code == http.StatusOK {
		t.Errorf("another answer with another key = %d %s; want refused, answered already", code, body)
	}
	acts, err := sp.owner.client.SharePeerActivity(ctx, sp.peerID, 0)
	if err != nil {
		t.Fatal(err)
	}
	answers := 0
	for _, a := range acts {
		if a.Kind == api.ShareDidAnswer {
			answers++
		}
	}
	if answers != 1 {
		t.Errorf("the peer's activity has %d answers; want 1", answers)
	}
}

func TestOwnerAway(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		status int
		body   string
		want   bool
	}{
		{http.StatusOK, `{}`, false},
		{http.StatusForbidden, `{"error":"project x isn't shared with Ana"}`, false},
		{http.StatusNotFound, `{"error":"not found"}`, false},
		{http.StatusUnauthorized, `{"error":"unpaired"}`, false},
		{http.StatusTooManyRequests, `{"error":"too many"}`, true},
		{http.StatusServiceUnavailable, `{"error":"incus isn't answering"}`, true},
		{http.StatusBadGateway, `<html>Bad gateway</html>`, true},
		{http.StatusBadGateway, `{"error":"the agent's machine answered nothing"}`, false},
		{530, `error code: 1033`, true},
		{http.StatusInternalServerError, `{"error":"disk I/O error"}`, false},
	} {
		if got := ownerAway(c.status, []byte(c.body)); got != c.want {
			t.Errorf("ownerAway(%d, %s) = %v; want %v", c.status, c.body, got, c.want)
		}
	}
}
