//go:build !unix

package agentd

// startTerminal has no pseudo-terminal to open here, so attach says so instead
// of pretending. A pod is Linux, and that is the only place a terminal is
// asked for.
func startTerminal(argv []string, dir string, env []string, cols, rows uint16) (terminal, error) {
	return nil, ErrNotSupported
}

// passwdShell has no passwd to read here, and loginShell falls back to
// /bin/sh.
func passwdShell() string { return "" }
