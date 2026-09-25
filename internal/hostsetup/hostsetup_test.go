package hostsetup

import (
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
)

// fakeLookup is a passwd database: dev is a normal account, root is UID 0.
func fakeLookup() Lookup {
	byName := map[string]*user.User{
		"dev":  {Username: "dev", Uid: "1000"},
		"root": {Username: "root", Uid: "0"},
	}
	byID := map[string]*user.User{"1000": byName["dev"], "0": byName["root"]}
	miss := func(what string) error { return os.ErrNotExist }
	return Lookup{
		ByName: func(name string) (*user.User, error) {
			if u, ok := byName[name]; ok {
				return u, nil
			}
			return nil, miss(name)
		},
		ByID: func(uid string) (*user.User, error) {
			if u, ok := byID[uid]; ok {
				return u, nil
			}
			return nil, miss(uid)
		},
	}
}

func TestTargetUserFindsWhoSetupIsFor(t *testing.T) {
	cases := []struct {
		name                     string
		sudoUser, pkexecUID, arg string
		want, wantErr            string
	}{
		{name: "sudo says", sudoUser: "dev", want: "dev"},
		{name: "pkexec says, with a UID", pkexecUID: "1000", want: "dev"},
		{name: "--user, when neither says", arg: "dev", want: "dev"},
		{name: "--user agreeing with pkexec", pkexecUID: "1000", arg: "dev", want: "dev"},
		{name: "--user agreeing with sudo", sudoUser: "dev", arg: "dev", want: "dev"},
		{name: "--user naming someone else", sudoUser: "dev", arg: "root", wantErr: "is not who ran this (dev)"},
		{name: "nobody says", wantErr: "run it as root, with sudo"},
		{name: "a user who isn't here", arg: "ghost", wantErr: "no such user: ghost"},
		{name: "root", arg: "root", wantErr: "not for root"},
		{name: "a name that isn't a name", arg: "dev; rm -rf /", wantErr: "not a user name"},
		{name: "a name with a slash", arg: "../root", wantErr: "not a user name"},
		{name: "PKEXEC_UID that isn't a number", pkexecUID: "dev", wantErr: "is not a UID"},
		{name: "PKEXEC_UID of nobody", pkexecUID: "4242", wantErr: "not a user on this machine"},
	}
	for _, c := range cases {
		got, err := TargetUser(c.sudoUser, c.pkexecUID, c.arg, fakeLookup())
		switch {
		case c.wantErr != "":
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("%s: TargetUser = %q, %v; want an error with %q", c.name, got, err, c.wantErr)
			}
		case err != nil:
			t.Errorf("%s: TargetUser = %v", c.name, err)
		case got != c.want:
			t.Errorf("%s: TargetUser = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestScriptWorksOutWhoSetupIsFor runs the script itself. It works the user out
// before it checks for root, so every one of these stops before it touches the
// machine — which is also what makes them safe to run here.
func TestScriptWorksOutWhoSetupIsFor(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("as root the script would go on and install Incus")
	}
	me, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "host-setup.sh")
	if err := os.WriteFile(path, Script, 0o755); err != nil {
		t.Fatal(err)
	}
	// "has to run as root" is as far as a correct run gets here, and it names
	// the user it worked out, so these check the answer as well as the refusal.
	cases := []struct {
		name     string
		env      []string
		args     []string
		want     string
		wantCode int
	}{
		{name: "sudo says", env: []string{"SUDO_USER=" + me.Username}, want: "host setup is for " + me.Username + ", but it has to run as root", wantCode: 1},
		{name: "pkexec says, with a UID", env: []string{"PKEXEC_UID=" + me.Uid}, want: "host setup is for " + me.Username, wantCode: 1},
		{name: "--user, when neither says", args: []string{"--user", me.Username}, want: "host setup is for " + me.Username, wantCode: 1},
		{name: "--user=name", args: []string{"--user=" + me.Username}, want: "host setup is for " + me.Username, wantCode: 1},
		{name: "--user agreeing with pkexec", env: []string{"PKEXEC_UID=" + me.Uid}, args: []string{"--user", me.Username}, want: "host setup is for " + me.Username, wantCode: 1},
		{name: "--user naming someone else", env: []string{"SUDO_USER=" + me.Username}, args: []string{"--user", "root"},
			want: "--user root is not who ran this (" + me.Username + ")", wantCode: 1},
		{name: "nobody says", want: "who is this for?", wantCode: 1},
		{name: "a user who isn't here", args: []string{"--user", "nosuchuser-agentbox"}, want: "no such user: nosuchuser-agentbox", wantCode: 1},
		{name: "root", args: []string{"--user", "root"}, want: "not for root", wantCode: 1},
		{name: "a name that isn't a name", args: []string{"--user", "dev; touch /tmp/agentbox-test-pwned"}, want: "not a user name", wantCode: 1},
		{name: "PKEXEC_UID that isn't a number", env: []string{"PKEXEC_UID=nobody"}, want: "is not a UID", wantCode: 1},
		{name: "PKEXEC_UID of nobody", env: []string{"PKEXEC_UID=424242"}, want: "not a user on this machine", wantCode: 1},
		{name: "--user with nothing after it", args: []string{"--user"}, want: "--user needs a user name", wantCode: 2},
		{name: "an argument it doesn't know", args: []string{"--install-everything"}, want: "unknown argument: --install-everything", wantCode: 2},
		{name: "--bridge-subnet with nothing after it", env: []string{"SUDO_USER=" + me.Username}, args: []string{"--bridge-subnet"}, want: "--bridge-subnet needs an address/prefix", wantCode: 2},
		{name: "--bridge-subnet that isn't one", env: []string{"SUDO_USER=" + me.Username}, args: []string{"--bridge-subnet", "10.87.0.1; reboot"}, want: "--bridge-subnet wants an IPv4 address and prefix", wantCode: 2},
		{name: "--bridge-subnet with no prefix", env: []string{"SUDO_USER=" + me.Username}, args: []string{"--bridge-subnet=10.87.0.1"}, want: "--bridge-subnet wants an IPv4 address and prefix", wantCode: 2},
		{name: "--bridge-subnet that is one", env: []string{"SUDO_USER=" + me.Username}, args: []string{"--bridge-subnet", "10.87.0.1/24"}, want: "host setup is for " + me.Username, wantCode: 1},
		{name: "--help", args: []string{"--help"}, want: "usage: host-setup.sh", wantCode: 0},
	}
	for _, c := range cases {
		cmd := exec.Command("bash", append([]string{path}, c.args...)...)
		// A clean environment: the SUDO_USER or PKEXEC_UID of whoever is
		// running the tests would decide these otherwise.
		cmd.Env = append([]string{"PATH=" + os.Getenv("PATH")}, c.env...)
		out, err := cmd.CombinedOutput()
		code := cmd.ProcessState.ExitCode()
		if code != c.wantCode {
			t.Errorf("%s: exit %d (%v), want %d\n%s", c.name, code, err, c.wantCode, out)
		}
		if !strings.Contains(string(out), c.want) {
			t.Errorf("%s: output %q, want it to contain %q", c.name, out, c.want)
		}
	}
	if _, err := os.Stat("/tmp/agentbox-test-pwned"); err == nil {
		t.Error("a user name with a shell command in it was run")
	}
}

// TestRootRangeIsAddedOnce runs the script's give_root_a_range on files shaped
// like /etc/subuid. Incus maps every large range of root's to the container's
// ID 0, so a second one is two mappings of the same IDs, which the kernel
// refuses and no container starts. Debian's incus package gives root a range of
// its own after the highest one it finds, which isn't 1000000 when a range
// already ends past it, as Lima's user's does.
func TestRootRangeIsAddedOnce(t *testing.T) {
	script := string(Script)
	start := strings.Index(script, "give_root_a_range() {")
	if start < 0 {
		t.Fatal("host-setup.sh has no give_root_a_range")
	}
	end := strings.Index(script[start:], "\n}\n")
	if end < 0 {
		t.Fatal("give_root_a_range has no end")
	}
	function := script[start : start+end+3]

	const ours = "root:1000000:1000000000\n"
	lima := "troliveiraa:524288:1073741824\n"
	cases := []struct {
		name, before, want string
	}{
		{name: "nothing yet", before: "", want: ours},
		{name: "only a user's range", before: "dev:100000:65536\n", want: "dev:100000:65536\n" + ours},
		{name: "only root's one-ID range for raw.idmap", before: "root:501:1\n", want: "root:501:1\n" + ours},
		{name: "ours already", before: ours, want: ours},
		{name: "Debian's package, after Lima's user", before: lima + "root:1074266113:1000000000\n", want: lima + "root:1074266113:1000000000\n"},
	}
	for _, c := range cases {
		f := filepath.Join(t.TempDir(), "subuid")
		if err := os.WriteFile(f, []byte(c.before), 0o644); err != nil {
			t.Fatal(err)
		}
		// Twice: setup is run again, and the second run must change nothing.
		cmd := exec.Command("bash", "-c", "set -euo pipefail\n"+function+`give_root_a_range "$1"; give_root_a_range "$1"`, "bash", f)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s: %v\n%s", c.name, err, out)
			continue
		}
		got, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != c.want {
			t.Errorf("%s: the file is\n%s\nwant\n%s", c.name, got, c.want)
		}
	}
}

// TestScriptGrantsTheSocketAtOnce holds the script to what makes logging out
// unnecessary. Nothing here runs the ACL itself — that needs root and a real
// Incus — so this reads what the script would do, and the part worth reading is
// which systemd section goes in which unit: ExecStartPost in a .socket drop-in
// belongs under [Socket], and systemd refuses a [Service] section there.
func TestScriptGrantsTheSocketAtOnce(t *testing.T) {
	script := string(Script)
	for _, want := range []string{
		// The socket, granted to this UID, so processes already running get it.
		`"$setfacl" -m "u:$TARGET_USER:rw" "$socket"`,
		// And put back whenever Incus recreates the socket.
		"ExecStartPost=-$setfacl -m u:$TARGET_USER:rw $socket",
		"write_acl_dropin incus.socket Socket",
		"write_acl_dropin incus.service Service",
		`cat >"/etc/systemd/system/$1.d/10-agentbox-$TARGET_USER.conf"`,
		// The group stays, as the fallback the next login picks up.
		`usermod -aG incus-admin "$TARGET_USER"`,
		// Without setfacl there is no ACL, and the next login is all there is.
		"no setfacl on this machine",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("host-setup.sh has no %q", want)
		}
	}
}

// TestScriptInstallsAnIncusNewEnoughToUse: Debian 12 and Ubuntu 22.04 have no
// incus package at all, and 6.0 is the oldest with the `incus snapshot create`
// form AgentBox uses. Those releases get Zabbly, the repository the Incus
// project points at, instead.
func TestScriptInstallsAnIncusNewEnoughToUse(t *testing.T) {
	script := string(Script)
	for _, want := range []string{
		`dpkg --compare-versions "$candidate" ge 6.0`,
		"https://pkgs.zabbly.com/incus/stable",
		"https://pkgs.zabbly.com/key.asc",
		"Signed-By: /etc/apt/keyrings/zabbly.asc",
		"Suites: $codename",
		// acl comes with Incus everywhere: no setfacl, no ACL.
		"pacman -S --needed --noconfirm incus btrfs-progs acl",
		"apt-get install -y -q incus btrfs-progs acl",
		"dnf install -y incus btrfs-progs acl",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("host-setup.sh has no %q", want)
		}
	}
}

// TestSystemLookupFindsThisUser: the production Lookup, over whichever of
// os/user and getent answers on this machine.
func TestSystemLookupFindsThisUser(t *testing.T) {
	me, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	got, err := TargetUser(me.Username, "", "", System())
	if err != nil || got != me.Username {
		t.Errorf("TargetUser(SUDO_USER=%s) = %q, %v", me.Username, got, err)
	}
	if got, err := TargetUser("", me.Uid, "", System()); err != nil || got != me.Username {
		t.Errorf("TargetUser(PKEXEC_UID=%s) = %q, %v", me.Uid, got, err)
	}
	if _, err := TargetUser("", "", "nosuchuser-agentbox", System()); err == nil {
		t.Error("TargetUser(--user nosuchuser-agentbox) found one")
	}
}
