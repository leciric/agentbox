package report

import (
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	r := NewRedactor("/home/lint", "/home/lint.linux/", "")
	cases := []struct{ name, in, want string }{
		{"github token", "push failed with ghp_0123456789abcdefABCDEF0123456789abcd", "push failed with [redacted]"},
		{"fine-grained github token", "GH github_pat_11ABCDEFG0123456789_abcdefghijklmnop", "GH [redacted]"},
		{"anthropic key", "key sk-ant-api03-AbC_dEf-0123456789", "key [redacted]"},
		{"claude oauth token", "sk-ant-oat01-ZYXWVUTSRQ_0123456789-abc expired", "[redacted] expired"},
		{"openai key", "using sk-proj-abcdefghijklmnopqrstuvwxyz012345", "using [redacted]"},
		{"aws key", "AKIAABCDEFGHIJKLMNOP in env", "[redacted] in env"},
		{"jwt", "id eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0In0.c2lnbmF0dXJlX2hlcmU done", "id [redacted] done"},
		{"bearer", "curl -H 'Authorization: Bearer abc.def-ghi_jkl' x", "curl -H 'Authorization: [redacted]' x"},
		{"bearer alone", "sent bearer 0123456789abcdef", "sent bearer [redacted]"},
		{"basic auth header", "authorization=Basic dXNlcjpwYXNz", "authorization=[redacted]"},
		{"named secret", "CLAUDE_CODE_OAUTH_TOKEN=abc123 next", "CLAUDE_CODE_OAUTH_TOKEN=[redacted] next"},
		{"json secret", `{"accessToken":"abc123","refresh_token": "xyz"}`, `{"accessToken":"[redacted]","refresh_token": "[redacted]"}`},
		{"password", "password: hunter2", "password: [redacted]"},
		{"api key header", "X-Api-Key: 1234abcd", "X-Api-Key: [redacted]"},
		{"query string", "GET /login?code=1&token=s3cr3t&x=1", "GET /login?code=1&token=[redacted]&x=1"},
		{"url credentials", "cloning https://lint:s3cr3t@github.com/a/b", "cloning https://[redacted]@github.com/a/b"},
		{"private key", "key:\n-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXk\n-----END OPENSSH PRIVATE KEY-----\nafter", "key:\n[private key]\nafter"},
		{"email", "signed in as leandro.ciric@example.com.", "signed in as [email]."},
		{"git ssh is not an email", "git@github.com:leciric/agentbox.git", "git@github.com:leciric/agentbox.git"},
		{"noreply is not an email", "Co-Authored-By: Claude <noreply@anthropic.com>", "Co-Authored-By: Claude <noreply@anthropic.com>"},
		{"own home", "open /home/lint/.agentbox/state.db", "open ~/.agentbox/state.db"},
		{"home inside home", "HOME=/home/lint.linux and /home/lint", "HOME=~ and ~"},
		{"home that only starts the same", "/home/linter/x", "/home/[user]/x"},
		{"other linux home", "at /home/alice/projects", "at /home/[user]/projects"},
		{"mac home", "/Users/alice/Library/Logs", "/Users/[user]/Library/Logs"},
		{"windows home", `C:\Users\Alice\AppData\Local`, `C:\Users\[user]\AppData\Local`},
		{"escaped windows home", `"C:\\Users\\Alice\\AppData"`, `"C:\Users\[user]\\AppData"`},
		{"token counts stay", "turn done: input_tokens=1234 output_tokens: 56", "turn done: input_tokens=1234 output_tokens: 56"},
		{"plain log line stays", "2026-10-01 12:00:00 agent-12 started in 3.2s", "2026-10-01 12:00:00 agent-12 started in 3.2s"},
		{"commit hashes stay", "at 8085c13a4f0e9b1d2c3a4b5c6d7e8f9a0b1c2d3e", "at 8085c13a4f0e9b1d2c3a4b5c6d7e8f9a0b1c2d3e"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := r.Redact(c.in)
			if got != c.want {
				t.Errorf("Redact(%q)\n got %q\nwant %q", c.in, got, c.want)
			}
			if again := r.Redact(got); again != got {
				t.Errorf("redacting again changed it: %q", again)
			}
		})
	}
}

// TestRedactLeavesNoSecret runs a log full of secrets through and checks
// none of them survive, whatever surrounds them.
func TestRedactLeavesNoSecret(t *testing.T) {
	secrets := []string{"ghp_0123456789abcdefABCDEF0123456789abcd", "sk-ant-oat01-ZYXWVUTSRQ_0123456789", "hunter2", "lint@example.org", "/home/lint"}
	log := strings.Join([]string{
		"GITHUB_TOKEN=ghp_0123456789abcdefABCDEF0123456789abcd",
		`{"claudeAiOauth":{"accessToken":"sk-ant-oat01-ZYXWVUTSRQ_0123456789"}}`,
		"db password=hunter2",
		"git config user.email lint@example.org",
		"worktree /home/lint/www/app",
	}, "\n")
	got := NewRedactor("/home/lint").Redact(log)
	for _, s := range secrets {
		if strings.Contains(got, s) {
			t.Errorf("%q survived:\n%s", s, got)
		}
	}
}
