package incus

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"

	incusclient "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/shared/api"
)

// The daemon talks to Incus through its REST API on the local unix socket,
// which is what the incus command does too, minus a process and a config load
// per call: 7–16 ms each, and an agent's create makes about fifty of them.
//
// One connection is shared. It is made on first use, and not remembered when
// it fails, so a daemon started before Incus was installed finds it later. The
// client's transport dials the socket for every request (it disables
// keep-alives), so an Incus restarted meanwhile is simply dialled again.
var shared struct {
	sync.Mutex
	server incusclient.InstanceServer
}

// server returns the shared connection, bound to ctx: every request made
// through it is cancelled with ctx. WithContext would set ctx on the shared
// connection itself, so it is set on a copy, which UseProject makes.
func (c Client) server(ctx context.Context) (incusclient.InstanceServer, error) {
	if err := c.notAnsweringErr(); err != nil {
		return nil, err
	}
	shared.Lock()
	defer shared.Unlock()
	if shared.server == nil {
		// The same socket the incus command uses: INCUS_SOCKET, then
		// INCUS_DIR, then Incus' own paths. The server's API extensions are
		// read once, here, for the client to pick the calls this Incus has.
		connect, cancel := context.WithTimeout(ctx, connectTimeout)
		defer cancel()
		s, err := incusclient.ConnectIncusUnixWithContext(connect, "", &incusclient.ConnectionArgs{
			UserAgent:     "agentbox",
			SkipGetEvents: true, // operations are waited on with /wait, not the event stream
		})
		if err != nil {
			return nil, err
		}
		shared.server = s
	}
	info, err := shared.server.GetConnectionInfo()
	if err != nil {
		return nil, err
	}
	s := shared.server.UseProject(info.Project).(interface {
		WithContext(context.Context) incusclient.InstanceServer
	})
	return s.WithContext(ctx), nil
}

// do runs one Incus operation. args is what the incus command would be given
// to do the same thing: with Bin set, that is what runs, and either way it is
// what the error names, so an error reads as it did when AgentBox ran
// `incus <args>` — "incus delete --force agent-01: Instance not found".
func (c Client) do(ctx context.Context, args []string, call func(incusclient.InstanceServer) error) error {
	if err := c.notAnsweringErr(); err != nil {
		return c.fail(args, err)
	}
	if c.cli() {
		_, err := c.run(ctx, args...)
		return err
	}
	s, err := c.server(ctx)
	if err == nil {
		err = call(s)
	}
	return c.fail(args, err)
}

// fail wraps err the way run does a failed incus command: its own message,
// after the command it stands for. Context errors stay matchable with
// errors.Is, as incus' own error stays matchable in run.
func (c Client) fail(args []string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("incus %s: %w", strings.Join(args, " "), err)
}

// wait waits for an operation Incus started to finish, or for ctx. It is
// op.WaitContext, which waits on the operation's /wait endpoint for as long as
// ctx allows, in a loop: a deadline under a second asks Incus not to wait at
// all, and it answers with the operation still running.
func wait(ctx context.Context, op incusclient.Operation, err error) error {
	if err != nil {
		return err
	}
	for {
		if err := op.WaitContext(ctx); err != nil {
			return err
		}
		if op.Get().StatusCode.IsFinal() {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}

// isNotFound reports whether Incus answered 404: an instance, snapshot or
// volume that isn't there.
func isNotFound(err error) bool {
	return api.StatusErrorCheck(err, http.StatusNotFound)
}

// errNotFound turns a 404 into ErrNotFound, named the way Details has always
// named it: "agent-01: instance not found".
func errNotFound(name string, err error) error {
	if isNotFound(err) {
		return fmt.Errorf("%s: %w", name, ErrNotFound)
	}
	return err
}
