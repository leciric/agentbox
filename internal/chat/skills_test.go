package chat

import (
	"reflect"
	"testing"
)

func TestSkillPromptRewritesMentionsForEachTool(t *testing.T) {
	known := map[string]bool{"review": true, "deploy-app": true}
	for _, tc := range []struct {
		tool, text string
		want       []string
	}{
		// Nothing to dispatch: an unknown $word, money, a $ inside a word.
		{"claude", "echo $HOME costs $5 and a$review", []string{"echo $HOME costs $5 and a$review"}},
		{"claude", "$review the last commit", []string{"/review the last commit"}},
		// The last mention gets a block of its own; earlier ones go inline.
		{"claude", "first $deploy-app then\n$review it carefully\n", []string{"first /deploy-app then", "/review it carefully"}},
		{"claude", "please $review", []string{"please", "/review"}},
		{"codex", "$review the last commit", []string{"$review the last commit"}},
		{"cursor", "use $review and $deploy-app.", []string{"use /review and $deploy-app."}},
		{"cursor", "use $review and $deploy-app", []string{"use /review and /deploy-app"}},
		{"opencode", "$review this", []string{"$review this\n\n[Use the \"review\" skill for this: load it with your skill tool first.]"}},
		{"opencode", "$review then $deploy-app then $review", []string{"$review then $deploy-app then $review\n\n[Use the \"review\", \"deploy-app\" skills for this: load them with your skill tool first.]"}},
	} {
		if got := skillPrompt(tc.tool, tc.text, known); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("skillPrompt(%s, %q) = %q; want %q", tc.tool, tc.text, got, tc.want)
		}
	}
}
