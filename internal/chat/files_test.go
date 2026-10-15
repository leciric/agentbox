package chat

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/acp"
	"agentbox/internal/api"
)

func fileUpload(name, mimeType string, data []byte) api.ChatFileUpload {
	return api.ChatFileUpload{Name: name, MimeType: mimeType, Data: base64.StdEncoding.EncodeToString(data)}
}

// A file that isn't a picture is put in the chat's working directory, under
// a folder git ignores, and the prompt names where it is; a small text one
// goes in the prompt too, as a resource block, to an adapter that takes them.
// A binary one doesn't, and neither reaches an adapter that takes no
// resources as anything but its path.
func TestAFileReachesTheToolByItsPath(t *testing.T) {
	t.Parallel()
	for _, embedded := range []bool{true, false} {
		store := openStore(t)
		f := newFakeTool(answerHello)
		f.embedded = embedded
		m, _ := newManager(t, store, f)
		withImages(t, m)
		a := testAgent
		a.Worktree = t.TempDir()

		csv := []byte("name,qty\napple,3\n")
		pdf := append([]byte("%PDF-1.7\n"), 0, 1, 2, 3)
		user, err := m.SendMessage(a, api.ChatMessageRequest{Text: "sum these", Files: []api.ChatFileUpload{
			fileUpload("data.csv", "text/csv", csv),
			fileUpload("../../report.pdf", "", pdf),
		}})
		if err != nil {
			t.Fatal(err)
		}
		if len(user.Files) != 2 || user.Text != "sum these" {
			t.Fatalf("the message = %q with files %+v", user.Text, user.Files)
		}
		got := user.Files[1]
		if got.Name != "report.pdf" || got.MimeType != "application/pdf" || got.Size != int64(len(pdf)) {
			t.Errorf("the PDF = %+v, want report.pdf, application/pdf, %d bytes", got, len(pdf))
		}
		base := filepath.Join(a.Worktree, ".agentbox", "attachments")
		for i, want := range [][]byte{csv, pdf} {
			f := user.Files[i]
			if filepath.Dir(filepath.Dir(f.Path)) != base {
				t.Errorf("%s is at %s, not under %s", f.Name, f.Path, base)
			}
			if b, err := os.ReadFile(f.Path); err != nil || !bytes.Equal(b, want) {
				t.Errorf("the copy of %s = %q, %v", f.Name, b, err)
			}
			kept, _ := m.ImagePath(a, f.ID)
			if b, err := os.ReadFile(kept); err != nil || !bytes.Equal(b, want) {
				t.Errorf("the kept %s = %q, %v", f.Name, b, err)
			}
		}
		if ignore, err := os.ReadFile(filepath.Join(base, ".gitignore")); err != nil || !strings.Contains(string(ignore), "\n*\n") {
			t.Errorf("the attachments' .gitignore = %q, %v", ignore, err)
		}

		waitThread(t, m, a, "the turn to end", turnsEnded(1))
		var req acp.PromptRequest
		_ = json.Unmarshal(f.called(acp.MethodSessionPrompt)[0], &req)
		text := req.Prompt[0].Text
		if !strings.HasPrefix(text, "sum these\n\n[AgentBox: the user attached 2 file(s)") || !strings.Contains(text, user.Files[0].Path+" (text/csv, 17 B)") || !strings.Contains(text, user.Files[1].Path+" (application/pdf") {
			t.Errorf("embedded %v: the prompt's text = %q", embedded, text)
		}
		var resources []acp.ContentBlock
		for _, b := range req.Prompt[1:] {
			if b.Type == "resource" {
				resources = append(resources, b)
			}
		}
		if !embedded {
			if len(resources) != 0 {
				t.Errorf("an adapter without embeddedContext got %d resource blocks", len(resources))
			}
			continue
		}
		if len(resources) != 1 || resources[0].Resource == nil || resources[0].Resource.Text != string(csv) || resources[0].Resource.URI != "file://"+user.Files[0].Path || resources[0].Resource.MimeType != "text/csv" {
			t.Errorf("the resource blocks = %+v, want the CSV's alone", resources)
		}
	}
}

// A message may be files alone, and a picture for a tool that can't read
// pictures can be sent as one.
func TestAMessageOfOnlyFiles(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	f := newFakeTool(answerHello)
	m, _ := newManager(t, store, f)
	withImages(t, m)
	a := testAgent
	a.Worktree = t.TempDir()
	_, raw := pngUpload(t, "")
	if _, err := m.SendMessage(a, api.ChatMessageRequest{Files: []api.ChatFileUpload{fileUpload("shot.png", "image/png", raw)}}); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, a, "the turn to end", turnsEnded(1))
	var req acp.PromptRequest
	_ = json.Unmarshal(f.called(acp.MethodSessionPrompt)[0], &req)
	if len(req.Prompt) != 1 || !strings.HasPrefix(req.Prompt[0].Text, "[AgentBox: the user attached 1 file(s)") || !strings.Contains(req.Prompt[0].Text, "shot.png (image/png") {
		t.Errorf("the prompt = %+v, want the note on the file alone", req.Prompt)
	}
}

// Files over the limits are refused before anything of the message is
// taken, and none of what came with them is kept.
func TestFilesThatAreRefused(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	m, _ := newManager(t, store, newFakeTool(answerHello))
	dir := withImages(t, m)
	a := testAgent
	a.Worktree = t.TempDir()
	small := fileUpload("a.txt", "text/plain", []byte("hi"))
	half := fileUpload("half.bin", "", make([]byte, api.MaxChatFileBytes))
	many := make([]api.ChatFileUpload, api.MaxChatFiles+1)
	for i := range many {
		many[i] = small
	}
	img, _ := pngUpload(t, "a.png")
	for name, tc := range map[string]struct {
		msg  api.ChatMessageRequest
		want string
	}{
		"not base64":               {api.ChatMessageRequest{Files: []api.ChatFileUpload{{Name: "x", Data: "%%%"}}}, "base64"},
		"too big":                  {api.ChatMessageRequest{Files: []api.ChatFileUpload{fileUpload("big.bin", "", make([]byte, api.MaxChatFileBytes+1))}}, "over the 25.0 MiB a file may be"},
		"too big together":         {api.ChatMessageRequest{Files: []api.ChatFileUpload{half, half, small}}, "over the 50.0 MiB a message may carry"},
		"too many":                 {api.ChatMessageRequest{Files: many}, "at most 10 files"},
		"beside a refused picture": {api.ChatMessageRequest{Images: []api.ChatImageUpload{{MimeType: "image/bmp", Data: img.Data}}, Files: []api.ChatFileUpload{small}}, "PNG, JPEG"},
	} {
		if _, err := m.SendMessage(a, tc.msg); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error saying %q", name, err, tc.want)
		}
	}
	if th, _ := m.Thread(a); len(th.Items) != 0 {
		t.Errorf("a refused message left %v in the conversation", kinds(th))
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, a.Project, a.Name)); len(entries) != 0 {
		t.Errorf("refused messages left %d files kept", len(entries))
	}
	if entries, _ := os.ReadDir(filepath.Join(a.Worktree, ".agentbox", "attachments")); len(entries) > 1 {
		t.Errorf("refused messages left %d entries in the attachments folder", len(entries))
	}
}

// A file sent while a turn runs joins it, its note with the message it came with.
func TestAFileSentDuringATurn(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	release := make(chan struct{})
	f := newSteeringTool(func(f *fakeTool, s, _ string) acp.PromptResponse {
		<-release
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	f.embedded = true
	m, _ := newManager(t, store, f)
	withImages(t, m)
	a := testAgent
	a.Worktree = t.TempDir()
	if _, err := m.Send(a, "build it"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, a, "the turn to start", func(th api.ChatThread) bool { return len(f.called(acp.MethodSessionPrompt)) == 1 })
	aside, err := m.SendMessage(a, api.ChatMessageRequest{Text: "use this", Files: []api.ChatFileUpload{fileUpload("notes.md", "text/markdown", []byte("# notes"))}})
	if err != nil {
		t.Fatal(err)
	}
	if aside.Kind != "aside" || len(aside.Files) != 1 {
		t.Fatalf("the aside = %s with %d files", aside.Kind, len(aside.Files))
	}
	waitThread(t, m, a, "the steer", func(th api.ChatThread) bool { return len(f.called(acp.MethodSessionSteer)) == 1 })
	close(release)
	var req acp.SteerRequest
	_ = json.Unmarshal(f.called(acp.MethodSessionSteer)[0], &req)
	if len(req.Prompt) != 2 || !strings.Contains(req.Prompt[0].Text, aside.Files[0].Path) || req.Prompt[1].Type != "resource" || req.Prompt[1].Resource.Text != "# notes" {
		t.Errorf("the steer = %+v", req.Prompt)
	}
}

func TestFileName(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("é", 100) + ".csv"
	for in, want := range map[string]string{
		"report.pdf":        "report.pdf",
		"../../etc/passwd":  "passwd",
		`C:\Users\me\a.zip`: "a.zip",
		".env":              "env",
		"a\x00b\nc.txt":     "abc.txt",
		"":                  "file",
		"..":                "file",
		long:                strings.Repeat("é", 58) + ".csv",
	} {
		if got := fileName(in); got != want {
			t.Errorf("fileName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFileType(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		up   api.ChatFileUpload
		data string
		want string
	}{
		{api.ChatFileUpload{Name: "a.csv", MimeType: "text/csv; charset=utf-8"}, "a,b", "text/csv"},
		{api.ChatFileUpload{Name: "a.zip"}, "PK\x03\x04", "application/zip"},
		{api.ChatFileUpload{Name: "noext"}, "%PDF-1.4", "application/pdf"},
	} {
		if got := fileType(tc.up, []byte(tc.data)); got != tc.want {
			t.Errorf("fileType(%+v) = %q, want %q", tc.up, got, tc.want)
		}
	}
}

func TestOnlyTextIsEmbedded(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		mimeType string
		data     string
		want     bool
	}{
		{"text/csv", "a,b\n", true},
		{"application/json", `{"a":1}`, true},
		{"application/vnd.api+json", `{}`, true},
		{"application/pdf", "%PDF-1.4 all ascii", false},
		{"text/plain", "nul\x00inside", false},
		{"text/plain", "\xff\xfe", false},
		{"text/plain", "", false},
		{"text/plain", strings.Repeat("a", api.MaxChatEmbeddedBytes+1), false},
	} {
		if got := embeddable(tc.mimeType, []byte(tc.data)); got != tc.want {
			t.Errorf("embeddable(%s, %.20q) = %v, want %v", tc.mimeType, tc.data, got, tc.want)
		}
	}
}
