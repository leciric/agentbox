package emulator

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"agentbox/internal/devicestream"
)

// A phone recording shows where each tap lands (devicestream.Touches). The
// taps come from Android's input dispatcher, which every touch goes through,
// whatever made it: the app's viewer and the emulator's own input (the touch
// panel), and `adb shell input tap` or `input swipe` (injected, which never
// reach the panel, so getevent can't see them). With its InboundEvent log tag
// at DEBUG, which a shell can set while it runs, the dispatcher logs each one:
//
//	D InputDispatcher: notifyMotion - id=816e7be eventTime=90593749000, deviceId=12, source=TOUCHSCREEN | STYLUS, displayId=0, policyFlags=0x0, action=DOWN, …
//	D InputDispatcher:   Pointer 0: id=0, toolType=FINGER, x=199.962158, y=399.975586, …
//	I InputDispatcher: injectInputEvent: targetUid=<not set>, …, event=MotionEvent { action=DOWN, id[0]=0, x[0]=300, y[0]=600, eventTime=62541000000, downTime=62541000000, deviceId=2, source=TOUCHSCREEN, displayId=0, …}
//
// eventTime is the device's monotonic clock, in nanoseconds; touchClock puts
// it on the recorder's.

// inboundTag is the dispatcher's log tag for what comes into it.
const inboundTag = "log.tag.InputDispatcherInboundEvent"

// motion is one touch the dispatcher logged: X,Y in the display's pixels, and
// its eventTime.
type motion struct {
	action    string // DOWN, MOVE, UP or CANCEL
	x, y      float64
	eventTime int64
}

// touchParser reads the dispatcher's lines. A notifyMotion line has its
// pointers on the lines after it, so it waits for the first.
type touchParser struct {
	pending *motion
}

// line reads one line of logcat, and returns the touch it completes, if any.
// Only the first finger is followed: a second one's POINTER_DOWN and the like
// are left out, as are a mouse's and a trackball's events.
func (p *touchParser) line(s string) (motion, bool) {
	_, rest, ok := strings.Cut(s, "InputDispatcher:")
	if !ok {
		return motion{}, false
	}
	rest = strings.TrimSpace(rest)
	switch {
	case strings.HasPrefix(rest, "notifyMotion"):
		p.pending = nil
		if !strings.Contains(field(rest, "source="), "TOUCHSCREEN") {
			return motion{}, false
		}
		m := motion{action: field(rest, "action=")}
		m.eventTime, _ = strconv.ParseInt(field(rest, "eventTime="), 10, 64)
		if touchAction(m.action) {
			p.pending = &m
		}
		return motion{}, false
	case strings.HasPrefix(rest, "Pointer 0:"):
		m := p.pending
		p.pending = nil
		if m == nil {
			return motion{}, false
		}
		var errX, errY error
		m.x, errX = strconv.ParseFloat(field(rest, "x="), 64)
		m.y, errY = strconv.ParseFloat(field(rest, "y="), 64)
		return *m, errX == nil && errY == nil
	case strings.HasPrefix(rest, "injectInputEvent"):
		p.pending = nil
		_, ev, ok := strings.Cut(rest, "MotionEvent {")
		if !ok || !strings.Contains(field(ev, "source="), "TOUCHSCREEN") {
			return motion{}, false
		}
		m := motion{action: field(ev, "action=")}
		if !touchAction(m.action) {
			return motion{}, false
		}
		var errX, errY error
		m.x, errX = strconv.ParseFloat(field(ev, "x[0]="), 64)
		m.y, errY = strconv.ParseFloat(field(ev, "y[0]="), 64)
		m.eventTime, _ = strconv.ParseInt(field(ev, "eventTime="), 10, 64)
		return m, errX == nil && errY == nil
	default:
		// Its other lines, and a Pointer 1 line, which is a second finger.
		if !strings.HasPrefix(rest, "Pointer ") {
			p.pending = nil
		}
		return motion{}, false
	}
}

func touchAction(a string) bool {
	return a == "DOWN" || a == "MOVE" || a == "UP" || a == "CANCEL"
}

// field is the value after key in a line of comma-separated key=value pairs:
// up to the next comma, or the end. A key must start a word, so "x=" isn't
// found in "index=".
func field(s, key string) string {
	for i := 0; ; {
		j := strings.Index(s[i:], key)
		if j < 0 {
			return ""
		}
		j += i
		if j == 0 || s[j-1] == ' ' || s[j-1] == ',' || s[j-1] == '{' {
			v := s[j+len(key):]
			if k := strings.IndexAny(v, ",}"); k >= 0 {
				v = v[:k]
			}
			return strings.TrimSpace(v)
		}
		i = j + len(key)
	}
}

// touchClock puts the device's eventTimes on the recorder's clock. Its offset
// is the smallest seen between when a line was read and the eventTime it
// gives: the line read soonest after its touch, which is within a few
// milliseconds of it.
type touchClock struct {
	offset time.Duration
	set    bool
}

func (c *touchClock) at(eventTime int64, read time.Time) time.Time {
	device := time.Duration(eventTime)
	if eventTime <= 0 {
		return read
	}
	if off := time.Duration(read.UnixNano()) - device; !c.set || off < c.offset {
		c.offset, c.set = off, true
	}
	return time.Unix(0, int64(device+c.offset))
}

// readTouches adds the touches in r, the dispatcher's log, to touches, as
// fractions of a width x height display, until r ends. Lines logged before
// since, by the epoch time logcat starts each with, are left out.
func readTouches(r io.Reader, width, height int, since time.Time, now func() time.Time, touches *devicestream.Touches) error {
	var p touchParser
	var clock touchClock
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 64<<10), 1<<20)
	for s.Scan() {
		line := s.Text()
		if f := strings.Fields(line); len(f) > 0 {
			if stamp, err := strconv.ParseFloat(f[0], 64); err == nil && stamp < float64(since.UnixMilli())/1000 {
				continue
			}
		}
		m, ok := p.line(line)
		if !ok {
			continue
		}
		touches.Add(devicestream.Touch{
			At:   clock.at(m.eventTime, now()),
			X:    m.x / float64(width),
			Y:    m.y / float64(height),
			Down: m.action == "DOWN" || m.action == "MOVE",
		})
	}
	return s.Err()
}

// WatchTouches adds the touches made on the device, through adb, to touches
// until ctx ends, as fractions of its width x height screen. It turns the
// dispatcher's log on as it starts and back as it was when it ends.
func WatchTouches(ctx context.Context, adb string, width, height int, touches *devicestream.Touches) error {
	if width <= 0 || height <= 0 {
		return fmt.Errorf("the screen's size isn't known")
	}
	prop, err := exec.CommandContext(ctx, adb, "shell", "getprop", inboundTag).Output()
	if err != nil {
		return fmt.Errorf("reaching the device: %w", err)
	}
	was := strings.TrimSpace(string(prop))
	if err := exec.CommandContext(ctx, adb, "shell", "setprop", inboundTag, "DEBUG").Run(); err != nil {
		return fmt.Errorf("turning on the input log: %w", err)
	}
	defer func() {
		// Quoted for the device's shell: adb would drop an empty argument.
		_ = exec.Command(adb, "shell", "setprop "+inboundTag+" '"+strings.ReplaceAll(was, "'", "")+"'").Run()
	}()
	// From now on, by the device's clock, which the emulator keeps to the
	// host's: not the touches logged before the recording started. logcat's
	// own -T <time> was seen to drop lines after it, so it starts from its
	// last line (-T 1) and readTouches leaves out what came before; a second
	// of slack allows for the two clocks.
	since := time.Now().Add(-time.Second)
	cmd := exec.CommandContext(ctx, adb, "logcat", "-v", "epoch", "-T", "1", "-s", "InputDispatcher:*")
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("reading the input log: %w", err)
	}
	readErr := readTouches(out, width, height, since, time.Now, touches)
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return nil
	}
	if readErr != nil {
		return readErr
	}
	return waitErr
}
