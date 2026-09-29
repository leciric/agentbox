package agent

import "testing"

// WithoutHostGPU makes HostGPU find nothing for the rest of the test, so a
// test that expects no GPU passes on a machine that has one.
func WithoutHostGPU(t *testing.T) {
	oldNvidia, oldGlob := nvidiaDevice, renderNodeGlob
	t.Cleanup(func() { nvidiaDevice, renderNodeGlob = oldNvidia, oldGlob })
	dir := t.TempDir()
	nvidiaDevice = dir + "/nvidia0"
	renderNodeGlob = dir + "/renderD*"
}

// GHPath is where the lead's own GitHub CLI is installed, for tests that stub
// it so no download is attempted.
func (m *Manager) GHPath() string { return m.ghPath() }
