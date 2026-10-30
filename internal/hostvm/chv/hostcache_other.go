//go:build !linux

package chv

// The host's page cache and memory are only measured, and its cache of the
// VM's disks only dropped, on Linux: a Mac's VM has its own disk cache.

func directIO(string) bool                                      { return false }
func imageCached(string) int64                                  { return 0 }
func dropImageCache(string)                                     {}
func hostMemSample() (total, available int64, pressure float64) { return 0, 0, 0 }
func footprint([]int) int64                                     { return 0 }
