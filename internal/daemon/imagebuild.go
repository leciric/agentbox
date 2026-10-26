package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/image"
)

// Building the base image never holds anything up. It runs as a job the
// daemon remembers (Server.imageBuild), so the app can show it in the top bar
// wherever you are, and Setup counts the step as done while it runs. What
// needs the image — making an agent, or an agent's machine again — waits for
// that job, or starts one, rather than failing (awaitImage). A rebuild makes
// agentbox-base-next and swaps it in at the end (image.Build), so agents keep
// being made from the old image until the new one is ready.

func (s *Server) imageStatus(w http.ResponseWriter, r *http.Request) error {
	out, err := s.imageBuildStatus(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

// imageBuildStatus is the base image as the top bar shows it: whether agents
// can be made from it, and the build you started last.
func (s *Server) imageBuildStatus(ctx context.Context) (api.ImageStatus, error) {
	ready, err := image.Ready(ctx, s.cfg.Incus)
	if err != nil {
		return api.ImageStatus{}, err
	}
	out := api.ImageStatus{Ready: ready, Snapshot: image.SnapshotRef(), Build: api.ImageBuildIdle}
	j := s.lastImageBuild()
	if j == nil {
		return out, nil
	}
	info := j.snapshot()
	out.Job = info.ID
	switch info.Status {
	case api.JobRunning:
		out.Build, out.Step = api.ImageBuilding, lastStep(j.logLines())
		out.Waiting = s.waitingForImage()
	case api.JobFailed:
		// Until a build succeeds: one that did leaves nothing to say.
		out.Build, out.Error = api.ImageBuildFailed, info.Error
	}
	return out, nil
}

// lastStep is the newest "==>" line of a build's log: what it is doing now.
func lastStep(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		if step, ok := strings.CutPrefix(lines[i], "==> "); ok {
			return step
		}
	}
	return ""
}

func (s *Server) buildImage(w http.ResponseWriter, r *http.Request) error {
	var req api.BuildImageRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	j, err := s.startImageBuild(r.Context(), req)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusAccepted, j)
}

// startImageBuild starts the job that makes the base image: from the
// published one when it can (#275), with the Codex and OpenCode tools added
// on top, built here for the other components or when that fails.
func (s *Server) startImageBuild(ctx context.Context, req api.BuildImageRequest) (api.Job, error) {
	if err := s.diskRefusal("building the base image"); err != nil {
		return api.Job{}, err
	}
	if err := image.CheckHost(s.cfg.User); err != nil {
		return api.Job{}, err
	}
	components, err := s.chooseImageComponents(ctx, req)
	if err != nil {
		return api.Job{}, err
	}
	opts := image.Options{Components: components, DownloadDir: filepath.Join(s.cfg.Paths.Data, "images")}
	if !req.Local {
		opts.ImagesURL = image.ImagesURL()
	}
	// One job at a time makes the next image: the daemon may be updating the
	// agent tools, or another build be under way.
	if !s.claimImage() {
		if j := s.runningImageBuild(); j != nil {
			return api.Job{}, fmt.Errorf("the base image is already being built (job %s): follow it from the top bar", j.snapshot().ID)
		}
		return api.Job{}, fmt.Errorf("the base image is being updated in the background: follow it in Setup, and build again once it's done")
	}
	j, err := s.launchJob("image-build", image.SnapshotRef(), func(ctx context.Context, log io.Writer) (any, error) {
		defer s.releaseImage()
		ready := setupReport{step: setupImageReady}
		opts.Source = func(source string) { ready.imageSource = source }
		began := time.Now()
		err := image.Build(ctx, s.cfg.Incus, s.cfg.User, opts, log)
		ready.took = time.Since(began)
		if err != nil {
			// One the job's cancel stopped didn't fail.
			if ctx.Err() == nil {
				s.recordErr(errImageBuildFailed, "", err)
			}
			s.setupStepFailed(context.WithoutCancel(ctx), ready, err)
			return nil, err
		}
		s.countFeature(api.FeatureImageBuild)
		s.setupReached(ctx, ready)
		// A build you made settles an update of the daemon's that failed or
		// was cancelled.
		s.setImagePhase(func(w *imageWork) { *w = imageWork{busy: w.busy, build: w.build} })
		return map[string]string{"snapshot": image.SnapshotRef()}, nil
	})
	if err != nil {
		s.releaseImage()
		return api.Job{}, err
	}
	s.setImagePhase(func(w *imageWork) { w.build = j.ID })
	return j, nil
}

// lastImageBuild is the build this daemon started last, running or not.
func (s *Server) lastImageBuild() *job {
	id := s.imageState().build
	if id == "" || s.jobs == nil {
		return nil
	}
	j, _ := s.jobs.get(id)
	return j
}

// runningImageBuild is the build under way, if there is one.
func (s *Server) runningImageBuild() *job {
	if j := s.lastImageBuild(); j != nil && !j.snapshot().Done() {
		return j
	}
	return nil
}

func (s *Server) waitingForImage() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.imageWaiters
}

// awaitImage returns once agents can be made from the base image. While it
// is being built it waits for the build; when there is none and no build, it
// starts one, the way Setup would. Only the build failing fails it, with the
// build's own error.
func (s *Server) awaitImage(ctx context.Context, log io.Writer) error {
	ready, err := image.Ready(ctx, s.cfg.Incus)
	if err != nil || ready {
		return err
	}
	j := s.runningImageBuild()
	if j == nil {
		started, err := s.startImageBuild(ctx, api.BuildImageRequest{})
		if err != nil {
			return fmt.Errorf("the base image isn't built, and building it couldn't start: %w", err)
		}
		if j, _ = s.jobs.get(started.ID); j == nil {
			return errors.New("the base image build vanished as it started")
		}
		_, _ = fmt.Fprintf(log, "==> The base image isn't built yet: building it (job %s). This carries on by itself once it's ready\n", started.ID)
	} else {
		_, _ = fmt.Fprintf(log, "==> Waiting for the base image, which is being built (job %s). This carries on by itself once it's ready\n", j.snapshot().ID)
	}
	s.mu.Lock()
	s.imageWaiters++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.imageWaiters--
		s.mu.Unlock()
	}()
	info, err := j.follow(ctx, 0, func(string) error { return nil })
	if err != nil {
		return err
	}
	if info.Status != api.JobSucceeded {
		return fmt.Errorf("the base image build failed (job %s): %s. Retry it from the top bar, or with agentbox image build", info.ID, buildError(info))
	}
	_, _ = fmt.Fprintln(log, "==> The base image is ready")
	return nil
}

// buildError is the first line of why a build failed.
func buildError(info api.Job) string {
	line, _, _ := strings.Cut(strings.TrimSpace(info.Error), "\n")
	return cmpOr(line, "no reason given")
}
