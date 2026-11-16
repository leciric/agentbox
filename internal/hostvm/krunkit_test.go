package hostvm

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// macInfo is what `limactl info` says on a Mac with Apple Silicon, cut down to
// what krunkitCheck reads, with Lima's krunkit driver or without it.
func macInfo(driver bool) string {
	ex := `"qemu":{"location":"internal"},"vz":{"location":"internal"}`
	if driver {
		ex += `,"krunkit":{"location":"/opt/homebrew/Cellar/lima/2.1.3/libexec/lima/lima-driver-krunkit"}`
	}
	return `{"version":"2.1.3","hostOS":"darwin","hostArch":"aarch64","vmTypesEx":{` + ex + `}}`
}

// withKrunkit installs a fake krunkit where findKrunkit looks, or none, with
// nothing else on PATH for it to find.
func withKrunkit(t *testing.T, dir string, installed bool) string {
	t.Helper()
	brew := filepath.Join(dir, "brew-bin")
	if err := os.MkdirAll(brew, 0o755); err != nil {
		t.Fatal(err)
	}
	krunkit := filepath.Join(brew, "krunkit")
	if installed {
		if err := os.WriteFile(krunkit, []byte("#!/bin/sh\necho krunkit 1.3.2\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	old := krunkitDirs
	krunkitDirs = []string{brew}
	t.Cleanup(func() { krunkitDirs = old })
	// The system's, for the fake limactl's commands, which has no krunkit.
	t.Setenv("PATH", "/usr/bin:/bin")
	return krunkit
}

func TestKrunkitCheck(t *testing.T) {
	for _, tc := range []struct {
		name      string
		info      string // "" for a limactl info that fails
		installed bool
		want      *KrunkitCheck
	}{
		{"available", macInfo(true), true, &KrunkitCheck{Available: true}},
		{"no krunkit", macInfo(true), false, &KrunkitCheck{Missing: "krunkit"}},
		{"no driver", macInfo(false), true, &KrunkitCheck{Missing: "driver"}},
		{"neither", macInfo(false), false, &KrunkitCheck{Missing: "krunkit"}},
		{"intel mac", `{"hostOS":"darwin","hostArch":"x86_64","vmTypesEx":{"vz":{}}}`, true, nil},
		{"linux", `{"hostOS":"linux","hostArch":"x86_64","vmTypesEx":{"qemu":{}}}`, true, nil},
		{"lima won't say", "", true, nil},
		{"not json", "limactl 1.0", true, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vm, dir := newFake(t)
			if tc.info != "" {
				_ = os.WriteFile(filepath.Join(dir, "info"), []byte(tc.info), 0o644)
			}
			withKrunkit(t, dir, tc.installed)
			got := vm.krunkitCheck(context.Background())
			switch {
			case got == nil && tc.want == nil:
			case got == nil || tc.want == nil || *got != *tc.want:
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestKrunkitOnPath(t *testing.T) {
	_, dir := newFake(t)
	krunkit := withKrunkit(t, dir, true)
	krunkitOnPath()
	if p, err := exec.LookPath("krunkit"); err != nil || p != krunkit {
		t.Fatalf("krunkit on PATH %q: got %q, %v", os.Getenv("PATH"), p, err)
	}
	// Once is enough.
	path := os.Getenv("PATH")
	krunkitOnPath()
	if os.Getenv("PATH") != path {
		t.Errorf("PATH grew again: %q", os.Getenv("PATH"))
	}

	_, dir = newFake(t)
	withKrunkit(t, dir, false)
	path = os.Getenv("PATH")
	krunkitOnPath()
	if os.Getenv("PATH") != path {
		t.Errorf("no krunkit, yet PATH changed to %q", os.Getenv("PATH"))
	}
}

func created(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "created.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func creates(t *testing.T, dir string) int {
	n := 0
	for _, c := range calls(t, dir) {
		if strings.HasPrefix(c, "create ") {
			n++
		}
	}
	return n
}

func TestCreateVMWithKrunkit(t *testing.T) {
	vm, dir := newFake(t)
	_ = os.WriteFile(filepath.Join(dir, "info"), []byte(macInfo(true)), 0o644)
	withKrunkit(t, dir, true)
	if err := vm.createVM(context.Background(), DefaultSize()); err != nil {
		t.Fatal(err)
	}
	if def := created(t, dir); !strings.Contains(def, "\nvmType: krunkit\n") || !strings.Contains(def, "agentbox-reclaim.timer") {
		t.Errorf("not made with krunkit and its reclaim timer:\n%s", def)
	}
	if log := vm.Log.(*bytes.Buffer).String(); !strings.Contains(log, "Making the VM with krunkit") {
		t.Errorf("init doesn't say it's using krunkit: %q", log)
	}
}

func TestCreateVMFallsBackToVZ(t *testing.T) {
	vm, dir := newFake(t)
	_ = os.WriteFile(filepath.Join(dir, "info"), []byte(macInfo(true)), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "refuse-krunkit"), nil, 0o644)
	withKrunkit(t, dir, true)
	if err := vm.createVM(context.Background(), DefaultSize()); err != nil {
		t.Fatal(err)
	}
	if n := creates(t, dir); n != 2 {
		t.Errorf("%d creates, want krunkit's then vz's: %q", n, calls(t, dir))
	}
	if def := created(t, dir); strings.Contains(def, "vmType") || strings.Contains(def, "agentbox-reclaim") {
		t.Errorf("the second create isn't Lima's default:\n%s", def)
	}
	if log := vm.Log.(*bytes.Buffer).String(); !strings.Contains(log, "wouldn't make the VM with krunkit") {
		t.Errorf("init doesn't say why it fell back: %q", log)
	}
}

func TestCreateVMWithoutKrunkit(t *testing.T) {
	for _, tc := range []struct {
		name, info string
		installed  bool
		note       string
	}{
		{"not installed", macInfo(true), false, krunkitInstall},
		{"no driver", macInfo(false), true, "lima-driver-krunkit"},
		{"intel", `{"hostOS":"darwin","hostArch":"x86_64","vmTypesEx":{"vz":{}}}`, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vm, dir := newFake(t)
			_ = os.WriteFile(filepath.Join(dir, "info"), []byte(tc.info), 0o644)
			withKrunkit(t, dir, tc.installed)
			if err := vm.createVM(context.Background(), DefaultSize()); err != nil {
				t.Fatal(err)
			}
			if def := created(t, dir); strings.Contains(def, "vmType") {
				t.Errorf("made with a VM type rather than Lima's default:\n%s", def)
			}
			log := vm.Log.(*bytes.Buffer).String()
			if tc.note == "" && strings.Contains(log, "krunkit") {
				t.Errorf("said %q where krunkit doesn't run", log)
			}
			if !strings.Contains(log, tc.note) {
				t.Errorf("init's note %q doesn't say %q", log, tc.note)
			}
		})
	}
}

// AGENTBOX_VM_TYPE is the VM type, whatever is installed.
func TestCreateVMTypeSet(t *testing.T) {
	vm, dir := newFake(t)
	_ = os.WriteFile(filepath.Join(dir, "info"), []byte(macInfo(true)), 0o644)
	withKrunkit(t, dir, true)
	vm.VMType = "vz"
	if err := vm.createVM(context.Background(), DefaultSize()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(created(t, dir), "\nvmType: vz\n") {
		t.Errorf("not made with vz:\n%s", created(t, dir))
	}
	if slices.ContainsFunc(calls(t, dir), func(c string) bool { return c == "info" }) {
		t.Error("asked Lima about krunkit with the VM type set")
	}
}

func TestCheckReporting(t *testing.T) {
	for _, tc := range []struct {
		name, vmType, features string
		warns                  bool
	}{
		{"reports", krunkitVMType, "1\n", false},
		{"libkrun too old", krunkitVMType, "0\n", true},
		{"no balloon", krunkitVMType, "", true},
		{"vz", "vz", "0\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vm, dir := newFake(t)
			_ = os.WriteFile(filepath.Join(dir, "reporting"), []byte(tc.features), 0o644)
			vm.checkReporting(context.Background(), State{Exists: true, VMType: tc.vmType})
			log := vm.Log.(*bytes.Buffer).String()
			if warns := strings.Contains(log, "libkrun 1.19"); warns != tc.warns {
				t.Errorf("warned %v, want %v: %q", warns, tc.warns, log)
			}
		})
	}
}

// The script finds the balloon among the guest's virtio devices by its device
// ID, and prints its sixth feature bit.
func TestReportingScript(t *testing.T) {
	root := t.TempDir()
	devices := filepath.Join(root, "sys/bus/virtio/devices")
	for name, dev := range map[string][2]string{
		"virtio0": {"0x0001", "1111111111"}, // network
		"virtio3": {"0x0005", "0010010000"}, // the balloon, with bit 5
	} {
		d := filepath.Join(devices, name)
		_ = os.MkdirAll(d, 0o755)
		_ = os.WriteFile(filepath.Join(d, "device"), []byte(dev[0]+"\n"), 0o644)
		_ = os.WriteFile(filepath.Join(d, "features"), []byte(dev[1]+"\n"), 0o644)
	}
	script := strings.ReplaceAll(reportingScript, "/sys/", root+"/sys/")
	out, err := exec.Command("sh", "-c", script).Output()
	if err != nil || string(out) != "1\n" {
		t.Fatalf("got %q, %v", out, err)
	}
	_ = os.WriteFile(filepath.Join(devices, "virtio3", "features"), []byte("0010000000\n"), 0o644)
	if out, _ := exec.Command("sh", "-c", script).Output(); string(out) != "0\n" {
		t.Fatalf("without bit 5: got %q", out)
	}
}

func TestDefinitionKrunkit(t *testing.T) {
	vm, _ := newFake(t)
	vm.VMType = krunkitVMType
	def, err := vm.Definition(DefaultSize())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(def, "\nvmType: krunkit\n") {
		t.Errorf("no vmType: krunkit in:\n%s", def)
	}
	scripts := provisionScripts(def)
	if len(scripts) != 2 {
		t.Fatalf("%d provision scripts, want the usual one and the reclaim one:\n%s", len(scripts), def)
	}
	if !strings.Contains(def, "\nportForwards:\n  - guestSocket:") {
		t.Errorf("the port forwards don't follow the provision scripts:\n%s", def)
	}
	script := scripts[1]
	if err := exec.Command("bash", "-n", "-c", script).Run(); err != nil {
		t.Errorf("the reclaim provision script doesn't parse: %v\n%s", err, script)
	}
	// What it installs, cut out of its heredoc.
	_, reclaim, _ := strings.Cut(script, "cat >/usr/local/sbin/agentbox-reclaim <<'EOF'\n")
	reclaim, _, found := strings.Cut(reclaim, "\nEOF\n")
	if !found || !strings.HasPrefix(reclaim, "#!/bin/sh\n") {
		t.Fatalf("no reclaim script in:\n%s", script)
	}
	if err := exec.Command("sh", "-n", "-c", reclaim).Run(); err != nil {
		t.Errorf("the reclaim script doesn't parse: %v\n%s", err, reclaim)
	}
	for _, want := range []string{"echo 3 >/proc/sys/vm/drop_caches", "echo 1 >/proc/sys/vm/compact_memory", "systemctl enable --now agentbox-reclaim.timer"} {
		if !strings.Contains(script, want) {
			t.Errorf("the provision script has no %q", want)
		}
	}

	for _, other := range []string{"", "vz", "qemu"} {
		vm.VMType = other
		def, _ := vm.Definition(DefaultSize())
		if strings.Contains(def, "agentbox-reclaim") {
			t.Errorf("vmType %q drops its caches, which gives the Mac nothing back", other)
		}
	}
}

// provisionScripts cuts the scripts out of a definition's provision list, the
// way YAML reads a literal block: its lines indented six spaces, up to the
// first line that isn't.
func provisionScripts(def string) []string {
	var scripts []string
	lines := strings.Split(def, "\n")
	for i, line := range lines {
		if line != "    script: |" || i == 0 || lines[i-1] != "  - mode: system" {
			continue
		}
		var b strings.Builder
		for _, l := range lines[i+1:] {
			body, ok := strings.CutPrefix(l, "      ")
			if !ok && l != "" {
				break
			}
			b.WriteString(body + "\n")
		}
		scripts = append(scripts, strings.TrimRight(b.String(), "\n")+"\n")
	}
	return scripts
}

// fakeBrew is Homebrew's brew, as far as InstallKrunkit uses it: it notes
// what it's asked, installs krunkit beside itself, and gives Lima the krunkit
// driver when it upgrades it.
const fakeBrew = `#!/bin/sh
printf '%s\n' "$*" >>"$FAKE_DIR/brew-calls"
[ ! -e "$FAKE_DIR/brew-fails" ] || exit 1
case "$*" in
  "trust slp/krun") [ ! -e "$FAKE_DIR/no-trust" ] || { echo 'Error: Unknown command: brew trust' >&2; exit 1; } ;;
  "install krunkit") printf '#!/bin/sh\necho krunkit 1.3.2\n' >"$(dirname "$0")/krunkit"; chmod +x "$(dirname "$0")/krunkit" ;;
  "upgrade lima") cat "$FAKE_DIR/info-upgraded" >"$FAKE_DIR/info" ;;
esac
`

// withBrew makes a Homebrew prefix in dir with brew in its bin, where
// findBrew and findKrunkit look, and, with limaBrew, the VM's limactl one of
// its formulae.
func withBrew(t *testing.T, vm *VM, dir string, limaBrew bool) {
	t.Helper()
	prefix := filepath.Join(dir, "homebrew")
	bin := filepath.Join(prefix, "bin")
	_ = os.MkdirAll(bin, 0o755)
	if err := os.WriteFile(filepath.Join(bin, "brew"), []byte(fakeBrew), 0o755); err != nil {
		t.Fatal(err)
	}
	krunkitDirs = []string{bin}
	if limaBrew {
		cellar := filepath.Join(prefix, "Cellar", "lima", "1.2.0", "bin")
		_ = os.MkdirAll(cellar, 0o755)
		if err := os.Rename(vm.Limactl, filepath.Join(cellar, "limactl")); err != nil {
			t.Fatal(err)
		}
		vm.Limactl = filepath.Join(bin, "limactl")
		if err := os.Symlink("../Cellar/lima/1.2.0/bin/limactl", vm.Limactl); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(filepath.Join(dir, "info-upgraded"), []byte(macInfo(true)), 0o644)
}

func brewCalls(dir string) string {
	b, _ := os.ReadFile(filepath.Join(dir, "brew-calls"))
	return string(b)
}

func TestKrunkitCheckBrew(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		driver, krunkit, brew bool
		limaBrew              bool
		want                  KrunkitCheck
	}{
		{"krunkit, with Homebrew", true, false, true, false, KrunkitCheck{Missing: "krunkit", Brew: true}},
		{"krunkit, without Homebrew", true, false, false, false, KrunkitCheck{Missing: "krunkit"}},
		{"driver, Homebrew's Lima", false, true, true, true, KrunkitCheck{Missing: "driver", Brew: true}},
		{"driver, another Lima", false, true, true, false, KrunkitCheck{Missing: "driver"}},
		{"both, Homebrew's Lima", false, false, true, true, KrunkitCheck{Missing: "krunkit", Brew: true}},
		{"both, another Lima", false, false, true, false, KrunkitCheck{Missing: "krunkit"}},
		{"installed", true, true, true, true, KrunkitCheck{Available: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vm, dir := newFake(t)
			_ = os.WriteFile(filepath.Join(dir, "info"), []byte(macInfo(tc.driver)), 0o644)
			withKrunkit(t, dir, tc.krunkit)
			if tc.brew {
				withBrew(t, vm, dir, tc.limaBrew)
				krunkitDirs = append(krunkitDirs, filepath.Join(dir, "brew-bin"))
			}
			if got := vm.krunkitCheck(context.Background()); got == nil || *got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestInstallKrunkit(t *testing.T) {
	for _, tc := range []struct {
		name      string
		driver    bool
		limaBrew  bool
		noTrust   bool
		wantCalls string
	}{
		{"krunkit", true, false, false, "tap slp/krun\ntrust slp/krun\ninstall krunkit\n"},
		{"a Homebrew without brew trust", true, false, true, "tap slp/krun\ntrust slp/krun\ninstall krunkit\n"},
		{"krunkit and the driver", false, true, false, "tap slp/krun\ntrust slp/krun\ninstall krunkit\nupgrade lima\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vm, dir := newFake(t)
			_ = os.WriteFile(filepath.Join(dir, "info"), []byte(macInfo(tc.driver)), 0o644)
			withKrunkit(t, dir, false)
			withBrew(t, vm, dir, tc.limaBrew)
			if tc.noTrust {
				_ = os.WriteFile(filepath.Join(dir, "no-trust"), nil, 0o644)
			}
			if err := vm.InstallKrunkit(context.Background()); err != nil {
				t.Fatalf("%v\n%s", err, vm.Log)
			}
			if got := brewCalls(dir); got != tc.wantCalls {
				t.Errorf("brew ran:\n%s\nwant:\n%s", got, tc.wantCalls)
			}
			if c := vm.krunkitCheck(context.Background()); c == nil || !c.Available {
				t.Errorf("not available afterwards: %+v", c)
			}
			log := vm.Log.(*bytes.Buffer).String()
			if !strings.Contains(log, "$ brew install krunkit") || !strings.Contains(log, "a VM made now is made with it") {
				t.Errorf("the log doesn't show the install: %q", log)
			}
		})
	}
}

// Only the driver missing, from Homebrew's Lima: brew upgrades Lima alone.
func TestInstallKrunkitDriver(t *testing.T) {
	vm, dir := newFake(t)
	_ = os.WriteFile(filepath.Join(dir, "info"), []byte(macInfo(false)), 0o644)
	withKrunkit(t, dir, true)
	withBrew(t, vm, dir, true)
	krunkitDirs = append(krunkitDirs, filepath.Join(dir, "brew-bin"))
	if err := vm.InstallKrunkit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := brewCalls(dir); got != "upgrade lima\n" {
		t.Errorf("brew ran %q", got)
	}
}

func TestInstallKrunkitCant(t *testing.T) {
	for _, tc := range []struct {
		name     string
		info     string
		brew     bool
		limaBrew bool
		failing  bool
		want     string
	}{
		{"no Homebrew", macInfo(true), false, false, false, "there's no Homebrew"},
		{"no driver, Lima not Homebrew's", macInfo(false), true, false, false, "isn't Homebrew's"},
		{"brew fails", macInfo(true), true, false, true, "brew tap slp/krun"},
		{"intel", `{"hostOS":"darwin","hostArch":"x86_64","vmTypesEx":{"vz":{}}}`, true, false, false, "Apple Silicon"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vm, dir := newFake(t)
			_ = os.WriteFile(filepath.Join(dir, "info"), []byte(tc.info), 0o644)
			withKrunkit(t, dir, false)
			if tc.brew {
				withBrew(t, vm, dir, tc.limaBrew)
			}
			if tc.failing {
				_ = os.WriteFile(filepath.Join(dir, "brew-fails"), nil, 0o644)
			}
			err := vm.InstallKrunkit(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error saying %q", err, tc.want)
			}
		})
	}
}

// A vz VM is installed for, deleted and made again with krunkit at its size.
func TestRemakeWithKrunkit(t *testing.T) {
	vm, dir := newFake(t)
	_ = os.WriteFile(filepath.Join(dir, "info"), []byte(macInfo(true)), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "track-list"), nil, 0o644)
	_ = os.WriteFile(filepath.Join(dir, "list"), []byte(`{"name":"agentbox","status":"Running","vmType":"vz","cpus":6,"memory":12884901888,"disk":214748364800}`+"\n"), 0o644)
	withKrunkit(t, dir, false)
	withBrew(t, vm, dir, false)
	if err := remakeWithKrunkit(context.Background(), func() (*VM, error) { return vm, nil }); err != nil {
		t.Fatalf("%v\n%s", err, vm.Log)
	}
	got := calls(t, dir)
	del := slices.Index(got, "delete --force agentbox")
	create := slices.IndexFunc(got, func(c string) bool { return strings.HasPrefix(c, "create ") })
	if del < 0 || create < del {
		t.Errorf("not deleted, then made: %q", got)
	}
	if !strings.Contains(brewCalls(dir), "install krunkit") {
		t.Error("krunkit wasn't installed")
	}
	def := created(t, dir)
	for _, want := range []string{"\nvmType: krunkit\n", "cpus: 6\n", "memory: 12GiB\n", "disk: 200GiB\n"} {
		if !strings.Contains(def, want) {
			t.Errorf("made without %q:\n%s", want, def)
		}
	}
	if !slices.ContainsFunc(got, func(c string) bool { return strings.Contains(c, "agentbox daemon start") }) {
		t.Errorf("AgentBox wasn't set up in the new VM: %q", got)
	}
}

// Nothing is deleted when krunkit can't be installed, nor when the VM is
// krunkit's already.
func TestRemakeWithKrunkitKeeps(t *testing.T) {
	for _, tc := range []struct {
		name, vmType string
		brew         bool
	}{
		{"no Homebrew", "vz", false},
		{"krunkit's already", "krunkit", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vm, dir := newFake(t)
			_ = os.WriteFile(filepath.Join(dir, "info"), []byte(macInfo(true)), 0o644)
			_ = os.WriteFile(filepath.Join(dir, "list"), []byte(`{"name":"agentbox","status":"Running","vmType":"`+tc.vmType+`"}`+"\n"), 0o644)
			withKrunkit(t, dir, tc.vmType == "krunkit")
			if tc.brew {
				withBrew(t, vm, dir, false)
			}
			err := remakeWithKrunkit(context.Background(), func() (*VM, error) { return vm, nil })
			if (err == nil) != (tc.vmType == "krunkit") {
				t.Errorf("got %v", err)
			}
			if slices.ContainsFunc(calls(t, dir), func(c string) bool { return strings.HasPrefix(c, "delete") || strings.HasPrefix(c, "create") }) {
				t.Errorf("the VM was touched: %q", calls(t, dir))
			}
		})
	}
}

// Lima falling back to vz after the VM was deleted is an error: the point
// was a GPU.
func TestRemakeWithKrunkitFallsBack(t *testing.T) {
	vm, dir := newFake(t)
	_ = os.WriteFile(filepath.Join(dir, "info"), []byte(macInfo(true)), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "track-list"), nil, 0o644)
	_ = os.WriteFile(filepath.Join(dir, "refuse-krunkit"), nil, 0o644)
	_ = os.WriteFile(filepath.Join(dir, "list"), []byte(`{"name":"agentbox","status":"Running","vmType":"vz"}`+"\n"), 0o644)
	withKrunkit(t, dir, true)
	err := remakeWithKrunkit(context.Background(), func() (*VM, error) { return vm, nil })
	if err == nil || !strings.Contains(err.Error(), "after all") {
		t.Errorf("got %v", err)
	}
}

// Nothing starts the VM while vm init is making it, which Lima lists as
// stopped: limactl create, finishing, would kill the krunkit driver, and the
// network of the VM started beside it with it.
func TestUpWaitsForInit(t *testing.T) {
	vm, dir := newFake(t)
	vm.VMType = krunkitVMType
	_ = os.WriteFile(filepath.Join(dir, "track-list"), nil, 0o644)
	_ = os.WriteFile(filepath.Join(dir, "create-waits"), nil, 0o644)
	inited := make(chan error, 1)
	go func() { inited <- vm.initLima(context.Background(), DefaultSize()) }()
	creating := func(c string) bool { return strings.HasPrefix(c, "create ") }
	for deadline := time.Now().Add(10 * time.Second); !slices.ContainsFunc(calls(t, dir), creating); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("vm init never made the VM")
		}
	}
	// The app's daemon start, in a process of its own.
	app := *vm
	app.Log = &bytes.Buffer{}
	upped := make(chan error, 1)
	go func() { upped <- app.Up(context.Background()) }()
	select {
	case err := <-upped:
		t.Fatalf("Up didn't wait for vm init: %v", err)
	case <-time.After(600 * time.Millisecond):
	}
	if slices.ContainsFunc(calls(t, dir), func(c string) bool { return strings.HasPrefix(c, "start") }) {
		t.Fatalf("the VM was started while it was being made: %q", calls(t, dir))
	}
	_ = os.Remove(filepath.Join(dir, "create-waits"))
	if err := <-inited; err != nil {
		t.Fatalf("%v\n%s", err, vm.Log)
	}
	if err := <-upped; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(app.Log.(*bytes.Buffer).String(), "set up") {
		t.Errorf("Up didn't say what it waits for: %q", app.Log)
	}
	got := calls(t, dir)
	if start := slices.Index(got, "start --tty=false agentbox"); start < slices.IndexFunc(got, creating) {
		t.Errorf("not made, then started: %q", got)
	}
}
