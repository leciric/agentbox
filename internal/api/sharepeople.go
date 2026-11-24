package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// The people of a shared project (internal/daemon/sharepeople.go): who wrote
// each message, who has what open now, and what each paired AgentBox did on
// the owner's machine.
//
// Everywhere a daemon tells its own app about a person, ID "" is the app's
// own user, whatever machine the person is on: on the owner, a paired
// AgentBox is its peer id ("peer_…"); on a paired AgentBox, the owner is its
// remote id ("r…") and the owner's other peers keep their peer id.

// Person is someone in a shared project.
type Person struct {
	// ID is "" for this app's own user.
	ID string `json:"id"`
	// Name is what their AgentBox is called (Settings → Share).
	Name string `json:"name"`
}

// Here is what someone has open, which their app says with PUT /v1/here
// every HereRenewSeconds while it is open, and at once when it changes.
type Here struct {
	// Project is the project on screen, "" when none is.
	Project string `json:"project"`
	// Session is the session of the project's chat on screen.
	Session string `json:"session,omitempty"`
	// Agent is the agent on screen, by name (not its ref).
	Agent string `json:"agent,omitempty"`
	// Tab is what of the agent is on screen: chat, desktop, terminal, or
	// another of its tabs (media, settings…).
	Tab string `json:"tab,omitempty"`
	// Typing says they are writing a message in what is on screen.
	Typing bool `json:"typing,omitempty"`
}

// HereRequest is PUT /v1/here.
type HereRequest struct {
	// Client is the window it comes from, the same each time: one person
	// may have the app open twice.
	Client string `json:"client"`
	Here
}

// HereRenewSeconds is how often an app renews what it has open; what isn't
// renewed for HereTTLSeconds is taken as closed. The TTL is generous: a
// window in the background may have its timers slowed to one a minute.
const (
	HereRenewSeconds = 15
	HereTTLSeconds   = 90
)

// PersonHere is someone and what they have open.
type PersonHere struct {
	Person
	Here
	// Since is when they opened it.
	Since time.Time `json:"since"`
}

// ProjectHere is EventHere's data and an entry of GET /v1/here: the people
// in a project now, and what each has open. People is empty when nobody is.
type ProjectHere struct {
	Project string       `json:"project"`
	People  []PersonHere `json:"people"`
}

// EventHere is published whenever who is in a project, or what they have
// open, changes.
const EventHere = "here"

// ShareActivity is one thing a paired AgentBox did on this machine.
type ShareActivity struct {
	ID   int64     `json:"id"`
	At   time.Time `json:"at"`
	Kind string    `json:"kind"` // one of the ShareDid… kinds
	// Project, Agent and Detail are what it was done to: the agent's name,
	// a session, a pull request's number.
	Project string `json:"project,omitempty"`
	Agent   string `json:"agent,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// What a paired AgentBox did (ShareActivity.Kind).
const (
	ShareDidCreateAgent  = "create-agent"
	ShareDidStartAgent   = "start-agent"
	ShareDidStopAgent    = "stop-agent"
	ShareDidRemoveAgent  = "remove-agent"
	ShareDidMessage      = "message" // in the project's chat; Detail is the session
	ShareDidPrompt       = "prompt"  // to an agent
	ShareDidAnswer       = "answer"  // an agent's question
	ShareDidMerge        = "merge"   // an agent, or a pull request (Detail its number)
	ShareDidReview       = "review"  // an agent's work: reviewed, or changes asked for
	ShareDidOpenDesktop  = "open-desktop"
	ShareDidOpenTerminal = "open-terminal"
)

// EventShareActivity is published, with the peer's id as data's Peer, when
// a paired AgentBox did something here.
const EventShareActivity = "share-activity"

// ShareActivityEvent is EventShareActivity's data.
type ShareActivityEvent struct {
	Peer string `json:"peer"`
}

// RenewHere says what this app has open.
func (c *Client) RenewHere(ctx context.Context, req HereRequest) error {
	return c.do(ctx, http.MethodPut, "/v1/here", req, nil)
}

// LeaveHere says the app's window closed.
func (c *Client) LeaveHere(ctx context.Context, client string) error {
	return c.do(ctx, http.MethodDelete, "/v1/here/"+url.PathEscape(client), nil, nil)
}

// WhoIsHere lists the people in each project that has someone in it.
func (c *Client) WhoIsHere(ctx context.Context) ([]ProjectHere, error) {
	var out []ProjectHere
	return out, c.do(ctx, http.MethodGet, "/v1/here", nil, &out)
}

// SharePeerActivity lists what a paired AgentBox did here, the newest first.
func (c *Client) SharePeerActivity(ctx context.Context, id string, limit int) ([]ShareActivity, error) {
	var out []ShareActivity
	path := "/v1/share/peers/" + url.PathEscape(id) + "/activity"
	if limit > 0 {
		path += "?limit=" + strconv.Itoa(limit)
	}
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}
