package gitrepo

import (
	"os/exec"
	"sync"
)

// lowPriority wraps a command to run at the lowest CPU priority (nice 19)
// and, where there is ionice (Linux), the lowest best-effort I/O priority:
// not the idle class, which a disk busy with swap would never serve. Either
// missing, the command runs as it is.
func lowPriority(name string, args []string) (string, []string) {
	nice, ionice := lowPriorityTools()
	if ionice != "" {
		args = append([]string{"-c", "2", "-n", "7", name}, args...)
		name = ionice
	}
	if nice != "" {
		args = append([]string{"-n", "19", name}, args...)
		name = nice
	}
	return name, args
}

var lowPriorityTools = sync.OnceValues(func() (nice, ionice string) {
	nice, _ = exec.LookPath("nice")
	ionice, _ = exec.LookPath("ionice")
	return nice, ionice
})
