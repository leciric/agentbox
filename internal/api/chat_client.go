package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// chatPath is where a chat lives. A ref with no slash, like "pawly", is a
// project's own chat, driven by its lead; "pawly/agent-01" is that agent's.
func chatPath(ref string) (string, error) {
	project, name, ok := strings.Cut(ref, "/")
	if project == "" {
		return "", errors.New("name a project, like pawly, or an agent, like pawly/agent-01")
	}
	// "pawly" and "pawly/lead" are both the project's chat.
	if !ok || name == LeadName {
		return "/v1/projects/" + url.PathEscape(project) + "/chat", nil
	}
	path, err := AgentPath(ref)
	return path + "/chat", err
}

// IsProjectChat reports whether a chat ref names a project rather than an agent.
func IsProjectChat(ref string) bool {
	_, name, ok := strings.Cut(ref, "/")
	return !ok || name == LeadName
}

func (c *Client) chatDo(ctx context.Context, method, ref, suffix string, body, out any) error {
	path, err := chatPath(ref)
	if err != nil {
		return err
	}
	return c.do(ctx, method, path+suffix, body, out)
}

// ProjectChat describes a project's chat, and whether it has been used yet.
func (c *Client) ProjectChat(ctx context.Context, project string) (ProjectChat, error) {
	var out ProjectChat
	return out, c.do(ctx, http.MethodGet, "/v1/projects/"+url.PathEscape(project)+"/lead", nil, &out)
}

// ResetProjectChat throws a project's chat away: its conversation, its worktree
// and its private HOME. The project's agents are untouched.
func (c *Client) ResetProjectChat(ctx context.Context, project string) error {
	return c.do(ctx, http.MethodDelete, "/v1/projects/"+url.PathEscape(project)+"/lead", nil, nil)
}

// Chat returns an agent's conversation.
func (c *Client) Chat(ctx context.Context, ref string) (ChatThread, error) {
	var out ChatThread
	return out, c.chatDo(ctx, http.MethodGet, ref, "", nil, &out)
}

// ChatPage returns a page of an agent's conversation: the items before the one
// called before (the end of the conversation when before is ""), going back
// far enough to hold limit messages, and starting where a turn does. Older
// says whether there is more before it.
func (c *Client) ChatPage(ctx context.Context, ref, before string, limit int) (ChatThread, error) {
	q := url.Values{"limit": {strconv.Itoa(limit)}}
	if before != "" {
		q.Set("before", before)
	}
	var out ChatThread
	return out, c.chatDo(ctx, http.MethodGet, ref, "?"+q.Encode(), nil, &out)
}

// ChatAround returns the part of an agent's conversation around the item id:
// n messages before it and n after, in whole turns (see ChatThread's Older
// and Newer). An id the conversation doesn't have is an empty page.
func (c *Client) ChatAround(ctx context.Context, ref, id string, n int) (ChatThread, error) {
	q := url.Values{"around": {id}, "limit": {strconv.Itoa(n)}}
	var out ChatThread
	return out, c.chatDo(ctx, http.MethodGet, ref, "?"+q.Encode(), nil, &out)
}

// ChatAfter returns the page after the item called after, going forward far
// enough to hold limit messages and ending where a turn does.
func (c *Client) ChatAfter(ctx context.Context, ref, after string, limit int) (ChatThread, error) {
	q := url.Values{"after": {after}, "limit": {strconv.Itoa(limit)}}
	var out ChatThread
	return out, c.chatDo(ctx, http.MethodGet, ref, "?"+q.Encode(), nil, &out)
}

// SearchChat finds query in a conversation (see ChatSearch), with at most
// limit hits; 0 is the daemon's default. kinds, when given, keeps only items
// of those kinds (user, aside, assistant, notice, error).
func (c *Client) SearchChat(ctx context.Context, ref, query string, kinds []string, limit int) (ChatSearch, error) {
	q := url.Values{"q": {query}}
	if len(kinds) > 0 {
		q.Set("kinds", strings.Join(kinds, ","))
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var out ChatSearch
	return out, c.chatDo(ctx, http.MethodGet, ref, "/search?"+q.Encode(), nil, &out)
}

// StartChat starts the agent's AI tool for its chat, unless it runs.
func (c *Client) StartChat(ctx context.Context, ref string) (ChatSession, error) {
	var out ChatSession
	return out, c.chatDo(ctx, http.MethodPost, ref, "/start", nil, &out)
}

// SendChat sends a message, with any pictures, which starts a turn; the turn
// follows as events.
func (c *Client) SendChat(ctx context.Context, ref, text string, images ...ChatImageUpload) (ChatItem, error) {
	var out ChatItem
	return out, c.chatDo(ctx, http.MethodPost, ref, "/messages", ChatMessageRequest{Text: text, Images: images}, &out)
}

func (c *Client) CancelChat(ctx context.Context, ref string) (ChatSession, error) {
	var out ChatSession
	return out, c.chatDo(ctx, http.MethodPost, ref, "/cancel", nil, &out)
}

// AnswerChat answers a permission request with one of its options; "" cancels it.
func (c *Client) AnswerChat(ctx context.Context, ref, item, option string) (ChatItem, error) {
	var out ChatItem
	return out, c.chatDo(ctx, http.MethodPost, ref, "/permissions/"+url.PathEscape(item), ChatAnswerRequest{OptionID: option}, &out)
}

func (c *Client) SetChatOption(ctx context.Context, ref, option, value string) (ChatSession, error) {
	var out ChatSession
	return out, c.chatDo(ctx, http.MethodPut, ref, "/options/"+url.PathEscape(option), ChatOptionRequest{Value: value}, &out)
}

// ClearChat removes the conversation; the next message starts a new session.
func (c *Client) ClearChat(ctx context.Context, ref string) error {
	return c.chatDo(ctx, http.MethodDelete, ref, "", nil, nil)
}

// filesPath is where an agent's or a project's lead's worktree file listing
// lives, following the same ref rule as chatPath: "pawly" and "pawly/lead"
// both name the project's lead.
func filesPath(ref string) (string, error) {
	project, name, ok := strings.Cut(ref, "/")
	if project == "" {
		return "", errors.New("name a project, like pawly, or an agent, like pawly/agent-01")
	}
	if !ok || name == LeadName {
		return "/v1/projects/" + url.PathEscape(project) + "/files", nil
	}
	path, err := AgentPath(ref)
	return path + "/files", err
}

// Files lists an agent's or a project lead's worktree files, for @ mentions
// in the composer.
func (c *Client) Files(ctx context.Context, ref string) (WorktreeFiles, error) {
	var out WorktreeFiles
	path, err := filesPath(ref)
	if err != nil {
		return out, err
	}
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// ChatRunners lists the AI tools and Claude Code accounts a project's chat (or
// Home's, HomeProject) can switch to.
func (c *Client) ChatRunners(ctx context.Context, project string) (LeadRunners, error) {
	var out LeadRunners
	return out, c.do(ctx, http.MethodGet, "/v1/projects/"+url.PathEscape(project)+"/chat/runners", nil, &out)
}

// SetChatRunner chooses the AI tool and account a project's chat runs from its
// next message.
func (c *Client) SetChatRunner(ctx context.Context, project string, req LeadRunnerRequest) (ChatSession, error) {
	var out ChatSession
	return out, c.do(ctx, http.MethodPut, "/v1/projects/"+url.PathEscape(project)+"/chat/runner", req, &out)
}

// ListFolder lists one folder of an agent's or a project lead's worktree, for
// the file tree; withIgnored includes what .gitignore leaves out.
func (c *Client) ListFolder(ctx context.Context, ref, dir string, withIgnored bool) (FileList, error) {
	var out FileList
	q := url.Values{"path": {dir}}
	if withIgnored {
		q.Set("ignored", "1")
	}
	return out, c.filesDo(ctx, ref, "/list?"+q.Encode(), &out)
}

// ReadFile reads one file of a worktree for the viewer. full asks for more
// than the usual cap of a large text file, base for what it was at the
// worktree's base when it changed.
func (c *Client) ReadFile(ctx context.Context, ref, file string, full, base bool) (FileContent, error) {
	var out FileContent
	q := url.Values{"path": {file}}
	if full {
		q.Set("full", "1")
	}
	if base {
		q.Set("base", "1")
	}
	return out, c.filesDo(ctx, ref, "/read?"+q.Encode(), &out)
}

// FileStatus is what changed in a worktree since its base; fresh reads it
// again rather than from the daemon's cache.
func (c *Client) FileStatus(ctx context.Context, ref string, fresh bool) (FileStatus, error) {
	var out FileStatus
	rest := "/status"
	if fresh {
		rest += "?fresh=1"
	}
	return out, c.filesDo(ctx, ref, rest, &out)
}

func (c *Client) filesDo(ctx context.Context, ref, rest string, out any) error {
	path, err := filesPath(ref)
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodGet, path+rest, nil, out)
}
