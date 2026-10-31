package daemon

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/hostos"
	"agentbox/internal/image"
)

// The setup funnel: one "setup" event for each step of a new installation's
// Setup, the first time it is reached, and the first time it fails, so the
// landing site can tell where new users stop. Like the health event, every
// field is flat, an enum or a bounded int, listed with its values in
// setupevent_test.go (setupFields) and in agentbox-landing's events.ts.
// Until the landing takes them, they go in batches of their own
// (eventBatch).
//
// Most steps happen here. The KVM check and the VM's making happen on the
// user's machine before this daemon exists, where its usage-stats setting
// can't be read: the front end keeps them and hands them over once the
// daemon is up (reportSetupFunnel), and nothing about them leaves before
// this daemon decides to keep them.

// The setup steps, in the order a new installation reaches them.
const (
	setupStarted      = "setup_started"
	setupKVM          = "kvm_check"
	setupVMReady      = "vm_ready"
	setupImageReady   = "image_ready"
	setupSignedIn     = "ai_signed_in"
	setupFirstProject = "first_project"
	setupFirstAgent   = "first_agent"
	setupFirstPR      = "first_pr"
)

// setupSteps is every step, as the server takes them.
var setupSteps = []string{setupStarted, setupKVM, setupVMReady, setupImageReady, setupSignedIn, setupFirstProject, setupFirstAgent, setupFirstPR}

// A step's outcomes. missing and no_permission are the KVM check's: no
// /dev/kvm, or one this user can't open.
const (
	setupOK           = "ok"
	setupFailed       = "failed"
	setupKVMMissing   = "missing"
	setupKVMForbidden = "no_permission"
)

var setupOutcomes = []string{setupOK, setupFailed, setupKVMMissing, setupKVMForbidden}

// imageSources are where the base image came from: the published one, built
// here, or built here after the published one couldn't be used.
var imageSources = []string{image.SourcePublished, image.SourceLocal, image.SourceLocalAfterPublished}

// setupNone is a field that says nothing for this step.
const setupNone = "none"

// setupMaxSeconds is the most a duration says, 30 days.
const setupMaxSeconds = int64(30 * 24 * time.Hour / time.Second)

// setupEvent is a setup event's fields.
type setupEvent struct {
	Step    string `json:"step"`
	Outcome string `json:"outcome"`
	// DurationS is how long the step itself ran, where AgentBox runs it
	// (the KVM check, the VM, the image, an in-app login, the first agent's
	// creation); 0 for a step that is a moment.
	DurationS int64 `json:"duration_s"`
	// Since and SinceS are how long after the setup began it was reached:
	// Since in the buckets the first funnel had, SinceS in seconds.
	Since       string `json:"since"`
	SinceS      int64  `json:"since_s"`
	ErrorClass  string `json:"error_class"`
	ImageSource string `json:"image_source"`
	Tool        string `json:"tool"`
	FrontEnd    string `json:"front_end"`
}

// setupReport is a step reached, or failed, as the code that saw it says.
type setupReport struct {
	step, outcome string
	took          time.Duration
	class         string // a failure's errorClass
	imageSource   string
	tool          string
	at            time.Time // when; now when zero
}

// setupMu keeps two steps reached at once from both reading the steps sent
// before either writes it down.
var setupMu sync.Mutex

// beginSetupFunnel decides, once, whether this installation's setup is still
// ahead of it: one with no projects yet starts it now, and one already in use
// never sends a step.
func (s *Server) beginSetupFunnel(ctx context.Context) {
	setupMu.Lock()
	defer setupMu.Unlock()
	if at, err := s.store.Setting(ctx, settingUsageSetupAt); err != nil || at != "" {
		return
	}
	projects, err := s.store.Projects(ctx)
	if err != nil {
		return
	}
	if len(projects) > 0 {
		_ = s.store.SetSetting(ctx, settingUsageSetupAt, "existing")
		return
	}
	_ = s.store.SetSetting(ctx, settingUsageSetupAt, strconv.FormatInt(time.Now().Unix(), 10))
	s.setupReachedLocked(ctx, setupReport{step: setupStarted})
}

// setupStep keeps a setup event the first time this installation reaches
// step.
func (s *Server) setupStep(ctx context.Context, step string) {
	s.setupReached(ctx, setupReport{step: step})
}

// setupStepFailed keeps a setup event the first time step fails, unless it
// was reached already: what fails after that isn't where anyone stopped.
func (s *Server) setupStepFailed(ctx context.Context, r setupReport, err error) {
	r.outcome = setupFailed
	if r.class == "" {
		r.class = errorClass(err, "")
	}
	s.setupReached(ctx, r)
}

func (s *Server) setupReached(ctx context.Context, r setupReport) {
	setupMu.Lock()
	defer setupMu.Unlock()
	s.setupReachedLocked(ctx, r)
}

// setupReachedLocked is setupReached under setupMu.
func (s *Server) setupReachedLocked(ctx context.Context, r setupReport) {
	if !s.usageStatsOn(ctx) {
		return
	}
	at, err := s.store.Setting(ctx, settingUsageSetupAt)
	if err != nil {
		return
	}
	began, err := strconv.ParseInt(at, 10, 64)
	if err != nil {
		return // never begun here, or "existing"
	}
	steps, err := s.store.Setting(ctx, settingUsageSetupSteps)
	if err != nil {
		return
	}
	if r.outcome == "" {
		r.outcome = setupOK
	}
	// A step reached is sent as its name, the first time, as the first
	// funnel did; one that failed as its name and outcome, the first time.
	key := r.step
	if r.outcome != setupOK {
		key += "." + r.outcome
	}
	done := strings.Split(steps, ",")
	if slices.Contains(done, r.step) || slices.Contains(done, key) {
		return
	}
	if err := s.store.SetSetting(ctx, settingUsageSetupSteps, strings.Trim(steps+","+key, ",")); err != nil {
		return
	}
	if r.at.IsZero() {
		r.at = time.Now()
	}
	since := max(r.at.Sub(time.Unix(began, 0)), 0)
	e := setupEvent{
		Step:        r.step,
		Outcome:     r.outcome,
		DurationS:   setupSeconds(r.took),
		Since:       usageSpan(since),
		SinceS:      setupSeconds(since),
		ErrorClass:  setupNone,
		ImageSource: setupNone,
		Tool:        setupNone,
		FrontEnd:    healthFrontEnd(hostos.OS(), agent.VenusVM()),
	}
	if r.outcome != setupOK {
		e.ErrorClass = classOther
		if slices.Contains(errorClasses, r.class) {
			e.ErrorClass = r.class
		}
	}
	if r.imageSource != "" {
		e.ImageSource = r.imageSource
	}
	if r.tool != "" {
		e.Tool = r.tool
	}
	s.recordEvent(eventSetup, e)
}

// setupSeconds is d in whole seconds, from 0 to setupMaxSeconds.
func setupSeconds(d time.Duration) int64 {
	return min(max(int64(d/time.Second), 0), setupMaxSeconds)
}

// setupSignedInTools are the AI tools a sign-in counts for, by the id of
// their Setup check: the first one reached is the step.
var setupSignedInTools = []string{"claude", "codex", "opencode", "cursor"}

// noteSetupChecks reaches ai_signed_in when Setup's checks show an AI tool
// signed in, however it was: the app's login, a token pasted in, the CLI.
// An in-app Claude Code login reaches it first, with how long it took.
func (s *Server) noteSetupChecks(ctx context.Context, checks []api.SetupCheck) {
	for _, c := range checks {
		if slices.Contains(setupSignedInTools, c.ID) && (c.Status == api.SetupOK || c.Status == api.SetupWarn) {
			s.setupReached(ctx, setupReport{step: setupSignedIn, tool: c.ID})
			return
		}
	}
}

// reportSetupFunnel is POST /v1/usage-events/setup: the front end's steps.
// The setup began when the first of them did, if that is earlier than this
// daemon's first start.
func (s *Server) reportSetupFunnel(w http.ResponseWriter, r *http.Request) error {
	var req api.SetupFunnelReport
	if err := readJSON(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	s.beginSetupFunnel(ctx)
	if s.usageStatsOn(ctx) {
		s.takeSetupFunnel(ctx, req)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) takeSetupFunnel(ctx context.Context, req api.SetupFunnelReport) {
	setupMu.Lock()
	defer setupMu.Unlock()
	if at, err := s.store.Setting(ctx, settingUsageSetupAt); err == nil && req.Began > 0 {
		if began, err := strconv.ParseInt(at, 10, 64); err == nil && req.Began < began {
			_ = s.store.SetSetting(ctx, settingUsageSetupAt, strconv.FormatInt(req.Began, 10))
		}
	}
	steps := slices.Clone(req.Steps)
	slices.SortStableFunc(steps, func(a, b api.SetupFunnelStep) int { return int(a.At - b.At) })
	for _, st := range steps {
		if (st.Step != setupKVM && st.Step != setupVMReady) || !slices.Contains(setupOutcomes, st.Outcome) ||
			(st.Step != setupKVM && (st.Outcome == setupKVMMissing || st.Outcome == setupKVMForbidden)) {
			continue
		}
		rep := setupReport{step: st.Step, outcome: st.Outcome, took: time.Duration(st.DurationS) * time.Second, at: time.Unix(st.At, 0)}
		switch st.Outcome {
		case setupKVMMissing:
			rep.class = classNotFound
		case setupKVMForbidden:
			rep.class = classPermission
		case setupFailed:
			rep.class = errorClass(nil, st.Error)
		}
		s.setupReachedLocked(ctx, rep)
	}
}
