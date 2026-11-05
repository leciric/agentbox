package daemon

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"agentbox/internal/api"
	"agentbox/internal/portable"
)

func init() { portable.ScryptWorkFactor = 10 }

func finishedJob(t *testing.T, d testDaemon, j api.Job) api.Job {
	t.Helper()
	ctx := context.Background()
	waitFor(t, "job "+j.ID, func() bool {
		got, err := d.client.Job(ctx, j.ID)
		return err == nil && got.Done()
	})
	got, _ := d.client.Job(ctx, j.ID)
	if got.Status != api.JobSucceeded {
		t.Fatalf("job %s: %s %s", j.Kind, got.Status, got.Error)
	}
	return got
}

// An export from one daemon imports into another through the same API the CLI
// and the app use, and a wrong passphrase is refused with its code, before a
// job starts.
func TestExportImportAPI(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	src := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := src.fixtureRepo(t, "hello-stack")
	if _, err := src.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	pv, err := src.client.ExportPreview(ctx)
	if err != nil || len(pv.Projects) != 1 {
		t.Fatalf("preview = %+v, %v", pv, err)
	}
	path := filepath.Join(t.TempDir(), "out.agentbox")
	req := api.InstanceExportRequest{Path: path, Passphrase: "passphrase",
		Projects: []api.InstanceExportProjectChoice{{Name: "hello-stack", Categories: []string{"repository", "settings", "notes"}}}}
	plain := req
	plain.Passphrase, plain.NoEncrypt = "", true
	plain.Logins = []string{"github"}
	if _, err := src.client.ExportInstance(ctx, plain); err == nil {
		t.Fatal("a plaintext export with logins was accepted")
	}
	j, err := src.client.ExportInstance(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	finishedJob(t, src, j)

	dst := startTestDaemon(t, t.TempDir(), fakeIncus)
	if _, err := dst.client.InspectImport(ctx, api.InstanceImportInspectRequest{Path: path}); !api.HasReason(err, api.ErrorPassphraseNeeded) {
		t.Fatalf("no passphrase: %v", err)
	}
	wrong := api.InstanceImportRequest{Path: path, Passphrase: "nope"}
	if _, err := dst.client.ImportInstance(ctx, wrong); !api.HasReason(err, api.ErrorWrongPassphrase) {
		t.Fatalf("wrong passphrase: %v", err)
	}
	contents, err := dst.client.InspectImport(ctx, api.InstanceImportInspectRequest{Path: path, Passphrase: "passphrase"})
	if err != nil {
		t.Fatal(err)
	}
	if p := contents.Projects[0]; p.Exists || p.Plan != "use" || p.Path != repo {
		t.Fatalf("contents = %+v", p)
	}
	in := api.InstanceImportRequest{Path: path, Passphrase: "passphrase",
		Projects: []api.InstanceImportProjectChoice{{Name: "hello-stack", Categories: []string{"repository", "settings", "notes"}}}}
	j, err = dst.client.ImportInstance(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	var rep api.InstanceImportReport
	if err := json.Unmarshal(finishedJob(t, dst, j).Result, &rep); err != nil {
		t.Fatal(err)
	}
	if len(rep.ProjectsAdded) != 1 {
		t.Fatalf("report = %+v", rep)
	}
	if p, err := dst.client.Project(ctx, "hello-stack"); err != nil || p.Root != repo {
		t.Fatalf("project = %+v, %v", p, err)
	}
}
