import * as A from '../../shared/api.ts';
import type * as T from '../../shared/api';

// The variables each AI tool picks its credential or endpoint by, generated
// from Go (internal/toolcreds). AgentBox keeps a secret of one of these names
// out of the tool's own environment while it runs on a signed-in account, so
// the app warns as the name is typed.
const variables: Record<string, string[]> = {
  claude: A.ClaudeCredentialVariables.split(' '),
  codex: A.CodexCredentialVariables.split(' '),
  opencode: A.OpenCodeCredentialVariables.split(' '),
};

// aiToolsReading are the AI tools (claude, codex, opencode) that would take
// name as their credential, in a stable order: empty for any other name.
export function aiToolsReading(name: string): string[] {
  const n = name.trim();
  if (!n) return [];
  return Object.keys(variables).filter((tool) => variables[tool].includes(n));
}

// credentialTone says how a tool's real credential should read: fine on an
// account or its own sign-in, a warning when a variable from its environment
// (a secret, most likely) is what it bills.
export function credentialTone(cred: T.ToolCredential): 'ok' | 'warning' | 'idle' {
  if (!cred.running || cred.error) return 'idle';
  if (!cred.variable || cred.variable === 'CLAUDE_CODE_OAUTH_TOKEN') return 'ok';
  return 'warning';
}

// CredentialGroup is how many agents run on one thing: an account by name,
// their own sign-in ('own'), or nothing yet ('idle': not running, or not read).
export type CredentialGroup = { on: string; count: number };

// summarizeCredentials is a project's agents in a line: those billing a
// variable rather than their account (the variables named), then the rest
// grouped by what they run on, the largest group first.
export function summarizeCredentials(creds: T.ToolCredential[]): { off: number; variables: string[]; groups: CredentialGroup[] } {
  const variables = new Set<string>();
  const counts = new Map<string, number>();
  let off = 0;
  for (const cred of creds) {
    const tone = credentialTone(cred);
    if (tone === 'warning') {
      off++;
      variables.add(cred.variable ?? '');
      continue;
    }
    const on = tone === 'idle' ? 'idle' : cred.variable ? cred.account || 'token' : cred.account || 'own';
    counts.set(on, (counts.get(on) ?? 0) + 1);
  }
  const groups = [...counts].map(([on, count]) => ({ on, count })).toSorted((a, b) => (a.on === 'idle' ? 1 : b.on === 'idle' ? -1 : b.count - a.count));
  return { off, variables: [...variables].toSorted(), groups };
}
