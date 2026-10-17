package image

import "testing"

// An override counts only when it is newer than its pin: tools.txt stays the
// floor, so a pin moved past it takes over.
func TestOverridesKeepThePinAsTheFloor(t *testing.T) {
	pin := pinnedVersion(Pin("claude"))
	for _, c := range []struct{ override, want string }{
		{"", pin},
		{"99.0.0", "99.0.0"},
		{"0.0.1", pin},
		{"not-a-version", pin},
	} {
		o := Overrides{}
		if c.override != "" {
			o["claude"] = c.override
		}
		if got := o.Version("claude"); got != c.want {
			t.Errorf("override %q: runs %s, want %s", c.override, got, c.want)
		}
	}
	tools := ToolsFor(Components{})
	moved := Overrides{"claude": "99.0.0"}.Apply(tools)
	if ToolsVersion(moved) == ToolsVersion(tools) {
		t.Error("moving claude on doesn't change the tools version")
	}
	plan := PlanFor(true, Installed{Version: Version, ToolsVersion: ToolsVersion(tools), Tools: specsOf(tools)}, Components{}, Overrides{"claude": "99.0.0"})
	if plan.Action != NeedsTools || len(plan.Install) != 1 || plan.Install[0].Spec != "claude@99.0.0" || len(plan.Remove) != 0 {
		t.Errorf("plan = %+v", plan)
	}
}
