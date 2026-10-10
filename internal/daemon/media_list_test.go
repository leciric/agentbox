package daemon

import (
	"context"
	"fmt"
	"net/url"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// A Media list read a page at a time comes out newest first, every item
// once, however many share a millisecond, and items added or deleted while
// it's read don't make a later page repeat or skip one.
func TestMediaPagesAreStable(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), oneAgentIncus)
	ctx := context.Background()
	a := addTestAgent(t, d)
	base := time.Now().Add(-time.Hour)
	for i := range 95 {
		// Five at a time in the same millisecond.
		item := state.Media{ID: fmt.Sprintf("item-%03d", i), Project: a.Project, Agent: a.Name, Kind: "note", Name: fmt.Sprint(i),
			Source: "agent", Meta: "{}", CreatedAt: base.Add(time.Duration(i/5) * time.Second)}
		if err := d.srv.store.AddMedia(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	path := "/v1/projects/" + a.Project + "/media"
	var ids []string
	cursor := ""
	for page := 0; ; page++ {
		got, err := d.client.MediaPage(ctx, path, url.Values{"limit": {"40"}, "cursor": {cursor}})
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Items) > 40 {
			t.Fatalf("page %d has %d items, want at most 40", page, len(got.Items))
		}
		for _, it := range got.Items {
			ids = append(ids, it.ID)
		}
		if page == 0 {
			// Something new arrives, and an item further down goes, mid-scroll.
			if err := d.srv.store.AddMedia(ctx, state.Media{ID: "new", Project: a.Project, Agent: a.Name, Kind: "note", Name: "new",
				Source: "agent", Meta: "{}", CreatedAt: time.Now()}); err != nil {
				t.Fatal(err)
			}
			if err := d.srv.store.DeleteMedia(ctx, "item-010"); err != nil {
				t.Fatal(err)
			}
		}
		if got.Next == "" {
			break
		}
		cursor = got.Next
	}
	if len(ids) != 94 {
		t.Fatalf("read %d items, want the 94 there were less the one deleted", len(ids))
	}
	for i, id := range ids {
		if want := fmt.Sprintf("item-%03d", 94-i-boolInt(94-i <= 10)); id != want {
			t.Fatalf("item %d = %s, want %s", i, id, want)
		}
	}

	if _, err := d.client.MediaPage(ctx, path, url.Values{"limit": {"40"}, "cursor": {"nonsense"}}); err == nil {
		t.Error("a cursor the daemon didn't make was accepted")
	}
	// Without limit it's every item at once, as the CLI reads it.
	all, err := d.client.ProjectMedia(ctx, a.Project, "", "")
	if err != nil || len(all) != 95 {
		t.Errorf("ProjectMedia() = %d items, %v; want 95", len(all), err)
	}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Filters and the search are applied by the daemon, so a page holds only
// what they keep, and the counts on the filters come from every item.
func TestMediaListFiltersSearchAndCounts(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), oneAgentIncus)
	ctx := context.Background()
	a := addTestAgent(t, d)
	other := addSecondTestAgent(t, d, a.Project)
	at := time.Now().Add(-time.Hour)
	add := func(agent, id, kind, name string, size int64, favorite bool) {
		t.Helper()
		at = at.Add(time.Second)
		if err := d.srv.store.AddMedia(ctx, state.Media{ID: id, Project: a.Project, Agent: agent, Kind: kind, Name: name, Size: size,
			Source: "agent", Meta: "{}", CreatedAt: at, Favorite: favorite}); err != nil {
			t.Fatal(err)
		}
	}
	add(a.Name, "s1", "screenshot", "Résumé page", 10, false)
	add(a.Name, "s2", "screenshot", "checkout", 20, true)
	add(a.Name, "r1", "recording", "checkout demo", 30, false)
	add(other.Name, "s3", "screenshot", "login", 40, false)
	add(other.Name, "n1", "note", "a note", 0, false)
	if err := d.srv.store.AddNotification(ctx, state.Notification{ID: "n-1", Project: a.Project, Agent: a.Name, Kind: "media", MediaID: "r1",
		CreatedAt: time.Now(), Data: []byte("{}")}); err != nil {
		t.Fatal(err)
	}

	ids := func(q url.Values) []string {
		t.Helper()
		q.Set("limit", "40")
		page, err := d.client.MediaPage(ctx, "/v1/media", q)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, it := range page.Items {
			out = append(out, it.ID)
		}
		return out
	}
	shown := url.Values{"kind": {"screenshot,recording"}}
	for _, tc := range []struct {
		name string
		q    url.Values
		want string
	}{
		{"shown kinds", shown, "[s3 r1 s2 s1]"},
		{"one agent", url.Values{"kind": shown["kind"], "project": {a.Project}, "agent": {a.Name}}, "[r1 s2 s1]"},
		{"only screenshots", url.Values{"kind": shown["kind"], "only": {"screenshot"}}, "[s3 s2 s1]"},
		{"only a kind not shown", url.Values{"kind": shown["kind"], "only": {"note"}}, "[]"},
		{"favorites", url.Values{"favorite": {"1"}}, "[s2]"},
		{"unseen", url.Values{"unseen": {"1"}}, "[r1]"},
		{"search, accents ignored", url.Values{"q": {"resume"}}, "[s1]"},
		{"search by type", url.Values{"q": {"video checkout"}}, "[r1]"},
		{"search a phrase", url.Values{"q": {`"checkout demo"`}}, "[r1]"},
		{"search by agent", url.Values{"q": {other.Name}}, "[n1 s3]"},
	} {
		if got := fmt.Sprint(ids(tc.q)); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}

	page, err := d.client.MediaPage(ctx, "/v1/media", url.Values{"limit": {"1"}, "unseen": {"1"}})
	if err != nil || len(page.Items) != 1 || !page.Items[0].Unseen || page.Items[0].AgentName != a.Name {
		t.Errorf("an unseen item = %+v, %v; want it marked unseen and labelled with its agent", page, err)
	}

	counts, err := d.client.MediaCounts(ctx, "/v1/media", url.Values{"kind": shown["kind"], "project": {a.Project}, "agent": {a.Name}, "q": {"checkout"}})
	if err != nil {
		t.Fatal(err)
	}
	if counts.Total != 3 || counts.Bytes != 60 || counts.Kinds["screenshot"] != 2 || counts.Kinds["recording"] != 1 || counts.Favorites != 1 || counts.Unseen != 1 {
		t.Errorf("counts in the agent = %+v", counts)
	}
	if counts.Matching != 2 || counts.MatchingBytes != 50 {
		t.Errorf("matching = %d (%d bytes), want the 2 checkouts' 50", counts.Matching, counts.MatchingBytes)
	}
	if len(counts.Agents) != 2 || counts.Agents[0].Name != other.Name || counts.Agents[0].Count != 1 || counts.Agents[1].Count != 3 {
		t.Errorf("agents = %+v, want both, newest first, the note left out", counts.Agents)
	}
	if len(counts.Projects) != 1 || counts.Projects[0].Count != 4 {
		t.Errorf("projects = %+v, want one with 4 shown items", counts.Projects)
	}

	agentCounts, err := d.client.MediaCounts(ctx, "/v1/agents/"+other.Ref()+"/media", url.Values{})
	if err != nil || agentCounts.Total != 2 || agentCounts.Matching != 2 || agentCounts.Kinds["note"] != 1 {
		t.Errorf("an agent's counts = %+v, %v", agentCounts, err)
	}

	// "Delete all" with a search takes what the search keeps, loaded or not.
	got, err := d.client.DeleteProjectMedia(ctx, a.Project, api.DeleteMediaRequest{All: true, Query: "checkout"})
	if err != nil || got.Deleted != 2 {
		t.Fatalf("deleting all of a search = %+v, %v; want the 2 checkouts", got, err)
	}
	if left := fmt.Sprint(ids(url.Values{})); left != "[n1 s3 s1]" {
		t.Errorf("left = %s", left)
	}
}
