package iossim

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"slices"
	"strings"
	"time"
)

// The screen as an agent reads it: simhost's accessibility elements (the
// flat list VoiceOver has, frames in points), where each one is, and whether
// a tap there lands on it.

// axElement is one element as simhost describes it.
type axElement struct {
	Role       string     `json:"role"`
	Label      string     `json:"label"`
	Identifier string     `json:"identifier"`
	Value      string     `json:"value"`
	Frame      [4]float64 `json:"frame"`
	Enabled    bool       `json:"enabled"`
	Selected   bool       `json:"selected"`
	Focused    bool       `json:"focused"`
	PID        int        `json:"pid"`
}

// axTree is the frontmost app (its frame is the screen) and its elements.
type axTree struct {
	axElement
	Elements []axElement `json:"elements"`
}

type rect struct{ x, y, w, h float64 }

func (r rect) empty() bool            { return r.w <= 0 || r.h <= 0 }
func (r rect) center() (x, y float64) { return r.x + r.w/2, r.y + r.h/2 }

func (r rect) intersect(o rect) rect {
	x1, y1 := max(r.x, o.x), max(r.y, o.y)
	x2, y2 := min(r.x+r.w, o.x+o.w), min(r.y+r.h, o.y+o.h)
	return rect{x1, y1, x2 - x1, y2 - y1}
}

// inside says r lies within o, give or take a point.
func (r rect) inside(o rect) bool {
	return r.x >= o.x-1 && r.y >= o.y-1 && r.x+r.w <= o.x+o.w+1 && r.y+r.h <= o.y+o.h+1
}

func frameOf(e axElement) rect { return rect{e.Frame[0], e.Frame[1], e.Frame[2], e.Frame[3]} }

// Where an element is: on the screen, or which way to scroll to it.
const (
	onScreen = ""
	above    = "above"
	below    = "below"
	left     = "left"
	right    = "right"
)

type uiElement struct {
	axElement
	index    int
	kind     string
	where    string
	visible  rect
	keyboard bool
	// From the hit-test, for elements on the screen: where a tap lands on
	// it, what is over it instead, or neither (nothing answered there).
	tapX, tapY float64
	tappable   bool
	coveredBy  string
	checked    bool
}

type snapshot struct {
	app       axTree
	screen    rect
	title     string
	elements  []*uiElement
	keyboard  bool
	keys      rect // where the keyboard is, when it's up
	hitTested bool
}

var kinds = map[string]string{
	"AXButton": "button", "AXStaticText": "text", "AXHeading": "heading", "AXTextField": "textfield",
	"AXSecureTextField": "textfield", "AXTextArea": "textarea", "AXSearchField": "searchfield",
	"AXLink": "link", "AXGenericElement": "element", "AXImage": "image", "AXGroup": "group",
	"AXCheckBox": "checkbox", "AXSwitch": "switch", "AXSlider": "slider", "AXCell": "cell",
	"AXRadioButton": "radiobutton", "AXPopUpButton": "popupbutton", "AXMenuItem": "menuitem",
	"AXTab": "tab", "AXComboBox": "combobox", "AXIncrementor": "incrementor",
}

var interactiveKinds = map[string]bool{
	"button": true, "textfield": true, "textarea": true, "searchfield": true, "link": true,
	"checkbox": true, "switch": true, "slider": true, "cell": true, "radiobutton": true,
	"popupbutton": true, "menuitem": true, "tab": true, "combobox": true, "incrementor": true,
}

var textKinds = map[string]bool{"textfield": true, "textarea": true, "searchfield": true, "combobox": true}

func kindOf(role string) string {
	if k, ok := kinds[role]; ok {
		return k
	}
	return strings.ToLower(strings.TrimPrefix(role, "AX"))
}

// interactive is what a tap is for: a control, or anything an app gave an
// id (React Native's Pressables are often plain elements with a testID).
func (e *uiElement) interactive() bool {
	return interactiveKinds[e.kind] || e.Identifier != ""
}

// shown is what the screen lists: elements with a label, a value or an id.
func (e *uiElement) shown() bool {
	return !e.keyboard && (e.Label != "" || e.Identifier != "" || (e.Value != "" && e.interactive()))
}

// newSnapshot works out where each element is and finds the keyboard.
func newSnapshot(t axTree) *snapshot {
	s := &snapshot{app: t, screen: frameOf(t.axElement)}
	if s.screen.empty() {
		s.screen = rect{0, 0, 1, 1}
	}
	for i, e := range t.Elements {
		u := &uiElement{axElement: e, index: i, kind: kindOf(e.Role)}
		u.Label = strings.TrimSpace(u.Label)
		f := frameOf(e)
		u.visible = f.intersect(s.screen)
		if u.visible.empty() {
			switch {
			case f.y >= s.screen.y+s.screen.h:
				u.where = below
			case f.y+f.h <= s.screen.y:
				u.where = above
			case f.x >= s.screen.x+s.screen.w:
				u.where = right
			default:
				u.where = left
			}
		}
		s.elements = append(s.elements, u)
	}
	s.findKeyboard()
	s.title = s.titleOf()
	return s
}

// findKeyboard marks the on-screen keyboard's keys: they come at the end of
// the app's list, after an unlabelled element as wide as the screen that
// holds its "delete" or "shift" key. Without that element, a dozen
// one-letter keys around them say it's a keyboard, not an app's Delete.
func (s *snapshot) findKeyboard() {
	var key *uiElement
	for _, e := range s.elements {
		if e.kind == "button" && (e.Label == "delete" || e.Label == "shift") && e.where == onScreen {
			key = e
			break
		}
	}
	if key == nil {
		return
	}
	start, area := -1, rect{s.screen.x, key.visible.y - 4*key.visible.h, s.screen.w, s.screen.h}
	for _, e := range s.elements {
		f := frameOf(e.axElement)
		if e.index < key.index && e.Label == "" && e.Identifier == "" && f.w >= 0.9*s.screen.w && frameOf(key.axElement).inside(f) {
			start, area = e.index, f
		}
	}
	if start < 0 {
		letters := 0
		for _, e := range s.elements {
			if e.kind == "button" && len([]rune(e.Label)) == 1 && frameOf(e.axElement).inside(area) {
				if letters == 0 {
					start = e.index
				}
				letters++
			}
		}
		if letters < 10 {
			return
		}
		start = min(start, key.index)
	}
	s.keyboard, s.keys = true, area
	for _, e := range s.elements[start:] {
		if frameOf(e.axElement).inside(area) {
			e.keyboard = true
		}
	}
}

// titleOf names the screen: a system one (an app with no name); the
// navigation header (a group as wide as the screen near its top, with the
// screen's name as its id); else the first heading near the top or above
// it; else the app.
func (s *snapshot) titleOf() string {
	if strings.TrimSpace(s.app.Label) == "" {
		return "a system screen (an alert or a sheet)"
	}
	top := s.screen.y + 0.2*s.screen.h
	for _, e := range s.elements {
		f := frameOf(e.axElement)
		if e.kind == "group" && e.Label == "" && e.Identifier != "" && f.w >= 0.9*s.screen.w && f.y < top && e.where == onScreen {
			return e.Identifier
		}
	}
	// Scrolled up out of sight, it still names the screen.
	for _, e := range s.elements {
		if e.kind == "heading" && (e.where == above || e.where == onScreen && e.visible.y < top) {
			return e.Label
		}
	}
	return s.app.Label
}

// hitter is simhost's hit-test: the element at each point, nil for none.
type hitter func(ctx context.Context, points [][2]float64) ([]*axElement, error)

// check hit-tests the elements on the screen: a hit on the element, inside
// it, or on a control it's part of means a tap there lands on it; a hit on
// something else means that is over it. A miss proves nothing: it gets a
// second point, and stays unchecked if both miss.
func (s *snapshot) check(ctx context.Context, hit hitter) error {
	var todo []*uiElement
	for _, e := range s.elements {
		if e.where != onScreen || !e.shown() {
			continue
		}
		if x, y := e.visible.center(); s.keyboard && (rect{x, y, 0, 0}).inside(s.keys) {
			e.coveredBy, e.checked = "the keyboard", true
			continue
		}
		todo = append(todo, e)
	}
	for pass := 0; pass < 2 && len(todo) > 0; pass++ {
		points := make([][2]float64, len(todo))
		for i, e := range todo {
			x, y := e.visible.center()
			if pass == 1 { // a quarter of the way in, where a bar over its middle may not be
				x, y = e.visible.x+e.visible.w/2, e.visible.y+e.visible.h/4
			}
			points[i] = [2]float64{x, y}
		}
		hits, err := hit(ctx, points)
		if err != nil {
			return err
		}
		var again []*uiElement
		for i, e := range todo {
			var h *axElement
			if i < len(hits) {
				h = hits[i]
			}
			switch {
			case h == nil:
				again = append(again, e)
				continue
			case h.PID != 0 && h.PID != s.app.PID && s.app.PID != 0:
				e.coveredBy = "the system's " + cmpOr(strings.TrimSpace(h.Label), "interface")
			case s.lands(e, h):
				e.tappable, e.tapX, e.tapY = true, points[i][0], points[i][1]
			default:
				e.coveredBy = describeHit(s, h)
			}
			e.checked = true
		}
		todo = again
	}
	s.hitTested = true
	return nil
}

func (s *snapshot) lands(e *uiElement, h *axElement) bool {
	hf, ef := frameOf(*h), frameOf(e.axElement)
	if h.Label == e.Label && h.Identifier == e.Identifier && h.Role == e.Role {
		return true
	}
	if hf.inside(ef) {
		return true
	}
	// Text inside a button: the tap lands on the button, which is what it's for.
	return ef.inside(hf) && interactiveKinds[kindOf(h.Role)] && !s.isKey(h)
}

func (s *snapshot) isKey(h *axElement) bool {
	for _, e := range s.elements {
		if e.keyboard && e.Label == h.Label && frameOf(e.axElement) == frameOf(*h) {
			return true
		}
	}
	return false
}

func describeHit(s *snapshot, h *axElement) string {
	if s.isKey(h) {
		return "the keyboard"
	}
	switch {
	case h.Label != "":
		return fmt.Sprintf("%s %q", kindOf(h.Role), clip(h.Label, 40))
	case h.Identifier != "":
		return "id:" + h.Identifier
	}
	return "another element"
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// clip makes a label one line of at most n characters.
func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// listed are the elements the screen shows, without echoes: a text that
// repeats the label of a control it lies in is that control's.
func (s *snapshot) listed() []*uiElement {
	var out []*uiElement
	for _, e := range s.elements {
		if !e.shown() {
			continue
		}
		echo := false
		for _, o := range s.elements {
			if o != e && o.shown() && o.interactive() && !e.interactive() && o.Label == e.Label && frameOf(e.axElement).inside(frameOf(o.axElement)) {
				echo = true
				break
			}
		}
		if !echo {
			out = append(out, e)
		}
	}
	return out
}

// near is the closest other labelled element, for telling apart elements
// with the same label ("Follow" near "Ana Souza"): text or a heading if
// there's any, as a tab bar's button beside it says nothing about it.
func (s *snapshot) near(e *uiElement) string {
	ex, ey := frameOf(e.axElement).center()
	for _, textOnly := range []bool{true, false} {
		best, dist := "", math.Inf(1)
		for _, o := range s.elements {
			if o == e || o.Label == "" || o.Label == e.Label || o.keyboard || textOnly && o.kind != "text" && o.kind != "heading" {
				continue
			}
			ox, oy := frameOf(o.axElement).center()
			if d := math.Hypot(ox-ex, oy-ey); d < dist {
				best, dist = o.Label, d
			}
		}
		if best != "" {
			return clip(best, 40)
		}
	}
	return ""
}

func (s *snapshot) line(e *uiElement, twins bool) string {
	var b strings.Builder
	b.WriteString(e.kind)
	if e.Label != "" {
		fmt.Fprintf(&b, " %q", clip(e.Label, 80))
	}
	if e.Value != "" {
		fmt.Fprintf(&b, " = %q", clip(e.Value, 60))
	}
	if e.Identifier != "" {
		b.WriteString("  id:" + e.Identifier)
	}
	if twins {
		fmt.Fprintf(&b, "  near %q", s.near(e))
	}
	var flags []string
	if e.Selected {
		flags = append(flags, "selected")
	}
	if e.Focused {
		flags = append(flags, "focused")
	}
	if !e.Enabled {
		flags = append(flags, "disabled")
	}
	switch {
	case e.where != onScreen:
		flags = append(flags, e.where)
	case e.coveredBy != "":
		flags = append(flags, "covered by "+e.coveredBy)
	case s.hitTested && e.interactive() && !e.tappable:
		flags = append(flags, "may be hidden")
	}
	if len(flags) > 0 {
		b.WriteString("  (" + strings.Join(flags, ", ") + ")")
	}
	return b.String()
}

// render is the screen as `agentbox ios ui` prints it: what's on it, one
// line each; what's off it, a line per direction; hints.
func (s *snapshot) render(all bool) string {
	var b strings.Builder
	if s.title == s.app.Label || strings.TrimSpace(s.app.Label) == "" {
		fmt.Fprintf(&b, "screen: %s\n", s.title)
	} else {
		fmt.Fprintf(&b, "screen %q in %s\n", s.title, s.app.Label)
	}
	listed := s.listed()
	labels := map[string]int{}
	for _, e := range listed {
		if all || e.where == onScreen {
			labels[e.Label]++
		}
	}
	off := map[string][]*uiElement{}
	shown, area := 0, 0.0
	for _, e := range listed {
		if e.where != onScreen && !all {
			off[e.where] = append(off[e.where], e)
			continue
		}
		b.WriteString(s.line(e, e.Label != "" && labels[e.Label] > 1) + "\n")
		shown++
		if e.where == onScreen && e.Label != "" {
			area += e.visible.w * e.visible.h
		}
	}
	if s.keyboard {
		b.WriteString(`keyboard on screen: type with agentbox ios type "…", close it with agentbox ios key return` + "\n")
	}
	for _, dir := range []string{above, below, left, right} {
		es := off[dir]
		if len(es) == 0 {
			continue
		}
		var names []string
		for _, e := range es[:min(len(es), 4)] {
			names = append(names, fmt.Sprintf("%s %q", e.kind, clip(e.Label, 40)))
		}
		more := ""
		if len(es) > 4 {
			more = ", …"
		}
		fmt.Fprintf(&b, "%s, off the screen (%d): %s%s\n", dir, len(es), strings.Join(names, ", "), more)
	}
	if len(off) > 0 {
		b.WriteString(`agentbox ios scroll-to <target> scrolls one into view; lists load more as they scroll, so an item not listed may need scroll-to on the last one` + "\n")
	}
	if shown == 0 || area < 0.25*s.screen.w*s.screen.h && !s.keyboard {
		b.WriteString("This screen tells accessibility little (a canvas, a web view, a game?): look at it with agentbox ios screenshot, and tap what you see with agentbox ios tap --fraction\n")
	}
	return b.String()
}

// Targets: id:<identifier>, or a label.

// matches are the elements a target means, and whether they matched only
// inside their labels. A label matches exactly, then ignoring case, then as
// a prefix, then anywhere; in each, controls before text.
func (s *snapshot) matches(target string, textOnly, wholeOnly bool) (found []*uiElement, loose bool) {
	candidates := s.listed()
	if textOnly {
		candidates = slices.DeleteFunc(slices.Clone(candidates), func(e *uiElement) bool { return !textKinds[e.kind] })
	}
	if id, ok := strings.CutPrefix(target, "id:"); ok {
		for _, e := range candidates {
			if e.Identifier == id {
				found = append(found, e)
			}
		}
		return found, false
	}
	lower := strings.ToLower(target)
	tiers := []func(e *uiElement) bool{
		func(e *uiElement) bool { return e.Label == target },
		func(e *uiElement) bool { return strings.EqualFold(e.Label, target) },
		func(e *uiElement) bool { return strings.HasPrefix(strings.ToLower(e.Label), lower) },
		func(e *uiElement) bool { return strings.Contains(strings.ToLower(e.Label), lower) },
	}
	if wholeOnly {
		tiers = tiers[:3]
	}
	for i, match := range tiers {
		for _, e := range candidates {
			if match(e) {
				found = append(found, e)
			}
		}
		if len(found) > 0 {
			loose = i == 3
			break
		}
	}
	if len(found) > 1 {
		if controls := slices.DeleteFunc(slices.Clone(found), func(e *uiElement) bool { return !e.interactive() }); len(controls) > 0 {
			found = controls
		}
	}
	return found, loose
}

// findTarget finds the one element a target means. Several are refused,
// each listed with what tells it apart; so is a match only inside labels
// ("Delete" in "Delete account") unless it's the only one.
func (s *snapshot) findTarget(target, nearText string, textOnly bool) (*uiElement, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, errors.New("say which element: id:<id> or its label, as agentbox ios ui lists them")
	}
	found, loose := s.matches(target, textOnly, false)
	if len(found) > 1 && nearText != "" {
		found = s.nearest(found, nearText)
	}
	what := "element"
	if textOnly {
		what = "text field"
	}
	switch {
	case len(found) == 0:
		return nil, fmt.Errorf("no %s matches %q on this screen: agentbox ios ui lists them", what, target)
	case len(found) == 1:
		return found[0], nil
	}
	var b strings.Builder
	how := "match"
	if loose {
		how = "have it in their labels"
	}
	fmt.Fprintf(&b, "%d elements %s %q; name one by its id or whole label, or add --near with text beside it:\n", len(found), how, target)
	for _, e := range found[:min(len(found), 8)] {
		b.WriteString("  " + s.line(e, true) + "\n")
	}
	return nil, errors.New(strings.TrimRight(b.String(), "\n"))
}

// nearest keeps the candidate closest to an element whose label contains text.
func (s *snapshot) nearest(found []*uiElement, text string) []*uiElement {
	var anchors []*uiElement
	for _, e := range s.elements {
		if strings.Contains(strings.ToLower(e.Label), strings.ToLower(text)) {
			anchors = append(anchors, e)
		}
	}
	if len(anchors) == 0 {
		return found
	}
	var best *uiElement
	dist := math.Inf(1)
	for _, e := range found {
		ex, ey := frameOf(e.axElement).center()
		for _, a := range anchors {
			if a == e {
				continue
			}
			ax, ay := frameOf(a.axElement).center()
			if d := math.Hypot(ax-ex, ay-ey); d < dist {
				best, dist = e, d
			}
		}
	}
	if best == nil {
		return found
	}
	return []*uiElement{best}
}

// reachable says why a tap on e would miss, or nil.
func (e *uiElement) reachable(target string, force bool) error {
	name := fmt.Sprintf("%s %q", e.kind, clip(cmpOr(e.Label, e.Identifier), 40))
	switch {
	case e.where != onScreen:
		return fmt.Errorf("%s is %s, off the screen: agentbox ios scroll-to %s first", name, e.where, quoteArg(target))
	case !e.Enabled:
		return fmt.Errorf("%s is disabled", name)
	case e.coveredBy == "the keyboard":
		return fmt.Errorf("%s is under the keyboard: close it with agentbox ios key return, or agentbox ios scroll-to %s", name, quoteArg(target))
	case e.coveredBy != "":
		return fmt.Errorf("%s is covered by %s: close that, or agentbox ios scroll-to %s", name, e.coveredBy, quoteArg(target))
	case !e.tappable && !force:
		return fmt.Errorf("can't confirm a tap on %s lands on it (a bar may be over it): agentbox ios scroll-to %s, or check with a screenshot and tap it with --force", name, quoteArg(target))
	}
	return nil
}

// tapPoint is where to tap e, in points: where the hit-test landed on it,
// else the middle of what's on the screen of it.
func (e *uiElement) tapPoint() (float64, float64) {
	if e.tappable {
		return e.tapX, e.tapY
	}
	return e.visible.center()
}

func quoteArg(s string) string {
	if strings.ContainsAny(s, " \"'#$&*()|;<>?`\\") || s == "" {
		return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
	}
	return s
}

// Settling: whether an action changed the screen, and how.

func (e *uiElement) key() string {
	return e.Role + "\x00" + e.Label + "\x00" + e.Identifier
}

// shape is the screen's structure: its elements and where they are,
// leaving out those still moving (a carousel, a shimmer).
func (s *snapshot) shape(moving map[string]bool) uint64 {
	h := fnv.New64a()
	_, _ = fmt.Fprintf(h, "%s\x01%d\x01", s.app.Label, s.app.PID)
	for _, e := range s.elements {
		if moving[e.key()] {
			continue
		}
		f := frameOf(e.axElement)
		_, _ = fmt.Fprintf(h, "%s\x01%.0f %.0f %.0f %.0f\x01", e.key(), f.x, f.y, f.w, f.h)
	}
	return h.Sum64()
}

// values is what the elements say: field text, switches, counts.
func (s *snapshot) values() uint64 {
	h := fnv.New64a()
	for _, e := range s.elements {
		_, _ = fmt.Fprintf(h, "%s\x01%s\x01%t\x01%t\x01%t\x01", e.key(), e.Value, e.Selected, e.Enabled, e.Focused)
	}
	return h.Sum64()
}

// moved adds to moving the elements whose frame differs between two reads.
func moved(a, b *snapshot, moving map[string]bool) {
	frames := map[string]rect{}
	for _, e := range a.elements {
		frames[e.key()] = frameOf(e.axElement)
	}
	for _, e := range b.elements {
		if f, ok := frames[e.key()]; ok && f != frameOf(e.axElement) {
			moving[e.key()] = true
		}
	}
}

// settle reads the screen after an action until it stops changing: up to
// 1.5s for a first change, 3s in all. Elements that keep moving between reads
// after the action (a carousel, a shimmer) are left out; what the action
// itself moved, a scroll, isn't. It returns the last read, whether it differs
// from before, and whether it's still changing.
func settle(ctx context.Context, read func(context.Context) (*snapshot, error), before *snapshot) (after *snapshot, changed, moving bool, err error) {
	start := time.Now()
	movingKeys := map[string]bool{}
	var prev *snapshot
	for {
		select {
		case <-ctx.Done():
			return nil, false, false, ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
		cur, err := read(ctx)
		if err != nil {
			return nil, false, false, err
		}
		if prev != nil {
			moved(prev, cur, movingKeys)
		}
		differs := cur.shape(movingKeys) != before.shape(movingKeys) || cur.values() != before.values()
		stable := prev != nil && cur.shape(movingKeys) == prev.shape(movingKeys) && cur.values() == prev.values()
		elapsed := time.Since(start)
		switch {
		case differs && stable:
			return cur, true, false, nil
		case !differs && elapsed > 1500*time.Millisecond:
			return cur, false, false, nil
		case elapsed > 3*time.Second:
			return cur, differs, true, nil
		}
		prev = cur
	}
}

// change says what an action did to the screen.
func change(before, after *snapshot, changed, moving bool, full func() string) string {
	if !changed {
		return "Nothing changed in the accessibility tree within 1.5s. If it may be slow, agentbox ios wait --text <text>; if the change may only be visual, take a screenshot. Don't repeat the action blindly.\n"
	}
	prefix := ""
	if moving {
		prefix = "Still changing after 3s; as it was then:\n"
	}
	if before.title != after.title || before.app.PID != after.app.PID {
		return prefix + fmt.Sprintf("screen %q → %q\n", before.title, after.title) + full()
	}
	var lines []string
	old := map[string]*uiElement{}
	for _, e := range before.listed() {
		old[e.key()] = e
	}
	seen := map[string]bool{}
	movedOnly := true
	for _, e := range after.listed() {
		k := e.key()
		seen[k] = true
		o := old[k]
		switch {
		case o == nil:
			if e.where == onScreen {
				lines = append(lines, "+ "+after.line(e, false))
				movedOnly = false
			}
		case o.Value != e.Value || o.Selected != e.Selected || o.Enabled != e.Enabled || o.Focused != e.Focused:
			lines = append(lines, fmt.Sprintf("~ %s %q: %s → %s", e.kind, clip(cmpOr(e.Label, e.Identifier), 40), state(o), state(e)))
			movedOnly = false
		}
	}
	for k, o := range old {
		if !seen[k] && o.where == onScreen {
			lines = append(lines, "- "+before.line(o, false))
			movedOnly = false
		}
	}
	if movedOnly {
		return prefix + "The screen moved (it scrolled, or something animated):\n" + full()
	}
	slices.Sort(lines) // + before -, then ~: stable output
	if len(lines) > 25 {
		lines = append(lines[:25], fmt.Sprintf("… and %d more: agentbox ios ui", len(lines)-25))
	}
	return prefix + fmt.Sprintf("screen %q changed:\n", after.title) + strings.Join(lines, "\n") + "\n"
}

func state(e *uiElement) string {
	parts := []string{fmt.Sprintf("%q", clip(e.Value, 40))}
	if e.Selected {
		parts = append(parts, "selected")
	}
	if e.Focused {
		parts = append(parts, "focused")
	}
	if !e.Enabled {
		parts = append(parts, "disabled")
	}
	return strings.Join(parts, " ")
}
