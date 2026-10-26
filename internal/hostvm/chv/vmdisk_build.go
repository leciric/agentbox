package chv

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"
	"time"
)

// diskOutSerial is the serial number of the disk the build VM writes its
// result to, a tar of what it has to say, found at
// /dev/disk/by-id/virtio-<serial>.
const diskOutSerial = "agentbox-out"

// diskBuildTimeout is the longest the build VM is waited for. Without KVM
// (GitHub's arm64 runners), QEMU emulates every instruction.
const diskBuildTimeout = 2 * time.Hour

// DiskManifest is vm-disk-<arch>.json.
type DiskManifest struct {
	Version string    `json:"version"`
	Arch    string    `json:"arch"`
	Debian  string    `json:"debian"`
	File    string    `json:"file"`
	Size    int64     `json:"size"`
	SHA256  string    `json:"sha256"`
	Commit  string    `json:"commit,omitempty"`
	Built   time.Time `json:"built"`
}

// renderDiskBuildUserData is the build VM's user-data.
func renderDiskBuildUserData() ([]byte, error) {
	t, err := template.New("vmdisk-build").Parse(diskBuildTemplate)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	err = t.Execute(&b, struct{ Packages, OutSerial string }{packages, diskOutSerial})
	return b.Bytes(), err
}

// BuildDiskForPublishing makes the ready-made VM disk for arch in dir, with
// what's published beside it (vmdisk.go): Debian's cloud image, fetched into
// cache, booted once in QEMU with the build's seed. It needs qemu-system for
// arch (and for arm64 its UEFI firmware, qemu-efi-aarch64), GNU tar and xz;
// it uses KVM when it can.
func BuildDiskForPublishing(ctx context.Context, arch, dir, cache, commit string, log io.Writer) error {
	debFile, debURL, debSHA512, err := debianImage(arch)
	if err != nil {
		return err
	}
	for _, d := range []string{dir, cache} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	work, err := os.MkdirTemp(cache, "build-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(work) }()

	tarball := filepath.Join(cache, debFile)
	if sum, err := fileSum(tarball, newSHA512()); err != nil || sum != debSHA512 {
		imageLogf(log, "==> Fetching Debian 13's cloud image (%s, %s)\n", debianBuild, arch)
		if _, err := download(ctx, debURL, tarball, newSHA512(), debSHA512, log); err != nil {
			return fmt.Errorf("fetching Debian's cloud image: %w", err)
		}
	}
	disk := filepath.Join(work, "disk.raw")
	if err := extractDiskRaw(ctx, tarball, disk); err != nil {
		return fmt.Errorf("unpacking Debian's cloud image: %w", err)
	}
	userData, err := renderDiskBuildUserData()
	if err != nil {
		return err
	}
	seed, err := fatImage("CIDATA", []seedFile{
		{Name: "meta-data", Data: []byte("instance-id: agentbox-vm-disk-build\nlocal-hostname: debian\n")},
		{Name: "user-data", Data: userData},
	})
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(work, "seed.img"), seed, 0o644); err != nil {
		return err
	}
	if err := makeSparse(filepath.Join(work, "out.raw"), 64<<20); err != nil {
		return err
	}

	kvm := canKVM(arch)
	args, err := qemuArgs(arch, kvm, work, runtime.NumCPU())
	if err != nil {
		return err
	}
	imageLogf(log, "==> Booting it once in QEMU (%s), which installs the packages and seals it\n", map[bool]string{true: "KVM", false: "emulated, no KVM: slow"}[kvm])
	start := time.Now()
	console, err := os.Create(filepath.Join(work, "console.log"))
	if err != nil {
		return err
	}
	defer func() { _ = console.Close() }()
	bctx, cancel := context.WithTimeout(ctx, diskBuildTimeout)
	defer cancel()
	cmd := exec.CommandContext(bctx, args[0], args[1:]...)
	cmd.Stdout = io.MultiWriter(console, log)
	cmd.Stderr = cmd.Stdout
	runErr := cmd.Run()
	result, err := readDiskOut(filepath.Join(work, "out.raw"))
	if runErr != nil || err != nil || strings.TrimSpace(string(result["result"])) != "ok" {
		tail, _ := os.ReadFile(console.Name())
		if len(tail) > 4<<10 {
			tail = tail[len(tail)-4<<10:]
		}
		return fmt.Errorf("making the disk in QEMU failed (qemu: %v, result: %v):\n%s", runErr, err, tail)
	}
	imageLogf(log, "==> Done in QEMU in %s\n", time.Since(start).Round(time.Second))

	file := filepath.Join(dir, diskFile(arch))
	imageLogf(log, "==> Packing %s\n", diskFile(arch))
	if out, err := exec.CommandContext(ctx, "tar", "--format=gnu", "--sparse", "--numeric-owner", "--owner=0", "--group=0",
		"-I", "xz -T0 -6", "-C", work, "-cf", file, "disk.raw").CombinedOutput(); err != nil {
		return fmt.Errorf("tar: %w: %s", err, bytes.TrimSpace(out))
	}
	// What a user's machine does with it: the disk.raw it unpacks is the one
	// that booted.
	check := filepath.Join(work, "check.raw")
	if err := extractDiskRaw(ctx, file, check); err != nil {
		return fmt.Errorf("unpacking %s again: %w", file, err)
	}
	a, _ := fileSum(disk, sha256.New())
	if b, _ := fileSum(check, sha256.New()); a == "" || a != b {
		return fmt.Errorf("%s doesn't unpack to the disk that booted", file)
	}
	_ = os.Remove(check)

	fi, err := os.Stat(file)
	if err != nil {
		return err
	}
	sum, err := fileSum(file, sha256.New())
	if err != nil {
		return err
	}
	m := DiskManifest{Version: DiskVersion(), Arch: arch, Debian: debianBuild, File: diskFile(arch),
		Size: fi.Size(), SHA256: sum, Commit: commit, Built: time.Now().UTC().Truncate(time.Second)}
	return writeDiskPublished(dir, m, result["packages.txt"], result["os-release"])
}

// writeDiskPublished writes what goes beside the disk, and SHA256SUMS of it
// all.
func writeDiskPublished(dir string, m DiskManifest, packageLines, osRelease []byte) error {
	manifest, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(packageLines)) == 0 {
		return errors.New("the build VM listed no packages")
	}
	list := append([]byte("# package\tversion\tarchitecture\tsource\tsource version\n"), packageLines...)
	files := []seedFile{
		{Name: diskManifestFile(m.Arch), Data: append(manifest, '\n')},
		{Name: diskPackagesFile(m.Arch), Data: list},
		{Name: diskSourcesFile(m.Arch), Data: diskSourcesNotice(m, packageLines, osRelease)},
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f.Name), f.Data, 0o644); err != nil {
			return err
		}
	}
	var sums bytes.Buffer
	fmt.Fprintf(&sums, "%s  %s\n", m.SHA256, m.File)
	for _, f := range files {
		fmt.Fprintf(&sums, "%x  %s\n", sha256.Sum256(f.Data), f.Name)
	}
	return os.WriteFile(filepath.Join(dir, "SHA256SUMS"), sums.Bytes(), 0o644)
}

// diskSourcesNotice is VM-SOURCES-<arch>.md: what the disk holds and where the
// source of each of its packages is, as the GPL asks of whoever publishes it.
func diskSourcesNotice(m DiskManifest, packageLines, osRelease []byte) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "# AgentBox's ready-made VM disk %s (%s): sources and licenses\n\n", m.Version, m.Arch)
	fmt.Fprintf(&b, "%s is Debian 13's genericcloud image, build %s (https://cloud.debian.org/images/cloud/trixie/%s/),\n", m.File, m.Debian, m.Debian)
	b.WriteString("with the Debian packages AgentBox's VM needs installed from Debian's own archive")
	if m.Commit != "" {
		fmt.Fprintf(&b, ", made by https://github.com/leciric/agentbox/tree/%s/internal/hostvm/chv", m.Commit)
	}
	b.WriteString(".\nThere is nothing in it but Debian's packages.\n\n")
	if pretty := osReleaseField(osRelease, "PRETTY_NAME"); pretty != "" {
		fmt.Fprintf(&b, "System: %s.\n\n", pretty)
	}
	b.WriteString("## Debian packages\n\n")
	fmt.Fprintf(&b, "%s lists every installed package with its exact version and the source package\n"+
		"it was built from. Debian keeps the source of every version it ever published at\n"+
		"snapshot.debian.org; each source package of this disk is below.\n\n", diskPackagesFile(m.Arch))
	seen := map[string]bool{}
	for line := range strings.SplitSeq(string(packageLines), "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 5 || f[3] == "" {
			continue
		}
		src, ver := f[3], f[4]
		if ver == "" {
			ver = f[1]
		}
		if seen[src+" "+ver] {
			continue
		}
		seen[src+" "+ver] = true
		fmt.Fprintf(&b, "- %s %s: https://snapshot.debian.org/package/%s/%s/\n", src, ver, src, ver)
	}
	return b.Bytes()
}

func osReleaseField(b []byte, key string) string {
	for line := range strings.SplitSeq(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, key+"="); ok {
			return strings.Trim(v, `"`)
		}
	}
	return ""
}

// readDiskOut reads the tar the build VM wrote to its out disk: nothing, an
// error, when it wrote none.
func readDiskOut(file string) (map[string][]byte, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	out := map[string][]byte{}
	tr := tar.NewReader(f)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		b, err := io.ReadAll(io.LimitReader(tr, 16<<20))
		if err != nil {
			return nil, err
		}
		out[filepath.Base(h.Name)] = b
	}
	if len(out) == 0 {
		return nil, errors.New("the build VM wrote no result")
	}
	return out, nil
}

// canKVM is whether QEMU can run arch with KVM here.
func canKVM(arch string) bool {
	if arch != runtime.GOARCH {
		return false
	}
	f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

// Where Debian's and Ubuntu's packages put QEMU's UEFI firmware: ovmf's for
// amd64, in two flash images, its code and a template of its variables, and
// qemu-efi-aarch64's for arm64, which -bios takes. amd64 boots with UEFI
// too, as Cloud Hypervisor boots it: from SeaBIOS, QEMU's default, grub
// never gets the kernel going when QEMU emulates the CPU.
var (
	amd64Firmware = [][2]string{
		{"/usr/share/OVMF/OVMF_CODE_4M.fd", "/usr/share/OVMF/OVMF_VARS_4M.fd"},
		{"/usr/share/OVMF/OVMF_CODE.fd", "/usr/share/OVMF/OVMF_VARS.fd"},
	}
	aarch64Firmware = []string{"/usr/share/qemu-efi-aarch64/QEMU_EFI.fd", "/usr/share/AAVMF/AAVMF_CODE.no-secboot.fd"}
)

func exists(file string) bool { _, err := os.Stat(file); return err == nil }

// qemuArgs runs the build VM from what's in work: disk.raw, seed.img and
// out.raw, with QEMU's own network (DHCP, NAT) and the console on standard
// output. amd64's UEFI variables are copied into work as vars.fd.
func qemuArgs(arch string, kvm bool, work string, cpus int) ([]string, error) {
	var args []string
	switch arch {
	case "amd64":
		var code, vars string
		for _, f := range amd64Firmware {
			if exists(f[0]) && exists(f[1]) {
				code, vars = f[0], f[1]
				break
			}
		}
		if code == "" {
			return nil, errors.New("no UEFI firmware for amd64: install ovmf")
		}
		b, err := os.ReadFile(vars)
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(work, "vars.fd"), b, 0o644); err != nil {
			return nil, err
		}
		args = []string{"qemu-system-x86_64", "-machine", "q35",
			"-drive", "if=pflash,format=raw,readonly=on,file=" + code,
			"-drive", "if=pflash,format=raw,file=" + filepath.Join(work, "vars.fd")}
	case "arm64":
		fw := ""
		for _, f := range aarch64Firmware {
			if exists(f) {
				fw = f
				break
			}
		}
		if fw == "" {
			return nil, errors.New("no UEFI firmware for arm64: install qemu-efi-aarch64")
		}
		args = []string{"qemu-system-aarch64", "-machine", "virt", "-bios", fw}
	default:
		return nil, fmt.Errorf("no ready-made disk for %s", arch)
	}
	if kvm {
		args = append(args, "-accel", "kvm", "-cpu", "host")
	} else {
		args = append(args, "-accel", "tcg", "-cpu", "max")
	}
	cpus = max(1, min(cpus, 4))
	drive := func(name, extra string) []string {
		return []string{"-drive", "file=" + filepath.Join(work, name) + ",format=raw,if=none,id=" + strings.TrimSuffix(name, filepath.Ext(name)) + extra}
	}
	args = append(args, "-smp", fmt.Sprint(cpus), "-m", "2048", "-nodefaults", "-no-reboot", "-display", "none", "-serial", "stdio")
	args = append(args, drive("disk.raw", ",discard=unmap,cache=unsafe")...)
	args = append(args, "-device", "virtio-blk-pci,drive=disk")
	args = append(args, drive("seed.img", ",readonly=on")...)
	args = append(args, "-device", "virtio-blk-pci,drive=seed")
	args = append(args, drive("out.raw", "")...)
	args = append(args, "-device", "virtio-blk-pci,drive=out,serial="+diskOutSerial)
	// romfile= leaves out the NIC's boot ROM (iPXE's efi-virtio.rom), which
	// nothing here boots from and which Ubuntu's qemu-system-arm only
	// recommends: without it, QEMU won't start.
	args = append(args, "-netdev", "user,id=net", "-device", "virtio-net-pci,netdev=net,romfile=", "-device", "virtio-rng-pci")
	return args, nil
}
