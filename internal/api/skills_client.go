package api

import (
	"context"
	"net/http"
	"net/url"
)

// The skills client.

// Skills lists every stored skill. With a project, each says whether that
// project's agents get it (Active).
func (c *Client) Skills(ctx context.Context, project string) ([]Skill, error) {
	path := "/v1/skills"
	if project != "" {
		path = "/v1/projects/" + url.PathEscape(project) + "/skills"
	}
	var out []Skill
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// Skill is one skill with its files.
func (c *Client) Skill(ctx context.Context, name string) (SkillDetail, error) {
	var out SkillDetail
	return out, c.do(ctx, http.MethodGet, "/v1/skills/"+url.PathEscape(name), nil, &out)
}

// SaveSkill writes a skill's SKILL.md, making the skill when it's new.
func (c *Client) SaveSkill(ctx context.Context, name string, req SaveSkillRequest) (Skill, error) {
	var out Skill
	return out, c.do(ctx, http.MethodPut, "/v1/skills/"+url.PathEscape(name), req, &out)
}

// SetSkillEnabled turns a skill on or off AgentBox-wide.
func (c *Client) SetSkillEnabled(ctx context.Context, name string, on bool) (Skill, error) {
	var out Skill
	return out, c.do(ctx, http.MethodPatch, "/v1/skills/"+url.PathEscape(name), UpdateSkillRequest{Enabled: &on}, &out)
}

// SetSkillOverride sets a project's say on a skill: "on", "off" or "".
func (c *Client) SetSkillOverride(ctx context.Context, project, name, override string) (Skill, error) {
	var out Skill
	return out, c.do(ctx, http.MethodPut, "/v1/projects/"+url.PathEscape(project)+"/skills/"+url.PathEscape(name), SkillOverrideRequest{Override: override}, &out)
}

// RemoveSkill deletes a skill, from every agent too.
func (c *Client) RemoveSkill(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, "/v1/skills/"+url.PathEscape(name), nil, nil)
}

// ScanSkills lists what a source holds, without storing anything.
func (c *Client) ScanSkills(ctx context.Context, source string) ([]SkillCandidate, error) {
	var out []SkillCandidate
	return out, c.do(ctx, http.MethodPost, "/v1/skills/scan", ScanSkillsRequest{Source: source}, &out)
}

// ImportSkills stores some of what a source holds.
func (c *Client) ImportSkills(ctx context.Context, req ImportSkillsRequest) ([]Skill, error) {
	var out []Skill
	return out, c.do(ctx, http.MethodPost, "/v1/skills/import", req, &out)
}
