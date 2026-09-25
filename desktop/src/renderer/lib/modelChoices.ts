import type * as T from '../../shared/api';

// Presentation for a tool's model menu. The menu itself is never composed
// here: it arrives over ACP and differs per account, so everything below only
// arranges choices the adapter really sent (see internal/acp/types.go).

// A group of choices under the heading the tool gave them. Choices the tool
// didn't group keep their order in one unnamed group, so a tool that groups
// nothing looks exactly as it did before.
export type ChoiceGroup = { name: string; choices: T.ChatOptionChoice[] };

export function groupChoices(choices: T.ChatOptionChoice[]): ChoiceGroup[] {
  const groups: ChoiceGroup[] = [];
  for (const choice of choices) {
    const name = choice.group ?? '';
    const last = groups.at(-1);
    if (last && last.name === name) last.choices.push(choice);
    else groups.push({ name, choices: [choice] });
  }
  return groups;
}

// isRecommended marks the choice the tool itself calls its default, so the
// menu can say "Default" once instead of leaving it in the choice's name on
// every screen. Claude Code sends value "default" named "Default
// (recommended)"; another tool saying "recommended" is treated the same.
export function isRecommended(choice: T.ChatOptionChoice): boolean {
  return choice.value === 'default' || /\brecommended\b/i.test(choice.name);
}

// choiceName is a choice's name without the "(recommended)" the badge already
// says, so the two don't appear together.
export function choiceName(choice: T.ChatOptionChoice): string {
  return choice.name.replace(/\s*\(recommended\)$/i, '');
}

// matchesQuery filters on what you can see: the name, the description and the
// value you'd have typed yourself.
export function matchesQuery(choice: T.ChatOptionChoice, query: string): boolean {
  const q = query.trim().toLowerCase();
  if (!q) return true;
  return [choice.name, choice.description ?? '', choice.value].some((s) => s.toLowerCase().includes(q));
}

// searchWorthwhile: a filter box earns its space only once a menu is long
// enough to scan. Most accounts offer four models, where it would be noise.
export const searchThreshold = 8;

// unavailableValue reports a chosen model the tool no longer offers — an
// account that lost an entitlement, or a preference typed for another
// account. It returns the value so the menu can keep showing it, marked,
// rather than silently presenting the tool's default as though you'd picked
// it. "" (never chosen) and a value that is on the menu both give undefined.
export function unavailableValue(choices: T.ChatOptionChoice[], value: string): string | undefined {
  if (!value) return undefined;
  return choices.some((c) => c.value === value) ? undefined : value;
}
