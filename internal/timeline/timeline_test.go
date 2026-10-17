package timeline

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
)

var t0 = time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)

// at is t0 plus mins minutes.
func at(mins float64) time.Time { return t0.Add(time.Duration(mins * float64(time.Minute))) }

type session struct {
	items []api.ChatItem
	n     int
}

func (s *session) item(it api.ChatItem, from, to float64) *session {
	s.n++
	it.ID = fmt.Sprintf("i%d", s.n)
	it.CreatedAt, it.UpdatedAt = at(from), at(to)
	s.items = append(s.items, it)
	return s
}

func (s *session) user(text string, from float64) *session {
	return s.item(api.ChatItem{Kind: "user", Text: text, Result: &api.ChatTurnResult{}}, from, from)
}

func (s *session) tool(t api.ChatTool, from, to float64) *session {
	if t.Status == "" {
		t.Status = "completed"
	}
	return s.item(api.ChatItem{Kind: "tool", Tool: &t}, from, to)
}

func kinds(tl api.Timeline) string {
	var k []string
	for _, s := range tl.Steps {
		k = append(k, s.Kind)
	}
	return strings.Join(k, " ")
}

func TestBuildASession(t *testing.T) {
	s := (&session{}).
		user("Keep the review modal's footer fixed.\nShow it with a recording.", 0).
		tool(api.ChatTool{Kind: "read", Paths: []string{"desktop/src/ReadyToReview.tsx"}}, 0.5, 0.6).
		tool(api.ChatTool{Kind: "search", Title: "grep footer"}, 0.7, 0.8).
		item(api.ChatItem{Kind: "plan", Plan: []api.ChatPlanEntry{{Content: "Move the footer", Status: "pending"}}}, 1, 1).
		item(api.ChatItem{Kind: "plan", Plan: []api.ChatPlanEntry{{Content: "Move the footer", Status: "completed"}}}, 1.2, 1.2).
		tool(api.ChatTool{Kind: "edit", Diffs: []api.ChatDiff{{Path: "a.tsx", OldText: "x\ny\n", NewText: "x\nz\nw\n"}}}, 2, 3).
		// A file read between two edits is part of the run of edits.
		tool(api.ChatTool{Kind: "read", Paths: []string{"b.tsx"}}, 3.1, 3.2).
		tool(api.ChatTool{Kind: "edit", Diffs: []api.ChatDiff{{Path: "b.tsx", OldText: "", NewText: "new\n", Created: true}}}, 3.3, 4).
		tool(api.ChatTool{Kind: "execute", Command: "npm --prefix desktop test", Output: "ℹ tests 214\nℹ fail 2\n"}, 5, 6).
		tool(api.ChatTool{Kind: "edit", Diffs: []api.ChatDiff{{Path: "a.tsx", OldText: "z\n", NewText: "zz\n"}}}, 6.5, 7).
		tool(api.ChatTool{Kind: "execute", Command: "go test ./internal/x", Output: "--- FAIL: TestX\nFAIL\n"}, 7, 8).
		tool(api.ChatTool{Kind: "execute", Command: "go test ./internal/x", Output: "ok  agentbox/internal/x 0.2s\n"}, 8, 9).
		tool(api.ChatTool{Kind: "execute", Command: "npm --prefix desktop test", Output: "ℹ tests 214\nℹ pass 214\nℹ fail 0\n"}, 9, 10).
		item(api.ChatItem{Kind: "subagent", Subagent: &api.ChatSubagent{Name: "desktop", Task: "Record the review modal", State: "completed"}}, 10, 14).
		// The subagent's own calls are its step's, not steps of their own.
		item(api.ChatItem{Kind: "tool", Parent: "i14", Tool: &api.ChatTool{Kind: "execute", Command: "agentbox media record start", Status: "completed"}}, 10.5, 10.6).
		item(api.ChatItem{Kind: "tool", Parent: "i14", Tool: &api.ChatTool{Kind: "other", Name: "mcp__desktop__click", Status: "completed"}}, 11, 11.1).
		tool(api.ChatTool{Kind: "other", Name: "mcp__hatch__publish_page", Page: &api.ChatPage{Title: "Footer, before and after"}}, 14.5, 15).
		tool(api.ChatTool{Kind: "execute", Command: "git push -u origin agentbox/x && gh pr create --title 'fix: footer'"}, 15, 16)
	media := []api.MediaItem{
		{ID: "rec", Kind: "recording", CreatedAt: at(10.6), Meta: api.MediaMeta{Duration: 180}},
		{ID: "shot", Kind: "screenshot", CreatedAt: at(12)},
		{ID: "early", Kind: "screenshot", CreatedAt: at(-5)},
		{ID: "note", Kind: "note", CreatedAt: at(12)},
		{ID: "junit", Kind: "report", CreatedAt: at(9.5)},
	}
	tl := Build(s.items, media)

	if got, want := kinds(tl), "task read plan edit test edit test test desktop page pr"; got != want {
		t.Fatalf("kinds:\n got %s\nwant %s", got, want)
	}
	st := tl.Steps
	if st[0].Title != "Keep the review modal's footer fixed." || len(st[0].Lines) != 2 {
		t.Errorf("task: %q %q", st[0].Title, st[0].Lines)
	}
	if st[1].Calls != 2 || st[1].Paths[0] != "desktop/src/ReadyToReview.tsx" {
		t.Errorf("read: %+v", st[1])
	}
	if st[2].Lines[0] != "[x] Move the footer" {
		t.Errorf("a plan ticked off replaces its lines: %q", st[2].Lines)
	}
	if e := st[3]; e.Calls != 3 || e.Added != 3 || e.Removed != 1 || strings.Join(e.Paths, ",") != "a.tsx,b.tsx" {
		t.Errorf("edits: calls %d +%d −%d %v", e.Calls, e.Added, e.Removed, e.Paths)
	}
	if st[4].Status != "failed" || st[4].Lines[0] != "$ npm --prefix desktop test" {
		t.Errorf("failing tests: %q %q", st[4].Status, st[4].Lines)
	}
	// Failing, then passing, is two steps; passing twice is one.
	if st[6].Status != "failed" || st[7].Status != "passed" || st[7].Calls != 2 {
		t.Errorf("tests: %q, then %q ×%d", st[6].Status, st[7].Status, st[7].Calls)
	}
	if d := st[8]; d.Title != "Record the review modal" || d.Calls != 2 || !d.End.Equal(at(14)) {
		t.Errorf("desktop: %+v", d)
	}
	if p := st[9]; p.Title != "Footer, before and after" || len(p.Pages) != 1 || p.Pages[0].Item != "i17" {
		t.Errorf("page: %+v", p)
	}
	if st[10].Title != "git push -u origin agentbox/x && gh pr create --title 'fix: footer'" {
		t.Errorf("pr: %q", st[10].Title)
	}

	ids := func(s api.TimelineStep) string {
		var out []string
		for _, m := range s.Media {
			out = append(out, m.ID)
		}
		return strings.Join(out, ",")
	}
	if ids(st[8]) != "rec,shot" || ids(st[0]) != "early" || ids(st[7]) != "junit" {
		t.Errorf("media: desktop %q, task %q, tests %q", ids(st[8]), ids(st[0]), ids(st[7]))
	}
	if !tl.Start.Equal(at(0)) || !tl.End.Equal(at(16)) || tl.Working {
		t.Errorf("span %v–%v working %v", tl.Start, tl.End, tl.Working)
	}
}

func TestBuildWorking(t *testing.T) {
	s := (&session{}).
		user("Walk every screen.", 0).
		tool(api.ChatTool{Kind: "execute", Command: "go test ./...", Status: "in_progress"}, 1, 2)
	s.items[0].Result = nil
	tl := Build(s.items, nil)
	if !tl.Working || tl.Steps[1].Status != "running" {
		t.Fatalf("working %v, status %q", tl.Working, tl.Steps[1].Status)
	}
	// Once the turn ended, a call that never finished isn't running.
	s.items[0].Result = &api.ChatTurnResult{}
	if tl := Build(s.items, nil); tl.Working || tl.Steps[1].Status != "" {
		t.Errorf("ended: working %v, status %q", tl.Working, tl.Steps[1].Status)
	}
}

func TestBuildEmpty(t *testing.T) {
	tl := Build(nil, []api.MediaItem{{ID: "x", Kind: "screenshot"}})
	if tl.Steps == nil || len(tl.Steps) != 0 || !tl.Start.IsZero() {
		t.Errorf("%+v", tl)
	}
	// Woken turns, hidden items and empty messages are no steps.
	s := (&session{}).
		item(api.ChatItem{Kind: "user", Woken: true, Text: "task ended"}, 0, 0).
		item(api.ChatItem{Kind: "user", Hidden: true, Text: "agent-3 finished"}, 1, 1).
		user("  ", 2)
	if tl := Build(s.items, nil); len(tl.Steps) != 0 {
		t.Errorf("steps: %s", kinds(tl))
	}
}

func TestClassify(t *testing.T) {
	for _, c := range []struct {
		tool api.ChatTool
		want string
	}{
		{api.ChatTool{Kind: "execute", Command: "cd desktop && npm run test -- --watch=false"}, "test"},
		{api.ChatTool{Kind: "execute", Command: "go test -run TestX ./internal/chat"}, "test"},
		{api.ChatTool{Kind: "execute", Command: "pytest -q"}, "test"},
		{api.ChatTool{Kind: "execute", Command: "cargo test"}, "test"},
		{api.ChatTool{Kind: "execute", Command: "scripts/check-coverage.sh"}, "test"},
		{api.ChatTool{Kind: "execute", Command: "npm run build"}, "run"},
		{api.ChatTool{Kind: "execute", Command: "cat latest.txt"}, "run"},
		{api.ChatTool{Kind: "execute", Command: "gh pr edit 12 --body x"}, "pr"},
		{api.ChatTool{Kind: "execute", Command: "gh pr view 12"}, "run"},
		{api.ChatTool{Kind: "execute", Command: "agentbox media screenshot --name x"}, "desktop"},
		{api.ChatTool{Kind: "execute", Title: "go test ./..."}, "test"},
		{api.ChatTool{Kind: "other", Name: "mcp__playwright__browser_click"}, "desktop"},
		{api.ChatTool{Kind: "other", Name: "mcp__hatch__update_page", Page: &api.ChatPage{ID: "p1"}}, "page"},
		{api.ChatTool{Kind: "delete"}, "edit"},
		{api.ChatTool{Kind: "fetch"}, "read"},
		{api.ChatTool{Kind: "other", Name: "TodoWrite"}, "read"},
	} {
		if got := Classify(&c.tool); got != c.want {
			t.Errorf("%+v: %s, want %s", c.tool, got, c.want)
		}
	}
}

func TestCallStatusReadsTestOutput(t *testing.T) {
	for out, want := range map[string]string{
		"ok  agentbox/internal/x 0.2s":            "passed",
		"--- FAIL: TestX (0.00s)":                 "failed",
		"Tests: 2 failed, 59 passed, 61 total":    "failed",
		"Tests: 61 passed, 61 total":              "passed",
		"test result: FAILED. 3 passed; 1 failed": "failed",
		"ℹ fail 0":                        "passed",
		"= 3 failed, 10 passed in 1.2s =": "failed",
	} {
		if got := callStatus("test", &api.ChatTool{Status: "completed", Output: out}); got != want {
			t.Errorf("%q: %s, want %s", out, got, want)
		}
	}
	if got := callStatus("test", &api.ChatTool{Status: "failed"}); got != "failed" {
		t.Errorf("a failed call: %s", got)
	}
}

func TestLongTextIsCut(t *testing.T) {
	long := strings.Repeat("é", 500)
	s := (&session{}).user(long+"\n"+strings.Repeat("line\n", 40), 0)
	st := Build(s.items, nil).Steps[0]
	if n := len([]rune(st.Title)); n != maxLineRunes {
		t.Errorf("title is %d runes", n)
	}
	if len(st.Lines) != maxLines {
		t.Errorf("%d lines", len(st.Lines))
	}
}
