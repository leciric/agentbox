import { ImageOff } from 'lucide-react';
import { memo, use, useMemo, useState, type ReactNode } from 'react';
import { fileLinkOf } from '../../lib/files';
import { FileLinks } from '../files/FilePanel';
import ReactMarkdown, { type Components } from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { remarkHTMLImages } from '../../lib/pulls';
import { heldBack } from '../../lib/plainText';
import { openFence, richKind } from '../../lib/rich';
import { cn } from '../../lib/utils';
import { CodeBlock } from './CodeBlock';
import { GeneratingCard, RichBlock, RichContext } from './RichBlock';

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
  text: whole,
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
  const text = useMemo(() => (streaming ? heldBack(whole) : whole), [streaming, whole]);
  const withImages = useMemo<Components>(
    () => (imageSrc ? { ...components, img: ({ src, alt, width }) => <MarkdownImage src={typeof src === 'string' ? src : ''} alt={alt} width={width} imageSrc={imageSrc} /> } : components),
    [imageSrc],
  );
  const context = useMemo(() => ({ rich: !!rich, streaming: !!streaming }), [rich, streaming]);
  // A renderable block still open is a card while its message streams, and
  // its code if the stream ended without closing it, never a half-drawn page.
  const open = useMemo(() => (rich ? openFence(text) : null), [rich, text]);
  const plugins = imageSrc ? [remarkGfm, remarkHTMLImages] : [remarkGfm];
  if (open) {
    return (
      <div className={cn('chat-markdown', className)} data-streaming={streaming || undefined}>
        <RichContext value={context}>
          {open.before.trim() && (
            <ReactMarkdown remarkPlugins={plugins} components={withImages}>
              {open.before}
            </ReactMarkdown>
          )}
        </RichContext>
        {streaming ? (
          <GeneratingCard kind={open.kind} lines={open.lines} bytes={open.bytes} />
        ) : (
          <RichContext value={{ rich: false, streaming: false }}>
            <ReactMarkdown remarkPlugins={plugins} components={withImages}>
              {text.slice(open.before.length)}
            </ReactMarkdown>
          </RichContext>
        )}
      </div>
    );
  }
  return (
    <div className={cn('chat-markdown', className)} data-streaming={streaming || undefined}>
      <RichContext value={context}>
        <ReactMarkdown remarkPlugins={plugins} components={withImages}>
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

// A link whose target is a file in the chat's worktree opens it beside the
// chat (components/files); any other opens in the browser.
function Link({ href, children }: { href?: string; children: ReactNode }) {
  const files = use(FileLinks);
  const file = files && href && !/^(https?:|mailto:)/.test(href) ? fileLinkOf(decodeURI(href), files.root) : undefined;
  return (
    <a
      href={href}
      data-file-link={file?.path}
      onClick={(event) => {
        event.preventDefault();
        if (file && files) files.open(file.path, file.line);
        else if (href && /^(https?:|mailto:)/.test(href)) void window.agentbox.openExternal(href);
      }}
    >
      {children}
    </a>
  );
}

// InlineCode that reads as a file path (`internal/api/files.go:42`) is a link
// to it, the way T3Code's resolveInlineCodeFileLinkMeta reads it.
function InlineCode({ children }: { children: ReactNode }) {
  const files = use(FileLinks);
  const text = typeof children === 'string' ? children : undefined;
  const file = files && text ? fileLinkOf(text, files.root) : undefined;
  if (!file || !files) return <code>{children}</code>;
  return (
    <code
      role="link"
      tabIndex={0}
      className="chat-file-link"
      title={file.line ? `${file.path}:${file.line}` : file.path}
      data-file-link={file.path}
      onClick={() => files.open(file.path, file.line)}
      onKeyDown={(event) => {
        if (event.key === 'Enter') files.open(file.path, file.line);
      }}
    >
      {children}
    </code>
  );
}

const components: Components = {
  a: ({ href, children }) => <Link href={href}>{children}</Link>,
  // CodeBlock draws its own frame.
  pre: ({ children }) => <>{children}</>,
  code: ({ className, children, node }) => {
    const code = String(children ?? '');
    const language = /language-([\w+#.-]+)/.exec(className ?? '')?.[1];
    if (!language && !code.includes('\n')) return <InlineCode>{children}</InlineCode>;
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
