package chv

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The hybrid vsock socket the vz driver serves takes the handshake dialVsock
// makes, carries the stream both ways from the byte after it, and refuses a
// port nothing answers on the way Cloud Hypervisor does.
func TestServeVsock(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "vsock.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	dial := func(port uint32) (net.Conn, error) {
		if port != 22 {
			return nil, errVsockRefused
		}
		vm, host := net.Pipe()
		go func() {
			defer func() { _ = vm.Close() }()
			// An echo that says which port it is first.
			_, _ = vm.Write([]byte("port 22\n"))
			_, _ = io.Copy(vm, vm)
		}()
		return host, nil
	}
	served := make(chan struct{})
	go func() { serveVsock(ln, dial, t.Logf); close(served) }()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	c, err := dialVsock(ctx, socket, 22)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Write([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len("port 22\nhello\n"))
	if _, err := io.ReadFull(c, got); err != nil || string(got) != "port 22\nhello\n" {
		t.Fatalf("read %q, %v", got, err)
	}
	_ = c.Close()

	if _, err := dialVsock(ctx, socket, 1024); err == nil || !strings.Contains(err.Error(), errVsockRefused.Error()) {
		t.Errorf("a port nothing answers on: %v", err)
	}

	// A handshake that isn't one is closed.
	raw, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = raw.Write([]byte("HELLO 22\n"))
	_ = raw.SetReadDeadline(time.Now().Add(5 * time.Second))
	if n, err := raw.Read(make([]byte, 8)); n != 0 || err == nil {
		t.Errorf("a bad handshake got %d bytes, %v", n, err)
	}
	_ = raw.Close()

	_ = ln.Close()
	select {
	case <-served:
	case <-time.After(5 * time.Second):
		t.Fatal("serveVsock didn't return once its listener closed")
	}
}

// A balloon that leaves MemTotal as it booted has what it holds taken off
// the guest's use; one that shrinks MemTotal itself is left as it is.
func TestWithBalloon(t *testing.T) {
	const booted = 16 * GiB
	out := "balloon 2097152\npagesize 4096\n" // 8 GiB
	s := memSample{Total: booted - 200<<20, Available: 5 * GiB}
	if got := withBalloon(s, out, booted); got.Available != 13*GiB || got.Used() != booted-200<<20-13*GiB {
		t.Errorf("MemTotal as booted: available %s, used %s", gib(got.Available), gib(got.Used()))
	}
	shrunk := memSample{Total: 8*GiB - 200<<20, Available: 5 * GiB}
	if got := withBalloon(shrunk, out, booted); got != shrunk {
		t.Errorf("MemTotal shrunk: %+v, want it as it was", got)
	}
	if got := withBalloon(s, "balloon 0\npagesize 4096\n", booted); got != s {
		t.Errorf("an empty balloon: %+v", got)
	}
	if got := withBalloon(s, "", booted); got != s {
		t.Errorf("no balloon lines: %+v", got)
	}
	// Never more available than there is.
	if got := withBalloon(memSample{Total: booted, Available: 12 * GiB}, out, booted); got.Available != booted {
		t.Errorf("available %s, more than MemTotal", gib(got.Available))
	}
}

func TestVZBootMemory(t *testing.T) {
	if got := vzBootMemory(Config{MemoryMin: 4 * GiB, MemoryCap: 8*GiB + 12345}); got != 8*GiB {
		t.Errorf("boots with %d", got)
	}
	if got := vzBootMemory(Config{MemoryMin: 4 * GiB, MemoryCap: 2 * GiB}); got != 4*GiB {
		t.Errorf("a cap under the minimum boots with %d", got)
	}
}

// The vz VM's seed: DHCP on the framework's NAT, the guard against the Mac
// found from the gateway, and its console on hvc0. Everything else is the
// Cloud Hypervisor VM's.
func TestUserDataVZ(t *testing.T) {
	c := testConfig()
	c.Driver = DriverVZ
	files, err := seedFiles(c, testKey)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]string{}
	for _, f := range files {
		byName[f.Name] = string(f.Data)
	}
	ud, network := byName["user-data"], byName["network-config"]
	for _, want := range []string{
		"console=hvc0",
		"ExecStart=/usr/local/lib/agentbox/host-guard.sh\n",
		"After=network-online.target\n",
		`iifname "incusbr0" ip daddr $gw udp dport != 53 drop`,
		"ExecStart=/usr/bin/socat VSOCK-LISTEN:1024,fork,reuseaddr UNIX-CONNECT:/home/lint.linux/.local/share/agentbox/run/agentbox.sock\n",
		"incus storage create default btrfs source=/dev/disk/by-id/virtio-agentbox-pool",
		"echo 'home /home/lint virtiofs rw,nofail,x-systemd.mount-timeout=30s 0 0' >>/etc/fstab",
	} {
		if !strings.Contains(ud, want) {
			t.Errorf("user-data has no %q", want)
		}
	}
	for _, unwanted := range []string{"10.0.2.2", "10.0.2.3", "WantedBy=sysinit.target"} {
		if strings.Contains(ud, unwanted) {
			t.Errorf("user-data has passt's %q", unwanted)
		}
	}
	if !strings.Contains(network, "dhcp4: true") || strings.Contains(network, GuestAddr) {
		t.Errorf("network-config:\n%s", network)
	}
	chFiles, _ := seedFiles(testConfig(), testKey)
	if string(chFiles[0].Data) == byName["meta-data"] {
		t.Error("the vz seed has the Cloud Hypervisor seed's instance-id")
	}
	if strings.Contains(string(chFiles[1].Data), "hvc0") || strings.Contains(string(chFiles[1].Data), "host-guard.sh") {
		t.Error("the Cloud Hypervisor seed has the vz VM's settings")
	}
}

// On a Mac the image is unpacked by its own tar (bsdtar), which has no xz of
// its own to lean on; here, by bsdtar where it's installed.
func TestExtractDiskRawBSD(t *testing.T) {
	bsdtar, err := exec.LookPath("bsdtar")
	if err != nil {
		if bsdtar, err = exec.LookPath("tar"); err != nil || !isBSDTar(bsdtar) {
			t.Skip("no bsdtar")
		}
	}
	dir := t.TempDir()
	const size = 4 << 20
	data := make([]byte, size)
	copy(data[8192:], "boot sector, roughly")
	copy(data[size-3:], "end")
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	_ = tw.WriteHeader(&tar.Header{Name: "disk.raw", Mode: 0o644, Size: size, Typeflag: tar.TypeReg})
	_, _ = tw.Write(data)
	_ = tw.Close()
	tarball := filepath.Join(dir, "image.tar")
	if err := os.WriteFile(tarball, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "root.raw")
	if err := extractDiskRawBSD(t.Context(), bsdtar, tarball, dst); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dst)
	if !bytes.Equal(got, data) {
		t.Fatalf("unpacked %d bytes, not the disk", len(got))
	}
	if fi, _ := os.Stat(dst); allocated(fi) > 1<<20 {
		t.Errorf("%d bytes allocated for a few bytes of data", allocated(fi))
	}
	if err := extractDiskRawBSD(t.Context(), bsdtar, filepath.Join(dir, "missing.tar"), dst); err == nil {
		t.Error("no error for a missing tarball")
	}
}

func isBSDTar(tar string) bool {
	out, _ := exec.Command(tar, "--version").Output()
	return strings.Contains(string(out), "bsdtar")
}
