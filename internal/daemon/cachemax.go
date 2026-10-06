package daemon

import (
	"context"
)

// The shared caches' caps fit the disk they're on: in a VM that was the
// system disk, of 20 GiB, under caps of 20 GiB each, and is now the agents'
// disk (moveDataToPool), of whatever size the user gave it.

// cacheDefaultShare is the most of its disk a cache takes when nobody chose
// its cap: an eighth, so the two together leave three quarters to the agents.
const cacheDefaultShare = 8

// minCacheMax is the least a cache is capped at: what setPackageCache and
// setImageCache refuse to go under.
const minCacheMax int64 = 1 << 30

// fitCacheMax is the cap a cache gets on a disk of total bytes with floor
// kept free, and its default there: def, or cacheDefaultShare of the disk
// when that's less; chosen, or what the disk can hold above its floor when
// that's less. A disk that can't be measured (total 0) changes nothing.
func fitCacheMax(chosen, def, total, floor int64) (maxBytes, fitDefault int64) {
	fitDefault = def
	if total > 0 {
		fitDefault = max(min(def, total/cacheDefaultShare), minCacheMax)
	}
	if chosen <= 0 {
		return fitDefault, fitDefault
	}
	if total > 0 {
		chosen = min(chosen, max(total-floor, minCacheMax))
	}
	return chosen, fitDefault
}

// cacheMax is fitCacheMax for a cache in dir, on the disk it's on now.
func (s *Server) cacheMax(ctx context.Context, dir string, chosen, def int64) (maxBytes, fitDefault int64) {
	_, total, _, ok := diskSpace(existingParent(dir))
	if !ok {
		total = 0
	}
	return fitCacheMax(chosen, def, total, s.diskFloor(ctx).For(total))
}
