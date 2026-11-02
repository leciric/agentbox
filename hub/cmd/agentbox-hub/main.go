// Command agentbox-hub runs a hub: accounts, and the environments (machines
// running AgentBox) that connect to it.
//
// It was `agentbox server` in the AgentBox repository, and is the same command
// with the same flags; only the binary it lives in changed.
package main

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"agentbox/hub/internal/hub"
)

// version is the hub's version, set with -ldflags "-X main.version=…".
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := newRootCmd().ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := newServerCmd()
	root.Use = "agentbox-hub"
	root.SilenceUsage, root.SilenceErrors = true, true
	root.Version = version
	return root
}

// dataDir is where the hub keeps its SQLite database when nothing says
// otherwise: AgentBox's own data directory, so a hub run on a machine that also
// runs AgentBox keeps its files in the familiar place.
func dataDir() string {
	if v := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(v) {
		return filepath.Join(v, "agentbox")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "agentbox"
	}
	return filepath.Join(home, ".local", "share", "agentbox")
}

func newServerCmd() *cobra.Command {
	var listen, db, web, cfZone, publicURL string
	var ts tailscaleFlags
	var openSignup bool
	cmd := &cobra.Command{
		Use:   "server",
		Short: "Run a hub: accounts, and the environments (machines running AgentBox) that connect to it",
		Long: `Runs a hub, which puts every environment you connect in one place: the app,
the CLI (agentbox login, --env) and a browser reach any of them through it.
Environments connect out to the hub with agentbox remote connect.

With no flags it keeps its data in SQLite, in AgentBox's data directory. The
first time it starts it prints a setup link: whoever opens it first becomes
the hub's owner, and approves every other device and environment that asks
to connect. agentbox-hub link prints another.

The hub serves plain HTTP. On a public machine, keep it on 127.0.0.1 and put
a TLS proxy such as Caddy in front of it, and give it its address with --url.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			store, err := openStore(ctx, db)
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			publicURL = strings.TrimRight(cmp.Or(publicURL, os.Getenv("AGENTBOX_HUB_URL")), "/")
			var files fs.FS
			if web != "" {
				if _, err := os.Stat(filepath.Join(web, "index.html")); err != nil {
					return fmt.Errorf("%s has no index.html: build the web app in an AgentBox checkout with npm --prefix desktop run build", web)
				}
				files = os.DirFS(web)
			}
			s := &hub.Server{Store: store, Log: cmd.ErrOrStderr(), Web: files, Version: version, URL: publicURL}
			if s.Cloudflare, s.Tailscale, err = transportConfig(ts, cfZone); err != nil {
				return err
			}
			if s.Cloudflare != nil || s.Tailscale != nil {
				go s.CleanTunnelsEvery(ctx, time.Hour)
			}
			srv := &http.Server{Addr: listen, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
			go func() {
				<-ctx.Done()
				s.Close()
				shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = srv.Shutdown(shutdown)
			}()
			out := cmd.ErrOrStderr()
			_, _ = fmt.Fprintf(out, "AgentBox hub %s listening on http://%s, with its data in %s\n", version, listen, databaseKind(hubDatabase(db)))
			switch {
			case s.Tailscale != nil:
				how := "the auth key in $AGENTBOX_HUB_TAILSCALE_AUTHKEY"
				if s.Tailscale.ClientID != "" {
					how = "keys from the OAuth client " + s.Tailscale.ClientID
				}
				_, _ = fmt.Fprintf(out, "Environments join the tailnet %s as %s, with %s: only its devices reach them\n", cmp.Or(s.Tailscale.Tailnet, "-"), cmp.Or(s.Tailscale.Tag, hub.DefaultTailscaleTag), how)
			case s.Cloudflare != nil:
				_, _ = fmt.Fprintf(out, "Environments' phones get addresses in the Cloudflare zone %s\n", s.Cloudflare.ZoneID)
			default:
				_, _ = fmt.Fprintln(out, "Environments' phones get no addresses from this hub: see --cloudflare-zone")
			}
			ln, err := net.Listen("tcp", listen)
			if err != nil {
				return err
			}
			base := cmp.Or(publicURL, addressOf(listen))
			_, _ = fmt.Fprintf(out, "Open %s/hub/ to see its environments and devices\n", base)
			if n, err := store.CountUsers(ctx); err == nil && n == 0 {
				link, _, err := hub.NewSetupLink(ctx, store, base)
				if err != nil {
					return err
				}
				printSetupLink(out, link, true)
			}
			if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			return nil
		},
	}
	f := cmd.PersistentFlags()
	f.StringVar(&db, "db", "", "the hub's database: postgres://… or sqlite:<path> (default: $AGENTBOX_HUB_DB, or SQLite in AgentBox's data directory)")
	cmd.Flags().StringVar(&listen, "listen", "127.0.0.1:8080", "the address to serve on")
	cmd.Flags().StringVar(&web, "web", "", "a directory with the web app to serve at /")
	f.StringVar(&publicURL, "url", "", "the hub's address as others reach it, like https://hub.example.com, for the links it prints and makes (default: $AGENTBOX_HUB_URL, or the address it listens on)")
	cmd.Flags().BoolVar(&openSignup, "open-signup", false, "")
	_ = cmd.Flags().MarkDeprecated("open-signup", "there are no sign-ups: devices connect by being approved, and the flag does nothing")
	cmd.Flags().StringVar(&cfZone, "cloudflare-zone", "", "the ID of the Cloudflare zone environments' phone addresses are made in, with an API token in $AGENTBOX_HUB_CLOUDFLARE_TOKEN or the file $AGENTBOX_HUB_CLOUDFLARE_TOKEN_FILE (default: $AGENTBOX_HUB_CLOUDFLARE_ZONE; none: no addresses)")
	cmd.Flags().StringVar(&ts.transport, "transport", "", "how environments' devices reach them directly: cloudflare, a tunnel in --cloudflare-zone, or tailscale, a node in --tailscale-tailnet (default: $AGENTBOX_HUB_TRANSPORT, or cloudflare)")
	cmd.Flags().StringVar(&ts.tailnet, "tailscale-tailnet", "", "with --transport tailscale, the tailnet environments join, as its admin console names it (default: $AGENTBOX_HUB_TAILSCALE_TAILNET, or \"-\", the key's own)")
	cmd.Flags().StringVar(&ts.tag, "tailscale-tag", "", "with --transport tailscale, the tag environments' nodes get (default: $AGENTBOX_HUB_TAILSCALE_TAG, or "+hub.DefaultTailscaleTag+")")
	cmd.Flags().StringVar(&ts.clientID, "tailscale-oauth-client", "", "with --transport tailscale, the ID of an OAuth client that makes each environment a key, with its secret in $AGENTBOX_HUB_TAILSCALE_OAUTH_SECRET or the file $AGENTBOX_HUB_TAILSCALE_OAUTH_SECRET_FILE (default: $AGENTBOX_HUB_TAILSCALE_OAUTH_CLIENT; none: the auth key in $AGENTBOX_HUB_TAILSCALE_AUTHKEY or the file $AGENTBOX_HUB_TAILSCALE_AUTHKEY_FILE)")
	cmd.AddCommand(newServerUserCmd(&db), newLinkCmd(&db, &publicURL))
	return cmd
}

func openStore(ctx context.Context, db string) (*hub.SQLStore, error) {
	dsn := hubDatabase(db)
	if path, ok := strings.CutPrefix(dsn, "sqlite:"); ok {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
	}
	return hub.OpenStore(ctx, dsn)
}

// newLinkCmd prints a setup link, for a hub whose first one was lost, or
// whose owner lost every device.
func newLinkCmd(db, publicURL *string) *cobra.Command {
	return &cobra.Command{
		Use:   "link",
		Short: "Print a one-time setup link: it claims a hub with no owner, or signs its owner in once",
		Long: `Prints a one-time setup link, and makes the earlier ones stop working.

On a hub with no owner, whoever opens it first becomes the owner. On a hub
with one, it signs the owner in once, within 15 minutes: for when every
device they had is gone. Run it on the hub's machine, with the hub's --db.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := openStore(cmd.Context(), *db)
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			base := cmp.Or(*publicURL, os.Getenv("AGENTBOX_HUB_URL"), "http://localhost:8080")
			link, claim, err := hub.NewSetupLink(cmd.Context(), store, base)
			if err != nil {
				return err
			}
			printSetupLink(cmd.OutOrStdout(), link, claim)
			if *publicURL == "" && os.Getenv("AGENTBOX_HUB_URL") == "" {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "(Not at localhost:8080? Give the hub's address with --url, or change it in the link: everything after # is what counts.)")
			}
			return nil
		},
	}
}

func printSetupLink(out io.Writer, link string, claim bool) {
	if claim {
		_, _ = fmt.Fprintf(out, "\nThis hub has no owner yet. Whoever opens this link first becomes its owner:\n\n  %s\n\n", link)
	} else {
		_, _ = fmt.Fprintf(out, "\nThis link signs the hub's owner in, once, in the next 15 minutes:\n\n  %s\n\n", link)
	}
	if rows, err := hub.QR(link); err == nil {
		_, _ = fmt.Fprint(out, hub.TerminalQR(rows))
	}
	_, _ = fmt.Fprintln(out, "\nIt works once, and a newer one replaces it. Paste it into the AgentBox app's “Connect to a hub”, or open it in a browser.")
}

// addressOf is where a hub listening on listen is reached from: the address
// itself, or this machine's LAN address when it listens on all of them.
func addressOf(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "http://" + listen
	}
	switch host {
	case "", "0.0.0.0", "::":
		host = "localhost"
		if ip := lanAddress(); ip != "" {
			host = ip
		}
	case "127.0.0.1", "::1":
		host = "localhost"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// lanAddress is this machine's first private IPv4 address, or "".
func lanAddress() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && n.IP.IsPrivate() {
			return n.IP.String()
		}
	}
	return ""
}

// tailscaleFlags are --transport and the --tailscale-* flags.
type tailscaleFlags struct {
	transport, tailnet, tag, clientID string
}

// transportConfig is how environments' devices reach them: Cloudflare
// Tunnels (the default), or the hub's tailnet with --transport tailscale. A
// hub on Tailscale keeps its Cloudflare zone, when it still has one, only to
// remove the tunnels it made before.
func transportConfig(f tailscaleFlags, cfZone string) (*hub.Cloudflare, *hub.Tailscale, error) {
	transport := strings.ToLower(cmp.Or(f.transport, os.Getenv("AGENTBOX_HUB_TRANSPORT"), "cloudflare"))
	if transport != "cloudflare" && transport != "tailscale" {
		return nil, nil, fmt.Errorf("--transport %s isn't one: use cloudflare or tailscale", transport)
	}
	cf, err := cloudflareConfig(cfZone)
	if err != nil || transport == "cloudflare" {
		return cf, nil, err
	}
	ts, err := tailscaleConfig(f)
	if err != nil {
		return nil, nil, err
	}
	return cf, ts, nil
}

// tailscaleConfig is the tailnet the hub puts environments in. Its secrets,
// an auth key or an OAuth client's secret, come from the environment or a
// file, never from a flag, and are never printed.
func tailscaleConfig(f tailscaleFlags) (*hub.Tailscale, error) {
	ts := &hub.Tailscale{
		Tailnet:  cmp.Or(f.tailnet, os.Getenv("AGENTBOX_HUB_TAILSCALE_TAILNET")),
		Tag:      cmp.Or(f.tag, os.Getenv("AGENTBOX_HUB_TAILSCALE_TAG")),
		ClientID: cmp.Or(f.clientID, os.Getenv("AGENTBOX_HUB_TAILSCALE_OAUTH_CLIENT")),
	}
	var err error
	if ts.AuthKey, err = secret("AGENTBOX_HUB_TAILSCALE_AUTHKEY", "the Tailscale auth key"); err != nil {
		return nil, err
	}
	if ts.ClientSecret, err = secret("AGENTBOX_HUB_TAILSCALE_OAUTH_SECRET", "the Tailscale OAuth client's secret"); err != nil {
		return nil, err
	}
	if ts.Tag != "" && !strings.HasPrefix(ts.Tag, "tag:") {
		return nil, fmt.Errorf("--tailscale-tag %s isn't a tag: tags start with tag:, like %s", ts.Tag, hub.DefaultTailscaleTag)
	}
	switch {
	case ts.ClientID != "" && ts.ClientSecret == "":
		return nil, errors.New("a Tailscale OAuth client, but not its secret: set $AGENTBOX_HUB_TAILSCALE_OAUTH_SECRET or $AGENTBOX_HUB_TAILSCALE_OAUTH_SECRET_FILE")
	case ts.ClientID == "" && ts.ClientSecret != "":
		return nil, errors.New("a Tailscale OAuth client's secret, but not its ID: set --tailscale-oauth-client or $AGENTBOX_HUB_TAILSCALE_OAUTH_CLIENT")
	case ts.ClientID != "" && ts.AuthKey != "":
		return nil, errors.New("both a Tailscale auth key and an OAuth client: give the hub one of them")
	case ts.ClientID == "" && ts.AuthKey == "":
		return nil, errors.New("--transport tailscale needs a way to put environments in the tailnet: an auth key in $AGENTBOX_HUB_TAILSCALE_AUTHKEY, or an OAuth client (--tailscale-oauth-client, with its secret in $AGENTBOX_HUB_TAILSCALE_OAUTH_SECRET)")
	case ts.AuthKey != "" && !strings.HasPrefix(ts.AuthKey, "tskey-auth-"):
		return nil, errors.New("$AGENTBOX_HUB_TAILSCALE_AUTHKEY isn't an auth key: those start with tskey-auth-")
	}
	return ts, nil
}

// secret is the value of the variable name, or of the file its _FILE
// variable names (a mounted secret, say).
func secret(name, what string) (string, error) {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v, nil
	}
	file := os.Getenv(name + "_FILE")
	if file == "" {
		return "", nil
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return "", fmt.Errorf("%s: %w", what, err)
	}
	return strings.TrimSpace(string(b)), nil
}

// cloudflareConfig is where the hub makes environments' addresses for phones,
// or nil for none. The API token comes from the environment, or a file such
// as a mounted secret, never from a flag, which other users of the machine
// could read.
func cloudflareConfig(zoneFlag string) (*hub.Cloudflare, error) {
	zone := cmp.Or(zoneFlag, os.Getenv("AGENTBOX_HUB_CLOUDFLARE_ZONE"))
	token := os.Getenv("AGENTBOX_HUB_CLOUDFLARE_TOKEN")
	if file := os.Getenv("AGENTBOX_HUB_CLOUDFLARE_TOKEN_FILE"); file != "" && token == "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("the Cloudflare API token: %w", err)
		}
		token = strings.TrimSpace(string(b))
	}
	switch {
	case zone == "" && token == "":
		return nil, nil
	case zone == "":
		return nil, errors.New("a Cloudflare API token, but no zone to make addresses in: set --cloudflare-zone or $AGENTBOX_HUB_CLOUDFLARE_ZONE to the zone's ID")
	case token == "":
		return nil, errors.New("a Cloudflare zone, but no API token: set $AGENTBOX_HUB_CLOUDFLARE_TOKEN or $AGENTBOX_HUB_CLOUDFLARE_TOKEN_FILE")
	}
	return &hub.Cloudflare{Token: token, ZoneID: zone}, nil
}

func hubDatabase(flag string) string {
	if flag != "" {
		return flag
	}
	if env := os.Getenv("AGENTBOX_HUB_DB"); env != "" {
		return env
	}
	return "sqlite:" + filepath.Join(dataDir(), "hub.db")
}

func databaseKind(dsn string) string {
	if strings.HasPrefix(dsn, "sqlite:") {
		return "SQLite (" + strings.TrimPrefix(dsn, "sqlite:") + ")"
	}
	return "Postgres"
}

func newServerUserCmd(db *string) *cobra.Command {
	cmd := &cobra.Command{Use: "user", Short: "Manage the accounts made before pairing, which sign in with a password"}
	add := &cobra.Command{
		Use:    "add",
		Short:  "Gone: accounts aren't made with a password any more",
		Hidden: true,
		RunE: func(*cobra.Command, []string) error {
			return errors.New("accounts aren't made with a password any more: the hub's owner is whoever opens its setup link first (agentbox-hub link prints one), and every other device is approved from one already connected")
		},
		DisableFlagParsing: true,
	}

	var resetEmail string
	var resetStdin bool
	password := &cobra.Command{
		Use:   "password",
		Short: "Set an account's password, and sign it out everywhere",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := openStore(cmd.Context(), *db)
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			pw, err := readPassword(cmd, resetStdin, true)
			if err != nil {
				return err
			}
			u, err := hub.ResetPassword(cmd.Context(), store, resetEmail, pw)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Set a new password for %s, and ended its sessions: sign in again everywhere\n", u.Email)
			return nil
		},
	}
	password.Flags().StringVar(&resetEmail, "email", "", "the account's email")
	password.Flags().BoolVar(&resetStdin, "password-stdin", false, "read the new password from stdin")
	_ = password.MarkFlagRequired("email")
	cmd.AddCommand(add, password)
	return cmd
}

// readPassword reads a password from stdin, or asks for it (twice when confirm is set).
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

// readSecret asks for something that shouldn't echo.
func readSecret(prompt string) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", errors.New("stdin is not a terminal: use --password-stdin")
	}
	fmt.Fprint(os.Stderr, prompt)
	b, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	return string(b), err
}
