import { useQuery, useQueryClient, type InfiniteData } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { AddProjectDialog } from "./components/AddProjectDialog";
import { AgentRail } from "./components/AgentRail";
import { AgentView } from "./components/AgentView";
import { openSettingsEvent, openSettingsSection, rememberSection } from "./components/SettingsPage";
import { openProjectEvent } from "./components/chat/ProjectMessageCard";
import { openAgentEvent } from "./components/chat/AgentPermissionCard";
import { isSearchShortcut, SearchModal } from "./components/SearchModal";
import { openSearch, toggleSearch } from "./lib/searchOpen";
import { ErrorReports } from "./components/ErrorReports";
import { SnapComposer } from "./components/SnapComposer";
import { HomeChatPanel } from "./components/HomeChatPanel";
import { HomeView } from "./components/HomeView";
import { AllMediaView } from "./components/AllMediaView";
import { ConfirmDialog } from "./components/ConfirmDialog";
import { MediaPlace } from "./components/MediaPlace";
import { MediaViewer } from "./components/MediaTab";
import { NoticeToast } from "./components/Notifications";
import { PageArrivals } from "./components/pages/PageArrivals";
import { PagePreviewHost } from "./components/pages/PagePreview";
import { markSeen as markNoticesSeen, noticeText, noticeView } from "./lib/notifications";
import { projectLabel } from "./lib/projectName";
import type * as T from "../shared/api";
import { JobsView } from "./components/JobsView";
import { NewAgentDialog } from "./components/NewAgentDialog";
import { ProjectView } from "./components/ProjectView";
import { PastAgents } from "./components/PastAgents";
import { SettingsView } from "./components/SettingsView";
import { Sidebar } from "./components/Sidebar";
import { SidebarV2 } from "./components/v2/SidebarV2";
import { NewTaskDialog } from "./components/v2/NewTaskDialog";
import { ShortcutSheet } from "./components/v2/ShortcutSheet";
import { TourHost } from "./components/v2/Tour";
import { isNewTaskShortcut, toggleNewTask } from "./lib/newTask";
import { OverviewV2, PastAgentsV2 } from "./components/v2/PagesV2";
import { cycleRegion, isSettingsShortcut, isSheetShortcut, toggleSheet } from "./lib/shortcuts";
import { AgentReviewCard, ReviewV2 } from "./components/v2/ReviewV2";
import { RightPanelHost } from "./components/v2/RightPanel";
import { aiLabel } from "./components/state";
import { TopBar } from "./components/TopBar";
import { VMSetup } from "./components/VMSetup";
import { LinuxVMSetup, MovePrompt } from "./components/RunInVM";
import { WhatsNewDialog } from "./components/WhatsNewDialog";
import { WSLSetup } from "./components/WSLSetup";
import { Notice } from "./components/ui/card";
import { api, isProjectChat } from "./lib/api";
import { t as tNow, useT } from "./lib/i18n";
import { useReadyAlerts } from "./lib/useReadyAlerts";
import { useUiV2 } from "./lib/uiV2";
import { resetChatEvents } from "./lib/chat";
import { onNotification, useConnection } from "./lib/events";
import { chatLabel } from "./lib/agentStatus";
import { reviewHeadKey, questionsKey } from "./lib/inboxPages";
import { mayNotify, type Kind } from "./lib/notifyPrefs";
import { playReadySound } from "./lib/reviewAlert";
import type { AgentPlaceName, ProjectPlaceName } from "./lib/tabs";
import { requestReveal } from "./lib/reveal";
import type { SearchTarget } from "./lib/search";
import { setupCard } from "./lib/setup";
import { useHostTheme } from "./lib/theme";
import { markSeen, shouldShowAutomatically } from "./lib/whatsnew";

export type View =
  | { kind: "home" }
  | { kind: "homeChat" }
  | { kind: "jobs" }
  | { kind: "media" }
  | { kind: "settings" }
  | { kind: "project"; project: string; tab?: ProjectPlaceName }
  | { kind: "agent"; ref: string; tab?: AgentPlaceName }
  // Every project's past agents, or one of them (ref) read in its chat.
  | { kind: "pastAgents"; ref?: string };

// initial is the view the app opens on: Home, except in the dev preview
// (dev/preview.tsx's ?app=), which opens it where a scenario points.
export function App({ initial }: { initial?: View } = {}) {
  const t = useT();
  // UI v2, the lead-first layout (Settings → General → Layout): its own
  // sidebar, with each project's agents under its lead and no agent rail, and
  // the review's decision in the carousel. It switches as the setting does.
  const v2 = useUiV2();
  const [view, setView] = useState<View>(initial ?? { kind: "home" });
  const [tabs, setTabs] = useState<Record<string, AgentPlaceName>>({});
  const [addingProject, setAddingProject] = useState(false);
  const [newAgentProject, setNewAgentProject] = useState<string | null>(null);
  const [whatsNew, setWhatsNew] = useState(false);
  // In a narrow window (below lg, 1024px: a small screen, a portrait monitor,
  // or a phone in the web app), the sidebar slides in over the page from the
  // left; below xl (1280px), the agent rail from the right. Each is a drawer
  // the top bar opens, so the page keeps the width.
  const [navOpen, setNavOpen] = useState(false);
  const [railOpen, setRailOpen] = useState(false);
  useEffect(() => {
    if (!navOpen && !railOpen) return;
    const close = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      setNavOpen(false);
      setRailOpen(false);
    };
    window.addEventListener("keydown", close);
    return () => window.removeEventListener("keydown", close);
  }, [navOpen, railOpen]);
  const agents = useQuery({ queryKey: ["agents"], queryFn: api.agents });
  const connection = useConnection();
  // On a Mac, the daemon lives in AgentBox's Linux VM, and on Windows in its
  // WSL distro: while it can't be reached, ask whether that's because the VM
  // or the distro isn't set up.
  const info = useQuery({
    queryKey: ["app-info"],
    queryFn: () => window.agentbox.info(),
    staleTime: Infinity,
  });
  // Show What's new once after an update: never on the first run, since
  // there's nothing "new" to someone who just installed AgentBox.
  useEffect(() => {
    const version = info.data?.version;
    if (!version) return;
    if (shouldShowAutomatically(version)) setWhatsNew(true);
    markSeen(version);
  }, [info.data?.version]);
  const hostSetup = useQuery({
    queryKey: ["host-setup"],
    queryFn: () => window.agentbox.hostSetup.status(),
    enabled:
      (info.data?.platform === "darwin" ||
        info.data?.platform === "win32" ||
        info.data?.platform === "linux") &&
      connection.state !== "connected",
    refetchInterval: 3_000,
  });
  const setup = setupCard(connection, hostSetup.data);
  const seen = useRef<string | null>(null);
  const queryClient = useQueryClient();
  // AgentBox wears the theme of the desktop it runs on, unless that is turned
  // off in Settings. Here, at the root, because it styles the whole window.
  useHostTheme();

  // Another environment: nothing on screen belongs to it, so start over from its overview.
  useEffect(
    () =>
      window.agentbox.target.onChange(() => {
        seen.current = null;
        setTabs({});
        setView({ kind: "home" });
        queryClient.clear();
        resetChatEvents();
      }),
    [queryClient],
  );

  const select = (next: View) => {
    setSettingsLink(undefined);
    if (next.kind === "agent" && next.tab)
      setTabs((t) => ({ ...t, [next.ref]: next.tab! }));
    setView(next);
    setNavOpen(false);
    setRailOpen(false);
  };

  // A link from inside a page to a section of Settings (openSettingsSection):
  // remember the section and remount Settings so it opens there.
  const [settingsVisit, setSettingsVisit] = useState(0);
  const [settingsLink, setSettingsLink] = useState<string>();
  useEffect(() => {
    const open = (e: Event) => {
      rememberSection((e as CustomEvent<string>).detail);
      setSettingsLink((e as CustomEvent<string>).detail);
      setSettingsVisit((n) => n + 1);
      setView({ kind: "settings" });
      setNavOpen(false);
    };
    window.addEventListener(openSettingsEvent, open);
    return () => window.removeEventListener(openSettingsEvent, open);
  }, []);

  // A link from a chat to another project's, on a message between their leads.
  useEffect(() => {
    const open = (e: Event) => {
      setView({ kind: "project", project: (e as CustomEvent<string>).detail });
      setNavOpen(false);
    };
    window.addEventListener(openProjectEvent, open);
    return () => window.removeEventListener(openProjectEvent, open);
  }, []);

  // A link from a project's chat to one of its agents, on its permission request.
  useEffect(() => {
    const open = (e: Event) => {
      setView({ kind: "agent", ref: (e as CustomEvent<string>).detail, tab: "chat" });
      setNavOpen(false);
    };
    window.addEventListener(openAgentEvent, open);
    return () => window.removeEventListener(openAgentEvent, open);
  }, []);

  // Leave an agent's view once the agent is gone, destroyed here or from the CLI.
  useEffect(() => {
    if (view.kind !== "agent" || !agents.data) return;
    if (agents.data.some((a) => a.ref === view.ref)) {
      seen.current = view.ref;
    } else if (seen.current === view.ref) {
      seen.current = null;
      setView({ kind: "project", project: view.ref.split("/")[0] });
    }
  }, [agents.data, view]);

  // A media item opened from a notification, in a viewer over its agent's
  // Media tab, with where it came from; or from a search, over its agent's
  // items in the project's Media, where an agent that's gone still has them.
  const [viewing, setViewing] = useState<{ ref: string; id: string; from: string; at: string; inProject?: boolean } | null>(null);
  const viewingMedia = useQuery({
    queryKey: viewing?.inProject ? ["projectMedia", ...viewing.ref.split("/")] : ["media", viewing?.ref],
    queryFn: () => {
      if (!viewing!.inProject) return api.media(viewing!.ref);
      const [project, agent] = viewing!.ref.split("/");
      return api.projectMedia(project, agent);
    },
    enabled: viewing !== null,
  });
  const viewingItems = viewingMedia.data ?? [];
  const [deleting, setDeleting] = useState<T.MediaItem | null>(null);
  const viewingIndex = viewing ? viewingItems.findIndex((m) => m.id === viewing.id) : -1;

  // openNotice goes where a notification points, from its toast, its OS
  // notification or the inbox, and marks it seen.
  const openNotice = (n: T.Notification) => {
    void markNoticesSeen(queryClient, { ids: [n.id] });
    select(noticeView(n));
  };
  const openNoticeRef = useRef(openNotice);
  openNoticeRef.current = openNotice;

  // The search modal: Ctrl+K or Ctrl+Shift+F (⌘ on a Mac) anywhere but a
  // terminal, the top bar's button, or openSearch from any component. What it
  // opens is brought forward on the page it opens.
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (!isSearchShortcut(event)) return;
      event.preventDefault();
      toggleSearch();
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, []);
  // UI v2's New task: Ctrl+N (⌘N on a Mac), or the sidebar's first row.
  useEffect(() => {
    if (!v2) return;
    const onKey = (event: KeyboardEvent) => {
      if (!isNewTaskShortcut(event)) return;
      event.preventDefault();
      toggleNewTask();
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, [v2]);
  // v2's other keys (lib/shortcuts.ts): Ctrl+/ for the shortcut sheet, Ctrl+,
  // for Settings, and F6 (Shift+F6 back) to move between the sidebar, the
  // chat and the panel.
  useEffect(() => {
    if (!v2) return;
    const onKey = (event: KeyboardEvent) => {
      if (isSheetShortcut(event)) {
        event.preventDefault();
        toggleSheet();
      } else if (isSettingsShortcut(event)) {
        event.preventDefault();
        setView({ kind: "settings" });
        setNavOpen(false);
      } else if (event.key === "F6" && !event.ctrlKey && !event.metaKey && !event.altKey) {
        if (cycleRegion(event.shiftKey)) event.preventDefault();
      }
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, [v2]);
  const openFound = (target: SearchTarget) => {
    switch (target.open) {
      case "media":
        select({ kind: "project", project: target.project, tab: "media" });
        setViewing({ ref: target.item.agent, id: target.item.id, from: "", at: "", inProject: true });
        return;
      case "settings":
        if (target.reveal) requestReveal(target.reveal);
        openSettingsSection(target.section);
        return;
      case "view":
        if (target.reveal) requestReveal(target.reveal);
        select(target.view);
    }
  };

  // toastNotice shows one in the app, the whole toast a link, and in the OS
  // while the window isn't in front (the bridge decides).
  const toastNotice = (n: T.Notification, onOpen: () => void, kind: Kind = "question") => {
    const projects = queryClient.getQueryData<T.Project[]>(["projects"]);
    const projectName = projectLabel(n.project, projects);
    toast.custom(
      (id) => (
        <NoticeToast
          notice={n}
          projectName={projectName}
          onOpen={() => {
            toast.dismiss(id);
            onOpen();
          }}
        />
      ),
      // Long enough to reach with the pointer: the toast is the way in.
      { id: n.id, duration: 10_000 },
    );
    // A finish's chime is the ready alert's (useReadyAlerts): only a question
    // or an error sounds here.
    if (mayNotify(kind, "sound")) playReadySound();
    if (mayNotify(kind, "system"))
      void window.agentbox.notify({ id: n.id, ...noticeText(n, projectName) });
  };
  const waiting = useRef(new Map<string, () => void>());

  // Tell you when something asks you a question, in any project.
  useEffect(
    () =>
      onNotification((n) =>
        toastNotice(n, () => openNoticeRef.current(n)),
      ),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [],
  );
  // An OS notification clicked: the window is in front again, go there.
  useEffect(
    () =>
      window.agentbox.onNotificationClick((id) => {
        toast.dismiss(id);
        const local = waiting.current.get(id);
        if (local) return local();
        const n = queryClient
          .getQueryData<InfiniteData<T.NotificationPage, string>>(questionsKey)
          ?.pages.flatMap((p) => p.items)
          .find((x) => x.id === id);
        if (n) openNoticeRef.current(n);
      }),
    [queryClient],
  );

  // Tell you when an agent's AI tool waits for your answer while you look elsewhere.
  const chatStates = useRef<Map<string, string | undefined> | null>(null);
  useEffect(() => {
    if (!agents.data) return;
    const before = chatStates.current;
    chatStates.current = new Map(agents.data.map((a) => [a.ref, a.chat]));
    if (!before) return;
    for (const a of agents.data) {
      if (
        a.chat !== "waiting" ||
        before.get(a.ref) === "waiting" ||
        (view.kind === "agent" && view.ref === a.ref)
      )
        continue;
      // Not one the daemon keeps: the agent's own chat says it's waiting
      // until it isn't, so it needs no history.
      const id = `waiting:${a.ref}:${Date.now()}`;
      // v2 answers it from the project's chat, where its card is (#323) and
      // the lead knows about it; v1 in the agent's own.
      const open = () => {
        waiting.current.delete(id);
        select(v2 && !isProjectChat(a.ref) ? { kind: "project", project: a.project, tab: "chat" } : { kind: "agent", ref: a.ref, tab: "chat" });
      };
      waiting.current.set(id, open);
      toastNotice(
        {
          id,
          kind: "question",
          project: a.project,
          agent: a.name,
          ref: a.ref,
          title: a.title,
          text: tNow("shell.app.asksPermission", { ai: aiLabel(a.ai) }),
          at: new Date().toISOString(),
        },
        open,
      );
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [agents.data]);

  // Tell you when an agent stops on an error while you look elsewhere: one
  // that failed and finished is in Ready to review, which tells you itself.
  const errored = useRef<Set<string> | null>(null);
  useEffect(() => {
    if (!agents.data) return;
    const before = errored.current;
    const inReview = new Set(queryClient.getQueryData<T.ReviewPage>(reviewHeadKey)?.items.map((i) => i.ref));
    errored.current = new Set(agents.data.filter((a) => chatLabel(a).tone === "error" && !inReview.has(a.ref)).map((a) => a.ref));
    if (!before) return;
    for (const a of agents.data) {
      if (!errored.current.has(a.ref) || before.has(a.ref) || (view.kind === "agent" && view.ref === a.ref)) continue;
      const open = () => select({ kind: "agent", ref: a.ref, tab: "chat" });
      toastNotice(
        {
          id: `error:${a.ref}:${Date.now()}`,
          kind: "error",
          project: a.project,
          agent: a.name,
          ref: a.ref,
          title: a.title,
          text: chatLabel(a).text,
          at: new Date().toISOString(),
        },
        open,
        "error",
      );
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [agents.data]);

  // Tell you when an agent newly lands in Ready to review: the chime and the
  // "new" marks are the hook's (lib/useReadyAlerts.ts); the OS notification,
  // shown only while the window isn't in front, is here.
  useReadyAlerts((items) => {
    const first = items[0];
    const id = `ready:${first.ref}:${Date.now()}`;
    waiting.current.set(id, () => {
      waiting.current.delete(id);
      select({ kind: "agent", ref: first.ref, tab: "chat" });
    });
    const projects = queryClient.getQueryData<T.Project[]>(["projects"]);
    if (!mayNotify("review", "system")) {
      waiting.current.delete(id);
      return;
    }
    void window.agentbox
      .notify({
        id,
        title: tNow("review.notice.title"),
        body:
          items.length === 1
            ? tNow("review.notice.one", {
                agent: first.title || first.agent,
                project: projectLabel(first.project, projects),
              })
            : tNow("review.notice.many", { count: items.length }),
      })
      .then((shown) => {
        if (!shown) waiting.current.delete(id);
      });
  });

  const viewKey = view.kind === "agent"
      ? view.ref
      : view.kind === "project"
        ? `project:${view.project}:${view.tab ?? ""}`
        : view.kind === "settings"
          ? `settings:${settingsVisit}`
          : view.kind === "pastAgents"
            ? `pastAgents:${view.ref ?? ""}`
            : view.kind;

  const shell = (
    <div className="flex h-full">
      <div className="hidden lg:flex">
        {v2 ? (
          <SidebarV2
            view={view}
            onSelect={select}
            onAddProject={() => setAddingProject(true)}
            onNewAgent={setNewAgentProject}
          />
        ) : (
          <Sidebar
            view={view}
            onSelect={select}
            onAddProject={() => setAddingProject(true)}
            onNewAgent={setNewAgentProject}
          />
        )}
      </div>
      {navOpen && (
        <div className="fixed inset-0 z-40 flex lg:hidden" data-nav-drawer>
          <div
            className="absolute inset-0 animate-fade-in bg-scrim backdrop-blur-sm"
            onClick={() => setNavOpen(false)}
          />
          <div className="relative flex h-full max-w-[85vw] animate-fade-in bg-modal shadow-[20px_0_60px_-20px_var(--ab-shadow-deep)]">
            {v2 ? (
              <SidebarV2
                view={view}
                onSelect={select}
                onAddProject={() => {
                  setNavOpen(false);
                  setAddingProject(true);
                }}
                onNewAgent={(project) => {
                  setNavOpen(false);
                  setNewAgentProject(project);
                }}
              />
            ) : (
              <Sidebar
                view={view}
                onSelect={select}
                onAddProject={() => {
                  setNavOpen(false);
                  setAddingProject(true);
                }}
                onNewAgent={(project) => {
                  setNavOpen(false);
                  setNewAgentProject(project);
                }}
              />
            )}
          </div>
        </div>
      )}
      <div className="flex min-w-0 flex-1 flex-col">
        <TopBar
          view={view}
          onSelect={select}
          onOpenNav={() => setNavOpen(true)}
          onOpenRail={v2 ? undefined : () => setRailOpen(true)}
          onOpenNotice={openNotice}
          onSearch={() => openSearch()}
        />
        <div className="flex min-h-0 min-w-0 flex-1">
          <main className="flex min-h-0 min-w-0 flex-1 flex-col">
            {/* Until the VM or the distro exists there's no daemon behind
                any page, so the setup is the page, not a card over an empty
                dashboard whose buttons can't do anything yet. */}
            {setup ? (
              <div className="flex min-h-0 flex-1 justify-center overflow-y-auto px-4 py-10 md:px-6">
                <div className="w-full max-w-2xl">
                  {setup.kind === "vm" ? (
                    <VMSetup vm={setup.vm} />
                  ) : setup.kind === "linux" ? (
                    <LinuxVMSetup linux={setup.linux} />
                  ) : (
                    <WSLSetup wsl={setup.wsl} />
                  )}
                </div>
              </div>
            ) : (
              connection.state === "disconnected" && (
                <div className="px-4 pt-4 md:px-6">
                  <Notice>
                    {t("shell.app.daemonUnreachable", {
                      error: connection.error ?? "",
                    })}
                  </Notice>
                </div>
              )
            )}
            {!setup && (
              <div key={viewKey} className="min-h-0 flex-1 animate-fade-in">
                {view.kind === "home" &&
                  (v2 ? (
                    <OverviewV2
                      onSelect={select}
                      onAddProject={() => setAddingProject(true)}
                      onNewAgent={() => setNewAgentProject("")}
                    />
                  ) : (
                    <HomeView
                      onSelect={select}
                      onAddProject={() => setAddingProject(true)}
                      onNewAgent={() => setNewAgentProject("")}
                    />
                  ))}
                {view.kind === "homeChat" && (
                  <HomeChatPanel onOverview={v2 ? () => select({ kind: "home" }) : undefined} />
                )}
                {view.kind === "jobs" && <JobsView />}
                {view.kind === "media" && <AllMediaView onSelect={select} />}
                {view.kind === "pastAgents" &&
                  (v2 ? (
                    <PastAgentsV2 open={view.ref} onSelect={select} />
                  ) : (
                    <PastAgents
                      open={view.ref}
                      onOpen={(ref) => select({ kind: "pastAgents", ref: ref ?? undefined })}
                      onSelect={select}
                    />
                  ))}
                {view.kind === "settings" && (
                  <SettingsView onHome={() => select({ kind: "home" })} linked={settingsLink} />
                )}
                {view.kind === "project" && (
                  <ProjectView
                    name={view.project}
                    tab={view.tab}
                    onSelect={select}
                    onNewAgent={() => setNewAgentProject(view.project)}
                  />
                )}
                {view.kind === "agent" && (
                  <AgentView
                    agentRef={view.ref}
                    tab={tabs[view.ref]}
                    onTab={(tab) => setTabs((t) => ({ ...t, [view.ref]: tab }))}
                    onSelect={select}
                    chatCard={v2 ? (agent) => <AgentReviewCard agent={agent} /> : undefined}
                  />
                )}
              </div>
            )}
          </main>
          {/* v2's right-hand panel: files, desktop, terminal, browser,
              pages and media, beside the chat or over it. */}
          {v2 && !setup && <RightPanelHost view={view} onSelect={select} />}
          {!v2 && (view.kind === "project" || view.kind === "agent") && (
            <div className="hidden xl:flex">
              <AgentRail
                view={view}
                onSelect={select}
                onNewAgent={setNewAgentProject}
              />
            </div>
          )}
          {!v2 && railOpen && (view.kind === "project" || view.kind === "agent") && (
            <div className="fixed inset-0 z-40 flex justify-end xl:hidden" data-rail-drawer>
              <div
                className="absolute inset-0 animate-fade-in bg-scrim backdrop-blur-sm"
                onClick={() => setRailOpen(false)}
              />
              <div className="relative flex h-full max-w-[85vw] animate-fade-in bg-modal shadow-[-20px_0_60px_-20px_var(--ab-shadow-deep)]">
                <AgentRail
                  view={view}
                  onSelect={select}
                  onNewAgent={(project) => {
                    setRailOpen(false);
                    setNewAgentProject(project);
                  }}
                  onClose={() => setRailOpen(false)}
                />
              </div>
            </div>
          )}
        </div>
      </div>
      <AddProjectDialog
        open={addingProject}
        onOpenChange={setAddingProject}
        onAdded={(project) =>
          select({ kind: "project", project: project.name })
        }
        onOpenSetup={() => {
          setAddingProject(false);
          select({ kind: "settings" });
        }}
      />
      <NewAgentDialog
        project={newAgentProject}
        onClose={() => setNewAgentProject(null)}
        onCreated={(ref) => select({ kind: "agent", ref })}
      />
      <MediaViewer
        items={viewingItems}
        index={viewingIndex}
        onIndex={(i) =>
          viewingItems[i] &&
          setViewing((v) => v && { ...v, id: viewingItems[i].id })
        }
        onClose={() => setViewing(null)}
        onDelete={setDeleting}
        context={(item) => (
          <MediaPlace
            item={item}
            fromNotice={item.id === viewing?.from ? viewing.at : undefined}
            onSelect={(next) => {
              setViewing(null);
              select(next);
            }}
          />
        )}
      />
      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => !open && setDeleting(null)}
        title={t("agent.mediaTab.deleteTitle", { name: deleting?.name ?? "" })}
        description={t("agent.mediaTab.deleteDescription")}
        confirmLabel={t("common.delete")}
        destructive
        onConfirm={async () => {
          await api.deleteMedia(deleting!.id);
          setViewing(null);
        }}
      />
      <PagePreviewHost onSelect={select} />
      <PageArrivals
        onNotified={(id, open) =>
          waiting.current.set(id, () => {
            waiting.current.delete(id);
            open();
          })
        }
      />
      <SearchModal onOpen={openFound} />
      {v2 && <NewTaskDialog onSelect={select} />}
      {v2 && <ShortcutSheet />}
      {/* The first-run tour, and What's new after an update (lib/tours.ts). */}
      {v2 && !setup && connection.state === "connected" && <TourHost view={view} onSelect={select} />}
      <ErrorReports />
      <SnapComposer
        project={view.kind === "project" ? view.project : view.kind === "agent" ? view.ref.split("/")[0] : undefined}
        onSent={(project, agent) =>
          select(agent ? { kind: "agent", ref: `${project}/${agent}` } : { kind: "project", project })
        }
      />
      {info.data && (
        <WhatsNewDialog
          open={whatsNew}
          onOpenChange={setWhatsNew}
          version={info.data.version}
        />
      )}
      {/* A Linux machine still running agents itself is told once after each
          update that AgentBox runs in a VM now, after What's new. */}
      {info.data?.platform === "linux" &&
        connection.state === "connected" &&
        !whatsNew && (
          <MovePrompt
            version={info.data.version}
            onOpen={() => select({ kind: "settings" })}
          />
        )}
    </div>
  );
  return v2 ? <ReviewV2 onSelect={select}>{shell}</ReviewV2> : shell;
}
