import { useQuery, useQueryClient } from '@tanstack/react-query';
import { AppWindow, Check, Code2, Copy, ExternalLink, Eye, Globe, GlobeLock, Image, LoaderCircle, Maximize2, Minus, PanelRight, Plus, TriangleAlert, Workflow } from 'lucide-react';
import { createContext, use, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { toast } from 'sonner';
import { api, isHomeChat } from '../../lib/api';
import { useT } from '../../lib/i18n';
import { drawMermaid, MermaidLoadError } from '../../lib/mermaid';
import { pagesKey } from '../../lib/pages';
import {
  BLOCKED_MESSAGE,
  blockedHost,
  ERROR_MESSAGE,
  FRAME_VIEWPORT,
  framedPage,
  frameHeight,
  HEIGHT_MESSAGE,
  LINK_MESSAGE,
  openableLink,
  pageOf,
  PICTURE_MAX,
  svgSize,
  svgUrl,
  THEME_MESSAGE,
  themeFragment,
  titleOf,
  ZOOM_MESSAGE,
  type PageTheme,
  type RichKind,
} from '../../lib/rich';
import { openPreview, type RichPreview } from '../../lib/rightPanel';
import { useMode, usePageTheme } from '../../lib/theme';
import { useUiV2 } from '../../lib/uiV2';
import { cn, errorMessage } from '../../lib/utils';
import { pagesQuery } from '../pages/usePages';
import { Button } from '../ui/button';
import { Dialog, DialogContent, DialogTitle } from '../ui/dialog';
import { Input } from '../ui/input';
import { Tip } from '../ui/tooltip';
import { ZoomableImage } from '../ZoomableImage';
import { ChatRefContext } from './chatRef';
import { CodeBlock } from './CodeBlock';

// RichContext is whether the Markdown around a block draws rich blocks (a
// chat's messages do), and whether its message is still streaming: a block
// is drawn once its message has finished, not on every chunk.
export const RichContext = createContext({ rich: false, streaming: false });

// The pages whose network the user turned on, by their HTML, so the block
// keeps it as the chat scrolls it out and back. Only for this window.
const networked = new Set<string>();

// RichBlock is a ```mermaid, ```svg or ```html block of a chat's message
// (lib/rich.ts): drawn, with its code a click away, or, for a plain ```html
// block, its code with a click to preview it.
export function RichBlock({ kind, drawn, code, language }: { kind: RichKind; drawn: boolean; code: string; language?: string }) {
  const t = useT();
  const { rich } = use(RichContext);
  const [showing, setShowing] = useState(drawn);
  if (!rich) return <CodeBlock code={code} language={language} />;
  if (!showing) {
    return (
      <CodeBlock
        code={code}
        language={language}
        actions={
          <HeaderButton label={t(kind === 'html' ? 'chat.rich.preview' : 'chat.rich.draw')} onClick={() => setShowing(true)}>
            <Eye className="size-3.5" />
          </HeaderButton>
        }
      />
    );
  }
  return <DrawnBlock kind={kind} code={code} onCode={() => setShowing(false)} />;
}

// GeneratingCard stands for a block whose code is still arriving: the kind's
// icon, a shimmer and what has come so far, until the fence closes and the
// block is drawn.
export function GeneratingCard({ kind, lines, bytes }: { kind: RichKind; lines: number; bytes: number }) {
  const t = useT();
  const Icon = kind === 'html' ? AppWindow : kind === 'svg' ? Image : Workflow;
  const size = bytes < 1024 ? `${bytes} B` : `${(bytes / 1024).toFixed(1)} KB`;
  return (
    <div className="chat-codeblock" data-rich-generating={kind} role="status">
      <div className="flex h-14 items-center gap-3 px-3 text-[12px] text-subtle">
        <Icon className="size-4 shrink-0" />
        <span className="min-w-0 flex-1 truncate">{t(`chat.rich.generating.${kind}`)}</span>
        <span className="shrink-0 tabular-nums text-faint">{t('chat.rich.generatingSoFar', { lines, size })}</span>
      </div>
      <div className="skeleton h-0.5 rounded-none" />
    </div>
  );
}

// PageProblem is what went wrong in a page the chat runs: something it tried
// to load with no network, or the first error its scripts threw.
type PageProblem = { kind: 'blocked'; host: string } | { kind: 'error'; message: string };

// worse is the problem to show of two: a page refused its network throws for
// want of what it couldn't load, and the network is what fixes both.
const worse = (had: PageProblem | null, next: PageProblem): PageProblem => (had?.kind === 'blocked' ? had : next);

function DrawnBlock({ kind, code, onCode }: { kind: RichKind; code: string; onCode: () => void }) {
  const t = useT();
  const chatRef = use(ChatRefContext);
  // Beside the chat is its panel, which UI v2 has, and a project's chats.
  const beside = useUiV2() && !!chatRef && !isHomeChat(chatRef);
  const [large, setLarge] = useState(false);
  const [network, setNetwork] = useState(() => networked.has(code));
  const [problem, setProblem] = useState<PageProblem | null>(null);
  const [copied, setCopied] = useState(false);
  const toggleNetwork = () => {
    if (network) networked.delete(code);
    else networked.add(code);
    setNetwork(!network);
    setProblem(null);
  };
  return (
    <div className="chat-codeblock" data-rich={kind}>
      <div className="flex h-8 items-center justify-between gap-2 border-b border-line-faint pl-3 pr-1 text-[11px] text-subtle">
        <span className="flex min-w-0 items-center gap-1.5">
          <span className="truncate">{t(`chat.rich.${kind}`)}</span>
          {kind === 'html' && <span className={cn('shrink-0 truncate', network ? 'text-amber-400' : 'text-faint')}>· {t(network ? 'chat.rich.online' : 'chat.rich.offline')}</span>}
        </span>
        <div className="flex shrink-0 items-center gap-0.5">
          {kind === 'html' && (
            <HeaderButton label={t(network ? 'chat.rich.blockNetwork' : 'chat.rich.allowNetwork')} onClick={toggleNetwork}>
              {network ? <Globe className="size-3.5" /> : <GlobeLock className="size-3.5" />}
            </HeaderButton>
          )}
          <HeaderButton label={t('chat.rich.showCode')} onClick={onCode}>
            <Code2 className="size-3.5" />
          </HeaderButton>
          {beside && (
            <HeaderButton label={t('chat.rich.openBeside')} onClick={() => openPreview(chatRef, { kind, code, network })} data-rich-beside>
              <PanelRight className="size-3.5" />
            </HeaderButton>
          )}
          <HeaderButton label={t('chat.rich.openLarge')} onClick={() => setLarge(true)}>
            <Maximize2 className="size-3.5" />
          </HeaderButton>
          <HeaderButton
            label={t('chat.markdown.copyCode')}
            onClick={() => {
              window.agentbox.copyText(code);
              setCopied(true);
              setTimeout(() => setCopied(false), 1200);
            }}
          >
            {copied ? <Check className="size-3.5 text-emerald-400" /> : <Copy className="size-3.5" />}
          </HeaderButton>
        </div>
      </div>
      {problem && <ProblemBar problem={problem} network={network} onNetwork={toggleNetwork} onCode={onCode} />}
      <RichView kind={kind} code={code} network={network} onOpen={() => setLarge(true)} onCode={onCode} onProblem={(p) => setProblem((had) => worse(had, p))} />
      {large && <RichDialog kind={kind} code={code} network={network} onClose={() => setLarge(false)} />}
    </div>
  );
}

function HeaderButton({ label, onClick, children, ...data }: { label: string; onClick: () => void; children: ReactNode } & Record<`data-${string}`, boolean>) {
  return (
    <Tip label={label}>
      <button aria-label={label} className="flex size-6 items-center justify-center rounded-md text-subtle transition hover:bg-surface-raised hover:text-secondary" onClick={onClick} {...data}>
        {children}
      </button>
    </Tip>
  );
}

// ProblemBar says what went wrong in a page, with what fixes it: its network
// turned on, for a page that reached for it, or its code, for one that threw.
function ProblemBar({ problem, network, onNetwork, onCode }: { problem: PageProblem; network: boolean; onNetwork: () => void; onCode?: () => void }) {
  const t = useT();
  const blocked = problem.kind === 'blocked' && !network;
  return (
    <div className="flex items-start gap-2 border-b border-line-faint px-3 py-1.5 text-[11.5px] text-amber-300" role="status" data-rich-problem={problem.kind}>
      <TriangleAlert className="mt-0.5 size-3.5 shrink-0" />
      <span className="min-w-0 flex-1 break-words">{problem.kind === 'blocked' ? t('chat.rich.blocked', { host: problem.host }) : t('chat.rich.scriptFailed', { error: problem.message })}</span>
      {blocked ? (
        <button className="shrink-0 underline underline-offset-2 hover:opacity-80" onClick={onNetwork}>
          {t('chat.rich.allowNetwork')}
        </button>
      ) : (
        problem.kind === 'error' &&
        onCode && (
          <button className="shrink-0 underline underline-offset-2 hover:opacity-80" onClick={onCode}>
            {t('chat.rich.showCode')}
          </button>
        )
      )}
    </div>
  );
}

// RichView is a block drawn: in the chat, fitted to the column and no taller
// than PICTURE_MAX (a page FRAME_VIEWPORT of the window), or large, filling
// the dialog or the panel, a page at zoom.
function RichView({ kind, code, network, large, zoom, onOpen, onCode, onProblem }: { kind: RichKind; code: string; network: boolean; large?: boolean; zoom?: number; onOpen?: () => void; onCode?: () => void; onProblem?: (problem: PageProblem) => void }) {
  if (kind === 'html') return <PageFrame html={code} network={network} large={large} zoom={zoom} onProblem={onProblem} />;
  return <Picture kind={kind} code={code} large={large} onOpen={onOpen} onCode={onCode} />;
}

function Picture({ kind, code, large, onOpen, onCode }: { kind: RichKind; code: string; large?: boolean; onOpen?: () => void; onCode?: () => void }) {
  const t = useT();
  const drawn = useSvg(kind, code);
  if (drawn.error) {
    return (
      <div className="flex items-start gap-2 px-3 py-2.5 text-[12px] text-rose-300" role="alert">
        <TriangleAlert className="mt-0.5 size-3.5 shrink-0" />
        <span className="min-w-0 break-words">
          {t(drawn.retry ? 'chat.rich.loadFailed' : 'chat.rich.drawFailed', { error: drawn.error })}{' '}
          {drawn.retry && (
            <button className="mr-2 underline underline-offset-2 hover:opacity-80" onClick={drawn.retry}>
              {t('chat.rich.retry')}
            </button>
          )}
          {onCode && (
            <button className="underline underline-offset-2 hover:opacity-80" onClick={onCode}>
              {t('chat.rich.showCode')}
            </button>
          )}
        </span>
      </div>
    );
  }
  if (!drawn.svg) return <div className="skeleton h-32 rounded-none" role="status" aria-label={t('chat.rich.drawing')} />;
  const url = svgUrl(drawn.svg);
  const label = titleOf(kind, code, t(`chat.rich.${kind}`));
  if (large) return <ZoomableImage src={url} alt={label} />;
  const size = svgSize(drawn.svg);
  return (
    <button className="block w-full cursor-zoom-in p-3" onClick={onOpen} aria-label={t('chat.rich.openLarge')}>
      <img
        src={url}
        alt={label}
        draggable={false}
        className="mx-auto block h-auto max-w-full object-contain"
        // An SVG sized 100% has no height of its own: its viewBox gives the shape.
        style={{ width: size ? `${size.width}px` : undefined, aspectRatio: size ? `${size.width} / ${size.height}` : undefined, maxHeight: PICTURE_MAX }}
      />
    </button>
  );
}

// useSvg is a picture's SVG: the block's own, or the diagram mermaid drew of
// it in the window's mode; when mermaid itself didn't load, a retry.
function useSvg(kind: RichKind, code: string): { svg?: string; error?: string; retry?: () => void } {
  const mode = useMode();
  const [attempt, setAttempt] = useState(0);
  const [drawn, setDrawn] = useState<{ key: string; svg?: string; error?: string; load?: boolean } | null>(null);
  const key = `${mode}\0${attempt}\0${code}`;
  useEffect(() => {
    if (kind !== 'mermaid') return;
    let current = true;
    drawMermaid(code, mode).then(
      (svg) => current && setDrawn({ key, svg }),
      (err: unknown) => current && setDrawn({ key, error: errorMessage(err), load: err instanceof MermaidLoadError }),
    );
    return () => {
      current = false;
    };
  }, [kind, code, mode, key]);
  if (kind !== 'mermaid') return { svg: code };
  if (drawn?.key !== key) return {};
  return drawn.load ? { error: drawn.error, retry: () => setAttempt((n) => n + 1) } : drawn;
}

// The heights pages last reported, so one the chat draws again doesn't jump.
const heights = new Map<string, number>();

// PageFrame runs a page in a frame sandboxed without allow-same-origin: an
// opaque origin, away from the app, its storage and the daemon, under the
// policy main/renders.ts serves it with. The page (lib/rich.ts framedPage)
// takes the app's theme from its URL's fragment and then from messages, so
// a switch between light and dark doesn't load it again; tells its height,
// which the frame takes up to FRAME_VIEWPORT of the window; hands the chat
// the http(s) links clicked in it, opened in the browser only while the
// frame has the focus and the user's click; and reports what went wrong.
function PageFrame({ html, network, large, zoom = 1, onProblem }: { html: string; network: boolean; large?: boolean; zoom?: number; onProblem?: (problem: PageProblem) => void }) {
  const t = useT();
  const theme = usePageTheme();
  const doc = useMemo(() => framedPage(html), [html]);
  const frame = useRef<HTMLIFrameElement>(null);
  const [src, setSrc] = useState<{ doc: string; network: boolean; url: string | null; theme: PageTheme } | null>(null);
  const [height, setHeight] = useState(() => heights.get(doc) ?? 160);
  const [loaded, setLoaded] = useState(false);
  const latest = useRef({ theme, zoom, onProblem });
  latest.current = { theme, zoom, onProblem };
  useEffect(() => {
    let current = true;
    setLoaded(false);
    window.agentbox.renderUrl(doc, network).then(
      (url) => current && setSrc({ doc, network, url, theme: latest.current.theme }),
      () => current && setSrc({ doc, network, url: null, theme: latest.current.theme }),
    );
    return () => {
      current = false;
    };
  }, [doc, network]);
  const tell = () => {
    const page = frame.current?.contentWindow;
    page?.postMessage({ [THEME_MESSAGE]: latest.current.theme }, '*');
    if (large) page?.postMessage({ [ZOOM_MESSAGE]: latest.current.zoom }, '*');
  };
  useEffect(tell, [theme, zoom, large]);
  useEffect(() => {
    const onMessage = (event: MessageEvent) => {
      if (!frame.current || event.source !== frame.current.contentWindow) return;
      const data = (event.data && typeof event.data === 'object' ? event.data : {}) as Record<string, unknown>;
      if (HEIGHT_MESSAGE in data) {
        setLoaded(true);
        const h = frameHeight(data[HEIGHT_MESSAGE], Math.round(window.innerHeight * FRAME_VIEWPORT));
        if (large || h == null) return;
        if (heights.size > 200) heights.clear();
        heights.set(doc, h);
        setHeight(h);
      } else if (LINK_MESSAGE in data) {
        // A page can post any link at any time: only one the user just
        // clicked in it is opened.
        const url = openableLink(data[LINK_MESSAGE]);
        if (url && document.activeElement === frame.current && navigator.userActivation?.isActive !== false) void window.agentbox.openExternal(url);
      } else if (BLOCKED_MESSAGE in data) {
        const host = blockedHost(data[BLOCKED_MESSAGE]);
        if (host) latest.current.onProblem?.({ kind: 'blocked', host });
      } else if (ERROR_MESSAGE in data && typeof data[ERROR_MESSAGE] === 'string') {
        latest.current.onProblem?.({ kind: 'error', message: data[ERROR_MESSAGE] });
      }
    };
    window.addEventListener('message', onMessage);
    return () => window.removeEventListener('message', onMessage);
  }, [doc, large]);
  const ready = src?.doc === doc && src.network === network;
  if (!ready) return <div className={cn('skeleton rounded-none', large ? 'h-full' : '')} style={large ? undefined : { height }} role="status" aria-label={t('chat.rich.loading')} />;
  return (
    <div className={cn('relative', large && 'h-full')} style={large ? undefined : { height }}>
      <iframe
        ref={frame}
        title={titleOf('html', html, t('chat.rich.html'))}
        // The phone has no render host (web/bridge.ts): there the page is the
        // frame's srcdoc, under the app's own policy, so none of its scripts
        // run, and its theme is written into it.
        src={src.url ? src.url + themeFragment(src.theme) : undefined}
        srcDoc={src.url ? undefined : framedPage(html, theme)}
        sandbox="allow-scripts"
        referrerPolicy="no-referrer"
        className="block h-full w-full border-0"
        onLoad={() => {
          tell();
          // A page whose scripts don't run never tells its height.
          if (!src.url) setLoaded(true);
        }}
      />
      {!loaded && <div className="skeleton pointer-events-none absolute inset-0 rounded-none" role="status" aria-label={t('chat.rich.loading')} />}
    </div>
  );
}

// The zoom a page opened large may take, in steps.
const ZOOMS = [0.5, 0.67, 0.8, 0.9, 1, 1.1, 1.25, 1.5, 1.75, 2];

// ZoomControls zoom a page opened large: − 100% +, the middle back to 100%.
function ZoomControls({ zoom, onZoom }: { zoom: number; onZoom: (zoom: number) => void }) {
  const t = useT();
  const at = ZOOMS.indexOf(zoom);
  return (
    <div className="flex shrink-0 items-center gap-0.5" data-rich-zoom>
      <Button size="icon-sm" variant="ghost" aria-label={t('chat.rich.zoomOut')} disabled={at <= 0} onClick={() => onZoom(ZOOMS[at - 1])}>
        <Minus />
      </Button>
      <Tip label={t('chat.rich.zoomReset')}>
        <Button size="sm" variant="ghost" className="min-w-14 tabular-nums" aria-label={t('chat.rich.zoomReset')} onClick={() => onZoom(1)}>
          {Math.round(zoom * 100)}%
        </Button>
      </Tip>
      <Button size="icon-sm" variant="ghost" aria-label={t('chat.rich.zoomIn')} disabled={at >= ZOOMS.length - 1} onClick={() => onZoom(ZOOMS[at + 1])}>
        <Plus />
      </Button>
    </div>
  );
}

// RichStage is a block large, in the dialog or the panel: a page at its zoom,
// a picture as ZoomableImage zooms it.
function RichStage({ kind, code, network, zoom, onProblem }: { kind: RichKind; code: string; network: boolean; zoom: number; onProblem?: (problem: PageProblem) => void }) {
  return (
    <div className={cn('relative min-h-0 flex-1', kind === 'html' ? 'overflow-hidden' : 'overflow-auto')}>
      <RichView kind={kind} code={code} network={network} zoom={zoom} onProblem={onProblem} large />
    </div>
  );
}

// RichDialog is a block opened large, with what it takes to publish it on
// Hatch as a page of its own.
function RichDialog({ kind, code, network, onClose }: { kind: RichKind; code: string; network: boolean; onClose: () => void }) {
  const t = useT();
  const [zoom, setZoom] = useState(1);
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="flex h-[86vh] w-[calc(100vw-3rem)] max-w-[1180px] flex-col gap-0 overflow-hidden p-0" data-rich-dialog={kind}>
        <div className="flex flex-wrap items-center gap-3 border-b border-line px-5 py-3 pr-14">
          <DialogTitle className="mr-auto text-sm font-semibold text-primary">{t(`chat.rich.${kind}`)}</DialogTitle>
          {kind === 'html' && <ZoomControls zoom={zoom} onZoom={setZoom} />}
          <Publish kind={kind} code={code} />
        </div>
        <RichStage kind={kind} code={code} network={network} zoom={zoom} />
      </DialogContent>
    </Dialog>
  );
}

// RichBeside is a block opened beside its chat, in the chat's panel
// (lib/rightPanel.ts openPreview): the panel's header names it, and this is
// its body, a bar of its zoom, network and Hatch over it.
export function RichBeside({ chat, preview }: { chat: string; preview: RichPreview }) {
  const t = useT();
  const { kind, code } = preview;
  const [zoom, setZoom] = useState(1);
  const [network, setNetwork] = useState(preview.network || networked.has(code));
  const [problem, setProblem] = useState<PageProblem | null>(null);
  const toggleNetwork = () => {
    if (network) networked.delete(code);
    else networked.add(code);
    setNetwork(!network);
    setProblem(null);
  };
  return (
    <ChatRefContext value={chat}>
      <div className="flex min-h-0 flex-1 flex-col" data-rich-beside-view={kind}>
        <div className="flex min-h-10 shrink-0 flex-wrap items-center gap-2 border-b border-line-faint px-2 py-1">
          {kind === 'html' && <ZoomControls zoom={zoom} onZoom={setZoom} />}
          {kind === 'html' && (
            <Tip label={t(network ? 'chat.rich.blockNetwork' : 'chat.rich.allowNetwork')}>
              <Button size="sm" variant="ghost" className={network ? 'text-amber-400' : 'text-subtle'} aria-label={t(network ? 'chat.rich.blockNetwork' : 'chat.rich.allowNetwork')} onClick={toggleNetwork}>
                {network ? <Globe className="size-3.5" /> : <GlobeLock className="size-3.5" />}
                {t(network ? 'chat.rich.online' : 'chat.rich.offline')}
              </Button>
            </Tip>
          )}
          <span className="min-w-0 flex-1" />
          <Publish kind={kind} code={code} />
        </div>
        {problem && <ProblemBar problem={problem} network={network} onNetwork={toggleNetwork} />}
        <RichStage kind={kind} code={code} network={network} zoom={zoom} onProblem={(p) => setProblem((had) => worse(had, p))} />
      </div>
    </ChatRefContext>
  );
}

// Publish publishes a block on Hatch through the project's connector
// (api.publishRender): a diagram drawn light, as a page holding its SVG.
function Publish({ kind, code }: { kind: RichKind; code: string }) {
  const t = useT();
  const queryClient = useQueryClient();
  const chatRef = use(ChatRefContext);
  const project = chatRef.split('/')[0];
  const enabled = !!project && !isHomeChat(chatRef);
  const pages = useQuery({ ...pagesQuery(project), enabled });
  const [title, setTitle] = useState(() => titleOf(kind, code, t(`chat.rich.${kind}`)));
  const [busy, setBusy] = useState(false);
  const [url, setUrl] = useState('');
  if (!enabled) return null;
  if (!pages.data?.connector) {
    return <span className="text-[12px] text-faint">{t('chat.rich.noHatch')}</span>;
  }
  if (url) {
    return (
      <Button size="sm" variant="secondary" onClick={() => void window.agentbox.openExternal(url)}>
        <ExternalLink className="size-3.5" />
        {t('chat.rich.openPublished')}
      </Button>
    );
  }
  const publish = async () => {
    setBusy(true);
    try {
      const name = title.trim() || t(`chat.rich.${kind}`);
      const svg = kind === 'mermaid' ? await drawMermaid(code, 'light') : undefined;
      const res = await api.publishRender(chatRef, { title: name, html: pageOf(kind, code, name, svg) });
      setUrl(res.url);
      toast.success(t('chat.rich.published'));
      void queryClient.invalidateQueries({ queryKey: pagesKey(project) });
    } catch (err) {
      toast.error(t('chat.rich.publishFailed', { error: errorMessage(err) }));
    } finally {
      setBusy(false);
    }
  };
  return (
    <form
      className="flex min-w-0 items-center gap-2"
      onSubmit={(event) => {
        event.preventDefault();
        if (!busy) void publish();
      }}
    >
      <Input aria-label={t('chat.rich.title')} placeholder={t('chat.rich.title')} value={title} maxLength={200} onChange={(e) => setTitle(e.target.value)} className="h-8 w-64 min-w-0 text-[13px]" />
      <Button size="sm" variant="primary" type="submit" disabled={busy}>
        {busy && <LoaderCircle className="size-3.5 animate-spin" />}
        {t(busy ? 'chat.rich.publishing' : 'chat.rich.publish')}
      </Button>
    </form>
  );
}
