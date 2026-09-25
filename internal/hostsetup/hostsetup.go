// Package hostsetup holds the one-time, root-only setup of a Linux host for
// AgentBox: Incus with its storage pool and network, the user mapping agents
// need, access to the Incus socket, and firewall rules. `agentbox host setup`
// runs it, and so does scripts/host-setup.sh in a checkout.
package hostsetup

import (
	_ "embed"
	"errors"
	"fmt"
	"os/exec"
	"os/user"
	"regexp"
	"strconv"
	"strings"
)

//go:embed host-setup.sh
var Script []byte

// Command is how to run the setup from a shell, with the agentbox on PATH.
const Command = `sudo "$(command -v agentbox)" host setup`

// name is a plain POSIX user name. The setup writes the name it is given into
// /etc/subuid, a systemd unit and a socket ACL, so nothing else is allowed
// through — the script checks the same shape.
var name = regexp.MustCompile(`^[a-z_][a-z0-9_-]*\$?$`)

// Lookup finds a user by name and by UID. user.Lookup and user.LookupId in
// production; a map in tests.
type Lookup struct {
	ByName func(name string) (*user.User, error)
	ByID   func(uid string) (*user.User, error)
}

// System looks users up in this machine's own passwd database. Each falls back
// on getent, which goes through NSS: a build without cgo reads /etc/passwd and
// nothing else, so a user from LDAP or SSSD wouldn't be found otherwise. The
// script does its own lookup with getent for the same reason.
func System() Lookup {
	return Lookup{
		ByName: func(name string) (*user.User, error) {
			u, err := user.Lookup(name)
			return orGetent(u, err, name, 0)
		},
		ByID: func(uid string) (*user.User, error) {
			u, err := user.LookupId(uid)
			return orGetent(u, err, uid, 2)
		},
	}
}

// orGetent answers with u when os/user found one, and otherwise asks getent and
// reads field (0 for the name, 2 for the UID) back out of the passwd line.
func orGetent(u *user.User, lookupErr error, key string, field int) (*user.User, error) {
	if lookupErr == nil {
		return u, nil
	}
	out, err := exec.Command("getent", "passwd", key).Output()
	if err != nil {
		return nil, fmt.Errorf("no passwd entry for %s", key)
	}
	fields := strings.Split(strings.TrimSpace(string(out)), ":")
	if len(fields) < 3 || fields[field] != key {
		return nil, fmt.Errorf("no passwd entry for %s", key)
	}
	return &user.User{Username: fields[0], Uid: fields[2]}, nil
}

// TargetUser works out who host setup is for. sudo says so in SUDO_USER and
// pkexec says it with a UID in PKEXEC_UID; flag is `host setup --user <name>`,
// which the desktop app passes as a fallback for a pkexec that says neither.
// The two have to agree when both are there, so a run can't quietly set another
// account up as this one.
func TargetUser(sudoUser, pkexecUID, flag string, look Lookup) (string, error) {
	caller := sudoUser
	if caller == "" && pkexecUID != "" {
		if _, err := strconv.Atoi(pkexecUID); err != nil {
			return "", fmt.Errorf("PKEXEC_UID=%s is not a UID", pkexecUID)
		}
		u, err := look.ByID(pkexecUID)
		if err != nil {
			return "", fmt.Errorf("PKEXEC_UID=%s is not a user on this machine", pkexecUID)
		}
		caller = u.Username
	}
	if caller != "" && flag != "" && caller != flag {
		return "", fmt.Errorf("--user %s is not who ran this (%s): leave --user out to set up for %s", flag, caller, caller)
	}
	target := caller
	if target == "" {
		target = flag
	}
	if target == "" {
		return "", errors.New("run it as root, with sudo: " + Command + "\n       or name the user: agentbox host setup --user <name>")
	}
	if !name.MatchString(target) {
		return "", fmt.Errorf("not a user name: %s", target)
	}
	u, err := look.ByName(target)
	if err != nil {
		return "", fmt.Errorf("no such user: %s", target)
	}
	if u.Uid == "0" {
		return "", errors.New("host setup is for the user who runs AgentBox, not for root")
	}
	return target, nil
}
