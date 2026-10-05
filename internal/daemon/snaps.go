package daemon

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/snap"
	"agentbox/internal/state"
)

// SnapShots (internal/snap) arrive here from agentbox snap on the user's
// machine, wait in memory for the app's composer, and leave as a message with
// a picture in a project's chat or an agent's, through the same path as a
// message the user types (tellAgent). Nothing is written to disk until it is
// sent: a capture nobody sends is gone after api.SnapTTL, or a daemon
// restart.

type heldSnap struct {
	api.Snap
	image    []byte
	received time.Time // the daemon's clock, not the capturing machine's
}

type snapStore struct {
	mu    sync.Mutex
	snaps []heldSnap // oldest first
}

// sweep drops what has outlived api.SnapTTL. The caller holds mu.
func (st *snapStore) sweep(now time.Time) {
	st.snaps = slices.DeleteFunc(st.snaps, func(h heldSnap) bool { return now.Sub(h.received) > api.SnapTTL })
}

func (st *snapStore) add(h heldSnap) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if h.received.IsZero() {
		h.received = time.Now()
	}
	st.sweep(time.Now())
	st.snaps = append(st.snaps, h)
	if over := len(st.snaps) - api.MaxSnaps; over > 0 {
		st.snaps = st.snaps[over:]
	}
}

func (st *snapStore) get(id string) (heldSnap, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.sweep(time.Now())
	for _, h := range st.snaps {
		if h.ID == id {
			return h, nil
		}
	}
	return heldSnap{}, fmt.Errorf("SnapShot %s: %w (it was sent, dropped, or older than an hour)", id, state.ErrNotFound)
}

func (st *snapStore) list() []api.Snap {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.sweep(time.Now())
	out := make([]api.Snap, 0, len(st.snaps))
	for _, h := range st.snaps {
		out = append(out, h.Snap)
	}
	return out
}

func (st *snapStore) drop(id string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.snaps = slices.DeleteFunc(st.snaps, func(h heldSnap) bool { return h.ID == id })
}

var snapImageTypes = []string{"image/png", "image/jpeg"}

// takeSnap holds a capture and tells the app, whose composer opens on it.
func (s *Server) takeSnap(w http.ResponseWriter, r *http.Request) error {
	var req api.SnapRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if !slices.Contains(snapImageTypes, req.Image.MimeType) {
		return fmt.Errorf("a SnapShot is a PNG or a JPEG, not %q", req.Image.MimeType)
	}
	img, err := base64.StdEncoding.DecodeString(req.Image.Data)
	if err != nil || len(img) == 0 {
		return errors.New("the SnapShot's picture isn't base64")
	}
	if len(img) > api.MaxChatImageBytes {
		return fmt.Errorf("the SnapShot's picture is %d bytes, more than the %d a chat takes", len(img), api.MaxChatImageBytes)
	}
	if req.TakenAt.IsZero() {
		req.TakenAt = time.Now()
	}
	id := make([]byte, 8)
	_, _ = rand.Read(id)
	h := heldSnap{image: img, Snap: api.Snap{
		ID: hex.EncodeToString(id), App: req.App, Title: req.Title, Desktop: req.Desktop, Window: req.Window,
		Accessibility: req.Accessibility, MimeType: req.Image.MimeType, Size: int64(len(img)), TakenAt: req.TakenAt, Project: req.Project,
	}}
	s.snaps.add(h)
	s.events.publish(api.EventSnap, h.Snap)
	return writeJSON(w, http.StatusCreated, h.Snap)
}

func (s *Server) listSnaps(w http.ResponseWriter, _ *http.Request) error {
	return writeJSON(w, http.StatusOK, s.snaps.list())
}

func (s *Server) snapImage(w http.ResponseWriter, r *http.Request) error {
	h, err := s.snaps.get(r.PathValue("id"))
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", h.MimeType)
	w.Header().Set("Content-Length", strconv.Itoa(len(h.image)))
	w.Header().Set("Cache-Control", "private, max-age=3600, immutable")
	_, err = w.Write(h.image)
	return err
}

func (s *Server) dropSnap(w http.ResponseWriter, r *http.Request) error {
	s.snaps.drop(r.PathValue("id"))
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// sendSnap sends a capture as a bug report to a project's chat, or to one of
// its agents, and lets it go.
func (s *Server) sendSnap(w http.ResponseWriter, r *http.Request) error {
	h, err := s.snaps.get(r.PathValue("id"))
	if err != nil {
		return err
	}
	var req api.SnapSendRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if req.Project == "" {
		return errors.New("send the SnapShot where? Name a project")
	}
	r.SetPathValue("project", req.Project)
	var a state.Agent
	if req.Agent == "" {
		a, err = s.ensureLeadFromPath(r)
	} else {
		r.SetPathValue("agent", req.Agent)
		a, err = s.agentFromPath(r)
	}
	if err != nil {
		return err
	}
	tree := ""
	if req.Accessibility {
		tree = h.Accessibility
	}
	text := snap.Report(req.Note, h.App, h.Title, h.Desktop, h.Window, tree, h.TakenAt)
	ext := ".png"
	if h.MimeType == "image/jpeg" {
		ext = ".jpg"
	}
	t, err := s.tellAgent(r.Context(), a, text, api.ChatImageUpload{MimeType: h.MimeType, Name: "snapshot-" + h.TakenAt.Format("20060102-150405") + ext, Data: base64.StdEncoding.EncodeToString(h.image)})
	if err != nil {
		return err
	}
	s.snaps.drop(h.ID)
	return writeJSON(w, http.StatusAccepted, t.item)
}
