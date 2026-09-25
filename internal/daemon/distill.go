package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"agentbox/internal/chat"
	"agentbox/internal/memory"
	"agentbox/internal/state"
)

// Distillation: raw events into memories, with a model (D76, D78).
//
// The mechanical pass (internal/memory/consolidation.go) tidies what is
// already remembered and costs nothing. This is the half that turns history
// into judgement, and it needs something that can read.
//
// There is no LLM session lying around in AgentBox. Compaction gets away with
// its hidden prompt because a lead turn is about to run and the session is
// right there; a consolidation triggered by a count of events has no such
// luck. D76 answered that by reusing the chat's own session through
// chat.Manager.Ask, which is Compact's hidden prompt with the rollover taken
// off. It worked, and it billed every pass to the model the user chats on.
//
// D78 gives the pass a model of its own: chat.Manager.AskAside starts a
// second session, on the project's consolidation model, through the same AI
// tool and the same login, and throws it away afterwards. The chat's session
// is what it falls back to, which is why Ask is still here — a cheap model an
// account won't run is a reason to consolidate on something else, never a
// reason not to consolidate.
//
// What differs from compaction is *when*. Compaction runs before a lead turn,
// blocking, because the turn must not land in the session being replaced.
// This runs after one, off the same Publish hook that already captures a
// lead_turn event, because a consolidation has no reason to make the user
// wait and every reason to happen while nobody is using the session. The cost
// of running after rather than before is a race with the user's next message,
// which the chat already knows how to lose safely.

// distillTimeout bounds the hidden prompt. The model is reading a few hundred
// events; nobody is waiting for it, and past this the window is simply read
// again next time. An aside session spends part of it starting an adapter,
// which is the price of not spending the chat's model and its context.
const distillTimeout = 5 * time.Minute

// errDistilling says this project is already being distilled. Like
// chat.ErrBusy it is not a failure and records no pass: the window is still
// there, and the pass that is running is reading it.
var errDistilling = errors.New("a distillation of this project is already running")

// distillIfDue asks a project's chat to turn the events it has gathered since
// its watermark into memories, when there are enough of them to be worth a
// prompt. It answers whether it ran.
//
// Everything about it is skippable and nothing about it is fatal: a project
// with consolidation switched off, a chat with no session, a chat in the
// middle of a turn, an answer that isn't JSON — each is a log line and a
// window read again later. The watermark only moves on a pass that worked.
func (s *Server) distillIfDue(ctx context.Context, a state.Agent) bool {
	if a.Role != state.RoleLead {
		return false
	}
	p, err := s.store.Project(ctx, a.Project)
	if err != nil || p.Consolidation == state.ConsolidationOff {
		return false
	}
	store := s.memory()
	watermark, err := store.Watermark(ctx, a.Project)
	if err != nil {
		s.logf("consolidating %s: reading the watermark: %v", a.Project, err)
		return false
	}
	pending, err := store.PendingEvents(ctx, a.Project, watermark)
	if err != nil || pending < p.Consolidation {
		return false
	}
	s.logf("consolidating %s: %d events since the last pass, over %d", a.Project, pending, p.Consolidation)
	if err := s.distill(ctx, p, a, watermark); err != nil {
		if errors.Is(err, chat.ErrBusy) || errors.Is(err, errDistilling) {
			return false // something else has the session; ask again next turn
		}
		s.logf("consolidating %s: %v", a.Project, err)
		return false
	}
	return true
}

// distill runs one pass: read the window, ask the session, apply the answer,
// record what it did. A pass that fails is recorded too, with its reason —
// the log is what makes "consolidation isn't working" something the user can
// see rather than guess at — and a recorded failure carries no watermark, so
// the next pass starts where this one did.
func (s *Server) distill(ctx context.Context, p state.Project, a state.Agent, watermark time.Time) error {
	// One at a time per project. Ask used to enforce this by accident — a
	// chat can only be asked one thing at once — and an aside session, which
	// starts a process of its own, would happily run two passes over the same
	// window and write everything twice.
	done, ok := s.beginDistill(a.Project)
	if !ok {
		return errDistilling
	}
	defer done()

	// It outlives whatever triggered it: a consolidation half-applied
	// because a request went away would leave memories with no watermark.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), distillTimeout)
	defer cancel()

	store := s.memory()
	events, err := store.EventsSince(ctx, a.Project, watermark, memory.MaxDistillEvents)
	if err != nil {
		return err
	}
	if len(events) == 0 {
		return nil
	}
	known, err := store.Memories(ctx, a.Project, nil)
	if err != nil {
		return err
	}
	// Handing the live memories to a model is a reference: it is what stops
	// the memories a project actually uses from quietly decaying.
	if err := store.MarkReferenced(ctx, a.Project, memory.MemoryIDs(known)...); err != nil {
		s.logf("consolidating %s: marking what it read: %v", a.Project, err)
	}

	started := time.Now()
	ask := distillAsk(events, known)
	pass := memory.Pass{
		Project: a.Project, Kind: memory.PassDistill, At: started,
		EventsRead: len(events), InputBytes: len(ask),
	}
	answer, model, askErr := s.askDistiller(ctx, p, a, ask)
	pass.OutputBytes, pass.Model = len(answer), model
	if askErr != nil {
		if errors.Is(askErr, chat.ErrBusy) {
			return askErr // not a pass at all: nothing was asked
		}
		return s.failedPass(ctx, pass, started, askErr)
	}
	result, err := parseDistillation(answer)
	if err != nil {
		return s.failedPass(ctx, pass, started, err)
	}
	if err := s.applyDistillation(ctx, a, &pass, result, answer); err != nil {
		return s.failedPass(ctx, pass, started, err)
	}
	// The watermark is the last event the model was actually shown. Events
	// that arrived while it thought are left for the next pass rather than
	// skipped, which is the whole point of keeping one.
	last := events[len(events)-1]
	pass.ThroughEventID, pass.ThroughAt = last.ID, last.At
	pass.Duration = time.Since(started)
	if _, err := s.memory().RecordPass(ctx, pass); err != nil {
		return err
	}
	s.logf("consolidating %s: %d events became %d memories (%d replaced, %d closed)%s",
		a.Project, pass.EventsRead, pass.MemoriesWritten, pass.MemoriesSuperseded, pass.MemoriesResolved,
		ranOnWords(pass.Model))
	return nil
}

// ranOnWords names the model a pass ran on, for a log line. A pass on the
// chat's own session says nothing: there is no second model to name.
func ranOnWords(model string) string {
	if model == "" {
		return ""
	}
	return ", on " + model
}

// askDistiller puts the prompt to the model the project chose, and says which
// model answered it.
//
// The project's own model goes first, in a session of its own (D78): a
// distillation is summarising, and spending the model the user chats on — and
// the context of the conversation they are having — on it is what this is for.
// Anything that goes wrong with that falls back to the chat's own session,
// which is what D76 shipped: an account that won't run the cheap model, an
// adapter that won't start or a tool that refuses the setting are all reasons
// to consolidate differently, and none of them is a reason not to consolidate.
//
// What it answers as the model is what really ran, never what was asked for,
// because that is the only version of it worth recording: a pass that says
// "haiku" while the expensive model wrote it would make the pass log a lie.
func (s *Server) askDistiller(ctx context.Context, p state.Project, a state.Agent, ask string) (answer, model string, err error) {
	if want := p.ConsolidationModelFor(a.AI); want != "" {
		answer, ranOn, err := s.askAside(ctx, a, want, ask)
		if err == nil {
			if ranOn == "" {
				ranOn = want
			}
			return answer, ranOn, nil
		}
		s.logf("consolidating %s: %v — falling back to the chat's own session", a.Project, err)
	}
	answer, err = s.askLead(ctx, a, ask)
	return answer, s.chat.Model(a), err
}

// beginDistill claims a project's distillation, and answers a release and
// whether the claim was made. A project already being distilled answers
// false, and its caller does nothing rather than queueing: there is one
// window, and the pass that holds it is already reading it.
func (s *Server) beginDistill(project string) (func(), bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.distilling[project] {
		return nil, false
	}
	s.distilling[project] = true
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.distilling, project)
	}, true
}

// failedPass records that a pass didn't work and why, and hands the reason
// back. It carries no watermark, so nothing is skipped by having tried.
func (s *Server) failedPass(ctx context.Context, pass memory.Pass, started time.Time, cause error) error {
	pass.Error, pass.Duration = cause.Error(), time.Since(started)
	if _, err := s.memory().RecordPass(ctx, pass); err != nil {
		s.logf("consolidating %s: recording the failed pass: %v", pass.Project, err)
	}
	return cause
}

// applyDistillation writes what the model said, through AddMemory and
// ResolveMemory and nothing lower: whatever a consolidation may do, it may
// only do it through the same doors a person or an agent goes through, so
// there is one place that decides what a memory is allowed to be.
//
// One event holds the raw answer and every memory names it, exactly as
// compaction does, so a pass can be read back as the model wrote it —
// including the parts that became nothing.
func (s *Server) applyDistillation(ctx context.Context, a state.Agent, pass *memory.Pass, d distillation, answer string) error {
	store := s.memory()
	event, err := store.AppendEvent(ctx, memory.Event{
		Project: a.Project, Type: memory.EventMemoryConsolidated, Payload: consolidationPayload(answer),
	})
	if err != nil {
		return err
	}
	write := func(item distilled, replacing string) {
		title, content := item.item().split()
		if title == "" {
			return
		}
		m := memory.Memory{
			Project: a.Project, Kind: item.kind(), Title: title, Content: content,
			Importance: item.importance(), SupersedesID: replacing, SourceEventID: event.ID,
		}
		_, err := store.AddMemory(ctx, m)
		if err != nil && replacing != "" {
			// An id that names no memory of this project. A model inventing
			// one is the failure mode a cheaper model has more of (D78), and
			// dropping the memory over it would throw away the judgement
			// because the bookkeeping was wrong — so it is written as a new
			// memory instead, exactly as a replacement that names nothing is.
			// Nothing is superseded: the claim that something was is the part
			// that couldn't be true.
			s.logf("consolidating %s: %q says it replaces %s, which isn't a memory of this project: keeping it as a new one",
				a.Project, title, replacing)
			m.SupersedesID, replacing = "", ""
			_, err = store.AddMemory(ctx, m)
		}
		if err != nil {
			s.logf("consolidating %s: keeping %q: %v", a.Project, title, err)
			return
		}
		if replacing != "" {
			pass.MemoriesSuperseded++
			return
		}
		pass.MemoriesWritten++
	}
	for _, item := range d.Memories {
		write(item, "")
	}
	for _, item := range d.Supersede {
		if strings.TrimSpace(item.Replaces) == "" {
			// A replacement that names nothing is a new memory, not a lost
			// one: the model had something to say either way.
			write(item, "")
			continue
		}
		write(item, strings.TrimSpace(item.Replaces))
	}
	for _, it := range d.Resolve {
		id := strings.TrimSpace(it.ID)
		if id == "" {
			continue
		}
		if _, err := store.ResolveMemory(ctx, a.Project, id, it.Why); err != nil {
			s.logf("consolidating %s: closing %s: %v", a.Project, id, err)
			continue
		}
		pass.MemoriesResolved++
	}
	return nil
}

// distillation is what the hidden prompt is asked for: what to remember, what
// to replace, and what to close.
type distillation struct {
	Memories  []distilled  `json:"memories"`
	Supersede []distilled  `json:"supersede"`
	Resolve   []resolution `json:"resolve"`
}

// distilled is one memory the model wants written down. Replaces is set only
// in the supersede list.
type distilled struct {
	Kind       string `json:"kind"`
	Title      string `json:"title"`
	Detail     string `json:"detail"`
	Importance int    `json:"importance"`
	Replaces   string `json:"replaces"`
}

// resolution is one issue the model says is over, with no replacement to name.
type resolution struct {
	ID  string `json:"id"`
	Why string `json:"why"`
}

// item is the memory as compaction's lenient title-and-body reader sees it,
// so a model that answers with a sentence where an object was asked for is
// read the same way in both places.
func (d distilled) item() consolidationItem {
	return consolidationItem{Title: d.Title, Detail: d.Detail}
}

// kind is the memory kind the model named, or "" to let AddMemory default it.
// A kind nobody recognises is not refused — the fact is still worth keeping,
// and "project" is what an unlabelled fact is.
func (d distilled) kind() string {
	kind := strings.ToLower(strings.TrimSpace(d.Kind))
	for _, k := range memory.Kinds {
		if kind == k {
			return k
		}
	}
	return ""
}

// importance is what the model asked for, clamped rather than refused: a
// memory lost to a 7 would be a memory lost to a typo.
func (d distilled) importance() int {
	switch {
	case d.Importance <= 0:
		return 0 // AddMemory's default
	case d.Importance < memory.MinImportance:
		return memory.MinImportance
	case d.Importance > memory.MaxImportance:
		return memory.MaxImportance
	}
	return d.Importance
}

// parseDistillation reads the answer as leniently as parseConsolidation does,
// and for the same reason: a model asked for JSON answers with JSON in a
// fenced block often enough that refusing those would throw away good work.
// Three empty lists is a valid answer and a successful pass: a stretch of
// history can genuinely establish nothing, and refusing that would have the
// same window re-read for ever. What is refused is an answer that isn't the
// shape at all — that is a session that didn't understand the question, and
// its window is worth showing to the next one.
func parseDistillation(answer string) (distillation, error) {
	body := strings.TrimSpace(answer)
	if start, end := strings.Index(body, "{"), strings.LastIndex(body, "}"); start >= 0 && end > start {
		body = body[start : end+1]
	}
	var out distillation
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		return distillation{}, err
	}
	return out, nil
}

// maxDistillPrompt bounds what is sent, whatever the window holds. An event's
// payload is capped at 64 KiB on its own, and three hundred of those would be
// a prompt no session could take: this is the line past which events stop
// being added, and the ones left over are read by the next pass.
const maxDistillPrompt = 60 << 10

// distillAsk builds the hidden prompt: what the project already knows, then
// the stretch of history nobody has read yet.
//
// The current memories go first and in full, because the job being asked for
// is mostly *not* writing things down — it is noticing that twenty events say
// one thing the project already knows, or that an issue from last month is
// visibly fixed in this one. A model shown only the events writes twenty new
// memories and the store grows exactly as fast as before.
func distillAsk(events []memory.Event, known []memory.Memory) string {
	var b strings.Builder
	b.WriteString(distillPreamble)
	b.WriteString("\n\n## What this project already remembers\n\n")
	if len(known) == 0 {
		b.WriteString("Nothing yet. This is the first pass.\n")
	}
	for _, m := range known {
		fmt.Fprintf(&b, "- [%s] `%s` **%s**", m.Kind, m.ID, m.Title)
		if content := collapseLines(m.Content); content != "" {
			b.WriteString(" — " + truncate(content, 400))
		}
		b.WriteString("\n")
	}
	b.WriteString("\n## What has happened since the last pass\n\n")
	kept := 0
	for _, e := range events {
		line := fmt.Sprintf("- %s %s", e.At.Format("2006-01-02 15:04"), e.Type)
		if e.Agent != "" {
			line += " (" + e.Agent + ")"
		}
		if payload := collapseLines(string(e.Payload)); payload != "" && payload != "{}" {
			line += " " + truncate(payload, 600)
		}
		if b.Len()+len(line) > maxDistillPrompt {
			break
		}
		b.WriteString(line + "\n")
		kept++
	}
	if kept < len(events) {
		fmt.Fprintf(&b, "\n(%d more events didn't fit; they will be read next time.)\n", len(events)-kept)
	}
	b.WriteString("\n" + distillShape)
	return b.String()
}

// collapseLines puts a payload or a body on one line, so one event is one
// line of the prompt however much JSON it carries.
func collapseLines(s string) string { return strings.Join(strings.Fields(s), " ") }

const distillPreamble = `[AgentBox] This is AgentBox asking, not the user: they wrote nothing, they are not waiting on this, and what you answer here never appears in your conversation with them. Answer it and carry on.

AgentBox has been recording what happens on this project — agents created and finished, questions asked and answered, pull requests merged, media produced. That history is kept whatever you say. What it does not have is anyone's judgement about it, and a thousand raw events nobody has read is not knowledge.

Read what the project already remembers, then read what has happened since, and say what the project should now remember. Be ruthless: a stretch like this is usually worth a handful of memories, not one per event. Most of what happened is noise, and a memory nobody will be glad to find in six months is worse than no memory at all — every one of them is context spent on every future agent.`

const distillShape = `Answer with one JSON object and nothing else, in this shape:

{
  "memories": [
    {"kind": "project|decision|discovery|issue|episodic",
     "title": "one line somebody would recognise it by",
     "detail": "the fact in full, and what somebody should do about it",
     "importance": 3}
  ],
  "supersede": [
    {"replaces": "mem_… the id of the memory that is no longer right",
     "kind": "project|decision|discovery|issue|episodic",
     "title": "what is true instead",
     "detail": "the detail",
     "importance": 3}
  ],
  "resolve": [
    {"id": "mem_… the id of an issue that is over", "why": "what closed it, in a line"}
  ]
}

- **memories** is what the project didn't know before. Nothing that is already in the list above, and nothing that will be false next week.
- **supersede** is for a memory that is now wrong or out of date: name its id and say what is true instead. The old one stops coming back from searches and stays readable.
- **resolve** is for an issue that is simply over, with nothing to put in its place — the bug was fixed, the test was deleted, it stopped mattering. Don't invent a replacement memory for it; that is what this list is for.
- **importance** is 1 to 5. 3 is ordinary. 5 is for what nobody should work on this project without knowing, and spending it freely makes it worthless.
- Every list may be empty. Answering with three empty lists is a fine answer if this stretch genuinely established nothing.`
