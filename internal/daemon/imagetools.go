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
// them in place in the background (image.UpdateTools). The current base stays
// in use until the updated one replaces it. If the update fails, the base is
// left as it was and Setup says why and offers the rebuild: a rebuild
// downloads gigabytes, and what failed the update (the network, a pin whose
// check fails) would usually fail it too, so it is yours to start. A
// different image version or other components ask you for a rebuild, as
// Setup always has.

// imageWork is what the daemon is doing to the base image, under Server.mu.
type imageWork struct {
	// busy is set while any job works on the base image, a build you started
	// included: they all make agentbox-base-next, so only one may run.
	busy bool
	// phase is where the daemon's own update has got to, job the job doing
	// it, and err why it failed.
	phase imagePhase
	job   string
	err   error
}

type imagePhase int

const (
	imageIdle            imagePhase = iota
	imageUpdatingTools              // updating the tools in place
	imageUpdateFailed               // it failed: the old base stays, and rebuilding is yours
	imageUpdateCancelled            // it was cancelled: the old base stays until you rebuild or AgentBox starts again
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
	plan, _, err := s.basePlan(ctx)
	if err != nil {
		return
	}
	s.startToolsUpdate(plan)
}

// startToolsUpdate starts updating the base image's tools, when plan says that
// is all it needs and nothing else is working on it. It is safe to call again
// and again, and Setup does whenever it finds the tools behind. After an
// update failed or was cancelled it leaves the image to you, until a build
// you start succeeds or the daemon starts again.
func (s *Server) startToolsUpdate(plan image.Plan) {
	if plan.Action != image.NeedsTools || s.jobs == nil {
		return
	}
	s.mu.Lock()
	if s.image.busy || s.image.phase == imageUpdateFailed || s.image.phase == imageUpdateCancelled {
		s.mu.Unlock()
		return
	}
	s.image = imageWork{busy: true, phase: imageUpdatingTools}
	s.mu.Unlock()

	j, err := s.jobs.start("image-tools", image.SnapshotRef(), func(ctx context.Context, log io.Writer) (any, error) {
		s.logf("job image-tools started")
		// The plan that started this may be stale by now: another job may have
		// changed the image since. Now that the image is ours, look again.
		plan, _, err := s.basePlan(ctx)
		if err == nil && plan.Action == image.NeedsTools {
			err = image.UpdateTools(ctx, s.cfg.Incus, s.cfg.User, plan, log)
		}
		switch {
		case err == nil:
			s.setImagePhase(func(w *imageWork) { *w = imageWork{} })
			return map[string]string{"snapshot": image.SnapshotRef(), "toolsVersion": plan.ToolsVersion}, nil
		case ctx.Err() != nil:
			// Cancelled, from Setup or because the daemon is stopping: not
			// again until the next start, or a cancel would only restart it.
			s.setImagePhase(func(w *imageWork) { *w = imageWork{phase: imageUpdateCancelled} })
		default:
			s.logf("job image-tools failed: %v", err)
			_, _ = fmt.Fprintf(log, "==> %v\n==> Agents keep using the current image. Rebuild it from Setup to get the new tools\n", err)
			s.setImagePhase(func(w *imageWork) { *w = imageWork{phase: imageUpdateFailed, err: err} })
		}
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
		s.startToolsUpdate(plan)
		work := s.imageState()
		// The image agents use still works, so neither of these holds anything
		// up: they say why its tools are behind, and offer the rebuild.
		switch work.phase {
		case imageUpdateFailed:
			base.Status = api.SetupWarn
			base.Detail = fmt.Sprintf("updating its agent tools failed: %s. Agents keep using it as it is; rebuild it for the new tools", firstLine(work.err))
			return base
		case imageUpdateCancelled:
			base.Status = api.SetupWarn
			base.Detail = "updating its agent tools was cancelled. Agents keep using it as it is; rebuild it for the new tools, or restart AgentBox to update them in place"
			return base
		}
		// Updating, or about to: another job may hold the image for now.
		base.Status, base.Job = api.SetupUpdating, work.job
		base.Detail = "Updating agent tools… " + describeTools(plan) + ". Agents keep using the current image until it's done"
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
