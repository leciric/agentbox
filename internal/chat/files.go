package chat

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"agentbox/internal/acp"
	"agentbox/internal/api"
)

// Files sent with a message that aren't pictures: a PDF, a CSV, a zip. No AI
// tool's adapter takes those as content of their own, so each is put where
// the tool can open it with its own tools, and its prompt names the path:
// AttachmentsDir in the chat's working directory (an agent's worktree, which
// its machine mounts at the same path, or the lead's), which ignores itself
// in git. The bytes are kept beside the pictures too (ImageDir, by id), for
// the app to show and open whatever happens to the copy. A small text file
// also goes as an ACP resource block, to an adapter that takes those
// (promptCapabilities.embeddedContext: claude-agent-acp, codex-acp and
// OpenCode all do), so the model has it without a tool call.
//
// They are the user's files, handed over as they are: nothing in them is
// scrubbed.

// AttachmentsDir is where, in a chat's working directory, the files sent in
// it are put, one directory per file so two of the same name don't meet.
const AttachmentsDir = ".agentbox/attachments"

// ignoreAll is the .gitignore AttachmentsDir keeps in itself, so nothing in
// it is ever committed, in any repository, without touching the project's own.
const ignoreAll = "# Files sent in AgentBox's chat: never committed.\n*\n"

// saveFiles checks the files sent with a message and keeps each one, beside
// the conversation and in its working directory, before anything of the
// message is in the conversation. The conversation is locked.
func (c *conversation) saveFiles(uploads []api.ChatFileUpload) ([]api.ChatFile, error) {
	if len(uploads) == 0 {
		return nil, nil
	}
	dir := c.m.imageDir(c.agent)
	if dir == "" {
		return nil, errors.New("this chat has nowhere to keep files")
	}
	if len(uploads) > api.MaxChatFiles {
		return nil, fmt.Errorf("a message takes at most %d files, not %d", api.MaxChatFiles, len(uploads))
	}
	datas := make([][]byte, len(uploads))
	total := 0
	for i, up := range uploads {
		what := "file " + strconv.Itoa(i+1)
		if up.Name != "" {
			what = strconv.Quote(up.Name)
		}
		data, err := base64.StdEncoding.DecodeString(up.Data)
		if err != nil {
			return nil, fmt.Errorf("%s isn't base64: %w", what, err)
		}
		if len(data) > api.MaxChatFileBytes {
			return nil, fmt.Errorf("%s is %s, over the %s a file may be", what, sizeOf(len(data)), sizeOf(api.MaxChatFileBytes))
		}
		total += len(data)
		datas[i] = data
	}
	if total > api.MaxChatFilesBytes {
		return nil, fmt.Errorf("the files come to %s, over the %s a message may carry", sizeOf(total), sizeOf(api.MaxChatFilesBytes))
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	files := make([]api.ChatFile, 0, len(uploads))
	undo := func() {
		for _, f := range files {
			_ = os.Remove(filepath.Join(dir, f.ID))
			if f.Path != "" {
				_ = os.RemoveAll(filepath.Dir(f.Path))
			}
		}
	}
	for i, up := range uploads {
		f := api.ChatFile{ID: newID(), Name: fileName(up.Name), MimeType: fileType(up, datas[i]), Size: int64(len(datas[i]))}
		if err := os.WriteFile(filepath.Join(dir, f.ID), datas[i], 0o600); err != nil {
			undo()
			return nil, fmt.Errorf("keeping %s: %w", strconv.Quote(f.Name), err)
		}
		path, err := c.placeFile(f, datas[i])
		if err != nil {
			_ = os.Remove(filepath.Join(dir, f.ID))
			undo()
			return nil, fmt.Errorf("putting %s where %s can read it: %w", strconv.Quote(f.Name), ToolNames[c.agent.AI], err)
		}
		f.Path = path
		files = append(files, f)
	}
	return files, nil
}

// placeFile puts a copy of a file where the chat's AI tool can read it, and
// says where: "" for a chat with no working directory. Files there are left
// readable, as the worktree's own are, for the agent's machine to open.
func (c *conversation) placeFile(f api.ChatFile, data []byte) (string, error) {
	root := c.agent.Worktree
	if root == "" {
		return "", nil
	}
	base := filepath.Join(root, filepath.FromSlash(AttachmentsDir))
	if err := os.MkdirAll(filepath.Join(base, f.ID), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(base, ".gitignore"), []byte(ignoreAll), 0o644); err != nil {
		return "", err
	}
	path := filepath.Join(base, f.ID, f.Name)
	return path, os.WriteFile(path, data, 0o644)
}

// fileName makes the name a file was sent with one that is safe to put on
// disk: its last element only, no control characters, not hidden, at most
// 120 bytes with its extension kept.
func fileName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = name[strings.LastIndex(name, "/")+1:]
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
	name = strings.TrimLeft(strings.TrimSpace(name), ".")
	if name == "" {
		return "file"
	}
	if len(name) > 120 {
		ext := filepath.Ext(name)
		if len(ext) > 20 {
			ext = ""
		}
		stem := name[:120-len(ext)]
		for !utf8.ValidString(stem) {
			stem = stem[:len(stem)-1]
		}
		name = stem + ext
	}
	return name
}

// fileType is a file's MIME type: what it was sent as, else what its
// extension says, else what its bytes look like.
func fileType(up api.ChatFileUpload, data []byte) string {
	if t, _, err := mime.ParseMediaType(up.MimeType); err == nil && strings.Contains(t, "/") {
		return t
	}
	if t := mime.TypeByExtension(filepath.Ext(up.Name)); t != "" {
		if t, _, err := mime.ParseMediaType(t); err == nil {
			return t
		}
	}
	t, _, _ := mime.ParseMediaType(http.DetectContentType(data))
	return t
}

// embeddable reports whether a file goes to the model in the prompt itself:
// small, of a text type, and UTF-8 with no NULs. Type matters as well as
// bytes: a PDF can be all ASCII and still no text to read.
func embeddable(mimeType string, data []byte) bool {
	return len(data) > 0 && len(data) <= api.MaxChatEmbeddedBytes && textType(mimeType) && utf8.Valid(data) && !bytes.ContainsRune(data, 0)
}

// textTypes are the application/ types that are text to read.
var textTypes = map[string]bool{
	"application/json": true, "application/xml": true, "application/yaml": true, "application/x-yaml": true,
	"application/toml": true, "application/javascript": true, "application/x-sh": true, "application/sql": true,
	"application/x-ndjson": true, "application/csv": true,
}

func textType(t string) bool {
	return strings.HasPrefix(t, "text/") || textTypes[t] || strings.HasSuffix(t, "+json") || strings.HasSuffix(t, "+xml")
}

// filesNote is what a prompt says of the files sent with it: where each is.
func filesNote(files []api.ChatFile) string {
	if len(files) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[AgentBox: the user attached %d file(s) to this message, saved where you can read them with your tools:", len(files))
	for _, f := range files {
		where := f.Path
		if where == "" {
			where = "(not saved: this chat has no working directory)"
		}
		fmt.Fprintf(&b, "\n- %s (%s, %s)", where, cmp(f.MimeType, "unknown type"), sizeOf(int(f.Size)))
	}
	b.WriteString("]")
	return b.String()
}

// fileBlocks are the resource blocks for the files small enough to embed,
// read back from where they are kept. The conversation needn't be locked.
func fileBlocks(dir string, files []api.ChatFile) []acp.ContentBlock {
	var blocks []acp.ContentBlock
	for _, f := range files {
		if f.Size > api.MaxChatEmbeddedBytes || !imageID.MatchString(f.ID) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, f.ID))
		if err != nil || !embeddable(f.MimeType, data) {
			continue
		}
		uri := "file://" + filepath.ToSlash(cmp(f.Path, f.Name))
		blocks = append(blocks, acp.ContentBlock{Type: "resource", Resource: &acp.EmbeddedResource{URI: uri, MimeType: f.MimeType, Text: string(data)}})
	}
	return blocks
}
