package share

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"fmt"
	"html/template"
	"io"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"agentbox/internal/api"
)

// Repo is where the page's footer sends a reader who wants AgentBox.
const Repo = "https://github.com/leciric/agentbox"

// Picture is an image to put in the page: a screenshot, or a frame of a
// recording.
type Picture struct {
	Mime string
	Data []byte
}

// Options are what Write needs beyond the run.
type Options struct {
	// Redactor is applied to every string of the run.
	Redactor Redactor
	// Picture reads a screenshot's file, or a frame from a recording; false
	// for one it can't. Nil puts no pictures in.
	Picture func(api.MediaItem) (Picture, bool)
	// MaxPictureBytes is the most the pictures may add up to, 40 MiB if 0:
	// the ones after it are named, not shown.
	MaxPictureBytes int
}

const defaultPictureBytes = 40 << 20

//go:embed page.html.tmpl
var pageSource string

var page = template.Must(template.New("page").Funcs(template.FuncMap{
	"markdown": markdown,
	"dir": func(p string) string {
		if i := strings.LastIndexByte(p, '/'); i >= 0 {
			return p[:i+1]
		}
		return ""
	},
	"base": path.Base,
	"maxChange": func(files []File) int {
		most := 1
		for _, f := range files {
			most = max(most, f.Added+f.Removed)
		}
		return most
	},
	// pct is n's share of the bar, the busiest file's being all of it.
	"pct": func(n, most int) string {
		return strconv.FormatFloat(float64(n)*100/float64(most), 'f', 1, 64)
	},
}).Parse(pageSource))

// Write writes run as a page, redacted.
func Write(w io.Writer, run Run, opt Options) error {
	run = opt.Redactor.Redacted(run)
	v := view(run, opt)
	var b bytes.Buffer
	if err := page.Execute(&b, v); err != nil {
		return err
	}
	_, err := w.Write(b.Bytes())
	return err
}

// FileName is what a run's page is saved as by default.
func FileName(project, agent string) string {
	name := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, project+"-"+agent)
	return name + ".html"
}

type pageView struct {
	Title, Project, Agent, AI, Branch, Task string
	TaskLong                                bool
	Start, End                              time.Time
	Duration                                string
	Steps                                   []stepView
	Counts                                  []count
	Messages                                []Message
	Files                                   []File
	FilesUncounted                          bool
	MoreFiles                               int
	Added, Removed                          int
	PR                                      *api.PullRequest
	Working                                 bool
	Exported                                time.Time
	Repo                                    string
	Skipped                                 int
}

type count struct {
	N     int
	Label string
}

type stepView struct {
	Kind, Title, Status, Offset string
	Mono                        bool
	At                          time.Time
	Calls, Added, Removed       int
	Lines                       []lineView
	Body                        string // a later message's words, past its first line
	Paths                       []string
	MorePaths                   int
	Shots                       []shotView
	Notes                       []string
	Reports                     []api.MediaItem
	Pages                       []api.TimelinePage
}

// lineView is one line a step shows, with what it is: a command, a file's
// name above its edits, a line added or removed.
type lineView struct {
	Text, Class string
}

func lineViews(kind string, lines []string) []lineView {
	out := make([]lineView, 0, len(lines))
	for _, l := range lines {
		lv := lineView{Text: l}
		switch {
		case strings.HasPrefix(l, "$ "):
			lv.Class = "cmd"
		case kind == api.TimelineKindEdit && strings.HasPrefix(l, "--- "):
			lv.Class, lv.Text = "file", strings.TrimPrefix(l, "--- ")
		case kind == api.TimelineKindEdit && strings.HasPrefix(l, "+"):
			lv.Class = "add"
		case kind == api.TimelineKindEdit && strings.HasPrefix(l, "-"):
			lv.Class = "del"
		case kind == api.TimelineKindPlan && strings.HasPrefix(l, "[x] "):
			lv.Class, lv.Text = "done", strings.TrimPrefix(l, "[x] ")
		case kind == api.TimelineKindPlan && strings.HasPrefix(l, "[~] "):
			lv.Class, lv.Text = "doing", strings.TrimPrefix(l, "[~] ")
		case kind == api.TimelineKindPlan && strings.HasPrefix(l, "[ ] "):
			lv.Class, lv.Text = "todo", strings.TrimPrefix(l, "[ ] ")
		}
		out = append(out, lv)
	}
	return out
}

// notShown are the paths no line names as the file its edits are in.
func notShown(paths []string, lines []lineView) []string {
	var out []string
	for _, p := range paths {
		if !slices.ContainsFunc(lines, func(l lineView) bool { return l.Class == "file" && l.Text == p }) {
			out = append(out, p)
		}
	}
	return out
}

type shotView struct {
	Name     string
	Src      template.URL
	Video    bool
	Duration string
	Width    int
	Height   int
}

// maxFiles is how many changed files a page lists, maxStepPaths how many
// paths a step does.
const (
	maxFiles     = 60
	maxStepPaths = 6
)

func view(run Run, opt Options) pageView {
	tl := run.Timeline
	v := pageView{
		Title: run.Title, Project: run.Project, Agent: run.Agent, AI: aiName(run.AI), Branch: run.Branch,
		Task: run.Task, Start: tl.Start, End: tl.End, PR: run.PR, Working: tl.Working,
		Messages: run.Messages, FilesUncounted: run.FilesUncounted,
		Exported: run.Exported.UTC(), Repo: Repo,
	}
	if v.Title == "" {
		v.Title = firstLine(run.Task)
	}
	if v.Title == "" {
		v.Title = run.Agent
	}
	v.TaskLong = strings.Count(run.Task, "\n") > 6 || len(run.Task) > 600
	if !tl.Start.IsZero() && tl.End.After(tl.Start) {
		v.Duration = duration(tl.End.Sub(tl.Start))
	}
	budget := opt.MaxPictureBytes
	if budget == 0 {
		budget = defaultPictureBytes
	}
	shots, tests := 0, 0
	for i, s := range tl.Steps {
		sv := stepView{
			Kind: s.Kind, Title: stepTitle(s), Status: s.Status, Mono: s.Kind == api.TimelineKindRun && s.Title != "",
			At: s.Start, Calls: s.Calls, Added: s.Added, Removed: s.Removed, Pages: s.Pages,
		}
		if i == 0 && s.Kind == api.TimelineKindTask && run.Task != "" {
			sv.Title = "Started on the task"
		}
		if !tl.Start.IsZero() {
			sv.Offset = offset(s.Start.Sub(tl.Start))
		}
		// A task's words are its title and lines both: shown once.
		// The first task is the page's Task; a later message shows its words
		// past the first line, its title.
		if s.Kind != api.TimelineKindTask {
			sv.Lines = lineViews(s.Kind, s.Lines)
			// A command its title names already.
			if sv.Mono && len(sv.Lines) > 0 && sv.Lines[0].Text == "$ "+s.Title {
				sv.Lines = sv.Lines[1:]
			}
		} else if i > 0 && len(s.Lines) > 1 {
			sv.Body = strings.TrimSpace(strings.Join(s.Lines[1:], "\n"))
		}
		if s.Kind == api.TimelineKindEdit || s.Kind == api.TimelineKindRead {
			sv.Paths = notShown(s.Paths, sv.Lines)
			if len(sv.Paths) > maxStepPaths {
				sv.MorePaths = len(sv.Paths) - maxStepPaths
				sv.Paths = sv.Paths[:maxStepPaths]
			}
		}
		if s.Kind == api.TimelineKindTest {
			tests++
		}
		for _, m := range s.Media {
			switch m.Kind {
			case "screenshot", "recording":
				shot := shotView{Name: m.Name, Video: m.Kind == "recording", Width: m.Meta.Width, Height: m.Meta.Height}
				if shot.Video && m.Meta.Duration > 0 {
					shot.Duration = duration(time.Duration(m.Meta.Duration * float64(time.Second)))
				}
				if opt.Picture != nil {
					if pic, ok := opt.Picture(m); ok && len(pic.Data) <= budget {
						budget -= len(pic.Data)
						shot.Src = template.URL("data:" + pic.Mime + ";base64," + base64.StdEncoding.EncodeToString(pic.Data)) //nolint:gosec // our own bytes, with a mime we chose
					} else if ok {
						v.Skipped++
					}
				}
				if shot.Src == "" && !shot.Video {
					continue
				}
				if !shot.Video {
					shots++
				}
				sv.Shots = append(sv.Shots, shot)
			case "note":
				if t := strings.TrimSpace(m.Text); t != "" {
					sv.Notes = append(sv.Notes, t)
				}
			case "report":
				if m.Meta.Tests != nil {
					sv.Reports = append(sv.Reports, m)
				}
			}
		}
		v.Steps = append(v.Steps, sv)
	}
	files := run.Files
	for _, f := range files {
		v.Added += f.Added
		v.Removed += f.Removed
	}
	if len(files) > maxFiles {
		v.MoreFiles = len(files) - maxFiles
		files = files[:maxFiles]
	}
	v.Files = files
	v.Counts = []count{
		{len(tl.Steps), plural(len(tl.Steps), "step", "steps")},
		{len(run.Files), plural(len(run.Files), "file changed", "files changed")},
		{shots, plural(shots, "screenshot", "screenshots")},
	}
	if tests > 0 {
		v.Counts = append(v.Counts, count{tests, plural(tests, "test run", "test runs")})
	}
	return v
}

// stepTitle is what a step is called, as the app calls it
// (useStepTitle in desktop/src/renderer/components/timeline/Timeline.tsx).
func stepTitle(s api.TimelineStep) string {
	files := s.Paths
	switch s.Kind {
	case api.TimelineKindTask:
		return s.Title
	case api.TimelineKindRead:
		if s.Title != "" {
			return s.Title
		}
		if len(files) > 0 {
			return fmt.Sprintf("Read %d %s", len(files), plural(len(files), "file", "files"))
		}
		return "Searched the code"
	case api.TimelineKindPlan:
		return "Planned the work"
	case api.TimelineKindEdit:
		switch len(files) {
		case 0:
			return "Edited files"
		case 1:
			return "Edited " + path.Base(files[0])
		default:
			return fmt.Sprintf("Edited %s and %d more", path.Base(files[0]), len(files)-1)
		}
	case api.TimelineKindTest:
		switch s.Status {
		case "failed":
			return "Tests failed"
		case "running":
			return "Running tests"
		}
		return "Tests passed"
	case api.TimelineKindDesktop:
		return or(s.Title, "Checked on the desktop")
	case api.TimelineKindPage:
		return or(s.Title, "Published a page")
	case api.TimelineKindPR:
		return "Opened the pull request"
	}
	return or(s.Title, "Ran a command")
}

func aiName(ai string) string {
	switch ai {
	case "claude", "":
		return "Claude Code"
	case "codex":
		return "Codex"
	case "opencode":
		return "OpenCode"
	case "cursor":
		return "Cursor"
	}
	return ai
}

func or(s, otherwise string) string {
	if s == "" {
		return otherwise
	}
	return s
}

func plural(n int, one, other string) string {
	if n == 1 {
		return one
	}
	return other
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if r := []rune(s); len(r) > 120 {
		s = strings.TrimSpace(string(r[:120])) + "…"
	}
	return s
}

// duration is d the way a person says it: 42s, 7m, 1h 20m.
func duration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Round(time.Second).Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Round(time.Minute).Minutes()))
	}
	d = d.Round(time.Minute)
	h, m := int(d.Hours()), int(d.Minutes())%60
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh %dm", h, m)
}

// offset is how far into the run a step began: 0:00, 12:04, 1:02:09.
func offset(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int(d.Seconds())
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}
