package cli

// Profiles are the daemons this command line can act on: this machine's, and
// ones on other machines by the address `agentbox tailnet` publishes. They live
// in one small file beside the rest of this machine's configuration, and one of
// them is active: every command that can act on another daemon acts on that
// one, and says so on stderr (root.go's announceProfile).
//
// A profile is an address, never a credential. Who is asking is the tailnet: the
// daemon trusts the identity `tailscale serve` puts in front of it, which is why
// there is no token in this file.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
)

// localProfile is this machine's daemon, which every installation has and no
// one can remove.
const localProfile = "local"

// probeTimeout bounds one address's answer, so `agentbox profiles` with an
// unreachable tailnet host still prints the others.
const probeTimeout = 5 * time.Second

// profileTarget is one profile: a daemon's address, or this machine when it is
// empty.
type profileTarget struct {
	URL string `json:"url"`
}

// profilesFile is profiles.json: the profiles and the one every command acts on
// unless it says otherwise.
type profilesFile struct {
	Active   string                   `json:"active"`
	Profiles map[string]profileTarget `json:"profiles"`
}

// newProfiles is the file an installation that made none has: this machine, and
// nothing else.
func newProfiles() profilesFile {
	return profilesFile{Active: localProfile, Profiles: map[string]profileTarget{localProfile: {}}}
}

// active is the profile commands act on, never empty: a file with no active
// profile, or one naming a profile it no longer holds, acts on this machine.
func (f profilesFile) active() string {
	if _, ok := f.Profiles[f.Active]; ok && f.Active != "" {
		return f.Active
	}
	return localProfile
}

// readProfiles reads the file, treating a missing one as an installation with
// this machine and nothing else. A file that can't be parsed is an error rather
// than a silent reset: something wrote it, and guessing would throw away the
// address it holds.
func readProfiles(path string) (profilesFile, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return newProfiles(), nil
	}
	if err != nil {
		return profilesFile{}, err
	}
	var f profilesFile
	if err := json.Unmarshal(data, &f); err != nil {
		return profilesFile{}, fmt.Errorf("%s: %w", path, err)
	}
	if f.Profiles == nil {
		f.Profiles = map[string]profileTarget{}
	}
	if _, ok := f.Profiles[localProfile]; !ok {
		f.Profiles[localProfile] = profileTarget{}
	}
	f.Active = f.active()
	return f, nil
}

// writeProfiles writes the file whole: a temporary file beside it, then a
// rename, so a command that is killed leaves the old one rather than half of a
// new one.
func writeProfiles(path string, f profilesFile) error {
	if _, ok := f.Profiles[localProfile]; !ok {
		f.Profiles[localProfile] = profileTarget{}
	}
	f.Active = f.active()
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// profileName is what a profile may be called: lowercase, digits and dashes,
// so it reads as one word on a command line and in the banner.
var profileName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// normalizeAddress is the daemon's own address as this file keeps it: a scheme
// (https when none is given), no trailing slash, and a path when `tailscale
// serve` publishes the daemon under one. It is the rule the app's own "Connect
// a server" applies (desktop/src/main/connection.ts), so both accept the same
// string.
func normalizeAddress(input string) (string, error) {
	text := strings.TrimSpace(input)
	if text == "" {
		return "", errors.New("no address")
	}
	if !regexp.MustCompile(`(?i)^https?://`).MatchString(text) {
		text = "https://" + text
	}
	u, err := url.Parse(text)
	if err != nil {
		return "", err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("not an http or https address: %s", input)
	}
	if u.Host == "" {
		return "", fmt.Errorf("not an address: %s", input)
	}
	if u.User != nil {
		return "", fmt.Errorf("the address should carry no user or password, since the tailnet says who is asking: %s", input)
	}
	return strings.TrimRight(u.String(), "/"), nil
}

// probeAddress asks a daemon what it is, which is the cheapest thing it answers
// and needs no identity. It is how `profile add` refuses an address that does
// not work before it becomes the default, and how `profiles` says which of them
// are up.
func probeAddress(ctx context.Context, address string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	info, err := api.NewRemoteClient(address, "").Version(ctx)
	if err != nil {
		return "", err
	}
	return info.Version, nil
}

// newProfileCmd is the set of daemons this CLI can act on.
func newProfileCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile",
		Short: "The daemons this command line can act on, and which one it acts on now",
		Long: `Lists the daemons by name: this machine's, and ones on other machines by the
address agentbox tailnet publishes. The active one is where commands that can
act on another daemon act; machine-only commands (shell, exec of an Incus agent,
host, daemon, tailnet) always act here and say so.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, err := readProfiles(a.paths.Profiles())
			if err != nil {
				return err
			}
			names := make([]string, 0, len(f.Profiles))
			for name := range f.Profiles {
				names = append(names, name)
			}
			sort.Strings(names)

			// Every address is asked at once: a tailnet host that is asleep must
			// not hold the list of the ones that are not.
			type answer struct {
				version string
				err     error
			}
			answers := make(map[string]answer, len(names))
			var mu sync.Mutex
			var wg sync.WaitGroup
			for _, name := range names {
				if name == localProfile {
					continue
				}
				wg.Add(1)
				go func(name string) {
					defer wg.Done()
					version, err := probeAddress(cmd.Context(), f.Profiles[name].URL)
					mu.Lock()
					defer mu.Unlock()
					answers[name] = answer{version: version, err: err}
				}(name)
			}
			wg.Wait()

			out := cmd.OutOrStdout()
			table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintln(table, "PROFILE\tUSE\tADDRESS\tANSWER")
			for _, name := range names {
				address, use, state := "this machine", "", ""
				if name != localProfile {
					address = f.Profiles[name].URL
				}
				if name == f.active() {
					use = "yes"
				}
				if name == localProfile {
					version, err := api.NewClient(a.paths.Socket()).Version(cmd.Context())
					if err == nil {
						state = "ok " + version.Version
					} else {
						state = "not running"
					}
				} else if got := answers[name]; got.err != nil {
					state = "unreachable"
				} else {
					state = "ok " + got.version
				}
				_, _ = fmt.Fprintf(table, "%s\t%s\t%s\t%s\n", name, use, address, state)
			}
			return table.Flush()
		},
	}
	cmd.AddCommand(newProfileAddCmd(a), newProfileRmCmd(a), newProfileSetCmd(a))
	return cmd
}

// newProfileAddCmd saves a daemon under a name, once it answers.
func newProfileAddCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <name> <address>",
		Short: "Save a daemon under a name, once it answers",
		Long: `Saves a daemon on another machine under a name. The address is the one
agentbox tailnet prints there, like https://host.tail…/agentbox: a scheme, the
host, and its path when tailscale serve publishes it under one.

The daemon is asked for its version before the profile is saved, so a typo is
refused here rather than becoming the next command's strange failure.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := checkProfileName(name); err != nil {
				return err
			}
			address, err := normalizeAddress(args[1])
			if err != nil {
				return err
			}
			version, err := probeAddress(cmd.Context(), address)
			if err != nil {
				return fmt.Errorf("%s: %w", address, err)
			}
			f, err := readProfiles(a.paths.Profiles())
			if err != nil {
				return err
			}
			replaced := false
			if _, ok := f.Profiles[name]; ok {
				replaced = true
			}
			if f.Profiles == nil {
				f.Profiles = map[string]profileTarget{}
			}
			f.Profiles[name] = profileTarget{URL: address}
			if err := writeProfiles(a.paths.Profiles(), f); err != nil {
				return err
			}
			what := "Added"
			if replaced {
				what = "Updated"
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "%s profile %s (%s at %s).\n", what, name, version, address)
			if f.active() != name {
				_, _ = fmt.Fprintf(out, "Use it with: agentbox use %s\n", name)
			}
			return nil
		},
	}
	return cmd
}

// newProfileSetCmd updates an address without touching the name or the active
// profile, which is what "the daemon moved" looks like.
func newProfileSetCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <name> <address>",
		Short: "Point an existing profile at another address",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if name == localProfile {
				return errors.New("local is this machine, and has no address to point elsewhere")
			}
			f, err := readProfiles(a.paths.Profiles())
			if err != nil {
				return err
			}
			if _, ok := f.Profiles[name]; !ok {
				return fmt.Errorf("no profile named %q: agentbox profile lists the ones there are", name)
			}
			address, err := normalizeAddress(args[1])
			if err != nil {
				return err
			}
			version, err := probeAddress(cmd.Context(), address)
			if err != nil {
				return fmt.Errorf("%s: %w", address, err)
			}
			f.Profiles[name] = profileTarget{URL: address}
			if err := writeProfiles(a.paths.Profiles(), f); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Profile %s is %s (%s).\n", name, address, version)
			return nil
		},
	}
	return cmd
}

// newProfileRmCmd forgets a profile. This machine's is not one of them.
func newProfileRmCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "rm <name>",
		Short: "Forget a profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if name == localProfile {
				return errors.New("local is this machine, and stays")
			}
			f, err := readProfiles(a.paths.Profiles())
			if err != nil {
				return err
			}
			if _, ok := f.Profiles[name]; !ok {
				return fmt.Errorf("no profile named %q: agentbox profile lists the ones there are", name)
			}
			delete(f.Profiles, name)
			if f.Active == name {
				f.Active = localProfile
			}
			if err := writeProfiles(a.paths.Profiles(), f); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "Forgot profile %s.\n", name)
			if f.active() == localProfile {
				_, _ = fmt.Fprintln(out, "This machine is the active profile now.")
			}
			return nil
		},
	}
}

// newUseCmd switches the profile every command acts on. It is the short verb
// beside `agentbox profiles`, which is the collection.
func newUseCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "use [profile]",
		Short: "Act on a profile, this machine's or one saved for another daemon",
		Long: `Switches the daemon every command acts on. With no argument it prints the
active one and where it came from.

Machine-only commands are not affected: shell, an exec for an agent of another
machine's Incus, host, daemon and tailnet always act here, and say so.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			f, err := readProfiles(a.paths.Profiles())
			if err != nil {
				return err
			}
			if a.local && len(args) > 0 {
				return errors.New("--local and a profile name are alternatives")
			}
			if len(args) == 0 && !a.local {
				_, _ = fmt.Fprintln(out, targetFor(f, f.active()).describe())
				if len(f.Profiles) <= 1 {
					_, _ = fmt.Fprintln(out, "agentbox profile add <name> <address> saves another daemon to act on.")
				} else {
					_, _ = fmt.Fprintln(out, "agentbox profile lists them; agentbox use <name> switches.")
				}
				return nil
			}
			name := localProfile
			if len(args) > 0 {
				name = args[0]
			}
			if _, ok := f.Profiles[name]; !ok {
				return fmt.Errorf("no profile named %q: agentbox profile lists the ones there are", name)
			}
			f.Active = name
			if err := writeProfiles(a.paths.Profiles(), f); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(out, "Now acting on %s.\n", targetFor(f, name).describe())
			return nil
		},
	}
}

// checkProfileName refuses a name that reads badly on a command line or in the
// banner, and the one name that is always taken.
func checkProfileName(name string) error {
	if name == localProfile {
		return errors.New("local is this machine, which needs no profile")
	}
	if !profileName.MatchString(name) {
		return fmt.Errorf("a profile's name is lowercase letters, digits and dashes: %q doesn't fit", name)
	}
	return nil
}

// targetFor is the target a named profile means: this machine for the local
// profile or one with no address, a daemon's address otherwise.
func targetFor(f profilesFile, name string) target {
	if name == "" {
		name = f.active()
	}
	profile, ok := f.Profiles[name]
	if !ok || profile.URL == "" {
		return target{label: "this machine", active: f.active()}
	}
	return target{label: "profile " + name, url: profile.URL, active: f.active()}
}
