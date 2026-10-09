import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { AddProjectDialog } from "./components/AddProjectDialog";
import { AgentRail } from "./components/AgentRail";
import { AgentView } from "./components/AgentView";
import { openSettingsEvent, openSettingsSection, rememberSection } from "./components/SettingsPage";
import { isSearchShortcut, SearchPalette } from "./components/SearchPalette";
import { ErrorReports } from "./components/ErrorReports";
import { SnapComposer } from "./components/SnapComposer";
import { HomeChatPanel } from "./components/HomeChatPanel";
import { HomeView } from "./components/HomeView";
import { AllMediaView } from "./components/AllMediaView";
import { ConfirmDialog } from "./components/ConfirmDialog";
import { MediaPlace } from "./components/MediaPlace";
import { MediaViewer } from "./components/MediaTab";
import { NoticeToast } from "./components/Notifications";
import { markSeen as markNoticesSeen, noticeText, noticeView } from "./lib/notifications";
import { projectLabel } from "./lib/projectName";
import type * as T from "../shared/api";
import { JobsView } from "./components/JobsView";
import { NewAgentDialog } from "./components/NewAgentDialog";
import { ProjectView } from "./components/ProjectView";
import { SettingsView } from "./components/SettingsView";
import { Sidebar } from "./components/Sidebar";
import { aiLabel } from "./components/state";
import { TopBar } from "./components/TopBar";
import { VMSetup } from "./components/VMSetup";
import { LinuxVMSetup, MovePrompt } from "./components/RunInVM";
import { WhatsNewDialog } from "./components/WhatsNewDialog";
import { WSLSetup } from "./components/WSLSetup";
import { Notice } from "./components/ui/card";
import { api } from "./lib/api";
import { t as tNow, useT } from "./lib/i18n";
import { resetChatEvents } from "./lib/chat";
import { onNotification, useConnection } from "./lib/events";
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
  | { kind: "agent"; ref: string; tab?: AgentPlaceName };

export function App() {
  const t = useT();
  const [view, setView] = useState<View>({ kind: "home" });
  const [tabs, setTabs] = useState<Record<string, AgentPlaceName>>({});
  const [addingProject, setAddingProject] = useState(false);
  const [newAgentProject, setNewAgentProject] = useState<string | null>(null);
  const [whatsNew, setWhatsNew] = useState(false);
  // On a narrow screen (a phone, in the web app), the sidebar slides in over the page.
  const [navOpen, setNavOpen] = useState(false);
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
    if (next.kind === "agent" && next.tab)
      setTabs((t) => ({ ...t, [next.ref]: next.tab! }));
    setView(next);
    setNavOpen(false);
  };

  // A link from inside a page to a section of Settings (openSettingsSection):
  // remember the section and remount Settings so it opens there.
  const [settingsVisit, setSettingsVisit] = useState(0);
  useEffect(() => {
    const open = (e: Event) => {
      rememberSection((e as CustomEvent<string>).detail);
      setSettingsVisit((n) => n + 1);
      setView({ kind: "settings" });
      setNavOpen(false);
    };
    window.addEventListener(openSettingsEvent, open);
    return () => window.removeEventListener(openSettingsEvent, open);
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
  // notification or the bell, and marks it seen.
  const openNotice = (n: T.Notification) => {
    void markNoticesSeen(queryClient, { ids: [n.id] });
    const next = noticeView(n, agents.data);
    select(next);
    if (n.media && !n.media.removed && next.kind === "agent")
      setViewing({ ref: n.ref, id: n.media.id, from: n.media.id, at: n.at });
  };
  const openNoticeRef = useRef(openNotice);
  openNoticeRef.current = openNotice;

  // The search palette: Ctrl+K (⌘K) anywhere but a terminal, or the top bar's
  // button. What it opens is brought forward on the page it opens.
  const [searching, setSearching] = useState(false);
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (!isSearchShortcut(event)) return;
      event.preventDefault();
      setSearching((open) => !open);
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, []);
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
  const toastNotice = (n: T.Notification, onOpen: () => void) => {
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
    void window.agentbox.notify({ id: n.id, ...noticeText(n, projectName) });
  };
  const waiting = useRef(new Map<string, () => void>());

  // Tell you when an agent finishes, asks you something or keeps a
  // screenshot or recording, in any project.
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
          .getQueryData<T.Notification[]>(["notifications"])
          ?.find((x) => x.id === id);
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
      const open = () => {
        waiting.current.delete(id);
        select({ kind: "agent", ref: a.ref, tab: "chat" });
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

  const viewKey =
    view.kind === "agent"
      ? view.ref
      : view.kind === "project"
        ? `project:${view.project}:${view.tab ?? ""}`
        : view.kind === "settings"
          ? `settings:${settingsVisit}`
          : view.kind;

  return (
    <div className="flex h-full">
      <div className="hidden md:flex">
        <Sidebar
          view={view}
          onSelect={select}
          onAddProject={() => setAddingProject(true)}
          onNewAgent={setNewAgentProject}
        />
      </div>
      {navOpen && (
        <div className="fixed inset-0 z-40 flex md:hidden" data-nav-drawer>
          <div
            className="absolute inset-0 animate-fade-in bg-scrim backdrop-blur-sm"
            onClick={() => setNavOpen(false)}
          />
          <div className="relative flex h-full max-w-[85vw] animate-fade-in bg-modal shadow-[20px_0_60px_-20px_var(--ab-shadow-deep)]">
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
          </div>
        </div>
      )}
      <div className="flex min-w-0 flex-1 flex-col">
        <TopBar
          view={view}
          onSelect={select}
          onOpenNav={() => setNavOpen(true)}
          onNewAgent={setNewAgentProject}
          onOpenNotice={openNotice}
          onSearch={() => setSearching(true)}
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
                {view.kind === "home" && (
                  <HomeView
                    onSelect={select}
                    onAddProject={() => setAddingProject(true)}
                    onNewAgent={() => setNewAgentProject("")}
                  />
                )}
                {view.kind === "homeChat" && <HomeChatPanel />}
                {view.kind === "jobs" && <JobsView />}
                {view.kind === "media" && <AllMediaView onSelect={select} />}
                {view.kind === "settings" && (
                  <SettingsView onHome={() => select({ kind: "home" })} />
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
                  />
                )}
              </div>
            )}
          </main>
          {(view.kind === "project" || view.kind === "agent") && (
            <div className="hidden lg:flex">
              <AgentRail
                view={view}
                onSelect={select}
                onNewAgent={setNewAgentProject}
              />
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
      <SearchPalette open={searching} onOpenChange={setSearching} onOpen={openFound} />
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
}
