package iossim

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"agentbox/internal/devicestream"
)

// What simhost writes on its stdout: frames of a big-endian uint32 length
// (of the kind and the payload), a kind byte, and the payload. The config and
// the frames are internal/devicestream's, which the helper sends on as they
// are, in that package's wire format, to the app's DeviceStreamView.
const (
	kindConfig = 1 // JSON: codec, width, height and description (an avcC, base64), before a keyframe when they change
	kindFrame  = 2 // a flags byte (1: a keyframe), a big-endian uint64 timestamp (µs since 1970), an access unit in AVCC
	kindEvent  = 3 // JSON: {"screen":{"width","height"}} once it found the screen, {"input":"dtuhid"} once input is ready, {"error":"…"}
)

const maxFrame = 32 << 20

// Proc is a running simhost: what it reads, what it writes, and how to stop it.
type Proc struct {
	Stdin  io.WriteCloser
	Stdout io.Reader
	// Stop kills it and waits for it, saying how it ended.
	Stop func() error
	// Done is closed when it has exited.
	Done <-chan struct{}
}

// Launcher starts simhost for a simulator. Tests give it a fake.
type Launcher func(ctx context.Context, udid string) (*Proc, error)

type viewer struct {
	// out are devicestream messages, each a binary WebSocket message.
	out chan []byte
	// keyed is false until the viewer has had a keyframe: until then, frames
	// that depend on earlier ones are no use to it.
	keyed bool
}

// host is one simulator's simhost, shared by everyone using that simulator.
type host struct {
	udid string
	proc *Proc

	writeMu sync.Mutex

	mu      sync.Mutex
	config  []byte // the last config message, for viewers that join later
	width   int    // the screen, in pixels, once simhost has said
	height  int
	screen  chan struct{} // closed once the size is known
	lastErr string
	viewers map[*viewer]bool
	used    time.Time
}

func newHost(udid string, proc *Proc) *host {
	h := &host{udid: udid, proc: proc, screen: make(chan struct{}), viewers: map[*viewer]bool{}, used: time.Now()}
	go h.read()
	return h
}

func (h *host) alive() bool {
	select {
	case <-h.proc.Done:
		return false
	default:
		return true
	}
}

func (h *host) send(o op) error {
	data, err := json.Marshal(o)
	if err != nil {
		return err
	}
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	h.touch()
	if _, err := h.proc.Stdin.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("the simulator's helper stopped: %s", h.why())
	}
	return nil
}

func (h *host) touch() {
	h.mu.Lock()
	h.used = time.Now()
	h.mu.Unlock()
}

func (h *host) why() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.lastErr != "" {
		return h.lastErr
	}
	return "it exited"
}

// run sends steps in order, waiting between them as each says.
func (h *host) run(ctx context.Context, steps []step) error {
	for _, s := range steps {
		if err := h.send(s.op); err != nil {
			return err
		}
		if s.wait > 0 {
			select {
			case <-time.After(s.wait):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return nil
}

// size is the screen's size in pixels, waiting for simhost to find it.
func (h *host) size(ctx context.Context) (int, int, error) {
	select {
	case <-h.screen:
	case <-h.proc.Done:
		return 0, 0, fmt.Errorf("the simulator's helper stopped: %s", h.why())
	case <-ctx.Done():
		return 0, 0, ctx.Err()
	case <-time.After(10 * time.Second):
		return 0, 0, errors.New("the simulator's screen wasn't found: is it booted?")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.width, h.height, nil
}

func (h *host) read() {
	r := bufio.NewReaderSize(h.proc.Stdout, 1<<16)
	var head [5]byte
	for {
		if _, err := io.ReadFull(r, head[:]); err != nil {
			return
		}
		n := binary.BigEndian.Uint32(head[:4])
		if n < 1 || n > maxFrame {
			h.fail(fmt.Sprintf("simhost wrote a frame of %d bytes", n))
			return
		}
		payload := make([]byte, n-1)
		if _, err := io.ReadFull(r, payload); err != nil {
			return
		}
		switch head[4] {
		case kindConfig:
			msg, err := configMessage(payload)
			if err != nil {
				h.fail(err.Error())
				return
			}
			h.mu.Lock()
			h.config = msg
			h.mu.Unlock()
			h.broadcast(msg)
		case kindFrame:
			h.frame(payload)
		case kindEvent:
			h.event(payload)
		}
	}
}

func (h *host) fail(msg string) {
	h.mu.Lock()
	h.lastErr = msg
	h.mu.Unlock()
	_ = h.proc.Stop()
}

// configMessage is simhost's config as the viewer gets it: devicestream's
// config message.
func configMessage(payload []byte) ([]byte, error) {
	var c devicestream.Config
	if err := json.Unmarshal(payload, &c); err != nil || c.Codec == "" || len(c.Description) == 0 {
		return nil, fmt.Errorf("simhost wrote a config that isn't one: %s", payload)
	}
	c.Platform = "ios"
	return devicestream.EncodeConfig(c), nil
}

func (h *host) event(payload []byte) {
	var e struct {
		Screen *struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"screen"`
		Error string `json:"error"`
	}
	if json.Unmarshal(payload, &e) != nil {
		return
	}
	if e.Screen != nil && e.Screen.Width > 0 && e.Screen.Height > 0 {
		h.mu.Lock()
		h.width, h.height = e.Screen.Width, e.Screen.Height
		select {
		case <-h.screen:
		default:
			close(h.screen)
		}
		h.mu.Unlock()
	}
	// Not the viewers': an input that didn't go through is no reason to stop
	// watching. Its error stays, for when simhost stops.
	if e.Error != "" {
		h.mu.Lock()
		h.lastErr = e.Error
		h.mu.Unlock()
	}
}

// frame hands a frame to every viewer that can use it. A viewer too slow to
// take one misses frames until the next keyframe, which it asks for, rather
// than holding the others back.
func (h *host) frame(payload []byte) {
	if len(payload) < 9 {
		return
	}
	key := payload[0]&1 != 0
	msg := devicestream.EncodeFrame(devicestream.Packet{Key: key, At: time.UnixMicro(int64(binary.BigEndian.Uint64(payload[1:9]))), Data: payload[9:]})
	h.mu.Lock()
	defer h.mu.Unlock()
	behind := false
	for v := range h.viewers {
		if !v.keyed && !key {
			continue
		}
		select {
		case v.out <- msg:
			v.keyed = true
		default:
			v.keyed = false
			behind = true
		}
	}
	if behind {
		go func() { _ = h.send(op{Op: "keyframe"}) }()
	}
}

func (h *host) broadcast(m []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for v := range h.viewers {
		select {
		case v.out <- m:
		default:
		}
	}
}

// watch adds a viewer: it gets the stream's config, if there is one yet,
// then frames from the next keyframe on. The first viewer starts the
// capture, and leave stops it after the last.
func (h *host) watch() (v *viewer, leave func(), err error) {
	v = &viewer{out: make(chan []byte, 64)}
	h.mu.Lock()
	first := len(h.viewers) == 0
	h.viewers[v] = true
	if h.config != nil {
		v.out <- h.config
	}
	h.mu.Unlock()
	if first {
		err = h.send(op{Op: "stream", On: true})
	} else {
		err = h.send(op{Op: "keyframe"})
	}
	leave = func() {
		h.mu.Lock()
		delete(h.viewers, v)
		last := len(h.viewers) == 0
		h.mu.Unlock()
		if last {
			_ = h.send(op{Op: "stream"})
		}
	}
	return v, leave, err
}

func (h *host) idle(after time.Duration) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.viewers) == 0 && time.Since(h.used) > after
}

// hosts are the simulators' simhosts, started when one is first used and
// stopped once nothing has used it for a while.
type hosts struct {
	launch Launcher
	idle   time.Duration

	mu sync.Mutex
	m  map[string]*host
}

func newHosts(launch Launcher, idle time.Duration) *hosts {
	return &hosts{launch: launch, idle: idle, m: map[string]*host{}}
}

func (hs *hosts) get(ctx context.Context, udid string) (*host, error) {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	if h := hs.m[udid]; h != nil && h.alive() {
		return h, nil
	}
	proc, err := hs.launch(ctx, udid)
	if err != nil {
		return nil, err
	}
	h := newHost(udid, proc)
	hs.m[udid] = h
	return h, nil
}

// stop stops a simulator's simhost, as it shuts down.
func (hs *hosts) stop(udid string) {
	hs.mu.Lock()
	h := hs.m[udid]
	delete(hs.m, udid)
	hs.mu.Unlock()
	if h != nil {
		_ = h.proc.Stop()
	}
}

// reap stops the simhosts nothing used lately, every so often until ctx ends,
// and all of them then.
func (hs *hosts) reap(ctx context.Context) {
	tick := time.NewTicker(max(hs.idle/4, time.Second))
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			hs.mu.Lock()
			all := hs.m
			hs.m = map[string]*host{}
			hs.mu.Unlock()
			for _, h := range all {
				_ = h.proc.Stop()
			}
			return
		case <-tick.C:
		}
		hs.mu.Lock()
		var idle []*host
		for udid, h := range hs.m {
			if !h.alive() || h.idle(hs.idle) {
				idle = append(idle, h)
				delete(hs.m, udid)
			}
		}
		hs.mu.Unlock()
		for _, h := range idle {
			_ = h.proc.Stop()
		}
	}
}
