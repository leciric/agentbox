package api

import "time"

// The kinds of thing GET /v1/search finds, each a group of its answer, in the
// order the app shows them.
const (
	SearchProjects   = "projects"
	SearchAgents     = "agents"
	SearchChats      = "chats" // messages in a project's or an agent's chat
	SearchMemories   = "memories"
	SearchEvents     = "events"
	SearchReports    = "reports"
	SearchMedia      = "media"
	SearchSkills     = "skills"
	SearchConnectors = "connectors"
	SearchNotes      = "notes" // lines of a project's notes
	SearchPulls      = "pulls" // pull requests, as AgentBox last read them from GitHub
)

// SearchResults is one search across everything AgentBox keeps, every
// project's at once: only the groups that found something, each at most the
// limit asked for.
type SearchResults struct {
	Query  string        `json:"query"`
	Groups []SearchGroup `json:"groups"`
}

// SearchGroup is what one kind of thing found, best first.
type SearchGroup struct {
	Kind string      `json:"kind"`
	Hits []SearchHit `json:"hits"`
	// More is whether there were more than the limit.
	More bool `json:"more,omitempty"`
}

// SearchHit is one thing found, with enough to show it in a list and to open
// it where it lives in the app.
type SearchHit struct {
	// ID is what it is among its kind: a project's slug, an agent's ref, the id
	// of a memory, an event, a report, a media item or a chat item, a skill's
	// or a connector's name, a pull request's number, a note's line number.
	ID     string `json:"id"`
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"` // the words around the match, or what else it is
	// Tag is a word about it, shown beside it: an agent's branch, a memory's
	// kind, a pull request's state, a media item's kind, a chat message's role.
	Tag string `json:"tag,omitempty"`
	// Project is the project it belongs to, by slug; empty for something
	// AgentBox-wide (a skill, an AgentBox-wide connector).
	Project string `json:"project,omitempty"`
	// Agent is the agent of Project it belongs to, by name; empty for the
	// project itself (its chat, its connectors).
	Agent string     `json:"agent,omitempty"`
	At    *time.Time `json:"at,omitempty"`
	URL   string     `json:"url,omitempty"` // a pull request's, on GitHub

	// The thing itself, for the kinds whose place in the app lists only some
	// of them (the newest events, a filtered list), so the app can show it
	// there even when the list wouldn't.
	Memory *Memory      `json:"memory,omitempty"`
	Event  *MemoryEvent `json:"event,omitempty"`
	Report *AgentReport `json:"report,omitempty"`
	Media  *MediaItem   `json:"media,omitempty"`
}
