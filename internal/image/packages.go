package image

import _ "embed"

// basePackages is base-packages.txt: the Debian packages every AgentBox runtime
// installs before any agent tool. One file, read by all of them, because a
// runtime missing one of these fails in a way that looks like the tool's own
// fault: a Claude Code login on a machine with no curl says "curl: command not
// found" only after it has announced the download.
//
//	system.sh, the Incus base image          apt with this file at /root
//	internal/agentimage/Dockerfile           the pod image
//	internal/machines/image/Dockerfile        the desktop machine
//	packaging/daemon/Dockerfile              the control plane container
//
// The machine-only pieces stay out and each runtime installs its own: sudo for
// the user the Incus image makes, Docker, mise, the desktop, Mesa.
//
//go:embed base-packages.txt
var basePackages []byte

// BasePackages is the file's content, for a build context assembled in Go
// (internal/machines/image.go) rather than copied from the repository.
func BasePackages() []byte { return basePackages }
