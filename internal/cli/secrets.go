package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"agentbox/internal/api"
)

// newSecretsCmd hands API keys and tokens to agents.
//
// A value never arrives as an argument, here or anywhere else: arguments land
// in your shell history and in `ps` for every process on the machine to read.
// It comes from stdin, or from a prompt that doesn't echo.
func newSecretsCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secrets",
		Short: "Give agents API keys and tokens",
		Long: `A secret is a name and a value. It becomes an environment variable inside the
agents that get it, and its value never comes back out: AgentBox stores it
encrypted and writes it into the agent, and no command or page reads it again.

A project's secrets go to every agent of the project, including the ones you
make later. An agent's own secrets go to that agent alone.

The value is read from stdin, or asked for without echoing it. It is never
taken as an argument: that would leave it in your shell history and in ps.`,
	}
	cmd.AddCommand(newSecretsSetCmd(a), newSecretsListCmd(a), newSecretsRemoveCmd(a))
	return cmd
}

func newSecretsSetCmd(a *app) *cobra.Command {
	var fromStdin bool
	cmd := &cobra.Command{
		Use:   "set <project | project/agent> NAME",
		Short: "Store a secret and write it into the agents that get it",
		Long: `Reads the value from stdin when stdin is a pipe or a file, and otherwise asks
for it without echoing it. --value-stdin always reads stdin.

  agentbox secrets set pawly OPENAI_API_KEY < key.txt
  pass show openai/key | agentbox secrets set pawly OPENAI_API_KEY --value-stdin
  agentbox secrets set pawly/agent-03 STRIPE_SECRET_KEY      # asks for the value

Setting a secret again replaces the value. Running agents get the new one
written in straight away; their shells and their AI tool see it the next time
they start.`,
		// A third argument is someone trying to pass the value. Saying why it
		// isn't taken matters more than a usage line: by the time this runs,
		// that value is already in the shell's history.
		Args: func(_ *cobra.Command, args []string) error {
			switch {
			case len(args) < 2:
				return errors.New("which secret? Pass a name: agentbox secrets set <project | project/agent> NAME")
			case len(args) > 2:
				return errors.New("a secret's value is never an argument: it would stay in your shell history and be readable in ps while this runs. Pipe it in instead (agentbox secrets set … NAME < key.txt), or leave it out to be asked for it")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			target, name := args[0], args[1]
			value, err := secretValue(cmd, name, fromStdin)
			if err != nil {
				return err
			}
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			secret, err := c.SetSecret(cmd.Context(), target, name, value)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if secret.Scope == "project" {
				_, _ = fmt.Fprintf(out, "Stored %s for every agent of %s\n", secret.Name, secret.Project)
			} else {
				_, _ = fmt.Fprintf(out, "Stored %s for %s/%s\n", secret.Name, secret.Project, secret.Agent)
			}
			if len(secret.Agents) == 0 {
				_, _ = fmt.Fprintln(out, "No agent has it yet: the ones you make next do.")
			} else {
				_, _ = fmt.Fprintf(out, "Written into %s: %s\n", agentsWord(len(secret.Agents)), strings.Join(secret.Agents, ", "))
			}
			_, _ = fmt.Fprintf(out, "Agents read it as $%s; the value can't be read back, here or in the app.\n", secret.Name)
			return nil
		},
	}
	cmd.Flags().BoolVar(&fromStdin, "value-stdin", false, "read the value from stdin, even when stdin is a terminal")
	// Deliberately no --value flag: see the package comment above.
	return cmd
}

func newSecretsListCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "list <project | project/agent>",
		Short: "List the secrets of a project, or the ones an agent has",
		Long: `Names, scopes and when each value was last set. Values are never listed.

A project lists its own secrets. An agent lists everything it has: its
project's secrets and its own, with the scope of each.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			secrets, err := c.Secrets(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(secrets) == 0 {
				_, _ = fmt.Fprintf(out, "No secrets for %s yet. Add one with: agentbox secrets set %s NAME\n", args[0], args[0])
				return nil
			}
			w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
			_, _ = fmt.Fprintln(w, "NAME\tSCOPE\tUPDATED\tIN")
			for _, s := range secrets {
				_, _ = fmt.Fprintf(w, "$%s\t%s\t%s\t%s\n", s.Name, s.Scope, ago(s.UpdatedAt), agentsIn(s))
			}
			return w.Flush()
		},
	}
}

func newSecretsRemoveCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "rm <project | project/agent> NAME",
		Aliases: []string{"remove"},
		Short:   "Forget a secret and take it out of the agents that have it",
		Long: `The variable leaves the agents that got it: their file is written again without
it, and their next shell and their AI tool no longer see it. A process that is
already running keeps the value it read at startup.

Removing a project's secret takes it out of every agent of the project. Removing
an agent's own leaves the project's, which the agent then gets instead.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			if err := c.RemoveSecret(cmd.Context(), args[0], args[1]); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Removed %s from %s\n", args[1], args[0])
			return nil
		},
	}
}

// secretValue reads a secret's value: stdin when it is a pipe or a file (or
// --value-stdin), and otherwise a prompt that doesn't echo. One trailing
// newline, which every pipe and editor adds, is dropped; nothing else is
// touched, so a value with spaces, quotes or its own newlines arrives whole.
func secretValue(cmd *cobra.Command, name string, fromStdin bool) (string, error) {
	if !fromStdin && term.IsTerminal(int(os.Stdin.Fd())) {
		value, err := readSecret(fmt.Sprintf("Value of %s (not shown): ", name))
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(value) == "" {
			return "", errors.New("no value: nothing was typed")
		}
		return value, nil
	}
	data, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return "", err
	}
	value := strings.TrimRight(string(data), "\r\n")
	if value == "" {
		return "", fmt.Errorf("no value for %s on stdin: pipe one in (pass show … | agentbox secrets set … --value-stdin), or run this in a terminal to be asked for it", name)
	}
	return value, nil
}

// agentsIn is the "IN" column: which agents hold a secret right now. A
// project's secret in many agents is counted rather than listed, because the
// answer people want from that column is "all of them".
func agentsIn(s api.Secret) string {
	switch {
	case len(s.Agents) == 0:
		return "no agent yet"
	case s.Scope == "project" && len(s.Agents) > 2:
		return agentsWord(len(s.Agents))
	}
	return strings.Join(s.Agents, ", ")
}

func agentsWord(n int) string {
	if n == 1 {
		return "1 agent"
	}
	return strconv.Itoa(n) + " agents"
}
