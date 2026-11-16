// Package installsh holds the scripts that install AgentBox with one line:
// install.sh, on Linux and on a Mac (curl -fsSL
// https://downloads.agentbox.linting.dev/install.sh | sh), and install.ps1, on
// Windows (irm https://downloads.agentbox.linting.dev/install.ps1 | iex).
// They live in a Go package so that Go's tests, which CI runs, cover them:
// they run each under its shell against a fake machine. scripts/r2-publish.sh
// uploads both to the downloads bucket with every stable release.
package installsh

import _ "embed"

// Script is install.sh.
//
//go:embed install.sh
var Script []byte

// PowerShell is install.ps1.
//
//go:embed install.ps1
var PowerShell []byte
