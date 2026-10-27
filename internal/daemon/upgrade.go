package daemon

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// Upgrading a cluster changes the image its pods run and recreates them one at a
// time, keeping the one image it ran before for `--rollback`. The image is the
// cluster's, not an agent's: an agent's pod runs what its cluster points at, and
// a row's own pinned image (one a restore wrote) is dropped when its pod is
// recreated.

// digestPrefix is what a full image digest starts with. Only sha256 is accepted:
// it is what the registry and the container runtime both report here.
const digestPrefix = "sha256:"

// upgradeCluster is POST /v1/clusters/{name}/upgrade: run the cluster's image at
// a new digest and recreate its pods one at a time, or roll it back.
func (s *Server) upgradeCluster(w http.ResponseWriter, r *http.Request) error {
	var req api.UpgradeClusterRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	req.Digest = strings.TrimSpace(req.Digest)
	ctx := r.Context()
	name := r.PathValue("name")
	cluster, err := s.store.Cluster(ctx, name)
	if err != nil {
		return err
	}
	// Refused before the job, because nothing has happened yet: the digest and
	// the image it composes are both decided here, and a bad one is a sentence
	// rather than a job that fails at once.
	if _, _, err := upgradeImages(cluster, req); err != nil {
		return err
	}
	return s.startJob(w, "upgrade", name, func(ctx context.Context, log io.Writer) (any, error) {
		return s.runUpgrade(ctx, log, cluster.Name, req)
	})
}

// upgradeImages is what an upgrade or a rollback writes: the image the cluster
// runs now, and the one it ran before, in that order. An upgrade keeps the tag
// the cluster's image was added with and appends the digest, so a rollback and a
// second rollback are both the same two strings swapped.
func upgradeImages(c state.Cluster, req api.UpgradeClusterRequest) (image, previous string, err error) {
	switch {
	case req.Rollback && req.Digest != "":
		return "", "", fmt.Errorf("cluster %s: pass a digest or --rollback, not both", c.Name)
	case !req.Rollback && req.Digest == "":
		return "", "", fmt.Errorf("cluster %s: pass a digest like sha256:... to upgrade it, or --rollback to go back to the image it ran before", c.Name)
	case req.Rollback:
		if c.PreviousImage == "" {
			return "", "", fmt.Errorf("cluster %s has not been upgraded: there is nothing to roll back to", c.Name)
		}
		// The image just replaced becomes the previous one, so a second
		// rollback goes forward again.
		return c.PreviousImage, c.Image, nil
	}
	image, err = imageAtDigest(c, req.Digest)
	if err != nil {
		return "", "", err
	}
	return image, c.Image, nil
}

// imageAtDigest is the cluster's image name at a digest: the tag it was added
// with, and the digest instead of any digest it already carried. An upgrade
// moves the same image, so a cluster that runs agentbox-agent:dev keeps that
// name and only the digest changes.
func imageAtDigest(c state.Cluster, digest string) (string, error) {
	if c.Image == "" {
		return "", fmt.Errorf("cluster %s has no image to upgrade: set one with agentbox cluster set %s --image <name>:<tag>", c.Name, c.Name)
	}
	if err := checkDigest(digest); err != nil {
		return "", fmt.Errorf("cluster %s's digest %q: %w", c.Name, digest, err)
	}
	name, _, _ := strings.Cut(c.Image, "@")
	return name + "@" + digest, nil
}

// checkDigest accepts a full image digest and nothing else: what a registry
// prints after "digest:", so a tag, a short digest or a digest of another
// algorithm is refused rather than written into a pod spec.
func checkDigest(digest string) error {
	hexPart, ok := strings.CutPrefix(digest, digestPrefix)
	if !ok {
		return fmt.Errorf("a digest is %s and 64 hex characters, as the registry prints it", digestPrefix)
	}
	if len(hexPart) != 64 {
		return fmt.Errorf("a %s digest is 64 hex characters after it, and this one has %d", digestPrefix, len(hexPart))
	}
	if _, err := hex.DecodeString(hexPart); err != nil {
		return errors.New("the part after " + digestPrefix + " is not hexadecimal")
	}
	if hexPart != strings.ToLower(hexPart) {
		return errors.New("a digest is lowercase hexadecimal")
	}
	return nil
}

// runUpgrade is the job: record the new image first, then recreate every running
// agent's pod at it, one at a time. Recording first is what keeps a reconciler
// round from making a pod at the old image between a stop and a start.
func (s *Server) runUpgrade(ctx context.Context, log io.Writer, name string, req api.UpgradeClusterRequest) (any, error) {
	cluster, err := s.store.Cluster(ctx, name)
	if err != nil {
		return nil, err
	}
	image, previous, err := upgradeImages(cluster, req)
	if err != nil {
		return nil, err
	}
	if err := s.store.SetClusterImage(ctx, name, image, previous); err != nil {
		return nil, err
	}
	if req.Rollback {
		_, _ = fmt.Fprintf(log, "Rolling %s back to %s.\n", name, image)
	} else {
		_, _ = fmt.Fprintf(log, "Upgrading %s to %s.\n", name, image)
	}
	agents, err := s.store.AgentsOnCluster(ctx, name)
	if err != nil {
		return nil, err
	}
	// A pod that is not recreated keeps the image it was made with, which is
	// the point of doing this one at a time: the others go on running while one
	// changes over.
	m := s.manager(log)
	for _, a := range agents {
		if a.IsLead() {
			continue
		}
		if !wantsRunning(a) {
			// A stopped agent has no pod to replace and starts at the new
			// image on its own; its claim keeps its work either way.
			_, _ = fmt.Fprintf(log, "%s is stopped: it runs the new image when it starts\n", a.Ref())
			continue
		}
		_, _ = fmt.Fprintf(log, "Recreating %s at %s...\n", a.Ref(), image)
		if err := m.Recreate(ctx, a, agent.RecreateOptions{}); err != nil {
			return nil, fmt.Errorf("upgrading %s failed: %w; the cluster's image is %s now, and the pods recreated before this one run it: run agentbox cluster upgrade %s --rollback to go back to %s",
				a.Ref(), err, image, name, previous)
		}
		_, _ = fmt.Fprintf(log, "Recreated %s at %s\n", a.Ref(), image)
	}
	cluster, err = s.store.Cluster(ctx, name)
	if err != nil {
		return nil, err
	}
	return s.clusterInfo(cluster), nil
}
