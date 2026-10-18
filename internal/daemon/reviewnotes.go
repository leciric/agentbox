package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// Notes on lines of an agent's diff, left in the review's Changes view and
// sent all at once with Request changes (requestChanges). They are kept by
// the daemon, not the app, so closing the review loses none, and once sent
// they stay as what the agent was asked, shown in its next review as
// pending or resolved.

// listReviewNotes is GET /v1/agents/{project}/{agent}/review-notes.
func (s *Server) listReviewNotes(w http.ResponseWriter, r *http.Request) error {
	a, err := s.agentFromPath(r)
	if err != nil {
		return err
	}
	notes, err := s.store.ReviewNotes(r.Context(), a.Project, a.Name)
	if err != nil {
		return err
	}
	out := make([]api.ReviewNote, 0, len(notes))
	for _, n := range notes {
		out = append(out, toAPIReviewNote(n, a.Worktree))
	}
	return writeJSON(w, http.StatusOK, out)
}

// addReviewNote is POST /v1/agents/{project}/{agent}/review-notes.
func (s *Server) addReviewNote(w http.ResponseWriter, r *http.Request) error {
	a, err := s.agentFromPath(r)
	if err != nil {
		return err
	}
	var req api.AddReviewNoteRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	body := strings.TrimSpace(req.Body)
	switch {
	case body == "":
		return errors.New("say what the note is")
	case !filepath.IsLocal(filepath.FromSlash(req.Path)):
		return fmt.Errorf("%q isn't a file in the agent's worktree", req.Path)
	case req.Side != api.ReviewSideNew && req.Side != api.ReviewSideOld:
		return fmt.Errorf("side must be %q or %q", api.ReviewSideNew, api.ReviewSideOld)
	case req.Start < 1 || req.End < req.Start:
		return fmt.Errorf("lines %d to %d aren't a range of lines", req.Start, req.End)
	}
	n, err := s.store.AddReviewNote(r.Context(), state.ReviewNote{
		Project: a.Project, Agent: a.Name, Path: req.Path, Side: req.Side, Start: req.Start, End: req.End, Code: req.Code, Body: body,
	})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, toAPIReviewNote(n, a.Worktree))
}

// editReviewNote is PATCH /v1/agents/{project}/{agent}/review-notes/{id}:
// a draft rewritten, or a sent note resolved.
func (s *Server) editReviewNote(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	a, id, err := s.reviewNoteFromPath(r)
	if err != nil {
		return err
	}
	var req api.EditReviewNoteRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if req.Body != nil {
		body := strings.TrimSpace(*req.Body)
		if body == "" {
			return errors.New("say what the note is")
		}
		if err := s.store.SetReviewNoteBody(ctx, a.Project, a.Name, id, body); err != nil {
			return sentNote(err)
		}
	}
	if req.Resolved != nil {
		if err := s.store.SetReviewNoteResolved(ctx, a.Project, a.Name, id, *req.Resolved, time.Now()); err != nil {
			return err
		}
	}
	n, err := s.store.ReviewNote(ctx, a.Project, a.Name, id)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, toAPIReviewNote(n, a.Worktree))
}

// sentNote says why a note couldn't be rewritten: the agent already has it.
func sentNote(err error) error {
	if errors.Is(err, state.ErrNotFound) {
		return fmt.Errorf("no draft note to change, it may have been sent: %w", err)
	}
	return err
}

// deleteReviewNote is DELETE /v1/agents/{project}/{agent}/review-notes/{id}.
func (s *Server) deleteReviewNote(w http.ResponseWriter, r *http.Request) error {
	a, id, err := s.reviewNoteFromPath(r)
	if err != nil {
		return err
	}
	if err := s.store.DeleteReviewNote(r.Context(), a.Project, a.Name, id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) reviewNoteFromPath(r *http.Request) (state.Agent, int64, error) {
	a, err := s.agentFromPath(r)
	if err != nil {
		return state.Agent{}, 0, err
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return state.Agent{}, 0, fmt.Errorf("note %q: %w", r.PathValue("id"), state.ErrNotFound)
	}
	return a, id, nil
}

// draftNotes are the agent's drafts by ids, in the order they were written,
// or an error naming one that isn't a draft of the agent's.
func (s *Server) draftNotes(ctx context.Context, a state.Agent, ids []int64) ([]state.ReviewNote, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	all, err := s.store.ReviewNotes(ctx, a.Project, a.Name)
	if err != nil {
		return nil, err
	}
	want := map[int64]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var out []state.ReviewNote
	for _, n := range all {
		if want[n.ID] {
			if !n.SentAt.IsZero() {
				return nil, fmt.Errorf("note %d was already sent", n.ID)
			}
			out = append(out, n)
			delete(want, n.ID)
		}
	}
	for id := range want {
		return nil, fmt.Errorf("note %d: %w", id, state.ErrNotFound)
	}
	return out, nil
}

// markNotesSent records notes as sent now, each with its file's hash.
func (s *Server) markNotesSent(ctx context.Context, a state.Agent, notes []state.ReviewNote) error {
	now := time.Now()
	for _, n := range notes {
		if err := s.store.MarkReviewNoteSent(ctx, a.Project, a.Name, n.ID, fileHash(a.Worktree, n.Path), now); err != nil {
			return err
		}
	}
	return nil
}

func toAPIReviewNote(n state.ReviewNote, worktree string) api.ReviewNote {
	out := api.ReviewNote{
		ID: n.ID, Path: n.Path, Side: n.Side, Start: n.Start, End: n.End, Code: n.Code, Body: n.Body,
		Status: noteStatus(n, worktree), CreatedAt: n.CreatedAt,
	}
	if !n.SentAt.IsZero() {
		out.SentAt = &n.SentAt
	}
	if !n.ResolvedAt.IsZero() {
		out.ResolvedAt = &n.ResolvedAt
	}
	return out
}

// noteStatus is where a note is: a draft, or sent and pending until the
// user resolves it or the code it quotes changes. A note on added or
// unchanged lines is resolved once those lines, together, are nowhere in
// the file in the agent's worktree (moved is still there); one on removed
// lines, which the worktree no longer has, once the file is any different
// from how it was when the note was sent.
func noteStatus(n state.ReviewNote, worktree string) string {
	switch {
	case n.SentAt.IsZero():
		return api.ReviewNoteDraft
	case !n.ResolvedAt.IsZero():
		return api.ReviewNoteResolved
	}
	if n.Side == api.ReviewSideNew {
		if quoted := newSide(n.Code); quoted != "" {
			content, err := readWorktreeFile(worktree, n.Path)
			if err != nil || !hasLines(content, quoted) {
				return api.ReviewNoteResolved
			}
			return api.ReviewNotePending
		}
	}
	if fileHash(worktree, n.Path) != n.SentHash {
		return api.ReviewNoteResolved
	}
	return api.ReviewNotePending
}

// newSide is the lines of a quote, as diff lines, that the file has after
// the change: the added and unchanged ones, without their signs.
func newSide(code string) string {
	var lines []string
	for line := range strings.SplitSeq(code, "\n") {
		if strings.HasPrefix(line, "+") || strings.HasPrefix(line, " ") {
			lines = append(lines, strings.TrimRight(line[1:], "\r"))
		}
	}
	return strings.Join(lines, "\n")
}

// hasLines is whether content has lines, whole lines one after another.
func hasLines(content, lines string) bool {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	return strings.Contains("\n"+content+"\n", "\n"+lines+"\n")
}

func readWorktreeFile(worktree, path string) (string, error) {
	rel := filepath.FromSlash(path)
	if worktree == "" || !filepath.IsLocal(rel) {
		return "", os.ErrNotExist
	}
	b, err := os.ReadFile(filepath.Join(worktree, rel))
	return string(b), err
}

// fileHash is the hash of a file in a worktree, or "" when there is none.
func fileHash(worktree, path string) string {
	content, err := readWorktreeFile(worktree, path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// noteLines is where a note is, as the agent is told it: path:12 or
// path:12-14, and for removed lines that they were the old file's.
func noteLines(n state.ReviewNote) string {
	where := n.Path + ":" + strconv.Itoa(n.Start)
	if n.End > n.Start {
		where += "-" + strconv.Itoa(n.End)
	}
	if n.Side == api.ReviewSideOld {
		where += " (removed lines, numbered as in the old file)"
	}
	return where
}

// notesText is the notes as the agent reads them: each one's place, the code
// it quotes as a diff, and what the user wrote.
func notesText(notes []state.ReviewNote) string {
	var b strings.Builder
	for i, n := range notes {
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "%d. %s\n", i+1, noteLines(n))
		if code := strings.TrimRight(n.Code, "\n"); code != "" {
			fence := "```"
			for strings.Contains(code, fence) {
				fence += "`"
			}
			fmt.Fprintf(&b, "\n%sdiff\n%s\n%s\n", fence, code, fence)
		}
		b.WriteString("\n" + n.Body)
	}
	return b.String()
}

// notesSummary is the notes in a line each, for what the app shows was asked
// (Agent.ChangesRequested).
func notesSummary(text string, notes []state.ReviewNote) string {
	parts := []string{}
	if text != "" {
		parts = append(parts, text)
	}
	for _, n := range notes {
		where := n.Path + ":" + strconv.Itoa(n.Start)
		if n.End > n.Start {
			where += "-" + strconv.Itoa(n.End)
		}
		parts = append(parts, where+" — "+n.Body)
	}
	return strings.Join(parts, "\n")
}
