import { useMutation, useQuery } from '@tanstack/react-query';
import {
  ArrowUp,
  Brain,
  Check,
  ChevronDown,
  CircleAlert,
  Cpu,
  SlidersHorizontal,
  File,
  Bot,
  FolderGit2,
  Gauge,
  Hourglass,
  ListTodo,
  LoaderCircle,
  Lock,
  LockOpen,
  LogIn,
  Moon,
  PencilLine,
  PencilRuler,
  Paperclip,
  Play,
  Plug,
  Search,
  ShieldAlert,
  Sparkles,
  SquareArrowOutUpRight,
  X,
  type LucideIcon,
} from 'lucide-react';
import { Fragment, use, useEffect, useLayoutEffect, useMemo, useRef, useState, type ClipboardEvent, type ComponentType, type DragEvent, type KeyboardEvent, type ReactNode, type SyntheticEvent } from 'react';
import { toast } from 'sonner';
import type * as T from '../../../shared/api';
import { ChatOptionPlan } from '../../../shared/api';
import { api, isHomeChat, isProjectChat } from '../../lib/api';
import { composerSkills, searchSkills, skillMentionAt } from '../../lib/skills';
import { SkillTile } from '../SkillsPanel';
import { contextBadge, contextHint, currentPlan, formatTokens, isMessage, pendingPermissions, toolOf, toolsReload } from '../../lib/chat';
import { choiceName, currentFirst, groupChoices, isRecommended, matchesQuery, searchThreshold, unavailableValue } from '../../lib/modelChoices';
import { FileLinks } from '../files/FilePanel';
import { mentionAt, matchAgents, matchFiles, matchProjects, mentionLimit, type MentionItem } from '../../lib/mentions';
import { onComposerFocus, takeComposerFocus } from '../../lib/newTask';
import { composeEvent, getDraft, setDraft } from '../../lib/drafts';
import { formatTime, t as translate, useT } from '../../lib/i18n';
import { useNow } from '../../lib/useNow';
import { useReviewCardShown } from '../v2/ReviewV2';
import { previewUrl } from '../../lib/chatImages';
import { droppedFiles, isElement, isFile, prepareAttachment, room, uploads, type Pending } from '../../lib/chatFiles';
import { splitPicked, takePicks, useWaitingPicks, withPicks } from '../../lib/picks';
import { ModelByName } from '../ModelByName';
import { cn, errorMessage } from '../../lib/utils';
import { aiLabel, ToolMark } from '../state';
import { Menu, MenuContent, MenuItem, MenuLabel, MenuSeparator, MenuSub, MenuTrigger } from '../ui/menu';
import { Tip } from '../ui/tooltip';
import { DiffView, relativePath } from './ChangedFiles';
import { PermissionActions } from './PermissionActions';
import { FileChip } from './Files';
import { ImageThumb } from './Images';
import { PickChip } from './PickChip';
import { NextTool, RunnerItems, useRunner, type Runner } from './RunnerPicker';
import { openSettingsSection } from '../SettingsPage';


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
  // The review card carries the wake button itself while it shows.
  const reviewCard = useReviewCardShown(agent);
  const fileLinks = use(FileLinks);
  const [text, setText] = useState(() => getDraft(agent.ref));
  const [dismissed, setDismissed] = useState<string | null>(null);
  const [highlighted, setHighlighted] = useState(0);
  const [cursor, setCursor] = useState(0);
  const [mentionDismissed, setMentionDismissed] = useState<string | null>(null);
  const [mentionHighlighted, setMentionHighlighted] = useState(0);
  const [dollarDismissed, setDollarDismissed] = useState<string | null>(null);
  const [dollarHighlighted, setDollarHighlighted] = useState(0);
  const [images, setImages] = useState<Pending[]>([]);
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
  const [held, setHeld] = useState<{ text: string; images: Pending[] } | null>(null);
  useEffect(() => {
    if (!held || cacheCard) return;
    setText((current) => current || held.text);
    setImages((current) => (current.length ? current : held.images));
    setHeld(null);
  }, [held, cacheCard]);

  const send = useMutation({
    mutationFn: (message: { text: string; images: Pending[] }) => {
      const { images, files } = uploads(message.images, !session?.noImages);
      return api.sendChat(agent.ref, withPicks(message.text, message.images.filter(isElement)), images, files);
    },
    onMutate: () => {
      setSendError(null);
      setText('');
      setImages([]);
      onSent();
    },
    // A message that didn't go comes back to the composer, and why stays
    // above it until the next one is sent or it's dismissed: a toast is gone
    // before it's read.
    onError: (err, message) => {
      setText((current) => current || message.text);
      setImages((current) => (current.length ? current : message.images));
      setSendError(errorMessage(err));
    },
  });
  const [sendError, setSendError] = useState<string | null>(null);

  // Elements picked in a browser whose chat this is (picks.ts) join what
  // waits here, as they arrive or when the composer comes back into view.
  const arriving = useWaitingPicks(agent.ref);
  useEffect(() => {
    if (!arriving.length) return;
    let next = images;
    for (const pick of takePicks(agent.ref)) {
      const full = room(next, pick);
      if (full) toast.error(full);
      else next = [...next, pick];
    }
    setImages(next);
    area.current?.focus();
  }, [arriving, agent.ref, images]);

  // A Claude Code chat with no login anywhere can't start: say so before the
  // message is sent, with the way to log in, rather than after it fails
  // (agent.resolveClaudeAccount). The draft stays where it is.
  const auth = useQuery({
    queryKey: ['auth'],
    queryFn: api.auth,
    enabled: agent.ai === 'claude',
    refetchInterval: (q) => (q.state.data && q.state.data.claudeAccounts.length === 0 ? 5_000 : false),
  });
  const signedOut = agent.ai === 'claude' && auth.data !== undefined && auth.data.claudeAccounts.length === 0;

  // Pictures go to the AI tool as ACP image blocks, which only an adapter that
  // said promptCapabilities.image takes; for one that didn't, they go as
  // files, like anything else: the daemon puts those where the tool can open
  // them. Before any adapter of this tool has started there's nothing to go
  // on, and pictures are sent as pictures.
  const noAttach = disabled ? t('chat.composer.startToChat', { name: agent.name }) : undefined;
  const attach = async (files: File[]) => {
    if (noAttach) {
      toast.error(noAttach);
      return;
    }
    if (files.length === 0) {
      toast.error(t('chat.composer.nothingAttached'));
      return;
    }
    let waiting = images;
    for (const file of files) {
      try {
        const next = await prepareAttachment(file, !session?.noImages);
        const full = room(waiting, next);
        if (full) {
          toast.error(full);
          continue;
        }
        waiting = [...waiting, next];
        setImages((current) => (room(current, next) ? current : [...current, next]));
      } catch (err) {
        toast.error(errorMessage(err));
      }
    }
  };
  // The file dialog can close having picked a file and still hand the page
  // nothing, no change and no cancel (the v2 UX audit's finding 14: typing a
  // path with Ctrl+L, Enter, and no chip). The window gets its focus back as
  // the dialog closes: if neither event follows soon after, say so, rather
  // than leave the composer as though nothing had been tried.
  const choosing = useRef(false);
  useEffect(() => {
    const el = picker.current;
    const cancelled = () => (choosing.current = false);
    el?.addEventListener('cancel', cancelled);
    return () => el?.removeEventListener('cancel', cancelled);
  }, []);
  const choose = () => {
    choosing.current = true;
    const back = () => {
      removeEventListener('focus', back);
      setTimeout(() => {
        if (!choosing.current) return;
        choosing.current = false;
        toast.error(t('chat.composer.nothingAttached'), { description: t('chat.composer.nothingAttachedHint') });
      }, 1500);
    };
    addEventListener('focus', back);
    picker.current?.click();
  };
  const onPaste = (event: ClipboardEvent<HTMLTextAreaElement>) => {
    const files = droppedFiles(event.clipboardData);
    if (files.length === 0) return;
    event.preventDefault();
    void attach(files);
  };
  const dragsFiles = (event: DragEvent) => !disabled && Array.from(event.dataTransfer.types).includes('Files');
  const onDragOver = (event: DragEvent<HTMLDivElement>) => {
    if (!dragsFiles(event)) return;
    event.preventDefault();
    event.dataTransfer.dropEffect = 'copy';
    setDragging(true);
  };
  const onDrop = (event: DragEvent<HTMLDivElement>) => {
    setDragging(false);
    if (!dragsFiles(event)) return;
    event.preventDefault();
    const files = droppedFiles(event.dataTransfer);
    if (files.length > 0) void attach(files);
  };
  // Cancelling the turn stops its reply being read aloud too.
  const stop = useMutation({ mutationFn: () => api.cancelChat(agent.ref), onError: (err) => toast.error(errorMessage(err)) });
  const retry = useMutation({ mutationFn: () => api.startChat(agent.ref), onError: (err) => toast.error(errorMessage(err)) });
  const resumeNow = useMutation({ mutationFn: () => api.resumeChat(agent.ref), onError: (err) => toast.error(errorMessage(err)) });
  const reloadTools = useMutation({ mutationFn: () => api.reloadChatTools(agent.ref), onError: (err) => toast.error(errorMessage(err)) });

  useEffect(() => {
    setDraft(agent.ref, text);
  }, [agent.ref, text]);

  // A chat opened from UI v2's New task, or its sidebar's project, is opened
  // to write in, on screen already or not.
  useEffect(() => {
    const take = () => {
      if (!disabled && takeComposerFocus(agent.ref)) area.current?.focus();
    };
    take();
    return onComposerFocus(take);
  }, [agent.ref, disabled]);

  // Text put here from elsewhere (composeIn), with the cursor where it says.
  useEffect(() => {
    const put = (e: Event) => {
      const { ref, text: next, cursor } = (e as CustomEvent<{ ref: string; text: string; cursor?: number }>).detail;
      if (ref !== agent.ref) return;
      pendingCursor.current = cursor ?? next.length;
      setText(next);
      area.current?.focus();
    };
    window.addEventListener(composeEvent, put);
    return () => window.removeEventListener(composeEvent, put);
  }, [agent.ref]);

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
  const projectsQuery = useQuery({ queryKey: ['projects'], queryFn: api.projects, enabled: mention !== undefined });
  const agentsQuery = useQuery({ queryKey: ['agents'], queryFn: api.agents, enabled: mention !== undefined });
  const mentionItems = useMemo(() => {
    if (mentionQuery === undefined || mentionKey === mentionDismissed) return [];
    const projects = matchProjects(projectsQuery.data ?? [], mentionQuery);
    const agents = matchAgents((agentsQuery.data ?? []).filter((a) => (home || a.project === agent.project) && a.ref !== agent.ref), mentionQuery);
    return [...projects, ...agents, ...matchFiles(filesQuery.data?.files ?? [], mentionQuery, mentionLimit)];
  }, [mentionQuery, mentionKey, mentionDismissed, filesQuery.data, projectsQuery.data, agentsQuery.data, home, agent.project, agent.ref]);
  useEffect(() => setMentionHighlighted(0), [mentionKey]);

  // Sending while the tool works is the point of the placeholder below: the
  // message joins the running turn and the model decides what it's worth.
  const canSend = !disabled && !signedOut && (text.trim() !== '' || images.length > 0) && !send.isPending && !held;
  // Enter or Send while signed out can't send: the banner pulses instead, so
  // the click isn't silent. The draft stays.
  const [nudged, setNudged] = useState(false);
  useEffect(() => {
    if (!nudged) return;
    const id = setTimeout(() => setNudged(false), 1200);
    return () => clearTimeout(id);
  }, [nudged]);
  const submit = () => {
    if (signedOut && !disabled) {
      setNudged(true);
      return;
    }
    if (!canSend) return;
    if (cacheCard) {
      setHeld({ text: text.trim(), images });
      setText('');
      setImages([]);
      return;
    }
    send.mutate({ text: text.trim(), images });
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

  // What was typed of it: the elements it was sent with are picked again.
  const lastMine = splitPicked(thread?.items.findLast((it) => it.kind === 'user' && !it.parent && !it.hidden)?.text ?? '').text;
  // A chat with nothing in it yet has nothing to follow up on.
  const empty = !!thread && !thread.older && !thread.items.some(isMessage);
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
      // Ctrl+Enter (⌘↵) opens the file in the panel beside the chat instead.
      const highlighted = mentionItems[mentionHighlighted];
      if (event.key === 'Enter' && (event.ctrlKey || event.metaKey) && fileLinks && highlighted?.kind === 'file') {
        event.preventDefault();
        fileLinks.open(highlighted.insert);
        return;
      }
      if (event.key === 'Tab' || (event.key === 'Enter' && !event.shiftKey)) {
        event.preventDefault();
        pickMention(highlighted);
        return;
      }
      if (event.key === 'Escape') {
        setMentionDismissed(mentionKey ?? null);
        return;
      }
    }
    // Up in an empty composer brings your last message back, to send again
    // or fix, as a shell's history does.
    if (event.key === 'ArrowUp' && !event.shiftKey && !event.altKey && !event.ctrlKey && !event.metaKey && text === '' && lastMine) {
      event.preventDefault();
      pendingCursor.current = lastMine.length;
      setText(lastMine);
      return;
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
        : empty
          ? t(projectChat && !home ? 'chat.composer.placeholderLead' : 'chat.composer.placeholderEmpty')
          : t('chat.composer.placeholder');

  return (
    <div className="relative" data-chat-composer>
      {plan && <TasksBadge plan={plan} />}
      {signedOut ? (
        <Attached tone="amber" className={nudged ? 'bg-amber-400/25 ring-2 ring-amber-300/80' : undefined}>
          <div className="flex items-center gap-2 text-[12.5px]" data-chat-signed-out data-nudged={nudged || undefined}>
            <LogIn className="size-3.5 shrink-0 text-amber-300" />
            <p className="min-w-0 flex-1 break-words leading-relaxed text-amber-100/90">{t('chat.composer.signedOut')}</p>
            <button
              className="flex h-6 shrink-0 items-center gap-1 rounded-md bg-brand-500/90 px-2.5 text-[12px] font-medium text-white transition hover:bg-brand-500"
              onClick={() => openSettingsSection('accounts')}
            >
              {t('chat.composer.logIn')}
            </button>
          </div>
        </Attached>
      ) : sendError ? (
        <Attached tone="rose">
          <div className="flex items-start gap-2 text-[12.5px]" data-chat-send-error>
            <CircleAlert className="mt-0.5 size-3.5 shrink-0 text-rose-300" />
            <p className="min-w-0 flex-1 break-words leading-relaxed text-rose-100/90">
              {t('chat.composer.notSent', { error: sendError })}
            </p>
            <button
              className="flex size-6 shrink-0 items-center justify-center rounded-md text-rose-100 transition hover:bg-rose-400/15"
              aria-label={t('common.close')}
              onClick={() => setSendError(null)}
            >
              <X className="size-3.5" />
            </button>
          </div>
        </Attached>
      ) : asleep && reviewCard ? null : asleep ? (
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
            <button
              className="h-6 shrink-0 rounded-md px-2 text-[12px] font-medium text-amber-100 transition hover:bg-amber-400/15"
              disabled={resumeNow.isPending}
              onClick={() => resumeNow.mutate()}
            >
              {t('chat.limit.resumeNow')}
            </button>
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
          'relative z-10 rounded-[22px] border border-line-strong bg-composer/85 shadow-[0_24px_60px_-28px_var(--ab-shadow-deep),inset_0_1px_0_rgb(255_255_255/0.04)] backdrop-blur-xl transition-colors focus-within:border-line-vivid focus-within:ring-2 focus-within:ring-brand-400/60',
          dragging && 'border-brand-400/80 bg-brand-500/5',
        )}
      >
        {dragging && (
          <div className="pointer-events-none absolute inset-0 z-20 flex items-center justify-center rounded-[22px] text-[12.5px] font-medium text-primary">
            <span className="rounded-full bg-overlay px-3 py-1.5 shadow">{t('chat.composer.drop')}</span>
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
          <div className="absolute inset-x-2 bottom-full mb-2 overflow-hidden rounded-2xl border border-line-strong bg-overlay p-1 shadow-[0_24px_60px_-20px_var(--ab-shadow-deep)] backdrop-blur-xl" role="listbox" aria-label={t('chat.composer.mentions')}>
            <p className="px-3 pb-1 pt-1.5 text-[11.5px] text-subtle" data-mention-hint>
              {t('chat.composer.mentionHint')}
            </p>
            <div className="max-h-72 overflow-y-auto">
            {mentionItems.map((item, i) => (
              <div key={`${item.kind}:${item.label}`} className={cn('flex items-center rounded-xl', i === mentionHighlighted && 'bg-surface-raised')} onMouseEnter={() => setMentionHighlighted(i)}>
                <button
                  role="option"
                  aria-selected={i === mentionHighlighted}
                  ref={(el) => {
                    if (i === mentionHighlighted) el?.scrollIntoView?.({ block: 'nearest' });
                  }}
                  className="flex min-w-0 flex-1 items-center gap-2 px-3 py-2 text-left"
                  onMouseDown={(event) => {
                    event.preventDefault();
                    pickMention(item);
                  }}
                >
                  {item.kind === 'project' ? <FolderGit2 className="size-3.5 shrink-0 text-subtle" /> : item.kind === 'agent' ? <Bot className="size-3.5 shrink-0 text-subtle" /> : <File className="size-3.5 shrink-0 text-subtle" />}
                  <span className="min-w-0 truncate font-mono text-[12.5px] text-primary">{item.label}</span>
                </button>
                {item.kind === 'file' && fileLinks && (
                  <button
                    className={cn('mr-1.5 flex shrink-0 items-center gap-1 rounded-lg px-2 py-1 text-[11px] text-subtle transition hover:bg-surface-strong hover:text-primary', i !== mentionHighlighted && 'invisible')}
                    aria-label={t('files.link.open', { path: item.insert })}
                    onMouseDown={(event) => {
                      event.preventDefault();
                      fileLinks.open(item.insert);
                    }}
                    data-mention-open
                  >
                    <SquareArrowOutUpRight className="size-3" />
                    {t('files.link.view')}
                    <kbd className="font-sans text-faint">{navigator.platform.startsWith('Mac') ? '⌘↵' : 'Ctrl+↵'}</kbd>
                  </button>
                )}
              </div>
            ))}
            </div>
          </div>
        )}
        {images.length > 0 && (
          <div className="flex flex-wrap gap-2 px-4 pt-3.5" aria-label={t('chat.composer.attached')}>
            {images.map((image) => {
              const remove = () => setImages((current) => current.filter((other) => other.key !== image.key));
              return isElement(image) ? (
                <PickChip key={image.key} pick={image} n={images.filter(isElement).indexOf(image) + 1} onRemove={remove} />
              ) : isFile(image) ? (
                <FileChip key={image.key} name={image.name} size={image.size} mimeType={image.mimeType} onRemove={remove} />
              ) : (
                <ImageThumb key={image.key} src={previewUrl(image)} name={image.name} className="size-14" onRemove={remove} />
              );
            })}
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
            multiple
            hidden
            onChange={(event) => {
              choosing.current = false;
              const files = Array.from(event.target.files ?? []);
              event.target.value = '';
              void attach(files);
            }}
          />
          <Tip label={noAttach ?? t('chat.composer.attachTip')}>
            {/* A span, so the reason still shows on a disabled button. */}
            <span className="shrink-0">
              <button
                type="button"
                aria-label={t('chat.composer.attach')}
                disabled={!!noAttach}
                onClick={choose}
                className="flex size-8 items-center justify-center rounded-full text-subtle transition hover:bg-surface-raised hover:text-primary disabled:cursor-not-allowed disabled:opacity-40 disabled:hover:bg-transparent"
              >
                <Paperclip className="size-4" />
              </button>
            </span>
          </Tip>
          <SessionOptions agent={agent} session={session} />
          <div className="ml-auto flex shrink-0 items-center gap-1.5">
            <ContextMeter session={session} />
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
            <Tip label={signedOut ? t('chat.composer.signInFirst', { tool }) : busy ? t('chat.composer.sendWhileWorking', { tool }) : t('chat.composer.send')}>
              <button
                aria-label={t('chat.composer.send')}
                // Signed out it stays clickable (dimmed), to say why it can't.
                disabled={!canSend && !signedOut}
                aria-disabled={!canSend || undefined}
                onClick={submit}
                className={cn('flex size-8 items-center justify-center rounded-full bg-gradient-to-b from-brand-500 to-indigo-600 text-white shadow-[inset_0_1px_0_rgb(255_255_255/0.2),0_8px_20px_-8px_rgb(99_102_241/0.8)] transition hover:scale-105 hover:brightness-110 disabled:scale-100 disabled:opacity-30 disabled:shadow-none', signedOut && 'opacity-30 shadow-none hover:scale-100')}
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
function Attached({ tone, className, children }: { tone: keyof typeof tones; className?: string; children: ReactNode }) {
  return <div className={cn('relative mx-auto -mb-4 w-[calc(100%-2.5rem)] animate-slide-up rounded-t-2xl border border-b-0 px-3.5 pb-6 pt-2.5 backdrop-blur-xl transition-[box-shadow,background-color] duration-300', tones[tone], className)}>{children}</div>;
}

function PermissionBanner({ agent, thread, request, count }: { agent: T.Agent; thread: T.ChatThread; request: T.ChatItem; count: number }) {
  const t = useT();
  const permission = request.permission!;
  const tool = toolOf(thread, permission.callId);
  const answer = useMutation({ mutationFn: (option: string) => api.answerChat(agent.ref, request.id, option), onError: (err) => toast.error(errorMessage(err)) });
  const approval = !!permission.approval;
  const detail = approval ? permission.detail : tool?.command || (tool?.paths?.[0] && relativePath(tool.paths[0], agent.worktree));
  const diffs = permission.diffs?.length ? permission.diffs : tool?.diffs;
  return (
    <Attached tone="amber">
      <div data-chat-permission={request.id}>
        <div className="flex items-center gap-2 text-[12px]">
          <ShieldAlert className="size-3.5 shrink-0 text-amber-300" />
          <span className="font-medium text-amber-100">
            {approval ? t(permission.approval === 'project' ? 'chat.permission.projectApproval' : 'chat.permission.approval') : t('chat.permission.asks', { tool: aiLabel(agent.ai), action: tool?.kind ?? '' })}
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
        <PermissionActions
          options={permission.options}
          agent={agent.title || agent.name}
          action={tool?.kind}
          pending={answer.isPending}
          sending={answer.variables}
          onAnswer={(id) => answer.mutate(id)}
          approveLabel={t('chat.permission.allow')}
          denyLabel={t('chat.permission.deny')}
          planApprove={permission.planApprove}
          keys
        />
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
  held: { text: string; images: Pending[] } | null;
  draft: () => { text: string; images: Pending[] } | null;
  onChosen: (message: { text: string; images: Pending[] } | null) => void;
  onFailed: (message: { text: string; images: Pending[] } | null) => void;
}) {
  const t = useT();
  const now = useNow(1000);
  const choose = useMutation({
    mutationFn: ({ compact, message }: { compact: boolean; message: { text: string; images: Pending[] } | null }) =>
      api.chooseChatCache(project, {
        compact,
        text: message ? withPicks(message.text, message.images.filter(isElement)) : undefined,
        ...(message ? uploads(message.images) : {}),
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
            {waiting.text || t('chat.cache.attachments', { count: waiting.images.length })}
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
  const lead = isProjectChat(agent.ref);
  const runner = useRunner(agent, session, lead);
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
  const find = (category: string) => options.find((o) => o.category === category && o.type === 'select');
  const model = find('model');
  const contextWindow = find('context_window');
  const effort = find('thought_level');
  const mode = find('mode');
  // Plan is a toggle beside the mode, not one of its choices (internal/chat/plan.go):
  // the chat plans, then runs on the mode chosen beside it.
  const plan = options.find((o) => o.id === ChatOptionPlan && o.type === 'boolean');
  const others = options.filter((o) => o !== model && o !== contextWindow && o !== effort && o !== mode && o !== plan);
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
      <SetupChip
        agent={agent}
        runner={lead ? runner : undefined}
        model={model}
        contextWindow={contextWindow}
        others={others}
        contextSize={contextSize}
        pending={pending}
        pick={pick}
      />
      {effort && <OptionMenu option={effort} icon={<Brain />} label={effort.value === 'default' ? t('chat.options.effort') : undefined} onPick={pick(effort)} />}
      {plan && <PlanToggle option={plan} onPick={pick(plan)} />}
      {mode && (!plan || mode.choices.length > 1) && <OptionMenu option={mode} icon={(() => { const Icon = modeIcon(mode); return <Icon />; })()} onPick={pick(mode)} />}
      {loadingMark}
    </div>
  );
}

// SetupChip is the composer's one chip for what runs the chat, read as a
// sentence: "Claude Code · Opus · 200k", the tool, its model and its context
// window (the v2 UX audit's finding 9: a bare tool icon, and a "200k" that
// read like a model). Its menu has a submenu for each: the AI tool and
// account (a lead's only: an agent's tool is fixed), the model, the context
// window, and the tool's other settings, which had a ⋯ of their own.
function SetupChip({
  agent,
  runner,
  model,
  contextWindow,
  others,
  contextSize,
  pending,
  pick,
}: {
  agent: T.Agent;
  runner?: Runner;
  model?: T.ChatOption;
  contextWindow?: T.ChatOption;
  others: T.ChatOption[];
  contextSize?: number;
  pending: boolean;
  pick: (option: T.ChatOption) => (value: string) => void;
}) {
  const t = useT();
  const tool = runner?.tool ?? agent.ai;
  const toolName = aiLabel(tool);
  const current = model?.choices.find((c) => c.value === model.value);
  const missing = model && !pending ? unavailableValue(model.choices, model.value) : undefined;
  const modelName = model ? (missing ?? (current && choiceName(current)) ?? model.value) : undefined;
  const windowChoice = contextWindow?.choices.find((c) => c.value === contextWindow.value);
  const size = contextWindow ? (windowChoice ? choiceName(windowChoice) : contextWindow.value) : current ? modelHint(current, true, contextSize) : undefined;
  const parts = [toolName, modelName, size].filter(Boolean).join(' · ');
  const label = t('chat.setup.label', { setup: parts });
  const nothing = !runner && !model && !contextWindow && others.length === 0;
  const chip = (
    <span className={cn('flex h-7 min-w-0 items-center gap-1.5 rounded-lg px-2 text-[12.5px] [&_svg]:size-4 [&_svg]:shrink-0', missing ? 'text-amber-300' : 'text-muted')}>
      {missing ? <CircleAlert className="text-amber-400" /> : <ToolMark ai={tool} />}
      {runner?.next && <NextTool next={runner.next} />}
      <span className="min-w-0 truncate" data-chat-setup-text>
        {toolName}
        {modelName && <span className="max-sm:hidden"> · {modelName}</span>}
        {size && <span className="max-sm:hidden text-subtle"> · {size}</span>}
      </span>
      {!nothing && <ChevronDown className="!size-3.5 text-faint" />}
    </span>
  );
  // An agent's chat with nothing to set reads its tool, and opens nothing.
  if (nothing) return chip;
  return (
    <Menu>
      <MenuTrigger asChild>
        <button
          type="button"
          aria-label={label}
          title={runner?.next ? t('chat.runner.nextTip', { tool: aiLabel(runner.next) }) : label}
          className="flex min-w-0 shrink rounded-lg transition-colors hover:bg-surface hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-400 data-[state=open]:bg-surface-raised"
          data-chat-setup
          data-runner-picker={runner ? '' : undefined}
        >
          {chip}
        </button>
      </MenuTrigger>
      <MenuContent align="start" side="top" className="w-64">
        {runner && (
          <MenuSub icon={toolIcon(tool)} label={t('chat.setup.tool')} value={runner.account ? `${toolName} · ${runner.account}` : toolName} className="max-h-[28rem] w-80 overflow-y-auto" data-chat-setup-sub="tool">
            <RunnerItems runner={runner} />
          </MenuSub>
        )}
        {!runner && <MenuLabel>{toolName}</MenuLabel>}
        {model && (
          <MenuSub icon={Cpu} label={model.name} value={modelName} className="max-h-96 w-72 overflow-y-auto" data-chat-setup-sub="model">
            <OptionChoices
              option={model}
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
          </MenuSub>
        )}
        {contextWindow && (
          <MenuSub icon={Gauge} label={contextWindow.name} value={size} className="max-h-96 w-72 overflow-y-auto" data-chat-setup-sub="context">
            <OptionChoices option={contextWindow} onPick={pick(contextWindow)} />
          </MenuSub>
        )}
        {others.map((option) => (
          <MenuSub key={option.id} icon={SlidersHorizontal} label={option.name} value={option.type === 'boolean' ? t(option.value === 'true' ? 'common.on' : 'common.off') : choiceName(option.choices.find((c) => c.value === option.value) ?? { value: option.value, name: option.value })} className="w-60" data-chat-setup-sub={option.id}>
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
          </MenuSub>
        ))}
      </MenuContent>
    </Menu>
  );
}

const toolIcon = (ai: string) => toolMarks[ai] ?? toolMarks.claude;
const toolMarks: Record<string, ComponentType<{ className?: string }>> = Object.fromEntries(
  ['claude', 'codex', 'opencode', 'cursor'].map((ai) => [ai, ({ className }: { className?: string }) => <ToolMark ai={ai} className={className} />]),
);

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

// PlanToggle turns plan mode on and off. The permission mode beside it stays
// what it is: with bypass permissions, planning asks nothing plan mode allows,
// and the approved plan runs on bypass.
function PlanToggle({ option, onPick }: { option: T.ChatOption; onPick: (value: string) => void }) {
  const t = useT();
  const on = option.value === 'true';
  return (
    <Tip label={on ? t('chat.options.planOnTip') : t('chat.options.planTip')} side="top">
      <button
        type="button"
        aria-pressed={on}
        data-chat-option={option.id}
        onClick={() => onPick(on ? 'false' : 'true')}
        className={cn(
          'flex h-7 items-center gap-1.5 rounded-lg px-2 text-[12.5px] transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-400 [&_svg]:size-4 [&_svg]:shrink-0',
          on ? 'bg-brand-500/15 text-brand-200 hover:bg-brand-500/25' : 'text-muted hover:bg-surface hover:text-primary',
        )}
      >
        <PencilRuler />
        <span className="max-sm:hidden">{t('chat.options.plan')}</span>
      </button>
    </Tip>
  );
}

// OptionMenu picks one of a setting's choices, the effort or the mode: the
// rest are in SetupChip. On a phone, only its icon shows.
function OptionMenu({
  option,
  icon,
  label,
  onPick,
}: {
  option: T.ChatOption;
  icon: ReactNode;
  label?: string;
  onPick: (value: string) => void;
}) {
  const current = option.choices.find((c) => c.value === option.value);
  const missing = unavailableValue(option.choices, option.value);
  return (
    <Menu>
      <MenuTrigger asChild>
        <button
          aria-label={option.name}
          data-chat-option={option.id}
          title={option.description || option.name}
          className={cn(
            'flex h-7 min-w-0 items-center gap-1.5 rounded-lg px-2 text-[12.5px] transition-colors hover:bg-surface hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-400 data-[state=open]:bg-surface-raised data-[state=open]:text-primary [&_svg]:size-4 [&_svg]:shrink-0',
            missing ? 'text-amber-300' : 'text-muted',
          )}
        >
          {missing ? <CircleAlert className="text-amber-400" /> : icon}
          <span className="max-w-[9rem] truncate max-sm:hidden">{label ?? (current && choiceName(current)) ?? option.value}</span>
          <ChevronDown className="!size-3.5 text-faint" />
        </button>
      </MenuTrigger>
      <MenuContent align="start" side="top" className="max-h-96 w-72 overflow-y-auto">
        <OptionChoices option={option} onPick={onPick} />
      </MenuContent>
    </Menu>
  );
}

// OptionChoices are a setting's choices as a menu's items, under its name,
// with a search when there are many. The model's carry their context-window
// sizes (contextSize is the current model's last confirmed reading, see
// modelHint and SessionOptions).
function OptionChoices({
  option,
  onPick,
  contextSize,
  showSizes = true,
  treatMissingAsUnavailable = true,
  allowNaming = false,
}: {
  option: T.ChatOption;
  onPick: (value: string) => void;
  contextSize?: number;
  showSizes?: boolean;
  treatMissingAsUnavailable?: boolean;
  allowNaming?: boolean;
}) {
  const t = useT();
  const [query, setQuery] = useState('');
  const isModel = option.category === 'model';
  const sized = isModel && showSizes;
  // A chosen value the tool no longer offers still names itself, and gets a
  // row of its own in the menu: showing the tool's default as though you'd
  // picked it is how agents ran for weeks on a model nobody chose.
  // treatMissingAsUnavailable is false for a session that hasn't started:
  // its value may be an unresolved alias, not a rejection.
  const missing = treatMissingAsUnavailable ? unavailableValue(option.choices, option.value) : undefined;
  const listed = groupChoices(option.choices.filter((c) => matchesQuery(c, query)));
  const groups = isModel ? currentFirst(listed, option.value) : listed;
  const searchable = option.choices.length >= searchThreshold;
  return (
    <>
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
    </>
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
