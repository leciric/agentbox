import type * as T from '../../shared/api';

// Skills in the app: the composer's slash menu and $ mentions, and the
// Settings pages that manage them. The search and the display names follow
// T3 Code's (providerSkillSearch.ts, inlineSkills.ts): a skill is found by its
// name first, then its title, then its description, and a picked skill goes
// into the message as `$name`, which the daemon turns into what the chat's AI
// tool runs (internal/chat/skills.go).

// skillTitle is a skill's name as words: review-pr is "Review Pr".
export function skillTitle(name: string): string {
  return name
    .split(/[\s:_-]+/)
    .filter(Boolean)
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
    .join(' ');
}

// scoreMatch ranks one field against a query, lower being better: the whole
// field, its start, the start of one of its words, anywhere in it, or (for
// names) its letters in order. null is no match.
function scoreMatch(value: string, query: string, base: number, fuzzy: boolean): number | null {
  if (!value) return null;
  if (value === query) return base;
  if (value.startsWith(query)) return base + 2;
  if (new RegExp(`(^|[\\s/_:-])${query.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}`).test(value)) return base + 4;
  if (value.includes(query)) return base + 6;
  if (fuzzy) {
    let at = 0;
    for (const ch of query) {
      at = value.indexOf(ch, at);
      if (at < 0) return null;
      at++;
    }
    return base + 100;
  }
  return null;
}

export function scoreSkill(skill: Pick<T.Skill, 'name' | 'description'>, query: string): number | null {
  const scores = [
    scoreMatch(skill.name.toLowerCase(), query, 0, true),
    scoreMatch(skillTitle(skill.name).toLowerCase(), query, 1, false),
    scoreMatch(skill.description.toLowerCase(), query, 30, false),
  ].filter((score): score is number => score !== null);
  return scores.length === 0 ? null : Math.min(...scores);
}

// searchSkills is the skills a query finds, best first; all of them, by name,
// for an empty one. A leading $ or / is the trigger, not the query.
export function searchSkills<S extends Pick<T.Skill, 'name' | 'description'>>(skills: readonly S[], query: string, limit = Infinity): S[] {
  const q = query.trim().toLowerCase().replace(/^[$/]+/, '');
  if (!q) return [...skills].sort((a, b) => a.name.localeCompare(b.name)).slice(0, limit);
  return skills
    .map((skill) => ({ skill, score: scoreSkill(skill, q) }))
    .filter((it): it is { skill: S; score: number } => it.score !== null)
    .sort((a, b) => a.score - b.score || a.skill.name.localeCompare(b.skill.name))
    .slice(0, limit)
    .map((it) => it.skill);
}

// composerSkills is what the composer offers: the skills the chat's agent
// has (a project's list says so in active; AgentBox-wide's in enabled), and
// that a user may invoke.
export function composerSkills(skills: readonly T.Skill[] | undefined, perProject: boolean): T.Skill[] {
  return (skills ?? []).filter((s) => (perProject ? s.active : s.enabled) && s.userInvocable);
}

// skillMentionAt finds the $word the cursor is in, for the $ menu: a $ at the
// start or after whitespace, and what's typed after it so far.
export function skillMentionAt(text: string, cursor: number): { start: number; query: string } | undefined {
  const before = text.slice(0, cursor);
  const match = /(^|\s)\$([a-zA-Z0-9:_-]*)$/.exec(before);
  if (!match) return undefined;
  const after = text.slice(cursor);
  if (/^[a-zA-Z0-9:_-]/.test(after)) return undefined;
  return { start: before.length - match[2].length - 1, query: match[2] };
}

// frontmatter splits a SKILL.md into its frontmatter's top-level fields and
// the instructions after it, for the preview.
export function frontmatter(content: string): { fields: [string, string][]; body: string } {
  const match = /^﻿?---\r?\n([\s\S]*?)\r?\n---(?:\r?\n|$)/.exec(content);
  if (!match) return { fields: [], body: content };
  const fields: [string, string][] = [];
  const lines = match[1].split(/\r?\n/);
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    const kv = /^([A-Za-z0-9_-]+):\s*(.*)$/.exec(line);
    if (!kv) continue;
    let value = kv[2].trim();
    const block: string[] = [];
    while (i + 1 < lines.length && (lines[i + 1] === '' || /^\s/.test(lines[i + 1]))) block.push(lines[++i].trim());
    if (/^[>|]/.test(value) || (value === '' && block.length > 0)) value = block.filter(Boolean).join(value.startsWith('|') ? '\n' : ' ');
    else if (block.length > 0) value = [value, ...block.filter(Boolean)].join(' ');
    value = value.replace(/^(['"])([\s\S]*)\1$/, '$2');
    fields.push([kv[1], value]);
  }
  return { fields, body: content.slice(match[0].length) };
}

// skillHue is a stable hue for a skill's tile, from its name.
export function skillHue(name: string): number {
  let h = 0;
  for (const ch of name) h = (h * 31 + ch.charCodeAt(0)) >>> 0;
  return h % 360;
}

// The places a skill can come from, in the order the import dialog lists them.
export const skillOrigins = ['claude', 'claude-plugin', 'codex', 'agents', 'opencode', 'cursor', 'folder', 'git'] as const;
export type SkillOrigin = (typeof skillOrigins)[number];

// originOf reads a stored skill's source back into where it came from.
export function originOf(source: string): SkillOrigin | 'agentbox' {
  if (!source) return 'agentbox';
  const prefix = source.split(':')[0];
  if ((skillOrigins as readonly string[]).includes(prefix) && prefix !== 'folder' && prefix !== 'git') return prefix as SkillOrigin;
  if (/^(https?:|ssh:|git@|github\.com\/)/.test(source)) return 'git';
  return 'folder';
}

// formatBytes is a skill's size, short.
export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(n < 10 * 1024 ? 1 : 0)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

// skillNameOf makes a valid skill name of anything typed: lowercase, digits,
// single hyphens, 64 at most. While typing, a hyphen may end it: the next
// word is on its way.
export function skillNameOf(text: string, typing = false): string {
  const name = text
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+/, '')
    .slice(0, 64);
  return typing ? name : name.replace(/-+$/, '');
}

export function skillTemplate(name: string, description: string): string {
  return `---\nname: ${name}\ndescription: ${description || 'What this skill does, and when to use it.'}\n---\n\n# ${skillTitle(name)}\n\nSay here, step by step, what the agent should do when it uses this skill.\n`;
}
