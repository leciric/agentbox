package desktop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"agentbox/internal/mcp"
)

func TestMarkUsedCreatesThenTouches(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state", "browser-used")
	then := time.Now().Add(-time.Hour).Truncate(time.Second)
	markUsed(path, then)
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	markUsed(path, then.Add(time.Minute))
	if st, err := os.Stat(path); err != nil || !st.ModTime().Equal(then.Add(time.Minute)) {
		t.Fatalf("modified %v (%v), want %v", st.ModTime(), err, then.Add(time.Minute))
	}
}

func TestMarkingUseMarksEachCall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	called := false
	tools := MarkingUse([]mcp.Tool{{Name: "x", Run: func(json.RawMessage) (string, error) { called = true; return "", nil }}})
	if _, err := tools[0].Run(nil); err != nil || !called {
		t.Fatalf("the tool didn't run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, browserUsedFile)); err != nil {
		t.Fatalf("the call didn't mark the browser used: %v", err)
	}
}
