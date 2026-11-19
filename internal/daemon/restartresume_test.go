package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// What a chat says of a turn held for the user (chat.NoteHeld).
const heldFor = "didn't resume it by itself"

// The chats go first, then agents oldest first, whenever their turns
// started and whether they had one.
func TestResumesGoChatsFirstThenAgentsOldestFirst(t *testing.T) {
	now := time.Now()
	turn := func(project, name, kind string, started time.Time) *state.RunningTurn {
		return &state.RunningTurn{Project: project, Agent: name, Kind: kind, StartedAt: started}
	}
	items := []resumeItem{
		{ref: "p/agent-03", made: now.Add(-1 * time.Hour), turn: turn("p", "agent-03", state.TurnOfAgent, now.Add(-time.Minute))},
		{ref: "p/lead", lead: true, made: now, turn: turn("p", "lead", state.TurnOfLead, now)},
		{ref: "p/agent-01", made: now.Add(-3 * time.Hour)}, // On hold for memory, no turn
		{ref: "home/main", lead: true, made: now.Add(-time.Second), turn: turn("home", "main", state.TurnOfHome, now.Add(-time.Second))},
		{ref: "q/agent-02", made: now.Add(-2 * time.Hour), turn: turn("q", "agent-02", state.TurnOfAgent, now.Add(-2*time.Minute))},
	}
	sortResumes(items)
	var got []string
	for _, it := range items {
		got = append(got, it.ref)
	}
	want := []string{"home/main", "p/lead", "p/agent-01", "q/agent-02", "p/agent-03"}
	if !slices.Equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

// machinesStopped is the fake Incus's list: each agent's machine, stopped.
func machinesStopped(names ...string) string {
	var parts []string
	for i, name := range names {
		parts = append(parts, fmt.Sprintf(`{"name":"ab-hello-stack-%s","status":"Stopped","config":{},"expanded_config":{},`+
			`"state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.8.8.%d"}]}}}}`, name, i+2))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// lastUserItem is the ID heading a's chat's latest turn.
func lastUserItem(t *testing.T, d testDaemon, a state.Agent) string {
	t.Helper()
	th, err := d.srv.chat.Thread(a)
	if err != nil {
		t.Fatal(err)
	}
	for i := len(th.Items) - 1; i >= 0; i-- {
		if th.Items[i].Kind == "user" {
			return th.Items[i].ID
		}
	}
	t.Fatalf("%s's chat has no turn", a.Ref())
	return ""
}

// After a restart, what it cut short starts one at a time, oldest agent
// first, whatever order their turns started in: each waits until the one
// before has settled, with nothing else starting meanwhile. An agent On hold
// for memory with no turn takes its place in the same line.
func TestResumesAfterARestartGoOneAtATimeOldestFirst(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	tool := &promptTool{}
	running := strings.ReplaceAll(machinesStopped("agent-01", "agent-02", "agent-03"), "Stopped", "Running")
	d := startTestDaemon(t, root, wakeIncus, testConfig{instances: running, setup: func(s *Server) {
		s.chat.Launch = tool.launch
		s.chat.Prepare = func(context.Context, state.Agent, string, int64) error { return nil }
	}})
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	young := addAgentMade(t, d, repo, "hello-stack", "agent-01", "Settings page", now.Add(-1*time.Hour))
	old := addAgentMade(t, d, repo, "hello-stack", "agent-02", "Reminders page", now.Add(-3*time.Hour))
	middle := addAgentMade(t, d, repo, "hello-stack", "agent-03", "Login page", now.Add(-2*time.Hour))
	// The youngest's turn started first, the oldest's last; the middle one
	// had none, and waited On hold for memory.
	for i, a := range []state.Agent{young, old} {
		if _, err := d.client.SendChat(ctx, a.Ref(), "Build the page."); err != nil {
			t.Fatal(err)
		}
		waitFor(t, a.Name+"'s turn to end", func() bool { return d.srv.chat.State(a.Ref()) == api.ChatReady && tool.count("Build the page.") == i+1 })
		rt := state.RunningTurn{Project: a.Project, Agent: a.Name, Kind: state.TurnOfAgent, Turn: lastUserItem(t, d, a),
			Prompt: "Build the page.", StartedAt: now.Add(time.Duration(i) * time.Minute)}
		if err := d.srv.store.SaveRunningTurn(ctx, rt); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.srv.store.SetHold(ctx, middle.Project, middle.Name, state.HoldMemory); err != nil {
		t.Fatal(err)
	}
	// The VM started again: every machine is stopped.
	d.setInstances(t, machinesStopped("agent-01", "agent-02", "agent-03"))

	var settles atomic.Int32
	release := make(chan struct{})
	d.srv.resumes.settle = func(ctx context.Context, since time.Time) error {
		if n := settles.Add(1); n == 1 && !since.IsZero() || n > 1 && since.IsZero() {
			t.Errorf("settle %d waited on a resume begun at %v", n, since)
		}
		if settles.Load() == 2 {
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	done := make(chan struct{})
	go func() {
		d.srv.continueTurns(ctx)
		close(done)
	}()
	w := &wakeTest{testDaemon: d, log: filepath.Join(root, "incus.log")}
	started := func(name string) bool { return w.order(t, "incus start ab-hello-stack-"+name)[0] >= 0 }
	waitFor(t, "the oldest agent's machine to start", func() bool { return started("agent-02") })
	waitFor(t, "the next to wait for it to settle", func() bool { return settles.Load() == 2 })
	waitFor(t, "the oldest agent's turn to be carried on", func() bool { return tool.count(resumeText) == 1 })
	time.Sleep(100 * time.Millisecond)
	if started("agent-03") || started("agent-01") {
		t.Fatal("another machine started before the first had settled")
	}
	close(release)
	waitFor(t, "every resume to have gone", func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	})
	at := w.order(t, "incus start ab-hello-stack-agent-02", "incus start ab-hello-stack-agent-03", "incus start ab-hello-stack-agent-01")
	if at[0] < 0 || at[1] < at[0] || at[2] < at[1] {
		t.Errorf("machines started at log lines %v; want agent-02, agent-03, then agent-01 (oldest first)", at)
	}
	if n := settles.Load(); n != 3 {
		t.Errorf("settled %d times, want once before each of the three", n)
	}
	waitFor(t, "both turns to be carried on", func() bool { return tool.count(resumeText) == 2 })
}

// After AgentBox stopped unexpectedly nothing carries on by itself: the
// agent is On hold, its turn held, its chat says why, and a clean restart
// leaves them so. Resume all carries it on.
func TestAfterAnUnexpectedStopNothingResumesUntilResumeAll(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	ctx := context.Background()

	hang := &hangTool{}
	d1 := restartDaemon(t, root, "Running", hang.launch)
	repo := d1.fixtureRepo(t, "hello-stack")
	if _, err := d1.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	a := addAgent(t, d1, repo, "hello-stack", "agent-01", "Reminders page")
	if _, err := d1.client.SendChat(ctx, a.Ref(), "Build the reminders page."); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the turn to reach the AI tool", hang.started)
	d1.stop()
	// As if it hadn't stopped as it should: the VM went down under it, and
	// its running marker was left behind (report.Crashes).
	marker := filepath.Join(d1.paths.Data, "crashes", "running.json")
	if err := os.WriteFile(marker, []byte(`{"version":"dev","pid":1,"started":"2026-11-18T11:34:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	tool := &promptTool{}
	d2 := restartDaemon(t, root, "Stopped", tool.launch)
	waitFor(t, "the agent to wait On hold for you", func() bool {
		cur, err := d2.srv.store.Agent(ctx, a.Project, a.Name)
		return err == nil && cur.Hold == state.HoldRestart
	})
	assertHeld := func(d testDaemon, tool *promptTool) {
		t.Helper()
		if tool.got(resumeText) {
			t.Error("the turn was carried on by itself")
		}
		if at := (&wakeTest{testDaemon: d, log: filepath.Join(root, "incus.log")}).order(t, "incus start ab-hello-stack-agent-01"); at[0] >= 0 {
			t.Error("the agent's machine was started")
		}
		if turns, _ := d.srv.store.RunningTurns(ctx); len(turns) != 1 || !turns[0].Held {
			t.Errorf("running turns = %+v; want the one turn, held", turns)
		}
		hold, err := d.client.RestartHold(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !hold.Unexpected || !slices.Equal(hold.Agents, []string{a.Ref()}) || len(hold.Chats) != 0 || hold.Since.IsZero() {
			t.Errorf("RestartHold() = %+v; want agent-01 waiting after an unexpected stop", hold)
		}
	}
	assertHeld(d2, tool)
	notices := strings.Join(chatTexts(t, d2, a, "notice"), "\n")
	if !strings.Contains(notices, heldFor) || !strings.Contains(notices, "stopped unexpectedly") || !strings.Contains(notices, "Resume all") {
		t.Errorf("the chat's notices are %q; want it to say the turn waits for you after an unexpected stop", notices)
	}

	// A restart that follows a clean stop leaves it waiting for you.
	d2.stop()
	again := &promptTool{}
	d3 := restartDaemon(t, root, "Stopped", again.launch)
	d3.srv.continueTurns(ctx) // what Run did already
	assertHeld(d3, again)

	res, err := d3.client.ResumeAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Agents != 1 || res.Chats != 0 {
		t.Errorf("ResumeAll() = %+v, want the one agent", res)
	}
	waitFor(t, "Resume all to carry the turn on", func() bool { return again.got(resumeText) })
	hold, err := d3.client.RestartHold(ctx)
	if err != nil || len(hold.Agents) != 0 || len(hold.Chats) != 0 {
		t.Errorf("RestartHold() = %+v, %v after Resume all; want nothing waiting", hold, err)
	}
	if cur, _ := d3.srv.store.Agent(ctx, a.Project, a.Name); cur.Hold == state.HoldRestart {
		t.Error("the agent is still On hold for you after Resume all")
	}
}

// The VM's supervisor saying this boot is after a crash loop holds
// everything too, with its account of why.
func TestASafeVMBootHoldsWhatARestartCutShort(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d, tool, a, _ := resumeSetup(t, 0)
	why := "the VM went down 3 times in 20 minutes"
	d.srv.resumes.readBoot = func() api.VMBoot {
		return api.VMBoot{Started: time.Now(), Safe: true, Why: why, Crashes: []time.Time{time.Now().Add(-time.Minute)}}
	}
	d.srv.noteLastStop(false, time.Time{})
	d.srv.continueTurns(ctx)
	if tool.got(resumeText) {
		t.Error("the turn was carried on in a safe boot")
	}
	hold, err := d.client.RestartHold(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !hold.Unexpected || hold.Why != why || !slices.Equal(hold.Agents, []string{a.Ref()}) {
		t.Errorf("RestartHold() = %+v; want agent-01 waiting, with the VM's reason", hold)
	}
	if notices := strings.Join(chatTexts(t, d, a, "notice"), "\n"); !strings.Contains(notices, why) {
		t.Errorf("the chat's notices are %q; want the VM's reason in them", notices)
	}
}

// AgentBox carries one agent's work on by itself at most three times in a
// row, counted across restarts and whatever its turns: a crash loop that
// gives it a new turn each time (its lead's next message) stops too.
func TestAnAgentIsResumedAtMostThreeTimesInARowAcrossRestarts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	tool := &promptTool{}
	d := restartDaemon(t, root, "Running", tool.launch)
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	a := addAgent(t, d, repo, "hello-stack", "agent-01", "Reminders page")
	newTurn := func(text string) state.RunningTurn {
		t.Helper()
		if _, err := d.client.SendChat(ctx, a.Ref(), text); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "the turn to end", func() bool { return d.srv.chat.State(a.Ref()) == api.ChatReady && tool.got(text) })
		return state.RunningTurn{Project: a.Project, Agent: a.Name, Kind: state.TurnOfAgent, Turn: lastUserItem(t, d, a), Prompt: text, StartedAt: time.Now()}
	}
	for i := 1; i <= maxAutoResumes; i++ {
		rt := newTurn(fmt.Sprintf("Step %d.", i))
		if err := d.srv.store.SaveRunningTurn(ctx, rt); err != nil {
			t.Fatal(err)
		}
		d.srv.continueTurns(ctx)
		waitFor(t, fmt.Sprintf("resume %d to be carried on", i), func() bool {
			turns, _ := d.srv.store.RunningTurns(ctx)
			return tool.count(resumeText) == i && len(turns) == 0 && d.srv.chat.State(a.Ref()) == api.ChatReady
		})
	}
	if err := d.srv.store.SaveRunningTurn(ctx, newTurn("Step 4.")); err != nil {
		t.Fatal(err)
	}
	d.stop()

	next := &promptTool{}
	d2 := restartDaemon(t, root, "Running", next.launch)
	waitFor(t, "the fourth in a row to wait for you", func() bool {
		cur, err := d2.srv.store.Agent(ctx, a.Project, a.Name)
		return err == nil && cur.Hold == state.HoldRestart
	})
	if next.got(resumeText) {
		t.Error("the turn was carried on a fourth time in a row")
	}
	notices := strings.Join(chatTexts(t, d2, a, "notice"), "\n")
	if !strings.Contains(notices, "restarted 3 times in a row") {
		t.Errorf("the chat's notices are %q; want it to say AgentBox restarted 3 times in a row", notices)
	}
	hold, err := d2.client.RestartHold(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if hold.Unexpected || !slices.Equal(hold.Agents, []string{a.Ref()}) {
		t.Errorf("RestartHold() = %+v; want agent-01 waiting, AgentBox having stopped as it should", hold)
	}
}
