package agent

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestWithHeavyHooks(t *testing.T) {
	t.Parallel()
	existing := []byte(`{"model":"opus","hooks":{"PreToolUse":[{"matcher":"Edit","hooks":[{"type":"command","command":"lint.sh"}]},` +
		`{"matcher":"Bash","hooks":[{"type":"command","command":"` + HeavyHookCommand + `","timeout":60}]}]}}`)
	merged, err := withHeavyHooks(existing)
	if err != nil || merged == nil {
		t.Fatalf("merge = %s, %v", merged, err)
	}
	var settings struct {
		Model string                       `json:"model"`
		Hooks map[string][]json.RawMessage `json:"hooks"`
	}
	if err := json.Unmarshal(merged, &settings); err != nil {
		t.Fatal(err)
	}
	if settings.Model != "opus" {
		t.Error("another setting was lost")
	}
	pre := settings.Hooks["PreToolUse"]
	if len(pre) != 2 || !strings.Contains(string(pre[0]), "lint.sh") {
		t.Fatalf("PreToolUse = %s; want the user's hook kept and ours replaced", pre)
	}
	var ours bytes.Buffer
	_ = json.Compact(&ours, pre[1])
	if !strings.Contains(ours.String(), `"timeout":660`) || !strings.Contains(ours.String(), `"matcher":"Bash"`) {
		t.Errorf("our PreToolUse hook = %s", ours.String())
	}
	for _, event := range []string{"PostToolUse", "PostToolUseFailure"} {
		if len(settings.Hooks[event]) != 1 {
			t.Errorf("%s = %s", event, settings.Hooks[event])
		}
	}
	if again, err := withHeavyHooks(merged); err != nil || again != nil {
		t.Errorf("merging again changed %s, %v", again, err)
	}
	if _, err := withHeavyHooks([]byte(`{"hooks":[1]}`)); err == nil {
		t.Error("hooks that aren't an object were overwritten")
	}
}
