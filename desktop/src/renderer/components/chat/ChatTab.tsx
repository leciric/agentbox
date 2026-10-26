import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ArrowDown, Archive, Ellipsis, EyeOff, FolderTree, LoaderCircle, MessageSquarePlus, Play, RefreshCw, Search, Trash2 } from 'lucide-react';
import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react';
import { toast } from 'sonner';
import type * as T from '../../../shared/api';
import { api, isHomeChat, isProjectChat } from '../../lib/api';
import type { Wake } from '../v2/ReviewV2';
import { asleep, chatKey, dropWindow, fetchThread, isSilent, loadOlder, loadWindowNewer, loadWindowOlder, sessionShown, toolsReload } from '../../lib/chat';
import { useT, type MessageKey } from '../../lib/i18n';
import { useProjectName } from '../../lib/useProjectName';
import { cn, errorMessage, shortcut } from '../../lib/utils';
import { ConfirmDialog } from '../ConfirmDialog';
import { ProjectCredentialCards } from '../CredentialCard';
import { AIIcon, aiLabel } from '../state';
import { Button } from '../ui/button';
import { Notice } from '../ui/card';
import { Menu, MenuCheckboxItem, MenuContent, MenuItem, MenuLabel, MenuTrigger } from '../ui/menu';
import { Tip } from '../ui/tooltip';
import { PageStack } from '../pages/PageStack';
import { Composer } from './Composer';
import { leadOn } from '../../lib/leadRunner';
import { FindBar } from './FindBar';
import { FileLinksProvider, FilePanel } from '../files/FilePanel';
import { hasFiles, requestFind, setPanelPrefs, usePanelPrefs } from '../../lib/files';
import { openPanelOn } from '../../lib/rightPanel';
import { useUiV2 } from '../../lib/uiV2';
import { useChatWindow, useRevealItem, type ChatOpenAt } from './reveal';
import { Timeline } from './Timeline';
import { ImageWait } from '../ImageWait';

// requestChatFind opens the find bar on a chat, as Ctrl+F (⌘F) does, for its
// ⋯ menu: after the menu has closed and handed focus back to its trigger, so
// the bar's field keeps it.
const findEvent = 'agentbox:chat-find';
export function requestChatFind(ref: string): void {
  setTimeout(() => window.dispatchEvent(new CustomEvent(findEvent, { detail: ref })), 50);
}

// ChatTab is the conversation with an agent's AI tool: the timeline, with the
// composer floating over its end.
// autoStart is off only where nothing should start, like the dev preview.
// openAt brings an item into view, reading the chat back to it if need be,
// with the find bar open on openAt.query when there is one; a new object
// does it again, so pass one per opening.
// readOnly is a past agent's chat (components/PastAgents.tsx): it never
// starts, and a bar to continue or delete it takes the composer's place.
export function ChatTab({
  agent: given,
  starting,
  onStart,
  autoStart = true,
  onOpenAgent,
  openAt,
  readOnly,
  aboveComposer,
}: {
  agent: T.Agent;
  starting: boolean;
  onStart: () => void;
  autoStart?: boolean;
  onOpenAgent?: (ref: string) => void;
  openAt?: ChatOpenAt;
  readOnly?: { onContinue: () => void; onDelete: () => void };
  // Something to show over the composer, such as UI v2's review card.
  // A function gets the wake button's state while the agent sleeps, to merge
  // it into its own bar.
  aboveComposer?: ReactNode | ((wake?: Wake) => ReactNode);
}) {
  const t = useT();
  const queryClient = useQueryClient();
  const thread = useQuery({ queryKey: chatKey(given.ref), queryFn: () => fetchThread(queryClient, given.ref) });
  const session = thread.data?.session;
  // A lead runs the tool and account its composer chose, which its session
  // says (RunnerPicker).
  const agent = leadOn(given, session);
  const running = agent.state === 'running';
  // A stopped or paused agent's chat is still all there to read, with the
  // button that starts it on the composer, which also wakes it by sending.
  const sleeping = asleep(agent);
  // A search result older than the chat's latest page opens a window on it,
  // which the timeline shows instead of the chat's end until you go back
  // there (lib/chat's openWindow). The session is the chat's own, as of now.
  const win = useChatWindow(agent.ref);
  const view = win && thread.data ? { ...win, session: thread.data.session } : thread.data;
  useEffect(() => () => dropWindow(queryClient, agent.ref), [queryClient, agent.ref]);
  const start = useMutation({
    mutationFn: () => api.startChat(agent.ref),
    // Starting a project's chat for the first time makes its lead, with a
    // worktree of its own: read the project's chat again to have it.
    onSuccess: () => {
      if (isProjectChat(agent.ref)) void queryClient.invalidateQueries({ queryKey: ['projectChat', agent.project] });
    },
  });

  // Start the AI tool when the chat opens, so its settings — model, effort,
  // mode — are there before you type. They are the tool's own menus, and only
  // a running session has them. A project's chat starts here too, on its
  // first opening: a project that's never opened still costs nothing.
  const sessionState = session?.state;
  const past = readOnly !== undefined;
  useEffect(() => {
    if (!past && autoStart && running && sessionState === 'off' && !start.isPending) start.mutate();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [past, autoStart, running, sessionState, agent.ref]);

  // Follow the conversation while you're at its end.
  const scroller = useRef<HTMLDivElement>(null);
  const content = useRef<HTMLDivElement>(null);
  const composer = useRef<HTMLDivElement>(null);
  const following = useRef(true);
  const lastTop = useRef(0);
  // When you last touched, wheeled, keyed or grabbed the scroller: only a
  // scroll up you made stops following. Content that got shorter as it laid
  // out — a diagram drawn smaller than its placeholder — pulls the scroll up
  // too, and on a phone that left a chat short of its end.
  const touched = useRef(0);
  const touch = () => {
    touched.current = Date.now();
  };
  const [atEnd, setAtEnd] = useState(true);
  const [composerHeight, setComposerHeight] = useState(120);

  const scrollToEnd = () => {
    following.current = true;
    setAtEnd(true);
    // Out of a window, the chat's end replaces it, and is followed as it lays out.
    if (win) return dropWindow(queryClient, agent.ref);
    const box = scroller.current;
    if (box) box.scrollTo({ top: box.scrollHeight, behavior: 'smooth' });
  };
  useLayoutEffect(() => {
    const box = scroller.current;
    if (box && !win && following.current) box.scrollTop = box.scrollHeight;
  }, [win]);

  useLayoutEffect(() => {
    const box = scroller.current;
    const inner = content.current;
    const bottom = composer.current;
    if (!box || !inner || !bottom) return;
    const follow = () => {
      if (following.current) box.scrollTop = box.scrollHeight;
    };
    const observer = new ResizeObserver(() => {
      setComposerHeight(bottom.offsetHeight);
      follow();
    });
    // The room left for the composer is padding, which only the border box includes.
    observer.observe(inner, { box: 'border-box' });
    observer.observe(bottom);
    follow();
    return () => observer.disconnect();
  }, []);

  // A banner on the composer, like a permission request, takes room at the end: keep the end in view.
  useLayoutEffect(() => {
    const box = scroller.current;
    if (box && following.current) box.scrollTop = box.scrollHeight;
  }, [composerHeight]);

  // Older messages come a page at a time as you get near the top. A page put
  // in front would push what you are reading down by its height, so the
  // distance from the end is kept across it instead: what was on screen stays
  // where it was. (The scroller has overflow-anchor off, for following the end.)
  const [loadingOlder, setLoadingOlder] = useState(false);
  const [loadingNewer, setLoadingNewer] = useState(false);
  const fromEnd = useRef<number | null>(null);
  const older = view?.older ?? false;
  const loadMore = () => {
    const box = scroller.current;
    if (!box || !older || loadingOlder || loadingNewer) return;
    setLoadingOlder(true);
    fromEnd.current = box.scrollHeight - box.scrollTop;
    (win ? loadWindowOlder : loadOlder)(queryClient, agent.ref)
      .catch(() => {})
      .finally(() => setLoadingOlder(false));
  };
  // A window reads on towards the chat's end the same way, as you get near
  // its bottom. What goes on after what you are reading doesn't move it, but
  // it would move the end a page in front is kept from: one at a time.
  const newer = win?.newer ?? false;
  const loadNewer = () => {
    if (!newer || loadingNewer || loadingOlder) return;
    setLoadingNewer(true);
    loadWindowNewer(queryClient, agent.ref)
      .catch(() => {})
      .finally(() => setLoadingNewer(false));
  };
  const firstId = view?.items[0]?.id;
  const lastId = view?.items.at(-1)?.id;
  useLayoutEffect(() => {
    const box = scroller.current;
    if (!box || fromEnd.current === null) return;
    if (!following.current) box.scrollTop = box.scrollHeight - fromEnd.current;
    fromEnd.current = null;
  }, [firstId]);
  // A first page too short to scroll has no top to get near: keep reading
  // back until the chat fills its view, or there is nothing older, and a
  // window on forward too.
  useEffect(() => {
    const box = scroller.current;
    if (!box || box.scrollHeight > box.clientHeight + 200) return;
    if (older) loadMore();
    else loadNewer();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [older, loadingOlder, newer, loadingNewer, firstId, lastId]);

  // The file tree and viewer beside the chat (components/files), for a chat
  // with a worktree. available is how wide the chat and the panel are
  // together, for the panel to leave the chat room.
  const files = hasFiles(agent.ref, past);
  const panel = usePanelPrefs();
  const v2 = useUiV2();
  const outer = useRef<HTMLDivElement>(null);
  const [available, setAvailable] = useState(0);
  useLayoutEffect(() => {
    const el = outer.current;
    if (!el) return;
    const observer = new ResizeObserver(() => setAvailable(el.clientWidth));
    observer.observe(el);
    setAvailable(el.clientWidth);
    return () => observer.disconnect();
  }, []);

  // Ctrl+F (⌘F) finds in the chat on screen. Every chat open in the app
  // listens, and only one is visible.
  const root = useRef<HTMLDivElement>(null);
  const [finding, setFinding] = useState(false);
  const [findFocus, setFindFocus] = useState(0);
  const [findStart, setFindStart] = useState<ChatOpenAt>();
  const [openings, setOpenings] = useState(0);
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      const key = event.key.toLowerCase();
      if ((key !== 'f' && key !== 'p') || !(event.ctrlKey || event.metaKey) || event.altKey || event.shiftKey) return;
      if (!root.current?.checkVisibility()) return;
      // Ctrl+P (⌘P) finds a file in the panel beside the chat.
      if (key === 'p') {
        if (!files) return;
        event.preventDefault();
        requestFind(agent.ref);
        return;
      }
      event.preventDefault();
      setFinding(true);
      setFindFocus((n) => n + 1);
    };
    // The ⋯ menu's Find in chat (requestChatFind).
    const onFind = (event: Event) => {
      if ((event as CustomEvent<string>).detail !== agent.ref) return;
      setFinding(true);
      setFindFocus((n) => n + 1);
    };
    window.addEventListener('keydown', onKey);
    window.addEventListener(findEvent, onFind);
    return () => {
      window.removeEventListener('keydown', onKey);
      window.removeEventListener(findEvent, onFind);
    };
  }, [files, agent.ref]);

  const jumped = () => {
    following.current = false;
    setAtEnd(false);
  };
  const { reveal } = useRevealItem(agent.ref, thread.data, scroller, jumped);
  useEffect(() => {
    if (!openAt) return;
    if (openAt.query) {
      // A fresh bar, on the query, starting at the item.
      setFindStart(openAt);
      setOpenings((n) => n + 1);
      setFinding(true);
      setFindFocus((n) => n + 1);
    } else reveal(openAt.item);
  }, [openAt, reveal]);

  // A conversation of nothing but notices — a project's chat whose only agent
  // finished without waking it — has items and still nothing to show, so the
  // hero belongs there too.
  const empty = !view?.items.some((it) => !isSilent(it));
  const chat = (
    <div ref={root} className="relative flex h-full min-h-0 min-w-0 flex-1 flex-col bg-chat" data-chat={agent.ref}>
      {finding && (
        <FindBar
          key={openings}
          chatRef={agent.ref}
          thread={thread.data}
          scroller={scroller}
          focus={findFocus}
          start={findStart}
          onJump={jumped}
          onClose={() => {
            setFinding(false);
            setFindStart(undefined);
          }}
        />
      )}
      <div
        ref={scroller}
        className="chat-scroller min-h-0 flex-1 overflow-y-auto overscroll-contain [overflow-anchor:none]"
        onWheel={touch}
        onTouchMove={touch}
        onKeyDown={touch}
        onPointerDown={touch}
        onScroll={(event) => {
          const box = event.currentTarget;
          const end = box.scrollHeight - box.scrollTop - box.clientHeight < 48;
          // Scrolling up stops following, and getting back to the end follows again.
          // A smooth scroll down to the end passes the middle, which doesn't count.
          // A window's end isn't the chat's: it reads on instead.
          if (end && !win) following.current = true;
          else if (box.scrollTop < lastTop.current - 1 && Date.now() - touched.current < 1500) following.current = false;
          lastTop.current = box.scrollTop;
          setAtEnd(!win && (end || following.current));
          if (box.scrollTop < 400) loadMore();
          if (win && box.scrollHeight - box.scrollTop - box.clientHeight < 400) loadNewer();
        }}
      >
        <div ref={content} className="mx-auto w-full max-w-4xl px-4 pt-6 md:px-6" style={{ paddingBottom: composerHeight + 28 }}>
          {thread.isPending ? (
            <div className="grid gap-3 pt-2" aria-label={t('chat.tab.loading')}>
              <div className="skeleton ml-auto h-10 w-2/5 rounded-2xl" />
              <div className="skeleton h-4 w-3/4 rounded-md" />
              <div className="skeleton h-4 w-2/3 rounded-md" />
            </div>
          ) : thread.error ? (
            <Notice>{errorMessage(thread.error)}</Notice>
          ) : empty || !view ? (
            <Hero agent={agent} />
          ) : (
            <>
              {view.older && (
                <div className="flex h-8 items-center justify-center gap-1.5 pb-4 text-[12px] text-subtle" data-chat-older>
                  {loadingOlder && <LoaderCircle className="size-3.5 animate-spin" />}
                  {loadingOlder ? t('chat.tab.loadingOlder') : ''}
                </div>
              )}
              <Timeline agent={agent} thread={view} onOpenAgent={onOpenAgent} />
              {view.newer && (
                <div className="flex h-8 items-center justify-center gap-1.5 pt-4 text-[12px] text-subtle" data-chat-newer>
                  {loadingNewer && <LoaderCircle className="size-3.5 animate-spin" />}
                  {loadingNewer ? t('chat.tab.loadingNewer') : ''}
                </div>
              )}
            </>
          )}
          {/* What the project's agents are waiting on you for, at the end of
              the project's chat where you are: cards, not messages the lead reads. */}
          {isProjectChat(agent.ref) && !isHomeChat(agent.ref) && !thread.isPending && <ProjectCredentialCards project={agent.ref.split('/')[0]} />}
        </div>
      </div>

      {(win || !atEnd) && (
        <button
          className="absolute left-1/2 z-20 flex -translate-x-1/2 animate-fade-in items-center gap-1.5 rounded-full border border-line-strong bg-overlay px-3 py-1.5 text-[12px] text-tertiary shadow-lg backdrop-blur-xl transition hover:text-title"
          style={{ bottom: composerHeight + 18 }}
          onClick={scrollToEnd}
          data-chat-to-end={win ? 'latest' : 'end'}
        >
          <ArrowDown className="size-3.5" />
          {t(win ? 'chat.tab.jumpToLatest' : 'chat.tab.scrollToEnd')}
        </button>
      )}

      {/* While one of its popups is open, the composer comes over a files
          panel floating over the chat (components/files), so the popup can be used. */}
      <div ref={composer} className="pointer-events-none absolute inset-x-0 bottom-0 px-3 pb-3 has-[[role=listbox]]:z-40 md:px-6 md:pb-5">
        <div className="pointer-events-auto relative mx-auto w-full max-w-4xl">
          {readOnly ? (
            <div className="flex flex-wrap items-center gap-x-3 gap-y-2 rounded-2xl border border-line-strong bg-overlay px-4 py-2.5 shadow-lg backdrop-blur-xl" data-chat-read-only>
              {/* Delete is quiet and at the other end from Continue, so a
                  slip of the mouse doesn't lose an agent's history. */}
              <Button size="sm" variant="ghost" className="shrink-0 text-subtle hover:text-rose-300" onClick={readOnly.onDelete} data-past-delete>
                <Trash2 />
                {t('common.delete')}
              </Button>
              <span className="h-5 w-px shrink-0 bg-line" aria-hidden />
              <Archive className="size-4 shrink-0 text-subtle" />
              <span className="min-w-0 flex-1 text-[12.5px] text-muted">{t('pastAgents.readOnly')}</span>
              <Button size="sm" variant="primary" className="shrink-0" onClick={readOnly.onContinue} data-past-continue>
                <Play />
                {t('pastAgents.continue')}
              </Button>
            </div>
          ) : (
            <>
              {agent.waitingForImage && <ImageWait agent={agent} />}
              {typeof aboveComposer === 'function' ? aboveComposer(sleeping ? { state: sleeping, starting, onStart } : undefined) : aboveComposer}
              <PageStack agent={agent} />
              <Composer
                agent={agent}
                thread={thread.data}
                disabled={!running && !sleeping}
                asleep={sleeping ? { state: sleeping, starting, onStart } : undefined}
                onSent={scrollToEnd}
              />
            </>
          )}
        </div>
      </div>

    </div>
  );
  return (
    <div ref={outer} className="relative flex h-full min-h-0">
      {files ? <FileLinksProvider agent={agent}>{chat}</FileLinksProvider> : chat}
      {/* v2's files are in its right-hand panel (components/v2/RightPanel.tsx). */}
      {files && panel.open && available > 0 && !v2 && <FilePanel agent={agent} available={available} />}
    </div>
  );
}

// The chat's controls live in its page's ⋯ menu, not in a row of buttons over
// it: useChatMenu gives a caller (AgentView, ProjectView, HomeChatPanel) the
// chat's items for its menu — Find in chat, Files, New chat, Reload tools —
// and the dialogs they open, which stay mounted outside the menu. What's left
// in view is ChatStatus, and only when the tool isn't simply ready.
// It's null without an agent, for a page whose agent is still loading: its
// hooks run all the same.
// buttons is New chat as the chat header's icon, where UI v2 has it on an
// agent's page instead of in a menu (its Find is ⌘F, its Files the panel's);
// newChat is the same as an item, for v2's lead header, whose ⋯ has it
// (ProjectMenu), with untracked, the warning that stays beside it. reload is
// Reload tools as an item for the page's ⋯, under More: it's rarely needed.
export function useChatMenu(agent: T.Agent | undefined): { items: ReactNode; buttons: ReactNode; newChat: ReactNode; untracked: ReactNode; reload: ReactNode; dialogs: ReactNode } | null {
  const t = useT();
  const queryClient = useQueryClient();
  const ref = agent?.ref ?? '';
  const projectName = useProjectName(agent?.project ?? '');
  const thread = useQuery({ queryKey: chatKey(ref), queryFn: () => fetchThread(queryClient, ref), enabled: !!agent });
  const session = thread.data?.session;
  const empty = (thread.data?.items ?? []).length === 0;
  const [clearing, setClearing] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const panel = usePanelPrefs();
  const v2 = useUiV2();
  const reload = useMutation({ mutationFn: () => api.reloadChatTools(ref), onError: (err) => toast.error(errorMessage(err)) });
  if (!agent) return null;
  const tool = aiLabel(agent.ai);
  const can = toolsReload(session);
  const reloadWhy = can === 'busy' ? t('chat.tab.reloadToolsBusy', { tool }) : can === 'off' ? t('chat.tab.reloadToolsOff', { tool }) : undefined;
  const reloadItem = (
    <MenuItem
      icon={RefreshCw}
      disabled={can !== 'ready' || reload.isPending}
      title={reloadWhy ?? t(session?.noResume ? 'chat.tab.reloadToolsNoResumeTip' : 'chat.tab.reloadToolsTip', { tool })}
      onSelect={() => (session?.noResume ? setConfirming(true) : reload.mutate())}
      data-chat-reload-tools
    >
      {t('chat.tab.reloadTools')}
    </MenuItem>
  );
  const newChatItem = (
    <MenuItem icon={MessageSquarePlus} disabled={empty} onSelect={() => setClearing(true)} data-chat-new>
      {t('chat.tab.newChat')}
    </MenuItem>
  );
  const items = (
    <>
      <MenuLabel>{t('chat.menu.label')}</MenuLabel>
      <MenuItem icon={Search} hint={shortcut('F')} onSelect={() => requestChatFind(agent.ref)} data-chat-find>
        {t('chat.menu.find')}
      </MenuItem>
      {hasFiles(agent.ref) && v2 && (
        <MenuItem icon={FolderTree} hint={shortcut('P')} onSelect={() => openPanelOn(agent.ref, 'files', agent.ref)} data-files-toggle>
          {t('files.panel.files')}
        </MenuItem>
      )}
      {hasFiles(agent.ref) && !v2 && (
        <MenuCheckboxItem icon={FolderTree} hint={shortcut('P')} checked={panel.open} onCheckedChange={(open) => setPanelPrefs({ open })} data-files-toggle>
          {t('files.panel.files')}
        </MenuCheckboxItem>
      )}
      {newChatItem}
      {reloadItem}
      {/* Only Claude Code's adapter reports background tasks: elsewhere an
          agent waiting on one reads as finished, and never wakes for it. */}
      {(agent.ai === 'codex' || agent.ai === 'opencode' || agent.ai === 'cursor') && (
        <p className="flex max-w-64 gap-2 px-2.5 py-1.5 text-[11.5px] leading-snug text-amber-300/80" data-chat-background-untracked>
          <EyeOff className="mt-0.5 size-3.5 shrink-0" />
          {t('chat.background.untrackedTip', { tool })}
        </p>
      )}
    </>
  );
  const untracked = agent.ai === 'codex' || agent.ai === 'opencode' || agent.ai === 'cursor';
  const untrackedMark = untracked && (
    <Tip label={t('chat.background.untrackedTip', { tool })}>
      <span className="flex size-8 items-center justify-center text-amber-300/80" data-chat-background-untracked>
        <EyeOff className="size-4" />
      </span>
    </Tip>
  );
  const buttons = (
    <>
      {untrackedMark}
      <Tip label={t('chat.tab.newChat')}>
        <Button size="icon" variant="ghost" aria-label={t('chat.tab.newChat')} disabled={empty} onClick={() => setClearing(true)} data-chat-new>
          <MessageSquarePlus />
        </Button>
      </Tip>
    </>
  );
  const dialogs = (
    <>
      <ConfirmDialog
        open={clearing}
        onOpenChange={setClearing}
        title={t('chat.tab.newChatTitle')}
        description={t('chat.tab.newChatDescription', {
          scope: isHomeChat(agent.ref) ? 'home' : isProjectChat(agent.ref) ? 'project' : 'agent',
          tool,
          project: projectName,
          name: agent.title || agent.name,
        })}
        confirmLabel={t('chat.tab.newChat')}
        onConfirm={() => api.clearChat(agent.ref)}
      />
      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title={t('chat.tab.reloadToolsTitle')}
        description={t('chat.tab.reloadToolsDescription', { tool })}
        confirmLabel={t('chat.tab.reloadToolsConfirm')}
        onConfirm={() => api.reloadChatTools(agent.ref)}
      />
    </>
  );
  return { items, buttons, newChat: newChatItem, untracked: untrackedMark, reload: reloadItem, dialogs };
}

// ChatMenu is a ⋯ of the chat's own, for a page with no menu to fold it into
// (the Home chat).
export function ChatMenu({ agent }: { agent: T.Agent }) {
  const t = useT();
  const menu = useChatMenu(agent);
  if (!menu) return null;
  return (
    <>
      <Menu>
        <MenuTrigger asChild>
          <Button size="icon-sm" variant="ghost" aria-label={t('chat.menu.label')} data-chat-menu>
            <Ellipsis />
          </Button>
        </MenuTrigger>
        <MenuContent>{menu.items}</MenuContent>
      </Menu>
      {menu.dialogs}
    </>
  );
}

// FilesOpen says the file panel is open, beside the status, while it is: the
// menu's tick is out of sight, and this closes it in one click.
export function FilesOpen({ agent }: { agent: T.Agent }) {
  const t = useT();
  const { open } = usePanelPrefs();
  if (!open || !hasFiles(agent.ref)) return null;
  return (
    <Tip label={t('files.panel.hide')}>
      <Button size="sm" variant="ghost" className="h-7 bg-surface-raised px-2 text-primary" aria-pressed onClick={() => setPanelPrefs({ open: false })} data-files-open>
        <FolderTree />
        <span className="hidden xl:inline">{t('files.panel.files')}</span>
      </Button>
    </Tip>
  );
}

function Hero({ agent }: { agent: T.Agent }) {
  const t = useT();
  const projectName = useProjectName(agent.project);
  if (!isProjectChat(agent.ref)) return <AgentHero agent={agent} />;
  if (isHomeChat(agent.ref)) return <HomeHero agent={agent} />;
  return (
    <div className="flex min-h-[42vh] animate-slide-up flex-col items-center justify-center pt-6 text-center" data-chat-hero>
      <div className="relative mb-5">
        <div className="brand-gradient absolute inset-0 rounded-2xl opacity-25 blur-xl" />
        <div className="relative flex size-12 items-center justify-center rounded-2xl border border-line-strong bg-overlay">
          <AIIcon ai={agent.ai} className="size-5 text-brand-300" />
        </div>
      </div>
      <h2 className="text-balance text-2xl font-normal tracking-tight text-primary">{t('chat.hero.title', { project: projectName })}</h2>
      <p className="mt-2 max-w-md text-balance text-sm leading-relaxed text-subtle">
        {t.rich('chat.hero.project', {
          project: projectName,
          branch: <span className="font-mono text-[12.5px] text-muted">{agent.baseRef || t('chat.hero.itsBranch')}</span>,
        })}
      </p>
    </div>
  );
}

function HomeHero({ agent }: { agent: T.Agent }) {
  const t = useT();
  return (
    <div className="flex min-h-[42vh] animate-slide-up flex-col items-center justify-center pt-6 text-center" data-chat-hero>
      <div className="relative mb-5">
        <div className="brand-gradient absolute inset-0 rounded-2xl opacity-25 blur-xl" />
        <div className="relative flex size-12 items-center justify-center rounded-2xl border border-line-strong bg-overlay">
          <AIIcon ai={agent.ai} className="size-5 text-brand-300" />
        </div>
      </div>
      <h2 className="text-balance text-2xl font-normal tracking-tight text-primary">{t('chat.hero.homeTitle')}</h2>
      <p className="mt-2 max-w-md text-balance text-sm leading-relaxed text-subtle">
        {t('chat.hero.home')}
      </p>
    </div>
  );
}

function AgentHero({ agent }: { agent: T.Agent }) {
  const t = useT();
  const projectName = useProjectName(agent.project);
  return (
    <div className="flex min-h-[42vh] animate-slide-up flex-col items-center justify-center pt-6 text-center" data-chat-hero>
      <div className="relative mb-5">
        <div className="brand-gradient absolute inset-0 rounded-2xl opacity-25 blur-xl" />
        <div className="relative flex size-12 items-center justify-center rounded-2xl border border-line-strong bg-overlay">
          <AIIcon ai={agent.ai} className="size-5 text-brand-300" />
        </div>
      </div>
      <h2 className="text-balance text-2xl font-normal tracking-tight text-primary">{t('chat.hero.title', { project: projectName })}</h2>
      <p className="mt-2 max-w-md text-balance text-sm leading-relaxed text-subtle">
        {t.rich('chat.hero.agent', { tool: aiLabel(agent.ai), branch: <span className="font-mono text-[12.5px] text-muted">{agent.branch}</span> })}
      </p>
    </div>
  );
}

const statusStyles: Record<string, { dot: string; label: MessageKey }> = {
  off: { dot: 'bg-faint', label: 'chat.status.off' },
  starting: { dot: 'bg-muted', label: 'chat.status.starting' },
  ready: { dot: 'bg-emerald-400', label: 'chat.status.ready' },
  running: { dot: 'bg-sky-400 animate-pulse', label: 'chat.status.running' },
  waiting: { dot: 'bg-amber-400 animate-pulse', label: 'chat.status.waiting' },
  // Ready, with work it left running in the background: not the session's own
  // state, but what a ready session with ChatSession.background is.
  awaiting: { dot: 'bg-violet-400 animate-pulse', label: 'chat.status.awaiting' },
  error: { dot: 'bg-rose-400', label: 'chat.status.error' },
};

// ChatStatus is the chat's AI tool when it isn't simply ready (lib/chat.ts
// sessionShown): starting, signing in, stopped on an error, or awaiting work
// it left in the background. Ready says nothing.
export function ChatStatus({ agent }: { agent: T.Agent }) {
  const t = useT();
  const queryClient = useQueryClient();
  const thread = useQuery({ queryKey: chatKey(agent.ref), queryFn: () => fetchThread(queryClient, agent.ref) });
  const session = thread.data?.session;
  const state = sessionShown(session);
  if (!state) return null;
  const background = session?.background ?? [];
  const style = statusStyles[state] ?? statusStyles.off;
  const tool = aiLabel(agent.ai);
  const tip = state === 'awaiting' ? t('chat.status.awaitingTip', { tasks: background.join(', ') }) : session?.error || session?.detail || session?.adapter || t('chat.status.adapter', { tool });
  return (
    <Tip label={tip}>
      <span className="flex min-w-0 items-center gap-2 text-[12.5px] text-muted" data-chat-state={state}>
        {state === 'starting' ? <LoaderCircle className="size-3 animate-spin text-muted" /> : <span className={cn('size-1.5 shrink-0 rounded-full', style.dot)} />}
        <span className="hidden truncate sm:inline">{t(style.label, { tool })}</span>
      </span>
    </Tip>
  );
}
