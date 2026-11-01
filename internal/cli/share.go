package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"agentbox/internal/share"
)

func newShareCmd(a *app) *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "share <project/agent>",
		Short: "Save what an agent did as one HTML page anyone can open",
		Long: `Save what an agent did as one self-contained HTML page: its task, the timeline of
its steps with their screenshots, the key messages of its chat, the files it
changed and its pull request. The page needs no network and no AgentBox to open.
Its secrets, the values in its environment and your home's path are removed.

With -o -, the page is written to standard output.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			page, err := c.AgentShare(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if out == "-" {
				_, err = fmt.Fprint(cmd.OutOrStdout(), page)
				return err
			}
			if out == "" {
				i := strings.LastIndex(args[0], "/")
				out = share.FileName(args[0][:max(i, 0)], args[0][i+1:])
			}
			if err := os.WriteFile(out, []byte(page), 0o644); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Saved %s (%d KB)\n", out, (len(page)+1023)/1024)
			return nil
		},
	}
	cmd.Flags().StringVarP(&out, "output", "o", "", "the file to write (default <project>-<agent>.html; - for standard output)")
	return cmd
}
