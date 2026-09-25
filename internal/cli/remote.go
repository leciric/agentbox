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

	"agentbox/internal/api"
)

// Environments: this machine connecting to a hub (agentbox remote), and this
// CLI using environments through a hub (agentbox login, agentbox env, --env).

func newRemoteCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remote",
		Short: "Connect this machine to a hub as an environment, so you can use it from anywhere",
	}
	var token string
	var tokenStdin bool
	connect := &cobra.Command{
		Use:   "connect <hub URL>",
		Short: "Connect to a hub with an environment's token (from agentbox env add, or the app)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if tokenStdin {
				line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
				if err != nil && !errors.Is(err, io.EOF) {
					return err
				}
				token = line
			}
			if strings.TrimSpace(token) == "" {
				return errors.New("pass the environment's token with --token or --token-stdin")
			}
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			st, err := c.ConnectRemote(cmd.Context(), api.RemoteConnectRequest{Hub: args[0], Token: token})
			if err != nil {
				return err
			}
			if !st.Connected {
				return fmt.Errorf("saved the hub, but couldn't connect yet: %s (it keeps trying)", cmp.Or(st.Error, "no answer in 10 seconds"))
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Connected to %s. This machine reconnects by itself, also after restarts.\n", st.Hub)
			return nil
		},
	}
	connect.Flags().StringVar(&token, "token", "", "the environment's token, abx_e_…")
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
				_, _ = fmt.Fprintln(out, "Not connected to a hub. Connect with: agentbox remote connect <hub URL> --token <token>")
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
		Use:   "login <hub URL>",
		Short: "Sign in to a hub, to use its environments with --env",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			url := strings.TrimRight(args[0], "/")
			if email == "" {
				_, _ = fmt.Fprint(cmd.ErrOrStderr(), "Email: ")
				line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
				if err != nil && !errors.Is(err, io.EOF) {
					return err
				}
				email = strings.TrimSpace(line)
			}
			password, err := readPassword(cmd, passwordStdin, false)
			if err != nil {
				return err
			}
			host, _ := os.Hostname()
			session, err := api.HubClient{URL: url}.Login(cmd.Context(), api.HubLoginRequest{Email: email, Password: password, Label: "cli on " + host})
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
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Signed in to %s as %s\n", url, session.User.Email)
			return nil
		},
	}
	cmd.Flags().StringVar(&email, "email", "", "your account's email")
	cmd.Flags().BoolVar(&passwordStdin, "password-stdin", false, "read the password from stdin")
	return cmd
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
		Short: "Add an environment, and print the command that connects a machine to it",
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
var localOnly = map[string]bool{"shell": true, "exec": true, "auth": true, "host": true, "daemon": true, "server": true, "remote": true, "login": true, "logout": true, "env": true, "path": true}

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
