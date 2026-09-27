//go:build !unix

package incus

import "os"

// owner is root elsewhere: the daemon only runs on Linux.
func owner(os.FileInfo) (uid, gid int64) { return 0, 0 }
