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
			if !st.Listening {
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
	cmd.AddCommand(on, off, pair, list, revoke)
	return cmd
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
	_, _ = fmt.Fprintln(out, "This is plain HTTP on your local network: use it on a network you trust.")
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
