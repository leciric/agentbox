package desktop

import (
	"errors"
	"strings"
	"testing"
)

func TestParseScriptTakesAnObjectOrAList(t *testing.T) {
	obj, err := ParseScript([]byte(`{"devtools":["127.0.0.1:9223"],"steps":[{"do":"click","label":"Save"},{"do":"note","text":"saved"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(obj.Steps) != 2 || obj.DevTools[0] != "127.0.0.1:9223" {
		t.Errorf("object: %+v", obj)
	}
	list, err := ParseScript([]byte(` [{"do":"key","combo":"ctrl+l"}]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Steps) != 1 || list.Steps[0].Combo != "ctrl+l" {
		t.Errorf("list: %+v", list)
	}
}

func TestParseScriptRefusesABadStepByNumber(t *testing.T) {
	for _, tc := range []struct{ script, want string }{
		{`{"steps":[]}`, "no steps"},
		{`[{"do":"click","label":"A"},{"do":"click"}]`, "step 2 (click): click needs x and y, or a label"},
		{`[{"do":"click","x":1}]`, "both x and y"},
		{`[{"do":"click","x":1,"y":2,"label":"A"}]`, "not both"},
		{`[{"do":"click","label":"A","button":"thumb"}]`, "left, middle or right"},
		{`[{"do":"type"}]`, "type needs text"},
		{`[{"do":"key","combo":"hello world!"}]`, "isn't a key name"},
		{`[{"do":"scroll","x":1,"y":1,"direction":"sideways"}]`, "step 1"},
		{`[{"do":"scroll","x":1,"y":1,"direction":"down","amount":99}]`, "up to 50"},
		{`[{"do":"wait"}]`, "wait needs text"},
		{`[{"do":"wait","text":"a","seconds":1}]`, "not both"},
		{`[{"do":"note","text":"  "}]`, "note needs text"},
		{`[{"do":"dance"}]`, `"dance" isn't a step`},
		{`[{"x":1,"y":1}]`, `say what the step does`},
		{`[{"do":"click","label":"A","lable":"B"}]`, `unknown field "lable"`},
	} {
		_, err := ParseScript([]byte(tc.script))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", tc.script, err, tc.want)
		}
	}
	_, err := ParseScript([]byte(`[{"do":"note","text":"a"},{"do":"type"}]`))
	var se *StepError
	if !errors.As(err, &se) || se.Index != 2 {
		t.Errorf("got %v, want a StepError for step 2", err)
	}
}

func TestStepStringNamesIt(t *testing.T) {
	x, y := 10, 20
	for _, tc := range []struct {
		step Step
		want string
	}{
		{Step{Do: "click", Label: "Save"}, `click "Save"`},
		{Step{Do: "click", X: &x, Y: &y}, `click (10, 20)`},
		{Step{Do: "type", Label: "Search", Text: "cats"}, `type "Search" "cats"`},
		{Step{Do: "key", Combo: " ctrl+l "}, `key ctrl+l`},
		{Step{Do: "scroll", X: &x, Y: &y, Direction: "Down"}, `scroll (10, 20) down`},
		{Step{Do: "wait", Text: "Saved"}, `wait for "Saved"`},
		{Step{Do: "wait", Seconds: 1.5}, `wait 1.5s`},
		{Step{Do: "note", Text: strings.Repeat("a", 50)}, `note "` + strings.Repeat("a", 40) + `…"`},
		{Step{}, `step`},
	} {
		if got := tc.step.String(); got != tc.want {
			t.Errorf("got %s, want %s", got, tc.want)
		}
	}
}

func TestPagePointIsOnTheScreen(t *testing.T) {
	// A maximised Chromium: the page is below 87 px of tabs and toolbar.
	p := pagePoint{X: 100, Y: 50, OW: 1440, IW: 1440, OH: 834, IH: 747, DPR: 1}
	if x, y := p.screen(0, 0); x != 100 || y != 137 {
		t.Errorf("got (%d, %d), want (100, 137)", x, y)
	}
	// A window elsewhere on a display at twice the scale.
	p = pagePoint{X: 10.4, Y: 10, OW: 800, IW: 800, OH: 600, IH: 560, DPR: 2}
	if x, y := p.screen(400, 200); x != 421 || y != 300 {
		t.Errorf("got (%d, %d), want (421, 300)", x, y)
	}
}

func TestPagePointsWindowIsTheXWindowNotTheFrame(t *testing.T) {
	// Electron in the window manager's frame says it is at the frame's
	// corner, 21 px above where X has its window.
	p := pagePoint{SX: 0, SY: 21, OW: 1440, IW: 1440, OH: 813, IH: 813, DPR: 1, Title: "AgentBox"}
	windows := []Window{
		{Name: "AgentBox", X: 0, Y: 0, W: 300, H: 200, hasGeometry: true},  // another size
		{Name: "Terminal", X: 5, Y: 5, W: 1440, H: 813, hasGeometry: true}, // another title
		{Name: "AgentBox", X: 0, Y: 42, W: 1440, H: 813, hasGeometry: true},
	}
	if x, y := p.window(windows); x != 0 || y != 42 {
		t.Errorf("got (%d, %d), want (0, 42)", x, y)
	}
	if x, y := p.window(nil); x != 0 || y != 21 {
		t.Errorf("with no window: got (%d, %d), want screenX and screenY, (0, 21)", x, y)
	}
}

func TestDevtoolsOfAddsTheScriptsToTheBrowsers(t *testing.T) {
	got := devtoolsOf([]string{"http://127.0.0.1:9223/", DevTools, " ", "ws://localhost:9333"})
	want := []string{DevTools, "127.0.0.1:9223", "localhost:9333"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v", got, want)
	}
}
