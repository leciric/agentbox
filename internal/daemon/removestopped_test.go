package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/gitrepo"
	"agentbox/internal/state"
	"agentbox/internal/testutil"
)

// stoppedIncus answers `list` from the instances file, with every volume
// holding 7 GB, and logs deletes.
const stoppedIncus = `case "$1" in
  list) cat "$INCUS_INSTANCES_FILE" ;;
  query)
    case "$2" in
      */state) echo '{"usage":{"used":7000000000}}' ;;
      */snapshots) echo '[]' ;;
      *) echo '{"config":{},"devices":{}}' ;;
    esac ;;
  stop) sed -i "s/\"name\":\"$2\",\"status\":\"[A-Za-z]*\"/\"name\":\"$2\",\"status\":\"Stopped\"/" "$INCUS_INSTANCES_FILE" ;;
  delete) echo "$*" >> "$INCUS_LOG" ;;
esac
exit 0
`

// stoppedInstances is `incus list` for agents, each Stopped unless named in
// running, all last started started.
func stoppedInstances(started time.Time, running map[string]bool, agents ...state.Agent) string {
	var out []string
	for _, a := range agents {
		status := "Stopped"
		if running[a.Name] {
			status = "Running"
		}
		out = append(out, fmt.Sprintf(`{"name":%q,"status":%q,"config":{},"expanded_config":{},"last_used_at":%q}`,
			a.Instance, status, started.UTC().Format(time.RFC3339)))
	}
	return "[" + strings.Join(out, ",") + "]"
}

// stoppedAgent adds an agent made a month ago and stopped stoppedAgo, with
// work as given: "commit", "untracked", "modified" or none.
func stoppedAgent(t *testing.T, d testDaemon, repo gitrepo.Repo, name, work string, stoppedAgo time.Duration) state.Agent {
	t.Helper()
	ctx := context.Background()
	now := time.Now()
	worktree := filepath.Join(t.TempDir(), name)
	branch := "agentbox/" + name
	if err := repo.AddWorktree(worktree, branch, "HEAD", "test"); err != nil {
		t.Fatal(err)
	}
	switch work {
	case "commit", "untracked":
		if err := os.WriteFile(filepath.Join(worktree, "work.txt"), []byte("work"), 0o644); err != nil {
			t.Fatal(err)
		}
		if work == "commit" {
			testutil.Git(t, worktree, "add", "work.txt")
			testutil.Git(t, worktree, "commit", "--quiet", "-m", "work")
		}
	case "modified":
		if err := os.WriteFile(filepath.Join(worktree, "README.md"), []byte("changed"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	base, _ := repo.ResolveCommit("main")
	a := state.Agent{
		Project: "hello-stack", Name: name, Instance: "ab-hello-stack-" + name, AI: "none",
		Branch: branch, BaseRef: "main", BaseCommit: base, Worktree: worktree,
		Status: state.AgentReady, CreatedAt: now.Add(-30 * 24 * time.Hour),
	}
	if err := d.srv.store.AddAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := d.srv.store.SetStoppedAt(ctx, a.Project, a.Name, now.Add(-stoppedAgo)); err != nil {
		t.Fatal(err)
	}
	return a
}

// stoppedProject starts a daemon with the hello-stack project and no
// GitHub, so the only pull request any agent has is none.
func stoppedProject(t *testing.T) (testDaemon, string, gitrepo.Repo) {
	t.Helper()
	d := startTestDaemon(t, t.TempDir(), stoppedIncus)
	root := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(context.Background(), api.AddProjectRequest{Path: root}); err != nil {
		t.Fatal(err)
	}
	repo, err := gitrepo.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	return d, root, repo
}

func agentExists(t *testing.T, d testDaemon, a state.Agent) bool {
	t.Helper()
	_, err := d.srv.store.Agent(context.Background(), a.Project, a.Name)
	return err == nil
}

const day = 24 * time.Hour

func TestStoppedVerdict(t *testing.T) {
	t.Parallel()
	now := time.Now()
	created := now.Add(-30 * day)
	old := now.Add(-10 * day)
	safe := stoppedFacts{Ready: true, State: "stopped", StoppedSince: old, CreatedAt: created}
	pr := func(state string, updated time.Time) *api.PullRequest {
		return &api.PullRequest{Number: 7, State: state, UpdatedAt: &updated}
	}
	with := func(change func(*stoppedFacts)) stoppedFacts {
		f := safe
		change(&f)
		return f
	}
	cases := []struct {
		name   string
		f      stoppedFacts
		remove bool
	}{
		{"no changes and no pull request", safe, true},
		{"merged", with(func(f *stoppedFacts) { f.Changed, f.PR = true, pr("merged", old) }), true},
		{"closed", with(func(f *stoppedFacts) { f.Changed, f.PR = true, pr("closed", old) }), true},
		{"open", with(func(f *stoppedFacts) { f.Changed, f.PR = true, pr("open", old) }), false},
		{"open, no changes", with(func(f *stoppedFacts) { f.PR = pr("open", old) }), false},
		{"merged before the agent existed", with(func(f *stoppedFacts) { f.Changed, f.PR = true, pr("merged", created.Add(-day)) }), false},
		{"changes and no pull request", with(func(f *stoppedFacts) { f.Changed = true }), false},
		{"uncommitted or untracked", with(func(f *stoppedFacts) { f.Dirty = true }), false},
		{"uncommitted, merged", with(func(f *stoppedFacts) { f.Dirty, f.PR = true, pr("merged", old) }), false},
		{"unpushed", with(func(f *stoppedFacts) { f.Unpushed = true }), false},
		{"unpushed, merged", with(func(f *stoppedFacts) { f.Unpushed, f.PR = true, pr("merged", old) }), false},
		{"stopped six days ago", with(func(f *stoppedFacts) { f.StoppedSince = now.Add(-6 * day) }), false},
		{"stopped exactly seven days ago", with(func(f *stoppedFacts) { f.StoppedSince = now.Add(-7 * day) }), true},
		{"running", with(func(f *stoppedFacts) { f.State = "running" }), false},
		{"paused", with(func(f *stoppedFacts) { f.State = "paused" }), false},
		{"busy chat", with(func(f *stoppedFacts) { f.Busy = true }), false},
		{"being destroyed", with(func(f *stoppedFacts) { f.Ready = false }), false},
	}
	for _, c := range cases {
		remove, keep := stoppedVerdict(c.f, now, 7)
		if (remove != "") != c.remove || (remove == "") == (keep == "") {
			t.Errorf("%s: remove %q, keep %q; want removed %v and exactly one reason", c.name, remove, keep, c.remove)
		}
	}
}

// Off, nothing is removed; on, only the agent stopped long enough with
// nothing to lose is, and the list says the same before it happens.
func TestRemoveStoppedMachines(t *testing.T) {
	t.Parallel()
	d, _, repo := stoppedProject(t)
	ctx := context.Background()
	nothing := stoppedAgent(t, d, repo, "nothing", "", 10*day)
	recent := stoppedAgent(t, d, repo, "recent", "", 2*day)
	untracked := stoppedAgent(t, d, repo, "untracked", "untracked", 10*day)
	modified := stoppedAgent(t, d, repo, "modified", "modified", 10*day)
	committed := stoppedAgent(t, d, repo, "committed", "commit", 10*day)
	running := stoppedAgent(t, d, repo, "running", "", 10*day)
	all := []state.Agent{nothing, recent, untracked, modified, committed, running}
	d.setInstances(t, stoppedInstances(time.Now().Add(-20*day), map[string]bool{"running": true}, all...))

	list, err := d.client.StoppedMachines(ctx, "hello-stack", true)
	if err != nil {
		t.Fatal(err)
	}
	if list.Days != state.DefaultRemoveStoppedDays || len(list.Machines) != 5 {
		t.Fatalf("list = %+v, want the five stopped agents against %d days", list, state.DefaultRemoveStoppedDays)
	}
	for _, sm := range list.Machines {
		if want := sm.Name == "nothing"; (sm.Remove != "") != want || (sm.Remove == "") == (sm.Keep == "") {
			t.Errorf("%s: remove %q, keep %q; want removed %v", sm.Name, sm.Remove, sm.Keep, want)
		}
		if sm.Machine == nil || *sm.Machine != 7e9 {
			t.Errorf("%s: machine = %v, want 7 GB", sm.Name, sm.Machine)
		}
	}
	if plain, err := d.client.StoppedMachines(ctx, "", false); err != nil || len(plain.Machines) != 5 || plain.Machines[0].Keep != "" || plain.Machines[0].Remove != "" {
		t.Errorf("unchecked list = %+v, %v; want five with no verdicts", plain, err)
	}

	d.srv.removeStoppedMachines(ctx, time.Now())
	for _, a := range all {
		if !agentExists(t, d, a) {
			t.Errorf("with the setting off, %s was removed", a.Name)
		}
	}

	on, days := true, 7
	if _, err := d.client.UpdateSettings(ctx, api.UpdateSettingsRequest{RemoveStopped: &on, RemoveStoppedDays: &days}); err != nil {
		t.Fatal(err)
	}
	d.srv.removeStoppedMachines(ctx, time.Now())
	if agentExists(t, d, nothing) {
		t.Error("the agent stopped for ten days with nothing is still there")
	}
	if _, err := os.Stat(nothing.Worktree); err == nil {
		t.Error("its worktree is still there")
	}
	if log, _ := os.ReadFile(filepath.Join(d.root, "incus.log")); !strings.Contains(string(log), nothing.Instance) {
		t.Errorf("its machine wasn't deleted: %s", log)
	}
	for _, a := range all[1:] {
		if !agentExists(t, d, a) {
			t.Errorf("%s was removed", a.Name)
		}
	}
	if _, err := os.Stat(filepath.Join(untracked.Worktree, "work.txt")); err != nil {
		t.Errorf("the untracked work is gone: %v", err)
	}
}

// "Remove these now" removes only what is still safe on a fresh look, and
// says why it kept the rest.
func TestRemoveStoppedNowRechecks(t *testing.T) {
	t.Parallel()
	d, root, repo := stoppedProject(t)
	ctx := context.Background()
	a := stoppedAgent(t, d, repo, "nothing", "", 10*day)
	b := stoppedAgent(t, d, repo, "later", "", 10*day)
	d.setInstances(t, stoppedInstances(time.Now().Add(-20*day), nil, a, b))
	list, err := d.client.StoppedMachines(ctx, "", true)
	if err != nil || len(list.Machines) != 2 || list.Machines[0].Remove == "" || list.Machines[1].Remove == "" {
		t.Fatalf("list = %+v, %v; want both removable", list, err)
	}

	// Since the list was read: work appeared in one, git fails in the other.
	if err := os.WriteFile(filepath.Join(b.Worktree, "new.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, fault := range readFaults {
		undo := gitrepo.FailUnder(a.Worktree, fault)
		res, err := d.client.RemoveStopped(ctx, api.RemoveStoppedRequest{Agents: []string{a.Ref(), b.Ref()}})
		undo()
		if err != nil || len(res.Removed) != 0 || len(res.Kept) != 2 {
			t.Fatalf("with %v: %+v, %v; want both kept", fault, res, err)
		}
		u := gitrepo.FailUnder(root, fault)
		res, err = d.client.RemoveStopped(ctx, api.RemoveStoppedRequest{Agents: []string{a.Ref()}})
		u()
		if err != nil || len(res.Removed) != 0 {
			t.Fatalf("with %v in the repository: %+v, %v; want it kept", fault, res, err)
		}
	}
	res, err := d.client.RemoveStopped(ctx, api.RemoveStoppedRequest{Agents: []string{a.Ref(), b.Ref(), "hello-stack/gone"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Removed) != 1 || res.Removed[0] != a.Ref() || len(res.Kept) != 2 {
		t.Errorf("result = %+v, want %s removed and the rest kept", res, a.Ref())
	}
	for _, k := range res.Kept {
		if k.Keep == "" {
			t.Errorf("%s kept with no reason", k.Ref)
		}
	}
	if !agentExists(t, d, b) {
		t.Error("the agent with new work was removed")
	}
}

// With ENFILE, EMFILE or git failing, in the worktrees or the repository,
// every agent is kept, the ones with nothing included; once git reads again,
// those go.
func TestRemoveStoppedKeepsWhatItCantRead(t *testing.T) {
	t.Parallel()
	d, root, repo := stoppedProject(t)
	ctx := context.Background()
	nothing := stoppedAgent(t, d, repo, "nothing", "", 10*day)
	committed := stoppedAgent(t, d, repo, "committed", "commit", 10*day)
	untracked := stoppedAgent(t, d, repo, "untracked", "untracked", 10*day)
	vanished := stoppedAgent(t, d, repo, "vanished", "", 10*day)
	all := []state.Agent{nothing, committed, untracked, vanished}
	d.setInstances(t, stoppedInstances(time.Now().Add(-20*day), nil, all...))
	if err := d.srv.store.SetFlag(ctx, state.SettingRemoveStopped, true); err != nil {
		t.Fatal(err)
	}
	kept := func(when string) {
		t.Helper()
		for _, a := range all {
			if !agentExists(t, d, a) {
				t.Fatalf("%s: %s was removed", when, a.Name)
			}
		}
	}
	for _, fault := range readFaults {
		var undo []func()
		for _, a := range all {
			undo = append(undo, gitrepo.FailUnder(a.Worktree, fault))
		}
		list, err := d.client.StoppedMachines(ctx, "", true)
		d.srv.removeStoppedMachines(ctx, time.Now())
		for _, u := range undo {
			u()
		}
		kept("with " + fault.Error() + " in the worktrees")
		if err != nil {
			t.Fatal(err)
		}
		for _, sm := range list.Machines {
			if sm.Remove != "" || !strings.Contains(sm.Keep, "can't be read") {
				t.Errorf("with %v, %s: remove %q, keep %q; want kept as unreadable", fault, sm.Name, sm.Remove, sm.Keep)
			}
		}

		u := gitrepo.FailUnder(root, fault)
		d.srv.removeStoppedMachines(ctx, time.Now())
		u()
		kept("with " + fault.Error() + " in the repository")
	}

	// Nor can a diff from nowhere.
	p, _ := d.srv.store.Project(ctx, nothing.Project)
	noBase := agent.Status{Agent: nothing, State: "stopped"}
	noBase.BaseCommit = ""
	if _, err := d.srv.newStoppedJudge(p, nil).facts(ctx, noBase); err == nil {
		t.Error("an agent with no base commit was judged")
	}

	// A worktree that isn't there can't say it had nothing.
	if err := os.RemoveAll(vanished.Worktree); err != nil {
		t.Fatal(err)
	}
	d.srv.removeStoppedMachines(ctx, time.Now())
	if !agentExists(t, d, vanished) {
		t.Error("the agent whose worktree is missing was removed")
	}

	d.srv.removeStoppedMachines(ctx, time.Now())
	if agentExists(t, d, nothing) {
		t.Error("once git reads again, the agent with nothing is still there")
	}
	if !agentExists(t, d, untracked) {
		t.Error("the agent with untracked work was removed")
	}
}

// A merged or closed pull request lets an agent with changes go; an open one
// keeps it.
func TestRemoveStoppedByPullRequest(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), stoppedIncus)
	gh := newSlowGitHub(t, d, "[]", 0)
	_, agents := pullsProject(t, d, "agent-01", "agent-02", "agent-03")
	ctx := context.Background()
	updated := time.Now().Add(time.Minute).UTC().Format(time.RFC3339)
	var prs []string
	for i, st := range []string{"merged", "open", "closed"} {
		a := agents[fmt.Sprintf("agent-0%d", i+1)]
		tip := commitOn(t, a, st+".txt") // a commit of each agent's own
		merged, prState := "null", "closed"
		switch st {
		case "merged":
			merged = fmt.Sprintf("%q", updated)
		case "open":
			prState = "open"
		}
		prs = append(prs, fmt.Sprintf(`{"number":%d,"title":"Work","state":%q,"merged_at":%s,"html_url":"https://github.com/acme/hello-stack/pull/%d","draft":false,"updated_at":%q,"base":{"ref":"main"},"head":{"ref":"feat/w%d","sha":%q}}`,
			31+i, prState, merged, 31+i, updated, i, tip))
	}
	gh.setList("[" + strings.Join(prs, ",") + "]")
	if out := pullsOf(t, d, "hello-stack"); len(out.PullRequests) != 3 {
		t.Fatalf("pull requests = %+v, want three", out.PullRequests)
	}
	d.setInstances(t, stoppedInstances(time.Now(), nil, agents["agent-01"], agents["agent-02"], agents["agent-03"]))

	// The agents are new: judge them as of ten days from now.
	later := time.Now().Add(10 * day)
	list, _, err := d.srv.stoppedMachines(ctx, "hello-stack", true, later, 7)
	if err != nil {
		t.Fatal(err)
	}
	var refs []string
	for _, sm := range list {
		refs = append(refs, sm.Ref)
	}
	res := d.srv.removeStopped(ctx, refs, later, 7)
	if len(res.Removed) != 2 || len(res.Kept) != 1 || res.Kept[0].Name != "agent-02" || !strings.Contains(res.Kept[0].Keep, "is open") {
		t.Errorf("result = %+v, want agent-01 and agent-03 removed and agent-02 kept for its open pull request", res)
	}
	got, err := d.srv.store.ArchivedAgentByName(ctx, "hello-stack", "agent-01")
	if err != nil || got.PR.Number != 31 {
		t.Errorf("archived agent-01 = %+v, %v; want it kept with pull request #31", got, err)
	}
}

// When AgentBox stopped the machine counts, unless it was started again
// since; then the last sign of life does.
func TestStoppedSince(t *testing.T) {
	t.Parallel()
	d, _, repo := stoppedProject(t)
	ctx := context.Background()
	a := stoppedAgent(t, d, repo, "a1", "", 10*day)
	started := time.Now().Add(-3 * day).Truncate(time.Second)
	for _, c := range []struct {
		name    string
		started time.Time
		want    time.Time
	}{
		{"stopped after its last start", time.Now().Add(-20 * day), time.Now().Add(-10 * day)},
		{"started again since", started, started},
	} {
		d.setInstances(t, stoppedInstances(c.started, nil, a))
		statuses, err := d.srv.manager(nil).List(ctx, a.Project)
		if err != nil || len(statuses) != 1 {
			t.Fatalf("List = %+v, %v", statuses, err)
		}
		got, err := d.srv.stoppedSince(ctx, statuses[0])
		if err != nil || got.Sub(c.want).Abs() > 2*time.Second {
			t.Errorf("%s: stoppedSince = %v, %v; want %v", c.name, got, err, c.want)
		}
	}
}

// Stopping an agent records when, which the days are counted from.
func TestStopRecordsWhen(t *testing.T) {
	t.Parallel()
	d, _, repo := stoppedProject(t)
	ctx := context.Background()
	a := stoppedAgent(t, d, repo, "a1", "", 10*day)
	d.setInstances(t, stoppedInstances(time.Now().Add(-20*day), map[string]bool{"a1": true}, a))
	before := time.Now().Add(-time.Second)
	if err := d.srv.manager(nil).Stop(ctx, a); err != nil {
		t.Fatal(err)
	}
	got, err := d.srv.store.Agent(ctx, a.Project, a.Name)
	if err != nil || got.StoppedAt.Before(before) {
		t.Errorf("StoppedAt = %v, %v; want now", got.StoppedAt, err)
	}
}

func TestRemoveStoppedSettings(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	s, err := d.client.Settings(ctx)
	if err != nil || s.RemoveStopped || s.RemoveStoppedDays != 7 {
		t.Fatalf("defaults = %v, %d, %v; want off after 7 days", s.RemoveStopped, s.RemoveStoppedDays, err)
	}
	for _, bad := range []int{0, -1, 366} {
		if _, err := d.client.UpdateSettings(ctx, api.UpdateSettingsRequest{RemoveStoppedDays: &bad}); err == nil {
			t.Errorf("%d days was accepted", bad)
		}
	}
	on, days := true, 14
	s, err = d.client.UpdateSettings(ctx, api.UpdateSettingsRequest{RemoveStopped: &on, RemoveStoppedDays: &days})
	if err != nil || !s.RemoveStopped || s.RemoveStoppedDays != 14 {
		t.Errorf("after turning it on for 14 days: %v, %d, %v", s.RemoveStopped, s.RemoveStoppedDays, err)
	}
}
