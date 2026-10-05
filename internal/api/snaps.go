package api

import "time"

// SnapShots: a picture of the window the user is working in, taken on their
// machine by agentbox snap (or the app's shortcut, which runs it), held by
// the daemon until the app's composer sends it to a project's chat or to an
// agent as a bug report, or drops it. Held in memory only, for SnapTTL.

// SnapRequest is a capture on its way to the daemon.
type SnapRequest struct {
	Image         ChatImageUpload `json:"image"`
	App           string          `json:"app,omitempty"`
	Title         string          `json:"title,omitempty"`
	Desktop       string          `json:"desktop,omitempty"` // hyprland, sway, kde, gnome, x11, mac or screen
	Window        bool            `json:"window"`            // false: the whole screen
	Accessibility string          `json:"accessibility,omitempty"`
	TakenAt       time.Time       `json:"takenAt"`
	// Project is the project the capture is meant for, when the one who
	// took it said; the composer starts there.
	Project string `json:"project,omitempty"`
}

// Snap is a capture the daemon holds. Its picture is at
// /v1/snaps/{id}/image.
type Snap struct {
	ID            string    `json:"id"`
	App           string    `json:"app,omitempty"`
	Title         string    `json:"title,omitempty"`
	Desktop       string    `json:"desktop,omitempty"`
	Window        bool      `json:"window"`
	Accessibility string    `json:"accessibility,omitempty"`
	MimeType      string    `json:"mimeType"`
	Size          int64     `json:"size"`
	TakenAt       time.Time `json:"takenAt"`
	Project       string    `json:"project,omitempty"`
}

// SnapSendRequest sends a capture: to a project's chat when Agent is empty,
// or to one of its agents, with the user's note.
type SnapSendRequest struct {
	Project string `json:"project"`
	Agent   string `json:"agent,omitempty"`
	Note    string `json:"note,omitempty"`
	// Accessibility says whether to include the accessibility summary.
	Accessibility bool `json:"accessibility"`
}

// EventSnap carries a Snap the moment it is taken: the app opens its
// composer and comes forward.
const EventSnap = "snap"

// SnapTTL is how long the daemon holds a capture nobody sent; MaxSnaps is how
// many it holds at once, the oldest dropped first.
const (
	SnapTTL  = time.Hour
	MaxSnaps = 8
)
