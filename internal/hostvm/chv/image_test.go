package chv

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"
)

func testConfig() Config {
	return Config{Name: "agentbox", CPUs: 4, MemoryMin: 4 * GiB, MemoryCap: 20 * GiB, Disk: 100 * GiB,
		User: "lint", UID: 1000, GID: 1000, Home: "/home/lint", GuestHome: "/home/lint.linux"}
}

const testKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFCdhsNFL7K0oOWU7+WaFGUW20mT+1qbmC9Cqiuj9kx+ agentbox-vm\n"

func TestUserData(t *testing.T) {
	b, err := renderUserData(testConfig(), testKey)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{
		"#cloud-config\n",
		"ssh_deletekeys: false\n",
		`hostname: "agentbox"`,
		`- name: "lint"`,
		`- "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFCdhsNFL7K0oOWU7+WaFGUW20mT+1qbmC9Cqiuj9kx+ agentbox-vm"`,
		"groupadd -g 1000 lint",
		"useradd -m -d /home/lint.linux -u 1000 -g 1000 -s /bin/bash",
		`GRUB_CMDLINE_LINUX="$GRUB_CMDLINE_LINUX memhp_default_state=online memory_hotplug.online_policy=auto-movable memory_hotplug.auto_movable_ratio=400"`,
		"echo auto-movable >/sys/module/memory_hotplug/parameters/online_policy",
		"echo 400 >/sys/module/memory_hotplug/parameters/auto_movable_ratio",
		"mkdir -p /home/lint\n",
		"echo 'home /home/lint virtiofs rw,nofail,x-systemd.mount-timeout=30s 0 0' >>/etc/fstab",
		"ExecStart=/usr/bin/socat VSOCK-LISTEN:1024,fork,reuseaddr UNIX-CONNECT:/home/lint.linux/.local/share/agentbox/run/agentbox.sock\n",
		"ExecStart=/usr/bin/socat VSOCK-LISTEN:7777,fork,reuseaddr TCP:127.0.0.1:7777\n",
		"User=lint\n",
		`iifname "incusbr0" ip daddr 10.0.2.2 drop`,
		`iifname "incusbr0" ip daddr 10.0.2.3 udp dport != 53 drop`,
		"systemctl enable --now agentbox-vsock-1024.service agentbox-vsock-7777.service",
		"incus storage create default btrfs source=/dev/disk/by-id/virtio-agentbox-pool",
		"install vsock_loopback /bin/false",
		"if getent group kvm >/dev/null; then usermod -aG kvm lint; fi\n",
		"path: /etc/sysctl.d/60-agentbox.conf",
		"fs.inotify.max_user_instances = 8192\n",
		"fs.inotify.max_user_watches = 524288\n",
		"kernel.keys.maxkeys = 2000\n",
		"sysctl -q --system ||",
		"path: /etc/agentbox/vm\n",
		`[ "${n:-0}" -gt 200000 ] || exit 0`,
		"systemctl enable --now agentbox-trim-share-inodes.timer\n",
		"      host=/home/lint/.ssh\n      vm=/home/lint.linux/.ssh\n",
		"      Include $host/config\n",
		"RequiresMountsFor=/home/lint\n",
		"systemctl enable agentbox-share-ssh.service\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("user-data has no %q", want)
		}
	}
	if strings.Contains(s, "<no value>") || strings.Contains(s, "\t") {
		t.Error("user-data has <no value> or a tab")
	}
	// Every unit that runs as someone runs as the user or as nobody in particular.
	if n := strings.Count(s, "DynamicUser=yes"); n != 1 {
		t.Errorf("%d units with DynamicUser, want 1", n)
	}

	// It's YAML, when there's something here to read it.
	if _, err := exec.LookPath("python3"); err == nil && exec.Command("python3", "-c", "import yaml").Run() == nil {
		cmd := exec.Command("python3", "-c", `import sys, yaml
d = yaml.safe_load(sys.stdin)
assert d["users"][0]["name"] == "lint", d["users"]
assert len(d["write_files"]) == 14, len(d["write_files"])
assert d["runcmd"] == [["/usr/local/lib/agentbox/provision.sh"]]
assert d["bootcmd"][0][:5] == ["cloud-init-per", "instance", "agentbox-user", "sh", "-c"]
`)
		cmd.Stdin = bytes.NewReader(b)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("user-data isn't the YAML expected: %v\n%s", err, out)
		}
	}
}

func TestUserDataRefusesWhatItCantHold(t *testing.T) {
	for name, change := range map[string]func(*Config){
		"user":             func(c *Config) { c.User = "Bad User" },
		"home with space":  func(c *Config) { c.Home = "/home/a b" },
		"guest home in it": func(c *Config) { c.GuestHome = "/home/lint/vm" },
		"same homes":       func(c *Config) { c.GuestHome = c.Home },
		"root":             func(c *Config) { c.UID = 0 },
	} {
		c := testConfig()
		change(&c)
		if _, err := renderUserData(c, testKey); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if _, err := renderUserData(testConfig(), ""); err == nil {
		t.Error("no key: no error")
	}
}

func TestSeedFilesInstanceID(t *testing.T) {
	id := func(c Config) string {
		files, err := seedFiles(c, testKey)
		if err != nil {
			t.Fatal(err)
		}
		m := regexp.MustCompile(`instance-id: (agentbox-[0-9a-f]{16})\n`).FindSubmatch(files[0].Data)
		if files[0].Name != "meta-data" || m == nil {
			t.Fatalf("meta-data is %q", files[0].Data)
		}
		return string(m[1])
	}
	c := testConfig()
	a, b := id(c), id(c)
	c.UID = 1001
	if a != b || a == id(c) {
		t.Error("the instance-id isn't the same for the same seed and different for another")
	}
	files, _ := seedFiles(testConfig(), testKey)
	if n := string(files[2].Data); files[2].Name != "network-config" || !strings.Contains(n, "driver: virtio_net") || !strings.Contains(n, "[10.0.2.15/24]") {
		t.Errorf("network-config is %q", n)
	}
}

func TestShQuote(t *testing.T) {
	for in, want := range map[string]string{
		"/home/lint": "/home/lint",
		"a b":        "'a b'",
		"it's":       `'it'\''s'`,
		"":           "''",
	} {
		if got := shQuote(in); got != want {
			t.Errorf("shQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestToolPins(t *testing.T) {
	hexRE := regexp.MustCompile(`^[0-9a-f]{64}$`)
	names := map[string]bool{}
	for _, tl := range tools {
		names[tl.name] = true
		if !hexRE.MatchString(tl.sha256) || (tl.zipMember != "") != hexRE.MatchString(tl.zipSHA256) {
			t.Errorf("%s: bad pin", tl.name)
		}
		if !strings.HasPrefix(tl.url, "https://") || tl.version == "" {
			t.Errorf("%s: bad URL or version", tl.name)
		}
	}
	for _, want := range []string{"cloud-hypervisor", "virtiofsd", "passt", "CLOUDHV.fd"} {
		if !names[want] {
			t.Errorf("no %s", want)
		}
	}
	if len(names) != len(tools) {
		t.Error("two tools of the same name")
	}
	for _, arch := range []string{"amd64", "arm64"} {
		file, url, sha512, err := debianImage(arch)
		if err != nil || !regexp.MustCompile(`^[0-9a-f]{128}$`).MatchString(sha512) || !strings.HasSuffix(url, "/"+debianBuild+"/"+file) || !strings.Contains(file, "-"+arch+"-") {
			t.Errorf("bad Debian pin for %s: %s %s %v", arch, url, sha512, err)
		}
	}
	if _, _, _, err := debianImage("riscv64"); err == nil {
		t.Error("an image for riscv64")
	}
}

func sum256(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func TestEnsureToolFetchesChecksAndSkips(t *testing.T) {
	bin := []byte("#!/bin/sh\necho hi\n")
	var zipped bytes.Buffer
	zw := zip.NewWriter(&zipped)
	w, _ := zw.Create("target/release/tool")
	_, _ = w.Write(bin)
	_ = zw.Close()
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		switch r.URL.Path {
		case "/plain":
			_, _ = w.Write(bin)
		case "/zip":
			_, _ = w.Write(zipped.Bytes())
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	l := Layout{Root: t.TempDir(), Name: "t"}
	_ = os.MkdirAll(filepath.Dir(l.Bin("x")), 0o755)
	ctx := context.Background()

	plain := tool{name: "plain", version: "1", url: srv.URL + "/plain", sha256: sum256(bin), exec: true}
	if err := ensureTool(ctx, l, plain, io.Discard); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(l.Bin("plain")); err != nil || fi.Mode().Perm() != 0o755 {
		t.Fatalf("plain: %v %v", fi, err)
	}
	if err := ensureTool(ctx, l, plain, io.Discard); err != nil || hits != 1 {
		t.Errorf("fetched again (%d fetches): %v", hits, err)
	}

	zipped1 := tool{name: "zipped", version: "1", url: srv.URL + "/zip", zipSHA256: sum256(zipped.Bytes()), zipMember: "target/release/tool", sha256: sum256(bin)}
	if err := ensureTool(ctx, l, zipped1, io.Discard); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(l.Bin("zipped")); !bytes.Equal(got, bin) {
		t.Errorf("zipped: %q", got)
	}

	// A download that isn't what's pinned is never installed, and a tool
	// there at another version stays until one that is replaces it.
	_ = os.WriteFile(l.Bin("other"), []byte("old"), 0o755)
	bad := tool{name: "other", version: "2", url: srv.URL + "/plain", sha256: sum256([]byte("other"))}
	if err := ensureTool(ctx, l, bad, io.Discard); !errors.Is(err, errChecksum) {
		t.Errorf("err = %v", err)
	}
	if got, _ := os.ReadFile(l.Bin("other")); string(got) != "old" {
		t.Errorf("other is %q", got)
	}

	// A tool published only as its latest build (passt) is taken when it
	// isn't the pinned one, with a warning, and kept once it's there.
	var warned strings.Builder
	latest := tool{name: "passt", version: "2", url: srv.URL + "/plain", sha256: sum256([]byte("other")), latestOnly: true}
	if err := ensureTool(ctx, l, latest, &warned); err != nil || !strings.Contains(warned.String(), "warning") {
		t.Errorf("err = %v, log %q", err, warned.String())
	}
	if got, _ := os.ReadFile(l.Bin("passt")); !bytes.Equal(got, bin) {
		t.Errorf("passt is %q", got)
	}
	before := hits
	if err := ensureTool(ctx, l, latest, io.Discard); err != nil || hits != before {
		t.Errorf("fetched passt again (%d fetches): %v", hits-before, err)
	}
	left, _ := filepath.Glob(l.Bin("*.part"))
	zips, _ := filepath.Glob(l.Bin("*.zip"))
	if len(left)+len(zips) != 0 {
		t.Errorf("left behind %v %v", left, zips)
	}
	if err := ensureTool(ctx, l, tool{name: "x", url: srv.URL + "/missing", sha256: sum256(nil)}, io.Discard); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("err = %v", err)
	}
}

func TestSparseCopy(t *testing.T) {
	const size = 8 << 20
	data := make([]byte, size)
	copy(data[4096:], "start")
	copy(data[size-10:], "end")
	f, err := os.Create(filepath.Join(t.TempDir(), "disk"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := sparseCopy(f, bytes.NewReader(data), size); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(f.Name())
	if !bytes.Equal(got, data) {
		t.Fatal("the copy differs")
	}
	var st syscall.Stat_t
	_ = syscall.Stat(f.Name(), &st)
	if st.Blocks*512 > 1<<20 {
		t.Errorf("%d bytes allocated for 8 KiB of data", st.Blocks*512)
	}
	if err := sparseCopy(f, bytes.NewReader(data[:10]), size); err == nil {
		t.Error("a short read wasn't an error")
	}
}

func TestMakeSparseAndSeedAreRedoable(t *testing.T) {
	l := Layout{Root: t.TempDir(), Name: "t"}
	_ = os.MkdirAll(l.Dir(), 0o755)
	if err := makeSparse(l.PoolDisk(), 100*GiB); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(l.PoolDisk())
	if err != nil || fi.Size() != 100*GiB {
		t.Fatalf("%v %v", fi, err)
	}
	_ = os.WriteFile(l.PoolDisk(), []byte("in use"), 0o644)
	if err := makeSparse(l.PoolDisk(), 100*GiB); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(l.PoolDisk()); string(b) != "in use" {
		t.Error("an existing pool disk was made again")
	}
	if err := setNoCOW(l.Dir()); err != nil {
		t.Errorf("setNoCOW: %v", err)
	}
	if _, err := exec.LookPath("ssh-keygen"); err == nil {
		pub, err := ensureKey(context.Background(), l)
		if err != nil || !strings.HasPrefix(pub, "ssh-ed25519 ") {
			t.Fatalf("%q %v", pub, err)
		}
		again, _ := ensureKey(context.Background(), l)
		if again != pub {
			t.Error("the key was made again")
		}
	}
}

func TestWaitCloudInit(t *testing.T) {
	type reply struct {
		out string
		err error
	}
	script := func(replies ...reply) (runInVM, *[]string) {
		var ran []string
		return func(ctx context.Context, argv ...string) ([]byte, error) {
			ran = append(ran, strings.Join(argv, " "))
			if strings.HasPrefix(argv[0], "sudo") {
				return []byte("E: Unable to locate package incus-base\n"), nil
			}
			r := replies[0]
			if len(replies) > 1 {
				replies = replies[1:]
			}
			return []byte(r.out), r.err
		}, &ran
	}
	down := errors.New("ssh: connect: connection refused")
	exit := errors.New("exit status 1")

	run, _ := script(reply{"", down}, reply{`{"status": "running"}`, nil},
		reply{`{"status": "done", "recoverable_errors": {"WARNING": ["x"]}}`, errors.New("exit status 2")})
	var log bytes.Buffer
	if err := waitCloudInit(context.Background(), run, &log, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "The VM is set up") || !strings.Contains(log.String(), "cloud-init warning: x") {
		t.Errorf("log: %s", log.String())
	}

	run, ran := script(reply{`{"status": "error", "extended_status": "error - done", "errors": ["runcmd failed"]}`, exit})
	err := waitCloudInit(context.Background(), run, io.Discard, time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "runcmd failed") || !strings.Contains(err.Error(), "Unable to locate package") {
		t.Errorf("err = %v", err)
	}
	if len(*ran) != 2 || !strings.Contains((*ran)[1], "cloud-init-output.log") {
		t.Errorf("ran %q", *ran)
	}

	run, _ = script(reply{"", down})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := waitCloudInit(ctx, run, io.Discard, time.Millisecond); err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("err = %v", err)
	}
}

// writeFile is the content user-data writes to path, from its block scalar.
func writeFile(t *testing.T, userData, path string) string {
	t.Helper()
	_, rest, ok := strings.Cut(userData, "  - path: "+path+"\n")
	if !ok {
		t.Fatalf("user-data writes no %s", path)
	}
	var b strings.Builder
	inContent := false
	for line := range strings.Lines(rest) {
		switch {
		case !inContent && strings.HasPrefix(line, "    content: |"):
			inContent = true
		case !inContent:
		case strings.HasPrefix(line, "      "):
			b.WriteString(line[6:])
		case strings.TrimSpace(line) == "":
			b.WriteString("\n")
		default:
			return b.String()
		}
	}
	return b.String()
}

// The VM's ssh is the host user's: share-ssh links the host's .ssh into the
// VM user's, leaving what that has of its own, and writes a config that
// includes the host's; run again, it keeps up with the host's.
func TestShareSSH(t *testing.T) {
	dir := t.TempDir()
	c := testConfig()
	c.Home, c.GuestHome = filepath.Join(dir, "host"), filepath.Join(dir, "guest")
	b, err := renderUserData(c, testKey)
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "share-ssh")
	if err := os.WriteFile(script, []byte(writeFile(t, string(b), "/usr/local/lib/agentbox/share-ssh")), 0o755); err != nil {
		t.Fatal(err)
	}
	host, guest := filepath.Join(c.Home, ".ssh"), filepath.Join(c.GuestHome, ".ssh")
	for _, f := range []string{"id_ed25519", "known_hosts", "config", "authorized_keys", "github"} {
		mustWrite(t, filepath.Join(host, f), f)
	}
	mustWrite(t, filepath.Join(guest, "authorized_keys"), "front end's")
	run := func() {
		t.Helper()
		if out, err := exec.Command("sh", script).CombinedOutput(); err != nil {
			t.Fatalf("share-ssh: %v\n%s", err, out)
		}
	}
	run()
	for _, f := range []string{"id_ed25519", "known_hosts", "github"} {
		if to, err := os.Readlink(filepath.Join(guest, f)); err != nil || to != filepath.Join(host, f) {
			t.Errorf("%s links to %q (%v), want the host's", f, to, err)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(guest, "authorized_keys")); string(b) != "front end's" {
		t.Errorf("authorized_keys = %q, want the VM's own kept", b)
	}
	config, _ := os.ReadFile(filepath.Join(guest, "config"))
	if !strings.Contains(string(config), "Include "+host+"/config\n") || !strings.Contains(string(config), "UserKnownHostsFile ~/.ssh/known_hosts "+host+"/known_hosts\n") {
		t.Errorf("config =\n%s", config)
	}
	if fi, err := os.Stat(guest); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("the VM's .ssh: %v, %v", fi.Mode(), err)
	}

	// A key gone from the host goes; a new one comes; a config of the VM
	// user's own, without the marker, stays.
	_ = os.Remove(filepath.Join(host, "github"))
	mustWrite(t, filepath.Join(host, "id_rsa"), "id_rsa")
	mustWrite(t, filepath.Join(guest, "config"), "Host x\n")
	run()
	if _, err := os.Lstat(filepath.Join(guest, "github")); !os.IsNotExist(err) {
		t.Errorf("the link to a key the host no longer has stays: %v", err)
	}
	if _, err := os.Readlink(filepath.Join(guest, "id_rsa")); err != nil {
		t.Errorf("a new key of the host's isn't linked: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(guest, "config")); string(b) != "Host x\n" {
		t.Errorf("the VM user's own config was replaced: %q", b)
	}
}

func mustWrite(t *testing.T, file, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
