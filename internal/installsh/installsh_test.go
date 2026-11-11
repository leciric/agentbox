package installsh

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// machine is a fake Linux machine for install.sh: the files it reads under
// AGENTBOX_INSTALL_ROOT, and what the stubbed uname and df answer.
type machine struct {
	os, arch  string // uname -s, uname -m
	memKB     int
	freeKB    int
	kvm       string // "rw", "denied" or "" for none
	fuse      bool   // /dev/fuse
	libfuse   bool   // libfuse.so.2
	osRelease string // ID and ID_LIKE lines
	pacman    bool   // a pacman on PATH
	cpuFlags  string // /proc/cpuinfo's flags
}

func goodMachine() machine {
	return machine{
		os: "Linux", arch: "x86_64", memKB: 16 << 20, freeKB: 200 << 20,
		kvm: "rw", fuse: true, libfuse: true, osRelease: "ID=ubuntu\nID_LIKE=debian\n",
	}
}

// shells are the shells the script is run with: sh, and dash, which is what
// Debian's and Ubuntu's sh is, when it's here.
func shells(t *testing.T) []string {
	var out []string
	for _, sh := range []string{"sh", "dash", "bash"} {
		if p, err := exec.LookPath(sh); err == nil {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		t.Skip("no sh")
	}
	return out
}

func write(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

// setup makes m under a new directory and returns the environment to run
// the script in, and the file the stubs log their calls to.
func setup(t *testing.T, m machine) (env []string, home, calls string) {
	t.Helper()
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	bin := filepath.Join(dir, "bin")
	home = filepath.Join(dir, "home")
	calls = filepath.Join(dir, "calls")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "proc/meminfo"), fmt.Sprintf("MemTotal:       %d kB\nMemFree: 1 kB\n", m.memKB), 0o644)
	write(t, filepath.Join(root, "etc/os-release"), m.osRelease, 0o644)
	write(t, filepath.Join(root, "proc/cpuinfo"), "processor\t: 0\nflags\t\t: "+m.cpuFlags+"\n", 0o644)
	switch m.kvm {
	case "rw":
		write(t, filepath.Join(root, "dev/kvm"), "", 0o666)
	case "denied":
		write(t, filepath.Join(root, "dev/kvm"), "", 0o000)
	}
	if m.fuse {
		write(t, filepath.Join(root, "dev/fuse"), "", 0o666)
	}
	if m.libfuse {
		write(t, filepath.Join(root, "usr/lib/x86_64-linux-gnu/libfuse.so.2"), "", 0o644)
	}
	write(t, filepath.Join(bin, "uname"), fmt.Sprintf("#!/bin/sh\ncase $1 in -s) echo %s ;; -m) echo %s ;; esac\n", m.os, m.arch), 0o755)
	write(t, filepath.Join(bin, "df"), fmt.Sprintf("#!/bin/sh\necho 'Filesystem 1024-blocks Used Available Capacity Mounted on'\necho \"/dev/x 999999999 1 %d 1%% $3\"\n", m.freeKB), 0o755)
	write(t, filepath.Join(bin, "sudo"), "#!/bin/sh\necho \"sudo $*\" >>\""+calls+"\"\nexec \"$@\"\n", 0o755)
	write(t, filepath.Join(bin, "apt-get"), "#!/bin/sh\necho \"apt-get $*\" >>\""+calls+"\"\n", 0o755)
	if m.pacman {
		write(t, filepath.Join(bin, "pacman"), "#!/bin/sh\necho \"pacman $*\" >>\""+calls+"\"\n", 0o755)
	}
	env = []string{
		"PATH=" + bin + ":/usr/bin:/bin",
		"HOME=" + home,
		"AGENTBOX_INSTALL_ROOT=" + root,
		"DOWNLOADS_URL=http://127.0.0.1:1", // nothing is downloaded unless a test serves it
	}
	return env, home, calls
}

func run(t *testing.T, shell string, env []string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(shell, append([]string{"-s", "--"}, args...)...)
	cmd.Stdin = strings.NewReader(string(Script)) // as from curl | sh
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return string(out), code
}

func TestChecks(t *testing.T) {
	tests := []struct {
		name   string
		change func(*machine)
		args   []string
		code   int
		want   []string
		absent []string
	}{
		{name: "ok", code: 0, want: []string{"it would install the .deb, with apt-get"}, absent: []string{"can't be installed"}},
		{name: "no kvm", change: func(m *machine) { m.kvm = "" }, code: 1, want: []string{"no /dev/kvm", "VT-x or AMD-V", "nested virtualization"}},
		{name: "no kvm module", change: func(m *machine) { m.kvm, m.cpuFlags = "", "fpu vme vmx sse" }, code: 0, want: []string{"KVM's module isn't loaded", "Setup loads it", "can run AgentBox"}},
		{name: "no kvm module, AMD", change: func(m *machine) { m.kvm, m.cpuFlags = "", "fpu svm" }, code: 0, want: []string{"KVM's module isn't loaded"}},
		{name: "kvm denied", change: func(m *machine) { m.kvm = "denied" }, code: 0, want: []string{"can't open /dev/kvm yet", "one password prompt", "can run AgentBox"}},
		{name: "low memory", change: func(m *machine) { m.memKB = 4 << 20 }, code: 1, want: []string{"4 GB of memory", "at least 8 GB"}},
		{name: "8 GB that reports less", change: func(m *machine) { m.memKB = 7_900_000 }, code: 0},
		{name: "low disk", change: func(m *machine) { m.freeKB = 10_000_000 }, code: 1, want: []string{"10 GB free", "at least 30 GB"}},
		{name: "arm64", change: func(m *machine) { m.arch = "aarch64" }, code: 1, want: []string{"ARM (arm64)", "only on x86_64"}},
		{name: "other processor", change: func(m *machine) { m.arch = "riscv64" }, code: 1, want: []string{"riscv64"}},
		{name: "every problem at once", change: func(m *machine) { m.kvm, m.memKB, m.freeKB = "", 1<<20, 1 }, code: 1, want: []string{"/dev/kvm", "8 GB", "30 GB"}},
		{name: "no fuse with the deb", change: func(m *machine) { m.fuse, m.libfuse = false, false }, code: 0, absent: []string{"fuse"}},
		{name: "no fuse with the AppImage", change: func(m *machine) { m.fuse = false; m.osRelease = "ID=fedora\n" }, code: 1, want: []string{"no /dev/fuse"}},
		{name: "no libfuse2 with the AppImage", change: func(m *machine) { m.libfuse = false }, args: []string{"--check", "--appimage"}, code: 1, want: []string{"libfuse2 isn't installed"}},
		{name: "fedora", change: func(m *machine) { m.osRelease = "ID=fedora\n" }, code: 0, want: []string{"the AppImage, in ~/.local/bin"}},
		{name: "arch", change: func(m *machine) { m.osRelease = "ID=arch\n"; m.pacman = true }, code: 0, want: []string{"the .pacman, with pacman"}},
		{name: "arch-like without pacman", change: func(m *machine) { m.osRelease = "ID=endeavouros\nID_LIKE=arch\n" }, code: 0, want: []string{"the AppImage"}},
		{name: "macOS", change: func(m *machine) { m.os = "Darwin" }, code: 1, want: []string{"On a Mac", ".dmg", "/index.html"}},
		{name: "unknown option", args: []string{"--nope"}, code: 2, want: []string{"unknown option --nope"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := goodMachine()
			if tt.change != nil {
				tt.change(&m)
			}
			if m.kvm == "denied" && os.Geteuid() == 0 {
				t.Skip("root opens /dev/kvm whatever its mode")
			}
			args := tt.args
			if args == nil {
				args = []string{"--check"}
			}
			for _, sh := range shells(t) {
				env, _, _ := setup(t, m)
				out, code := run(t, sh, env, args...)
				if code != tt.code {
					t.Errorf("%s: exit %d, want %d:\n%s", sh, code, tt.code, out)
				}
				for _, w := range tt.want {
					if !strings.Contains(out, w) {
						t.Errorf("%s: output lacks %q:\n%s", sh, w, out)
					}
				}
				for _, a := range tt.absent {
					if strings.Contains(strings.ToLower(out), strings.ToLower(a)) {
						t.Errorf("%s: output has %q:\n%s", sh, a, out)
					}
				}
			}
		})
	}
}

// releasesJSON is releases.json as scripts/r2-publish.sh writes it: newest
// first, one field a line.
const releasesJSON = `[
  {
    "tag_name": "v0.17.0-nightly.20261026.1",
    "name": "v0.17.0-nightly.20261026.1",
    "draft": false,
    "prerelease": true,
    "assets": []
  },
  {
    "tag_name": "v0.16.0",
    "name": "v0.16.0",
    "draft": false,
    "prerelease": false,
    "assets": []
  }
]
`

// server serves releases.json, and each release's SHA256SUMS and assets,
// with the sums of sums (by default, the real ones).
func server(t *testing.T, sums map[string]string) *httptest.Server {
	files := map[string]string{}
	for _, tag := range []string{"v0.16.0", "v0.17.0-nightly.20261026.1"} {
		v := strings.TrimPrefix(tag, "v")
		var list strings.Builder
		for _, name := range []string{"AgentBox-" + v + "-amd64.deb", "AgentBox-" + v + "-x64.pacman", "AgentBox-" + v + "-x86_64.AppImage"} {
			body := "package " + name
			files["/releases/"+tag+"/"+name] = body
			sum := sha256.Sum256([]byte(body))
			s := hex.EncodeToString(sum[:])
			if o, ok := sums[name]; ok {
				s = o
			}
			fmt.Fprintf(&list, "%s  %s\n", s, name)
		}
		files["/releases/"+tag+"/SHA256SUMS"] = list.String()
	}
	files["/releases.json"] = releasesJSON
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func withURL(env []string, url string) []string {
	out := []string{}
	for _, e := range env {
		if !strings.HasPrefix(e, "DOWNLOADS_URL=") {
			out = append(out, e)
		}
	}
	return append(out, "DOWNLOADS_URL="+url)
}

func TestInstall(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		if _, err := exec.LookPath("wget"); err != nil {
			t.Skip("neither curl nor wget")
		}
	}
	srv := server(t, nil)
	sh := shells(t)[0]

	t.Run("deb", func(t *testing.T) {
		env, _, calls := setup(t, goodMachine())
		out, code := run(t, sh, withURL(env, srv.URL))
		if code != 0 {
			t.Fatalf("exit %d:\n%s", code, out)
		}
		got, _ := os.ReadFile(calls)
		if !strings.Contains(string(got), "sudo apt-get install -y ") || !strings.Contains(string(got), "/AgentBox-0.16.0-amd64.deb") {
			t.Errorf("calls:\n%s\noutput:\n%s", got, out)
		}
		if !strings.Contains(out, "AgentBox 0.16.0 is installed") || !strings.Contains(out, "needs your password for sudo") {
			t.Errorf("output:\n%s", out)
		}
	})

	t.Run("pacman, nightly", func(t *testing.T) {
		m := goodMachine()
		m.osRelease, m.pacman = "ID=arch\n", true
		env, _, calls := setup(t, m)
		out, code := run(t, sh, append(withURL(env, srv.URL), "AGENTBOX_CHANNEL=nightly"))
		if code != 0 {
			t.Fatalf("exit %d:\n%s", code, out)
		}
		got, _ := os.ReadFile(calls)
		if !strings.Contains(string(got), "sudo pacman -U --noconfirm ") || !strings.Contains(string(got), "/AgentBox-0.17.0-nightly.20261026.1-x64.pacman") {
			t.Errorf("calls:\n%s\noutput:\n%s", got, out)
		}
	})

	t.Run("AppImage", func(t *testing.T) {
		m := goodMachine()
		m.osRelease = "ID=fedora\n"
		env, home, calls := setup(t, m)
		out, code := run(t, sh, withURL(env, srv.URL))
		if code != 0 {
			t.Fatalf("exit %d:\n%s", code, out)
		}
		app := filepath.Join(home, ".local/bin/AgentBox.AppImage")
		fi, err := os.Stat(app)
		if err != nil || fi.Mode()&0o111 == 0 {
			t.Fatalf("no executable %s (%v):\n%s", app, err, out)
		}
		if b, _ := os.ReadFile(app); string(b) != "package AgentBox-0.16.0-x86_64.AppImage" {
			t.Errorf("installed %q", b)
		}
		entry, err := os.ReadFile(filepath.Join(home, ".local/share/applications/agentbox.desktop"))
		if err != nil || !strings.Contains(string(entry), "Exec="+app) {
			t.Errorf("desktop entry %q, %v", entry, err)
		}
		if _, err := os.Stat(calls); err == nil {
			t.Error("the AppImage install ran sudo or a package manager")
		}
	})

	t.Run("checksum mismatch", func(t *testing.T) {
		bad := server(t, map[string]string{"AgentBox-0.16.0-amd64.deb": strings.Repeat("0", 64)})
		env, _, calls := setup(t, goodMachine())
		out, code := run(t, sh, withURL(env, bad.URL))
		if code == 0 || !strings.Contains(out, "doesn't match the release's SHA256SUMS") || !strings.Contains(out, "nothing was installed") {
			t.Errorf("exit %d:\n%s", code, out)
		}
		if _, err := os.Stat(calls); err == nil {
			got, _ := os.ReadFile(calls)
			t.Errorf("installed anyway: %s", got)
		}
	})

	t.Run("checks fail, nothing downloaded", func(t *testing.T) {
		m := goodMachine()
		m.kvm = ""
		env, _, calls := setup(t, m)
		var hits int
		counting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
		defer counting.Close()
		out, code := run(t, sh, withURL(env, counting.URL))
		if code != 1 || hits != 0 {
			t.Errorf("exit %d, %d requests:\n%s", code, hits, out)
		}
		if _, err := os.Stat(calls); err == nil {
			t.Error("installed anyway")
		}
	})
}

// A download cut off half-way, piped into sh, runs none of it: everything is
// in main, called on the last line.
func TestCutOffRunsNothing(t *testing.T) {
	env, _, calls := setup(t, goodMachine())
	cut := Script[:bytes.Index(Script, []byte("# --- Install"))]
	for _, shell := range shells(t) {
		cmd := exec.Command(shell, "-s", "--")
		cmd.Stdin = bytes.NewReader(cut)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Errorf("%s ran a cut-off script to the end: %s", shell, out)
		}
		if strings.Contains(string(out), "This machine can run AgentBox") {
			t.Errorf("%s ran part of a cut-off script: %s", shell, out)
		}
	}
	if _, err := os.Stat(calls); err == nil {
		t.Errorf("a cut-off script called something")
	}
}
