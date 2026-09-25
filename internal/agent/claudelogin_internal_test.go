package agent

import (
	"strings"
	"testing"
)

// realOutput is what `claude setup-token` (2.1.273) draws on an 80-column pty,
// captured on a real machine. The URL is a terminal hyperlink whose text the
// terminal then wrapped across five lines, which is why the escape sequence is
// the one worth reading.
const realOutput = "\x1b7\x1b[r\x1b8\x1b[?25h\x1b[?25l\x1b[?2004h\x1b[?2031h\x1b[?1004hWelcome\x1b[9Gto\x1b[12GClaude\x1b[19GCode\x1b[24Gv2.1.273\r\r\n" +
	"\r\r\n" +
	"\x1b[2GThis\x1b[7Gwill\x1b[12Gguide\x1b[18Gyou\x1b[22Gthrough\x1b[30Glong-lived\x1b[41G(1-year)\x1b[50Gauth\x1b[55Gtoken\x1b[61Gsetup\x1b[67Gfor\x1b[71Gyour\r\r\n" +
	"\x1b[2GClaude\x1b[9Gaccount.\x1b[18GClaude\x1b[25Gsubscription\x1b[38Grequired.\r\r\n" +
	"\r\r\n" +
	"\x1b[2GBrowser\x1b[10Gdidn't\x1b[17Gopen?\x1b[23GUse\x1b[27Gthe\x1b[31Gurl\x1b[35Gbelow\x1b[41Gto\x1b[44Gsign\x1b[49Gin\x1b[52G(c\x1b[55Gto\x1b[58Gcopy)\r\r\n" +
	"\r\r\n" +
	"\x1b]8;id=13w88kp;https://claude.com/cai/oauth/authorize?code=true&client_id=9d1c250a-e61b-44d9-88ed-5944d1962f5e&response_type=code&redirect_uri=https%3A%2F%2Fplatform.claude.com%2Foauth%2Fcode%2Fcallback&scope=user%3Ainference&code_challenge=t749CSxy9Aco_e2lpLcza2Q5GZPM0XkOHflWPGBgXFE&code_challenge_method=S256&state=qwFlJWLfYUS83JkzhIg9UtoldOvrRhM0vvw9MxuGb_I\x07https://claude.com/cai/oauth/authorize?code=true&client_id=9d1c250a-e61b-44d9-88\x1b]8;;\x07\r\r\n" +
	"\x1b]8;id=13w88kp;https://claude.com/cai/oauth/authorize?code=true&client_id=9d1c250a-e61b-44d9-88ed-5944d1962f5e&response_type=code&redirect_uri=https%3A%2F%2Fplatform.claude.com%2Foauth%2Fcode%2Fcallback&scope=user%3Ainference&code_challenge=t749CSxy9Aco_e2lpLcza2Q5GZPM0XkOHflWPGBgXFE&code_challenge_method=S256&state=qwFlJWLfYUS83JkzhIg9UtoldOvrRhM0vvw9MxuGb_I\x07ed-5944d1962f5e&response_type=code&redirect_uri=https%3A%2F%2Fplatform.claude.co\x1b]8;;\x07\r\r\n" +
	"\r\r\n" +
	"\x1b[2GPaste\x1b[8Gcode\x1b[13Ghere\x1b[18Gif\x1b[21Gprompted\x1b[30G>\r\r\n"

const wantURL = "https://claude.com/cai/oauth/authorize?code=true&client_id=9d1c250a-e61b-44d9-88ed-5944d1962f5e&response_type=code&redirect_uri=https%3A%2F%2Fplatform.claude.com%2Foauth%2Fcode%2Fcallback&scope=user%3Ainference&code_challenge=t749CSxy9Aco_e2lpLcza2Q5GZPM0XkOHflWPGBgXFE&code_challenge_method=S256&state=qwFlJWLfYUS83JkzhIg9UtoldOvrRhM0vvw9MxuGb_I"

// The token, as Claude Code prints it: on its own line, in the "warning"
// colour, on a pty wide enough not to wrap it (setupTokenCols).
const tokenScreen = "\x1b[32m✓ Long-lived authentication token created successfully!\x1b[39m\r\n" +
	"\r\n" +
	"Your OAuth token (valid for 1 year):\r\n" +
	"\x1b[33msk-ant-oat01-aBcD3f_GhIjKlMnOpQrStUvWxYz0123456789-abcdefghijklmnopqrstuvwxyz_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789AA\x1b[39m\r\n" +
	"\x1b[2mStore this token securely. You won't be able to see it again.\x1b[22m\r\n"

const wantToken = "sk-ant-oat01-aBcD3f_GhIjKlMnOpQrStUvWxYz0123456789-abcdefghijklmnopqrstuvwxyz_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789AA"

func TestLoginURL(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct{ out, want string }{
		"a real 80-column screen": {realOutput, wantURL},
		// Without hyperlinks there is only the text, and a wide pty is what
		// keeps it on one line.
		"plain text, no hyperlink": {
			"Browser didn't open? Use the url below to sign in\r\n\r\n" + wantURL + "\r\n",
			wantURL,
		},
		"nothing yet": {"\x1b[2GWelcome to Claude Code\r\n", ""},
		"a link that isn't a login": {
			"See \x1b]8;;https://docs.claude.com/en/docs/claude-code\x07the docs\x1b]8;;\x07 for help\r\n",
			"",
		},
		// Somebody else's host with "claude.com" in it is not the sign-in page.
		"a lookalike host": {"https://claude.com.evil.test/oauth/authorize?code=true\r\n", ""},
	} {
		t.Run(name, func(t *testing.T) {
			if got := loginURL(tc.out); got != tc.want {
				t.Errorf("loginURL = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSetupTokenIn(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct{ out, want string }{
		"the token screen": {tokenScreen, wantToken},
		"before the token": {realOutput, ""},
		// The warning names the variable, not a token.
		"only the prefix in prose": {
			"Warning: CLAUDE_CODE_OAUTH_TOKEN is set in your environment (sk-ant-oat…)\r\n",
			"",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := setupTokenIn(tc.out); got != tc.want {
				t.Errorf("setupTokenIn = %q, want %q", got, tc.want)
			}
		})
	}
}

// A token split across reads is the ordinary case — the pty hands over
// whatever has arrived — and half of one, saved, is a login that fails much
// later inside an agent. It is only a token once something follows it.
func TestSetupTokenInWaitsForTheWholeToken(t *testing.T) {
	t.Parallel()
	cut := strings.Index(tokenScreen, wantToken) + len(wantToken) - 30
	if got := setupTokenIn(tokenScreen[:cut]); got != "" {
		t.Errorf("setupTokenIn of a half-read token = %q, want %q", got, "")
	}
	if got := setupTokenIn(tokenScreen); got != wantToken {
		t.Errorf("setupTokenIn of the whole screen = %q, want %q", got, wantToken)
	}
}

func TestCleanTerminal(t *testing.T) {
	t.Parallel()
	got := cleanTerminal(realOutput)
	for _, escape := range []string{"\x1b", "\x07", "\r"} {
		if strings.Contains(got, escape) {
			t.Errorf("cleanTerminal left %q in %q", escape, got)
		}
	}
	if !strings.Contains(got, "Paste") || !strings.Contains(got, "Browser") {
		t.Errorf("cleanTerminal dropped the text: %q", got)
	}
}
