package chv

import (
	"io/fs"
	"os"
	"path/filepath"

	"agentbox/internal/api"
)

// DiskImage is a disk image's apparent size and what it takes on the host's
// disk; zero when it isn't there.
func DiskImage(path string) api.VMDiskImage {
	fi, err := os.Stat(path)
	if err != nil {
		return api.VMDiskImage{}
	}
	return api.VMDiskImage{Size: fi.Size(), Allocated: allocated(fi)}
}

// DirAllocated is what the files under dir take on the host's disk, counting
// a file with several links once; 0 when dir isn't there. What can't be read
// is left out.
func DirAllocated(dir string) int64 {
	var total int64
	seen := map[[2]uint64]bool{}
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		if id, ok := fileID(fi); ok {
			if seen[id] {
				return nil
			}
			seen[id] = true
		}
		total += allocated(fi)
		return nil
	})
	return total
}
