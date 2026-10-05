package agent

import (
	"context"

	"agentbox/internal/state"
)

// GHPath is where the lead's own GitHub CLI is installed, for tests that stub
// it so no download is attempted.
func (m *Manager) GHPath() string { return m.ghPath() }

// EnsureRecordingStage mounts an agent's recording stage, as StartRecording does.
func (m *Manager) EnsureRecordingStage(ctx context.Context, a state.Agent) (bool, error) {
	return m.ensureRecordingStage(ctx, a)
}

// KeepTurnCheckpoints is how many turns' checkpoints are kept.
const KeepTurnCheckpoints = keepTurnCheckpoints
