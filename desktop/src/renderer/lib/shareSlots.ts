// The default number of agents each paired AgentBox may run here, as typed in
// Settings → Share (ShareSettings.tsx): a whole number from 0 to 64, or null
// while the field holds anything else, such as nothing at all, which saves
// nothing.
export const maxShareSlots = 64;

export function parseSlots(text: string): number | null {
  const s = text.trim();
  if (!/^\d+$/.test(s)) return null;
  const n = Number(s);
  return n <= maxShareSlots ? n : null;
}
