package omarchy

import (
	"context"
	"sync"
	"time"
)

// DefaultInterval is how often the watcher looks. A theme change is something
// a person does, by hand, a few times a day at most: the cost of noticing it a
// second late is nothing, and the cost of looking is two stats and a
// six-hundred-byte read.
const DefaultInterval = 2 * time.Second

// Watcher keeps the current Omarchy theme, and says when it changes.
//
// It polls rather than watching with inotify, and that is not laziness:
// omarchy-theme-set builds the new theme in a staging directory and `mv`s it
// over the current one, so every inotify watch on that directory dies with the
// directory it was on. Re-establishing watches after each swap is more moving
// parts than re-reading a small file, and it has a race a poll doesn't.
type Watcher struct {
	home     string
	interval time.Duration

	mu      sync.RWMutex
	palette Palette
	found   bool
}

// NewWatcher reads home's current theme once, so Palette is true before Run
// has ever ticked.
func NewWatcher(home string) *Watcher {
	w := &Watcher{home: home, interval: DefaultInterval}
	w.palette, w.found = Read(home)
	return w
}

// Palette is the theme Omarchy has applied, or ok=false when there is none —
// which is the answer on any machine without Omarchy on it.
func (w *Watcher) Palette() (Palette, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.palette, w.found
}

// Run calls changed every time the theme becomes something other than what it
// was, until ctx is done. It never calls changed for the theme it started
// with: a caller that wants that reads Palette. changed runs on the watcher's
// own goroutine, so a slow one delays the next poll and nothing else.
func (w *Watcher) Run(ctx context.Context, changed func()) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if w.refresh() && changed != nil {
			changed()
		}
	}
}

// refresh re-reads the theme and reports whether it moved.
func (w *Watcher) refresh() bool {
	palette, found := Read(w.home)
	w.mu.Lock()
	defer w.mu.Unlock()
	if found == w.found && palette == w.palette {
		return false
	}
	w.palette, w.found = palette, found
	return true
}
