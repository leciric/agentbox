package api

import "time"

// The skills API: Agent Skills stored by the daemon and installed into every
// agent and lead whose project has them on (internal/skills).

// Skill is one stored skill, without its files.
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Source is where it was imported from: a path, a git URL, or
	// "<origin>:<folder>" for one of the user's AI tools' own skills
	// ("claude:review", "claude-plugin:<plugin>/<skill>"). Empty for a skill
	// written in AgentBox.
	Source string `json:"source"`
	// Enabled is the AgentBox-wide switch: on in every project that doesn't
	// say otherwise.
	Enabled bool `json:"enabled"`
	// Overrides are the projects that say otherwise, by project name: true
	// for on there, false for off.
	Overrides map[string]bool `json:"overrides"`
	// UserInvocable is false when its SKILL.md says `user-invocable: false`:
	// the composer doesn't offer it, and the model may still use it.
	UserInvocable bool      `json:"userInvocable"`
	FileCount     int       `json:"fileCount"`
	Size          int64     `json:"size"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
	// Active is set in a project's list: whether its agents get it, after
	// its override.
	Active *bool `json:"active,omitempty"`
}

// SkillFile is one file of a skill's folder. Content is set for text files
// when one skill is fetched; binary ones list their size alone.
type SkillFile struct {
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	Binary  bool   `json:"binary,omitempty"`
	Content string `json:"content,omitempty"`
}

// SkillDetail is one skill with its files, SKILL.md first.
type SkillDetail struct {
	Skill
	Files []SkillFile `json:"files"`
}

// SaveSkillRequest is the body of PUT /v1/skills/{name}: a new skill, or a
// new SKILL.md for one. Its other files are kept. Enabled and Project apply
// to a new skill alone: AgentBox-wide on or off, and, from a project's
// settings, on in that project.
type SaveSkillRequest struct {
	Content string `json:"content"`
	Enabled *bool  `json:"enabled,omitempty"`
	Project string `json:"project,omitempty"`
}

// UpdateSkillRequest is the body of PATCH /v1/skills/{name}.
type UpdateSkillRequest struct {
	Enabled *bool `json:"enabled,omitempty"`
}

// SkillOverrideRequest is the body of PUT /v1/projects/{project}/skills/{name}:
// "on", "off", or "" to follow the AgentBox-wide switch again.
type SkillOverrideRequest struct {
	Override string `json:"override"`
}

// ScanSkillsRequest is the body of POST /v1/skills/scan and the start of
// /v1/skills/import: a folder or SKILL.md on the machine AgentBox runs on, a
// git URL, or "" for the skills the user's own AI tools have (Claude Code's
// and its plugins', Codex's, OpenCode's, Cursor's, ~/.agents/skills).
type ScanSkillsRequest struct {
	Source string `json:"source"`
}

// SkillCandidate is a skill found by a scan, not stored.
type SkillCandidate struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Origin is "claude", "claude-plugin", "codex", "agents", "opencode",
	// "cursor", "folder" or "git".
	Origin string `json:"origin"`
	Plugin string `json:"plugin,omitempty"`
	Source string `json:"source"`
	Files  int    `json:"files"`
	Size   int64  `json:"size"`
	// Content is its SKILL.md, for the preview.
	Content string `json:"content"`
	// Exists says a skill of the same name is stored: importing replaces it.
	Exists bool `json:"exists"`
	// Problem is why it can't be imported.
	Problem string `json:"problem,omitempty"`
}

// ImportSkillsRequest imports some of what a scan of Source found, by name
// (all of them when Names is empty). From a project's settings, Project says
// which: the skills come in on there and off AgentBox-wide.
type ImportSkillsRequest struct {
	Source  string   `json:"source"`
	Names   []string `json:"names,omitempty"`
	Project string   `json:"project,omitempty"`
}

// LeadNewSkillRequest is the body of POST /v1/project/skills on a lead's
// socket: a skill the lead wrote, on in its own project alone or, with
// Everywhere, AgentBox-wide. A name already taken is refused: changing a
// skill is the user's to approve (PUT /v1/project/skills/{name}).
type LeadNewSkillRequest struct {
	Name       string `json:"name"`
	Content    string `json:"content"`
	Everywhere bool   `json:"everywhere,omitempty"`
}

// LeadSkillSwitchRequest is the body of PUT /v1/project/skills/{name}/switch
// on a lead's socket. In the lead's project, Override is "on", "off" or "" to
// follow the AgentBox-wide switch; with Everywhere, it is that switch, "on" or
// "off". Turning a skill off anywhere it was on waits for the user's approval.
type LeadSkillSwitchRequest struct {
	Override   string `json:"override"`
	Everywhere bool   `json:"everywhere,omitempty"`
}
