package desktop

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// A script is a walkthrough as a list of steps: the desktop tools' steps tool
// does them in one call while an agent rehearses, and `agentbox media record
// --script` replays the same list into a recording, with no model in the loop.
// A recording driven step by step by a model is mostly the model thinking
// between steps; replayed, it is as long as the walkthrough itself.

const (
	// StepPause is the gap between two steps of a script: long enough to see
	// what a step did before the next one starts, short enough not to drag.
	StepPause = 300 * time.Millisecond
	// maxStepWait is the longest one wait step waits, for text or not. A
	// script waits for a page to load or a dialog to open, not for a build.
	maxStepWait = 30 * time.Second
	// defaultTextWait is how long a wait for text gives it to appear.
	defaultTextWait = 10 * time.Second
	// maxSteps keeps a script a walkthrough rather than a program.
	maxSteps = 200
)

// Step is one step of a script. Do says what it is; the other fields are its
// arguments, each read only by the steps that take it:
//
//   - click: x and y, or label; button and double optional.
//   - move: x and y, or label: hover without clicking.
//   - type: text, into what has the focus, or into x and y or label clicked
//     first; key pressed afterwards, optional.
//   - key: combo, like ctrl+l or Return.
//   - scroll: x and y, or label; direction, and amount (3 by default).
//   - wait: text to appear on a page (timeout seconds, 10 by default), or
//     seconds to sleep.
//   - note: text, a chapter of the recording.
//
// Coordinates are in the screenshot's pixels, as every desktop tool takes
// them. A label is an element's accessible name or its text, found in a page
// the DevTools protocol reaches: the browser's, or an Electron app started
// with --remote-debugging-port and named in the script's devtools.
type Step struct {
	Do        string   `json:"do"`
	X         *int     `json:"x,omitempty"`
	Y         *int     `json:"y,omitempty"`
	Label     string   `json:"label,omitempty"`
	Button    string   `json:"button,omitempty"`
	Double    bool     `json:"double,omitempty"`
	Text      string   `json:"text,omitempty"`
	Key       string   `json:"key,omitempty"`
	Combo     string   `json:"combo,omitempty"`
	Direction string   `json:"direction,omitempty"`
	Amount    int      `json:"amount,omitempty"`
	Seconds   float64  `json:"seconds,omitempty"`
	Timeout   *float64 `json:"timeout,omitempty"`
}

// Script is a walkthrough as it is saved: its steps, and the DevTools
// endpoints its labels and texts are looked for in besides the browser's.
type Script struct {
	DevTools []string `json:"devtools,omitempty"`
	Steps    []Step   `json:"steps"`
}

// ParseScript reads a script: an object with steps, or just the list of them,
// as the steps tool is given it. It is checked whole, so a replay doesn't
// start recording only to stop at a typo in step nine.
func ParseScript(data []byte) (Script, error) {
	var s Script
	data = bytes.TrimSpace(data)
	dec := func(v any) error {
		d := json.NewDecoder(bytes.NewReader(data))
		d.DisallowUnknownFields()
		return d.Decode(v)
	}
	var err error
	if len(data) > 0 && data[0] == '[' {
		err = dec(&s.Steps)
	} else {
		err = dec(&s)
	}
	if err != nil {
		return Script{}, fmt.Errorf("reading the script: %w", err)
	}
	return s, s.Check()
}

// Check refuses a script with a step that can't run, naming the step.
func (s Script) Check() error {
	if len(s.Steps) == 0 {
		return errors.New("the script has no steps")
	}
	if len(s.Steps) > maxSteps {
		return fmt.Errorf("the script has %d steps: keep it to %d", len(s.Steps), maxSteps)
	}
	for i, st := range s.Steps {
		if err := st.check(); err != nil {
			return &StepError{Index: i + 1, Step: st, Err: err}
		}
	}
	return nil
}

func (st Step) check() error {
	hasXY := st.X != nil || st.Y != nil
	if (st.X == nil) != (st.Y == nil) {
		return errors.New("give both x and y, or neither")
	}
	if hasXY && st.Label != "" {
		return errors.New("give x and y or a label, not both")
	}
	target := hasXY || st.Label != ""
	switch st.Do {
	case "click", "move":
		if !target {
			return fmt.Errorf("%s needs x and y, or a label", st.Do)
		}
		if st.Do == "click" {
			if _, err := mouseButton(st.Button); err != nil {
				return err
			}
		}
	case "type":
		if st.Text == "" {
			return errors.New("type needs text")
		}
	case "key":
		if _, err := keyCombo(st.Combo); err != nil {
			return err
		}
	case "scroll":
		if !target {
			return errors.New("scroll needs x and y, or a label")
		}
		if _, err := scrollButton(st.Direction); err != nil {
			return err
		}
		if st.Amount > 50 {
			return fmt.Errorf("amount is %d: scroll up to 50 notches at a time", st.Amount)
		}
	case "wait":
		if (st.Text == "") == (st.Seconds <= 0) {
			return errors.New("wait needs text to wait for, or seconds to sleep, but not both")
		}
	case "note":
		if strings.TrimSpace(st.Text) == "" {
			return errors.New("note needs text")
		}
	case "":
		return errors.New(`say what the step does with "do": click, move, type, key, scroll, wait or note`)
	default:
		return fmt.Errorf("%q isn't a step: use click, move, type, key, scroll, wait or note", st.Do)
	}
	return nil
}

// String names a step the way an error about it does: click "Save", type
// "hello", key ctrl+l.
func (st Step) String() string {
	var b strings.Builder
	b.WriteString(cmp.Or(st.Do, "step"))
	switch {
	case st.Label != "":
		fmt.Fprintf(&b, " %q", st.Label)
	case st.X != nil && st.Y != nil:
		fmt.Fprintf(&b, " (%d, %d)", *st.X, *st.Y)
	}
	switch st.Do {
	case "type", "note":
		fmt.Fprintf(&b, " %q", shortText(st.Text))
	case "key":
		b.WriteString(" " + strings.TrimSpace(st.Combo))
	case "scroll":
		b.WriteString(" " + strings.ToLower(st.Direction))
	case "wait":
		if st.Text != "" {
			fmt.Fprintf(&b, " for %q", shortText(st.Text))
		} else {
			fmt.Fprintf(&b, " %gs", st.Seconds)
		}
	}
	return b.String()
}

func shortText(s string) string {
	if r := []rune(s); len(r) > 40 {
		return string(r[:40]) + "…"
	}
	return s
}

// StepError is a step that failed, numbered from 1.
type StepError struct {
	Index int
	Step  Step
	Err   error
}

func (e *StepError) Error() string { return fmt.Sprintf("step %d (%s): %v", e.Index, e.Step, e.Err) }
func (e *StepError) Unwrap() error { return e.Err }

// RunOptions is how a script runs.
type RunOptions struct {
	// Note is called for each note step. Nil skips them: a rehearsal has no
	// recording to mark chapters in.
	Note func(ctx context.Context, text string) error
	// Pause is the gap between steps; StepPause when zero.
	Pause time.Duration
}

// Run does a script's steps in order, and stops at the first that fails, with
// a *StepError. It returns how many steps were done.
func (s Script) Run(ctx context.Context, opts RunOptions) (int, error) {
	if err := s.Check(); err != nil {
		return 0, err
	}
	if err := requireDisplay(ctx); err != nil {
		return 0, err
	}
	if opts.Pause <= 0 {
		opts.Pause = StepPause
	}
	sc, err := DisplayScale(ctx)
	if err != nil {
		return 0, err
	}
	r := runner{sc: sc, devtools: devtoolsOf(s.DevTools), note: opts.Note}
	for i, st := range s.Steps {
		if i > 0 && st.Do != "note" {
			select {
			case <-time.After(opts.Pause):
			case <-ctx.Done():
				return i, ctx.Err()
			}
		}
		if err := r.do(ctx, st); err != nil {
			return i, &StepError{Index: i + 1, Step: st, Err: err}
		}
	}
	return len(s.Steps), nil
}

// devtoolsOf is the browser's DevTools endpoint and any others a script names.
func devtoolsOf(more []string) []string {
	out := []string{DevTools}
	for _, d := range more {
		d = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(d), "http://"), "ws://")
		if d != "" && d != DevTools {
			out = append(out, strings.TrimSuffix(d, "/"))
		}
	}
	return out
}

type runner struct {
	sc       Scale
	devtools []string
	note     func(context.Context, string) error
}

func (r runner) do(ctx context.Context, st Step) error {
	switch st.Do {
	case "click":
		x, y, err := r.where(ctx, st)
		if err != nil {
			return err
		}
		return r.clickReal(ctx, x, y, st.Button, st.Double)
	case "move":
		x, y, err := r.where(ctx, st)
		if err != nil {
			return err
		}
		from, err := CursorPosition(ctx)
		if err != nil {
			return err
		}
		a, err := moveArgs(from.X, from.Y, x, y)
		if err != nil || a == nil {
			return err
		}
		_, err = xdotool(ctx, a...)
		return err
	case "type":
		if st.X != nil || st.Label != "" {
			x, y, err := r.where(ctx, st)
			if err != nil {
				return err
			}
			if err := r.clickReal(ctx, x, y, "", false); err != nil {
				return err
			}
		}
		a, err := typeArgs(st.Text)
		if err != nil {
			return err
		}
		if _, err := xdotool(ctx, a...); err != nil {
			return err
		}
		if strings.TrimSpace(st.Key) != "" {
			return pressKeys(ctx, st.Key)
		}
		return nil
	case "key":
		return pressKeys(ctx, st.Combo)
	case "scroll":
		x, y, err := r.where(ctx, st)
		if err != nil {
			return err
		}
		from, err := CursorPosition(ctx)
		if err != nil {
			return err
		}
		a, err := scrollArgs(from.X, from.Y, x, y, st.Direction, st.Amount)
		if err != nil {
			return err
		}
		_, err = xdotool(ctx, a...)
		return err
	case "wait":
		if st.Text != "" {
			return r.waitText(ctx, st.Text, st.timeout())
		}
		d := min(time.Duration(st.Seconds*float64(time.Second)), maxStepWait)
		select {
		case <-time.After(d):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	case "note":
		if r.note == nil {
			return nil
		}
		return r.note(ctx, strings.TrimSpace(st.Text))
	}
	return fmt.Errorf("%q isn't a step", st.Do)
}

func (st Step) timeout() time.Duration {
	if st.Timeout == nil || *st.Timeout <= 0 {
		return defaultTextWait
	}
	return min(time.Duration(*st.Timeout*float64(time.Second)), maxStepWait)
}

// where is the point on the real display a step aims at: its x and y, scaled
// from the screenshot's pixels, or the middle of the element its label names.
func (r runner) where(ctx context.Context, st Step) (int, int, error) {
	if st.X != nil && st.Y != nil {
		return r.sc.ToReal(*st.X, *st.Y)
	}
	return r.findLabel(ctx, st.Label)
}

func (r runner) clickReal(ctx context.Context, x, y int, button string, double bool) error {
	from, err := CursorPosition(ctx)
	if err != nil {
		return err
	}
	a, err := clickArgs(from.X, from.Y, x, y, button, double)
	if err != nil {
		return err
	}
	_, err = xdotool(ctx, a...)
	return err
}

// findLabelJS finds the element a label names in a page, scrolls it into view
// and answers with where its middle is and where the page is on the screen.
// An exact match beats a partial one, a control beats any other element, and
// a smaller element beats one containing it: the button, not the toolbar
// around it. An element under something else (a modal's backdrop, say) is
// skipped, since a click there wouldn't reach it.
const findLabelJS = `(label) => {
  if (document.visibilityState !== 'visible') return null;
  const norm = s => (s || '').replace(/\s+/g, ' ').trim().toLowerCase();
  const want = norm(label);
  const control = 'a,button,input,select,textarea,summary,label,[role=button],[role=link],[role=tab],[role=menuitem],[role=checkbox],[role=radio],[role=switch],[role=option],[tabindex],[onclick]';
  const names = el => {
    const n = [el.getAttribute('aria-label'), el.getAttribute('title'), el.getAttribute('placeholder'), el.getAttribute('alt')];
    const by = el.getAttribute('aria-labelledby');
    if (by) n.push(by.split(/\s+/).map(id => (document.getElementById(id) || {}).innerText).join(' '));
    if (el.labels && el.labels.length) n.push(el.labels[0].innerText);
    if (el.tagName === 'INPUT' && /^(button|submit|reset)$/.test(el.type)) n.push(el.value);
    n.push(el.innerText);
    return n.map(norm).filter(Boolean);
  };
  const found = [];
  for (const el of document.querySelectorAll('body *')) {
    const ns = names(el);
    const exact = ns.includes(want), part = !exact && ns.some(n => n.includes(want));
    if (!exact && !part) continue;
    const r = el.getBoundingClientRect();
    if (r.width < 1 || r.height < 1) continue;
    if (el.checkVisibility && !el.checkVisibility({visibilityProperty: true, opacityProperty: true})) continue;
    found.push({el, score: (exact ? 2 : 0) + (el.matches(control) ? 1 : 0), area: r.width * r.height});
  }
  found.sort((a, b) => b.score - a.score || a.area - b.area);
  for (const {el} of found.slice(0, 20)) {
    let r = el.getBoundingClientRect();
    if (r.top < 0 || r.left < 0 || r.bottom > innerHeight || r.right > innerWidth) {
      el.scrollIntoView({block: 'center', inline: 'center', behavior: 'instant'});
      r = el.getBoundingClientRect();
    }
    const x = r.left + r.width / 2, y = r.top + r.height / 2;
    const hit = document.elementFromPoint(x, y);
    if (!hit || !(el === hit || el.contains(hit) || hit.contains(el))) continue;
    return {x, y, sx: screenX, sy: screenY, ow: outerWidth, iw: innerWidth, oh: outerHeight, ih: innerHeight, dpr: devicePixelRatio, title: document.title};
  }
  return null;
}`

// hasTextJS is whether a page shows some text.
const hasTextJS = `(text) => {
  if (document.visibilityState !== 'visible' || !document.body) return false;
  const norm = s => (s || '').replace(/\s+/g, ' ').trim().toLowerCase();
  return norm(document.body.innerText).includes(norm(text));
}`

// pagePoint is where findLabelJS found an element: its middle in the page's
// CSS pixels, and the page's window.
type pagePoint struct {
	X, Y   float64
	SX, SY float64
	OW, IW float64
	OH, IH float64
	DPR    float64
	Title  string
}

// screen is the point on the real display, given where the page's window is
// on it. The page sits at the bottom of its window, below the tab strip and
// the toolbar, so its top is the window's height less the page's; a window
// has no side borders worth splitting.
func (p pagePoint) screen(winX, winY int) (int, int) {
	dpr := p.DPR
	if dpr <= 0 {
		dpr = 1
	}
	x := float64(winX) + ((p.OW-p.IW)/2+p.X)*dpr
	y := float64(winY) + ((p.OH-p.IH)+p.Y)*dpr
	return int(x + 0.5), int(y + 0.5)
}

// window is where the page's window is on the real display. The page's own
// screenX and screenY won't do: an Electron window in the window manager's
// frame reports the frame's corner, a title bar above its own. So it is the
// X window with the page's title in its name and the page's window's size,
// and screenX and screenY only when there is none.
func (p pagePoint) window(windows []Window) (int, int) {
	dpr := p.DPR
	if dpr <= 0 {
		dpr = 1
	}
	w, h := int(p.OW*dpr+0.5), int(p.OH*dpr+0.5)
	for _, win := range windows {
		if win.hasGeometry && win.W == w && win.H == h && strings.Contains(win.Name, p.Title) {
			return win.X, win.Y
		}
	}
	return int(p.SX*dpr + 0.5), int(p.SY*dpr + 0.5)
}

// findLabel is the middle of the element a label names, on the real display,
// looked for in every page each DevTools endpoint has that is showing.
func (r runner) findLabel(ctx context.Context, label string) (int, int, error) {
	var p *pagePoint
	reached, err := r.evaluate(ctx, findLabelJS, label, func(raw json.RawMessage) bool {
		var got *pagePoint
		if json.Unmarshal(raw, &got) == nil && got != nil {
			p = got
			return true
		}
		return false
	})
	if err != nil {
		return 0, 0, err
	}
	if p == nil {
		if reached == 0 {
			return 0, 0, fmt.Errorf("can't look for %q: no page is open in the browser%s; use x and y for anything outside a page", label, r.others())
		}
		return 0, 0, fmt.Errorf("nothing visible is labelled %q on the page", label)
	}
	windows, _ := Windows(ctx)
	x, y := p.screen(p.window(windows))
	if err := point(x, y); err != nil || x >= r.sc.Real.Width || y >= r.sc.Real.Height {
		return 0, 0, fmt.Errorf("%q is at (%d, %d), off the display", label, x, y)
	}
	return x, y, nil
}

func (r runner) others() string {
	if len(r.devtools) > 1 {
		return " or at " + strings.Join(r.devtools[1:], ", ")
	}
	return ""
}

// waitText waits until a page shows text, looking five times a second.
func (r runner) waitText(ctx context.Context, text string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		found := false
		_, err := r.evaluate(ctx, hasTextJS, text, func(raw json.RawMessage) bool {
			var yes bool
			found = json.Unmarshal(raw, &yes) == nil && yes
			return found
		})
		if found {
			return nil
		}
		if err != nil && !errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		select {
		case <-time.After(200 * time.Millisecond):
		case <-ctx.Done():
			if ctx.Err() == context.DeadlineExceeded {
				return fmt.Errorf("%q didn't appear on a page within %s", text, timeout)
			}
			return ctx.Err()
		}
	}
}

// evaluate calls fn(arg) in each page of each DevTools endpoint until take
// accepts an answer, and says how many pages it reached. An endpoint that
// isn't there is skipped: an Electron app named in a script may not be open.
func (r runner) evaluate(ctx context.Context, fn, arg string, take func(json.RawMessage) bool) (int, error) {
	a, err := json.Marshal(arg)
	if err != nil {
		return 0, err
	}
	expr := "(" + fn + ")(" + string(a) + ")"
	reached := 0
	for _, dt := range r.devtools {
		lctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		pages, err := listPages(lctx, dt)
		cancel()
		if err != nil {
			continue
		}
		for _, pg := range pages {
			if strings.HasPrefix(pg.URL, "chrome://") || strings.HasPrefix(pg.URL, "devtools://") {
				continue // the browser's own UI: the omnibox's popup is a page too
			}
			raw, err := evaluateIn(ctx, pg.WebSocket, expr)
			if err != nil {
				continue
			}
			reached++
			if take(raw) {
				return reached, nil
			}
		}
	}
	return reached, ctx.Err()
}

func evaluateIn(ctx context.Context, ws, expr string) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	c, err := dialCDP(ctx, ws, nil)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	var res struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	if err := c.call(ctx, "Runtime.evaluate", map[string]any{"expression": expr, "returnByValue": true}, &res); err != nil {
		return nil, err
	}
	if res.ExceptionDetails != nil {
		return nil, errors.New(res.ExceptionDetails.Text)
	}
	return res.Result.Value, nil
}
