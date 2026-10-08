package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
	"agentbox/internal/cursor"
)

// newAuthCursorCmd signs agents in to Cursor, through the daemon so the app's
// Settings and this command keep the same one sign-in (internal/daemon/cursor.go).
func newAuthCursorCmd(a *app) *cobra.Command {
	var fromStdin, login, logout bool
	cmd := &cobra.Command{
		Use:   "cursor",
		Short: "Sign agents in to Cursor",
		Long: `Signs agents in to Cursor, which AgentBox runs through Cursor's SDK. Either
paste an API key from Cursor's dashboard (Integrations → API Keys), or use
--login for Cursor's own sign-in in the browser, which makes a key named
"AgentBox" on your account that expires in 90 days.

The key is checked with Cursor before it is stored, in AgentBox's own
directory: never your host's ~/.cursor. Agents get a copy as their chat starts.`,
		Example: `  agentbox auth cursor
  printenv CURSOR_API_KEY | agentbox auth cursor --key-stdin
  agentbox auth cursor --login
  agentbox auth cursor --logout`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			switch {
			case logout:
				if err := c.RemoveCursorLogin(cmd.Context()); err != nil {
					return err
				}
				_, _ = fmt.Fprintln(out, "Signed agents out of Cursor. A key Cursor made at sign-in stays valid until it expires or you revoke it in Cursor's dashboard.")
				return nil
			case login:
				status, err := c.StartCursorLogin(cmd.Context())
				if err != nil {
					return err
				}
				shown := false
				deadline := time.Now().Add(cursor.LoginTimeout)
				for time.Now().Before(deadline) {
					switch status.State {
					case api.CursorLoginWaiting:
						if !shown {
							_, _ = fmt.Fprintf(out, "Open this page and sign in to Cursor:\n\n  %s\n\nWaiting for the sign-in to finish...\n", status.URL)
							shown = true
						}
					case api.CursorLoginDone:
						_, _ = fmt.Fprintln(out, "Signed agents in to Cursor"+cursorAccount(status.Email))
						return nil
					case api.CursorLoginFailed:
						return errors.New(status.Error)
					case api.CursorLoginIdle:
						return errors.New("the sign-in was stopped")
					}
					select {
					case <-cmd.Context().Done():
						return cmd.Context().Err()
					case <-time.After(time.Second):
					}
					if status, err = c.CursorLoginStatus(cmd.Context()); err != nil {
						return err
					}
				}
				return errors.New("the sign-in wasn't finished in time: run agentbox auth cursor --login again")
			}
			var key string
			if fromStdin {
				b, err := io.ReadAll(io.LimitReader(os.Stdin, 64<<10))
				if err != nil {
					return err
				}
				key = string(b)
			} else if key, err = readSecret("Cursor API key (from Cursor's dashboard, Integrations → API Keys): "); err != nil {
				return fmt.Errorf("%w, or --login to sign in in the browser", err)
			}
			if key = strings.TrimSpace(key); key == "" {
				return errors.New("no API key: paste one from Cursor's dashboard, or use --login")
			}
			email, err := c.SaveCursorKey(cmd.Context(), key)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintln(out, "Signed agents in to Cursor"+cursorAccount(email))
			return nil
		},
	}
	cmd.Flags().BoolVar(&fromStdin, "key-stdin", false, "read the API key from stdin")
	cmd.Flags().BoolVar(&login, "login", false, "sign in with Cursor in the browser instead of pasting a key")
	cmd.Flags().BoolVar(&logout, "logout", false, "forget the Cursor sign-in")
	cmd.MarkFlagsMutuallyExclusive("key-stdin", "login", "logout")
	return cmd
}

func cursorAccount(email string) string {
	if email == "" {
		return ""
	}
	return " as " + email
}
