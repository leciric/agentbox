package daemon

import (
	"context"
	"fmt"
	"io"
	"strings"

	"agentbox/internal/api"
	"agentbox/internal/image"
)

// The daemon moves the base image's agent tools on by itself: when the image
// was provisioned by this AgentBox's image version, with the components the
// installation wants, and only its tools differ from tools.txt, it updates
// them in place in the background (image.UpdateTools). If that fails it builds
// the image again, in the background too. Either way the current base stays
// in use until the new one replaces it. A different image version or other
// components still ask you for a rebuild, as Setup always has.

// imageWork is what the daemon is doing to the base image, under Server.mu.
type imageWork struct {
	// busy is set while any job works on the base image, a build you started
	// included: they all make agentbox-base-next, so only one may run.
	busy bool
	// phase is where the daemon's own update has got to, job the job doing
	// it, and toolsErr and buildErr why each step of it failed.
	phase              imagePhase
	job                string
	toolsErr, buildErr error
}

type imagePhase int

const (
	imageIdle          imagePhase = iota
	imageUpdatingTools            // updating the tools in place
	imageRebuilding               // building again, after the in-place update failed
	imageUpdateFailed             // both failed: it's yours to rebuild, and Setup says so
)

// claimImage reserves the base image for one job; false means another has it.
func (s *Server) claimImage() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.image.busy {
		return false
	}
	s.image.busy = true
	return true
}

func (s *Server) releaseImage() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.image.busy = false
}

func (s *Server) imageState() imageWork {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.image
}

func (s *Server) setImagePhase(update func(w *imageWork)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	update(&s.image)
}

// basePlan is what the base image on this machine needs.
func (s *Server) basePlan(ctx context.Context) (image.Plan, image.Installed, error) {
	built, err := image.Ready(ctx, s.cfg.Incus)
	if err != nil {
		return image.Plan{}, image.Installed{}, err
	}
	var installed image.Installed
	if built {
		if installed, err = image.InstalledBuild(ctx, s.cfg.Incus); err != nil {
			return image.Plan{}, image.Installed{}, err
		}
	}
	wanted, err := s.imageComponents(ctx)
	if err != nil {
		return image.Plan{}, image.Installed{}, err
	}
	return image.PlanFor(built, installed, wanted), installed, nil
}

// updateBaseTools starts updating the base image's tools when that is all it
// needs. Run calls it at start, which is also when an upgraded AgentBox first
// runs.
func (s *Server) updateBaseTools(ctx context.Context) {
	plan, installed, err := s.basePlan(ctx)
	if err != nil {
		return
	}
	s.startToolsUpdate(plan, installed)
}

// startToolsUpdate starts updating the base image's tools, when plan says that
// is all it needs and nothing else is working on it. It is safe to call again
// and again, and Setup does whenever it finds the tools behind. After both the
// update and the rebuild failed, it leaves the image to you until the daemon
// starts again.
func (s *Server) startToolsUpdate(plan image.Plan, installed image.Installed) {
	if plan.Action != image.NeedsTools || s.jobs == nil {
		return
	}
	s.mu.Lock()
	if s.image.busy || s.image.phase == imageUpdateFailed {
		s.mu.Unlock()
		return
	}
	s.image = imageWork{busy: true, phase: imageUpdatingTools}
	s.mu.Unlock()

	j, err := s.jobs.start("image-tools", image.SnapshotRef(), func(ctx context.Context, log io.Writer) (any, error) {
		s.logf("job image-tools started")
		err := image.UpdateTools(ctx, s.cfg.Incus, s.cfg.User, plan, log)
		if err == nil {
			s.setImagePhase(func(w *imageWork) { *w = imageWork{} })
			return map[string]string{"snapshot": image.SnapshotRef(), "toolsVersion": plan.ToolsVersion}, nil
		}
		s.logf("job image-tools failed: %v", err)
		if ctx.Err() != nil {
			// Cancelled, or the daemon is stopping: the next start tries again.
			s.setImagePhase(func(w *imageWork) { *w = imageWork{} })
			return nil, err
		}
		fmt.Fprintf(log, "==> %v\n==> Building the image again instead, in a job of its own; agents keep using the current one meanwhile\n", err)
		// The image stays claimed: the rebuild takes it over.
		s.rebuildBase(installed.Components, err)
		return nil, err
	})
	if err != nil {
		s.setImagePhase(func(w *imageWork) { *w = imageWork{} })
		s.logf("image-tools: %v", err)
		return
	}
	s.setImagePhase(func(w *imageWork) {
		if w.phase == imageUpdatingTools && w.job == "" {
			w.job = j.info.ID
		}
	})
}

// rebuildBase builds the base image again after updating its tools in place
// failed, with the components it has. image.Build keeps the current base
// until the new one is ready, and keeps it for good if the build fails too.
func (s *Server) rebuildBase(components image.Components, toolsErr error) {
	s.setImagePhase(func(w *imageWork) { *w = imageWork{busy: true, phase: imageRebuilding, toolsErr: toolsErr} })
	j, err := s.jobs.start("image-build", image.SnapshotRef(), func(ctx context.Context, log io.Writer) (any, error) {
		s.logf("job image-build started, after updating the agent tools in place failed")
		err := image.Build(ctx, s.cfg.Incus, s.cfg.User, image.Options{Components: components}, log)
		if err != nil {
			s.logf("job image-build failed: %v", err)
			s.setImagePhase(func(w *imageWork) { *w = imageWork{phase: imageUpdateFailed, toolsErr: toolsErr, buildErr: err} })
			return nil, err
		}
		s.setImagePhase(func(w *imageWork) { *w = imageWork{} })
		return map[string]string{"snapshot": image.SnapshotRef()}, nil
	})
	if err != nil {
		s.setImagePhase(func(w *imageWork) { *w = imageWork{phase: imageUpdateFailed, toolsErr: toolsErr, buildErr: err} })
		return
	}
	s.setImagePhase(func(w *imageWork) {
		if w.phase == imageRebuilding && w.job == "" {
			w.job = j.info.ID
		}
	})
}

// baseImageCheck is Setup's check of the base image.
func (s *Server) baseImageCheck(plan image.Plan, installed image.Installed, wanted image.Components) api.SetupCheck {
	base := api.SetupCheck{ID: "image", Title: "Base image", Required: true, Status: api.SetupMissing, Fix: "agentbox image build"}
	switch plan.Action {
	case image.NeedsBuild:
		base.Detail = "not built yet"
	case image.NeedsRebuild:
		base.Status = api.SetupOutdated
		if installed.Version != image.Version {
			base.Detail = fmt.Sprintf("built by an older AgentBox (version %s, now %s): rebuild it for the latest agent tools", cmpOr(installed.Version, "unknown"), image.Version)
		} else {
			// A component turned on since the build asks for a rebuild the same
			// way a version bump does: it is in the settings but not in the
			// image, and only a build puts it there.
			base.Detail = fmt.Sprintf("built with %s, and you now want %s: rebuild it", installed.Components.Summary(), wanted.Summary())
		}
	case image.NeedsTools:
		s.startToolsUpdate(plan, installed)
		work := s.imageState()
		switch work.phase {
		case imageUpdateFailed:
			base.Status = api.SetupOutdated
			base.Detail = fmt.Sprintf("its agent tools are out of date, and updating them failed: %s; rebuilding it failed too: %s. Rebuild it", firstLine(work.toolsErr), firstLine(work.buildErr))
		case imageRebuilding:
			base.Status, base.Job = api.SetupUpdating, work.job
			base.Detail = fmt.Sprintf("Rebuilding it in the background, since updating the agent tools in place failed (%s). Agents keep using the current image until the new one is ready", firstLine(work.toolsErr))
		default:
			// Updating, or about to: another job may hold the image for now.
			base.Status, base.Job = api.SetupUpdating, work.job
			base.Detail = "Updating agent tools… " + describeTools(plan) + ". Agents keep using the current image until it's done"
		}
	default:
		base.Status = api.SetupOK
		base.Detail = "ready, version " + installed.Version + ", agent tools " + installed.ToolsVersion
		if wanted != (image.Components{}) {
			base.Detail += ", with " + wanted.Summary()
		}
	}
	return base
}

// describeTools says what an update installs and removes.
func describeTools(plan image.Plan) string {
	var parts []string
	if n := len(plan.Install); n == len(plan.Tools) {
		parts = append(parts, "all of them, to record which the image has")
	} else if n > 0 {
		specs := make([]string, 0, n)
		for _, t := range plan.Install {
			specs = append(specs, t.Spec)
		}
		parts = append(parts, "installing "+strings.Join(specs, ", "))
	}
	if n := len(plan.Remove); n > 0 {
		specs := make([]string, 0, n)
		for _, t := range plan.Remove {
			specs = append(specs, t.Spec)
		}
		parts = append(parts, "removing "+strings.Join(specs, ", "))
	}
	return "(" + strings.Join(parts, "; ") + ")"
}
