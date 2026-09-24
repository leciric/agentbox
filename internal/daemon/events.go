package daemon

import (
	"encoding/json"
	"sync"
	"time"

	"agentbox/internal/api"
)

// broker fans events out to subscribers. A subscriber that falls behind
// misses events rather than slowing the daemon down.
type broker struct {
	mu   sync.Mutex
	subs map[chan api.Event]struct{}
}

func newBroker() *broker { return &broker{subs: map[chan api.Event]struct{}{}} }

func (b *broker) subscribe() (<-chan api.Event, func()) {
	ch := make(chan api.Event, 256)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		delete(b.subs, ch)
		b.mu.Unlock()
	}
}

func (b *broker) publish(typ string, data any) {
	raw, err := json.Marshal(data)
	if err != nil {
		return
	}
	ev := api.Event{Type: typ, Time: time.Now(), Data: raw}
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

func (b *broker) subscribers() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}
