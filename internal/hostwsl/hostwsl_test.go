package hostwsl

import (
	"errors"
	"testing"
)

func TestLinuxPath(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		ok       bool
	}{
		{`C:\Users\Ana\src\app`, "/mnt/c/Users/Ana/src/app", true},
		{`d:/work/`, "/mnt/d/work", true},
		{`C:`, "/mnt/c", true},
		{`C:\`, "/mnt/c", true},
		{`\\wsl.localhost\AgentBox\home\ana\app`, "/home/ana/app", true},
		{`\\wsl$\agentbox\home\ana`, "/home/ana", true},
		{`\\wsl.localhost\AgentBox`, "/", true},
		{`\\wsl.localhost\Ubuntu\home\ana`, "", false},
		{`\\server\share\x`, "", false},
		{"/home/ana", "/home/ana", true},
		{`relative\dir`, "", false},
		// A junction's target, as Windows keeps it.
		{`\??\C:\Users\Ana\.agents\skills\x`, "/mnt/c/Users/Ana/.agents/skills/x", true},
		{`\\?\D:\x`, "/mnt/d/x", true},
	} {
		got, ok := LinuxPath(tc.in, "AgentBox")
		if got != tc.want || ok != tc.ok {
			t.Errorf("LinuxPath(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestWindowsPath(t *testing.T) {
	for in, want := range map[string]string{
		"/home/ana/app": `\\wsl.localhost\AgentBox\home\ana\app`,
		"/mnt/c/Users":  `C:\Users`,
		"/mnt/d":        `D:\`,
		"/":             `\\wsl.localhost\AgentBox\`,
	} {
		if got := WindowsPath(in, "AgentBox"); got != want {
			t.Errorf("WindowsPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTranslateArgs(t *testing.T) {
	got := translateArgs([]string{"add", `C:\src\app`, "--name", "C:", "a:b", `\\wsl.localhost\AgentBox\home\ana`, `\\wsl.localhost\Other\x`})
	want := []string{"add", "/mnt/c/src/app", "--name", "/mnt/c", "a:b", "/home/ana", `\\wsl.localhost\Other\x`}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("arg %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestLinuxUser(t *testing.T) {
	for in, want := range map[string]string{
		"Ana":           "ana",
		"Ana Souza":     "ana-souza",
		"José":          "jos",
		"1stuser":       "stuser",
		"root":          "root-agentbox",
		"":              "agentbox",
		"Administrator": "administrator",
	} {
		if got := LinuxUser(in); got != want {
			t.Errorf("LinuxUser(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseList(t *testing.T) {
	out := "  NAME        STATE           VERSION\r\n* Ubuntu      Running         2\r\n  AgentBox    Stopped         2\r\n  Old         Stopped         1\r\n"
	if st := parseList(out, "agentbox"); st != (State{Exists: true, State: "Stopped", Version: 2}) {
		t.Errorf("AgentBox: %+v", st)
	}
	if st := parseList(out, "Old"); st.Version != 1 {
		t.Errorf("Old: %+v", st)
	}
	if st := parseList(out, "Missing"); st.Exists {
		t.Errorf("Missing: %+v", st)
	}
}

func TestDecode(t *testing.T) {
	utf16 := []byte{0xff, 0xfe, 'W', 0, 'S', 0, 'L', 0}
	if got := decode(utf16); got != "WSL" {
		t.Errorf("decode(UTF-16 with BOM) = %q", got)
	}
	if got := decode([]byte{'W', 0, 'S', 0}); got != "WS" {
		t.Errorf("decode(UTF-16) = %q", got)
	}
	if got := decode([]byte("WSL version: 2")); got != "WSL version: 2" {
		t.Errorf("decode(UTF-8) = %q", got)
	}
}

func TestNeedsDaemon(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{nil, false},
		{[]string{"list"}, true},
		{[]string{"list", "--help"}, false},
		{[]string{"host", "setup"}, false},
		{[]string{"daemon"}, false},
		{[]string{"daemon", "stop"}, false},
		{[]string{"daemon", "start"}, true},
		{[]string{"--version"}, false},
	} {
		if got := needsDaemon(tc.args); got != tc.want {
			t.Errorf("needsDaemon(%q) = %v", tc.args, got)
		}
	}
}

func TestFindSum(t *testing.T) {
	sums := "aaa  other.tar.gz\nbbb *ubuntu.tar.gz\n"
	if got := findSum(sums, "ubuntu.tar.gz"); got != "bbb" {
		t.Errorf("findSum = %q", got)
	}
	if got := findSum(sums, "missing"); got != "" {
		t.Errorf("findSum(missing) = %q", got)
	}
}

func TestNoVirtualization(t *testing.T) {
	real := "The operation could not be started because a required feature is not installed.\r\nError code: Wsl/Service/RegisterDistro/CreateVm/HCS/HCS_E_SERVICE_NOT_AVAILABLE\r\n"
	for said, want := range map[string]bool{
		real: true,
		"WSL2 is not supported with your current machine configuration.\nPlease enable the \"Virtual Machine Platform\" optional component and ensure virtualization is enabled in the BIOS.": true,
		"Error: 0x80370102 The virtual machine could not be started because a required feature is not installed.":                                                                             true,
		"A operação não pôde ser iniciada.\nCódigo de erro: Wsl/Service/RegisterDistro/CreateVm/HCS/HCS_E_SERVICE_NOT_AVAILABLE":                                                              true,
		"The system cannot find the file specified.": false,
	} {
		if got := noVirtualization(said); got != want {
			t.Errorf("noVirtualization(%q) = %v", said, got)
		}
	}
	err := explainImport(&wslError{args: []string{"--import"}, said: real})
	if !errors.Is(err, ErrNoVirtualization) {
		t.Errorf("explainImport = %v, want ErrNoVirtualization", err)
	}
	other := &wslError{said: "disk full"}
	if explainImport(other) != error(other) {
		t.Error("explainImport changed an unrelated error")
	}
}
