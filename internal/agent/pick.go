package agent

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// The element picker: while you watch an agent's browser, you pick an element
// on its page to send to a chat. The daemon puts an overlay on the page the
// browser shows (picker.js), over the DevTools protocol it already reaches
// the browser by, waits for the click and crops the element from a
// screenshot. The overlay runs in an isolated world, apart from the page's
// own scripts, and is the only thing the picker adds to the page.

//go:embed picker.js
var pickerScript string

// pickWorld names the isolated world the overlay runs in.
const pickWorld = "agentbox-picker"

// pickPoll is how often the daemon asks the page whether something was picked.
const pickPoll = 150 * time.Millisecond

// pickResult asks the overlay how it is doing: null when it isn't on the page
// (a page that has since navigated), 0 while waiting, false when cancelled,
// and the element picked otherwise.
const pickResult = `(p => !p ? null : p.result === undefined ? 0 : p.result === null ? false : p.result)(globalThis.__agentboxPicker)`

// PickElement shows the picker on the page the agent's browser shows and
// waits for an element to be clicked. It returns nil, and no error, when the
// picker is cancelled: with Escape on the page, or by ending ctx.
func (m *Manager) PickElement(ctx context.Context, a state.Agent, hint string) (*api.ElementPick, error) {
	status, err := m.BrowserStatus(ctx, a)
	if err != nil {
		return nil, err
	}
	if !status.Running || len(status.Pages) == 0 {
		return nil, fmt.Errorf("%s's browser has no page open to pick from", a.Ref())
	}
	return pickElement(ctx, m.devtools(a), status.Pages, hint)
}

func pickElement(ctx context.Context, client *http.Client, pages []BrowserPage, hint string) (*api.ElementPick, error) {
	s, err := visiblePage(ctx, client, pages)
	if err != nil {
		return nil, err
	}
	defer func() { _ = s.conn.CloseNow() }()
	// The connection's calls never run on ctx: the websocket closes when the
	// context of a read in flight ends, and cancelling must still reach the
	// page to take the overlay off it. ctx is watched between calls instead.
	call := func(timeout time.Duration) (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.WithoutCancel(ctx), timeout)
	}
	evaluate := func(world int, expression string) (json.RawMessage, error) {
		c, cancel := call(5 * time.Second)
		defer cancel()
		return s.evaluate(c, world, expression)
	}
	install := func(script string) (int, error) {
		c, cancel := call(10 * time.Second)
		defer cancel()
		return s.installPicker(c, script)
	}
	hintJSON, _ := json.Marshal(hint)
	script := fmt.Sprintf("(%s)(%s)", strings.TrimRight(strings.TrimSpace(pickerScript), ";"), hintJSON)

	world, err := install(script)
	if err != nil {
		return nil, err
	}
	tick := time.NewTicker(pickPoll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			// Take the overlay off the page.
			_, _ = evaluate(world, "globalThis.__agentboxPicker?.cancel()")
			return nil, nil
		case <-tick.C:
		}
		value, err := evaluate(world, pickResult)
		var cdp *devtoolsError
		switch {
		case errors.As(err, &cdp) || string(value) == "null":
			// The page navigated, taking the world and the overlay with it:
			// put them on the new page, as soon as it has a document.
			if w, err := install(script); err == nil {
				world = w
			}
			continue
		case err != nil:
			return nil, fmt.Errorf("the browser stopped answering: %w", err)
		case string(value) == "0":
			continue
		case string(value) == "false":
			return nil, nil
		}
		var picked struct {
			api.ElementPick
			Scroll struct{ X, Y float64 }
		}
		if err := json.Unmarshal(value, &picked); err != nil {
			return nil, fmt.Errorf("reading the element picked: %w", err)
		}
		el := picked.ElementPick
		// Without its picture the element is still worth sending.
		shot, cancel := call(15 * time.Second)
		el.Image, _ = s.crop(shot, el.Box, el.Viewport, picked.Scroll.X, picked.Scroll.Y)
		cancel()
		return &el, nil
	}
}

// visiblePage connects to the page the browser shows: the one whose document
// is visible, or the most recently used when none says so.
func visiblePage(ctx context.Context, client *http.Client, pages []BrowserPage) (*devtoolsSession, error) {
	var first *devtoolsSession
	for _, page := range pages {
		conn, _, err := websocketDial(ctx, page.webSocket, client)
		if err != nil {
			continue
		}
		conn.SetReadLimit(32 << 20)
		s := &devtoolsSession{conn: conn}
		value, err := s.evaluate(ctx, 0, "document.visibilityState")
		if err == nil && string(value) == `"visible"` {
			if first != nil {
				_ = first.conn.CloseNow()
			}
			return s, nil
		}
		if first == nil {
			first = s
		} else {
			_ = conn.CloseNow()
		}
	}
	if first == nil {
		return nil, errors.New("couldn't connect to the browser's page")
	}
	return first, nil
}

// installPicker makes an isolated world on the page's main frame and puts the
// overlay in it, returning the world's execution context.
func (s *devtoolsSession) installPicker(ctx context.Context, script string) (int, error) {
	var tree struct {
		FrameTree struct {
			Frame struct{ ID string }
		}
	}
	if err := s.call(ctx, "Page.getFrameTree", map[string]any{}, &tree); err != nil {
		return 0, err
	}
	var world struct{ ExecutionContextID int }
	if err := s.call(ctx, "Page.createIsolatedWorld", map[string]any{"frameId": tree.FrameTree.Frame.ID, "worldName": pickWorld}, &world); err != nil {
		return 0, err
	}
	if _, err := s.evaluate(ctx, world.ExecutionContextID, script); err != nil {
		return 0, err
	}
	return world.ExecutionContextID, nil
}

// evaluate runs expression in the execution context world (the page's own
// for 0) and returns its value as JSON. An exception thrown is an error.
func (s *devtoolsSession) evaluate(ctx context.Context, world int, expression string) (json.RawMessage, error) {
	params := map[string]any{"expression": expression, "returnByValue": true}
	if world != 0 {
		params["contextId"] = world
	}
	var out struct {
		Result struct {
			Type  string
			Value json.RawMessage
		}
		ExceptionDetails *struct {
			Text      string
			Exception struct{ Description string }
		}
	}
	if err := s.call(ctx, "Runtime.evaluate", params, &out); err != nil {
		return nil, err
	}
	if e := out.ExceptionDetails; e != nil {
		message := e.Exception.Description
		if message == "" {
			message = e.Text
		}
		return nil, &devtoolsError{method: "Runtime.evaluate", message: message}
	}
	if out.Result.Type == "undefined" || len(out.Result.Value) == 0 {
		return json.RawMessage("null"), nil
	}
	return out.Result.Value, nil
}

// cropMargin is how much of what is around the element its picture shows.
const cropMargin = 8

// crop is the element's box, and a little around it, from a screenshot of
// the page as it is shown: the part of the element in view, if it is
// larger. A PNG when that fits a chat's limit for a picture, a JPEG otherwise.
func (s *devtoolsSession) crop(ctx context.Context, box api.ElementBox, viewport api.ElementSize, scrollX, scrollY float64) (*api.ChatImageUpload, error) {
	x0, y0 := math.Max(0, box.X-cropMargin), math.Max(0, box.Y-cropMargin)
	x1, y1 := math.Min(viewport.Width, box.X+box.Width+cropMargin), math.Min(viewport.Height, box.Y+box.Height+cropMargin)
	if x1-x0 < 1 || y1-y0 < 1 {
		return nil, errors.New("the element isn't in view")
	}
	// The clip is in the document's coordinates; the box, the viewport's.
	clip := map[string]any{"x": x0 + scrollX, "y": y0 + scrollY, "width": x1 - x0, "height": y1 - y0, "scale": 1}
	for _, format := range []map[string]any{{"format": "png"}, {"format": "jpeg", "quality": 80}} {
		params := map[string]any{"clip": clip}
		for k, v := range format {
			params[k] = v
		}
		var shot struct{ Data string }
		if err := s.call(ctx, "Page.captureScreenshot", params, &shot); err != nil {
			return nil, err
		}
		if base64.StdEncoding.DecodedLen(len(shot.Data)) <= api.MaxChatImageBytes {
			return &api.ChatImageUpload{MimeType: "image/" + format["format"].(string), Data: shot.Data, Name: "element." + format["format"].(string)}, nil
		}
	}
	return nil, errors.New("the element's picture is too big to send")
}

// devtoolsError is the browser refusing a command, rather than not being
// reached: the connection is still good.
type devtoolsError struct{ method, message string }

func (e *devtoolsError) Error() string { return e.method + ": " + e.message }
