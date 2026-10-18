// The element picker's preview scenario (?pick=1, with &pip=1 and
// &panel=desktop:agent-25): agent-25's desktop shows a settings page, and the
// dev bridge plays the daemon's picker on it. A click on the page answers
// POST .../browser/pick with what a real Chromium picked from the same page
// (dev/pickFixture.ts); an outline on hover stands in for the overlay the
// daemon puts on the agent's page (internal/agent/picker.js), which this
// page, a frame in the preview, doesn't have. A message sent to the lead
// joins its chat, its pictures served from here.
import type { QueryClient } from '@tanstack/react-query';
import type * as T from '../../shared/api';
import { chatKey } from '../lib/chat';
import { setDesktopStandIn, standIn } from '../components/pip/standIn';
import { PROJECT } from './fixtures';
import { sayInPip } from './pip';
import { Frame, H, W } from './pipDesktops';
import { pickDemoPage, pickDemoUrl } from './pickDemo';
import { pickedSave, pickedUpgrade } from './pickFixture';

const picker = `${PROJECT}/agent-25`;
const json = (v: unknown) => ({ status: 200, body: JSON.stringify(v), contentType: 'application/json' });

function DemoPage() {
  return (
    <Frame>
      <iframe title="Settings · Pawly" srcDoc={pickDemoPage} width={W} height={H} style={{ border: 0, display: 'block', background: '#fff' }} data-pick-demo />
    </Frame>
  );
}

// waitForClick resolves with the element picked on the page, or nothing once
// cancelled.
let cancelPick: (() => void) | undefined;
function waitForClick(): Promise<T.ElementPickResult> {
  const docs = [...document.querySelectorAll<HTMLIFrameElement>('iframe[data-pick-demo]')].flatMap((f) => (f.contentDocument ? [f.contentDocument] : []));
  return new Promise((resolve) => {
    let lit: HTMLElement | null = null;
    const hover = (event: MouseEvent) => {
      if (lit) lit.style.outline = '';
      lit = event.target as HTMLElement;
      lit.style.outline = '2px solid #8b7cf6';
      lit.style.cursor = 'crosshair';
    };
    const done = (result: T.ElementPickResult) => {
      if (lit) lit.style.outline = '';
      for (const doc of docs) {
        doc.removeEventListener('click', click, true);
        doc.removeEventListener('mouseover', hover, true);
      }
      cancelPick = undefined;
      resolve(result);
    };
    const click = (event: MouseEvent) => {
      event.preventDefault();
      event.stopPropagation();
      const el = event.target as Element;
      done({ element: el.closest('.btn-upgrade, header') ? pickedUpgrade : pickedSave });
    };
    for (const doc of docs) {
      doc.addEventListener('click', click, true);
      doc.addEventListener('mouseover', hover, true);
    }
    cancelPick = () => done({});
  });
}

const sentImages = new Map<string, string>();
let sent = 0;

export function seedPick(queryClient: QueryClient): void {
  const others = standIn;
  setDesktopStandIn((a) => (a.ref === picker ? <DemoPage /> : others?.(a)));
  type Bridge = { request: (method: string, path: string, body?: unknown) => Promise<unknown>; chatImageUrl: (path: string) => string };
  const bridge = (window as unknown as { agentbox: Bridge }).agentbox;
  const inner = bridge.request;
  const agentPath = `/v1/agents/${PROJECT}/agent-25`;
  bridge.request = async (method, path, body) => {
    if (method === 'GET' && path === `${agentPath}/browser`) return json({ display: true, running: true, pages: [{ id: 'demo', url: pickDemoUrl, title: 'Settings · Pawly' }] } satisfies T.BrowserStatus);
    if (method === 'POST' && path === `${agentPath}/browser/pick`) return json(await waitForClick());
    if (method === 'POST' && path === `${agentPath}/browser/pick/cancel`) {
      cancelPick?.();
      return { status: 204, body: '', contentType: 'application/json' };
    }
    if (method === 'POST' && path.startsWith(`/v1/projects/${PROJECT}/`) && path.endsWith('/messages')) {
      const req = body as T.ChatMessageRequest;
      const now = new Date().toISOString();
      const images = (req.images ?? []).map((image) => {
        const id = `pick-img-${++sent}`;
        sentImages.set(id, `data:${image.mimeType};base64,${image.data}`);
        return { id, mimeType: image.mimeType, name: image.name, size: Math.round((image.data.length * 3) / 4) };
      });
      const item = { id: `pick-m${sent}`, turn: `pick-m${sent}`, kind: 'user', text: req.text, images, createdAt: now, updatedAt: now } as T.ChatItem;
      queryClient.setQueryData(chatKey(`${PROJECT}/lead`), sayInPip(item));
      return json(item);
    }
    return inner(method, path, body);
  };
  const imageUrl = bridge.chatImageUrl;
  bridge.chatImageUrl = (path) => sentImages.get(/\/images\/([^/?]+)$/.exec(path)?.[1] ?? '') ?? imageUrl(path);
}
