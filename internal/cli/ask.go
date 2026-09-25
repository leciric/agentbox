package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// newAskCmd is how an agent asks its project's chat for a decision it can't
// make alone. It waits for the answer, so the agent's own turn pauses until
// somebody decides.
func newAskCmd(a *app) *cobra.Command {
	var about string
	cmd := &cobra.Command{
		Use:   "ask <question>",
		Short: "Ask this project's chat something you can't decide alone",
		Long: `Asks the project's chat a question and waits for the answer.

Run it inside an agent. The chat answers if it can; when the decision is really
the user's, it passes the question on to them. Either way the answer comes back
here, so use it when carrying on would mean guessing.

Ask about decisions, not about facts you could look up yourself.`,
		Example: `  agentbox ask "Should the reminders page paginate, or load everything?"
  agentbox ask "The API returns 500 for an empty cart. Bug, or expected?" --about "adding the checkout test"`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			question := strings.TrimSpace(strings.Join(args, " "))
			if question == "" {
				return errors.New("say what you need decided")
			}
			c, err := a.scopedClient(cmd, "")
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Asked the project's chat. Waiting for an answer…")
			answer, err := c.Ask(cmd.Context(), question, about)
			if err != nil {
				return err
			}
			who := "the project's chat"
			if answer.AnsweredBy == "user" {
				who = "the user"
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s answered:\n\n%s\n", who, answer.Answer)
			return nil
		},
	}
	cmd.Flags().StringVar(&about, "about", "", "what you were doing, so whoever answers has the context")
	return cmd
}
