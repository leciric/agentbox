package api

import (
	"context"
	"net/http"
	"time"
)

// The timeline API: what an agent did, as steps over time, read out of its
// chat and its media (internal/timeline). The app draws it as a timeline
// beside the chat and in the review, each step with the screenshots,
// recordings and pages it made inline, and scrubbing it plays the
// recordings that cover that moment.

// Timeline is one agent's steps, oldest first, from its first message to its
// last item.
type Timeline struct {
	Steps []TimelineStep `json:"steps"`
	Start time.Time      `json:"start"`
	End   time.Time      `json:"end"`
	// Working is true while a turn is still running: the last step may grow.
	Working bool `json:"working,omitempty"`
}

// TimelineStep is a run of the chat's items that did one kind of thing:
// the task it was given, reading, a plan, edits, tests, other commands, a
// check on the desktop, a page, the pull request.
type TimelineStep struct {
	// Kind is one of the TimelineKind* constants.
	Kind  string    `json:"kind"`
	Title string    `json:"title"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	// Status is passed or failed for tests, failed for anything else that
	// failed, running while it still is, and empty otherwise.
	Status string `json:"status,omitempty"`
	// Calls is how many tool calls the step gathers.
	Calls int `json:"calls,omitempty"`
	// Lines are what the step shows as text: a command and the end of its
	// output, a diff's changed lines, the plan's entries, the task's words.
	Lines []string `json:"lines,omitempty"`
	// Paths are the files it read or changed, Added and Removed the lines
	// its edits added and removed.
	Paths   []string `json:"paths,omitempty"`
	Added   int      `json:"added,omitempty"`
	Removed int      `json:"removed,omitempty"`
	// Media are the screenshots and recordings made while it ran.
	Media []MediaItem `json:"media,omitempty"`
	// Pages are the Hatch pages it published or updated.
	Pages []TimelinePage `json:"pages,omitempty"`
	// Item is the chat item it starts at, for the app to open the chat there.
	Item string `json:"item"`
}

// TimelinePage is a page a step published on Hatch: its title, and the chat
// item of the call, which the project's artifacts (Artifact.Item) know it by.
type TimelinePage struct {
	ID    string `json:"id,omitempty"`
	Title string `json:"title"`
	Item  string `json:"item"`
}

// The kinds of TimelineStep.
const (
	TimelineKindTask    = "task"    // a message it was sent
	TimelineKindRead    = "read"    // reading and searching
	TimelineKindPlan    = "plan"    // a plan it set out
	TimelineKindEdit    = "edit"    // changing files
	TimelineKindTest    = "test"    // running tests
	TimelineKindRun     = "run"     // other commands
	TimelineKindDesktop = "desktop" // checking its work on the desktop, the browser or the phone
	TimelineKindPage    = "page"    // a page published on Hatch
	TimelineKindPR      = "pr"      // opening or updating its pull request
)

// AgentTimeline is what the agent ref did, as steps (a live agent or a past one).
func (c *Client) AgentTimeline(ctx context.Context, ref string) (Timeline, error) {
	path, err := AgentPath(ref)
	if err != nil {
		return Timeline{}, err
	}
	var out Timeline
	return out, c.do(ctx, http.MethodGet, path+"/timeline", nil, &out)
}
