package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"agentbox/internal/gateway"
	"agentbox/internal/k8s"
	"agentbox/internal/machine"
	"agentbox/internal/state"
)

// A cluster agent's machine is its claim, and the cluster keeps no copy of it.
// A snapshot is therefore an archive of that claim streamed to this host, with a
// small JSON file beside it saying what it was taken from: the archive outlives
// the claim, and a restore or a fork reads it back into a claim of its own. The
// git half of a snapshot is the ref internal/agent keeps, so nothing about the
// worktree is in here.
const (
	// podStopTimeout is how long a snapshot or a restore waits for the pod to be
	// gone. Deleting one asks agentd to checkpoint first, inside its 60-second
	// grace period, and the reconciler only deletes it on its next round.
	podStopTimeout = 3 * time.Minute
	// snapshotPoll is how often the loops above ask the cluster again.
	snapshotPoll = 2 * time.Second
)

// snapshotMeta is the file beside an archive. The image is what the pod ran, so
// a restore puts the same machine back rather than whatever the cluster's tag
// points at today; the ID is the digest the node reported, kept so the archive
// says exactly what built the tools inside it.
type snapshotMeta struct {
	Name      string    `json:"name"`
	Ref       string    `json:"ref"`
	Commit    string    `json:"commit,omitempty"`
	Head      string    `json:"head,omitempty"`
	Image     string    `json:"image"`
	ImageID   string    `json:"imageID,omitempty"`
	Bytes     int64     `json:"bytes"`
	CreatedAt time.Time `json:"createdAt"`

	// dir is where this file was read from, which is the agent's snapshot
	// directory; it is not in the JSON, so moving the snapshots directory
	// takes every archive with it.
	dir string
}

// Snapshot archives an agent's claim to this host. The machine snapshot is
// consistent by construction for a pod: the claim is ReadWriteOnce, so the pod
// is stopped before a helper mounts it, and started again afterwards when it was
// running.
func (p podLifecycle) Snapshot(ctx context.Context, ref gateway.Ref, name string) error {
	if err := p.s.checkAgentFree(ctx, ref, "snapshot"); err != nil {
		return err
	}
	a, cluster, reg, client, err := p.target(ctx, ref)
	if err != nil {
		return err
	}
	if err := checkSnapshotName(name); err != nil {
		return err
	}
	dir := filepath.Join(p.s.cfg.Paths.Snapshots(), a.Project, a.Name)
	archive := filepath.Join(dir, name+".tar.gz")
	if _, err := os.Stat(archive); err == nil {
		return fmt.Errorf("%s already has a snapshot named %q", a.Ref(), name)
	}
	image, imageID, err := p.agentImage(ctx, a, client)
	if err != nil {
		return err
	}
	spec := p.s.podSpec(ctx, cluster, reg, a)
	if image == "" {
		image = spec.Image
	}
	spec.Image = image

	wasRunning, err := p.stopForClaim(ctx, client, a, "the claim cannot be archived while it is mounted")
	restarted := false
	defer func() {
		if wasRunning && !restarted {
			// The snapshot failed: the agent goes back to what it was doing.
			// Its error is the one the caller needs, so this one is only logged.
			if err := p.restart(context.WithoutCancel(ctx), a); err != nil {
				p.s.logf("starting %s again after a failed snapshot: %v", a.Ref(), err)
			}
		}
	}()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("making %s's snapshot directory: %w", a.Ref(), err)
	}
	tmp := archive + ".tmp"
	file, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("writing %s's snapshot: %w", a.Ref(), err)
	}
	archiveErr := client.ArchiveClaim(ctx, spec, image, name, file)
	if archiveErr == nil {
		archiveErr = file.Sync()
	}
	if closeErr := file.Close(); archiveErr == nil {
		archiveErr = closeErr
	}
	if archiveErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("archiving %s: %w", a.Ref(), archiveErr)
	}
	var size int64
	if info, err := os.Stat(tmp); err == nil {
		size = info.Size()
	}
	if err := os.Rename(tmp, archive); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("writing %s's snapshot: %w", a.Ref(), err)
	}
	meta := snapshotMeta{
		Name:      name,
		Ref:       "refs/agentbox/snapshots/" + a.Name + "/" + name,
		Image:     image,
		ImageID:   imageID,
		Bytes:     size,
		CreatedAt: time.Now().UTC(),
	}
	if err := writeSnapshotMeta(dir, meta); err != nil {
		_ = os.Remove(archive)
		return fmt.Errorf("writing %s's snapshot: %w", a.Ref(), err)
	}
	if wasRunning {
		if err := p.restart(ctx, a); err != nil {
			restarted = true // it was tried; the defer must not try twice
			return fmt.Errorf("the snapshot of %s is taken, and starting it again failed: %w", a.Ref(), err)
		}
		restarted = true
	}
	return nil
}

// stopForClaim makes sure no pod has the agent's claim mounted, which is what a
// snapshot or a restore needs before a helper mounts it or a new claim replaces
// it. It goes by what the row asks for and what the cluster has, never by the
// pod the row names: a stopped agent that was just started (a restore of one
// does that for its pre-restore ref) has a pod the reconciler hasn't adopted
// yet, and one just stopped may still be there. wasRunning is whether the row
// asked for a pod, which is whether the caller starts it again afterwards; it
// is set even with an error, once the row has been asked to stop.
func (p podLifecycle) stopForClaim(ctx context.Context, client *k8s.Cluster, a state.Agent, why string) (wasRunning bool, err error) {
	if a.DesiredState != state.DesiredStopped {
		if err := p.s.store.SetAgentDesiredState(ctx, a.Project, a.Name, state.DesiredStopped); err != nil {
			return false, err
		}
		wasRunning = true
	}
	if err := waitPodGone(ctx, client, a, why); err != nil {
		return wasRunning, err
	}
	// A session the deleted pod left behind would answer the WaitFor of the
	// pod that replaces it.
	p.s.awaitSessionGone(ctx, a, 30*time.Second)
	return wasRunning, nil
}

// restart asks the reconciler for a pod again and waits for its agentd.
func (p podLifecycle) restart(ctx context.Context, a state.Agent) error {
	if err := p.s.store.SetAgentDesiredState(ctx, a.Project, a.Name, state.DesiredRunning); err != nil {
		return err
	}
	return p.s.gateway.WaitFor(ctx, gateway.Ref{Project: a.Project, Agent: a.Name}, podStopTimeout)
}

// agentImage is the image the agent's pod runs now, and the digest the node
// reported for it. A stopped agent has no pod, so its row's image, or the
// cluster's current one, is what the next pod would run.
func (p podLifecycle) agentImage(ctx context.Context, a state.Agent, client *k8s.Cluster) (image, imageID string, err error) {
	pod, getErr := client.Get(ctx, a.Project, a.Name)
	switch {
	case getErr == nil:
		image = pod.Image
		imageID = pod.ImageID
	case errors.Is(getErr, k8s.ErrNotFound):
		// A stopped agent still has a claim to archive.
	default:
		return "", "", getErr
	}
	if image == "" {
		image = a.Image
	}
	return image, imageID, nil
}

// Snapshots lists what this host holds for an agent, oldest first.
func (p podLifecycle) Snapshots(ctx context.Context, ref gateway.Ref) ([]machine.Snapshot, error) {
	dir := filepath.Join(p.s.cfg.Paths.Snapshots(), ref.Project, ref.Agent)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []machine.Snapshot
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		meta, err := readSnapshotMeta(dir, name)
		if err != nil {
			// A metadata file with no archive is a snapshot that failed partway
			// or a file somebody removed: it isn't one to offer.
			continue
		}
		out = append(out, machine.Snapshot{Name: meta.Name, CreatedAt: meta.CreatedAt, Head: meta.Head})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// RestoreSnapshot puts an agent back on an archive. A restore never writes over
// the claim the agent has: it prepares a new one, moves the row to it, and only
// deletes the old claim once the new pod is up. A failure therefore leaves the
// agent on the work it had.
func (p podLifecycle) RestoreSnapshot(ctx context.Context, ref gateway.Ref, name string) error {
	if err := p.s.checkAgentFree(ctx, ref, "restore"); err != nil {
		return err
	}
	a, cluster, reg, client, err := p.target(ctx, ref)
	if err != nil {
		return err
	}
	if err := checkSnapshotName(name); err != nil {
		return err
	}
	meta, err := p.readSnapshot(a, name)
	if err != nil {
		return err
	}
	wasRunning, err := p.stopForClaim(ctx, client, a, "its claim cannot be replaced while it is mounted")
	done := false
	defer func() {
		if wasRunning && !done {
			if err := p.restart(context.WithoutCancel(ctx), a); err != nil {
				p.s.logf("starting %s again after a failed restore: %v", a.Ref(), err)
			}
		}
	}()
	if err != nil {
		return err
	}
	spec := p.s.podSpec(ctx, cluster, reg, a)
	oldClaim := spec.WorkspaceClaim()
	newClaim := nextClaimName(oldClaim)
	spec.Claim = newClaim
	spec.Image = p.imageOf(meta, spec.Image)
	if err := p.prepareClaim(ctx, a, client, spec, meta, newClaim); err != nil {
		return err
	}
	// The row is what the reconciler makes the next pod from, and it now names
	// the new claim and the image the snapshot was taken with.
	if err := p.s.store.SetAgentClaim(ctx, a.Project, a.Name, newClaim, spec.Image); err != nil {
		_ = client.DeleteClaim(context.WithoutCancel(ctx), newClaim)
		return err
	}
	if err := p.s.store.SetAgentDesiredState(ctx, a.Project, a.Name, state.DesiredRunning); err != nil {
		return err
	}
	done = true
	if err := p.s.gateway.WaitFor(ctx, ref, podStopTimeout); err != nil {
		return fmt.Errorf("%s is on the claim from %q, and its pod did not come up: %w (the claim it had is still there as %s)", a.Ref(), name, err, oldClaim)
	}
	// The gateway says the pod is up; the row is how everything else reads
	// whether it is. The reconciler writes it on its next round.
	if err := waitAdopted(ctx, p.s.store, a); err != nil {
		p.s.logf("restoring %s: %v", a.Ref(), err)
	}
	// The new machine is up; the old volume is what the snapshot replaced.
	if err := client.DeleteClaim(context.WithoutCancel(ctx), oldClaim); err != nil {
		p.s.logf("deleting %s's old claim %s: %v", a.Ref(), oldClaim, err)
	}
	return nil
}

// DeleteSnapshot forgets an archive and its metadata. A snapshot the host no
// longer has is done, not an error.
func (p podLifecycle) DeleteSnapshot(ctx context.Context, ref gateway.Ref, name string) error {
	if err := checkSnapshotName(name); err != nil {
		return err
	}
	dir := filepath.Join(p.s.cfg.Paths.Snapshots(), ref.Project, ref.Agent)
	for _, file := range []string{filepath.Join(dir, name+".tar.gz"), filepath.Join(dir, name+".json")} {
		if err := os.Remove(file); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// HasSnapshot reports whether this host holds that agent's archive.
func (p podLifecycle) HasSnapshot(ctx context.Context, ref gateway.Ref, name string) (bool, error) {
	if err := checkSnapshotName(name); err != nil {
		return false, err
	}
	_, err := os.Stat(filepath.Join(p.s.cfg.Paths.Snapshots(), ref.Project, ref.Agent, name+".tar.gz"))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// SnapshotGit records the worktree half of a snapshot in its metadata, which is
// where a pod's must live: the worktree is on the claim, and the host has no ref
// for it. It is called after Snapshot, so the metadata is already there.
func (p podLifecycle) SnapshotGit(ctx context.Context, ref gateway.Ref, name, refName, commit, head string) error {
	if err := checkSnapshotName(name); err != nil {
		return err
	}
	dir := filepath.Join(p.s.cfg.Paths.Snapshots(), ref.Project, ref.Agent)
	meta, err := readSnapshotMeta(dir, name)
	if err != nil {
		return fmt.Errorf("%s has no snapshot %q to record the worktree of: %w", ref, name, err)
	}
	meta.Ref, meta.Commit, meta.Head = refName, commit, head
	return writeSnapshotMeta(dir, meta)
}

// SnapshotGitInfo reads that record back, which works without the pod being up.
func (p podLifecycle) SnapshotGitInfo(ctx context.Context, ref gateway.Ref, name string) (string, string, error) {
	if err := checkSnapshotName(name); err != nil {
		return "", "", err
	}
	meta, err := readSnapshotMeta(filepath.Join(p.s.cfg.Paths.Snapshots(), ref.Project, ref.Agent), name)
	if err != nil {
		return "", "", fmt.Errorf("%s has no snapshot %q: %w", ref, name, err)
	}
	if meta.Commit == "" {
		return "", "", fmt.Errorf("%s's snapshot %q has no worktree recorded: it was taken before the archive carried one", ref, name)
	}
	return meta.Commit, meta.Head, nil
}

// PrepareClaim primes a new agent's claim from a snapshot another agent took,
// which is how a fork starts with the machine its source had. from is
// project/agent@snapshot.
func (p podLifecycle) PrepareClaim(ctx context.Context, ref gateway.Ref, claim, from string) error {
	a, cluster, reg, client, err := p.target(ctx, ref)
	if err != nil {
		return err
	}
	project, rest, ok := strings.Cut(from, "/")
	if !ok {
		return fmt.Errorf("the snapshot to fork from is project/agent@name, not %q", from)
	}
	source, name, ok := strings.Cut(rest, "@")
	if !ok {
		return fmt.Errorf("the snapshot to fork from is project/agent@name, not %q", from)
	}
	sourceAgent, err := p.s.store.Agent(ctx, project, source)
	if err != nil {
		return err
	}
	if sourceAgent.Cluster != a.Cluster {
		return fmt.Errorf("%s runs in cluster %s and %s in %s: a fork stays on the cluster its source runs on", a.Ref(), a.Cluster, sourceAgent.Ref(), sourceAgent.Cluster)
	}
	if err := checkSnapshotName(name); err != nil {
		return err
	}
	meta, err := p.readSnapshot(sourceAgent, name)
	if err != nil {
		return fmt.Errorf("forking %s: %w", sourceAgent.Ref(), err)
	}
	spec := p.s.podSpec(ctx, cluster, reg, a)
	if claim != "" {
		spec.Claim = claim
	}
	spec.Image = p.imageOf(meta, spec.Image)
	if err := p.prepareClaim(ctx, a, client, spec, meta, spec.WorkspaceClaim()); err != nil {
		return err
	}
	if err := p.s.store.SetAgentClaim(ctx, a.Project, a.Name, spec.WorkspaceClaim(), spec.Image); err != nil {
		_ = client.DeleteClaim(context.WithoutCancel(ctx), spec.WorkspaceClaim())
		return err
	}
	return nil
}

// prepareClaim unpacks one archive into the claim spec names, deleting it again
// if anything fails so a half-written claim is never the one a row points at.
func (p podLifecycle) prepareClaim(ctx context.Context, a state.Agent, client *k8s.Cluster, spec k8s.Spec, meta snapshotMeta, claim string) error {
	file, err := os.Open(meta.archive())
	if err != nil {
		return fmt.Errorf("reading %s's snapshot %q: %w", a.Ref(), meta.Name, err)
	}
	defer func() { _ = file.Close() }()
	if err := client.RestoreClaim(ctx, spec, spec.Image, claim, file); err != nil {
		_ = client.DeleteClaim(context.WithoutCancel(ctx), claim)
		return fmt.Errorf("preparing %s's claim from snapshot %q: %w", a.Ref(), meta.Name, err)
	}
	return nil
}

// target reads the agent, its cluster and that cluster's client, which every
// snapshot operation starts with.
func (p podLifecycle) target(ctx context.Context, ref gateway.Ref) (state.Agent, state.Cluster, *state.Registry, *k8s.Cluster, error) {
	a, err := p.s.store.Agent(ctx, ref.Project, ref.Agent)
	if err != nil {
		return state.Agent{}, state.Cluster{}, nil, nil, err
	}
	if !a.OnCluster() {
		return state.Agent{}, state.Cluster{}, nil, nil, fmt.Errorf("%s runs on this host: its snapshots are Incus snapshots, not archives", a.Ref())
	}
	cluster, err := p.s.store.Cluster(ctx, a.Cluster)
	if err != nil {
		return state.Agent{}, state.Cluster{}, nil, nil, err
	}
	reg, err := p.s.registryFor(ctx, cluster.Registry)
	if err != nil {
		return state.Agent{}, state.Cluster{}, nil, nil, err
	}
	client, err := p.s.clusterClient(cluster)
	if err != nil {
		return state.Agent{}, state.Cluster{}, nil, nil, err
	}
	return a, cluster, reg, client, nil
}

// readSnapshot is the metadata of one of an agent's snapshots, and no snapshot
// when the archive it names is gone.
func (p podLifecycle) readSnapshot(a state.Agent, name string) (snapshotMeta, error) {
	meta, err := readSnapshotMeta(filepath.Join(p.s.cfg.Paths.Snapshots(), a.Project, a.Name), name)
	if err != nil {
		return snapshotMeta{}, fmt.Errorf("%s has no snapshot %q: %w", a.Ref(), name, err)
	}
	return meta, nil
}

// imageOf is the image a snapshot was taken with, or the current one for an
// archive that predates the field.
func (p podLifecycle) imageOf(meta snapshotMeta, current string) string {
	if meta.Image != "" {
		return meta.Image
	}
	return current
}

// writeSnapshotMeta writes the JSON beside an archive through a temporary file,
// so a reader never sees half of it.
func writeSnapshotMeta(dir string, meta snapshotMeta) error {
	body, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, meta.Name+".json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// readSnapshotMeta reads one metadata file and refuses it when its archive is
// not there: the pair is what a snapshot is.
func readSnapshotMeta(dir, name string) (snapshotMeta, error) {
	body, err := os.ReadFile(filepath.Join(dir, name+".json"))
	if err != nil {
		return snapshotMeta{}, err
	}
	var meta snapshotMeta
	if err := json.Unmarshal(body, &meta); err != nil {
		return snapshotMeta{}, fmt.Errorf("reading the metadata of %q: %w", name, err)
	}
	if meta.Name == "" {
		meta.Name = name
	}
	meta.dir = dir
	if _, err := os.Stat(meta.archive()); err != nil {
		return snapshotMeta{}, fmt.Errorf("the archive of %q is not there: %w", name, err)
	}
	return meta, nil
}

// archive is where the metadata's archive is: beside it.
func (m snapshotMeta) archive() string {
	return filepath.Join(m.dir, m.Name+".tar.gz")
}

// waitPodGone waits until the cluster no longer has the agent's pod, which is
// what has to be true before a helper can mount its ReadWriteOnce claim, and
// before a new pod can take its place. why says what is waiting on it.
func waitPodGone(ctx context.Context, client *k8s.Cluster, a state.Agent, why string) error {
	deadline := time.Now().Add(podStopTimeout)
	for {
		_, err := client.Get(ctx, a.Project, a.Name)
		if errors.Is(err, k8s.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s's pod is still there after %s: %s", a.Ref(), podStopTimeout, why)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(snapshotPoll):
		}
	}
}

// waitAdopted waits for the reconciler to write the pod into the agent's row.
// A restore starts the pod before the reconciler knows it exists, and the caller
// that follows a restore (a browser start, the app) reads the row.
func waitAdopted(ctx context.Context, store *state.Store, a state.Agent) error {
	const timeout = 30 * time.Second
	deadline := time.Now().Add(timeout)
	for {
		row, err := store.Agent(ctx, a.Project, a.Name)
		if err != nil {
			return err
		}
		if row.Pod != "" && row.StatusDetail == "" {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s's pod is up, and the cluster hasn't adopted it after %s", a.Ref(), timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// nextClaimName is the name of the claim a restore prepares: the old one with
// the time it was made, so a failed restore never collides with the volume the
// agent still has.
func nextClaimName(old string) string {
	if len(old) > 180 {
		old = old[:180]
	}
	return fmt.Sprintf("%s-r%d", old, time.Now().UTC().Unix())
}

// checkSnapshotName refuses a name that could leave the snapshot directory.
func checkSnapshotName(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("invalid snapshot name %q", name)
	}
	return nil
}
