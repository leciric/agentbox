package main

import "fmt"

// What the prototype's guest runs. The user-data makes the bench user, and a
// unit that writes the guest's address to the serial console at every boot,
// which is how the host finds it on the bridge's DHCP.
func guestUserData(pubkey string) string {
	return `#cloud-config
hostname: agentbox-bench
users:
  - name: bench
    shell: /bin/bash
    lock_passwd: true
    sudo: ALL=(ALL) NOPASSWD:ALL
    ssh_authorized_keys:
      - ` + pubkey + `
write_files:
  - path: /etc/systemd/system/bench-ip.service
    content: |
      [Unit]
      Description=Say the guest's address on the serial console, for agentbox-bench
      Wants=network-online.target
      After=network-online.target
      [Service]
      Type=oneshot
      ExecStart=/bin/sh -c 'for i in $(seq 1 600); do ip=$(ip -4 route get 1.1.1.1 2>/dev/null | sed -n "s/.* src \\([0-9.]*\\).*/\\1/p"); [ -n "$ip" ] && break; sleep 0.1; done; echo "BENCH-IP=$ip" >/dev/ttyS0'
      [Install]
      WantedBy=multi-user.target
runcmd:
  - [systemctl, enable, --now, bench-ip.service]
`
}

const guestInstallIncus = `set -eux
export DEBIAN_FRONTEND=noninteractive
apt() { sudo -E apt-get -o DPkg::Lock::Timeout=300 -q "$@"; }
apt update
apt install -y --no-install-recommends incus btrfs-progs dnsmasq-base nftables git ca-certificates
`

// guestInitIncus puts Incus's pool on the VM's second disk, btrfs so an agent
// is an instant snapshot of the base as it is on the host, and gives it a
// bridge of its own.
const guestInitIncus = `set -eux
sudo incus admin init --preseed <<'EOF'
networks:
- name: incusbr0
  type: bridge
  config:
    ipv4.address: 10.99.9.1/24
    ipv4.nat: "true"
    ipv6.address: none
storage_pools:
- name: default
  driver: btrfs
  config:
    source: /dev/vdb
profiles:
- name: default
  devices:
    root: {path: /, pool: default, type: disk}
    eth0: {name: eth0, network: incusbr0, type: nic}
EOF
`

// guestBase makes the container every agent is copied from: Debian 13, the
// build tools, Go and Node as the repository asks for them, and a user with
// uid 1000, as an agent has.
func guestBase(goVersion, nodeVersion string) string {
	return fmt.Sprintf(`set -eux
sudo incus launch images:debian/13 base
sudo incus exec base -- sh -c 'for i in $(seq 1 300); do getent hosts deb.debian.org >/dev/null && exit 0; sleep 0.2; done; exit 1'
sudo incus exec base -- sh -euxc '
export DEBIAN_FRONTEND=noninteractive
apt-get -q update
apt-get -q install -y --no-install-recommends git ca-certificates curl xz-utils build-essential python3 procps
curl -fsSL https://go.dev/dl/go%[1]s.linux-amd64.tar.gz | tar -C /usr/local -xz
ln -sf /usr/local/go/bin/go /usr/local/go/bin/gofmt /usr/local/bin/
curl -fsSL https://nodejs.org/dist/%[2]s/node-%[2]s-linux-x64.tar.xz | tar -C /usr/local --strip-components=1 -xJ
useradd -m -u 1000 -s /bin/bash bench
su bench -c "git config --global user.name bench && git config --global user.email bench@example.com"
'
sudo incus stop base
`, goVersion, nodeVersion)
}
