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

const darkTheme = {
  background: '#18181b',
  primaryColor: '#312e81',
  primaryTextColor: '#e4e4e7',
  primaryBorderColor: '#818cf8',
  secondaryColor: '#3f3f46',
  tertiaryColor: '#27272a',
  lineColor: '#a1a1aa',
  textColor: '#e4e4e7',
  noteBkgColor: '#422006',
  noteTextColor: '#fde68a',
  pie1: '#6366f1',
  pie2: '#f59e0b',
  pie3: '#10b981',
  pie4: '#ec4899',
  pie5: '#06b6d4',
  pie6: '#a3e635',
  pie7: '#f97316',
  pie8: '#8b5cf6',
  pieStrokeColor: '#18181b',
  pieOuterStrokeColor: '#3f3f46',
  pieSectionTextColor: '#ffffff',
  pieTitleTextColor: '#e4e4e7',
  pieLegendTextColor: '#d4d4d8',
};

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
      // mermaid's own dark theme draws a pie's first slices near black: the
      // base theme, dark, with the app's colours, keeps every slice and
      // label readable on the chat's background.
      ...(mode === 'dark' ? { theme: 'base' as const, darkMode: true, themeVariables: darkTheme } : { theme: 'default' as const }),
      // A font every machine has: the SVG is measured here and drawn as an
      // <img>, which can't use the app's own fonts.
      // mermaid 12 defaults to a "neo" look (gradients, new colours) on the
      // base theme; classic with dagre draws diagrams as 11 did.
      look: 'classic',
      layout: 'dagre',
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
