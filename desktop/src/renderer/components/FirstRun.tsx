import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowRight, CircleCheck, CircleDashed, FolderGit2, GitFork, Lock, LoaderCircle, Plus, Search } from "lucide-react";
import { useEffect, useRef, useState, type ReactNode } from "react";
import type { ConnectionState } from "../../preload";
import * as T from "../../shared/api";
import { api } from "../lib/api";
import {
  filterRepos,
  firstRunStep,
  firstRunSteps,
  firstRunTools,
  isFirstRunTool,
  stepDone,
  type FirstRunMark,
  type FirstRunProgress,
  type FirstRunStep,
  type FirstRunTool,
} from "../lib/firstRun";
import { useT, type MessageKey } from "../lib/i18n";
import type { SetupCard } from "../lib/setup";
import { cn, errorMessage, timeAgo } from "../lib/utils";
import { DoctorPanel } from "./DoctorPanel";
import { JobProgress } from "./JobProgress";
import { LinuxVMSetup } from "./RunInVM";
import { GitHubAccounts, StepIcon, usable, useSetupSteps, type Step } from "./SettingsView";
import { StartingNotice } from "./StartingNotice";
import { aiLabel, ToolMark } from "./state";
import { Button } from "./ui/button";
import { Notice, Panel } from "./ui/card";
import { Input } from "./ui/input";
import { VMSetup } from "./VMSetup";
import { WSLSetup } from "./WSLSetup";

// The first run's own record, beside the daemon's (Settings.firstRun): see
// FirstRunMark. The tool is kept here too, chosen before there is a daemon to
// tell, and told once there is.
const markKey = "agentbox.firstRun";
const toolKey = "agentbox.firstRun.tool";

function read(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

function write(key: string, value: string | null) {
  try {
    if (value === null) localStorage.removeItem(key);
    else localStorage.setItem(key, value);
  } catch {
    // Private storage: the daemon's record still ends the first run.
  }
}

export function firstRunMark(): FirstRunMark {
  const v = read(markKey);
  return v === "started" || v === "done" ? v : null;
}

export function setFirstRunMark(mark: FirstRunMark) {
  write(markKey, mark);
}

const titles: Record<FirstRunStep, MessageKey> = {
  tool: "firstRun.tool.title",
  machine: "firstRun.machine.title",
  login: "firstRun.login.title",
  github: "firstRun.github.title",
  project: "firstRun.project.title",
  check: "firstRun.check.title",
};

// What account each tool needs, in one line.
const needs: Record<FirstRunTool, MessageKey> = {
  claude: "firstRun.tool.needs.claude",
  codex: "firstRun.tool.needs.codex",
  opencode: "firstRun.tool.needs.opencode",
  cursor: "firstRun.tool.needs.cursor",
  gemini: "firstRun.tool.needs.gemini",
};

// The steps of Setup the machine step waits for: everything agents can't
// run without.
const machineIds = new Set(["cli", "incus", "host", "image", "storage"]);

// FirstRun is the app's first screen on a new installation: one page that
// goes from choosing an AI tool to a first project, each step skipped when
// it's done already, and ends on the doctor. Before there is a daemon it can
// only choose the tool and make the VM (or distro); the rest waits for it.
export function FirstRun({
  connection,
  setup,
  windows,
  onFinish,
}: {
  connection: ConnectionState;
  // setup is the VM, the distro or the Linux VM still to make, as App
  // decides it (setupCard).
  setup: SetupCard;
  windows: boolean;
  // onFinish leaves it, at the project it cloned when it did.
  onFinish: (project?: string) => void;
}) {
  const [tool, setTool] = useState<FirstRunTool | undefined>(() => {
    const kept = read(toolKey) ?? undefined;
    return isFirstRunTool(kept) ? kept : undefined;
  });
  const choose = (next: FirstRunTool) => {
    write(toolKey, next);
    setTool(next);
  };
  useEffect(() => {
    if (firstRunMark() === null) setFirstRunMark("started");
  }, []);

  if (connection.state !== "connected") {
    const progress: FirstRunProgress = { tool, machine: false, login: false, github: false, project: false };
    return (
      <Frame progress={progress} onFinish={() => onFinish()}>
        {!tool ? (
          <ToolStep onChoose={choose} />
        ) : (
          <StepPanel step="machine">
            {setup?.kind === "vm" ? (
              <VMSetup vm={setup.vm} />
            ) : setup?.kind === "linux" ? (
              <LinuxVMSetup linux={setup.linux} />
            ) : setup?.kind === "wsl" ? (
              <WSLSetup wsl={setup.wsl} />
            ) : connection.starting ? (
              <StartingNotice starting={connection.starting} windows={windows} />
            ) : (
              <WaitingForDaemon error={connection.state === "disconnected" ? connection.error : undefined} />
            )}
          </StepPanel>
        )}
      </Frame>
    );
  }
  return <Connected tool={tool} onChoose={choose} onFinish={onFinish} />;
}

function WaitingForDaemon({ error }: { error?: string }) {
  const t = useT();
  return (
    <div className="grid gap-3">
      <p className="flex items-center gap-2 text-ui text-muted">
        <LoaderCircle className="size-4 animate-spin text-subtle" /> {t("firstRun.machine.waiting")}
      </p>
      {error && <p className="break-words text-xs text-subtle">{error}</p>}
    </div>
  );
}

// Connected is the first run once the daemon answers: everything it asks
// about comes from there.
function Connected({
  tool: chosen,
  onChoose,
  onFinish,
}: {
  tool?: FirstRunTool;
  onChoose: (tool: FirstRunTool) => void;
  onFinish: (project?: string) => void;
}) {
  const t = useT();
  const queryClient = useQueryClient();
  const { steps, setup } = useSetupSteps();
  const settings = useQuery({ queryKey: ["settings"], queryFn: api.settings });
  const auth = useQuery({ queryKey: ["auth"], queryFn: api.auth, refetchInterval: 5_000 });
  const projects = useQuery({ queryKey: ["projects"], queryFn: api.projects });
  const [skipped, setSkipped] = useState<Set<FirstRunStep>>(() => new Set());
  const [cloned, setCloned] = useState<string>();
  // A step opened from the rail, to look at or change, over the one the
  // first run is on.
  const [viewing, setViewing] = useState<FirstRunStep>();

  // The tool is the installation's default from here (Settings'
  // lastAgentPick): the Home chat, every project's chat and new agents start
  // on it. Chosen before the daemon answered, it is told now, once.
  const saved = settings.data?.lastAgentPick.ai;
  const tool = chosen ?? (isFirstRunTool(saved) ? saved : undefined);
  const told = useRef<string | undefined>(undefined);
  const tell = useMutation({
    mutationFn: (ai: FirstRunTool) => api.updateSettings({ lastAgentPick: { ai, model: "", effort: "" } }),
    onSuccess: (updated) => queryClient.setQueryData(["settings"], updated),
  });
  useEffect(() => {
    if (!chosen || !settings.data || saved === chosen || told.current === chosen) return;
    told.current = chosen;
    tell.mutate(chosen);
  }, [chosen, saved, settings.data, tell]);

  const machineSteps = steps.filter((s) => machineIds.has(s.id) && !s.optional);
  const loginStep = tool ? steps.find((s) => s.id === tool) : undefined;
  // A first base image still downloading holds the step, with its progress,
  // unless you go on while it does: agents wait for it, signing in doesn't.
  const building = machineSteps.some((s) => s.status === "building");
  const loaded = settings.data !== undefined && auth.data !== undefined && projects.data !== undefined && setup.data !== undefined;
  const progress: FirstRunProgress = {
    tool,
    machine: loaded && machineSteps.every((s) => usable(s.status)) && (!building || skipped.has("machine")),
    login: loginStep !== undefined && loginStep.status !== "optional" && usable(loginStep.status),
    github: !!auth.data?.github || skipped.has("github"),
    project: (projects.data?.length ?? 0) > 0 || skipped.has("project"),
  };
  const at = firstRunStep(progress);
  const step = viewing && (stepDone(viewing, progress) || viewing === at) ? viewing : at;
  const skip = (s: FirstRunStep) => {
    setViewing(undefined);
    setSkipped((prev) => new Set(prev).add(s));
  };

  const finish = useMutation({
    mutationFn: () => api.updateSettings({ firstRunDone: true }),
    onSettled: (updated) => {
      setFirstRunMark("done");
      if (updated) queryClient.setQueryData(["settings"], updated);
      onFinish(cloned ?? projects.data?.[0]?.name);
    },
  });

  if (!loaded) {
    return (
      <Frame progress={progress} onFinish={() => finish.mutate()}>
        <div className="flex justify-center py-10">
          <LoaderCircle className="size-5 animate-spin text-subtle" />
        </div>
      </Frame>
    );
  }

  return (
    <Frame progress={progress} current={step} onOpen={setViewing} onFinish={() => finish.mutate()}>
      {step === "tool" && (
        <ToolStep
          initial={tool}
          onChoose={(next) => {
            setViewing(undefined);
            onChoose(next);
          }}
        />
      )}
      {step === "machine" && (
        <StepPanel
          step="machine"
          footer={
            progress.machine ? (
              <NextButton onClick={() => setViewing(undefined)} />
            ) : (
              building &&
              machineSteps.every((s) => usable(s.status)) && (
                <Button variant="secondary" onClick={() => skip("machine")}>
                  {t("firstRun.machine.continue")}
                  <ArrowRight />
                </Button>
              )
            )
          }
        >
          <StepList steps={machineSteps} />
        </StepPanel>
      )}
      {step === "login" && tool && (
        <StepPanel
          step="login"
          title={t("firstRun.login.titleFor", { tool: aiLabel(tool) })}
          footer={progress.login && <NextButton onClick={() => setViewing(undefined)} />}
        >
          <LoginStep tool={tool} step={loginStep} />
        </StepPanel>
      )}
      {step === "github" && (
        <StepPanel
          step="github"
          footer={
            progress.github ? (
              <NextButton onClick={() => setViewing(undefined)} />
            ) : (
              <Button variant="ghost" onClick={() => skip("github")}>
                {t("firstRun.skip")}
              </Button>
            )
          }
        >
          <GitHubAccounts accounts={auth.data?.githubAccounts ?? []} />
        </StepPanel>
      )}
      {step === "project" && (
        <StepPanel
          step="project"
          footer={
            (projects.data?.length ?? 0) > 0 ? (
              <NextButton onClick={() => setViewing(undefined)} />
            ) : (
              <Button variant="ghost" onClick={() => skip("project")}>
                {t("firstRun.skip")}
              </Button>
            )
          }
        >
          <ProjectStep
            github={!!auth.data?.github}
            projects={projects.data ?? []}
            onCloned={(name) => {
              setCloned(name);
              setViewing(undefined);
            }}
          />
        </StepPanel>
      )}
      {step === "check" && (
        <StepPanel
          step="check"
          footer={
            <Button variant="primary" className="ml-auto" disabled={finish.isPending} onClick={() => finish.mutate()}>
              {finish.isPending ? <LoaderCircle className="animate-spin" /> : null}
              {cloned || (projects.data?.length ?? 0) > 0 ? t("firstRun.check.openProject") : t("firstRun.check.goHome")}
              <ArrowRight />
            </Button>
          }
        >
          <DoctorPanel />
        </StepPanel>
      )}
    </Frame>
  );
}

// Frame is the page around the step: a heading, the steps in a rail, and a
// way out for someone who'd rather set things up from Settings.
function Frame({
  progress,
  current,
  onOpen,
  onFinish,
  children,
}: {
  progress: FirstRunProgress;
  current?: FirstRunStep;
  onOpen?: (step: FirstRunStep) => void;
  onFinish: () => void;
  children: ReactNode;
}) {
  const t = useT();
  const at = current ?? firstRunStep(progress);
  return (
    <div className="h-full overflow-y-auto" data-first-run={at}>
      <div className="mx-auto max-w-4xl px-4 py-6 md:px-8 md:py-9">
        <div className="flex flex-wrap items-end gap-4">
          <div className="min-w-0">
            <h1 className="text-display font-semibold tracking-tight text-title">{t("firstRun.heading")}</h1>
            <p className="mt-1 text-sm text-muted">{t("firstRun.intro")}</p>
          </div>
          <Button variant="ghost" size="sm" className="ml-auto" onClick={onFinish}>
            {t("firstRun.later")}
          </Button>
        </div>
        <div className="mt-6 grid gap-5 md:grid-cols-[13rem_minmax(0,1fr)]">
          <nav aria-label={t("firstRun.steps")} className="md:sticky md:top-0 md:self-start">
            <ol className="grid gap-0.5">
              {firstRunSteps.map((s, i) => {
                const done = stepDone(s, progress);
                const active = s === at;
                const reachable = !!onOpen && (done || s === firstRunStep(progress));
                return (
                  <li key={s}>
                    <button
                      type="button"
                      disabled={!reachable}
                      onClick={() => onOpen?.(s)}
                      data-first-run-step={s}
                      data-done={done || undefined}
                      aria-current={active ? "step" : undefined}
                      className={cn(
                        "flex w-full items-center gap-2.5 rounded-lg px-2 py-1.5 text-left text-ui transition",
                        active ? "bg-surface-raised text-primary" : "text-muted hover:bg-surface hover:text-secondary",
                        !reachable && !active && "hover:bg-transparent hover:text-muted",
                        !done && !active && "opacity-60",
                      )}
                    >
                      {done ? (
                        <CircleCheck className="size-4 shrink-0 text-emerald-400" />
                      ) : active ? (
                        <span className="flex size-4 shrink-0 items-center justify-center rounded-full bg-brand-500 text-[10px] font-semibold text-on-brand">
                          {i + 1}
                        </span>
                      ) : (
                        <CircleDashed className="size-4 shrink-0 text-subtle" />
                      )}
                      <span className="min-w-0 truncate">{t(titles[s])}</span>
                    </button>
                  </li>
                );
              })}
            </ol>
          </nav>
          <div className="min-w-0">{children}</div>
        </div>
      </div>
    </div>
  );
}

function StepPanel({ step, title, footer, children }: { step: FirstRunStep; title?: string; footer?: ReactNode; children: ReactNode }) {
  const t = useT();
  return (
    <div className="grid gap-4">
      <Panel className="p-5" data-first-run-panel={step}>
        <h2 className="text-heading font-semibold text-title">{title ?? t(titles[step])}</h2>
        <p className="mt-1 text-sm text-muted">{t(`firstRun.${step}.intro` as MessageKey)}</p>
        <div className="mt-4">{children}</div>
      </Panel>
      {footer && <div className="flex flex-wrap items-center justify-end gap-2">{footer}</div>}
    </div>
  );
}

function NextButton({ onClick }: { onClick: () => void }) {
  const t = useT();
  return (
    <Button variant="primary" onClick={onClick}>
      {t("common.next")}
      <ArrowRight />
    </Button>
  );
}

// ToolStep chooses the AI tool, Claude Code unless another is picked.
function ToolStep({ initial, onChoose }: { initial?: FirstRunTool; onChoose: (tool: FirstRunTool) => void }) {
  const t = useT();
  const [picked, setPicked] = useState<FirstRunTool>(initial ?? "claude");
  return (
    <StepPanel
      step="tool"
      footer={
        <Button variant="primary" onClick={() => onChoose(picked)}>
          {t("firstRun.tool.use", { tool: aiLabel(picked) })}
          <ArrowRight />
        </Button>
      }
    >
      <div role="radiogroup" aria-label={t("firstRun.tool.title")} className="grid gap-2 sm:grid-cols-2">
        {firstRunTools.map((tool) => (
          <button
            key={tool}
            type="button"
            role="radio"
            aria-checked={picked === tool}
            data-first-run-tool={tool}
            onClick={() => setPicked(tool)}
            className={cn(
              "flex min-w-0 items-start gap-3 rounded-xl border p-3.5 text-left transition",
              picked === tool ? "border-brand-400/70 bg-brand-500/10" : "border-line bg-surface-faint hover:border-line-strong hover:bg-surface",
            )}
          >
            <ToolMark ai={tool} className="mt-0.5 size-6" />
            <span className="grid min-w-0 gap-0.5">
              <span className="text-body font-semibold text-primary">{aiLabel(tool)}</span>
              <span className="text-ui text-muted">{t(needs[tool])}</span>
            </span>
          </button>
        ))}
      </div>
      <p className="mt-3 text-xs text-subtle">{t("firstRun.tool.later")}</p>
    </StepPanel>
  );
}

// StepList is Setup's steps the machine needs, each with its icon, and the
// first one not done open on what it offers to do about itself — the base
// image's download, with its progress, among them.
function StepList({ steps }: { steps: Step[] }) {
  const open = steps.find((s) => !usable(s.status) || s.status === "building" || s.status === "updating");
  return (
    <ol className="grid gap-2">
      {steps.map((s) => (
        <li key={s.id} className="rounded-xl border border-line bg-surface-faint p-3" data-setup-step={s.id} data-status={s.status}>
          <div className="flex items-start gap-2.5">
            <span className="mt-0.5">
              <StepIcon status={s.status} small />
            </span>
            <div className="grid min-w-0 flex-1 gap-0.5">
              <p className="text-body font-medium text-primary">{s.title}</p>
              {s.detail && <p className="break-words text-xs text-subtle">{s.detail}</p>}
            </div>
          </div>
          {s === open && s.body && <div className="mt-3">{s.body}</div>}
        </li>
      ))}
    </ol>
  );
}

// LoginStep signs the chosen tool in, and only it: Setup's own sign-in for
// the tool. Codex and OpenCode run in the base image, so one built without
// the tool gets it added first.
function LoginStep({ tool, step }: { tool: FirstRunTool; step?: Step }) {
  const t = useT();
  const queryClient = useQueryClient();
  const setup = useQuery({ queryKey: ["setup"], queryFn: api.setup });
  const check = setup.data?.checks.find((c) => c.id === tool);
  const needsImage = (tool === "codex" || tool === "opencode") && check?.fix === `agentbox image build --${tool}`;
  const [job, setJob] = useState<string>();
  const build = useMutation({
    mutationFn: () => api.buildImage(tool === "codex" ? { codex: true } : { opencode: true }),
    onSuccess: (started) => setJob(started.id),
  });
  if (!step) return <LoaderCircle className="size-5 animate-spin text-subtle" />;
  return (
    <div className="grid gap-3" data-first-run-login={tool} data-status={step.status}>
      {step.detail && (
        <p className={cn("break-words text-xs", step.status === "ok" ? "text-emerald-300/80" : "text-subtle")}>{step.detail}</p>
      )}
      {needsImage && (
        <div className="grid gap-2">
          <p className="text-ui text-muted">{t("firstRun.login.addToImage", { tool: aiLabel(tool) })}</p>
          <div>
            <Button variant="primary" disabled={build.isPending || !!job} onClick={() => build.mutate()}>
              {build.isPending ? <LoaderCircle className="animate-spin" /> : <Plus />}
              {t("firstRun.login.addToImageButton", { tool: aiLabel(tool) })}
            </Button>
          </div>
          {job && <JobProgress jobId={job} onDone={() => void queryClient.invalidateQueries({ queryKey: ["setup"] })} />}
          {build.error && <Notice>{errorMessage(build.error)}</Notice>}
        </div>
      )}
      {step.body}
    </div>
  );
}

// ProjectStep clones one of the GitHub account's repositories as the first
// project; without a GitHub account it says that's what lists them.
function ProjectStep({ github, projects, onCloned }: { github: boolean; projects: T.Project[]; onCloned: (name: string) => void }) {
  const t = useT();
  const queryClient = useQueryClient();
  const repos = useQuery({ queryKey: ["githubRepos"], queryFn: api.githubRepos, enabled: github, staleTime: 60_000 });
  const [query, setQuery] = useState("");
  const [picked, setPicked] = useState<T.GitHubRepo>();
  const [job, setJob] = useState<string>();
  const clone = useMutation({
    mutationFn: (repo: T.GitHubRepo) => api.cloneProject({ url: repo.cloneUrl, path: "" }),
    onSuccess: (started) => setJob(started.id),
  });
  const done = async (finished: T.Job) => {
    if (finished.status !== "succeeded") return;
    await queryClient.invalidateQueries({ queryKey: ["projects"] });
    onCloned((finished.result as T.Project).name);
  };

  if (projects.length > 0) {
    return (
      <ul className="grid gap-1.5">
        {projects.map((p) => (
          <li key={p.name} className="flex items-center gap-2 text-ui text-secondary">
            <CircleCheck className="size-4 text-emerald-400" /> {p.displayName || p.name}
          </li>
        ))}
      </ul>
    );
  }
  if (!github) return <p className="text-ui text-muted">{t("firstRun.project.noGitHub")}</p>;
  if (job && picked) {
    return (
      <div className="grid gap-2">
        <p className="text-ui text-secondary">{t("firstRun.project.cloning", { repo: picked.fullName })}</p>
        <JobProgress jobId={job} onDone={(j) => void done(j)} />
      </div>
    );
  }
  const shown = filterRepos(repos.data ?? [], query);
  return (
    <div className="grid gap-3">
      <div className="relative">
        <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-subtle" />
        <Input
          autoFocus
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder={t("firstRun.project.search")}
          aria-label={t("firstRun.project.search")}
          className="pl-9"
        />
      </div>
      {repos.isLoading ? (
        <p className="flex items-center gap-2 text-ui text-subtle">
          <LoaderCircle className="size-4 animate-spin" /> {t("firstRun.project.loading")}
        </p>
      ) : repos.error ? (
        <Notice>{errorMessage(repos.error)}</Notice>
      ) : shown.length === 0 ? (
        <p className="text-ui text-subtle">{query ? t("firstRun.project.noMatch") : t("firstRun.project.none")}</p>
      ) : (
        <ul className="grid max-h-80 gap-1 overflow-y-auto pr-1" aria-label={t("firstRun.project.repos")} role="listbox">
          {shown.map((r) => (
            <li key={r.fullName}>
              <button
                type="button"
                role="option"
                aria-selected={picked?.fullName === r.fullName}
                data-repo={r.fullName}
                onClick={() => setPicked(r)}
                className={cn(
                  "flex w-full min-w-0 items-start gap-2.5 rounded-lg border px-3 py-2 text-left transition",
                  picked?.fullName === r.fullName ? "border-brand-400/70 bg-brand-500/10" : "border-transparent hover:bg-surface",
                )}
              >
                {r.private ? <Lock className="mt-0.5 size-4 shrink-0 text-subtle" /> : <FolderGit2 className="mt-0.5 size-4 shrink-0 text-subtle" />}
                <span className="grid min-w-0 flex-1 gap-0.5">
                  <span className="truncate text-body font-medium text-primary">{r.fullName}</span>
                  {r.description && <span className="truncate text-xs text-muted">{r.description}</span>}
                </span>
                {r.pushedAt && <span className="shrink-0 text-xs text-faint">{timeAgo(r.pushedAt)}</span>}
              </button>
            </li>
          ))}
        </ul>
      )}
      {clone.error && <Notice>{errorMessage(clone.error)}</Notice>}
      <div>
        <Button variant="primary" disabled={!picked || clone.isPending} onClick={() => picked && clone.mutate(picked)}>
          {clone.isPending ? <LoaderCircle className="animate-spin" /> : <GitFork />}
          {picked ? t("firstRun.project.clone", { repo: picked.fullName }) : t("firstRun.project.pick")}
        </Button>
      </div>
    </div>
  );
}
