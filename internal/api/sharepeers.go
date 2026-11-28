package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// Sharing projects with another AgentBox (internal/daemon/share.go). The
// owner pairs another AgentBox, a peer, with a one-time link made under
// Settings → Share; the peer gets a revocable token of its own and reaches
// the projects it was given, through the owner's phone page (its LAN port or
// its Cloudflare Tunnel). Agents a peer makes run on the owner's machine and
// AI accounts, within the peer's slots.

// EventShare is published, with no data, when anything in ShareStatus
// changes.
const EventShare = "share"

// ShareStatus is GET /v1/share.
type ShareStatus struct {
	// Name is what this AgentBox is called on the others: the computer's
	// name unless the user chose one.
	Name string `json:"name"`
	// Slots is how many agents a peer may have running here when it has no
	// limit of its own.
	Slots int `json:"slots"`
	// Ready says this machine can make a pairing link: its phone page is on
	// and has an address (Settings → Share → Phones). Error says why not.
	Ready bool   `json:"ready"`
	Error string `json:"error,omitempty"`
	// Peers are the AgentBoxes paired to reach this machine's projects.
	Peers []SharePeer `json:"peers"`
	// Remotes are the AgentBoxes this one paired with.
	Remotes []ShareRemote `json:"remotes"`
}

// SharePeer is another AgentBox that reaches some of this machine's projects.
type SharePeer struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Paired   time.Time `json:"paired"`
	LastSeen time.Time `json:"lastSeen,omitzero"`
	LastAddr string    `json:"lastAddr,omitempty"`
	// AllProjects shares every project, those added later included;
	// otherwise Projects, by name.
	AllProjects bool     `json:"allProjects"`
	Projects    []string `json:"projects"`
	// Slots is its own limit of agents running here, -1 for ShareStatus.Slots;
	// Running is how many of the agents it made run now.
	Slots   int `json:"slots"`
	Running int `json:"running"`
	// Access is what it may do in the projects it reaches: ShareAccessView,
	// ShareAccessChat or ShareAccessFull.
	Access string `json:"access"`
	// OwnMachine lets it run the agents a project's chat makes for it on its
	// own computer instead of here ("On my PC" in its composer), and Machine
	// says its computer is connected to take them now.
	OwnMachine bool `json:"ownMachine"`
	Machine    bool `json:"machine"`
	// Sync lets it sync with this AgentBox (POST /v1/share/sync on its
	// side): copy the data of the projects it reaches, merge its own into
	// them, and add its own projects here.
	Sync bool `json:"sync"`
	// Usage is what it spent here, on this machine and its AI accounts.
	Usage SharePeerUsage `json:"usage"`
}

// What a paired AgentBox may do in the projects it reaches. Each level may do
// what the one before it may, and more; the daemon enforces it.
const (
	// ShareAccessView reads: the chats, agents, pull requests and files.
	ShareAccessView = "view"
	// ShareAccessChat writes in the chats as well: messages to the project's
	// chat and to agents, and answers to their questions.
	ShareAccessChat = "chat"
	// ShareAccessFull makes agents (within its slots), merges and opens pull
	// requests, and reaches the agents' terminals and desktops.
	ShareAccessFull = "full"
)

// SharePeerUsage is what a peer spent on the owner's machine and accounts.
type SharePeerUsage struct {
	// Tokens are its agents' and the chat turns its messages started, every
	// kind (input, output, cache) together; CostUSD what the tools said
	// those cost, 0 when they didn't say.
	Tokens  int64   `json:"tokens"`
	CostUSD float64 `json:"costUsd"`
	// AgentMinutes is how long its agents' machines ran here.
	AgentMinutes int64 `json:"agentMinutes"`
}

// UpdateShareRequest is PATCH /v1/share.
type UpdateShareRequest struct {
	Slots *int    `json:"slots,omitempty"`
	Name  *string `json:"name,omitempty"`
}

// UpdateSharePeerRequest is PATCH /v1/share/peers/{id}: what is left out
// stays as it is.
type UpdateSharePeerRequest struct {
	AllProjects *bool     `json:"allProjects,omitempty"`
	Projects    *[]string `json:"projects,omitempty"`
	// Slots is -1 to follow ShareStatus.Slots.
	Slots *int `json:"slots,omitempty"`
	// Access is ShareAccessView, ShareAccessChat or ShareAccessFull.
	Access *string `json:"access,omitempty"`
	// OwnMachine lets it run agents on its own computer, or stops it.
	OwnMachine *bool `json:"ownMachine,omitempty"`
	// Sync lets it sync with this AgentBox, or stops it.
	Sync *bool `json:"sync,omitempty"`
}

// SharePairing is POST /v1/share/pairings: a one-time link another
// AgentBox pastes to pair, valid until Expires.
type SharePairing struct {
	Link    string    `json:"link"`
	Links   []string  `json:"links"` // every address it can be reached at, the likeliest first
	Expires time.Time `json:"expires"`
}

// ShareRemote is another AgentBox this one paired with: the owner of the
// projects it shares here.
type ShareRemote struct {
	ID     string    `json:"id"`
	Name   string    `json:"name"`
	URL    string    `json:"url"`
	Paired time.Time `json:"paired"`
	// Projects are how many projects it shares now, and Error why it
	// couldn't be reached the last time it was asked.
	Projects int    `json:"projects"`
	Error    string `json:"error,omitempty"`
	// Machine says this computer is connected to it to run, here, the
	// agents its projects' chats make for this AgentBox, and MachineError
	// why it isn't, when it isn't.
	Machine      bool   `json:"machine"`
	MachineError string `json:"machineError,omitempty"`
}

// Where the agents a project's chat makes for a paired AgentBox run, as its
// composer chooses for each message (ChatMessageRequest.RunOn).
const (
	RunOnOwner = "owner" // on the computer that shares the project, the default
	RunOnPeer  = "peer"  // on the paired AgentBox's own computer
)

// ShareRunOn is GET /v1/projects/{project}/run-on, for a project another
// AgentBox shares with this one: whether this computer can run the agents
// its chat makes for this AgentBox ("On my PC"), and why not when it can't.
type ShareRunOn struct {
	Owner string `json:"owner"` // what the project's AgentBox is called
	Here  bool   `json:"here"`
	// Why is why it can't, for the composer to show.
	Why string `json:"why,omitempty"`
}

// ShareOwnerRunOn is what the owner's /share/run-on answers a peer about one
// of the projects it shares: whether it lets the peer run agents for it on
// its own computer, and the repository a computer clones the project from.
type ShareOwnerRunOn struct {
	Allowed bool   `json:"allowed"`
	Why     string `json:"why,omitempty"`
	Repo    string `json:"repo,omitempty"`
	// Connected says the peer's computer is connected to take agents.
	Connected bool `json:"connected"`
}

// ShareAgentEvent is what a paired AgentBox tells the owner, at
// /share/agent-events, about an agent it runs for one of the owner's
// projects' chats: what the owner shows of it, and what its lead is told.
type ShareAgentEvent struct {
	Project string `json:"project"` // the project's name on the owner
	Agent   string `json:"agent"`
	Title   string `json:"title,omitempty"`
	Branch  string `json:"branch,omitempty"`
	Status  string `json:"status,omitempty"`
	Summary string `json:"summary,omitempty"`
	PRURL   string `json:"prUrl,omitempty"`
	Notice  string `json:"notice,omitempty"`
	Act     bool   `json:"act,omitempty"`
	// Session is the session of the project's chat whose lead made the
	// agent, which the notice goes to; "" for the one most recently active.
	Session string `json:"session,omitempty"`
}

// AddShareRemoteRequest is POST /v1/share/remotes: the link the owner made.
type AddShareRemoteRequest struct {
	Link string `json:"link"`
}

// ProjectRemote is set on a Project that another AgentBox shares with this
// one: whose it is. Its Name is "<remote>~<project>", which the daemon
// passes on to that AgentBox.
type ProjectRemote struct {
	ID    string `json:"id"`
	Owner string `json:"owner"`
	// Access is what its owner lets this AgentBox do in it (ShareAccessView
	// and the rest); "" from an owner from before access levels, which
	// allowed everything.
	Access string `json:"access,omitempty"`
}

// SharePairRequest is what a peer's daemon sends the owner's
// /share/pair: the link's secret, and what it calls itself.
type SharePairRequest struct {
	Secret string `json:"secret"`
	Name   string `json:"name"`
	// URLs are where the owner can reach the peer, to tell it its own new
	// address when that changes: the peer's phone page's addresses, none
	// when it has none.
	URLs []string `json:"urls,omitempty"`
	// Again proves each pairing the peer already has, with any owner: an
	// owner that finds itself among them gives no new token, only takes
	// the link as its new address (a quick tunnel's, which changes).
	Again []ShareAgain `json:"again,omitempty"`
}

// ShareAgain is a pairing a peer has already: its id on its owner (the one
// in its token), and an HMAC of the link's secret keyed with its token's
// hash, which only that owner can check.
type ShareAgain struct {
	Peer  string `json:"peer"`
	Proof string `json:"proof"`
}

// SharePairResponse is the owner's answer: the peer's token, and what the
// owner calls itself.
type SharePairResponse struct {
	Token string `json:"token"`
	Owner string `json:"owner"`
	// Peer and Proof answer a request with Again instead of Token: the
	// pairing the owner found, and an HMAC that shows it is that owner.
	Peer  string `json:"peer,omitempty"`
	Proof string `json:"proof,omitempty"`
}

// ShareAddress is where one side of a pairing is reached now: what an owner
// tells its peers when its address changes (/share/owner-address, signed),
// what a peer tells its owner of its own (/share/peer-address), and what an
// owner answers another machine of its hub account looking for it.
type ShareAddress struct {
	Peer string    `json:"peer"` // the pairing's id on the owner
	URLs []string  `json:"urls"` // the likeliest first
	At   time.Time `json:"at"`
}

// ShareHello is an owner's answer to a peer making sure an address is the
// owner's before it sends its token there (/share/hello): an HMAC of the
// peer's nonce.
type ShareHello struct {
	Proof string `json:"proof"`
}

// ErrorShareSlotsFull is the Code of a request from a paired AgentBox to
// make an agent when as many of its agents run as its slots allow.
const ErrorShareSlotsFull = "share-slots-full"

// Share is what this AgentBox shares, with whom, and whose projects it shows.
func (c *Client) Share(ctx context.Context) (ShareStatus, error) {
	var out ShareStatus
	return out, c.do(ctx, http.MethodGet, "/v1/share", nil, &out)
}

// UpdateShare changes the slots every peer gets, or this AgentBox's name.
func (c *Client) UpdateShare(ctx context.Context, req UpdateShareRequest) (ShareStatus, error) {
	var out ShareStatus
	return out, c.do(ctx, http.MethodPatch, "/v1/share", req, &out)
}

// PairShare makes a one-time link another AgentBox pairs with.
func (c *Client) PairShare(ctx context.Context) (SharePairing, error) {
	var out SharePairing
	return out, c.do(ctx, http.MethodPost, "/v1/share/pairings", nil, &out)
}

// UpdateSharePeer changes what a peer reaches and its slots.
func (c *Client) UpdateSharePeer(ctx context.Context, id string, req UpdateSharePeerRequest) (SharePeer, error) {
	var out SharePeer
	return out, c.do(ctx, http.MethodPatch, "/v1/share/peers/"+url.PathEscape(id), req, &out)
}

// RemoveSharePeer unpairs a peer.
func (c *Client) RemoveSharePeer(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/share/peers/"+url.PathEscape(id), nil, nil)
}

// AddShareRemote pairs with another AgentBox, with the link it made.
func (c *Client) AddShareRemote(ctx context.Context, link string) (ShareRemote, error) {
	var out ShareRemote
	return out, c.do(ctx, http.MethodPost, "/v1/share/remotes", AddShareRemoteRequest{Link: link}, &out)
}

// ShareRunOn says whether this computer can run the agents a remote
// project's chat makes for this AgentBox.
func (c *Client) ShareRunOn(ctx context.Context, project string) (ShareRunOn, error) {
	var out ShareRunOn
	return out, c.do(ctx, http.MethodGet, "/v1/projects/"+url.PathEscape(project)+"/run-on", nil, &out)
}

// RemoveShareRemote forgets a pairing with another AgentBox.
func (c *Client) RemoveShareRemote(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/share/remotes/"+url.PathEscape(id), nil, nil)
}

// Syncing with the AgentBoxes this one paired with (internal/daemon/sync.go).
// It happens only when asked: POST /v1/share/sync/plan is a job that fetches
// what each shares and works out what a sync would do, a SyncPlan, changing
// nothing; POST /v1/share/sync is the job that does it, with the user's
// choice for everything both sides have with other values.

// SyncPlanRequest is POST /v1/share/sync/plan.
type SyncPlanRequest struct {
	// Remotes are the AgentBoxes to sync with (ShareRemote.ID); none is
	// every one this AgentBox paired with.
	Remotes []string `json:"remotes,omitempty"`
}

// SyncPlan is what a sync would do, until Expires.
type SyncPlan struct {
	ID      string           `json:"id"`
	Expires time.Time        `json:"expires"`
	Remotes []SyncPlanRemote `json:"remotes"`
	// Projects are every project a sync could carry, here or there.
	Projects []SyncPlanProject `json:"projects"`
	// Conflicts are what this AgentBox and another have with other values:
	// one of each thing, with every other AgentBox's version of it.
	Conflicts []SyncConflict `json:"conflicts"`
}

// SyncPlanRemote is one AgentBox of a plan, and Error why it can't be synced
// with, when it can't.
type SyncPlanRemote struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Error string `json:"error,omitempty"`
	// Incoming is how many things it has that this AgentBox doesn't.
	Incoming int `json:"incoming"`
}

// SyncPlanProject is a project of a plan.
type SyncPlanProject struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	// Here says this AgentBox has it, and On which of the others do.
	Here bool     `json:"here"`
	On   []string `json:"on"`
	// Incoming is how many things of it would be added here.
	Incoming int `json:"incoming"`
	// Skip is why it can't be synced: another repository under one name.
	Skip string `json:"skip,omitempty"`
	// Secrets are the names of the secrets both this AgentBox and another
	// have for it. Secrets are never synced, and their values never
	// compared: each side keeps its own.
	Secrets []string `json:"secrets,omitempty"`
}

// SyncConflict is one thing with other values here and elsewhere. Mine is in
// each version's fields; a version is one AgentBox's.
type SyncConflict struct {
	ID       string                `json:"id"`
	Project  string                `json:"project,omitempty"`
	Kind     string                `json:"kind"`
	Label    string                `json:"label"`
	Versions []SyncConflictVersion `json:"versions"`
}

type SyncConflictVersion struct {
	Remote string                  `json:"remote"`
	Name   string                  `json:"name"`
	Fields []TransferConflictField `json:"fields"`
}

// SyncRequest is POST /v1/share/sync: carry out a plan.
type SyncRequest struct {
	Plan string `json:"plan"`
	// Projects are the ones to sync, by name; nil is all of the plan's.
	Projects *[]string `json:"projects,omitempty"`
	// Keep says, by SyncConflict.ID, whose version every AgentBox ends up
	// with: a ShareRemote.ID, or "" (or absent) for this AgentBox's.
	Keep map[string]string `json:"keep,omitempty"`
}

// SyncReport is a finished sync job's result.
type SyncReport struct {
	Remotes       []SyncRemoteReport `json:"remotes"`
	ProjectsAdded []string           `json:"projectsAdded"`
}

// SyncRemoteReport is what a sync did with one AgentBox: what came here from
// it, and what went there. Left is what still differs there, because it
// changed while the sync was being decided: syncing again asks about it.
type SyncRemoteReport struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Error    string `json:"error,omitempty"`
	Received int    `json:"received"`
	Replaced int    `json:"replaced"`
	Sent     int    `json:"sent"`
	SentOver int    `json:"sentOver"`
	Left     int    `json:"left"`
	// ProjectsAdded are the projects it didn't have and has now.
	ProjectsAdded []string `json:"projectsAdded"`
	Notes         []string `json:"notes,omitempty"`
}

// ShareSyncPush is what a paired AgentBox sends before the archive it pushes
// (/share/sync/push): the conflicts where its values win.
type ShareSyncPush struct {
	Take []string `json:"take,omitempty"`
}

// PlanSync starts the job that works out what syncing with paired
// AgentBoxes would do; its result is a SyncPlan.
func (c *Client) PlanSync(ctx context.Context, req SyncPlanRequest) (Job, error) {
	var out Job
	return out, c.do(ctx, http.MethodPost, "/v1/share/sync/plan", req, &out)
}

// Sync starts the job that carries a plan out; its result is a SyncReport.
func (c *Client) Sync(ctx context.Context, req SyncRequest) (Job, error) {
	var out Job
	return out, c.do(ctx, http.MethodPost, "/v1/share/sync", req, &out)
}

// ShareSyncSecrets is what an owner answers a peer that may sync with it
// (/share/sync/secrets): the names, never the values, of the secrets of each
// project it shares with it, so a plan can say which both sides have.
type ShareSyncSecrets struct {
	Projects map[string][]string `json:"projects"`
}
