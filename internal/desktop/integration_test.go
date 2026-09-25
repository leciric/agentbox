//go:build integration

package desktop_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image/jpeg"
	"testing"

	"agentbox/internal/desktop"
	"agentbox/internal/mcp"
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

	tools := map[string]mcp.Tool{}
	for _, tool := range desktop.Tools(ctx) {
		tools[tool.Name] = tool
	}
	sc, err := desktop.DisplayScale(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Moving the pointer is the cheapest end-to-end check: X is asked to put
	// it somewhere, in the screenshot's pixels, and then asked where it is.
	// It answers with a screenshot of the display settled afterwards.
	want := [2]int{sc.Shown.Width / 3, sc.Shown.Height / 3}
	args, _ := json.Marshal(map[string]int{"x": want[0], "y": want[1]})
	answer, err := tools["mouse_move"].RunContent(args)
	if err != nil {
		t.Fatalf("mouse_move: %v", err)
	}
	checkShot(t, answer, sc)
	cursor, err := desktop.CursorPosition(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if x, y, _ := sc.ToReal(want[0], want[1]); cursor.X != x || cursor.Y != y {
		t.Errorf("the pointer is at (%d, %d), not (%d, %d)", cursor.X, cursor.Y, x, y)
	}

	// A key nobody can press must fail, not quietly do nothing: xdotool warns
	// on stderr and exits 0 for this one.
	bad, _ := json.Marshal(map[string]any{"combo": "NotAKeyAtAll", "screenshot": false})
	if _, err := tools["key"].RunContent(bad); err == nil {
		t.Error("a key name that doesn't exist was reported as pressed")
	}

	answer, err = tools["screenshot"].RunContent(nil)
	if err != nil {
		t.Fatal(err)
	}
	if sc.Real != size {
		t.Errorf("the scale reports the display as %v, not %v", sc.Real, size)
	}
	checkShot(t, answer, sc)
}

// checkShot checks that a tool answered with a JPEG as big as it says.
func checkShot(t *testing.T, answer []mcp.Content, sc desktop.Scale) {
	t.Helper()
	if len(answer) == 0 || answer[0].Type != "image" {
		t.Fatalf("the answer has no screenshot: %+v", answer)
	}
	shot, err := base64.StdEncoding.DecodeString(answer[0].Data)
	if err != nil {
		t.Fatal(err)
	}
	config, err := jpeg.DecodeConfig(bytes.NewReader(shot))
	if err != nil {
		t.Fatalf("the screenshot isn't a JPEG: %v", err)
	}
	if config.Width != sc.Shown.Width || config.Height != sc.Shown.Height {
		t.Errorf("the screenshot is %d×%d, but it says it is %v", config.Width, config.Height, sc.Shown)
	}
	if config.Width > 1024 {
		t.Errorf("the screenshot is %d px wide: it should be scaled down to 1024", config.Width)
	}
}
