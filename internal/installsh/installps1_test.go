package installsh

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// pc is a fake Windows computer for install.ps1: what it would ask Windows,
// which it reads from AGENTBOX_INSTALL_FAKE instead.
type pc struct {
	Arch           string  `json:"arch"`
	Build          int     `json:"build"`
	Memory         float64 `json:"memory"`
	Free           float64 `json:"free"`
	Data           string  `json:"data"`
	Virtualization bool    `json:"virtualization"`
	WSL            bool    `json:"wsl"`
	StartMenu      string  `json:"startMenu"`
	Calls          string  `json:"calls"`
}

func goodPC() pc {
	return pc{Arch: "AMD64", Build: 26100, Memory: 16 << 30, Free: 200e9, Data: `C:\Users\ana\AppData\Local`, Virtualization: true, WSL: true}
}

// powershells are the PowerShells here: pwsh (7, which GitHub's Linux runners
// have too) and, on Windows, the 5.1 that `irm | iex` runs in.
func powershells(t *testing.T) []string {
	var out []string
	for _, sh := range []string{"pwsh", "powershell.exe"} {
		if p, err := exec.LookPath(sh); err == nil {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		t.Skip("no PowerShell")
	}
	return out
}

// runPS runs install.ps1 on m, piped into iex with options as environment
// variables, or with args as a file's parameters. It returns what it said,
// its exit status and what the stubbed installer was asked to do.
func runPS(t *testing.T, shell string, m pc, env []string, args ...string) (string, int, string) {
	t.Helper()
	dir := t.TempDir()
	m.StartMenu = filepath.Join(dir, "Start Menu")
	if err := os.MkdirAll(m.StartMenu, 0o755); err != nil {
		t.Fatal(err)
	}
	// The shortcuts the installers leave.
	for _, name := range []string{"AgentBox.lnk", "AgentBox Native (experimental).lnk"} {
		write(t, filepath.Join(m.StartMenu, name), "", 0o644)
	}
	m.Calls = filepath.Join(dir, "calls")
	fake := filepath.Join(dir, "machine.json")
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	write(t, fake, string(b), 0o644)
	script := filepath.Join(dir, "install.ps1")
	write(t, script, string(PowerShell), 0o644)
	var cmd *exec.Cmd
	if args == nil {
		// As `irm … | iex`: the script's text, evaluated, its exit status
		// left in $LASTEXITCODE.
		cmd = exec.Command(shell, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command",
			"Get-Content -Raw '"+script+"' | Invoke-Expression; exit $LASTEXITCODE")
	} else {
		cmd = exec.Command(shell, append([]string{"-NoLogo", "-NoProfile", "-NonInteractive", "-File", script}, args...)...)
	}
	cmd.Env = append(append(os.Environ(), "AGENTBOX_INSTALL_FAKE="+fake, "DOWNLOADS_URL=http://127.0.0.1:1", "NO_COLOR=1"), env...)
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	calls, _ := os.ReadFile(m.Calls)
	// PowerShell wraps what it writes at the console's width.
	return strings.Join(strings.Fields(string(out)), " "), code, string(calls)
}

func TestPowerShellChecks(t *testing.T) {
	tests := []struct {
		name   string
		change func(*pc)
		code   int
		want   []string
		absent []string
	}{
		{name: "ok", code: 0, want: []string{"This computer can run AgentBox", "no administrator"}, absent: []string{"Note:", "can't be installed"}},
		{name: "arm64", change: func(m *pc) { m.Arch = "ARM64" }, code: 1, want: []string{"ARM (arm64)", "only on x64"}},
		{name: "32-bit", change: func(m *pc) { m.Arch = "x86" }, code: 1, want: []string{"processor is x86"}},
		{name: "old Windows", change: func(m *pc) { m.Build = 18363 }, code: 1, want: []string{"build 18363", "version 2004"}},
		{name: "low memory", change: func(m *pc) { m.Memory = 4 << 30 }, code: 1, want: []string{"4 GB of memory", "at least 8 GB"}},
		{name: "8 GB that reports less", change: func(m *pc) { m.Memory = 7.8 * (1 << 30) }, code: 0},
		{name: "low disk", change: func(m *pc) { m.Free = 10e9 }, code: 1, want: []string{"10 GB free", "at least 30 GB", `C:\Users\ana\AppData\Local`}},
		{name: "every problem at once", change: func(m *pc) { m.Build, m.Memory, m.Free = 17763, 2<<30, 1e9 }, code: 1, want: []string{"build 17763", "8 GB", "30 GB"}},
		{name: "no WSL yet", change: func(m *pc) { m.WSL = false }, code: 0, want: []string{"WSL isn't installed yet", "Setup installs it", "can run AgentBox"}},
		{name: "virtualization off", change: func(m *pc) { m.Virtualization = false }, code: 0, want: []string{"virtualization is off", "VT-x or AMD SVM", "can run AgentBox"}},
	}
	for _, shell := range powershells(t) {
		for _, tc := range tests {
			t.Run(filepath.Base(shell)+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				m := goodPC()
				if tc.change != nil {
					tc.change(&m)
				}
				out, code, calls := runPS(t, shell, m, []string{"AGENTBOX_INSTALL_CHECK=1"})
				if code != tc.code {
					t.Errorf("exit %d, want %d:\n%s", code, tc.code, out)
				}
				for _, w := range tc.want {
					if !strings.Contains(out, w) {
						t.Errorf("no %q in:\n%s", w, out)
					}
				}
				for _, a := range tc.absent {
					if strings.Contains(out, a) {
						t.Errorf("%q in:\n%s", a, out)
					}
				}
				if calls != "" {
					t.Errorf("--check installed: %s", calls)
				}
			})
		}
	}
}

func TestPowerShellInstall(t *testing.T) {
	sh := powershells(t)[0]
	srv := server(t, nil)
	url := "DOWNLOADS_URL=" + srv.URL

	t.Run("the app, piped into iex", func(t *testing.T) {
		out, code, calls := runPS(t, sh, goodPC(), []string{url})
		if code != 0 {
			t.Fatalf("exit %d:\n%s", code, out)
		}
		if calls != "AgentBox-0.16.0-x64-setup.exe /S\nopen AgentBox.lnk\n" {
			t.Errorf("ran %q\n%s", calls, out)
		}
		for _, want := range []string{"Downloading AgentBox 0.16.0", "Checked against the release's SHA256SUMS", "is installed, and opening"} {
			if !strings.Contains(out, want) {
				t.Errorf("no %q in:\n%s", want, out)
			}
		}
	})

	t.Run("the nightly's native app, as a file's parameters", func(t *testing.T) {
		out, code, calls := runPS(t, sh, goodPC(), []string{url}, "-Native", "-Channel", "nightly", "-NoOpen")
		if code != 0 {
			t.Fatalf("exit %d:\n%s", code, out)
		}
		if calls != "AgentBox-Native-experimental-0.17.0-nightly.20261026.1-windows-x64-setup.exe /S\n" {
			t.Errorf("ran %q\n%s", calls, out)
		}
		if !strings.Contains(out, "Open AgentBox Native (experimental) from the Start menu") {
			t.Errorf("%s", out)
		}
	})

	t.Run("a release with no native app", func(t *testing.T) {
		out, code, calls := runPS(t, sh, goodPC(), []string{url, "AGENTBOX_INSTALL_NATIVE=1"})
		if code != 1 || calls != "" || !strings.Contains(out, "has no native app for Windows") {
			t.Errorf("exit %d, ran %q:\n%s", code, calls, out)
		}
	})

	t.Run("a download that doesn't match", func(t *testing.T) {
		bad := server(t, map[string]string{"AgentBox-0.16.0-x64-setup.exe": strings.Repeat("0", 64)})
		out, code, calls := runPS(t, sh, goodPC(), []string{"DOWNLOADS_URL=" + bad.URL})
		if code != 1 || calls != "" || !strings.Contains(out, "doesn't match the release's SHA256SUMS") {
			t.Errorf("exit %d, ran %q:\n%s", code, calls, out)
		}
	})

	t.Run("checks fail, nothing downloaded", func(t *testing.T) {
		m := goodPC()
		m.Build = 17763
		out, code, calls := runPS(t, sh, m, []string{url})
		if code != 1 || calls != "" || strings.Contains(out, "Downloading") {
			t.Errorf("exit %d, ran %q:\n%s", code, calls, out)
		}
	})
}

// A download cut off half-way, piped into iex, runs none of it: everything
// is in one function, called at the end.
func TestPowerShellCutOffRunsNothing(t *testing.T) {
	cut := PowerShell[:bytes.Index(PowerShell, []byte("# --- Install"))]
	for _, shell := range powershells(t) {
		dir := t.TempDir()
		script := filepath.Join(dir, "cut.ps1")
		write(t, script, string(cut), 0o644)
		cmd := exec.Command(shell, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", "Get-Content -Raw '"+script+"' | Invoke-Expression")
		cmd.Env = append(os.Environ(), "DOWNLOADS_URL=http://127.0.0.1:1")
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Errorf("%s ran a cut-off script to the end: %s", shell, out)
		}
		if strings.Contains(string(out), "can run AgentBox") || strings.Contains(string(out), "can't be installed") {
			t.Errorf("%s ran part of a cut-off script: %s", shell, out)
		}
	}
}
