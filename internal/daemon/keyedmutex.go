package daemon

import "sync"

// keyedMutex is a mutex for each key: holding one key's waits for nobody
// else's. A wake held one lock for every agent across a whole boot (up to
// readyTimeout), so after a restart every message to any stopped agent, and
// every turn carried on, waited behind each boot in turn.
type keyedMutex struct {
	mu    sync.Mutex
	locks map[string]*keyedLock
}

type keyedLock struct {
	mu    sync.Mutex
	users int
}

// lock locks key, and returns what unlocks it.
func (k *keyedMutex) lock(key string) (unlock func()) {
	k.mu.Lock()
	if k.locks == nil {
		k.locks = map[string]*keyedLock{}
	}
	l := k.locks[key]
	if l == nil {
		l = &keyedLock{}
		k.locks[key] = l
	}
	l.users++
	k.mu.Unlock()
	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		k.mu.Lock()
		if l.users--; l.users == 0 {
			delete(k.locks, key)
		}
		k.mu.Unlock()
	}
}
