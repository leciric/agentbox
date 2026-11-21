package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
	"agentbox/internal/incus"
	"agentbox/internal/sshconfig"
)

// newSSHProxyCmd is the ProxyCommand agentbox ssh-config writes for
// <instance>.agentbox: ssh's stdin and stdout, which the front end forwards
// to the VM's agentbox, become an sshd's in the agent's machine, started for
// this one connection (sshconfig.ServerScript). A stopped or paused agent is
// started first, as opening it in an editor means working in it.
func newSSHProxyCmd(a *app) *cobra.Command {
	var key string
	cmd := &cobra.Command{
		Use:    "ssh-proxy <instance>" + sshconfig.Suffix,
		Short:  "Connect ssh to an agent's machine (the ProxyCommand agentbox ssh-config writes)",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			inst := sshconfig.Instance(args[0])
			if inst == "" {
				return fmt.Errorf("%s isn't an agent's machine: ssh reaches one as ab-<project>-<agent>%s", args[0], sshconfig.Suffix)
			}
			if !sshconfig.ValidKey(key) {
				return fmt.Errorf("--key must be one public key, as agentbox ssh-config writes it")
			}
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			ag, err := agentByInstance(cmd.Context(), c, inst)
			if err != nil {
				return err
			}
			if ag, err = awake(cmd.Context(), c, ag); err != nil {
				return err
			}
			u, err := hostUser()
			if err != nil {
				return err
			}
			// -T: no terminal, whatever stdin is, so the bytes go through as
			// they are.
			run := incus.Client{}.Command(cmd.Context(), "exec", "-T", ag.Instance, "--", "sh", "-c", sshconfig.ServerScript, "sh", key, u.Name)
			run.Stdin, run.Stdout, run.Stderr = cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()
			err = run.Run()
			if code, ok := incus.ExitCode(err); ok {
				return exitCodeError(code)
			}
			return err
		},
	}
	cmd.Flags().StringVar(&key, "key", "", "the public key to let in: AgentBox's own, from agentbox ssh-config")
	return cmd
}

// agentByInstance is the agent whose machine is inst.
func agentByInstance(ctx context.Context, c *api.Client, inst string) (api.Agent, error) {
	agents, err := c.Agents(ctx, "")
	if err != nil {
		return api.Agent{}, err
	}
	for _, ag := range agents {
		if ag.Instance == inst {
			return ag, nil
		}
	}
	return api.Agent{}, fmt.Errorf("no agent's machine is called %s: it may have been removed", inst)
}

// awake starts the agent, or resumes it, unless it's running.
func awake(ctx context.Context, c *api.Client, ag api.Agent) (api.Agent, error) {
	var action string
	switch ag.State {
	case "running":
		return ag, nil
	case "paused":
		action = "resume"
	default:
		action = "start"
	}
	woken, err := c.AgentAction(ctx, ag.Ref, action)
	if err != nil {
		return ag, fmt.Errorf("%s is %s, and it didn't %s: %w", ag.Ref, ag.State, action, err)
	}
	if woken.State != "running" {
		return woken, notRunning(woken)
	}
	return woken, nil
}
