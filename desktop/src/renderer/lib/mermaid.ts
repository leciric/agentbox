import type { Mermaid } from 'mermaid';

// drawMermaid draws a ```mermaid block as SVG, in the window's mode. mermaid
// is a megabyte or two of JavaScript, so it loads the first time a chat has a
// diagram to draw. It runs at securityLevel strict (its own DOMPurify, no
// click handlers or HTML in labels), and the chat shows what it draws as an
// <img>, where nothing in it runs or loads.
//
// One diagram is drawn at a time, since mermaid keeps one configuration, and
// the last ones drawn are kept: a chat redraws its history as it scrolls.

const KEEP = 64;
const drawn = new Map<string, Promise<string>>();
let mermaid: Promise<Mermaid> | undefined;
let queue: Promise<unknown> = Promise.resolve();
let next = 0;

export function drawMermaid(source: string, mode: 'dark' | 'light'): Promise<string> {
  const key = `${mode}\0${source}`;
  const had = drawn.get(key);
  if (had) {
    drawn.delete(key);
    drawn.set(key, had);
    return had;
  }
  const svg = queue.then(async () => {
    mermaid ??= import('mermaid').then((m) => m.default);
    const m = await mermaid;
    m.initialize({
      startOnLoad: false,
      securityLevel: 'strict',
      theme: mode === 'dark' ? 'dark' : 'default',
      // A font every machine has: the SVG is measured here and drawn as an
      // <img>, which can't use the app's own fonts.
      fontFamily: 'ui-sans-serif, system-ui, sans-serif',
      htmlLabels: false,
      flowchart: { htmlLabels: false },
    });
    const { svg } = await m.render(`agentbox-mermaid-${next++}`, source);
    return svg;
  });
  queue = svg.catch(() => {});
  drawn.set(key, svg);
  svg.catch(() => drawn.delete(key));
  for (const k of drawn.keys()) {
    if (drawn.size <= KEEP) break;
    drawn.delete(k);
  }
  return svg;
}
