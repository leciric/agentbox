package iossim

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"agentbox/internal/api"
)

// Screens read from a real simulator (Xcode 27, iOS 26, a React Native app),
// names and text replaced.
func loadScreen(t *testing.T, name string) axTree {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "ui", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var tree axTree
	if err := json.Unmarshal(data, &tree); err != nil {
		t.Fatal(err)
	}
	return tree
}

// hitFrom hit-tests as the simulator does, roughly: the last element whose
// frame holds the point, and nothing below missBelow (under a tab bar, the
// simulator answers nothing).
func hitFrom(tree axTree, missBelow float64) hitter {
	return func(_ context.Context, points [][2]float64) ([]*axElement, error) {
		out := make([]*axElement, len(points))
		for i, p := range points {
			if p[1] >= missBelow {
				continue
			}
			for j := len(tree.Elements) - 1; j >= 0; j-- {
				e := tree.Elements[j]
				f := frameOf(e)
				if e.Role != "AXGroup" && e.Role != "AXGenericElement" || e.Identifier != "" || e.Label != "" {
					if p[0] >= f.x && p[0] <= f.x+f.w && p[1] >= f.y && p[1] <= f.y+f.h {
						out[i] = &tree.Elements[j]
						break
					}
				}
			}
		}
		return out, nil
	}
}

func checked(t *testing.T, name string, missBelow float64) *snapshot {
	t.Helper()
	tree := loadScreen(t, name)
	s := newSnapshot(tree)
	if err := s.check(context.Background(), hitFrom(tree, missBelow)); err != nil {
		t.Fatal(err)
	}
	return s
}

func find(t *testing.T, s *snapshot, target string) *uiElement {
	t.Helper()
	e, err := s.findTarget(target, "", false)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestProfileScreen(t *testing.T) {
	s := checked(t, "profile", 860)
	out := s.render(false)
	for _, want := range []string{
		`screen "Profile" in Acme (Preview)`,
		`button "Back"  id:profile-back`,
		`textfield "My LinkedIn" = "https://www.linkedin.com/in/your-name"  id:profile-linkedin-input`,
		`button "Sign out"  id:profile-sign-out  (may be hidden)`,
		`element "Home"  id:tab-home`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "off the screen") {
		t.Errorf("everything is on the screen, under the tab bar or not:\n%s", out)
	}
}

func TestTapUnderATabBarIsRefusedWithScrollTo(t *testing.T) {
	s := checked(t, "profile", 860)
	err := find(t, s, "Sign out").reachable("Sign out", false)
	if err == nil || !strings.Contains(err.Error(), "agentbox ios scroll-to 'Sign out'") {
		t.Fatalf("got %v", err)
	}
	if err := find(t, s, "Sign out").reachable("Sign out", true); err != nil {
		t.Fatalf("--force: %v", err)
	}
	if err := find(t, s, "id:profile-back").reachable("id:profile-back", false); err != nil {
		t.Fatalf("Back is in plain view: %v", err)
	}
}

func TestSameLabelIsRefusedUntilTold(t *testing.T) {
	s := checked(t, "profile", 860)
	_, err := s.findTarget("Save", "", false)
	if err == nil || !strings.Contains(err.Error(), `2 elements match "Save"`) || !strings.Contains(err.Error(), "near") {
		t.Fatalf("got %v", err)
	}
	e, err := s.findTarget("Save", "My LinkedIn", false)
	if err != nil || e.Identifier != "profile-linkedin-save" {
		t.Fatalf("--near My LinkedIn: %v, %+v", err, e)
	}
	if e := find(t, s, "id:profile-bio-save"); e.Identifier != "profile-bio-save" {
		t.Fatalf("by id: %+v", e)
	}
}

func TestLabelsPreferControlsAndFields(t *testing.T) {
	s := checked(t, "profile", 860)
	// The text area's label carries its placeholder: "About me Tell your…".
	// A heading is "About me" exactly; --into wants the field.
	e, err := s.findTarget("About me", "", true)
	if err != nil || e.Identifier != "profile-bio-input" {
		t.Fatalf("--into About me: %v, %+v", err, e)
	}
	if e := find(t, s, "about me"); e.kind != "heading" {
		t.Fatalf("a tap on About me: %+v", e)
	}
	if _, err := s.findTarget("nothing like it", "", false); err == nil || !strings.Contains(err.Error(), "agentbox ios ui") {
		t.Fatalf("got %v", err)
	}
}

func TestKeyboardIsOneLineAndCoversWhatsUnderIt(t *testing.T) {
	s := checked(t, "keyboard", 2000)
	out := s.render(false)
	if !s.keyboard || !strings.Contains(out, "keyboard on screen") {
		t.Fatalf("no keyboard in:\n%s", out)
	}
	for _, key := range []string{`"q"`, `"shift"`, `"delete"`, `"Emoji"`} {
		if strings.Contains(out, key) {
			t.Errorf("key %s listed:\n%s", key, out)
		}
	}
	tabs := find(t, s, "Tab Bar")
	if tabs.coveredBy != "the keyboard" {
		t.Fatalf("the tab bar under the keyboard: %+v", tabs)
	}
	if err := tabs.reachable("Tab Bar", true); err == nil || !strings.Contains(err.Error(), "ios key return") {
		t.Fatalf("--force doesn't tap through the keyboard: %v", err)
	}
	if e := find(t, s, "Connect"); !e.tappable {
		t.Fatalf("Connect is above the keyboard: %+v", e)
	}
}

func TestSystemAlertIsAScreen(t *testing.T) {
	s := checked(t, "alert", 2000)
	out := s.render(false)
	if !strings.HasPrefix(out, "screen: a system screen (an alert or a sheet)\n") || !strings.Contains(out, `button "Don’t Allow"`) {
		t.Fatalf("got:\n%s", out)
	}
	if e := find(t, s, "Allow Once"); !e.tappable {
		t.Fatalf("%+v", e)
	}
}

func TestOffScreenIsSummedUp(t *testing.T) {
	s := checked(t, "home", 2000)
	out := s.render(false)
	if !strings.Contains(out, "below, off the screen (4)") || !strings.Contains(out, "scroll-to") {
		t.Fatalf("got:\n%s", out)
	}
	if !strings.Contains(out, `element "Home"  id:tab-home  (selected)`) {
		t.Errorf("the selected tab:\n%s", out)
	}
	// One "View all" on the screen, another below: no twins on the screen.
	if strings.Contains(out, `"View all, Latest news"  near`) {
		t.Errorf("near for an element with no twin on the screen:\n%s", out)
	}
	e := find(t, s, "News story 20")
	if err := e.reachable("News story 20", false); err == nil || !strings.Contains(err.Error(), "below, off the screen") {
		t.Fatalf("got %v", err)
	}
	all := s.render(true)
	if !strings.Contains(all, `News story 20, News · Mar 20"  id:news-20  (below)`) {
		t.Fatalf("--all lists what's off the screen:\n%s", all)
	}
}

func TestChangeSaysWhatAnActionDid(t *testing.T) {
	before := newSnapshot(loadScreen(t, "profile"))
	if got := change(before, before, false, false, nil); !strings.Contains(got, "Nothing changed") {
		t.Fatalf("no change: %q", got)
	}
	tree := loadScreen(t, "profile")
	for i, e := range tree.Elements {
		if e.Identifier == "profile-linkedin-input" {
			tree.Elements[i].Value = "https://example.com"
		}
	}
	tree.Elements = append(tree.Elements, axElement{Role: "AXStaticText", Label: "Saved", Frame: [4]float64{30, 400, 100, 20}, Enabled: true})
	after := newSnapshot(tree)
	got := change(before, after, true, false, nil)
	for _, want := range []string{`screen "Profile" changed:`, `+ text "Saved"`, `~ textfield "My LinkedIn": "https://www.linkedin.com/in/your-name" → "https://example.com"`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	other := newSnapshot(loadScreen(t, "home"))
	got = change(before, other, true, false, func() string { return "the new screen\n" })
	if !strings.HasPrefix(got, `screen "Profile" → "Acme"`) || !strings.HasSuffix(got, "the new screen\n") {
		t.Fatalf("navigation: %q", got)
	}
}

func TestSettleIgnoresWhatKeepsMoving(t *testing.T) {
	before := newSnapshot(loadScreen(t, "home"))
	n := 0
	read := func(context.Context) (*snapshot, error) {
		n++
		tree := loadScreen(t, "home")
		// A carousel that never stops.
		tree.Elements[0].Frame[0] = float64(n * 10)
		if n >= 2 {
			tree.Elements = append(tree.Elements, axElement{Role: "AXStaticText", Label: "Loaded", Frame: [4]float64{10, 300, 100, 20}, Enabled: true})
		}
		return newSnapshot(tree), nil
	}
	after, changed, moving, err := settle(context.Background(), read, before)
	if err != nil || !changed || moving {
		t.Fatalf("changed %v, moving %v, err %v", changed, moving, err)
	}
	if _, err := after.findTarget("Loaded", "", false); err != nil {
		t.Fatal(err)
	}
}

// uiHelper is the helper with a simhost that answers from screens: before
// the first touch ends, the first; after it, the second.
func uiHelper(t *testing.T, screens ...string) (*testHelper, func() []op) {
	t.Helper()
	trees := make([]axTree, len(screens))
	for i, name := range screens {
		trees[i] = loadScreen(t, name)
	}
	return uiHelperOn(t, trees...)
}

func uiHelperOn(t *testing.T, trees ...axTree) (*testHelper, func() []op) {
	t.Helper()
	h := newTestHelper(t)
	var mu sync.Mutex
	current := 0
	h.simhost.answer = func(o map[string]any) any {
		mu.Lock()
		defer mu.Unlock()
		// A finger lifted moves to the next screen.
		for _, seen := range h.simhost.opsSoFar() {
			if seen.Op == "touch" && seen.Phase == "up" {
				current = min(len(trees)-1, 1)
			}
		}
		tree := trees[current]
		switch o["op"] {
		case "tree":
			return tree
		case "hit":
			var points [][2]float64
			data, _ := json.Marshal(o["points"])
			_ = json.Unmarshal(data, &points)
			hits, _ := hitFrom(tree, 860)(context.Background(), points)
			return map[string]any{"hits": hits}
		}
		return map[string]any{}
	}
	return h, h.simhost.opsSoFar
}

func TestTapLandsWhereTheHitTestSaysAndSaysWhatChanged(t *testing.T) {
	h, ops := uiHelper(t, "profile", "home")
	out, err := h.client.UI(testContext(t), testUDID, api.IOSUIRequest{Action: "tap", Target: "My network"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.Text, "Tapped button \"My network\"  id:profile-network\nscreen \"Profile\" → \"Acme\"") || out.Screen != "Acme" {
		t.Fatalf("got %+v", out)
	}
	var touches []op
	for _, o := range ops() {
		if o.Op == "touch" {
			touches = append(touches, o)
		}
	}
	// My network is [17 523 406 48] in points, on a 440×956 screen.
	if len(touches) != 2 || touches[0].Phase != "down" || math.Abs(touches[0].X-220.0/440) > 0.001 || math.Abs(touches[0].Y-547.0/956) > 0.001 {
		t.Fatalf("touches %+v", touches)
	}
}

func TestTapOnWhatItCantReachSendsNothing(t *testing.T) {
	h, ops := uiHelper(t, "profile")
	_, err := h.client.UI(testContext(t), testUDID, api.IOSUIRequest{Action: "tap", Target: "Sign out"})
	if err == nil || !strings.Contains(err.Error(), "scroll-to 'Sign out'") {
		t.Fatalf("got %v", err)
	}
	for _, o := range ops() {
		if o.Op == "touch" {
			t.Fatalf("touched: %+v", o)
		}
	}
	read, err := h.client.UI(testContext(t), testUDID, api.IOSUIRequest{})
	if err != nil || !strings.Contains(read.Text, `button "Sign out"  id:profile-sign-out  (may be hidden)`) {
		t.Fatalf("ios ui: %v\n%s", err, read.Text)
	}
}

// focusing is the profile screen before and after a tap on About me, which
// takes the focus or not.
func focusing(t *testing.T, takes bool) (*testHelper, func() []op) {
	before, after := loadScreen(t, "profile"), loadScreen(t, "profile")
	after.Elements = slices.Clone(after.Elements)
	for i, e := range after.Elements {
		if e.Identifier == "profile-bio-input" {
			after.Elements[i].Focused = takes
		}
	}
	return uiHelperOn(t, before, after)
}

func TestTypeIntoWaitsForTheFieldToTakeTheFocus(t *testing.T) {
	h, ops := focusing(t, true)
	out, err := h.client.UI(testContext(t), testUDID, api.IOSUIRequest{Action: "type", Target: "About me", Text: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Text, "focused") {
		t.Fatalf("got %+v", out)
	}
	up, keys := -1, 0
	for i, o := range ops() {
		switch {
		case o.Op == "touch" && o.Phase == "up":
			up = i
		case o.Op == "key" && o.Down:
			if up < 0 {
				t.Fatalf("typed before the tap: %+v", ops())
			}
			keys++
		}
	}
	// ⌘↓ to the end of the field's text, then h and i.
	if keys != 4 {
		t.Fatalf("%d keys: %+v", keys, ops())
	}
}

func TestTypeIntoAFieldThatTakesNoFocusTypesNothing(t *testing.T) {
	h, ops := focusing(t, false)
	_, err := h.client.UI(testContext(t), testUDID, api.IOSUIRequest{Action: "type", Target: "About me", Text: "hi"})
	if err == nil || !strings.Contains(err.Error(), "didn't take the keyboard focus") {
		t.Fatalf("got %v", err)
	}
	for _, o := range ops() {
		if o.Op == "key" {
			t.Fatalf("typed: %+v", o)
		}
	}
}

func TestUncheckedScreenClaimsNothingHidden(t *testing.T) {
	s := newSnapshot(loadScreen(t, "profile"))
	if out := s.render(false); strings.Contains(out, "may be hidden") {
		t.Fatalf("not hit-tested, yet:\n%s", out)
	}
	// The LinkedIn field's Save is told apart by the text by it, not the tab bar under it.
	if near := s.near(find(t, s, "id:profile-linkedin-save")); near == "Home" {
		t.Fatalf("near %q", near)
	}
}

func TestAScrollIsAChange(t *testing.T) {
	before := newSnapshot(loadScreen(t, "home"))
	read := func(context.Context) (*snapshot, error) {
		tree := loadScreen(t, "home")
		for i := range tree.Elements {
			tree.Elements[i].Frame[1] -= 300
		}
		return newSnapshot(tree), nil
	}
	after, changed, moving, err := settle(context.Background(), read, before)
	if err != nil || !changed || moving {
		t.Fatalf("changed %v, moving %v, err %v", changed, moving, err)
	}
	if got := change(before, after, changed, moving, func() string { return "full\n" }); !strings.HasPrefix(got, "The screen moved") {
		t.Fatalf("got %q", got)
	}
}

func TestACarouselAloneIsNoChange(t *testing.T) {
	before := newSnapshot(loadScreen(t, "home"))
	n := 0
	read := func(context.Context) (*snapshot, error) {
		n++
		tree := loadScreen(t, "home")
		tree.Elements[0].Frame[0] = float64(n * 10)
		return newSnapshot(tree), nil
	}
	if _, changed, _, err := settle(context.Background(), read, before); err != nil || changed {
		t.Fatalf("changed %v, err %v", changed, err)
	}
}

func TestAnAppsDeleteButtonIsNoKeyboard(t *testing.T) {
	tree := loadScreen(t, "profile")
	tree.Elements = append(tree.Elements, axElement{Role: "AXButton", Label: "Delete", Frame: [4]float64{16, 880, 200, 44}, Enabled: true},
		axElement{Role: "AXButton", Label: "delete", Frame: [4]float64{220, 880, 200, 44}, Enabled: true})
	s := newSnapshot(tree)
	if s.keyboard {
		t.Fatal("two Delete buttons made a keyboard")
	}
	if _, err := s.findTarget("Delete", "", false); err != nil {
		t.Fatal(err)
	}
}

func TestALabelOnlyInsideAnotherIsRefusedWhenThereAreSeveral(t *testing.T) {
	tree := loadScreen(t, "profile")
	tree.Elements = append(tree.Elements, axElement{Role: "AXButton", Label: "Unsaved changes", Frame: [4]float64{16, 200, 200, 44}, Enabled: true},
		axElement{Role: "AXButton", Label: "Discard unsaved", Frame: [4]float64{16, 250, 200, 44}, Enabled: true})
	s := newSnapshot(tree)
	if _, err := s.findTarget("saved", "", false); err == nil || !strings.Contains(err.Error(), "have it in their labels") {
		t.Fatalf("got %v", err)
	}
	if found, _ := s.matches("Saved", false, true); len(found) != 0 {
		t.Fatalf("wait --text Saved matched %d", len(found))
	}
}

func TestWhatsPressedIsLetGoWhenAnInputIsCanceled(t *testing.T) {
	var held []op
	for _, o := range []op{
		{Op: "key", Usage: usageShift, Down: true},
		{Op: "touch", Phase: "down", X: 0.1, Y: 0.2},
		{Op: "touch", Phase: "move", X: 0.3, Y: 0.4},
		{Op: "key", Usage: 0x04, Down: true},
		{Op: "key", Usage: 0x04},
	} {
		held = holding(held, o)
	}
	want := []op{{Op: "key", Usage: usageShift}, {Op: "touch", Phase: "up", X: 0.3, Y: 0.4}}
	if !slices.Equal(held, want) {
		t.Fatalf("held %+v, want %+v", held, want)
	}
	if held = holding(held, op{Op: "touch", Phase: "up", X: 0.3, Y: 0.4}); len(held) != 1 {
		t.Fatalf("after the finger lifts: %+v", held)
	}
}
