package sshconfig

// ServerScript runs as root in an agent's machine for each ssh connection
// (`incus exec … sh -c ServerScript sh <key> <user>`, by agentbox ssh-proxy)
// and becomes sshd in inetd mode, speaking ssh on the stdin and stdout incus
// exec carries. Nothing listens: the image's ssh service is masked
// (image/system.sh), and each connection is an sshd of its own.
//
// The machine's host key is made on its first connection, so machines don't
// share the image's. The key it is given is the only one let in, for the
// machine's user only (AllowUsers), and never root's; PAM stays on, as
// Debian's sshd has it, since the user's account has no password and sshd
// without PAM turns an account like that away.
const ServerScript = `set -e
key=$1 user=$2
if [ ! -x /usr/sbin/sshd ]; then
  echo "agentbox: this agent's machine has no SSH server: its base image is from before editors could connect to agents. Rebuild it from AgentBox's Setup, then make a new agent." >&2
  exit 1
fi
[ -s /etc/ssh/ssh_host_ed25519_key ] || ssh-keygen -q -t ed25519 -N '' -f /etc/ssh/ssh_host_ed25519_key
mkdir -p /run/sshd
umask 022
printf '%s\n' "$key" >/etc/ssh/agentbox_authorized_keys
exec /usr/sbin/sshd -i -e \
  -h /etc/ssh/ssh_host_ed25519_key \
  -o AuthorizedKeysFile=/etc/ssh/agentbox_authorized_keys \
  -o AllowUsers="$user" \
  -o PermitRootLogin=no \
  -o PubkeyAuthentication=yes \
  -o PasswordAuthentication=no \
  -o KbdInteractiveAuthentication=no \
  -o UsePAM=yes \
  -o PrintMotd=no \
  -o LogLevel=ERROR
`
