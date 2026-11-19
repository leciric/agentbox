//go:build !linux

package chv

// supervisorArgv is the supervisor's command line: a Mac's VM (vz) runs at
// the priority it was started with (priority_linux.go is Linux's).
func supervisorArgv(self string, _ Config) []string { return []string{self, "vm", "run"} }

func limitScope(int64) string { return "" }

func lowerPriority() []string { return nil }

func priorityGaps(string) []string { return nil }
