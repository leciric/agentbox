package github

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeGH writes a gh that does what `gh auth login --web` does with no
// terminal: prints the code and the page on stderr, then ends as ending
// says, keeping the token in GH_CONFIG_DIR for `gh auth token`. It writes
// down its arguments and its config directory next to itself.
func fakeGH(t *testing.T, ending string) (bin, dir string) {
	t.Helper()
	dir = t.TempDir()
	bin = filepath.Join(dir, "gh")
	script := `#!/bin/sh
case "$1 $2" in
"auth login")
	[ -n "$GH_CONFIG_DIR" ] && [ -z "$GH_TOKEN$GITHUB_TOKEN" ] || { echo "gh: wrong environment" >&2; exit 2; }
	echo "$*" >` + dir + `/args
	echo "$GH_CONFIG_DIR" >` + dir + `/config
	echo
	echo "! First copy your one-time code: ABCD-1234" >&2
	echo "Open this URL to continue in your web browser: https://github.com/login/device" >&2
	case ` + ending + ` in
	token) echo gho_fromgh >"$GH_CONFIG_DIR/token"; echo "✓ Authentication complete." >&2 ;;
	denied) echo "failed to authenticate via web browser: The authorization request was denied. (access_denied)" >&2; exit 1 ;;
	expired) echo "failed to authenticate via web browser: authentication timed out" >&2; exit 1 ;;
	wait) while :; do sleep 0.05; done ;;
	*) echo "error connecting to github.com" >&2; exit 1 ;;
	esac ;;
"auth token") cat "$GH_CONFIG_DIR/token" ;;
*) exit 3 ;;
esac
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, dir
}

func TestGHLoginReturnsGHsToken(t *testing.T) {
	t.Setenv("GH_TOKEN", "the-daemons-own")
	bin, dir := fakeGH(t, "token")
	var codes []LoginCode
	var log strings.Builder
	token, err := GHLogin{Bin: bin}.Run(context.Background(), &log, func(c LoginCode) { codes = append(codes, c) })
	if err != nil || token != "gho_fromgh" {
		t.Fatalf("Run() = %q, %v\n%s", token, err, log.String())
	}
	if len(codes) != 1 || codes[0].UserCode != "ABCD-1234" || codes[0].VerificationURI != "https://github.com/login/device" ||
		time.Until(codes[0].ExpiresAt) < 14*time.Minute {
		t.Errorf("codes = %+v", codes)
	}
	args, _ := os.ReadFile(filepath.Join(dir, "args"))
	for _, want := range []string{"--hostname github.com", "--git-protocol https", "--web", "--insecure-storage", "--scopes repo,read:org,workflow,gist"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("gh auth login %s, want %s", strings.TrimSpace(string(args)), want)
		}
	}
	// gh's config, with the token in it, is gone.
	config, _ := os.ReadFile(filepath.Join(dir, "config"))
	if c := strings.TrimSpace(string(config)); c == "" {
		t.Error("gh had no GH_CONFIG_DIR")
	} else if _, err := os.Stat(c); !os.IsNotExist(err) {
		t.Errorf("%s is still there: %v", c, err)
	}
	if !strings.Contains(log.String(), "Authentication complete") {
		t.Errorf("gh's output wasn't logged: %q", log.String())
	}
}

func TestGHLoginEndings(t *testing.T) {
	for ending, want := range map[string]error{"denied": ErrLoginDenied, "expired": ErrLoginExpired} {
		bin, _ := fakeGH(t, ending)
		if _, err := (GHLogin{Bin: bin}).Run(context.Background(), nil, nil); !errors.Is(err, want) {
			t.Errorf("%s: Run() = %v, want %v", ending, err, want)
		}
	}
	bin, _ := fakeGH(t, "offline")
	if _, err := (GHLogin{Bin: bin}).Run(context.Background(), nil, nil); err == nil || !strings.Contains(err.Error(), "error connecting to github.com") {
		t.Errorf("offline: Run() = %v", err)
	}
	if _, err := (GHLogin{Bin: filepath.Join(t.TempDir(), "gh")}).Run(context.Background(), nil, nil); !errors.Is(err, ErrNoGH) {
		t.Errorf("no gh: Run() = %v", err)
	}
}

// Cancelling stops gh, which would otherwise wait the code's 15 minutes.
func TestGHLoginCancel(t *testing.T) {
	bin, _ := fakeGH(t, "wait")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := GHLogin{Bin: bin}.Run(ctx, nil, func(LoginCode) { cancel() })
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run() = %v, want context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run() didn't end when cancelled")
	}
}
