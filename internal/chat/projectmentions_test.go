package chat

import "testing"

func TestProjectMentionTextAddsTheNameAndPath(t *testing.T) {
	roots := map[string]string{"saiapp4": "/home/lint/www/stf/saiapp4", "web": "/w"}
	for _, tc := range []struct{ in, want string }{
		{"no mentions here", "no mentions here"},
		{"see @saiapp4 for it", "see @saiapp4 (project at /home/lint/www/stf/saiapp4) for it"},
		{"@saiapp4", "@saiapp4 (project at /home/lint/www/stf/saiapp4)"},
		{"like @saiapp4, and @web.", "like @saiapp4 (project at /home/lint/www/stf/saiapp4), and @web (project at /w)."},
		{"twice @web @web\n", "twice @web (project at /w) @web (project at /w)\n"},
		// Not a mention: another name, a longer name, a path, an email, no project.
		{"@saiapp40 @saiapp4/src/a.go @web-app", "@saiapp40 @saiapp4/src/a.go @web-app"},
		{"mail me@web or @nobody", "mail me@web or @nobody"},
		{"@@web", "@@web"},
	} {
		if got := projectMentionText(tc.in, roots); got != tc.want {
			t.Errorf("projectMentionText(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := projectMentionText("@web", nil); got != "@web" {
		t.Errorf("with no projects: %q", got)
	}
}
