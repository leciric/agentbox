//! The daemon's connection, and what the window shows while there is none
//! (App.tsx's connection notices, StartingNotice.tsx, VMSetup.tsx,
//! RunInVM.tsx's LinuxVMSetup and WSLSetup.tsx as far as a thin client can).
//!
//! It asks GET /v1/version every few seconds. When nothing answers on the
//! default socket and the command-line tool is on PATH, it starts the daemon
//! the way Electron's main process does (desktop/src/main/daemon.ts): runs
//! `agentbox daemon start` once, showing the last line it says on stderr
//! and how long it has taken, then waits for the socket. On a Mac, or a
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

/// What `agentbox daemon start` has said so far, and how it ended.
#[derive(Default)]
struct StartRun {
    step: String,
    done: Option<Result<(), String>>,
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
    /// start_error is how the app's own start failed.
    pub start_error: Option<String>,
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
/// the Electron app's managed copy first), else the one on PATH.
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
    which("agentbox")
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
                self.maybe_start_daemon();
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
    fn maybe_start_daemon(&mut self) {
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
        std::thread::spawn(move || {
            use std::io::Read as _;
            let child = std::process::Command::new(&bin)
                .args(["daemon", "start"])
                .stdin(std::process::Stdio::null())
                .stdout(std::process::Stdio::null())
                .stderr(std::process::Stdio::piped())
                .spawn();
            let mut child = match child {
                Ok(c) => c,
                Err(e) => {
                    run.lock().unwrap().done = Some(Err(format!("{}: {e}", bin.display())));
                    return;
                }
            };
            let mut said = String::new();
            if let Some(mut err) = child.stderr.take() {
                let mut buf = [0u8; 512];
                while let Ok(n) = err.read(&mut buf) {
                    if n == 0 {
                        break;
                    }
                    said.push_str(&String::from_utf8_lossy(&buf[..n]));
                    if said.len() > 4000 {
                        said = said[said.len() - 4000..].to_string();
                    }
                    run.lock().unwrap().step = last_line(&said);
                }
            }
            let status = child.wait();
            let done = match status {
                Ok(s) if s.success() => Ok(()),
                Ok(s) => Err(said
                    .lines()
                    .rev()
                    .find(|l| !l.trim().is_empty())
                    .map(|l| l.trim().trim_start_matches("error: ").to_string())
                    .unwrap_or_else(|| format!("agentbox daemon start: {s}"))),
                Err(e) => Err(e.to_string()),
            };
            run.lock().unwrap().done = Some(done);
        });
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
        if let Some(done) = &r.done {
            c.starting = None;
            if let Err(e) = done {
                c.start_error = Some(e.clone());
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
                let mut s = tv(
                    "shell.app.daemonStarting",
                    &[("elapsed", elapsed(since.elapsed()).into())],
                );
                if !step.is_empty() {
                    s = format!("{s} {step}");
                }
                (s, theme::accent())
            }
            None => (
                tv(
                    "shell.app.daemonUnreachable",
                    &[("error", c.error.clone().unwrap_or_default().into())],
                ),
                theme::error(),
            ),
        };
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
                .child(div().text_sm().child(tv(
                    "shell.app.daemonStarting",
                    &[("elapsed", elapsed(since.elapsed()).into())],
                )))
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
                            .child(div().size_2().rounded_full().bg(if c.starting.is_some() {
                                theme::starting()
                            } else {
                                theme::error()
                            }))
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
