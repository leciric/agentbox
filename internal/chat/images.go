package chat

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"agentbox/internal/acp"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// Pictures sent with a message. Each is a file of its own in the directory
// Manager.ImageDir names, kept out of state.db, where the conversation lives:
// the item records only its id, type and size, and the app loads the picture
// from .../chat/images/{id}. The adapter is given them as ACP image blocks
// beside the message's text, read back from those files when the prompt goes.

// imageTypes are the types a picture may be: what Anthropic's API reads, and
// what http.DetectContentType recognises, so what a file says it is can be
// checked against what it holds.
var imageTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}

var imageID = regexp.MustCompile(`^[0-9a-f]{16}$`)

// imageSetting is where whether a tool's adapter takes images is remembered,
// so the composer knows before that tool's first session of the day starts.
func imageSetting(ai string) string { return "chat_images_" + ai }

func (m *Manager) imageDir(a state.Agent) string {
	if m.ImageDir == nil {
		return ""
	}
	return m.ImageDir(a.Project, a.Name)
}

// ImagePath is the file holding one of the pictures sent in an agent's chat.
func (m *Manager) ImagePath(a state.Agent, id string) (string, error) {
	dir := m.imageDir(a)
	if dir == "" || !imageID.MatchString(id) {
		return "", os.ErrNotExist
	}
	return filepath.Join(dir, id), nil
}

// removeImages throws away every picture sent in an agent's chat, for a
// conversation that is being cleared or an agent that is going.
func (m *Manager) removeImages(project, name string) {
	if m.ImageDir == nil {
		return
	}
	if err := os.RemoveAll(m.ImageDir(project, name)); err != nil {
		m.logf("chat %s/%s: removing its images: %v", project, name, err)
	}
}

// saveImages checks the pictures sent with a message and keeps each one in a
// file, before anything of the message is in the conversation: a message is
// either taken whole or refused. The conversation is locked.
func (c *conversation) saveImages(uploads []api.ChatImageUpload) ([]api.ChatImage, error) {
	if len(uploads) == 0 {
		return nil, nil
	}
	if c.session.NoImages {
		return nil, fmt.Errorf("%s doesn't take images here: its ACP adapter says it can't read them", ToolNames[c.agent.AI])
	}
	dir := c.m.imageDir(c.agent)
	if dir == "" {
		return nil, errors.New("this chat has nowhere to keep images")
	}
	if len(uploads) > api.MaxChatImages {
		return nil, fmt.Errorf("a message takes at most %d images, not %d", api.MaxChatImages, len(uploads))
	}
	datas := make([][]byte, len(uploads))
	for i, up := range uploads {
		what := "image " + strconv.Itoa(i+1)
		if up.Name != "" {
			what = strconv.Quote(up.Name)
		}
		data, err := base64.StdEncoding.DecodeString(up.Data)
		if err != nil {
			return nil, fmt.Errorf("%s isn't base64: %w", what, err)
		}
		if len(data) == 0 {
			return nil, fmt.Errorf("%s is empty", what)
		}
		if len(data) > api.MaxChatImageBytes {
			return nil, fmt.Errorf("%s is %s, over the %s an image may be", what, sizeOf(len(data)), sizeOf(api.MaxChatImageBytes))
		}
		if !imageTypes[up.MimeType] {
			return nil, fmt.Errorf("%s is %q: an image must be PNG, JPEG, GIF or WebP", what, up.MimeType)
		}
		if sniffed := http.DetectContentType(data); sniffed != up.MimeType {
			return nil, fmt.Errorf("%s says it is %s but holds %s", what, up.MimeType, sniffed)
		}
		datas[i] = data
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	images := make([]api.ChatImage, 0, len(uploads))
	for i, up := range uploads {
		id := newID()
		if err := os.WriteFile(filepath.Join(dir, id), datas[i], 0o600); err != nil {
			for _, img := range images {
				_ = os.Remove(filepath.Join(dir, img.ID))
			}
			return nil, fmt.Errorf("keeping an image: %w", err)
		}
		images = append(images, api.ChatImage{ID: id, MimeType: up.MimeType, Name: up.Name, Size: int64(len(datas[i]))})
	}
	return images, nil
}

// promptBlocks is a prompt's content: its text blocks (one, or two for a
// Claude Code skill: skillPrompt), the last saying where the files sent with
// it are, then its pictures read back from their files, then the files small
// enough to embed, for an adapter that takes them (embed). A picture that has
// gone missing is said in the text rather than failing the turn. The
// conversation needn't be locked.
func (c *conversation) promptBlocks(dir string, texts []string, sent attached, embed bool) []acp.ContentBlock {
	var blocks []acp.ContentBlock
	var missing []string
	for _, img := range sent.images {
		data, err := os.ReadFile(filepath.Join(dir, img.ID))
		if err != nil {
			missing = append(missing, cmp(img.Name, img.ID))
			continue
		}
		blocks = append(blocks, acp.ContentBlock{Type: "image", MimeType: img.MimeType, Data: base64.StdEncoding.EncodeToString(data)})
	}
	var notes []string
	if len(missing) > 0 {
		notes = append(notes, fmt.Sprintf("[AgentBox: %d image(s) sent with this message couldn't be read back, and aren't attached: %v]", len(missing), missing))
	}
	if note := filesNote(sent.files); note != "" {
		notes = append(notes, note)
	}
	if note := strings.Join(notes, "\n\n"); note != "" {
		if len(texts) == 0 || texts[len(texts)-1] == "" {
			texts = append(texts[:max(len(texts)-1, 0)], note)
		} else {
			texts[len(texts)-1] += "\n\n" + note
		}
	}
	if embed {
		blocks = append(blocks, fileBlocks(dir, sent.files)...)
	}
	var out []acp.ContentBlock
	for _, text := range texts {
		if text != "" {
			out = append(out, acp.ContentBlock{Type: "text", Text: text})
		}
	}
	return append(out, blocks...)
}

// attached is what a message carries besides its text, once kept.
type attached struct {
	images []api.ChatImage
	files  []api.ChatFile
}

func (a attached) join(b attached) attached {
	return attached{images: append(a.images, b.images...), files: append(a.files, b.files...)}
}

// saveAttached keeps a message's pictures and files, or neither. The
// conversation is locked.
func (c *conversation) saveAttached(msg api.ChatMessageRequest) (attached, error) {
	images, err := c.saveImages(msg.Images)
	if err != nil {
		return attached{}, err
	}
	files, err := c.saveFiles(msg.Files)
	if err != nil {
		dir := c.m.imageDir(c.agent)
		for _, img := range images {
			_ = os.Remove(filepath.Join(dir, img.ID))
		}
		return attached{}, err
	}
	return attached{images: images, files: files}, nil
}

// setImageSupport records whether the running adapter takes images, for the
// composer and for every later session of the same tool. The conversation is
// locked.
func (c *conversation) setImageSupport(ok bool) {
	c.session.NoImages = !ok
	if err := c.m.Store.SetSetting(context.Background(), imageSetting(c.agent.AI), strconv.FormatBool(ok)); err != nil {
		c.m.logf("chat %s: remembering whether it takes images: %v", c.agent.Ref(), err)
	}
}

// rememberedImageSupport seeds NoImages, before a session has started, from
// what this tool's adapter said last time. The conversation is locked.
func (c *conversation) rememberedImageSupport() {
	raw, err := c.m.Store.Setting(context.Background(), imageSetting(c.agent.AI))
	if err != nil || raw == "" {
		return
	}
	ok, err := strconv.ParseBool(raw)
	c.session.NoImages = err == nil && !ok
}

func sizeOf(n int) string {
	if n < 1<<10 {
		return fmt.Sprintf("%d B", n)
	}
	if n < 1<<20 {
		return fmt.Sprintf("%.0f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
}
