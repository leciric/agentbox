package redact

import "testing"

func TestSecrets(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"github token", "push failed with ghp_0123456789abcdefABCDEF0123456789abcd", "push failed with [redacted]"},
		{"fine-grained github token", "GH github_pat_11ABCDEFG0123456789_abcdefghijklmnop", "GH [redacted]"},
		{"anthropic key", "key sk-ant-api03-AbC_dEf-0123456789", "key [redacted]"},
		{"openai key", "using sk-proj-abcdefghijklmnopqrstuvwxyz012345", "using [redacted]"},
		{"slack token", "posted with xoxb-1234567890-abcdefghij", "posted with [redacted]"},
		{"aws key", "AKIAABCDEFGHIJKLMNOP in env", "[redacted] in env"},
		{"jwt", "id eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0In0.c2lnbmF0dXJlX2hlcmU done", "id [redacted] done"},
		{"private key", "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXk\n-----END OPENSSH PRIVATE KEY-----\nthen", "[private key]\nthen"},
		{"bearer header", "curl -H 'Authorization: Bearer abc.def-ghi_jkl' x", "curl -H 'Authorization: [redacted]' x"},
		{"basic header", "authorization=Basic dXNlcjpwYXNz", "authorization=[redacted]"},
		{"bearer alone", "sent bearer 0123456789abcdef", "sent bearer [redacted]"},
		{"env var", "export CURSOR_API_KEY=cur_abc123 first", "export CURSOR_API_KEY=[redacted] first"},
		{"json field", `{"accessToken":"abc123","refresh_token": "xyz"}`, `{"accessToken":"[redacted]","refresh_token": "[redacted]"}`},
		{"password", "password: hunter2", "password: [redacted]"},
		{"url credentials", "cloning https://lint:s3cr3t@github.com/a/b", "cloning https://[redacted]@github.com/a/b"},
		{"query string", "GET https://x.dev/cb?code=1&access_token=s3cr3t&x=1", "GET https://x.dev/cb?code=1&access_token=[redacted]&x=1"},
		{"token count", "input_tokens=1234 max_tokens: 4096", "input_tokens=1234 max_tokens: 4096"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Secrets(tc.in); got != tc.want {
				t.Errorf("Secrets(%q)\n = %q\nwant %q", tc.in, got, tc.want)
			}
			if again := Secrets(Secrets(tc.in)); again != tc.want {
				t.Errorf("twice: %q", again)
			}
		})
	}
}

// What memory is full of, and must keep: hashes, ids, numbers, paths and
// URLs without credentials.
func TestSecretsKeepsHashesAndIDs(t *testing.T) {
	for _, s := range []string{
		"merged as c769d9d0a1b2c3d4e5f60718293a4b5c6d7e8f90 in #252",
		"sha256:9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
		"memory mem_01J9ZK3X4Y5Z6A7B8C9D0E1F2G and question q_7f3a9c2e, event ev_b41c2d",
		"session 5f2b7c1e-8d4a-4e3b-9c6f-0a1b2c3d4e5f was rolled",
		"state.db is at /home/lint.linux/.local/share/agentbox/state.db",
		"https://github.com/leciric/agentbox/pull/251?tab=files#diff-abc123",
		"git@github.com:leciric/agentbox.git",
		"the token budget is 200000 and the session lasted 42 minutes",
		"input_tokens=1234",
		"base64 aGVsbG8gd29ybGQgdGhpcyBpcyBub3QgYSBzZWNyZXQ=",
	} {
		if got := Secrets(s); got != s {
			t.Errorf("Secrets(%q) = %q, want it unchanged", s, got)
		}
	}
}

func TestNamed(t *testing.T) {
	for _, tc := range []struct{ name, value, want string }{
		{"apiKey", "abc123", Placeholder},
		{"GH_TOKEN", "anything", Placeholder},
		{"password", "", ""},
		{"inputTokens", "1234", "1234"},
		{"secretName", "[redacted]", "[redacted]"},
		{"reason", "it says Authorization: Bearer abcdefghijkl", "it says Authorization: [redacted]"},
		{"sha", "c769d9d0a1b2c3d4e5f60718293a4b5c6d7e8f90", "c769d9d0a1b2c3d4e5f60718293a4b5c6d7e8f90"},
	} {
		if got := Named(tc.name, tc.value); got != tc.want {
			t.Errorf("Named(%q, %q) = %q, want %q", tc.name, tc.value, got, tc.want)
		}
	}
}
