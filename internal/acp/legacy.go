package acp

import (
	"context"
	"encoding/json"
	"fmt"
)

// Some agents still describe a session's permission modes and models with
// ACP's older fields, "modes" and "models", and change them with session/set_mode
// and session/set_model, rather than with configOptions and
// session/set_config_option: Gemini CLI (0.64) does. Conn translates for
// them, so the rest of AgentBox sees configOptions from every agent. A session
// whose answer has configOptions is left alone.

// The methods of the older fields.
const (
	methodSetMode  = "session/set_mode"
	methodSetModel = "session/set_model"
)

// The ids the translation gives the two options, which are the categories
// AgentBox looks them up by.
const (
	legacyModeID  = "mode"
	legacyModelID = "model"
)

// legacySession is what a session's agent said about its modes and models.
type legacySession struct {
	modes  *legacyModes
	models *legacyModels
}

type legacyModes struct {
	Current   string `json:"currentModeId"`
	Available []struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
	} `json:"availableModes"`
}

type legacyModels struct {
	Current   string `json:"currentModelId"`
	Available []struct {
		ID          string `json:"modelId"`
		Name        string `json:"name"`
		Description string `json:"description"`
	} `json:"availableModels"`
}

// options are the session's modes and models as configOptions, in their wire
// form, so they are read like any agent's.
func (l *legacySession) options() []map[string]any {
	var out []map[string]any
	if l.modes != nil && len(l.modes.Available) > 0 {
		choices := make([]map[string]any, len(l.modes.Available))
		for i, m := range l.modes.Available {
			choices[i] = map[string]any{"value": m.ID, "name": m.Name, "description": m.Description}
		}
		out = append(out, map[string]any{"id": legacyModeID, "name": "Mode", "category": "mode", "type": "select", "currentValue": l.modes.Current, "options": choices})
	}
	if l.models != nil && len(l.models.Available) > 0 {
		choices := make([]map[string]any, len(l.models.Available))
		for i, m := range l.models.Available {
			choices[i] = map[string]any{"value": m.ID, "name": m.Name, "description": m.Description}
		}
		out = append(out, map[string]any{"id": legacyModelID, "name": "Model", "category": "model", "type": "select", "currentValue": l.models.Current, "options": choices})
	}
	return out
}

// callLegacy is call, translating for an agent that uses the older fields:
// a session's answer gets configOptions made from them, and setting one of
// those options is sent as the older method.
func (c *Conn) callLegacy(ctx context.Context, method string, params any) (json.RawMessage, error) {
	switch method {
	case MethodSetConfigOption:
		if raw, ok, err := c.setLegacyOption(ctx, params); ok {
			return raw, err
		}
	case MethodSessionNew, MethodSessionLoad, MethodSessionResume:
		raw, err := c.call(ctx, method, params)
		if err != nil {
			return nil, err
		}
		return c.readLegacySession(params, raw), nil
	}
	return c.call(ctx, method, params)
}

// readLegacySession remembers a session answered with the older fields and
// returns the answer with configOptions made from them, or as it came.
func (c *Conn) readLegacySession(params any, raw json.RawMessage) json.RawMessage {
	var answer map[string]json.RawMessage
	if json.Unmarshal(raw, &answer) != nil {
		return raw
	}
	var options []json.RawMessage
	if json.Unmarshal(answer["configOptions"], &options) == nil && len(options) > 0 {
		return raw
	}
	l := &legacySession{}
	if m := answer["modes"]; len(m) > 0 && string(m) != "null" {
		l.modes = &legacyModes{}
		if json.Unmarshal(m, l.modes) != nil {
			l.modes = nil
		}
	}
	if m := answer["models"]; len(m) > 0 && string(m) != "null" {
		l.models = &legacyModels{}
		if json.Unmarshal(m, l.models) != nil {
			l.models = nil
		}
	}
	if l.modes == nil && l.models == nil {
		return raw
	}
	id := sessionIDOf(params)
	if id == "" {
		_ = json.Unmarshal(answer["sessionId"], &id)
	}
	if id == "" {
		return raw
	}
	c.mu.Lock()
	if c.legacy == nil {
		c.legacy = map[string]*legacySession{}
	}
	c.legacy[id] = l
	opts := l.options()
	c.mu.Unlock()
	b, err := json.Marshal(opts)
	if err != nil {
		return raw
	}
	answer["configOptions"] = b
	out, err := json.Marshal(answer)
	if err != nil {
		return raw
	}
	return out
}

// sessionIDOf is the sessionId a request names, "" for one that names none
// (session/new).
func sessionIDOf(params any) string {
	b, err := json.Marshal(params)
	if err != nil {
		return ""
	}
	var p struct {
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(b, &p)
	return p.SessionID
}

// setLegacyOption sends a change to one of a legacy session's two options as
// the older method, and answers with the options as they now are. ok is false
// for a session or an option the translation doesn't own.
func (c *Conn) setLegacyOption(ctx context.Context, params any) (raw json.RawMessage, ok bool, err error) {
	b, err := json.Marshal(params)
	if err != nil {
		return nil, false, nil
	}
	var req struct {
		SessionID string `json:"sessionId"`
		ConfigID  string `json:"configId"`
		Value     any    `json:"value"`
	}
	if json.Unmarshal(b, &req) != nil {
		return nil, false, nil
	}
	c.mu.Lock()
	l := c.legacy[req.SessionID]
	c.mu.Unlock()
	value, isString := req.Value.(string)
	if l == nil || !isString {
		return nil, false, nil
	}
	switch {
	case req.ConfigID == legacyModeID && l.modes != nil:
		if _, err := c.call(ctx, methodSetMode, map[string]string{"sessionId": req.SessionID, "modeId": value}); err != nil {
			return nil, true, fmt.Errorf("%s: %w", MethodSetConfigOption, err)
		}
		c.mu.Lock()
		l.modes.Current = value
		c.mu.Unlock()
	case req.ConfigID == legacyModelID && l.models != nil:
		if _, err := c.call(ctx, methodSetModel, map[string]string{"sessionId": req.SessionID, "modelId": value}); err != nil {
			return nil, true, fmt.Errorf("%s: %w", MethodSetConfigOption, err)
		}
		c.mu.Lock()
		l.models.Current = value
		c.mu.Unlock()
	default:
		return nil, false, nil
	}
	c.mu.Lock()
	opts := l.options()
	c.mu.Unlock()
	raw, err = json.Marshal(map[string]any{"configOptions": opts})
	return raw, true, err
}

// noteLegacyUpdate keeps a legacy session's mode in step with a
// current_mode_update the agent sends, so the next answer the translation
// makes has it.
func (c *Conn) noteLegacyUpdate(params json.RawMessage) {
	c.mu.Lock()
	none := len(c.legacy) == 0
	c.mu.Unlock()
	if none {
		return
	}
	var n struct {
		SessionID string `json:"sessionId"`
		Update    struct {
			Kind string `json:"sessionUpdate"`
			Mode string `json:"currentModeId"`
		} `json:"update"`
	}
	if json.Unmarshal(params, &n) != nil || n.Update.Kind != "current_mode_update" || n.Update.Mode == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if l := c.legacy[n.SessionID]; l != nil && l.modes != nil {
		l.modes.Current = n.Update.Mode
	}
}
