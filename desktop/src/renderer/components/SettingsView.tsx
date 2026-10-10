import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowLeft,
  Languages,
  ArrowRight,
  Bot,
  Check,
  ChevronDown,
  CircleCheck,
  CircleDashed,
  Copy,
  Cpu,
  ExternalLink,
  KeyRound,
  Lightbulb,
  ListChecks,
  LoaderCircle,
  LogIn,
  Mic,
  Plug,
  Wand2,
  Monitor,
  Moon,
  MoonStar,
  PartyPopper,
  Pencil,
  RefreshCw,
  ShieldCheck,
  SlidersHorizontal,
  Smartphone,
  Sparkles,
  SquareTerminal,
  Sun,
  Trash2,
  TriangleAlert,
  Wand,
  Wrench,
} from "lucide-react";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { toast } from "sonner";
import * as T from "../../shared/api";
import { api } from "../lib/api";
import { formatDate, formatList, languages, t, useT, type MessageKey } from "../lib/i18n";
import { isNightly, isUpgrade } from "../lib/nightly";
import { openLatestRelease } from "../lib/releaseLink";
import { updateHint } from "../lib/appUpdate";
import { useAppUpdate } from "../lib/useAppUpdate";
import type { SettingSection } from "../lib/settingsSearch";
import { cn, errorMessage } from "../lib/utils";
import { ImageDownloads } from "./ImageDownloads";
import { JobProgress } from "./JobProgress";
import { ReportDialog } from "./ReportDialog";
import { UsageSentDialog } from "./UsageSentDialog";
import { WhatsNewDialog } from "./WhatsNewDialog";
import {
  AutoStopIdle,
  DockerPruneOnStop,
  CompactWindow,
  DefaultContextWindow,
  CursorDefaults,
  DefaultModel,
  LeadRecheck,
  MediaRetention,
  DiskFloor,
  DockerImageCache,
  SharedPackageCaches,
  NewAgentEffort,
  OpenCodeInImage,
  ResumeAfterLimit,
  ContinueAfterRestart,
  TaskTarget,
} from "./NewAgentDefaults";
import { Badge } from "./ui/badge";
import { Button } from "./ui/button";
import { Code, Notice, Panel } from "./ui/card";
import { projectSection } from "./ProjectSettings";
import { SkillsPanel } from "./SkillsPanel";
import { ConnectorsTab } from "./ConnectorsTab";
import { phoneGroups } from "./PhoneSettings";
import { SettingsPage, type SectionIcons } from "./SettingsPage";
import { useVoiceSettings } from "../lib/voice/settings";
import { SettingNote, SettingRow } from "./ui/settings";
import { Input, Label } from "./ui/input";
import { Switch } from "./ui/switch";
import { setTopBarDetailed, useTopBarDetailed } from "../lib/topBarLayout";
import { VMMigrate } from "./VMMigrate";
import { CHVSize, VMSize } from "./VMSize";
import { VMSwap } from "./VMSwap";
import { MoveToVM, moveToVMKeywords } from "./RunInVM";
import { pushToTalkGroup, useReadAloudGroup } from "./VoiceSettings";

type Status =
  | "ok"
  | "missing"
  | "outdated"
  | "optional"
  | "warn"
  | "updating"
  | "checking";

// usable is done, as far as the wizard goes: a base image whose agent tools
// the daemon is updating in the background, or failed to, is still the one
// agents use, and a warning is a caveat, never a reason to stop.
const usable = (status: Status) =>
  status === "ok" || status === "updating" || status === "warn";

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
  // settingsBody replaces body on the Settings page, for a step whose body in
  // the wizard carries a setting that has its own place there.
  settingsBody?: ReactNode;
};

// skippable is the wizard's rule for the Next/Skip buttons: everything the
// daemon doesn't require may be skipped. A missing or bad login shows up as a
// warning, same as everywhere else in Settings, rather than stopping the
// wizard — agents that need it fail with a clear error of their own.
const skippable = (check: T.SetupCheck) => !check.required;

// descriptions say what each step is for, in one sentence. The daemon names a
// check and says what's wrong with it; what it is *for* belongs here, with the
// page that shows it.
const descriptions: Record<string, MessageKey> = {
  cli: "settings.setup.desc.cli",
  incus: "settings.setup.desc.incus",
  host: "settings.setup.desc.host",
  image: "settings.setup.desc.image",
  storage: "settings.setup.desc.storage",
  claude: "settings.setup.desc.claude",
  codex: "settings.setup.desc.codex",
  opencode: "settings.setup.desc.opencode",
  cursor: "settings.setup.desc.cursor",
  github: "settings.setup.desc.github",
  android: "settings.setup.desc.android",
  preview: "settings.setup.desc.preview",
};

// checkTitles name the daemon's checks by id; one the app doesn't know keeps
// the daemon's own title.
const checkTitles: Record<string, MessageKey> = {
  incus: "settings.setup.title.incus",
  host: "settings.setup.title.host",
  image: "settings.setup.title.image",
  storage: "settings.setup.title.storage",
  claude: "settings.setup.title.claude",
  codex: "settings.setup.title.codex",
  opencode: "settings.setup.title.opencode",
  cursor: "settings.setup.title.cursor",
  android: "settings.setup.title.android",
  preview: "settings.setup.title.preview",
};

// The two groups the tabbed page sorts steps into, once the wizard is behind
// you. Everything else (Agents) is the app's own defaults, not a daemon check.
const environmentIds = new Set([
  "cli",
  "incus",
  "host",
  "image",
  "storage",
  "android",
  "preview",
]);
const accountIds = new Set(["claude", "codex", "opencode", "cursor", "github"]);

// githubDetail says who the default account's token belongs to, or why it
// can't be used.
function githubDetail(auth?: T.AuthStatus): string | undefined {
  if (!auth?.github) return undefined;
  if (auth.githubError) return auth.githubError;
  return auth.githubUser
    ? t("settings.setup.githubAs", { user: auth.githubUser })
    : t("settings.setup.githubOwn");
}

export function SettingsView({ onHome }: { onHome?: () => void }) {
  const t = useT();
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
      toast(t("settings.setup.cliInstalled"), { description: status.linkPath });
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
    title: t("settings.setup.title.cli"),
    status: cliStatus,
    required: true,
    optional: false,
    detail: cli.data?.path
      ? `${cli.data.path} (${cli.data.version ?? t("settings.setup.unknownVersion")})`
      : t("settings.setup.cliNotOnPath"),
    description: t(descriptions.cli),
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
            {t("settings.setup.installCli")}
          </Button>
          <span className="text-xs text-subtle">
            {t("settings.setup.linksAs", {
              path: cli.data?.linkPath ?? "~/.local/bin/agentbox",
            })}
          </span>
        </div>
        {cli.data?.linked && !cli.data.onPath && (
          <Notice tone="warning">
            {t.rich("settings.setup.notOnPathNotice", {
              code: (c) => <Code>{c}</Code>,
            })}
          </Notice>
        )}
        {cli.data?.bundled === false && (
          <Notice tone="info">
            {t.rich("settings.setup.noBinaryNotice", {
              code: (c) => <Code>{c}</Code>,
            })}
          </Notice>
        )}
        {install.error && <Notice>{errorMessage(install.error)}</Notice>}
      </div>
    ),
  };
  const githubStep: Step = {
    id: "github",
    title: t("settings.setup.title.github"),
    status: auth.data?.github ? "ok" : "optional",
    required: false,
    optional: true,
    detail: githubDetail(auth.data),
    description: t(descriptions.github),
    // The watch is asked here, once, on the first run: it is what an agent's
    // GitHub account is for once its pull request is open. On the Settings
    // page it has a place of its own, under Agents.
    body: (
      <div className="grid gap-5">
        <GitHubAccounts accounts={auth.data?.githubAccounts ?? []} />
        <PRWatch />
      </div>
    ),
    settingsBody: <GitHubAccounts accounts={auth.data?.githubAccounts ?? []} />,
  };

  // What each daemon check offers to do about itself.
  const bodies: Record<string, ReactNode> = {
    incus: (
      <HostSetup agentbox={agentbox} onRun={() => setHostSetupRan(true)} />
    ),
    host: (
      <p className="text-[13px] text-muted">
        {t.rich("settings.setup.hostBody", {
          b: (c) => <span className="text-secondary">{c}</span>,
        })}
      </p>
    ),
    image: (
      <div className="grid gap-3">
        <div>
          <Button
            variant={statusOf("image") === "ok" ? "secondary" : "primary"}
            disabled={
              build.isPending ||
              statusOf("incus") !== "ok" ||
              statusOf("image") === "updating" ||
              imageJob !== null
            }
            onClick={() => build.mutate()}
          >
            {build.isPending ? (
              <LoaderCircle className="animate-spin" />
            ) : (
              <RefreshCw />
            )}
            {statusOf("image") === "missing"
              ? t("settings.setup.buildImage")
              : t("settings.setup.rebuildImage")}
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
        {/* The daemon's own work on the image: updating the agent tools. */}
        {!imageJob && check("image")?.job && (
          <JobProgress
            key={check("image")?.job}
            jobId={check("image")?.job ?? ""}
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
            t("settings.setup.codexNoImage")
          ) : (
            t.rich("settings.setup.codexBody", {
              code: (c) => <Code>{c}</Code>,
            })
          )}
        </p>
        <CommandBox command={check("codex")?.fix ?? "agentbox auth codex"} />
      </div>
    ),
    opencode: (
      <div className="grid gap-2">
        <p className="text-[13px] text-muted">
          {check("opencode")?.fix === "agentbox image build --opencode" ? (
            t("settings.setup.opencodeNoImage")
          ) : (
            t.rich("settings.setup.opencodeBody", {
              code: (c) => <Code>{c}</Code>,
            })
          )}
        </p>
        <CommandBox
          command={check("opencode")?.fix ?? "agentbox auth opencode"}
        />
      </div>
    ),
    cursor: <CursorAccount command={check("cursor")?.fix ?? "agentbox auth cursor"} />,
    android: check("android")?.fix ? (
      <div className="grid gap-2">
        <p className="text-[13px] text-muted">
          {t.rich("settings.setup.androidBody", {
            code: (c) => <Code>{c}</Code>,
          })}
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
      title: checkTitles[c.id] ? t(checkTitles[c.id]) : c.title,
      status: c.status as Status,
      required: c.required,
      optional: skippable(c),
      detail: c.detail,
      description: descriptions[c.id] ? t(descriptions[c.id]) : "",
      body:
        bodies[c.id] ?? (c.fix ? <CommandBox command={c.fix} /> : undefined),
    });
    if (c.id === "opencode") steps.push(githubStep);
  }

  // Which page: the wizard while there is setting up left to do, the tabbed
  // page once there isn't. It waits for both answers — the daemon's checks and
  // the app's own command-line tool — and then decides once, so that finishing
  // the last required step doesn't pull the wizard out from under you.
  //
  // A Linux machine that runs agents itself, set up before AgentBox ran in a
  // VM on Linux, gets the tabbed page whatever is left: its way on is Setup's
  // Move to a VM, not the host setup the wizard would walk it through.
  const [page, setPage] = useState<"wizard" | "tabs" | null>(null);
  const hostSetup = useQuery({
    queryKey: ["host-setup"],
    queryFn: () => window.agentbox.hostSetup.status(),
  });
  const loaded =
    setup.data !== undefined &&
    cli.data !== undefined &&
    (hostSetup.data !== undefined || hostSetup.isError);
  const settled =
    loaded &&
    (hostSetup.data?.linux?.mode === "host" ||
      steps.every((s) => s.optional || usable(s.status)));
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
    <InstalledSettings
      steps={steps}
      done={done}
      total={required.length}
      error={setup.error ? errorMessage(setup.error) : undefined}
      info={info.data}
      imageJob={imageJob}
      hostSetupRan={hostSetupRan}
      agentbox={cli.data?.path ? "agentbox" : (cli.data?.binary ?? "agentbox")}
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
  const t = useT();
  const blocked = steps.findIndex((s) => !s.optional && !usable(s.status));
  const ready = blocked === -1;
  // The last screen is the wizard's own: everything required is done.
  const last = steps.length;
  const limit = ready ? last : blocked;
  const [index, setIndex] = useState(() => limit);
  // Nothing is reachable past the first required step still to do, wherever
  // you were when it stopped being done.
  const at = Math.min(index, limit);
  const step = at === last ? undefined : steps[at];
  const stuck = step !== undefined && !step.optional && !usable(step.status);
  const doneCount = steps.filter((s) => usable(s.status)).length;

  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-4xl px-4 py-6 md:px-8 md:py-9">
        <div className="flex flex-wrap items-end gap-4">
          <div>
            <h1 className="text-2xl font-semibold tracking-tight text-title">
              {t("settings.setup.heading")}
            </h1>
            <p className="mt-1 text-sm text-muted">
              {t("settings.setup.intro")}
            </p>
          </div>
          <Button
            variant="ghost"
            size="sm"
            className="ml-auto"
            onClick={onSettings}
          >
            <ListChecks />
            {t("settings.setup.showSettings")}
          </Button>
        </div>
        {error && <Notice className="mt-4">{error}</Notice>}

        <div className="mt-6 grid gap-5 md:grid-cols-[13rem_minmax(0,1fr)]">
          <nav
            aria-label={t("settings.setup.steps")}
            className="md:sticky md:top-0 md:self-start"
          >
            <p className="px-2 pb-2 text-[11px] uppercase tracking-wider text-faint">
              {t("settings.setup.progress", { done: doneCount, total: steps.length })}
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
                  title: t("settings.setup.allSet"),
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
                      {t("settings.setup.stepOf", { n: at + 1, total: steps.length })}
                    </span>
                    {step.optional ? (
                      step.status === "warn" ? (
                        <Badge variant="warning">{t("settings.setup.badgeCheck")}</Badge>
                      ) : (
                        <Badge>{t("settings.setup.badgeOptional")}</Badge>
                      )
                    ) : step.status === "updating" ? (
                      <Badge>{t("settings.setup.badgeUpdating")}</Badge>
                    ) : (
                      step.status !== "ok" &&
                      step.status !== "checking" && (
                        <Badge variant="warning">
                          {step.status === "outdated" ? t("settings.setup.badgeOutdated") : t("settings.setup.badgeNeeded")}
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
                {t("common.back")}
              </Button>
              {step?.optional && (
                <Button variant="ghost" onClick={() => setIndex(at + 1)}>
                  {t("settings.setup.skip")}
                </Button>
              )}
              {at < last && (
                <Button
                  variant="primary"
                  className="ml-auto"
                  disabled={stuck}
                  onClick={() => setIndex(at + 1)}
                >
                  {t("common.next")}
                  <ArrowRight />
                </Button>
              )}
              {stuck && (
                <span className="w-full text-xs text-subtle">
                  {t("settings.setup.stuck")}
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
  if (status === "updating")
    return <LoaderCircle className={cn(size, "animate-spin text-sky-400")} />;
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
  const t = useT();
  return (
    <div className="grid justify-items-center gap-3 py-6 text-center">
      <span className="flex size-12 items-center justify-center rounded-2xl border border-line-strong bg-overlay">
        <PartyPopper
          className={cn("size-5", ready ? "text-emerald-400" : "text-subtle")}
        />
      </span>
      <h2 className="text-lg font-semibold text-title">
        {ready ? t("settings.setup.complete") : t("settings.setup.almost")}
      </h2>
      <p className="max-w-md text-sm leading-relaxed text-muted">
        {ready
          ? t("settings.setup.completeBody")
          : t("settings.setup.almostBody")}
      </p>
      <div className="mt-1 flex flex-wrap justify-center gap-2">
        {onHome && (
          <Button variant="primary" disabled={!ready} onClick={onHome}>
            {t("settings.setup.goHome")}
            <ArrowRight />
          </Button>
        )}
        <Button variant="secondary" onClick={onSettings}>
          <ListChecks />
          {t("settings.setup.showSettings")}
        </Button>
      </div>
    </div>
  );
}

// InstalledSettings is the page once setup is done: every setting, in the
// sections SettingsPage draws with a sidebar and a search. This is where each
// setting's place is decided, and the words a search finds it by.
//
// The installation's sections go from what you change most to what you set
// once: how it looks, what agents run on, what they do, what they may take,
// the logins they use, and what this machine needs. Each project's settings
// follow them, as a section per project. Settings that most people never
// change are marked advanced, and folded at the end of their section.
function InstalledSettings({
  steps,
  done,
  total,
  error,
  info,
  imageJob,
  hostSetupRan,
  agentbox,
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
  // The agentbox to name in a command run without sudo.
  agentbox: string;
  onWizard: () => void;
}) {
  const t = useT();
  const settings = useQuery({ queryKey: ["settings"], queryFn: api.settings });
  const theme = useQuery({ queryKey: ["theme"], queryFn: api.theme });
  const update = useQuery({ queryKey: ["update"], queryFn: api.update, staleTime: Infinity });
  const setup = useQuery({ queryKey: ["setup"], queryFn: api.setup });
  const projects = useQuery({ queryKey: ["projects"], queryFn: api.projects });
  const readAloud = useReadAloudGroup();
  const topBarDetailed = useTopBarDetailed();
  // On a Mac, the VM everything runs in, whose size can be changed here.
  const hostSetup = useQuery({
    queryKey: ["host-setup"],
    queryFn: () => window.agentbox.hostSetup.status(),
    refetchInterval: 2_000,
  });
  const vm = hostSetup.data?.vm;
  // On a Linux machine that runs AgentBox itself: what there is to move into
  // AgentBox's VM, and how far a move got.
  const migration = useQuery({
    queryKey: ["vm-migration"],
    queryFn: () => window.agentbox.vmMigrate.status(),
    refetchInterval: 15_000,
  });
  const offered =
    migration.data &&
    (["available", "started"].includes(migration.data.state) ||
      (migration.data.state === "verified" && (migration.data.oldMachines?.length ?? 0) > 0));
  // Once shown, it stays for as long as the page is open, so the end of a
  // move, and of removing the old machines, can be read.
  const [migrationShown, setMigrationShown] = useState(false);
  useEffect(() => {
    if (offered) setMigrationShown(true);
  }, [offered]);
  const moving = offered || (migrationShown && migration.data?.state !== "none") ? (migration.data ?? null) : null;
  const voice = useVoiceSettings();
  // Phones pair with this machine's own daemon: not from a browser, nor with
  // an environment on a hub.
  const target = useQuery({ queryKey: ["target"], queryFn: () => window.agentbox.target.get(), staleTime: Infinity });
  const local = !("web" in window.agentbox) && target.data?.kind === "local";
  // On Linux in VM mode, AgentBox's Cloud Hypervisor VM, sized here too.
  const chv = hostSetup.data?.chv?.mode === "vm" ? hostSetup.data.chv : null;
  // On Linux, a machine set up to run agents itself before AgentBox ran in a
  // VM on Linux, which Setup moves into AgentBox's VM (the app's MovePrompt
  // opens it here).
  const linux = hostSetup.data?.linux;
  const hostMode = linux?.mode === "host";
  const s = settings.data;
  // changed is undefined until settings arrive, so nothing is marked on a
  // guess.
  const changed = (test: (s: T.Settings) => boolean) => (s ? test(s) : undefined);

  const stepEntry = (step: Step, alwaysShow = false) => ({
    id: step.id,
    label: step.title,
    keywords: `${step.description} ${step.detail ?? ""}`,
    render: () => (
      <ChecklistStep step={step} alwaysShow={alwaysShow} />
    ),
  });
  const needsLook = (list: Step[]) =>
    list.filter((step) => ["missing", "outdated", "warn"].includes(step.status)).length;
  const machineSteps = steps.filter((step) => environmentIds.has(step.id));
  const accountSteps = steps.filter((step) => accountIds.has(step.id));
  const aiSteps = accountSteps.filter((step) => step.id !== "github");
  const githubSteps = accountSteps.filter((step) => step.id === "github");

  const sections: SettingSection[] = [
    {
      id: "general",
      title: t("settings.section.general.title"),
      description: t("settings.section.general.description"),
      scope: "installation",
      groups: [
        {
          id: "appearance",
          title: t("settings.group.appearance.title"),
          entries: [
            {
              id: "appearance",
              label: t("settings.entry.appearance.label"),
              keywords: t("settings.entry.appearance.keywords"),
              modified: theme.data ? theme.data.appearance !== "follow" : undefined,
              render: () => <Appearance />,
            },
            {
              id: "language",
              label: t("common.language"),
              keywords: t("settings.languageKeywords"),
              modified: changed((s) => s.language !== T.DefaultLanguage),
              render: () => <LanguageSetting />,
            },
            {
              id: "topbar-detailed",
              label: t("settings.entry.topbar-detailed.label"),
              keywords: t("settings.entry.topbar-detailed.keywords"),
              modified: topBarDetailed,
              render: () => <TopBarDetailedSetting />,
            },
          ],
        },
        {
          id: "updates",
          title: t("settings.group.updates.title"),
          entries: [
            {
              id: "update-check",
              label: t("settings.entry.update-check.label"),
              keywords: t("settings.entry.update-check.keywords"),
              modified: changed((s) => !s.updateCheck),
              render: () => <UpdateCheck />,
            },
            {
              id: "update-channel",
              label: t("settings.entry.update-channel.label"),
              keywords: t("settings.entry.update-channel.keywords"),
              modified: update.data ? update.data.channel !== (update.data.nightly ? "nightly" : "stable") : undefined,
              render: () => <UpdateChannel />,
            },
            {
              id: "usage-stats",
              label: t("settings.entry.usage-stats.label"),
              keywords: t("settings.entry.usage-stats.keywords"),
              modified: changed((s) => !s.usageStats),
              render: () => <UsageStats />,
            },
            {
              id: "error-reports",
              label: t("settings.entry.error-reports.label"),
              keywords: t("settings.entry.error-reports.keywords"),
              modified: changed((s) => s.errorReports),
              render: () => <ErrorReportsSetting />,
            },
            ...(info
              ? [
                  {
                    id: "whats-new",
                    label: t("settings.entry.whats-new.label"),
                    keywords: t("settings.entry.whats-new.keywords"),
                    render: () => <WhatsNewRow version={info.version} />,
                  },
                ]
              : []),
          ],
        },
        {
          id: "help",
          title: t("settings.group.help.title"),
          entries: [
            {
              id: "report-problem",
              label: t("settings.entry.report-problem.label"),
              keywords: t("settings.entry.report-problem.keywords"),
              render: () => <ReportProblemRow />,
            },
          ],
        },
      ],
      footer: info && (
        <p className="text-center font-mono text-[11px] text-faint">
          AgentBox {isNightly(info.version) ? `Nightly ${info.version}` : info.version} · Electron {info.electron} · {info.socket}
        </p>
      ),
    },
    ...(local
      ? [
          {
            id: "phone",
            title: t("settings.section.phone.title"),
            description: t("settings.section.phone.description"),
            scope: "installation" as const,
            groups: phoneGroups(),
          },
        ]
      : []),
    {
      id: "skills",
      title: t("settings.section.skills.title"),
      description: t("settings.section.skills.description"),
      scope: "installation",
      groups: [
        {
          id: "skills",
          title: t("settings.group.skills.title"),
          cards: true,
          entries: [
            {
              id: "skills",
              label: t("settings.section.skills.title"),
              keywords: t("settings.entry.skills.keywords"),
              render: () => <SkillsPanel embedded />,
            },
          ],
        },
      ],
    },
    {
      id: "connectors",
      title: t("settings.section.connectors.title"),
      description: t("settings.section.connectors.description"),
      scope: "installation",
      groups: [
        {
          id: "connectors",
          title: t("settings.group.connectors.title"),
          cards: true,
          entries: [
            {
              id: "connectors",
              label: t("settings.section.connectors.title"),
              keywords: t("settings.entry.connectors.keywords"),
              render: () => <ConnectorsTab target="" embedded />,
            },
          ],
        },
      ],
    },
    {
      id: "voice",
      title: t("settings.section.voice.title"),
      description: t("settings.section.voice.description"),
      scope: "installation",
      groups: [pushToTalkGroup(voice), readAloud],
    },
    {
      id: "models",
      title: t("settings.section.models.title"),
      description: t("settings.section.models.description"),
      scope: "installation",
      groups: [
        {
          id: "new-agents",
          title: t("settings.group.new-agents.title"),
          description: t("settings.group.new-agents.description"),
          entries: [
            {
              id: "agent-model",
              label: t("settings.entry.agent-model.label"),
              keywords: t("settings.entry.agent-model.keywords"),
              modified: changed((s) => s.defaultClaudeModel !== ""),
              render: () => <DefaultModel role="agents" />,
            },
            {
              id: "agent-window",
              label: t("settings.entry.agent-window.label"),
              keywords: t("settings.entry.agent-window.keywords"),
              modified: changed((s) => s.defaultAgentContextWindow !== ""),
              render: () => <DefaultContextWindow role="agents" />,
            },
            {
              id: "agent-effort",
              label: t("settings.entry.agent-effort.label"),
              keywords: t("settings.entry.agent-effort.keywords"),
              modified: changed((s) => s.defaultClaudeEffort !== ""),
              render: () => <NewAgentEffort />,
            },
            {
              id: "cursor-model",
              label: t("settings.entry.cursor-model.label"),
              keywords: t("settings.entry.cursor-model.keywords"),
              modified: changed((s) => s.defaultCursorModel !== ""),
              render: () => <CursorDefaults part="model" />,
            },
            {
              id: "cursor-effort",
              label: t("settings.entry.cursor-effort.label"),
              keywords: t("settings.entry.cursor-effort.keywords"),
              modified: changed((s) => s.defaultCursorEffort !== ""),
              render: () => <CursorDefaults part="effort" />,
            },
            {
              id: "enforce",
              label: t("settings.entry.enforce.label"),
              keywords: t("settings.entry.enforce.keywords"),
              modified: changed((s) => s.enforceAgentDefaults),
              render: () => <EnforceAgentDefaults />,
            },
          ],
        },
        {
          id: "lead",
          title: t("settings.group.lead.title"),
          description: t("settings.group.lead.description"),
          entries: [
            {
              id: "lead-model",
              label: t("settings.entry.lead-model.label"),
              keywords: t("settings.entry.lead-model.keywords"),
              modified: changed((s) => s.defaultLeadModel !== ""),
              render: () => <DefaultModel role="lead" />,
            },
            {
              id: "lead-window",
              label: t("settings.entry.lead-window.label"),
              keywords: t("settings.entry.lead-window.keywords"),
              modified: changed((s) => s.defaultLeadContextWindow !== ""),
              render: () => <DefaultContextWindow role="lead" />,
            },
          ],
        },
        {
          id: "chats",
          title: t("settings.group.chats.title"),
          description: t("settings.group.chats.description"),
          entries: [
            {
              id: "compact",
              label: t("settings.entry.compact.label"),
              keywords: t("settings.entry.compact.keywords"),
              modified: changed((s) => s.claudeCompactWindow !== s.defaultClaudeCompactWindow),
              render: () => <CompactWindow />,
            },
            {
              id: "resume",
              label: t("settings.entry.resume.label"),
              keywords: t("settings.entry.resume.keywords"),
              modified: changed((s) => !s.resumeAfterLimit),
              render: () => <ResumeAfterLimit />,
            },
            {
              id: "continue-after-restart",
              label: t("settings.entry.continueAfterRestart.label"),
              keywords: t("settings.entry.continueAfterRestart.keywords"),
              modified: changed((s) => !s.continueAfterRestart),
              render: () => <ContinueAfterRestart />,
            },
          ],
        },
      ],
      // The lead saves a preference like this with remember, and its brief has
      // it search memory before every create_agent (lead.md.tmpl, "What the
      // user asked of agents").
      footer: (
        <p
          data-lead-preference-tip
          className="flex items-start gap-2 px-1 text-[12px] leading-relaxed text-subtle"
        >
          <Lightbulb className="mt-0.5 size-3.5 shrink-0 text-brand-300" />
          <span>
            {t("settings.models.tip")}
          </span>
        </p>
      ),
    },
    {
      id: "agents",
      title: t("settings.section.agents.title"),
      description: t("settings.section.agents.description"),
      scope: "installation",
      groups: [
        {
          id: "pulls",
          title: t("settings.group.pulls.title"),
          description: t("settings.group.pulls.description"),
          entries: [
            {
              id: "pr-watch",
              label: t("settings.entry.pr-watch.label"),
              keywords: t("settings.entry.pr-watch.keywords"),
              modified: changed((s) => !s.prWatch),
              render: () => <PRWatch />,
            },
          ],
        },
        {
          id: "tasks",
          title: t("settings.group.tasks.title"),
          description: t("settings.group.tasks.description"),
          entries: [
            {
              id: "task-target",
              label: t("settings.entry.task-target.label"),
              keywords: t("settings.entry.task-target.keywords"),
              modified: changed((s) => s.taskTarget === "lead"),
              render: () => <TaskTarget />,
            },
            {
              id: "lead-recheck",
              label: t("settings.entry.lead-recheck.label"),
              keywords: t("settings.entry.lead-recheck.keywords"),
              modified: changed((s) => s.leadRecheck),
              render: () => <LeadRecheck />,
            },
          ],
        },
        {
          id: "lifecycle",
          title: t("settings.group.lifecycle.title"),
          entries: [
            {
              id: "auto-stop",
              label: t("settings.entry.auto-stop.label"),
              keywords: t("settings.entry.auto-stop.keywords"),
              modified: changed((s) => s.autoStopIdle),
              render: () => <AutoStopIdle />,
            },
            {
              id: "docker-prune",
              label: t("settings.entry.docker-prune.label"),
              keywords: t("settings.entry.docker-prune.keywords"),
              modified: changed((s) => !s.dockerPruneOnStop),
              render: () => <DockerPruneOnStop />,
            },
            {
              id: "media-retention",
              label: t("settings.entry.media-retention.label"),
              keywords: t("settings.entry.media-retention.keywords"),
              modified: changed((s) => s.mediaRetention !== "1d"),
              render: () => <MediaRetention />,
            },
          ],
        },
      ],
    },
    {
      id: "resources",
      title: t("settings.section.resources.title"),
      description: t("settings.section.resources.description"),
      scope: "installation",
      groups: [
        {
          id: "disk",
          title: t("settings.group.disk.title"),
          entries: [
            {
              id: "disk-floor",
              label: t("settings.entry.disk-floor.label"),
              keywords: t("settings.entry.disk-floor.keywords"),
              modified: changed((s) => s.diskFloorMin !== 10 * 1024 ** 3 || s.diskFloorPercent !== 5),
              render: () => <DiskFloor />,
            },
            {
              id: "docker-image-cache",
              label: t("settings.entry.docker-image-cache.label"),
              keywords: t("settings.entry.docker-image-cache.keywords"),
              modified: changed((s) => !s.imageCache || s.imageCacheMaxBytes !== s.defaultImageCacheMaxBytes),
              render: () => <DockerImageCache />,
            },
            {
              id: "package-caches",
              label: t("settings.entry.package-caches.label"),
              keywords: t("settings.entry.package-caches.keywords"),
              modified: changed((s) => !s.packageCache || s.packageCacheMaxBytes !== s.defaultPackageCacheMaxBytes),
              render: () => <SharedPackageCaches />,
            },
          ],
        },
        ...(vm?.exists || chv
          ? [
              {
                id: "vm",
                title: t("settings.group.vm.title"),
                cards: true,
                entries: [
                  {
                    id: "vm-size",
                    label: t("settings.entry.vm-size.label"),
                    keywords: t("settings.entry.vm-size.keywords"),
                    render: () =>
                      chv ? (
                        <CHVSize vm={chv} busy={hostSetup.data?.resizing === true} />
                      ) : (
                        <VMSize vm={vm!} busy={hostSetup.data?.resizing === true} />
                      ),
                  },
                  {
                    id: "vm-swap",
                    label: t("settings.entry.vm-swap.label"),
                    keywords: t("settings.entry.vm-swap.keywords"),
                    render: () =>
                      chv ? (
                        <VMSwap swap={chv.swap} running={chv.state === "running"} />
                      ) : (
                        <VMSwap swap={vm!.swap} running={vm!.status === "Running"} />
                      ),
                  },
                ],
              },
            ]
          : []),
      ],
    },
    {
      id: "accounts",
      title: t("settings.section.accounts.title"),
      description: t("settings.section.accounts.description"),
      scope: "installation",
      attention: needsLook(accountSteps),
      groups: [
        {
          id: "ai",
          title: t("settings.group.ai.title"),
          cards: true,
          // Claude Code's and Cursor's are managed here even once they're done:
          // accounts to add, a key to change, a sign-in to end.
          entries: aiSteps.map((step) => stepEntry(step, step.id === "claude" || step.id === "cursor")),
        },
        {
          id: "github",
          title: t("settings.group.github.title"),
          cards: true,
          entries: githubSteps.map((step) => stepEntry({ ...step, body: step.settingsBody ?? step.body }, true)),
        },
      ],
    },
    {
      id: "setup",
      title: t("settings.section.setup.title"),
      description: t("settings.section.setup.description"),
      scope: "installation",
      attention: needsLook(machineSteps),
      header: (
        <div className="grid gap-2 px-1">
          <div className="flex flex-wrap items-center gap-3">
            <span
              className="text-[13px] tabular-nums text-muted"
              data-setup-progress={`${done}/${total}`}
            >
              {t("settings.setup.requiredReady", { done, total })}
            </span>
            <Button variant="ghost" size="sm" className="ml-auto" onClick={onWizard}>
              <Wand />
              {t("settings.setup.runAgain")}
            </Button>
          </div>
          <div className="h-1.5 overflow-hidden rounded-full bg-surface-raised">
            <div
              className="h-full rounded-full bg-gradient-to-r from-brand-400 to-emerald-400 transition-all duration-500"
              style={{ width: `${total ? (done / total) * 100 : 0}%` }}
            />
          </div>
        </div>
      ),
      groups: [
        ...(hostMode || moving
          ? [
              {
                id: "where",
                title: t("settings.group.where.title"),
                cards: true,
                entries: [
                  {
                    id: "move-to-vm",
                    label: t("settings.entry.move-to-vm.label"),
                    keywords: `${moveToVMKeywords()} ${t("settings.setup.moveKeywords")}`,
                    // Once moved, the machine is in VM mode: what's left is
                    // the move's result, and removing the old machines.
                    render: () =>
                      moving && ["verified", "removed"].includes(moving.state) ? (
                        <VMMigrate migration={moving} kvm />
                      ) : (
                        <MoveToVM
                          kvm={linux?.kvm ?? false}
                          command={<CommandBox command={`${agentbox} vm migrate`} />}
                          action={moving ? <VMMigrate migration={moving} kvm={linux?.kvm ?? false} embedded /> : undefined}
                        />
                      ),
                  },
                ],
              },
            ]
          : []),
        {
          id: "required",
          title: t("settings.group.required.title"),
          cards: true,
          entries: machineSteps
            .filter((step) => step.required)
            .map((step) =>
              stepEntry(
                step,
                (step.id === "image" && imageJob !== null) ||
                  (step.id === "incus" && hostSetupRan),
              ),
            ),
        },
        {
          id: "optional",
          title: t("settings.group.optional.title"),
          cards: true,
          entries: machineSteps.filter((step) => !step.required).map((step) => stepEntry(step)),
        },
        {
          id: "image",
          title: t("settings.group.image.title"),
          entries: [
            {
              id: "opencode-image",
              label: t("settings.entry.opencode-image.label"),
              keywords: t("settings.entry.opencode-image.keywords"),
              modified: setup.data ? setup.data.image.components.opencode : undefined,
              render: () => <OpenCodeInImage />,
            },
          ],
        },
      ],
    },
    ...(projects.data ?? []).map(projectSection),
  ];

  return (
    <SettingsPage
      sections={sections.map((section) => ({
        ...section,
        groups: section.groups.filter((g) => g.entries.length > 0),
      }))}
      icons={sectionIcons}
      error={error}
    />
  );
}

const sectionIcons: SectionIcons = {
  general: SlidersHorizontal,
  models: Sparkles,
  voice: Mic,
  skills: Wand2,
  connectors: Plug,
  phone: Smartphone,
  agents: Bot,
  resources: Cpu,
  accounts: KeyRound,
  setup: Wrench,
};

// EnforceAgentDefaults decides what the model and window above mean to a
// project's lead: the only ones it may give the agents it creates, or the most
// it may. agent.CheckLeadChoice refuses the rest on create_agent, and the
// lead's brief and create_agent's description say which, so keep this text in
// step with lead.md.tmpl and chatSettingParams (internal/cli/mcp.go).
function EnforceAgentDefaults() {
  const t = useT();
  const settings = useQuery({ queryKey: ["settings"], queryFn: api.settings });
  const queryClient = useQueryClient();
  const save = useMutation({
    mutationFn: (enforceAgentDefaults: boolean) =>
      api.updateSettings({ enforceAgentDefaults }),
    onSuccess: (next) => queryClient.setQueryData(["settings"], next),
    onError: (err) => toast.error(errorMessage(err)),
  });
  const enforced = settings.data?.enforceAgentDefaults ?? false;
  return (
    <SettingRow
      label={t("settings.entry.enforce.label")}
      description={
        enforced
          ? t("settings.enforce.descriptionOn")
          : t("settings.enforce.descriptionOff")
      }
      details={
        <>
          {enforced ? t("settings.enforce.detailsOn") : t("settings.enforce.detailsOff")}{" "}
          {t("settings.enforce.detailsNote")}
        </>
      }
      control={
        <Switch
          data-enforce-agent-defaults
          aria-label={t("settings.entry.enforce.label")}
          disabled={save.isPending || settings.data === undefined}
          checked={enforced}
          onCheckedChange={(next) => save.mutate(next)}
        />
      }
    />
  );
}

// UpdateCheck is the daily request that asks whether a newer AgentBox is out,
// which is also how installations are counted. The label says exactly what the
// request carries, as the README's "Update check" does; keep them in step with
// internal/update. When something outside this switch keeps the check off — a
// development build, or AGENTBOX_NO_UPDATE_CHECK / DO_NOT_TRACK in the
// daemon's environment — it says so, rather than a switch that does nothing.
function UpdateCheck() {
  const t = useT();
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
    <SettingRow
      label={t("settings.entry.update-check.label")}
      description={t("settings.updateCheck.description")}
      details={t("settings.updateCheck.details", {
        version: update.data?.current ? ` (${update.data.current})` : "",
      })}
      control={
        <Switch
          data-update-check
          aria-label={t("settings.entry.update-check.label")}
          disabled={save.isPending || settings.data === undefined || !!blocked}
          checked={!blocked && (settings.data?.updateCheck ?? true)}
          onCheckedChange={(next) => save.mutate(next)}
        />
      }
    >
      {blocked && <SettingNote>{t("settings.update.offBecause", { reason: blocked })}</SettingNote>}
      {available && isUpgrade(available.version, update.data!.current) && (
        <SettingNote>
          {t("settings.updateCheck.isOut", { version: available.version })}{" "}
          <button
            className="rounded text-brand-300 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-400/50"
            onClick={() => void openLatestRelease(api.latestRelease, window.agentbox.openExternal, available.url)}
          >
            {t("settings.updateCheck.seeWhatsNew")}
          </button>
        </SettingNote>
      )}
    </SettingRow>
  );
}

const channels = [
  { value: "stable", label: "settings.channel.stable", icon: CircleCheck },
  { value: "nightly", label: "settings.channel.nightly", icon: MoonStar },
] as const satisfies readonly { value: string; label: MessageKey; icon: unknown }[];

// UpdateChannel picks what the update check offers: stable releases only, or
// the nightly builds as well (internal/update's Offer). A nightly build starts
// out on nightly. Going back to stable offers the latest stable release though
// its version is lower, which the note says rather than calling it an update.
function UpdateChannel() {
  const t = useT();
  const update = useQuery({
    queryKey: ["update"],
    queryFn: api.update,
    staleTime: Infinity,
  });
  const queryClient = useQueryClient();
  const set = useMutation({
    mutationFn: (updateChannel: string) => api.updateSettings({ updateChannel }),
    // The daemon answers with the settings, and pushes the new update status
    // as an event once it has checked again; until then, show the choice, and
    // read the status again in case the check was quicker than this.
    onSuccess: (_, updateChannel) => {
      queryClient.setQueryData<T.UpdateStatus>(["update"], (old) => old && { ...old, channel: updateChannel, available: undefined });
      void queryClient.invalidateQueries({ queryKey: ["update"] });
    },
    onError: (err) => toast.error(errorMessage(err)),
  });

  const current = update.data?.channel;
  const available = update.data?.available;
  const backToStable = !!available && current === "stable" && !!update.data?.nightly && !isUpgrade(available.version, update.data.current);
  const appUpdate = useAppUpdate();
  return (
    <SettingRow
      label={t("settings.entry.update-channel.label")}
      description={t("settings.channel.description")}
      details={t("settings.channel.details")}
    >
      <div
        className="inline-flex w-fit rounded-xl border border-line bg-rail p-0.5"
        role="radiogroup"
        aria-label={t("settings.entry.update-channel.label")}
        data-update-channel={current}
      >
        {channels.map(({ value, label, icon: Icon }) => (
          <button
            key={value}
            role="radio"
            aria-checked={current === value}
            data-channel={value}
            disabled={set.isPending || update.data === undefined || !!update.data.blocked}
            onClick={() => set.mutate(value)}
            className={cn(
              "inline-flex h-8 items-center gap-1.5 rounded-[10px] px-3 text-[12.5px] font-medium transition focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-400/50 disabled:opacity-50",
              current === value
                ? "bg-surface-strong text-title shadow-[inset_0_1px_0_rgb(255_255_255/0.08)]"
                : "text-muted hover:text-primary",
            )}
          >
            <Icon className="size-[15px]" />
            {t(label)}
          </button>
        ))}
      </div>
      {update.data?.blocked && <SettingNote>{t("settings.update.offBecause", { reason: update.data.blocked })}</SettingNote>}
      {backToStable && (
        <SettingNote>
          {t("settings.channel.backToStable", { version: available.version })}{" "}
          <button
            className="rounded text-brand-300 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-400/50"
            title={updateHint(appUpdate.support, available.version)}
            disabled={appUpdate.updating}
            onClick={() => void appUpdate.start(available.url)}
          >
            {appUpdate.label ?? t("settings.channel.getIt")}
          </button>
        </SettingNote>
      )}
    </SettingRow>
  );
}

// PRWatch is the pull request watch (internal/daemon/prwatch.go): every
// agent's open pull request, until it's merged or closed. A project can
// override it from its own settings.
export function PRWatch() {
  const t = useT();
  const settings = useQuery({ queryKey: ["settings"], queryFn: api.settings });
  const queryClient = useQueryClient();
  const save = useMutation({
    mutationFn: (prWatch: boolean) => api.updateSettings({ prWatch }),
    onSuccess: (next) => {
      queryClient.setQueryData(["settings"], next);
      void queryClient.invalidateQueries({ queryKey: ["projects"] });
    },
    onError: (err) => toast.error(errorMessage(err)),
  });
  return (
    <SettingRow
      label={t("settings.entry.pr-watch.label")}
      htmlFor="pr-watch"
      description={t("settings.prWatch.description")}
      details={t("settings.prWatch.details")}
      control={
        <Switch
          id="pr-watch"
          data-pr-watch
          aria-label={t("settings.entry.pr-watch.label")}
          disabled={save.isPending || settings.data === undefined}
          checked={settings.data?.prWatch ?? true}
          onCheckedChange={(next) => save.mutate(next)}
        />
      }
    />
  );
}

// UsageStats rides on the update check: the same request's day, carrying how
// many times each feature was used (internal/daemon/usagestats.go), and the
// anonymous events (usageevents.go). Its lines say what it sends, and "See
// what's sent" shows it exactly; keep them in step with the README's "Update
// check" section. It can't be on while the check is off or blocked,
// and says which, rather than a switch that does nothing.
function UsageStats() {
  const t = useT();
  const settings = useQuery({ queryKey: ["settings"], queryFn: api.settings });
  const update = useQuery({
    queryKey: ["update"],
    queryFn: api.update,
    staleTime: Infinity,
  });
  const queryClient = useQueryClient();
  const save = useMutation({
    mutationFn: (usageStats: boolean) => api.updateSettings({ usageStats }),
    onSuccess: (next) => queryClient.setQueryData(["settings"], next),
    onError: (err) => toast.error(errorMessage(err)),
  });

  const [seeing, setSeeing] = useState(false);
  const blocked = update.data?.blocked;
  const checkOff = settings.data?.updateCheck === false;
  return (
    <SettingRow
      label={t("settings.entry.usage-stats.label")}
      description={t("settings.usageStats.description")}
      details={t("settings.usageStats.details")}
      control={
        <Switch
          data-usage-stats
          aria-label={t("settings.entry.usage-stats.label")}
          disabled={
            save.isPending || settings.data === undefined || !!blocked || checkOff
          }
          checked={!blocked && !checkOff && (settings.data?.usageStats ?? true)}
          onCheckedChange={(next) => save.mutate(next)}
        />
      }
    >
      {(blocked || checkOff) && (
        <SettingNote>
          {t("settings.update.offBecause", { reason: blocked ?? t("settings.usageStats.checkOff") })}
        </SettingNote>
      )}
      <div>
        <Button variant="ghost" size="sm" data-usage-see onClick={() => setSeeing(true)}>
          {t("settings.usageStats.see")}
        </Button>
      </div>
      <UsageSentDialog open={seeing} onOpenChange={setSeeing} />
    </SettingRow>
  );
}

// ErrorReportsSetting is whether the app sends a report of each uncaught
// error by itself (components/ErrorReports.tsx), which it offers the first
// time one happens.
// TopBarDetailedSetting is the top bar's layout on this computer: one status
// bubble for the machine, or its memory, CPU and disk always in view
// (lib/topBarLayout.ts).
function TopBarDetailedSetting() {
  const t = useT();
  const on = useTopBarDetailed();
  return (
    <SettingRow
      label={t("settings.entry.topbar-detailed.label")}
      description={t("settings.topbarDetailed.description")}
      control={
        <Switch
          data-topbar-detailed-setting
          aria-label={t("settings.entry.topbar-detailed.label")}
          checked={on}
          onCheckedChange={setTopBarDetailed}
        />
      }
    />
  );
}

function ErrorReportsSetting() {
  const t = useT();
  const settings = useQuery({ queryKey: ["settings"], queryFn: api.settings });
  const queryClient = useQueryClient();
  const save = useMutation({
    mutationFn: (errorReports: boolean) => api.updateSettings({ errorReports }),
    onSuccess: (next) => queryClient.setQueryData(["settings"], next),
    onError: (err) => toast.error(errorMessage(err)),
  });
  return (
    <SettingRow
      label={t("settings.entry.error-reports.label")}
      description={t("settings.errorReports.description")}
      details={t("settings.errorReports.details")}
      control={
        <Switch
          data-error-reports
          aria-label={t("settings.entry.error-reports.label")}
          disabled={save.isPending || settings.data === undefined}
          checked={settings.data?.errorReports ?? false}
          onCheckedChange={(next) => save.mutate(next)}
        />
      }
    />
  );
}

// ReportProblemRow opens "Report a problem" (components/ReportDialog.tsx).
function ReportProblemRow() {
  const t = useT();
  const [open, setOpen] = useState(false);
  return (
    <SettingRow
      label={t("settings.entry.report-problem.label")}
      description={t("settings.reportProblem.description")}
      control={
        <Button variant="secondary" data-report-problem onClick={() => setOpen(true)}>
          {t("settings.reportProblem.button")}
        </Button>
      }
    >
      <ReportDialog open={open} onOpenChange={setOpen} />
    </SettingRow>
  );
}

// WhatsNewRow opens the same dialog App shows once after an update, so it can
// be read again any time.
function WhatsNewRow({ version }: { version: string }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  return (
    <SettingRow
      label={t("settings.entry.whats-new.label")}
      description={t("settings.whatsNew.description")}
      control={
        <Button variant="secondary" onClick={() => setOpen(true)}>
          {t("settings.whatsNew.show")}
        </Button>
      }
    >
      <WhatsNewDialog open={open} onOpenChange={setOpen} version={version} />
    </SettingRow>
  );
}

// LanguageSetting is the language the app speaks. Picking one retranslates the
// whole window at once (main.tsx's Root follows the setting); the CLI, the
// brief and the agents' chats stay in English, which the description says.
function LanguageSetting() {
  const t = useT();
  const queryClient = useQueryClient();
  const settings = useQuery({ queryKey: ["settings"], queryFn: api.settings });
  const set = useMutation({
    mutationFn: (language: string) => api.updateSettings({ language }),
    onSuccess: (next) => queryClient.setQueryData(["settings"], next),
    onError: (err) => toast.error(errorMessage(err)),
  });
  const current = settings.data?.language ?? t.lang;
  return (
    <SettingRow label={t("common.language")} description={t("common.languageDescription")}>
      <div
        className="inline-flex w-fit flex-wrap rounded-xl border border-line bg-rail p-0.5"
        role="radiogroup"
        aria-label={t("common.language")}
        data-language-choice
      >
        {languages.map(({ tag, name }) => (
          <button
            key={tag}
            role="radio"
            lang={tag}
            aria-checked={current === tag}
            data-language={tag}
            disabled={set.isPending || settings.data === undefined}
            onClick={() => set.mutate(tag)}
            className={cn(
              "inline-flex h-8 items-center gap-1.5 rounded-[10px] px-3 text-[12.5px] font-medium transition focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-400/50 disabled:opacity-50",
              current === tag
                ? "bg-surface-strong text-title shadow-[inset_0_1px_0_rgb(255_255_255/0.08)]"
                : "text-muted hover:text-primary",
            )}
          >
            <Languages className="size-[15px]" />
            {name}
          </button>
        ))}
      </div>
    </SettingRow>
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
  label: MessageKey;
  icon: typeof Monitor;
}[] = [
  { value: "follow", label: "settings.appearance.follow", icon: Monitor },
  { value: "light", label: "settings.appearance.light", icon: Sun },
  { value: "dark", label: "settings.appearance.dark", icon: Moon },
];

function Appearance() {
  const t = useT();
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
    <SettingRow
      label={t("settings.entry.appearance.label")}
      description={t("settings.appearance.description")}
      details={t("settings.appearance.details")}
    >
      <div
        className="inline-flex w-fit rounded-xl border border-line bg-rail p-0.5"
        role="radiogroup"
        aria-label={t("settings.entry.appearance.label")}
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
              "inline-flex h-8 items-center gap-1.5 rounded-[10px] px-3 text-[12.5px] font-medium transition focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-400/50 disabled:opacity-50",
              current === value
                ? "bg-surface-strong text-title shadow-[inset_0_1px_0_rgb(255_255_255/0.08)]"
                : "text-muted hover:text-primary",
            )}
          >
            <Icon className="size-[15px]" />
            {t(label)}
          </button>
        ))}
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
              {t.rich(
                current === "follow"
                  ? "settings.appearance.onThemeFollowing"
                  : "settings.appearance.onThemeNot",
                {
                  name: (
                    <span className="text-tertiary">
                      {theme.data.name || t("settings.appearance.noName")}
                    </span>
                  ),
                  mode: theme.data.mode === "light" ? "light" : "dark",
                },
              )}
            </span>
          </div>
        ) : (
          <SettingNote>
            {t("settings.appearance.noTheme")}
          </SettingNote>
        ))}
    </SettingRow>
  );
}

// savedAtLabel says when a token was stored. A token stored before AgentBox
// recorded the date arrives as Go's zero time, which is no date to show.
function savedAtLabel(savedAt: string): string {
  const at = new Date(savedAt);
  if (!savedAt || Number.isNaN(at.getTime()) || at.getUTCFullYear() < 2000)
    return t("settings.accounts.savedUnknown");
  return t("settings.accounts.savedOn", { date: formatDate(at, {}) });
}

// ClaudeAccounts lists the stored Claude Code logins and adds more. Agents use
// the account their project picked, and otherwise the default one. A token
// Anthropic no longer accepts is badged rejected, so a dead login shows here
// rather than as a 401 inside an agent.
export function ClaudeAccounts({ accounts }: { accounts: T.ClaudeAccount[] }) {
  const t = useT();
  const queryClient = useQueryClient();
  const projects = useQuery({ queryKey: ["projects"], queryFn: api.projects });
  const notDefault = (projects.data ?? []).filter((p) => p.claudeAccount);
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
          t("settings.accounts.nProjects", { count: got.projects.length }),
        got.agents.length > 0 &&
          t("settings.accounts.nAgents", { count: got.agents.length }),
      ].filter((x): x is string => !!x);
      toast(t("settings.accounts.renamed", { old: got.old, name: got.name }), {
        description:
          (carried.length > 0
            ? `${t("settings.accounts.movedWithIt", { what: formatList(carried) })} `
            : "") + t("settings.accounts.sameToken"),
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
      toast(t("settings.accounts.newAgentsUse", { name }));
      await refresh();
    },
  });
  const remove = useMutation({
    mutationFn: (name: string) => api.removeClaudeAccount(name),
    onSuccess: async (_, name) => {
      toast(t("settings.accounts.removedClaude", { name }), {
        description: t("settings.accounts.removedNote"),
      });
      await refresh();
    },
  });
  const error = makeDefault.error ?? remove.error ?? rename.error;

  return (
    <div className="grid gap-3">
      {accounts.length > 0 && (
        <p className="text-[12.5px] text-subtle">
          {t("settings.accounts.defaultReaches")}
          {notDefault.length > 0 &&
            ` ${t("settings.accounts.theRest", { names: notDefault.map((p) => p.name).join(", ") })}`}
        </p>
      )}
      {accounts.length > 0 && (
        <ul className="grid gap-1.5" aria-label={t("settings.accounts.claudeList")}>
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
                    aria-label={t("settings.accounts.newNameFor", { name: acc.name })}
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
                    {t("common.rename")}
                  </Button>
                  <Button
                    type="button"
                    size="sm"
                    variant="ghost"
                    disabled={rename.isPending}
                    onClick={() => setRenaming(null)}
                  >
                    {t("common.cancel")}
                  </Button>
                </form>
              ) : (
                <span className="truncate font-mono text-[12.5px] text-secondary">
                  {acc.name}
                </span>
              )}
              {acc.default && <Badge variant="brand">{t("settings.accounts.badgeDefault")}</Badge>}
              {acc.valid === "rejected" && (
                <Badge variant="danger">{t("settings.accounts.badgeRejected")}</Badge>
              )}
              {acc.valid === "valid" && <Badge variant="success">{t("settings.accounts.badgeValid")}</Badge>}
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
                    {t("settings.accounts.makeDefault")}
                  </Button>
                )}
                <Button
                  size="icon-sm"
                  variant="ghost"
                  aria-label={t("settings.accounts.renameAccount", { name: acc.name })}
                  disabled={rename.isPending}
                  onClick={() => startRename(acc.name)}
                >
                  <Pencil />
                </Button>
                <Button
                  size="icon-sm"
                  variant="ghost"
                  aria-label={t("settings.accounts.removeAccount", { name: acc.name })}
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
          {t("settings.accounts.pasteToken")}
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
  const t = useT();
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
        {t.rich("settings.claudeLogin.intro", { code: (c) => <Code>{c}</Code> })}
      </p>
      <div className="flex flex-wrap items-end gap-2">
        <div className="grid gap-1.5">
          <Label htmlFor="claude-login-account">{t("settings.accounts.account")}</Label>
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
          {accounts.length === 0 ? t("settings.claudeLogin.logIn") : t("settings.claudeLogin.addAccount")}
        </Button>
        {running && (
          <Button
            variant="ghost"
            disabled={cancel.isPending}
            onClick={() => cancel.mutate()}
          >
            {t("common.cancel")}
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
                ? t("settings.claudeLogin.saved", { account: login.data.account })
                : login.data?.status === "cancelled"
                  ? t("settings.claudeLogin.cancelled")
                  : login.data?.status === "failed"
                    ? t("settings.claudeLogin.failed")
                    : url
                      ? t("settings.claudeLogin.waiting")
                      : codeUrl
                        ? t("settings.claudeLogin.approveThenCode")
                        : t("settings.claudeLogin.starting")}
            </span>
            {url && running && (
              <Button
                size="sm"
                variant="secondary"
                className="ml-auto"
                onClick={() => void window.agentbox.openExternal(url)}
              >
                <ExternalLink />
                {t("settings.claudeLogin.openAgain")}
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
                {t.rich(
                  url ? "settings.claudeLogin.codeHintAlt" : "settings.claudeLogin.codeHint",
                  {
                    link: (c) => (
                      <button
                        type="button"
                        className="text-brand-300 underline-offset-2 hover:underline"
                        onClick={() => void window.agentbox.openExternal(codeUrl)}
                      >
                        {c}
                      </button>
                    ),
                  },
                )}
              </p>
              <div className="flex flex-wrap items-center gap-2">
                <Input
                  aria-label={t("settings.claudeLogin.codePlaceholder")}
                  placeholder={t("settings.claudeLogin.codePlaceholder")}
                  value={code}
                  onChange={(event) => setCode(event.target.value)}
                  className="w-64 font-mono"
                />
                <Button
                  type="submit"
                  variant="secondary"
                  disabled={!code.trim() || sendCode.isPending}
                >
                  {t("settings.claudeLogin.send")}
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
                t("settings.claudeLogin.saved", {
                  account: login.data?.account ?? "default",
                }),
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
  const t = useT();
  const [token, setToken] = useState("");
  const [account, setAccount] = useState("");
  const save = useMutation({
    mutationFn: () => api.saveClaudeToken(token, account.trim() || undefined),
    onSuccess: async () => {
      toast(
        t("settings.claudeLogin.saved", {
          account: account.trim() || "default",
        }),
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
        {t.rich("settings.claudeToken.intro", { code: (c) => <Code>{c}</Code> })}
      </p>
      <div className="flex flex-wrap items-end gap-2">
        <div className="grid gap-1.5">
          <Label htmlFor="claude-account-name">{t("settings.accounts.account")}</Label>
          <Input
            id="claude-account-name"
            placeholder={accounts.length === 0 ? "default" : "work"}
            value={account}
            onChange={(event) => setAccount(event.target.value)}
            className="w-32 font-mono"
          />
        </div>
        <div className="grid min-w-48 flex-1 gap-1.5">
          <Label htmlFor="claude-account-token">{t("settings.accounts.token")}</Label>
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
          {t("common.save")}
        </Button>
      </div>
      <p className="text-xs text-subtle">
        {t("settings.claudeToken.note")}
      </p>
      {save.error && <Notice>{errorMessage(save.error)}</Notice>}
    </form>
  );
}

// CursorAccount is Cursor's sign-in: an API key pasted here, or Cursor's own
// browser flow, which the daemon runs while this polls how far it got. Agents
// share the one sign-in; the key is never shown again once it is saved.
function CursorAccount({ command }: { command: string }) {
  const t = useT();
  const queryClient = useQueryClient();
  const auth = useQuery({ queryKey: ["auth"], queryFn: api.auth });
  const [key, setKey] = useState("");
  const opened = useRef("");
  const refresh = () =>
    Promise.all(
      ["auth", "setup", "settings"].map((k) =>
        queryClient.invalidateQueries({ queryKey: [k] }),
      ),
    );
  // Polled only once this window started a sign-in: a disabled query never
  // runs its refetchInterval.
  const [polling, setPolling] = useState(false);
  const login = useQuery({
    queryKey: ["cursor-login"],
    queryFn: api.cursorLogin,
    enabled: polling,
    refetchInterval: (query) =>
      query.state.data?.state === "starting" || query.state.data?.state === "waiting"
        ? 1000
        : false,
  });
  const start = useMutation({
    mutationFn: api.startCursorLogin,
    onSuccess: (next) => {
      opened.current = "";
      queryClient.setQueryData(["cursor-login"], next);
      setPolling(true);
    },
  });
  const state = login.data?.state;
  const url = login.data?.url;
  const waiting = state === "starting" || state === "waiting";
  const signedIn = auth.data?.cursor === true;

  // Open Cursor's page once, as the Claude login does; the link below it
  // opens it again.
  useEffect(() => {
    if (state !== "waiting" || !url || opened.current === url) return;
    opened.current = url;
    void window.agentbox.openExternal(url);
  }, [state, url]);
  useEffect(() => {
    if (state !== "done") return;
    toast(t("settings.cursor.signedIn"));
    void refresh();
    queryClient.setQueryData(["cursor-login"], { state: "idle" });
    setPolling(false);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state]);

  const save = useMutation({
    mutationFn: () => api.saveCursorKey(key.trim()),
    onSuccess: async () => {
      toast(t("settings.cursor.keySaved"));
      setKey("");
      await refresh();
    },
    onError: (err) => toast.error(errorMessage(err)),
  });
  const signOut = useMutation({
    mutationFn: api.removeCursorLogin,
    onSuccess: async () => {
      toast(t("settings.cursor.signedOut"));
      await refresh();
    },
    onError: (err) => toast.error(errorMessage(err)),
  });

  return (
    <div className="grid gap-3">
      <div className="flex flex-wrap items-center gap-2 text-[13px]">
        <StepIcon status={signedIn ? "ok" : "optional"} small />
        <span className="text-secondary">
          {signedIn
            ? auth.data?.cursorEmail
              ? t("settings.cursor.signedInAs", { email: auth.data.cursorEmail })
              : t("settings.cursor.signedInPlain")
            : t("settings.cursor.notSignedIn")}
        </span>
        {signedIn && (
          <Button
            size="sm"
            variant="ghost"
            className="ml-auto"
            disabled={signOut.isPending}
            onClick={() => signOut.mutate()}
          >
            {t("settings.cursor.signOut")}
          </Button>
        )}
      </div>
      <form
        className="flex flex-wrap items-end gap-2"
        onSubmit={(event) => {
          event.preventDefault();
          if (key.trim()) save.mutate();
        }}
      >
        <div className="grid gap-1.5">
          <Label htmlFor="cursor-api-key">{t("settings.cursor.keyLabel")}</Label>
          <Input
            id="cursor-api-key"
            type="password"
            autoComplete="off"
            spellCheck={false}
            placeholder={t("settings.cursor.keyPlaceholder")}
            value={key}
            onChange={(event) => setKey(event.target.value)}
            className="w-72 font-mono"
          />
        </div>
        <Button
          type="submit"
          variant="secondary"
          disabled={!key.trim() || save.isPending}
        >
          {save.isPending && <LoaderCircle className="animate-spin" />}
          {t("settings.cursor.saveKey")}
        </Button>
      </form>
      <div className="flex flex-wrap items-center gap-2">
        <Button
          variant="secondary"
          disabled={waiting || start.isPending}
          onClick={() => start.mutate()}
        >
          {waiting || start.isPending ? (
            <LoaderCircle className="animate-spin" />
          ) : (
            <LogIn />
          )}
          {t("settings.cursor.signIn")}
        </Button>
        {state === "waiting" && url && (
          <button
            type="button"
            className="inline-flex items-center gap-1 text-[13px] text-brand-300 underline-offset-2 hover:underline"
            onClick={() => void window.agentbox.openExternal(url)}
          >
            <ExternalLink className="size-3.5" />
            {t("settings.cursor.openPage")}
          </button>
        )}
      </div>
      {waiting && (
        <p className="text-xs text-subtle">
          {state === "waiting"
            ? t("settings.cursor.waiting")
            : t("settings.cursor.starting")}
        </p>
      )}
      {state === "failed" && (
        <Notice>{login.data?.error || t("settings.cursor.failed")}</Notice>
      )}
      {save.error && <Notice>{errorMessage(save.error)}</Notice>}
      {start.error && <Notice>{errorMessage(start.error)}</Notice>}
      <p className="text-[13px] text-muted">
        {t.rich("settings.cursor.cliBody", { code: (c) => <Code>{c}</Code> })}
      </p>
      <CommandBox command={command} />
    </div>
  );
}

// GitHubAccounts lists the stored GitHub logins and adds more. A project uses
// the account it picked on its own page; agents of the other projects use the
// default one.
export function GitHubAccounts({ accounts }: { accounts: T.GitHubAccount[] }) {
  const t = useT();
  const queryClient = useQueryClient();
  const projects = useQuery({ queryKey: ["projects"], queryFn: api.projects });
  const notDefault = (projects.data ?? []).filter((p) => p.githubAccount);
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
        t("settings.github.saved", {
          account: account.trim() || "default",
          user: result.user,
        }),
      );
      setToken("");
      setAccount("");
      await refresh();
    },
  });
  const makeDefault = useMutation({
    mutationFn: (name: string) => api.setDefaultGitHubAccount(name),
    onSuccess: async (_, name) => {
      toast(t("settings.accounts.newAgentsUse", { name }));
      await refresh();
    },
  });
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
      api.renameGitHubAccount(from, to),
    onSuccess: async (got) => {
      setRenaming(null);
      const carried = [
        got.projects.length > 0 &&
          t("settings.accounts.nProjects", { count: got.projects.length }),
        got.agents.length > 0 &&
          t("settings.accounts.nAgents", { count: got.agents.length }),
      ].filter((x): x is string => !!x);
      toast(t("settings.accounts.renamed", { old: got.old, name: got.name }), {
        description:
          (carried.length > 0
            ? `${t("settings.accounts.movedWithIt", { what: formatList(carried) })} `
            : "") + t("settings.github.keepToken"),
      });
      // The Pull requests tab and the fleet name the account they read with.
      await Promise.all([
        refresh(),
        queryClient.invalidateQueries({ queryKey: ["agents"] }),
        queryClient.invalidateQueries({ queryKey: ["pulls"] }),
        queryClient.invalidateQueries({ queryKey: ["fleet"] }),
      ]);
    },
  });
  const remove = useMutation({
    mutationFn: (name: string) => api.removeGitHubAccount(name),
    onSuccess: async (_, name) => {
      toast(t("settings.github.removed", { name }), {
        description: t("settings.accounts.removedNote"),
      });
      await refresh();
    },
  });
  const error = save.error ?? makeDefault.error ?? remove.error ?? rename.error;

  return (
    <div className="grid gap-3">
      {accounts.length > 0 && (
        <p className="text-[12.5px] text-subtle">
          {t("settings.accounts.defaultReaches")}
          {notDefault.length > 0 &&
            ` ${t("settings.accounts.theRest", { names: notDefault.map((p) => p.name).join(", ") })}`}
        </p>
      )}
      {accounts.length > 0 && (
        <ul className="grid gap-1.5" aria-label={t("settings.github.list")}>
          {accounts.map((acc) => (
            <li
              key={acc.name}
              data-github-account={acc.name}
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
                    aria-label={t("settings.accounts.newNameFor", { name: acc.name })}
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
                    {t("common.rename")}
                  </Button>
                  <Button
                    type="button"
                    size="sm"
                    variant="ghost"
                    disabled={rename.isPending}
                    onClick={() => setRenaming(null)}
                  >
                    {t("common.cancel")}
                  </Button>
                </form>
              ) : (
                <span className="truncate font-mono text-[12.5px] text-secondary">
                  {acc.name}
                </span>
              )}
              {acc.login && (
                <span className="truncate text-[12px] text-subtle">
                  {acc.login}
                </span>
              )}
              {acc.default && <Badge variant="brand">{t("settings.accounts.badgeDefault")}</Badge>}
              <div className="ml-auto flex items-center gap-1">
                {!acc.default && (
                  <Button
                    size="sm"
                    variant="ghost"
                    disabled={makeDefault.isPending}
                    onClick={() => makeDefault.mutate(acc.name)}
                  >
                    {t("settings.accounts.makeDefault")}
                  </Button>
                )}
                <Button
                  size="icon-sm"
                  variant="ghost"
                  aria-label={t("settings.accounts.renameAccount", { name: acc.name })}
                  disabled={rename.isPending}
                  onClick={() => startRename(acc.name)}
                >
                  <Pencil />
                </Button>
                <Button
                  size="icon-sm"
                  variant="ghost"
                  aria-label={t("settings.accounts.removeAccount", { name: acc.name })}
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
          {t.rich("settings.github.intro", { code: (c) => <Code>{c}</Code> })}
        </p>
        <div className="flex flex-wrap items-end gap-2">
          <div className="grid gap-1.5">
            <Label htmlFor="github-account-name">{t("settings.accounts.account")}</Label>
            <Input
              id="github-account-name"
              placeholder={accounts.length === 0 ? "default" : "work"}
              value={account}
              onChange={(event) => setAccount(event.target.value)}
              className="w-32 font-mono"
            />
          </div>
          <div className="grid min-w-48 flex-1 gap-1.5">
            <Label htmlFor="github-account-token">{t("settings.accounts.token")}</Label>
            <Input
              id="github-account-token"
              type="password"
              placeholder={t("settings.github.tokenPlaceholder")}
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
            {t("common.save")}
          </Button>
        </div>
        <p className="text-xs text-subtle">
          {t("settings.github.note")}
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
//
// On a Mac, and on Linux, where AgentBox runs in a VM of its own, this sets
// that VM up instead (`agentbox vm init`, no password). A Linux machine set
// up to run agents itself, before AgentBox ran in a VM on Linux, isn't
// offered host setup again: Setup's Move to a VM is its way on, and the
// command stays, for a terminal, only so nothing breaks before it moves.
function HostSetup({
  agentbox,
  onRun,
}: {
  agentbox: string;
  onRun: () => void;
}) {
  const t = useT();
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
        mac ? t("settings.hostSetup.doneVM") : t("settings.hostSetup.doneHost"),
        {
          description: mac
            ? t("settings.hostSetup.doneVMNote")
            : result.restarted
              ? t("settings.hostSetup.doneHostRestarted")
              : t("settings.hostSetup.doneHostLater"),
        },
      );
      await queryClient.invalidateQueries({ queryKey: ["setup"] });
    },
  });

  // On a Mac, AgentBox runs in a Linux VM, and this sets the VM up instead:
  // `agentbox vm init`, with no password to ask for. So on Linux.
  const linux = status.data?.linux ?? null;
  const mac = status.data?.vm != null || linux?.mode === "vm";
  const busy = run.isPending || status.data?.running === true;
  // On Windows, host setup is `agentbox wsl init`, in AgentBox's WSL distro,
  // which needs no password either (D94).
  const wsl = status.data?.wsl != null;
  if (linux?.mode === "host") {
    return (
      <div className="grid gap-2" data-host-setup="moving">
        <p className="text-[13px] text-muted">
          {t("settings.hostSetup.moving")}
        </p>
        <CommandBox command={`sudo ${agentbox} host setup`} />
      </div>
    );
  }
  const noDialog =
    status.data !== undefined && !mac && !wsl && status.data.pkexec === null;
  return (
    <div
      className="grid gap-2"
      data-host-setup={noDialog ? "terminal" : "button"}
    >
      <p className="text-[13px] text-muted">
        {mac
          ? t("settings.hostSetup.introVM")
          : wsl
            ? t("settings.hostSetup.introWSL")
            : t("settings.hostSetup.introHost")}
      </p>
      {!noDialog && (
        <div className="flex flex-wrap items-center gap-2">
          <Button
            variant="primary"
            disabled={busy}
            onClick={() => run.mutate()}
          >
            {busy ? <LoaderCircle className="animate-spin" /> : <ShieldCheck />}
            {mac ? t("settings.hostSetup.setUpVM") : t("settings.hostSetup.setUpHost")}
          </Button>
          <span className="text-xs text-subtle">
            {busy
              ? mac
                ? t("settings.hostSetup.busyVM")
                : t("settings.hostSetup.busyHost")
              : mac || wsl
                ? t("settings.hostSetup.noPassword")
                : t("settings.hostSetup.asksPassword")}
          </span>
        </div>
      )}
      {(lines.length > 0 || busy) && (
        <SetupLog lines={lines} label={t("settings.hostSetup.log")} />
      )}
      {run.error && <Notice>{errorMessage(run.error)}</Notice>}
      {(noDialog || run.error) && (
        <div className="grid gap-2">
          <p className="text-[13px] text-muted">
            {noDialog
              ? t("settings.hostSetup.noPkexec")
              : mac || wsl
                ? t("settings.hostSetup.terminal")
                : t("settings.hostSetup.terminalSudo")}
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
  const t = useT();
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
        aria-label={t("settings.commandBox.copy")}
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
  step,
  alwaysShow,
}: {
  step: Step;
  alwaysShow?: boolean;
}) {
  const t = useT();
  const ok = step.status === "ok";
  return (
    <div className="min-w-0" data-setup-step={step.id} data-status={step.status}>
      <Panel
        className={cn(
          "p-4 transition",
          !ok &&
            !step.optional &&
            step.status !== "checking" &&
            step.status !== "updating" &&
            "border-amber-400/15",
        )}
      >
        <div className="flex items-start gap-3.5">
          <span className="mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-full bg-surface ring-1 ring-inset ring-line">
            <StepIcon status={step.status} />
          </span>
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2">
              <h3 className="text-[14px] font-semibold text-primary">
                {step.title}
              </h3>
              {step.optional ? (
                step.status === "warn" ? (
                  <Badge variant="warning">{t("settings.setup.badgeCheck")}</Badge>
                ) : (
                  <Badge>{t("settings.setup.badgeOptional")}</Badge>
                )
              ) : step.status === "updating" ? (
                <Badge>{t("settings.setup.badgeUpdating")}</Badge>
              ) : (
                !ok &&
                step.status !== "checking" && (
                  <Badge variant="warning">
                    {step.status === "outdated" ? t("settings.setup.badgeOutdated") : t("settings.setup.badgeNeeded")}
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
    </div>
  );
}
