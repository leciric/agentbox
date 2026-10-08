package cursor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The adapter's own tests, against a fake SDK, when this machine has Node.
func TestAdapter(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node isn't installed")
	}
	cmd := exec.Command(node, "--test", "adapter.test.mjs")
	cmd.Env = append(os.Environ(), "AGENTBOX_BRIEF=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("node --test adapter.test.mjs: %v\n%s", err, out)
	}
}

func TestSDKIsPinned(t *testing.T) {
	if SDKPackage != "npm:@cursor/sdk@"+SDKVersion() || !strings.Contains(SDKVersion(), ".") {
		t.Fatalf("SDKPackage = %q, SDKVersion = %q", SDKPackage, SDKVersion())
	}
	if len(Script) == 0 {
		t.Fatal("the adapter isn't embedded")
	}
}

// fakeNode is a "node" that ignores the script and runs body, a shell script
// that sees the script's arguments as $2 onwards.
func fakeNode(t *testing.T, body string) Helper {
	t.Helper()
	dir := t.TempDir()
	node := filepath.Join(dir, "node")
	if err := os.WriteFile(node, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return Helper{Node: node, Script: filepath.Join(dir, ScriptName), SDK: dir}
}

func TestModels(t *testing.T) {
	h := fakeNode(t, `[ "$2" = models ] && [ -n "$AGENTBOX_CURSOR_SDK" ] && [ "$CURSOR_API_KEY" = k ] || exit 3
echo "14:00 INFO noise" >&2
echo 'INFO a log line on stdout'
echo '[{"value":"composer-2","name":"Composer 2","efforts":[]},{"value":"claude-opus-5-5","name":"Opus","efforts":["low","high"]}]'
`)
	models, err := h.Models(context.Background(), "k")
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[1].Value != "claude-opus-5-5" || strings.Join(models[1].Efforts, ",") != "low,high" {
		t.Fatalf("Models = %+v", models)
	}
}

func TestCheckKeyReadsTheKeyFromStdin(t *testing.T) {
	store := filepath.Join(t.TempDir(), "auth.json")
	h := fakeNode(t, `[ "$2" = check-key ] || exit 3
key=$(cat)
[ "$key" = secret ] || { echo "Cursor refused the sign-in (Invalid User API Key)" >&2; exit 1; }
echo "{\"apiKey\":\"$key\"}" > "$3"
echo '{"email":"a@b.c"}'
`)
	email, err := h.CheckKey(context.Background(), "secret", store)
	if err != nil || email != "a@b.c" {
		t.Fatalf("CheckKey = %q, %v", email, err)
	}
	if _, err := os.Stat(store); err != nil {
		t.Fatal(err)
	}
	// The script's last line on stderr is the reason.
	if _, err := h.CheckKey(context.Background(), "wrong", store); err == nil || !strings.Contains(err.Error(), "Invalid User API Key") {
		t.Fatalf("a refused key: %v", err)
	}
}

func TestLoginShowsTheURLBeforeItFinishes(t *testing.T) {
	h := fakeNode(t, `[ "$2" = login ] || exit 3
echo '{"loginUrl":"https://cursor.com/loginDeepControl?x"}'
echo '{"email":"a@b.c","expiresAtMs":1}'
`)
	var urls []string
	email, err := h.Login(context.Background(), "/store", func(u string) { urls = append(urls, u) })
	if err != nil || email != "a@b.c" || len(urls) != 1 || urls[0] != "https://cursor.com/loginDeepControl?x" {
		t.Fatalf("Login = %q, %v, urls %v", email, err, urls)
	}
}
