package daemon

import (
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/pressure"
	"agentbox/internal/state"
)

// Timing (state/timing.go): the chat times turns and tool calls itself
// (chat/timing.go); the daemon times heavy commands, as their agent says
// they ended, and answers GET /v1/timing.

// maxCommandDetail is how much of a command line a timing row keeps.
const maxCommandDetail = 300

// recordCommand queues the timing of a heavy command of a's that ended at
// now: as it said (end), or, from an agentbox that says nothing, from when
// its run started (ended), with its exit status unknown. Neither, and there
// is nothing to time.
func (s *Server) recordCommand(a state.Agent, end api.HeavyEnd, ended *pressure.Run, now time.Time) {
	row := state.TimingRow{Kind: state.TimingCommand, Project: a.Project, Agent: a.Name, AgentID: a.ID, AI: a.AI,
		Jobs: end.Jobs, ExitCode: end.ExitCode}
	command := end.Command
	switch {
	case end.Command != "":
		row.Wall = time.Duration(max(end.DurationMS, 0)) * time.Millisecond
		row.At = now.Add(-row.Wall)
	case ended != nil && !ended.Started.IsZero():
		command = ended.Command
		row.At, row.Wall, row.ExitCode = ended.Started, now.Sub(ended.Started), -1
	default:
		return
	}
	row.Name, row.Detail = commandName(command), headWords(command, maxCommandDetail)
	s.store.RecordTiming(row)
}

// commandName is the program a command line runs, past any VAR=value
// before it: "go" for `GOFLAGS=-p=2 go test ./...`, "gradlew" for
// `./gradlew build`.
func commandName(command string) string {
	for _, word := range strings.Fields(command) {
		if name, _, isVar := strings.Cut(word, "="); isVar && name != "" && !strings.ContainsAny(name, "/-") {
			continue
		}
		return filepath.Base(strings.Trim(word, `'"`))
	}
	return ""
}

// timingTop is how many of the slowest tools, tool calls and commands a
// timing report lists when not asked, and maxTimingTop the most it will.
const (
	timingTop    = 10
	maxTimingTop = 200
)

// timingReport is GET /v1/timing: ?project=, ?agent= (with its project or
// alone, that name in every project), ?since= (a time or a stretch back
// like 1h or 7d) and ?top=.
func (s *Server) timingReport(w http.ResponseWriter, r *http.Request) error {
	now := time.Now()
	q := r.URL.Query()
	f := state.TimingFilter{Project: q.Get("project"), Agent: q.Get("agent")}
	if raw := strings.TrimSpace(q.Get("since")); raw != "" {
		since, err := parseSince(raw, now)
		if err != nil {
			return err
		}
		f.Since = since
	}
	top := timingTop
	if raw := q.Get("top"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			return errors.New("top is how many of the slowest to list: a whole number over 0")
		}
		top = min(n, maxTimingTop)
	}
	// What ended a moment ago is still queued: written first, so it counts.
	if err := s.store.FlushTiming(r.Context()); err != nil {
		s.logf("timing: %v", err)
	}
	ctx := r.Context()
	sums, err := s.store.TimingSums(ctx, f)
	if err != nil {
		return err
	}
	tools, err := s.store.ToolTimeSums(ctx, f, top)
	if err != nil {
		return err
	}
	calls, err := s.store.SlowestTimings(ctx, state.TimingTool, f, top)
	if err != nil {
		return err
	}
	commands, err := s.store.SlowestTimings(ctx, state.TimingCommand, f, top)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, buildTimingReport(f, now, sums, tools, calls, commands))
}

// buildTimingReport makes the report from what the store added up.
func buildTimingReport(f state.TimingFilter, now time.Time, sums []state.TimingSum, tools []state.ToolTimeSum, calls, commands []state.TimingRow) api.TimingReport {
	report := api.TimingReport{Until: now, Agents: []api.AgentTiming{}, Tools: []api.ToolTiming{},
		SlowCalls: []api.TimedCall{}, SlowCommands: []api.TimedCommand{}}
	if !f.Since.IsZero() {
		since := f.Since
		report.Since = &since
	}
	for _, t := range sums {
		totals := api.TimingTotals{
			Turns: t.Turns, Answered: t.Answered,
			TurnMS: t.Wall.Milliseconds(), StartMS: t.Start.Milliseconds(),
			ModelMS: t.Model.Milliseconds(), ModelCalls: t.ModelCalls,
			FirstTokenMS: t.FirstToken.Milliseconds(), TTFTMS: t.TTFT.Milliseconds(),
			ToolMS: t.Tools.Milliseconds(), ToolCalls: t.ToolCalls,
			Commands: t.Commands, CommandMS: t.CommandTime.Milliseconds(), FailedCommands: t.FailedCommands,
		}
		report.Agents = append(report.Agents, api.AgentTiming{Ref: t.Project + "/" + t.Agent, TimingTotals: totals, LastAt: t.LastAt})
		addTimingTotals(&report.TimingTotals, totals)
	}
	for _, t := range tools {
		report.Tools = append(report.Tools, api.ToolTiming{Name: t.Name, Subagent: t.Sub, Calls: t.Calls, Failed: t.Failed,
			TotalMS: t.Total.Milliseconds(), MaxMS: t.Max.Milliseconds()})
	}
	for _, c := range calls {
		report.SlowCalls = append(report.SlowCalls, api.TimedCall{Agent: c.Project + "/" + c.Agent, Turn: c.Turn, Name: c.Name,
			Title: c.Detail, Subagent: c.Sub, Status: c.Status, At: c.At, MS: c.Wall.Milliseconds()})
	}
	for _, c := range commands {
		report.SlowCommands = append(report.SlowCommands, api.TimedCommand{Agent: c.Project + "/" + c.Agent, Command: c.Detail,
			Jobs: c.Jobs, ExitCode: c.ExitCode, At: c.At, MS: c.Wall.Milliseconds()})
	}
	return report
}

func addTimingTotals(to *api.TimingTotals, t api.TimingTotals) {
	to.Turns += t.Turns
	to.Answered += t.Answered
	to.TurnMS += t.TurnMS
	to.StartMS += t.StartMS
	to.ModelMS += t.ModelMS
	to.ModelCalls += t.ModelCalls
	to.FirstTokenMS += t.FirstTokenMS
	to.TTFTMS += t.TTFTMS
	to.ToolMS += t.ToolMS
	to.ToolCalls += t.ToolCalls
	to.Commands += t.Commands
	to.CommandMS += t.CommandMS
	to.FailedCommands += t.FailedCommands
}
