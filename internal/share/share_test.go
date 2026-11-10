package share

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
)

func TestMessagesKeepWhatWasSaidAndEachTurnsLastWords(t *testing.T) {
	t0 := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	items := []api.ChatItem{
		{ID: "u1", Turn: "u1", Kind: "user", Text: "Fix the footer", CreatedAt: t0},
		{ID: "a1", Turn: "u1", Kind: "assistant", Text: "Looking at it.", CreatedAt: t0.Add(time.Minute)},
		{ID: "t1", Turn: "u1", Kind: "tool", Tool: &api.ChatTool{Kind: "read"}, CreatedAt: t0.Add(2 * time.Minute)},
		{ID: "a2", Turn: "u1", Kind: "assistant", Text: "Fixed: the footer wraps now.", CreatedAt: t0.Add(3 * time.Minute)},
		{ID: "h1", Turn: "h1", Kind: "user", Text: "agent-02 finished.", Hidden: true, CreatedAt: t0.Add(4 * time.Minute)},
		{ID: "w1", Turn: "w1", Kind: "user", Woken: true, CreatedAt: t0.Add(5 * time.Minute)},
		{ID: "u2", Turn: "u2", Kind: "user", Text: "Also in dark mode?", CreatedAt: t0.Add(6 * time.Minute)},
		{ID: "s1", Turn: "u2", Kind: "assistant", Text: "a subagent's words", Parent: "x", CreatedAt: t0.Add(7 * time.Minute)},
		{ID: "a3", Turn: "u2", Kind: "assistant", Text: "Yes, checked in both.", CreatedAt: t0.Add(8 * time.Minute)},
	}
	if got := Task(items); got != "Fix the footer" {
		t.Errorf("task %q", got)
	}
	var said []string
	for _, m := range Messages(items) {
		said = append(said, m.From+": "+m.Text)
	}
	want := []string{"agent: Fixed: the footer wraps now.", "user: Also in dark mode?", "agent: Yes, checked in both."}
	if strings.Join(said, "\n") != strings.Join(want, "\n") {
		t.Errorf("messages:\n%s\nwant:\n%s", strings.Join(said, "\n"), strings.Join(want, "\n"))
	}
}

func TestRedactorTakesOutSecretsEnvironmentAndHomes(t *testing.T) {
	r := NewRedactor([]string{"hunter2-but-longer", "abc"}, []string{"/home/lint/.local/share/agentbox/worktrees/p/agent-01"}, "/home/lint")
	for in, want := range map[string]string{
		"curl -H 'X: hunter2-but-longer' x":                                   "curl -H 'X: [redacted]' x",
		"/home/lint/.local/share/agentbox/worktrees/p/agent-01/internal/a.go": "internal/a.go",
		"cd /home/lint/.local/share/agentbox/worktrees/p/agent-01 && ls":      "cd . && ls",
		"/home/lint/.local/share/agentbox/worktrees/p/agent-012/x":            "~/.local/share/agentbox/worktrees/p/agent-012/x",
		"open /home/lint/notes.txt and /home/maria/x":                         "open ~/notes.txt and /home/[user]/x",
		"DATABASE_URL=postgres://u:p@db/x":                                    "DATABASE_URL=[redacted]",
		"export STRIPE=sk_live_1234\n  CGO_ENABLED=0":                         "export STRIPE=[redacted]\n  CGO_ENABLED=[redacted]",
		"UPDATE_TS=1 go test ./internal/api":                                  "UPDATE_TS=1 go test ./internal/api",
		"token: ghp_abcdefghijklmnopqrstuvwxyz0123":                           "token: [redacted]",
		"abc is too short to be taken for a secret":                           "abc is too short to be taken for a secret",
	} {
		if got := r.Redact(in); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMarkdownEscapesAndLinksOnlyTheWeb(t *testing.T) {
	got := string(markdown("**Done**: `<b>` fixed, see [PR](https://github.com/x/y/pull/1) and [x](javascript:alert(1))\n\n- one\n- <script>alert(1)</script>\n\n```go\nif a < b {}\n```"))
	for _, want := range []string{
		"<strong>Done</strong>", "<code>&lt;b&gt;</code>", `<a href="https://github.com/x/y/pull/1" rel="noopener noreferrer">PR</a>`,
		"<li>&lt;script&gt;alert(1)&lt;/script&gt;</li>", `<pre class="code" data-lang="go"><code>if a &lt; b {}</code></pre>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("markdown lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "<script") || strings.Contains(got, `href="javascript`) {
		t.Errorf("markdown let markup through:\n%s", got)
	}
}

// The page needs nothing from the network: no address it loads from, only
// links a reader may follow.
func TestPageIsSelfContainedAndRedacted(t *testing.T) {
	var b bytes.Buffer
	run := sampleRun()
	run.Task += "\n\nThe key is hunter2-but-longer, in /home/lint/secret.txt."
	if err := Write(&b, run, Options{Redactor: NewRedactor([]string{"hunter2-but-longer"}, nil, "/home/lint"), Picture: generatedPicture}); err != nil {
		t.Fatal(err)
	}
	page := b.String()
	if loads := regexp.MustCompile(`(?i)\b(src|srcset|poster|data)\s*=\s*"(https?:)?//`).FindString(page); loads != "" {
		t.Errorf("the page loads %s", loads)
	}
	if regexp.MustCompile(`(?i)<link\b|@import|url\(\s*['"]?https?:`).MatchString(page) {
		t.Error("the page loads a stylesheet or a font")
	}
	for _, want := range []string{"Made with", Repo, "data:image/png;base64,", "The key is [redacted], in ~/secret.txt.", "Opened the pull request", "#562", "internal/share/page.go"} {
		if !strings.Contains(page, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
	if strings.Contains(page, "hunter2-but-longer") || strings.Contains(page, "/home/lint") {
		t.Error("the page has what should have been redacted")
	}
}

func TestPicturesStopAtTheirBudget(t *testing.T) {
	var b bytes.Buffer
	if err := Write(&b, sampleRun(), Options{Picture: generatedPicture, MaxPictureBytes: 1}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "data:image/png") || !strings.Contains(b.String(), "left out to keep this page small") {
		t.Error("pictures past the budget were put in, or not named")
	}
}

// TestSamplePage writes the sample run's page to $AGENTBOX_SHARE_SAMPLE, to
// look at, with the PNGs in $AGENTBOX_SHARE_PICTURES as its screenshots
// (generated ones otherwise).
func TestSamplePage(t *testing.T) {
	out := os.Getenv("AGENTBOX_SHARE_SAMPLE")
	if out == "" {
		t.Skip("set AGENTBOX_SHARE_SAMPLE to write the sample page")
	}
	picture := generatedPicture
	if dir := os.Getenv("AGENTBOX_SHARE_PICTURES"); dir != "" {
		files, _ := filepath.Glob(filepath.Join(dir, "*.png"))
		jpgs, _ := filepath.Glob(filepath.Join(dir, "*.jpg"))
		files = append(files, jpgs...)
		n := 0
		picture = func(m api.MediaItem) (Picture, bool) {
			if len(files) == 0 {
				return Picture{}, false
			}
			data, err := os.ReadFile(files[n%len(files)])
			n++
			mime := "image/png"
			if strings.HasSuffix(files[(n-1)%len(files)], ".jpg") {
				mime = "image/jpeg"
			}
			return Picture{Mime: mime, Data: data}, err == nil
		}
	}
	var b bytes.Buffer
	if err := Write(&b, sampleRun(), Options{Redactor: NewRedactor(nil, []string{"/home/lint/.local/share/agentbox/worktrees/agentbox/agent-562"}, "/home/lint"), Picture: picture}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func generatedPicture(m api.MediaItem) (Picture, bool) {
	img := image.NewRGBA(image.Rect(0, 0, 320, 200))
	for y := range 200 {
		for x := range 320 {
			img.Set(x, y, color.RGBA{uint8(40 + x/4), uint8(30 + y/4), 120, 255})
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return Picture{Mime: "image/png", Data: b.Bytes()}, true
}

// sampleRun is a run like a real one: this package's own.
func sampleRun() Run {
	t0 := time.Date(2026, 10, 24, 3, 20, 0, 0, time.UTC)
	at := func(m float64) time.Time { return t0.Add(time.Duration(m * float64(time.Minute))) }
	wt := "/home/lint/.local/share/agentbox/worktrees/agentbox/agent-562/"
	task := "Let a user share what an agent did, as one self-contained HTML file anyone can open (no network, no AgentBox): the task, the timeline of steps with the screenshots inline, the chat condensed to its key messages, the diff summary and the PR link, with a small \"Made with AgentBox\" footer linking the repo.\n\n- Add a **Share** button in the agent's review and timeline\n- Add `agentbox share <agent> -o run.html`\n- Include a redaction pass: no secrets, env values or tokens, and file paths under the user's home shortened."
	steps := []api.TimelineStep{
		{Kind: api.TimelineKindTask, Title: firstLine(task), Start: at(0), End: at(0), Lines: strings.Split(task, "\n")},
		{Kind: api.TimelineKindRead, Start: at(0.5), End: at(6), Calls: 23, Paths: []string{wt + "internal/api/timeline.go", wt + "internal/timeline/timeline.go", wt + "internal/redact/redact.go", wt + "internal/report/redact.go", wt + "internal/daemon/agenttimeline.go", wt + "desktop/src/renderer/components/timeline/Timeline.tsx", wt + "desktop/src/renderer/components/ReadyToReview.tsx", wt + "desktop/src/main/chatfiles.ts"}},
		{Kind: api.TimelineKindPlan, Start: at(6.2), End: at(41), Lines: []string{"[x] Render the page in Go, pure, in internal/share", "[x] Serve it at GET /v1/agents/{project}/{agent}/share, redacted", "[x] agentbox share <agent> -o run.html", "[~] A Share button in the review and the timeline", "[ ] Screenshots of the page in light and dark"}},
		{Kind: api.TimelineKindEdit, Start: at(7), End: at(19), Calls: 6, Added: 612, Removed: 3, Paths: []string{wt + "internal/share/share.go", wt + "internal/share/page.go", wt + "internal/share/page.html.tmpl", wt + "internal/share/markdown.go"},
			Lines: []string{"--- " + wt + "internal/share/page.go", "+// Write writes run as a page, redacted.", "+func Write(w io.Writer, run Run, opt Options) error {", "+\trun = opt.Redactor.Redacted(run)", "--- " + wt + "internal/gitrepo/gitrepo.go", "-\t\"sort\"", "+\t\"sort\"", "+\t\"strconv\""}},
		{Kind: api.TimelineKindTest, Title: "go test ./internal/share", Status: "failed", Start: at(19.5), End: at(20.4), Calls: 1, Lines: []string{"$ go test ./internal/share", "--- FAIL: TestRedactorTakesOutSecretsEnvironmentAndHomes (0.00s)", `    share_test.go:61: Redact("cd …/agent-01 && ls") = "cd …/agent-01 && ls", want "cd . && ls"`, "FAIL"}},
		{Kind: api.TimelineKindEdit, Start: at(20.6), End: at(21.5), Calls: 1, Added: 4, Removed: 1, Paths: []string{wt + "internal/share/share.go"}, Lines: []string{"--- " + wt + "internal/share/share.go", "-\t\ts = strings.ReplaceAll(s, root, \".\")", "+\t\ts = replaceWhole(s, root, \".\")"}},
		{Kind: api.TimelineKindTest, Title: "go test ./internal/share ./internal/daemon -run Share", Status: "passed", Start: at(22), End: at(23.1), Calls: 2, Lines: []string{"$ go test ./internal/share ./internal/daemon -run Share", "ok  \tagentbox/internal/share\t0.041s", "ok  \tagentbox/internal/daemon\t0.634s"},
			Media: []api.MediaItem{{ID: "r1", Kind: "report", Name: "share-tests", Meta: api.MediaMeta{Tests: &api.TestCounts{Passed: 12}}}}},
		{Kind: api.TimelineKindEdit, Start: at(24), End: at(33), Calls: 9, Added: 141, Removed: 2, Paths: []string{wt + "desktop/src/renderer/components/ShareButton.tsx", wt + "desktop/src/renderer/components/ReadyToReview.tsx", wt + "desktop/src/renderer/components/v2/RightPanel.tsx", wt + "desktop/src/main/share.ts", wt + "desktop/src/preload/index.ts", wt + "desktop/src/shared/i18n/en-US.ts", wt + "desktop/src/shared/i18n/pt-BR.ts"},
			Lines: []string{"--- " + wt + "desktop/src/renderer/components/ReadyToReview.tsx", "+                <ShareButton agentRef={item.ref} />"}},
		{Kind: api.TimelineKindRun, Title: "npm --prefix desktop run typecheck", Start: at(33.5), End: at(34.6), Calls: 1, Lines: []string{"$ npm --prefix desktop run typecheck", "> agentbox@0.15.0 typecheck", "> tsc --noEmit -p ."}},
		{Kind: api.TimelineKindDesktop, Title: "Open the review, click Share and save the page", Start: at(35), End: at(39), Calls: 14,
			Media: []api.MediaItem{
				{ID: "s1", Kind: "screenshot", Name: "share-button-after", Meta: api.MediaMeta{Width: 1600, Height: 1000}, CreatedAt: at(36)},
				{ID: "v1", Kind: "recording", Name: "share-walkthrough", Meta: api.MediaMeta{Duration: 48, Width: 1600, Height: 1000}, CreatedAt: at(37)},
				{ID: "n1", Kind: "note", Text: "The page saved opens with no network: light, then dark.", CreatedAt: at(38)},
			}},
		{Kind: api.TimelineKindDesktop, Title: "Open the exported page in light and dark", Start: at(39.2), End: at(41), Calls: 6,
			Media: []api.MediaItem{
				{ID: "s2", Kind: "screenshot", Name: "share-page-light", CreatedAt: at(40)},
				{ID: "s3", Kind: "screenshot", Name: "share-page-dark", CreatedAt: at(40.5)},
			}},
		{Kind: api.TimelineKindPR, Start: at(42), End: at(43), Calls: 2},
	}
	return Run{
		Project: "agentbox", Agent: "agent-562", Title: "Share an agent's run as a page", AI: "claude", Branch: "agentbox/feat-share-run-page",
		Task:     task,
		Timeline: api.Timeline{Steps: steps, Start: t0, End: at(43)},
		Messages: []Message{
			{From: "agent", At: at(23.5), Text: "The page renders, and its tests pass. It's written by a new package, `internal/share`, and served at `GET /v1/agents/{project}/{agent}/share`:\n\n- **Self-contained**: screenshots as data URLs, a frame of each recording, styles and script inline, a CSP that loads nothing.\n- **Redacted** where the secrets are known: their values, the GitHub token, the worktree's path and your home.\n\nNext is the Share button."},
			{From: "user", At: at(30), Text: "Does it work for a past agent too?"},
			{From: "agent", At: at(43.5), Text: "Yes: a past agent's page comes from the chat and media it left, with its pull request as last read; its files are the ones its edits named, since its worktree is gone.\n\nI opened [#562](https://github.com/leciric/agentbox/pull/562) with screenshots of the button and the page in light and dark."},
		},
		Files: []File{
			{Path: "internal/share/page.html.tmpl", Added: 318},
			{Path: "internal/share/page.go", Added: 276},
			{Path: "internal/share/share.go", Added: 241},
			{Path: "internal/share/share_test.go", Added: 212},
			{Path: "internal/share/markdown.go", Added: 121},
			{Path: "internal/daemon/agentshare.go", Added: 164},
			{Path: "internal/daemon/agentshare_test.go", Added: 98},
			{Path: "internal/cli/share.go", Added: 52},
			{Path: "internal/gitrepo/gitrepo.go", Added: 33, Removed: 1},
			{Path: "desktop/src/renderer/components/ShareButton.tsx", Added: 58},
			{Path: "desktop/src/renderer/components/ReadyToReview.tsx", Added: 2},
			{Path: "desktop/src/main/share.ts", Added: 47},
			{Path: "desktop/src/shared/i18n/pt-BR.ts", Added: 6, Removed: 1},
			{Path: "desktop/build/icon.png", Binary: true},
		},
		PR:       &api.PullRequest{Number: 562, Title: "feat: share what an agent did as one HTML page anyone can open", State: "open", URL: "https://github.com/leciric/agentbox/pull/562"},
		Exported: at(44),
	}
}
