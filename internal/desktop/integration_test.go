//go:build integration

package desktop_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image/png"
	"testing"

	"agentbox/internal/desktop"
)

// This test drives a real X display with real xdotool and ffmpeg. It is what
// an agent has after `agentbox browser start`, so run it inside one:
//
//	agentbox browser start && go test -tags integration ./internal/desktop
//
// Unlike the other integration tests in this repository it needs no Incus,
// only a display on :99.

func TestDesktopOnADisplay(t *testing.T) {
	if !desktop.Running() {
		t.Skip("no display on :99: start the browser first (agentbox browser start)")
	}
	ctx := context.Background()

	size, err := desktop.DisplaySize(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if size.Width <= 0 || size.Height <= 0 {
		t.Fatalf("the display is %v", size)
	}

	tools := map[string]func(json.RawMessage) (string, error){}
	for _, tool := range desktop.Tools(ctx) {
		if tool.Run != nil {
			tools[tool.Name] = tool.Run
		}
	}

	// Moving the pointer is the cheapest end-to-end check: X is asked to put
	// it somewhere, and then asked where it is.
	want := [2]int{size.Width / 3, size.Height / 3}
	args, _ := json.Marshal(map[string]int{"x": want[0], "y": want[1]})
	if _, err := tools["mouse_move"](args); err != nil {
		t.Fatalf("mouse_move: %v", err)
	}
	cursor, err := desktop.CursorPosition(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cursor.X != want[0] || cursor.Y != want[1] {
		t.Errorf("the pointer is at (%d, %d), not (%d, %d)", cursor.X, cursor.Y, want[0], want[1])
	}

	// A key nobody can press must fail, not quietly do nothing: xdotool warns
	// on stderr and exits 0 for this one.
	bad, _ := json.Marshal(map[string]string{"combo": "NotAKeyAtAll"})
	if _, err := tools["key"](bad); err == nil {
		t.Error("a key name that doesn't exist was reported as pressed")
	}

	shot, real, shown, err := desktop.Screenshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if real != size {
		t.Errorf("the screenshot reports the display as %v, not %v", real, size)
	}
	config, err := png.DecodeConfig(bytes.NewReader(shot))
	if err != nil {
		t.Fatalf("the screenshot isn't a PNG: %v", err)
	}
	if config.Width != shown.Width || config.Height != shown.Height {
		t.Errorf("the screenshot is %d×%d, but it says it is %v", config.Width, config.Height, shown)
	}
	if config.Width > 1280 {
		t.Errorf("the screenshot is %d px wide: it should be scaled down to 1280", config.Width)
	}
}
