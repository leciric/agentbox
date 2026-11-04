// The system tray icon, a Mac's menu bar icon: how many agents are working
// and how many things wait for the user, a menu of them by project, and the
// app's quick actions. Like the rest of the main process (D19) it holds no
// AgentBox logic: it reads the API the window reads, through the same relay
// (traymodel.ts says which reads), and its actions are API calls the window
// makes too, or the VM's power, as the top bar asks it (vmpower.ts). Where
// an entry points is the window's to open, sent as tray:open.
//
// It reads again when the event stream says something it shows moved, a
// second after the last such event, and every minute besides, as the inbox
// does. With keepRunning (tray.json in userData, Settings → General) closing
// the window hides it instead, and AgentBox carries on in the tray until
// Quit; the window keeps running hidden, so its toasts and OS notifications
// still come.
import { app, dialog, ipcMain, Menu, nativeImage, shell, Tray, type MenuItemConstructorOptions, type NativeImage } from 'electron';
import { readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import type * as T from '../shared/api';
import { t } from '../shared/i18n/index.ts';
import type { ApiResponse } from './daemon';
import { isSafeExternalUrl } from './externalurl';
import { badgeText, trayIcon, type TrayIconSpec, type TrayTone } from './trayicon';
import { macTitle, offline, summary, trayMenu, trayStatus, type TrayAction, type TrayData, type TrayEntry, type TrayItemKind, type TrayStatus, type TrayTarget } from './traymodel';
import { actOnVM, vmPower } from './vmpower';

const onMac = process.platform === 'darwin';

export interface TraySettings {
  keepRunning: boolean;
}

interface Saved extends TraySettings {
  // Whether this computer reviews finished agents (the window's Settings →
  // General → Review finished agents), which the window says: off leaves
  // Ready to review out of the count.
  reviewing: boolean;
}

const file = () => join(app.getPath('userData'), 'tray.json');

function load(): Saved {
  try {
    const saved = JSON.parse(readFileSync(file(), 'utf8')) as Partial<Saved>;
    return { keepRunning: saved.keepRunning === true, reviewing: saved.reviewing !== false };
  } catch {
    return { keepRunning: false, reviewing: true };
  }
}

// The events that can change what the tray shows.
const moving = new Set(['agent', 'notification', 'notification.seen', 'question', 'review', 'pulls', 'project']);
const pollEvery = 60_000;
const settleFor = 1_000;
const pageSize = '50';

export interface TrayHost {
  call: (method: string, path: string, body?: unknown) => Promise<ApiResponse>;
  connected: () => boolean;
  // showWindow brings the window forward, making it again if it's gone.
  showWindow: () => void;
  // open hands a target to the window, after showing it.
  open: (target: TrayTarget) => void;
}

export class AppTray {
  private tray?: Tray;
  private saved = load();
  private status: TrayStatus = offline;
  private vm: 'on' | 'off' | 'none' = 'none';
  private timer?: NodeJS.Timeout;
  private poll?: NodeJS.Timeout;
  private reading = false;
  // When the VM's power was last asked, which runs the command-line tool:
  // with the minute's poll, a connection that changed and an action, not
  // every event.
  private vmAsked = 0;
  private again = false;
  private icons = new Map<string, NativeImage>();

  constructor(private readonly host: TrayHost) {
    ipcMain.handle('tray:settings', (): TraySettings => ({ keepRunning: this.saved.keepRunning }));
    ipcMain.handle('tray:set', (_event, next: Partial<TraySettings>) => {
      if (typeof next?.keepRunning === 'boolean') this.save({ keepRunning: next.keepRunning });
      return { keepRunning: this.saved.keepRunning };
    });
    ipcMain.on('tray:reviewing', (_event, on: boolean) => {
      if (this.saved.reviewing === !!on) return;
      this.save({ reviewing: !!on });
      this.refreshSoon();
    });
  }

  get keepRunning(): boolean {
    return this.saved.keepRunning;
  }

  start(): void {
    this.tray = new Tray(this.icon('offline', 0, 0));
    // Windows' tray and a Linux XEmbed one (tint2, Xfce's) open the menu on
    // a right click, and a left click opens the window, as a Mac's dock icon
    // does. A Mac, and a Linux tray of StatusNotifierItems (KDE, GNOME's
    // AppIndicator), open the menu on any click.
    if (!onMac) this.tray.on('click', () => this.host.showWindow());
    this.draw();
    this.poll = setInterval(() => {
      this.vmAsked = 0;
      this.refreshSoon(0);
    }, pollEvery);
    this.refreshSoon(0);
  }

  stop(): void {
    clearTimeout(this.timer);
    clearInterval(this.poll);
    this.tray?.destroy();
    this.tray = undefined;
  }

  onEvent(event: unknown): void {
    const type = (event as { type?: unknown } | null)?.type;
    if (typeof type === 'string' && moving.has(type)) this.refreshSoon();
  }

  onConnection(): void {
    this.vmAsked = 0;
    this.refreshSoon(0);
  }

  private save(next: Partial<Saved>): void {
    // Kept running, AgentBox is one instance (index.ts's second-instance).
    if (next.keepRunning === true && !this.saved.keepRunning) app.requestSingleInstanceLock();
    if (next.keepRunning === false && this.saved.keepRunning) app.releaseSingleInstanceLock();
    this.saved = { ...this.saved, ...next };
    try {
      writeFileSync(file(), JSON.stringify(this.saved));
    } catch (err) {
      console.error('saving tray settings:', err);
    }
    this.draw();
  }

  private refreshSoon(delay = settleFor): void {
    clearTimeout(this.timer);
    this.timer = setTimeout(() => void this.refresh(), delay);
  }

  private async refresh(): Promise<void> {
    if (!this.tray) return;
    if (this.reading) {
      this.again = true;
      return;
    }
    this.reading = true;
    try {
      this.status = trayStatus(await this.read());
      if (Date.now() - this.vmAsked >= pollEvery / 2) {
        this.vmAsked = Date.now();
        const power = await vmPower().catch(() => null);
        this.vm = !power ? 'none' : power.state === 'off' || power.state === 'stopping' ? 'off' : 'on';
      }
    } finally {
      this.reading = false;
    }
    this.draw();
    if (this.again) {
      this.again = false;
      this.refreshSoon();
    }
  }

  private async read(): Promise<TrayData | undefined> {
    if (!this.host.connected()) return undefined;
    const get = async <V>(path: string): Promise<V> => {
      const res = await this.host.call('GET', path);
      if (res.status < 200 || res.status >= 300) throw new Error(`${path}: HTTP ${res.status}`);
      return JSON.parse(res.body) as V;
    };
    const unseen = new URLSearchParams({ limit: pageSize, unseen: '1' });
    try {
      const [agents, projects, questions, review] = await Promise.all([
        get<T.Agent[]>('/v1/agents'),
        get<T.Project[]>('/v1/projects'),
        get<T.NotificationPage>(`/v1/notifications?${unseen}`),
        this.saved.reviewing ? get<T.ReviewPage>(`/v1/review?limit=${pageSize}`) : Promise.resolve(undefined),
      ]);
      return { agents, projects, questions, review };
    } catch (err) {
      console.error('tray:', err instanceof Error ? err.message : err);
      return undefined;
    }
  }

  private icon(tone: TrayTone, waiting: number, working: number): NativeImage {
    const key = `${tone}:${badgeText(waiting)}:${badgeText(working)}`;
    let image = this.icons.get(key);
    if (image) return image;
    // A Mac's menu bar is 22pt tall, its icons 18pt; Windows' tray takes 16px
    // at 100%; a Linux tray is commonly 22–24px. Each gets its 1× and 2×.
    const size = onMac ? 18 : process.platform === 'win32' ? 16 : 24;
    const spec = (scale: number): TrayIconSpec => ({ size: size * scale, tone, waiting, working, template: onMac });
    image = nativeImage.createFromBitmap(trayIcon(spec(1)), { width: size, height: size, scaleFactor: 1 });
    image.addRepresentation({ scaleFactor: 2, width: size * 2, height: size * 2, buffer: nativeImage.createFromBitmap(trayIcon(spec(2)), { width: size * 2, height: size * 2, scaleFactor: 2 }).toPNG() });
    if (onMac) image.setTemplateImage(true);
    this.icons.set(key, image);
    return image;
  }

  private draw(): void {
    if (!this.tray) return;
    const s = this.status;
    this.tray.setImage(this.icon(s.tone, s.waiting, s.working));
    this.tray.setToolTip(`AgentBox: ${summary(s)}`);
    if (onMac) this.tray.setTitle(macTitle(s), { fontType: 'monospacedDigit' });
    this.tray.setContextMenu(Menu.buildFromTemplate(trayMenu(s, { vm: this.vm, keepRunning: this.saved.keepRunning }).map((e) => this.item(e))));
  }

  private item(e: TrayEntry): MenuItemConstructorOptions {
    if (e.type === 'separator') return { type: 'separator' };
    const action = e.action;
    return {
      type: e.type === 'checkbox' ? 'checkbox' : 'normal',
      // Windows and Linux take & for a mnemonic.
      label: onMac ? e.label : e.label?.replaceAll('&', '&&'),
      enabled: e.enabled ?? true,
      checked: e.checked,
      icon: e.icon ? this.dot(e.icon) : undefined,
      click: action ? () => void this.act(action) : undefined,
    };
  }

  // dot is a menu entry's mark: orange for what waits on the user, violet for
  // review, green for working, the icon's badges' colours.
  private dot(kind: TrayItemKind): NativeImage {
    const key = `dot:${kind}`;
    let image = this.icons.get(key);
    if (image) return image;
    const colour = kind === 'working' ? [34, 197, 94] : kind === 'review' ? [139, 92, 246] : [249, 115, 22];
    const size = 16;
    const px = Buffer.alloc(size * size * 4);
    for (let y = 0; y < size; y++) {
      for (let x = 0; x < size; x++) {
        // A 8px dot, its edge smoothed over a pixel.
        const alpha = Math.max(0, Math.min(1, 4.5 - Math.hypot(x + 0.5 - size / 2, y + 0.5 - size / 2)));
        const i = (y * size + x) * 4;
        px[i] = colour[2];
        px[i + 1] = colour[1];
        px[i + 2] = colour[0];
        px[i + 3] = Math.round(alpha * 255);
      }
    }
    image = nativeImage.createFromBitmap(px, { width: size, height: size });
    this.icons.set(key, image);
    return image;
  }

  private async act(action: TrayAction): Promise<void> {
    try {
      switch (action.kind) {
        case 'open':
          return this.host.showWindow();
        case 'go':
          if (action.target.kind === 'url') {
            if (isSafeExternalUrl(action.target.url)) await shell.openExternal(action.target.url);
            return;
          }
          return this.host.open(action.target);
        case 'pauseAll':
          return await this.pauseAll();
        case 'stopVM':
          return await this.stopVM();
        case 'keepRunning':
          return this.save({ keepRunning: !this.saved.keepRunning });
        case 'quit':
          return app.quit();
      }
    } catch (err) {
      dialog.showErrorBox(t('main.tray.failed'), err instanceof Error ? err.message : String(err));
    } finally {
      this.vmAsked = 0;
      this.refreshSoon(0);
    }
  }

  // pauseAll pauses every running agent, as each one's Pause in the window
  // does (POST /v1/agents/{project}/{agent}/pause): their machines freeze
  // where they are, and Resume carries on.
  private async pauseAll(): Promise<void> {
    const res = await this.host.call('GET', '/v1/agents');
    if (res.status !== 200) throw new Error(res.body || `HTTP ${res.status}`);
    const running = (JSON.parse(res.body) as T.Agent[]).filter((a) => a.state === 'running');
    const results = await Promise.all(running.map((a) => this.host.call('POST', `/v1/agents/${a.ref.split('/').map(encodeURIComponent).join('/')}/pause`)));
    const failed = results.filter((r) => r.status >= 300);
    if (failed.length) throw new Error(failed.map((r) => errorText(r)).join('\n'));
  }

  // stopVM turns AgentBox's VM off, after asking: every agent stops with it.
  private async stopVM(): Promise<void> {
    const working = this.status.working;
    const { response } = await dialog.showMessageBox({
      type: 'warning',
      title: 'AgentBox',
      message: t('main.tray.stopVMConfirm'),
      detail: [working ? t('main.tray.stopVMWorking', { count: working }) : '', t('main.tray.stopVMDetail')].filter(Boolean).join(' '),
      buttons: [t('main.tray.stopVMButton'), t('main.tray.cancel')],
      defaultId: 1,
      cancelId: 1,
    });
    if (response !== 0) return;
    this.vm = 'off';
    this.draw();
    await actOnVM('stop');
  }
}

function errorText(res: ApiResponse): string {
  try {
    return (JSON.parse(res.body) as { error?: string }).error ?? res.body;
  } catch {
    return res.body || `HTTP ${res.status}`;
  }
}
