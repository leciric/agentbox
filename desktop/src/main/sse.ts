// Parsing for the daemon's event stream (server-sent events). Kept apart from
// events.ts, which also imports electron and can't be loaded outside it.

// parseEvents splits the complete events off the front of buf and returns their
// data fields, plus the incomplete rest.
export function parseEvents(buf: string): { data: string[]; rest: string } {
  const data: string[] = [];
  for (let end = buf.indexOf('\n\n'); end >= 0; end = buf.indexOf('\n\n')) {
    const lines = buf
      .slice(0, end)
      .split('\n')
      .filter((line) => line.startsWith('data:'))
      .map((line) => line.slice(5).replace(/^ /, ''));
    if (lines.length > 0) data.push(lines.join('\n'));
    buf = buf.slice(end + 2);
  }
  return { data, rest: buf };
}
