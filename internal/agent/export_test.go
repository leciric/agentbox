package agent

// GHPath is where the lead's own GitHub CLI is installed, for tests that stub
// it so no download is attempted.
func (m *Manager) GHPath() string { return m.ghPath() }

// KeepTurnCheckpoints is how many turns' checkpoints are kept.
const KeepTurnCheckpoints = keepTurnCheckpoints
