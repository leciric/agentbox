package daemon

import (
	"context"
	"net/http"
	"os"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/chat"
	"agentbox/internal/state"
)

// The chat: each agent's AI tool behind its ACP adapter, run by package chat.

// launchChat starts an ACP adapter: inside the agent, or on the host for a
// project's lead, which has no machine of its own.
func (s *Server) launchChat(ctx context.Context, a state.Agent, status func(string)) (*chat.Process, error) {
	// The daemon's log, so what a failing chat did is recoverable afterwards.
	m := s.manager(s.cfg.Log)
	launch := m.ChatCommand
	if a.IsLead() {
		launch = m.LeadChatCommand
	}
	cmd, err := launch(ctx, a, status)
	if err != nil {
		return nil, err
	}
	return chat.StartCommand(cmd)
}

// prepareChatModel writes the model a chat should start on, and the context it
// compacts at, into its AI tool's own configuration, before launchChat starts
// the adapter. Both paths the manager has — an agent's own machine, and a
// project's lead on the host — lead to a HOME of their own, so this is scoped
// per agent (see PrepareChatModel). The chat works the compact window out at
// every start, from the chat's own context window and the installation's
// (D83, D91), so a change to either reaches its next session.
func (s *Server) prepareChatModel(ctx context.Context, a state.Agent, model string, window int64) error {
	return s.manager(s.cfg.Log).PrepareChatModel(ctx, a, model, window)
}

// agentInfo describes an agent for the API, with the state of its chat.
func (s *Server) agentInfo(st agent.Status) api.Agent {
	info := toAPIAgent(st)
	info.Chat = s.chat.State(info.Ref)
	return info
}

func (s *Server) getChat(from agentFrom) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		a, err := from(r)
		if err != nil {
			return err
		}
		thread, err := s.chat.Thread(a)
		if err != nil {
			return err
		}
		return writeJSON(w, http.StatusOK, thread)
	}
}

func (s *Server) startChat(from agentFrom) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		a, err := from(r)
		if err != nil {
			return err
		}
		session, err := s.chat.Start(a)
		if err != nil {
			return err
		}
		return writeJSON(w, http.StatusOK, session)
	}
}

// sendChat answers with your message at once; the turn it starts follows as events.
func (s *Server) sendChat(from agentFrom) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		a, err := from(r)
		if err != nil {
			return err
		}
		var req api.ChatMessageRequest
		if err := readJSON(r, &req); err != nil {
			return err
		}
		item, err := s.chat.Send(a, req.Text, req.Images...)
		if err != nil {
			return err
		}
		return writeJSON(w, http.StatusAccepted, item)
	}
}

// chatImage serves a picture sent in the chat. An image never changes once
// sent, so the app may keep it as long as it likes.
func (s *Server) chatImage(from agentFrom) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		a, err := from(r)
		if err != nil {
			return err
		}
		path, err := s.chat.ImagePath(a, r.PathValue("image"))
		if err == nil {
			_, err = os.Stat(path)
		}
		if err != nil {
			http.NotFound(w, r)
			return nil
		}
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.ServeFile(w, r, path)
		return nil
	}
}

func (s *Server) cancelChat(from agentFrom) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		a, err := from(r)
		if err != nil {
			return err
		}
		session, err := s.chat.Cancel(a)
		if err != nil {
			return err
		}
		return writeJSON(w, http.StatusOK, session)
	}
}

func (s *Server) answerChat(from agentFrom) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		a, err := from(r)
		if err != nil {
			return err
		}
		var req api.ChatAnswerRequest
		if err := readJSON(r, &req); err != nil {
			return err
		}
		item, err := s.chat.Answer(a, r.PathValue("item"), req.OptionID)
		if err != nil {
			return err
		}
		return writeJSON(w, http.StatusOK, item)
	}
}

func (s *Server) setChatOption(from agentFrom) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		a, err := from(r)
		if err != nil {
			return err
		}
		var req api.ChatOptionRequest
		if err := readJSON(r, &req); err != nil {
			return err
		}
		session, err := s.chat.SetOption(r.Context(), a, r.PathValue("option"), req.Value)
		if err != nil {
			return err
		}
		return writeJSON(w, http.StatusOK, session)
	}
}

func (s *Server) clearChat(from agentFrom) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		a, err := from(r)
		if err != nil {
			return err
		}
		if err := s.chat.Clear(a); err != nil {
			return err
		}
		// A new chat has no cache to lose.
		if a.IsLead() {
			s.settleLeadCache(a.Project, true)
		}
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
}
