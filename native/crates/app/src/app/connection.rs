//! The daemon's connection, and what the window shows while there is none
//! (App.tsx's connection notices, StartingNotice.tsx, VMSetup.tsx,
//! RunInVM.tsx's LinuxVMSetup and WSLSetup.tsx as far as a thin client can).
//!
//! It asks GET /v1/version every few seconds. When nothing answers on the
//! default socket and the command-line tool is on PATH, it starts AgentBox
//! the way Electron's main process does (desktop/src/main/daemon.ts,
//! vmpower.ts), once, showing the last line it says on stderr and how long
//! it has taken, then waits for the socket. On Linux and Windows it first
//! asks `agentbox vm power --json`: a VM (or WSL distro) that's off is
//! started with `agentbox vm start` and a paused one resumed with `agentbox
//! vm resume`, each returning once the daemon in it answers; host mode (a
//! tool that says {"mode":"host"}, or one from before `vm power`), a VM
//! that's already up and a Mac run `agentbox daemon start`, which on a Mac
//! boots Lima's VM itself. Once the app has reached a daemon, a VM that's
//! off or paused stays so (the top bar's Free resources, `agentbox vm
//! stop`): the app shows the command that brings it back rather than boot
//! it behind the user's back. On a Mac, or a
//! Linux machine in VM mode, `agentbox vm status --json` says whether the VM
//! is there: one that isn't is set up with the command shown, which the
//! user runs, since making the VM, the WSL distro or running host setup
//! with sudo is the main process's work in Electron and has no place in a
//! client of the socket.
use std::path::PathBuf;
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::{h_flex, v_flex};

use super::AppView;
use crate::theme;

/// What a start runs: `agentbox daemon start`, or `agentbox vm start` or
/// `vm resume` for a VM (wsl: AgentBox's WSL distro) whose daemon answers
/// once it's up.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub(super) enum Starting {
    #[default]
    Daemon,
    Vm {
        resume: bool,
        wsl: bool,
    },
}

/// How a start ended short of a daemon: why, and the command that brings
/// the VM back, when it's the VM that's in the way.
#[derive(Clone, Debug, PartialEq, Eq)]
struct StartFailure {
    error: String,
    fix: Option<&'static str>,
}

/// What the start has said so far, what it runs, and how it ended.
#[derive(Default)]
struct StartRun {
    step: String,
    what: Starting,
    done: Option<Result<(), StartFailure>>,
}

#[derive(Default)]
pub(super) struct Conn {
    /// answered is whether the daemon has answered since the app started.
    pub answered: bool,
    pub error: Option<String>,
    /// version is the daemon's, from GET /v1/version.
    pub version: Option<String>,
    /// starting is a start under way: since when, and what it said last.
    pub starting: Option<(Instant, String)>,
    run: Option<Arc<Mutex<StartRun>>>,
    /// what is what the start under way runs.
    pub what: Starting,
    /// start_error is how the app's own start failed, and start_fix the
    /// command to run about it.
    pub start_error: Option<String>,
    pub start_fix: Option<&'static str>,
    tried: bool,
    /// vm is what `agentbox vm status --json` said, while there's no daemon.
    pub vm: Option<serde_json::Value>,
    vm_at: Option<Instant>,
}

/// which finds a command on PATH, as a shell would (hostsetup.ts's which).
pub(super) fn which(name: &str) -> Option<PathBuf> {
    let path = std::env::var_os("PATH")?;
    std::env::split_paths(&path).find_map(|dir| {
        let p = dir.join(name);
        let exe = p.with_extension(std::env::consts::EXE_EXTENSION);
        [p, exe].into_iter().find(|p| p.is_file())
    })
}

/// agentbox_cli is the command-line tool to start the daemon with: on
/// Windows the one the relay runs from (agentbox_api::relay::agentbox_bin,
/// the Electron app's managed copy first), else the one on PATH or where the
/// Electron app and the installers put it (find_cli).
fn agentbox_cli() -> Option<PathBuf> {
    #[cfg(windows)]
    {
        let exe = std::env::current_exe().ok();
        let bin = agentbox_api::relay::agentbox_bin(
            |k| std::env::var(k).ok(),
            exe.as_deref().and_then(std::path::Path::parent),
            |p| p.is_file(),
        );
        if bin.is_file() {
            return Some(bin);
        }
    }
    let home = std::env::var_os("HOME").map(PathBuf::from);
    find_cli(
        |k| std::env::var(k).ok(),
        home.as_deref(),
        |p| p.is_file(),
        which("agentbox"),
        login_shell_path,
    )
}

/// find_cli looks for the CLI the way the Electron app does (desktop/src/main/
/// cli.ts, internal/homehost/tools.go): AGENTBOX_BIN, its managed copy, the
/// link in ~/.local/bin, Homebrew's and /usr/local's, then PATH, then the
/// login shell's PATH. A Mac opens an app from Finder or the Dock with
/// launchd's PATH (/usr/bin:/bin:...), which has none of them.
fn find_cli(
    env: impl Fn(&str) -> Option<String>,
    home: Option<&std::path::Path>,
    exists: impl Fn(&std::path::Path) -> bool,
    on_path: Option<PathBuf>,
    login_path: impl FnOnce() -> Option<String>,
) -> Option<PathBuf> {
    if let Some(b) = env("AGENTBOX_BIN").filter(|b| !b.is_empty()) {
        return Some(b.into());
    }
    let mut known: Vec<PathBuf> = Vec::new();
    if let Some(h) = home {
        known.push(h.join(".local/share/agentbox/bin/agentbox"));
        known.push(h.join(".local/bin/agentbox"));
    }
    known.push("/opt/homebrew/bin/agentbox".into());
    known.push("/usr/local/bin/agentbox".into());
    if let Some(p) = known.into_iter().find(|p| exists(p)) {
        return Some(p);
    }
    on_path.or_else(|| {
        let path = login_path()?;
        std::env::split_paths(&path)
            .map(|d| d.join("agentbox"))
            .find(|p| exists(p))
    })
}

/// login_shell_path is the PATH the user's login shell sets
/// (loginShellPath in internal/homehost/tools.go): `$SHELL -ilc 'printf %s
/// "$PATH"'`, given 5 seconds. Only unix has a login shell to ask.
#[cfg(unix)]
fn login_shell_path() -> Option<String> {
    use std::io::Read as _;
    use std::process::{Command, Stdio};
    let shell = std::env::var("SHELL").ok().filter(|s| !s.is_empty())?;
    let mut child = Command::new(shell)
        .args(["-ilc", "printf %s \"$PATH\""])
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::null())
        .spawn()
        .ok()?;
    let mut out = child.stdout.take()?;
    let reader = std::thread::spawn(move || {
        let mut s = String::new();
        out.read_to_string(&mut s).ok().map(|_| s)
    });
    let deadline = Instant::now() + Duration::from_secs(5);
    loop {
        match child.try_wait() {
            Ok(Some(_)) => break,
            Ok(None) if Instant::now() < deadline => std::thread::sleep(Duration::from_millis(25)),
            _ => {
                let _ = child.kill();
                let _ = child.wait();
                return None;
            }
        }
    }
    reader.join().ok().flatten().filter(|p| !p.is_empty())
}

#[cfg(not(unix))]
fn login_shell_path() -> Option<String> {
    None
}

/// last_line is progressStep: the last line said that isn't an error.
fn last_line(said: &str) -> String {
    said.lines()
        .map(str::trim)
        .rfind(|l| !l.is_empty() && !l.starts_with("error:"))
        .unwrap_or_default()
        .to_string()
}

/// elapsed is a start's time as a clock: 0:42, 2:05.
fn elapsed(d: Duration) -> String {
    let s = d.as_secs();
    format!("{}:{:02}", s / 60, s % 60)
}

/// Power is what `agentbox vm power --json` says (internal/hostvm's Power,
/// internal/hostwsl's): host mode, or a VM's state (off, starting, running,
/// pausing, paused, resuming, stopping) and why it can't be used.
#[derive(Debug, PartialEq, Eq)]
enum Power {
    Host,
    Vm {
        state: String,
        error: String,
        wsl: bool,
        /// for_disk is a VM the supervisor paused because the host's disk
        /// is nearly full, which it resumes by itself once there's room.
        for_disk: bool,
    },
}

/// parse_power reads `agentbox vm power --json`'s output; None when it isn't
/// one, which a tool from before VM mode is (host mode, as vmpower.ts has it).
fn parse_power(stdout: &[u8]) -> Option<Power> {
    let v: serde_json::Value = serde_json::from_slice(stdout).ok()?;
    let str_of = |k: &str| v.get(k).and_then(|s| s.as_str()).unwrap_or_default();
    if str_of("mode") == "host" {
        return Some(Power::Host);
    }
    let state = str_of("state");
    if state.is_empty() {
        return None;
    }
    Some(Power::Vm {
        state: state.to_string(),
        error: str_of("error").to_string(),
        wsl: str_of("driver") == "wsl",
        for_disk: v.get("pausedForDisk").and_then(|b| b.as_bool()) == Some(true),
    })
}

/// Plan is what a start does about the power it found.
#[derive(Debug, PartialEq, Eq)]
enum Plan {
    /// Daemon is `agentbox daemon start`.
    Daemon,
    /// Vm is `agentbox vm start`, or `vm resume`.
    Vm { resume: bool, wsl: bool },
    /// Leave starts nothing, and says why.
    Leave(StartFailure),
}

/// plan decides how to start AgentBox (daemon.ts's start): host mode, or a
/// VM that's up with no daemon answering, is a daemon start; a VM that's off
/// is started and a paused one resumed, unless the app has already reached
/// its daemon (answered), when only the user brings it back. A VM that can't
/// be used (one vm init hasn't made) says why, and isn't started.
fn plan(power: Option<&Power>, answered: bool) -> Plan {
    let Some(Power::Vm {
        state,
        error,
        wsl,
        for_disk,
    }) = power
    else {
        return Plan::Daemon;
    };
    let wsl = *wsl;
    let resume = state == "paused";
    match state.as_str() {
        "running" | "starting" | "resuming" => Plan::Daemon,
        "paused" if *for_disk => Plan::Leave(StartFailure {
            error: t("shell.diskGuard.vmPausedTitle"),
            fix: None,
        }),
        _ if !error.is_empty() => Plan::Leave(StartFailure {
            error: error.clone(),
            fix: None,
        }),
        _ if answered => Plan::Leave(StartFailure {
            error: if resume {
                t("web.main.vmPaused")
            } else if wsl {
                t("native.vm.wslOff")
            } else {
                tv("web.main.vmNotRunning", &[("state", state.as_str().into())])
            },
            fix: Some(fix_for(resume)),
        }),
        _ => Plan::Vm { resume, wsl },
    }
}

/// fix_for is the command that starts the VM, or resumes it.
fn fix_for(resume: bool) -> &'static str {
    if resume {
        "agentbox vm resume"
    } else {
        "agentbox vm start"
    }
}

/// start runs the start with the tool at bin (vm_power: on Linux and
/// Windows, where `vm power` decides it), saying how it goes in run. It
/// returns once the command has.
fn start(bin: &std::path::Path, vm_power: bool, answered: bool, run: &Mutex<StartRun>) {
    let power = if vm_power { read_power(bin) } else { None };
    let (args, what) = match plan(power.as_ref(), answered) {
        Plan::Leave(f) => {
            run.lock().unwrap().done = Some(Err(f));
            return;
        }
        Plan::Daemon => (["daemon", "start"], Starting::Daemon),
        Plan::Vm { resume, wsl } => (
            ["vm", if resume { "resume" } else { "start" }],
            Starting::Vm { resume, wsl },
        ),
    };
    run.lock().unwrap().what = what;
    let done = run_said(bin, &args, |step| run.lock().unwrap().step = step).map_err(|error| {
        StartFailure {
            error,
            fix: match what {
                Starting::Vm { resume, .. } => Some(fix_for(resume)),
                Starting::Daemon => None,
            },
        }
    });
    run.lock().unwrap().done = Some(done);
}

/// read_power is `agentbox vm power --json`, None when the tool fails or
/// says something else.
fn read_power(bin: &std::path::Path) -> Option<Power> {
    let out = std::process::Command::new(bin)
        .args(["vm", "power", "--json"])
        .stdin(std::process::Stdio::null())
        .stderr(std::process::Stdio::null())
        .output()
        .ok()?;
    if !out.status.success() {
        return None;
    }
    parse_power(&out.stdout)
}

/// run_said runs bin with args, telling step the last line it says on
/// stderr as it goes; an error is its last line, or how it exited.
fn run_said(
    bin: &std::path::Path,
    args: &[&str],
    mut step: impl FnMut(String),
) -> Result<(), String> {
    use std::io::Read as _;
    let mut child = std::process::Command::new(bin)
        .args(args)
        .stdin(std::process::Stdio::null())
        .stdout(std::process::Stdio::null())
        .stderr(std::process::Stdio::piped())
        .spawn()
        .map_err(|e| format!("{}: {e}", bin.display()))?;
    let mut said = String::new();
    if let Some(mut err) = child.stderr.take() {
        let mut buf = [0u8; 512];
        while let Ok(n) = err.read(&mut buf) {
            if n == 0 {
                break;
            }
            said.push_str(&String::from_utf8_lossy(&buf[..n]));
            if said.len() > 4000 {
                let mut cut = said.len() - 4000;
                while !said.is_char_boundary(cut) {
                    cut += 1;
                }
                said.drain(..cut);
            }
            step(last_line(&said));
        }
    }
    match child.wait() {
        Ok(s) if s.success() => Ok(()),
        Ok(s) => Err(said
            .lines()
            .rev()
            .find(|l| !l.trim().is_empty())
            .map(|l| l.trim().trim_start_matches("error: ").to_string())
            .unwrap_or_else(|| format!("agentbox {}: {s}", args.join(" ")))),
        Err(e) => Err(e.to_string()),
    }
}

/// starting_text says what a start under way does, and for how long:
/// "Starting AgentBox… 0:42", or "VM starting… 0:42" while it's the VM.
fn starting_text(what: Starting, took: Duration) -> String {
    let Starting::Vm { resume, wsl } = what else {
        return tv(
            "shell.app.daemonStarting",
            &[("elapsed", elapsed(took).into())],
        );
    };
    let busy = tv(
        "shell.machine.busy",
        &[
            ("state", if resume { "resuming" } else { "starting" }.into()),
            (
                "what",
                t(if wsl {
                    "shell.machine.wsl"
                } else {
                    "shell.machine.vm"
                })
                .into(),
            ),
        ],
    );
    format!("{busy} {}", elapsed(took))
}

/// vm_missing is a VM `agentbox vm status --json` says isn't made yet: the
/// Mac's (exists: false) or a Linux VM mode's (state "missing").
fn vm_missing(v: &serde_json::Value) -> bool {
    let str_of = |k: &str| v.get(k).and_then(|s| s.as_str());
    v.get("exists").and_then(|e| e.as_bool()) == Some(false)
        || (str_of("state") == Some("missing") && str_of("mode") != Some("host"))
}

impl AppView {
    /// watch_connection asks the daemon whether it's there, every two
    /// seconds while it isn't and every five while it is.
    pub(super) fn watch_connection(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let client = self.client.clone();
        cx.spawn_in(window, async move |this, cx| {
            loop {
                let res = client.get::<serde_json::Value>("/v1/version").await;
                let up = res.is_ok();
                let alive = this.update_in(cx, |this, window, cx| {
                    this.on_version(res.map_err(|e| format!("{e:#}")), window, cx)
                });
                if alive.is_err() {
                    return;
                }
                let wait = if up { 5000 } else { 1000 };
                cx.background_executor()
                    .timer(Duration::from_millis(wait))
                    .await;
            }
        })
        .detach();
    }

    fn on_version(
        &mut self,
        res: Result<serde_json::Value, String>,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let was = self.connected;
        match res {
            Ok(v) => {
                let c = &mut self.conn;
                c.version = v
                    .get("version")
                    .and_then(|v| v.as_str())
                    .map(str::to_string);
                c.error = None;
                c.starting = None;
                c.start_error = None;
                c.start_fix = None;
                c.vm = None;
                if !c.answered {
                    c.answered = true;
                    self.on_first_answer(window, cx);
                }
                self.connected = true;
            }
            Err(e) => {
                self.connected = false;
                self.conn.error = Some(e);
                self.maybe_start_daemon(cx);
                self.poll_start();
                self.read_vm_status(cx);
            }
        }
        if was != self.connected {
            cx.notify();
        } else if self.conn.starting.is_some() {
            // The start's clock and its last line.
            cx.notify();
        }
    }

    /// maybe_start_daemon starts the daemon once, as Electron's ensureDaemon:
    /// only on the default socket, with the tool on PATH, and unless
    /// AGENTBOX_NO_AUTOSTART says not to. On Windows it's agentbox.exe, which
    /// starts the daemon in its WSL distro (the relay reaches it there).
    fn maybe_start_daemon(&mut self, cx: &mut Context<Self>) {
        let c = &mut self.conn;
        if c.tried || std::env::var_os("AGENTBOX_NO_AUTOSTART").is_some() {
            return;
        }
        if self.client.socket() != agentbox_api::client::default_socket() {
            return;
        }
        let Some(bin) = agentbox_cli() else {
            return;
        };
        c.tried = true;
        let run = Arc::new(Mutex::new(StartRun::default()));
        c.run = Some(run.clone());
        c.starting = Some((Instant::now(), String::new()));
        c.what = Starting::Daemon;
        c.start_error = None;
        c.start_fix = None;
        let answered = c.answered;
        // A Mac's VM is Lima's, which `daemon start` boots (vmpower.ts asks
        // `vm power` on Linux and Windows only).
        let vm_power = !cfg!(target_os = "macos");
        std::thread::spawn(move || start(&bin, vm_power, answered, &run));
        // What it says, its clock and how it ends, as they happen: the
        // version checks come only every few seconds while nothing answers
        // (the client tries a socket nobody listens on for a while).
        cx.spawn(async move |this, cx| {
            loop {
                cx.background_executor()
                    .timer(Duration::from_millis(500))
                    .await;
                let going = this.update(cx, |this, cx| {
                    this.poll_start();
                    cx.notify();
                    this.conn.run.is_some()
                });
                if !matches!(going, Ok(true)) {
                    return;
                }
            }
        })
        .detach();
    }

    /// poll_start takes what the start has said since the last look.
    fn poll_start(&mut self) {
        let c = &mut self.conn;
        let Some(run) = c.run.clone() else {
            return;
        };
        let r = run.lock().unwrap();
        if let Some((_, step)) = c.starting.as_mut() {
            step.clone_from(&r.step);
        }
        c.what = r.what;
        if let Some(done) = &r.done {
            c.starting = None;
            if let Err(e) = done {
                c.start_error = Some(e.error.clone());
                c.start_fix = e.fix;
            }
            drop(r);
            c.run = None;
        }
    }

    /// read_vm_status asks the tool about AgentBox's VM, at most every five
    /// seconds, on a Mac and on Linux; Windows' distro is the relay's.
    fn read_vm_status(&mut self, cx: &mut Context<Self>) {
        if cfg!(windows)
            || self
                .conn
                .vm_at
                .is_some_and(|t| t.elapsed() < Duration::from_secs(5))
        {
            return;
        }
        let Some(bin) = agentbox_cli() else {
            return;
        };
        self.conn.vm_at = Some(Instant::now());
        let task = cx.background_executor().spawn(async move {
            std::process::Command::new(bin)
                .args(["vm", "status", "--json"])
                .stdin(std::process::Stdio::null())
                .stderr(std::process::Stdio::null())
                .output()
                .ok()
                .and_then(|o| serde_json::from_slice::<serde_json::Value>(&o.stdout).ok())
        });
        cx.spawn(async move |this, cx| {
            let v = task.await;
            this.update(cx, |this, cx| {
                if !this.connected {
                    this.conn.vm = v;
                    cx.notify();
                }
            })
            .ok();
        })
        .detach();
    }

    /// render_offline_notice is the line over the page while a daemon that
    /// answered before doesn't: its start, or its error and a retry.
    pub(super) fn render_offline_notice(&self) -> AnyElement {
        let c = &self.conn;
        let (text, color) = match &c.starting {
            Some((since, step)) => {
                let mut s = starting_text(c.what, since.elapsed());
                if !step.is_empty() {
                    s = format!("{s} {step}");
                }
                (s, theme::accent())
            }
            None => (
                tv(
                    "shell.app.daemonUnreachable",
                    &[(
                        "error",
                        c.start_error
                            .clone()
                            .or_else(|| c.error.clone())
                            .unwrap_or_default()
                            .into(),
                    )],
                ),
                theme::error(),
            ),
        };
        // A VM left off or paused: the command that brings it back.
        let fix = c.start_fix.filter(|_| c.starting.is_none());
        div()
            .flex_none()
            .px_4()
            .py_2()
            .border_b_1()
            .border_color(color.opacity(0.35))
            .bg(color.opacity(0.08))
            .text_sm()
            .text_color(theme::primary())
            .child(text)
            .when_some(fix, |el, fix| {
                el.child(
                    h_flex()
                        .gap_2()
                        .text_xs()
                        .text_color(theme::muted())
                        .child(t("native.vm.runIt"))
                        .child(
                            div()
                                .font_family(theme::mono())
                                .text_color(theme::secondary())
                                .child(format!("$ {fix}")),
                        ),
                )
            })
            .into_any_element()
    }

    /// render_unreachable is what the main area shows while the daemon
    /// doesn't answer: the start under way, the VM to make, or the error.
    pub(super) fn render_unreachable(&self, cx: &mut Context<Self>) -> AnyElement {
        let c = &self.conn;
        let vm = c.vm.as_ref().filter(|v| vm_missing(v));
        let notice = |text: String, color: Hsla| {
            div()
                .px_3()
                .py_2()
                .rounded_md()
                .border_1()
                .border_color(color.opacity(0.35))
                .bg(color.opacity(0.08))
                .text_sm()
                .text_color(theme::primary())
                .child(text)
                .into_any_element()
        };
        let body: AnyElement = if let Some(v) = vm {
            let mac = cfg!(target_os = "macos");
            let problem = v
                .get("problem")
                .and_then(|p| p.as_str())
                .filter(|p| !p.is_empty())
                .map(str::to_string);
            v_flex()
                .gap_3()
                .child(
                    div()
                        .text_lg()
                        .font_weight(FontWeight::SEMIBOLD)
                        .child(t(if mac {
                            "vm.setup.title"
                        } else {
                            "vm.run.setupTitle"
                        })),
                )
                .when(mac, |el| {
                    el.child(
                        div()
                            .text_sm()
                            .text_color(theme::muted())
                            .child(t("vm.setup.description")),
                    )
                })
                .when_some(problem, |el, p| el.child(notice(p, theme::starting())))
                .child(
                    div()
                        .text_sm()
                        .text_color(theme::muted())
                        .child(t("native.setup.runIt")),
                )
                .child(
                    div()
                        .text_xs()
                        .text_color(theme::faint())
                        .child(t("vm.setup.orTerminal")),
                )
                .child(self.command_box("vm-init", "agentbox vm init", cx))
                .into_any_element()
        } else if let Some((since, step)) = &c.starting {
            v_flex()
                .gap_1()
                .px_3()
                .py_2()
                .rounded_md()
                .border_1()
                .border_color(theme::accent().opacity(0.35))
                .bg(theme::accent().opacity(0.08))
                .child(
                    div()
                        .text_sm()
                        .child(starting_text(c.what, since.elapsed())),
                )
                .when(!step.is_empty(), |el| {
                    el.child(
                        div()
                            .truncate()
                            .font_family(theme::mono())
                            .text_xs()
                            .text_color(theme::muted())
                            .child(step.clone()),
                    )
                })
                .into_any_element()
        } else if c.error.is_none() && c.start_error.is_none() {
            // The first look hasn't failed yet: the client tries a socket
            // nobody listens on for a few seconds before giving up.
            notice(t("shell.machine.connecting"), theme::accent())
        } else {
            let error = c
                .start_error
                .clone()
                .or_else(|| c.error.clone())
                .unwrap_or_default();
            v_flex()
                .gap_3()
                .child(notice(
                    tv("shell.app.daemonUnreachable", &[("error", error.into())]),
                    theme::error(),
                ))
                .when_some(c.start_fix, |el, fix| {
                    el.child(
                        div()
                            .text_sm()
                            .text_color(theme::muted())
                            .child(t("native.vm.runIt")),
                    )
                    .child(self.command_box("vm-fix", fix, cx))
                })
                .when(
                    !c.tried && self.client.socket() == agentbox_api::client::default_socket(),
                    |el| {
                        el.child(
                            div()
                                .text_sm()
                                .text_color(theme::muted())
                                .child(t("native.setup.startIt")),
                        )
                        .child(self.command_box(
                            "daemon-start",
                            "agentbox daemon start",
                            cx,
                        ))
                    },
                )
                .into_any_element()
        };
        div()
            .id("unreachable")
            .size_full()
            .overflow_y_scroll()
            .flex()
            .justify_center()
            .child(
                v_flex()
                    .w_full()
                    .max_w(px(640.))
                    .px_6()
                    .py_10()
                    .gap_4()
                    .child(
                        h_flex()
                            .gap_2()
                            .text_sm()
                            .text_color(theme::muted())
                            .child(div().size_2().rounded_full().bg(
                                if c.starting.is_some() || c.error.is_none() {
                                    theme::starting()
                                } else {
                                    theme::error()
                                },
                            ))
                            .child(if c.answered {
                                t("shell.machine.offline")
                            } else {
                                t("firstRun.machine.waiting")
                            }),
                    )
                    .child(body),
            )
            .into_any_element()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[::core::prelude::v1::test]
    fn the_last_line_said_is_the_step() {
        assert_eq!(last_line("one\n two \n\n"), "two");
        assert_eq!(last_line("starting\nerror: no\n"), "starting");
        assert_eq!(elapsed(Duration::from_secs(125)), "2:05");
        assert!(vm_missing(&serde_json::json!({"exists": false})));
        assert!(vm_missing(&serde_json::json!({"state": "missing"})));
        assert!(!vm_missing(&serde_json::json!({"state": "running"})));
    }
}

#[cfg(test)]
mod start_tests {
    use super::*;

    fn vm(state: &str) -> Power {
        Power::Vm {
            state: state.into(),
            error: String::new(),
            wsl: false,
            for_disk: false,
        }
    }

    #[::core::prelude::v1::test]
    fn vm_power_is_read_as_host_mode_or_a_vm() {
        assert_eq!(parse_power(br#"{"mode":"host"}"#), Some(Power::Host));
        assert_eq!(
            parse_power(br#"{"state":"paused","memoryUsed":0,"cpus":4}"#),
            Some(vm("paused"))
        );
        assert_eq!(
            parse_power(br#"{"state":"off","driver":"wsl","error":"why"}"#),
            Some(Power::Vm {
                state: "off".into(),
                error: "why".into(),
                wsl: true,
                for_disk: false
            })
        );
        assert_eq!(parse_power(b"unknown command \"power\""), None);
        assert_eq!(parse_power(b"{}"), None);
    }

    #[::core::prelude::v1::test]
    fn host_mode_and_a_vm_that_runs_start_the_daemon() {
        assert_eq!(plan(None, false), Plan::Daemon);
        assert_eq!(plan(Some(&Power::Host), false), Plan::Daemon);
        assert_eq!(plan(Some(&Power::Host), true), Plan::Daemon);
        for state in ["running", "starting", "resuming"] {
            assert_eq!(plan(Some(&vm(state)), false), Plan::Daemon, "{state}");
            assert_eq!(plan(Some(&vm(state)), true), Plan::Daemon, "{state}");
        }
    }

    #[::core::prelude::v1::test]
    fn at_launch_a_vm_off_is_started_and_one_paused_resumed() {
        let off_wsl = Power::Vm {
            state: "off".into(),
            error: String::new(),
            wsl: true,
            for_disk: false,
        };
        assert_eq!(
            plan(Some(&vm("off")), false),
            Plan::Vm {
                resume: false,
                wsl: false
            }
        );
        assert_eq!(
            plan(Some(&off_wsl), false),
            Plan::Vm {
                resume: false,
                wsl: true
            }
        );
        assert_eq!(
            plan(Some(&vm("paused")), false),
            Plan::Vm {
                resume: true,
                wsl: false
            }
        );
    }

    #[::core::prelude::v1::test]
    fn once_reached_a_vm_off_or_paused_is_left_to_the_user() {
        let Plan::Leave(f) = plan(Some(&vm("paused")), true) else {
            panic!("paused VM started again");
        };
        assert_eq!(f.fix, Some("agentbox vm resume"));
        let Plan::Leave(f) = plan(Some(&vm("stopping")), true) else {
            panic!("stopping VM started again");
        };
        assert_eq!(f.fix, Some("agentbox vm start"));
        assert!(f.error.contains("turning off"), "{}", f.error);
    }

    #[::core::prelude::v1::test]
    fn a_vm_that_cant_be_used_says_why_and_isnt_started() {
        let broken = Power::Vm {
            state: "off".into(),
            error: "AgentBox's VM isn't made yet: run agentbox vm init".into(),
            wsl: false,
            for_disk: false,
        };
        assert_eq!(
            plan(Some(&broken), false),
            Plan::Leave(StartFailure {
                error: "AgentBox's VM isn't made yet: run agentbox vm init".into(),
                fix: None
            })
        );
    }

    #[::core::prelude::v1::test]
    fn a_vm_paused_for_a_full_disk_isnt_resumed() {
        let full = parse_power(br#"{"state":"paused","pausedForDisk":true}"#).unwrap();
        let Plan::Leave(f) = plan(Some(&full), false) else {
            panic!("a VM paused for a full disk was resumed");
        };
        assert_eq!(f.fix, None);
        assert!(f.error.contains("disk"), "{}", f.error);
    }

    #[::core::prelude::v1::test]
    fn the_vm_starting_says_so() {
        let took = Duration::from_secs(42);
        assert_eq!(
            starting_text(
                Starting::Vm {
                    resume: false,
                    wsl: false
                },
                took
            ),
            "VM starting… 0:42"
        );
        assert_eq!(
            starting_text(
                Starting::Vm {
                    resume: true,
                    wsl: false
                },
                took
            ),
            "VM resuming… 0:42"
        );
        assert_eq!(
            starting_text(
                Starting::Vm {
                    resume: false,
                    wsl: true
                },
                took
            ),
            "WSL starting… 0:42"
        );
        assert_eq!(
            starting_text(Starting::Daemon, took),
            "Starting AgentBox… 0:42"
        );
    }
}

/// The start against a fake command-line tool: a shell script that answers
/// `vm power --json` with $POWER, logs every command it's given and fails
/// `vm start` when $FAIL says so.
#[cfg(all(test, unix))]
mod start_run_tests {
    use super::*;
    use std::os::unix::fs::PermissionsExt as _;

    fn fake(dir: &std::path::Path, power: &str, fail: bool) -> PathBuf {
        let bin = dir.join("agentbox");
        let log = dir.join("log");
        let script = format!(
            r#"#!/bin/sh
echo "$*" >> '{log}'
case "$*" in
  "vm power --json") echo '{power}' ;;
  "vm start"|"vm resume")
    echo "==> Starting AgentBox's VM (agentbox)" >&2
    {fail}
    ;;
esac
"#,
            log = log.display(),
            fail = if fail {
                "echo 'error: cloud-hypervisor: /dev/kvm: permission denied' >&2; exit 1"
            } else {
                ":"
            },
        );
        std::fs::write(&bin, script).unwrap();
        std::fs::set_permissions(&bin, std::fs::Permissions::from_mode(0o755)).unwrap();
        bin
    }

    /// One at a time: a test that forks while another has its script open
    /// for writing hands that descriptor to its child, and the script's own
    /// exec then fails with "Text file busy", leaving its log empty.
    static ONE: Mutex<()> = Mutex::new(());

    fn run(power: &str, fail: bool, vm_power: bool, answered: bool) -> (StartRun, String) {
        let _one = ONE.lock().unwrap_or_else(|e| e.into_inner());
        let dir = std::env::temp_dir().join(format!(
            "agentbox-start-{}-{}",
            std::process::id(),
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap()
                .as_nanos()
        ));
        std::fs::create_dir_all(&dir).unwrap();
        let bin = fake(&dir, power, fail);
        let r = Mutex::new(StartRun::default());
        start(&bin, vm_power, answered, &r);
        let log = std::fs::read_to_string(dir.join("log")).unwrap_or_default();
        std::fs::remove_dir_all(&dir).ok();
        (r.into_inner().unwrap(), log)
    }

    #[::core::prelude::v1::test]
    fn a_vm_off_is_started() {
        let (r, log) = run(r#"{"state":"off"}"#, false, true, false);
        assert_eq!(log, "vm power --json\nvm start\n");
        assert_eq!(r.done, Some(Ok(())));
        assert_eq!(
            r.what,
            Starting::Vm {
                resume: false,
                wsl: false
            }
        );
        assert_eq!(r.step, "==> Starting AgentBox's VM (agentbox)");
    }

    #[::core::prelude::v1::test]
    fn a_paused_vm_is_resumed() {
        let (r, log) = run(r#"{"state":"paused"}"#, false, true, false);
        assert_eq!(log, "vm power --json\nvm resume\n");
        assert_eq!(r.done, Some(Ok(())));
    }

    #[::core::prelude::v1::test]
    fn a_failed_start_shows_stderr_and_the_command() {
        let (r, _) = run(r#"{"state":"off"}"#, true, true, false);
        assert_eq!(
            r.done,
            Some(Err(StartFailure {
                error: "cloud-hypervisor: /dev/kvm: permission denied".into(),
                fix: Some("agentbox vm start"),
            }))
        );
    }

    #[::core::prelude::v1::test]
    fn host_mode_starts_the_daemon() {
        let (r, log) = run(r#"{"mode":"host"}"#, false, true, false);
        assert_eq!(log, "vm power --json\ndaemon start\n");
        assert_eq!(r.done, Some(Ok(())));
        assert_eq!(r.what, Starting::Daemon);
        // A Mac doesn't ask.
        let (_, log) = run(r#"{"state":"off"}"#, false, false, false);
        assert_eq!(log, "daemon start\n");
    }

    #[::core::prelude::v1::test]
    fn a_vm_the_user_paused_after_launch_is_left_paused() {
        let (r, log) = run(r#"{"state":"paused"}"#, false, true, true);
        assert_eq!(log, "vm power --json\n");
        assert_eq!(
            r.done.and_then(Result::err).and_then(|f| f.fix),
            Some("agentbox vm resume")
        );
    }
}

#[cfg(test)]
mod cli_tests {
    use super::find_cli;
    use std::path::{Path, PathBuf};

    fn lookup(
        env: &[(&str, &str)],
        have: &[&str],
        on_path: Option<&str>,
        login: Option<&str>,
    ) -> Option<PathBuf> {
        find_cli(
            |k| {
                env.iter()
                    .find(|(n, _)| *n == k)
                    .map(|(_, v)| v.to_string())
            },
            Some(Path::new("/Users/me")),
            |p| have.iter().any(|h| Path::new(h) == p),
            on_path.map(PathBuf::from),
            || login.map(String::from),
        )
    }

    #[test]
    fn cli_lookup_order() {
        let all = [
            "/usr/local/bin/agentbox",
            "/opt/homebrew/bin/agentbox",
            "/Users/me/.local/bin/agentbox",
            "/Users/me/.local/share/agentbox/bin/agentbox",
        ];
        assert_eq!(
            lookup(&[("AGENTBOX_BIN", "/x/ab")], &all, None, None),
            Some("/x/ab".into())
        );
        assert_eq!(
            lookup(&[], &all, Some("/p/agentbox"), None),
            Some("/Users/me/.local/share/agentbox/bin/agentbox".into())
        );
        assert_eq!(
            lookup(&[], &all[..3], None, None),
            Some("/Users/me/.local/bin/agentbox".into())
        );
        assert_eq!(
            lookup(&[], &all[..2], None, None),
            Some("/opt/homebrew/bin/agentbox".into())
        );
        assert_eq!(
            lookup(&[], &all[..1], None, None),
            Some("/usr/local/bin/agentbox".into())
        );
    }

    #[test]
    fn cli_lookup_falls_back_to_path_then_login_shell() {
        assert_eq!(
            lookup(
                &[("AGENTBOX_BIN", "")],
                &[],
                Some("/p/agentbox"),
                Some("/l")
            ),
            Some("/p/agentbox".into())
        );
        assert_eq!(
            lookup(&[], &["/opt/x/agentbox"], None, Some("/a:/opt/x")),
            Some("/opt/x/agentbox".into())
        );
        assert_eq!(lookup(&[], &[], None, Some("/a:/b")), None);
        assert_eq!(lookup(&[], &[], None, None), None);
    }
}
