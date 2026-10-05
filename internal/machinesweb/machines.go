package machinesweb

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"agentbox/internal/machines"
	"agentbox/internal/machinesmedia"
)

// Machines is what the page's Machines section needs of the machines of
// `agentbox machines mcp`.
type Machines interface {
	List(ctx context.Context) ([]machines.Status, error)
	// MemoryUsage is what the named running machines use, by name.
	MemoryUsage(ctx context.Context, names []string) (map[string]string, error)
	Start(ctx context.Context, worktree string) error
	Stop(ctx context.Context, worktree string) error
	// DialVNC connects to the VNC server of the worktree's machine's display.
	DialVNC(ctx context.Context, worktree string) (io.ReadWriteCloser, error)
}

// DockerMachines are the machines of Docker or Podman.
type DockerMachines struct{ *machines.Docker }

// Start starts the worktree's machine with its configuration, as its
// machine_start does.
func (d DockerMachines) Start(ctx context.Context, worktree string) error {
	c, err := machines.Load(worktree)
	if err != nil {
		return err
	}
	_, err = d.Docker.Start(ctx, worktree, c, nil)
	return err
}

// Machine is one machine, as the page shows it.
type Machine struct {
	Name     string    `json:"name"`
	Worktree string    `json:"worktree"`
	Repo     string    `json:"repo,omitempty"`
	Branch   string    `json:"branch,omitempty"`
	Running  bool      `json:"running"`
	Started  time.Time `json:"started,omitzero"`
	// Memory is what it uses, and Limit what it may.
	Memory string `json:"memory,omitempty"`
	Limit  string `json:"limit,omitempty"`
	// Busy is "starting" or "stopping" while the page's request is under way,
	// and Error why the last one failed.
	Busy  string `json:"busy,omitempty"`
	Error string `json:"error,omitempty"`
}

// MachineList is GET /api/machines: Error says why there are none when the
// runtime can't be asked.
type MachineList struct {
	Machines []Machine `json:"machines"`
	Error    string    `json:"error,omitempty"`
}

// startTimeout bounds a start from the page, which builds the image the
// first time.
const startTimeout = 15 * time.Minute

type machineOp struct {
	busy string
	err  string
}

func (s *Server) machineList(ctx context.Context) ([]machines.Status, error) {
	if s.Machines == nil {
		return nil, errors.New("machines need Docker or Podman, and neither is installed")
	}
	return s.Machines.List(ctx)
}

func (s *Server) handleMachines(w http.ResponseWriter, r *http.Request) {
	list, err := s.machineList(r.Context())
	if err != nil {
		writeJSON(w, MachineList{Machines: []Machine{}, Error: err.Error()})
		return
	}
	var running []string
	for _, st := range list {
		if st.Running {
			running = append(running, st.Name)
		}
	}
	usage, _ := s.Machines.MemoryUsage(r.Context(), running)
	out := MachineList{Machines: make([]Machine, 0, len(list))}
	s.opsMu.Lock()
	ops := make(map[string]machineOp, len(s.ops))
	for k, v := range s.ops {
		ops[k] = v
	}
	s.opsMu.Unlock()
	for _, st := range list {
		m := Machine{Name: st.Name, Worktree: st.Worktree, Running: st.Running, Started: st.Started,
			Memory: usage[st.Name], Limit: st.Memory, Busy: ops[st.Name].busy, Error: ops[st.Name].err}
		_, m.Repo, m.Branch = machinesmedia.Describe(r.Context(), st.Worktree)
		out.Machines = append(out.Machines, m)
	}
	writeJSON(w, out)
}

// machine is a machine by its name, from the runtime: the page names one, and
// only a machine there is can be started, stopped or viewed.
func (s *Server) machine(ctx context.Context, name string) (machines.Status, error) {
	list, err := s.machineList(ctx)
	if err != nil {
		return machines.Status{}, err
	}
	for _, st := range list {
		if st.Name == name {
			return st, nil
		}
	}
	return machines.Status{}, errNoMachine
}

var errNoMachine = errors.New("no such machine")

func machineError(w http.ResponseWriter, err error) {
	code := http.StatusBadGateway
	if errors.Is(err, errNoMachine) {
		code = http.StatusNotFound
	}
	http.Error(w, err.Error(), code)
}

// handleMachineOp starts or stops a machine in the background: a start can
// take minutes. The list says when it's under way and why it failed.
func (s *Server) handleMachineOp(busy string, op func(Machines, context.Context, string) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		st, err := s.machine(r.Context(), r.PathValue("name"))
		if err != nil {
			machineError(w, err)
			return
		}
		s.opsMu.Lock()
		if s.ops[st.Name].busy != "" {
			s.opsMu.Unlock()
			http.Error(w, "the machine is "+s.ops[st.Name].busy, http.StatusConflict)
			return
		}
		s.ops[st.Name] = machineOp{busy: busy}
		s.opsMu.Unlock()
		go func() {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), startTimeout)
			defer cancel()
			err := op(s.Machines, ctx, st.Worktree)
			s.opsMu.Lock()
			if err != nil {
				s.ops[st.Name] = machineOp{err: err.Error()}
			} else {
				delete(s.ops, st.Name)
			}
			s.opsMu.Unlock()
		}()
		w.WriteHeader(http.StatusAccepted)
	}
}

// handleView bridges a WebSocket to the machine's VNC server, for the page's
// noVNC. Binary frames carry the VNC protocol's bytes both ways.
//
// websocket.Accept refuses a page from another origin, which every browser
// names in a WebSocket's Origin header: guard's Host check stops a page
// that rebinds a name of its own to 127.0.0.1, and this one a page that
// simply connects to 127.0.0.1.
func (s *Server) handleView(w http.ResponseWriter, r *http.Request) {
	st, err := s.machine(r.Context(), r.PathValue("name"))
	if err == nil && !st.Running {
		err = errors.New("the machine isn't running")
	}
	if err != nil {
		machineError(w, err)
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return // Accept has written the response
	}
	defer func() { _ = conn.CloseNow() }()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	vnc, err := s.Machines.DialVNC(ctx, st.Worktree)
	if err != nil {
		_ = conn.Close(websocket.StatusInternalError, err.Error())
		return
	}
	defer func() { _ = vnc.Close() }()
	stream := websocket.NetConn(ctx, conn, websocket.MessageBinary)
	go func() {
		_, _ = io.Copy(stream, vnc)
		cancel()
	}()
	_, _ = io.Copy(vnc, stream)
}
