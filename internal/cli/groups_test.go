package cli

import (
	"strings"
	"testing"
)

func TestMissingGroupsFindsWhatADaemonStartedWithout(t *testing.T) {
	gids := map[string]int{"incus-admin": 990, "kvm": 992} // no "incus" group on this host
	lookup := func(name string) (int, bool) {
		gid, ok := gids[name]
		return gid, ok
	}
	cases := []struct {
		name         string
		mine, daemon []int
		want         string
	}{
		{"joined incus-admin after the daemon started", []int{1000, 990, 992}, []int{1000, 992}, "incus-admin"},
		{"joined both", []int{1000, 990, 992}, []int{1000}, "incus-admin kvm"},
		{"the daemon has them all", []int{1000, 990}, []int{1000, 990, 992}, ""},
		{"this command runs in an old session", []int{1000}, []int{1000, 990}, ""},
		{"an unrelated group differs", []int{1000, 27}, []int{1000}, ""},
	}
	for _, c := range cases {
		if got := strings.Join(missingGroups(c.mine, c.daemon, lookup), " "); got != c.want {
			t.Errorf("%s: missingGroups = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestStaleReasonSaysWhyADaemonIsBehind(t *testing.T) {
	yes, no := true, false
	cases := []struct {
		name        string
		missing     []string
		daemonIncus *bool
		canUseIncus bool
		want        string
	}{
		{"joined a group the daemon hasn't", []string{"incus-admin"}, &yes, true, "you joined incus-admin"},
		{"joined two", []string{"incus-admin", "kvm"}, &yes, true, "you joined incus-admin, kvm"},
		{"the daemon started when there was no Incus", nil, &no, true, "Incus was set up on this machine"},
		{"neither of them can reach Incus yet", nil, &no, false, ""},
		{"both can", nil, &yes, true, ""},
		{"a daemon too old to say", nil, nil, true, ""},
		{"nothing to go on", nil, nil, false, ""},
	}
	for _, c := range cases {
		if got := staleReason(c.missing, c.daemonIncus, c.canUseIncus); got != c.want {
			t.Errorf("%s: staleReason = %q, want %q", c.name, got, c.want)
		}
	}
}
