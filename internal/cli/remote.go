package cli

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"agentbox/hubapi"
	"agentbox/internal/api"
	"agentbox/internal/lan"
)

// Environments: this machine connecting to a hub (agentbox remote), and this
// CLI using environments through a hub (agentbox login, agentbox env, --env).

func newRemoteCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remote",
		Short: "Connect this machine to a hub as an environment, so you can use it from anywhere",
	}
	var token, name string
	var tokenStdin bool
	connect := &cobra.Command{
		Use:   "connect <hub URL>",
		Short: "Connect this machine to a hub, once a device signed in to it approves",
		Long: `Connects this machine to a hub as an environment. It shows a code, and
connects once a device signed in to the hub approves it: open the link it
prints in a browser signed in to the hub, scan its QR code with a phone that
is, or type the code into the AgentBox app (Approve a device…).

In the app, "Connect this AgentBox to the hub" does all of this in one click.
With --token, it connects with an environment's token instead, from
agentbox env add.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if tokenStdin {
				line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
				if err != nil && !errors.Is(err, io.EOF) {
					return err
				}
				token = line
			}
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if strings.TrimSpace(token) == "" {
				st, err := c.PairRemote(cmd.Context(), api.RemotePairRequest{Hub: args[0], Name: name})
				if err != nil {
					return err
				}
				if st.Pairing == nil {
					return errors.New("the daemon didn't start pairing")
				}
				printPairing(out, st.Pairing.Code, st.Pairing.ApproveURL, st.Pairing.QR)
				for {
					select {
					case <-cmd.Context().Done():
						return cmd.Context().Err()
					case <-time.After(time.Second):
					}
					st, err = c.RemoteStatus(cmd.Context())
					if err != nil {
						return err
					}
					if st.Pairing != nil && st.Pairing.Error != "" {
						return errors.New(st.Pairing.Error)
					}
					if st.Pairing == nil && st.Connected {
						_, _ = fmt.Fprintf(out, "Approved: connected to %s. This machine reconnects by itself, also after restarts.\n", st.Hub)
						return nil
					}
					if st.Pairing == nil && st.Configured && st.Error != "" {
						return fmt.Errorf("approved, but couldn't connect yet: %s (it keeps trying)", st.Error)
					}
				}
			}
			st, err := c.ConnectRemote(cmd.Context(), api.RemoteConnectRequest{Hub: args[0], Token: token})
			if err != nil {
				return err
			}
			if !st.Connected {
				return fmt.Errorf("saved the hub, but couldn't connect yet: %s (it keeps trying)", cmp.Or(st.Error, "no answer in 10 seconds"))
			}
			_, _ = fmt.Fprintf(out, "Connected to %s. This machine reconnects by itself, also after restarts.\n", st.Hub)
			return nil
		},
	}
	connect.Flags().StringVar(&name, "name", "", "the environment's name on the hub (default: this machine's hostname)")
	connect.Flags().StringVar(&token, "token", "", "connect with an environment's token, abx_e_…, instead of asking for approval")
	connect.Flags().BoolVar(&tokenStdin, "token-stdin", false, "read the token from stdin")

	status := &cobra.Command{
		Use:   "status",
		Short: "Show whether this machine is connected to a hub",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			st, err := c.RemoteStatus(cmd.Context())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			switch {
			case !st.Configured:
				if st.Pairing != nil && st.Pairing.Error == "" {
					_, _ = fmt.Fprintf(out, "Waiting for %s to approve the code %s\n", st.Pairing.Hub, st.Pairing.Code)
					return nil
				}
				_, _ = fmt.Fprintln(out, "Not connected to a hub. Connect with: agentbox remote connect <hub URL>")
			case st.Connected:
				_, _ = fmt.Fprintf(out, "Connected to %s since %s\n", st.Hub, st.Since.Local().Format(time.DateTime))
			default:
				_, _ = fmt.Fprintf(out, "Not connected to %s: %s (it keeps trying)\n", st.Hub, cmp.Or(st.Error, "connecting"))
			}
			return nil
		},
	}

	disconnect := &cobra.Command{
		Use:   "disconnect",
		Short: "Disconnect from the hub, and forget it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			if err := c.DisconnectRemote(cmd.Context()); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Disconnected from the hub")
			return nil
		},
	}
	cmd.AddCommand(connect, status, disconnect)
	return cmd
}

// savedHub is a hub this CLI signed in to.
type savedHub struct {
	URL   string `json:"url"`
	Email string `json:"email"`
	Token string `json:"token"`
}

func (a *app) hubsPath() string { return filepath.Join(a.paths.Config, "hubs.json") }

func (a *app) savedHubs() ([]savedHub, error) {
	data, err := os.ReadFile(a.hubsPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var hubs []savedHub
	return hubs, json.Unmarshal(data, &hubs)
}

func (a *app) saveHubs(hubs []savedHub) error {
	if err := os.MkdirAll(a.paths.Config, 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(hubs, "", "  ")
	tmp := a.hubsPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, a.hubsPath())
}

// pickHub returns the saved hub with url, or the only saved hub when url is empty.
func (a *app) pickHub(url string) (savedHub, error) {
	hubs, err := a.savedHubs()
	if err != nil {
		return savedHub{}, err
	}
	url = strings.TrimRight(url, "/")
	for _, h := range hubs {
		if h.URL == url || (url == "" && len(hubs) == 1) {
			return h, nil
		}
	}
	if url == "" && len(hubs) > 1 {
		return savedHub{}, errors.New("you're signed in to several hubs: pick one with --hub")
	}
	return savedHub{}, errors.New("not signed in to that hub: agentbox login <hub URL>")
}

func newLoginCmd(a *app) *cobra.Command {
	var email string
	var passwordStdin bool
	cmd := &cobra.Command{
		Use:   "login <hub URL or setup link>",
		Short: "Connect this CLI to a hub, to use its environments with --env",
		Long: `Connects this CLI to a hub as a device. It shows a code, and is connected
once a device signed in to the hub approves it: in a browser on the link it
prints, by scanning its QR code with a phone, or by typing the code into the
AgentBox app (Approve a device…).

Given the hub's setup link instead (https://…/hub/setup#abx_l_…), it uses it:
on a hub with no owner, that makes you its owner.

Accounts made on a hub before pairing can still sign in with --email and
their password.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			url, secret, err := api.ParseHubLink(args[0])
			if err != nil {
				return err
			}
			host, _ := os.Hostname()
			label := "agentbox CLI on " + cmp.Or(host, "a machine")
			hc := api.HubClient{URL: url}
			var session api.HubSession
			switch {
			case secret != "":
				session, err = hc.Setup(cmd.Context(), api.HubSetupRequest{Secret: secret, Label: label})
			case email != "" || passwordStdin:
				var password string
				if password, err = readPassword(cmd, passwordStdin, false); err == nil {
					session, err = hc.Login(cmd.Context(), api.HubLoginRequest{Email: email, Password: password, Label: label})
				}
			default:
				var p api.HubPairing
				if p, err = hc.Pair(cmd.Context(), api.HubPairRequest{Kind: hubapi.PairDevice, Label: label, Hostname: host}); err != nil {
					return err
				}
				printPairing(cmd.OutOrStdout(), p.Code, p.ApproveURL, p.QR)
				var r api.HubPairResult
				if r, err = hc.WaitForPairing(cmd.Context(), p); err == nil {
					if r.Session == nil {
						return errors.New("the hub approved it, but sent no token")
					}
					session = *r.Session
				}
			}
			if err != nil {
				return err
			}
			hubs, err := a.savedHubs()
			if err != nil {
				return err
			}
			kept := []savedHub{{URL: url, Email: session.User.Email, Token: session.Token}}
			for _, h := range hubs {
				if h.URL != url {
					kept = append(kept, h)
				}
			}
			if err := a.saveHubs(kept); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Connected to %s as %s\n", url, cmp.Or(session.User.Email, session.User.Name, "its owner"))
			return nil
		},
	}
	cmd.Flags().StringVar(&email, "email", "", "sign in with the email and password of an account made before pairing")
	cmd.Flags().BoolVar(&passwordStdin, "password-stdin", false, "with --email, read the password from stdin")
	return cmd
}

// printPairing shows what a device signed in to the hub approves.
func printPairing(out io.Writer, code, approveURL string, qr []string) {
	_, _ = fmt.Fprintf(out, "Approve the code %s from a device signed in to the hub: open\n\n  %s\n\nin a browser there, type the code into the AgentBox app (Approve a device…),\nor scan this with a phone that's signed in:\n\n", code, approveURL)
	if len(qr) > 0 {
		_, _ = fmt.Fprint(out, lan.Terminal(qr))
	}
	_, _ = fmt.Fprintln(out, "\nWaiting for approval…")
}

func newLogoutCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "logout [hub URL]",
		Short: "Sign out of a hub",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			h, err := a.pickHub(optionalRef(args))
			if err != nil {
				return err
			}
			_ = api.HubClient{URL: h.URL, Token: h.Token}.Logout(cmd.Context())
			hubs, _ := a.savedHubs()
			kept := []savedHub{}
			for _, other := range hubs {
				if other.URL != h.URL {
					kept = append(kept, other)
				}
			}
			if err := a.saveHubs(kept); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Signed out of %s\n", h.URL)
			return nil
		},
	}
}

func newEnvCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "env",
		Short: "The environments on the hubs you signed in to",
	}
	list := &cobra.Command{
		Use:   "list",
		Short: "List environments, and whether they're online",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			hubs, err := a.savedHubs()
			if err != nil {
				return err
			}
			if len(hubs) == 0 {
				return errors.New("not signed in to a hub: agentbox login <hub URL>")
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
			_, _ = fmt.Fprintln(w, "ENVIRONMENT\tSTATE\tHOSTNAME\tVERSION\tLAST SEEN\tHUB")
			for _, h := range hubs {
				envs, err := api.HubClient{URL: h.URL, Token: h.Token}.Environments(cmd.Context())
				if err != nil {
					_, _ = fmt.Fprintf(w, "-\t%s\t\t\t\t%s\n", err, h.URL)
					continue
				}
				for _, e := range envs {
					state, seen := "offline", "never"
					if e.Online {
						state = "online"
					}
					if !e.LastSeenAt.IsZero() {
						seen = e.LastSeenAt.Local().Format(time.DateTime)
					}
					_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", e.Name, state, e.Hostname, e.Version, seen, h.URL)
				}
			}
			return w.Flush()
		},
	}
	var hubURL string
	add := &cobra.Command{
		Use:   "add <name>",
		Short: "Add an environment, and print the command that connects a machine to it with its token",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			h, err := a.pickHub(hubURL)
			if err != nil {
				return err
			}
			created, err := api.HubClient{URL: h.URL, Token: h.Token}.CreateEnvironment(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Added the environment %s. On the machine it stands for, run:\n\n  agentbox remote connect %s --token %s\n\nThe token isn't shown again.\n", created.Environment.Name, h.URL, created.Token)
			return nil
		},
	}
	add.Flags().StringVar(&hubURL, "hub", "", "the hub, when you're signed in to several")
	rm := &cobra.Command{
		Use:   "rm <name>",
		Short: "Delete an environment from its hub; its machine disconnects",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			h, e, err := a.findEnvironment(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if err := (api.HubClient{URL: h.URL, Token: h.Token}).DeleteEnvironment(cmd.Context(), e.ID); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Deleted the environment %s from %s\n", e.Name, h.URL)
			return nil
		},
	}
	cmd.AddCommand(list, add, rm)
	return cmd
}

// findEnvironment finds an environment by name on the hubs this CLI signed in to.
func (a *app) findEnvironment(ctx context.Context, name string) (savedHub, api.HubEnvironment, error) {
	hubs, err := a.savedHubs()
	if err != nil {
		return savedHub{}, api.HubEnvironment{}, err
	}
	if len(hubs) == 0 {
		return savedHub{}, api.HubEnvironment{}, errors.New("not signed in to a hub: agentbox login <hub URL>")
	}
	var foundHub savedHub
	var found []api.HubEnvironment
	for _, h := range hubs {
		envs, err := api.HubClient{URL: h.URL, Token: h.Token}.Environments(ctx)
		if err != nil {
			return savedHub{}, api.HubEnvironment{}, fmt.Errorf("%s: %w", h.URL, err)
		}
		for _, e := range envs {
			if e.Name == name {
				foundHub, found = h, append(found, e)
			}
		}
	}
	switch len(found) {
	case 0:
		return savedHub{}, api.HubEnvironment{}, fmt.Errorf("no environment named %s: agentbox env list shows yours", name)
	case 1:
		return foundHub, found[0], nil
	}
	return savedHub{}, api.HubEnvironment{}, fmt.Errorf("several hubs have an environment named %s", name)
}

// localOnly are commands that work on this machine only, so --env can't apply.
var localOnly = map[string]bool{"shell": true, "exec": true, "auth": true, "host": true, "daemon": true, "server": true, "remote": true, "login": true, "logout": true, "env": true, "path": true, "machines": true, "debug": true}

func topCommand(cmd *cobra.Command) string {
	for cmd.HasParent() && cmd.Parent().HasParent() {
		cmd = cmd.Parent()
	}
	return cmd.Name()
}

// readPassword reads a password from stdin, or asks for it (twice when confirm
// is set, which signing in doesn't need).
func readPassword(cmd *cobra.Command, fromStdin, confirm bool) (string, error) {
	if fromStdin {
		line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	password, err := readSecret("Password: ")
	if err != nil {
		return "", err
	}
	if confirm {
		again, err := readSecret("Password again: ")
		if err != nil {
			return "", err
		}
		if again != password {
			return "", errors.New("the passwords differ")
		}
	}
	return password, nil
}
