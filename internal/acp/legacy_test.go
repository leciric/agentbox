package acp

import (
	"context"
	"strings"
	"testing"
)

// Gemini CLI answers session/new with modes and models, as Gemini CLI 0.64
// sent them, and changes them with session/set_mode and session/set_model.
const geminiSession = `{"sessionId":"g-1",
	"modes":{"availableModes":[{"id":"default","name":"Default","description":"Prompts for approval"},{"id":"autoEdit","name":"Auto Edit"},{"id":"yolo","name":"YOLO"},{"id":"plan","name":"Plan"}],"currentModeId":"default"},
	"models":{"availableModels":[{"modelId":"auto","name":"Auto"},{"modelId":"gemini-3.1-pro-preview","name":"gemini-3.1-pro-preview"}],"currentModelId":"auto"}}`

func TestLegacyModesAndModelsReadAsConfigOptions(t *testing.T) {
	c, agent, _ := connect(t)
	done := make(chan error, 1)
	var resp SessionResponse
	go func() {
		done <- c.Call(context.Background(), MethodSessionNew, NewSessionRequest{Cwd: "/work"}, &resp)
	}()
	m := agent.next(t)
	agent.send(t, `{"jsonrpc":"2.0","id":`+string(m.ID)+`,"result":`+strings.ReplaceAll(geminiSession, "\n\t", "")+`}`)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if resp.SessionID != "g-1" || len(resp.ConfigOptions) != 2 {
		t.Fatalf("session/new = %+v", resp)
	}
	mode, model := resp.ConfigOptions[0], resp.ConfigOptions[1]
	if mode.Category != "mode" || mode.Value != "default" || len(mode.Choices) != 4 {
		t.Fatalf("mode = %+v", mode)
	}
	kinds := map[string]string{}
	for _, ch := range mode.Choices {
		kinds[ch.Value] = ch.Kind
	}
	if kinds["default"] != "standard" || kinds["yolo"] != "full_access" || kinds["plan"] != "plan" || kinds["autoEdit"] != "" {
		t.Errorf("mode kinds = %v", kinds)
	}
	if model.Category != "model" || model.Value != "auto" || len(model.Choices) != 2 {
		t.Fatalf("model = %+v", model)
	}

	// Setting the model goes out as session/set_model, and the answer is the
	// options as they now are.
	var set SetConfigOptionResponse
	go func() {
		done <- c.Call(context.Background(), MethodSetConfigOption, SetConfigOptionRequest{SessionID: "g-1", ConfigID: model.ID, Value: "gemini-3.1-pro-preview"}, &set)
	}()
	m = agent.next(t)
	if m.Method != "session/set_model" || string(m.Params) != `{"modelId":"gemini-3.1-pro-preview","sessionId":"g-1"}` {
		t.Fatalf("the request was %s %s", m.Method, m.Params)
	}
	agent.send(t, `{"jsonrpc":"2.0","id":`+string(m.ID)+`,"result":{}}`)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(set.ConfigOptions) != 2 || set.ConfigOptions[1].Value != "gemini-3.1-pro-preview" {
		t.Fatalf("set_config_option = %+v", set)
	}

	// A mode the agent switches to by itself is in the next answer.
	agent.send(t, `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"g-1","update":{"sessionUpdate":"current_mode_update","currentModeId":"plan"}}}`)
	go func() {
		done <- c.Call(context.Background(), MethodSetConfigOption, SetConfigOptionRequest{SessionID: "g-1", ConfigID: model.ID, Value: "auto"}, &set)
	}()
	m = agent.next(t)
	agent.send(t, `{"jsonrpc":"2.0","id":`+string(m.ID)+`,"result":{}}`)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if set.ConfigOptions[0].Value != "plan" {
		t.Errorf("mode after current_mode_update = %q", set.ConfigOptions[0].Value)
	}

	// The mode goes out as session/set_mode.
	go func() {
		done <- c.Call(context.Background(), MethodSetConfigOption, SetConfigOptionRequest{SessionID: "g-1", ConfigID: mode.ID, Value: "yolo"}, &set)
	}()
	m = agent.next(t)
	if m.Method != "session/set_mode" || string(m.Params) != `{"modeId":"yolo","sessionId":"g-1"}` {
		t.Fatalf("the request was %s %s", m.Method, m.Params)
	}
	agent.send(t, `{"jsonrpc":"2.0","id":`+string(m.ID)+`,"result":{}}`)
	if err := <-done; err != nil || set.ConfigOptions[0].Value != "yolo" {
		t.Fatalf("set_config_option = %+v, %v", set, err)
	}
}

func TestSessionsWithConfigOptionsAreLeftAlone(t *testing.T) {
	c, agent, _ := connect(t)
	done := make(chan error, 1)
	var resp SessionResponse
	go func() {
		done <- c.Call(context.Background(), MethodSessionNew, NewSessionRequest{Cwd: "/work"}, &resp)
	}()
	m := agent.next(t)
	agent.send(t, `{"jsonrpc":"2.0","id":`+string(m.ID)+`,"result":{"sessionId":"s-1","configOptions":[{"id":"effort","name":"Effort","category":"thought_level","type":"select","currentValue":"high","options":[{"value":"high","name":"High"}]}],"modes":{"availableModes":[{"id":"default","name":"Default"}],"currentModeId":"default"}}}`)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(resp.ConfigOptions) != 1 || resp.ConfigOptions[0].ID != "effort" {
		t.Fatalf("session/new = %+v", resp)
	}
	go func() {
		done <- c.Call(context.Background(), MethodSetConfigOption, SetConfigOptionRequest{SessionID: "s-1", ConfigID: "mode", Value: "default"}, nil)
	}()
	if m = agent.next(t); m.Method != MethodSetConfigOption {
		t.Fatalf("the request was %s", m.Method)
	}
	agent.send(t, `{"jsonrpc":"2.0","id":`+string(m.ID)+`,"result":{}}`)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
