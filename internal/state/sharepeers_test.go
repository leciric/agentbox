package state

import (
	"context"
	"testing"
	"time"
)

// A peer reaches the projects it was given, or every one of this machine's
// own; never one another AgentBox shares with this one, which two AgentBoxes
// sharing everything with each other would pass back and forth for good.
func TestSharePeerShares(t *testing.T) {
	t.Parallel()
	all := SharePeer{AllProjects: true}
	some := SharePeer{Projects: []string{"web", "r1~api"}}
	for _, c := range []struct {
		peer SharePeer
		slug string
		want bool
	}{
		{all, "web", true},
		{all, "r1" + RemoteProjectSep + "web", false},
		{all, HomeProject, false},
		{all, "", false},
		{some, "web", true},
		{some, "api", false},
		{some, "r1~api", false},
	} {
		if got := c.peer.Shares(c.slug); got != c.want {
			t.Errorf("%+v.Shares(%q) = %v, want %v", c.peer, c.slug, got, c.want)
		}
	}
}

// What a peer spent is its agents' tokens and run time, and the tokens of the
// chat turns its messages started: not the owner's own turns, nor another
// peer's.
func TestSharePeerUsage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRemoveTest(t)
	ana, _, err := s.AddSharePeer(ctx, "Ana")
	if err != nil {
		t.Fatal(err)
	}
	bob, _, err := s.AddSharePeer(ctx, "Bob")
	if err != nil {
		t.Fatal(err)
	}
	if ana.Access != ShareAccessFull {
		t.Errorf("a new peer's access = %q, want full", ana.Access)
	}
	if err := s.AddSharePeerAgent(ctx, ana.ID, "web", "agent-01"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddShareRunSeconds(ctx, "web", "agent-01", 120); err != nil {
		t.Fatal(err)
	}
	item := func(id, author string) ChatItem {
		data := `{"id":"` + id + `","kind":"user","turn":"` + id + `"}`
		if author != "" {
			data = `{"id":"` + id + `","kind":"user","turn":"` + id + `","author":{"id":"` + author + `","name":"x"}}`
		}
		return ChatItem{ID: id, Data: []byte(data)}
	}
	if err := s.SaveChatItems(ctx, "web", "lead", []ChatItem{item("t1", ana.ID), item("t2", ""), item("t3", bob.ID)}); err != nil {
		t.Fatal(err)
	}
	rows := []TokenRow{
		{Project: "web", Agent: "agent-01", AI: "claude", Kind: TokensTurn, At: time.Now(), Input: 100, Output: 10, CostUSD: 0.5},
		{Project: "web", Agent: "lead", AI: "claude", Turn: "t1", Kind: TokensTurn, At: time.Now(), Input: 1000, CacheRead: 5},
		{Project: "web", Agent: "lead", AI: "claude", Turn: "t2", Kind: TokensTurn, At: time.Now(), Input: 7777},
		{Project: "web", Agent: "lead", AI: "claude", Turn: "t3", Kind: TokensTurn, At: time.Now(), Input: 9999},
	}
	if err := s.AddTokenRows(ctx, rows); err != nil {
		t.Fatal(err)
	}
	u, err := s.SharePeerUsage(ctx, ana.ID)
	if err != nil || u.Tokens != 1115 || u.CostUSD != 0.5 || u.RunSeconds != 120 {
		t.Errorf("Ana's usage = %+v, %v; want 1115 tokens, $0.50, 120 s", u, err)
	}

	for i := range MaxShareActivity + 3 {
		if _, err := s.AddShareActivity(ctx, ShareActivity{Peer: ana.ID, Kind: "prompt", Project: "web", Agent: "agent-0" + string(rune('0'+i%10))}); err != nil {
			t.Fatal(err)
		}
	}
	acts, err := s.ShareActivities(ctx, ana.ID, 0)
	if err != nil || len(acts) != MaxShareActivity || acts[0].ID < acts[1].ID {
		t.Errorf("Ana's activity = %d items, %v; want the newest %d, newest first", len(acts), err, MaxShareActivity)
	}
	if err := s.SetSharePeerAccess(ctx, ana.ID, "admin"); err == nil {
		t.Error("SetSharePeerAccess(admin) succeeded")
	}
	if err := s.SetSharePeerAccess(ctx, ana.ID, ShareAccessChat); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.SharePeer(ctx, ana.ID); p.Access != ShareAccessChat || p.May(ShareAccessFull) || !p.May(ShareAccessView) {
		t.Errorf("Ana after SetSharePeerAccess(chat) = %+v", p)
	}
	if err := s.RemoveSharePeer(ctx, ana.ID); err != nil {
		t.Fatal(err)
	}
	if acts, _ := s.ShareActivities(ctx, ana.ID, 0); len(acts) != 0 {
		t.Errorf("an unpaired peer's activity = %d items, want none", len(acts))
	}
}
