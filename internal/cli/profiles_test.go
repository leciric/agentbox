package cli_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeDaemon answers the two routes these tests need: what a daemon is, which
// `profile add` asks first, and the cluster list a command reads once it is the
// active profile.
func fakeDaemon(t *testing.T, version string) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/version", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"version":%q}`, version)
	})
	mux.HandleFunc("GET /v1/clusters", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

// noProfiles clears what an installed CLI would read from its own machine, so a
// test's answer comes only from the file it writes.
func noProfiles(t *testing.T) {
	t.Helper()
	t.Setenv("AGENTBOX_PROFILE", "")
	t.Setenv("AGENTBOX_NO_PROFILE_BANNER", "")
}

// TestProfileAddRefusesAnAddressThatDoesNotAnswer: a typo is refused where it
// is made, rather than becoming the next command's strange failure.
func TestProfileAddRefusesAnAddressThatDoesNotAnswer(t *testing.T) {
	isolate(t)
	noProfiles(t)
	out, err := run(t, "", "profile", "add", "nas", "http://127.0.0.1:1")
	if err == nil {
		t.Fatalf("saved a profile for an address that answers nothing:\n%s", out)
	}
	if !strings.Contains(err.Error(), "127.0.0.1:1") {
		t.Errorf("error = %v, want the address that failed", err)
	}
	out, err = run(t, "", "profile")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "nas") {
		t.Errorf("profile list has nas after a refused add:\n%s", out)
	}
}

// TestProfileAddListUseAndAct is the whole path: save an address, see it in the
// list with its answer, switch to it, and have a command act on it and say so.
func TestProfileAddListUseAndAct(t *testing.T) {
	isolate(t)
	noProfiles(t)
	address := fakeDaemon(t, "9.9.9")

	out, err := run(t, "", "profile", "add", "nas", address)
	if err != nil {
		t.Fatalf("profile add: %v\n%s", err, out)
	}
	mustContain(t, out, "Added profile nas", "9.9.9", address, "agentbox use nas")

	out, err = run(t, "", "profile")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "PROFILE", "local", "this machine", "nas", "ok 9.9.9")

	out, err = run(t, "", "use", "nas")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Now acting on profile nas", address)

	// A command that can act elsewhere acts on the active profile, and its
	// banner names it first, on stderr.
	out, err = run(t, "", "cluster", "list")
	if err != nil {
		t.Fatalf("cluster list: %v\n%s", err, out)
	}
	if !strings.HasPrefix(out, "profile nas  "+address+"\n") {
		t.Errorf("output doesn't start with the profile banner:\n%s", out)
	}
	mustContain(t, out, "No clusters")

	// `use` with no name says where it is, and --local steps out for one
	// command.
	out, err = run(t, "", "use")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "profile nas  "+address, "agentbox profile lists them")

	out, err = run(t, "", "--local", "cluster", "list")
	if err == nil {
		t.Fatalf("--local acted on the profile:\n%s", out)
	}
	if !strings.HasPrefix(out, "this machine  (profile nas is active; this command acts here)\n") {
		t.Errorf("--local didn't say it acted here:\n%s", out)
	}
}

// TestMachineOnlyCommandActsHereWhateverTheProfile: a profile is not the user
// asking `agentbox env` to run elsewhere, so it acts on this machine and says
// which profile it did not follow.
func TestMachineOnlyCommandActsHereWhateverTheProfile(t *testing.T) {
	isolate(t)
	noProfiles(t)
	address := fakeDaemon(t, "9.9.9")
	if _, err := run(t, "", "profile", "add", "nas", address); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "", "use", "nas"); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, "", "env", "list")
	if err == nil {
		t.Fatalf("env list: %v", err)
	}
	mustContain(t, out, "this machine  (profile nas is active; this command acts here)")
}

// TestProfileBannerIsSilentWithoutProfiles: an installation that never made one
// reads exactly as it did before profiles existed.
func TestProfileBannerIsSilentWithoutProfiles(t *testing.T) {
	isolate(t)
	noProfiles(t)
	out, _ := run(t, "", "cluster", "list")
	if strings.Contains(out, "profile ") || strings.Contains(out, "this machine  (") {
		t.Errorf("a profile line was printed with no profiles:\n%s", out)
	}
	// The file itself is not created by reading it.
	if _, err := os.Stat(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "agentbox", "profiles.json")); !os.IsNotExist(err) {
		t.Errorf("reading a profile list wrote profiles.json: %v", err)
	}
}

// TestProfileNamesAndTheLocalOne: a name reads as one word, `local` is always
// this machine, and an address carries no credential.
func TestProfileNamesAndTheLocalOne(t *testing.T) {
	isolate(t)
	noProfiles(t)
	address := fakeDaemon(t, "9.9.9")
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "local is taken", args: []string{"profile", "add", "local", address}, want: "local is this machine"},
		{name: "a name with a space", args: []string{"profile", "add", "my nas", address}, want: "lowercase letters"},
		{name: "a name with a slash", args: []string{"profile", "add", "nas/1", address}, want: "lowercase letters"},
		{name: "a credential in the address", args: []string{"profile", "add", "nas", "https://user:secret@host/x"}, want: "no user or password"},
		{name: "removing local", args: []string{"profile", "rm", "local"}, want: "local is this machine"},
		{name: "an unknown profile", args: []string{"use", "nope"}, want: "no profile named \"nope\""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := run(t, "", tc.args...)
			if err == nil {
				t.Fatalf("%v was accepted", tc.args)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want %q", err, tc.want)
			}
		})
	}
	// The address with a credential was refused before anything was saved.
	out, err := run(t, "", "profile")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "nas") {
		t.Errorf("a refused name or address was saved:\n%s", out)
	}
}
