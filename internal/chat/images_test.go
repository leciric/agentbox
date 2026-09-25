package chat

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/acp"
	"agentbox/internal/api"
)

func pngUpload(t *testing.T, name string) (api.ChatImageUpload, []byte) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return api.ChatImageUpload{MimeType: "image/png", Name: name, Data: base64.StdEncoding.EncodeToString(b.Bytes())}, b.Bytes()
}

func withImages(t *testing.T, m *Manager) string {
	dir := t.TempDir()
	m.ImageDir = func(project, agent string) string { return filepath.Join(dir, project, agent) }
	return dir
}

// A picture sent with a message reaches the AI tool as an ACP image block
// beside the text, is kept out of the conversation's row in a file of its own,
// and is still there for a daemon that starts afresh.
func TestAnImageReachesTheToolAndOutlivesTheDaemon(t *testing.T) {
	store := openStore(t)
	f := newFakeTool(answerHello)
	f.images = true
	m, _ := newManager(t, store, f)
	dir := withImages(t, m)

	up, raw := pngUpload(t, "shot.png")
	user, err := m.Send(testAgent, "what's in this?", up)
	if err != nil {
		t.Fatal(err)
	}
	if len(user.Images) != 1 || user.Images[0].MimeType != "image/png" || user.Images[0].Name != "shot.png" || user.Images[0].Size != int64(len(raw)) {
		t.Fatalf("the message's images = %+v", user.Images)
	}
	waitThread(t, m, testAgent, "the turn to end", turnsEnded(1))

	var req acp.PromptRequest
	_ = json.Unmarshal(f.called(acp.MethodSessionPrompt)[0], &req)
	if len(req.Prompt) != 2 || req.Prompt[0].Type != "text" || req.Prompt[0].Text != "what's in this?" {
		t.Fatalf("the prompt = %+v, want the text and then the image", req.Prompt)
	}
	if b := req.Prompt[1]; b.Type != "image" || b.MimeType != "image/png" || b.Data != up.Data {
		t.Errorf("the image block = %s %s, %d bytes of base64", b.Type, b.MimeType, len(b.Data))
	}

	path, err := m.ImagePath(testAgent, user.Images[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, raw) {
		t.Errorf("the kept file = %d bytes, %v", len(got), err)
	}
	if !strings.HasPrefix(path, dir) {
		t.Errorf("kept at %s, outside the image directory %s", path, dir)
	}
	rows, _ := store.ChatItems(t.Context(), testAgent.Project, testAgent.Name)
	for _, row := range rows {
		if bytes.Contains(row.Data, []byte(up.Data[:40])) {
			t.Errorf("the image's bytes are in the conversation's row: %.120s", row.Data)
		}
	}

	again, _ := newManager(t, store, newFakeTool(answerHello))
	again.ImageDir = m.ImageDir
	th, err := again.Thread(testAgent)
	if err != nil {
		t.Fatal(err)
	}
	if got := find(th, "user", 0).Images; len(got) != 1 || got[0].ID != user.Images[0].ID {
		t.Errorf("after a restart the message's images = %+v", got)
	}
	if th.Session.NoImages {
		t.Error("an adapter that takes images was remembered as one that doesn't")
	}

	if _, err := m.ImagePath(testAgent, "../../state.db"); err == nil {
		t.Error("an image id that is a path was taken")
	}
	if err := m.Clear(testAgent); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("clearing the chat left its image behind: %v", err)
	}
}

// A message may be pictures alone.
func TestAMessageOfOnlyImages(t *testing.T) {
	store := openStore(t)
	f := newFakeTool(answerHello)
	f.images = true
	m, _ := newManager(t, store, f)
	withImages(t, m)
	up, _ := pngUpload(t, "")
	if _, err := m.Send(testAgent, "  ", up); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn to end", turnsEnded(1))
	var req acp.PromptRequest
	_ = json.Unmarshal(f.called(acp.MethodSessionPrompt)[0], &req)
	if len(req.Prompt) != 1 || req.Prompt[0].Type != "image" {
		t.Errorf("the prompt = %+v, want the image alone", req.Prompt)
	}
}

// What isn't an image AgentBox can pass on is refused before anything of the
// message is taken.
func TestImagesThatAreRefused(t *testing.T) {
	store := openStore(t)
	f := newFakeTool(answerHello)
	f.images = true
	m, _ := newManager(t, store, f)
	withImages(t, m)
	good, _ := pngUpload(t, "a.png")
	big := make([]byte, api.MaxChatImageBytes+1)
	copy(big, "\x89PNG\r\n\x1a\n")
	for name, tc := range map[string]struct {
		images []api.ChatImageUpload
		want   string
	}{
		"a type the model can't read":     {[]api.ChatImageUpload{{MimeType: "image/bmp", Data: good.Data}}, "PNG, JPEG, GIF or WebP"},
		"a type that isn't what it holds": {[]api.ChatImageUpload{{MimeType: "image/jpeg", Data: good.Data}}, "holds image/png"},
		"not base64":                      {[]api.ChatImageUpload{{MimeType: "image/png", Data: "%%%"}}, "base64"},
		"too big":                         {[]api.ChatImageUpload{{MimeType: "image/png", Data: base64.StdEncoding.EncodeToString(big)}}, "over the 3.8 MiB"},
		"too many":                        {[]api.ChatImageUpload{good, good, good, good, good, good, good, good, good}, "at most 8"},
	} {
		if _, err := m.Send(testAgent, "look", tc.images...); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error saying %q", name, err, tc.want)
		}
	}
	if th, _ := m.Thread(testAgent); len(th.Items) != 0 {
		t.Errorf("a refused message left %v in the conversation", kinds(th))
	}
}

// An adapter that says it can't read images is taken at its word: the
// session says so, for the composer, from then on — a later daemon included
// — and a message carrying one is refused rather than quietly stripped.
func TestAnAdapterWithoutImagesRefusesThem(t *testing.T) {
	store := openStore(t)
	f := newFakeTool(answerHello)
	m, _ := newManager(t, store, f)
	withImages(t, m)
	if _, err := m.Send(testAgent, "hi"); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, testAgent, "the turn to end", turnsEnded(1))
	if !th.Session.NoImages {
		t.Fatal("the session doesn't say its adapter can't take images")
	}
	up, _ := pngUpload(t, "a.png")
	if _, err := m.Send(testAgent, "and this", up); err == nil || !strings.Contains(err.Error(), "doesn't take images") {
		t.Errorf("an image for an adapter without them: %v", err)
	}

	again, _ := newManager(t, store, newFakeTool(answerHello))
	other := testAgent
	other.Name = "agent-02"
	if th, _ := again.Thread(other); !th.Session.NoImages {
		t.Error("before its first session, another chat of the same tool doesn't know it can't take images")
	}
}

// An image sent while a turn runs joins it, with the message it came with.
func TestAnImageSentDuringATurn(t *testing.T) {
	store := openStore(t)
	release := make(chan struct{})
	f := newSteeringTool(func(f *fakeTool, s, _ string) acp.PromptResponse {
		<-release
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	f.images = true
	m, _ := newManager(t, store, f)
	withImages(t, m)
	if _, err := m.Send(testAgent, "build it"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn to start", func(th api.ChatThread) bool { return th.Session.State == api.ChatRunning })
	up, _ := pngUpload(t, "bug.png")
	aside, err := m.Send(testAgent, "it looks like this", up)
	if err != nil {
		t.Fatal(err)
	}
	if aside.Kind != "aside" || len(aside.Images) != 1 {
		t.Errorf("the aside = %+v", aside)
	}
	f.waitSteer(t)
	close(release)
	waitThread(t, m, testAgent, "the turn to end", turnsEnded(1))
	var req acp.SteerRequest
	_ = json.Unmarshal(f.called(acp.MethodSessionSteer)[0], &req)
	if len(req.Prompt) != 2 || req.Prompt[1].Type != "image" || req.Prompt[1].Data != up.Data {
		t.Errorf("the steered prompt has %d blocks, want the text and the image", len(req.Prompt))
	}
}
