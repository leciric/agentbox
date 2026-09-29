package chv

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// The vz driver: on a Mac, AgentBox can run its VM with Apple's
// Virtualization framework itself (github.com/Code-Hex/vz), in the
// supervisor's own process, rather than through Lima. It is experimental:
// Lima stays the default, and nothing of this has run on a real Mac yet.
// The VM is the one Cloud Hypervisor runs on Linux, with the Mac's pieces:
//
//   - Debian's genericcloud image for the Mac's architecture (arm64 or
//     amd64), booted with the framework's EFI, and the same seed.
//   - The Mac's home shared at its own path over the framework's virtiofs.
//   - The framework's NAT network (vmnet), whose DHCP gives the address,
//     gateway and DNS, instead of passt's fixed ones.
//   - vsock, reached from outside the supervisor through a unix socket that
//     speaks Cloud Hypervisor's hybrid vsock handshake (serveVsock), so ssh,
//     `vm proxy` and the forwards are the same on both.
//   - Memory: the framework can't plug memory in, so the VM boots with its
//     whole cap and the memory policy sets a traditional balloon's target
//     instead. Whether macOS gets back what the balloon takes is unproven:
//     Lima's users have seen the guest give memory up and the Mac free none
//     of it (lima-vm/lima#4220, #4226). CPUs are fixed while it runs.
//   - The binary needs the com.apple.security.virtualization entitlement:
//     the app's and the release's are signed with it; a build of one's own
//     is signed with scripts/mac-sign.sh.

// VZEntitlement is what a process needs to run a VM with Apple's
// Virtualization framework.
const VZEntitlement = "com.apple.security.virtualization"

// EFIVars is the VM's EFI variable store, and MachineID the identity the
// framework gives it, both kept from one boot to the next (vz).
func (l Layout) EFIVars() string   { return filepath.Join(l.Dir(), "efi-vars.fd") }
func (l Layout) MachineID() string { return filepath.Join(l.Dir(), "machine-id") }

// vzBootMemory is what a vz VM boots with: its cap, in whole MiB, which the
// balloon then keeps it under.
func vzBootMemory(c Config) int64 {
	return max(c.MemoryCap, c.MemoryMin) >> 20 << 20
}

// serveVsock serves ln the way Cloud Hypervisor's hybrid vsock socket does,
// for a VM whose vsock only this process can reach (vz): each connection
// says "CONNECT <port>\n", and once dial has reached that port in the VM it
// is answered "OK <port>\n" and becomes the stream. A port nothing answers on
// is closed without an answer, which dialVsock takes for errVsockRefused. It
// returns once ln is closed, and the connections it carries have ended.
func serveVsock(ln net.Listener, dial func(port uint32) (net.Conn, error), logf func(string, ...any)) {
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		c, err := ln.Accept()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				logf("vsock: %v", err)
			}
			return
		}
		wg.Go(func() {
			port, err := readConnect(c)
			if err != nil {
				_ = c.Close()
				return
			}
			vm, err := dial(port)
			if err != nil {
				_ = c.Close()
				return
			}
			if _, err := fmt.Fprintf(c, "OK %d\n", port); err != nil {
				_ = c.Close()
				_ = vm.Close()
				return
			}
			pipe(c, vm)
		})
	}
}

// readConnect reads a hybrid vsock handshake's "CONNECT <port>\n", a byte at
// a time so nothing of the stream after it is read.
func readConnect(c net.Conn) (uint32, error) {
	var line []byte
	b := make([]byte, 1)
	for {
		if _, err := c.Read(b); err != nil {
			return 0, err
		}
		if b[0] == '\n' {
			break
		}
		if line = append(line, b[0]); len(line) > 64 {
			return 0, errors.New("vsock: handshake too long")
		}
	}
	p, ok := strings.CutPrefix(strings.TrimSpace(string(line)), "CONNECT ")
	if !ok {
		return 0, fmt.Errorf("vsock: unexpected handshake %q", line)
	}
	port, err := strconv.ParseUint(p, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("vsock: unexpected handshake %q", line)
	}
	return uint32(port), nil
}

// balloonScript prints how much the guest's balloon holds: pages taken less
// pages given back, and the page size.
const balloonScript = `awk '$1 == "balloon_inflate" { i = $2 } $1 == "balloon_deflate" { d = $2 } END { print "balloon", i - d }' /proc/vmstat; echo "pagesize $(getconf PAGESIZE)"`

// withBalloon corrects a sample of a VM whose memory is a balloon over the
// booted bytes it booted with, from balloonScript's lines in out. A balloon
// driver that deflates on OOM leaves MemTotal where it booted and counts
// what the balloon holds as used, which would make the policy grow the VM
// by what it had just taken back: that is taken off again. One that doesn't
// takes it off MemTotal itself, and the sample is right as it is. Unproven,
// like the balloon itself (vz.go).
func withBalloon(s memSample, out string, booted int64) memSample {
	var pages, size int64
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 2 {
			continue
		}
		switch f[0] {
		case "balloon":
			pages, _ = strconv.ParseInt(f[1], 10, 64)
		case "pagesize":
			size, _ = strconv.ParseInt(f[1], 10, 64)
		}
	}
	held := pages * size
	if held > 0 && s.Total > booted-held/2 {
		s.Available = min(s.Available+held, s.Total)
	}
	return s
}
