// Package desktop drives an agent's virtual display: the mouse, the keyboard,
// the windows on it and screenshots of the whole screen. It is served to the
// agent's own AI tool over the Model Context Protocol (`agentbox desktop mcp`),
// alongside the Playwright MCP server, which only reaches inside Chromium's
// pages (D64).
//
// Everything runs against DISPLAY=:99, the display browser.sh starts, through
// xdotool and ffmpeg. Both are in the base image.
package desktop

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	// Display is the agent's display, the one browser.sh starts.
	Display = ":99"
	// displaySocket is what proves it is running, the same file
	// displayScreenshot checks before grabbing the screen.
	displaySocket = "/tmp/.X11-unix/X99"
	// notRunning is what every tool says when the display is down. It names
	// the command that fixes it, because "no display" alone leaves an agent
	// guessing at an X problem it doesn't have.
	notRunning = "the display isn't running: start the browser first: agentbox browser start"

	// maxWidth is how wide a screenshot may be before it is scaled down. A
	// 1440×900 display is a large image to put in a model's context every
	// time it looks at the screen, and it doesn't need the pixels.
	// 1024 wide is about 900 tokens an image rather than 1,400 at 1280, and
	// still reads a UI's smallest text.
	maxWidth = 1024
	// typeDelay is the gap between keystrokes, in milliseconds. xdotool's own
	// default is 12 ms, which some toolkits drop characters at.
	typeDelay = 25
	// maxWait is the longest wait a tool will do. Waiting is for letting a
	// window appear, not for sleeping through a build.
	maxWait = 5 * time.Second

	// commandTimeout bounds one xdotool or ffmpeg run, so a tool call can't
	// hang the agent's AI tool waiting on the display.
	commandTimeout = 20 * time.Second

	// moveStepPx is roughly how many pixels apart two points of an animated
	// move are, so a short hop takes few steps and a long one takes more.
	moveStepPx = 20
	// maxMoveSteps caps how many xdotool mousemove calls one animated move
	// chains, so a move across a 4K display doesn't spawn an enormous line.
	maxMoveSteps = 40
	// moveDuration is roughly how long one animated move takes, start to
	// finish, regardless of distance: quick enough that a click still feels
	// immediate, slow enough that a recording shows the pointer travelling.
	moveDuration = 300 * time.Millisecond
)

// Running reports whether the agent's display is up.
func Running() bool {
	_, err := os.Stat(displaySocket)
	return err == nil
}

// requireDisplay is the check every tool makes first.
func requireDisplay() error {
	if !Running() {
		return errors.New(notRunning)
	}
	return nil
}

// runBoth executes a command against the agent's display and returns both its
// streams. A failure carries the command's own stderr, which is what says why.
// The stderr of a run that succeeded is returned too, because xdotool warns
// there and exits 0 (see pressKeys).
func runBoth(ctx context.Context, name string, args ...string) (stdout, stderr string, err error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "DISPLAY="+Display)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(errOut.String()); msg != "" {
			return "", msg, fmt.Errorf("%s: %s", name, msg)
		}
		return "", "", fmt.Errorf("%s: %w", name, err)
	}
	return out.String(), strings.TrimSpace(errOut.String()), nil
}

// run executes a command against the agent's display and returns its standard
// output.
func run(ctx context.Context, name string, args ...string) (string, error) {
	out, _, err := runBoth(ctx, name, args...)
	return out, err
}

// xdotool runs one xdotool command line, which may chain several actions.
func xdotool(ctx context.Context, args ...string) (string, error) {
	if err := requireDisplay(); err != nil {
		return "", err
	}
	return run(ctx, "xdotool", args...)
}

// buttons are xdotool's mouse button numbers. Left, middle and right are all
// an agent needs; 4–7 are the scroll wheel, which scroll uses by direction.
var buttons = map[string]string{"left": "1", "middle": "2", "right": "3"}

// mouseButton translates a button name to xdotool's number. An empty name is
// the left button, which is what a click means when nothing says otherwise.
func mouseButton(name string) (string, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return buttons["left"], nil
	}
	code, ok := buttons[name]
	if !ok {
		return "", fmt.Errorf("button is %q: it is left, middle or right", name)
	}
	return code, nil
}

// scrollButtons are the wheel directions, as X11 button numbers.
var scrollButtons = map[string]string{"up": "4", "down": "5", "left": "6", "right": "7"}

func scrollButton(direction string) (string, error) {
	direction = strings.ToLower(strings.TrimSpace(direction))
	code, ok := scrollButtons[direction]
	if !ok {
		return "", fmt.Errorf("direction is %q: it is up, down, left or right", direction)
	}
	return code, nil
}

// point checks a coordinate pair. Screen coordinates start at the top left and
// are never negative; an off-screen one is refused here rather than moving the
// pointer somewhere the agent can't see.
func point(x, y int) error {
	if x < 0 || y < 0 {
		return fmt.Errorf("(%d, %d) is off the screen: coordinates start at (0, 0), the top left", x, y)
	}
	return nil
}

// keyCombo splits a key argument into the key strokes xdotool should press.
// "ctrl+l" is one stroke, "alt+Tab" another, and "Escape Return" is two in a
// row. Names are X keysyms and xdotool's modifier names, which this doesn't
// try to enumerate — xdotool refuses an unknown one clearly enough. What it
// does refuse is anything that isn't a key name at all, so a sentence handed
// to key instead of type fails here with an answer rather than as forty
// separate keystroke errors.
func keyCombo(combo string) ([]string, error) {
	strokes := strings.Fields(combo)
	if len(strokes) == 0 {
		return nil, errors.New("say which key to press, like Return, ctrl+l or alt+Tab")
	}
	for _, stroke := range strokes {
		for _, part := range strings.Split(stroke, "+") {
			if part == "" {
				return nil, fmt.Errorf("%q isn't a key combination: write it like ctrl+l, with one key each side of the +", stroke)
			}
			if strings.ContainsFunc(part, func(r rune) bool {
				return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_'
			}) {
				return nil, fmt.Errorf("%q isn't a key name: key presses keys (Return, ctrl+l, alt+Tab); to write text, use type", part)
			}
		}
	}
	return strokes, nil
}

// step is one point an animated move passes through.
type step struct{ x, y int }

// easeInOutQuad speeds a move up through its first half and slows it down
// through its second, the way a hand does, instead of the constant speed a
// linear interpolation would give a recording.
func easeInOutQuad(t float64) float64 {
	if t < 0.5 {
		return 2 * t * t
	}
	return 1 - (-2*t+2)*(-2*t+2)/2
}

// movePath is the points an animated move from (fromX, fromY) to (toX, toY)
// passes through, eased and spaced roughly moveStepPx apart, up to
// maxMoveSteps of them. It is nil when the two points are the same: nothing
// to travel.
func movePath(fromX, fromY, toX, toY int) []step {
	dx, dy := toX-fromX, toY-fromY
	dist := math.Hypot(float64(dx), float64(dy))
	if dist < 1 {
		return nil
	}
	steps := int(math.Ceil(dist / moveStepPx))
	if steps < 1 {
		steps = 1
	}
	if steps > maxMoveSteps {
		steps = maxMoveSteps
	}
	path := make([]step, steps)
	for i := 1; i <= steps; i++ {
		e := easeInOutQuad(float64(i) / float64(steps))
		path[i-1] = step{
			x: fromX + int(math.Round(float64(dx)*e)),
			y: fromY + int(math.Round(float64(dy)*e)),
		}
	}
	path[steps-1] = step{toX, toY} // land exactly: rounding can miss by a pixel
	return path
}

// moveSteps is movePath as xdotool arguments: chained mousemove commands with
// a sleep between each, so the whole animation is one xdotool invocation
// rather than dozens of processes. --sync is only on the last move, so a
// click or button-down chained after it lands where it was told to; nil when
// the pointer is already at the target, so a caller can skip moving at all.
func moveSteps(fromX, fromY, toX, toY int) []string {
	path := movePath(fromX, fromY, toX, toY)
	if path == nil {
		return nil
	}
	sleep := strconv.FormatFloat((moveDuration / time.Duration(len(path))).Seconds(), 'f', -1, 64)
	var args []string
	for i, p := range path {
		if i == len(path)-1 {
			args = append(args, "mousemove", "--sync", strconv.Itoa(p.x), strconv.Itoa(p.y))
		} else {
			args = append(args, "mousemove", strconv.Itoa(p.x), strconv.Itoa(p.y), "sleep", sleep)
		}
	}
	return args
}

// moveArgs move the pointer from the given position to (x, y), animated.
// Nil, nil when the pointer is already there: nothing to do.
func moveArgs(fromX, fromY, x, y int) ([]string, error) {
	if err := point(x, y); err != nil {
		return nil, err
	}
	return moveSteps(fromX, fromY, x, y), nil
}

// clickArgs move the pointer from the given position and click at (x, y). A
// double click is one xdotool click with --repeat 2, whose default 100 ms gap
// is inside every toolkit's double-click time.
func clickArgs(fromX, fromY, x, y int, button string, double bool) ([]string, error) {
	args, err := moveArgs(fromX, fromY, x, y)
	if err != nil {
		return nil, err
	}
	code, err := mouseButton(button)
	if err != nil {
		return nil, err
	}
	args = append(args, "click")
	if double {
		args = append(args, "--repeat", "2")
	}
	return append(args, code), nil
}

// dragArgs travel from the given position to where the drag starts, press the
// button, travel to where it ends and release. Both legs are animated moves:
// a single jump sends one motion event, and file managers and canvases that
// track the drag see nothing to track.
func dragArgs(curX, curY, fromX, fromY, toX, toY int, button string) ([]string, error) {
	if err := point(fromX, fromY); err != nil {
		return nil, err
	}
	if err := point(toX, toY); err != nil {
		return nil, err
	}
	code, err := mouseButton(button)
	if err != nil {
		return nil, err
	}
	// moveSteps is nil when the two ends of a leg are already the same point:
	// the pointer is already there, so there is nothing to travel.
	args := moveSteps(curX, curY, fromX, fromY)
	args = append(args, "mousedown", code)
	args = append(args, moveSteps(fromX, fromY, toX, toY)...)
	return append(args, "mouseup", code), nil
}

// scrollArgs move the pointer and turn the wheel there: X11 has no scroll
// event, only clicks of buttons 4 to 7, one per notch.
func scrollArgs(fromX, fromY, x, y int, direction string, amount int) ([]string, error) {
	args, err := moveArgs(fromX, fromY, x, y)
	if err != nil {
		return nil, err
	}
	code, err := scrollButton(direction)
	if err != nil {
		return nil, err
	}
	if amount <= 0 {
		amount = 3
	}
	if amount > 50 {
		return nil, fmt.Errorf("amount is %d: scroll up to 50 notches at a time", amount)
	}
	return append(args, "click", "--repeat", strconv.Itoa(amount), code), nil
}

// typeArgs type text as keystrokes into whatever has the focus.
func typeArgs(text string) ([]string, error) {
	if text == "" {
		return nil, errors.New("say what to type")
	}
	return []string{"type", "--delay", strconv.Itoa(typeDelay), "--", text}, nil
}

// keyArgs press one or more key combinations in order.
func keyArgs(combo string) ([]string, error) {
	strokes, err := keyCombo(combo)
	if err != nil {
		return nil, err
	}
	return append([]string{"key", "--delay", strconv.Itoa(typeDelay), "--"}, strokes...), nil
}

// pressKeys presses a key combination, and turns xdotool's quietest failure
// into a real one. Given a name that is not a keysym, xdotool prints "No such
// key name 'x'. Ignoring it." on stderr and exits 0, so a tool that only
// looked at the exit status would tell the agent it had pressed a key when
// nothing happened at all.
func pressKeys(ctx context.Context, combo string) error {
	args, err := keyArgs(combo)
	if err != nil {
		return err
	}
	if err := requireDisplay(); err != nil {
		return err
	}
	_, warnings, err := runBoth(ctx, "xdotool", args...)
	if err != nil {
		return err
	}
	if strings.Contains(warnings, "No such key name") {
		return fmt.Errorf("xdotool pressed nothing: %s. Key names are X keysyms — Return, Escape, Tab, "+
			"BackSpace, Page_Up, F5, a letter on its own — and to write text, use type", firstLine(warnings))
	}
	return nil
}

// firstLine is the first line of a command's complaint; xdotool repeats itself
// once per key press.
func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

// waitFor caps how long a wait tool sleeps, and refuses a negative one.
func waitFor(seconds float64) (time.Duration, error) {
	if seconds <= 0 {
		return 0, errors.New("say how many seconds to wait, as a number above zero")
	}
	d := time.Duration(seconds * float64(time.Second))
	if d > maxWait {
		return maxWait, nil
	}
	return d, nil
}

// Size is the display's real size in pixels.
type Size struct{ Width, Height int }

func (s Size) String() string { return fmt.Sprintf("%d×%d", s.Width, s.Height) }

// DisplaySize asks X how big the screen is. A screenshot is scaled down before
// the model sees it, so this is what tells it which coordinates are real.
func DisplaySize(ctx context.Context) (Size, error) {
	out, err := xdotool(ctx, "getdisplaygeometry")
	if err != nil {
		return Size{}, err
	}
	fields := strings.Fields(out)
	if len(fields) != 2 {
		return Size{}, fmt.Errorf("the display's size came back as %q", strings.TrimSpace(out))
	}
	w, err := strconv.Atoi(fields[0])
	if err != nil {
		return Size{}, err
	}
	h, err := strconv.Atoi(fields[1])
	if err != nil {
		return Size{}, err
	}
	return Size{w, h}, nil
}

// scaledTo is the size a screenshot of a display this size is returned at:
// the display itself when it is narrow enough, and maxWidth wide with the
// aspect ratio kept when it isn't. A screenshot is never scaled up.
func scaledTo(s Size) Size {
	if s.Width <= maxWidth || s.Width == 0 {
		return s
	}
	return Size{maxWidth, s.Height * maxWidth / s.Width}
}

// Scale converts between the pixels of the screenshots the model is shown and
// the display's real ones. Every tool takes and reports coordinates in the
// screenshot's pixels and converts them here: told to give the display's own,
// models gave the image's anyway, and every click on a scaled-down screen
// landed short and to the left of what they aimed at.
type Scale struct{ Real, Shown Size }

// DisplayScale is the display's size and the size its screenshots are shown at.
func DisplayScale(ctx context.Context) (Scale, error) {
	real, err := DisplaySize(ctx)
	if err != nil {
		return Scale{}, err
	}
	return Scale{real, scaledTo(real)}, nil
}

// ToReal turns a point in a screenshot into the display's pixels, refusing one
// outside the screenshot.
func (s Scale) ToReal(x, y int) (int, int, error) {
	if err := point(x, y); err != nil {
		return 0, 0, err
	}
	if s.Shown.Width > 0 && (x >= s.Shown.Width || y >= s.Shown.Height) {
		return 0, 0, fmt.Errorf("(%d, %d) is off the screen: the screenshot is %s, so x is below %d and y below %d",
			x, y, s.Shown, s.Shown.Width, s.Shown.Height)
	}
	return s.convert(x, s.Real.Width, s.Shown.Width), s.convert(y, s.Real.Height, s.Shown.Height), nil
}

// ToShown turns a point on the display into the screenshot's pixels.
func (s Scale) ToShown(x, y int) (int, int) {
	return s.convert(x, s.Shown.Width, s.Real.Width), s.convert(y, s.Shown.Height, s.Real.Height)
}

func (s Scale) convert(v, to, from int) int {
	if from == 0 || to == from {
		return v
	}
	return int(math.Round(float64(v) * float64(to) / float64(from)))
}

const (
	// grabRate is how often a capture looks at the screen. x11grab holds the
	// first frame back about one interval, so a slower rate makes even a
	// single screenshot slower: 230 ms at 10 a second against 100 at 30.
	grabRate = 30
	// settledFrames is how many frames in a row must be the same for the
	// screen to count as settled: five at grabRate is about 130 ms without a
	// change, long enough to see a click's repaint begin.
	settledFrames = 5
	// maxSettle is the longest a capture waits for the screen to stop
	// changing. A spinner or a video never does, and gets the frame it
	// reached by then.
	maxSettle = 1500 * time.Millisecond
	// jpegQuality keeps text crisp. JPEG rather than PNG because a model is
	// charged by pixels, not bytes, and a photo, a canvas or a gradient makes
	// a PNG many times bigger for no gain; saved media stays PNG (media.go).
	jpegQuality = 85
)

// Screenshot grabs the whole display as a JPEG, scaled down to at most
// maxWidth so it doesn't fill the model's context, and reports the display's
// real size with it. It grabs the screen the way media.go's display screenshot
// does, with ffmpeg's x11grab.
func Screenshot(ctx context.Context) (jpg []byte, sc Scale, err error) {
	return capture(ctx, false)
}

// SettledScreenshot is Screenshot once the screen has stopped changing: what a
// click, a key or a scroll led to, without a fixed sleep that is too long for
// most and too short for some. The screen has settled when settledFrames in a
// row are the same, or after maxSettle.
func SettledScreenshot(ctx context.Context) (jpg []byte, sc Scale, err error) {
	return capture(ctx, true)
}

// capture runs one ffmpeg, which grabs the display, scales it and streams raw
// RGBA frames; the first frame, or the first of the screen settled, is encoded
// here. One process for however many frames settling takes, and no
// temporary file.
func capture(ctx context.Context, settle bool) ([]byte, Scale, error) {
	if err := requireDisplay(); err != nil {
		return nil, Scale{}, err
	}
	sc, err := DisplayScale(ctx)
	if err != nil {
		return nil, Scale{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	args := []string{"-loglevel", "error", "-f", "x11grab", "-framerate", strconv.Itoa(grabRate), "-i", Display}
	if sc.Shown != sc.Real {
		args = append(args, "-vf", fmt.Sprintf("scale=%d:%d", sc.Shown.Width, sc.Shown.Height))
	}
	if !settle {
		args = append(args, "-frames:v", "1")
	}
	args = append(args, "-pix_fmt", "rgba", "-f", "rawvideo", "-")
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Env = append(os.Environ(), "DISPLAY="+Display)
	var errOut bytes.Buffer
	cmd.Stderr = &errOut
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, sc, err
	}
	if err := cmd.Start(); err != nil {
		return nil, sc, fmt.Errorf("taking the screenshot: %w", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	size := sc.Shown.Width * sc.Shown.Height * 4
	frame, prev := make([]byte, size), make([]byte, size)
	deadline := time.Now().Add(maxSettle)
	same := 1
	for n := 0; ; n++ {
		if _, err := io.ReadFull(out, frame); err != nil {
			if n > 0 {
				frame = prev // the stream ended: keep the last whole frame
				break
			}
			if msg := strings.TrimSpace(errOut.String()); msg != "" {
				return nil, sc, fmt.Errorf("taking the screenshot: ffmpeg: %s", msg)
			}
			return nil, sc, fmt.Errorf("taking the screenshot: %w", err)
		}
		if n > 0 && bytes.Equal(frame, prev) {
			same++
		} else {
			same = 1
		}
		if !settle || same >= settledFrames || time.Now().After(deadline) {
			break
		}
		frame, prev = prev, frame
	}
	img := &image.RGBA{Pix: frame, Stride: sc.Shown.Width * 4, Rect: image.Rect(0, 0, sc.Shown.Width, sc.Shown.Height)}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, sc, err
	}
	return buf.Bytes(), sc, nil
}

// Window is one window on the display.
type Window struct {
	ID          string
	Name        string
	X, Y, W, H  int
	hasGeometry bool
}

// Windows lists the visible windows, with their titles and geometry. xdotool
// matches every window with `--name ”`, and the panel and the root-ish
// windows come back with no title, so those are dropped: a window an agent
// can't name isn't one it can ask to focus.
func Windows(ctx context.Context) ([]Window, error) {
	out, err := xdotool(ctx, "search", "--onlyvisible", "--name", "")
	if err != nil {
		// xdotool search exits non-zero when it matches nothing, which is an
		// empty desktop rather than a failure.
		if strings.Contains(err.Error(), "exit status 1") {
			return nil, nil
		}
		return nil, err
	}
	var windows []Window
	for _, id := range strings.Fields(out) {
		name, err := run(ctx, "xdotool", "getwindowname", id)
		if err != nil {
			continue // it closed between the search and here
		}
		w := Window{ID: id, Name: strings.TrimSpace(name)}
		if w.Name == "" {
			continue
		}
		if geometry, err := run(ctx, "xdotool", "getwindowgeometry", "--shell", id); err == nil {
			w.X, w.Y, w.W, w.H, w.hasGeometry = parseGeometry(geometry)
		}
		windows = append(windows, w)
	}
	return windows, nil
}

// parseGeometry reads `xdotool getwindowgeometry --shell`, which prints
// WINDOW=, X=, Y=, WIDTH= and HEIGHT= as shell assignments.
func parseGeometry(out string) (x, y, w, h int, ok bool) {
	values := map[string]int{}
	for _, line := range strings.Split(out, "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if !found {
			continue
		}
		if n, err := strconv.Atoi(value); err == nil {
			values[key] = n
		}
	}
	_, hasW := values["WIDTH"]
	_, hasH := values["HEIGHT"]
	return values["X"], values["Y"], values["WIDTH"], values["HEIGHT"], hasW && hasH
}

// Focus raises and focuses a window, named either by the id windows reports or
// by a piece of its title.
func Focus(ctx context.Context, target string) (Window, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return Window{}, errors.New("say which window: its id, or part of its title")
	}
	windows, err := Windows(ctx)
	if err != nil {
		return Window{}, err
	}
	match, err := matchWindow(windows, target)
	if err != nil {
		return Window{}, err
	}
	if _, err := xdotool(ctx, "windowactivate", "--sync", match.ID); err != nil {
		return match, err
	}
	return match, nil
}

// matchWindow finds the window target names: an exact id, else the first
// window whose title contains it, ignoring case.
func matchWindow(windows []Window, target string) (Window, error) {
	for _, w := range windows {
		if w.ID == target {
			return w, nil
		}
	}
	lowered := strings.ToLower(target)
	for _, w := range windows {
		if strings.Contains(strings.ToLower(w.Name), lowered) {
			return w, nil
		}
	}
	if len(windows) == 0 {
		return Window{}, fmt.Errorf("no window matches %q: there are no windows open", target)
	}
	var names []string
	for _, w := range windows {
		names = append(names, strconv.Quote(w.Name))
	}
	return Window{}, fmt.Errorf("no window matches %q. Open: %s", target, strings.Join(names, ", "))
}

// Cursor is where the pointer is, and which window it is over.
type Cursor struct {
	X, Y   int
	Window string
}

// CursorPosition asks X where the pointer is.
func CursorPosition(ctx context.Context) (Cursor, error) {
	out, err := xdotool(ctx, "getmouselocation", "--shell")
	if err != nil {
		return Cursor{}, err
	}
	var c Cursor
	for _, line := range strings.Split(out, "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if !found {
			continue
		}
		switch key {
		case "X":
			c.X, _ = strconv.Atoi(value)
		case "Y":
			c.Y, _ = strconv.Atoi(value)
		case "WINDOW":
			c.Window = value
		}
	}
	return c, nil
}
