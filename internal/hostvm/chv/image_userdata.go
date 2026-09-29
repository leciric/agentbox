package chv

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"text/template"
)

//go:embed user-data.yaml.tmpl
var userDataTemplate string

// PoolDiskSerial is the pool disk's serial number, which the supervisor gives
// it (Cloud Hypervisor's --disk serial=) and the VM finds it by, at
// /dev/disk/by-id/virtio-<serial>.
const PoolDiskSerial = "agentbox-pool"

// movableRatio is memory_hotplug.auto_movable_ratio, in percent: how much
// movable memory the kernel lets there be for its Normal memory, the memory
// the VM boots with. 400 covers the default 4 GiB booted and 16 GiB plugged in.
const movableRatio = 400

// kernelParams are the memory_hotplug module's parameters the VM sets, on its
// kernel's command line and, on the first boot, in /sys.
var kernelParams = []struct{ Name, Value string }{
	{"online_policy", "auto-movable"},
	{"auto_movable_ratio", fmt.Sprint(movableRatio)},
}

// fuseInodeLimit is how many of the shared home's inodes the VM's kernel may
// keep cached before it drops them (trim-share-inodes in the user-data), each
// being a file descriptor virtiofsd holds: well under the 524288 a systemd
// session's RLIMIT_NOFILE allows it, with room for a minute's work between
// checks. minVirtiofsdFiles is the least the supervisor is content with.
const (
	fuseInodeLimit    = 200_000
	minVirtiofsdFiles = 2 * fuseInodeLimit
)

type vsockForward struct {
	Port uint32
	What string
	To   string // socat's address
	User string // who runs it; nobody in particular when empty
}

type userDataParams struct {
	Hostname, User, Home, GuestHome, AuthorizedKey string
	UID, GID                                       int
	Gateway, DNS, PoolDevice                       string
	Forwards                                       []vsockForward
	PortSSH                                        int
	MovableRatio                                   int
	KernelParams                                   []struct{ Name, Value string }
	Packages                                       string
	FuseInodeLimit                                 int
}

// KernelArgs is what the VM adds to its kernel's command line.
func (p userDataParams) KernelArgs() string {
	args := []string{"memhp_default_state=online"}
	for _, k := range p.KernelParams {
		args = append(args, "memory_hotplug."+k.Name+"="+k.Value)
	}
	return strings.Join(args, " ")
}

// MakeUser is the shell command that makes the VM's user and their group.
// A GID the image already has is kept, under the name it has.
func (p userDataParams) MakeUser() string {
	return fmt.Sprintf("getent group %d >/dev/null || groupadd -g %d %s; getent passwd %s >/dev/null || useradd -m -d %s -u %d -g %d -s /bin/bash -c AgentBox %s",
		p.GID, p.GID, p.User, p.User, shQuote(p.GuestHome), p.UID, p.GID, p.User)
}

func (p userDataParams) FstabLine() string {
	return fmt.Sprintf("home %s virtiofs rw,nofail,x-systemd.mount-timeout=30s 0 0", p.Home)
}

var (
	userNameRE = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	// A path that fstab, systemd units and the scripts can all hold as is.
	plainPathRE = regexp.MustCompile(`^/[A-Za-z0-9._@+/-]*$`)
)

// packages are what the VM installs on its first boot. incus-base is Incus
// without its virtual machines: the incus package would bring QEMU. dnsmasq
// and apparmor are incus-base's recommends, which Incus's bridge and its
// containers' profiles need.
const packages = "git ca-certificates nftables btrfs-progs acl incus-base dnsmasq-base apparmor"

func newUserDataParams(c Config, authorizedKey string) (userDataParams, error) {
	if !userNameRE.MatchString(c.User) {
		return userDataParams{}, fmt.Errorf("the VM can't have a user named %q", c.User)
	}
	for _, p := range []string{c.Home, c.GuestHome} {
		if !plainPathRE.MatchString(p) || p == "/" {
			return userDataParams{}, fmt.Errorf("the VM can't have a home at %q", p)
		}
	}
	if c.Home == c.GuestHome || strings.HasPrefix(c.GuestHome+"/", c.Home+"/") {
		return userDataParams{}, fmt.Errorf("the VM user's home %s can't be in the host's home %s, which is mounted over it", c.GuestHome, c.Home)
	}
	if c.UID <= 0 || c.GID <= 0 {
		return userDataParams{}, fmt.Errorf("the VM's user can't be root")
	}
	authorizedKey = strings.TrimSpace(authorizedKey)
	if authorizedKey == "" || strings.ContainsAny(authorizedKey, "\n") {
		return userDataParams{}, fmt.Errorf("bad ssh key %q", authorizedKey)
	}
	name := c.Name
	if name == "" {
		name = DefaultName
	}
	return userDataParams{
		Hostname: name, User: c.User, UID: c.UID, GID: c.GID,
		Home: c.Home, GuestHome: c.GuestHome, AuthorizedKey: authorizedKey,
		Gateway: GuestGateway, DNS: GuestDNS,
		PoolDevice: "/dev/disk/by-id/virtio-" + PoolDiskSerial,
		Forwards: []vsockForward{
			{Port: PortDaemon, What: "the daemon's socket", To: "UNIX-CONNECT:" + c.GuestHome + "/.local/share/agentbox/run/agentbox.sock", User: c.User},
			{Port: PortPreview, What: "the preview proxy", To: "TCP:127.0.0.1:7777"},
		},
		PortSSH:        PortSSH,
		MovableRatio:   movableRatio,
		KernelParams:   kernelParams,
		Packages:       packages,
		FuseInodeLimit: fuseInodeLimit,
	}, nil
}

// renderUserData is the seed's user-data for c.
func renderUserData(c Config, authorizedKey string) ([]byte, error) {
	p, err := newUserDataParams(c, authorizedKey)
	if err != nil {
		return nil, err
	}
	t, err := template.New("user-data").Funcs(template.FuncMap{"q": yamlQuote, "sh": shQuote}).Parse(userDataTemplate)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	if err := t.Execute(&b, p); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// networkConfig is the seed's network-config: passt's fixed addresses, on
// whichever virtio NIC the VM has, rather than cloud-init's default of DHCP
// on the first boot's MAC address, which the next boot may not have.
func networkConfig() []byte {
	return fmt.Appendf(nil, `version: 2
ethernets:
  vm:
    match:
      driver: virtio_net
    addresses: [%s/24]
    routes:
      - to: default
        via: %s
    nameservers:
      addresses: [%s]
    dhcp4: false
`, GuestAddr, GuestGateway, GuestDNS)
}

// seedFiles are the seed's files: its instance-id is taken from the rest, so
// a changed seed is a new instance to cloud-init, which sets it up again.
func seedFiles(c Config, authorizedKey string) ([]seedFile, error) {
	userData, err := renderUserData(c, authorizedKey)
	if err != nil {
		return nil, err
	}
	network := networkConfig()
	h := sha256.New()
	h.Write(userData)
	h.Write(network)
	name := c.Name
	if name == "" {
		name = DefaultName
	}
	metaData := fmt.Appendf(nil, "instance-id: agentbox-%s\nlocal-hostname: %s\n", hex.EncodeToString(h.Sum(nil))[:16], name)
	return []seedFile{
		{Name: "meta-data", Data: metaData},
		{Name: "user-data", Data: userData},
		{Name: "network-config", Data: network},
	}, nil
}

// yamlQuote is s as a YAML scalar: JSON's strings are YAML's double-quoted ones.
func yamlQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// shQuote is s as one shell word.
func shQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._/-=:+@") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
