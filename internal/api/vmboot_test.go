package api

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// With no directory or no boot.json the boot isn't Safe; with one, it says
// what the supervisor wrote.
func TestReadVMBoot(t *testing.T) {
	if b, err := ReadVMBoot(""); err != nil || b.Safe {
		t.Fatalf("ReadVMBoot(\"\") = %+v, %v", b, err)
	}
	dir := t.TempDir()
	if b, err := ReadVMBoot(dir); err != nil || b.Safe {
		t.Fatalf("ReadVMBoot() with no file = %+v, %v", b, err)
	}
	at := time.Date(2026, 11, 18, 12, 8, 0, 0, time.UTC)
	data := `{"started":"2026-11-18T12:08:00Z","safe":true,"why":"the VM went down 6 times","crashes":["2026-11-18T12:01:00Z"]}`
	if err := os.WriteFile(filepath.Join(dir, "boot.json"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := ReadVMBoot(dir)
	if err != nil || !b.Safe || b.Why != "the VM went down 6 times" || !b.Started.Equal(at) || len(b.Crashes) != 1 {
		t.Fatalf("ReadVMBoot() = %+v, %v", b, err)
	}
}
