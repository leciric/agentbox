package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agentbox/internal/mcp"
)

// Tools are everything the agent's AI tool can do to its display. They are
// deliberately few and low-level: this is a mouse, a keyboard and a camera,
// and the model composes them. Anything inside a web page is better done with
// the Playwright MCP server, which sees the DOM; these see pixels.
func Tools(ctx context.Context) []mcp.Tool {
	return []mcp.Tool{
		{
			Name: "screenshot",
			Description: "Look at the whole virtual display: every window, the panel and the mouse cursor, " +
				"not just a browser page. The actions answer with a screenshot of what they led to, so take " +
				"one yourself only to start, or after something changed on its own. Give coordinates in " +
				"this image's pixels: the tools scale them to the display.",
			RunContent: func(json.RawMessage) ([]mcp.Content, error) {
				jpg, sc, err := Screenshot(ctx)
				if err != nil {
					return nil, err
				}
				return shotContent(jpg, sc, ""), nil
			},
		},
		{
			Name:        "mouse_move",
			Description: "Move the mouse pointer, without clicking. The pointer travels visibly rather than jumping, so use it to hover something, or to show the user where you are about to click in a recording." + afterAction,
			Schema:      object([]string{"x", "y"}, merge(point2props("x", "y"), lookProp())),
			RunContent: act(ctx, func(ctx context.Context, sc Scale, in args) (string, error) {
				x, y, err := sc.ToReal(in.X, in.Y)
				if err != nil {
					return "", err
				}
				from, err := CursorPosition(ctx)
				if err != nil {
					return "", err
				}
				if a := moveSteps(from.X, from.Y, x, y); a != nil {
					if _, err := xdotool(ctx, a...); err != nil {
						return "", err
					}
				}
				return fmt.Sprintf("The pointer is at (%d, %d).", in.X, in.Y), nil
			}),
		},
		{
			Name:        "click",
			Description: "Click at a point on the display, in the pixels of the last screenshot. The pointer travels there visibly rather than jumping before it clicks." + afterAction,
			Schema: object([]string{"x", "y"}, merge(point2props("x", "y"), merge(map[string]any{
				"button": choice("which button; left by default", "left", "right", "middle"),
				"double": boolean("double-click instead of a single click, to open a file or select a word"),
			}, lookProp()))),
			RunContent: act(ctx, func(ctx context.Context, sc Scale, in args) (string, error) {
				if err := click(ctx, sc, in.X, in.Y, in.Button, in.Double); err != nil {
					return "", err
				}
				return fmt.Sprintf("%s at (%d, %d).", describeClick(in.Button, in.Double), in.X, in.Y), nil
			}),
		},
		{
			Name:        "drag",
			Description: "Press the mouse button at one point, move to another and release: drag a file, a window's titlebar, a scrollbar, or a selection. The pointer travels visibly both getting there and dragging." + afterAction,
			Schema: object([]string{"from_x", "from_y", "to_x", "to_y"}, merge(map[string]any{
				"from_x": integer("where the drag starts, in the screenshot's pixels"),
				"from_y": integer("where the drag starts, in the screenshot's pixels"),
				"to_x":   integer("where it ends"),
				"to_y":   integer("where it ends"),
				"button": choice("which button; left by default", "left", "right", "middle"),
			}, lookProp())),
			RunContent: act(ctx, func(ctx context.Context, sc Scale, in args) (string, error) {
				fromX, fromY, err := sc.ToReal(in.FromX, in.FromY)
				if err != nil {
					return "", err
				}
				toX, toY, err := sc.ToReal(in.ToX, in.ToY)
				if err != nil {
					return "", err
				}
				cur, err := CursorPosition(ctx)
				if err != nil {
					return "", err
				}
				a, err := dragArgs(cur.X, cur.Y, fromX, fromY, toX, toY, in.Button)
				if err != nil {
					return "", err
				}
				if _, err := xdotool(ctx, a...); err != nil {
					return "", err
				}
				return fmt.Sprintf("Dragged from (%d, %d) to (%d, %d).", in.FromX, in.FromY, in.ToX, in.ToY), nil
			}),
		},
		{
			Name:        "scroll",
			Description: "Turn the mouse wheel over a point. The pointer travels there visibly rather than jumping. Whatever is under the pointer scrolls, so aim at the list or pane you mean." + afterAction,
			Schema: object([]string{"x", "y", "direction"}, merge(point2props("x", "y"), merge(map[string]any{
				"direction": choice("which way to scroll", "up", "down", "left", "right"),
				"amount":    integer("how many notches of the wheel; 3 by default, up to 50"),
			}, lookProp()))),
			RunContent: act(ctx, func(ctx context.Context, sc Scale, in args) (string, error) {
				x, y, err := sc.ToReal(in.X, in.Y)
				if err != nil {
					return "", err
				}
				from, err := CursorPosition(ctx)
				if err != nil {
					return "", err
				}
				a, err := scrollArgs(from.X, from.Y, x, y, in.Direction, in.Amount)
				if err != nil {
					return "", err
				}
				if _, err := xdotool(ctx, a...); err != nil {
					return "", err
				}
				return fmt.Sprintf("Scrolled %s at (%d, %d).", strings.ToLower(in.Direction), in.X, in.Y), nil
			}),
		},
		{
			// A field is filled in one call rather than three: click it, type,
			// press Return, each a model request carrying the whole conversation.
			Name: "type",
			Description: "Type text into whatever has the keyboard focus, or give x and y to click a field first. " +
				"This types characters; key presses a key afterwards, like Return to submit or Tab to move on, " +
				"and the key tool presses keys on their own." + afterAction,
			Schema: object([]string{"text"}, merge(map[string]any{
				"text": str("the text to type"),
				"x":    integer("optional: click here first, in the screenshot's pixels, to focus the field"),
				"y":    integer("optional: click here first"),
				"key":  str("optional: a key to press after typing, like Return or Tab"),
			}, lookProp())),
			RunContent: act(ctx, func(ctx context.Context, sc Scale, in args) (string, error) {
				a, err := typeArgs(in.Text)
				if err != nil {
					return "", err
				}
				if in.hasX != in.hasY {
					return "", fmt.Errorf("give both x and y to click a field first, or neither")
				}
				done := ""
				if in.hasX {
					if err := click(ctx, sc, in.X, in.Y, "", false); err != nil {
						return "", err
					}
					done = fmt.Sprintf("Clicked at (%d, %d), then typed", in.X, in.Y)
				}
				if _, err := xdotool(ctx, a...); err != nil {
					return "", err
				}
				if done == "" {
					done = "Typed"
				}
				done = fmt.Sprintf("%s %d character(s)", done, len([]rune(in.Text)))
				if strings.TrimSpace(in.Key) != "" {
					if err := pressKeys(ctx, in.Key); err != nil {
						return "", fmt.Errorf("%s, but: %w", done, err)
					}
					done += " and pressed " + strings.TrimSpace(in.Key)
				}
				return done + ".", nil
			}),
		},
		{
			Name: "key",
			Description: "Press a key or a combination: Return, Escape, ctrl+l, ctrl+shift+t, alt+Tab, super. " +
				"Several separated by spaces are pressed in order. Key names are X keysyms, so a letter is " +
				"its letter and a named key is capitalised (Return, Tab, BackSpace, Escape, Up, Down)." + afterAction,
			Schema: object([]string{"combo"}, merge(map[string]any{"combo": str("the key or combination, like ctrl+l or Return")}, lookProp())),
			RunContent: act(ctx, func(ctx context.Context, _ Scale, in args) (string, error) {
				if err := pressKeys(ctx, in.Combo); err != nil {
					return "", err
				}
				return "Pressed " + strings.TrimSpace(in.Combo) + ".", nil
			}),
		},
		{
			Name:        "windows",
			Description: "List the visible windows: their id, title and where each one is on the display. Use it to find what to focus, or to check that something you opened really appeared.",
			Run: func(json.RawMessage) (string, error) {
				windows, err := Windows(ctx)
				if err != nil {
					return "", err
				}
				sc, err := DisplayScale(ctx)
				if err != nil {
					return "", err
				}
				return describeWindows(windows, sc), nil
			},
		},
		{
			Name:        "focus",
			Description: "Raise a window and give it the keyboard focus, so type and key go to it. Name it by the id windows reports, or by part of its title.",
			Schema:      object([]string{"window"}, map[string]any{"window": str("a window id from windows, or part of its title, like \"Terminal\"")}),
			Run: run1(ctx, func(ctx context.Context, in args) (string, error) {
				w, err := Focus(ctx, in.Window)
				if err != nil {
					return "", err
				}
				sc, err := DisplayScale(ctx)
				if err != nil {
					return "", err
				}
				w = shownWindow(w, sc)
				return fmt.Sprintf("Focused %q (id %s), at (%d, %d), %d×%d.", w.Name, w.ID, w.X, w.Y, w.W, w.H), nil
			}),
		},
		{
			// Waiting answers with the screenshot that always follows it. Every
			// call costs a model request carrying the whole conversation, so a
			// wait that only waited made every "let it paint, then look" two
			// of them (D83).
			Name: "wait",
			Description: fmt.Sprintf("Wait for the display to catch up — a window opening, a menu appearing, a page painting — and "+
				"get a screenshot of it afterwards, so there is no need to take one yourself. At most %d seconds; for anything "+
				"longer, do the waiting in the shell.", int(maxWait.Seconds())),
			Schema: object([]string{"seconds"}, map[string]any{"seconds": number("how long to wait")}),
			RunContent: func(raw json.RawMessage) ([]mcp.Content, error) {
				var in args
				if len(raw) > 0 {
					if err := json.Unmarshal(raw, &in); err != nil {
						return nil, err
					}
				}
				d, err := waitFor(in.Seconds)
				if err != nil {
					return nil, err
				}
				select {
				case <-time.After(d):
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				jpg, sc, err := Screenshot(ctx)
				if err != nil {
					return nil, err
				}
				return shotContent(jpg, sc, fmt.Sprintf("Waited %s. ", d)), nil
			},
		},
		{
			Name:        "cursor",
			Description: "Where the mouse pointer is now.",
			Run: func(json.RawMessage) (string, error) {
				c, err := CursorPosition(ctx)
				if err != nil {
					return "", err
				}
				sc, err := DisplayScale(ctx)
				if err != nil {
					return "", err
				}
				x, y := sc.ToShown(c.X, c.Y)
				return fmt.Sprintf("The pointer is at (%d, %d), over window %s.", x, y, c.Window), nil
			},
		},
	}
}

// afterAction ends the description of every tool that does something.
const afterAction = " Answers with a screenshot once the screen has stopped changing, so there is no need to take " +
	"one yourself; pass screenshot false while chaining steps whose results you don't need to see."

func lookProp() map[string]any {
	return map[string]any{"screenshot": boolean("answer with a screenshot of the result; true by default")}
}

// act runs an action with the display's scale, and answers with a screenshot
// of what it led to, taken once the screen has settled. Every call costs a
// model request carrying the whole conversation, and an action is nearly
// always followed by a look, so answering with one halves the requests (D83).
func act(ctx context.Context, fn func(context.Context, Scale, args) (string, error)) func(json.RawMessage) ([]mcp.Content, error) {
	return func(raw json.RawMessage) ([]mcp.Content, error) {
		in, err := decodeArgs(raw)
		if err != nil {
			return nil, err
		}
		sc, err := DisplayScale(ctx)
		if err != nil {
			return nil, err
		}
		done, err := fn(ctx, sc, in)
		if err != nil {
			return nil, err
		}
		if in.Screenshot != nil && !*in.Screenshot {
			return []mcp.Content{mcp.Text(done)}, nil
		}
		jpg, sc, err := SettledScreenshot(ctx)
		if err != nil {
			return []mcp.Content{mcp.Text(done + " The screenshot afterwards failed: " + err.Error())}, nil
		}
		return shotContent(jpg, sc, done+" "), nil
	}
}

// click moves the pointer to a point in the screenshot's pixels and clicks.
func click(ctx context.Context, sc Scale, x, y int, button string, double bool) error {
	rx, ry, err := sc.ToReal(x, y)
	if err != nil {
		return err
	}
	from, err := CursorPosition(ctx)
	if err != nil {
		return err
	}
	a, err := clickArgs(from.X, from.Y, rx, ry, button, double)
	if err != nil {
		return err
	}
	_, err = xdotool(ctx, a...)
	return err
}

// shotContent is a screenshot as a tool answers with it: the image, and a
// note saying which pixels coordinates are given in. before is said first,
// for a tool that did something before looking.
func shotContent(jpg []byte, sc Scale, before string) []mcp.Content {
	note := fmt.Sprintf("The screenshot is %s: give coordinates in its pixels.", sc.Shown)
	return []mcp.Content{mcp.Image(jpg, "image/jpeg"), mcp.Text(before + note)}
}

// args is every argument any of these tools takes. One struct keeps the
// decoding in one place; each tool reads only the fields its schema offers.
type args struct {
	X, Y                int
	FromX               int `json:"from_x"`
	FromY               int `json:"from_y"`
	ToX                 int `json:"to_x"`
	ToY                 int `json:"to_y"`
	Button, Direction   string
	Amount              int
	Double              bool
	Text, Combo, Window string
	Key                 string
	Seconds             float64
	Screenshot          *bool // nil is true
	// hasX and hasY say whether x and y were given at all, which type's
	// optional field to click first needs to tell apart from (0, 0).
	hasX, hasY bool
}

// decodeArgs reads a tool's arguments.
func decodeArgs(raw json.RawMessage) (args, error) {
	var in args
	if len(raw) == 0 {
		return in, nil
	}
	var given struct{ X, Y *int }
	if err := json.Unmarshal(raw, &in); err != nil {
		return in, err
	}
	if err := json.Unmarshal(raw, &given); err != nil {
		return in, err
	}
	in.hasX, in.hasY = given.X != nil, given.Y != nil
	return in, nil
}

// run1 decodes a tool's arguments and hands them to fn.
func run1(ctx context.Context, fn func(context.Context, args) (string, error)) func(json.RawMessage) (string, error) {
	return func(raw json.RawMessage) (string, error) {
		var in args
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &in); err != nil {
				return "", err
			}
		}
		return fn(ctx, in)
	}
}

func describeClick(button string, double bool) string {
	name := strings.ToLower(strings.TrimSpace(button))
	if name == "" {
		name = "left"
	}
	if double {
		return "Double-clicked the " + name + " button"
	}
	return "Clicked the " + name + " button"
}

// shownWindow is a window's geometry in the screenshot's pixels.
func shownWindow(w Window, sc Scale) Window {
	x2, y2 := sc.ToShown(w.X+w.W, w.Y+w.H)
	w.X, w.Y = sc.ToShown(w.X, w.Y)
	w.W, w.H = x2-w.X, y2-w.Y
	return w
}

func describeWindows(windows []Window, sc Scale) string {
	if len(windows) == 0 {
		return "No windows are open on the display. The browser may still be starting: wait a second and look again."
	}
	var b strings.Builder
	b.WriteString("Visible windows, in the order X lists them:\n")
	for _, w := range windows {
		w = shownWindow(w, sc)
		fmt.Fprintf(&b, "- %s — %s", w.ID, w.Name)
		if w.hasGeometry {
			fmt.Fprintf(&b, " (at %d,%d, %d×%d)", w.X, w.Y, w.W, w.H)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// The schema helpers below build the small JSON Schemas these tools need.
// internal/cli has its own set for the project chat's tools; duplicating five
// one-line constructors is cheaper than exporting them from a command package.

func object(required []string, props map[string]any) map[string]any {
	schema := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func merge(props, more map[string]any) map[string]any {
	for name, schema := range more {
		props[name] = schema
	}
	return props
}

func str(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func integer(description string) map[string]any {
	return map[string]any{"type": "integer", "description": description}
}

func number(description string) map[string]any {
	return map[string]any{"type": "number", "description": description}
}

func boolean(description string) map[string]any {
	return map[string]any{"type": "boolean", "description": description}
}

func choice(description string, values ...string) map[string]any {
	return map[string]any{"type": "string", "description": description, "enum": values}
}

func point2props(x, y string) map[string]any {
	return map[string]any{
		x: integer("how far across, in the screenshot's pixels, from the left"),
		y: integer("how far down, in the screenshot's pixels, from the top"),
	}
}
