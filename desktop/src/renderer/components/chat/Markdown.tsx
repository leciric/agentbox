import { Check, Copy } from 'lucide-react';
import { memo, useEffect, useState } from 'react';
import ReactMarkdown, { type Components } from 'react-markdown';
import remarkGfm from 'remark-gfm';
import type { ThemedToken } from 'shiki/core';
import { highlight, languageOf } from '../../lib/highlight';
import { useMode } from '../../lib/theme';
import { cn } from '../../lib/utils';

// Markdown renders an AI tool's message. While it streams, new blocks fade in.
export const Markdown = memo(function Markdown({ text, streaming, className }: { text: string; streaming?: boolean; className?: string }) {
  return (
    <div className={cn('chat-markdown', className)} data-streaming={streaming || undefined}>
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={components}>
        {text}
      </ReactMarkdown>
    </div>
  );
});

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
          aria-label="Copy the code"
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
