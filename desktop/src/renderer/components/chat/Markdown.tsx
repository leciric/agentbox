import { ImageOff } from 'lucide-react';
import { memo, useMemo, useState } from 'react';
import ReactMarkdown, { type Components } from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { remarkHTMLImages } from '../../lib/pulls';
import { richKind } from '../../lib/rich';
import { cn } from '../../lib/utils';
import { CodeBlock } from './CodeBlock';
import { RichBlock, RichContext } from './RichBlock';

// Markdown renders an AI tool's message. While it streams, new blocks fade in.
//
// Given imageSrc, it shows pictures too — the ones written as HTML <img> as
// well, as GitHub's uploader writes them — loading each from where imageSrc
// says, or as a link to it when that is nowhere. Without it, as in a chat, a
// picture is only its alt text.
//
// With rich, as in a chat's messages, ```mermaid, ```svg and ```html preview
// blocks are drawn (RichBlock, lib/rich.ts) once the message has finished.
export const Markdown = memo(function Markdown({
  text,
  streaming,
  className,
  imageSrc,
  rich,
}: {
  text: string;
  streaming?: boolean;
  className?: string;
  imageSrc?: (src: string) => string | undefined;
  rich?: boolean;
}) {
  const withImages = useMemo<Components>(
    () => (imageSrc ? { ...components, img: ({ src, alt, width }) => <MarkdownImage src={typeof src === 'string' ? src : ''} alt={alt} width={width} imageSrc={imageSrc} /> } : components),
    [imageSrc],
  );
  const context = useMemo(() => ({ rich: !!rich, streaming: !!streaming }), [rich, streaming]);
  return (
    <div className={cn('chat-markdown', className)} data-streaming={streaming || undefined}>
      <RichContext value={context}>
        <ReactMarkdown remarkPlugins={imageSrc ? [remarkGfm, remarkHTMLImages] : [remarkGfm]} components={withImages}>
          {text}
        </ReactMarkdown>
      </RichContext>
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
  code: ({ className, children, node }) => {
    const code = String(children ?? '');
    const language = /language-([\w+#.-]+)/.exec(className ?? '')?.[1];
    if (!language && !code.includes('\n')) return <code>{children}</code>;
    // The rest of the info string (```html preview) is the node's meta.
    const rich = richKind(language, (node?.data as { meta?: string } | undefined)?.meta);
    if (rich) return <RichBlock kind={rich.kind} drawn={rich.drawn} code={code.replace(/\n$/, '')} language={language} />;
    return <CodeBlock code={code.replace(/\n$/, '')} language={language} />;
  },
  table: ({ children }) => (
    <div className="chat-markdown-table">
      <table>{children}</table>
    </div>
  ),
};
