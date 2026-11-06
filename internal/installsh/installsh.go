// Package installsh holds install.sh, the script that installs AgentBox on
// Linux (curl -fsSL https://downloads.agentbox.linting.dev/install.sh | sh).
// It lives in a Go package so that Go's tests, which CI runs, cover it: they
// run it under sh against a fake machine. scripts/r2-publish.sh uploads it to
// the downloads bucket with every stable release.
package installsh

import _ "embed"

// Script is install.sh.
//
//go:embed install.sh
var Script []byte
