package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"agentbox/internal/acp"
	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// reviewer is a review that answers from a list, and keeps what it was given.
type reviewer struct {
	mu      sync.Mutex
	answers []string // in order; the last one again once they run out
	models  []string
	prompts [][]acp.ContentBlock
}

func (r *reviewer) ask(_ context.Context, _ state.Agent, model string, prompt []acp.ContentBlock) (string, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.models = append(r.models, model)
	r.prompts = append(r.prompts, prompt)
	answer := r.answers[0]
	if len(r.answers) > 1 {
		r.answers = r.answers[1:]
	}
	return answer, model + "-4-5", nil
}

func (r *reviewer) asked() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.prompts)
}

// text is the review prompt's text blocks, and how many images it carried.
func (r *reviewer) text(i int) (string, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var b strings.Builder
	images := 0
	for _, block := range r.prompts[i] {
		switch block.Type {
		case "text":
			b.WriteString(block.Text + "\n")
		case "image":
			images++
		}
	}
	return b.String(), images
}

// reviewDaemon is restartDaemon with a reviewer behind askReview, and a
// project whose agent-01 has a task, a change and a screenshot.
func reviewDaemon(t *testing.T, tool *sayTool, rv *reviewer) (testDaemon, state.Agent, state.Agent) {
	t.Helper()
	ctx := context.Background()
	instances := `[{"name":"ab-hello-stack-agent-01","status":"Running","config":{},"expanded_config":{},` +
		`"state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.8.8.2"}]}}}}]`
	d := startTestDaemon(t, t.TempDir(), wakeIncus, testConfig{instances: instances, setup: func(s *Server) {
		s.chat.Launch = tool.launch
		s.chat.Prepare = func(context.Context, state.Agent, string, int64) error { return nil }
		if rv != nil {
			s.askReview = rv.ask
		}
	}})
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	lead := leadReadyToChat(t, d, "hello-stack")
	a := addAgent(t, d, repo, "hello-stack", "agent-01", "Reminders page")
	d.srv.captureEvent(ctx, a.Project, a.Name, "agent_created", map[string]any{"task": "Add a reminders page, twenty to a page."}, "")
	if err := os.WriteFile(filepath.Join(a.Worktree, "reminders.mjs"), []byte("export const perPage = 20;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	addScreenshot(t, d, a, "reminders-after")
	return d, lead, a
}

// addScreenshot saves a screenshot to a's media, as `agentbox media
// screenshot` does.
func addScreenshot(t *testing.T, d testDaemon, a state.Agent, name string) {
	t.Helper()
	id := "20261126T120000-" + name
	dir := filepath.Join(d.srv.manager(nil).MediaDir(a.Project, a.Name), id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 2400, 1200))
	for x := range 2400 {
		img.Set(x, 600, color.RGBA{R: 200, A: 255})
	}
	f, err := os.Create(filepath.Join(dir, "shot.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if err := d.srv.store.AddMedia(context.Background(), state.Media{
		ID: id, Project: a.Project, Agent: a.Name, Kind: agent.MediaScreenshot, Name: name,
		File: id + "/shot.png", Mime: "image/png", Source: "agent", Meta: "{}", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
}

// reviewsOf is a project's reviews, oldest first.
func reviewsOf(t *testing.T, d testDaemon, project string) []api.AgentReview {
	t.Helper()
	rows, err := d.srv.store.AgentEvents(context.Background(), project)
	if err != nil {
		t.Fatal(err)
	}
	var out []api.AgentReview
	for _, row := range rows {
		var ev api.AgentEvent
		if json.Unmarshal(row.Data, &ev) == nil && ev.Kind == api.AgentReviewed && ev.Review != nil {
			out = append(out, *ev.Review)
		}
	}
	slices.Reverse(out)
	return out
}

// finishNotices is what the lead was told of the agent's finishes.
func finishNotices(t *testing.T, d testDaemon, lead state.Agent, ref string) []string {
	t.Helper()
	th, err := d.srv.chat.Thread(lead)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, it := range th.Items {
		if it.Kind == "notice" && slices.Contains(it.Finished, ref) {
			out = append(out, it.Text)
		}
	}
	return out
}

// A finish of work the lead asked for is reviewed first, on Sonnet, with the
// task, the diff and the screenshot in front of it; a pass tells the lead,
// once, and says so.
func TestReviewPassTellsLead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tool := &sayTool{replies: []string{"Added the reminders page; screenshot reminders-after."}}
	rv := &reviewer{answers: []string{"The page matches the task.\n\nVERDICT: PASS"}}
	d, lead, a := reviewDaemon(t, tool, rv)
	d.srv.leadAsked(a)
	if _, err := d.client.SendChat(ctx, a.Ref(), "Add a reminders page, twenty to a page."); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the lead to be told", func() bool { return len(finishNotices(t, d, lead, a.Ref())) > 0 })
	notices := finishNotices(t, d, lead, a.Ref())
	if len(notices) != 1 || !strings.Contains(strings.SplitN(notices[0], "\n", 2)[0], "Reviewed: pass.") {
		t.Errorf("the lead was told %q, want one finish that says it passed", notices)
	}
	if rv.asked() != 1 || rv.models[0] != "sonnet" {
		t.Fatalf("reviewed %d times on %q, want once on sonnet", rv.asked(), rv.models)
	}
	prompt, images := rv.text(0)
	for _, want := range []string{"Add a reminders page, twenty to a page.", "reminders.mjs", "perPage = 20", `screenshot "reminders-after"`, "VERDICT: PASS", "Added the reminders page"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the review's prompt lacks %q:\n%s", want, prompt)
		}
	}
	if images != 1 {
		t.Errorf("the review saw %d images, want the screenshot", images)
	}
	reviews := reviewsOf(t, d, a.Project)
	if len(reviews) != 1 || reviews[0].Verdict != api.AgentReviewPass || reviews[0].Round != 1 || reviews[0].Model != "sonnet-4-5" || reviews[0].Images != 1 || reviews[0].Finish == "" {
		t.Errorf("reviews = %+v, want one pass of the finish", reviews)
	}
	if n := tool.got("found problems"); n != 0 {
		t.Errorf("a passing agent was sent back %d times", n)
	}
}

// Problems go back to the agent before the lead hears anything; its next
// finish is reviewed again, against them, and a pass then tells the lead
// what was fixed. Problems a second time tell the lead, and the agent isn't
// sent back again.
func TestReviewSendsBackOnce(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, second, want string
	}{
		{"fixed", "VERDICT: PASS", "Reviewed: pass, after it was sent back once"},
		{"left", "VERDICT: PROBLEMS\n- The pager still shows 10 a page.", "Reviewed: problems. A first review sent it back once"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			tool := &sayTool{replies: []string{"Added the reminders page.", "Fixed: twenty a page now."}}
			rv := &reviewer{answers: []string{"Looking at it.\n\nVERDICT: PROBLEMS\n- The pager shows 10 a page, not 20 (reminders-after).\n- No screenshot of an empty list.", tc.second}}
			d, lead, a := reviewDaemon(t, tool, rv)
			d.srv.leadAsked(a)
			if _, err := d.client.SendChat(ctx, a.Ref(), "Add a reminders page, twenty to a page."); err != nil {
				t.Fatal(err)
			}
			waitFor(t, "the lead to be told", func() bool { return len(finishNotices(t, d, lead, a.Ref())) > 0 })
			if n := tool.got("found problems"); n != 1 {
				t.Errorf("sent back %d times, want once", n)
			}
			if n := tool.got("The pager shows 10 a page, not 20"); n != 1 {
				t.Errorf("told the problem %d times, want once", n)
			}
			notices := finishNotices(t, d, lead, a.Ref())
			if len(notices) != 1 || !strings.Contains(notices[0], tc.want) {
				t.Fatalf("the lead was told %q, want one finish with %q", notices, tc.want)
			}
			if !strings.Contains(notices[0], "Fixed: twenty a page now.") {
				t.Errorf("the notice isn't of the second finish: %q", notices[0])
			}
			if rv.asked() != 2 {
				t.Fatalf("reviewed %d times, want twice", rv.asked())
			}
			if second, _ := rv.text(1); !strings.Contains(second, "This is a second review") || !strings.Contains(second, "The pager shows 10 a page, not 20") {
				t.Errorf("the second review wasn't told what the first sent back:\n%s", second)
			}
			reviews := reviewsOf(t, d, a.Project)
			if len(reviews) != 2 || reviews[0].Verdict != api.AgentReviewProblems || !reviews[0].SentBack || len(reviews[0].Problems) != 2 ||
				reviews[1].Round != 2 || len(reviews[1].Earlier) != 2 || reviews[1].SentBack || reviews[0].Finish == reviews[1].Finish {
				t.Errorf("reviews = %+v", reviews)
			}
			if tc.name == "left" && !strings.Contains(notices[0], "- The pager still shows 10 a page.") {
				t.Errorf("the lead wasn't told what is left: %q", notices[0])
			}
		})
	}
}

// With the project's reviews off, a finish reaches the lead at once, as it
// always did, and nothing is reviewed.
func TestReviewOff(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tool := &sayTool{replies: []string{"Added the reminders page."}}
	rv := &reviewer{answers: []string{"VERDICT: PROBLEMS\n- Everything."}}
	d, lead, a := reviewDaemon(t, tool, rv)
	p, err := d.client.Project(ctx, a.Project)
	if err != nil || !p.ReviewFinishes {
		t.Fatalf("a new project reviews finishes: %v (%v)", p.ReviewFinishes, err)
	}
	off := false
	if p, err := d.client.UpdateProject(ctx, a.Project, api.UpdateProjectRequest{ReviewFinishes: &off}); err != nil || p.ReviewFinishes {
		t.Fatalf("turning reviews off: %+v, %v", p.ReviewFinishes, err)
	}
	d.srv.leadAsked(a)
	if _, err := d.client.SendChat(ctx, a.Ref(), "Add a reminders page."); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the lead to be told", func() bool { return len(finishNotices(t, d, lead, a.Ref())) > 0 })
	if notice := finishNotices(t, d, lead, a.Ref())[0]; strings.Contains(notice, "eviewed") {
		t.Errorf("the lead was told %q, of a review that is off", notice)
	}
	if rv.asked() != 0 || tool.got("found problems") != 0 || len(reviewsOf(t, d, a.Project)) != 0 {
		t.Errorf("reviewed %d times with reviews off", rv.asked())
	}
}

// Work the lead didn't ask for — a turn somebody drove from the agent's own
// chat — isn't reviewed: problems sent into it would talk over them.
func TestReviewOnlyLeadsWork(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tool := &sayTool{replies: []string{"Added the reminders page."}}
	rv := &reviewer{answers: []string{"VERDICT: PASS"}}
	d, lead, a := reviewDaemon(t, tool, rv)
	if _, err := d.client.SendChat(ctx, a.Ref(), "Add a reminders page."); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the lead to be told", func() bool { return len(finishNotices(t, d, lead, a.Ref())) > 0 })
	if rv.asked() != 0 {
		t.Errorf("reviewed %d times, for a turn the lead didn't ask for", rv.asked())
	}
}

func TestParseReview(t *testing.T) {
	for _, tc := range []struct {
		answer   string
		verdict  string
		problems []string
	}{
		{"All good.\n\nVERDICT: PASS", api.AgentReviewPass, nil},
		{"verdict: **pass**", api.AgentReviewPass, nil},
		{"VERDICT: PROBLEMS\n- one\n* two\n1. three\n2) four", api.AgentReviewProblems, []string{"one", "two", "three", "four"}},
		// The last verdict counts: a reviewer quoting the format first.
		{"I'll end with VERDICT: PASS or VERDICT: PROBLEMS.\n\nVERDICT: PROBLEMS\nThe page is blank.", api.AgentReviewProblems, []string{"The page is blank."}},
		{"VERDICT: PROBLEMS", api.AgentReviewProblems, []string{"The review found problems and didn't say which."}},
		{"Looks fine to me.", "", nil},
		{"VERDICT: MAYBE", "", nil},
	} {
		verdict, problems, ok := parseReview(tc.answer)
		if verdict != tc.verdict || ok != (tc.verdict != "") || !slices.Equal(problems, tc.problems) {
			t.Errorf("parseReview(%q) = %q, %q, %v", tc.answer, verdict, problems, ok)
		}
	}
	many := "VERDICT: PROBLEMS\n" + strings.Repeat("- "+strings.Repeat("x", 900)+"\n", 9)
	if _, problems, _ := parseReview(many); len(problems) != maxReviewProblems || len(problems[0]) > maxReviewProblem+10 {
		t.Errorf("%d problems of %d bytes, want them capped", len(problems), len(problems[0]))
	}
}

// Each verdict reads as the lead's brief says it does.
func TestReviewedNotice(t *testing.T) {
	ev := api.AgentEvent{Agent: "agent-01", Title: "Reminders", Summary: "Added the page.", Changes: &api.AgentChanges{Files: 2, Insertions: 10, Deletions: 1}}
	for _, tc := range []struct {
		review *api.AgentReview
		first  string // on the notice's first line
		after  string // further down
	}{
		{nil, "finished.", ""},
		{&api.AgentReview{Verdict: api.AgentReviewPass, Round: 1}, "Reviewed: pass.", ""},
		{&api.AgentReview{Verdict: api.AgentReviewPass, Round: 2, Earlier: []string{"No empty state."}}, "", "Reviewed: pass, after it was sent back once to fix what a first review found:\n- No empty state."},
		{&api.AgentReview{Verdict: api.AgentReviewProblems, Round: 2, Problems: []string{"Still 10 a page."}}, "", "Reviewed: problems. A first review sent it back once; a second still finds these, and it won't be sent back again:\n- Still 10 a page."},
		{&api.AgentReview{Verdict: api.AgentReviewProblems, Round: 1, Problems: []string{"Blank page."}, Note: "couldn't be sent back: the chat is gone"}, "", "Reviewed: problems, which couldn't be sent back to it (the chat is gone):\n- Blank page."},
		{&api.AgentReview{Verdict: api.AgentReviewError, Round: 1, Note: "Claude Code wouldn't run on \"sonnet\"."}, "Not reviewed: the review couldn't run (Claude Code wouldn't run on \"sonnet\").", ""},
	} {
		got := reviewedNotice(ev, tc.review)
		first, rest, _ := strings.Cut(got, "\n")
		if !strings.HasPrefix(got, "agent-01 (Reminders) finished.") || !strings.Contains(first, tc.first) || !strings.Contains(rest, tc.after) || !strings.Contains(got, "Added the page.") {
			t.Errorf("reviewedNotice(%+v) =\n%s", tc.review, got)
		}
	}
}

// A screenshot is sent as a JPEG no larger than a model reads well.
func TestScaledJPEG(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shot.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, image.NewNRGBA(image.Rect(0, 0, 3000, 1000))); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	out, err := scaledJPEG(path, reviewImageEdge)
	if err != nil {
		t.Fatal(err)
	}
	cfg, format, err := image.DecodeConfig(strings.NewReader(string(out)))
	if err != nil || format != "jpeg" || cfg.Width != reviewImageEdge || cfg.Height != 522 {
		t.Errorf("scaled to %s %dx%d (%v)", format, cfg.Width, cfg.Height, err)
	}
	if _, err := scaledJPEG(filepath.Join(t.TempDir(), "missing.png"), reviewImageEdge); err == nil {
		t.Error("a missing screenshot was read")
	}
}

// A recording's frames are taken spread over it, where ffmpeg is.
func TestRecordingFrames(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg here")
	}
	path := filepath.Join(t.TempDir(), "take.mp4")
	if out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc=duration=8:size=320x240:rate=10", "-pix_fmt", "yuv420p", path).CombinedOutput(); err != nil {
		t.Skipf("ffmpeg couldn't make a recording: %v: %s", err, out)
	}
	s := &Server{}
	frames, err := s.recordingFrames(context.Background(), state.Agent{}, path, 0, 8)
	if err != nil {
		t.Fatal(err)
	}
	var at []string
	for _, f := range frames {
		if cfg, format, err := image.DecodeConfig(strings.NewReader(string(f.jpeg))); err != nil || format != "jpeg" || cfg.Width != 320 {
			t.Errorf("frame at %s: %s %dx%d (%v)", f.at, format, cfg.Width, cfg.Height, err)
		}
		at = append(at, f.at)
	}
	if fmt.Sprint(at) != "[1s in 3s in 5s in 7s in]" {
		t.Errorf("frames at %v, want four spread over it", at)
	}
}
