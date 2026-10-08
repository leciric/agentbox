package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"agentbox/internal/state"
)

func TestSkillsOverridesAndCleanup(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	if err := st.AddProject(ctx, state.Project{Name: "pawly", Root: t.TempDir(), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Second)
	files := []state.SkillFile{{Path: "SKILL.md", Mode: 0o644, Content: []byte("x")}, {Path: "a/b.sh", Mode: 0o755, Content: []byte("echo")}}
	if err := st.SetSkill(ctx, state.Skill{Name: "pdf", Description: "PDFs", Enabled: false, Overrides: map[string]bool{"pawly": true}, UpdatedAt: now}, files); err != nil {
		t.Fatal(err)
	}
	sk, err := st.Skill(ctx, "pdf")
	if err != nil {
		t.Fatal(err)
	}
	if sk.Enabled || !sk.EnabledFor("pawly") || sk.EnabledFor("other") || sk.EnabledFor("") || !sk.CreatedAt.Equal(now) {
		t.Errorf("Skill() = %+v", sk)
	}
	// Saving it again keeps its overrides and replaces its files.
	sk.Overrides = nil
	if err := st.SetSkill(ctx, sk, files[:1]); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.SkillFiles(ctx, "pdf"); len(got) != 1 {
		t.Errorf("SkillFiles() after a save = %+v", got)
	}
	if sk, _ := st.Skill(ctx, "pdf"); !sk.EnabledFor("pawly") {
		t.Error("saving a skill lost its override")
	}
	off := false
	if err := st.SetSkillOverride(ctx, "pdf", "pawly", &off); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSkillEnabled(ctx, "pdf", true); err != nil {
		t.Fatal(err)
	}
	if sk, _ := st.Skill(ctx, "pdf"); sk.EnabledFor("pawly") || !sk.EnabledFor("other") {
		t.Errorf("after off in pawly, on AgentBox-wide: %+v", sk)
	}
	// A removed project's overrides go with it.
	if err := st.RemoveProject(ctx, "pawly"); err != nil {
		t.Fatal(err)
	}
	if sk, _ := st.Skill(ctx, "pdf"); len(sk.Overrides) != 0 {
		t.Errorf("a removed project's override stayed: %+v", sk.Overrides)
	}
	if err := st.RemoveSkill(ctx, "pdf"); err != nil {
		t.Fatal(err)
	}
	if files, _ := st.SkillFiles(ctx, "pdf"); len(files) != 0 {
		t.Error("a removed skill's files stayed")
	}
	if err := st.RemoveSkill(ctx, "pdf"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("RemoveSkill() twice = %v", err)
	}
	if err := st.SetSkillOverride(ctx, "pdf", "pawly", nil); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("an override of a missing skill = %v", err)
	}
}
