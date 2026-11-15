package skills

import (
	"testing"
)

// TestBuiltinSkillsAreValid: each built-in skill is a folder of its name with
// a SKILL.md naming it and saying when to use it, as a stored one must.
func TestBuiltinSkillsAreValid(t *testing.T) {
	list := Builtin()
	if len(list) == 0 {
		t.Fatal("no built-in skills")
	}
	for _, sk := range list {
		if err := ValidateName(sk.Name); err != nil {
			t.Error(err)
		}
		var found bool
		for _, f := range sk.Files {
			if f.Path != File {
				continue
			}
			found = true
			meta, body, err := Parse(string(f.Content))
			if err != nil {
				t.Fatalf("%s: %v", sk.Name, err)
			}
			if meta.Name != sk.Name || meta.Description == "" || body == "" {
				t.Errorf("%s: name %q, description %q", sk.Name, meta.Name, meta.Description)
			}
		}
		if !found {
			t.Errorf("%s has no %s", sk.Name, File)
		}
	}
}

// TestWithBuiltinKeepsAStoredSkillOfTheSameName: the user's own skill of a
// built-in's name is the one installed.
func TestWithBuiltinKeepsAStoredSkillOfTheSameName(t *testing.T) {
	own := Installed{Name: "troubleshooting-setup"}
	list := WithBuiltin([]Installed{own})
	if len(list) != len(Builtin()) || list[0].Files != nil {
		t.Errorf("WithBuiltin = %+v; want the stored skill kept, and no second one of its name", list)
	}
	if got := WithBuiltin(nil); len(got) != len(Builtin()) {
		t.Errorf("WithBuiltin(nil) = %d skills, want %d", len(got), len(Builtin()))
	}
}
