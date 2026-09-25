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
				"not just a browser page. Take one before clicking anywhere you haven't already seen — " +
				"coordinates from a stale screenshot click the wrong thing. The image is scaled down to " +
				"fit, and the reply says how big the display really is: give coordinates in real display " +
				"pixels, not in the pixels of the image you were shown.",
			RunContent: func(json.RawMessage) ([]mcp.Content, error) {
				return screenshotContent(ctx, "")
			},
		},
		{
			Name:        "mouse_move",
			Description: "Move the mouse pointer, without clicking. The pointer travels visibly rather than jumping, so use it to hover something, or to show the user where you are about to click in a recording.",
			Schema:      point2("x", "y"),
			Run: run1(ctx, func(ctx context.Context, in args) (string, error) {
				from, err := CursorPosition(ctx)
				if err != nil {
					return "", err
				}
				a, err := moveArgs(from.X, from.Y, in.X, in.Y)
				if err != nil {
					return "", err
				}
				if a != nil {
					if _, err := xdotool(ctx, a...); err != nil {
						return "", err
					}
				}
				return fmt.Sprintf("The pointer is at (%d, %d).", in.X, in.Y), nil
			}),
		},
		{
			Name:        "click",
			Description: "Click at a point on the display. The pointer travels there visibly rather than jumping before it clicks. Take a screenshot first, so you know what is there.",
			Schema: object([]string{"x", "y"}, merge(point2props("x", "y"), map[string]any{
				"button": choice("which button; left by default", "left", "right", "middle"),
				"double": boolean("double-click instead of a single click, to open a file or select a word"),
			})),
			Run: run1(ctx, func(ctx context.Context, in args) (string, error) {
				from, err := CursorPosition(ctx)
				if err != nil {
					return "", err
				}
				a, err := clickArgs(from.X, from.Y, in.X, in.Y, in.Button, in.Double)
				if err != nil {
					return "", err
				}
				if _, err := xdotool(ctx, a...); err != nil {
					return "", err
				}
				return fmt.Sprintf("%s at (%d, %d).", describeClick(in.Button, in.Double), in.X, in.Y), nil
			}),
		},
		{
			Name:        "drag",
			Description: "Press the mouse button at one point, move to another and release: drag a file, a window's titlebar, a scrollbar, or a selection. The pointer travels visibly both getting there and dragging.",
			Schema: object([]string{"from_x", "from_y", "to_x", "to_y"}, map[string]any{
				"from_x": integer("where the drag starts, in display pixels"),
				"from_y": integer("where the drag starts, in display pixels"),
				"to_x":   integer("where it ends"),
				"to_y":   integer("where it ends"),
				"button": choice("which button; left by default", "left", "right", "middle"),
			}),
			Run: run1(ctx, func(ctx context.Context, in args) (string, error) {
				cur, err := CursorPosition(ctx)
				if err != nil {
					return "", err
				}
				a, err := dragArgs(cur.X, cur.Y, in.FromX, in.FromY, in.ToX, in.ToY, in.Button)
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
			Description: "Turn the mouse wheel over a point. The pointer travels there visibly rather than jumping. Whatever is under the pointer scrolls, so aim at the list or pane you mean.",
			Schema: object([]string{"x", "y", "direction"}, merge(point2props("x", "y"), map[string]any{
				"direction": choice("which way to scroll", "up", "down", "left", "right"),
				"amount":    integer("how many notches of the wheel; 3 by default, up to 50"),
			})),
			Run: run1(ctx, func(ctx context.Context, in args) (string, error) {
				from, err := CursorPosition(ctx)
				if err != nil {
					return "", err
				}
				a, err := scrollArgs(from.X, from.Y, in.X, in.Y, in.Direction, in.Amount)
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
			Name: "type",
			Description: "Type text into whatever has the keyboard focus. Click the field first. This types " +
				"characters; for Return, Tab, Escape or any combination with a modifier, use key.",
			Schema: object([]string{"text"}, map[string]any{"text": str("the text to type")}),
			Run: run1(ctx, func(ctx context.Context, in args) (string, error) {
				a, err := typeArgs(in.Text)
				if err != nil {
					return "", err
				}
				if _, err := xdotool(ctx, a...); err != nil {
					return "", err
				}
				return fmt.Sprintf("Typed %d character(s).", len([]rune(in.Text))), nil
			}),
		},
		{
			Name: "key",
			Description: "Press a key or a combination: Return, Escape, ctrl+l, ctrl+shift+t, alt+Tab, super. " +
				"Several separated by spaces are pressed in order. Key names are X keysyms, so a letter is " +
				"its letter and a named key is capitalised (Return, Tab, BackSpace, Escape, Up, Down).",
			Schema: object([]string{"combo"}, map[string]any{"combo": str("the key or combination, like ctrl+l or Return")}),
			Run: run1(ctx, func(ctx context.Context, in args) (string, error) {
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
				return describeWindows(windows), nil
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
				return screenshotContent(ctx, fmt.Sprintf("Waited %s. ", d))
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
				return fmt.Sprintf("The pointer is at (%d, %d), over window %s.", c.X, c.Y, c.Window), nil
			},
		},
	}
}

// screenshotContent is a screenshot of the display as a tool answers with it:
// the image, and a note saying which pixels coordinates are given in. before
// is said first, for a tool that did something before looking.
func screenshotContent(ctx context.Context, before string) ([]mcp.Content, error) {
	png, real, shown, err := Screenshot(ctx)
	if err != nil {
		return nil, err
	}
	note := fmt.Sprintf("The display is %s. Give coordinates in those pixels.", real)
	if shown != real {
		note = fmt.Sprintf("The display is %s; this image is scaled down to %s. Give coordinates in the display's %s, not the image's.", real, shown, real)
	}
	return []mcp.Content{mcp.Image(png, "image/png"), mcp.Text(before + note)}, nil
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
	Seconds             float64
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

func describeWindows(windows []Window) string {
	if len(windows) == 0 {
		return "No windows are open on the display. The browser may still be starting: wait a second and look again."
	}
	var b strings.Builder
	b.WriteString("Visible windows, in the order X lists them:\n")
	for _, w := range windows {
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
		x: integer("how far across the display, in its real pixels, from the left"),
		y: integer("how far down the display, in its real pixels, from the top"),
	}
}

func point2(x, y string) map[string]any {
	return object([]string{x, y}, point2props(x, y))
}
