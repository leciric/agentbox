package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
	"agentbox/internal/testutil"
)

// TestRecapLoad times the recap against half a year of history:
// AGENTBOX_RECAP_LOAD=1 runs it, and it logs how long each part took. It
// times rather than checks.
func TestRecapLoad(t *testing.T) {
	if os.Getenv("AGENTBOX_RECAP_LOAD") == "" {
		t.Skip("AGENTBOX_RECAP_LOAD=1 runs it")
	}
	ctx := context.Background()
	// AGENTBOX_RECAP_LOAD_KEEP keeps what it seeded there, for a real daemon
	// (XDG_DATA_HOME) and the app to open.
	root := os.Getenv("AGENTBOX_RECAP_LOAD_KEEP")
	if root == "" {
		root = t.TempDir()
	}
	d := startTestDaemon(t, root, fakeIncus)
	now := time.Now()
	const projects, pastPer, livePer, weeks = 4, 400, 5, 26
	pad := strings.Repeat("lorem ipsum dolor sit amet ", 70) // ~2 KB an item
	for p := range projects {
		name := fmt.Sprintf("proj%d", p)
		// AGENTBOX_RECAP_LOAD_REPO is a real repository whose agents' branches
		// the past agents get, for git diffs of a real size; else each has a
		// branch adding one small file to the fixture.
		var repo, head string
		var branches, bases []string
		if src := os.Getenv("AGENTBOX_RECAP_LOAD_REPO"); src != "" {
			repo = filepath.Join(root, "repos", name)
			if out, err := exec.Command("git", "clone", "-q", "--shared", src, repo).CombinedOutput(); err != nil {
				t.Fatalf("clone: %v %s", err, out)
			}
			head = strings.TrimSpace(testutil.Git(t, repo, "rev-parse", "--abbrev-ref", "HEAD"))
			for _, b := range strings.Fields(testutil.Git(t, repo, "for-each-ref", "--format=%(refname:lstrip=3)", "refs/remotes/origin/agentbox/")) {
				if base, err := exec.Command("git", "-C", repo, "merge-base", "origin/"+head, "origin/"+b).Output(); err == nil {
					branches, bases = append(branches, b), append(bases, strings.TrimSpace(string(base)))
				}
			}
		} else {
			repo = d.fixtureRepo(t, "hello-stack")
			base := strings.TrimSpace(testutil.Git(t, repo, "rev-parse", "HEAD"))
			head = strings.TrimSpace(testutil.Git(t, repo, "rev-parse", "--abbrev-ref", "HEAD"))
			var fi strings.Builder
			for i := range pastPer {
				content := fmt.Sprintf("work %d\n", i)
				fmt.Fprintf(&fi, "commit refs/heads/agentbox/past-%d\ncommitter A <a@b> %d +0000\ndata 4\nwork\nfrom %s\nM 644 inline added-%d.txt\ndata %d\n%s\n", i, now.Unix(), base, i, len(content), content)
				branches, bases = append(branches, fmt.Sprintf("agentbox/past-%d", i)), append(bases, base)
			}
			cmd := exec.Command("git", "fast-import", "--quiet")
			cmd.Dir, cmd.Stdin = repo, strings.NewReader(fi.String())
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("fast-import: %v %s", err, out)
			}
		}
		if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo, Name: name}); err != nil {
			t.Fatal(err)
		}
		var rows []state.TokenRow
		var lead []state.ChatItem
		for i := range pastPer {
			agent := fmt.Sprintf("agent-%d", i)
			at := now.Add(-time.Duration(i) * weeks * 7 * 24 * time.Hour / pastPer)
			a := state.Agent{
				Project: name, Name: agent, Instance: "ab-" + name + "-" + agent, AI: "claude", Title: "Past " + agent,
				Branch: branches[i%len(branches)], BaseRef: head, BaseCommit: bases[i%len(branches)], Worktree: "/gone/" + agent,
				Status: state.AgentReady, CreatedAt: at.Add(-3 * time.Hour), Interface: state.InterfaceChat,
			}
			if err := d.srv.store.AddAgent(ctx, a); err != nil {
				t.Fatal(err)
			}
			var items []state.ChatItem
			for k := range 60 {
				data, _ := json.Marshal(map[string]any{"kind": []string{"user", "agent", "tool"}[k%3], "createdAt": at.Add(time.Duration(k) * time.Minute).Format(time.RFC3339Nano), "text": pad})
				items = append(items, state.ChatItem{ID: fmt.Sprint(k), Position: int64(k), Data: data})
			}
			if err := d.srv.store.SaveChatItems(ctx, name, agent, items); err != nil {
				t.Fatal(err)
			}
			pr := state.ArchivedPR{Number: i + 1, Title: "feat: x", State: state.PRMerged}
			if err := d.srv.store.ArchiveAgent(ctx, a, "", pr, at); err != nil {
				t.Fatal(err)
			}
			for k := range 40 {
				end := at.Add(-time.Duration(k) * 4 * time.Minute)
				for _, model := range []string{"claude-opus-5", "claude-haiku-5"} {
					rows = append(rows, state.TokenRow{Project: name, Agent: agent, AI: "claude", Turn: fmt.Sprintf("t%d", k), Kind: state.TokensTurn, Model: model, At: end, Input: 1000, Output: 300, GenerationMS: 3 * 60 * 1000})
				}
			}
			for k := range 20 {
				data, _ := json.Marshal(map[string]any{"kind": []string{"user", "agent"}[k%2], "createdAt": at.Add(time.Duration(k) * time.Minute).Format(time.RFC3339Nano), "text": pad})
				lead = append(lead, state.ChatItem{ID: fmt.Sprintf("%d-%d", i, k), Position: int64(i*20 + k), Data: data})
			}
		}
		if err := d.srv.store.AddTokenRows(ctx, rows); err != nil {
			t.Fatal(err)
		}
		if err := d.srv.store.SaveChatItems(ctx, name, state.LeadName, lead); err != nil {
			t.Fatal(err)
		}
		for i := range livePer {
			addAgent(t, d, repo, name, fmt.Sprintf("live-%d", i), fmt.Sprintf("Live %d", i))
		}
	}
	t.Logf("seeded %d projects, %d past agents, %d live", projects, projects*pastPer, projects*livePer)

	offset := -3 * time.Hour
	timed := func(what string, f func() error) {
		t.Helper()
		begin := time.Now()
		if err := f(); err != nil {
			t.Fatal(err)
		}
		t.Logf("%-28s %v", what, time.Since(begin).Round(100*time.Microsecond))
	}
	start, end := now.AddDate(0, 0, -7), now
	timed("TurnSpans", func() error { _, err := d.srv.store.TurnSpans(ctx, start, end); return err })
	timed("UserPresence", func() error { _, err := d.srv.store.UserPresence(ctx, start.Add(-api.RecapPresence), end); return err })
	timed("TurnDays", func() error {
		_, err := d.srv.store.TurnDays(ctx, now.AddDate(0, 0, -recapStreakDays), offset)
		return err
	})
	timed("TokenTotals", func() error {
		_, err := d.srv.store.TokenTotals(ctx, state.TokenFilter{Since: start, Until: end})
		return err
	})
	timed("projectWorkEntries (all)", func() error {
		ps, _ := d.srv.store.Projects(ctx)
		for _, p := range ps {
			if _, err := d.srv.projectWorkEntries(ctx, p); err != nil {
				return err
			}
		}
		return nil
	})
	for _, w := range []int{0, 0, 1, 4, 12} {
		timed(fmt.Sprintf("buildRecap weeks=%d", w), func() error { _, err := d.srv.buildRecap(ctx, now, offset, w); return err })
	}
}
