import { useMutation, useQuery } from '@tanstack/react-query';
import {
  ArrowUp,
  Brain,
  Check,
  ChevronDown,
  CircleAlert,
  Ellipsis,
  File,
  Gauge,
  Hourglass,
  ImagePlus,
  ListTodo,
  LoaderCircle,
  Lock,
  LockOpen,
  PencilLine,
  PencilRuler,
  Search,
  ShieldAlert,
  Sparkles,
  type LucideIcon,
} from 'lucide-react';
import { useEffect, useLayoutEffect, useMemo, useRef, useState, type ClipboardEvent, type DragEvent, type KeyboardEvent, type ReactNode, type SyntheticEvent } from 'react';
import { toast } from 'sonner';
import type * as T from '../../../shared/api';
import { api, isProjectChat } from '../../lib/api';
import { contextHint, currentPlan, formatTokens, pendingPermissions, toolOf } from '../../lib/chat';
import { choiceName, groupChoices, isRecommended, matchesQuery, searchThreshold, unavailableValue } from '../../lib/modelChoices';
import { mentionAt, matchFiles, type MentionItem } from '../../lib/mentions';
import { getDraft, setDraft } from '../../lib/drafts';
import { imageFiles, imageTypes, maxImages, prepareImage, previewUrl, type PendingImage } from '../../lib/chatImages';
import { ModelByName } from '../ModelByName';
import { cn, errorMessage } from '../../lib/utils';
import { AIIcon, aiLabel } from '../state';
import { Menu, MenuContent, MenuItem, MenuLabel, MenuSeparator, MenuTrigger } from '../ui/menu';
import { Tip } from '../ui/tooltip';
import { DiffView, relativePath } from './ChangedFiles';
import { ImageThumb } from './Images';

export function Composer({ agent, thread, disabled, onSent }: { agent: T.Agent; thread?: T.ChatThread; disabled: boolean; onSent: () => void }) {
  const [text, setText] = useState(() => getDraft(agent.ref));
  const [dismissed, setDismissed] = useState<string | null>(null);
  const [highlighted, setHighlighted] = useState(0);
  const [cursor, setCursor] = useState(0);
  const [mentionDismissed, setMentionDismissed] = useState<string | null>(null);
  const [mentionHighlighted, setMentionHighlighted] = useState(0);
  const [images, setImages] = useState<PendingImage[]>([]);
  const [dragging, setDragging] = useState(false);
  const area = useRef<HTMLTextAreaElement>(null);
  const picker = useRef<HTMLInputElement>(null);
  const pendingCursor = useRef<number | null>(null);
  const session = thread?.session;
  const busy = !!session?.turnStartedAt;
  const requests = thread ? pendingPermissions(thread) : [];
  const plan = thread ? currentPlan(thread) : undefined;
  const tool = aiLabel(agent.ai);

  // A project chat idle long enough for its prompt cache to be about to
  // expire asks before the next message re-sends it all. A message written
  // while it asks is held here until you choose; a turn running, or the card
  // going away without a choice, puts it back in the composer.
  const projectChat = isProjectChat(agent.ref);
  const cache = useQuery({ queryKey: ['chatCache', agent.project], queryFn: () => api.chatCache(agent.project), enabled: projectChat });
  const cacheCard = projectChat && cache.data?.due && !busy && !disabled ? cache.data : undefined;
  const [held, setHeld] = useState<{ text: string; images: PendingImage[] } | null>(null);
  useEffect(() => {
    if (!held || cacheCard) return;
    setText((current) => current || held.text);
    setImages((current) => (current.length ? current : held.images));
    setHeld(null);
  }, [held, cacheCard]);

  const send = useMutation({
    mutationFn: (message: { text: string; images: PendingImage[] }) =>
      api.sendChat(
        agent.ref,
        message.text,
        message.images.map(({ mimeType, data, name }) => ({ mimeType, data, name })),
      ),
    onMutate: () => {
      setText('');
      setImages([]);
      onSent();
    },
    onError: (err, message) => {
      setText((current) => current || message.text);
      setImages((current) => (current.length ? current : message.images));
      toast.error(errorMessage(err));
    },
  });

  // Images go to the AI tool as ACP image blocks, which only an adapter that
  // said promptCapabilities.image takes. Before any adapter of this tool has
  // started there's nothing to go on, and attaching is allowed.
  const noImages = disabled ? `Start ${agent.name} to chat` : session?.noImages ? `${tool} can't read images here: its ACP adapter doesn't take them` : undefined;
  const attach = async (files: File[]) => {
    if (noImages) {
      toast.error(noImages);
      return;
    }
    const room = maxImages - images.length;
    if (files.length > room) toast.error(`A message takes at most ${maxImages} images.`);
    for (const file of files.slice(0, Math.max(0, room))) {
      try {
        const image = await prepareImage(file);
        setImages((current) => (current.length < maxImages ? [...current, image] : current));
      } catch (err) {
        toast.error(errorMessage(err));
      }
    }
  };
  const onPaste = (event: ClipboardEvent<HTMLTextAreaElement>) => {
    const files = imageFiles(event.clipboardData);
    if (files.length === 0) return;
    event.preventDefault();
    void attach(files);
  };
  const dragsFiles = (event: DragEvent) => !disabled && Array.from(event.dataTransfer.types).includes('Files');
  const onDragOver = (event: DragEvent<HTMLDivElement>) => {
    if (!dragsFiles(event)) return;
    event.preventDefault();
    event.dataTransfer.dropEffect = noImages ? 'none' : 'copy';
    setDragging(true);
  };
  const onDrop = (event: DragEvent<HTMLDivElement>) => {
    setDragging(false);
    if (!dragsFiles(event)) return;
    event.preventDefault();
    const files = imageFiles(event.dataTransfer);
    if (files.length === 0) toast.error('Only images can be attached.');
    else void attach(files);
  };
  const stop = useMutation({ mutationFn: () => api.cancelChat(agent.ref), onError: (err) => toast.error(errorMessage(err)) });
  const retry = useMutation({ mutationFn: () => api.startChat(agent.ref), onError: (err) => toast.error(errorMessage(err)) });

  useEffect(() => {
    setDraft(agent.ref, text);
  }, [agent.ref, text]);

  useLayoutEffect(() => {
    const el = area.current;
    if (!el) return;
    el.style.height = '0px';
    el.style.height = `${Math.min(el.scrollHeight, 240)}px`;
    if (pendingCursor.current !== null) {
      el.setSelectionRange(pendingCursor.current, pendingCursor.current);
      setCursor(pendingCursor.current);
      pendingCursor.current = null;
    }
  }, [text]);

  // Typing "/" lists the tool's commands.
  const query = /^\/(\S*)$/.exec(text)?.[1];
  const commands = useMemo(
    () => (query === undefined || query === dismissed ? [] : (session?.commands ?? []).filter((c) => c.name.toLowerCase().includes(query.toLowerCase())).slice(0, 8)),
    [query, dismissed, session?.commands],
  );
  useEffect(() => setHighlighted(0), [query]);

  // Typing "@" anywhere lists the worktree's files. mentionKey names the
  // token the cursor sits in, and stays the same while the popup it opened is
  // simply navigated, so an Escape lookup (mentionDismissed) survives that.
  const mention = mentionAt(text, cursor);
  const mentionKey = mention && `${mention.start}:${mention.query}`;
  const filesQuery = useQuery({ queryKey: ['files', agent.ref], queryFn: () => api.files(agent.ref), enabled: mention !== undefined, staleTime: 4000 });
  const mentionItems = useMemo(
    () => (mention === undefined || mentionKey === mentionDismissed ? [] : matchFiles(filesQuery.data?.files ?? [], mention.query)),
    [mentionKey, mentionDismissed, filesQuery.data],
  );
  useEffect(() => setMentionHighlighted(0), [mentionKey]);

  // Sending while the tool works is the point of the placeholder below: the
  // message joins the running turn and the model decides what it's worth.
  const canSend = !disabled && (text.trim() !== '' || images.length > 0) && !send.isPending && !held;
  const submit = () => {
    if (!canSend) return;
    if (cacheCard) {
      setHeld({ text: text.trim(), images });
      setText('');
      setImages([]);
      return;
    }
    send.mutate({ text: text.trim(), images });
  };
  const pick = (command: T.ChatCommand) => {
    setText(`/${command.name} `);
    area.current?.focus();
  };
  const pickMention = (item: MentionItem) => {
    if (!mention) return;
    const insert = `@${item.insert} `;
    pendingCursor.current = mention.start + insert.length;
    setText(text.slice(0, mention.start) + insert + text.slice(cursor));
    area.current?.focus();
  };
  const syncCursor = (event: SyntheticEvent<HTMLTextAreaElement>) => setCursor(event.currentTarget.selectionStart);

  const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.nativeEvent.isComposing) return;
    if (commands.length > 0) {
      if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
        event.preventDefault();
        setHighlighted((i) => (i + (event.key === 'ArrowDown' ? 1 : commands.length - 1)) % commands.length);
        return;
      }
      if (event.key === 'Tab' || (event.key === 'Enter' && !event.shiftKey)) {
        event.preventDefault();
        pick(commands[highlighted]);
        return;
      }
      if (event.key === 'Escape') {
        setDismissed(query ?? null);
        return;
      }
    }
    if (mentionItems.length > 0) {
      if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
        event.preventDefault();
        setMentionHighlighted((i) => (i + (event.key === 'ArrowDown' ? 1 : mentionItems.length - 1)) % mentionItems.length);
        return;
      }
      if (event.key === 'Tab' || (event.key === 'Enter' && !event.shiftKey)) {
        event.preventDefault();
        pickMention(mentionItems[mentionHighlighted]);
        return;
      }
      if (event.key === 'Escape') {
        setMentionDismissed(mentionKey ?? null);
        return;
      }
    }
    if (event.key === 'Enter' && !event.shiftKey) {
      event.preventDefault();
      submit();
    }
  };

  const placeholder = disabled
    ? `Start ${agent.name} to chat`
    : held
      ? 'Your message is waiting: choose above how to send it'
      : requests.length > 0
      ? 'Answer the request above to go on'
      : busy
        ? `${tool} is working. Write your next message…`
        : 'Ask for changes, or send a follow-up';

  return (
    <div className="relative" data-chat-composer>
      {plan && <TasksBadge plan={plan} />}
      {requests.length > 0 && thread ? (
        <PermissionBanner agent={agent} thread={thread} request={requests[0]} count={requests.length} />
      ) : cacheCard ? (
        <CacheCard
          project={agent.project}
          cache={cacheCard}
          held={held}
          draft={() => {
            // With nothing held, what is in the composer is the message. It
            // leaves both while the choice is made, and comes back if it fails.
            if (held) {
              setHeld(null);
              return held;
            }
            if (text.trim() === '' && images.length === 0) return null;
            const draft = { text: text.trim(), images };
            setText('');
            setImages([]);
            return draft;
          }}
          onChosen={(message) => {
            if (message) onSent();
          }}
          onFailed={(message) => {
            if (message) setHeld(message);
          }}
        />
      ) : session?.limited && !busy ? (
        <Attached tone="amber">
          <div className="flex items-start gap-2 text-[12.5px]">
            <Hourglass className="mt-0.5 size-3.5 shrink-0 text-amber-300" />
            <p className="min-w-0 flex-1 break-words leading-relaxed text-amber-100/90">{limitMessage(session)}</p>
            {/* With the resume off there is nothing waiting to happen, so the
                banner keeps the button every other failure has. */}
            {!session.resumeAt && (
              <button
                className="h-6 shrink-0 rounded-md px-2 text-[12px] font-medium text-amber-100 transition hover:bg-amber-400/15"
                disabled={retry.isPending}
                onClick={() => retry.mutate()}
              >
                {retry.isPending ? 'Starting…' : 'Start again'}
              </button>
            )}
          </div>
        </Attached>
      ) : session?.state === 'error' && !busy ? (
        <Attached tone="rose">
          <div className="flex items-start gap-2 text-[12.5px]">
            <CircleAlert className="mt-0.5 size-3.5 shrink-0 text-rose-300" />
            <p className="min-w-0 flex-1 break-words leading-relaxed text-rose-100/90">{session.error}</p>
            <button className="h-6 shrink-0 rounded-md px-2 text-[12px] font-medium text-rose-100 transition hover:bg-rose-400/15" disabled={retry.isPending} onClick={() => retry.mutate()}>
              {retry.isPending ? 'Starting…' : 'Start again'}
            </button>
          </div>
        </Attached>
      ) : session?.state === 'starting' && !busy ? (
        <Attached tone="neutral">
          <div className="flex items-center gap-2 text-[12.5px] text-muted">
            <LoaderCircle className="size-3.5 animate-spin" />
            {session.detail || `Starting ${tool}`}
          </div>
        </Attached>
      ) : null}

      <div
        onDragOver={onDragOver}
        onDragLeave={(event) => {
          if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setDragging(false);
        }}
        onDrop={onDrop}
        className={cn(
          'relative z-10 rounded-[22px] border border-line-strong bg-composer/85 shadow-[0_24px_60px_-28px_var(--ab-shadow-deep),inset_0_1px_0_rgb(255_255_255/0.04)] backdrop-blur-xl transition-colors focus-within:border-line-vivid',
          dragging && (noImages ? 'border-rose-400/60' : 'border-brand-400/80 bg-brand-500/5'),
        )}
      >
        {dragging && (
          <div className="pointer-events-none absolute inset-0 z-20 flex items-center justify-center rounded-[22px] text-[12.5px] font-medium text-primary">
            <span className="rounded-full bg-overlay px-3 py-1.5 shadow">{noImages ?? 'Drop images to attach them'}</span>
          </div>
        )}
        {commands.length > 0 && (
          <div className="absolute inset-x-2 bottom-full mb-2 overflow-hidden rounded-2xl border border-line-strong bg-overlay p-1 shadow-[0_24px_60px_-20px_var(--ab-shadow-deep)] backdrop-blur-xl" role="listbox" aria-label="Commands">
            {commands.map((command, i) => (
              <button
                key={command.name}
                role="option"
                aria-selected={i === highlighted}
                className={cn('flex w-full items-baseline gap-2.5 rounded-xl px-3 py-2 text-left', i === highlighted && 'bg-surface-raised')}
                onMouseDown={(event) => {
                  event.preventDefault();
                  pick(command);
                }}
                onMouseEnter={() => setHighlighted(i)}
              >
                <span className="shrink-0 font-mono text-[12.5px] text-primary">/{command.name}</span>
                {command.hint && <span className="shrink-0 font-mono text-[11.5px] text-faint">{command.hint}</span>}
                <span className="min-w-0 truncate text-[12px] text-subtle">{command.description}</span>
              </button>
            ))}
          </div>
        )}
        {mentionItems.length > 0 && (
          <div className="absolute inset-x-2 bottom-full mb-2 overflow-hidden rounded-2xl border border-line-strong bg-overlay p-1 shadow-[0_24px_60px_-20px_var(--ab-shadow-deep)] backdrop-blur-xl" role="listbox" aria-label="Files">
            {mentionItems.map((item, i) => (
              <button
                key={item.label}
                role="option"
                aria-selected={i === mentionHighlighted}
                className={cn('flex w-full items-center gap-2 rounded-xl px-3 py-2 text-left', i === mentionHighlighted && 'bg-surface-raised')}
                onMouseDown={(event) => {
                  event.preventDefault();
                  pickMention(item);
                }}
                onMouseEnter={() => setMentionHighlighted(i)}
              >
                <File className="size-3.5 shrink-0 text-subtle" />
                <span className="min-w-0 truncate font-mono text-[12.5px] text-primary">{item.label}</span>
              </button>
            ))}
          </div>
        )}
        {images.length > 0 && (
          <div className="flex flex-wrap gap-2 px-4 pt-3.5" aria-label="Attached images">
            {images.map((image) => (
              <ImageThumb
                key={image.key}
                src={previewUrl(image)}
                name={image.name}
                className="size-14"
                onRemove={() => setImages((current) => current.filter((other) => other.key !== image.key))}
              />
            ))}
          </div>
        )}
        <div className="px-4 pb-1 pt-3.5">
          <textarea
            ref={area}
            rows={1}
            value={text}
            disabled={disabled}
            placeholder={placeholder}
            aria-label="Message"
            onChange={(event) => {
              setText(event.target.value);
              syncCursor(event);
            }}
            onKeyDown={onKeyDown}
            onPaste={onPaste}
            onKeyUp={syncCursor}
            onClick={syncCursor}
            onSelect={syncCursor}
            className="block max-h-60 min-h-6 w-full resize-none bg-transparent text-sm leading-relaxed text-primary outline-none placeholder:text-subtle disabled:cursor-not-allowed"
          />
        </div>
        <div className="flex items-center gap-1 px-2 pb-2">
          <input
            ref={picker}
            type="file"
            accept={imageTypes.join(',')}
            multiple
            hidden
            onChange={(event) => {
              const files = Array.from(event.target.files ?? []);
              event.target.value = '';
              void attach(files);
            }}
          />
          <Tip label={noImages ?? 'Attach images (or paste or drop them)'}>
            {/* A span, so the reason still shows on a disabled button. */}
            <span className="shrink-0">
              <button
                type="button"
                aria-label="Attach images"
                disabled={!!noImages}
                onClick={() => picker.current?.click()}
                className="flex size-8 items-center justify-center rounded-full text-subtle transition hover:bg-surface-raised hover:text-primary disabled:cursor-not-allowed disabled:opacity-40 disabled:hover:bg-transparent"
              >
                <ImagePlus className="size-4" />
              </button>
            </span>
          </Tip>
          <SessionOptions agent={agent} session={session} />
          <div className="ml-auto flex shrink-0 items-center gap-1.5">
            <ContextMeter session={session} />
            {busy && (
              <Tip label="Stop">
                <button
                  aria-label="Stop the turn"
                  disabled={stop.isPending}
                  onClick={() => stop.mutate()}
                  className="flex size-8 items-center justify-center rounded-full bg-rose-500/90 text-white shadow-[0_8px_20px_-8px_rgb(244_63_94/0.7)] transition hover:scale-105 hover:bg-rose-500 disabled:opacity-60"
                >
                  <span className="size-2.5 rounded-[3px] bg-white" />
                </button>
              </Tip>
            )}
            <Tip label={busy ? `Send to ${tool} while it works` : 'Send'}>
              <button
                aria-label="Send"
                disabled={!canSend}
                onClick={submit}
                className="flex size-8 items-center justify-center rounded-full bg-gradient-to-b from-brand-500 to-indigo-600 text-white shadow-[inset_0_1px_0_rgb(255_255_255/0.2),0_8px_20px_-8px_rgb(99_102_241/0.8)] transition hover:scale-105 hover:brightness-110 disabled:scale-100 disabled:opacity-30 disabled:shadow-none"
              >
                {send.isPending ? <LoaderCircle className="size-3.5 animate-spin" /> : <ArrowUp className="size-4" strokeWidth={2.2} />}
              </button>
            </Tip>
          </div>
        </div>
      </div>
    </div>
  );
}

// limitMessage says what a chat stopped by Claude's usage limit is doing about
// it: the raw refusal is already in the conversation, and what you want here is
// whether you have to come back to it yourself. A reset time AgentBox wasn't
// told isn't guessed at — "waiting" is the honest version of it.
function limitMessage(session: T.ChatSession): string {
  if (!session.resumeAt) return 'Usage limit reached. Send a message once it resets.';
  if (!session.limitedUntil) return 'Usage limit reached, waiting for the limit to reset';
  return `Usage limit reached, resumes at ${new Date(session.resumeAt).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}`;
}

const tones = {
  amber: 'border-amber-300/25 bg-attached-warning/90',
  rose: 'border-rose-400/25 bg-attached-danger/90',
  neutral: 'border-line-strong bg-attached-neutral/90',
};

// Attached is a banner that sits on the composer's top edge.
function Attached({ tone, children }: { tone: keyof typeof tones; children: ReactNode }) {
  return <div className={cn('relative mx-auto -mb-4 w-[calc(100%-2.5rem)] animate-slide-up rounded-t-2xl border border-b-0 px-3.5 pb-6 pt-2.5 backdrop-blur-xl', tones[tone])}>{children}</div>;
}

const actions: Record<string, string> = {
  execute: 'run a command',
  edit: 'edit a file',
  delete: 'delete a file',
  move: 'move a file',
  read: 'read a file',
  fetch: 'fetch a page',
  search: 'search',
};

function optionLabel(option: T.ChatPermissionOption): string {
  switch (option.kind) {
    case 'allow_once':
      return 'Allow';
    case 'reject_once':
      return 'Deny';
    case 'reject_always':
      return 'Always deny';
  }
  // The tool's own words say what "always" covers, like "Yes, allow all edits during this session".
  const words = option.name.replace(/^yes,?\s*/i, '').replace(/^and\s+/i, '');
  return words.length > 3 ? words.charAt(0).toUpperCase() + words.slice(1) : 'Always allow';
}

function PermissionBanner({ agent, thread, request, count }: { agent: T.Agent; thread: T.ChatThread; request: T.ChatItem; count: number }) {
  const permission = request.permission!;
  const tool = toolOf(thread, permission.callId);
  const answer = useMutation({ mutationFn: (option: string) => api.answerChat(agent.ref, request.id, option), onError: (err) => toast.error(errorMessage(err)) });
  const detail = tool?.command || (tool?.paths?.[0] && relativePath(tool.paths[0], agent.worktree));
  const rejects = permission.options.filter((o) => o.kind.startsWith('reject'));
  const allows = permission.options.filter((o) => o.kind.startsWith('allow')).sort((a, b) => (a.kind === 'allow_once' ? 1 : 0) - (b.kind === 'allow_once' ? 1 : 0));
  return (
    <Attached tone="amber">
      <div data-chat-permission={request.id}>
        <div className="flex items-center gap-2 text-[12px]">
          <ShieldAlert className="size-3.5 shrink-0 text-amber-300" />
          <span className="font-medium text-amber-100">
            {aiLabel(agent.ai)} asks to {actions[tool?.kind ?? ''] ?? 'use a tool'}
          </span>
          {count > 1 && <span className="ml-auto text-[10.5px] tabular-nums text-amber-200/60">1 of {count}</span>}
        </div>
        <p className="mt-1 break-words text-[13px] text-primary">{permission.title}</p>
        {detail && !permission.title.includes(detail) && (
          <code className="mt-1 block max-h-20 overflow-auto whitespace-pre-wrap break-all font-mono text-[11.5px] text-muted">{detail}</code>
        )}
        {tool?.diffs?.length ? (
          <div className="mt-2 overflow-hidden rounded-lg border border-line bg-well">
            {tool.diffs.map((diff, i) => (
              <DiffView key={i} diff={diff} className="max-h-40" />
            ))}
          </div>
        ) : null}
        <div className="mt-2.5 flex flex-wrap items-center justify-end gap-1.5">
          {rejects.map((option) => (
            <button
              key={option.id}
              disabled={answer.isPending}
              onClick={() => answer.mutate(option.id)}
              className="h-7 rounded-lg px-2.5 text-[12.5px] text-rose-200/90 transition hover:bg-rose-500/10 disabled:opacity-50"
            >
              {optionLabel(option)}
            </button>
          ))}
          {allows.map((option) => (
            <button
              key={option.id}
              disabled={answer.isPending}
              onClick={() => answer.mutate(option.id)}
              title={option.name}
              className={cn(
                'h-7 rounded-lg px-2.5 text-[12.5px] transition disabled:opacity-50',
                option.kind === 'allow_once' ? 'bg-amber-300 px-3 font-medium text-on-bright hover:bg-amber-200' : 'border border-amber-200/20 text-amber-50 hover:bg-amber-200/10',
              )}
            >
              {optionLabel(option)}
            </button>
          ))}
        </div>
      </div>
    </Attached>
  );
}

// CacheCard asks, once a project chat has sat idle to within a margin of its
// prompt cache's TTL, whether to compact it before the next message re-sends
// the whole context uncached. Compacting runs the same consolidation as the
// chat's Compact action and then sends the held message, if any, in the fresh
// session; sending as it is sends it the way it would have gone anyway.
function CacheCard({
  project,
  cache,
  held,
  draft,
  onChosen,
  onFailed,
}: {
  project: string;
  cache: T.ChatCache;
  held: { text: string; images: PendingImage[] } | null;
  draft: () => { text: string; images: PendingImage[] } | null;
  onChosen: (message: { text: string; images: PendingImage[] } | null) => void;
  onFailed: (message: { text: string; images: PendingImage[] } | null) => void;
}) {
  const now = useNow(1000);
  const choose = useMutation({
    mutationFn: ({ compact, message }: { compact: boolean; message: { text: string; images: PendingImage[] } | null }) =>
      api.chooseChatCache(project, {
        compact,
        text: message?.text,
        images: message?.images.map(({ mimeType, data, name }) => ({ mimeType, data, name })),
      }),
    onSuccess: (_, { message }) => onChosen(message),
    onError: (err, { message }) => {
      onFailed(message);
      toast.error(errorMessage(err));
    },
  });
  const pick = (compact: boolean) => choose.mutate({ compact, message: draft() });
  const idle = cache.idleSince ? now - Date.parse(cache.idleSince) : 0;
  const left = cache.expiresAt ? Date.parse(cache.expiresAt) - now : 0;
  const expired = left <= 0;
  const tokens = `~${formatTokens(cache.contextUsed ?? 0)} tokens`;
  const compacting = choose.isPending && choose.variables?.compact;
  const waiting = held ?? (choose.isPending ? choose.variables?.message : null);
  return (
    <Attached tone="amber">
      <div data-chat-cache={expired ? 'expired' : 'expiring'}>
        <div className="flex items-center gap-2 text-[12px]">
          <Hourglass className="size-3.5 shrink-0 text-amber-300" />
          <span className="font-medium text-amber-100">
            {expired ? 'The prompt cache has expired' : `The prompt cache expires in ${formatSpan(left)}`}
          </span>
          <span className="ml-auto shrink-0 tabular-nums text-[11px] text-amber-200/60">idle for {formatSpan(idle)}</span>
        </div>
        <p className="mt-1 break-words text-[12.5px] leading-relaxed text-amber-100/85">
          {expired
            ? `The next message re-sends ${tokens} of context uncached.`
            : `After that, the next message re-sends ${tokens} of context uncached.`}{' '}
          Compacting summarises the conversation into the project's memory and carries on in a fresh session.
        </p>
        {waiting && (
          <p className="mt-1.5 line-clamp-2 break-words rounded-lg bg-black/10 px-2.5 py-1.5 text-[12.5px] text-primary" data-chat-cache-held>
            {waiting.text || `${waiting.images.length} ${waiting.images.length === 1 ? 'image' : 'images'}`}
          </p>
        )}
        <div className="mt-2.5 flex flex-wrap items-center justify-end gap-1.5">
          <button
            disabled={choose.isPending}
            onClick={() => pick(false)}
            className="h-7 rounded-lg border border-amber-200/20 px-2.5 text-[12.5px] text-amber-50 transition hover:bg-amber-200/10 disabled:opacity-50"
          >
            Send anyway
          </button>
          <button
            disabled={choose.isPending}
            onClick={() => pick(true)}
            className="flex h-7 items-center gap-1.5 rounded-lg bg-amber-300 px-3 text-[12.5px] font-medium text-on-bright transition hover:bg-amber-200 disabled:opacity-60"
          >
            {compacting && <LoaderCircle className="size-3.5 animate-spin" />}
            {compacting ? 'Compacting…' : 'Compact and send'}
          </button>
        </div>
      </div>
    </Attached>
  );
}

// useNow is the time, again every interval.
function useNow(interval: number): number {
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), interval);
    return () => clearInterval(id);
  }, [interval]);
  return now;
}

// formatSpan is a duration as "4min 30s", "1h 5min" or "12s".
function formatSpan(ms: number): string {
  const s = Math.max(0, Math.round(Math.abs(ms) / 1000));
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}min ${s % 60}s`;
  return `${Math.floor(m / 60)}h ${m % 60}min`;
}

function TasksBadge({ plan }: { plan: T.ChatPlanEntry[] }) {
  const [open, setOpen] = useState(false);
  const done = plan.filter((e) => e.status === 'completed').length;
  const finished = done === plan.length;
  const current = plan.find((e) => e.status === 'in_progress') ?? plan.find((e) => e.status === 'pending');
  return (
    <Attached tone="neutral">
      <button className="flex w-full items-center gap-2 text-left text-[12px]" aria-expanded={open} onClick={() => setOpen(!open)} data-chat-tasks>
        <ListTodo className="size-3.5 shrink-0 text-muted" />
        <span className="shrink-0 text-subtle">Tasks</span>
        <span className="min-w-0 flex-1 truncate font-medium text-secondary">{finished ? 'All done' : current?.content}</span>
        <span className={cn('shrink-0 tabular-nums', finished ? 'text-emerald-400' : 'text-subtle')}>
          {done}/{plan.length}
        </span>
        <span className="hidden shrink-0 gap-0.5 sm:flex">
          {plan.slice(0, 10).map((entry, i) => (
            <span key={i} className={cn('h-[3px] w-2.5 rounded-full', entry.status === 'completed' ? 'bg-emerald-400' : entry.status === 'in_progress' ? 'bg-brand-400' : 'bg-ghost')} />
          ))}
        </span>
        <ChevronDown className={cn('size-3.5 shrink-0 text-subtle transition-transform', open && 'rotate-180')} />
      </button>
      {open && (
        <ol className="mt-2 grid max-h-56 gap-1 overflow-y-auto">
          {plan.map((entry, i) => (
            <li key={i} className="flex items-start gap-2 text-[12.5px] leading-relaxed">
              <span className={cn('mt-[3px] flex size-3.5 shrink-0 items-center justify-center', entry.status === 'completed' ? 'text-emerald-400' : entry.status === 'in_progress' ? 'text-brand-300' : 'text-faint')}>
                {entry.status === 'completed' ? <Check className="size-3.5" /> : <span className={cn('size-1.5 rounded-full', entry.status === 'in_progress' ? 'bg-brand-300' : 'border border-faint')} />}
              </span>
              <span className={cn(entry.status === 'completed' ? 'text-subtle line-through decoration-ghost' : entry.status === 'in_progress' ? 'text-primary' : 'text-muted')}>{entry.content}</span>
            </li>
          ))}
        </ol>
      )}
    </Attached>
  );
}

const modeIcons: Record<string, LucideIcon> = { plan: PencilRuler, auto_review: Sparkles, full_access: LockOpen };

function modeIcon(option: T.ChatOption): LucideIcon {
  if (option.value === 'acceptEdits') return PencilLine;
  return modeIcons[option.choices.find((c) => c.value === option.value)?.kind ?? ''] ?? Lock;
}

// SessionOptions are the tool's settings: its model, context window, reasoning
// effort and permission mode, and anything else it offers in a menu. The
// context window is AgentBox's own option, not the tool's (D91): the daemon
// only sends it when the model has more than one window to choose from, and
// sends it whether or not a session is running.
function SessionOptions({ agent, session }: { agent: T.Agent; session?: T.ChatSession }) {
  const set = useMutation({
    mutationFn: ({ id, value }: { id: string; value: string }) => api.setChatOption(agent.ref, id, value),
    onError: (err) => toast.error(errorMessage(err)),
  });
  // The last confirmed (turn-settled) context size per model value: a size
  // once confirmed survives a later turn's own placeholder reading, so the
  // badge doesn't blink out every time you send a message on a model it
  // already knows. A model with no confirmed reading yet — just switched to,
  // or session.contextSize was cleared for it (see setOptions in chat.go) —
  // still shows nothing until a turn on it actually finishes.
  const confirmed = useRef<Map<string, number>>(new Map());
  const modelValue = session?.options.find((o) => o.category === 'model' && o.type === 'select')?.value;
  if (modelValue && session?.contextSize && !session.turnStartedAt) confirmed.current.set(modelValue, session.contextSize);
  const options = session?.options ?? [];
  const toolLabel = (
    <span className="flex h-7 items-center gap-1.5 px-2 text-[12.5px] text-subtle">
      <AIIcon ai={agent.ai} className="size-4" />
      {aiLabel(agent.ai)}
    </span>
  );
  if (options.length === 0) return toolLabel;
  const find = (category: string) => options.find((o) => o.category === category && o.type === 'select');
  const model = find('model');
  const contextWindow = find('context_window');
  const effort = find('thought_level');
  const mode = find('mode');
  const others = options.filter((o) => o !== model && o !== contextWindow && o !== effort && o !== mode);
  const pick = (option: T.ChatOption) => (value: string) => value !== option.value && set.mutate({ id: option.id, value });
  const contextSize = model ? confirmed.current.get(model.value) : undefined;
  // Before the session has ever started, the model shown is a stored default
  // like "opus" that the adapter resolves on connect (see wanted() in
  // internal/chat/chat.go), and may not literally be on the menu yet. Flagging
  // it "Unavailable" here, for every freshly made agent, would be a false
  // alarm: that badge is for a choice we know was rejected, not one nobody's
  // tried yet.
  const pending = !session || session.state === 'off';
  return (
    <div className="flex min-w-0 items-center gap-0.5">
      {/* No model menu yet (no Claude Code chat has run here) but a context
          window to choose: name the tool beside it, as with no options at all. */}
      {!model && toolLabel}
      {model && (
        <OptionMenu
          option={model}
          icon={<AIIcon ai={agent.ai} />}
          onPick={pick(model)}
          contextSize={contextSize}
          // With a context window of its own beside it, a "1M" on the model
          // would contradict a chat compacting at 200k.
          showSizes={!contextWindow}
          treatMissingAsUnavailable={!pending}
          // Only Claude Code has the launch-time channel a named model goes
          // through (see PrepareChatModel). Codex and OpenCode keep their own
          // menus: OpenCode's session/new advertises every model its providers
          // can run, and set_config_option takes any of them.
          allowNaming={agent.ai === 'claude'}
        />
      )}
      {contextWindow && <OptionMenu option={contextWindow} icon={<Gauge />} onPick={pick(contextWindow)} />}
      {effort && <OptionMenu option={effort} icon={<Brain />} label={effort.value === 'default' ? 'Effort' : undefined} onPick={pick(effort)} />}
      {mode && <OptionMenu option={mode} icon={(() => { const Icon = modeIcon(mode); return <Icon />; })()} onPick={pick(mode)} />}
      {others.length > 0 && (
        <Menu>
          <MenuTrigger asChild>
            <button
              aria-label="More settings"
              className="flex size-7 items-center justify-center rounded-lg text-subtle transition hover:bg-surface hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-400/40 data-[state=open]:bg-surface-raised"
            >
              <Ellipsis className="size-4" />
            </button>
          </MenuTrigger>
          <MenuContent align="start" side="top" className="w-72">
            {others.map((option, i) => (
              <div key={option.id}>
                {i > 0 && <MenuSeparator />}
                <MenuLabel>{option.name}</MenuLabel>
                {(option.type === 'boolean'
                  ? [
                      { value: 'true', name: 'On' },
                      { value: 'false', name: 'Off' },
                    ]
                  : option.choices
                ).map((choice) => (
                  <MenuItem key={choice.value} onSelect={() => pick(option)(choice.value)} hint={choice.value === option.value ? <Check className="size-3.5 text-brand-300" /> : undefined}>
                    {choice.name}
                  </MenuItem>
                ))}
              </div>
            ))}
          </MenuContent>
        </Menu>
      )}
    </div>
  );
}

// A model's context window, e.g. "1M": read from the tool's own option data
// (see contextHint) or, for the model actually running, the size the adapter
// reported after a turn — never invented. Only the model menu carries one.
function ContextBadge({ hint }: { hint: string }) {
  return <span className="shrink-0 rounded-full bg-surface-raised px-1.5 py-px text-[9.5px] font-semibold tabular-nums text-muted">{hint}</span>;
}

// modelHint is a choice's context-window badge. The adapter doesn't advertise
// a size per model, only for whichever one is actually running (contextSize,
// from usage_update, once a turn has reported one) — so only the current
// choice can use that authoritative number; every other choice falls back to
// whatever Claude Code states in its own text, which is often nothing.
function modelHint(choice: T.ChatOptionChoice, isCurrent: boolean, contextSize?: number): string | undefined {
  if (isCurrent && contextSize) return formatTokens(contextSize).toUpperCase();
  return contextHint(choice);
}

// OptionMenu picks one of a setting's choices. On a phone, only the model keeps its label.
// contextSize is the current model's last confirmed context-window reading (see modelHint and SessionOptions); only the model menu uses it.
function OptionMenu({
  option,
  icon,
  label,
  onPick,
  contextSize,
  showSizes = true,
  treatMissingAsUnavailable = true,
  allowNaming = false,
}: {
  option: T.ChatOption;
  icon: ReactNode;
  label?: string;
  onPick: (value: string) => void;
  contextSize?: number;
  showSizes?: boolean;
  treatMissingAsUnavailable?: boolean;
  allowNaming?: boolean;
}) {
  const [query, setQuery] = useState('');
  const current = option.choices.find((c) => c.value === option.value);
  const isModel = option.category === 'model';
  const sized = isModel && showSizes;
  const currentHint = current && sized ? modelHint(current, true, contextSize) : undefined;
  // A chosen value the tool no longer offers still names itself on the
  // trigger, and gets a row of its own in the menu: showing the tool's
  // default as though you'd picked it is how agents ran for weeks on a model
  // nobody chose. treatMissingAsUnavailable is false for a session that
  // hasn't started: its value may be an unresolved alias, not a rejection.
  const missing = treatMissingAsUnavailable ? unavailableValue(option.choices, option.value) : undefined;
  const groups = groupChoices(option.choices.filter((c) => matchesQuery(c, query)));
  const searchable = option.choices.length >= searchThreshold;
  return (
    <Menu onOpenChange={(open) => !open && setQuery('')}>
      <MenuTrigger asChild>
        <button
          aria-label={currentHint ? `${option.name}: ${current!.name}, ${currentHint} context` : option.name}
          data-chat-option={option.id}
          title={option.description || option.name}
          className={cn(
            'flex h-7 min-w-0 items-center gap-1.5 rounded-lg px-2 text-[12.5px] transition-colors hover:bg-surface hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-400/40 data-[state=open]:bg-surface-raised data-[state=open]:text-primary [&_svg]:size-4 [&_svg]:shrink-0',
            missing ? 'text-amber-300' : 'text-muted',
          )}
        >
          {missing ? <CircleAlert className="text-amber-400" /> : icon}
          <span className={cn('max-w-[9rem] truncate', option.category !== 'model' && 'max-sm:hidden')}>
            {label ?? (current && choiceName(current)) ?? option.value}
          </span>
          {currentHint && <ContextBadge hint={currentHint} />}
          <ChevronDown className="!size-3.5 text-faint" />
        </button>
      </MenuTrigger>
      <MenuContent align="start" side="top" className="max-h-96 w-72 overflow-y-auto">
        <MenuLabel>{option.name}</MenuLabel>
        {searchable && (
          <div className="mb-1 flex items-center gap-2 rounded-lg bg-surface px-2.5 py-1.5">
            <Search className="size-3.5 shrink-0 text-subtle" />
            <input
              autoFocus
              aria-label={`Search ${option.name.toLowerCase()}`}
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              onKeyDown={(e) => e.stopPropagation()}
              placeholder="Search"
              className="w-full bg-transparent text-[13px] text-primary placeholder:text-faint focus:outline-none"
            />
          </div>
        )}
        {missing && (
          <MenuItem disabled hint={<Check className="size-3.5 text-amber-300" />}>
            <span className="grid">
              <span className="flex items-center gap-1.5 text-amber-200">
                {missing}
                <span className="shrink-0 rounded-full bg-amber-400/15 px-1.5 py-px text-[9.5px] font-semibold uppercase tracking-wide text-amber-300">Off the menu</span>
              </span>
              <span className="text-[11px] text-subtle">Not on the menu this session was offered — either a model you named, or one this account has stopped offering.</span>
            </span>
          </MenuItem>
        )}
        {groups.map((group, gi) => (
          <div key={group.name || gi}>
            {group.name && (
              <>
                {(gi > 0 || Boolean(missing)) && <MenuSeparator />}
                <MenuLabel>{group.name}</MenuLabel>
              </>
            )}
            {group.choices.map((choice) => {
              const sizeHint = sized ? modelHint(choice, choice.value === option.value, contextSize) : undefined;
              return (
                <MenuItem key={choice.value} onSelect={() => onPick(choice.value)} hint={choice.value === option.value ? <Check className="size-3.5 text-brand-300" /> : undefined}>
                  <span className="grid">
                    <span className="flex items-center gap-1.5">
                      {choiceName(choice)}
                      {isRecommended(choice) && <RecommendedBadge />}
                      {sizeHint && <ContextBadge hint={sizeHint} />}
                    </span>
                    {choice.description && <span className="text-[11px] text-subtle">{choice.description}</span>}
                  </span>
                </MenuItem>
              );
            })}
          </div>
        ))}
        {groups.length === 0 && <div className="px-2.5 py-3 text-center text-[12px] text-subtle">Nothing matches "{query.trim()}".</div>}
        {allowNaming && isModel && <ModelByName onPick={onPick} />}
      </MenuContent>
    </Menu>
  );
}

// RecommendedBadge marks the choice the tool itself recommends, so its name
// doesn't have to carry "(recommended)" everywhere it appears.
function RecommendedBadge() {
  return <span className="shrink-0 rounded-full bg-brand-400/15 px-1.5 py-px text-[9.5px] font-semibold uppercase tracking-wide text-brand-300">Recommended</span>;
}

// ContextMeter shows how full the context window is, once that starts to matter:
// a sliver of a ring would only look like a spinner.
function ContextMeter({ session }: { session?: T.ChatSession }) {
  if (!session?.contextSize) return null;
  const used = session.contextUsed ?? 0;
  const fraction = Math.min(used / session.contextSize, 1);
  if (fraction < 0.2) return null;
  const r = 7;
  const circumference = 2 * Math.PI * r;
  return (
    <Tip label={`${formatTokens(used)} of ${formatTokens(session.contextSize)} tokens of context used`}>
      <span className="flex size-7 items-center justify-center" aria-label="Context used" data-chat-context>
        <svg viewBox="0 0 18 18" className="size-[18px] -rotate-90">
          <circle cx="9" cy="9" r={r} fill="none" stroke="var(--ab-surface-strong)" strokeWidth="2" />
          <circle
            cx="9"
            cy="9"
            r={r}
            fill="none"
            stroke={fraction > 0.85 ? 'var(--ab-amber-400)' : 'var(--ab-brand-400)'}
            strokeWidth="2"
            strokeLinecap="round"
            strokeDasharray={circumference}
            strokeDashoffset={circumference * (1 - Math.max(fraction, 0.02))}
          />
        </svg>
      </span>
    </Tip>
  );
}
