package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
	"agentbox/internal/lan"
	"agentbox/internal/state"
)

// Chatting from a phone on the local network: agentbox phone.

func newPhoneCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "phone",
		Short: "Read and send chat messages from your phone's browser, on this network",
		Long: `Read and send chat messages from your phone's browser, on the same network
as this computer. Off until you turn it on; turned on, scan the QR code it shows
with the phone's camera to pair the phone. Nothing works from a phone that isn't
paired. It is plain HTTP on your local network: use it on a network you trust.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			st, err := c.LAN(cmd.Context())
			if err != nil {
				return err
			}
			printPhoneStatus(cmd.OutOrStdout(), st)
			return nil
		},
	}
	var port int
	on := &cobra.Command{
		Use:   "on",
		Short: "Turn chatting from a phone on, and show a QR code to pair one",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			yes := true
			req := api.UpdateLANRequest{Enabled: &yes}
			if cmd.Flags().Changed("port") {
				req.Port = &port
			}
			st, err := c.UpdateLAN(cmd.Context(), req)
			if err != nil {
				return err
			}
			st, err = waitPhonePort(cmd.Context(), c, st)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if st.Tunnel.Enabled {
				if st, err = waitTunnel(cmd.Context(), c, st, out); err != nil {
					return err
				}
			}
			if !st.Listening && st.Tunnel.URL == "" {
				printPhoneStatus(out, st)
				return errors.New("it's on, but phones can't reach it yet")
			}
			return showPairing(cmd.Context(), out, c)
		},
	}
	on.Flags().IntVar(&port, "port", state.DefaultLANPort, "the port phones connect to")

	off := &cobra.Command{
		Use:   "off",
		Short: "Turn chatting from a phone off: the port closes, paired phones stay paired",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			no := false
			if _, err := c.UpdateLAN(cmd.Context(), api.UpdateLANRequest{Enabled: &no}); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Chatting from a phone is off.")
			return nil
		},
	}

	pair := &cobra.Command{
		Use:   "pair",
		Short: "Show a new QR code to pair a phone, valid for 10 minutes and one phone",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			return showPairing(cmd.Context(), cmd.OutOrStdout(), c)
		},
	}

	list := &cobra.Command{
		Use:   "list",
		Short: "List the paired phones",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			st, err := c.LAN(cmd.Context())
			if err != nil {
				return err
			}
			printPhones(cmd.OutOrStdout(), st.Phones)
			return nil
		},
	}

	revoke := &cobra.Command{
		Use:   "revoke <id or name>",
		Short: "Unpair a phone: it's cut off at once, and needs pairing again",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			st, err := c.LAN(cmd.Context())
			if err != nil {
				return err
			}
			var match []api.LANPhone
			for _, p := range st.Phones {
				if p.ID == args[0] {
					match = []api.LANPhone{p}
					break
				}
				if strings.EqualFold(p.Name, args[0]) {
					match = append(match, p)
				}
			}
			switch len(match) {
			case 0:
				return fmt.Errorf("no paired phone is %q: agentbox phone list shows them", args[0])
			case 1:
			default:
				return fmt.Errorf("%d phones are called %q: revoke one by its id (agentbox phone list)", len(match), args[0])
			}
			if err := c.RemoveLANPhone(cmd.Context(), match[0].ID); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Unpaired %s.\n", match[0].Name)
			return nil
		},
	}
	cmd.AddCommand(on, off, pair, list, revoke, newPhoneTunnelCmd(a))
	return cmd
}

// newPhoneTunnelCmd is agentbox phone tunnel: reaching the phone page from
// anywhere, through a Cloudflare Tunnel the daemon runs.
func newPhoneTunnelCmd(a *app) *cobra.Command {
	status := func(cmd *cobra.Command, _ []string) error {
		c, err := a.client(cmd)
		if err != nil {
			return err
		}
		st, err := c.LAN(cmd.Context())
		if err != nil {
			return err
		}
		printTunnelStatus(cmd.OutOrStdout(), st)
		return nil
	}
	cmd := &cobra.Command{
		Use:   "tunnel",
		Short: "Chat from your phone from anywhere, through a Cloudflare Tunnel",
		Long: `Chat from your phone from anywhere, not only on this computer's network:
AgentBox runs a Cloudflare Tunnel (cloudflared, downloaded the first time) that
puts the phone page on an https address on the internet. Anyone who has the
address reaches the pairing page; only a phone paired with a QR code gets further.

By default it's a quick tunnel, on a trycloudflare.com address that needs no
Cloudflare account and changes each time AgentBox starts, so phones pair again
then. For an address that stays, make a tunnel in Cloudflare's dashboard, point
its public hostname at the address "agentbox phone tunnel" shows, and use:

  agentbox phone tunnel on --hostname chat.example.com   (asks for its token)`,
		Args: cobra.NoArgs,
		RunE: status,
	}
	var hostname string
	var quick, fromStdin bool
	on := &cobra.Command{
		Use:   "on",
		Short: "Put the phone page on the internet, and show a QR code to pair a phone",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			yes := true
			req := api.UpdateLANRequest{Enabled: &yes, Tunnel: &yes}
			switch {
			case quick && hostname != "":
				return errors.New("--quick and --hostname are two different tunnels: pick one")
			case quick:
				req.TunnelToken = new("")
			case hostname != "":
				token, err := secretValue(cmd, "the tunnel's token", fromStdin)
				if err != nil {
					return err
				}
				req.TunnelToken, req.TunnelHostname = &token, &hostname
			}
			st, err := c.UpdateLAN(cmd.Context(), req)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if st, err = waitTunnel(cmd.Context(), c, st, out); err != nil {
				return err
			}
			if st.Tunnel.State != "running" {
				printTunnelStatus(out, st)
				return errors.New("it's on, but the tunnel isn't up yet")
			}
			return showPairing(cmd.Context(), out, c)
		},
	}
	on.Flags().StringVar(&hostname, "hostname", "", "use your own named tunnel, on this public hostname; its token is read from stdin")
	on.Flags().BoolVar(&fromStdin, "token-stdin", false, "read the named tunnel's token from stdin even in a terminal")
	on.Flags().BoolVar(&quick, "quick", false, "go back to a quick tunnel, forgetting the named tunnel's token")

	off := &cobra.Command{
		Use:   "off",
		Short: "Take the phone page off the internet: phones on this network still reach it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			no := false
			if _, err := c.UpdateLAN(cmd.Context(), api.UpdateLANRequest{Tunnel: &no}); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "The tunnel is off: the phone page is on this computer's network only.")
			return nil
		},
	}
	cmd.AddCommand(on, off, &cobra.Command{Use: "status", Short: "Whether the tunnel is on, and its address", Args: cobra.NoArgs, RunE: status})
	return cmd
}

// waitTunnel waits for the tunnel to come up: the first time, cloudflared is
// downloaded first.
func waitTunnel(ctx context.Context, c *api.Client, st api.LANStatus, out io.Writer) (api.LANStatus, error) {
	deadline := time.Now().Add(3 * time.Minute)
	said := ""
	for st.Tunnel.State == "starting" && time.Now().Before(deadline) {
		if st.Tunnel.Error != "" && st.Tunnel.Error != said {
			said = st.Tunnel.Error
			_, _ = fmt.Fprintln(out, said+"…")
		}
		select {
		case <-ctx.Done():
			return st, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
		var err error
		if st, err = c.LAN(ctx); err != nil {
			return st, err
		}
	}
	return st, nil
}

func printTunnelStatus(out io.Writer, st api.LANStatus) {
	t := st.Tunnel
	kind := "a quick tunnel (trycloudflare.com)"
	if t.Named {
		kind = "your named tunnel for " + t.Hostname
	}
	switch {
	case !t.Enabled:
		_, _ = fmt.Fprintln(out, "The tunnel is off: phones reach AgentBox on this computer's network only. Turn it on with: agentbox phone tunnel on")
	case !st.Enabled:
		_, _ = fmt.Fprintf(out, "The tunnel, %s, is on, but chatting from a phone is off: agentbox phone tunnel on turns both on.\n", kind)
	case t.State == "running":
		_, _ = fmt.Fprintf(out, "On, through %s, at %s\n", kind, t.URL)
	case t.Error != "":
		_, _ = fmt.Fprintf(out, "On, through %s, but not up: %s\n", kind, t.Error)
	default:
		_, _ = fmt.Fprintf(out, "On, through %s: starting\n", kind)
	}
	if t.Named {
		_, _ = fmt.Fprintf(out, "In Cloudflare's dashboard, %s's public hostname points at %s.\n", t.Hostname, t.Origin)
	}
}

// waitPhonePort waits a little for the port to open: in AgentBox's VM the
// host opens it, a few seconds after it's turned on.
func waitPhonePort(ctx context.Context, c *api.Client, st api.LANStatus) (api.LANStatus, error) {
	deadline := time.Now().Add(10 * time.Second)
	for !st.Listening && time.Now().Before(deadline) && (st.Error == "" || strings.HasPrefix(st.Error, "waiting")) {
		select {
		case <-ctx.Done():
			return st, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
		var err error
		if st, err = c.LAN(ctx); err != nil {
			return st, err
		}
	}
	return st, nil
}

func showPairing(ctx context.Context, out io.Writer, c *api.Client) error {
	p, err := c.PairLAN(ctx)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "Scan this with your phone's camera to pair it (valid until %s, for one phone):\n\n", p.Expires.Local().Format(time.TimeOnly))
	_, _ = fmt.Fprint(out, lan.Terminal(p.QR))
	_, _ = fmt.Fprintf(out, "\nOr open %s on the phone.\n", p.URLs[0])
	if len(p.URLs) > 1 {
		_, _ = fmt.Fprintf(out, "On another network of this computer's: %s\n", strings.Join(p.URLs[1:], " "))
	}
	if strings.HasPrefix(p.URLs[0], "https://") {
		_, _ = fmt.Fprintln(out, "This goes through a Cloudflare Tunnel, from anywhere: only a phone paired with this code gets past the pairing page.")
	} else {
		_, _ = fmt.Fprintln(out, "This is plain HTTP on your local network: use it on a network you trust.")
	}
	return nil
}

func printPhoneStatus(out io.Writer, st api.LANStatus) {
	switch {
	case !st.Enabled:
		_, _ = fmt.Fprintln(out, "Chatting from a phone is off. Turn it on with: agentbox phone on")
	case st.Listening:
		_, _ = fmt.Fprintf(out, "On, at %s\n", strings.Join(st.URLs, " "))
	default:
		_, _ = fmt.Fprintf(out, "On, but phones can't reach it: %s\n", st.Error)
	}
	if st.Tunnel.Enabled {
		printTunnelStatus(out, st)
	}
	if st.Enabled && st.WebVersion == "" {
		_, _ = fmt.Fprintln(out, "Open the AgentBox app on this computer once: it installs the page phones are shown.")
	}
	if len(st.Phones) > 0 {
		_, _ = fmt.Fprintln(out)
		printPhones(out, st.Phones)
	}
}

func printPhones(out io.Writer, phones []api.LANPhone) {
	if len(phones) == 0 {
		_, _ = fmt.Fprintln(out, "No phone is paired. Pair one with: agentbox phone pair")
		return
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ID\tNAME\tPAIRED\tLAST SEEN")
	for _, p := range phones {
		seen := "never"
		if !p.LastSeen.IsZero() {
			seen = p.LastSeen.Local().Format(time.DateTime)
			if p.LastAddr != "" {
				seen += " from " + p.LastAddr
			}
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", p.ID, p.Name, p.Paired.Local().Format(time.DateOnly), seen)
	}
	_ = w.Flush()
}
