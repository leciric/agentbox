package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// What an agent reports about itself — it was created, it finished, it asked
// its project's chat something, that question was answered — exists twice, on
// purpose.
//
// The lead is told it in prose, because the lead is a model reading a
// conversation and prose is what it can act on. The app is given the same
// thing as an api.AgentEvent: fields it can lay out as a thread, with the
// changes, the pull request and the question as themselves rather than as a
// sentence it would have to parse back. The prose is built *from* the event
// (see finishNotice), so the two can't come apart.
//
// Events are kept per project, so the threads survive a reload, and pushed on
// the event stream so an open app doesn't have to poll.

// createdEvent records an agent coming into existence, with the task it was
// given as what there is to read — which for an agent that has done nothing
// yet is the only thing there is.
func createdEvent(a state.Agent, task string, at time.Time) api.AgentEvent {
	// Not summaryOf: that drops a message too short to be a summary of work,
	// and "Bump the image version" is a whole task.
	summary, cut := shorten(strings.TrimSpace(task), maxSummary)
	return api.AgentEvent{
		Project: a.Project, Agent: a.Name, Ref: a.Ref(), Title: a.Title,
		Kind: api.AgentCreated, Summary: summary, Cut: cut, At: at,
	}
}

// finishedEvent records an agent finishing what it was given: what it had
// changed at that moment, the pull request it had opened, and what it said
// last, which its brief asks it to make a summary of the work.
func finishedEvent(a state.Agent, changes api.AgentChanges, pr *api.PullRequest, lastMessage string, at time.Time) api.AgentEvent {
	summary, cut := summaryOf(lastMessage)
	return api.AgentEvent{
		Project: a.Project, Agent: a.Name, Ref: a.Ref(), Title: a.Title,
		Kind: api.AgentFinished, Summary: summary, Cut: cut,
		Changes: &changes, PR: pr, At: at,
	}
}

// questionEvent records a question being asked, or answered. It carries the
// question's ID rather than its text: a question changes after it is asked —
// it is escalated, then answered — and the thread shows it as it is now,
// which is also what lets it offer to answer one still waiting.
func questionEvent(q state.Question, title, kind string, at time.Time) api.AgentEvent {
	return api.AgentEvent{
		Project: q.Project, Agent: q.Agent, Ref: q.Ref(), Title: title,
		Kind: kind, Question: q.ID, At: at,
	}
}

// finishNotice is the prose a lead is told when one of its agents finishes,
// written from the event so the two say the same thing.
func finishNotice(ev api.AgentEvent) string {
	notice := fmt.Sprintf("%s (%s) finished.", ev.Agent, titleOrNone(ev.Title))
	if c := ev.Changes; c != nil && c.Files > 0 {
		notice += fmt.Sprintf(" It has changed %d file(s), +%d/-%d", c.Files, c.Insertions, c.Deletions)
		if c.Dirty {
			notice += ", not yet committed"
		}
		notice += "."
	} else {
		notice += " It has changed nothing."
	}
	if ev.PR != nil {
		notice += fmt.Sprintf(" PR #%d, %s: %s.", ev.PR.Number, ev.PR.State, ev.PR.URL)
	}
	if ev.Summary == "" {
		// Nothing to quote, so reading the conversation is the first step again.
		return notice + fmt.Sprintf("\n\nIt left no summary. Read what it did with read_agent(%q).", ev.Agent)
	}
	head := fmt.Sprintf("Its summary (read_agent(%q) has more)", ev.Agent)
	if ev.Cut {
		head = fmt.Sprintf("The start of a longer summary (read_agent(%q) has the rest)", ev.Agent)
	}
	// The closing line is what keeps the lead from answering a routine
	// finish with a proposal and a menu: its brief says what is routine.
	return notice + fmt.Sprintf("\n\n%s:\n\n%s\n\nDo what follows from it, and tell the user in a line — or nothing, if nothing does.",
		head, quote(ev.Summary))
}

// record stores an event and pushes it to whoever is watching. It never fails
// the thing it is recording: an agent that finished has finished whether or
// not the app hears about it.
func (s *Server) record(ctx context.Context, ev api.AgentEvent) api.AgentEvent {
	ev.ID = newID()
	data, err := json.Marshal(ev)
	if err != nil {
		s.logf("recording what %s did: %v", ev.Ref, err)
		return ev
	}
	row := state.AgentEvent{ID: ev.ID, Project: ev.Project, Agent: ev.Agent, CreatedAt: ev.At, Data: data}
	if err := s.store.AddAgentEvent(ctx, row); err != nil {
		s.logf("recording what %s did: %v", ev.Ref, err)
	}
	s.events.publish(api.EventAgentEvent, ev)
	return ev
}

// projectAgentEvents is the app's view: everything this project's agents have
// reported, newest first.
func (s *Server) projectAgentEvents(w http.ResponseWriter, r *http.Request) error {
	project := r.PathValue("project")
	if _, err := s.store.Project(r.Context(), project); err != nil {
		return err
	}
	rows, err := s.store.AgentEvents(r.Context(), project)
	if err != nil {
		return err
	}
	out := make([]api.AgentEvent, 0, len(rows))
	for _, row := range rows {
		var ev api.AgentEvent
		if json.Unmarshal(row.Data, &ev) != nil {
			continue // written by a newer AgentBox, or corrupted: skip it
		}
		out = append(out, ev)
	}
	return writeJSON(w, http.StatusOK, out)
}

func titleOrNone(title string) string {
	if title == "" {
		return "no title"
	}
	return title
}
