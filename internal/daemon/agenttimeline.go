package daemon

import (
	"net/http"

	"agentbox/internal/api"
	"agentbox/internal/chat"
	"agentbox/internal/timeline"
)

// agentTimeline is GET /v1/agents/{project}/{agent}/timeline: what the agent
// did, as steps read out of its whole chat, with its screenshots, recordings
// and reports in the steps that made them (internal/timeline). A past agent's
// is read from the chat and media it left.
func (s *Server) agentTimeline(w http.ResponseWriter, r *http.Request) error {
	a, err := s.agentOrArchivedFromPath(r)
	if err != nil {
		return err
	}
	// An agent with no AI tool to chat with (made before the chat) has no
	// steps, only its media.
	var items []api.ChatItem
	if _, ok := chat.ToolNames[a.AI]; ok {
		thread, err := s.chat.Page(a, "", 0)
		if err != nil {
			return err
		}
		items = thread.Items
	}
	kept, err := s.store.Media(r.Context(), a.Project, a.Name)
	if err != nil {
		return err
	}
	media := make([]api.MediaItem, 0, len(kept))
	for _, m := range kept {
		media = append(media, toAPIMedia(m, ""))
	}
	return writeJSON(w, http.StatusOK, timeline.Build(items, media))
}
