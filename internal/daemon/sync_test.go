package daemon

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// syncPair pairs peer with owner, which shares hello-stack with it, and
// returns the pairing as each side knows it.
func syncPair(t *testing.T, owner, peer testDaemon, name string) (api.ShareRemote, api.SharePeer) {
	t.Helper()
	ctx := context.Background()
	owner.srv.lan.mu.Lock()
	owner.srv.lan.own = true
	owner.srv.lan.addresses = func() []string { return []string{"127.0.0.1"} }
	owner.srv.lan.mu.Unlock()
	port, on := freePort(t), true
	if _, err := owner.client.UpdateLAN(ctx, api.UpdateLANRequest{Enabled: &on, Port: &port}); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.client.UpdateShare(ctx, api.UpdateShareRequest{Name: &name}); err != nil {
		t.Fatal(err)
	}
	pairing, err := owner.client.PairShare(ctx)
	if err != nil {
		t.Fatal(err)
	}
	remote, err := peer.client.AddShareRemote(ctx, pairing.Link)
	if err != nil {
		t.Fatal(err)
	}
	st, err := owner.client.Share(ctx)
	if err != nil || len(st.Peers) == 0 {
		t.Fatalf("Share() = %+v, %v", st, err)
	}
	p, err := owner.client.UpdateSharePeer(ctx, st.Peers[0].ID, api.UpdateSharePeerRequest{Projects: &[]string{"hello-stack"}})
	if err != nil {
		t.Fatal(err)
	}
	return remote, p
}

func addMemory(t *testing.T, d testDaemon, id, title, content string) {
	t.Helper()
	if _, err := d.srv.store.DB().Exec(`INSERT INTO memories (id, project, kind, title, content, created_at, updated_at, referenced_at)
		VALUES (?, 'hello-stack', 'fact', ?, ?, 1, ?, ?)`, id, title, content, len(d.root), len(d.root)); err != nil {
		t.Fatal(err)
	}
}

func memoryContent(t *testing.T, d testDaemon, id string) string {
	t.Helper()
	var content string
	if err := d.srv.store.DB().QueryRow(`SELECT content FROM memories WHERE id = ?`, id).Scan(&content); err != nil {
		t.Fatalf("memory %s: %v", id, err)
	}
	return content
}

func syncPlanOf(t *testing.T, d testDaemon, remotes ...string) api.SyncPlan {
	t.Helper()
	j, err := d.client.PlanSync(context.Background(), api.SyncPlanRequest{Remotes: remotes})
	if err != nil {
		t.Fatal(err)
	}
	var plan api.SyncPlan
	if err := json.Unmarshal(finishedJob(t, d, j).Result, &plan); err != nil {
		t.Fatal(err)
	}
	return plan
}

func syncNow(t *testing.T, d testDaemon, req api.SyncRequest) api.SyncReport {
	t.Helper()
	j, err := d.client.Sync(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	var report api.SyncReport
	if err := json.Unmarshal(finishedJob(t, d, j).Result, &report); err != nil {
		t.Fatal(err)
	}
	return report
}

// A sync merges a project's memories both ways, asks about those both sides
// have with other text, leaves what the user unticked out, and finds nothing
// to do the second time.
func TestSyncWithAPairedAgentBox(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	owner := startTestDaemon(t, t.TempDir(), fakeIncus)
	peer := startTestDaemon(t, t.TempDir(), fakeIncus)
	for _, d := range []testDaemon{owner, peer} {
		if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: d.fixtureRepo(t, "hello-stack")}); err != nil {
			t.Fatal(err)
		}
	}
	if err := owner.srv.store.AddProject(ctx, state.Project{Name: "private", Root: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if _, err := peer.client.AddProject(ctx, api.AddProjectRequest{Path: peer.fixtureRepo(t, "hello-android")}); err != nil {
		t.Fatal(err)
	}
	addMemory(t, owner, "mem_owner", "Only the owner knows", "from the owner")
	addMemory(t, peer, "mem_peer", "Only the peer knows", "from the peer")
	addMemory(t, peer, "mem_left_out", "In a project left out", "stays")
	if _, err := peer.srv.store.DB().Exec(`UPDATE memories SET project = 'hello-android' WHERE id = 'mem_left_out'`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"mem_take_theirs", "mem_keep_mine"} {
		addMemory(t, owner, id, "Both know "+id, "the owner's text")
		addMemory(t, peer, id, "Both know "+id, "the peer's text")
	}
	// A secret both have, with other values, and one only the owner has.
	for _, c := range []struct {
		d           testDaemon
		name, value string
	}{{owner, "STRIPE_KEY", "sk_owner"}, {peer, "STRIPE_KEY", "sk_peer"}, {owner, "ONLY_OWNER", "x"}} {
		if _, err := c.d.srv.secrets().Set(ctx, "hello-stack", "", c.name, c.value); err != nil {
			t.Fatal(err)
		}
	}
	remote, p := syncPair(t, owner, peer, "Owner")

	// Not until the owner says so.
	plan := syncPlanOf(t, peer)
	if len(plan.Remotes) != 1 || !strings.Contains(plan.Remotes[0].Error, "May sync") {
		t.Fatalf("a plan before the owner allowed syncing = %+v; want its remote refused", plan.Remotes)
	}
	if _, err := peer.client.Sync(ctx, api.SyncRequest{Plan: plan.ID}); err == nil {
		t.Error("a sync with no AgentBox that allows it started")
	}
	on := true
	if p, err := owner.client.UpdateSharePeer(ctx, p.ID, api.UpdateSharePeerRequest{Sync: &on}); err != nil || !p.Sync {
		t.Fatalf("UpdateSharePeer(sync) = %+v, %v", p, err)
	}

	plan = syncPlanOf(t, peer)
	if plan.Remotes[0].Error != "" || plan.Remotes[0].Incoming == 0 {
		t.Fatalf("the plan's remote = %+v; want something incoming", plan.Remotes[0])
	}
	var ids []string
	for _, c := range plan.Conflicts {
		ids = append(ids, c.ID)
		if c.Kind != "memory" || c.Project != "hello-stack" || len(c.Versions) != 1 || c.Versions[0].Remote != remote.ID ||
			len(c.Versions[0].Fields) != 1 || c.Versions[0].Fields[0] != (api.TransferConflictField{Name: "content", Mine: "the peer's text", Theirs: "the owner's text"}) {
			t.Errorf("conflict = %+v", c)
		}
	}
	theirs, mine := "project/hello-stack/memory#memories/mem_take_theirs", "project/hello-stack/memory#memories/mem_keep_mine"
	if len(ids) != 2 || !strings.Contains(strings.Join(ids, " "), theirs) || !strings.Contains(strings.Join(ids, " "), mine) {
		t.Fatalf("conflicts = %v; want the two memories both wrote", ids)
	}
	var names []string
	for _, pr := range plan.Projects {
		names = append(names, pr.Name+":"+strconv.FormatBool(pr.Here)+":"+strconv.Itoa(len(pr.On)))
	}
	if got := strings.Join(names, " "); got != "hello-android:true:0 hello-stack:true:1" {
		t.Errorf("the plan's projects = %s; want the peer's two, one of them on the owner, and not the owner's private one", got)
	}
	for _, pr := range plan.Projects {
		if want := map[string][]string{"hello-stack": {"STRIPE_KEY"}}[pr.Name]; !slices.Equal(pr.Secrets, want) {
			t.Errorf("the plan's secrets both have for %s = %v, want %v", pr.Name, pr.Secrets, want)
		}
	}
	// Planning changed nothing.
	if got := memoryContent(t, peer, "mem_take_theirs"); got != "the peer's text" {
		t.Errorf("after the plan, the peer's memory = %q", got)
	}

	report := syncNow(t, peer, api.SyncRequest{Plan: plan.ID, Projects: &[]string{"hello-stack"}, Keep: map[string]string{theirs: remote.ID}})
	if len(report.Remotes) != 1 || report.Remotes[0].Error != "" {
		t.Fatalf("report = %+v", report)
	}
	if r := report.Remotes[0]; r.Received == 0 || r.Replaced != 1 || r.Sent == 0 || r.SentOver != 1 || r.Left != 0 {
		t.Errorf("report = %+v; want things both ways, one replaced on each side and nothing left", r)
	}
	for _, c := range []struct {
		d        testDaemon
		id, want string
	}{
		{peer, "mem_owner", "from the owner"}, {owner, "mem_peer", "from the peer"},
		{peer, "mem_take_theirs", "the owner's text"}, {owner, "mem_take_theirs", "the owner's text"},
		{peer, "mem_keep_mine", "the peer's text"}, {owner, "mem_keep_mine", "the peer's text"},
	} {
		if got := memoryContent(t, c.d, c.id); got != c.want {
			t.Errorf("after the sync, %s = %q, want %q", c.id, got, c.want)
		}
	}
	var n int
	if err := owner.srv.store.DB().QueryRow(`SELECT count(*) FROM memories WHERE id = 'mem_left_out'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("the owner has %d memories of the project left out (%v)", n, err)
	}
	if _, err := owner.srv.store.Project(ctx, "hello-android"); err == nil {
		t.Error("the owner got the project left out")
	}
	if _, err := peer.client.Sync(ctx, api.SyncRequest{Plan: plan.ID}); err == nil {
		t.Error("a plan was carried out twice")
	}
	// Secrets stayed where they were.
	if v, err := peer.srv.secrets().Resolve(ctx, "hello-stack", "", "STRIPE_KEY"); err != nil || v != "sk_peer" {
		t.Errorf("the peer's secret after the sync = %q, %v", v, err)
	}
	if v, err := owner.srv.secrets().Resolve(ctx, "hello-stack", "", "STRIPE_KEY"); err != nil || v != "sk_owner" {
		t.Errorf("the owner's secret after the sync = %q, %v", v, err)
	}
	if _, err := peer.srv.secrets().Resolve(ctx, "hello-stack", "", "ONLY_OWNER"); err == nil {
		t.Error("the peer got a secret only the owner had")
	}

	// Nothing differs any more.
	plan = syncPlanOf(t, peer, remote.ID)
	if len(plan.Conflicts) != 0 || plan.Remotes[0].Incoming != 0 {
		t.Errorf("a second plan = %d conflicts, %d incoming; want none: %+v", len(plan.Conflicts), plan.Remotes[0].Incoming, plan.Conflicts)
	}
	report = syncNow(t, peer, api.SyncRequest{Plan: plan.ID, Projects: &[]string{"hello-stack"}})
	if r := report.Remotes[0]; r.Received+r.Replaced+r.Sent+r.SentOver+r.Left != 0 {
		t.Errorf("a second sync = %+v; want nothing done", r)
	}
}

// An owner merges only into the projects it shares with a peer, and what it
// has with other values that the peer wasn't asked about stays.
func TestSyncPushIsHeldToWhatIsShared(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	owner := startTestDaemon(t, t.TempDir(), fakeIncus)
	peer := startTestDaemon(t, t.TempDir(), fakeIncus)
	for _, d := range []testDaemon{owner, peer} {
		for _, name := range []string{"hello-stack", "hello-android"} {
			if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: d.fixtureRepo(t, name)}); err != nil {
				t.Fatal(err)
			}
		}
	}
	addMemory(t, peer, "mem_unshared", "For a project not shared", "from the peer")
	if _, err := peer.srv.store.DB().Exec(`UPDATE memories SET project = 'hello-android' WHERE id = 'mem_unshared'`); err != nil {
		t.Fatal(err)
	}
	_, p := syncPair(t, owner, peer, "Owner")
	on := true
	if _, err := owner.client.UpdateSharePeer(ctx, p.ID, api.UpdateSharePeerRequest{Sync: &on}); err != nil {
		t.Fatal(err)
	}
	plan := syncPlanOf(t, peer)
	// The owner's changes after the plan: the peer wasn't asked about it.
	addMemory(t, owner, "mem_late", "Written meanwhile", "the owner's text")
	addMemory(t, peer, "mem_late", "Written meanwhile", "the peer's text")
	report := syncNow(t, peer, api.SyncRequest{Plan: plan.ID})
	r := report.Remotes[0]
	if r.Error != "" || r.Left != 1 {
		t.Errorf("report = %+v; want one thing left as it is on the owner", r)
	}
	if got := memoryContent(t, owner, "mem_late"); got != "the owner's text" {
		t.Errorf("the owner's memory the peer wasn't asked about = %q", got)
	}
	var n int
	if err := owner.srv.store.DB().QueryRow(`SELECT count(*) FROM memories WHERE id = 'mem_unshared'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("the owner took %d memories into a project it doesn't share (%v)", n, err)
	}
	if !strings.Contains(strings.Join(r.Notes, "\n"), "hello-android wasn't synced on Owner") {
		t.Errorf("notes = %q; want why hello-android wasn't synced", r.Notes)
	}
}
