package cli

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// keyringSecret fetches a Chromium browser's cookie-encryption passphrase from
// this computer's keyring, so 'from-browser' can import its sealed cookies.
// The desktop app does the same in its main process (desktop/src/main/
// browserkeys.ts); the command line can do it only where it runs on the host
// itself, not from inside AgentBox's VM. It returns "" on any failure, so the
// import falls back to the unsealed cookies. The secret is never printed.
func keyringSecret(keyring string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	switch runtime.GOOS {
	case "darwin":
		k, ok := macKeychain[keyring]
		if !ok {
			return ""
		}
		return runQuiet(ctx, "/usr/bin/security", "find-generic-password", "-w", "-s", k.service, "-a", k.account)
	case "linux":
		if s := runQuiet(ctx, "secret-tool", "lookup", "xdg:schema", "chrome_libsecret_os_crypt_password_v2", "application", keyring); s != "" {
			return s
		}
		return runQuiet(ctx, "secret-tool", "lookup", "application", keyring)
	}
	return ""
}

var macKeychain = map[string]struct{ service, account string }{
	"chrome":   {"Chrome Safe Storage", "Chrome"},
	"chromium": {"Chromium Safe Storage", "Chromium"},
	"brave":    {"Brave Safe Storage", "Brave"},
	"msedge":   {"Microsoft Edge Safe Storage", "Microsoft Edge"},
	"vivaldi":  {"Vivaldi Safe Storage", "Vivaldi"},
}

func runQuiet(ctx context.Context, name string, args ...string) string {
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(out), "\n")
}
