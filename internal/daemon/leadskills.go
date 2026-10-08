package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"agentbox/internal/api"
	"agentbox/internal/skills"
	"agentbox/internal/state"
)

// A lead manages skills: it lists, reads and writes them, and turns them on
// in its project or AgentBox-wide, without asking. What takes something away
// from the user's agents — a skill's text changed, a skill deleted, a skill
// turned off where it was on — waits for the user: AgentBox puts a card in
// the lead's chat, the same one the AI tool's own permission requests make
// (chat.Manager.Approve), and does it only once they approve. The lead's tool
// call waits on the card, so the lead hears what the user said.

// errRefused is the user saying no to a lead's change: the lead is told, and
// nothing changes.
var errRefused = errors.New("the user refused")

// approveInChat is Server.approve: a card in the project's chat.
func (s *Server) approveInChat(ctx context.Context, project string, req api.ChatPermission) (bool, error) {
	lead, err := s.manager(nil).Lead(ctx, project)
	if err != nil {
		return false, err
	}
	return s.chat.Approve(ctx, lead, req)
}

// askApproval waits for the user's approval of what a project's lead asked
// for, and is errRefused, said in the lead's terms, when they don't give it.
func (s *Server) askApproval(ctx context.Context, project string, req api.ChatPermission) error {
	req.Approval = "skill"
	ok, err := s.approve(ctx, project, req)
	if err != nil {
		return fmt.Errorf("asking the user to approve it: %w", err)
	}
	if !ok {
		s.logf("%s's chat: the user refused: %s", project, req.Title)
		return fmt.Errorf("%w: %s. Nothing changed; don't ask again unless they say so", errRefused, strings.ToLower(req.Title[:1])+req.Title[1:])
	}
	s.logf("%s's chat: the user approved: %s", project, req.Title)
	return nil
}

// leadCreateSkill stores a skill the lead wrote. It never replaces one: that
// is an edit, which the user approves.
func (s *Server) leadCreateSkill(w http.ResponseWriter, r *http.Request) error {
	project := r.PathValue("project")
	var req api.LeadNewSkillRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if err := skills.ValidateName(req.Name); err != nil {
		return err
	}
	if _, err := s.store.Skill(r.Context(), req.Name); err == nil {
		return fmt.Errorf("skill %s: %w: edit_skill changes it, with the user's approval", req.Name, state.ErrExists)
	}
	save := api.SaveSkillRequest{Content: req.Content, Project: project}
	if req.Everywhere {
		on := true
		save = api.SaveSkillRequest{Content: req.Content, Enabled: &on}
	}
	if err := s.storeSkill(r.Context(), req.Name, save); err != nil {
		return err
	}
	return s.writeProjectSkill(w, r, project, req.Name)
}

// leadEditSkill replaces a skill's SKILL.md once the user has approved the
// diff.
func (s *Server) leadEditSkill(w http.ResponseWriter, r *http.Request) error {
	project, name := r.PathValue("project"), r.PathValue("name")
	var req api.SaveSkillRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	if _, err := s.store.Skill(ctx, name); err != nil {
		return err
	}
	files, err := s.store.SkillFiles(ctx, name)
	if err != nil {
		return err
	}
	var old string
	content := skills.WithName(req.Content, name)
	for i, f := range files {
		if f.Path == skills.File {
			old = string(f.Content)
			files[i].Content = []byte(content)
		}
	}
	if content != old {
		// Checked before asking: the user shouldn't approve what can't be saved.
		if _, err := skills.Check(name, files); err != nil {
			return err
		}
		if err := s.askApproval(ctx, project, api.ChatPermission{
			Title:  "Edit the skill " + name,
			Detail: "Every agent that has it gets the new text.",
			Diffs:  []api.ChatDiff{{Path: name + "/" + skills.File, OldText: old, NewText: content}},
		}); err != nil {
			return err
		}
		if err := s.storeSkill(ctx, name, api.SaveSkillRequest{Content: content}); err != nil {
			return err
		}
	}
	return s.writeProjectSkill(w, r, project, name)
}

// leadRemoveSkill deletes a skill once the user has approved it.
func (s *Server) leadRemoveSkill(w http.ResponseWriter, r *http.Request) error {
	project, name := r.PathValue("project"), r.PathValue("name")
	ctx := r.Context()
	if _, err := s.store.Skill(ctx, name); err != nil {
		return err
	}
	files, err := s.store.SkillFiles(ctx, name)
	if err != nil {
		return err
	}
	var diffs []api.ChatDiff
	for _, f := range files {
		if f.Path == skills.File {
			diffs = append(diffs, api.ChatDiff{Path: name + "/" + f.Path, OldText: string(f.Content)})
		}
	}
	detail := "It goes from AgentBox and from every agent that has it."
	if len(files) > 1 {
		detail = fmt.Sprintf("It goes from AgentBox and from every agent that has it, with its %d files.", len(files))
	}
	if err := s.askApproval(ctx, project, api.ChatPermission{Title: "Delete the skill " + name, Detail: detail, Diffs: diffs}); err != nil {
		return err
	}
	if err := s.deleteSkill(ctx, name); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// leadSwitchSkill turns a skill on or off, in the lead's project or
// AgentBox-wide. On is the lead's to decide; off, where it was on, the user's.
func (s *Server) leadSwitchSkill(w http.ResponseWriter, r *http.Request) error {
	project, name := r.PathValue("project"), r.PathValue("name")
	var req api.LeadSkillSwitchRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	sk, err := s.store.Skill(ctx, name)
	if err != nil {
		return err
	}
	on, err := parseOverride(req.Override)
	if err != nil {
		return err
	}
	if req.Everywhere {
		if on == nil {
			return errors.New("AgentBox-wide, a skill is on or off: say which")
		}
		if sk.Enabled && !*on {
			if err := s.askApproval(ctx, project, api.ChatPermission{
				Title:  "Turn the skill " + name + " off AgentBox-wide",
				Detail: "Projects that turned it on themselves keep it.",
			}); err != nil {
				return err
			}
		}
		if sk.Enabled != *on {
			if err := s.setSkillEnabled(ctx, name, *on); err != nil {
				return err
			}
		}
		return s.writeProjectSkill(w, r, project, name)
	}
	after := sk
	after.Overrides = map[string]bool{project: false}
	if on != nil {
		after.Overrides[project] = *on
	} else {
		delete(after.Overrides, project)
	}
	if sk.EnabledFor(project) && !after.EnabledFor(project) {
		if err := s.askApproval(ctx, project, api.ChatPermission{
			Title:  "Turn the skill " + name + " off in " + project,
			Detail: "This project's agents stop getting it; other projects keep it as they have it.",
		}); err != nil {
			return err
		}
	}
	if err := s.setSkillOverride(ctx, project, name, on); err != nil {
		return err
	}
	return s.writeProjectSkill(w, r, project, name)
}
