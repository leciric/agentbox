package desktop

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Record makes a recording in one pass: ffmpeg encodes what it captures as it
// captures it, with the input drawn on as it happens, straight into the file
// the recording is kept as. It captures one of two things:
//
//   - The display (RecordDisplay), with x11grab: anything on it, a desktop or
//     an Electron app, and the real cursor.
//   - The browser's page (RecordBrowser), with the DevTools protocol's
//     screencast: just the page, at its own size, in frames Chromium already
//     has, sent only when it changes. The cursor is drawn on, since the page
//     has none.
//
// RecordAuto takes the page when the browser is all there is to see, and the
// display otherwise.

const (
	RecordAuto    = "auto"
	RecordDisplay = "display"
	RecordBrowser = "browser"
)

// FrameRate is a recording's: enough for a UI being clicked through, and
// little enough for a software encoder to keep up with beside other agents.
const FrameRate = 15

// EncodeArgs are what a recording is encoded with: an MP4 every Chromium
// plays and can show the first frame of before it has the rest, which is what
// the Media grid's thumbnails are. That means 4:2:0 (x11grab's frames are RGB,
// which libx264 would otherwise keep as High 4:4:4, which Chromium can't
// decode) and the index at the front (+faststart), where the app reads it
// with its first range request. EvenSize goes in front of it, since 4:2:0
// needs an even width and height. TestRecordingsPlayInChromium encodes with
// these and checks both.
var EncodeArgs = []string{"-c:v", "libx264", "-preset", "veryfast", "-crf", "28", "-pix_fmt", "yuv420p", "-movflags", "+faststart"}

const EvenSize = "scale=trunc(iw/2)*2:trunc(ih/2)*2"

type RecordOptions struct {
	Target string // RecordAuto, RecordDisplay or RecordBrowser
	// Input draws the keys and clicks on the display, and the cursor on a
	// browser recording, when it is "desktop": a flow driven through X.
	// Playwright's input happens inside Chromium, where X sees none of it.
	Input string
	Out   string
	Limit time.Duration
	// Bottom is how far above the display's bottom edge the key caption's
	// centre sits: the middle of the dock.
	Bottom   int
	DevTools string // DevTools by default
	// Started is called with what is being recorded once the first frames
	// are on their way to the file.
	Started func(target string)
	// Log gets ffmpeg's errors.
	Log io.Writer
}

// Record records until ctx ends or opts.Limit passes, and leaves the file
// finished either way.
func Record(ctx context.Context, opts RecordOptions) error {
	if opts.DevTools == "" {
		opts.DevTools = DevTools
	}
	if opts.Log == nil {
		opts.Log = io.Discard
	}
	if opts.Limit <= 0 {
		opts.Limit = 10 * time.Minute
	}
	target := opts.Target
	if target == "" || target == RecordAuto {
		target = chooseTarget(ctx, opts.Input, opts.DevTools)
	}
	switch target {
	case RecordDisplay:
		return recordDisplay(ctx, opts)
	case RecordBrowser:
		return recordBrowser(ctx, opts)
	}
	return fmt.Errorf("unknown recording target %q: use auto, display or browser", target)
}

// chooseTarget records the browser's page when there is one and the flow is
// in it: Playwright's always is, and an X-driven one is when Chromium is the
// only window on the display. Anything else, an Electron app, a terminal or a
// dialog of the desktop's, needs the display.
func chooseTarget(ctx context.Context, input, devtools string) string {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	pages, err := listPages(ctx, devtools)
	if err != nil || len(pages) == 0 {
		return RecordDisplay
	}
	if input != "desktop" {
		return RecordBrowser
	}
	windows, err := listWindows(ctx)
	if err != nil || !browserOnly(windows) {
		return RecordDisplay
	}
	return RecordBrowser
}

// window is one of the window manager's clients, as xprop describes it.
type window struct {
	class, kind, state string
}

func listWindows(ctx context.Context) ([]window, error) {
	xprop := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "xprop", args...)
		cmd.Env = append(os.Environ(), "DISPLAY="+Display)
		out, err := cmd.Output()
		return string(out), err
	}
	list, err := xprop("-root", "_NET_CLIENT_LIST")
	if err != nil {
		return nil, err
	}
	_, ids, _ := strings.Cut(list, "#")
	var windows []window
	for _, id := range strings.Split(ids, ",") {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		props, err := xprop("-id", id, "WM_CLASS", "_NET_WM_WINDOW_TYPE", "_NET_WM_STATE")
		if err != nil {
			continue // gone since the list
		}
		windows = append(windows, parseWindow(props))
	}
	return windows, nil
}

func parseWindow(props string) window {
	var w window
	for _, line := range strings.Split(props, "\n") {
		name, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch {
		case strings.HasPrefix(name, "WM_CLASS"):
			w.class = strings.TrimSpace(value)
		case strings.HasPrefix(name, "_NET_WM_WINDOW_TYPE"):
			w.kind = strings.TrimSpace(value)
		case strings.HasPrefix(name, "_NET_WM_STATE"):
			w.state = strings.TrimSpace(value)
		}
	}
	return w
}

// browserOnly is whether Chromium's windows are all there is to see: the dock
// and the desktop don't count, nor does a minimised window.
func browserOnly(windows []window) bool {
	browser := false
	for _, w := range windows {
		if strings.Contains(w.kind, "_DOCK") || strings.Contains(w.kind, "_DESKTOP") || strings.Contains(w.state, "_HIDDEN") {
			continue
		}
		// "chromium (<its profile>)", "Chromium" with --user-data-dir.
		if !strings.HasPrefix(strings.ToLower(w.class), `"chromium`) {
			return false
		}
		browser = true
	}
	return browser
}

// recordDisplay captures the display with x11grab, with the input drawn on
// when there is input to draw.
func recordDisplay(ctx context.Context, opts RecordOptions) error {
	if _, err := os.Stat("/tmp/.X11-unix/X" + strings.TrimPrefix(Display, ":")); err != nil {
		return errors.New("the display isn't running: start the browser first")
	}
	// draw_mouse is x11grab's default, and Xvnc serves the pointer through
	// XFIXES, so the cursor lands in the frames; it is spelled out here
	// because the whole point of desktop input is seeing it.
	grab := []string{"-f", "x11grab", "-draw_mouse", "1", "-framerate", strconv.Itoa(FrameRate), "-i", Display}
	if opts.Input != "desktop" {
		return runFFmpeg(ctx, opts, RecordDisplay, ffmpegArgs(grab, EvenSize, opts), nil, nil)
	}
	x, err := dialX(Display)
	if err != nil {
		return err
	}
	defer func() { _ = x.Close() }()
	size := image.Pt(x.width, x.height)
	if size.X == 0 || size.Y == 0 {
		return errors.New("the display didn't say how big it is")
	}
	ov, err := newOverlay(opts.Bottom, false)
	if err != nil {
		return err
	}
	inputCtx, stopInput := context.WithCancel(ctx)
	defer stopInput()
	go func() { _ = watchInput(inputCtx, x, ov.add, nil) }()
	return runFFmpeg(ctx, opts, RecordDisplay, captureArgs(grab, size, opts), nil, composite(ov, size, opts))
}

// recordBrowser captures the browser's page with a screencast. Chromium sends
// a frame only when the page changes, so the last one is sent again in
// between, at the frame rate: the overlay over it keeps moving while the page
// stands still, and the video keeps time.
func recordBrowser(ctx context.Context, opts RecordOptions) error {
	sc := &screencast{devtools: opts.DevTools, cursor: opts.Input == "desktop", fresh: make(chan struct{}, 1)}
	if err := sc.follow(ctx); err != nil {
		return err
	}
	defer sc.close()
	select {
	case <-sc.fresh:
	case <-time.After(5 * time.Second):
		return errors.New("the browser sent no frames of its page")
	case <-ctx.Done():
		return ctx.Err()
	}
	first, _ := sc.latest()
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(first))
	if err != nil {
		return fmt.Errorf("reading the browser's frame: %w", err)
	}
	// Probing the pipe would hold the recording back for ffmpeg's default
	// five seconds: the first frame says all there is to know.
	pageInput := []string{"-f", "image2pipe", "-c:v", "mjpeg", "-probesize", "32", "-analyzeduration", "0", "-framerate", strconv.Itoa(FrameRate), "-use_wallclock_as_timestamps", "1", "-i", "pipe:3"}
	pageFrames := func(ctx context.Context, w io.Writer) error {
		return everyFrame(ctx, func() error {
			frame, _ := sc.latest()
			_, err := w.Write(frame)
			return err
		})
	}
	// The video keeps the first frame's size, even, whatever the page is
	// resized to: an encoder can't change it midway.
	size := image.Pt(cfg.Width&^1, cfg.Height&^1)
	if !sc.cursor {
		return runFFmpeg(ctx, opts, RecordBrowser, ffmpegArgs(pageInput, fmt.Sprintf("scale=%d:%d", size.X, size.Y), opts), pageFrames, nil)
	}
	ov, err := newOverlay(0, true)
	if err != nil {
		return err
	}
	sc.mu.Lock()
	sc.ov, sc.size = ov, size
	sc.mu.Unlock()
	sc.place(ctx)
	x, err := dialX(Display)
	if err != nil {
		return err
	}
	defer func() { _ = x.Close() }()
	inputCtx, stopInput := context.WithCancel(ctx)
	defer stopInput()
	go func() { _ = watchInput(inputCtx, x, ov.add, ov.move) }()
	return runFFmpeg(ctx, opts, RecordBrowser, captureArgs(pageInput, size, opts), pageFrames, composite(ov, size, opts))
}

// ffmpegArgs are the whole command line after "ffmpeg" for a recording with
// nothing to draw on: the input, filtered with vf, and the encoding.
func ffmpegArgs(input []string, vf string, opts RecordOptions) []string {
	args := append([]string{"-hide_banner", "-loglevel", "error", "-nostdin"}, input...)
	args = append(args, "-vf", vf, "-t", strconv.Itoa(int(opts.Limit.Seconds())))
	args = append(args, EncodeArgs...)
	return append(args, "-y", opts.Out)
}

// A recording with input to draw on goes through the recorder: ffmpeg hands
// it raw frames at a steady frame rate (captureArgs), it draws the overlay
// onto each, and a second ffmpeg encodes them (composite). Drawing is done
// here rather than with ffmpeg's overlay filter, which blends and converts
// every pixel of every frame, which tripled the CPU a recording takes for
// what is, most of the time, nothing at all: the recorder only touches the
// few rectangles the overlay drew on.

// captureArgs has ffmpeg write input's frames to its stdout, size, in BGR0,
// FrameRate of them a second.
func captureArgs(input []string, size image.Point, opts RecordOptions) []string {
	args := append([]string{"-hide_banner", "-loglevel", "error", "-nostdin"}, input...)
	return append(args, "-vf", fmt.Sprintf("scale=%d:%d", size.X, size.Y), "-fps_mode", "cfr", "-r", strconv.Itoa(FrameRate),
		"-t", strconv.Itoa(int(opts.Limit.Seconds())), "-pix_fmt", "bgr0", "-f", "rawvideo", "pipe:1")
}

// compositor is the stage between the capture and the encoder.
type compositor struct {
	args   []string // the encoder's
	frames func(r io.Reader, w io.Writer) error
}

func composite(ov *overlay, size image.Point, opts RecordOptions) *compositor {
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin",
		"-f", "rawvideo", "-pixel_format", "bgr0", "-video_size", fmt.Sprintf("%dx%d", size.X, size.Y), "-framerate", strconv.Itoa(FrameRate), "-i", "pipe:0",
		"-vf", EvenSize}
	args = append(append(args, EncodeArgs...), "-y", opts.Out)
	return &compositor{args: args, frames: func(r io.Reader, w io.Writer) error {
		frame := make([]byte, size.X*size.Y*4)
		canvas := image.NewRGBA(image.Rectangle{Max: size})
		var drawn []image.Rectangle
		for {
			if _, err := io.ReadFull(r, frame); err != nil {
				if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
					return nil
				}
				return err
			}
			for _, rect := range drawn {
				clearRect(canvas, rect)
			}
			// The frame was taken a moment ago, the time ffmpeg took to
			// hand it over: a few milliseconds, well inside one frame.
			drawn = mergeRects(ov.draw(canvas, float64(time.Now().UnixMicro())/1e6))
			for _, rect := range drawn {
				blend(frame, size.X*4, canvas, rect)
			}
			if _, err := w.Write(frame); err != nil {
				return err
			}
		}
	}}
}

// mergeRects joins overlapping rectangles, so that no pixel is blended twice.
func mergeRects(rects []image.Rectangle) []image.Rectangle {
	for i := 0; i < len(rects); i++ {
		for j := i + 1; j < len(rects); j++ {
			if rects[i].Overlaps(rects[j]) {
				rects[i] = rects[i].Union(rects[j])
				rects = append(rects[:j], rects[j+1:]...)
				j = i // the larger one may now overlap one passed over
			}
		}
	}
	return rects
}

// blend draws canvas, premultiplied RGBA, over frame, BGR0 with stride bytes
// a row, inside r.
func blend(frame []byte, stride int, canvas *image.RGBA, r image.Rectangle) {
	r = r.Intersect(canvas.Rect)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		src := canvas.Pix[canvas.PixOffset(r.Min.X, y):canvas.PixOffset(r.Max.X, y)]
		dst := frame[y*stride+r.Min.X*4:]
		for i := 0; i+3 < len(src); i += 4 {
			a := uint32(src[i+3])
			if a == 0 {
				continue
			}
			k := 255 - a
			dst[i] = byte(uint32(src[i+2]) + (uint32(dst[i])*k+127)/255)
			dst[i+1] = byte(uint32(src[i+1]) + (uint32(dst[i+1])*k+127)/255)
			dst[i+2] = byte(uint32(src[i]) + (uint32(dst[i+2])*k+127)/255)
		}
	}
}

// A feed writes one of ffmpeg's piped inputs until ctx ends.
type feed func(ctx context.Context, w io.Writer) error

// runFFmpeg runs ffmpeg with args until ctx ends, then has it finish the file.
// main, when it isn't nil, feeds its pipe:3. With post, args only capture,
// and post's encoder makes the file of what post.frames makes of them; target
// is what it records, for opts.Started.
func runFFmpeg(ctx context.Context, opts RecordOptions, target string, args []string, main feed, post *compositor) error {
	cmd := exec.Command("ffmpeg", args...)
	cmd.Stderr = opts.Log
	var feedR, feedW *os.File
	if main != nil {
		var err error
		if feedR, feedW, err = os.Pipe(); err != nil {
			return err
		}
		cmd.ExtraFiles = []*os.File{feedR}
	}
	var enc *exec.Cmd
	var rawR, rawW, encR, encW *os.File
	if post != nil {
		var err error
		if rawR, rawW, err = os.Pipe(); err != nil {
			return err
		}
		if encR, encW, err = os.Pipe(); err != nil {
			return err
		}
		cmd.Stdout = rawW
		enc = exec.Command("ffmpeg", post.args...)
		enc.Stdin, enc.Stderr = encR, opts.Log
		if err := enc.Start(); err != nil {
			return fmt.Errorf("starting ffmpeg: %w", err)
		}
		_ = encR.Close()
	}
	if err := cmd.Start(); err != nil {
		if feedR != nil {
			_, _ = feedR.Close(), feedW.Close()
		}
		if enc != nil {
			_ = encW.Close()
			_ = enc.Wait()
		}
		return fmt.Errorf("starting ffmpeg: %w", err)
	}
	if rawW != nil {
		_ = rawW.Close()
	}
	// ffmpeg has its own copy of the feed's read end: with this one closed,
	// a feed whose ffmpeg has stopped fails, rather than blocking on a full
	// pipe forever.
	if feedR != nil {
		_ = feedR.Close()
	}

	// exited is the whole chain's: the capture's, and then, once the frames
	// it left are through, the encoder's, which makes the file.
	exited := make(chan error, 1)
	frames := make(chan error, 1)
	if enc != nil {
		go func() {
			frames <- post.frames(rawR, encW)
			_ = encW.Close()
			// A capture still running, should the encoder have stopped
			// first, isn't left blocked on a pipe nobody reads.
			_ = rawR.Close()
		}()
	}
	go func() {
		err := cmd.Wait()
		if enc == nil {
			exited <- err
			return
		}
		encErr := enc.Wait()
		// An encoder that stops first leaves the frames blocked on a pipe
		// nobody reads: closing it ends them.
		_ = rawR.Close()
		<-frames
		if encErr != nil {
			exited <- encErr
			return
		}
		exited <- err
	}()

	feedCtx, stopFeeds := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	if main != nil {
		wg.Go(func() {
			defer func() { _ = feedW.Close() }()
			_ = main(feedCtx, feedW)
		})
	}
	stop := func() {
		stopFeeds()
		wg.Wait()
	}
	kill := func() {
		_ = cmd.Process.Kill()
		if enc != nil {
			_ = enc.Process.Kill()
		}
	}

	// ffmpeg creates the file once it has opened its inputs and the encoder,
	// about 150 ms in.
	for {
		if _, err := os.Stat(opts.Out); err == nil {
			break
		}
		select {
		case err := <-exited:
			stop()
			return fmt.Errorf("ffmpeg stopped: %v", err)
		case <-ctx.Done():
			kill()
			stop()
			<-exited
			return errors.New("stopped before ffmpeg started")
		case <-time.After(30 * time.Millisecond):
		}
	}
	if opts.Started != nil {
		opts.Started(target)
	}

	select {
	case err := <-exited:
		stop()
		return finished(err)
	case <-ctx.Done():
	}
	// Told to stop, ffmpeg finishes the file; the feeds end so that it isn't
	// left waiting on a pipe.
	_ = cmd.Process.Signal(syscall.SIGINT)
	stop()
	select {
	case err := <-exited:
		return finished(err)
	case <-time.After(20 * time.Second):
		kill()
		return errors.New("ffmpeg didn't finish the file")
	}
}

// finished is nil for an ffmpeg that stopped because it was told to, or at
// its limit.
func finished(err error) error {
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 255 {
		return nil // what it exits with after an interrupt
	}
	if err != nil {
		return fmt.Errorf("ffmpeg: %w", err)
	}
	return nil
}

// everyFrame calls fn at the frame rate until ctx ends or fn fails. A frame
// that comes late, behind a slow ffmpeg, is skipped rather than caught up on.
func everyFrame(ctx context.Context, fn func() error) error {
	tick := time.NewTicker(time.Second / FrameRate)
	defer tick.Stop()
	for {
		if err := fn(); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}

func clearRect(img *image.RGBA, r image.Rectangle) {
	r = r.Intersect(img.Rect)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		row := img.Pix[img.PixOffset(r.Min.X, y):img.PixOffset(r.Max.X, y)]
		clear(row)
	}
}

// screencast follows the browser's tab on screen, keeping the last frame of
// it, and where its page is on the display for the overlay.
type screencast struct {
	devtools string
	cursor   bool
	ov       *overlay
	size     image.Point

	mu     sync.Mutex
	conn   *cdpConn
	page   string
	frame  []byte
	width  float64 // the page's, in CSS pixels, from the frame's metadata
	cancel context.CancelFunc
	fresh  chan struct{}
}

func (s *screencast) latest() ([]byte, float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.frame, s.width
}

// follow starts the screencast of the tab on screen, and keeps switching it
// to whichever tab is.
func (s *screencast) follow(ctx context.Context) error {
	if err := s.attach(ctx); err != nil {
		return err
	}
	ctx, s.cancel = context.WithCancel(ctx)
	go func() {
		tick := time.NewTicker(500 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
			_ = s.attach(ctx)
			s.place(ctx)
		}
	}()
	return nil
}

// attach screencasts the tab on screen, unless it already is.
func (s *screencast) attach(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	pages, err := listPages(ctx, s.devtools)
	if err != nil {
		return fmt.Errorf("the browser isn't running: %w", err)
	}
	if len(pages) == 0 {
		return errors.New("the browser has no page open")
	}
	s.mu.Lock()
	same := pages[0].ID == s.page
	s.mu.Unlock()
	if same {
		return nil
	}
	var conn *cdpConn
	conn, err = dialCDP(ctx, pages[0].WebSocket, func(method string, params json.RawMessage) {
		if method != "Page.screencastFrame" {
			return
		}
		var f struct {
			Data      string `json:"data"`
			SessionID int    `json:"sessionId"`
			Metadata  struct {
				DeviceWidth float64 `json:"deviceWidth"`
			} `json:"metadata"`
		}
		if json.Unmarshal(params, &f) != nil {
			return
		}
		// Chromium sends the next frame once this one is acknowledged.
		conn.send(context.Background(), "Page.screencastFrameAck", map[string]any{"sessionId": f.SessionID})
		frame, err := base64.StdEncoding.DecodeString(f.Data)
		if err != nil {
			return
		}
		s.mu.Lock()
		s.frame, s.width = frame, f.Metadata.DeviceWidth
		s.mu.Unlock()
		select {
		case s.fresh <- struct{}{}:
		default:
		}
	})
	if err != nil {
		return fmt.Errorf("connecting to the browser: %w", err)
	}
	if err := conn.call(ctx, "Page.startScreencast", map[string]any{"format": "jpeg", "quality": 80, "everyNthFrame": 1}, nil); err != nil {
		conn.Close()
		return err
	}
	s.mu.Lock()
	old := s.conn
	s.conn, s.page = conn, pages[0].ID
	s.mu.Unlock()
	if old != nil {
		old.Close()
	}
	s.place(ctx)
	return nil
}

// originScript keeps where the page's top-left corner is on the screen, in
// CSS pixels: exactly, from the pointer's events, once it has moved over the
// page, and from the window's size until then, which assumes the browser's
// own bar is all there is above the page.
const originScript = `(() => {
  if (!window.__agentboxOrigin) {
    window.__agentboxOrigin = [screenX + (outerWidth - innerWidth) / 2, screenY + outerHeight - innerHeight];
    addEventListener("mousemove", (e) => { window.__agentboxOrigin = [e.screenX - e.clientX, e.screenY - e.clientY]; }, { capture: true, passive: true });
  }
  return [...window.__agentboxOrigin, devicePixelRatio];
})()`

// place tells the overlay where the page is on the display.
func (s *screencast) place(ctx context.Context) {
	s.mu.Lock()
	conn, ov, width := s.conn, s.ov, s.width
	s.mu.Unlock()
	if ov == nil || conn == nil {
		return
	}
	var res struct {
		Result struct {
			Value []float64 `json:"value"`
		} `json:"result"`
	}
	if conn.call(ctx, "Runtime.evaluate", map[string]any{"expression": originScript, "returnByValue": true}, &res) != nil || len(res.Result.Value) != 3 {
		return
	}
	v := res.Result.Value
	dpr := max(v[2], 0.1)
	scale := 1.0
	if width > 0 {
		scale = float64(s.size.X) / (width * dpr)
	}
	ov.place(image.Pt(int(v[0]*dpr), int(v[1]*dpr)), scale)
}

func (s *screencast) close() {
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil {
		s.conn.Close()
	}
}
