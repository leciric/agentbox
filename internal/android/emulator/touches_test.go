package emulator

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"agentbox/internal/devicestream"
)

// The dispatcher's lines as an API 35 emulator logs them with its
// InboundEvent tag at DEBUG, through `adb logcat -v epoch`: a tap from the
// touch panel (the app's viewer), then `adb shell input tap 300 600`.
const dispatcherLog = `--------- beginning of main
         1793792764.276   604   728 D InputDispatcher: notifyMotion - id=816e7be eventTime=90593749000, deviceId=12, source=TOUCHSCREEN | STYLUS, displayId=0, policyFlags=0x0, action=DOWN, actionButton=0x0, flags=0x80, metaState=0x0, buttonState=0x0, edgeFlags=0x0, xPrecision=1.000000, yPrecision=1.000000, downTime=90593749000
         1793792764.277   604   728 D InputDispatcher:   Pointer 0: id=0, toolType=FINGER, x=199.962158, y=399.975586, pressure=1.000000, size=0.000001, touchMajor=67.968750, touchMinor=67.968750, toolMajor=67.968750, toolMinor=67.968750, orientation=0.000000
         1793792764.360   604   728 D InputDispatcher: notifyMotion - id=38656916 eventTime=90676000000, deviceId=12, source=TOUCHSCREEN | STYLUS, displayId=0, policyFlags=0x0, action=MOVE, actionButton=0x0, flags=0x80, metaState=0x0
         1793792764.360   604   728 D InputDispatcher:   Pointer 0: id=0, toolType=FINGER, x=210.0, y=420.5, pressure=1.000000
         1793792764.449   604   728 D InputDispatcher: notifyMotion - id=38656915 eventTime=90802911000, deviceId=12, source=TOUCHSCREEN | STYLUS, displayId=0, policyFlags=0x0, action=UP, actionButton=0x0, flags=0x80, metaState=0x0, buttonState=0x0, edgeFlags=0x0
         1793792764.449   604   728 D InputDispatcher:   Pointer 0: id=0, toolType=FINGER, x=210.0, y=420.5, pressure=1.000000
         1793792765.000   604   728 D InputDispatcher: notifyMotion - id=1 eventTime=91000000000, deviceId=3, source=MOUSE, displayId=0, policyFlags=0x0, action=HOVER_MOVE, actionButton=0x0
         1793792765.000   604   728 D InputDispatcher:   Pointer 0: id=0, toolType=MOUSE, x=5.0, y=5.0
         1793792765.100   604   728 D InputDispatcher: notifyKey - id=2 eventTime=91100000000, deviceId=1, source=KEYBOARD, displayId=0, policyFlags=0x0, action=DOWN, flags=0x8, keyCode=0x1d
         1793792766.187   604  1121 I InputDispatcher: injectInputEvent: targetUid=<not set>, syncMode=WAIT_FOR_FINISHED, timeout=30000ms, policyFlags=0x8000000, event=MotionEvent { action=DOWN, id[0]=0, x[0]=300, y[0]=600, eventTime=92541000000, downTime=92541000000, deviceId=2, source=TOUCHSCREEN, displayId=0, eventId=0xed53365c}
         1793792766.195   604  1121 I InputDispatcher: injectInputEvent: targetUid=<not set>, syncMode=WAIT_FOR_FINISHED, timeout=30000ms, policyFlags=0x8000000, event=MotionEvent { action=UP, id[0]=0, x[0]=300, y[0]=600, eventTime=92541000000, downTime=92541000000, deviceId=2, source=TOUCHSCREEN, displayId=0, eventId=0xe5248764}
`

func TestReadTouchesLeavesOutWhatCameBefore(t *testing.T) {
	ts := &devicestream.Touches{}
	now := time.Unix(1793792766, 0)
	// Everything in the log was logged before 1793792767.
	if err := readTouches(strings.NewReader(dispatcherLog), 1080, 2400, time.Unix(1793792767, 0), func() time.Time { return now }, ts); err != nil {
		t.Fatal(err)
	}
	if ts.Showing(now.Add(50 * time.Millisecond)) {
		t.Error("a touch logged before the recording started is drawn")
	}
}

func TestTouchParserReadsTheDispatchersLines(t *testing.T) {
	var p touchParser
	var got []motion
	for _, l := range strings.Split(dispatcherLog, "\n") {
		if m, ok := p.line(l); ok {
			got = append(got, m)
		}
	}
	want := []motion{
		{"DOWN", 199.962158, 399.975586, 90593749000},
		{"MOVE", 210, 420.5, 90676000000},
		{"UP", 210, 420.5, 90802911000},
		{"DOWN", 300, 600, 92541000000},
		{"UP", 300, 600, 92541000000},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d touches, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("touch %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestTouchParserLeavesOutASecondFinger(t *testing.T) {
	var p touchParser
	lines := []string{
		"D InputDispatcher: notifyMotion - id=1 eventTime=5, deviceId=12, source=TOUCHSCREEN, displayId=0, action=POINTER_DOWN(1), actionButton=0x0",
		"D InputDispatcher:   Pointer 0: id=0, toolType=FINGER, x=1.0, y=2.0",
		"D InputDispatcher:   Pointer 1: id=1, toolType=FINGER, x=3.0, y=4.0",
		// A Pointer line with no notifyMotion before it is nobody's.
		"D InputDispatcher:   Pointer 0: id=0, toolType=FINGER, x=1.0, y=2.0",
	}
	for _, l := range lines {
		if m, ok := p.line(l); ok {
			t.Errorf("%q gave %+v", l, m)
		}
	}
}

func TestFieldStartsAWord(t *testing.T) {
	s := "actionButton=0x0, action=MOVE, index=4, x=1.5, y=2}"
	for key, want := range map[string]string{"action=": "MOVE", "x=": "1.5", "y=": "2", "z=": ""} {
		if got := field(s, key); got != want {
			t.Errorf("field(%q) = %q, want %q", key, got, want)
		}
	}
}

func TestTouchClockKeepsTheSoonestRead(t *testing.T) {
	var c touchClock
	base := time.Unix(1000, 0)
	// Read 40 ms after it happened, then a touch read 5 ms after: the second
	// sets the clock, and puts the first 5 ms after it was made, not 40.
	if got := c.at(int64(10*time.Second), base.Add(40*time.Millisecond)); !got.Equal(base.Add(40 * time.Millisecond)) {
		t.Fatalf("first touch at %v", got)
	}
	if got := c.at(int64(11*time.Second), base.Add(time.Second+5*time.Millisecond)); !got.Equal(base.Add(time.Second + 5*time.Millisecond)) {
		t.Fatalf("second touch at %v", got)
	}
	if got := c.at(int64(12*time.Second), base.Add(3*time.Second)); !got.Equal(base.Add(2*time.Second + 5*time.Millisecond)) {
		t.Errorf("a touch read late is at %v, want when it was made", got)
	}
}

func TestReadTouchesAddsThemAsFractions(t *testing.T) {
	ts := &devicestream.Touches{}
	now := time.Unix(1793792766, 0)
	if err := readTouches(strings.NewReader(dispatcherLog), 1080, 2400, time.Unix(1793792700, 0), func() time.Time { return now }, ts); err != nil {
		t.Fatal(err)
	}
	// The last touch read lifted the finger; its tap ripples where it
	// landed, at 300,600 of 1080x2400.
	img := make([]byte, 108*240*3)
	if !ts.Draw(img, 108, 240, now.Add(50*time.Millisecond)) {
		t.Fatal("nothing drawn just after the tap")
	}
	if px := img[(60*108+30)*3:]; px[0] == 0 && px[1] == 0 && px[2] == 0 {
		t.Error("the tap's ripple isn't where it landed")
	}
}

// WatchTouches turns the dispatcher's log on, reads it, and puts the tag back
// as it was when it ends, here through an adb that logs a tap and stays.
func TestWatchTouchesPutsTheLogBack(t *testing.T) {
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	stamp := strconv.FormatInt(time.Now().Unix()+1, 10)
	adb := filepath.Join(dir, "adb")
	script := `#!/bin/sh
echo "$*" >>` + calls + `
case "$1 $2" in
"shell getprop") echo INFO ;;
"logcat -v")
  echo "  ` + stamp + `.000   604  1121 I InputDispatcher: injectInputEvent: x, event=MotionEvent { action=DOWN, id[0]=0, x[0]=540, y[0]=1200, eventTime=5000000000, downTime=5000000000, deviceId=2, source=TOUCHSCREEN, displayId=0}"
  exec sleep 30 ;;
esac
`
	if err := os.WriteFile(adb, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	ts := &devicestream.Touches{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- WatchTouches(ctx, adb, 1080, 2400, ts) }()
	deadline := time.Now().Add(5 * time.Second)
	for !ts.Showing(time.Now()) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !ts.Showing(time.Now()) {
		t.Error("the tap the log has isn't drawn")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(calls)
	for _, want := range []string{"shell setprop log.tag.InputDispatcherInboundEvent DEBUG", "logcat -v epoch -T 1 -s InputDispatcher:*", "shell setprop log.tag.InputDispatcherInboundEvent 'INFO'"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("adb wasn't called with %q; calls:\n%s", want, got)
		}
	}
}

func TestWatchTouchesNeedsTheScreensSize(t *testing.T) {
	if err := WatchTouches(context.Background(), "adb", 0, 0, &devicestream.Touches{}); err == nil {
		t.Error("no error without the screen's size")
	}
}
