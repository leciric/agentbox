package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
)

// newMoveCmd is `agentbox move`: an agent's machine from one Kubernetes cluster
// to another. Its worktree is checkpointed and pushed first, then the machine's
// claim is archived from the source and restored into a new one on the target,
// and the source pod and claim go once the target pod answers.
func newMoveCmd(a *app) *cobra.Command {
	var cluster string
	cmd := &cobra.Command{
		Use:   "move <project/agent> --cluster <name>",
		Short: "Move an agent's machine from one Kubernetes cluster to another",
		Long: `Moves an agent's machine to another cluster. Its worktree is checkpointed and
pushed first, so the branch carries the work even if the machine's archive
fails; then the claim its files live on is archived from the source cluster and
restored into a new claim on the target, and its pod is started there. The
source pod and claim are removed once the target pod answers.

An agent that runs on this host is not moved: its machine is an Incus instance,
and a move is one cluster's pod for another's. A stopped agent is moved too, and
starts on the target.

--cluster auto picks the cluster with the most room, by the same rule a create
uses, and refuses a full or draining one.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			j, err := c.MoveAgent(cmd.Context(), args[0], api.MoveAgentRequest{Cluster: cluster})
			if err != nil {
				return err
			}
			final, err := waitJob(cmd, c, j)
			if err != nil {
				return err
			}
			var result struct {
				Cluster string `json:"cluster"`
				Claim   string `json:"claim"`
			}
			if err := json.Unmarshal(final.Result, &result); err != nil {
				return fmt.Errorf("reading the job result: %w", err)
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "Moved %s to cluster %s.\n", args[0], result.Cluster)
			if result.Claim != "" {
				_, _ = fmt.Fprintf(out, "Its workspace is on claim %s there.\n", result.Claim)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&cluster, "cluster", "", "the cluster to move it to, or \"auto\" for the one with the most room")
	_ = cmd.MarkFlagRequired("cluster")
	return cmd
}
