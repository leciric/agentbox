import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Check, Code2, Copy, ExternalLink, Eye, Globe, GlobeLock, LoaderCircle, Maximize2, TriangleAlert } from 'lucide-react';
import { createContext, use, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { toast } from 'sonner';
import { api, isHomeChat } from '../../lib/api';
import { useT } from '../../lib/i18n';
import { drawMermaid } from '../../lib/mermaid';
import { pagesKey } from '../../lib/pages';
import { framedPage, frameHeight, HEIGHT_MESSAGE, pageOf, PICTURE_MAX, svgSize, svgUrl, titleOf, type RichKind } from '../../lib/rich';
import { useMode } from '../../lib/theme';
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
  const { rich, streaming } = use(RichContext);
  const [showing, setShowing] = useState(drawn);
  if (!rich) return <CodeBlock code={code} language={language} />;
  if (streaming || !showing) {
    return (
      <CodeBlock
        code={code}
        language={language}
        actions={
          !streaming && (
            <HeaderButton label={t(kind === 'html' ? 'chat.rich.preview' : 'chat.rich.draw')} onClick={() => setShowing(true)}>
              <Eye className="size-3.5" />
            </HeaderButton>
          )
        }
      />
    );
  }
  return <DrawnBlock kind={kind} code={code} onCode={() => setShowing(false)} />;
}

function DrawnBlock({ kind, code, onCode }: { kind: RichKind; code: string; onCode: () => void }) {
  const t = useT();
  const [large, setLarge] = useState(false);
  const [network, setNetwork] = useState(() => networked.has(code));
  const [copied, setCopied] = useState(false);
  const toggleNetwork = () => {
    if (network) networked.delete(code);
    else networked.add(code);
    setNetwork(!network);
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
      <RichView kind={kind} code={code} network={network} onOpen={() => setLarge(true)} onCode={onCode} />
      {large && <RichDialog kind={kind} code={code} network={network} onClose={() => setLarge(false)} />}
    </div>
  );
}

function HeaderButton({ label, onClick, children }: { label: string; onClick: () => void; children: ReactNode }) {
  return (
    <Tip label={label}>
      <button aria-label={label} className="flex size-6 items-center justify-center rounded-md text-subtle transition hover:bg-surface-raised hover:text-secondary" onClick={onClick}>
        {children}
      </button>
    </Tip>
  );
}

// RichView is a block drawn: in the chat, fitted to the column and no taller
// than PICTURE_MAX (a page FRAME_MAX), or large, filling the dialog.
function RichView({ kind, code, network, large, onOpen, onCode }: { kind: RichKind; code: string; network: boolean; large?: boolean; onOpen?: () => void; onCode?: () => void }) {
  if (kind === 'html') return <PageFrame html={code} network={network} large={large} />;
  return <Picture kind={kind} code={code} large={large} onOpen={onOpen} onCode={onCode} />;
}

function Picture({ kind, code, large, onOpen, onCode }: { kind: RichKind; code: string; large?: boolean; onOpen?: () => void; onCode?: () => void }) {
  const t = useT();
  const drawn = useSvg(kind, code);
  if (drawn.error) {
    return (
      <div className="flex items-start gap-2 px-3 py-2.5 text-[12px] text-rose-300">
        <TriangleAlert className="mt-0.5 size-3.5 shrink-0" />
        <span className="min-w-0 break-words">
          {t('chat.rich.drawFailed', { error: drawn.error })}{' '}
          {onCode && (
            <button className="underline underline-offset-2 hover:text-rose-200" onClick={onCode}>
              {t('chat.rich.showCode')}
            </button>
          )}
        </span>
      </div>
    );
  }
  if (!drawn.svg) return <div className="skeleton h-32 rounded-none" />;
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
        className="mx-auto block h-auto w-full object-contain"
        // An SVG sized 100% has no height of its own: its viewBox gives the shape.
        style={{ maxWidth: size ? `${size.width}px` : undefined, aspectRatio: size ? `${size.width} / ${size.height}` : undefined, maxHeight: PICTURE_MAX }}
      />
    </button>
  );
}

// useSvg is a picture's SVG: the block's own, or the diagram mermaid drew of
// it in the window's mode.
function useSvg(kind: RichKind, code: string): { svg?: string; error?: string } {
  const mode = useMode();
  const [drawn, setDrawn] = useState<{ key: string; svg?: string; error?: string } | null>(null);
  const key = `${mode}\0${code}`;
  useEffect(() => {
    if (kind !== 'mermaid') return;
    let current = true;
    drawMermaid(code, mode).then(
      (svg) => current && setDrawn({ key, svg }),
      (err: unknown) => current && setDrawn({ key, error: errorMessage(err) }),
    );
    return () => {
      current = false;
    };
  }, [kind, code, mode, key]);
  if (kind !== 'mermaid') return { svg: code };
  return drawn?.key === key ? drawn : {};
}

// The heights pages last reported, so one the chat draws again doesn't jump.
const heights = new Map<string, number>();

// PageFrame runs a page in a frame sandboxed without allow-same-origin: an
// opaque origin, away from the app, its storage and the daemon, under the
// policy main/renders.ts serves it with. The page tells its height
// (lib/rich.ts framedPage), which the frame takes up to FRAME_MAX.
function PageFrame({ html, network, large }: { html: string; network: boolean; large?: boolean }) {
  const t = useT();
  const mode = useMode();
  const doc = useMemo(() => framedPage(html, mode), [html, mode]);
  const frame = useRef<HTMLIFrameElement>(null);
  const [src, setSrc] = useState<{ doc: string; network: boolean; url: string | null } | null>(null);
  const [height, setHeight] = useState(() => heights.get(doc) ?? 160);
  useEffect(() => {
    let current = true;
    window.agentbox.renderUrl(doc, network).then(
      (url) => current && setSrc({ doc, network, url }),
      () => current && setSrc({ doc, network, url: null }),
    );
    return () => {
      current = false;
    };
  }, [doc, network]);
  useEffect(() => {
    if (large) return;
    const onMessage = (event: MessageEvent) => {
      if (event.source !== frame.current?.contentWindow) return;
      const h = frameHeight((event.data as Record<string, unknown> | null)?.[HEIGHT_MESSAGE]);
      if (h == null) return;
      if (heights.size > 200) heights.clear();
      heights.set(doc, h);
      setHeight(h);
    };
    window.addEventListener('message', onMessage);
    return () => window.removeEventListener('message', onMessage);
  }, [doc, large]);
  const ready = src?.doc === doc && src.network === network;
  if (!ready) return <div className={cn('skeleton rounded-none', large ? 'h-full' : '')} style={large ? undefined : { height }} />;
  return (
    <iframe
      ref={frame}
      title={titleOf('html', html, t('chat.rich.html'))}
      // The phone has no render host (web/bridge.ts): there the page is the
      // frame's srcdoc, under the app's own policy, so none of its scripts run.
      src={src.url ?? undefined}
      srcDoc={src.url ? undefined : doc}
      sandbox="allow-scripts"
      referrerPolicy="no-referrer"
      className={cn('block w-full border-0', large && 'h-full')}
      style={large ? undefined : { height }}
    />
  );
}

// RichDialog is a block opened large, with what it takes to publish it on
// Hatch as a page of its own.
function RichDialog({ kind, code, network, onClose }: { kind: RichKind; code: string; network: boolean; onClose: () => void }) {
  const t = useT();
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="flex h-[86vh] w-[calc(100vw-3rem)] max-w-[1180px] flex-col gap-0 overflow-hidden p-0" data-rich-dialog={kind}>
        <div className="flex flex-wrap items-center gap-3 border-b border-line px-5 py-3 pr-14">
          <DialogTitle className="mr-auto text-sm font-semibold text-primary">{t(`chat.rich.${kind}`)}</DialogTitle>
          <Publish kind={kind} code={code} />
        </div>
        <div className={cn('relative min-h-0 flex-1', kind === 'html' ? 'overflow-hidden' : 'overflow-auto')}>
          <RichView kind={kind} code={code} network={network} large />
        </div>
      </DialogContent>
    </Dialog>
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
