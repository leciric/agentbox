package sshconfig

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Command is `agentbox ssh-config`, which runs on the user's computer even
// where every other command runs in the VM (cmd/agentbox/main.go): the ssh
// config, the key and the agentbox ssh runs are this computer's. The apps run
// it before opening an agent in an editor, so a moved agentbox, or a key that
// was removed, is put right first.
func Command() *cobra.Command {
	var asJSON, remove bool
	cmd := &cobra.Command{
		Use:   "ssh-config",
		Short: "Let ssh and editors reach agents' machines as <instance>.agentbox (adds one Include line to ~/.ssh/config; --remove takes it out)",
		Long: `Lets ssh, and editors that connect over it (VS Code's Remote-SSH, Cursor, Windsurf,
VSCodium, Zed), reach an agent's machine as <instance>.agentbox, which is how the
apps' "Open in <editor>" opens an agent's worktree inside its machine:

  ssh ab-myproject-agent-01.agentbox

It keeps a key of this installation's and an ssh config in AgentBox's data folder,
and adds one Include line for that config at the top of ~/.ssh/config. ssh reaches
the machine through agentbox ssh-proxy, which starts an sshd in it for each
connection: nothing listens on the network. --remove takes the line and the
folder out again.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, err := DefaultFiles()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if remove {
				if err := Remove(f); err != nil {
					return err
				}
				_, _ = fmt.Fprintf(out, "Removed AgentBox's Include line from %s, and %s\n", f.UserConfig, f.Dir)
				return nil
			}
			bin, err := os.Executable()
			if err != nil {
				return err
			}
			user, err := CurrentUser()
			if err != nil {
				return err
			}
			setup, err := Ensure(f, bin, user)
			if err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(out).Encode(setup)
			}
			_, _ = fmt.Fprintf(out, "ssh reaches an agent's machine as ab-<project>-<agent>%s (ssh ab-myproject-agent-01%s)\n", Suffix, Suffix)
			_, _ = fmt.Fprintf(out, "Its config is %s, included from %s; agentbox ssh-config --remove takes it out\n", setup.Config, setup.UserConfig)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the user and host-name suffix as JSON, for the apps")
	cmd.Flags().BoolVar(&remove, "remove", false, "take AgentBox's Include line out of ~/.ssh/config, and remove its key and config")
	return cmd
}
