package daemon

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"agentbox/internal/api"
	"agentbox/internal/hostos"
	"agentbox/internal/skills"
	"agentbox/internal/state"
)

// The skills API (internal/skills). Every change is installed again, in the
// background, into each lead and running agent it reaches (syncSkills): the
// request answers once it is stored.

func (s *Server) listSkills(w http.ResponseWriter, r *http.Request) error {
	list, err := s.skillsInfo(r.Context(), "")
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, list)
}

// listProjectSkills is every skill, each saying whether the project's agents
// get it (Active).
func (s *Server) listProjectSkills(w http.ResponseWriter, r *http.Request) error {
	project := r.PathValue("project")
	if _, err := s.store.Project(r.Context(), project); err != nil {
		return err
	}
	list, err := s.skillsInfo(r.Context(), project)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, list)
}

func (s *Server) skillsInfo(ctx context.Context, project string) ([]api.Skill, error) {
	all, err := s.store.Skills(ctx)
	if err != nil {
		return nil, err
	}
	sizes, err := s.store.SkillSizes(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]api.Skill, 0, len(all))
	for _, sk := range all {
		info, err := s.skillInfo(ctx, sk, sizes[sk.Name])
		if err != nil {
			return nil, err
		}
		if project != "" {
			on := sk.EnabledFor(project)
			info.Active = &on
		}
		out = append(out, info)
	}
	return out, nil
}

func (s *Server) skillInfo(ctx context.Context, sk state.Skill, size [2]int64) (api.Skill, error) {
	info := api.Skill{
		Name: sk.Name, Description: sk.Description, Source: sk.Source, Enabled: sk.Enabled,
		Overrides: sk.Overrides, UserInvocable: true, FileCount: int(size[0]), Size: size[1],
		CreatedAt: sk.CreatedAt, UpdatedAt: sk.UpdatedAt,
	}
	if info.Overrides == nil {
		info.Overrides = map[string]bool{}
	}
	if f, err := s.store.SkillFile(ctx, sk.Name, skills.File); err == nil {
		if meta, _, err := skills.Parse(string(f.Content)); err == nil {
			info.UserInvocable = meta.UserInvocable
		}
	}
	return info, nil
}

func (s *Server) getSkill(w http.ResponseWriter, r *http.Request) error {
	name := r.PathValue("name")
	sk, err := s.store.Skill(r.Context(), name)
	if err != nil {
		return err
	}
	files, err := s.store.SkillFiles(r.Context(), name)
	if err != nil {
		return err
	}
	var size int64
	detail := api.SkillDetail{}
	for _, f := range files {
		size += int64(len(f.Content))
		file := api.SkillFile{Path: f.Path, Size: int64(len(f.Content))}
		if utf8.Valid(f.Content) && !slices.Contains(f.Content, 0) {
			file.Content = string(f.Content)
		} else {
			file.Binary = true
		}
		detail.Files = append(detail.Files, file)
	}
	// SKILL.md first: it's what the preview opens on.
	slices.SortStableFunc(detail.Files, func(a, b api.SkillFile) int {
		switch {
		case a.Path == skills.File:
			return -1
		case b.Path == skills.File:
			return 1
		}
		return strings.Compare(a.Path, b.Path)
	})
	detail.Skill, err = s.skillInfo(r.Context(), sk, [2]int64{int64(len(files)), size})
	if err != nil {
		return err
	}
	// A lead reads it from its project (leadRoutes): whether that has it.
	if project := r.PathValue("project"); project != "" {
		on := sk.EnabledFor(project)
		detail.Active = &on
	}
	return writeJSON(w, http.StatusOK, detail)
}

// saveSkill writes a skill's SKILL.md: a new skill, or a new version of one,
// whose other files stay. The name is the path's: a SKILL.md naming another
// is corrected to it rather than refused, so pasting one in just works.
func (s *Server) saveSkill(w http.ResponseWriter, r *http.Request) error {
	name := r.PathValue("name")
	var req api.SaveSkillRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if err := s.storeSkill(r.Context(), name, req); err != nil {
		return err
	}
	return s.writeSkill(w, r, name)
}

// storeSkill is saveSkill without HTTP, for the lead's tools too.
func (s *Server) storeSkill(ctx context.Context, name string, req api.SaveSkillRequest) error {
	if err := skills.ValidateName(name); err != nil {
		return err
	}
	sk, err := s.store.Skill(ctx, name)
	isNew := err != nil
	var files []state.SkillFile
	if isNew {
		sk = state.Skill{Name: name, Enabled: true, Overrides: map[string]bool{}}
		if req.Enabled != nil {
			sk.Enabled = *req.Enabled
		}
		if req.Project != "" {
			if _, err := s.store.Project(ctx, req.Project); err != nil {
				return err
			}
			sk.Enabled = req.Enabled != nil && *req.Enabled
			sk.Overrides[req.Project] = true
		}
	} else if files, err = s.store.SkillFiles(ctx, name); err != nil {
		return err
	}
	content := skills.WithName(req.Content, name)
	files = slices.DeleteFunc(files, func(f state.SkillFile) bool { return f.Path == skills.File })
	files = append(files, state.SkillFile{Path: skills.File, Mode: 0o644, Content: []byte(content)})
	meta, err := skills.Check(name, files)
	if err != nil {
		return err
	}
	sk.Description = meta.Description
	sk.UpdatedAt = time.Now().Truncate(time.Second)
	if err := s.store.SetSkill(ctx, sk, files); err != nil {
		return err
	}
	if isNew {
		s.logf("skill %s added", name)
	} else {
		s.logf("skill %s changed", name)
	}
	s.syncSkills("")
	return nil
}

func (s *Server) writeSkill(w http.ResponseWriter, r *http.Request, name string) error {
	sk, err := s.store.Skill(r.Context(), name)
	if err != nil {
		return err
	}
	sizes, err := s.store.SkillSizes(r.Context())
	if err != nil {
		return err
	}
	info, err := s.skillInfo(r.Context(), sk, sizes[name])
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, info)
}

func (s *Server) updateSkill(w http.ResponseWriter, r *http.Request) error {
	name := r.PathValue("name")
	var req api.UpdateSkillRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if req.Enabled != nil {
		if err := s.setSkillEnabled(r.Context(), name, *req.Enabled); err != nil {
			return err
		}
	}
	return s.writeSkill(w, r, name)
}

func (s *Server) setSkillEnabled(ctx context.Context, name string, on bool) error {
	if err := s.store.SetSkillEnabled(ctx, name, on); err != nil {
		return err
	}
	s.logf("skill %s turned %s AgentBox-wide", name, onOff(on))
	s.syncSkills("")
	return nil
}

func (s *Server) removeSkill(w http.ResponseWriter, r *http.Request) error {
	if err := s.deleteSkill(r.Context(), r.PathValue("name")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) deleteSkill(ctx context.Context, name string) error {
	if err := s.store.RemoveSkill(ctx, name); err != nil {
		return err
	}
	s.logf("skill %s removed", name)
	s.syncSkills("")
	return nil
}

func (s *Server) setProjectSkill(w http.ResponseWriter, r *http.Request) error {
	project, name := r.PathValue("project"), r.PathValue("name")
	if _, err := s.store.Project(r.Context(), project); err != nil {
		return err
	}
	var req api.SkillOverrideRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	on, err := parseOverride(req.Override)
	if err != nil {
		return err
	}
	if err := s.setSkillOverride(r.Context(), project, name, on); err != nil {
		return err
	}
	return s.writeProjectSkill(w, r, project, name)
}

// parseOverride reads a project's say on a skill: on, off, or nil to follow
// AgentBox's.
func parseOverride(override string) (*bool, error) {
	switch override {
	case "on", "off":
		v := override == "on"
		return &v, nil
	case "":
		return nil, nil
	}
	return nil, fmt.Errorf("invalid override %q: use on, off, or nothing to follow AgentBox's", override)
}

func (s *Server) setSkillOverride(ctx context.Context, project, name string, on *bool) error {
	if err := s.store.SetSkillOverride(ctx, name, project, on); err != nil {
		return err
	}
	override := ""
	if on != nil {
		override = onOff(*on)
	}
	s.logf("skill %s in %s: %s", name, project, cmpOr(override, "as AgentBox-wide"))
	s.syncSkills(project)
	return nil
}

// writeProjectSkill answers with a skill as a project sees it.
func (s *Server) writeProjectSkill(w http.ResponseWriter, r *http.Request, project, name string) error {
	sk, err := s.store.Skill(r.Context(), name)
	if err != nil {
		return err
	}
	sizes, err := s.store.SkillSizes(r.Context())
	if err != nil {
		return err
	}
	info, err := s.skillInfo(r.Context(), sk, sizes[name])
	if err != nil {
		return err
	}
	active := sk.EnabledFor(project)
	info.Active = &active
	return writeJSON(w, http.StatusOK, info)
}

// skillHomes are where the user's own AI tools keep their skills: the host's
// home when the VM shares it (hostos.HomeEnv), and this machine's.
func skillHomes() []string {
	var homes []string
	if h := os.Getenv(hostos.HomeEnv); h != "" {
		homes = append(homes, h)
	}
	if h, err := os.UserHomeDir(); err == nil && !slices.Contains(homes, h) {
		homes = append(homes, h)
	}
	return homes
}

func (s *Server) scanSkills(w http.ResponseWriter, r *http.Request) error {
	var req api.ScanSkillsRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	found, err := skills.Scan(r.Context(), req.Source, s.skillHomesFor())
	if err != nil {
		return err
	}
	stored, err := s.store.Skills(r.Context())
	if err != nil {
		return err
	}
	out := make([]api.SkillCandidate, 0, len(found))
	for _, c := range found {
		cand := api.SkillCandidate{
			Name: c.Name, Description: c.Description, Origin: c.Origin, Plugin: c.Plugin,
			Source: c.Source, Files: len(c.Files), Size: c.Size(), Problem: c.Problem,
			Exists: slices.ContainsFunc(stored, func(sk state.Skill) bool { return sk.Name == c.Name }),
		}
		for _, f := range c.Files {
			if f.Path == skills.File {
				cand.Content = string(f.Content)
			}
		}
		out = append(out, cand)
	}
	return writeJSON(w, http.StatusOK, out)
}

// skillHomesFor is skillHomes, or the test's.
func (s *Server) skillHomesFor() []string {
	if s.cfg.SkillHomes != nil {
		return s.cfg.SkillHomes
	}
	return skillHomes()
}

// importSkills scans a source again and stores what was picked of it. A
// skill of the same name is replaced, keeping where it was on.
func (s *Server) importSkills(w http.ResponseWriter, r *http.Request) error {
	var req api.ImportSkillsRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	if req.Project != "" {
		if _, err := s.store.Project(ctx, req.Project); err != nil {
			return err
		}
	}
	found, err := skills.Scan(ctx, req.Source, s.skillHomesFor())
	if err != nil {
		return err
	}
	picked := found[:0:0]
	for _, c := range found {
		if len(req.Names) == 0 && c.Problem == "" || slices.Contains(req.Names, c.Name) {
			picked = append(picked, c)
		}
	}
	for _, n := range req.Names {
		if !slices.ContainsFunc(picked, func(c skills.Candidate) bool { return c.Name == n }) {
			return fmt.Errorf("there's no skill %s in %s", n, cmpOr(req.Source, "your AI tools' skill folders"))
		}
	}
	if len(picked) == 0 {
		return fmt.Errorf("there's no skill to import in %s", cmpOr(req.Source, "your AI tools' skill folders"))
	}
	now := time.Now().Truncate(time.Second)
	var names []string
	for _, c := range picked {
		files, meta, err := c.Prepare(c.Name)
		if err != nil {
			return err
		}
		sk, err := s.store.Skill(ctx, c.Name)
		if err != nil {
			sk = state.Skill{Name: c.Name, Enabled: req.Project == "", Overrides: map[string]bool{}, CreatedAt: now}
		}
		if req.Project != "" {
			sk.Overrides[req.Project] = true
		}
		sk.Description, sk.Source, sk.UpdatedAt = meta.Description, c.Source, now
		if err := s.store.SetSkill(ctx, sk, files); err != nil {
			return err
		}
		names = append(names, c.Name)
	}
	s.logf("imported skills %s from %s", strings.Join(names, ", "), cmpOr(req.Source, "the AI tools' skill folders"))
	s.syncSkills("")
	list, err := s.skillsInfo(ctx, req.Project)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, slices.DeleteFunc(list, func(sk api.Skill) bool { return !slices.Contains(names, sk.Name) }))
}

// syncSkills installs skills again, in the background, wherever a change to
// project's (or, for "", any) reaches. One runs at a time, and each reads the
// store as it starts, so the last one leaves every agent as the store is.
func (s *Server) syncSkills(project string) {
	go func() {
		s.skillsMu.Lock()
		defer s.skillsMu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := s.manager(nil).RewriteSkills(ctx, project); err != nil {
			s.logf("skills: %v", err)
		}
		if s.skillsSynced != nil {
			s.skillsSynced()
		}
	}()
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}
