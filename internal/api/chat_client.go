package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
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
