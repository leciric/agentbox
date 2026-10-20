//go:build integration

package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// TestScriptOnTheBrowser runs a script against a page of the agent's
// Chromium: a click by label lands on the button the label names, a wait for
// text sees what it did, and a label that isn't there fails its step. Run it
// inside an agent, after `agentbox browser start`:
//
//	go test -tags integration -run TestScriptOnTheBrowser ./internal/desktop
func TestScriptOnTheBrowser(t *testing.T) {
	ctx := context.Background()
	if !Running() {
		t.Skip("no display on :99: start the browser first (agentbox browser start)")
	}
	pages, err := listPages(ctx, DevTools)
	if err != nil || len(pages) == 0 {
		t.Skip("the browser has no page")
	}
	page := `<title>script test</title><div style="height:1200px"></div>` +
		`<button onclick="document.body.insertAdjacentText('beforeend', 'Pressed it')">Press me</button>`
	html, _ := json.Marshal(page)
	if _, err := evaluateIn(ctx, pages[0].WebSocket, `document.documentElement.innerHTML = `+string(html)+`; document.title = "script test"`); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)

	s := Script{Steps: []Step{
		{Do: "note", Text: "the button"},
		{Do: "click", Label: "press me"},
		{Do: "wait", Text: "Pressed it", Timeout: ptr(5.0)},
	}}
	var notes []string
	done, err := s.Run(ctx, RunOptions{Note: func(_ context.Context, text string) error {
		notes = append(notes, text)
		return nil
	}})
	if err != nil || done != 3 {
		t.Fatalf("did %d steps: %v", done, err)
	}
	if len(notes) != 1 || notes[0] != "the button" {
		t.Errorf("notes: %v", notes)
	}

	s = Script{Steps: []Step{{Do: "wait", Seconds: 0.1}, {Do: "click", Label: "Not on the page"}}}
	done, err = s.Run(ctx, RunOptions{})
	var se *StepError
	if !errors.As(err, &se) || se.Index != 2 || done != 1 {
		t.Errorf("did %d steps: %v, want step 2 to fail", done, err)
	}
}

func ptr[T any](v T) *T { return &v }
