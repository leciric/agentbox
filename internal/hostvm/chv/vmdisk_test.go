package chv

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// diskTarball is a tar.xz holding disk.raw, as Debian's image and the
// ready-made disk are, with content at its start and zeros after.
func diskTarball(t *testing.T, content string) []byte {
	t.Helper()
	if _, err := exec.LookPath("xz"); err != nil {
		t.Skip("no xz here")
	}
	data := append([]byte(content), make([]byte, 1<<20)...)
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	if err := tw.WriteHeader(&tar.Header{Name: "disk.raw", Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	_, _ = tw.Write(data)
	_ = tw.Close()
	cmd := exec.Command("xz", "-c")
	cmd.Stdin = &raw
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// diskStart is the first bytes of a disk and its size, without reading the
// rest: a root disk is RootDisk bytes, sparse.
func diskStart(t *testing.T, file string) ([]byte, int64) {
	t.Helper()
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	b := make([]byte, 16)
	n, _ := io.ReadFull(f, b)
	fi, _ := f.Stat()
	return b[:n], fi.Size()
}

func hexSum(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// diskServer serves releases: path → body, and counts what was asked for.
type diskServer struct {
	*httptest.Server
	mu    sync.Mutex
	files map[string][]byte
	hits  map[string]int
}

func newDiskServer(t *testing.T, files map[string][]byte) *diskServer {
	s := &diskServer{files: files, hits: map[string]int{}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits[r.URL.Path]++
		b, ok := s.files[r.URL.Path]
		s.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *diskServer) hit(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits[path]
}

// publishedDisk is a release with arch's disk in it.
func publishedDisk(arch string, tarball []byte, sum string) map[string][]byte {
	dir := "/" + DiskTag() + "/"
	return map[string][]byte{
		dir + "SHA256SUMS":   fmt.Appendf(nil, "%s  %s\n%s  %s\n", sum, diskFile(arch), strings.Repeat("0", 64), diskManifestFile(arch)),
		dir + diskFile(arch): tarball,
	}
}

func TestDiskVersion(t *testing.T) {
	if !regexp.MustCompile(`^[0-9a-f]{12}$`).MatchString(DiskVersion()) {
		t.Fatalf("DiskVersion() = %q", DiskVersion())
	}
	if DiskTag() != "vmdisk-"+DiskVersion() {
		t.Fatalf("DiskTag() = %q", DiskTag())
	}
	// The build template is part of it: a changed build is a new disk.
	old := diskBuildTemplate
	t.Cleanup(func() { diskBuildTemplate = old })
	v := DiskVersion()
	diskBuildTemplate += "# changed\n"
	if DiskVersion() == v {
		t.Error("DiskVersion() doesn't change with the build's template")
	}
	// Not image-…, which scripts/github-images.sh deletes as an old base image.
	if strings.HasPrefix(DiskTag(), "image-") {
		t.Fatal("the disk's tag would be taken for a base image's")
	}
}

func TestReadyMadeDisk(t *testing.T) {
	arch := "amd64"
	tarball := diskTarball(t, "ready-made")
	srv := newDiskServer(t, publishedDisk(arch, tarball, hexSum(tarball)))
	cache := filepath.Join(t.TempDir(), "cache")
	dst := filepath.Join(t.TempDir(), "root.raw")
	// An earlier version's tarball, which has no more use.
	_ = os.MkdirAll(cache, 0o755)
	stale := filepath.Join(cache, "agentbox-vm-000000000000-amd64.tar.xz")
	_ = os.WriteFile(stale, []byte("old"), 0o644)

	var log bytes.Buffer
	if err := readyMadeDisk(context.Background(), srv.URL, arch, cache, dst, &log); err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if b, size := diskStart(t, dst); !bytes.HasPrefix(b, []byte("ready-made")) || size != RootDisk {
		t.Fatalf("root disk: %q, %d bytes", b, size)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("an earlier version's tarball is still cached")
	}
	if !strings.Contains(log.String(), "Fetching AgentBox's ready-made VM disk") {
		t.Errorf("log: %s", log.String())
	}

	// Made again (a VM deleted and made anew): from the cache, unfetched.
	_ = os.Remove(dst)
	if err := readyMadeDisk(context.Background(), srv.URL, arch, cache, dst, io.Discard); err != nil {
		t.Fatal(err)
	}
	if n := srv.hit("/" + DiskTag() + "/" + diskFile(arch)); n != 1 {
		t.Errorf("the disk was fetched %d times, want once", n)
	}
}

func TestReadyMadeDiskRefuses(t *testing.T) {
	tarball := diskTarball(t, "ready-made")
	for name, tc := range map[string]struct {
		base   func(t *testing.T) string
		noDisk bool // errNoDisk, which falls back without a warning
	}{
		"off": {base: func(*testing.T) string { return "off" }, noDisk: true},
		"no release for this version": {base: func(t *testing.T) string {
			return newDiskServer(t, map[string][]byte{}).URL
		}, noDisk: true},
		"none for this architecture": {base: func(t *testing.T) string {
			return newDiskServer(t, publishedDisk("arm64", tarball, hexSum(tarball))).URL
		}, noDisk: true},
		"a checksum that doesn't match": {base: func(t *testing.T) string {
			return newDiskServer(t, publishedDisk("amd64", tarball, strings.Repeat("a", 64))).URL
		}},
		"a sum and no disk": {base: func(t *testing.T) string {
			files := publishedDisk("amd64", tarball, hexSum(tarball))
			delete(files, "/"+DiskTag()+"/"+diskFile("amd64"))
			return newDiskServer(t, files).URL
		}},
		"a disk that won't unpack": {base: func(t *testing.T) string {
			junk := []byte("not a tarball")
			return newDiskServer(t, publishedDisk("amd64", junk, hexSum(junk))).URL
		}},
		"a server that fails": {base: func(t *testing.T) string {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
			t.Cleanup(s.Close)
			return s.URL
		}},
	} {
		t.Run(name, func(t *testing.T) {
			cache := t.TempDir()
			dst := filepath.Join(t.TempDir(), "root.raw")
			err := readyMadeDisk(context.Background(), tc.base(t), "amd64", cache, dst, io.Discard)
			if err == nil {
				t.Fatal("made a disk")
			}
			if errors.Is(err, errNoDisk) != tc.noDisk {
				t.Errorf("errNoDisk: %v, want %v (%v)", errors.Is(err, errNoDisk), tc.noDisk, err)
			}
			if _, err := os.Stat(dst); !os.IsNotExist(err) {
				t.Error("left a root disk behind")
			}
			left, _ := filepath.Glob(filepath.Join(cache, "*"))
			for _, f := range left {
				if strings.HasSuffix(f, ".tar.xz") && name != "a disk that won't unpack" {
					t.Errorf("cached %s", f)
				}
			}
			if name == "a disk that won't unpack" && len(left) != 0 {
				t.Errorf("kept a tarball that won't unpack: %v", left)
			}
		})
	}
}

// makeRootDisk takes the ready-made disk when there is one, and Debian's cloud
// image when there's none or it fails, saying which.
func TestMakeRootDiskFallsBack(t *testing.T) {
	arch := runtime.GOARCH
	debianTarball := diskTarball(t, "debian")
	sum := sha512.Sum512(debianTarball)
	old, hadOld := debianImageSHA512[arch]
	debianImageSHA512[arch] = hex.EncodeToString(sum[:])
	t.Cleanup(func() {
		if hadOld {
			debianImageSHA512[arch] = old
		} else {
			delete(debianImageSHA512, arch)
		}
	})
	file, _, _, _ := debianImage(arch)
	ready := diskTarball(t, "ready-made")

	for name, tc := range map[string]struct {
		files       map[string][]byte
		want, said  string
		debianFetch int
	}{
		"published":         {files: publishedDisk(arch, ready, hexSum(ready)), want: "ready-made", said: "Fetching AgentBox's ready-made VM disk"},
		"none published":    {files: map[string][]byte{}, want: "debian", said: "No ready-made VM disk for this AgentBox", debianFetch: 1},
		"one that's broken": {files: publishedDisk(arch, ready, strings.Repeat("b", 64)), want: "debian", said: "warning: the ready-made VM disk failed", debianFetch: 1},
	} {
		t.Run(name, func(t *testing.T) {
			files := map[string][]byte{"/debian/" + debianBuild + "/" + file: debianTarball}
			for k, v := range tc.files {
				files[k] = v
			}
			srv := newDiskServer(t, files)
			t.Setenv("AGENTBOX_VM_DISKS_URL", srv.URL)
			old := debianURL
			debianURL = srv.URL + "/debian"
			t.Cleanup(func() { debianURL = old })

			l := Layout{Root: t.TempDir(), Name: "agentbox"}
			_ = os.MkdirAll(l.Dir(), 0o755)
			var log bytes.Buffer
			if err := makeRootDisk(context.Background(), l, &log); err != nil {
				t.Fatalf("%v\n%s", err, log.String())
			}
			b, _ := diskStart(t, l.RootDisk())
			if !bytes.HasPrefix(b, []byte(tc.want)) {
				t.Errorf("the root disk is %.10q, want %s's", b, tc.want)
			}
			if !strings.Contains(log.String(), tc.said) {
				t.Errorf("log has no %q:\n%s", tc.said, log.String())
			}
			if n := srv.hit("/debian/" + debianBuild + "/" + file); n != tc.debianFetch {
				t.Errorf("Debian's image fetched %d times, want %d", n, tc.debianFetch)
			}
		})
	}
}

func TestParseSums(t *testing.T) {
	a, b := strings.Repeat("a", 64), strings.Repeat("B", 64)
	sums, err := parseSums(strings.NewReader(a + "  agentbox-vm-amd64.tar.xz\n" + b + " *vm-disk-amd64.json\nnonsense\n\n"))
	if err != nil {
		t.Fatal(err)
	}
	if sums["agentbox-vm-amd64.tar.xz"] != a || sums["vm-disk-amd64.json"] != strings.ToLower(b) || len(sums) != 2 {
		t.Fatalf("%v", sums)
	}
}

func TestDiskBuildUserData(t *testing.T) {
	b, err := renderDiskBuildUserData()
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{
		"#cloud-config\n",
		"users: []\n",
		"apt-get install -y -q --no-install-recommends socat " + packages + "\n",
		"path: /usr/sbin/policy-rc.d\n",
		"cloud-init clean --logs --machine-id --seed --configs all\n",
		"rm -f /etc/ssh/ssh_host_*\n",
		"tar -C \"$out\" -cf /dev/disk/by-id/virtio-agentbox-out .\n",
		"systemctl poweroff",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("the build's user-data has no %q", want)
		}
	}
	if strings.Contains(s, "<no value>") {
		t.Error("<no value>")
	}
	if _, err := exec.LookPath("python3"); err == nil && exec.Command("python3", "-c", "import yaml").Run() == nil {
		cmd := exec.Command("python3", "-c", `import sys, yaml
d = yaml.safe_load(sys.stdin)
assert d["users"] == [], d["users"]
assert d["runcmd"][0][0] == "systemd-run", d["runcmd"]
assert d["runcmd"][0][-1] == "/usr/local/lib/agentbox/build-disk", d["runcmd"]
assert [f["path"] for f in d["write_files"]] == ["/usr/sbin/policy-rc.d", "/usr/local/lib/agentbox/build-disk"]
`)
		cmd.Stdin = bytes.NewReader(b)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("the build's user-data isn't the YAML expected: %v\n%s", err, out)
		}
	}
	// The VM's own first boot skips the packages when they're there.
	ud, err := renderUserData(testConfig(), testKey)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ud), "      if ! installed "+packages+"; then\n        apt-get update") {
		t.Error("the VM's first boot installs its packages whether they're there or not")
	}
}

func TestQemuArgs(t *testing.T) {
	work := t.TempDir()
	fwDir := t.TempDir()
	code, vars := filepath.Join(fwDir, "CODE.fd"), filepath.Join(fwDir, "VARS.fd")
	_ = os.WriteFile(code, []byte("code"), 0o644)
	oldAmd64 := amd64Firmware
	t.Cleanup(func() { amd64Firmware = oldAmd64 })
	amd64Firmware = [][2]string{{code, vars}}
	if _, err := qemuArgs("amd64", false, work, 16); err == nil || !strings.Contains(err.Error(), "ovmf") {
		t.Errorf("amd64 without its variables: %v", err)
	}
	_ = os.WriteFile(vars, []byte("vars"), 0o644)
	args, err := qemuArgs("amd64", false, work, 16)
	if err != nil {
		t.Fatal(err)
	}
	s := strings.ReplaceAll(strings.Join(args, " "), work, "/w")
	if b, _ := os.ReadFile(filepath.Join(work, "vars.fd")); string(b) != "vars" {
		t.Error("the UEFI variables aren't copied for the VM to write")
	}
	for _, want := range []string{
		"qemu-system-x86_64 -machine q35 -drive if=pflash,format=raw,readonly=on,file=" + code + " -drive if=pflash,format=raw,file=/w/vars.fd -accel tcg -cpu max -smp 4 -m 2048",
		"-drive file=/w/disk.raw,format=raw,if=none,id=disk,discard=unmap,cache=unsafe -device virtio-blk-pci,drive=disk",
		"-drive file=/w/seed.img,format=raw,if=none,id=seed,readonly=on",
		"-device virtio-blk-pci,drive=out,serial=agentbox-out",
		"-serial stdio", "-no-reboot", "-netdev user,id=net -device virtio-net-pci,netdev=net,romfile= ",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("%s\nhas no %q", s, want)
		}
	}
	if args, _ := qemuArgs("amd64", true, work, 1); !strings.Contains(strings.Join(args, " "), "-accel kvm -cpu host -smp 1") {
		t.Errorf("with KVM: %v", args)
	}

	old := aarch64Firmware
	t.Cleanup(func() { aarch64Firmware = old })
	aarch64Firmware = []string{filepath.Join(t.TempDir(), "none.fd")}
	if _, err := qemuArgs("arm64", false, work, 2); err == nil || !strings.Contains(err.Error(), "qemu-efi-aarch64") {
		t.Errorf("arm64 without firmware: %v", err)
	}
	fw := filepath.Join(t.TempDir(), "QEMU_EFI.fd")
	_ = os.WriteFile(fw, nil, 0o644)
	aarch64Firmware = []string{fw}
	args, err = qemuArgs("arm64", false, work, 2)
	if err != nil || !strings.HasPrefix(strings.Join(args, " "), "qemu-system-aarch64 -machine virt -bios "+fw+" -accel tcg") {
		t.Errorf("arm64: %v, %v", args, err)
	}
	if _, err := qemuArgs("riscv64", false, work, 2); err == nil {
		t.Error("riscv64")
	}
}

func TestReadDiskOutAndPublished(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.raw")
	if err := makeSparse(out, 1<<20); err != nil {
		t.Fatal(err)
	}
	// Nothing written: the build failed.
	if _, err := readDiskOut(out); err == nil {
		t.Fatal("read a result from an empty disk")
	}
	packageLines := "acl\t2.3.2-2\tamd64\tacl\t2.3.2-2\nlibacl1\t2.3.2-2\tamd64\tacl\t2.3.2-2\nincus-base\t6.0.4-2\tamd64\tincus\t6.0.4-2\n"
	f, _ := os.OpenFile(out, os.O_WRONLY, 0)
	tw := tar.NewWriter(f)
	for name, body := range map[string]string{"./result": "ok\n", "./packages.txt": packageLines, "./os-release": "PRETTY_NAME=\"Debian GNU/Linux 13 (trixie)\"\n"} {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.Close()
	_ = f.Close()
	got, err := readDiskOut(out)
	if err != nil || string(got["result"]) != "ok\n" || string(got["packages.txt"]) != packageLines {
		t.Fatalf("%q, %v", got, err)
	}

	pub := t.TempDir()
	disk := []byte("the disk")
	_ = os.WriteFile(filepath.Join(pub, diskFile("amd64")), disk, 0o644)
	m := DiskManifest{Version: DiskVersion(), Arch: "amd64", Debian: debianBuild, File: diskFile("amd64"), Size: int64(len(disk)), SHA256: hexSum(disk), Commit: "abc123"}
	if err := writeDiskPublished(pub, m, got["packages.txt"], got["os-release"]); err != nil {
		t.Fatal(err)
	}
	sums, _ := os.ReadFile(filepath.Join(pub, "SHA256SUMS"))
	parsed, _ := parseSums(bytes.NewReader(sums))
	if len(parsed) != 4 {
		t.Fatalf("SHA256SUMS: %s", sums)
	}
	for name, sum := range parsed {
		b, err := os.ReadFile(filepath.Join(pub, name))
		if err != nil || hexSum(b) != sum {
			t.Errorf("%s doesn't match its sum (%v)", name, err)
		}
	}
	var back DiskManifest
	b, _ := os.ReadFile(filepath.Join(pub, diskManifestFile("amd64")))
	if err := json.Unmarshal(b, &back); err != nil || back.Version != DiskVersion() || back.SHA256 != m.SHA256 {
		t.Errorf("manifest: %s, %v", b, err)
	}
	notice, _ := os.ReadFile(filepath.Join(pub, diskSourcesFile("amd64")))
	for _, want := range []string{
		"- acl 2.3.2-2: https://snapshot.debian.org/package/acl/2.3.2-2/\n",
		"- incus 6.0.4-2: https://snapshot.debian.org/package/incus/6.0.4-2/\n",
		"System: Debian GNU/Linux 13 (trixie).",
		"https://github.com/leciric/agentbox/tree/abc123/internal/hostvm/chv",
	} {
		if !strings.Contains(string(notice), want) {
			t.Errorf("%s has no %q:\n%s", diskSourcesFile("amd64"), want, notice)
		}
	}
	if strings.Count(string(notice), "package/acl/") != 1 {
		t.Error("a source package listed twice")
	}
	if err := writeDiskPublished(pub, m, nil, nil); err == nil {
		t.Error("published a disk with no packages listed")
	}
}
