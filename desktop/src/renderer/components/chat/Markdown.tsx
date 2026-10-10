import { Check, Copy, ImageOff } from 'lucide-react';
import { memo, useEffect, useMemo, useState } from 'react';
import ReactMarkdown, { type Components } from 'react-markdown';
import remarkGfm from 'remark-gfm';
import type { ThemedToken } from 'shiki/core';
import { highlight, languageOf } from '../../lib/highlight';
import { useT } from '../../lib/i18n';
import { remarkHTMLImages } from '../../lib/pulls';
import { useMode } from '../../lib/theme';
import { cn } from '../../lib/utils';

// Markdown renders an AI tool's message. While it streams, new blocks fade in.
//
// Given imageSrc, it shows pictures too — the ones written as HTML <img> as
// well, as GitHub's uploader writes them — loading each from where imageSrc
// says, or as a link to it when that is nowhere. Without it, as in a chat, a
// picture is only its alt text.
export const Markdown = memo(function Markdown({
  text,
  streaming,
  className,
  imageSrc,
}: {
  text: string;
  streaming?: boolean;
  className?: string;
  imageSrc?: (src: string) => string | undefined;
}) {
  const withImages = useMemo<Components>(
    () => (imageSrc ? { ...components, img: ({ src, alt, width }) => <MarkdownImage src={typeof src === 'string' ? src : ''} alt={alt} width={width} imageSrc={imageSrc} /> } : components),
    [imageSrc],
  );
  return (
    <div className={cn('chat-markdown', className)} data-streaming={streaming || undefined}>
      <ReactMarkdown remarkPlugins={imageSrc ? [remarkGfm, remarkHTMLImages] : [remarkGfm]} components={withImages}>
        {text}
      </ReactMarkdown>
    </div>
  );
});

function MarkdownImage({ src, alt, width, imageSrc }: { src: string; alt?: string; width?: number | string; imageSrc: (src: string) => string | undefined }) {
  const [failed, setFailed] = useState(false);
  const url = imageSrc(src);
  if (!url || failed) {
    return (
      <a
        href={src}
        className="inline-flex items-center gap-1.5"
        onClick={(event) => {
          event.preventDefault();
          if (/^https?:/.test(src)) void window.agentbox.openExternal(src);
        }}
      >
        <ImageOff className="size-3.5 shrink-0" />
        {alt || src}
      </a>
    );
  }
  return <img src={url} alt={alt ?? ''} width={width} loading="lazy" className="chat-markdown-image" onError={() => setFailed(true)} />;
}

const components: Components = {
  a: ({ href, children }) => (
    <a
      href={href}
      onClick={(event) => {
        event.preventDefault();
        if (href && /^(https?:|mailto:)/.test(href)) void window.agentbox.openExternal(href);
      }}
    >
      {children}
    </a>
  ),
  // CodeBlock draws its own frame.
  pre: ({ children }) => <>{children}</>,
  code: ({ className, children }) => {
    const code = String(children ?? '');
    const language = /language-([\w+#.-]+)/.exec(className ?? '')?.[1];
    if (!language && !code.includes('\n')) return <code>{children}</code>;
    return <CodeBlock code={code.replace(/\n$/, '')} language={language} />;
  },
  table: ({ children }) => (
    <div className="chat-markdown-table">
      <table>{children}</table>
    </div>
  ),
};

function CodeBlock({ code, language }: { code: string; language?: string }) {
  const t = useT();
  const lang = languageOf(language);
  // Shiki's colours are inline, so a block already on screen has to be
  // highlighted again when the window turns over: there is a palette per mode.
  const mode = useMode();
  const [highlighted, setHighlighted] = useState<{ code: string; mode: string; tokens: ThemedToken[][] } | null>(null);
  const [copied, setCopied] = useState(false);

  // Waits for a pause, so a block that's still streaming isn't highlighted on every chunk.
  useEffect(() => {
    if (!lang) return;
    let current = true;
    const timer = setTimeout(() => {
      highlight(code, lang)
        .then((tokens) => current && setHighlighted({ code, mode, tokens }))
        .catch(() => {});
    }, 150);
    return () => {
      current = false;
      clearTimeout(timer);
    };
  }, [code, lang, mode]);

  const tokens = highlighted?.code === code && highlighted.mode === mode ? highlighted.tokens : null;
  return (
    <div className="chat-codeblock group/code">
      <div className="flex h-8 items-center justify-between border-b border-line-faint pl-3 pr-1 text-[11px] text-subtle">
        <span className="font-mono">{language || 'text'}</span>
        <button
          aria-label={t('chat.markdown.copyCode')}
          className="flex size-6 items-center justify-center rounded-md text-subtle opacity-0 transition hover:bg-surface-raised hover:text-secondary focus-visible:opacity-100 group-hover/code:opacity-100"
          onClick={() => {
            window.agentbox.copyText(code);
            setCopied(true);
            setTimeout(() => setCopied(false), 1200);
          }}
        >
          {copied ? <Check className="size-3.5 text-emerald-400" /> : <Copy className="size-3.5" />}
        </button>
      </div>
      <pre>
        <code>
          {tokens
            ? tokens.map((line, i) => (
                <span key={i}>
                  {line.map((token, j) => (
                    <span key={j} style={{ color: token.color, fontStyle: token.fontStyle === 1 ? 'italic' : undefined }}>
                      {token.content}
                    </span>
                  ))}
                  {i < tokens.length - 1 ? '\n' : null}
                </span>
              ))
            : code}
        </code>
      </pre>
    </div>
  );
}
