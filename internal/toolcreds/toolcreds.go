// Package toolcreds lists the environment variables with which each AI tool
// picks its own credential or endpoint, so that a project secret of the same
// name can't quietly take over from the account the tool was signed in with.
//
// An agent's secrets are exported in every shell it has (the env file sources
// them), and the AI tools read their credentials from the environment before
// any stored sign-in: Claude Code prefers ANTHROPIC_API_KEY to
// CLAUDE_CODE_OAUTH_TOKEN, Codex OPENAI_API_KEY to its ChatGPT login. So when a
// tool runs on an account AgentBox signed it in with, internal/agent starts it
// with these variables removed, and gives them back to the shells it starts for
// commands, where the agent's own code needs them.
//
// It is a package of its own because the secrets store, the agent manager, the
// command line and the API's TypeScript constants all read the same lists.
package toolcreds

import (
	"slices"
	"strings"
)

// Variables are, per AI tool (agent.Tools' keys), the variables it reads to
// pick a credential, a provider or an endpoint, from each tool's own docs.
// CLAUDE_CODE_OAUTH_TOKEN isn't here: it is how AgentBox signs Claude Code in,
// and a secret can't be named after it (secrets.ValidateName).
var Variables = map[string][]string{
	"claude": {
		"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "ANTHROPIC_CUSTOM_HEADERS",
		"ANTHROPIC_PROFILE", "ANTHROPIC_FEDERATION_RULE_ID", "ANTHROPIC_ORGANIZATION_ID", "ANTHROPIC_WORKSPACE_ID",
		"CLAUDE_CODE_OAUTH_REFRESH_TOKEN", "CLAUDE_CONFIG_DIR", "CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST",
		"CLAUDE_CODE_USE_BEDROCK", "AWS_BEARER_TOKEN_BEDROCK", "ANTHROPIC_BEDROCK_BASE_URL", "ANTHROPIC_BEDROCK_MANTLE_BASE_URL",
		"ANTHROPIC_AWS_API_KEY", "ANTHROPIC_AWS_BASE_URL",
		"CLAUDE_CODE_USE_VERTEX", "ANTHROPIC_VERTEX_BASE_URL", "ANTHROPIC_VERTEX_PROJECT_ID",
		"CLAUDE_CODE_USE_FOUNDRY", "ANTHROPIC_FOUNDRY_API_KEY", "ANTHROPIC_FOUNDRY_AUTH_TOKEN", "ANTHROPIC_FOUNDRY_BASE_URL", "ANTHROPIC_FOUNDRY_RESOURCE",
	},
	"codex": {"OPENAI_API_KEY", "CODEX_API_KEY", "OPENAI_BASE_URL", "CODEX_HOME"},
	// OpenCode reads a key for every provider it knows; these are the
	// providers people sign in to, whose key would replace the sign-in.
	"opencode": {
		"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL",
		"OPENAI_API_KEY", "OPENAI_BASE_URL", "OPENROUTER_API_KEY", "GEMINI_API_KEY", "GOOGLE_GENERATIVE_AI_API_KEY",
		"GROQ_API_KEY", "XAI_API_KEY", "DEEPSEEK_API_KEY", "MISTRAL_API_KEY", "OPENCODE_API_KEY",
	},
}

// Names are the tools' names as people know them.
var Names = map[string]string{"claude": "Claude Code", "codex": "Codex", "opencode": "OpenCode"}

// Keys are the variables that hold a credential, rather than choosing an
// endpoint or a provider: the ones a status reports as what a tool runs on.
var Keys = map[string]bool{
	"ANTHROPIC_API_KEY": true, "ANTHROPIC_AUTH_TOKEN": true, "AWS_BEARER_TOKEN_BEDROCK": true, "ANTHROPIC_AWS_API_KEY": true,
	"ANTHROPIC_FOUNDRY_API_KEY": true, "ANTHROPIC_FOUNDRY_AUTH_TOKEN": true, "CLAUDE_CODE_OAUTH_REFRESH_TOKEN": true,
	"OPENAI_API_KEY": true, "CODEX_API_KEY": true, "OPENROUTER_API_KEY": true, "GEMINI_API_KEY": true,
	"GOOGLE_GENERATIVE_AI_API_KEY": true, "GROQ_API_KEY": true, "XAI_API_KEY": true, "DEEPSEEK_API_KEY": true,
	"MISTRAL_API_KEY": true, "OPENCODE_API_KEY": true,
}

// Tools are the tools that read name, in a stable order: empty for a variable
// no AI tool chooses its credential with.
func Tools(name string) []string {
	var tools []string
	for _, tool := range []string{"claude", "codex", "opencode"} {
		if slices.Contains(Variables[tool], name) {
			tools = append(tools, tool)
		}
	}
	return tools
}

// Warning is what someone adding a secret named name is told, or "" when no AI
// tool reads it. Same words in the command line, the API and the app.
func Warning(name string) string {
	tools := Tools(name)
	if len(tools) == 0 {
		return ""
	}
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = Names[t]
	}
	return name + " is for the agent's code only: " + strings.Join(names, " and ") +
		" won't use it while signed in to an account. AgentBox keeps it out of the AI tool's own environment, so it never replaces that account; the agent's shell, its tests and the commands its AI tool runs still have it."
}

// Joined is a tool's variables as one space-separated string, for the
// TypeScript constants the app checks names against.
func Joined(tool string) string { return strings.Join(Variables[tool], " ") }
