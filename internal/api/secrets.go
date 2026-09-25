package api

import "time"

// The secrets API: the keys and tokens you hand to agents. In its own file
// because of the one rule that shapes all of it — **no response here ever
// carries a value**. A secret's value is written, once, into the agent that
// gets it; it is never read back, by this API or by the app
// (D52).

// Secret is one stored secret, without its value: its name, where it lives,
// when the value was last written, and which agents hold it now.
type Secret struct {
	Name string `json:"name"`
	// Scope is "project" (every agent of the project gets it) or "agent".
	Scope   string `json:"scope"`
	Project string `json:"project"`
	// Agent is empty for a project secret.
	Agent string `json:"agent,omitempty"`
	// UpdatedAt is when the value was last written.
	UpdatedAt time.Time `json:"updatedAt"`
	// Agents are the agents this secret is delivered into, by ref. A project
	// secret lists every agent of the project that has it now; an agent secret
	// lists that one agent. An agent created later gets it too.
	Agents []string `json:"agents"`
}

// SetSecretRequest is the body of PUT .../secrets/{name}. Value is the only
// place a value appears in this API, and only ever on the way in.
type SetSecretRequest struct {
	Value string `json:"value"`
}
