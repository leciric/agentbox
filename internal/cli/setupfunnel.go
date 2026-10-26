package cli

import (
	"encoding/json"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
)

// newSetupFunnelCmd hands the daemon the steps of Setup the front end took
// before it was up, read from standard input (package setupfunnel). The
// front end runs it in the VM once `vm init` is done; nobody else has a
// reason to.
func newSetupFunnelCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:    "setup-funnel",
		Short:  "Hand the daemon the front end's Setup steps, for the anonymous usage stats",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var r api.SetupFunnelReport
			if err := json.NewDecoder(cmd.InOrStdin()).Decode(&r); err != nil {
				return err
			}
			return api.NewClient(a.paths.Socket()).ReportSetupFunnel(cmd.Context(), r)
		},
	}
}
