package desktop

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// The schema helpers just build small JSON Schema fragments; wrong shapes
// here would mean an AI tool's argument schema doesn't validate the way the
// description promises.

func TestObject(t *testing.T) {
	got := object([]string{"x", "y"}, map[string]any{"x": integer("x")})
	want := map[string]any{"type": "object", "properties": map[string]any{"x": integer("x")}, "required": []string{"x", "y"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("object() = %#v, want %#v", got, want)
	}
	// No required fields: the key is left out rather than set to an empty slice.
	got = object(nil, map[string]any{"x": integer("x")})
	if _, ok := got["required"]; ok {
		t.Errorf("object(nil, ...) set \"required\": %#v", got)
	}
}

func TestMerge(t *testing.T) {
	props := map[string]any{"a": str("a")}
	got := merge(props, map[string]any{"b": str("b")})
	want := map[string]any{"a": str("a"), "b": str("b")}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("merge() = %#v, want %#v", got, want)
	}
	// merge writes into and returns the first map.
	if &props != &got && !reflect.DeepEqual(props, got) {
		t.Errorf("merge() didn't mutate its first argument in place")
	}
}

func TestScalarSchemas(t *testing.T) {
	if got, want := str("d"), (map[string]any{"type": "string", "description": "d"}); !reflect.DeepEqual(got, want) {
		t.Errorf("str() = %#v, want %#v", got, want)
	}
	if got, want := integer("d"), (map[string]any{"type": "integer", "description": "d"}); !reflect.DeepEqual(got, want) {
		t.Errorf("integer() = %#v, want %#v", got, want)
	}
	if got, want := number("d"), (map[string]any{"type": "number", "description": "d"}); !reflect.DeepEqual(got, want) {
		t.Errorf("number() = %#v, want %#v", got, want)
	}
	if got, want := boolean("d"), (map[string]any{"type": "boolean", "description": "d"}); !reflect.DeepEqual(got, want) {
		t.Errorf("boolean() = %#v, want %#v", got, want)
	}
	got := choice("d", "a", "b")
	want := map[string]any{"type": "string", "description": "d", "enum": []string{"a", "b"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("choice() = %#v, want %#v", got, want)
	}
}

func TestPoint2(t *testing.T) {
	props := point2props("fx", "fy")
	want := map[string]any{
		"fx": integer("how far across, in the screenshot's pixels, from the left"),
		"fy": integer("how far down, in the screenshot's pixels, from the top"),
	}
	if !reflect.DeepEqual(props, want) {
		t.Errorf("point2props() = %#v, want %#v", props, want)
	}
	schema := point2("fx", "fy")
	if req, ok := schema["required"].([]string); !ok || !reflect.DeepEqual(req, []string{"fx", "fy"}) {
		t.Errorf("point2() required = %#v, want [fx fy]", schema["required"])
	}
}

func TestDecodeArgsWithNoArgumentsOrBadJSON(t *testing.T) {
	in, err := decodeArgs(nil)
	if err != nil {
		t.Fatalf("decodeArgs(nil) = %v", err)
	}
	if in.hasX || in.hasY {
		t.Errorf("decodeArgs(nil) = %+v, want no point", in)
	}
	if _, err := decodeArgs([]byte("not json")); err == nil {
		t.Error("decodeArgs() with malformed JSON: want an error")
	}
}

func TestLookProp(t *testing.T) {
	got := lookProp()
	want := map[string]any{"screenshot": boolean("answer with a screenshot of the result; true by default")}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("lookProp() = %#v, want %#v", got, want)
	}
}

func TestRun1DecodesArgsAndCallsFn(t *testing.T) {
	fn := run1(context.Background(), func(_ context.Context, in args) (string, error) {
		return "window=" + in.Window, nil
	})
	got, err := fn(json.RawMessage(`{"window":"Terminal"}`))
	if err != nil {
		t.Fatalf("run1() = %v", err)
	}
	if got != "window=Terminal" {
		t.Errorf("run1() = %q, want %q", got, "window=Terminal")
	}
	if _, err := fn(json.RawMessage(`not json`)); err == nil {
		t.Error("run1() with malformed JSON: want an error")
	}
}

func TestToolsListsDistinctlyNamedTools(t *testing.T) {
	tools := Tools(context.Background())
	if len(tools) == 0 {
		t.Fatal("Tools() returned none")
	}
	seen := map[string]bool{}
	for _, tool := range tools {
		if tool.Name == "" {
			t.Errorf("a tool has no name: %+v", tool)
		}
		if seen[tool.Name] {
			t.Errorf("tool name %q is used twice", tool.Name)
		}
		seen[tool.Name] = true
		if tool.Description == "" {
			t.Errorf("tool %q has no description", tool.Name)
		}
		if tool.RunContent == nil && tool.Run == nil {
			t.Errorf("tool %q has neither RunContent nor Run", tool.Name)
		}
	}
}

func TestShotContent(t *testing.T) {
	sc := Scale{Real: Size{Width: 2000, Height: 1000}, Shown: Size{Width: 1000, Height: 500}}
	content := shotContent([]byte("jpeg-bytes"), sc, "Clicked. ")
	if len(content) != 2 {
		t.Fatalf("shotContent() = %d parts, want 2", len(content))
	}
	if content[0].Type != "image" || content[0].MIMEType != "image/jpeg" {
		t.Errorf("shotContent() image part = %+v", content[0])
	}
	if content[0].Data == "" {
		t.Error("shotContent() image part has no data")
	}
	if !strings.HasPrefix(content[1].Text, "Clicked. ") {
		t.Errorf("shotContent() text = %q, want it to start with the before text", content[1].Text)
	}
	if !strings.Contains(content[1].Text, sc.Shown.String()) {
		t.Errorf("shotContent() text = %q, want it to say the shown size %s", content[1].Text, sc.Shown)
	}
}

func TestDescribeClick(t *testing.T) {
	cases := []struct {
		button string
		double bool
		want   string
	}{
		{"", false, "Clicked the left button"},
		{"left", false, "Clicked the left button"},
		{"Right", false, "Clicked the right button"},
		{"  middle  ", false, "Clicked the middle button"},
		{"left", true, "Double-clicked the left button"},
		{"right", true, "Double-clicked the right button"},
	}
	for _, c := range cases {
		if got := describeClick(c.button, c.double); got != c.want {
			t.Errorf("describeClick(%q, %v) = %q, want %q", c.button, c.double, got, c.want)
		}
	}
}

func TestShownWindow(t *testing.T) {
	sc := Scale{Real: Size{Width: 2000, Height: 1000}, Shown: Size{Width: 1000, Height: 500}}
	w := Window{ID: "1", Name: "Terminal", X: 100, Y: 200, W: 400, H: 200, hasGeometry: true}
	got := shownWindow(w, sc)
	if got.X != 50 || got.Y != 100 {
		t.Errorf("shownWindow origin = (%d, %d), want (50, 100)", got.X, got.Y)
	}
	if got.W != 200 || got.H != 100 {
		t.Errorf("shownWindow size = %dx%d, want 200x100", got.W, got.H)
	}
	if got.ID != w.ID || got.Name != w.Name {
		t.Errorf("shownWindow changed identity: %+v", got)
	}
}

func TestDescribeWindows(t *testing.T) {
	sc := Scale{}
	if got := describeWindows(nil, sc); got == "" {
		t.Error("describeWindows(nil) is empty, want an explanation that none are open")
	}

	windows := []Window{
		{ID: "0x1", Name: "Terminal", X: 10, Y: 20, W: 300, H: 200, hasGeometry: true},
		{ID: "0x2", Name: "No geometry"},
	}
	got := describeWindows(windows, sc)
	for _, want := range []string{"0x1", "Terminal", "10,20", "300×200", "0x2", "No geometry"} {
		if !strings.Contains(got, want) {
			t.Errorf("describeWindows() = %q, want it to mention %q", got, want)
		}
	}
	// The window with no geometry gets no coordinates.
	if strings.Contains(got, "No geometry (") {
		t.Errorf("describeWindows() gave geometry for a window that has none: %q", got)
	}
}
