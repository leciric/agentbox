import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { AddProjectDialog } from "./components/AddProjectDialog";
import { AgentRail } from "./components/AgentRail";
import { AgentView, type AgentTab } from "./components/AgentView";
import { HomeView } from "./components/HomeView";
import { JobsView } from "./components/JobsView";
import { NewAgentDialog } from "./components/NewAgentDialog";
import { ProjectView } from "./components/ProjectView";
import { SettingsView } from "./components/SettingsView";
import { Sidebar } from "./components/Sidebar";
import { aiLabel } from "./components/state";
import { TopBar } from "./components/TopBar";
import { VMSetup } from "./components/VMSetup";
import { WSLSetup } from "./components/WSLSetup";
import { Notice } from "./components/ui/card";
import { api } from "./lib/api";
import { resetChatEvents } from "./lib/chat";
import { onMedia, useConnection } from "./lib/events";
import { setupCard } from "./lib/setup";
import { kindInfo } from "./lib/media";
import { useHostTheme } from "./lib/theme";

export type View =
  | { kind: "home" }
  | { kind: "jobs" }
  | { kind: "settings" }
  | { kind: "project"; project: string }
  | { kind: "agent"; ref: string; tab?: AgentTab };

export function App() {
  const [view, setView] = useState<View>({ kind: "home" });
  const [tabs, setTabs] = useState<Record<string, AgentTab>>({});
  const [addingProject, setAddingProject] = useState(false);
  const [newAgentProject, setNewAgentProject] = useState<string | null>(null);
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
  const hostSetup = useQuery({
    queryKey: ["host-setup"],
    queryFn: () => window.agentbox.hostSetup.status(),
    enabled:
      (info.data?.platform === "darwin" || info.data?.platform === "win32") &&
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

  // Tell you when an agent keeps something in its media.
  useEffect(
    () =>
      onMedia((item) => {
        if (item.removed || item.source !== "agent") return;
        const agent = agents.data?.find((a) => a.ref === item.agent);
        toast(
          `${agent?.title || item.agent} saved a ${kindInfo(item.kind).one}`,
          {
            description: item.name,
            action: {
              label: "View",
              onClick: () =>
                select({ kind: "agent", ref: item.agent, tab: "media" }),
            },
          },
        );
      }),
    [agents.data],
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
      toast(`${a.title || a.ref} is waiting for you`, {
        description: `${aiLabel(a.ai)} asks for permission.`,
        action: {
          label: "Open",
          onClick: () => select({ kind: "agent", ref: a.ref, tab: "chat" }),
        },
      });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [agents.data]);

  const viewKey =
    view.kind === "agent"
      ? view.ref
      : view.kind === "project"
        ? `project:${view.project}`
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
        />
        <div className="flex min-h-0 min-w-0 flex-1">
          <main className="flex min-h-0 min-w-0 flex-1 flex-col">
            {setup?.kind === "vm" ? (
              <div className="px-4 pt-4 md:px-6">
                <VMSetup vm={setup.vm} />
              </div>
            ) : setup?.kind === "wsl" ? (
              <div className="px-4 pt-4 md:px-6">
                <WSLSetup wsl={setup.wsl} />
              </div>
            ) : (
              connection.state === "disconnected" && (
                <div className="px-4 pt-4 md:px-6">
                  <Notice>
                    The AgentBox daemon isn't reachable: {connection.error}.
                    Retrying…
                  </Notice>
                </div>
              )
            )}
            <div key={viewKey} className="min-h-0 flex-1 animate-fade-in">
              {view.kind === "home" && (
                <HomeView
                  onSelect={select}
                  onAddProject={() => setAddingProject(true)}
                  onNewAgent={() => setNewAgentProject("")}
                />
              )}
              {view.kind === "jobs" && <JobsView />}
              {view.kind === "settings" && (
                <SettingsView onHome={() => select({ kind: "home" })} />
              )}
              {view.kind === "project" && (
                <ProjectView
                  name={view.project}
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
    </div>
  );
}
