package snap

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

// The accessibility summary: the window's AT-SPI tree on Linux, read from the
// accessibility bus, as indented "role: name" lines with the text of text
// fields. Apps expose it when the desktop's accessibility is on (GNOME and
// KDE turn it on for a screen reader; toolkit-accessibility in gsettings
// turns it on for GTK, and Chromium and Electron apps read it at start), so
// it is often missing, and a SnapShot goes without it rather than waiting:
// the whole walk has accessibilityTimeout.

const (
	accessibilityTimeout = 3 * time.Second
	maxNodes             = 600
	maxDepth             = 40
	maxSummaryBytes      = 12 << 10
	maxTextRunes         = 300
)

// node is one element of an accessibility tree.
type node interface {
	Role() string
	Name() string
	Text() string // a text field's or a document's text, or ""
	Children() []node
}

// Summarize renders a tree. Unnamed containers (panels, sections, fillers)
// add no line of their own and no indent, so what is left reads like the
// window does: its controls and its text. It stops at maxNodes visited or
// maxSummaryBytes written, and says so. A label repeating the name of the
// control it is in (a button's own caption) is left out too.
func Summarize(root node) string {
	var b strings.Builder
	visited, cut := 0, false
	var walk func(n node, depth, indent int, parent string)
	walk = func(n node, depth, indent int, parent string) {
		if cut {
			return
		}
		if visited++; visited > maxNodes || b.Len() > maxSummaryBytes {
			cut = true
			return
		}
		role, name, text := n.Role(), clean(n.Name()), clean(n.Text())
		if text == name {
			text = ""
		}
		line := ""
		switch {
		case role == "label" && name == parent && text == "":
		case name != "" && text != "":
			line = fmt.Sprintf("%s %q: %s", role, name, text)
		case name != "":
			line = fmt.Sprintf("%s %q", role, name)
		case text != "":
			line = role + ": " + text
		case !container(role):
			line = role
		}
		if line != "" {
			b.WriteString(strings.Repeat("  ", indent) + line + "\n")
			indent++
		}
		if depth >= maxDepth {
			return
		}
		for _, c := range n.Children() {
			walk(c, depth+1, indent, name)
		}
	}
	walk(root, 0, 0, "")
	if cut {
		b.WriteString("… (cut: the window has more)\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// container roles carry nothing of their own without a name.
func container(role string) bool {
	switch role {
	case "panel", "filler", "section", "grouping", "layered pane", "scroll pane", "viewport",
		"split pane", "internal frame", "redundant object", "unknown", "invalid", "table cell",
		"list item", "tool bar", "block quote", "landmark", "static", "paragraph", "image", "separator",
		"generic", "group", "tab panel": // GTK 4's unnamed boxes

		return true
	}
	return false
}

// clean folds whitespace and keeps a long text to its start.
func clean(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > maxTextRunes {
		s = string(r[:maxTextRunes]) + "…"
	}
	return s
}

// Accessibility summarizes the window of the app with pid (or, failing
// that, the app named like app) and the title, or "" when the accessibility
// bus doesn't answer, or has no such app.
func Accessibility(ctx context.Context, pid int, app, title string) string {
	ctx, cancel := context.WithTimeout(ctx, accessibilityTimeout)
	defer cancel()
	conn, err := a11yBus(ctx)
	if err != nil {
		return ""
	}
	defer func() { _ = conn.Close() }()
	root := &atspi{ctx: ctx, conn: conn, dest: "org.a11y.atspi.Registry", path: "/org/a11y/atspi/accessible/root"}
	apps := root.Children()
	var match node
	for _, a := range apps {
		if pid > 0 && a.(*atspi).pid() == uint32(pid) {
			match = a
			break
		}
	}
	if match == nil && app != "" {
		for _, a := range apps {
			if strings.EqualFold(a.Name(), app) {
				match = a
				break
			}
		}
	}
	if match == nil {
		return ""
	}
	// The app's frames are its windows: the one titled like ours, or its
	// only one.
	frames := match.Children()
	for _, f := range frames {
		if title != "" && clean(f.Name()) == clean(title) {
			return Summarize(f)
		}
	}
	if len(frames) == 1 {
		return Summarize(frames[0])
	}
	return ""
}

// a11yBus connects to the accessibility bus, whose address the session bus
// gives.
func a11yBus(ctx context.Context) (*dbus.Conn, error) {
	session, err := dbus.ConnectSessionBus(dbus.WithContext(ctx))
	if err != nil {
		return nil, err
	}
	defer func() { _ = session.Close() }()
	var addr string
	if err := session.Object("org.a11y.Bus", "/org/a11y/bus").CallWithContext(ctx, "org.a11y.Bus.GetAddress", 0).Store(&addr); err != nil {
		return nil, err
	}
	return dbus.Connect(addr, dbus.WithContext(ctx))
}

// atspi is a node on the accessibility bus. Every call shares the walk's
// deadline; a failed one reads as empty.
type atspi struct {
	ctx  context.Context
	conn *dbus.Conn
	dest string
	path dbus.ObjectPath
}

func (a *atspi) obj() dbus.BusObject { return a.conn.Object(a.dest, a.path) }

func (a *atspi) Role() string {
	var role string
	_ = a.obj().CallWithContext(a.ctx, "org.a11y.atspi.Accessible.GetRoleName", 0).Store(&role)
	return role
}

func (a *atspi) Name() string {
	v, err := a.obj().GetProperty("org.a11y.atspi.Accessible.Name")
	if err != nil {
		return ""
	}
	s, _ := v.Value().(string)
	return s
}

func (a *atspi) Text() string {
	v, err := a.obj().GetProperty("org.a11y.atspi.Text.CharacterCount")
	if err != nil {
		return ""
	}
	n, _ := v.Value().(int32)
	if n <= 0 {
		return ""
	}
	var text string
	_ = a.obj().CallWithContext(a.ctx, "org.a11y.atspi.Text.GetText", 0, int32(0), min(n, maxTextRunes*4)).Store(&text)
	return text
}

func (a *atspi) Children() []node {
	if a.ctx.Err() != nil {
		return nil
	}
	var refs []struct {
		Name string
		Path dbus.ObjectPath
	}
	if err := a.obj().CallWithContext(a.ctx, "org.a11y.atspi.Accessible.GetChildren", 0).Store(&refs); err != nil {
		return nil
	}
	out := make([]node, 0, len(refs))
	for _, r := range refs {
		out = append(out, &atspi{ctx: a.ctx, conn: a.conn, dest: r.Name, path: r.Path})
	}
	return out
}

func (a *atspi) pid() uint32 {
	var pid uint32
	_ = a.conn.BusObject().CallWithContext(a.ctx, "org.freedesktop.DBus.GetConnectionUnixProcessID", 0, a.dest).Store(&pid)
	return pid
}
