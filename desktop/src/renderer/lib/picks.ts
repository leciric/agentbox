// Elements picked on a page in an agent's browser (the daemon's element
// picker, internal/agent/pick.go), on their way to a chat's composer. A pick
// is made in a desktop view and lands in the composer of the chat that view
// belongs to: kept here until that composer takes it, so a pick made with the
// chat out of sight waits for it. A message carries each pick as a block of
// text the AI tool reads (pickedText) with its crop as a picture, and the
// timeline shows the block as a chip again (splitPicked).
import { useSyncExternalStore } from 'react';
import type * as T from '../../shared/api';

// PendingElement is a pick waiting in a composer, beside its pictures and files.
export interface PendingElement {
  key: string;
  element: T.ElementPick;
  // from is the agent whose browser it was picked in.
  from: string;
}

const waiting = new Map<string, PendingElement[]>();
const listeners = new Set<() => void>();
const empty: PendingElement[] = [];
let next = 0;

function changed() {
  for (const l of listeners) l();
}

// sendPick hands an element picked in from's browser to chat's composer.
export function sendPick(chat: string, from: string, element: T.ElementPick): void {
  waiting.set(chat, [...(waiting.get(chat) ?? []), { key: `pick-${++next}`, element, from }]);
  changed();
}

// takePicks is what waits for chat's composer, which now has it.
export function takePicks(chat: string): PendingElement[] {
  const picks = waiting.get(chat) ?? empty;
  if (picks.length) {
    waiting.delete(chat);
    changed();
  }
  return picks;
}

// useWaitingPicks is what waits for chat's composer, as it changes.
export function useWaitingPicks(chat: string): PendingElement[] {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l);
      return () => listeners.delete(l);
    },
    () => waiting.get(chat) ?? empty,
  );
}

// pickLabel names an element the way its chip does: its tag, with its id or
// first class.
export function pickLabel(el: Pick<T.ElementPick, 'tag' | 'selector'>): string {
  const last = el.selector.split(' > ').pop() ?? '';
  const id = /#[^.#:[\s]+/.exec(last)?.[0];
  const cls = /\.[^.#:[\s]+/.exec(last)?.[0];
  return `${el.tag || last.replace(/[.#:[].*/, '')}${id ?? cls ?? ''}`.replace(/\\/g, '');
}

export const pickImageName = (n: number, image: T.ChatImageUpload) => `element-${n}.${image.mimeType === 'image/jpeg' ? 'jpg' : 'png'}`;

// pickImageNumber is which pick a sent picture is the crop of, or 0.
export const pickImageNumber = (name?: string) => Number(/^element-(\d+)\.(png|jpg)$/.exec(name ?? '')?.[1] ?? 0);

// pickedText is the block a message carries for its nth pick: where it is,
// what it is and how it looks, for the AI tool to find it in the code.
export function pickedText(n: number, pick: PendingElement): string {
  const el = pick.element;
  const box = el.box;
  const lines = [
    `<picked-element n="${n}" from="${pick.from}"${el.image ? ` image="${pickImageName(n, el.image)}"` : ''}>`,
    `Page: ${el.url}${el.title ? ` (${el.title})` : ''}`,
    `Selector: ${el.selector}`,
    `Box: ${box.width}×${box.height} at ${box.x},${box.y} in a ${el.viewport.width}×${el.viewport.height} viewport`,
  ];
  if (el.text) lines.push(`Text: ${el.text}`);
  if (el.styles.length) lines.push(`Styles: ${el.styles.map((s) => `${s.name}: ${s.value}`).join('; ')}`);
  lines.push('HTML:', '```html', el.html, '```', '</picked-element>');
  return lines.join('\n');
}

// withPicks is a message's text with its picks after it.
export function withPicks(text: string, picks: PendingElement[]): string {
  if (!picks.length) return text;
  return [text, ...picks.map((p, i) => pickedText(i + 1, p))].filter(Boolean).join('\n\n');
}

export interface PickedChip {
  n: number;
  label: string;
  selector: string;
  url: string;
  from: string;
}

const block = /\n*<picked-element n="(\d+)" from="([^"]*)"[^>]*>\n([\s\S]*?)\n<\/picked-element>/g;

// splitPicked parts a sent message's text into what was typed and its picks.
export function splitPicked(text: string): { text: string; picks: PickedChip[] } {
  const picks: PickedChip[] = [];
  const rest = text.replace(block, (_, n: string, from: string, body: string) => {
    const field = (name: string) => new RegExp(`^${name}: (.*)$`, 'm').exec(body)?.[1] ?? '';
    const selector = field('Selector');
    const tag = /^```html\n<([a-z][\w-]*)/m.exec(body)?.[1] ?? '';
    picks.push({ n: Number(n), selector, url: field('Page').replace(/ \(.*\)$/, ''), from, label: pickLabel({ tag, selector }) });
    return '';
  });
  return { text: rest.trim(), picks };
}
