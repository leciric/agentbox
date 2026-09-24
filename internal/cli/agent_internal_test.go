package cli

import "testing"

func TestCommandLine(t *testing.T) {
	for _, c := range []struct {
		words []string
		want  string
	}{
		{[]string{"--", "ls", "-la"}, "ls -la"},
		{[]string{"ls", "-la"}, "ls -la"},
		{[]string{"--", "echo hi && pwd"}, "echo hi && pwd"},
		{[]string{"--", "--version"}, "--version"},
	} {
		if got, err := commandLine(c.words); err != nil || got != c.want {
			t.Errorf("commandLine(%q) = %q, %v; want %q", c.words, got, err, c.want)
		}
	}
	for _, words := range [][]string{nil, {"--"}} {
		if _, err := commandLine(words); err == nil {
			t.Errorf("commandLine(%q) succeeded, want a missing-command error", words)
		}
	}
}
