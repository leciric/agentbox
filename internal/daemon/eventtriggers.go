package daemon

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"agentbox/internal/github"
	"agentbox/internal/schedule"
	"agentbox/internal/state"
)

// Event triggers are schedules of kind "event": instead of a time, they run
// each time something happens on the project's GitHub repository — CI goes
// red on the default branch, a review is asked of the user, an issue gets a
// label, a pull request is opened from outside AgentBox — and their run does
// what any schedule's does (deliverSchedule), with what happened added to
// the task.
//
// There is no webhook: GitHub can't reach the user's machine. The pull
// request watch (prwatch.go) notices instead, reading what the triggers need
// in the same single GraphQL request it already makes per look
// (github.WatchRepository), and looking at least every prWatchCalm while a
// project has a trigger, instead of backing off to prWatchIdle.
//
// One event fires once. Each is named by a key (ci:<sha>, review:<number>,
// issue:<number>, pr:<number>), and a run is recorded together with its key
// (state.ClaimScheduleEvent), so neither a second look nor a restart fires it
// again. A trigger's first look — when it is made, changed or resumed —
// takes everything already there as seen, so making "when an issue is
// labelled bug" doesn't start an agent for every bug already open. A review
// request the user answered is forgotten, so being asked again is another
// event.

// maxEventFires is how many runs one trigger may start in one look: a burst
// (twenty issues labelled at once) fires over the next looks, a few at a
// time, rather than making twenty agents in a moment.
const maxEventFires = 3

// maxEventBody is how much of an issue's text goes into the task.
const maxEventBody = 2000

// triggerEvent is one thing that happened, as a trigger fires for it.
type triggerEvent struct {
	// Key names it, for firing once.
	Key string
	// Summary is it in a line, for the run's history and the lead; Short a
	// few characters for the title of the agent it makes; URL where to see
	// it.
	Summary string
	Short   string
	URL     string
	// Details is what the task is given about it.
	Details string
}

// eventTriggers are a project's triggers the watch reads for, and the
// options its look needs for them.
func (s *Server) eventTriggers(ctx context.Context, project string) ([]state.Schedule, github.WatchOptions) {
	list, err := s.store.EventSchedules(ctx, project)
	if err != nil {
		s.logf("event triggers in %s: %v", project, err)
		return nil, github.WatchOptions{}
	}
	var opts github.WatchOptions
	for _, sc := range list {
		opts.Events = true
		if sc.Event == schedule.EventIssueLabeled && sc.EventLabel != "" && !slices.Contains(opts.Labels, sc.EventLabel) {
			opts.Labels = append(opts.Labels, sc.EventLabel)
		}
	}
	return list, opts
}

// agentPR says whether a pull request is an agent's: from a branch under the
// project's prefix, or carrying an agent's commits.
func agentPR(p state.Project, heads []agentHead) func(github.WatchedPR) bool {
	prefix := p.BranchPrefix
	if prefix == "" {
		prefix = state.DefaultBranchPrefix
	}
	return func(pr github.WatchedPR) bool {
		if strings.HasPrefix(pr.HeadBranch, prefix) {
			return true
		}
		return slices.ContainsFunc(heads, func(h agentHead) bool {
			return h.owns(pr.HeadSHA) || (h.tip != "" && h.tip == pr.HeadSHA) || h.branch == pr.HeadBranch
		})
	}
}

// matchEvents is every event a trigger would fire for in what a look read,
// and, for review requests, the pull requests read open without one, whose
// request (if the trigger saw one) was answered or withdrawn.
func matchEvents(sc state.Schedule, repo github.Repo, ev *github.RepoEvents, open []github.WatchedPR, isAgents func(github.WatchedPR) bool) (out []triggerEvent, answered []string) {
	if ev == nil {
		return nil, nil
	}
	switch sc.Event {
	case schedule.EventCIFailed:
		h := ev.Head
		if h.SHA == "" || h.Checks != "failing" {
			return nil, nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "CI failed on %s of %s: commit %s (%s)", ev.DefaultBranch, repo, h.SHA, h.URL)
		if h.Headline != "" {
			fmt.Fprintf(&b, ", %q", h.Headline)
		}
		b.WriteString(".")
		if len(h.Failing) > 0 {
			b.WriteString("\nFailing checks:")
			for _, c := range h.Failing {
				fmt.Fprintf(&b, "\n- %s", c.Name)
				if c.URL != "" {
					fmt.Fprintf(&b, " (%s)", c.URL)
				}
			}
		}
		fmt.Fprintf(&b, "\nRead a failing job's log with `gh run view <run-id> --log-failed`.")
		summary := fmt.Sprintf("CI failed on %s at %s", ev.DefaultBranch, shortSHA(h.SHA))
		if names := failingNames(github.WatchedPR{Failing: h.Failing}); names != "" {
			summary += " (" + names + ")"
		}
		return []triggerEvent{{Key: "ci:" + h.SHA, Summary: summary, Short: shortSHA(h.SHA), URL: h.URL, Details: b.String()}}, nil
	case schedule.EventReviewRequested:
		if ev.Viewer == "" {
			return nil, nil
		}
		for _, pr := range open {
			key := "review:" + strconv.Itoa(pr.Number)
			if !slices.ContainsFunc(pr.ReviewRequested, func(l string) bool { return strings.EqualFold(l, ev.Viewer) }) {
				answered = append(answered, key)
				continue
			}
			out = append(out, triggerEvent{
				Key: key, Summary: fmt.Sprintf("review requested from %s on PR #%d %q", ev.Viewer, pr.Number, pr.Title),
				Short: "#" + strconv.Itoa(pr.Number), URL: pr.URL,
				Details: fmt.Sprintf("A review was requested from %s (the user) on pull request #%d of %s, %q, by %s: %s\nBranch %s into %s, head %s. Read it with `gh pr view %d` and `gh pr diff %d`.",
					ev.Viewer, pr.Number, repo, pr.Title, orSomeone(pr.Author), pr.URL, pr.HeadBranch, pr.BaseBranch, shortSHA(pr.HeadSHA), pr.Number, pr.Number),
			})
		}
		return out, answered
	case schedule.EventIssueLabeled:
		for _, is := range ev.Issues {
			if !slices.ContainsFunc(is.Labels, func(l string) bool { return strings.EqualFold(l, sc.EventLabel) }) {
				continue
			}
			body := strings.TrimSpace(is.Body)
			if r := []rune(body); len(r) > maxEventBody {
				body = string(r[:maxEventBody]) + "…"
			}
			if body == "" {
				body = "(no description)"
			}
			out = append(out, triggerEvent{
				Key: "issue:" + strconv.Itoa(is.Number), Summary: fmt.Sprintf("issue #%d %q labelled %s", is.Number, is.Title, sc.EventLabel),
				Short: "#" + strconv.Itoa(is.Number), URL: is.URL,
				Details: fmt.Sprintf("Issue #%d of %s, %q, opened by %s, is labelled %q: %s\nLabels: %s. Read it with `gh issue view %d --comments`.\n\nIts description:\n\n%s",
					is.Number, repo, is.Title, orSomeone(is.Author), sc.EventLabel, is.URL, strings.Join(is.Labels, ", "), is.Number, body),
			})
		}
		return out, nil
	case schedule.EventPROpened:
		for _, pr := range open {
			if isAgents(pr) {
				continue
			}
			draft := ""
			if pr.Draft {
				draft = " (a draft)"
			}
			out = append(out, triggerEvent{
				Key: "pr:" + strconv.Itoa(pr.Number), Summary: fmt.Sprintf("PR #%d %q opened by %s", pr.Number, pr.Title, orSomeone(pr.Author)),
				Short: "#" + strconv.Itoa(pr.Number), URL: pr.URL,
				Details: fmt.Sprintf("Pull request #%d of %s, %q, was opened by %s%s, not by an agent: %s\nBranch %s into %s, head %s. Read it with `gh pr view %d` and `gh pr diff %d`.",
					pr.Number, repo, pr.Title, orSomeone(pr.Author), draft, pr.URL, pr.HeadBranch, pr.BaseBranch, shortSHA(pr.HeadSHA), pr.Number, pr.Number),
			})
		}
		return out, nil
	}
	return nil, nil
}

func orSomeone(login string) string {
	if login == "" {
		return "a deleted account"
	}
	return "@" + login
}

// fireEventTriggers fires a project's triggers for what a look read.
func (s *Server) fireEventTriggers(ctx context.Context, p state.Project, repo github.Repo, triggers []state.Schedule, watch github.Watch, heads []agentHead) {
	isAgents := agentPR(p, heads)
	now := s.prWatch.now()
	for _, sc := range triggers {
		events, answered := matchEvents(sc, repo, watch.Events, watch.Open, isAgents)
		looked, err := s.store.ScheduleEventsSeen(ctx, sc.ID)
		if err != nil {
			s.logf("event trigger %s of %s: %v", sc.ID, p.Name, err)
			continue
		}
		if !looked {
			keys := make([]string, len(events))
			for i, e := range events {
				keys[i] = e.Key
			}
			if err := s.store.MarkScheduleEventsSeen(ctx, p.Name, sc.ID, keys, now); err != nil {
				s.logf("event trigger %s of %s: %v", sc.ID, p.Name, err)
			}
			continue
		}
		if len(answered) > 0 {
			if err := s.store.ForgetScheduleEvents(ctx, sc.ID, answered); err != nil {
				s.logf("event trigger %s of %s: %v", sc.ID, p.Name, err)
			}
		}
		fired := 0
		for _, e := range events {
			if fired >= maxEventFires {
				break
			}
			run := state.ScheduleRun{
				ID: newID(), Schedule: sc.ID, Project: sc.Project, Trigger: state.ScheduleTriggerEvent,
				RanAt: now, Event: e.Summary, EventURL: e.URL,
			}
			err := s.store.ClaimScheduleEvent(ctx, run, e.Key)
			if errors.Is(err, state.ErrEventSeen) {
				continue
			}
			if err != nil {
				if !errors.Is(err, state.ErrScheduleMoved) {
					s.logf("event trigger %s of %s: %v", sc.ID, p.Name, err)
				}
				break
			}
			fired++
			s.logf("event trigger %q of %s fired: %s", sc.Title, p.Name, e.Summary)
			s.deliverSchedule(ctx, withEvent(sc, e), run)
		}
	}
}

// withEvent is the schedule as one run of it for an event goes out: the
// event's details after the task, and the event in the title of the agent it
// makes.
func withEvent(sc state.Schedule, e triggerEvent) state.Schedule {
	sc.Task = sc.Task + "\n\nWhat happened (AgentBox's event trigger, " + schedule.DescribeEvent(sc.Event, sc.EventLabel) + "):\n\n" + e.Details
	if e.Short != "" {
		sc.Title = sc.Title + " · " + e.Short
	}
	return sc
}

// eventTriggerEvery is the longest a project with an event trigger goes
// between two looks.
const eventTriggerEvery = prWatchCalm
