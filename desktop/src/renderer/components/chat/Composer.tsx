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
  Moon,
  PencilLine,
  PencilRuler,
  Play,
  Plug,
  Search,
  ShieldAlert,
  Sparkles,
  type LucideIcon,
} from 'lucide-react';
import { Fragment, useEffect, useLayoutEffect, useMemo, useRef, useState, type ClipboardEvent, type DragEvent, type KeyboardEvent, type ReactNode, type SyntheticEvent } from 'react';
import { toast } from 'sonner';
import type * as T from '../../../shared/api';
import { api, isHomeChat, isProjectChat } from '../../lib/api';
import { composerSkills, searchSkills, skillMentionAt } from '../../lib/skills';
import { SkillTile } from '../SkillsPanel';
import { contextBadge, contextHint, currentPlan, formatTokens, pendingPermissions, toolOf, toolsReload } from '../../lib/chat';
import { choiceName, groupChoices, isRecommended, matchesQuery, searchThreshold, unavailableValue } from '../../lib/modelChoices';
import { mentionAt, matchFiles, type MentionItem } from '../../lib/mentions';
import { getDraft, setDraft } from '../../lib/drafts';
import { formatTime, t as translate, useT } from '../../lib/i18n';
import { useNow } from '../../lib/useNow';
import { imageFiles, imageTypes, maxImages, prepareImage, previewUrl, type PendingImage } from '../../lib/chatImages';
import { ModelByName } from '../ModelByName';
import { cn, errorMessage } from '../../lib/utils';
import { stop as stopReading } from '../../lib/voice/reader';
import { AIIcon, aiLabel } from '../state';
import { Menu, MenuContent, MenuItem, MenuLabel, MenuSeparator, MenuTrigger } from '../ui/menu';
import { Tip } from '../ui/tooltip';
import { DiffView, relativePath } from './ChangedFiles';
import { ImageThumb } from './Images';
import { VoiceButton } from './VoiceButton';
import { voiceSettings } from '../../lib/voice/settings';

// onPhone is the app on a phone paired with the daemon (web/bridge.ts's lan).
const onPhone = (window.agentbox as { lan?: boolean } | undefined)?.lan === true;

// asleep is set while the agent's machine is stopped or paused: the composer
// says so, with the button that starts it, and a message sent wakes it.
export function Composer({
  agent,
  thread,
  disabled,
  asleep,
  onSent,
}: {
  agent: T.Agent;
  thread?: T.ChatThread;
  disabled: boolean;
  asleep?: { state: 'stopped' | 'paused'; starting: boolean; onStart: () => void };
  onSent: () => void;
}) {
  const t = useT();
  const [text, setText] = useState(() => getDraft(agent.ref));
  const [dismissed, setDismissed] = useState<string | null>(null);
  const [highlighted, setHighlighted] = useState(0);
  const [cursor, setCursor] = useState(0);
  const [mentionDismissed, setMentionDismissed] = useState<string | null>(null);
  const [mentionHighlighted, setMentionHighlighted] = useState(0);
  const [dollarDismissed, setDollarDismissed] = useState<string | null>(null);
  const [dollarHighlighted, setDollarHighlighted] = useState(0);
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
  const noImages = disabled ? t('chat.composer.startToChat', { name: agent.name }) : session?.noImages ? t('chat.composer.noImages', { tool }) : undefined;
  const attach = async (files: File[]) => {
    if (noImages) {
      toast.error(noImages);
      return;
    }
    const room = maxImages - images.length;
    if (files.length > room) toast.error(t('chat.composer.maxImages', { count: maxImages }));
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
    if (files.length === 0) toast.error(t('chat.composer.onlyImages'));
    else void attach(files);
  };
  // Cancelling the turn stops its reply being read aloud too.
  const stop = useMutation({ mutationFn: () => api.cancelChat(agent.ref), onMutate: stopReading, onError: (err) => toast.error(errorMessage(err)) });
  const retry = useMutation({ mutationFn: () => api.startChat(agent.ref), onError: (err) => toast.error(errorMessage(err)) });
  const reloadTools = useMutation({ mutationFn: () => api.reloadChatTools(agent.ref), onError: (err) => toast.error(errorMessage(err)) });

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

  // The skills this chat has (Settings → Skills), which "/" and "$" offer as
  // T3 Code's composer does: a picked skill goes in as $name, and the daemon
  // turns it into what the chat's AI tool runs (internal/chat/skills.go).
  const home = isHomeChat(agent.ref);
  const skillsQuery = useQuery({ queryKey: ['skills', home ? '' : agent.project], queryFn: () => api.skills(home ? undefined : agent.project), staleTime: 15_000 });
  const skills = useMemo(() => composerSkills(skillsQuery.data, !home), [skillsQuery.data, home]);

  // Typing "/" lists the chat's skills, then the tool's commands. Claude
  // Code lists skills among its commands too: those show once, as skills.
  const query = /^\/(\S*)$/.exec(text)?.[1];
  const commands = useMemo((): SlashItem[] => {
    if (query === undefined || query === dismissed) return [];
    const found = searchSkills(skills, query, 6);
    const names = new Set(skills.map((s) => s.name));
    const tools = (session?.commands ?? []).filter((c) => !names.has(c.name) && c.name.toLowerCase().includes(query.toLowerCase())).slice(0, Math.max(3, 9 - found.length));
    return [...found.map((skill) => ({ kind: 'skill' as const, skill })), ...tools.map((command) => ({ kind: 'command' as const, command }))];
  }, [query, dismissed, session?.commands, skills]);
  useEffect(() => setHighlighted(0), [query]);

  // Typing "$" anywhere lists the skills alone, for one in mid-sentence.
  const dollar = query === undefined ? skillMentionAt(text, cursor) : undefined;
  const dollarKey = dollar && `${dollar.start}:${dollar.query}`;
  const dollarItems = useMemo(
    () => (dollar === undefined || dollarKey === dollarDismissed ? [] : searchSkills(skills, dollar.query, 8)),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [dollarKey, dollarDismissed, skills],
  );
  useEffect(() => setDollarHighlighted(0), [dollarKey]);

  // Typing "@" anywhere lists the worktree's files. mentionKey names the
  // token the cursor sits in, and stays the same while the popup it opened is
  // simply navigated, so an Escape lookup (mentionDismissed) survives that.
  const mention = mentionAt(text, cursor);
  const mentionKey = mention && `${mention.start}:${mention.query}`;
  const mentionQuery = mention?.query;
  const filesQuery = useQuery({ queryKey: ['files', agent.ref], queryFn: () => api.files(agent.ref), enabled: mention !== undefined, staleTime: 4000 });
  const mentionItems = useMemo(
    () => (mentionQuery === undefined || mentionKey === mentionDismissed ? [] : matchFiles(filesQuery.data?.files ?? [], mentionQuery)),
    [mentionQuery, mentionKey, mentionDismissed, filesQuery.data],
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
  // What push-to-talk heard joins what's already typed, and is sent at once
  // when Settings → Voice says so and nothing stands in the way; otherwise it
  // waits in the composer to be edited.
  const dictated = (words: string) => {
    const next = [text.trim(), words].filter(Boolean).join(' ');
    if (voiceSettings().after === 'send' && !disabled && !send.isPending && !held && !cacheCard) {
      send.mutate({ text: next, images });
      return;
    }
    pendingCursor.current = next.length;
    setText(next);
    area.current?.focus();
  };
  const pick = (item: SlashItem) => {
    setText(item.kind === 'skill' ? `$${item.skill.name} ` : `/${item.command.name} `);
    area.current?.focus();
  };
  const pickSkill = (skill: T.Skill) => {
    if (!dollar) return;
    const insert = `$${skill.name} `;
    pendingCursor.current = dollar.start + insert.length;
    setText(text.slice(0, dollar.start) + insert + text.slice(cursor).replace(/^\s/, ''));
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
    if (dollarItems.length > 0) {
      if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
        event.preventDefault();
        setDollarHighlighted((i) => (i + (event.key === 'ArrowDown' ? 1 : dollarItems.length - 1)) % dollarItems.length);
        return;
      }
      if (event.key === 'Tab' || (event.key === 'Enter' && !event.shiftKey)) {
        event.preventDefault();
        pickSkill(dollarItems[dollarHighlighted]);
        return;
      }
      if (event.key === 'Escape') {
        setDollarDismissed(dollarKey ?? null);
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
    ? t('chat.composer.startToChat', { name: agent.name })
    : asleep
      ? t('chat.composer.wakeToChat', { state: asleep.state, name: agent.name })
    : held
      ? t('chat.composer.held')
      : requests.length > 0
      ? t('chat.composer.answerRequest')
      : busy
        ? t('chat.composer.busy', { tool })
        : t('chat.composer.placeholder');

  return (
    <div className="relative" data-chat-composer>
      {plan && <TasksBadge plan={plan} />}
      {asleep ? (
        <Attached tone="neutral">
          <div className="flex items-center gap-2 text-[12.5px]" data-chat-asleep={asleep.state}>
            {asleep.starting || send.isPending ? <LoaderCircle className="size-3.5 shrink-0 animate-spin text-muted" /> : <Moon className="size-3.5 shrink-0 text-muted" />}
            <p className="min-w-0 flex-1 break-words leading-relaxed text-tertiary">{t('chat.composer.asleep', { state: asleep.state, name: agent.name })}</p>
            <button
              className="flex h-6 shrink-0 items-center gap-1 rounded-md bg-brand-500/90 px-2.5 text-[12px] font-medium text-white transition hover:bg-brand-500 disabled:opacity-60"
              disabled={asleep.starting || send.isPending}
              onClick={asleep.onStart}
            >
              <Play className="size-3" />
              {t('chat.composer.wake', { state: asleep.state })}
            </button>
          </div>
        </Attached>
      ) : requests.length > 0 && thread ? (
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
                {retry.isPending ? t('chat.composer.starting') : t('chat.composer.startAgain')}
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
              {retry.isPending ? t('chat.composer.starting') : t('chat.composer.startAgain')}
            </button>
          </div>
        </Attached>
      ) : session?.state === 'starting' && !busy ? (
        <Attached tone="neutral">
          <div className="flex items-center gap-2 text-[12.5px] text-muted">
            <LoaderCircle className="size-3.5 animate-spin" />
            {session.detail || t('chat.composer.startingTool', { tool })}
          </div>
        </Attached>
      ) : session?.toolsChanged ? (
        // The connectors changed since the AI tool started, which reads its
        // MCP servers only then. The daemon restarts it before the next
        // message when it can resume the session; "Reload now" doesn't wait.
        <Attached tone="neutral">
          <div className="flex items-start gap-2 text-[12.5px]" data-chat-tools-changed>
            <Plug className="mt-0.5 size-3.5 shrink-0 text-muted" />
            <p className="min-w-0 flex-1 break-words leading-relaxed text-tertiary">
              {t(session.noResume ? 'chat.composer.toolsChangedNoResume' : 'chat.composer.toolsChanged', { tool })}
            </p>
            <button
              className="h-6 shrink-0 rounded-md px-2 text-[12px] font-medium text-title transition hover:bg-white/10 disabled:opacity-50"
              disabled={reloadTools.isPending || toolsReload(session) !== 'ready'}
              onClick={() => reloadTools.mutate()}
            >
              {t('chat.composer.reloadNow')}
            </button>
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
            <span className="rounded-full bg-overlay px-3 py-1.5 shadow">{noImages ?? t('chat.composer.drop')}</span>
          </div>
        )}
        {commands.length > 0 && (
          <div className="absolute inset-x-2 bottom-full mb-2 overflow-hidden rounded-2xl border border-line-strong bg-overlay p-1 shadow-[0_24px_60px_-20px_var(--ab-shadow-deep)] backdrop-blur-xl" role="listbox" aria-label={t('chat.composer.commands')}>
            {commands.map((item, i) => (
              <Fragment key={item.kind === 'skill' ? `skill:${item.skill.name}` : item.command.name}>
                {(i === 0 || commands[i - 1].kind !== item.kind) && commands.some((c) => c.kind === 'skill') && (
                  <p className={cn('px-3 pb-1 text-[10.5px] font-medium uppercase tracking-wide text-faint', i === 0 ? 'pt-1.5' : 'pt-2.5')}>
                    {item.kind === 'skill' ? t('chat.composer.skills') : t('chat.composer.commandsGroup')}
                  </p>
                )}
                {item.kind === 'skill' ? (
                  <SkillOption skill={item.skill} active={i === highlighted} onPick={() => pick(item)} onHover={() => setHighlighted(i)} />
                ) : (
                  <button
                    role="option"
                    aria-selected={i === highlighted}
                    className={cn('flex w-full items-baseline gap-2.5 rounded-xl px-3 py-2 text-left', i === highlighted && 'bg-surface-raised')}
                    onMouseDown={(event) => {
                      event.preventDefault();
                      pick(item);
                    }}
                    onMouseEnter={() => setHighlighted(i)}
                  >
                    <span className="shrink-0 font-mono text-[12.5px] text-primary">/{item.command.name}</span>
                    {item.command.hint && <span className="shrink-0 font-mono text-[11.5px] text-faint">{item.command.hint}</span>}
                    <span className="min-w-0 truncate text-[12px] text-subtle">{item.command.description}</span>
                  </button>
                )}
              </Fragment>
            ))}
          </div>
        )}
        {dollarItems.length > 0 && (
          <div className="absolute inset-x-2 bottom-full mb-2 overflow-hidden rounded-2xl border border-line-strong bg-overlay p-1 shadow-[0_24px_60px_-20px_var(--ab-shadow-deep)] backdrop-blur-xl" role="listbox" aria-label={t('chat.composer.skills')}>
            <p className="px-3 pb-1 pt-1.5 text-[10.5px] font-medium uppercase tracking-wide text-faint">{t('chat.composer.skills')}</p>
            {dollarItems.map((skill, i) => (
              <SkillOption key={skill.name} skill={skill} active={i === dollarHighlighted} onPick={() => pickSkill(skill)} onHover={() => setDollarHighlighted(i)} />
            ))}
          </div>
        )}
        {mentionItems.length > 0 && (
          <div className="absolute inset-x-2 bottom-full mb-2 overflow-hidden rounded-2xl border border-line-strong bg-overlay p-1 shadow-[0_24px_60px_-20px_var(--ab-shadow-deep)] backdrop-blur-xl" role="listbox" aria-label={t('chat.composer.files')}>
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
          <div className="flex flex-wrap gap-2 px-4 pt-3.5" aria-label={t('chat.composer.attached')}>
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
            aria-label={t('chat.composer.message')}
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
          <Tip label={noImages ?? t('chat.composer.attachTip')}>
            {/* A span, so the reason still shows on a disabled button. */}
            <span className="shrink-0">
              <button
                type="button"
                aria-label={t('chat.composer.attach')}
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
            {/* A phone (web/bridge.ts's lan) has its own keyboard's dictation,
                and isn't sent the models the button runs. */}
            {!onPhone && <VoiceButton disabled={disabled} onText={dictated} scope={area} />}
            {busy && (
              <Tip label={t('common.stop')}>
                <button
                  aria-label={t('chat.composer.stopTurn')}
                  disabled={stop.isPending}
                  onClick={() => stop.mutate()}
                  className="flex size-8 items-center justify-center rounded-full bg-rose-500/90 text-white shadow-[0_8px_20px_-8px_rgb(244_63_94/0.7)] transition hover:scale-105 hover:bg-rose-500 disabled:opacity-60"
                >
                  <span className="size-2.5 rounded-[3px] bg-white" />
                </button>
              </Tip>
            )}
            <Tip label={busy ? t('chat.composer.sendWhileWorking', { tool }) : t('chat.composer.send')}>
              <button
                aria-label={t('chat.composer.send')}
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
  if (!session.resumeAt) return translate('chat.limit.reached');
  if (!session.limitedUntil) return translate('chat.limit.waiting');
  return translate('chat.limit.resumes', { time: formatTime(session.resumeAt, { hour: '2-digit', minute: '2-digit' }) });
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

function optionLabel(option: T.ChatPermissionOption, approval: boolean): string {
  // AgentBox's own requests for approval (a lead changing a skill) answer in their own words.
  if (approval) return translate(option.kind.startsWith('allow') ? 'chat.permission.approve' : 'chat.permission.refuse');
  switch (option.kind) {
    case 'allow_once':
      return translate('chat.permission.allow');
    case 'reject_once':
      return translate('chat.permission.deny');
    case 'reject_always':
      return translate('chat.permission.alwaysDeny');
  }
  // The tool's own words say what "always" covers, like "Yes, allow all edits during this session".
  const words = option.name.replace(/^yes,?\s*/i, '').replace(/^and\s+/i, '');
  return words.length > 3 ? words.charAt(0).toUpperCase() + words.slice(1) : translate('chat.permission.alwaysAllow');
}

function PermissionBanner({ agent, thread, request, count }: { agent: T.Agent; thread: T.ChatThread; request: T.ChatItem; count: number }) {
  const t = useT();
  const permission = request.permission!;
  const tool = toolOf(thread, permission.callId);
  const answer = useMutation({ mutationFn: (option: string) => api.answerChat(agent.ref, request.id, option), onError: (err) => toast.error(errorMessage(err)) });
  const approval = !!permission.approval;
  const detail = approval ? permission.detail : tool?.command || (tool?.paths?.[0] && relativePath(tool.paths[0], agent.worktree));
  const diffs = permission.diffs?.length ? permission.diffs : tool?.diffs;
  const rejects = permission.options.filter((o) => o.kind.startsWith('reject'));
  const allows = permission.options.filter((o) => o.kind.startsWith('allow')).sort((a, b) => (a.kind === 'allow_once' ? 1 : 0) - (b.kind === 'allow_once' ? 1 : 0));
  return (
    <Attached tone="amber">
      <div data-chat-permission={request.id}>
        <div className="flex items-center gap-2 text-[12px]">
          <ShieldAlert className="size-3.5 shrink-0 text-amber-300" />
          <span className="font-medium text-amber-100">
            {approval ? t('chat.permission.approval') : t('chat.permission.asks', { tool: aiLabel(agent.ai), action: tool?.kind ?? '' })}
          </span>
          {count > 1 && <span className="ml-auto text-[10.5px] tabular-nums text-amber-200/60">{t('chat.permission.oneOf', { count })}</span>}
        </div>
        <p className="mt-1 break-words text-[13px] text-primary">{permission.title}</p>
        {detail && approval && <p className="mt-0.5 break-words text-[12px] text-muted">{detail}</p>}
        {detail && !approval && !permission.title.includes(detail) && (
          <code className="mt-1 block max-h-20 overflow-auto whitespace-pre-wrap break-all font-mono text-[11.5px] text-muted">{detail}</code>
        )}
        {diffs?.length ? (
          <div className="mt-2 overflow-hidden rounded-lg border border-line bg-well">
            {diffs.map((diff, i) => (
              <DiffView key={i} diff={diff} className={approval ? 'max-h-64' : 'max-h-40'} />
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
              {optionLabel(option, approval)}
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
              {optionLabel(option, approval)}
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
  const t = useT();
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
  const tokens = t('chat.cache.tokens', { n: formatTokens(cache.contextUsed ?? 0) });
  const compacting = choose.isPending && choose.variables?.compact;
  const waiting = held ?? (choose.isPending ? choose.variables?.message : null);
  return (
    <Attached tone="amber">
      <div data-chat-cache={expired ? 'expired' : 'expiring'}>
        <div className="flex items-center gap-2 text-[12px]">
          <Hourglass className="size-3.5 shrink-0 text-amber-300" />
          <span className="font-medium text-amber-100">
            {expired ? t('chat.cache.expired') : t('chat.cache.expires', { span: formatSpan(left) })}
          </span>
          <span className="ml-auto shrink-0 tabular-nums text-[11px] text-amber-200/60">{t('chat.cache.idle', { span: formatSpan(idle) })}</span>
        </div>
        <p className="mt-1 break-words text-[12.5px] leading-relaxed text-amber-100/85">
          {t('chat.cache.resend', { expired: expired ? 'yes' : 'no', tokens })}
        </p>
        {waiting && (
          <p className="mt-1.5 line-clamp-2 break-words rounded-lg bg-black/10 px-2.5 py-1.5 text-[12.5px] text-primary" data-chat-cache-held>
            {waiting.text || t('chat.cache.images', { count: waiting.images.length })}
          </p>
        )}
        <div className="mt-2.5 flex flex-wrap items-center justify-end gap-1.5">
          <button
            disabled={choose.isPending}
            onClick={() => pick(false)}
            className="h-7 rounded-lg border border-amber-200/20 px-2.5 text-[12.5px] text-amber-50 transition hover:bg-amber-200/10 disabled:opacity-50"
          >
            {t('chat.cache.sendAnyway')}
          </button>
          <button
            disabled={choose.isPending}
            onClick={() => pick(true)}
            className="flex h-7 items-center gap-1.5 rounded-lg bg-amber-300 px-3 text-[12.5px] font-medium text-on-bright transition hover:bg-amber-200 disabled:opacity-60"
          >
            {compacting && <LoaderCircle className="size-3.5 animate-spin" />}
            {compacting ? t('chat.cache.compacting') : t('chat.cache.compactAndSend')}
          </button>
        </div>
      </div>
    </Attached>
  );
}

// formatSpan is a duration as "4m 30s", "1h 5m" or "12s".
function formatSpan(ms: number): string {
  const s = Math.max(0, Math.round(Math.abs(ms) / 1000));
  if (s < 60) return translate('chat.duration.seconds', { s });
  const m = Math.floor(s / 60);
  if (m < 60) return translate('chat.duration.minutes', { m, s: s % 60 });
  return translate('chat.duration.hours', { h: Math.floor(m / 60), m: m % 60 });
}

function TasksBadge({ plan }: { plan: T.ChatPlanEntry[] }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const done = plan.filter((e) => e.status === 'completed').length;
  const finished = done === plan.length;
  const current = plan.find((e) => e.status === 'in_progress') ?? plan.find((e) => e.status === 'pending');
  return (
    <Attached tone="neutral">
      <button className="flex w-full items-center gap-2 text-left text-[12px]" aria-expanded={open} onClick={() => setOpen(!open)} data-chat-tasks>
        <ListTodo className="size-3.5 shrink-0 text-muted" />
        <span className="shrink-0 text-subtle">{t('chat.tasks.label')}</span>
        <span className="min-w-0 flex-1 truncate font-medium text-secondary">{finished ? t('chat.tasks.allDone') : current?.content}</span>
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
  const t = useT();
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
  // While the tool starts, its menus haven't arrived: at most the model menu
  // remembered from an earlier chat is here. Say the rest is on its way,
  // rather than leaving a composer that looks like it has nothing to set.
  const loading = session?.state === 'starting' && !options.some((o) => o.category === 'thought_level' || o.category === 'mode');
  const loadingMark = loading && (
    <Tip label={t('chat.options.loadingTip', { tool: aiLabel(agent.ai) })} side="top">
      <span role="status" aria-label={t('chat.options.loading')} className="flex h-7 items-center gap-1.5 px-1.5 text-[12px] text-subtle">
        <LoaderCircle className="size-3.5 animate-spin" />
        {t('common.settings')}
      </span>
    </Tip>
  );
  if (options.length === 0)
    return loading ? (
      <div className="flex min-w-0 items-center gap-0.5">
        {toolLabel}
        {loadingMark}
      </div>
    ) : (
      toolLabel
    );
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
      {effort && <OptionMenu option={effort} icon={<Brain />} label={effort.value === 'default' ? t('chat.options.effort') : undefined} onPick={pick(effort)} />}
      {mode && <OptionMenu option={mode} icon={(() => { const Icon = modeIcon(mode); return <Icon />; })()} onPick={pick(mode)} />}
      {loadingMark}
      {others.length > 0 && (
        <Menu>
          <MenuTrigger asChild>
            <button
              aria-label={t('chat.options.more')}
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
                      { value: 'true', name: t('common.on') },
                      { value: 'false', name: t('common.off') },
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
  if (isCurrent && contextSize) return contextBadge(contextSize);
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
  const t = useT();
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
          aria-label={currentHint ? t('chat.options.withContext', { option: option.name, choice: current!.name, size: currentHint }) : option.name}
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
              aria-label={t('chat.options.search', { name: option.name.toLowerCase() })}
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              onKeyDown={(e) => e.stopPropagation()}
              placeholder={t('common.search')}
              className="w-full bg-transparent text-[13px] text-primary placeholder:text-faint focus:outline-none"
            />
          </div>
        )}
        {missing && (
          <MenuItem disabled hint={<Check className="size-3.5 text-amber-300" />}>
            <span className="grid">
              <span className="flex items-center gap-1.5 text-amber-200">
                {missing}
                <span className="shrink-0 rounded-full bg-amber-400/15 px-1.5 py-px text-[9.5px] font-semibold uppercase tracking-wide text-amber-300">{t('chat.options.offMenu')}</span>
              </span>
              <span className="text-[11px] text-subtle">{t('chat.options.offMenuHint')}</span>
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
        {groups.length === 0 && <div className="px-2.5 py-3 text-center text-[12px] text-subtle">{t('chat.options.noMatch', { query: query.trim() })}</div>}
        {allowNaming && isModel && <ModelByName onPick={onPick} />}
      </MenuContent>
    </Menu>
  );
}

// RecommendedBadge marks the choice the tool itself recommends, so its name
// doesn't have to carry "(recommended)" everywhere it appears.
function RecommendedBadge() {
  const t = useT();
  return <span className="shrink-0 rounded-full bg-brand-400/15 px-1.5 py-px text-[9.5px] font-semibold uppercase tracking-wide text-brand-300">{t('chat.options.recommended')}</span>;
}

// ContextMeter shows how full the context window is, once that starts to matter:
// a sliver of a ring would only look like a spinner.
function ContextMeter({ session }: { session?: T.ChatSession }) {
  const t = useT();
  if (!session?.contextSize) return null;
  const used = session.contextUsed ?? 0;
  const fraction = Math.min(used / session.contextSize, 1);
  if (fraction < 0.2) return null;
  const r = 7;
  const circumference = 2 * Math.PI * r;
  return (
    <Tip label={t('chat.context.used', { used: formatTokens(used), total: formatTokens(session.contextSize) })}>
      <span className="flex size-7 items-center justify-center" aria-label={t('chat.context.label')} data-chat-context>
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

type SlashItem = { kind: 'skill'; skill: T.Skill } | { kind: 'command'; command: T.ChatCommand };

// SkillOption is one skill in the composer's menus: its tile, $name and what
// it's for.
function SkillOption({ skill, active, onPick, onHover }: { skill: T.Skill; active: boolean; onPick: () => void; onHover: () => void }) {
  return (
    <button
      role="option"
      aria-selected={active}
      data-skill-option={skill.name}
      className={cn('flex w-full items-center gap-2.5 rounded-xl px-2.5 py-1.5 text-left', active && 'bg-surface-raised')}
      onMouseDown={(event) => {
        event.preventDefault();
        onPick();
      }}
      onMouseEnter={onHover}
    >
      <SkillTile name={skill.name} size="sm" />
      <span className="shrink-0 font-mono text-[12.5px] text-primary">${skill.name}</span>
      <span className="min-w-0 truncate text-[12px] text-subtle">{skill.description}</span>
    </button>
  );
}
