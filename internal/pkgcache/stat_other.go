//go:build !linux

package pkgcache

import (
	"io/fs"
	"time"
)

// The daemon runs on Linux, in AgentBox's VM; elsewhere a file's length and
// when it was written stand in.

func diskSize(info fs.FileInfo) int64 { return info.Size() }

func lastUsed(info fs.FileInfo) time.Time { return info.ModTime() }
