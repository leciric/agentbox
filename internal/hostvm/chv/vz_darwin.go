//go:build darwin && cgo

package chv

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Code-Hex/vz/v3"
	"golang.org/x/sys/unix"

	"agentbox/internal/api"
)

// vzMachine runs the VM with Apple's Virtualization framework, in this
// process (vz.go). The framework runs the guest in a process of its own
// (com.apple.Virtualization.VirtualMachine), which ends with this one, so a
// supervisor that dies can't leave a VM behind that nothing controls.
type vzMachine struct {
	c      Config
	l      Layout
	logf   func(string, ...any)
	booted int64

	vm      *vz.VirtualMachine
	bal     *vz.VirtioTraditionalMemoryBalloonDevice
	sock    *vz.VirtioSocketDevice
	vsockLn net.Listener

	stopped  chan struct{} // closed once the VM has stopped
	stopOnce sync.Once
	err      error // why, once stopped is closed
}

func newVZMachine(c Config, l Layout, logf func(string, ...any)) (machine, error) {
	return &vzMachine{c: c, l: l, logf: logf, booted: vzBootMemory(c), stopped: make(chan struct{})}, nil
}

func (m *vzMachine) boot(ctx context.Context) error {
	config, err := m.configuration()
	if err != nil {
		return vzError(err)
	}
	if ok, err := config.Validate(); !ok || err != nil {
		if err == nil {
			err = errors.New("the Virtualization framework refused it")
		}
		return fmt.Errorf("the VM's configuration: %w", vzError(err))
	}
	vm, err := vz.NewVirtualMachine(config)
	if err != nil {
		return vzError(err)
	}
	m.vm = vm
	go m.watch()
	if err := vm.Start(); err != nil {
		return fmt.Errorf("starting the VM: %w", vzError(err))
	}
	socks := vm.SocketDevices()
	if len(socks) == 0 {
		return errors.New("the VM has no vsock device")
	}
	m.sock = socks[0]
	for _, d := range vm.MemoryBalloonDevices() {
		if b := vz.AsVirtioTraditionalMemoryBalloonDevice(d); b != nil {
			m.bal = b
			break
		}
	}
	// The memory policy starts from MemoryMin (Supervise), and grows it
	// from there as the guest needs it.
	if err := m.setMemory(ctx, m.c.MemoryMin); err != nil {
		m.logf("memory: %v", err)
	}
	ln, err := net.Listen("unix", m.l.VsockSocket())
	if err != nil {
		return err
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(true)
	_ = os.Chmod(m.l.VsockSocket(), 0o600)
	m.vsockLn = ln
	go serveVsock(ln, m.dial, m.logf)
	m.logf("the Virtualization framework runs the VM: %d CPUs, %s booted, balloon target %s", m.c.CPUs, gib(m.booted), gib(m.c.MemoryMin))
	return nil
}

// configuration is the VM, as the Virtualization framework is told it.
func (m *vzMachine) configuration() (*vz.VirtualMachineConfiguration, error) {
	var store *vz.EFIVariableStore
	var err error
	if _, serr := os.Stat(m.l.EFIVars()); serr == nil {
		store, err = vz.NewEFIVariableStore(m.l.EFIVars())
	} else {
		store, err = vz.NewEFIVariableStore(m.l.EFIVars(), vz.WithCreatingEFIVariableStore())
	}
	if err != nil {
		return nil, fmt.Errorf("the VM's EFI variables (%s): %w", m.l.EFIVars(), err)
	}
	boot, err := vz.NewEFIBootLoader(vz.WithEFIVariableStore(store))
	if err != nil {
		return nil, err
	}
	config, err := vz.NewVirtualMachineConfiguration(boot, uint(max(m.c.CPUs, 1)), uint64(m.booted))
	if err != nil {
		return nil, err
	}

	id, err := m.machineID()
	if err != nil {
		return nil, err
	}
	platform, err := vz.NewGenericPlatformConfiguration(vz.WithGenericMachineIdentifier(id))
	if err != nil {
		return nil, err
	}
	config.SetPlatformVirtualMachineConfiguration(platform)

	var disks []vz.StorageDeviceConfiguration
	for _, d := range []struct {
		path, id string
		readOnly bool
	}{
		{m.l.RootDisk(), "", false},
		{m.l.PoolDisk(), PoolDiskSerial, false},
		{m.l.Seed(), "", true},
	} {
		if d.readOnly {
			if _, err := os.Stat(d.path); err != nil {
				continue
			}
		}
		att, err := vz.NewDiskImageStorageDeviceAttachmentWithCacheAndSync(d.path, d.readOnly, vz.DiskImageCachingModeCached, vz.DiskImageSynchronizationModeFsync)
		if err != nil {
			return nil, fmt.Errorf("the disk %s: %w", d.path, err)
		}
		dev, err := vz.NewVirtioBlockDeviceConfiguration(att)
		if err != nil {
			return nil, err
		}
		if d.id != "" {
			// The VM finds its pool disk by it, at /dev/disk/by-id/virtio-<id>.
			if err := dev.SetBlockDeviceIdentifier(d.id); err != nil {
				return nil, err
			}
		}
		disks = append(disks, dev)
	}
	config.SetStorageDevicesVirtualMachineConfiguration(disks)

	nat, err := vz.NewNATNetworkDeviceAttachment()
	if err != nil {
		return nil, err
	}
	nic, err := vz.NewVirtioNetworkDeviceConfiguration(nat)
	if err != nil {
		return nil, err
	}
	hw, err := net.ParseMAC(macAddress(m.c.Name))
	if err != nil {
		return nil, err
	}
	mac, err := vz.NewMACAddress(hw)
	if err != nil {
		return nil, err
	}
	nic.SetMACAddress(mac)
	config.SetNetworkDevicesVirtualMachineConfiguration([]*vz.VirtioNetworkDeviceConfiguration{nic})

	if m.c.Home != "" {
		dir, err := vz.NewSharedDirectory(m.c.Home, false)
		if err != nil {
			return nil, fmt.Errorf("sharing %s: %w", m.c.Home, err)
		}
		share, err := vz.NewSingleDirectoryShare(dir)
		if err != nil {
			return nil, err
		}
		fs, err := vz.NewVirtioFileSystemDeviceConfiguration("home")
		if err != nil {
			return nil, err
		}
		fs.SetDirectoryShare(share)
		config.SetDirectorySharingDevicesVirtualMachineConfiguration([]vz.DirectorySharingDeviceConfiguration{fs})
	}

	sock, err := vz.NewVirtioSocketDeviceConfiguration()
	if err != nil {
		return nil, err
	}
	config.SetSocketDevicesVirtualMachineConfiguration([]vz.SocketDeviceConfiguration{sock})

	balloon, err := vz.NewVirtioTraditionalMemoryBalloonDeviceConfiguration()
	if err != nil {
		return nil, err
	}
	config.SetMemoryBalloonDevicesVirtualMachineConfiguration([]vz.MemoryBalloonDeviceConfiguration{balloon})

	entropy, err := vz.NewVirtioEntropyDeviceConfiguration()
	if err != nil {
		return nil, err
	}
	config.SetEntropyDevicesVirtualMachineConfiguration([]*vz.VirtioEntropyDeviceConfiguration{entropy})

	serial, err := vz.NewFileSerialPortAttachment(m.l.SerialLog(), false)
	if err != nil {
		return nil, err
	}
	console, err := vz.NewVirtioConsoleDeviceSerialPortConfiguration(serial)
	if err != nil {
		return nil, err
	}
	config.SetSerialPortsVirtualMachineConfiguration([]*vz.VirtioConsoleDeviceSerialPortConfiguration{console})
	return config, nil
}

// machineID is the VM's identity to the framework, made on its first boot
// and kept, as its EFI variables are.
func (m *vzMachine) machineID() (*vz.GenericMachineIdentifier, error) {
	if _, err := os.Stat(m.l.MachineID()); err == nil {
		return vz.NewGenericMachineIdentifierWithDataPath(m.l.MachineID())
	}
	id, err := vz.NewGenericMachineIdentifier()
	if err != nil {
		return nil, err
	}
	return id, writeFileAtomic(m.l.MachineID(), id.DataRepresentation(), 0o644)
}

// watch closes stopped once the VM, having started, has stopped.
func (m *vzMachine) watch() {
	started := false
	for st := range m.vm.StateChangedNotify() {
		switch st {
		case vz.VirtualMachineStateStopped:
			if started {
				m.stop(nil)
				return
			}
		case vz.VirtualMachineStateError:
			m.stop(errors.New("the Virtualization framework stopped the VM with an error"))
			return
		default:
			started = true
		}
	}
}

func (m *vzMachine) stop(err error) {
	m.stopOnce.Do(func() {
		m.err = err
		close(m.stopped)
	})
}

// dial connects to a vsock port in the VM.
func (m *vzMachine) dial(port uint32) (net.Conn, error) {
	return m.sock.Connect(port)
}

func (m *vzMachine) done() <-chan struct{} { return m.stopped }
func (m *vzMachine) exitErr() error        { return m.err }

func (m *vzMachine) running() bool {
	if m.vm == nil {
		return false
	}
	select {
	case <-m.stopped:
		return false
	default:
		return true
	}
}

func (m *vzMachine) pause(context.Context) error  { return vzError(m.vm.Pause()) }
func (m *vzMachine) resume(context.Context) error { return vzError(m.vm.Resume()) }

func (m *vzMachine) powerOff(timeout time.Duration) (bool, error) {
	if m.vm.State() == vz.VirtualMachineStatePaused {
		_ = m.vm.Resume()
	}
	if _, err := m.vm.RequestStop(); err != nil {
		return false, vzError(err)
	}
	select {
	case <-m.stopped:
		return true, nil
	case <-time.After(timeout):
		return false, nil
	}
}

func (m *vzMachine) halt() {
	if m.running() && m.vm.CanStop() {
		if err := m.vm.Stop(); err != nil {
			m.logf("stopping the VM: %v", err)
		}
		select {
		case <-m.stopped:
		case <-time.After(5 * time.Second):
		}
	}
	if m.vsockLn != nil {
		_ = m.vsockLn.Close()
	}
}

func (m *vzMachine) setCPUs(context.Context, int) error {
	return errConflict("the VM's CPUs can't change while it runs")
}

// setMemory sets the balloon's target: what the guest is left of the memory
// it booted with.
func (m *vzMachine) setMemory(_ context.Context, bytes int64) error {
	if m.bal == nil {
		return errors.New("the VM has no memory balloon")
	}
	m.bal.SetTargetVirtualMachineMemorySize(uint64(min(max(bytes, 0), m.booted) >> 20 << 20))
	return nil
}

// granted is the balloon's target: the framework doesn't say how far the
// guest has got towards it.
func (m *vzMachine) granted(context.Context) int64 {
	if m.bal == nil || !m.running() {
		return 0
	}
	return int64(m.bal.GetTargetVirtualMachineMemorySize())
}

// resident is unknown: the guest's memory is held by the framework's own
// process, not this one.
func (m *vzMachine) resident() (vm, all int64) { return 0, 0 }

// live is what the running VM can be resized to: its CPUs as they are, and a
// memory cap up to what it booted with.
func (m *vzMachine) live(c Config) api.VMLimits {
	return api.VMLimits{MinCPUs: m.c.CPUs, MaxCPUs: m.c.CPUs, MinMemory: c.MemoryMin, MaxMemory: m.booted}
}

func (m *vzMachine) balloon() int64 { return m.booted }

func (m *vzMachine) runFiles() []string { return []string{m.l.VsockSocket()} }

// vzError is err, with what to do about it when it's the entitlement the
// framework wants.
func vzError(err error) error {
	if err != nil && strings.Contains(err.Error(), VZEntitlement) {
		return fmt.Errorf("%w\n%s", err, entitlementHint())
	}
	return err
}

func entitlementHint() string {
	exe, _ := os.Executable()
	return fmt.Sprintf("The app's and the release's agentbox are signed with it; a build of your own isn't: sign it with scripts/mac-sign.sh %s", exe)
}

// CheckVZ says why this Mac can't run the vz driver's VM, or nil: the
// framework's EFI needs macOS 13, and exe has to be signed with
// VZEntitlement. A signature codesign can't read isn't held against it:
// the framework says so itself when the VM starts.
func CheckVZ(exe string) error {
	if v, err := unix.Sysctl("kern.osproductversion"); err == nil {
		major, _, _ := strings.Cut(v, ".")
		if n, err := strconv.Atoi(major); err == nil && n < 13 {
			return fmt.Errorf("the vz driver needs macOS 13 or later, and this Mac has %s", v)
		}
	}
	out, err := exec.Command("codesign", "-d", "--entitlements", "-", "--xml", exe).Output()
	if err == nil && !bytes.Contains(out, []byte(VZEntitlement)) {
		return fmt.Errorf("%s isn't signed with the %s entitlement, which Apple's Virtualization framework needs.\n%s", exe, VZEntitlement, entitlementHint())
	}
	return nil
}
