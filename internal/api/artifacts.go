package api

import "time"

// The artifacts API: the pages a project's chats published on Hatch, the
// project's own connector for HTML pages (hatch.linting.dev). A project's
// artifacts are read out of its chats — the calls its lead and its agents
// made to Hatch's publish_page and update_page, and what Hatch answered —
// not from Hatch's list_pages, which has every page of the account and
// nothing to tell one project's from another's. The app shows them in a
// tray over the chat's composer, and opens one in a sandboxed frame with its
// HTML fetched by the daemon, with the connector's sign-in: a private page
// can't be loaded from its link without the user's Hatch session.

// Artifact is one page on Hatch, as a project's chats made it: published by
// one chat, maybe updated since by others. update_page changes the page in
// place, under the same ID and link.
type Artifact struct {
	// ID is Hatch's page id, the one in its link (/p/<id>).
	ID    string `json:"id"`
	Title string `json:"title"`
	// URL is the page's share link, which opens it in the browser.
	URL string `json:"url"`
	// Agent is the ref of the agent whose chat made it (<project>/lead for
	// the project's chat): the one that published it, or, when the publish
	// isn't in any of the project's chats, the first that updated it.
	Agent string `json:"agent"`
	// Item is the chat item of that call, in Agent's chat.
	Item string `json:"item"`
	// Agents are the refs of every chat that published or updated it,
	// Agent first.
	Agents []string `json:"agents"`
	// Version is the newest version the chats published.
	Version int `json:"version"`
	// Public is whether it was published for anyone with its link.
	Public    bool      `json:"public,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"` // its newest version, from the chats
	// ExpiresAt is when Hatch deletes it, nil when it is permanent or the
	// chats don't say (Permanent tells the two apart).
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	Permanent bool       `json:"permanent,omitempty"`
	// Expired is ExpiresAt having passed: Hatch has deleted it.
	Expired bool `json:"expired,omitempty"`
}

// Artifacts is GET /v1/projects/{project}/artifacts[?agent=<name>]: the
// project's artifacts, newest first, or only those one agent's chat made or
// updated. Connector is the Hatch connector the project's agents get, enabled
// and signed in, that a preview is fetched with; when there is none,
// Connector is empty and Artifacts too, and the app shows nothing.
type Artifacts struct {
	Connector string     `json:"connector"`
	Artifacts []Artifact `json:"artifacts"`
}

// ArtifactPreview is GET /v1/projects/{project}/artifacts/{id}: one artifact
// as the chats have it, brought up to date by asking Hatch (get_page) — its
// title, its expiry, whether it is still there — and whether its HTML can be
// shown, at GET .../artifacts/{id}/page.
type ArtifactPreview struct {
	Artifact Artifact `json:"artifact"`
	// Status is ArtifactActive, ArtifactExpired or ArtifactUnavailable.
	Status string `json:"status"`
	// Page is whether .../page serves its HTML. Hatch hands it over
	// (get_page's content_url) only since leciric/hatch#3; before, and when
	// Hatch can't be reached, the app offers the link instead.
	Page bool `json:"page"`
	// Error is why Status is ArtifactUnavailable, or why Page is false.
	Error string `json:"error,omitempty"`
}

// What became of an artifact on Hatch.
const (
	ArtifactActive  = "active"
	ArtifactExpired = "expired" // deleted when it expired, or by its owner
	// ArtifactUnavailable is Hatch not answering, or the connector unable
	// to ask: the artifact may well be there.
	ArtifactUnavailable = "unavailable"
)
