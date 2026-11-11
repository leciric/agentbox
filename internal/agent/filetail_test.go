package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"agentbox/internal/state"
)

func TestFileTailReadsALeadsFileAPieceAtATime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out")
	if err := os.WriteFile(path, []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := &Manager{}
	lead := state.Agent{Project: "p", Name: "lead", Role: state.RoleLead}
	data, start, size, err := m.FileTail(context.Background(), lead, path, 0, 4)
	if err != nil || string(data) != "6789" || start != 6 || size != 10 {
		t.Fatalf("the end = %q at %d of %d, %v", data, start, size, err)
	}
	data, start, _, err = m.FileTail(context.Background(), lead, path, start, 100)
	if err != nil || string(data) != "012345" || start != 0 {
		t.Fatalf("the piece before = %q at %d, %v", data, start, err)
	}
	if _, _, _, err := m.FileTail(context.Background(), lead, filepath.Join(t.TempDir(), "none"), 0, 4); err == nil {
		t.Error("a missing file read without an error")
	}
}

func TestParseTailSkipsWhatTheLoginShellPrinted(t *testing.T) {
	data, start, size, err := parseTail([]byte("motd\n\x01agentbox-tail 21 14\nline 3\n"))
	if err != nil || string(data) != "line 3\n" || start != 14 || size != 21 {
		t.Fatalf("parsed %q at %d of %d, %v", data, start, size, err)
	}
	if _, _, _, err := parseTail([]byte("no such file")); err == nil {
		t.Error("an answer with no header parsed")
	}
}
