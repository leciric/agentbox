package daemon

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/image"
	"agentbox/internal/state"
)

// "Keep Claude Code up to date" (off by default): new Claude Code releases
// without an AgentBox release. While it's on, the daemon asks npm for the
// newest Claude Code and claude-agent-acp as it starts, once a day and when
// you press Check now, and moves the base image's copies on in place the way
// a tools.txt bump is (image.UpdateTools): a copy of the base gets them, every
// tool's check from tools.txt runs, and the copy is swapped in only if all
// pass. The versions it moved to are recorded in state (tool_versions), never
// in tools.txt, whose pins stay the floor (image.Overrides): a pin moved past
// them takes over. A failure leaves the base and the recorded versions as
// they were and says why in Settings; unlike a failed tools.txt bump it asks
// for no rebuild, since the base works as it is. Turning it off forgets the
// versions, which puts the base back on the pins (updateBaseTools).
//
// Agents made after an update start on the new base. One that already exists
// gets the versions when its chat starts its adapter (agent.ChatCommand), and
// a running chat restarts on them before its next turn, never during one
// (chat.ToolUpdated). Leads run the host's Claude Code and stay as they are.

// autoUpdate is an AI tool AgentBox can keep up to date past its pins. Codex
// and OpenCode could join with an entry each, and a setting.
type autoUpdate struct {
	tool    string // as agents name it: "claude"
	setting string // the flag that turns it on, off by default
	// packages are what it moves on, by mise's name and npm's. The first is
	// the tool itself, whose version Settings shows.
	packages []autoPackage
}

type autoPackage struct{ name, npm string }

var claudeAutoUpdate = autoUpdate{
	tool:    "claude",
	setting: state.SettingClaudeCodeAutoUpdate,
	packages: []autoPackage{
		{"claude", "@anthropic-ai/claude-code"},
		{"npm:@agentclientprotocol/claude-agent-acp", "@agentclientprotocol/claude-agent-acp"},
	},
}

var autoUpdates = []autoUpdate{claudeAutoUpdate}

func (u autoUpdate) names() []string {
	names := make([]string, 0, len(u.packages))
	for _, p := range u.packages {
		names = append(names, p.name)
	}
	return names
}

const (
	// toolUpdateInterval is how often the daemon looks for newer versions.
	toolUpdateInterval = 24 * time.Hour
	// toolUpdateRetry is how soon it looks again when another job held the
	// base image.
	toolUpdateRetry = 5 * time.Minute
	// defaultNPMRegistry is where it looks (Config.NPMRegistry).
	defaultNPMRegistry = "https://registry.npmjs.org"
)

// toolUpdates is what the daemon is doing about them, by autoUpdate.tool.
type toolUpdates struct {
	mu       sync.Mutex
	checking map[string]bool   // a check, or the update it started, runs
	job      map[string]string // the update's job
	now      chan struct{}     // pokes watchToolUpdates to check at once
}

func newToolUpdates() *toolUpdates {
	return &toolUpdates{checking: map[string]bool{}, job: map[string]string{}, now: make(chan struct{}, 1)}
}

func (t *toolUpdates) poke() {
	select {
	case t.now <- struct{}{}:
	default:
	}
}

// begin marks a check of tool as running: false when one already is.
func (t *toolUpdates) begin(tool string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.checking[tool] {
		return false
	}
	t.checking[tool] = true
	return true
}

func (t *toolUpdates) end(tool string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.checking, tool)
	delete(t.job, tool)
}

// watchToolUpdates checks as the daemon starts, every toolUpdateInterval
// after, and when poked: by Check now, turning a setting on, or a retry.
func (s *Server) watchToolUpdates(ctx context.Context) {
	tick := time.NewTicker(toolUpdateInterval)
	defer tick.Stop()
	for {
		for _, u := range autoUpdates {
			s.checkToolUpdate(ctx, u)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-s.toolUpdates.now:
		}
	}
}

// checkToolUpdate looks for newer versions of u's packages, while u is on
// and no check of it runs, and starts moving the base image to them when
// there are.
func (s *Server) checkToolUpdate(ctx context.Context, u autoUpdate) {
	if s.beginToolCheck(ctx, u) {
		s.runToolCheck(ctx, u)
	}
}

// checkToolUpdateNow is Check now: the check is marked running before this
// returns, so the settings it answers with say so.
func (s *Server) checkToolUpdateNow(u autoUpdate) {
	if ctx := s.background(); s.beginToolCheck(ctx, u) {
		go s.runToolCheck(ctx, u)
	}
}

func (s *Server) beginToolCheck(ctx context.Context, u autoUpdate) bool {
	if on, err := s.store.Flag(ctx, u.setting); err != nil || !on {
		return false
	}
	return s.toolUpdates.begin(u.tool)
}

// runToolCheck is the check beginToolCheck began, and ends it.
func (s *Server) runToolCheck(ctx context.Context, u autoUpdate) {
	started := false
	defer func() {
		if !started {
			s.toolUpdates.end(u.tool)
		}
	}()
	now := time.Now()
	latest := map[string]string{}
	for _, p := range u.packages {
		v, err := s.npmLatest(ctx, p.npm)
		if err != nil {
			s.recordToolCheck(ctx, u, nil, now, fmt.Errorf("asking npm for the newest %s: %w", p.npm, err))
			return
		}
		latest[p.name] = v
	}
	// What the base would have: the newest of each, where it is newer than
	// its pin.
	want, err := s.store.ToolOverrides(ctx)
	if err != nil {
		return
	}
	for _, p := range u.packages {
		if v := latest[p.name]; image.NewerVersion(v, image.Overrides(nil).Version(p.name)) {
			want[p.name] = v
		} else {
			delete(want, p.name)
		}
	}
	plan, _, err := s.basePlanWith(ctx, want)
	if err != nil {
		s.recordToolCheck(ctx, u, latest, now, fmt.Errorf("reading the base image: %w", err))
		return
	}
	switch plan.Action {
	case image.NeedsBuild, image.NeedsRebuild:
		s.recordToolCheck(ctx, u, latest, now, errors.New("the agents' base image needs building first: Setup offers it"))
		return
	case image.UpToDate:
		s.recordToolVersions(ctx, u, want, latest, now, nil)
		return
	}
	if s.disk != nil && s.diskStatus().Level == agent.DiskFull {
		s.recordToolCheck(ctx, u, latest, now, errors.New("the disk is too full to copy the base image"))
		return
	}
	if !s.claimImage() {
		// A build, or tools.txt's own update, has it: once it's done.
		time.AfterFunc(toolUpdateRetry, s.toolUpdates.poke)
		return
	}
	j, err := s.jobs.start("tool-update", image.SnapshotRef(), func(ctx context.Context, log io.Writer) (any, error) {
		revert := false
		defer func() {
			s.releaseImage()
			s.toolUpdates.end(u.tool)
			if revert {
				// Turned off while it ran: back to the pins, now the image is free.
				s.updateBaseTools(s.background())
			}
		}()
		s.logf("job tool-update started")
		// Another job may have changed the image since the plan was made.
		plan, _, err := s.basePlanWith(ctx, want)
		if err == nil && plan.Action == image.NeedsTools {
			err = image.UpdateTools(ctx, s.cfg.Incus, s.cfg.User, plan, log)
		}
		if err != nil {
			s.logf("job tool-update failed: %v", err)
			_, _ = fmt.Fprintf(log, "==> %v\n==> Agents keep the versions they have\n", err)
			s.recordToolCheck(context.WithoutCancel(ctx), u, latest, now, err)
			return nil, err
		}
		if on, err := s.store.Flag(ctx, u.setting); err == nil && !on {
			revert = true
			return nil, errors.New("keeping Claude Code up to date was turned off while it updated")
		}
		s.recordToolVersions(ctx, u, want, latest, now, nil)
		s.chat.ToolUpdated(u.tool)
		return map[string]string{"snapshot": image.SnapshotRef(), "toolsVersion": plan.ToolsVersion}, nil
	})
	if err != nil {
		s.releaseImage()
		s.recordToolCheck(ctx, u, latest, now, err)
		return
	}
	started = true
	s.toolUpdates.mu.Lock()
	if s.toolUpdates.checking[u.tool] {
		s.toolUpdates.job[u.tool] = j.info.ID
	}
	s.toolUpdates.mu.Unlock()
}

// recordToolVersions records that the base image runs want, which latest
// found at the check made at.
func (s *Server) recordToolVersions(ctx context.Context, u autoUpdate, want, latest map[string]string, at time.Time, failed error) {
	for _, p := range u.packages {
		v := state.ToolVersion{Name: p.name, Version: want[p.name], Latest: latest[p.name], CheckedAt: at}
		if failed != nil {
			v.Error = failed.Error()
		}
		if err := s.store.SetToolVersion(ctx, v); err != nil {
			s.logf("recording %s's version: %v", p.name, err)
		}
	}
}

// recordToolCheck records a check that changed nothing, failed saying why:
// the versions the base runs stay what they were. latest is nil when the
// check didn't get as far as finding them.
func (s *Server) recordToolCheck(ctx context.Context, u autoUpdate, latest map[string]string, at time.Time, failed error) {
	s.logf("keeping %s up to date: %v", u.tool, failed)
	known, err := s.store.ToolVersions(ctx)
	if err != nil {
		return
	}
	want := map[string]string{}
	newest := map[string]string{}
	for _, p := range u.packages {
		want[p.name] = known[p.name].Version
		newest[p.name] = cmp.Or(latest[p.name], known[p.name].Latest)
	}
	s.recordToolVersions(ctx, u, want, newest, at, failed)
}

// setToolAutoUpdate turns u on, which checks at once, or off, which forgets
// the versions it moved to and puts the base image back on the pins.
func (s *Server) setToolAutoUpdate(ctx context.Context, u autoUpdate, on bool) error {
	if err := s.store.SetFlag(ctx, u.setting, on); err != nil {
		return err
	}
	if on {
		s.checkToolUpdateNow(u)
		return nil
	}
	if err := s.store.ForgetToolVersions(ctx, u.names()...); err != nil {
		return err
	}
	s.chat.ToolUpdated(u.tool)
	go s.updateBaseTools(s.background())
	return nil
}

// npmLatest is the version npm's latest tag points at for pkg.
func (s *Server) npmLatest(ctx context.Context, pkg string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	url := strings.TrimSuffix(cmp.Or(s.cfg.NPMRegistry, defaultNPMRegistry), "/") + "/" + pkg + "/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s answered %s", url, resp.Status)
	}
	var body struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", fmt.Errorf("reading %s: %w", url, err)
	}
	if !image.NewerVersion(body.Version, "0.0.0") {
		return "", fmt.Errorf("%s gave no version", url)
	}
	return body.Version, nil
}

// toolAutoUpdate is u as Settings shows it.
func (s *Server) toolAutoUpdate(ctx context.Context, u autoUpdate) (api.ToolAutoUpdate, error) {
	on, err := s.store.Flag(ctx, u.setting)
	if err != nil {
		return api.ToolAutoUpdate{}, err
	}
	known, err := s.store.ToolVersions(ctx)
	if err != nil {
		return api.ToolAutoUpdate{}, err
	}
	name := u.packages[0].name
	v := known[name]
	out := api.ToolAutoUpdate{On: on, Pinned: image.Overrides(nil).Version(name), Latest: v.Latest, Error: v.Error}
	if !v.CheckedAt.IsZero() {
		out.CheckedAt = &v.CheckedAt
	}
	// What the base image really has, which is what new agents get.
	read, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if installed, err := image.InstalledBuild(read, s.cfg.Incus); err == nil {
		for _, spec := range installed.Tools {
			if (image.Tool{Spec: spec}).Name() == name {
				out.Installed = strings.TrimPrefix(spec, name+"@")
			}
		}
	}
	s.toolUpdates.mu.Lock()
	out.Checking, out.Job = s.toolUpdates.checking[u.tool], s.toolUpdates.job[u.tool]
	s.toolUpdates.mu.Unlock()
	return out, nil
}
