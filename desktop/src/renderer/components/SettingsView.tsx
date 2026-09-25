import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowLeft,
  ArrowRight,
  Check,
  ChevronDown,
  CircleCheck,
  CircleDashed,
  Copy,
  ExternalLink,
  KeyRound,
  ListChecks,
  LoaderCircle,
  LogIn,
  MessagesSquare,
  Monitor,
  Moon,
  PartyPopper,
  Pencil,
  RefreshCw,
  ShieldCheck,
  SquareTerminal,
  Sun,
  Trash2,
  TriangleAlert,
  Wand,
  Wrench,
} from "lucide-react";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { toast } from "sonner";
import type * as T from "../../shared/api";
import { api } from "../lib/api";
import { cn, errorMessage } from "../lib/utils";
import { ImageDownloads } from "./ImageDownloads";
import { JobProgress } from "./JobProgress";
import {
  CompactWindow,
  DefaultContextWindow,
  DefaultModel,
  NewAgentEffort,
  NewAgentResources,
  OpenCodeInImage,
  ResumeAfterLimit,
} from "./NewAgentDefaults";
import { Badge } from "./ui/badge";
import { Button } from "./ui/button";
import { Code, Notice, Panel } from "./ui/card";
import { Input, Label } from "./ui/input";
import { Switch } from "./ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "./ui/tabs";
import { VMSize } from "./VMSize";

type Status = "ok" | "missing" | "outdated" | "optional" | "warn" | "checking";

// A step is one thing to get right, whether the daemon checks it (Incus, the
// base image) or the app does (its own command-line tool, GitHub).
type Step = {
  id: string;
  title: string;
  description: string;
  status: Status;
  // required: AgentBox can't run agents at all without it, which is what the
  // daemon's SetupStatus.Ready is about, and what the wizard counts.
  required: boolean;
  // optional: the wizard offers to skip it. Not the opposite of required —
  // Ready doesn't need a Claude Code login, since shell-only agents need none,
  // but nothing forces the wizard to stop over it either: it's the daemon's
  // Required flag, not a step-by-step guess, that decides what actually blocks.
  optional: boolean;
  detail?: string;
  body?: ReactNode;
};

// skippable is the wizard's rule for the Next/Skip buttons: everything the
// daemon doesn't require may be skipped. A missing or bad login shows up as a
// warning, same as everywhere else in Settings, rather than stopping the
// wizard — agents that need it fail with a clear error of their own.
const skippable = (check: T.SetupCheck) => !check.required;

// descriptions say what each step is for, in one sentence. The daemon names a
// check and says what's wrong with it; what it is *for* belongs here, with the
// page that shows it.
const descriptions: Record<string, string> = {
  cli: "The agentbox command, for your terminal and scripts, and for the next step. Agents get their own copy.",
  incus: "Runs each agent in its own Linux machine.",
  host: "Lets agents write files in their worktrees as you.",
  image:
    "The machine every agent is copied from: Debian with Docker, Node.js, Claude Code, Chromium and ffmpeg. Downloaded ready-made and made this machine's; made here from scratch, in a few minutes, when the download fails or you ask for an optional component.",
  claude:
    "AgentBox's own logins for agents. Your ~/.claude isn't shared with them.",
  codex: "For agents that run Codex.",
  opencode:
    "For agents that run OpenCode. Its logins are provider API keys, and they decide which models an OpenCode agent can run.",
  github:
    "Tokens for agents, as GH_TOKEN, so gh works in them. AgentBox also uses the default one to show each agent’s pull request and whether its checks pass.",
  android:
    "KVM and an Android SDK with a system image, shared read-only with agents of Android projects.",
  preview: "Opens agents' dev servers from your own browser.",
};

// The two groups the tabbed page sorts steps into, once the wizard is behind
// you. Everything else (Agents) is the app's own defaults, not a daemon check.
const environmentIds = new Set([
  "cli",
  "incus",
  "host",
  "image",
  "android",
  "preview",
]);
const accountIds = new Set(["claude", "codex", "opencode", "github"]);

// githubDetail says who the default account's token belongs to, or why it
// can't be used.
function githubDetail(auth?: T.AuthStatus): string | undefined {
  if (!auth?.github) return undefined;
  if (auth.githubError) return auth.githubError;
  return auth.githubUser
    ? `Agents use GitHub as ${auth.githubUser} by default`
    : "AgentBox has its own login for agents";
}

export function SettingsView({ onHome }: { onHome?: () => void }) {
  const queryClient = useQueryClient();
  const setup = useQuery({
    queryKey: ["setup"],
    queryFn: api.setup,
    refetchInterval: 5_000,
  });
  const cli = useQuery({
    queryKey: ["cli"],
    queryFn: () => window.agentbox.cli.status(),
    refetchInterval: 10_000,
  });
  const info = useQuery({
    queryKey: ["app-info"],
    queryFn: () => window.agentbox.info(),
    staleTime: Infinity,
  });
  const [imageJob, setImageJob] = useState<string | null>(null);
  const [hostSetupRan, setHostSetupRan] = useState(false);
  const auth = useQuery({
    queryKey: ["auth"],
    queryFn: api.auth,
    refetchInterval: 5_000,
  });

  const install = useMutation({
    mutationFn: () => window.agentbox.cli.install(),
    onSuccess: (status) => {
      queryClient.setQueryData(["cli"], status);
      toast("Installed the agentbox command", { description: status.linkPath });
    },
  });
  const build = useMutation({
    mutationFn: api.buildImage,
    onSuccess: (job) => setImageJob(job.id),
  });

  const check = (id: string): T.SetupCheck | undefined =>
    setup.data?.checks.find((c) => c.id === id);
  const statusOf = (id: string): Status =>
    (check(id)?.status as Status | undefined) ?? "checking";
  const cliStatus: Status = !cli.data
    ? "checking"
    : cli.data.path
      ? "ok"
      : "missing";
  // sudo doesn't search ~/.local/bin, so the command names the binary by its path.
  const agentbox = cli.data?.path
    ? '"$(command -v agentbox)"'
    : (cli.data?.binary ?? "agentbox");

  // The app's own steps, which the daemon knows nothing about.
  const cliStep: Step = {
    id: "cli",
    title: "Command-line tool",
    status: cliStatus,
    required: true,
    optional: false,
    detail: cli.data?.path
      ? `${cli.data.path} (${cli.data.version ?? "unknown version"})`
      : "agentbox isn't on your shell's PATH",
    description: descriptions.cli,
    body: (
      <div className="grid gap-2">
        <div className="flex flex-wrap items-center gap-2">
          <Button
            variant="primary"
            disabled={install.isPending || cli.data?.bundled === false}
            onClick={() => install.mutate()}
          >
            {install.isPending ? (
              <LoaderCircle className="animate-spin" />
            ) : (
              <SquareTerminal />
            )}
            Install command-line tool
          </Button>
          <span className="text-xs text-subtle">
            Links it as {cli.data?.linkPath ?? "~/.local/bin/agentbox"}
          </span>
        </div>
        {cli.data?.linked && !cli.data.onPath && (
          <Notice tone="warning">
            <Code>~/.local/bin</Code> isn't on your shell's PATH. Add{" "}
            <Code>export PATH="$HOME/.local/bin:$PATH"</Code> to your shell
            profile, then open a new terminal.
          </Notice>
        )}
        {cli.data?.bundled === false && (
          <Notice tone="info">
            This build of the app has no agentbox binary to install. Build it
            with <Code>go build -o bin/agentbox ./cmd/agentbox</Code>.
          </Notice>
        )}
        {install.error && <Notice>{errorMessage(install.error)}</Notice>}
      </div>
    ),
  };
  const githubStep: Step = {
    id: "github",
    title: "GitHub for agents",
    status: auth.data?.github ? "ok" : "optional",
    required: false,
    optional: true,
    detail: githubDetail(auth.data),
    description: descriptions.github,
    body: <GitHubAccounts accounts={auth.data?.githubAccounts ?? []} />,
  };

  // What each daemon check offers to do about itself.
  const bodies: Record<string, ReactNode> = {
    incus: (
      <HostSetup agentbox={agentbox} onRun={() => setHostSetupRan(true)} />
    ),
    host: (
      <p className="text-[13px] text-muted">
        Host setup does this one too: use{" "}
        <span className="text-secondary">Set up host</span> in the Incus step
        above.
      </p>
    ),
    image: (
      <div className="grid gap-3">
        <div>
          <Button
            variant={statusOf("image") === "ok" ? "secondary" : "primary"}
            disabled={
              build.isPending || statusOf("incus") !== "ok" || imageJob !== null
            }
            onClick={() => build.mutate()}
          >
            {build.isPending ? (
              <LoaderCircle className="animate-spin" />
            ) : (
              <RefreshCw />
            )}
            {statusOf("image") === "missing"
              ? "Build base image"
              : "Rebuild base image"}
          </Button>
        </div>
        <ImageDownloads image={setup.data?.image} />
        {imageJob && (
          <JobProgress
            jobId={imageJob}
            onDone={() =>
              void queryClient.invalidateQueries({ queryKey: ["setup"] })
            }
          />
        )}
        {build.error && <Notice>{errorMessage(build.error)}</Notice>}
      </div>
    ),
    claude: <ClaudeAccounts accounts={auth.data?.claudeAccounts ?? []} />,
    codex: (
      <div className="grid gap-2">
        <p className="text-[13px] text-muted">
          {check("codex")?.fix === "agentbox image build --codex" ? (
            <>
              The base image was built without Codex. Make it again with Codex
              in, then log in.
            </>
          ) : (
            <>
              Run this in a terminal. It needs the Codex CLI on this machine:{" "}
              <Code>npm install -g @openai/codex</Code>.
            </>
          )}
        </p>
        <CommandBox command={check("codex")?.fix ?? "agentbox auth codex"} />
      </div>
    ),
    opencode: (
      <div className="grid gap-2">
        <p className="text-[13px] text-muted">
          {check("opencode")?.fix === "agentbox image build --opencode" ? (
            <>
              The base image was built without OpenCode. Make it again with
              OpenCode in, then log in.
            </>
          ) : (
            <>
              Run this in a terminal and pick a provider. It needs the OpenCode
              CLI on this machine: <Code>npm install -g opencode-ai</Code>.
            </>
          )}
        </p>
        <CommandBox
          command={check("opencode")?.fix ?? "agentbox auth opencode"}
        />
      </div>
    ),
    android: check("android")?.fix ? (
      <div className="grid gap-2">
        <p className="text-[13px] text-muted">
          Install Android Studio, or the SDK command-line tools, then add the
          emulator and a system image. AgentBox looks in{" "}
          <Code>$ANDROID_HOME</Code> and <Code>~/Android/Sdk</Code>.
        </p>
        <CommandBox command={check("android")!.fix!} />
      </div>
    ) : undefined,
  };

  // The steps, in the order the daemon reports its checks, with the app's own
  // two put where they belong: its command-line tool first, because the Incus
  // step is a command you run with it, and GitHub next to the other logins.
  const steps: Step[] = [cliStep];
  for (const c of setup.data?.checks ?? []) {
    steps.push({
      id: c.id,
      title: c.title,
      status: c.status as Status,
      required: c.required,
      optional: skippable(c),
      detail: c.detail,
      description: descriptions[c.id] ?? "",
      body:
        bodies[c.id] ?? (c.fix ? <CommandBox command={c.fix} /> : undefined),
    });
    if (c.id === "opencode") steps.push(githubStep);
  }

  // Which page: the wizard while there is setting up left to do, the tabbed
  // page once there isn't. It waits for both answers — the daemon's checks and
  // the app's own command-line tool — and then decides once, so that finishing
  // the last required step doesn't pull the wizard out from under you.
  const [page, setPage] = useState<"wizard" | "tabs" | null>(null);
  const loaded = setup.data !== undefined && cli.data !== undefined;
  const settled = loaded && steps.every((s) => s.optional || s.status === "ok");
  useEffect(() => {
    if (page === null && loaded) setPage(settled ? "tabs" : "wizard");
  }, [page, loaded, settled]);

  const required = steps.filter((s) => s.required);
  const done = required.filter((s) => s.status === "ok").length;

  if (page === null) {
    return (
      <div className="flex h-full items-center justify-center text-sm text-subtle">
        {setup.error ? (
          <Notice>{errorMessage(setup.error)}</Notice>
        ) : (
          <LoaderCircle className="size-5 animate-spin" />
        )}
      </div>
    );
  }
  if (page === "wizard") {
    return (
      <SetupWizard
        steps={steps}
        error={setup.error ? errorMessage(setup.error) : undefined}
        onSettings={() => setPage("tabs")}
        onHome={onHome}
      />
    );
  }
  return (
    <SettingsTabs
      steps={steps}
      done={done}
      total={required.length}
      error={setup.error ? errorMessage(setup.error) : undefined}
      info={info.data}
      imageJob={imageJob}
      hostSetupRan={hostSetupRan}
      onWizard={() => setPage("wizard")}
    />
  );
}

// SetupWizard walks through the steps one at a time, so a new machine is a
// sequence rather than a wall. A required step that isn't done yet stops it:
// nothing after it would work, and the rail says how much is left.
function SetupWizard({
  steps,
  error,
  onSettings,
  onHome,
}: {
  steps: Step[];
  error?: string;
  onSettings: () => void;
  onHome?: () => void;
}) {
  const blocked = steps.findIndex((s) => !s.optional && s.status !== "ok");
  const ready = blocked === -1;
  // The last screen is the wizard's own: everything required is done.
  const last = steps.length;
  const limit = ready ? last : blocked;
  const [index, setIndex] = useState(() => limit);
  // Nothing is reachable past the first required step still to do, wherever
  // you were when it stopped being done.
  const at = Math.min(index, limit);
  const step = at === last ? undefined : steps[at];
  const stuck = step !== undefined && !step.optional && step.status !== "ok";
  const doneCount = steps.filter((s) => s.status === "ok").length;

  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-4xl px-4 py-6 md:px-8 md:py-9">
        <div className="flex flex-wrap items-end gap-4">
          <div>
            <h1 className="text-2xl font-semibold tracking-tight text-title">
              Set up AgentBox
            </h1>
            <p className="mt-1 text-sm text-muted">
              One step at a time. This page checks again every few seconds, so a
              step you finish elsewhere ticks itself off.
            </p>
          </div>
          <Button
            variant="ghost"
            size="sm"
            className="ml-auto"
            onClick={onSettings}
          >
            <ListChecks />
            Show settings
          </Button>
        </div>
        {error && <Notice className="mt-4">{error}</Notice>}

        <div className="mt-6 grid gap-5 md:grid-cols-[13rem_minmax(0,1fr)]">
          <nav
            aria-label="Setup steps"
            className="md:sticky md:top-0 md:self-start"
          >
            <p className="px-2 pb-2 text-[11px] uppercase tracking-wider text-faint">
              {doneCount} of {steps.length} done
            </p>
            <ol className="grid gap-0.5">
              {steps.map((s, i) => (
                <RailItem
                  key={s.id}
                  step={s}
                  active={i === at}
                  reachable={i <= limit}
                  onClick={() => setIndex(i)}
                />
              ))}
              <RailItem
                step={{
                  id: "done",
                  title: "All set",
                  description: "",
                  status: ready ? "ok" : "optional",
                  required: false,
                  optional: true,
                }}
                active={at === last}
                reachable={last <= limit}
                onClick={() => setIndex(last)}
              />
            </ol>
          </nav>

          <div className="grid gap-4">
            <Panel
              className="p-5"
              data-wizard-step={step?.id ?? "done"}
              data-status={step?.status ?? (ready ? "ok" : "optional")}
            >
              {step ? (
                <>
                  <div className="flex items-center gap-2">
                    <span className="text-[11px] tabular-nums text-faint">
                      Step {at + 1} of {steps.length}
                    </span>
                    {step.optional ? (
                      step.status === "warn" ? (
                        <Badge variant="warning">check</Badge>
                      ) : (
                        <Badge>optional</Badge>
                      )
                    ) : (
                      step.status !== "ok" &&
                      step.status !== "checking" && (
                        <Badge variant="warning">
                          {step.status === "outdated" ? "outdated" : "needed"}
                        </Badge>
                      )
                    )}
                    <span className="ml-auto">
                      <StepIcon status={step.status} />
                    </span>
                  </div>
                  <h2 className="mt-2 text-lg font-semibold text-title">
                    {step.title}
                  </h2>
                  <p className="mt-1 text-sm text-muted">{step.description}</p>
                  {step.detail && (
                    <p
                      className={cn(
                        "mt-2 break-words text-xs",
                        step.status === "ok"
                          ? "text-emerald-300/80"
                          : "text-subtle",
                      )}
                    >
                      {step.detail}
                    </p>
                  )}
                  {step.body && <div className="mt-4">{step.body}</div>}
                </>
              ) : (
                <Finished
                  ready={ready}
                  onHome={onHome}
                  onSettings={onSettings}
                />
              )}
            </Panel>

            <div className="flex flex-wrap items-center gap-2">
              <Button
                variant="secondary"
                disabled={at === 0}
                onClick={() => setIndex(at - 1)}
              >
                <ArrowLeft />
                Back
              </Button>
              {step?.optional && (
                <Button variant="ghost" onClick={() => setIndex(at + 1)}>
                  Skip for now
                </Button>
              )}
              {at < last && (
                <Button
                  variant="primary"
                  className="ml-auto"
                  disabled={stuck}
                  onClick={() => setIndex(at + 1)}
                >
                  Next
                  <ArrowRight />
                </Button>
              )}
              {stuck && (
                <span className="w-full text-xs text-subtle">
                  AgentBox can't run agents without this one, so it waits here
                  until it's done.
                </span>
              )}
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}

function RailItem({
  step,
  active,
  reachable,
  onClick,
}: {
  step: Step;
  active: boolean;
  reachable: boolean;
  onClick: () => void;
}) {
  return (
    <li>
      <button
        type="button"
        disabled={!reachable}
        onClick={onClick}
        data-rail-step={step.id}
        data-status={step.status}
        aria-current={active ? "step" : undefined}
        className={cn(
          "flex w-full items-center gap-2.5 rounded-lg px-2 py-1.5 text-left text-[13px] transition",
          active
            ? "bg-surface-raised text-primary"
            : "text-muted hover:bg-surface hover:text-secondary",
          !reachable && "opacity-40 hover:bg-transparent hover:text-muted",
        )}
      >
        <StepIcon status={step.status} small />
        <span className="min-w-0 truncate">{step.title}</span>
      </button>
    </li>
  );
}

function StepIcon({ status, small }: { status: Status; small?: boolean }) {
  const size = small ? "size-4" : "size-5";
  if (status === "checking")
    return <LoaderCircle className={cn(size, "animate-spin text-subtle")} />;
  if (status === "ok")
    return <CircleCheck className={cn(size, "text-emerald-400")} />;
  if (status === "optional")
    return <CircleDashed className={cn(size, "text-subtle")} />;
  return <TriangleAlert className={cn(size, "text-amber-300")} />;
}

function Finished({
  ready,
  onHome,
  onSettings,
}: {
  ready: boolean;
  onHome?: () => void;
  onSettings: () => void;
}) {
  return (
    <div className="grid justify-items-center gap-3 py-6 text-center">
      <span className="flex size-12 items-center justify-center rounded-2xl border border-line-strong bg-overlay">
        <PartyPopper
          className={cn("size-5", ready ? "text-emerald-400" : "text-subtle")}
        />
      </span>
      <h2 className="text-lg font-semibold text-title">
        {ready ? "Setup is complete" : "Almost there"}
      </h2>
      <p className="max-w-md text-sm leading-relaxed text-muted">
        {ready
          ? "This machine has everything AgentBox needs. Add a project from the Home page, then create your first agent."
          : "Go back through the steps above: something required is still missing."}
      </p>
      <div className="mt-1 flex flex-wrap justify-center gap-2">
        {onHome && (
          <Button variant="primary" disabled={!ready} onClick={onHome}>
            Go to Home
            <ArrowRight />
          </Button>
        )}
        <Button variant="secondary" onClick={onSettings}>
          <ListChecks />
          Show settings
        </Button>
      </div>
    </div>
  );
}

// SettingsTabs is the page once setup is done: everything grouped by tabs, to
// check on or to change. The wizard is a click away for a machine that needs
// it again.
function SettingsTabs({
  steps,
  done,
  total,
  error,
  info,
  imageJob,
  hostSetupRan,
  onWizard,
}: {
  steps: Step[];
  done: number;
  total: number;
  error?: string;
  info?: { version: string; electron: string; socket: string };
  imageJob: string | null;
  // hostSetupRan keeps the Incus step open once host setup has gone green, so
  // its log stays readable.
  hostSetupRan: boolean;
  onWizard: () => void;
}) {
  type Section = "environment" | "accounts" | "lead" | "agents";
  const [section, setSection] = useState<Section>("environment");
  const environmentSteps = steps.filter((s) => environmentIds.has(s.id));
  const accountSteps = steps.filter((s) => accountIds.has(s.id));
  // On a Mac, the VM everything runs in, whose size can be changed here.
  const hostSetup = useQuery({
    queryKey: ["host-setup"],
    queryFn: () => window.agentbox.hostSetup.status(),
    refetchInterval: 2_000,
  });
  const vm = hostSetup.data?.vm;

  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-3xl px-4 py-6 md:px-8 md:py-9">
        <div className="flex flex-wrap items-end gap-4">
          <div>
            <h1 className="text-2xl font-semibold tracking-tight text-title">
              Settings
            </h1>
            <p className="mt-1 text-sm text-muted">
              What AgentBox needs on this machine, and what it applies to every
              agent. This page checks again every few seconds.
            </p>
          </div>
          <div className="ml-auto flex items-center gap-3">
            <Button variant="ghost" size="sm" onClick={onWizard}>
              <Wand />
              Run setup again
            </Button>
            <span
              className="text-sm tabular-nums text-muted"
              data-setup-progress={`${done}/${total}`}
            >
              {done} of {total} ready
            </span>
          </div>
        </div>
        <div className="mt-4 h-1.5 overflow-hidden rounded-full bg-surface-raised">
          <div
            className="h-full rounded-full bg-gradient-to-r from-brand-400 to-emerald-400 transition-all duration-500"
            style={{ width: `${(done / total) * 100}%` }}
          />
        </div>
        {error && <Notice className="mt-4">{error}</Notice>}

        <Tabs
          className="mt-6"
          value={section}
          onValueChange={(value) => setSection(value as Section)}
        >
          <TabsList>
            <TabsTrigger value="environment">
              <Wrench />
              Environment
            </TabsTrigger>
            <TabsTrigger value="accounts">
              <KeyRound />
              Accounts
            </TabsTrigger>
            <TabsTrigger value="lead">
              <MessagesSquare />
              Lead
            </TabsTrigger>
            <TabsTrigger value="agents">
              <SquareTerminal />
              Agents
            </TabsTrigger>
          </TabsList>

          <TabsContent value="environment" className="mt-4">
            <ol className="grid gap-3">
              {environmentSteps.map((step, i) => (
                <ChecklistStep
                  key={step.id}
                  n={i + 1}
                  step={step}
                  alwaysShow={
                    (step.id === "image" && imageJob !== null) ||
                    (step.id === "incus" && hostSetupRan)
                  }
                />
              ))}
            </ol>
            {vm?.exists && (
              <VMSize vm={vm} busy={hostSetup.data?.resizing === true} />
            )}
            <Appearance />
            <UpdateCheck />
          </TabsContent>

          <TabsContent value="accounts" className="mt-4">
            <ol className="grid gap-3">
              {accountSteps.map((step, i) => (
                <ChecklistStep
                  key={step.id}
                  n={i + 1}
                  step={step}
                  alwaysShow={step.id === "claude" || step.id === "github"}
                />
              ))}
            </ol>
          </TabsContent>

          <TabsContent value="lead" className="mt-4">
            <Panel className="p-5">
              <h2 className="text-[12px] font-semibold uppercase tracking-[0.08em] text-subtle">
                Lead
              </h2>
              <p className="mt-1.5 text-[13px] leading-relaxed text-subtle">
                Each project's chat, which plans the work and directs its
                agents. Its composer can still pick another model or window
                for one project, and what it picks there wins.
              </p>
              <div className="mt-3">
                <DefaultModel role="lead" />
                <DefaultContextWindow role="lead" />
              </div>
            </Panel>
          </TabsContent>

          <TabsContent value="agents" className="mt-4">
            <Panel className="p-5">
              <h2 className="text-[12px] font-semibold uppercase tracking-[0.08em] text-subtle">
                New agents
              </h2>
              <p className="mt-1.5 text-[13px] leading-relaxed text-subtle">
                Each can be overridden for a single agent as you create it.
              </p>
              <div className="mt-3">
                <DefaultModel role="agents" />
                <DefaultContextWindow role="agents" />
                <NewAgentEffort />
                <div className="mt-3" />
                <NewAgentResources />
                <OpenCodeInImage />
              </div>
            </Panel>
            <Panel className="mt-3 p-5">
              <h2 className="text-[12px] font-semibold uppercase tracking-[0.08em] text-subtle">
                Every agent
              </h2>
              <p className="mt-1.5 text-[13px] leading-relaxed text-subtle">
                These apply to the agents you already have, as well as the next
                one.
              </p>
              <div className="mt-3">
                <ResumeAfterLimit />
                <CompactWindow />
              </div>
            </Panel>
          </TabsContent>
        </Tabs>

        {info && (
          <p className="mt-8 text-center font-mono text-[11px] text-faint">
            AgentBox {info.version} · Electron {info.electron} · {info.socket}
          </p>
        )}
      </div>
    </div>
  );
}

// UpdateCheck is the daily request that asks whether a newer AgentBox is out,
// which is also how installations are counted. The label says exactly what the
// request carries, as the README's "Update check" does; keep them in step with
// internal/update. When something outside this switch keeps the check off — a
// development build, or AGENTBOX_NO_UPDATE_CHECK / DO_NOT_TRACK in the
// daemon's environment — it says so, rather than a switch that does nothing.
function UpdateCheck() {
  const settings = useQuery({ queryKey: ["settings"], queryFn: api.settings });
  const update = useQuery({
    queryKey: ["update"],
    queryFn: api.update,
    staleTime: Infinity,
  });
  const queryClient = useQueryClient();
  const save = useMutation({
    mutationFn: (updateCheck: boolean) => api.updateSettings({ updateCheck }),
    onSuccess: (next) => queryClient.setQueryData(["settings"], next),
    onError: (err) => toast.error(errorMessage(err)),
  });

  const available = update.data?.available;
  const blocked = update.data?.blocked;
  return (
    <Panel className="mt-3 grid gap-3 p-4">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-3">
        <div className="min-w-0 flex-1">
          <div className="text-[13px] font-medium text-primary">
            Check for updates
          </div>
          <p className="mt-0.5 text-[12px] leading-relaxed text-subtle">
            Once a day, asks agentbox.linting.dev whether a newer AgentBox is
            out, which is also how installations are counted. It sends exactly
            four things: a random ID made for this purpose, this version of
            AgentBox{update.data?.current ? ` (${update.data.current})` : ""},
            the operating system and the processor architecture. Nothing about
            you, your projects or your agents, and nothing else that identifies
            this machine.
          </p>
        </div>
        <Switch
          data-update-check
          aria-label="Check for updates"
          disabled={save.isPending || settings.data === undefined || !!blocked}
          checked={!blocked && (settings.data?.updateCheck ?? true)}
          onCheckedChange={(next) => save.mutate(next)}
        />
      </div>
      {blocked && (
        <p className="text-[12px] text-subtle">Off, because {blocked}.</p>
      )}
      {available && (
        <p className="text-[12px] text-subtle">
          AgentBox {available.version} is out.{" "}
          <button
            className="text-brand-300 hover:underline"
            onClick={() => void window.agentbox.openExternal(available.url)}
          >
            See what's new
          </button>
        </p>
      )}
    </Panel>
  );
}

// Appearance is how AgentBox is painted: following the theme of the desktop it
// runs on, or its own colours pinned light or dark. It sits with the
// Environment checks rather than with the agents' defaults because that is what
// it is about: this machine, and what AgentBox looks like on it. Following
// applies to the agents' desktops as well, which the copy says, since nothing
// else on this page reaches into an agent's window.
//
// Three choices and no more. Wearing the desktop's theme means wearing its
// mode too, so "follow" already covers a light desktop; what light and dark are
// for is the person who wants AgentBox one way round whatever Omarchy is doing.
// Pinning is AgentBox's own palette by definition, so it is also how you keep
// its own colours on a machine that has a theme.
//
// On a machine with no theme to follow, following is still offered and still
// says what it would do — and says there is nothing to follow — rather than
// disappearing and leaving someone wondering where the setting went.
const appearances: {
  value: T.Theme["appearance"];
  label: string;
  icon: typeof Monitor;
}[] = [
  { value: "follow", label: "Follow desktop", icon: Monitor },
  { value: "light", label: "Light", icon: Sun },
  { value: "dark", label: "Dark", icon: Moon },
];

function Appearance() {
  const queryClient = useQueryClient();
  const theme = useQuery({ queryKey: ["theme"], queryFn: api.theme });
  const set = useMutation({
    mutationFn: (appearance: string) => api.updateTheme({ appearance }),
    onSuccess: (updated) => queryClient.setQueryData(["theme"], updated),
    onError: (err) => toast.error(errorMessage(err)),
  });

  const current = theme.data?.appearance;
  const found = theme.data?.available === true;
  return (
    <Panel className="mt-3 grid gap-3 p-4">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-3">
        <div className="min-w-0 flex-1">
          <div className="text-[13px] font-medium text-primary">Appearance</div>
          <p className="mt-0.5 text-[12px] leading-relaxed text-subtle">
            Following takes the colours from the Omarchy theme this machine is
            running — its accent and whether it is light or dark — for
            AgentBox's own window and for every agent's desktop, its dock and
            window decorations. Agents' browsers are left alone: a page an agent
            looks at renders the way it would anywhere else. Light and dark are
            AgentBox's own colours, one way round or the other, whatever the
            desktop is doing.
          </p>
        </div>
        <div
          className="inline-flex rounded-xl border border-line bg-rail p-0.5"
          role="radiogroup"
          aria-label="Appearance"
          data-appearance-choice
        >
          {appearances.map(({ value, label, icon: Icon }) => (
            <button
              key={value}
              role="radio"
              aria-checked={current === value}
              data-appearance={value}
              disabled={set.isPending || theme.data === undefined}
              onClick={() => set.mutate(value)}
              className={cn(
                "inline-flex h-8 items-center gap-1.5 rounded-[10px] px-3 text-[12.5px] font-medium transition disabled:opacity-50",
                current === value
                  ? "bg-surface-strong text-title shadow-[inset_0_1px_0_rgb(255_255_255/0.08)]"
                  : "text-muted hover:text-primary",
              )}
            >
              <Icon className="size-[15px]" />
              {label}
            </button>
          ))}
        </div>
      </div>
      {theme.data !== undefined &&
        (found ? (
          <div className="flex items-center gap-2 text-[12px] text-subtle">
            <span
              className="size-3 rounded-full border border-line-vivid"
              style={{ background: theme.data.accent }}
              data-theme-accent={theme.data.accent}
              aria-hidden
            />
            <span data-theme-name>
              This machine is on{" "}
              <span className="text-tertiary">
                {theme.data.name || "a theme with no name"}
              </span>
              {current === "follow"
                ? `, a ${theme.data.mode || "dark"} theme.`
                : ", which AgentBox isn't wearing."}
            </span>
          </div>
        ) : (
          <p className="text-[12px] text-subtle">
            No Omarchy theme was found on this machine, so AgentBox is wearing
            its own colours.
          </p>
        ))}
    </Panel>
  );
}

// savedAtLabel says when a token was stored. A token stored before AgentBox
// recorded the date arrives as Go's zero time, which is no date to show.
function savedAtLabel(savedAt: string): string {
  const at = new Date(savedAt);
  if (!savedAt || Number.isNaN(at.getTime()) || at.getUTCFullYear() < 2000)
    return "saved on an unknown date";
  return `saved ${at.toLocaleDateString()}`;
}

// ClaudeAccounts lists the stored Claude Code logins and adds more. Agents use
// the account their project picked, and otherwise the default one. A token
// Anthropic no longer accepts is badged rejected, so a dead login shows here
// rather than as a 401 inside an agent.
export function ClaudeAccounts({ accounts }: { accounts: T.ClaudeAccount[] }) {
  const queryClient = useQueryClient();
  const [pasting, setPasting] = useState(false);
  const refresh = async () => {
    await queryClient.invalidateQueries({ queryKey: ["setup"] });
    await queryClient.invalidateQueries({ queryKey: ["auth"] });
    await queryClient.invalidateQueries({ queryKey: ["projects"] });
  };
  // The account being renamed, and the name it is getting.
  const [renaming, setRenaming] = useState<string | null>(null);
  const [newName, setNewName] = useState("");
  const startRename = (name: string) => {
    rename.reset();
    setRenaming(name);
    setNewName(name);
  };
  const rename = useMutation({
    mutationFn: ({ from, to }: { from: string; to: string }) =>
      api.renameClaudeAccount(from, to),
    onSuccess: async (got) => {
      setRenaming(null);
      const carried = [
        got.projects.length > 0 &&
          `${got.projects.length} project${got.projects.length === 1 ? "" : "s"}`,
        got.agents.length > 0 &&
          `${got.agents.length} agent${got.agents.length === 1 ? "" : "s"}`,
      ].filter(Boolean);
      toast(`Renamed "${got.old}" to "${got.name}"`, {
        description:
          (carried.length > 0 ? `${carried.join(" and ")} moved with it. ` : "") +
          "Agents on it keep the same token, so nothing needs a restart.",
      });
      await Promise.all([
        refresh(),
        queryClient.invalidateQueries({ queryKey: ["agents"] }),
        queryClient.invalidateQueries({ queryKey: ["claudeLimits"] }),
      ]);
    },
  });
  const makeDefault = useMutation({
    mutationFn: (name: string) => api.setDefaultClaudeAccount(name),
    onSuccess: async (_, name) => {
      toast(
        `New agents use "${name}" unless their project picks another account`,
      );
      await refresh();
    },
  });
  const remove = useMutation({
    mutationFn: (name: string) => api.removeClaudeAccount(name),
    onSuccess: async (_, name) => {
      toast(`Removed the Claude Code account "${name}"`, {
        description: "Agents that already have its token keep working.",
      });
      await refresh();
    },
  });
  const error = makeDefault.error ?? remove.error ?? rename.error;

  return (
    <div className="grid gap-3">
      {accounts.length > 0 && (
        <ul className="grid gap-1.5" aria-label="Claude Code accounts">
          {accounts.map((acc) => (
            <li
              key={acc.name}
              data-claude-account={acc.name}
              className="flex flex-wrap items-center gap-2 rounded-xl border border-line bg-surface-faint py-1.5 pl-3 pr-1.5"
            >
              <KeyRound className="size-3.5 shrink-0 text-subtle" />
              {renaming === acc.name ? (
                <form
                  className="flex min-w-0 items-center gap-1"
                  onSubmit={(event) => {
                    event.preventDefault();
                    const to = newName.trim();
                    if (to === acc.name) setRenaming(null);
                    else rename.mutate({ from: acc.name, to });
                  }}
                >
                  <Input
                    aria-label={`New name for ${acc.name}`}
                    value={newName}
                    onChange={(event) => setNewName(event.target.value)}
                    onKeyDown={(event) => {
                      if (event.key === "Escape") setRenaming(null);
                    }}
                    disabled={rename.isPending}
                    autoFocus
                    className="h-7 w-32 font-mono text-[12.5px]"
                  />
                  <Button
                    type="submit"
                    size="sm"
                    disabled={rename.isPending || newName.trim() === ""}
                  >
                    Rename
                  </Button>
                  <Button
                    type="button"
                    size="sm"
                    variant="ghost"
                    disabled={rename.isPending}
                    onClick={() => setRenaming(null)}
                  >
                    Cancel
                  </Button>
                </form>
              ) : (
                <span className="truncate font-mono text-[12.5px] text-secondary">
                  {acc.name}
                </span>
              )}
              {acc.default && <Badge variant="brand">default</Badge>}
              {acc.valid === "rejected" && (
                <Badge variant="danger">rejected</Badge>
              )}
              {acc.valid === "valid" && <Badge variant="success">valid</Badge>}
              <span className="truncate text-[11.5px] text-subtle">
                {savedAtLabel(acc.savedAt)}
              </span>
              <div className="ml-auto flex items-center gap-1">
                {!acc.default && (
                  <Button
                    size="sm"
                    variant="ghost"
                    disabled={makeDefault.isPending}
                    onClick={() => makeDefault.mutate(acc.name)}
                  >
                    Make default
                  </Button>
                )}
                <Button
                  size="icon-sm"
                  variant="ghost"
                  aria-label={`Rename ${acc.name}`}
                  disabled={rename.isPending}
                  onClick={() => startRename(acc.name)}
                >
                  <Pencil />
                </Button>
                <Button
                  size="icon-sm"
                  variant="ghost"
                  aria-label={`Remove ${acc.name}`}
                  disabled={remove.isPending}
                  onClick={() => remove.mutate(acc.name)}
                >
                  <Trash2 />
                </Button>
              </div>
            </li>
          ))}
        </ul>
      )}
      <ClaudeLogin accounts={accounts} onSaved={refresh} />
      <div className="grid gap-2">
        <button
          type="button"
          onClick={() => setPasting((open) => !open)}
          aria-expanded={pasting}
          className="flex w-fit items-center gap-1.5 text-xs text-subtle transition hover:text-tertiary"
        >
          <ChevronDown
            className={cn(
              "size-3.5 transition-transform",
              pasting && "rotate-180",
            )}
          />
          Paste a token instead
        </button>
        {pasting && <ClaudeTokenForm accounts={accounts} onSaved={refresh} />}
      </div>
      {error && <Notice>{errorMessage(error)}</Notice>}
    </div>
  );
}

// ClaudeLogin runs `claude setup-token` on this machine, through the daemon,
// and opens the page it asks for in your browser. Approving it there is the
// whole login: nothing is copied, and no terminal is involved (D59).
function ClaudeLogin({
  accounts,
  onSaved,
}: {
  accounts: T.ClaudeAccount[];
  onSaved: () => Promise<void>;
}) {
  const [account, setAccount] = useState("");
  const [job, setJob] = useState<string | null>(null);
  const [code, setCode] = useState("");
  const opened = useRef<string | null>(null);

  const start = useMutation({
    mutationFn: () => api.startClaudeLogin(account.trim() || undefined),
    onSuccess: (started) => {
      opened.current = null;
      setCode("");
      setJob(started.id);
    },
  });
  const login = useQuery({
    queryKey: ["claude-login", job],
    queryFn: () => api.claudeLogin(job!),
    enabled: job !== null,
    // The page to approve appears a moment after the job starts, and the login
    // ends whenever you get round to approving it.
    refetchInterval: (query) =>
      query.state.data && query.state.data.status !== "running" ? false : 1_000,
  });
  const cancel = useMutation({ mutationFn: () => api.cancelJob(job!) });
  // A login outlives this component: it keeps going in the daemon while you
  // look at another step, or another page. Picking it back up is what keeps
  // "a login is already running" from being a dead end.
  const jobs = useQuery({
    queryKey: ["jobs"],
    queryFn: api.jobs,
    enabled: job === null,
  });
  useEffect(() => {
    if (job !== null) return;
    const running = jobs.data?.find(
      (j) => j.kind === "claude-login" && j.status === "running",
    );
    if (running) setJob(running.id);
  }, [job, jobs.data]);
  const sendCode = useMutation({
    mutationFn: () => api.claudeLoginCode(job!, code.trim()),
    onSuccess: () => setCode(""),
  });

  // Open the page as soon as the daemon knows it, and only once: reopening it
  // on every poll would pile up browser tabs. The code page is never opened by
  // itself — it is a second, parallel login, and only one is worth finishing.
  const url = login.data?.url;
  const codeUrl = login.data?.codeUrl;
  useEffect(() => {
    if (!url || opened.current === url) return;
    opened.current = url;
    void window.agentbox.openExternal(url);
  }, [url]);

  const running =
    job !== null && (!login.data || login.data.status === "running");
  const error = start.error ?? cancel.error ?? sendCode.error;

  return (
    <div
      className="grid gap-3"
      data-claude-login={login.data?.status ?? (job ? "running" : "idle")}
    >
      <p className="text-[13px] text-muted">
        AgentBox runs <Code>claude setup-token</Code> for you and keeps the
        token it mints. Add one account per Anthropic account you want agents to
        use.
      </p>
      <div className="flex flex-wrap items-end gap-2">
        <div className="grid gap-1.5">
          <Label htmlFor="claude-login-account">Account</Label>
          <Input
            id="claude-login-account"
            placeholder={accounts.length === 0 ? "default" : "work"}
            value={account}
            onChange={(event) => setAccount(event.target.value)}
            disabled={running}
            className="w-32 font-mono"
          />
        </div>
        <Button
          variant="primary"
          disabled={running || start.isPending}
          onClick={() => start.mutate()}
        >
          {running || start.isPending ? (
            <LoaderCircle className="animate-spin" />
          ) : (
            <LogIn />
          )}
          {accounts.length === 0 ? "Log in to Claude Code" : "Add an account"}
        </Button>
        {running && (
          <Button
            variant="ghost"
            disabled={cancel.isPending}
            onClick={() => cancel.mutate()}
          >
            Cancel
          </Button>
        )}
      </div>

      {job && (
        <div className="grid gap-3 rounded-xl border border-line bg-surface-faint p-3.5">
          <div className="flex flex-wrap items-center gap-2 text-[13px]">
            {running ? (
              <LoaderCircle className="size-4 animate-spin text-brand-400" />
            ) : (
              <StepIcon
                status={login.data?.status === "succeeded" ? "ok" : "missing"}
                small
              />
            )}
            <span className="text-secondary">
              {login.data?.status === "succeeded"
                ? `Saved the Claude Code account "${login.data.account}" for agents`
                : login.data?.status === "cancelled"
                  ? "The login was cancelled"
                  : login.data?.status === "failed"
                    ? "The login failed"
                    : url
                      ? "Waiting for you to approve it in the browser…"
                      : codeUrl
                        ? "Approve the sign-in page, then paste the code it gives you"
                        : "Starting Claude Code…"}
            </span>
            {url && running && (
              <Button
                size="sm"
                variant="secondary"
                className="ml-auto"
                onClick={() => void window.agentbox.openExternal(url)}
              >
                <ExternalLink />
                Open the page again
              </Button>
            )}
          </div>
          {running && codeUrl && (
            <form
              className="grid gap-2"
              onSubmit={(event) => {
                event.preventDefault();
                sendCode.mutate();
              }}
            >
              <p className="text-xs text-subtle">
                {url
                  ? "If the page can't reach this machine, approve "
                  : "Approve "}
                <button
                  type="button"
                  className="text-brand-300 underline-offset-2 hover:underline"
                  onClick={() => void window.agentbox.openExternal(codeUrl)}
                >
                  {url ? "this one" : "this page"}
                </button>{" "}
                {url
                  ? "instead and paste the code it gives you."
                  : "and paste the code it gives you."}
              </p>
              <div className="flex flex-wrap items-center gap-2">
                <Input
                  aria-label="Code from the browser"
                  placeholder="Code from the browser"
                  value={code}
                  onChange={(event) => setCode(event.target.value)}
                  className="w-64 font-mono"
                />
                <Button
                  type="submit"
                  variant="secondary"
                  disabled={!code.trim() || sendCode.isPending}
                >
                  Send
                </Button>
              </div>
            </form>
          )}
          <JobProgress
            jobId={job}
            header={false}
            logClassName="h-28"
            onDone={(finished) => {
              if (finished.status !== "succeeded") return;
              toast(
                `Saved the Claude Code account "${login.data?.account ?? "default"}" for agents`,
              );
              setAccount("");
              void onSaved();
            }}
          />
        </div>
      )}
      {error && <Notice>{errorMessage(error)}</Notice>}
    </div>
  );
}

// ClaudeTokenForm is the way in for a token minted somewhere else: another
// machine, or a `claude setup-token` you ran yourself.
function ClaudeTokenForm({
  accounts,
  onSaved,
}: {
  accounts: T.ClaudeAccount[];
  onSaved: () => Promise<void>;
}) {
  const [token, setToken] = useState("");
  const [account, setAccount] = useState("");
  const save = useMutation({
    mutationFn: () => api.saveClaudeToken(token, account.trim() || undefined),
    onSuccess: async () => {
      toast(
        `Saved the Claude Code account "${account.trim() || "default"}" for agents`,
      );
      setToken("");
      setAccount("");
      await onSaved();
    },
  });
  return (
    <form
      className="grid gap-2"
      onSubmit={(event) => {
        event.preventDefault();
        save.mutate();
      }}
    >
      <p className="text-[13px] text-muted">
        Run <Code>claude setup-token</Code> in a terminal, then paste the token
        it prints.
      </p>
      <div className="flex flex-wrap items-end gap-2">
        <div className="grid gap-1.5">
          <Label htmlFor="claude-account-name">Account</Label>
          <Input
            id="claude-account-name"
            placeholder={accounts.length === 0 ? "default" : "work"}
            value={account}
            onChange={(event) => setAccount(event.target.value)}
            className="w-32 font-mono"
          />
        </div>
        <div className="grid min-w-48 flex-1 gap-1.5">
          <Label htmlFor="claude-account-token">Token</Label>
          <Input
            id="claude-account-token"
            type="password"
            placeholder="sk-ant-oat01-…"
            value={token}
            onChange={(event) => setToken(event.target.value)}
            className="font-mono"
          />
        </div>
        <Button
          type="submit"
          variant="primary"
          disabled={!token.trim() || save.isPending}
        >
          {save.isPending ? (
            <LoaderCircle className="animate-spin" />
          ) : (
            <KeyRound />
          )}
          Save
        </Button>
      </div>
      <p className="text-xs text-subtle">
        An empty account name saves it as “default”. A project picks its account
        on its own page; agents of the other projects use the default one.
      </p>
      {save.error && <Notice>{errorMessage(save.error)}</Notice>}
    </form>
  );
}

// GitHubAccounts lists the stored GitHub logins and adds more. A project uses
// the account it picked on its own page; agents of the other projects use the
// default one.
function GitHubAccounts({ accounts }: { accounts: T.GitHubAccount[] }) {
  const queryClient = useQueryClient();
  const [token, setToken] = useState("");
  const [account, setAccount] = useState("");
  const refresh = async () => {
    await queryClient.invalidateQueries({ queryKey: ["setup"] });
    await queryClient.invalidateQueries({ queryKey: ["auth"] });
    await queryClient.invalidateQueries({ queryKey: ["projects"] });
  };
  const save = useMutation({
    mutationFn: () => api.saveGitHubToken(token, account.trim() || undefined),
    onSuccess: async (result) => {
      toast(
        `Saved the GitHub account "${account.trim() || "default"}" for agents, as ${result.user}`,
      );
      setToken("");
      setAccount("");
      await refresh();
    },
  });
  const makeDefault = useMutation({
    mutationFn: (name: string) => api.setDefaultGitHubAccount(name),
    onSuccess: async (_, name) => {
      toast(
        `New agents use "${name}" unless their project picks another account`,
      );
      await refresh();
    },
  });
  const remove = useMutation({
    mutationFn: (name: string) => api.removeGitHubAccount(name),
    onSuccess: async (_, name) => {
      toast(`Removed the GitHub account "${name}"`, {
        description: "Agents that already have its token keep working.",
      });
      await refresh();
    },
  });
  const error = save.error ?? makeDefault.error ?? remove.error;

  return (
    <div className="grid gap-3">
      {accounts.length > 0 && (
        <ul className="grid gap-1.5" aria-label="GitHub accounts">
          {accounts.map((acc) => (
            <li
              key={acc.name}
              data-github-account={acc.name}
              className="flex flex-wrap items-center gap-2 rounded-xl border border-line bg-surface-faint py-1.5 pl-3 pr-1.5"
            >
              <KeyRound className="size-3.5 shrink-0 text-subtle" />
              <span className="truncate font-mono text-[12.5px] text-secondary">
                {acc.name}
              </span>
              {acc.login && (
                <span className="truncate text-[12px] text-subtle">
                  {acc.login}
                </span>
              )}
              {acc.default && <Badge variant="brand">default</Badge>}
              <div className="ml-auto flex items-center gap-1">
                {!acc.default && (
                  <Button
                    size="sm"
                    variant="ghost"
                    disabled={makeDefault.isPending}
                    onClick={() => makeDefault.mutate(acc.name)}
                  >
                    Make default
                  </Button>
                )}
                <Button
                  size="icon-sm"
                  variant="ghost"
                  aria-label={`Remove ${acc.name}`}
                  disabled={remove.isPending}
                  onClick={() => remove.mutate(acc.name)}
                >
                  <Trash2 />
                </Button>
              </div>
            </li>
          ))}
        </ul>
      )}
      <form
        className="grid gap-2"
        onSubmit={(event) => {
          event.preventDefault();
          save.mutate();
        }}
      >
        <p className="text-[13px] text-muted">
          Paste a token from <Code>gh auth token</Code>, or a personal access
          token. Add one account per GitHub account you want agents to use.
        </p>
        <div className="flex flex-wrap items-end gap-2">
          <div className="grid gap-1.5">
            <Label htmlFor="github-account-name">Account</Label>
            <Input
              id="github-account-name"
              placeholder={accounts.length === 0 ? "default" : "work"}
              value={account}
              onChange={(event) => setAccount(event.target.value)}
              className="w-32 font-mono"
            />
          </div>
          <div className="grid min-w-48 flex-1 gap-1.5">
            <Label htmlFor="github-account-token">Token</Label>
            <Input
              id="github-account-token"
              type="password"
              placeholder="ghp_… or gho_…"
              value={token}
              onChange={(event) => setToken(event.target.value)}
              className="font-mono"
            />
          </div>
          <Button
            type="submit"
            variant="primary"
            disabled={!token.trim() || save.isPending}
          >
            {save.isPending ? (
              <LoaderCircle className="animate-spin" />
            ) : (
              <KeyRound />
            )}
            Save
          </Button>
        </div>
        <p className="text-xs text-subtle">
          An empty account name saves it as “default”. A project picks its
          account on its own page; agents of the other projects use the default
          one. Agents are told to read pull requests, not to push or merge.
        </p>
      </form>
      {error && <Notice>{errorMessage(error)}</Notice>}
    </div>
  );
}

// appendOutput adds a chunk of a command's output to the lines so far. A chunk
// can stop in the middle of a line, so the last one stays open.
export function appendOutput(lines: string[], text: string): string[] {
  const parts = text.split("\n");
  const next = lines.length > 0 ? [...lines] : [""];
  next[next.length - 1] += parts[0];
  return next.concat(parts.slice(1));
}

// HostSetup runs `agentbox host setup` as root, from the app: pkexec asks for
// the password in the desktop's own dialog, and the output arrives here as it
// is printed. The command for a terminal stays, for a machine with no password
// dialog to show — a server, or a desktop without a polkit agent — and for a
// run that failed.
//
// One run fixes Incus and the user mapping both, so there is one of these, on
// the Incus step; the user mapping points at it.
function HostSetup({
  agentbox,
  onRun,
}: {
  agentbox: string;
  onRun: () => void;
}) {
  const queryClient = useQueryClient();
  const status = useQuery({
    queryKey: ["host-setup"],
    queryFn: () => window.agentbox.hostSetup.status(),
    refetchInterval: 2_000,
  });
  const [lines, setLines] = useState<string[]>([]);

  useEffect(
    () =>
      window.agentbox.hostSetup.onOutput((text) =>
        setLines((prev) => appendOutput(prev, text)),
      ),
    [],
  );

  const run = useMutation({
    mutationFn: () => window.agentbox.hostSetup.run(),
    onMutate: () => {
      setLines([]);
      // Keeps this step open once it has gone green, so the log stays readable.
      onRun();
    },
    onSuccess: async (result) => {
      toast(
        mac ? "AgentBox's VM is set up" : "This machine is set up for AgentBox",
        {
          description: mac
            ? "Incus, its storage and the user mapping are in place inside the VM."
            : result.restarted
              ? "Incus is installed, and you can use it in this session: no need to log out."
              : "Incus is installed. The daemon keeps its running jobs and picks this up when they finish.",
        },
      );
      await queryClient.invalidateQueries({ queryKey: ["setup"] });
    },
  });

  // On a Mac, AgentBox runs in a Linux VM, and this sets the VM up instead:
  // `agentbox vm init`, with no password to ask for.
  const mac = status.data?.vm != null;
  const busy = run.isPending || status.data?.running === true;
  // On Windows, host setup is `agentbox wsl init`, in AgentBox's WSL distro,
  // which needs no password either (D94).
  const wsl = status.data?.wsl != null;
  const noDialog =
    status.data !== undefined && !mac && !wsl && status.data.pkexec === null;
  return (
    <div
      className="grid gap-2"
      data-host-setup={noDialog ? "terminal" : "button"}
    >
      <p className="text-[13px] text-muted">
        {mac
          ? "Sets AgentBox's Linux VM up: Incus, its storage and network, and your user's mapping, inside the VM."
          : wsl
            ? "Installs Incus in AgentBox's WSL distro and gives its user access to it. It needs no password."
            : "Installs Incus and gives your user access to it, in this session too. It changes system files, so it asks for your password."}
      </p>
      {!noDialog && (
        <div className="flex flex-wrap items-center gap-2">
          <Button
            variant="primary"
            disabled={busy}
            onClick={() => run.mutate()}
          >
            {busy ? <LoaderCircle className="animate-spin" /> : <ShieldCheck />}
            {mac ? "Set up the VM" : "Set up host"}
          </Button>
          <span className="text-xs text-subtle">
            {busy
              ? mac
                ? "Setting the VM up. This takes a few minutes the first time."
                : "Installing Incus. This takes a few minutes."
              : mac || wsl
                ? "No password needed"
                : "Your system asks for the password"}
          </span>
        </div>
      )}
      {(lines.length > 0 || busy) && (
        <SetupLog lines={lines} label="Host setup log" />
      )}
      {run.error && <Notice>{errorMessage(run.error)}</Notice>}
      {(noDialog || run.error) && (
        <div className="grid gap-2">
          <p className="text-[13px] text-muted">
            {noDialog
              ? "This machine has no pkexec, so the app can't ask for your password. Run it in a terminal instead:"
              : mac || wsl
                ? "Run it in a terminal instead:"
                : "Run it in a terminal instead, with sudo:"}
          </p>
          <CommandBox
            command={
              mac
                ? `${agentbox} vm init`
                : wsl
                  ? "agentbox wsl init"
                  : `sudo ${agentbox} host setup`
            }
          />
        </div>
      )}
    </div>
  );
}

// SetupLog is a setup command's output as it arrives, kept scrolled to its end,
// with its "==>" steps picked out.
export function SetupLog({ lines, label }: { lines: string[]; label: string }) {
  const log = useRef<HTMLDivElement>(null);
  useEffect(() => {
    log.current?.scrollTo({ top: log.current.scrollHeight });
  }, [lines]);
  return (
    <div
      ref={log}
      data-host-setup-log
      aria-label={label}
      className="h-64 overflow-auto rounded-xl border border-line bg-well p-3.5 font-mono text-[11.5px] leading-relaxed text-subtle"
    >
      {lines.map((line = "", i) => (
        <div
          key={i}
          className={cn(
            "whitespace-pre-wrap break-all",
            line.startsWith("==>") && "text-secondary",
          )}
        >
          {line.startsWith("==>") ? (
            <>
              <span className="text-brand-400">==&gt;</span>
              {line.slice(3)}
            </>
          ) : (
            line || " "
          )}
        </div>
      ))}
    </div>
  );
}

export function CommandBox({ command }: { command: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="flex items-center gap-2 rounded-xl border border-line-strong bg-well py-1.5 pl-3.5 pr-1.5 font-mono text-[12.5px] text-secondary">
      <span className="select-none text-faint">$</span>
      <span className="min-w-0 flex-1 truncate" title={command}>
        {command}
      </span>
      <Button
        size="icon-sm"
        variant="ghost"
        aria-label="Copy the command"
        onClick={() => {
          window.agentbox.copyText(command);
          setCopied(true);
          setTimeout(() => setCopied(false), 1500);
        }}
      >
        {copied ? <Check className="text-emerald-400" /> : <Copy />}
      </Button>
    </div>
  );
}

function ChecklistStep({
  n,
  step,
  alwaysShow,
}: {
  n: number;
  step: Step;
  alwaysShow?: boolean;
}) {
  const ok = step.status === "ok";
  return (
    <li data-setup-step={step.title} data-status={step.status}>
      <Panel
        className={cn(
          "p-4 transition",
          !ok &&
            !step.optional &&
            step.status !== "checking" &&
            "border-amber-400/15",
        )}
      >
        <div className="flex items-start gap-3.5">
          <span className="mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-full bg-surface ring-1 ring-inset ring-line">
            <StepIcon status={step.status} />
          </span>
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2">
              <span className="text-[11px] tabular-nums text-faint">{n}</span>
              <h3 className="text-[14px] font-semibold text-primary">
                {step.title}
              </h3>
              {step.optional ? (
                step.status === "warn" ? (
                  <Badge variant="warning">check</Badge>
                ) : (
                  <Badge>optional</Badge>
                )
              ) : (
                !ok &&
                step.status !== "checking" && (
                  <Badge variant="warning">
                    {step.status === "outdated" ? "outdated" : "needed"}
                  </Badge>
                )
              )}
            </div>
            <p className="mt-0.5 text-[13px] text-muted">{step.description}</p>
            {step.detail && (
              <p
                className={cn(
                  "mt-1.5 break-words text-xs",
                  ok ? "text-emerald-300/80" : "text-subtle",
                )}
              >
                {step.detail}
              </p>
            )}
            {step.body && (!ok || alwaysShow) && (
              <div className="mt-3">{step.body}</div>
            )}
          </div>
        </div>
      </Panel>
    </li>
  );
}
