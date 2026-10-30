package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"

	"agentbox/internal/gateway"
	"agentbox/internal/state"
)

// A cluster agent's lifecycle is a handful of writes to its row that the
// reconciler acts on, and its operations are several of those in a row: a
// restore stops the pod, makes a claim, names it, and asks for the pod again.
// Two of them interleaving is how a destroy got undone by a restore asking for
// the pod back, a stop by a snapshot's restart, and how two restores both moved
// the row. So each operation holds the agent for as long as it runs, and any
// other one is refused with ErrAgentBusy until it is done.
//
// The hold lives in this daemon's memory, not the row: every operation runs in
// this process, and a daemon that stops ends them all, so a hold kept in the
// database would only be one a crash left behind for good.
//
// It is reentrant through the context: the job that holds an agent goes on
// through the manager to the pod's own Start, Stop and Destroy (podLifecycle),
// which check the hold and let its holder through.

// ErrAgentBusy is a lifecycle operation refused because another one is running
// on the same agent.
var ErrAgentBusy = errors.New("busy")

// agentLifecycles is who holds which agent.
type agentLifecycles struct {
	sync.Mutex
	held map[string]*agentHold
}

// agentHold is one operation's hold; its pointer is what a context carries.
type agentHold struct{ op string }

// heldAgent is the context key under which a hold on one agent travels.
type heldAgent string

// holdAgent takes a for op, unless something else already holds it. An agent
// on this host is not held: Incus serialises its own operations, and these
// races are a pod's. The context it returns is the one the operation runs
// with; release is safe to call more than once, and does nothing for a hold
// the context already carried.
func (s *Server) holdAgent(ctx context.Context, a state.Agent, op string) (context.Context, func(), error) {
	if !a.OnCluster() {
		return ctx, func() {}, nil
	}
	return s.holdRef(ctx, a.Ref(), op)
}

func (s *Server) holdRef(ctx context.Context, ref, op string) (context.Context, func(), error) {
	l := &s.lifecycles
	l.Lock()
	defer l.Unlock()
	if mine, _ := ctx.Value(heldAgent(ref)).(*agentHold); mine != nil && l.held[ref] == mine {
		return ctx, func() {}, nil
	}
	if h := l.held[ref]; h != nil {
		return ctx, func() {}, busyError(ref, op, h.op)
	}
	if l.held == nil {
		l.held = map[string]*agentHold{}
	}
	h := &agentHold{op: op}
	l.held[ref] = h
	var once sync.Once
	release := func() {
		once.Do(func() {
			l.Lock()
			if l.held[ref] == h {
				delete(l.held, ref)
			}
			l.Unlock()
		})
	}
	return context.WithValue(ctx, heldAgent(ref), h), release, nil
}

// checkAgentFree refuses op when another operation holds the agent, and lets
// the holder itself through. It is what the pod's own lifecycle calls ask, so
// a stop from anywhere (the idle sweep, the manager) can't land mid-restore.
func (s *Server) checkAgentFree(ctx context.Context, ref gateway.Ref, op string) error {
	key := ref.Project + "/" + ref.Agent
	l := &s.lifecycles
	l.Lock()
	defer l.Unlock()
	h := l.held[key]
	if h == nil {
		return nil
	}
	if mine, _ := ctx.Value(heldAgent(key)).(*agentHold); mine == h {
		return nil
	}
	return busyError(key, op, h.op)
}

func busyError(ref, op, holder string) error {
	return fmt.Errorf("%s is %w: it is %s, and can't %s until that is done", ref, ErrAgentBusy, holderVerb(holder), op)
}

// holderVerb is what an operation reads as in "it is ...".
func holderVerb(op string) string {
	switch op {
	case "start":
		return "starting"
	case "stop":
		return "stopping"
	case "destroy":
		return "being destroyed"
	case "snapshot":
		return "taking a snapshot"
	case "restore":
		return "being restored from a snapshot"
	case "fork":
		return "being forked"
	case "move":
		return "moving to another cluster"
	case "recreate":
		return "getting a new machine"
	}
	return "busy with " + op
}

// startAgentJob is startJob for a lifecycle job: the agent is held from the
// request until the job ends, so a second one is refused before it is
// accepted, and the job's context carries the hold to everything it calls.
func (s *Server) startAgentJob(ctx context.Context, w http.ResponseWriter, kind string, a state.Agent, fn func(context.Context, io.Writer) (any, error)) error {
	held, release, err := s.holdAgent(ctx, a, kind)
	if err != nil {
		return err
	}
	hold := held.Value(heldAgent(a.Ref()))
	err = s.startJob(w, kind, a.Ref(), func(ctx context.Context, log io.Writer) (any, error) {
		defer release()
		if hold != nil {
			ctx = context.WithValue(ctx, heldAgent(a.Ref()), hold)
		}
		return fn(ctx, log)
	})
	if err != nil {
		release()
	}
	return err
}
