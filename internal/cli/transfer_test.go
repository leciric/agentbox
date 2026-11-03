package cli

import (
	"reflect"
	"slices"
	"testing"

	"agentbox/internal/api"
)

func TestExportChoices(t *testing.T) {
	pv := api.InstanceExportPreview{Projects: []api.InstanceExportProjectPreview{{Name: "web"}, {Name: "api"}}}
	req, err := exportChoices(pv, nil, nil, []string{"memory", "web:chats"}, []string{"github"}, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Projects) != 2 || !req.Projects[0].Bundle {
		t.Fatalf("projects = %+v", req.Projects)
	}
	web, apiP := req.Projects[0].Categories, req.Projects[1].Categories
	if slices.Contains(web, "memory") || slices.Contains(web, "chats") || !slices.Contains(web, "secrets") {
		t.Errorf("web = %v", web)
	}
	if !slices.Contains(apiP, "chats") || slices.Contains(apiP, "memory") {
		t.Errorf("api = %v", apiP)
	}
	if !reflect.DeepEqual(req.Logins, []string{"github"}) {
		t.Errorf("logins = %v", req.Logins)
	}

	for _, bad := range []func() error{
		func() error { _, err := exportChoices(pv, []string{"nope"}, nil, nil, nil, false, false); return err },
		func() error { _, err := exportChoices(pv, nil, []string{"bogus"}, nil, nil, false, false); return err },
		func() error { _, err := exportChoices(pv, nil, nil, []string{"other:memory"}, nil, false, false); return err },
		func() error { _, err := exportChoices(pv, nil, nil, nil, []string{"gitlab"}, false, false); return err },
	} {
		if bad() == nil {
			t.Error("a bad flag was accepted")
		}
	}
	all, _ := exportChoices(pv, []string{"api"}, nil, nil, []string{"all"}, false, false)
	if len(all.Projects) != 1 || len(all.Logins) != 6 || slices.Contains(all.Projects[0].Categories, "secrets") {
		t.Errorf("all = %+v", all)
	}
}

func TestImportChoices(t *testing.T) {
	contents := api.InstanceImportContents{
		Projects: []api.InstanceImportProjectContents{{Name: "web", Categories: []api.TransferCategory{{Kind: "settings"}, {Kind: "memory"}}}},
		Logins:   []api.TransferCategory{{Kind: "github"}, {Kind: "claude"}},
	}
	req, err := importChoices(contents, nil, []string{"web:memory", "claude"}, []string{"web=/srv/web"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if p := req.Projects[0]; !reflect.DeepEqual(p.Categories, []string{"settings"}) || p.Path != "/srv/web" {
		t.Errorf("project = %+v", p)
	}
	if !reflect.DeepEqual(req.Logins, []string{"github"}) {
		t.Errorf("logins = %v", req.Logins)
	}
	if none, _ := importChoices(contents, nil, nil, nil, true); len(none.Logins) != 0 {
		t.Errorf("--no-logins kept %v", none.Logins)
	}
	if _, err := importChoices(contents, nil, nil, []string{"api=/x"}, false); err == nil {
		t.Error("--repo for a project not in the file was accepted")
	}
}
