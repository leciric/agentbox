package daemon

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/gitrepo"
	"agentbox/internal/state"
	"agentbox/internal/transfer"
)

// Syncing with paired AgentBoxes: merging what each has into the other, only
// when the user asks (Settings → Share, "Sync"). It is package transfer's
// export and import over a pairing (sharepeers.go, shareremote.go) instead of
// through a file, between instances that are both in use (transfer.Merge.Sync).
//
// The AgentBox that pasted the link starts it, since it is the one that
// reaches the other: it is the peer, the other the owner. The owner must have
// said the peer may (state.SharePeer.Sync, off until then): a peer otherwise
// never copies a project's data, nor makes or changes one.
//
//  1. The plan (POST /v1/share/sync/plan, a job). From each owner the peer
//     fetches an archive of the projects it shares with it (/share/sync/pull),
//     and imports each as a dry run: what would come in, and what both have
//     with other values, the conflicts. Nothing changes yet.
//  2. The user chooses, for each conflict, whose version every AgentBox ends
//     up with, and which projects to sync.
//  3. The sync (POST /v1/share/sync, a job). The peer imports each archive,
//     taking the owner's version where the user chose it; then it holds what
//     the user decided, exports it, and pushes it to each owner
//     (/share/sync/push), where its version wins for every conflict the user
//     was asked about. So with several owners, each ends up with what all of
//     them had. What changed on an owner in the meantime is left as it is
//     there and reported: syncing again asks about it.
//
// What is carried is transfer.SyncCategories: never secrets (the plan names
// the ones both sides have, shareSyncSecrets), logins or schedules, and repositories by their remote's URL, cloned by each side with
// its own access. Projects are matched by name, and not merged when both have
// a remote and they aren't the same repository. An owner takes new projects
// from a peer, but merges only into those it shares with it.
//
// Archives are encrypted with a key both sides of a pairing have and that
// never crosses the wire (syncPassphrase), as LAN pairings are plain HTTP.

const (
	// syncPlanTTL is how long a plan may wait for the user's choices: the
	// longer it waits, the more may have changed since it was worked out.
	syncPlanTTL = 30 * time.Minute
	// maxSyncArchive is the largest archive an owner takes from a peer.
	maxSyncArchive = 4 << 30
)

// syncPassphrase encrypts the archives of one pairing: derived from the hash
// of the peer's token, which the owner keeps and the peer works out.
func syncPassphrase(key string) string { return shareMAC(key, "sync") }

// syncState is the plan waiting for the user's choices: one at a time.
type syncState struct {
	mu   sync.Mutex
	plan *syncPlan
}

type syncPlan struct {
	api.SyncPlan
	dir    string // where the fetched archives are
	pulled map[string]syncPulled
}

// syncPulled is what one owner shares, as fetched for a plan.
type syncPulled struct {
	conn     *remoteConn
	path     string
	projects []api.InstanceImportProjectContents
}

// keep replaces the waiting plan, and take hands it over to be carried out.
func (st *syncState) keep(p *syncPlan) {
	st.mu.Lock()
	old := st.plan
	st.plan = p
	st.mu.Unlock()
	if old != nil {
		_ = os.RemoveAll(old.dir)
	}
}

func (st *syncState) take(id string) (*syncPlan, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	p := st.plan
	if p == nil || p.ID != id {
		return nil, errors.New("this sync was already done or replaced by a newer one: start it again")
	}
	st.plan = nil
	if time.Now().After(p.Expires) {
		_ = os.RemoveAll(p.dir)
		return nil, errors.New("this sync waited too long, and things may have changed since: start it again")
	}
	return p, nil
}

// The owner's side.

// shareSyncPeer is the peer asking to sync, when the owner lets it.
func (s *Server) shareSyncPeer(w http.ResponseWriter, r *http.Request, addr string) (state.SharePeer, bool) {
	p, ok := s.sharePeerOf(w, r, addr)
	if !ok {
		return p, false
	}
	if !p.Sync {
		writeLANError(w, http.StatusForbidden, fmt.Sprintf(`it doesn't let this AgentBox sync with it yet. There, in Settings → Share, turn on "May sync with this AgentBox" for %s`, p.Name))
		return p, false
	}
	return p, true
}

// syncProjects are the projects of this machine a sync can carry.
func (s *Server) syncProjects(ctx context.Context) ([]state.Project, error) {
	all, err := s.store.Projects(ctx)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(all, func(p state.Project) bool { return p.Name == state.HomeProject }), nil
}

// syncExport writes an archive of the named projects for the other side of
// a pairing.
func (s *Server) syncExport(ctx context.Context, path, key string, projects []string) error {
	opts := transfer.ExportOptions{Passphrase: syncPassphrase(key)}
	for _, name := range projects {
		opts.Projects = append(opts.Projects, transfer.ProjectChoice{Name: name, Categories: transfer.SyncCategories})
	}
	_, err := s.transferInstance().Export(ctx, path, opts, io.Discard)
	return err
}

// shareSyncPull answers a peer with an archive of the projects it reaches:
// /share/sync/pull.
func (s *Server) shareSyncPull(w http.ResponseWriter, r *http.Request, addr string) {
	p, ok := s.shareSyncPeer(w, r, addr)
	if !ok {
		return
	}
	ctx := r.Context()
	fail := func(err error) { writeLANError(w, http.StatusInternalServerError, err.Error()) }
	key, err := s.store.SharePeerKey(ctx, p.ID)
	if err != nil {
		fail(err)
		return
	}
	projects, err := s.syncProjects(ctx)
	if err != nil {
		fail(err)
		return
	}
	var names []string
	for _, proj := range projects {
		if p.Shares(proj.Name) {
			names = append(names, proj.Name)
		}
	}
	dir, err := os.MkdirTemp("", "agentbox-sync-*")
	if err != nil {
		fail(err)
		return
	}
	defer func() { _ = os.RemoveAll(dir) }()
	path := filepath.Join(dir, "pull.agentbox")
	if err := s.syncExport(ctx, path, key, names); err != nil {
		fail(err)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		fail(err)
		return
	}
	defer func() { _ = f.Close() }()
	s.logf("share: %s (%s) fetched %d projects to sync", p.Name, p.ID, len(names))
	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = io.Copy(w, f)
}

// shareSyncSecrets answers a peer with the names of the secrets of the
// projects it reaches: /share/sync/secrets. A sync never carries a secret, and
// no value or anything worked out from one leaves this machine, so the peer
// can say which names both sides have, not whether their values differ.
func (s *Server) shareSyncSecrets(w http.ResponseWriter, r *http.Request, addr string) {
	p, ok := s.shareSyncPeer(w, r, addr)
	if !ok {
		return
	}
	ctx := r.Context()
	projects, err := s.syncProjects(ctx)
	if err != nil {
		writeLANError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := api.ShareSyncSecrets{Projects: map[string][]string{}}
	for _, proj := range projects {
		if !p.Shares(proj.Name) {
			continue
		}
		if names := s.syncSecretNames(ctx, proj.Name); len(names) > 0 {
			out.Projects[proj.Name] = names
		}
	}
	_ = writeJSON(w, http.StatusOK, out)
}

// syncSecretNames are the names of a project's secrets here.
func (s *Server) syncSecretNames(ctx context.Context, project string) []string {
	list, err := s.secrets().List(ctx, project, "")
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(list))
	for _, secret := range list {
		names = append(names, secret.Name)
	}
	return names
}

// shareSyncPush merges what a peer sends into this machine:
// /share/sync/push, an api.ShareSyncPush on a line and then the archive.
func (s *Server) shareSyncPush(w http.ResponseWriter, r *http.Request, addr string) {
	p, ok := s.shareSyncPeer(w, r, addr)
	if !ok {
		return
	}
	ctx := r.Context()
	fail := func(status int, err error) { writeLANError(w, status, err.Error()) }
	key, err := s.store.SharePeerKey(ctx, p.ID)
	if err != nil {
		fail(http.StatusInternalServerError, err)
		return
	}
	body := bufio.NewReader(http.MaxBytesReader(w, r.Body, maxSyncArchive))
	line, err := body.ReadBytes('\n')
	var push api.ShareSyncPush
	if err == nil {
		err = json.Unmarshal(line, &push)
	}
	if err != nil {
		fail(http.StatusBadRequest, fmt.Errorf("not a sync: %w", err))
		return
	}
	dir, err := os.MkdirTemp("", "agentbox-sync-*")
	if err != nil {
		fail(http.StatusInternalServerError, err)
		return
	}
	defer func() { _ = os.RemoveAll(dir) }()
	path := filepath.Join(dir, "push.agentbox")
	if err := writeFileFrom(path, body); err != nil {
		fail(http.StatusBadRequest, err)
		return
	}
	how := transfer.Merge{Sync: true, Take: map[string]bool{}}
	for _, id := range push.Take {
		how.Take[id] = true
	}
	report, _, err := s.syncImport(ctx, path, syncPassphrase(key), how, func(c api.InstanceImportProjectContents) string {
		if c.Exists && !p.Shares(c.Name) {
			return "it has a project of that name that it doesn't share with this AgentBox"
		}
		return ""
	})
	if err != nil {
		fail(http.StatusBadRequest, err)
		return
	}
	s.logf("share: %s (%s) synced: %d added, %d replaced, %d left as they are here", p.Name, p.ID, report.Added, report.Updated, report.Conflicted)
	_ = writeJSON(w, http.StatusOK, report)
}

func writeFileFrom(path string, r io.Reader) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// Both sides.

// syncSkippedKind is the kind of the entry that says, in a sync's report, why
// a project was left out of it, and syncLeftOut the reason that needs no
// saying: the user left it out.
const (
	syncSkippedKind = "skipped"
	syncLeftOut     = "-"
)

func syncSkipped(project, why string) api.InstanceImportEntryReport {
	return api.InstanceImportEntryReport{ID: transfer.ProjectEntryID(project, syncSkippedKind), Kind: syncSkippedKind, Project: project, Skipped: 1, Notes: []string{why}}
}

// syncImport merges an archive from the other side of a pairing into this
// machine, every project of it but those skip gives a reason for, and
// returns what the archive holds beside what was done with it.
func (s *Server) syncImport(ctx context.Context, path, passphrase string, how transfer.Merge, skip func(api.InstanceImportProjectContents) string) (api.InstanceImportReport, []api.InstanceImportProjectContents, error) {
	in := s.transferInstance()
	contents, err := in.Inspect(ctx, path, passphrase)
	if err != nil {
		return api.InstanceImportReport{}, nil, err
	}
	req := api.InstanceImportRequest{Path: path, Passphrase: passphrase}
	var skipped []api.InstanceImportEntryReport
	for _, c := range contents.Projects {
		why := s.syncMismatch(c)
		if why == "" && skip != nil {
			why = skip(c)
		}
		if why == syncLeftOut {
			continue
		}
		if why != "" {
			skipped = append(skipped, syncSkipped(c.Name, why))
			continue
		}
		choice := api.InstanceImportProjectChoice{Name: c.Name}
		for _, cat := range c.Categories {
			if slices.Contains(transfer.SyncCategories, cat.Kind) {
				choice.Categories = append(choice.Categories, cat.Kind)
			}
		}
		req.Projects = append(req.Projects, choice)
	}
	report, err := in.ImportWith(ctx, req, how, io.Discard, s.projectImported)
	if err != nil {
		return report, contents.Projects, err
	}
	report.Entries = append(report.Entries, skipped...)
	report.Skipped += len(skipped)
	return report, contents.Projects, nil
}

// syncName is what a project's name is made of (naming.ProjectSlug): an
// archive from another machine names files and rows by it.
var syncName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// syncMismatch is why an archive's project can't be merged into the one of
// its name here, "" when it can: a name no project has, or two repositories
// under one name.
func (s *Server) syncMismatch(c api.InstanceImportProjectContents) string {
	if c.Name == state.HomeProject || !syncName.MatchString(c.Name) {
		return "that isn't a project's name"
	}
	if !c.Exists || c.Remote == "" {
		return ""
	}
	mine, err := gitrepo.RemoteURL(c.Path, "origin")
	if err != nil || mine == "" || sameRepo(mine, c.Remote) {
		return ""
	}
	return fmt.Sprintf("the two AgentBoxes have different repositories under that name (%s and %s)", shareableRepo(mine), shareableRepo(c.Remote))
}

// projectImported is a project an import or a sync made: its lead's API
// starts, and the apps hear of it.
func (s *Server) projectImported(project string) {
	if err := s.serveLeadAPI(project); err != nil {
		s.logf("lead API socket for %s: %v", project, err)
	}
	s.events.publish(api.EventProject, api.ProjectChange{Name: project})
}

// The peer's side.

// syncRequest is a request to an owner's sync gateway.
func (s *Server) syncRequest(ctx context.Context, c *remoteConn, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.r.URL, "/")+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/octet-stream")
	}
	resp, err := s.peering.remotes.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s can't be reached: %s", c.r.Name, errText(err))
	}
	if resp.StatusCode != http.StatusOK {
		defer func() { _ = resp.Body.Close() }()
		return nil, remoteStatusError(c, resp)
	}
	return resp, nil
}

// syncPull fetches what an owner shares into path.
func (s *Server) syncPull(ctx context.Context, c *remoteConn, path string) error {
	resp, err := s.syncRequest(ctx, c, "/share/sync/pull", nil)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	return writeFileFrom(path, resp.Body)
}

// syncSecretsOf are the names of the secrets an owner has, by project; none
// when it doesn't say (an AgentBox from before it could).
func (s *Server) syncSecretsOf(ctx context.Context, c *remoteConn) map[string][]string {
	resp, err := s.syncRequest(ctx, c, "/share/sync/secrets", nil)
	if err != nil {
		return nil
	}
	defer func() { _ = resp.Body.Close() }()
	var out api.ShareSyncSecrets
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out) != nil {
		return nil
	}
	return out.Projects
}

// syncPush sends an owner the archive at path, to merge with this machine's
// values winning the conflicts in take.
func (s *Server) syncPush(ctx context.Context, c *remoteConn, path string, take []string) (api.InstanceImportReport, error) {
	var report api.InstanceImportReport
	head, err := json.Marshal(api.ShareSyncPush{Take: take})
	if err != nil {
		return report, err
	}
	f, err := os.Open(path)
	if err != nil {
		return report, err
	}
	defer func() { _ = f.Close() }()
	resp, err := s.syncRequest(ctx, c, "/share/sync/push", io.MultiReader(bytes.NewReader(append(head, '\n')), f))
	if err != nil {
		return report, err
	}
	defer func() { _ = resp.Body.Close() }()
	return report, json.NewDecoder(resp.Body).Decode(&report)
}

// syncConns are the owners to sync with: those asked for, or all.
func (s *Server) syncConns(ids []string) ([]*remoteConn, error) {
	conns := s.remoteConns()
	slices.SortFunc(conns, func(a, b *remoteConn) int { return strings.Compare(a.r.ID, b.r.ID) })
	if len(ids) > 0 {
		for _, id := range ids {
			if !slices.ContainsFunc(conns, func(c *remoteConn) bool { return c.r.ID == id }) {
				return nil, fmt.Errorf("this AgentBox isn't paired with %s: %w", id, state.ErrNotFound)
			}
		}
		conns = slices.DeleteFunc(conns, func(c *remoteConn) bool { return !slices.Contains(ids, c.r.ID) })
	}
	if len(conns) == 0 {
		return nil, errors.New("there is no AgentBox to sync with: paste another AgentBox's link first, in Settings → Share")
	}
	return conns, nil
}

func syncNames(conns []*remoteConn) string {
	names := make([]string, len(conns))
	for i, c := range conns {
		names[i] = c.r.Name
	}
	return strings.Join(names, ", ")
}

// planSync starts working out what a sync would do: POST /v1/share/sync/plan.
func (s *Server) planSync(w http.ResponseWriter, r *http.Request) error {
	var req api.SyncPlanRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	conns, err := s.syncConns(req.Remotes)
	if err != nil {
		return err
	}
	return s.startJob(w, "sync-plan", syncNames(conns), func(ctx context.Context, log io.Writer) (any, error) {
		plan, err := s.syncPlan(ctx, conns, log)
		if err != nil {
			return nil, err
		}
		s.syncs.keep(plan)
		return plan.SyncPlan, nil
	})
}

func (s *Server) syncPlan(ctx context.Context, conns []*remoteConn, log io.Writer) (*syncPlan, error) {
	id := make([]byte, 8)
	if _, err := rand.Read(id); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "agentbox-sync-*")
	if err != nil {
		return nil, err
	}
	plan := &syncPlan{dir: dir, pulled: map[string]syncPulled{}}
	plan.SyncPlan = api.SyncPlan{ID: hex.EncodeToString(id), Expires: time.Now().Add(syncPlanTTL),
		Remotes: []api.SyncPlanRemote{}, Projects: []api.SyncPlanProject{}, Conflicts: []api.SyncConflict{}}
	done := false
	defer func() {
		if !done {
			_ = os.RemoveAll(dir)
		}
	}()

	projects := map[string]*api.SyncPlanProject{}
	project := func(name, display string) *api.SyncPlanProject {
		p, ok := projects[name]
		if !ok {
			p = &api.SyncPlanProject{Name: name, DisplayName: display, On: []string{}}
			projects[name] = p
		}
		if p.DisplayName == "" || p.DisplayName == name {
			p.DisplayName = display
		}
		return p
	}
	mine, err := s.syncProjects(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range mine {
		project(p.Name, p.DisplayName).Here = true
	}
	conflicts := map[string]int{}
	for _, c := range conns {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		remote := api.SyncPlanRemote{ID: c.r.ID, Name: c.r.Name}
		_, _ = fmt.Fprintf(log, "Fetching what %s shares\n", c.r.Name)
		path := filepath.Join(dir, c.r.ID+".agentbox")
		err := s.syncPull(ctx, c, path)
		var report api.InstanceImportReport
		var contents []api.InstanceImportProjectContents
		if err == nil {
			report, contents, err = s.syncImport(ctx, path, syncPassphrase(state.HashPhoneToken(c.token)), transfer.Merge{Sync: true, DryRun: true}, nil)
		}
		if err != nil {
			remote.Error = errText(err)
			_, _ = fmt.Fprintf(log, "%s\n", remote.Error)
			plan.Remotes = append(plan.Remotes, remote)
			continue
		}
		plan.pulled[c.r.ID] = syncPulled{conn: c, path: path, projects: contents}
		theirs := s.syncSecretsOf(ctx, c)
		for _, pc := range contents {
			p := project(pc.Name, pc.DisplayName)
			p.On = append(p.On, c.r.ID)
			if !p.Here {
				continue
			}
			for _, name := range s.syncSecretNames(ctx, pc.Name) {
				if slices.Contains(theirs[pc.Name], name) && !slices.Contains(p.Secrets, name) {
					p.Secrets = append(p.Secrets, name)
				}
			}
		}
		for _, e := range report.Entries {
			if e.Project == "" {
				continue
			}
			p := project(e.Project, e.Project)
			p.Incoming += e.Added
			if e.Kind == syncSkippedKind && p.Skip == "" {
				p.Skip = e.Notes[0]
			}
		}
		for _, conflict := range report.Conflicts {
			i, ok := conflicts[conflict.ID]
			if !ok {
				i = len(plan.Conflicts)
				conflicts[conflict.ID] = i
				plan.Conflicts = append(plan.Conflicts, api.SyncConflict{ID: conflict.ID, Project: conflict.Project, Kind: conflict.Kind, Label: conflict.Label})
			}
			plan.Conflicts[i].Versions = append(plan.Conflicts[i].Versions, api.SyncConflictVersion{Remote: c.r.ID, Name: c.r.Name, Fields: conflict.Fields})
		}
		remote.Incoming = report.Added
		_, _ = fmt.Fprintf(log, "%s: %d new here, %d that differ\n", c.r.Name, report.Added, len(report.Conflicts))
		plan.Remotes = append(plan.Remotes, remote)
	}
	for _, p := range projects {
		plan.Projects = append(plan.Projects, *p)
	}
	slices.SortFunc(plan.Projects, func(a, b api.SyncPlanProject) int { return strings.Compare(a.Name, b.Name) })
	done = true
	return plan, nil
}

// runSync carries a plan out: POST /v1/share/sync.
func (s *Server) runSync(w http.ResponseWriter, r *http.Request) error {
	var req api.SyncRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	plan, err := s.syncs.take(req.Plan)
	if err != nil {
		return err
	}
	if len(plan.pulled) == 0 {
		_ = os.RemoveAll(plan.dir)
		return errors.New("none of the AgentBoxes could be synced with")
	}
	var conns []*remoteConn
	for _, p := range plan.pulled {
		conns = append(conns, p.conn)
	}
	slices.SortFunc(conns, func(a, b *remoteConn) int { return strings.Compare(a.r.ID, b.r.ID) })
	err = s.startJob(w, "sync", syncNames(conns), func(ctx context.Context, log io.Writer) (any, error) {
		defer func() { _ = os.RemoveAll(plan.dir) }()
		return s.sync(ctx, plan, conns, req, log)
	})
	if err != nil {
		_ = os.RemoveAll(plan.dir)
	}
	return err
}

func (s *Server) sync(ctx context.Context, plan *syncPlan, conns []*remoteConn, req api.SyncRequest, log io.Writer) (api.SyncReport, error) {
	out := api.SyncReport{Remotes: []api.SyncRemoteReport{}, ProjectsAdded: []string{}}
	chosen := func(name string) bool { return req.Projects == nil || slices.Contains(*req.Projects, name) }
	reports := map[string]*api.SyncRemoteReport{}
	for _, r := range plan.Remotes {
		out.Remotes = append(out.Remotes, api.SyncRemoteReport{ID: r.ID, Name: r.Name, Error: r.Error, ProjectsAdded: []string{}})
	}
	for i := range out.Remotes {
		reports[out.Remotes[i].ID] = &out.Remotes[i]
	}
	// asked is every conflict the user was asked about: what this machine
	// holds for it after the imports below is their answer, which each
	// owner then takes.
	var asked []string
	for _, c := range plan.Conflicts {
		if chosen(c.Project) {
			asked = append(asked, c.ID)
		}
	}

	// Everything comes here first.
	for _, c := range conns {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		rr, pulled := reports[c.r.ID], plan.pulled[c.r.ID]
		how := transfer.Merge{Sync: true, Take: map[string]bool{}}
		for id, keep := range req.Keep {
			if keep == c.r.ID {
				how.Take[id] = true
			}
		}
		_, _ = fmt.Fprintf(log, "Merging what %s shares into this AgentBox\n", c.r.Name)
		report, _, err := s.syncImport(ctx, pulled.path, syncPassphrase(state.HashPhoneToken(c.token)), how, func(pc api.InstanceImportProjectContents) string {
			if !chosen(pc.Name) {
				return syncLeftOut
			}
			return ""
		})
		if err != nil {
			rr.Error = errText(err)
			_, _ = fmt.Fprintf(log, "%s: %s\n", c.r.Name, rr.Error)
			continue
		}
		rr.Received, rr.Replaced = report.Added, report.Updated
		rr.Notes = append(rr.Notes, syncNotes(report, func(name string) bool {
			_, err := s.store.Project(ctx, name)
			return err == nil
		}, "here")...)
		out.ProjectsAdded = append(out.ProjectsAdded, report.ProjectsAdded...)
		_, _ = fmt.Fprintf(log, "%s: %d added here, %d replaced here\n", c.r.Name, report.Added, report.Updated)
	}

	// Then what this machine holds now goes to each.
	mine, err := s.syncProjects(ctx)
	if err != nil {
		return out, err
	}
	var names []string
	for _, p := range mine {
		if chosen(p.Name) || slices.Contains(out.ProjectsAdded, p.Name) {
			names = append(names, p.Name)
		}
	}
	for _, c := range conns {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		rr, pulled := reports[c.r.ID], plan.pulled[c.r.ID]
		if rr.Error != "" {
			continue
		}
		_, _ = fmt.Fprintf(log, "Sending this AgentBox's projects to %s\n", c.r.Name)
		path := filepath.Join(plan.dir, c.r.ID+".push.agentbox")
		err := s.syncExport(ctx, path, state.HashPhoneToken(c.token), names)
		var report api.InstanceImportReport
		if err == nil {
			report, err = s.syncPush(ctx, c, path, asked)
		}
		_ = os.Remove(path)
		if err != nil {
			rr.Error = errText(err)
			_, _ = fmt.Fprintf(log, "%s: %s\n", c.r.Name, rr.Error)
			continue
		}
		rr.Sent, rr.SentOver, rr.Left = report.Added, report.Updated, report.Conflicted
		rr.ProjectsAdded = append(rr.ProjectsAdded, report.ProjectsAdded...)
		rr.Notes = append(rr.Notes, syncNotes(report, func(name string) bool {
			return slices.Contains(report.ProjectsAdded, name) || slices.ContainsFunc(pulled.projects, func(pc api.InstanceImportProjectContents) bool { return pc.Name == name })
		}, "on "+c.r.Name)...)
		_, _ = fmt.Fprintf(log, "%s: %d added there, %d replaced there, %d left as they are there\n", c.r.Name, report.Added, report.Updated, report.Conflicted)
	}
	return out, nil
}

// syncNotes says, for each project of a report that was left out, or that
// isn't on the side it was merged into (has), why: where is that side, as a
// sentence names it.
func syncNotes(report api.InstanceImportReport, has func(project string) bool, where string) []string {
	var notes []string
	said := map[string]bool{}
	for _, e := range report.Entries {
		if e.Project == "" || said[e.Project] || len(e.Notes) == 0 || e.Added > 0 {
			continue
		}
		made := e.Kind == transfer.Settings || e.Kind == transfer.Repository
		if e.Kind == syncSkippedKind || (made && !has(e.Project)) {
			said[e.Project] = true
			notes = append(notes, fmt.Sprintf("%s wasn't synced %s: %s", e.Project, where, e.Notes[0]))
		}
	}
	return notes
}
