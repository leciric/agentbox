package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/chat"
	"agentbox/internal/state"
)

// An agent's timeline: a checkpoint of its worktree at the end of every chat
// turn (agent/checkpoint.go), which the user, the CLI or the lead can roll it
// back to or fork a new agent from, its conversation following
// (chat/timeline.go).
//
// Rolling back is quick git work, so it answers at once rather than as a job.
// Checkpoints and rollbacks of one agent take turns (timelineLock): a
// checkpoint landing in the middle of a rollback would record files half put
// back as a turn that is about to be gone.

func (s *Server) timelineLock(ref string) *sync.Mutex {
	mu, _ := s.timeline.LoadOrStore(ref, &sync.Mutex{})
	return mu.(*sync.Mutex)
}

// checkpointTurn is the chat's TurnEnded: it records the agent's worktree as
// the end of the turn, unless the turn has been rolled back or cleared since.
func (s *Server) checkpointTurn(a state.Agent, turn string) {
	mu := s.timelineLock(a.Ref())
	mu.Lock()
	defer mu.Unlock()
	ctx := context.Background()
	a, err := s.store.Agent(ctx, a.Project, a.Name)
	if err != nil || a.Worktree == "" {
		return
	}
	number, prompt, ok := s.chat.Turn(a, turn)
	if !ok {
		return
	}
	if _, err := s.manager(nil).Checkpoint(ctx, a, turn, number, prompt); err != nil {
		s.logf("Checkpointing turn %d of %s: %v", number, a.Ref(), err)
		return
	}
	s.chat.Checkpointed(a, turn)
}

func toAPICheckpoint(cp state.Checkpoint) api.Checkpoint {
	return api.Checkpoint{
		ID: cp.ID, Kind: cp.Kind, Turn: cp.Turn, Number: cp.Number, Prompt: cp.Prompt,
		Commit: cp.Commit, Head: cp.Head, CreatedAt: cp.CreatedAt,
	}
}

func (s *Server) listCheckpoints(w http.ResponseWriter, r *http.Request) error {
	a, err := s.agentFromPath(r)
	if err != nil {
		return err
	}
	list, err := s.manager(nil).Checkpoints(r.Context(), a)
	if err != nil {
		return err
	}
	out := make([]api.Checkpoint, 0, len(list))
	for _, cp := range list {
		out = append(out, toAPICheckpoint(cp))
	}
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) rollback(w http.ResponseWriter, r *http.Request) error {
	a, err := s.agentFromPath(r)
	if err != nil {
		return err
	}
	var req api.RollbackRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	saved, err := s.rollBack(r.Context(), a, req.Checkpoint)
	if err != nil {
		return err
	}
	s.countFeature(api.FeatureAgentRollback)
	return writeJSON(w, http.StatusOK, api.RollbackResult{Saved: toAPICheckpoint(saved)})
}

// rollBack puts the agent's files back to a turn's checkpoint, then its
// conversation, whose session starts again told the turns up to there.
func (s *Server) rollBack(ctx context.Context, a state.Agent, id string) (state.Checkpoint, error) {
	if id == "" {
		return state.Checkpoint{}, errors.New("say which checkpoint to roll back to")
	}
	mu := s.timelineLock(a.Ref())
	mu.Lock()
	defer mu.Unlock()
	cp, err := s.store.Checkpoint(ctx, a.Project, a.Name, agent.CheckpointID(id))
	if err != nil {
		return state.Checkpoint{}, err
	}
	if cp.Kind != state.CheckpointTurn {
		return state.Checkpoint{}, fmt.Errorf("%s is what %s had before a rollback, not a turn: fork from it instead", cp.ID, a.Ref())
	}
	if s.chat.Busy(a) {
		return state.Checkpoint{}, fmt.Errorf("%s: %w", a.Ref(), chat.ErrTurnRunning)
	}
	all, err := s.chat.UpTo(a, "")
	if err != nil {
		return state.Checkpoint{}, err
	}
	conversation := chat.Handoff(pointers(all), "This was "+a.Ref()+"'s conversation before it was rolled back to turn "+fmt.Sprint(cp.Number)+".")
	saved, err := s.manager(nil).RollBack(ctx, a, cp, conversation)
	if err != nil {
		return state.Checkpoint{}, err
	}
	intro := fmt.Sprintf("The user rolled this conversation back to the end of turn %d, and your worktree with it: "+
		"what you did after that is undone, and the files are as they were then.", cp.Number)
	notice := fmt.Sprintf("Rolled back to turn %d. What the worktree had is saved as %s, which can be forked from.", cp.Number, saved.ID)
	if err := s.chat.Rewind(a, cp.Turn, intro, notice); err != nil {
		// The files are back but the conversation isn't: a turn began in the
		// moment between. Rolling back again, once it ends, finishes the job.
		return saved, fmt.Errorf("%s's files are back at turn %d, but not its conversation: %w", a.Ref(), cp.Number, err)
	}
	return saved, nil
}

// forkConversation is what a fork from a checkpoint starts its chat with: the
// source's conversation up to the turn, and the handoff its first session is
// told. A saved checkpoint's conversation is gone from the source's chat, so
// the fork is only told it.
type forkConversation struct {
	items        []api.ChatItem
	handoff      string
	notice       string
	checkpointID string
}

func (s *Server) forkConversation(ctx context.Context, src state.Agent, id string) (*forkConversation, error) {
	cp, err := s.store.Checkpoint(ctx, src.Project, src.Name, agent.CheckpointID(id))
	if err != nil {
		return nil, err
	}
	fc := &forkConversation{checkpointID: cp.ID}
	if cp.Kind == state.CheckpointSaved {
		fc.handoff = cp.Context
		fc.notice = fmt.Sprintf("Forked from %s as it was before a rollback (%s).", src.Ref(), cp.ID)
		return fc, nil
	}
	items, err := s.chat.UpTo(src, cp.Turn)
	if err != nil {
		// The turn's checkpoint outlived its conversation (cleared): the
		// files alone, then.
		fc.notice = fmt.Sprintf("Forked from %s at turn %d, whose conversation is gone.", src.Ref(), cp.Number)
		return fc, nil
	}
	fc.items = items
	fc.handoff = chat.Handoff(pointers(items), fmt.Sprintf("You are a fork of %s, from the end of its turn %d: "+
		"your branch starts from its files as they were then.", src.Ref(), cp.Number))
	fc.notice = fmt.Sprintf("Forked from %s at turn %d.", src.Ref(), cp.Number)
	return fc, nil
}

func pointers(items []api.ChatItem) []*api.ChatItem {
	out := make([]*api.ChatItem, len(items))
	for i := range items {
		out[i] = &items[i]
	}
	return out
}
