package iossim

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/devicestream"
)

func noSize() (int, int, error) { return 0, 0, errors.New("no size") }

func TestTapInPixelsIsAFractionOfTheScreen(t *testing.T) {
	steps, paste, err := plan(api.IOSInput{Type: "tap", X: 603, Y: 1311, Pixels: true}, func() (int, int, error) { return 1206, 2622, nil })
	if err != nil || paste != "" {
		t.Fatal(err, paste)
	}
	if len(steps) != 2 || steps[0].op.Phase != "down" || steps[1].op.Phase != "up" {
		t.Fatalf("steps = %+v", steps)
	}
	if steps[0].op.X != 0.5 || steps[0].op.Y != 0.5 || steps[0].wait < 80*time.Millisecond {
		t.Errorf("down = %+v, held %v", steps[0].op, steps[0].wait)
	}
}

func TestTapOffTheScreenIsRefused(t *testing.T) {
	if _, _, err := plan(api.IOSInput{Type: "tap", X: 2000, Y: 10, Pixels: true}, func() (int, int, error) { return 1206, 2622, nil }); err == nil {
		t.Error("a tap off the screen was planned")
	}
	if _, _, err := plan(api.IOSInput{Type: "tap", X: 10, Y: 10, Pixels: true}, noSize); err == nil {
		t.Error("a tap in pixels with no screen size was planned")
	}
}

func TestSwipeMovesFromStartToEnd(t *testing.T) {
	steps, _, err := plan(api.IOSInput{Type: "swipe", X: 0.5, Y: 0.9, X2: 0.5, Y2: 0.1, DurationMS: 160}, noSize)
	if err != nil {
		t.Fatal(err)
	}
	first, last := steps[0].op, steps[len(steps)-1].op
	if first.Phase != "down" || first.Y != 0.9 || last.Phase != "up" || last.Y != 0.1 {
		t.Fatalf("first %+v, last %+v", first, last)
	}
	moves := 0
	for _, s := range steps[1 : len(steps)-1] {
		if s.op.Phase != "move" {
			t.Errorf("middle step %+v isn't a move", s.op)
		}
		moves++
	}
	if moves < 2 {
		t.Errorf("%d moves, want a few", moves)
	}
}

func TestTextTypesKeysWithShift(t *testing.T) {
	steps, paste, err := plan(api.IOSInput{Type: "text", Text: "Hi!"}, noSize)
	if err != nil || paste != "" {
		t.Fatal(err, paste)
	}
	var got []op
	for _, s := range steps {
		got = append(got, s.op)
	}
	want := []op{
		{Op: "key", Usage: usageShift, Down: true}, {Op: "key", Usage: 0x0b, Down: true}, {Op: "key", Usage: 0x0b}, {Op: "key", Usage: usageShift},
		{Op: "key", Usage: 0x0c, Down: true}, {Op: "key", Usage: 0x0c},
		{Op: "key", Usage: usageShift, Down: true}, {Op: "key", Usage: 0x1e, Down: true}, {Op: "key", Usage: 0x1e}, {Op: "key", Usage: usageShift},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("step %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestTextAUSKeyboardCantTypeIsPasted(t *testing.T) {
	steps, paste, err := plan(api.IOSInput{Type: "text", Text: "Olá 👋"}, noSize)
	if err != nil || steps != nil || paste != "Olá 👋" {
		t.Fatalf("steps %v, paste %q, err %v", steps, paste, err)
	}
	v := pasteSteps()
	if v[0].op.Usage != modifierKeys["command"] || !v[0].op.Down || v[1].op.Usage != 0x19 {
		t.Errorf("paste is %+v, want ⌘V", v)
	}
}

func TestKeysAndModifiers(t *testing.T) {
	steps, _, err := plan(api.IOSInput{Type: "key", Key: "Return"}, noSize)
	if err != nil || steps[0].op.Usage != 0x28 {
		t.Fatalf("return: %+v %v", steps, err)
	}
	steps, _, err = plan(api.IOSInput{Type: "key", Key: "a", Modifiers: []string{"command", "shift"}}, noSize)
	if err != nil {
		t.Fatal(err)
	}
	order := []int{0xe3, 0xe1, 0x04, 0x04, 0xe1, 0xe3}
	for i, u := range order {
		if steps[i].op.Usage != u {
			t.Errorf("step %d = %+v, want usage %#x", i, steps[i].op, u)
		}
	}
	for _, bad := range []api.IOSInput{{Type: "key", Key: "f13"}, {Type: "key", Key: "a", Modifiers: []string{"hyper"}}} {
		if _, _, err := plan(bad, noSize); err == nil {
			t.Errorf("%+v was planned", bad)
		}
	}
}

func TestButtons(t *testing.T) {
	steps, _, err := plan(api.IOSInput{Type: "button", Button: "home"}, noSize)
	if err != nil || len(steps) != 2 || !steps[0].op.Down || steps[1].op.Down || steps[0].op.Button != "home" {
		t.Fatalf("home: %+v %v", steps, err)
	}
	steps, _, err = plan(api.IOSInput{Type: "button", Button: "lock", Phase: "down"}, noSize)
	if err != nil || len(steps) != 1 || !steps[0].op.Down {
		t.Fatalf("lock down: %+v %v", steps, err)
	}
	for _, bad := range []api.IOSInput{{Type: "button", Button: "action"}, {Type: "button", Button: "home", Phase: "sideways"}, {Type: "wiggle"}, {Type: "touch", Phase: "hover"}} {
		if _, _, err := plan(bad, noSize); err == nil {
			t.Errorf("%+v was planned", bad)
		}
	}
}

func TestFromViewer(t *testing.T) {
	reach := wheelReach // as fromViewer works it out, in float64
	for in, want := range map[string]struct {
		planned *api.IOSInput
		steps   []op
	}{
		`{"type":"touch","action":"move","x":0.5,"y":0.5,"id":1}`: {planned: &api.IOSInput{Type: "touch", Phase: "move", X: 0.5, Y: 0.5}},
		`{"type":"touch","action":"cancel","x":0.5,"y":0.5}`:      {planned: &api.IOSInput{Type: "touch", Phase: "up", X: 0.5, Y: 0.5}},
		`{"type":"key","action":"up","key":"ArrowLeft"}`:          {steps: []op{{Op: "key", Usage: 0x50}}},
		`{"type":"key","action":"down","key":"Meta"}`:             {steps: []op{{Op: "key", Usage: 0xe3, Down: true}}},
		`{"type":"key","action":"down","key":"a"}`:                {steps: []op{{Op: "key", Usage: 0x04, Down: true}}},
		`{"type":"text","text":"héllo"}`:                          {planned: &api.IOSInput{Type: "text", Text: "héllo"}},
		`{"type":"wheel","x":0.5,"y":0.5,"dx":0,"dy":2}`:          {planned: &api.IOSInput{Type: "swipe", X: 0.5, Y: 0.5, X2: 0.5, Y2: 0.5 - float64(2*reach), DurationMS: 100}},
		`{"type":"wheel","x":0.5,"y":0.05,"dx":0,"dy":-3}`:        {planned: &api.IOSInput{Type: "swipe", X: 0.5, Y: 0.05, X2: 0.5, Y2: 0.05 + float64(3*reach), DurationMS: 100}},
		`{"type":"button","button":"home"}`:                       {planned: &api.IOSInput{Type: "button", Button: "home"}},
		`{"type":"button","button":"recents"}`:                    {},
	} {
		d, err := devicestream.DecodeInput([]byte(in))
		if err != nil {
			t.Fatal(err)
		}
		planned, steps, err := fromViewer(d)
		if err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if (planned == nil) != (want.planned == nil) || (planned != nil && fmt.Sprint(*planned) != fmt.Sprint(*want.planned)) {
			t.Errorf("%s: planned %+v, want %+v", in, planned, want.planned)
		}
		var got []op
		for _, s := range steps {
			got = append(got, s.op)
		}
		if fmt.Sprint(got) != fmt.Sprint(want.steps) {
			t.Errorf("%s: steps %+v, want %+v", in, got, want.steps)
		}
	}
	for _, bad := range []string{`{"type":"key","action":"down","key":"F13"}`, `{"type":"key","action":"press","key":"Enter"}`, `{"type":"pinch"}`} {
		d, _ := devicestream.DecodeInput([]byte(bad))
		if _, _, err := fromViewer(d); err == nil {
			t.Errorf("%s was taken", bad)
		}
	}
}
