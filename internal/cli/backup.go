package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"agentbox/internal/backup"
)

// newBackupCmd copies this machine's own state somewhere else. It reads the
// files itself rather than through the daemon: a backup is often taken from a
// machine whose daemon is down, or from one that is being given up, and it
// must not depend on starting one.
func newBackupCmd(a *app) *cobra.Command {
	var to string
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Copy AgentBox's state, secrets key and logins to a directory you keep",
		Long: `Writes what this machine's AgentBox knows to a new timestamped directory under
--to: the state database, the key its secrets are sealed under, and the logins
and git credentials. Restoring is a script, not a command, so it works on a
machine where AgentBox is no longer installed:

  agentbox backup --to ~/agentbox-backups
  agentbox backup verify ~/agentbox-backups/agentbox-backup-20261027-033000

The database is copied with SQLite's VACUUM INTO, so a backup is one whole file
even while the daemon is running. Worktrees, media, snapshots, chat images and
the VM's disk are not in it, and are not meant to be: see docs/backup.md.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if to == "" {
				return errors.New("--to is required: a backup beside the original is not a backup, so there is no default directory")
			}
			backup.Version = Version()
			dir, err := backup.Create(a.paths, to)
			if err != nil {
				return err
			}
			size, err := backup.Size(dir)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Backed up to %s (%s)\n", dir, backupSize(size))
			return nil
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "the directory to write a timestamped backup directory into (required)")
	cmd.AddCommand(newBackupVerifyCmd())
	return cmd
}

func newBackupVerifyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "verify <dir>",
		Short: "Check a backup against its manifest before you need it",
		Long: `Checks every file's SHA-256 against manifest.json, opens the copied database
read-only and runs SQLite's integrity_check, and checks that the secrets key is
a key. It writes nothing, so it is safe on the only copy.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backup.Verify(args[0]); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Verified %s: every checksum matches and the database passes SQLite's integrity check.\n", args[0])
			return nil
		},
	}
}

// backupSize is a backup's size as a person reads it.
func backupSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
