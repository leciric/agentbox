package chat

import "slices"

// An agent listing (GET /v1/agents, and every agent in an action's reply)
// reads each agent's chat state, background tasks and progress. Read under
// the conversation's lock alone, one conversation holding it — a turn's end
// storing its items while the database is busy with other agents' writes,
// which can take the write gate's 30 seconds and more — stalled the listing
// of every agent behind it, and with it the app. A reader takes the lock only
// when it is free; otherwise it reads what the last flush left, which is at
// most a flush behind (flushDelay), as clients see the chat anyway.

// listing is what a reader takes of a conversation without its lock.
type listing struct {
	state      string
	background []string
	progress   Progress
	running    bool
}

// keepListing stores what a listing reads now. The conversation is locked.
func (c *conversation) keepListing() {
	p, running := c.progress()
	c.listing.Store(&listing{
		state:      c.session.State,
		background: slices.Clone(c.session.Background),
		progress:   p,
		running:    running,
	})
}

// listingNow is the conversation's listing: read live when its lock is free,
// what the last flush kept when it isn't. ok is false when the lock is held
// and nothing was kept yet: the caller then waits for the lock.
func (c *conversation) listingNow() (listing, bool) {
	if c.mu.TryLock() {
		c.keepListing()
		c.mu.Unlock()
	}
	l := c.listing.Load()
	if l == nil {
		return listing{}, false
	}
	return *l, true
}
