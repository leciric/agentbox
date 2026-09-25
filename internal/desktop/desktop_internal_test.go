package desktop

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// These test the translation from a tool's arguments to an xdotool command
// line, which is where the mistakes an agent would see live: a button name
// that means nothing, a key combination that is really a sentence, a
// coordinate off the screen. None of it needs a display.

func TestMouseButton(t *testing.T) {
	for name, want := range map[string]string{"left": "1", "middle": "2", "right": "3", "": "1", "RIGHT": "3", " left ": "1"} {
		got, err := mouseButton(name)
		if err != nil {
			t.Errorf("mouseButton(%q): %v", name, err)
			continue
		}
		if got != want {
			t.Errorf("mouseButton(%q) = %q, want %q", name, got, want)
		}
	}
	for _, name := range []string{"primary", "1", "wheel", "leftt"} {
		if _, err := mouseButton(name); err == nil {
			t.Errorf("mouseButton(%q) was accepted", name)
		} else if !strings.Contains(err.Error(), "left, middle or right") {
			t.Errorf("mouseButton(%q) said %q, which doesn't name the buttons", name, err)
		}
	}
}

func TestScrollButton(t *testing.T) {
	for direction, want := range map[string]string{"up": "4", "down": "5", "left": "6", "right": "7", "Down": "5"} {
		got, err := scrollButton(direction)
		if err != nil || got != want {
			t.Errorf("scrollButton(%q) = %q, %v; want %q", direction, got, err, want)
		}
	}
	// An empty direction is a mistake, not a default: there is no obvious way
	// to scroll when nobody said which.
	for _, direction := range []string{"", "north", "3"} {
		if _, err := scrollButton(direction); err == nil {
			t.Errorf("scrollButton(%q) was accepted", direction)
		}
	}
}

func TestPointRefusesOffScreen(t *testing.T) {
	if err := point(0, 0); err != nil {
		t.Errorf("(0, 0) is the top left corner: %v", err)
	}
	if err := point(1439, 899); err != nil {
		t.Errorf("(1439, 899) is on a 1440×900 display: %v", err)
	}
	for _, p := range [][2]int{{-1, 0}, {0, -1}, {-5, -5}} {
		if err := point(p[0], p[1]); err == nil {
			t.Errorf("point%v was accepted", p)
		}
	}
}

func TestKeyCombo(t *testing.T) {
	for combo, want := range map[string][]string{
		"Return":        {"Return"},
		"ctrl+l":        {"ctrl+l"},
		"alt+Tab":       {"alt+Tab"},
		"ctrl+shift+t":  {"ctrl+shift+t"},
		"super":         {"super"},
		"  Escape  ":    {"Escape"},
		"ctrl+a Delete": {"ctrl+a", "Delete"},
		"F5":            {"F5"},
		"ctrl+Page_Up":  {"ctrl+Page_Up"},
	} {
		got, err := keyCombo(combo)
		if err != nil {
			t.Errorf("keyCombo(%q): %v", combo, err)
			continue
		}
		if !slices.Equal(got, want) {
			t.Errorf("keyCombo(%q) = %v, want %v", combo, got, want)
		}
	}

	// An empty combination, a dangling +, and anything with a character no
	// key name has — which is most prose handed to key instead of type.
	for _, combo := range []string{"", "   ", "ctrl+", "+l", "hello, world!", "ctrl++l", "ctrl+l;reboot"} {
		if _, err := keyCombo(combo); err == nil {
			t.Errorf("keyCombo(%q) was accepted", combo)
		}
	}
	if _, err := keyCombo("hello, world!"); err == nil || !strings.Contains(err.Error(), "use type") {
		t.Errorf("prose should be sent to type; got %v", err)
	}
	// Prose made only of bare words — "type this out" — passes this check,
	// because "out" is shaped exactly like the real keysym "space". xdotool
	// is what refuses it, and it does so by printing a warning and exiting 0,
	// which is why pressKeys reads its stderr.
	if _, err := keyCombo("type this out"); err != nil {
		t.Errorf("bare words reach xdotool, which names them: %v", err)
	}
}

func TestFirstLine(t *testing.T) {
	warning := "(symbol) No such key name 'this'. Ignoring it.\n(symbol) No such key name 'this'. Ignoring it.\n"
	if got := firstLine(warning); got != "(symbol) No such key name 'this'. Ignoring it." {
		t.Errorf("firstLine = %q", got)
	}
}

func TestClickArgs(t *testing.T) {
	// The pointer is already at the target: no move, just the click.
	got, err := clickArgs(100, 200, 100, 200, "", false)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"click", "1"}
	if !slices.Equal(got, want) {
		t.Errorf("clickArgs already there = %v, want %v", got, want)
	}

	got, err = clickArgs(10, 20, 10, 20, "right", true)
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"click", "--repeat", "2", "3"}
	if !slices.Equal(got, want) {
		t.Errorf("a double right-click = %v, want %v", got, want)
	}

	// Travelling somewhere ends with a synced move to the target, then the
	// click.
	got, err = clickArgs(0, 0, 100, 0, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got[len(got)-6:], []string{"mousemove", "--sync", "100", "0", "click", "1"}) {
		t.Errorf("clickArgs after travelling = %v", got)
	}

	if _, err := clickArgs(0, 0, -1, 20, "left", false); err == nil {
		t.Error("clicking off the screen was accepted")
	}
	if _, err := clickArgs(0, 0, 1, 2, "sideways", false); err == nil {
		t.Error("clicking a button that doesn't exist was accepted")
	}
}

// A drag has to send motion events on the way, or an app that tracks the drag
// sees the pointer teleport and does nothing.
func TestDragArgsMovesInSteps(t *testing.T) {
	got, err := dragArgs(0, 0, 0, 0, 80, 40, "left")
	if err != nil {
		t.Fatal(err)
	}
	// Already at the drag's start: no approach move, straight to pressing.
	if got[0] != "mousedown" || got[1] != "1" {
		t.Errorf("a drag already at its start should press immediately: %v", got)
	}
	if got[len(got)-2] != "mouseup" || got[len(got)-1] != "1" {
		t.Errorf("a drag should release the button: %v", got)
	}
	moves := 0
	for _, arg := range got {
		if arg == "mousemove" {
			moves++
		}
	}
	if moves < 3 {
		t.Errorf("a drag made %d moves; it should travel in steps: %v", moves, got)
	}
	// It must end exactly where it was asked to, not at a rounded step.
	tail := got[len(got)-5 : len(got)-2]
	if !slices.Equal(tail, []string{"--sync", "80", "40"}) {
		t.Errorf("a drag ended at %v, not (80, 40)", tail)
	}

	// Approaching from elsewhere adds a travel leg before the press.
	got, err = dragArgs(200, 200, 0, 0, 80, 40, "left")
	if err != nil {
		t.Fatal(err)
	}
	pressed := slices.Index(got, "mousedown")
	if pressed < 1 {
		t.Errorf("a drag approaching from elsewhere should move before pressing: %v", got)
	}
	if !slices.Equal(got[pressed-4:pressed], []string{"mousemove", "--sync", "0", "0"}) {
		t.Errorf("a drag should press exactly where it starts: %v", got)
	}

	if _, err := dragArgs(0, 0, 0, 0, 10, -1, "left"); err == nil {
		t.Error("dragging off the screen was accepted")
	}
}

func TestScrollArgs(t *testing.T) {
	// Already there: no move, just the scroll.
	got, err := scrollArgs(50, 60, 50, 60, "down", 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"click", "--repeat", "3", "5"}
	if !slices.Equal(got, want) {
		t.Errorf("scrolling with no amount = %v, want three notches: %v", got, want)
	}
	if got, err := scrollArgs(1, 1, 1, 1, "up", 7); err != nil || got[len(got)-2] != "7" || got[len(got)-1] != "4" {
		t.Errorf("scrollArgs(up, 7) = %v, %v", got, err)
	}
	if _, err := scrollArgs(1, 1, 1, 1, "down", 500); err == nil {
		t.Error("scrolling 500 notches was accepted")
	}
}

// movePath is what every animated move shares: mouse_move, click, drag and
// scroll all build their xdotool arguments from it.
func TestMovePath(t *testing.T) {
	if got := movePath(50, 50, 50, 50); got != nil {
		t.Errorf("movePath to the same point = %v, want nil", got)
	}

	// A short hop still takes at least one step, and lands exactly.
	got := movePath(0, 0, 5, 0)
	if len(got) < 1 {
		t.Fatalf("movePath(short hop) = %v", got)
	}
	if last := got[len(got)-1]; last != (step{5, 0}) {
		t.Errorf("a short move should still land exactly: %v", got)
	}

	// Steps scale with distance, roughly one per moveStepPx, capped.
	got = movePath(0, 0, 200, 0)
	if len(got) < 5 || len(got) > maxMoveSteps {
		t.Errorf("movePath(200px) has %d steps, want a handful and no more than %d", len(got), maxMoveSteps)
	}
	if last := got[len(got)-1]; last != (step{200, 0}) {
		t.Errorf("a move should land exactly on its target: %v", got)
	}

	// A very long move is capped, not one step per moveStepPx forever.
	got = movePath(0, 0, 100000, 0)
	if len(got) != maxMoveSteps {
		t.Errorf("movePath(very far) has %d steps, want the cap of %d", len(got), maxMoveSteps)
	}
	if last := got[len(got)-1]; last != (step{100000, 0}) {
		t.Errorf("even a capped move should land exactly: %v", got)
	}

	// The path is eased, not a straight constant-speed line: ease-in-out is
	// slowest at both ends and fastest in the middle, so the first step
	// covers less ground than one near the midpoint.
	got = movePath(0, 0, 400, 0)
	first := got[0].x
	mid := got[len(got)/2].x - got[len(got)/2-1].x
	if first >= mid {
		t.Errorf("an eased move should start slower than its middle: first step %dpx, middle step %dpx", first, mid)
	}
}

func TestMoveStepsEndsSynced(t *testing.T) {
	if got := moveSteps(10, 10, 10, 10); got != nil {
		t.Errorf("moveSteps to the same point = %v, want nil: nothing to do", got)
	}

	got := moveSteps(0, 0, 60, 0)
	if len(got) == 0 {
		t.Fatal("moveSteps produced nothing for a real move")
	}
	if !slices.Equal(got[len(got)-3:], []string{"--sync", "60", "0"}) {
		t.Errorf("moveSteps should end synced at the target: %v", got)
	}
	// Every move but the last is followed by a sleep, so the whole thing is
	// one xdotool invocation that still animates.
	sleeps := 0
	for _, arg := range got {
		if arg == "sleep" {
			sleeps++
		}
	}
	moves := 0
	for _, arg := range got {
		if arg == "mousemove" {
			moves++
		}
	}
	if sleeps != moves-1 {
		t.Errorf("moveSteps has %d moves and %d sleeps; want one sleep between each pair of moves", moves, sleeps)
	}
}

func TestTypeAndKeyArgs(t *testing.T) {
	got, err := typeArgs("hello --version")
	if err != nil {
		t.Fatal(err)
	}
	// The -- matters: without it xdotool reads "--version" as its own flag.
	want := []string{"type", "--delay", "25", "--", "hello --version"}
	if !slices.Equal(got, want) {
		t.Errorf("typeArgs = %v, want %v", got, want)
	}
	if _, err := typeArgs(""); err == nil {
		t.Error("typing nothing was accepted")
	}

	got, err = keyArgs("ctrl+l Return")
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"key", "--delay", "25", "--", "ctrl+l", "Return"}
	if !slices.Equal(got, want) {
		t.Errorf("keyArgs = %v, want %v", got, want)
	}
}

func TestWaitForIsCapped(t *testing.T) {
	if d, err := waitFor(0.5); err != nil || d != 500*time.Millisecond {
		t.Errorf("waitFor(0.5) = %v, %v", d, err)
	}
	if d, err := waitFor(600); err != nil || d != maxWait {
		t.Errorf("waitFor(600) = %v, %v; it should be capped at %v", d, err, maxWait)
	}
	for _, seconds := range []float64{0, -1} {
		if _, err := waitFor(seconds); err == nil {
			t.Errorf("waitFor(%v) was accepted", seconds)
		}
	}
}

// A screenshot is scaled down so it doesn't fill the model's context, and
// never scaled up: a small display comes back as it is.
func TestScaledTo(t *testing.T) {
	for _, c := range []struct{ in, want Size }{
		{Size{1440, 900}, Size{1280, 800}},
		{Size{1280, 800}, Size{1280, 800}},
		{Size{1024, 768}, Size{1024, 768}},
		{Size{1920, 1080}, Size{1280, 720}},
	} {
		if got := scaledTo(c.in); got != c.want {
			t.Errorf("scaledTo(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseGeometry(t *testing.T) {
	x, y, w, h, ok := parseGeometry("WINDOW=41943043\nX=0\nY=28\nWIDTH=1440\nHEIGHT=844\nSCREEN=0\n")
	if !ok || x != 0 || y != 28 || w != 1440 || h != 844 {
		t.Errorf("parseGeometry = %d,%d %dx%d, ok %v", x, y, w, h, ok)
	}
	if _, _, _, _, ok := parseGeometry("xdotool: no such window\n"); ok {
		t.Error("geometry was read out of an error message")
	}
}

func TestMatchWindow(t *testing.T) {
	windows := []Window{
		{ID: "12345", Name: "about:blank - Chromium"},
		{ID: "67890", Name: "Terminal - dev@agent-55"},
	}
	if w, err := matchWindow(windows, "67890"); err != nil || w.Name != "Terminal - dev@agent-55" {
		t.Errorf("matching by id = %v, %v", w, err)
	}
	if w, err := matchWindow(windows, "terminal"); err != nil || w.ID != "67890" {
		t.Errorf("matching part of a title, ignoring case = %v, %v", w, err)
	}
	// A miss names what is open, so the next call can be right.
	_, err := matchWindow(windows, "Files")
	if err == nil || !strings.Contains(err.Error(), "Chromium") {
		t.Errorf("a miss should list the open windows; got %v", err)
	}
	if _, err := matchWindow(nil, "anything"); err == nil || !strings.Contains(err.Error(), "no windows open") {
		t.Errorf("matching against an empty desktop = %v", err)
	}
}
