package cli

import (
	"strings"
	"testing"
)

func TestBackupNeedsATo(t *testing.T) {
	cmd := newBackupCmd(&app{})
	cmd.SetArgs(nil)
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	err := cmd.Execute()
	if err == nil {
		t.Fatal("a backup with no --to was taken")
	}
	if !strings.Contains(err.Error(), "--to is required") {
		t.Errorf("error = %q, want it to say --to is required", err)
	}
	// The reason, not only the flag: a default beside the original is the one
	// thing a backup must not do.
	if !strings.Contains(err.Error(), "not a backup") {
		t.Errorf("error = %q, want the reason a default is a bad idea", err)
	}
}

func TestBackupSize(t *testing.T) {
	for _, tc := range []struct {
		bytes int64
		want  string
	}{
		{512, "512 B"},
		{2 << 10, "2.0 KiB"},
		{5 << 20, "5.0 MiB"},
	} {
		if got := backupSize(tc.bytes); got != tc.want {
			t.Errorf("backupSize(%d) = %q, want %q", tc.bytes, got, tc.want)
		}
	}
}
