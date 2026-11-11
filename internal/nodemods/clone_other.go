//go:build !linux

package nodemods

import (
	"errors"
	"io/fs"
	"time"
)

// errNoClone is the file system can't clone a file: copyTree copies
// instead. Agents only run on Linux.
var errNoClone = errors.New("cloning isn't supported here")

func cloneFile(src, dst string, info fs.FileInfo) error { return errNoClone }

func inode(info fs.FileInfo) ([2]uint64, bool) { return [2]uint64{}, false }

func diskSize(info fs.FileInfo) int64 { return info.Size() }

func linkTime(path string, mod time.Time) error { return nil }
