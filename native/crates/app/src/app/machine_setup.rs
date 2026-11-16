//! Setting AgentBox's machine up from the app, while there is no daemon to
//! reach (Electron's WSLSetup.tsx, VMSetup.tsx and RunInVM.tsx's
//! LinuxVMSetup, main/hostsetup.ts's initWSL, installWSL and initVM).
//!
//! The app has the agentbox tool built in (cli.rs), so what Electron's main
//! process runs for its Setup the app runs too, with the same tool: on
//! Windows `agentbox wsl init`, which makes AgentBox's WSL distro, and before
//! it `agentbox wsl install` when WSL itself is missing, behind Windows' own
//! administrator prompt; on Linux and a Mac `agentbox vm init`, at the size
//! the tool picks. None of them asks this app for a password. The card says
//! which one is needed from what the tool reports (`agentbox wsl status
//! --json`, `agentbox vm status --json`), shows the last line the run says
//! and how long it has taken, and keeps the command itself below, for a
//! terminal.
//!
//! What still takes the user: restarting Windows when WSL's install asks,
//! virtualization in the firmware, and on Linux access to /dev/kvm (`sudo
//! agentbox host kvm`, which the status' problem names).
use std::path::Path;
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::{h_flex, v_flex};

use super::AppView;
use super::controls::{Btn, button_busy};
use crate::theme;

/// Step is one command of the tool that sets the machine up.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(super) enum Step {
    /// `agentbox wsl install`: WSL itself, as administrator.
    InstallWsl,
    /// `agentbox wsl init`: AgentBox's WSL distro.
    InitWsl,
    /// `agentbox vm init`: AgentBox's VM, on Linux and a Mac.
    InitVm,
}

impl Step {
    fn args(self) -> [&'static str; 2] {
        match self {
            Step::InstallWsl => ["wsl", "install"],
            Step::InitWsl => ["wsl", "init"],
            Step::InitVm => ["vm", "init"],
        }
    }

    /// command is the step as typed in a terminal.
    fn command(self) -> &'static str {
        match self {
            Step::InstallWsl => "agentbox wsl install",
            Step::InitWsl => "agentbox wsl init",
            Step::InitVm => "agentbox vm init",
        }
    }
}

/// Need is what stands between the app and a daemon, that a Step makes.
#[derive(Clone, Debug, PartialEq, Eq)]
pub(super) enum Need {
    /// Windows has no WSL that will do.
    Wsl,
    /// AgentBox's WSL distro isn't made (exists: false), or is half made.
    Distro { exists: bool, name: String },
    /// AgentBox's VM isn't made.
    Vm,
}

impl Need {
    fn step(&self) -> Step {
        match self {
            Need::Wsl => Step::InstallWsl,
            Need::Distro { .. } => Step::InitWsl,
            Need::Vm => Step::InitVm,
        }
    }
}

/// need reads what the tool said of the machine: `agentbox wsl status --json`
/// on Windows (hostwsl.Status), `agentbox vm status --json` elsewhere. None
/// is a machine that's there, or one only the user can repair (a WSL 1
/// distro), whose problem the start's error says. start_failed is the app's
/// own start of the daemon having failed: on Windows a distro that's there
/// and still has no daemon is one `wsl init` didn't finish (it stopped, or
/// the app was closed, after the import), which running it again finishes.
pub(super) fn need(windows: bool, status: &serde_json::Value, start_failed: bool) -> Option<Need> {
    let str_of = |k: &str| status.get(k).and_then(|s| s.as_str());
    let exists = status.get("exists").and_then(|e| e.as_bool());
    if windows {
        // "wsl" is WSL's version: only Windows' status has it.
        let wsl = str_of("wsl")?;
        if wsl.is_empty() {
            return Some(Need::Wsl);
        }
        let unusable = str_of("problem").is_some_and(|p| !p.is_empty());
        let half_made = exists == Some(true) && !unusable && start_failed;
        return (exists == Some(false) || half_made).then(|| Need::Distro {
            exists: exists == Some(true),
            name: str_of("name").unwrap_or("AgentBox").to_string(),
        });
    }
    let missing = exists == Some(false)
        || (str_of("state") == Some("missing") && str_of("mode") != Some("host"));
    missing.then_some(Need::Vm)
}

/// status_args is the question need answers, for this system.
pub(super) fn status_args(windows: bool) -> [&'static str; 3] {
    if windows {
        ["wsl", "status", "--json"]
    } else {
        ["vm", "status", "--json"]
    }
}

/// no_virtualization is `agentbox wsl init` finding that WSL can't start a
/// WSL 2 distro (hostwsl.ErrNoVirtualization, whose words the Electron app
/// looks for too): the Virtual Machine Platform is off, which `agentbox wsl
/// install` turns on, or virtualization is off in the firmware.
fn no_virtualization(error: &str) -> bool {
    error.contains("Virtual Machine Platform is off")
}

/// RESTART is `agentbox wsl install`'s exit status when WSL is installed and
/// Windows has to restart before it can be used (hostwsl's restartExit).
const RESTART: i32 = 3;

/// How a step ended.
#[derive(Clone, Debug, PartialEq, Eq)]
enum Outcome {
    Done,
    /// WSL is installed, and counts after Windows restarts.
    Restart,
    Failed(String),
}

#[derive(Default)]
struct Run {
    step: String,
    done: Option<Outcome>,
}

/// Setup is the app's own setup of the machine: the step under way, and how
/// the last one ended.
#[derive(Default)]
pub(super) struct Setup {
    /// running is the step under way, and since when.
    pub running: Option<(Step, Instant)>,
    run: Option<Arc<Mutex<Run>>>,
    /// said is the last line the step under way printed.
    pub said: String,
    pub error: Option<String>,
    /// restart is WSL installed, waiting for Windows to restart.
    pub restart: bool,
    /// last is the step run last, which an error or a restart is about.
    pub last: Option<Step>,
}

/// quiet keeps a console program the app runs from opening a console window
/// on Windows, where the app has none of its own.
pub(super) fn quiet(cmd: &mut std::process::Command) {
    #[cfg(windows)]
    {
        use std::os::windows::process::CommandExt as _;
        const CREATE_NO_WINDOW: u32 = 0x0800_0000;
        cmd.creation_flags(CREATE_NO_WINDOW);
    }
    #[cfg(not(windows))]
    let _ = cmd;
}

/// last_line is the last line said that isn't blank. A line redrawn in place
/// (a download's "12% of 340 MB\r38% of 340 MB") is its last redraw.
fn last_line(said: &str) -> String {
    said.split(['\n', '\r'])
        .map(str::trim)
        .rfind(|l| !l.is_empty())
        .unwrap_or_default()
        .to_string()
}

/// run_logged runs bin with args, telling said the last line it prints, on
/// stdout or stderr, as it goes. An error is how it exited, and its last
/// line without "error: ".
fn run_logged(
    bin: &Path,
    args: &[&str],
    mut said: impl FnMut(String),
) -> Result<(), (Option<i32>, String)> {
    use std::io::Read as _;
    let failed = |e: std::io::Error| (None, format!("{}: {e}", bin.display()));
    let (mut reader, writer) = std::io::pipe().map_err(failed)?;
    let mut child = {
        let mut cmd = std::process::Command::new(bin);
        cmd.args(args)
            .stdin(std::process::Stdio::null())
            .stdout(writer.try_clone().map_err(failed)?)
            .stderr(writer);
        quiet(&mut cmd);
        cmd.spawn().map_err(failed)?
        // cmd, and with it this side's ends of the pipe, goes here: the
        // read below ends when the child's do.
    };
    let mut all = String::new();
    let mut buf = [0u8; 1024];
    while let Ok(n) = reader.read(&mut buf) {
        if n == 0 {
            break;
        }
        all.push_str(&String::from_utf8_lossy(&buf[..n]));
        if all.len() > 4000 {
            let mut cut = all.len() - 4000;
            while !all.is_char_boundary(cut) {
                cut += 1;
            }
            all.drain(..cut);
        }
        said(last_line(&all));
    }
    match child.wait() {
        Ok(s) if s.success() => Ok(()),
        Ok(s) => {
            let last = last_line(&all);
            let last = last.trim_start_matches("error: ");
            Err((
                s.code(),
                if last.is_empty() {
                    format!("agentbox {}: {s}", args.join(" "))
                } else {
                    last.to_string()
                },
            ))
        }
        Err(e) => Err((None, e.to_string())),
    }
}

/// run_step runs a step with the tool at bin, and says in run how it goes.
fn run_step(bin: &Path, step: Step, run: &Mutex<Run>) {
    let done = match run_logged(bin, &step.args(), |s| run.lock().unwrap().step = s) {
        Ok(()) => Outcome::Done,
        Err((Some(RESTART), _)) if step == Step::InstallWsl => Outcome::Restart,
        Err((_, e)) => Outcome::Failed(e),
    };
    run.lock().unwrap().done = Some(done);
}

/// elapsed is a run's time as a clock: 0:42, 2:05.
fn elapsed(d: Duration) -> String {
    let s = d.as_secs();
    format!("{}:{:02}", s / 60, s % 60)
}

impl AppView {
    /// run_machine_setup starts a step, unless one is under way, and follows it:
    /// WSL installed with no restart needed goes straight on to the distro,
    /// and a machine that's made is the daemon's to start.
    pub(super) fn run_machine_setup(&mut self, step: Step, cx: &mut Context<Self>) {
        if self.conn.setup.running.is_some() {
            return;
        }
        let Some(bin) = super::connection::agentbox_cli() else {
            self.conn.setup.error = Some(t("native.setup.noTool"));
            cx.notify();
            return;
        };
        let run = Arc::new(Mutex::new(Run::default()));
        let s = &mut self.conn.setup;
        s.running = Some((step, Instant::now()));
        s.last = Some(step);
        s.run = Some(run.clone());
        s.said.clear();
        s.error = None;
        s.restart = false;
        std::thread::spawn(move || run_step(&bin, step, &run));
        cx.spawn(async move |this, cx| {
            loop {
                cx.background_executor()
                    .timer(Duration::from_millis(500))
                    .await;
                let going = this.update(cx, |this, cx| {
                    let next = this.poll_machine_setup();
                    cx.notify();
                    if let Some(next) = next {
                        this.run_machine_setup(next, cx);
                        return false;
                    }
                    this.conn.setup.running.is_some()
                });
                if !matches!(going, Ok(true)) {
                    return;
                }
            }
        })
        .detach();
        cx.notify();
    }

    /// poll_machine_setup takes what the step has said since the last look, and how
    /// it ended; it returns the step that follows it, if one does.
    fn poll_machine_setup(&mut self) -> Option<Step> {
        let run = self.conn.setup.run.clone()?;
        let r = run.lock().unwrap();
        self.conn.setup.said.clone_from(&r.step);
        let done = r.done.clone()?;
        drop(r);
        let (step, _) = self.conn.setup.running.take()?;
        self.conn.setup.run = None;
        // The machine's state has changed: ask again at once.
        self.conn.forget_vm_status();
        match done {
            Outcome::Failed(e) => self.conn.setup.error = Some(e),
            Outcome::Restart => self.conn.setup.restart = true,
            Outcome::Done if step == Step::InstallWsl => return Some(Step::InitWsl),
            // `wsl init` and `vm init` leave a daemon running, which the
            // connection finds by itself; one that isn't is started again.
            Outcome::Done => self.conn.start_again(),
        }
        None
    }

    /// render_machine_setup is the card that sets the machine up: what's missing,
    /// the button that makes it, how that goes, and the command for a
    /// terminal.
    pub(super) fn render_machine_setup(
        &self,
        need: &Need,
        problem: Option<String>,
        cx: &mut Context<Self>,
    ) -> AnyElement {
        let s = &self.conn.setup;
        let mac = cfg!(target_os = "macos");
        let muted = |text: String| {
            div()
                .text_sm()
                .text_color(theme::muted())
                .child(text)
                .into_any_element()
        };
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
        let (title, description, label, hint) = match need {
            Need::Wsl => (
                t("vm.wsl.title"),
                Some(t("vm.wsl.noWSL")),
                t("vm.wsl.install"),
                t("vm.wsl.onePrompt"),
            ),
            Need::Distro { exists, name } => (
                t("vm.wsl.title"),
                Some(tv(
                    "vm.wsl.description",
                    &[
                        ("name", name.as_str().into()),
                        ("path", format!("\\\\wsl.localhost\\{name}").into()),
                    ],
                )),
                t(if *exists {
                    "vm.wsl.finish"
                } else {
                    "vm.wsl.setUp"
                }),
                t("vm.wsl.noPassword"),
            ),
            Need::Vm => (
                t(if mac {
                    "vm.setup.title"
                } else {
                    "vm.run.setupTitle"
                }),
                mac.then(|| t("vm.setup.description")),
                t(if mac {
                    "vm.setup.setUp"
                } else {
                    "vm.run.setUp"
                }),
                t("vm.wsl.noPassword"),
            ),
        };
        let step = need.step();
        let busy = s.running.is_some();
        let run = |id: &'static str, label: String, step: Step, cx: &mut Context<Self>| {
            button_busy(
                id,
                label,
                Btn::Primary,
                busy,
                s.restart,
                cx.listener(move |this, _: &ClickEvent, _, cx| this.run_machine_setup(step, cx)),
            )
        };
        // What the run under way does, and its last line.
        let progress = s.running.map(|(running, since)| {
            v_flex()
                .gap_1()
                .px_3()
                .py_2()
                .rounded_md()
                .border_1()
                .border_color(theme::accent().opacity(0.35))
                .bg(theme::accent().opacity(0.08))
                .child(div().text_sm().child(format!(
                    "{} {}",
                    if running == Step::InstallWsl {
                        t("vm.wsl.installing")
                    } else {
                        t("vm.wsl.takesAWhile")
                    },
                    elapsed(since.elapsed())
                )))
                .when(!s.said.is_empty(), |el| {
                    el.child(
                        div()
                            .truncate()
                            .font_family(theme::mono())
                            .text_xs()
                            .text_color(theme::muted())
                            .child(s.said.clone()),
                    )
                })
        });
        let no_virt = s.error.as_deref().is_some_and(no_virtualization);
        let (terminal, command) = match need {
            Need::Wsl => (t("vm.wsl.orPowerShell"), "wsl --install --no-distribution"),
            _ => (t("vm.setup.orTerminal"), step.command()),
        };
        v_flex()
            .gap_3()
            .child(
                div()
                    .text_lg()
                    .font_weight(FontWeight::SEMIBOLD)
                    .child(title),
            )
            .when_some(description, |el, d| el.child(muted(d)))
            .when_some(problem.filter(|_| !busy && s.error.is_none()), |el, p| {
                el.child(notice(p, theme::starting()))
            })
            .child(
                h_flex()
                    .gap_2()
                    .child(run("setup-run", label, step, cx))
                    .when(!busy, |el| {
                        el.child(div().text_xs().text_color(theme::faint()).child(hint))
                    }),
            )
            .when_some(progress, |el, p| el.child(p))
            .when(s.restart, |el| {
                el.child(notice(t("vm.wsl.restart"), theme::starting()))
            })
            .when(no_virt, |el| {
                el.child(notice(t("vm.wsl.noVirtualization"), theme::error()))
                    .child(h_flex().gap_2().child(run(
                        "setup-platform",
                        t("vm.wsl.turnOn"),
                        Step::InstallWsl,
                        cx,
                    )))
                    .child(muted(t("vm.wsl.noVirtualizationBios")))
            })
            .when_some(s.error.clone().filter(|_| !no_virt), |el, e| {
                el.child(notice(e, theme::error()))
            })
            .child(div().text_xs().text_color(theme::faint()).child(terminal))
            .child(self.command_box("setup-command", command, cx))
            .into_any_element()
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[::core::prelude::v1::test]
    fn windows_needs_wsl_then_the_distro() {
        // hostwsl.Status: no WSL, WSL with no distro, a distro that's there.
        let no_wsl = json!({"wsl": "", "name": "AgentBox", "user": "", "exists": false,
            "problem": "WSL isn't installed."});
        assert_eq!(need(true, &no_wsl, false), Some(Need::Wsl));
        let no_distro = json!({"wsl": "2.4.13.0", "name": "AgentBox", "user": "ana", "exists": false,
            "problem": "AgentBox's WSL distro isn't set up: run agentbox wsl init"});
        assert_eq!(
            need(true, &no_distro, false),
            Some(Need::Distro {
                exists: false,
                name: "AgentBox".into()
            })
        );
        let there = json!({"wsl": "2.4.13.0", "name": "AgentBox", "user": "ana", "exists": true,
            "state": "Stopped", "version": 2});
        assert_eq!(need(true, &there, false), None);
        // A WSL 1 distro is the user's to convert: the problem says how.
        let wsl1 = json!({"wsl": "2.4.13.0", "name": "AgentBox", "user": "ana", "exists": true,
            "version": 1, "problem": "AgentBox is a WSL 1 distro"});
        assert_eq!(need(true, &wsl1, false), None);
        // A distro that's there with no daemon to start: finish its setup.
        assert_eq!(
            need(true, &there, true),
            Some(Need::Distro {
                exists: true,
                name: "AgentBox".into()
            })
        );
        assert_eq!(need(true, &wsl1, true), None);
        // Not a status at all (an older tool): nothing to offer.
        assert_eq!(
            need(true, &json!({"error": "unknown command"}), false),
            None
        );
        assert_eq!(need(true, &no_wsl, false).unwrap().step(), Step::InstallWsl);
        assert_eq!(need(true, &no_distro, false).unwrap().step(), Step::InitWsl);
    }

    #[::core::prelude::v1::test]
    fn a_vm_that_isnt_made_needs_vm_init() {
        // A Mac's (exists), Linux VM mode's (state), and host mode, which has
        // no VM to make.
        assert_eq!(
            need(false, &json!({"exists": false}), false),
            Some(Need::Vm)
        );
        assert_eq!(
            need(false, &json!({"mode": "vm", "state": "missing"}), false),
            Some(Need::Vm)
        );
        assert_eq!(
            need(false, &json!({"mode": "host", "state": "missing"}), false),
            None
        );
        assert_eq!(
            need(false, &json!({"exists": true, "status": "Running"}), false),
            None
        );
        assert_eq!(Need::Vm.step().command(), "agentbox vm init");
        assert_eq!(status_args(true), ["wsl", "status", "--json"]);
        assert_eq!(status_args(false), ["vm", "status", "--json"]);
    }

    #[::core::prelude::v1::test]
    fn the_last_line_is_the_step_even_redrawn_in_place() {
        assert_eq!(
            last_line("==> Downloading Ubuntu\n  12% of 340 MB\r  38% of 340 MB\r"),
            "38% of 340 MB"
        );
        assert_eq!(last_line("one\n\n"), "one");
        assert_eq!(last_line(""), "");
    }

    #[::core::prelude::v1::test]
    fn hostwsl_says_virtualization_is_off_in_these_words() {
        // internal/hostwsl/setup.go's ErrNoVirtualization, which the card
        // tells from any other failure by its words.
        let go = include_str!("../../../../../internal/hostwsl/setup.go");
        assert!(go.contains("Windows' Virtual Machine Platform is off"));
        assert!(no_virtualization(
            "WSL can't start a WSL 2 distro: Windows' Virtual Machine Platform is off, or virtualization is off in the BIOS/UEFI."
        ));
        assert!(!no_virtualization("downloading Ubuntu: connection reset"));
        // And its install's exit status for "restart Windows".
        let install = include_str!("../../../../../internal/hostwsl/install.go");
        assert!(install.contains(&format!("const restartExit = {RESTART}")));
    }

    #[cfg(unix)]
    mod runs {
        use super::super::*;
        use std::path::PathBuf;

        /// One at a time (connection.rs's tests say why).
        static ONE: Mutex<()> = Mutex::new(());

        /// tool is a stand-in for agentbox that says what each step would.
        fn tool(body: &str) -> (PathBuf, PathBuf) {
            let dir = std::env::temp_dir().join(format!(
                "agentbox-setup-{}-{}",
                std::process::id(),
                std::time::SystemTime::now()
                    .duration_since(std::time::UNIX_EPOCH)
                    .unwrap()
                    .as_nanos()
            ));
            std::fs::create_dir_all(&dir).unwrap();
            let bin = dir.join("agentbox");
            // Written by a child, not by this process: a script this process
            // has open for writing is inherited by whatever another test
            // forks meanwhile, and its exec then fails with "Text file busy"
            // (connection.rs's tests hold a lock of their own for the same
            // reason, which these can't share with every test that forks).
            let wrote = std::process::Command::new("sh")
                .args([
                    "-c",
                    "printf '%s\n' \"$BODY\" > \"$OUT\" && chmod 755 \"$OUT\"",
                ])
                .env(
                    "BODY",
                    format!("#!/bin/sh\necho \"$*\" >> '{}/log'\n{body}", dir.display()),
                )
                .env("OUT", &bin)
                .status()
                .unwrap();
            assert!(wrote.success());
            (dir, bin)
        }

        fn step(body: &str, step: Step) -> (Outcome, String, String) {
            let _one = ONE.lock().unwrap_or_else(|e| e.into_inner());
            let (dir, bin) = tool(body);
            let run = Mutex::new(Run::default());
            run_step(&bin, step, &run);
            let log = std::fs::read_to_string(dir.join("log")).unwrap_or_default();
            std::fs::remove_dir_all(&dir).ok();
            let run = run.into_inner().unwrap();
            (run.done.unwrap(), run.step, log)
        }

        #[::core::prelude::v1::test]
        fn a_step_runs_its_command_and_says_its_last_line() {
            // Progress on stdout and on stderr are both the step's.
            let (done, said, log) = step(
                "echo '==> Downloading Ubuntu 24.04 for WSL (340 MB)' >&2\necho \"AgentBox's distro, AgentBox, is ready.\"",
                Step::InitWsl,
            );
            assert_eq!(done, Outcome::Done);
            assert_eq!(said, "AgentBox's distro, AgentBox, is ready.");
            assert_eq!(log.trim(), "wsl init");
            let (done, _, log) = step(":", Step::InitVm);
            assert_eq!(done, Outcome::Done);
            assert_eq!(log.trim(), "vm init");
        }

        #[::core::prelude::v1::test]
        fn a_failed_step_says_why_without_the_prefix() {
            let (done, _, _) = step(
                "echo '==> Checking' >&2\necho 'error: downloading Ubuntu: connection reset' >&2\nexit 1",
                Step::InitWsl,
            );
            assert_eq!(
                done,
                Outcome::Failed("downloading Ubuntu: connection reset".into())
            );
            // Nothing said: how it exited.
            let (done, _, _) = step("exit 2", Step::InitVm);
            assert!(
                matches!(&done, Outcome::Failed(e) if e.starts_with("agentbox vm init: ")),
                "{done:?}"
            );
        }

        #[::core::prelude::v1::test]
        fn installing_wsl_can_end_in_a_restart() {
            let (done, _, log) = step(
                "echo 'WSL is installed, and Windows has to restart to finish' >&2\nexit 3",
                Step::InstallWsl,
            );
            assert_eq!(done, Outcome::Restart);
            assert_eq!(log.trim(), "wsl install");
            // Only the install's: any other step that exits 3 failed.
            let (done, _, _) = step("exit 3", Step::InitWsl);
            assert!(matches!(done, Outcome::Failed(_)), "{done:?}");
        }

        #[::core::prelude::v1::test]
        fn a_tool_that_isnt_there_fails_the_step() {
            let run = Mutex::new(Run::default());
            run_step(Path::new("/nonexistent/agentbox"), Step::InitVm, &run);
            let done = run.into_inner().unwrap().done.unwrap();
            assert!(
                matches!(&done, Outcome::Failed(e) if e.contains("/nonexistent/agentbox")),
                "{done:?}"
            );
        }
    }
}
