//go:build !linux

package daemon

// onSharedFS is false off Linux: only a Linux side runs in AgentBox's VM.
func onSharedFS(string) bool { return false }

// diskSpace measures nothing off Linux, where no daemon runs.
func diskSpace(string) (free, total int64, id string, ok bool) { return 0, 0, "", false }
