package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
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
It is never stored, on either machine. The daemon is the one that talks to the
cluster, so the server address in it must be one the daemon can reach.`,
	}
	cmd.AddCommand(newClusterAddCmd(a), newClusterSetCmd(a), newClusterListCmd(a), newClusterDoctorCmd(a),
		newClusterRotateCmd(a), newClusterRemoveCmd(a), newClusterDrainCmd(a), newClusterUndrainCmd(a),
		newClusterUpgradeCmd(a))
	return cmd
}

func newClusterAddCmd(a *app) *cobra.Command {
	var (
		kubeconfigPath   string
		namespace        string
		storageClass     string
		image            string
		masterURL        string
		registryName     string
		maxAgents        int
		cpu              string
		memory           string
		agentCPU         string
		agentMemory      string
		workspaceSize    string
		ephemeralStorage string
		labels           []string
		tailnet          string
		capabilities     []string
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

--master-url is where those pods dial this daemon: its MagicDNS name through the
tailnet, like https://monster.example.ts.net, or http://host.k3d.internal:7782
for a k3d cluster on this machine. Nothing starts here without one, and
"cluster set --master-url" adds it later.

  agentbox cluster add k3d --kubeconfig ~/.kube/config --storage-class local-path \
      --image agentbox-agent:dev --registry forgejo --max-agents 4 --tailnet none \
      --master-url http://host.k3d.internal:7782

A daemon with no Incus (packaging/daemon/Dockerfile) has nothing here to run an
agent on, so a project that names no cluster places its new agents with auto,
which is this cluster while it is the only one. "agentbox project cluster
<project> local" still means this machine, which runs no agents.`,
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
				Name:             name,
				Namespace:        namespace,
				StorageClass:     storageClass,
				Image:            image,
				MasterURL:        masterURL,
				Registry:         registryName,
				MaxAgents:        maxAgents,
				CPU:              cpu,
				Memory:           memory,
				AgentCPU:         agentCPU,
				AgentMemory:      agentMemory,
				WorkspaceSize:    workspaceSize,
				EphemeralStorage: ephemeralStorage,
				Labels:           named,
				Tailnet:          tailnet,
				Capabilities:     capabilities,
				Kubeconfig:       string(kubeconfig),
			})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "Added cluster %s.\n", cluster.Name)
			_, _ = fmt.Fprintf(out, "  Namespace:  %s\n", cluster.Namespace)
			if cluster.Registry != "" {
				_, _ = fmt.Fprintf(out, "  Registry:   %s\n", cluster.Registry)
			}
			if cluster.Image != "" {
				_, _ = fmt.Fprintf(out, "  Image:      %s\n", cluster.Image)
			}
			if size := podSizeWords(cluster); size != "" {
				_, _ = fmt.Fprintf(out, "  Agent size: %s\n", size)
			}
			if cluster.MasterURL != "" {
				_, _ = fmt.Fprintf(out, "  Master URL: %s\n", cluster.MasterURL)
			}
			_, _ = fmt.Fprintf(out, "  Tailnet:    %s\n", cluster.Tailnet)
			if cluster.MasterURL == "" {
				_, _ = fmt.Fprintf(out, "\nNo master URL yet: no agent can start here until you set one with:\n  agentbox cluster set %s --master-url <url>\n", cluster.Name)
			}
			// The first cluster of a control plane is where the projects that
			// name none start placing their agents: a change worth saying once,
			// at the add that makes it.
			if !runsAgentsHere(cmd, c) {
				if clusters, err := c.Clusters(cmd.Context()); err == nil && len(clusters) == 1 {
					_, _ = fmt.Fprintf(out, "\nWith no Incus here, a project that names no cluster now starts its new agents in %s (auto).\n", cluster.Name)
					_, _ = fmt.Fprintf(out, "agentbox project cluster <project> local still asks for this machine, which runs no agents.\n")
				}
			}
			_, _ = fmt.Fprintf(out, "\nCheck it can run agents: agentbox cluster doctor %s\n", cluster.Name)
			return nil
		},
	}
	cmd.Flags().StringVar(&kubeconfigPath, "kubeconfig", "", "the admin kubeconfig to apply AgentBox's access with (~/.kube/config)")
	cmd.Flags().StringVar(&namespace, "namespace", "agentbox", "the namespace agents run in")
	cmd.Flags().StringVar(&storageClass, "storage-class", "", "the storage class an agent's workspace asks for (the cluster's default)")
	cmd.Flags().StringVar(&image, "image", "", "the agent image under the registry, like agentbox-agent:dev")
	cmd.Flags().StringVar(&masterURL, "master-url", "", "where the cluster's pods reach this daemon (https://<machine>.<tailnet>.ts.net, or http://host.k3d.internal:7782 for k3d)")
	cmd.Flags().StringVar(&registryName, "registry", "", "the registry its images come from (agentbox registry add)")
	cmd.Flags().IntVar(&maxAgents, "max-agents", 0, "the most agents to run here at once (0 is as many as the nodes allow)")
	cmd.Flags().StringVar(&cpu, "cpu", "", "the CPU budget for agents, as a Kubernetes quantity (2, 500m)")
	cmd.Flags().StringVar(&memory, "memory", "", "the memory budget for agents, as a Kubernetes quantity (8Gi)")
	cmd.Flags().StringVar(&agentCPU, "agent-cpu", "", "what one agent's pod asks for, as a Kubernetes quantity (2, 500m)")
	cmd.Flags().StringVar(&agentMemory, "agent-memory", "", "what one agent's pod asks for, as a Kubernetes quantity (8Gi)")
	cmd.Flags().StringVar(&workspaceSize, "workspace-size", "", "an agent's workspace claim, as a Kubernetes quantity (20Gi)")
	cmd.Flags().StringVar(&ephemeralStorage, "ephemeral-storage", "", "the local disk one pod may fill outside its claim (10Gi)")
	cmd.Flags().StringArrayVar(&labels, "label", nil, "a node label agents are placed on, as name=value (repeatable)")
	cmd.Flags().StringVar(&tailnet, "tailnet", "none", "how pods reach this machine: operator, sidecar or none")
	cmd.Flags().StringArrayVar(&capabilities, "capability", nil, "what agents here may ask for: kvm, gpu, privileged, csi-snapshots (repeatable)")
	return cmd
}

func newClusterSetCmd(a *app) *cobra.Command {
	var (
		masterURL        string
		image            string
		storageClass     string
		registryName     string
		maxAgents        int
		cpu              string
		memory           string
		agentCPU         string
		agentMemory      string
		workspaceSize    string
		ephemeralStorage string
		labels           []string
		tailnet          string
		capabilities     []string
	)
	cmd := &cobra.Command{
		Use:   "set NAME",
		Short: "Change a cluster's master address, image, registry, sizes and budget",
		Long: `Changes a cluster that is already added. Only the flags you pass are written: the
rest stays as the cluster has it.

--master-url is where this cluster's pods dial this daemon: its MagicDNS name
through the tailnet, like https://monster.example.ts.net, or
http://host.k3d.internal:7782 for a k3d cluster on this machine. A cluster with
none is one the doctor fails and no agent starts in, because a pod that can't
reach this machine can't be driven.

  agentbox cluster set k3d --master-url http://host.k3d.internal:7782`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			name := args[0]
			// The request replaces the whole config, so it starts as the
			// cluster is and only what was asked for moves.
			cluster, err := c.Cluster(ctx, name)
			if err != nil {
				return err
			}
			cfg := api.ClusterConfig{
				StorageClass:     cluster.StorageClass,
				Image:            cluster.Image,
				MasterURL:        cluster.MasterURL,
				Registry:         cluster.Registry,
				MaxAgents:        cluster.MaxAgents,
				CPU:              cluster.CPU,
				Memory:           cluster.Memory,
				AgentCPU:         cluster.AgentCPU,
				AgentMemory:      cluster.AgentMemory,
				WorkspaceSize:    cluster.WorkspaceSize,
				EphemeralStorage: cluster.EphemeralStorage,
				Labels:           cluster.Labels,
				Tailnet:          cluster.Tailnet,
				Capabilities:     cluster.Capabilities,
			}
			changed := cmd.Flags().Changed
			if changed("master-url") {
				cfg.MasterURL = masterURL
			}
			if changed("image") {
				cfg.Image = image
			}
			if changed("storage-class") {
				cfg.StorageClass = storageClass
			}
			if changed("registry") {
				cfg.Registry = registryName
			}
			if changed("max-agents") {
				cfg.MaxAgents = maxAgents
			}
			if changed("cpu") {
				cfg.CPU = cpu
			}
			if changed("memory") {
				cfg.Memory = memory
			}
			if changed("agent-cpu") {
				cfg.AgentCPU = agentCPU
			}
			if changed("agent-memory") {
				cfg.AgentMemory = agentMemory
			}
			if changed("workspace-size") {
				cfg.WorkspaceSize = workspaceSize
			}
			if changed("ephemeral-storage") {
				cfg.EphemeralStorage = ephemeralStorage
			}
			if changed("label") {
				named, err := parseKV(labels, "--label")
				if err != nil {
					return err
				}
				cfg.Labels = named
			}
			if changed("tailnet") {
				cfg.Tailnet = tailnet
			}
			if changed("capability") {
				cfg.Capabilities = capabilities
			}
			updated, err := c.SetClusterConfig(ctx, name, cfg)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "Updated cluster %s.\n", updated.Name)
			if updated.MasterURL == "" {
				_, _ = fmt.Fprintf(out, "\nNo master URL: no agent can start here until you set one with:\n  agentbox cluster set %s --master-url <url>\n", updated.Name)
				return nil
			}
			_, _ = fmt.Fprintf(out, "  Master URL: %s\n", updated.MasterURL)
			return nil
		},
	}
	cmd.Flags().StringVar(&masterURL, "master-url", "", "where the cluster's pods reach this daemon")
	cmd.Flags().StringVar(&image, "image", "", "the agent image under the registry, like agentbox-agent:dev")
	cmd.Flags().StringVar(&storageClass, "storage-class", "", "the storage class an agent's workspace asks for (the cluster's default)")
	cmd.Flags().StringVar(&registryName, "registry", "", "the registry its images come from (agentbox registry add)")
	cmd.Flags().IntVar(&maxAgents, "max-agents", 0, "the most agents to run here at once (0 is as many as the nodes allow)")
	cmd.Flags().StringVar(&cpu, "cpu", "", "the CPU budget for agents, as a Kubernetes quantity (2, 500m)")
	cmd.Flags().StringVar(&memory, "memory", "", "the memory budget for agents, as a Kubernetes quantity (8Gi)")
	cmd.Flags().StringVar(&agentCPU, "agent-cpu", "", "what one agent's pod asks for, as a Kubernetes quantity (2, 500m)")
	cmd.Flags().StringVar(&agentMemory, "agent-memory", "", "what one agent's pod asks for, as a Kubernetes quantity (8Gi)")
	cmd.Flags().StringVar(&workspaceSize, "workspace-size", "", "an agent's workspace claim, as a Kubernetes quantity (20Gi)")
	cmd.Flags().StringVar(&ephemeralStorage, "ephemeral-storage", "", "the local disk one pod may fill outside its claim (10Gi)")
	cmd.Flags().StringArrayVar(&labels, "label", nil, "a node label agents are placed on, as name=value (repeatable)")
	cmd.Flags().StringVar(&tailnet, "tailnet", "", "how pods reach this machine: operator, sidecar or none")
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
				// Reads correctly for a control plane too: the daemon reports
				// this machine as its backend until a cluster exists
				// (setupBackends in internal/daemon/setupbackends.go), so there
				// is nothing here to tell a cluster-only installation by.
				_, _ = fmt.Fprintf(out, "No clusters: agents run on this machine. Add one with: agentbox cluster add k3d\n")
				return nil
			}
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "NAME\tSTATE\tNAMESPACE\tREGISTRY\tIMAGE\tMASTER URL\tAGENTS\tFREE\tLAST SEEN\tERROR")
			for _, cluster := range clusters {
				lastSeen := "never"
				if cluster.LastSeen != nil {
					lastSeen = ago(*cluster.LastSeen)
				}
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", cluster.Name, clusterStateWord(cluster), cluster.Namespace,
					orDash(cluster.Registry), orDash(shortImage(cluster.Image)), orDash(cluster.MasterURL),
					clusterAgentsWord(cluster), freeWord(cluster), lastSeen, orDash(cluster.LastError))
			}
			return w.Flush()
		},
	}
}

// runsAgentsHere says whether this installation runs agents on this machine. A
// daemon with no Incus is a control plane: its agents are pods in its clusters,
// so nothing runs here and no project can ask for it. A daemon that can't
// answer counts as one that does, which is how it read before Backends.
func runsAgentsHere(cmd *cobra.Command, c *api.Client) bool {
	status, err := c.Setup(cmd.Context())
	if err != nil || len(status.Backends) == 0 {
		return true
	}
	return slices.Contains(status.Backends, api.SetupBackendHost)
}

func newClusterUndrainCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "undrain NAME",
		Short: "Let a draining cluster take new agents again",
		Long: `Clears a cluster's drain: admission takes it into account again, and the next
create that names it, or the queue's next pass, may place an agent there.

The agents the drain stopped stay stopped until they are started, and the ones it
kept running are untouched.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			cluster, err := c.UndrainCluster(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "%s takes new agents again.\n", cluster.Name)
			printClusterState(out, cluster)
			return nil
		},
	}
}

func newClusterDrainCmd(a *app) *cobra.Command {
	var (
		moveTo      string
		keepRunning bool
	)
	cmd := &cobra.Command{
		Use:   "drain NAME",
		Short: "Stop a cluster taking new agents, and move or stop the ones it has",
		Long: `Drains a cluster: admission refuses it from then on, and no new agent starts
there.

By default every running agent on it is stopped, keeping its claim, so starting it
again brings it back. --move-to moves each one to another cluster instead, one at
a time. --keep-running only marks the cluster: its agents keep running until they
are stopped or moved by hand.

Draining stops new agents, not the ones the cluster has. A drain that fails
partway leaves the cluster draining and the agents it had not reached as they
were; run it again, or "agentbox cluster undrain" to let it take agents again.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if moveTo != "" && keepRunning {
				return errors.New("--move-to and --keep-running can't be used together: pass one of them, or neither to stop every agent on the cluster")
			}
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			name := args[0]
			j, err := c.DrainCluster(cmd.Context(), name, api.DrainClusterRequest{MoveTo: moveTo, KeepRunning: keepRunning})
			if err != nil {
				return err
			}
			if _, err := waitJob(cmd, c, j); err != nil {
				return fmt.Errorf("%w; %s is still draining, so fix what the job says and run agentbox cluster undrain %s to let it take agents again", err, name, name)
			}
			cluster, err := c.Cluster(cmd.Context(), name)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "%s is draining: it takes no new agents.\n", cluster.Name)
			printClusterState(out, cluster)
			_, _ = fmt.Fprintf(out, "Let it take agents again with: agentbox cluster undrain %s\n", cluster.Name)
			return nil
		},
	}
	cmd.Flags().StringVar(&moveTo, "move-to", "", "move every running agent here to that cluster")
	cmd.Flags().BoolVar(&keepRunning, "keep-running", false, "only mark the cluster: leave its agents running")
	return cmd
}

func newClusterUpgradeCmd(a *app) *cobra.Command {
	var (
		digest   string
		rollback bool
	)
	cmd := &cobra.Command{
		Use:   "upgrade NAME",
		Short: "Run a cluster's image at a new digest, recreating its pods one at a time",
		Long: `Changes the image a cluster's pods run and recreates them one at a time, keeping
the image each one had until it is replaced.

The digest is a full sha256:... a registry reported. The image name keeps the tag
the cluster was added with, so "agentbox-agent:dev" becomes
"agentbox-agent:dev@sha256:...", and only the digest changes from then on.

The image the cluster ran before is kept for --rollback, which swaps the two: a
second --rollback goes forward to the image it just left. An agent that is stopped
has no pod to replace and runs the new image when it starts. A failure stops the
rollout at the agent it names and says how to roll back; the pods already
recreated keep the new image either way.

A pod restored from a snapshot keeps the image the snapshot was taken with until
it is recreated or upgraded.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch {
			case digest != "" && rollback:
				return errors.New("--digest and --rollback can't be used together: pass one of them")
			case digest == "" && !rollback:
				return errors.New("pass --digest sha256:... to upgrade the cluster, or --rollback to go back to the image it ran before")
			}
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			name := args[0]
			j, err := c.UpgradeCluster(cmd.Context(), name, api.UpgradeClusterRequest{Digest: digest, Rollback: rollback})
			if err != nil {
				return err
			}
			if _, err := waitJob(cmd, c, j); err != nil {
				return err
			}
			cluster, err := c.Cluster(cmd.Context(), name)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if rollback {
				_, _ = fmt.Fprintf(out, "Rolled %s back to %s.\n", cluster.Name, cluster.Image)
			} else {
				_, _ = fmt.Fprintf(out, "Upgraded %s to %s.\n", cluster.Name, cluster.Image)
			}
			if cluster.PreviousImage == "" {
				return nil
			}
			_, _ = fmt.Fprintf(out, "Previous image: %s\n", cluster.PreviousImage)
			_, _ = fmt.Fprintf(out, "\nRoll back with: agentbox cluster upgrade %s --rollback\n", cluster.Name)
			return nil
		},
	}
	cmd.Flags().StringVar(&digest, "digest", "", "the full sha256:... digest to run")
	cmd.Flags().BoolVar(&rollback, "rollback", false, "go back to the image before the last upgrade")
	return cmd
}

// printClusterState is the little a drain, an undrain or an upgrade says about
// the cluster it changed: the image it runs, and the one it ran before when
// there is one.
func printClusterState(out io.Writer, cluster api.Cluster) {
	_, _ = fmt.Fprintf(out, "  Image: %s\n", orDash(cluster.Image))
	if cluster.PreviousImage != "" {
		_, _ = fmt.Fprintf(out, "  Previous image: %s\n", cluster.PreviousImage)
	}
}

func newClusterDoctorCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor NAME",
		Short: "Check that a cluster can run agents, with the access the daemon has",
		Long: `Checks a cluster with the ServiceAccount's own token, which is what the daemon
really has: whether it knows where this machine is from the cluster, whether the
API answers, whether its access works, whether its nodes are ready and how much
room they have, whether the storage class exists and provisions, whether the
registry's login reached the namespace, and whether the image pulls.

A master address that is plain http is reported as unencrypted, which is only
right for a local test cluster: any other cluster reaches this machine through the
tailnet's certificate.

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
		Short: "Apply AgentBox's access again and give a cluster a new token",
		Long: `Applies AgentBox's namespace, ServiceAccount, Role and binding again, then
deletes the cluster's token Secret, makes a new one, and keeps the kubeconfig for
it, sealed. Anything holding the old token stops reaching the cluster.

The access is applied again because a release that adds a permission reaches an
existing cluster this way: "cluster add" refuses a name that already exists, and
a cluster with agents on it cannot be removed and added back.

Rotating needs an admin kubeconfig: applying the Role and deleting the token
Secret are not things the token itself is for, and the old token may be the thing
that stopped working. Agents running there keep running.`,
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

// podSizeWords is what one agent's pod asks for, and the claim it gets, in the
// order a person reads them: "2 CPU, 8Gi memory, 20Gi workspace".
func podSizeWords(c api.Cluster) string {
	var parts []string
	if c.AgentCPU != "" {
		parts = append(parts, c.AgentCPU+" CPU")
	}
	if c.AgentMemory != "" {
		parts = append(parts, c.AgentMemory+" memory")
	}
	if c.WorkspaceSize != "" {
		parts = append(parts, c.WorkspaceSize+" workspace")
	}
	return strings.Join(parts, ", ")
}

// clusterAgentsWord is how many agents a cluster has against its ceiling, as "2 of 4"
// or "2" when there is none.
func clusterAgentsWord(c api.Cluster) string {
	if c.Capacity == nil {
		// The daemon hasn't looked yet, or this cluster was just added.
		if c.MaxAgents > 0 {
			return fmt.Sprintf("? of %d", c.MaxAgents)
		}
		return "-"
	}
	if c.MaxAgents > 0 {
		return fmt.Sprintf("%d of %d", c.Capacity.Running, c.MaxAgents)
	}
	return strconv.Itoa(c.Capacity.Running)
}

// clusterStateWord is a cluster's state in the list: DRAINING while it takes no
// new agents, and nothing in particular otherwise.
func clusterStateWord(c api.Cluster) string {
	if c.Draining {
		return "DRAINING"
	}
	return "-"
}

// shortImage is an image with its digest cut to what a table can show: the name
// and tag stay, and "sha256:" keeps eight characters of the digest, which is
// enough to tell one upgrade from another. The whole image is what an upgrade
// prints, because that is the one to keep for a rollback.
func shortImage(image string) string {
	name, digest, ok := strings.Cut(image, "@")
	if !ok {
		return image
	}
	if hex, ok := strings.CutPrefix(digest, "sha256:"); ok && len(hex) > 8 {
		digest = "sha256:" + hex[:8]
	}
	return name + "@" + digest
}

// freeWord is how many more agents fit now, "no limit" when there is no ceiling
// to count, and "-" until the daemon has looked and been able to answer. A
// draining cluster takes none, whatever room it still has.
func freeWord(c api.Cluster) string {
	if c.Draining {
		return "0"
	}
	if c.Capacity == nil || c.Capacity.Error != "" {
		return "-"
	}
	if c.Capacity.Agents < 0 {
		return "no limit"
	}
	return strconv.Itoa(c.Capacity.Agents)
}
