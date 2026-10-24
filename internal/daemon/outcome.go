package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/chat"
	"agentbox/internal/state"
)

// What an agent's turn left it doing.
//
// A turn ending is not the agent finishing. It ends the same way, end_turn,
// when the agent has opened its pull request, when it stops to ask "launch,
// change, or stop?" in its own words, when it says "I'll report when CI
// finishes" with nothing that will wake it, and when it is about to be woken
// by a background command it left. Calling all of those "finished" left
// agents sitting idle on a question nobody saw, and woke the lead to read a
// summary that wasn't one.
//
// So every turn's end is judged, from the most reliable signal down:
//
//  1. How the chat ended it (chat.TurnEnd): another turn already running or
//     AgentBox stopping are still work; a usage limit is a pause, resumed when
//     it resets, which is neither finished nor failed; a
//     cancellation is the user's; an error, the AI tool exiting, or a stop
//     reason other than end_turn is the agent stopped mid-work (failed).
//  2. Background work the session reports (Claude Code's tasks and
//     monitors): it is woken when that ends, so it is waiting, not done.
//  3. What it said last: a closing question is asking; a promise to report
//     on work, with no background task to keep it, or tool calls still open
//     as the turn ended, is the gap Codex and OpenCode leave, and the agent
//     is told once to wait for that work inside a turn.
//  4. What it has to show: a pull request or changes on its branch is done,
//     nothing is idle.
//  5. Only when the closing words are neither a plain question nor plainly
//     not one — "let me know which you prefer", "I'll wait for your go" —
//     with nothing to show, the cheap model reads them (askTurnModel): a few
//     hundred words, one word back, a handful of turns a day.
//
// The judgement is Agent.Outcome, which the rail and the sidebar show, and the
// notice the lead gets, which says the same thing.

// verdict is what judgeTurn makes of a turn's end.
type verdict string

const (
	verdictWorking    verdict = "working"    // still at it: nothing to say
	verdictPaused     verdict = "paused"     // cut short by a usage limit, resumed when it resets
	verdictCancelled  verdict = "cancelled"  // stopped by whoever drove it
	verdictFailed     verdict = "failed"     // stopped mid-work
	verdictBackground verdict = "background" // waits on work it left running
	verdictPromise    verdict = "promise"    // says it waits on work, but nothing will wake it
	verdictAsking     verdict = "asking"     // waits for an answer
	verdictUnsure     verdict = "unsure"     // perhaps asking: the cheap model reads it
	verdictEnded      verdict = "ended"      // done or idle, by what it has to show
)

// judgeTurn is the judgement made from the turn alone, before anything is
// looked up: what it has to show decides only between done and idle, and
// whether a closing question is one (see settleTurn). note is the error a
// failed turn stopped on.
func judgeTurn(end chat.TurnEnd) (v verdict, note string) {
	switch {
	case end.Shutdown, end.Continues:
		return verdictWorking, ""
	case end.Limited:
		return verdictPaused, firstLineOf(end.Error)
	case end.State == "cancelled":
		return verdictCancelled, ""
	case end.State == "failed":
		return verdictFailed, firstLineOf(end.Error)
	case end.StopReason != "" && end.StopReason != "end_turn":
		if n := chat.StopNote(end.StopReason); n != "" {
			return verdictFailed, n
		}
		return verdictFailed, "The turn stopped: " + end.StopReason + "."
	case len(end.Background) > 0:
		return verdictBackground, ""
	}
	closing := closingWords(end.LastMessage)
	switch {
	case endsOnQuestion(closing):
		return verdictAsking, ""
	case promisesToReport(closing) || len(end.Open) > 0:
		return verdictPromise, ""
	case mayBeAsking(closing):
		return verdictUnsure, ""
	}
	return verdictEnded, ""
}

var (
	fence     = regexp.MustCompile("(?s)```.*?```")
	listItem  = regexp.MustCompile(`^\s*([-*+•]|\d+[.)])\s`)
	trailJunk = " \t\r\n*_`\"'”’)]>"
	// sentenceEnd splits prose into sentences, keeping each one's mark.
	sentenceEnd = regexp.MustCompile(`[.!?…]+["'”’)*_]*(\s|$)`)
)

// closingWords is the end of what an agent said: its last paragraph, and the
// one before when the last is a list of options, with code left out. That is
// where an agent puts what it wants from you, and the only part read.
func closingWords(msg string) string {
	msg = strings.TrimSpace(fence.ReplaceAllString(msg, ""))
	var paras []string
	for _, p := range strings.Split(strings.ReplaceAll(msg, "\r\n", "\n"), "\n\n") {
		if p = strings.TrimSpace(p); p != "" {
			paras = append(paras, p)
		}
	}
	if len(paras) == 0 {
		return ""
	}
	out := paras[len(paras)-1]
	if isList(out) && len(paras) > 1 {
		out = paras[len(paras)-2] + "\n\n" + out
	}
	return out
}

func isList(p string) bool {
	for _, line := range strings.Split(p, "\n") {
		if strings.TrimSpace(line) != "" && !listItem.MatchString(line) {
			return false
		}
	}
	return true
}

// endsOnQuestion is closing words whose last sentence, or the line before a
// list of options, is a question: "What do you want me to do next: launch,
// change, or stop?"
func endsOnQuestion(closing string) bool {
	if closing == "" {
		return false
	}
	lines := strings.Split(closing, "\n")
	// Options listed after the question: the question is the line above them.
	for len(lines) > 1 && (strings.TrimSpace(lines[len(lines)-1]) == "" || listItem.MatchString(lines[len(lines)-1])) {
		lines = lines[:len(lines)-1]
	}
	last := strings.TrimRight(strings.Join(lines, "\n"), trailJunk)
	return strings.HasSuffix(last, "?")
}

// promised is an agent saying it will come back to something once work still
// running ends: what Claude Code's background tasks keep, and nothing keeps
// for Codex or OpenCode, or for an agent that only meant to start one.
var promised = regexp.MustCompile(`(?i)\b(i'll|i will|i'm going to|i am going to|will) (report|check back|let you know|follow up|update you|get back|circle back|come back)\b.*\b(when|once|after|as soon as)\b` +
	`|\b(once|when|as soon as) (it|ci|that|this|they|the (build|run|tests?|job|jobs|checks?|deploy\w*|pipeline|workflow|install\w*|download|script|command|suite))( \w+)? (finish\w*|complete\w*|is done|are done|pass\w*|end\w*|land\w*|come\w* back)\b` +
	`|\b(waiting|wait) (for|on) (ci|it to|the (build|run|tests?|job|jobs|checks?|deploy\w*|pipeline|workflow)|.{0,40}\bto (finish|complete))\b` +
	`|\b(running|left running|started|kicked off) in the background\b`)

func promisesToReport(closing string) bool {
	return closing != "" && promised.MatchString(closing)
}

// softAsks are the ways an agent asks for a decision without a question mark,
// or with one somewhere other than at the end. "Let me know if you need
// anything else" is one of them too, which is why these only make the model
// look, never decide.
var softAsks = regexp.MustCompile(`(?i)\b(let me know|would you like|do you want|want me to|shall i|should i|your call|your approval|your go|your decision|approve|confirm|which (option|one|approach|would)|how would you like|waiting for (your|you|an answer|a decision)|awaiting (your|you|an answer)|before i (proceed|continue|go on|start)|before proceeding|up to you)\b`)

func mayBeAsking(closing string) bool {
	if closing == "" {
		return false
	}
	if softAsks.MatchString(closing) {
		return true
	}
	for _, s := range sentenceEnd.FindAllString(closing, -1) {
		if strings.Contains(s, "?") {
			return true
		}
	}
	return false
}

func firstLineOf(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

// question is the question an agent's closing words ask, cut to fit a line
// of the rail's tooltip and the lead's notice: the line that asks it.
func question(closing string) string {
	lines := strings.Split(closing, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasSuffix(strings.TrimRight(lines[i], trailJunk), "?") {
			return headWords(strings.TrimSpace(lines[i]), 300)
		}
	}
	return headWords(closing, 300)
}

// turnCheckAsk is what the cheap model is asked about closing words the rules
// couldn't place. Only the closing words go, never the conversation.
const turnCheckAsk = `An AI coding agent ended its turn with the words below. Answer with exactly one word:
ASKING if they ask whoever drives it a question, or wait for a decision, an approval or an answer before it goes on;
WAITING if they say it waits for something still running (a build, CI, a command) and will carry on when that ends;
DONE otherwise (it reports what it did, or offers more help it doesn't need an answer to).

<words>
%s
</words>`

// turnCheckTimeout bounds the cheap model's look: the lead's notice waits
// for it, and without an answer the rules' lean stands.
const turnCheckTimeout = 90 * time.Second

// askTurnModel has the cheap model place closing words the rules couldn't,
// in a throwaway session on the agent's own tool and login (chat.AskAside).
// Without a cheap model for the tool, or an answer, it leans to asking: a
// question taken for a finish is an agent left idle, the failure this is
// for, and the other way round costs the lead one look.
func (s *Server) askTurnModel(ctx context.Context, a state.Agent, closing string) verdict {
	model := state.CheapModelFor(a.AI)
	if model == "" || s.askAside == nil {
		return verdictAsking
	}
	ctx, cancel := context.WithTimeout(ctx, turnCheckTimeout)
	defer cancel()
	answer, _, err := s.askAside(ctx, a, model, fmt.Sprintf(turnCheckAsk, headWords(closing, 1500)))
	if err != nil {
		s.logf("judging %s's turn: %v", a.Ref(), err)
		return verdictAsking
	}
	switch word := strings.ToUpper(strings.Trim(firstLineOf(answer), " .*`\"'")); {
	case strings.HasPrefix(word, "DONE"):
		return verdictEnded
	case strings.HasPrefix(word, "WAITING"):
		return verdictPromise
	default:
		return verdictAsking
	}
}

// agentOutcome is Agent.Outcome as the daemon keeps it.
type agentOutcome struct {
	kind, note string
	until      time.Time // when a limited agent resumes; zero when it won't
}

// setOutcome records how an agent's last turn left it.
func (s *Server) setOutcome(a state.Agent, kind, note string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.outcomes[a.Ref()] = agentOutcome{kind: kind, note: note}
}

// setOutcomeUntil is setOutcome for an outcome with a time.
func (s *Server) setOutcomeUntil(a state.Agent, o agentOutcome) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.outcomes[a.Ref()] = o
}

// outcomeOf is how an agent's last turn left it. A daemon that has just
// started has judged nothing yet, and reads it from the project's events
// once: the last finish, question or failure each agent recorded.
func (s *Server) outcomeOf(ctx context.Context, project, ref string) agentOutcome {
	s.mu.Lock()
	loaded := s.outcomesRead[project]
	s.outcomesRead[project] = true
	s.mu.Unlock()
	if !loaded {
		read := s.outcomesFromEvents(ctx, project)
		s.mu.Lock()
		for r, o := range read {
			if _, judged := s.outcomes[r]; !judged {
				s.outcomes[r] = o
			}
		}
		s.mu.Unlock()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.outcomes[ref]
}

func (s *Server) outcomesFromEvents(ctx context.Context, project string) map[string]agentOutcome {
	out := map[string]agentOutcome{}
	rows, err := s.store.AgentEvents(ctx, project)
	if err != nil {
		return out
	}
	seen := map[string]bool{}
	for _, row := range rows { // newest first
		if seen[row.Agent] {
			continue
		}
		var ev api.AgentEvent
		if json.Unmarshal(row.Data, &ev) != nil {
			continue
		}
		if o, ok := outcomeOfEvent(ev); ok {
			seen[row.Agent] = true
			if o.kind != "" {
				out[ev.Ref] = o
			}
		}
	}
	return out
}

// outcomeOfEvent is the outcome an event leaves, and whether it says
// anything about one: a creation clears it, a stall or a stop says nothing.
func outcomeOfEvent(ev api.AgentEvent) (agentOutcome, bool) {
	switch ev.Kind {
	case api.AgentCreated:
		return agentOutcome{}, true
	case api.AgentFinished:
		kind := ev.Outcome
		if kind == "" { // recorded before outcomes were
			kind = api.AgentOutcomeIdle
			if ev.PR != nil || (ev.Changes != nil && ev.Changes.Files > 0) {
				kind = api.AgentOutcomeDone
			}
		}
		return agentOutcome{kind: kind}, true
	case api.AgentAsking:
		return agentOutcome{kind: api.AgentOutcomeAsking, note: ev.Summary}, true
	case api.AgentLimited:
		o := agentOutcome{kind: api.AgentOutcomeLimited, note: ev.Summary}
		if ev.ResumeAt != nil {
			o.until = *ev.ResumeAt
		}
		return o, true
	case api.AgentFailed, api.AgentChatLost:
		return agentOutcome{kind: api.AgentOutcomeFailed, note: ev.Summary}, true
	}
	return agentOutcome{}, false
}

// shows is whether an agent has something to show for its work: a pull
// request, or changes on its branch.
func shows(changes api.AgentChanges, pr *api.PullRequest) bool {
	return pr != nil || changes.Files > 0
}

// promiseNudge is what an agent is told when it ended its turn saying it
// waits on work, with nothing that will wake it when the work ends.
func promiseNudge(a state.Agent, open []string) string {
	running := ""
	if len(open) > 0 {
		running = fmt.Sprintf(" Tool calls were still running when it ended (%s).", strings.Join(open, "; "))
	}
	return fmt.Sprintf("[AgentBox: your turn ended while you wait on work that is still running.%s"+
		" Nothing wakes you when it ends: %s doesn't report background work to AgentBox, so you would sit idle."+
		" Wait for it now, in this turn, with one blocking command (for CI, `gh run watch <id> --exit-status`; for your own process, `wait` or a loop inside one shell command), then finish with your result."+
		" If nothing is running after all, say what you have and stop.]",
		running, toolName(a.AI))
}

func toolName(ai string) string {
	if n := chat.ToolNames[ai]; n != "" {
		return n
	}
	return ai
}

// askingNotice tells the lead an agent ended its turn on a question of its
// own, and waits for the answer.
func askingNotice(a state.Agent, q string) string {
	return fmt.Sprintf("[AgentBox: %s (%s) ended its turn on a question and is waiting for an answer; it isn't finished. It asked:\n\n%s\n\n"+
		"Answer it with tell_agent(%q) if the task already says; if only the user can, ask them.]",
		a.Name, titleOrNone(a.Title), quote(q), a.Name)
}

// failedNotice tells the lead an agent's turn stopped on an error, in the
// middle of its work.
func failedNotice(a state.Agent, why string) string {
	return fmt.Sprintf("[AgentBox: %s (%s) stopped in the middle of its turn: %s Nothing is running on it now. "+
		"Look with read_agent(%q); tell_agent carries it on from where it stopped.]",
		a.Name, titleOrNone(a.Title), strings.TrimSuffix(why, ".")+".", a.Name)
}

// unwokenNotice tells the lead an agent was told to wait on its work and
// ended its turn waiting all the same: nothing will wake it.
func unwokenNotice(a state.Agent) string {
	return fmt.Sprintf("[AgentBox: %s (%s) ended its turn saying it waits on work still running, twice, and nothing will wake it when that ends. "+
		"Look with read_agent(%q), and tell it what to do.]", a.Name, titleOrNone(a.Title), a.Name)
}

// limitedNotice tells the lead an agent hit its AI tool's usage limit: it is
// paused, resumes by itself, and isn't finished or failed.
func limitedNotice(a state.Agent, until time.Time) string {
	when := "AgentBox won't resume it by itself: tell_agent carries it on"
	if !until.IsZero() {
		when = "AgentBox resumes it at " + until.Format("15:04 MST")
	}
	return fmt.Sprintf("[AgentBox: %s (%s) hit its usage limit and is paused, not finished: %s. "+
		"switch_agent_account moves it to another account with room (list_accounts); otherwise nothing to do.]",
		a.Name, titleOrNone(a.Title), when)
}

// pausedNote is the words that go with a limited outcome.
func pausedNote(until time.Time) string {
	if until.IsZero() {
		return "Paused: usage limit"
	}
	return "Paused: usage limit, resumes at " + until.Format("15:04")
}

// pauseForLimit records that a's turn ended on a usage limit: the outcome, an
// event that keeps it out of "Ready to review" and the finished notices, and
// one short notice to the lead, the first time. A resumed turn that meets the
// limit again moves the time without saying it all again.
func (s *Server) pauseForLimit(ctx context.Context, a state.Agent, end chat.TurnEnd) {
	prev := s.outcomeOf(ctx, a.Project, a.Ref())
	until := end.ResumeAt
	note := pausedNote(until)
	s.setOutcomeUntil(a, agentOutcome{kind: api.AgentOutcomeLimited, note: note, until: until})
	if prev.kind == api.AgentOutcomeLimited && prev.until.Equal(until) {
		return
	}
	ev := api.AgentEvent{
		Project: a.Project, Agent: a.Name, Ref: a.Ref(), Title: a.Title,
		Kind: api.AgentLimited, Outcome: api.AgentOutcomeLimited, Summary: note, At: time.Now(),
	}
	if !until.IsZero() {
		ev.ResumeAt = &until
	}
	s.record(ctx, ev)
	if prev.kind != api.AgentOutcomeLimited {
		s.tellLead(ctx, a.Project, limitedNotice(a, until), false)
	}
}

// resumeLimited is "Resume now": carries a paused agent on at once.
func (s *Server) resumeLimited(a state.Agent) error {
	if err := s.chat.ResumeNow(a); err != nil {
		return err
	}
	s.setOutcome(a, "", "")
	return nil
}

// rearmLimits puts back the wake-ups of agents paused on a usage limit when
// the last daemon stopped (chat.RearmLimit): the timers lived in memory, the
// pause in the events.
func (s *Server) rearmLimits(ctx context.Context) {
	projects, err := s.store.Projects(ctx)
	if err != nil {
		s.logf("rearming usage-limit waits: %v", err)
		return
	}
	for _, p := range projects {
		for ref, o := range s.outcomesFromEvents(ctx, p.Name) {
			if o.kind != api.AgentOutcomeLimited || ctx.Err() != nil {
				continue
			}
			a, err := s.store.Agent(ctx, p.Name, strings.TrimPrefix(ref, p.Name+"/"))
			if err != nil || a.Status != state.AgentReady {
				continue
			}
			if at, ok := s.chat.RearmLimit(a); ok {
				s.setOutcomeUntil(a, agentOutcome{kind: api.AgentOutcomeLimited, note: pausedNote(at), until: at})
				s.logf("%s: waiting out its usage limit, resumes at %s", a.Ref(), at.Format(time.RFC3339))
			}
		}
	}
}
