package transfer

import (
	"fmt"
	"slices"
	"unicode/utf8"

	"agentbox/internal/api"
)

// Merge is how an import settles what this instance already has with other
// values. The zero value is an import from a file: this instance's stays, and
// the report says so.
type Merge struct {
	// Sync is a merge between two instances that are both in use
	// (internal/daemon/sync.go), where much differs that isn't a choice
	// anyone made: the accounts a project uses on each machine, when a
	// memory was last read, a pull request's state as each last saw it.
	// Those stay as each instance has them and are no conflict (local), and
	// a chat both sides wrote in stays apart on each.
	Sync bool
	// Take is the conflicts (api.TransferConflict.ID) the imported values win.
	Take map[string]bool
	// DryRun changes nothing: the report is what importing would do.
	DryRun bool
}

// SyncCategories is what a sync carries of a project: what an export takes by
// default, without its schedules, which would then run on both machines.
// Repositories go by reference, and secrets and logins never.
var SyncCategories = []string{Repository, Settings, Sections, Icon, Notes, Skills, Memory, Chats, Connectors}

// local is, by table, the columns that are each instance's own in a sync:
// they are neither compared nor overwritten in a row both sides have. "*" is
// every column that isn't in localKeep.
var local = map[string][]string{
	"projects": {"created_at", "claude_account", "claude_accounts", "github_account", "codex_account", "opencode_accounts",
		"cursor_account", "gemini_account", "copilot_account", "nesting", "ios_forward_ports"},
	"project_icons":   {"detected", "detected_mime", "detected_from", "detected_at", "candidates", "updated_at"},
	"memories":        {"updated_at", "referenced_at", "mentioned_at", "decayed_at", "confirmations", "importance", "promotion", "promotion_at", "resolved_by"},
	"connectors":      {"updated_at"},
	"chats":           {"*"},
	"archived_agents": {"*"},
}

// localKeep is what "*" leaves compared: an archived agent's id, which says
// whether two of one name are the same agent.
var localKeep = map[string][]string{"archived_agents": {"id"}}

// isLocal says whether a column is this instance's own in this merge.
func (m Merge) isLocal(table, col string) bool {
	if !m.Sync {
		return false
	}
	cols := local[table]
	if slices.Contains(cols, "*") {
		return !slices.Contains(localKeep[table], col)
	}
	return slices.Contains(cols, col)
}

// maxConflicts is how many conflicts of one entry are listed to choose
// between; the rest are counted, and kept as this instance has them.
const maxConflicts = 200

// choice records a conflict that can be chosen between.
func (c *Counts) choice(id, label string, fields ...api.TransferConflictField) {
	if len(c.Conflicts) < maxConflicts {
		c.Conflicts = append(c.Conflicts, api.TransferConflict{ID: id, Label: label, Fields: fields})
	}
}

// conflictID names a row on any instance.
func (c *Counts) conflictID(table string, key []any) string {
	return c.entry + "#" + table + "/" + describeKey(key)
}

// part names a conflict that is the entry's one thing: its notes, its section.
func (c *Counts) part(what string) string { return c.entry + "#" + what }

// labelOf is what people call a row: its title or name, else its key.
func labelOf(row map[string]any, key []any) string {
	for _, col := range []string{"title", "display_name", "name", "skill", "connector", "path"} {
		if s, ok := row[col].(string); ok && s != "" {
			return s
		}
	}
	return describeKey(key)
}

const maxShown = 600

// shown is a value as a conflict shows it.
func shown(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case []byte:
		return fmt.Sprintf("(%s of data)", Size(int64(len(x))))
	case string:
		return clip(x)
	}
	return clip(fmt.Sprint(v))
}

func clip(s string) string {
	if len(s) <= maxShown {
		return s
	}
	n := maxShown
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}
