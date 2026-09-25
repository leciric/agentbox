package agent

import (
	"testing"
	"time"
)

func TestPromptCacheTTLOf(t *testing.T) {
	t.Parallel()
	for doc, want := range map[string]time.Duration{
		``:                         0,
		`not json`:                 0,
		`{}`:                       0,
		`{"promptCacheTtl": "5m"}`: 5 * time.Minute,
		`{"promptCacheTtl": "1h"}`: time.Hour,
		`{"promptCacheTtl": "2h"}`: 0,
		`{"promptCacheTtl": "1h", "env": {"CLAUDE_CODE_PROMPT_CACHE_TTL": "5m"}}`:    5 * time.Minute,
		`{"promptCacheTtl": "5m", "env": {"CLAUDE_CODE_PROMPT_CACHE_TTL": "bogus"}}`: 5 * time.Minute,
		`{"env": {"CLAUDE_CODE_PROMPT_CACHE_TTL": "1h", "OTHER": 3}}`:                time.Hour,
		`{"promptCacheTtl": "1h", "env": {"OTHER": 3}}`:                              time.Hour,
	} {
		if got := promptCacheTTLOf([]byte(doc)); got != want {
			t.Errorf("promptCacheTTLOf(%s) = %s, want %s", doc, got, want)
		}
	}
}
