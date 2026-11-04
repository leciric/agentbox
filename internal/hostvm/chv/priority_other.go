//go:build !linux

package chv

// supervisorArgv is the supervisor's command line: a Mac's VM (vz) runs at
// the priority it was started with (priority_linux.go is Linux's).
func supervisorArgv(self, _ string) []string { return []string{self, "vm", "run"} }

func lowerPriority() []string { return nil }
