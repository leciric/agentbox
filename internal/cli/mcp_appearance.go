package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"agentbox/internal/api"
	"agentbox/internal/customize"
	"agentbox/internal/mcp"
)

// customizationParts are what reset may put back one at a time: the
// customization's top-level fields but its version.
var customizationParts = []string{"theme", "fonts", "density", "sidebar", "css"}

// appearanceTool is the lead's and the Home chat's way to change how the app
// looks and is laid out when the user asks — "make it compact", "a green
// accent", "hide Jobs" — through the customization layer the daemon keeps
// (internal/customize), never by editing AgentBox. The fields and their values
// come back from show rather than sitting in the schema, which every call
// pays for; the daemon refuses a bad value and says why, which is returned as
// the tool's error for the chat to correct.
func appearanceTool(ctx context.Context, c *api.Client) mcp.Tool {
	type args struct {
		Changes    json.RawMessage
		Appearance string
		Part       string
	}
	read := func(raw json.RawMessage) (args, error) {
		var in args
		err := decodeArgs(raw, &in)
		return in, err
	}
	return actionTool("appearance",
		"The AgentBox app's look and layout, which the user may ask you to change: colours, fonts, density, sidebar, "+
			"CSS, light or dark. Changes show at once; never edit AgentBox's code for them.",
		"show: what is set and every field's values; read it before set. set: changes (only what changes; null "+
			"resets a field) and/or appearance. reset: part, or all",
		map[string]any{
			"changes":    map[string]any{"type": "object", "description": `set: like {"theme":{"accent":"#22c55e"},"density":"compact"}`},
			"appearance": choiceOf("set: the window's mode", api.AppearanceFollow, api.AppearanceLight, api.AppearanceDark, api.AppearanceDracula),
			"part":       choiceOf("reset: only this part", customizationParts...),
		},
		action{"show", func(json.RawMessage) (string, error) {
			status, err := c.Customization(ctx)
			if err != nil {
				return "", err
			}
			theme, err := c.Theme(ctx)
			if err != nil {
				return "", err
			}
			var b strings.Builder
			fmt.Fprintf(&b, "Window: %s.\n", describeAppearance(theme))
			b.WriteString(describeCustomization(status))
			b.WriteString("\n\n")
			b.WriteString(customize.Guide())
			return b.String(), nil
		}},
		action{"set", func(raw json.RawMessage) (string, error) {
			in, err := read(raw)
			if err != nil {
				return "", err
			}
			changes, err := changesObject(in.Changes)
			if err != nil {
				return "", err
			}
			if changes == nil && in.Appearance == "" {
				return "", errors.New(`set needs changes, like {"density":"compact"}, or appearance`)
			}
			var said []string
			if changes != nil {
				status, err := c.PatchCustomization(ctx, changes)
				if err != nil {
					return "", fmt.Errorf("nothing was changed: %w", err)
				}
				said = append(said, "The app shows it now. "+describeCustomization(status))
			}
			if in.Appearance != "" {
				theme, err := c.SetAppearance(ctx, in.Appearance)
				if err != nil {
					return "", err
				}
				said = append(said, fmt.Sprintf("Window: %s.", describeAppearance(theme)))
			}
			return strings.Join(said, "\n"), nil
		}},
		action{"reset", func(raw json.RawMessage) (string, error) {
			in, err := read(raw)
			if err != nil {
				return "", err
			}
			var status api.CustomizationStatus
			if in.Part == "" {
				status, err = c.ResetCustomization(ctx)
			} else {
				status, err = c.PatchCustomization(ctx, json.RawMessage(fmt.Sprintf(`{%q:null}`, in.Part)))
			}
			if err != nil {
				return "", err
			}
			what := "Everything is"
			if in.Part != "" {
				what = "The " + in.Part + " is"
			}
			return what + " back to AgentBox's own. " + describeCustomization(status), nil
		}},
	)
}

// changesObject is set's changes as a JSON object, nil when there are none. A
// model may send the object as a string of JSON; that is taken too.
func changesObject(raw json.RawMessage) (json.RawMessage, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	if strings.HasPrefix(trimmed, `"`) {
		var inner string
		if err := json.Unmarshal(raw, &inner); err != nil {
			return nil, err
		}
		trimmed = strings.TrimSpace(inner)
	}
	if !strings.HasPrefix(trimmed, "{") {
		return nil, errors.New(`changes is an object of the fields that change, like {"density":"compact"}`)
	}
	return json.RawMessage(trimmed), nil
}

func describeAppearance(t api.Theme) string {
	switch {
	case t.Appearance == api.AppearanceDracula:
		return "dracula"
	case t.Appearance == api.AppearanceFollow && t.Available:
		return fmt.Sprintf("follow, wearing the desktop's %s theme %s", t.Mode, t.Name)
	case t.Appearance == api.AppearanceFollow:
		return "follow (no desktop theme found, so AgentBox's own dark)"
	}
	return t.Appearance
}

// describeCustomization is what is set, as the JSON a change would write,
// with what is AgentBox's own left out.
func describeCustomization(status api.CustomizationStatus) string {
	var b strings.Builder
	if status.Problem != "" {
		fmt.Fprintf(&b, "Problem: %s\n", status.Problem)
	}
	raw, _ := json.Marshal(status.Customization)
	var doc map[string]any
	_ = json.Unmarshal(raw, &doc)
	delete(doc, "version")
	pruned, _ := prune(doc).(map[string]any)
	if len(pruned) == 0 {
		b.WriteString("Nothing is customized: it is AgentBox's own look.")
		return b.String()
	}
	out, _ := json.MarshalIndent(pruned, "", "  ")
	fmt.Fprintf(&b, "Customized (%s):\n%s", status.Path, out)
	return b.String()
}

// prune drops the empty objects a customization's unset parts leave.
func prune(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	out := map[string]any{}
	for k, val := range m {
		val = prune(val)
		if inner, ok := val.(map[string]any); ok && len(inner) == 0 {
			continue
		}
		out[k] = val
	}
	return out
}
