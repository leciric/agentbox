import { Check, Copy } from 'lucide-react';
import { useEffect, useState, type ReactNode } from 'react';
import type { ThemedToken } from 'shiki/core';
import { highlight, languageOf } from '../../lib/highlight';
import { useT } from '../../lib/i18n';
import { useMode } from '../../lib/theme';

// CodeBlock is a fenced block's code, highlighted, with a button to copy it
// and whatever actions its caller adds beside that (a rich block's).
export function CodeBlock({ code, language, actions }: { code: string; language?: string; actions?: ReactNode }) {
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
        <span className="min-w-0 truncate font-mono">{language || 'text'}</span>
        <div className="flex shrink-0 items-center gap-0.5">
          {actions}
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
