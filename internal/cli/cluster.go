package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
)

func newClusterCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster",
		Short: "Run agents in a Kubernetes cluster, from this machine",
		Long: `Where AgentBox's agents run.

An agent is a machine, a worktree and a branch. On this machine its machine is an
Incus instance; in a cluster it is a pod of its own with a claim for its
workspace, and this daemon still drives it: the same chat, terminal, git and
tools, over a connection the pod opens to here.

Adding a cluster applies what AgentBox needs in its namespace with the admin
kubeconfig you already use for kubectl: the namespace, a ServiceAccount, a Role
that can run agents' pods and nothing else, and a read-only ClusterRole for the
nodes and storage classes placement reads. It then makes that ServiceAccount a
long-lived token, writes the kubeconfig for it, seals it, and drops the admin
one. From then on this machine holds only the ServiceAccount's token.

The admin kubeconfig is read here and sent to the daemon for that one request.
It is never stored, on either machine.`,
	}
	cmd.AddCommand(newClusterAddCmd(a), newClusterListCmd(a), newClusterDoctorCmd(a),
		newClusterRotateCmd(a), newClusterRemoveCmd(a))
	return cmd
}

func newClusterAddCmd(a *app) *cobra.Command {
	var (
		kubeconfigPath string
		namespace      string
		storageClass   string
		image          string
		registryName   string
		maxAgents      int
		cpu            string
		memory         string
		labels         []string
		tailnet        string
		capabilities   []string
	)
	cmd := &cobra.Command{
		Use:   "add NAME",
		Short: "Add a cluster, applying AgentBox's own access to it",
		Long: `Adds a cluster and keeps a token of its own for it.

The admin kubeconfig is read from --kubeconfig, else from the first path in
KUBECONFIG, else from ~/.kube/config. It is used for this one command: what is
kept is the ServiceAccount's token, scoped to the namespace.

--registry names a registry added with "agentbox registry add". Its login is
written into the namespace as the pull secret pods read, and its url is the
prefix the image is pulled from. Without one, a cluster pulls whatever image is
already imported into it: on k3d, "k3d image import".

--tailnet says how this cluster's pods reach this machine and the private git
remote: operator (the Tailscale operator's egress proxy), sidecar (a sidecar of
their own), or none, when both are addresses the cluster already reaches.

  agentbox cluster add k3d --kubeconfig ~/.kube/config --storage-class local-path \
      --image agentbox-agent:dev --registry forgejo --max-agents 4 --tailnet none`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			named, err := parseKV(labels, "--label")
			if err != nil {
				return err
			}
			kubeconfig, err := readKubeconfig(kubeconfigPath)
			if err != nil {
				return err
			}
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			cluster, err := c.AddCluster(cmd.Context(), api.AddClusterRequest{
				Name:         name,
				Namespace:    namespace,
				StorageClass: storageClass,
				Image:        image,
				Registry:     registryName,
				MaxAgents:    maxAgents,
				CPU:          cpu,
				Memory:       memory,
				Labels:       named,
				Tailnet:      tailnet,
				Capabilities: capabilities,
				Kubeconfig:   string(kubeconfig),
			})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "Added cluster %s.\n", cluster.Name)
			_, _ = fmt.Fprintf(out, "  Namespace: %s\n", cluster.Namespace)
			if cluster.Registry != "" {
				_, _ = fmt.Fprintf(out, "  Registry:  %s\n", cluster.Registry)
			}
			if cluster.Image != "" {
				_, _ = fmt.Fprintf(out, "  Image:     %s\n", cluster.Image)
			}
			_, _ = fmt.Fprintf(out, "  Tailnet:   %s\n", cluster.Tailnet)
			_, _ = fmt.Fprintf(out, "\nCheck it can run agents: agentbox cluster doctor %s\n", cluster.Name)
			return nil
		},
	}
	cmd.Flags().StringVar(&kubeconfigPath, "kubeconfig", "", "the admin kubeconfig to apply AgentBox's access with (~/.kube/config)")
	cmd.Flags().StringVar(&namespace, "namespace", "agentbox", "the namespace agents run in")
	cmd.Flags().StringVar(&storageClass, "storage-class", "", "the storage class an agent's workspace asks for (the cluster's default)")
	cmd.Flags().StringVar(&image, "image", "", "the agent image under the registry, like agentbox-agent:dev")
	cmd.Flags().StringVar(&registryName, "registry", "", "the registry its images come from (agentbox registry add)")
	cmd.Flags().IntVar(&maxAgents, "max-agents", 0, "the most agents to run here at once (0 is as many as the nodes allow)")
	cmd.Flags().StringVar(&cpu, "cpu", "", "the CPU budget for agents, as a Kubernetes quantity (2, 500m)")
	cmd.Flags().StringVar(&memory, "memory", "", "the memory budget for agents, as a Kubernetes quantity (8Gi)")
	cmd.Flags().StringArrayVar(&labels, "label", nil, "a node label agents are placed on, as name=value (repeatable)")
	cmd.Flags().StringVar(&tailnet, "tailnet", "none", "how pods reach this machine: operator, sidecar or none")
	cmd.Flags().StringArrayVar(&capabilities, "capability", nil, "what agents here may ask for: kvm, gpu, privileged, csi-snapshots (repeatable)")
	return cmd
}

func newClusterListCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Show the clusters agents can run in",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			clusters, err := c.Clusters(cmd.Context())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(clusters) == 0 {
				_, _ = fmt.Fprintf(out, "No clusters: agents run on this machine. Add one with: agentbox cluster add k3d\n")
				return nil
			}
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "NAME\tNAMESPACE\tREGISTRY\tIMAGE\tAGENTS\tLAST SEEN\tERROR")
			for _, cluster := range clusters {
				lastSeen := "never"
				if cluster.LastSeen != nil {
					lastSeen = ago(*cluster.LastSeen)
				}
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", cluster.Name, cluster.Namespace,
					orDash(cluster.Registry), orDash(cluster.Image), ceilingWord(cluster.MaxAgents),
					lastSeen, orDash(cluster.LastError))
			}
			return w.Flush()
		},
	}
}

func newClusterDoctorCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor NAME",
		Short: "Check that a cluster can run agents, with the access the daemon has",
		Long: `Checks a cluster with the ServiceAccount's own token, which is what the daemon
really has: whether the API answers, whether its access works, whether its nodes
are ready and how much room they have, whether the storage class exists and
provisions, whether the registry's login reached the namespace, and whether the
image pulls.

It makes and deletes two canaries in the namespace: a 1Gi claim, and a pod of the
configured image. Nothing else is touched.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			name := args[0]
			report, err := c.DoctorCluster(cmd.Context(), name)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if report.Server != "" {
				_, _ = fmt.Fprintf(out, "%s (%s)\n", name, report.Server)
			} else {
				_, _ = fmt.Fprintf(out, "%s\n", name)
			}
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			for _, check := range report.Checks {
				_, _ = fmt.Fprintf(w, "  %s\t%s\t%s\n", check.Name, checkResult(check), check.Detail)
			}
			if err := w.Flush(); err != nil {
				return err
			}
			if report.OK {
				_, _ = fmt.Fprintf(out, "\n%s can run agents.\n", name)
				return nil
			}
			for _, check := range report.Checks {
				if !check.OK && !check.Skipped {
					return fmt.Errorf("%s can't run agents yet: %s: %s", name, check.Name, check.Detail)
				}
			}
			return nil
		},
	}
}

// checkResult is a doctor check's result, in one word.
func checkResult(check api.ClusterCheck) string {
	switch {
	case check.Skipped:
		return "skipped"
	case check.OK:
		return "ok"
	}
	return "FAILED"
}

func newClusterRotateCmd(a *app) *cobra.Command {
	var kubeconfigPath string
	cmd := &cobra.Command{
		Use:   "rotate NAME",
		Short: "Give a cluster a new token, and forget the old one",
		Long: `Deletes the cluster's token Secret, makes a new one, and keeps the kubeconfig for
it, sealed. Anything holding the old token stops reaching the cluster.

Rotating needs an admin kubeconfig: deleting the token Secret is not something
the token itself is for, and the old token may be the thing that stopped working.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kubeconfig, err := readKubeconfig(kubeconfigPath)
			if err != nil {
				return err
			}
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			if _, err := c.RotateCluster(cmd.Context(), args[0], api.RotateClusterRequest{Kubeconfig: string(kubeconfig)}); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Rotated the token for %s; the one it had no longer reaches the cluster.\n", args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&kubeconfigPath, "kubeconfig", "", "the admin kubeconfig to rotate with (~/.kube/config)")
	return cmd
}

func newClusterRemoveCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "remove NAME",
		Short: "Stop using a cluster, leaving what is in it",
		Long: `Forgets a cluster, once no agent runs in it.

What AgentBox applied there stays: the namespace, the ServiceAccount, its Role and
the token Secret. The token is forgotten here, so nothing on this machine reaches
the cluster afterwards, and "agentbox cluster add" applies what is missing again.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			if err := c.RemoveCluster(cmd.Context(), args[0]); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Removed cluster %s. What AgentBox put in it is still there, and its token no longer is here.\n", args[0])
			return nil
		},
	}
}

func newRegistryCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "registry",
		Short: "Say where agents' images are pushed and pulled from",
		Long: `The OCI registry AgentBox pushes its agent image to and a cluster pulls it from.

Which one is configuration, not a choice AgentBox makes for you: a Forgejo's
container registry, a Docker Hub account, a GHCR namespace and a small
registry:2 of your own are all a URL and a login. A registry is private unless
you say --public, because the agent image carries Claude Code and the ACP
adapters, which may not be redistributed.

The login is sealed with the same key as your agents' secrets, and it is written
into a cluster's namespace as its pull secret. The token is read from stdin or
asked for without echoing it; it is never an argument, which would leave it in
your shell history and in ps.`,
	}
	cmd.AddCommand(newRegistryAddCmd(a), newRegistryListCmd(a), newRegistryTestCmd(a),
		newRegistryRotateCmd(a), newRegistryRemoveCmd(a))
	return cmd
}

// registryFlags are the flags add and rotate share: where the registry is, and
// how to sign in to it.
type registryFlags struct {
	url        string
	username   string
	tokenStdin bool
	caPath     string
	insecure   bool
	public     bool
}

func (f *registryFlags) add(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.url, "url", "", "the repository prefix, like hub.example.com/you")
	cmd.Flags().StringVar(&f.username, "username", "", "the login's username (leave it out for a registry that needs none)")
	cmd.Flags().BoolVar(&f.tokenStdin, "token-stdin", false, "read the token from stdin instead of asking for it")
	cmd.Flags().StringVar(&f.caPath, "ca", "", "the registry's PEM certificate, when a private CA signed it")
	cmd.Flags().BoolVar(&f.insecure, "insecure", false, "accept http or a certificate no CA can verify (a local cluster only)")
}

func (f *registryFlags) addPublic(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&f.public, "public", false, "anyone may pull what is pushed here, which the private agent image may not be")
}

// credentials reads the login the flags name: a username and a token, or
// neither. The token is never a flag.
func (f *registryFlags) credentials(cmd *cobra.Command, name string) (username, token string, err error) {
	if f.username == "" && !f.tokenStdin {
		return "", "", nil
	}
	token, err = secretValue(cmd, "the token for "+name, f.tokenStdin)
	if err != nil {
		return "", "", err
	}
	return f.username, token, nil
}

// ca reads the certificate authority the flags name, if any.
func (f *registryFlags) ca() (string, error) {
	if f.caPath == "" {
		return "", nil
	}
	pem, err := os.ReadFile(f.caPath)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", f.caPath, err)
	}
	return string(pem), nil
}

func newRegistryAddCmd(a *app) *cobra.Command {
	var flags registryFlags
	cmd := &cobra.Command{
		Use:   "add NAME",
		Short: "Add a registry agents' images are pushed to",
		Long: `Adds a registry and seals its login.

  agentbox registry add forgejo --url hub.example.com/you --username you
  op read op://work/registry/token | agentbox registry add forgejo --url hub.example.com/you --username you --token-stdin

Without --username it is a registry that needs no login to pull what you push.

It does not test the registry: "agentbox registry test" does, and says whether it
answers and accepts the login.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			username, token, err := flags.credentials(cmd, name)
			if err != nil {
				return err
			}
			ca, err := flags.ca()
			if err != nil {
				return err
			}
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			reg, err := c.AddRegistry(cmd.Context(), api.AddRegistryRequest{
				Name:     name,
				URL:      flags.url,
				Username: username,
				Token:    token,
				Public:   flags.public,
				CA:       ca,
				Insecure: flags.insecure,
			})
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Added registry %s (%s), %s.\n", reg.Name, reg.URL, visibility(reg))
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Test it with: agentbox registry test %s\n", reg.Name)
			return nil
		},
	}
	flags.add(cmd)
	flags.addPublic(cmd)
	return cmd
}

func newRegistryListCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Show the registries agents' images come from",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			registries, err := c.Registries(cmd.Context())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(registries) == 0 {
				_, _ = fmt.Fprintf(out, "No registries. Add one with: agentbox registry add forgejo --url hub.example.com/you\n")
				return nil
			}
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "NAME\tURL\tVISIBILITY\tLOGIN\tLAST SEEN\tERROR")
			for _, reg := range registries {
				lastSeen := "never"
				if reg.LastSeen != nil {
					lastSeen = ago(*reg.LastSeen)
				}
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", reg.Name, reg.URL, visibility(reg),
					orDash(loginWord(reg)), lastSeen, orDash(reg.LastError))
			}
			return w.Flush()
		},
	}
}

// visibility is a registry's visibility in one word, and what it means for the
// agent image.
func visibility(reg api.Registry) string {
	if reg.Public {
		return "public"
	}
	return "private"
}

func loginWord(reg api.Registry) string {
	switch {
	case !reg.HasCredential:
		return "none"
	case reg.HasCA:
		return "sealed, private CA"
	}
	return "sealed"
}

func newRegistryTestCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "test NAME",
		Short: "Check that a registry answers and accepts the stored login",
		Long: `Asks the registry whether it is there and whether the login works, through the
distribution API's own token flow: GET /v2/, the challenge it answers with, and
the same request again with what that produced.

It pushes nothing. What it checks is the address and the credential, before an
image build spends minutes finding out.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			name := args[0]
			result, err := c.TestRegistry(cmd.Context(), name)
			if err != nil {
				return err
			}
			if !result.OK {
				return fmt.Errorf("%s: %s", name, result.Detail)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", name, result.Detail)
			return nil
		},
	}
}

func newRegistryRotateCmd(a *app) *cobra.Command {
	var flags registryFlags
	cmd := &cobra.Command{
		Use:   "rotate NAME",
		Short: "Replace a registry's login, and every pull secret that uses it",
		Long: `Replaces the sealed login and rewrites the pull secret of every cluster that
pulls from this registry, so a token you can no longer see can be replaced
without adding the registry again.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			username, token, err := flags.credentials(cmd, name)
			if err != nil {
				return err
			}
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			if _, err := c.RotateRegistry(cmd.Context(), name, api.RotateRegistryRequest{Username: username, Token: token}); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Rotated the login for %s, and every cluster that pulls from it.\n", name)
			return nil
		},
	}
	cmd.Flags().StringVar(&flags.username, "username", "", "the login's username")
	cmd.Flags().BoolVar(&flags.tokenStdin, "token-stdin", false, "read the token from stdin instead of asking for it")
	return cmd
}

func newRegistryRemoveCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "remove NAME",
		Short: "Forget a registry, once no cluster pulls from it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			if err := c.RemoveRegistry(cmd.Context(), args[0]); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Removed registry %s.\n", args[0])
			return nil
		},
	}
}

// readKubeconfig is the admin kubeconfig a cluster command applies with: the one
// it names, else the first in KUBECONFIG, else kubectl's own.
func readKubeconfig(path string) ([]byte, error) {
	if path == "" {
		path = defaultKubeconfig()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading the admin kubeconfig: %w: pass --kubeconfig, or set KUBECONFIG", err)
	}
	return data, nil
}

func defaultKubeconfig() string {
	if env := os.Getenv("KUBECONFIG"); env != "" {
		first, _, _ := strings.Cut(env, string(os.PathListSeparator))
		return first
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".kube", "config")
	}
	return filepath.Join(home, ".kube", "config")
}

// parseKV is a repeated name=value flag, as a map.
func parseKV(pairs []string, flag string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		name, value, ok := strings.Cut(pair, "=")
		if !ok || name == "" {
			return nil, fmt.Errorf("%s takes name=value, not %q", flag, pair)
		}
		out[name] = value
	}
	return out, nil
}

// ceilingWord is a cluster's ceiling on agents: a number, or none.
func ceilingWord(n int) string {
	if n == 0 {
		return "no limit"
	}
	return strconv.Itoa(n)
}
