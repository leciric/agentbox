package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
)

// tailnetServePath is where the daemon is published by default: a path, not
// the root, because a tailnet serves one thing on 443 and this is rarely it.
const (
	defaultTailnetServePort = 443
	defaultTailnetServePath = "/agentbox"
)

func newTailnetCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tailnet",
		Short: "Serve this machine's daemon to your tailnet, for the app on another machine",
		Long: `Serve this machine's daemon to your tailnet.

The daemon listens on 127.0.0.1:7781 and tailscale serve publishes it as
https://<machine>.<tailnet>.ts.net<path>. Who makes a request is taken from
the Tailscale-User-Login header that serve sets, and only the logins you allow
are let in.

A daemon in AgentBox's own VM can't be published from inside the guest: run
"agentbox tailnet on --publish=false" there, and proxy its port from the host.`,
	}
	var allow string
	var publish bool
	var port int
	var path string
	on := &cobra.Command{
		Use:   "on",
		Short: "Listen for the tailnet, and publish it with tailscale serve",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			enabled := true
			req := api.UpdateTailnetRequest{Enabled: &enabled}
			if cmd.Flags().Changed("allow") {
				logins := splitLogins(allow)
				if len(logins) == 0 {
					return errors.New("--allow needs a Tailscale login, like you@example.com")
				}
				req.Allow = &logins
			}
			st, err := c.UpdateTailnet(cmd.Context(), req)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if !st.Listening {
				return fmt.Errorf("saved, but the daemon couldn't listen on %s: %s", st.Addr, cmp.Or(st.Error, "no reason given"))
			}
			if !publish {
				_, _ = fmt.Fprintf(out, "Listening on %s. Publish it with:\n  %s\n", st.Addr, publishCommand(port, path))
				return nil
			}
			tailscale, err := exec.LookPath("tailscale")
			if err != nil {
				_, _ = fmt.Fprintf(out, "Listening on %s. tailscale isn't on this machine's PATH, so nothing publishes it yet; run on the machine that has it:\n  %s\n", st.Addr, publishCommand(port, path))
				return nil
			}
			if err := runTailscaleServe(cmd.Context(), tailscale, port, path); err != nil {
				return fmt.Errorf("the daemon is listening on %s, but tailscale serve didn't publish it (%w); run:\n  %s", st.Addr, err, publishCommand(port, path))
			}
			url, _ := findTailscaleServe(cmd.Context(), st.Addr)
			_, _ = fmt.Fprintf(out, "Serving the daemon at %s\n", cmp.Or(url.URL, "https://"+tailscaleHostname(cmd.Context(), tailscale)+path))
			_, _ = fmt.Fprintf(out, "Allowed in: %s\n", strings.Join(st.Allow, ", "))
			return nil
		},
	}
	on.Flags().StringVar(&allow, "allow", "", "the Tailscale logins to let in, comma separated, like you@example.com")
	on.Flags().BoolVar(&publish, "publish", true, "publish it with tailscale serve now")
	on.Flags().IntVar(&port, "https", defaultTailnetServePort, "the HTTPS port tailscale serve publishes it on")
	on.Flags().StringVar(&path, "path", defaultTailnetServePath, "the path tailscale serve publishes it under")

	off := &cobra.Command{
		Use:   "off",
		Short: "Stop listening for the tailnet, and stop publishing it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			enabled := false
			st, err := c.UpdateTailnet(cmd.Context(), api.UpdateTailnetRequest{Enabled: &enabled})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "The tailnet listener is off (%s).\n", st.Addr)
			tailscale, err := exec.LookPath("tailscale")
			if err != nil {
				return nil
			}
			if served, ok := findTailscaleServe(cmd.Context(), st.Addr); ok {
				if err := runTailscaleUnserve(cmd.Context(), tailscale, served); err != nil {
					return fmt.Errorf("stopped listening, but tailscale serve still publishes it: %w", err)
				}
				_, _ = fmt.Fprintf(out, "Stopped publishing %s.\n", served.URL)
			}
			return nil
		},
	}

	status := &cobra.Command{
		Use:   "status",
		Short: "Show whether the daemon is served to the tailnet, and to whom",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			st, err := c.TailnetStatus(cmd.Context())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if !st.Enabled {
				_, _ = fmt.Fprintln(out, "The tailnet listener is off. Turn it on with: agentbox tailnet on --allow <login>")
				return nil
			}
			if !st.Listening {
				return fmt.Errorf("the tailnet listener is on, but %s isn't open: %s", st.Addr, cmp.Or(st.Error, "no reason given"))
			}
			_, _ = fmt.Fprintf(out, "Listening on %s\n", st.Addr)
			if served, ok := findTailscaleServe(cmd.Context(), st.Addr); ok {
				_, _ = fmt.Fprintf(out, "Published at %s\n", served.URL)
			} else {
				_, _ = fmt.Fprintln(out, "Nothing publishes it yet: run agentbox tailnet on again, or tailscale serve --bg --https=443 --set-path=/agentbox http://127.0.0.1:7781")
			}
			_, _ = fmt.Fprintf(out, "Allowed in: %s\n", strings.Join(st.Allow, ", "))
			return nil
		},
	}
	cmd.AddCommand(on, off, status)
	return cmd
}

func splitLogins(value string) []string {
	out := []string{}
	for _, part := range strings.Split(value, ",") {
		if login := strings.TrimSpace(part); login != "" {
			out = append(out, login)
		}
	}
	return out
}

func publishCommand(port int, path string) string {
	return fmt.Sprintf("tailscale serve --bg --https=%d --set-path=%s http://127.0.0.1:7781", port, path)
}

func runTailscaleServe(ctx context.Context, tailscale string, port int, path string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	args := []string{"serve", "--bg", fmt.Sprintf("--https=%d", port), "--set-path=" + path, "http://127.0.0.1:7781"}
	out, err := exec.CommandContext(ctx, tailscale, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func runTailscaleUnserve(ctx context.Context, tailscale string, served tailscaleServe) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	args := []string{"serve", fmt.Sprintf("--https=%d", served.Port), "--set-path=" + served.Path, "off"}
	out, err := exec.CommandContext(ctx, tailscale, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// tailscaleServe is one handler tailscale serve runs: where it is published,
// and the URL a client opens.
type tailscaleServe struct {
	URL  string
	Host string
	Port int
	Path string
}

// findTailscaleServe is the handler that proxies to the daemon's address, read
// from tailscale's own configuration: tailscale serve status --json, whose Web
// map is host:port to the handlers it has.
func findTailscaleServe(ctx context.Context, addr string) (tailscaleServe, bool) {
	tailscale, err := exec.LookPath("tailscale")
	if err != nil {
		return tailscaleServe{}, false
	}
	out, err := exec.CommandContext(ctx, tailscale, "serve", "status", "--json").Output()
	if err != nil {
		return tailscaleServe{}, false
	}
	var status struct {
		Web map[string]struct {
			Handlers map[string]struct {
				Proxy string `json:"Proxy"`
			} `json:"Handlers"`
		} `json:"Web"`
	}
	if err := json.Unmarshal(out, &status); err != nil {
		return tailscaleServe{}, false
	}
	for hostPort, entry := range status.Web {
		host, port := splitHostPort(hostPort)
		for path, handler := range entry.Handlers {
			if sameProxy(handler.Proxy, addr) {
				return tailscaleServe{URL: serveURL(host, port, path), Host: host, Port: port, Path: path}, true
			}
		}
	}
	return tailscaleServe{}, false
}

func splitHostPort(hostPort string) (string, int) {
	host, port, err := net.SplitHostPort(hostPort)
	if err != nil {
		return hostPort, defaultTailnetServePort
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return host, defaultTailnetServePort
	}
	return host, n
}

// serveURL is the address a client opens for a handler on host:port at path.
func serveURL(host string, port int, path string) string {
	if port != 443 {
		host = net.JoinHostPort(host, strconv.Itoa(port))
	}
	if path == "/" {
		return "https://" + host
	}
	return "https://" + host + path
}

func sameProxy(proxy, addr string) bool {
	proxy = strings.TrimSuffix(strings.TrimPrefix(proxy, "http://"), "/")
	return strings.EqualFold(proxy, strings.TrimSuffix(addr, "/"))
}

// tailscaleHostname is this machine's MagicDNS name, without its trailing dot.
func tailscaleHostname(ctx context.Context, tailscale string) string {
	out, err := exec.CommandContext(ctx, tailscale, "status", "--json").Output()
	if err != nil {
		return ""
	}
	var status struct {
		Self struct {
			DNSName string `json:"DNSName"`
		} `json:"Self"`
	}
	if err := json.Unmarshal(out, &status); err != nil {
		return ""
	}
	return strings.TrimSuffix(status.Self.DNSName, ".")
}
