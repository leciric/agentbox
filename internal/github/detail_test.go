package github

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPullRequestDetailReadsBodyLabelsAndCheckRuns(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/app/pulls/7":
			_, _ = w.Write([]byte(`{"number":7,"title":"Pages","state":"open","body":"Adds **pages**","additions":12,"changed_files":4,"deletions":3,
				"labels":[{"name":"ui","color":"a2eeef","description":"The app"}],
				"user":{"login":"lea"},"base":{"ref":"main"},"head":{"ref":"feat/pages","sha":"abc"}}`))
		case "/repos/acme/app/commits/abc/check-runs":
			_, _ = w.Write([]byte(`{"total_count":2,"check_runs":[
				{"name":"go","status":"completed","conclusion":"success","html_url":"https://github.com/acme/app/runs/1"},
				{"name":"desktop","status":"in_progress","conclusion":null}]}`))
		default:
			t.Errorf("unexpected call %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	pr, err := Client{BaseURL: srv.URL}.PullRequestDetail(context.Background(), Repo{"acme", "app"}, 7)
	if err != nil {
		t.Fatal(err)
	}
	if pr.Body != "Adds **pages**" || pr.Author != "lea" || pr.Additions != 12 || pr.ChangedFiles != 4 || pr.HeadBranch != "feat/pages" {
		t.Errorf("detail = %+v", pr)
	}
	if len(pr.Labels) != 1 || pr.Labels[0] != (Label{Name: "ui", Color: "a2eeef", Description: "The app"}) {
		t.Errorf("Labels = %+v", pr.Labels)
	}
	if len(pr.CheckRuns) != 2 || pr.CheckRuns[0].URL != "https://github.com/acme/app/runs/1" || pr.CheckRuns[1].Status != "in_progress" {
		t.Errorf("CheckRuns = %+v", pr.CheckRuns)
	}
	if pr.Checks != "pending" {
		t.Errorf("Checks = %q, want pending while one runs", pr.Checks)
	}
}

func TestPullRequestFilesReadsEveryPage(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/app/pulls/7/files" {
			t.Errorf("unexpected call %s", r.URL.Path)
		}
		n := 100
		if r.URL.Query().Get("page") == "2" {
			n = 1
		}
		var items []string
		for i := range n {
			items = append(items, fmt.Sprintf(`{"filename":"f%s-%d.go","status":"modified","additions":1,"deletions":0,"patch":"@@ -1 +1 @@\n+x"}`, r.URL.Query().Get("page"), i))
		}
		_, _ = w.Write([]byte("[" + strings.Join(items, ",") + "]"))
	}))
	defer srv.Close()

	files, truncated, err := Client{BaseURL: srv.URL}.PullRequestFiles(context.Background(), Repo{"acme", "app"}, 7)
	if err != nil || truncated {
		t.Fatal(err, truncated)
	}
	if len(files) != 101 || files[100].Path != "f2-0.go" || files[0].Patch != "@@ -1 +1 @@\n+x" {
		t.Errorf("files = %d, last %+v", len(files), files[len(files)-1])
	}
}

func TestImageURLOnlyReadsGitHub(t *testing.T) {
	t.Parallel()
	repo := Repo{"Acme", "App"}
	for src, want := range map[string]string{
		"https://github.com/user-attachments/assets/4b37bf83-2696-4c69-9789-9c88a55e0963":                                       "https://github.com/user-attachments/assets/4b37bf83-2696-4c69-9789-9c88a55e0963",
		"https://github.com/acme/app/assets/1/shot.png":                                                                         "https://github.com/acme/app/assets/1/shot.png",
		"https://raw.githubusercontent.com/acme/app/main/docs/shot.png":                                                         "https://raw.githubusercontent.com/acme/app/main/docs/shot.png",
		"https://private-user-images.githubusercontent.com/24596032/670384570-4b37bf83-2696-4c69-9789-9c88a55e0963.png?jwt=old": "https://github.com/user-attachments/assets/4b37bf83-2696-4c69-9789-9c88a55e0963",
		"https://camo.githubusercontent.com/abc/def":                                                                            "https://camo.githubusercontent.com/abc/def",
		// Another repository on github.com would get this account's token.
		"https://github.com/other/repo/assets/1/shot.png":            "",
		"https://raw.githubusercontent.com/other/repo/main/shot.png": "",
		"https://github.com/login":                                   "",
		"http://github.com/user-attachments/assets/x":                "",
		"https://github.com:8443/user-attachments/x":                 "",
		"https://evil.example/user-attachments/assets":               "",
		"https://github.com.evil.example/acme/app/x":                 "",
		"https://localhost/acme/app/x":                               "",
	} {
		u, err := imageURL(repo, src)
		switch {
		case want == "" && !errors.Is(err, ErrNotAGitHubImage):
			t.Errorf("imageURL(%s) = %v, %v; want refused", src, u, err)
		case want != "" && (err != nil || u.String() != want):
			t.Errorf("imageURL(%s) = %v, %v; want %s", src, u, err, want)
		}
	}
}

// An attachment is read with the token, which is never sent on to where
// GitHub redirects it.
func TestImageSendsTheTokenOnlyToGitHub(t *testing.T) {
	t.Parallel()
	var storageAuth string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Host {
		case "github.com":
			if r.Header.Get("Authorization") != "Bearer secret" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			http.Redirect(w, r, "https://storage.example/shot.png?sig=1", http.StatusFound)
		case "storage.example":
			storageAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("PNG"))
		default:
			t.Errorf("unexpected host %s", r.Host)
		}
	}))
	defer srv.Close()

	res, err := Client{Token: "secret", HTTP: everyHostTo(srv)}.Image(context.Background(), Repo{"acme", "app"}, "https://github.com/user-attachments/assets/4b37bf83-2696-4c69-9789-9c88a55e0963")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	if string(body) != "PNG" || res.Header.Get("Content-Type") != "image/png" {
		t.Errorf("image = %q %s", body, res.Header.Get("Content-Type"))
	}
	if storageAuth != "" {
		t.Errorf("the token went to the storage host: %q", storageAuth)
	}
}

func TestImageRefusesWhatIsNotAnImage(t *testing.T) {
	t.Parallel()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>"))
	}))
	defer srv.Close()
	if _, err := (Client{HTTP: everyHostTo(srv)}).Image(context.Background(), Repo{"acme", "app"}, "https://camo.githubusercontent.com/x"); err == nil || !strings.Contains(err.Error(), "not an image") {
		t.Errorf("err = %v, want not an image", err)
	}
}

// everyHostTo is a client that reaches srv whatever host a URL names.
func everyHostTo(srv *httptest.Server) *http.Client {
	tr := srv.Client().Transport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // a test server
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
	}
	return &http.Client{Transport: tr}
}
