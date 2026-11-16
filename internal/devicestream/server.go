package devicestream

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Frame is one image of the screen, RGB24, row after row, upright as shown.
type Frame struct {
	Width, Height int
	Pixels        []byte
	At            time.Time
}

// Device is what is particular to one kind of device.
type Device interface {
	// Platform names it in config messages: "android", "ios".
	Platform() string
	// Screen calls fn with the screen as it is, then each time it changes,
	// until ctx ends or fn fails.
	Screen(ctx context.Context, fn func(Frame) error) error
	// Input passes a viewer's touch, key, text, wheel or button into the
	// device. x and y are from 0 to 1 across the last frame Screen gave.
	Input(ctx context.Context, in Input) error
}

// Serve serves a viewer's WebSocket: the device's screen as video, and its
// input into the device. Each viewer gets its own encoder, so the first
// frame it gets is a keyframe.
func Serve(w http.ResponseWriter, r *http.Request, dev Device, logf func(string, ...any)) {
	// Only the daemon reaches this, through the agent's proxy device.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(1 << 20)
	err = (&session{dev: dev, conn: conn, logf: logf, restart: make(chan struct{}, 1), frames: make(chan struct{}, 1)}).run(r.Context())
	if err != nil {
		_ = conn.Close(websocket.StatusInternalError, "")
		return
	}
	_ = conn.Close(websocket.StatusNormalClosure, "")
}

type session struct {
	dev  Device
	conn *websocket.Conn
	logf func(string, ...any)

	mu     sync.Mutex
	latest *Frame // the newest frame, not yet encoded
	last   *Frame // the frame last encoded, to start over from
	frames chan struct{}

	restart chan struct{}
}

// run streams until the viewer leaves, or until the device or the encoder
// fails, which it tells the viewer about. It returns only that failure.
func (s *session) run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, viewer := make(chan error, 2), make(chan error, 1)
	go func() { stream <- s.screen(ctx) }()
	go func() { stream <- s.encode(ctx) }()
	go func() { viewer <- s.input(ctx) }()
	select {
	case <-viewer:
		return nil
	case err := <-stream:
		if ctx.Err() != nil {
			return nil
		}
		// Before anything is canceled: a canceled read closes the WebSocket.
		s.logf("device stream: %v", err)
		write, done := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		_ = s.conn.Write(write, websocket.MessageBinary, EncodeStatus(err))
		done()
		return err
	}
}

// screen keeps the newest frame for encode, which takes them as fast as it
// can: when it's slower than the screen, frames in between are skipped.
func (s *session) screen(ctx context.Context) error {
	err := s.dev.Screen(ctx, func(f Frame) error {
		s.mu.Lock()
		s.latest = &f
		s.mu.Unlock()
		select {
		case s.frames <- struct{}{}:
		default:
		}
		return nil
	})
	if err == nil {
		err = errors.New("the device stopped sending its screen")
	}
	return err
}

// encode feeds frames to an encoder, starting a new one for a new size or
// when the viewer asks for a keyframe, and sends what comes out.
func (s *session) encode(ctx context.Context) error {
	var enc *Encoder
	var done chan error
	var buf []byte
	defer func() {
		if enc != nil {
			enc.Close()
		}
	}()
	for {
		var frame *Frame
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-done:
			return err
		case <-s.frames:
			s.mu.Lock()
			frame, s.latest = s.latest, nil
			s.mu.Unlock()
		case <-s.restart:
			s.mu.Lock()
			frame = s.latest
			if frame == nil {
				frame = s.last
			}
			s.latest = nil
			s.mu.Unlock()
			if enc != nil {
				enc.Close()
				<-done
				enc = nil
			}
		}
		if frame == nil {
			continue
		}
		width, height := evenSize(frame.Width), evenSize(frame.Height)
		if enc != nil && (enc.Width != width || enc.Height != height) {
			enc.Close()
			<-done
			enc = nil
		}
		if enc == nil {
			var err error
			if enc, err = StartEncoder(ctx, width, height); err != nil {
				return err
			}
			done = make(chan error, 1)
			go func(enc *Encoder, done chan<- error) { done <- s.send(ctx, enc) }(enc, done)
		}
		if err := enc.Encode(Pixels(*frame, width, height, &buf), frame.At); err != nil {
			select {
			case err := <-done: // ffmpeg's own reason
				return err
			case <-time.After(time.Second):
				return err
			}
		}
		s.mu.Lock()
		s.last = frame
		s.mu.Unlock()
	}
}

// send sends what enc encodes to the viewer, until enc is closed.
func (s *session) send(ctx context.Context, enc *Encoder) error {
	for {
		avcC, p, err := enc.Next()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		var msg []byte
		if avcC != nil {
			codec, err := CodecString(avcC)
			if err != nil {
				return err
			}
			msg = EncodeConfig(Config{Platform: s.dev.Platform(), Codec: codec, Width: enc.Width, Height: enc.Height, Description: avcC})
		} else {
			msg = EncodeFrame(p)
		}
		if err := s.conn.Write(ctx, websocket.MessageBinary, msg); err != nil {
			return err
		}
	}
}

func (s *session) input(ctx context.Context) error {
	for {
		typ, data, err := s.conn.Read(ctx)
		if err != nil {
			return err
		}
		if typ != websocket.MessageText {
			continue
		}
		in, err := DecodeInput(data)
		if err != nil {
			s.logf("device stream: %v", err)
			continue
		}
		if in.Type == InputKeyframe {
			select {
			case s.restart <- struct{}{}:
			default:
			}
			continue
		}
		if err := s.dev.Input(ctx, in); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// A key the device doesn't know is no reason to stop watching.
			s.logf("device stream: %s: %v", in.Type, err)
		}
	}
}

// Point maps x, y from 0 to 1 across a width x height screen to its pixels.
func Point(x, y float64, width, height int) (int, int) {
	clamp := func(v float64, n int) int {
		p := int(v * float64(n))
		return max(0, min(n-1, p))
	}
	return clamp(x, width), clamp(y, height)
}
