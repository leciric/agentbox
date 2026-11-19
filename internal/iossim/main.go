package iossim

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// Main is `agentbox ios-helper --config <dir> [--root <dir>]…`: the helper,
// which the front end starts (Ensure) and nobody runs by hand.
func Main(args []string, version string) int {
	fs := flag.NewFlagSet(HelperCommand, flag.ContinueOnError)
	config := fs.String("config", "", "AgentBox's config directory, where the state file goes")
	var roots multiFlag
	fs.Var(&roots, "root", "a directory apps may be installed from (repeatable)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *config == "" {
		fmt.Fprintln(os.Stderr, "error: --config is required")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	if err := Serve(ctx, ServeOptions{ConfigDir: *config, Roots: roots, Version: version, Log: os.Stderr}); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

type multiFlag []string

func (m *multiFlag) String() string     { return fmt.Sprint(*m) }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }
