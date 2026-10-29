import { ChevronLeft, ChevronRight, ZoomIn, ZoomOut } from 'lucide-react';
import type { PDFDocumentProxy, RenderTask } from 'pdfjs-dist';
import workerUrl from 'pdfjs-dist/build/pdf.worker.min.mjs?url';
import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react';
import { useT } from '../../lib/i18n';
import { cn, errorMessage } from '../../lib/utils';
import { BarButton } from './DataGrid';

// The zoom steps, as a share of the page's own size; "fit" is the panel's
// width.
const zooms = [0.5, 0.67, 0.8, 1, 1.25, 1.5, 2, 3, 4];

// PdfView shows a PDF a page at a time with pdf.js, loaded only when a PDF is
// opened: back and forward, a page number to go to, and zoom from the width of
// the panel. data is the file, base64; fallback shows what pdf.js can't read.
export function PdfView({ data, name, fallback }: { data: string; name: string; fallback: (error: string) => ReactNode }) {
  const t = useT();
  const [doc, setDoc] = useState<PDFDocumentProxy | null>(null);
  const [error, setError] = useState('');
  const [page, setPage] = useState(1);
  const [zoom, setZoom] = useState<number | 'fit'>('fit');
  const [fitScale, setFitScale] = useState(1);
  const [width, setWidth] = useState(0);
  const scroller = useRef<HTMLDivElement>(null);
  const canvas = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    setDoc(null);
    setError('');
    setPage(1);
    setZoom('fit');
    let current = true;
    let destroy: (() => Promise<void>) | undefined;
    (async () => {
      const pdfjs = await import('pdfjs-dist');
      pdfjs.GlobalWorkerOptions.workerSrc = workerUrl;
      const bytes = Uint8Array.from(atob(data), (c) => c.charCodeAt(0));
      const task = pdfjs.getDocument({ data: bytes });
      destroy = () => task.destroy();
      const loaded = await task.promise;
      if (current) setDoc(loaded);
    })().catch((err: unknown) => current && setError(errorMessage(err)));
    return () => {
      current = false;
      void destroy?.();
    };
  }, [data]);

  useLayoutEffect(() => {
    const el = scroller.current;
    if (!el) return;
    const observer = new ResizeObserver(() => setWidth(el.clientWidth));
    observer.observe(el);
    setWidth(el.clientWidth);
    return () => observer.disconnect();
  }, [doc]);

  useEffect(() => {
    if (!doc || !width || !canvas.current) return;
    let current = true;
    let task: RenderTask | undefined;
    (async () => {
      const p = await doc.getPage(page);
      const natural = p.getViewport({ scale: 1 });
      const fit = Math.max(0.1, (width - 32) / natural.width);
      if (!current) return;
      setFitScale(fit);
      const viewport = p.getViewport({ scale: zoom === 'fit' ? fit : zoom });
      const el = canvas.current;
      if (!el) return;
      const ratio = window.devicePixelRatio || 1;
      el.width = Math.floor(viewport.width * ratio);
      el.height = Math.floor(viewport.height * ratio);
      el.style.width = `${Math.floor(viewport.width)}px`;
      el.style.height = `${Math.floor(viewport.height)}px`;
      task = p.render({ canvas: el, viewport, transform: ratio === 1 ? undefined : [ratio, 0, 0, ratio, 0, 0] });
      await task.promise;
    })().catch((err: unknown) => {
      if (current && (err as { name?: string })?.name !== 'RenderingCancelledException') setError(errorMessage(err));
    });
    return () => {
      current = false;
      task?.cancel();
    };
  }, [doc, page, zoom, width]);

  // A new page starts at its top.
  useEffect(() => {
    if (scroller.current) scroller.current.scrollTop = 0;
  }, [page]);

  if (error) return fallback(error);
  const pages = doc?.numPages ?? 0;
  const scale = zoom === 'fit' ? fitScale : zoom;
  const go = (n: number) => setPage(Math.min(Math.max(1, n), pages || 1));
  const zoomBy = (step: 1 | -1) => {
    const next = step > 0 ? zooms.find((z) => z > scale + 0.001) : [...zooms].reverse().find((z) => z < scale - 0.001);
    if (next) setZoom(next);
  };

  return (
    <div
      className="flex h-full flex-col outline-none"
      tabIndex={0}
      data-file-pdf
      onKeyDown={(e) => {
        if (e.target instanceof HTMLInputElement) return;
        if (e.key === 'ArrowRight' || e.key === 'PageDown') go(page + 1);
        else if (e.key === 'ArrowLeft' || e.key === 'PageUp') go(page - 1);
        else if (e.key === '+' || e.key === '=') zoomBy(1);
        else if (e.key === '-') zoomBy(-1);
        else return;
        e.preventDefault();
      }}
    >
      <div className="flex h-8 shrink-0 items-center gap-1 border-b border-line-faint px-2 text-[12px] text-subtle">
        <BarButton label={t('files.pdf.previous')} disabled={page <= 1} onClick={() => go(page - 1)}>
          <ChevronLeft />
        </BarButton>
        <PageInput page={page} pages={pages} onGo={go} label={t('files.pdf.page')} />
        <span className="tabular-nums">{t('files.pdf.of', { pages: pages || '…' })}</span>
        <BarButton label={t('files.pdf.next')} disabled={page >= pages} onClick={() => go(page + 1)}>
          <ChevronRight />
        </BarButton>
        <span className="flex-1" />
        <BarButton label={t('files.pdf.zoomOut')} disabled={scale <= zooms[0] + 0.001} onClick={() => zoomBy(-1)}>
          <ZoomOut />
        </BarButton>
        <button
          className={cn('min-w-12 rounded-md px-1.5 py-0.5 tabular-nums hover:bg-surface-raised hover:text-primary', zoom === 'fit' && 'text-secondary')}
          title={t('files.pdf.fit')}
          onClick={() => setZoom('fit')}
          data-pdf-zoom
        >
          {zoom === 'fit' ? t('files.pdf.fitShort') : `${Math.round(scale * 100)}%`}
        </button>
        <BarButton label={t('files.pdf.zoomIn')} disabled={scale >= zooms[zooms.length - 1] - 0.001} onClick={() => zoomBy(1)}>
          <ZoomIn />
        </BarButton>
      </div>
      <div ref={scroller} className="min-h-0 flex-1 overflow-auto bg-sunken p-4">
        {!doc && <div className="skeleton mx-auto aspect-[1/1.414] w-full max-w-md rounded" />}
        <canvas ref={canvas} className={cn('mx-auto block bg-white shadow-lg ring-1 ring-line-strong', !doc && 'hidden')} aria-label={t('files.pdf.pageOf', { name, page })} role="img" />
      </div>
    </div>
  );
}


// PageInput is the page shown, which takes a number to go to.
function PageInput({ page, pages, onGo, label }: { page: number; pages: number; onGo: (n: number) => void; label: string }) {
  const [text, setText] = useState(String(page));
  useEffect(() => setText(String(page)), [page]);
  return (
    <input
      className="w-10 rounded-md border border-line bg-transparent px-1 py-0.5 text-center tabular-nums text-secondary outline-none focus:border-line-vivid"
      value={text}
      inputMode="numeric"
      aria-label={label}
      disabled={!pages}
      onChange={(e) => setText(e.target.value.replace(/\D/g, ''))}
      onBlur={() => setText(String(page))}
      onKeyDown={(e) => {
        if (e.key !== 'Enter') return;
        const n = Number(text);
        if (n) onGo(n);
        setText(String(Math.min(Math.max(1, n || page), pages)));
      }}
    />
  );
}
