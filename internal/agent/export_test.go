package agent

import (
	"context"

	"agentbox/internal/image"
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

// CheckPair is checkPair, which makes a before/after pair whole.
var CheckPair = checkPair

func (m *Manager) EnsureNodeModules(ctx context.Context, a state.Agent) { m.ensureNodeModules(ctx, a) }

// KeepNodeModules is what Destroy does with a's node_modules.
func (m *Manager) KeepNodeModules(a state.Agent) {
	m.keepNodeModules(a, m.nodeModulesInstalls(context.Background(), a, false))
}

// The paths a machine's user and tree live at, for machinepaths_test.go,
// which checks the seam from outside the package as the daemon uses it.
func (m *Manager) MachineUser(a state.Agent) image.User { return m.machineUser(a) }
func (m *Manager) MachineHome(a state.Agent) string     { return m.home(a) }
func (m *Manager) EnvPathFor(a state.Agent) string      { return m.envPath(a) }
func (m *Manager) BashEnvPathFor(a state.Agent) string  { return m.bashEnvPath(a) }
func (m *Manager) SecretsPathFor(a state.Agent) string  { return m.secretsPath(a) }
func (m *Manager) AttachArgv(a state.Agent) []string    { return m.attachArgv(a) }
