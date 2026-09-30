//go:build !linux

package daemon

// onSharedFS is false off Linux: only a Linux side runs in AgentBox's VM.
func onSharedFS(string) bool { return false }
