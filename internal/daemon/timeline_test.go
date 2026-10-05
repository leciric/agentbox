package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
	"agentbox/internal/testutil"
)

// timelineAgent is an agent with a real worktree and a chat of three
// settled turns, stored as a daemon that restarted would find them.
func timelineAgent(t *testing.T) (testDaemon, state.Agent, []string) {
	t.Helper()
	d := startTestDaemon(t, t.TempDir(), `case "$1" in
  list) echo '[]' ;;
esac`)
	ctx := context.Background()
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	worktree := d.paths.Worktree("hello-stack", "agent-01")
	commit := testutil.Git(t, repo, "rev-parse", "HEAD")
	testutil.Git(t, repo, "worktree", "add", "--quiet", "-b", "agentbox/agent-01", worktree, commit)
	a := state.Agent{
		Project: "hello-stack", Name: "agent-01", Title: "Reminders page", Instance: "ab-hello-stack-agent-01",
		AI: "claude", Branch: "agentbox/agent-01", BaseRef: "main", BaseCommit: commit,
		Worktree: worktree, Status: state.AgentReady, CreatedAt: time.Now(), Interface: state.InterfaceChat,
	}
	if err := d.srv.store.AddAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	var rows []state.ChatItem
	var turns []string
	for i, text := range []string{"write one", "write two", "write three"} {
		user := "u" + string(rune('1'+i))
		turns = append(turns, user)
		for j, it := range []api.ChatItem{
			{ID: user, Turn: user, Kind: "user", Text: text, Result: &api.ChatTurnResult{State: "completed"}},
			{ID: "a" + user, Turn: user, Kind: "assistant", Text: "Done: " + text},
		} {
			it.CreatedAt, it.UpdatedAt = now, now
			data, _ := json.Marshal(it)
			rows = append(rows, state.ChatItem{ID: it.ID, Position: int64(2*i + j), Data: data})
		}
	}
	if err := d.srv.store.SaveChatItems(ctx, a.Project, a.Name, rows); err != nil {
		t.Fatal(err)
	}
	return d, a, turns
}

// TestRollbackPutsFilesAndChatBack: each turn's end is checkpointed; rolling
// back to turn 1 puts its file back, saves what came after as a checkpoint,
// and cuts the chat after turn 1 with a notice carrying the handoff.
func TestRollbackPutsFilesAndChatBack(t *testing.T) {
	t.Parallel()
	d, a, turns := timelineAgent(t)
	ctx := context.Background()
	file := filepath.Join(a.Worktree, "notes.txt")
	for i, turn := range turns {
		if err := os.WriteFile(file, []byte(strings.Repeat("x", i+1)), 0o644); err != nil {
			t.Fatal(err)
		}
		d.srv.checkpointTurn(a, turn)
	}
	d.srv.checkpointTurn(a, "gone") // a turn no longer in the chat: nothing
	list, err := d.client.Checkpoints(ctx, a.Ref())
	if err != nil || len(list) != 3 || list[0].ID != "turn-1" || list[0].Prompt != "write one" || list[2].Turn != "u3" {
		t.Fatalf("Checkpoints() = %+v, %v", list, err)
	}
	if _, err := d.client.Rollback(ctx, a.Ref(), "nope"); err == nil {
		t.Error("rolled back to no checkpoint")
	}
	res, err := d.client.Rollback(ctx, a.Ref(), "1")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(file); string(got) != "x" {
		t.Errorf("notes.txt is %q after rolling back to turn 1, want x", got)
	}
	if res.Saved.Kind != state.CheckpointSaved {
		t.Errorf("saved %+v", res.Saved)
	}
	list, _ = d.client.Checkpoints(ctx, a.Ref())
	var ids []string
	for _, cp := range list {
		ids = append(ids, cp.ID)
	}
	if strings.Join(ids, ",") != "turn-1,"+res.Saved.ID {
		t.Errorf("checkpoints after the rollback: %v", ids)
	}
	th, err := d.srv.chat.Thread(a)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(th.Items); n != 3 || th.Items[2].Kind != "notice" || !strings.Contains(th.Items[2].Handoff, "User: write one") {
		t.Fatalf("chat after the rollback has %d items: %+v", n, th.Items)
	}
	if _, err := d.client.Rollback(ctx, a.Ref(), res.Saved.ID); err == nil || !strings.Contains(err.Error(), "fork from it") {
		t.Errorf("rolling back to a saved checkpoint: %v", err)
	}

	// A fork from the saved state is told the conversation that was cut.
	fc, err := d.srv.forkConversation(ctx, a, res.Saved.ID)
	if err != nil || len(fc.items) != 0 || !strings.Contains(fc.handoff, "User: write three") {
		t.Fatalf("forkConversation(saved) = %+v, %v", fc, err)
	}
	fc, err = d.srv.forkConversation(ctx, a, "turn-1")
	if err != nil || len(fc.items) != 3 || !strings.Contains(fc.handoff, "You are a fork of hello-stack/agent-01, from the end of its turn 1") {
		t.Fatalf("forkConversation(turn-1) = %+v, %v", fc, err)
	}
	// The project's chat sees the same, through its own socket.
	lead := api.NewClient(d.srv.leadSocketPath("hello-stack"))
	if got, err := lead.AgentCheckpoints(ctx, "agent-01"); err != nil || len(got) != 2 {
		t.Errorf("AgentCheckpoints() = %+v, %v", got, err)
	}
	if _, err := lead.RollbackAgent(ctx, "agent-01", "turn-3"); err == nil {
		t.Error("the lead rolled back to a turn that was itself rolled back")
	}
}
