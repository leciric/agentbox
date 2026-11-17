//! Restarting a daemon older than this app, and the notice over the page
//! while it can't be yet (StaleDaemonNotice.tsx, daemon.ts's restartIfStale).
//! The decisions are staledaemon.rs's; this asks the daemon its jobs, stops
//! it, and lets the connection start this app's own (connection.rs).
//!
//! Only an app with its own tool built in does any of it (cli.rs), against
//! this computer's daemon: one from a checkout keeps the sidebar's hint and
//! its command to copy (sidebar_actions.rs's Offered::DaemonBehind).
use std::time::{Duration, Instant};

use agentbox_api::client::Method;
use agentbox_i18n::{t, tv};
use gpui::*;

use super::AppView;
use super::controls::{self, Btn};
use crate::staledaemon::{Look, RETRY, Stale, Tracker};
use crate::theme;

/// How long a daemon told to stop is given to stop answering (daemon.ts's
/// restart: 300 looks, 100 ms apart).
const STOP_WAIT: Duration = Duration::from_secs(30);

#[derive(Default)]
pub(super) struct StaleDaemon {
    /// None for an app that restarts no daemon: see [`AppView::stale_tracker`].
    tracker: Option<Tracker>,
    made: bool,
    /// asked_at is when the daemon was last asked its jobs.
    asked_at: Option<Instant>,
    /// asking is a look at its jobs under way.
    asking: bool,
    /// stopping is a restart under way: the daemon was told to stop.
    stopping: bool,
    /// down is that the daemon told to stop no longer answers.
    down: bool,
    /// stopped is a restart whose daemon has stopped: the next one to
    /// answer is its outcome.
    stopped: bool,
}

impl StaleDaemon {
    /// current is the stale daemon the window tells about.
    pub fn current(&self) -> Option<&Stale> {
        self.tracker.as_ref().and_then(Tracker::current)
    }

    /// tracks is whether this app restarts a stale daemon itself.
    pub fn tracks(&self) -> bool {
        self.tracker.is_some()
    }

    fn restarting(&self) -> bool {
        self.stopping || self.stopped
    }
}

/// running counts the jobs a restart would cut short.
fn running(jobs: &[agentbox_api::Job]) -> usize {
    jobs.iter().filter(|j| j.status == "running").count()
}

impl AppView {
    /// stale_tracker makes the tracker the first time it's asked for: only
    /// for an app that knows its version, has the tool to start a daemon of
    /// it, and talks to this computer's daemon.
    fn stale_tracker(&mut self) -> Option<&mut Tracker> {
        if !self.stale.made {
            self.stale.made = true;
            // Nor does an app told to start no daemon stop one.
            let local = self.client.socket() == agentbox_api::client::default_socket()
                && std::env::var_os("AGENTBOX_NO_AUTOSTART").is_none();
            // The version it restarts into is the managed copy's: this
            // app's, or the Electron app's newer one (cli.rs).
            self.stale.tracker = crate::cli::daemon_version()
                .filter(|_| local && crate::cli::bundled().is_some())
                .map(|v| Tracker::new(&v));
        }
        self.stale.tracker.as_mut()
    }

    /// stale_look is told each time the daemon answers with its version:
    /// every few seconds (connection.rs), and so as the app starts and
    /// whenever it reconnects.
    pub(super) fn stale_look(&mut self, daemon: &str, cx: &mut Context<Self>) {
        let was = self.stale.current().cloned();
        if self.stale_tracker().is_none() || self.stale.stopping {
            return;
        }
        if self.stale.stopped {
            // The daemon that answers after the restart.
            self.stale.stopped = false;
            if let Some(t) = self.stale.tracker.as_mut() {
                t.restarted(Some(daemon));
            }
            cx.notify();
            return;
        }
        let look = self.stale.tracker.as_mut().map(|t| t.look(daemon));
        if look == Some(Look::AskJobs)
            && !self.stale.asking
            && self.stale.asked_at.is_none_or(|at| at.elapsed() >= RETRY)
        {
            self.stale.asking = true;
            self.stale.asked_at = Some(Instant::now());
            let client = self.client.clone();
            let daemon = daemon.to_string();
            cx.spawn(async move |this, cx| {
                let jobs = client.get::<Vec<agentbox_api::Job>>("/v1/jobs").await;
                this.update(cx, |this, cx| {
                    this.stale.asking = false;
                    // A daemon that didn't say is asked again next time.
                    let Ok(jobs) = jobs else {
                        this.stale.asked_at = None;
                        return;
                    };
                    let restart = this
                        .stale
                        .tracker
                        .as_mut()
                        .is_some_and(|t| t.jobs(&daemon, running(&jobs)));
                    if restart {
                        this.restart_stale(cx);
                    }
                    cx.notify();
                })
                .ok();
            })
            .detach();
        }
        if was.as_ref() != self.stale.current() {
            cx.notify();
        }
    }

    /// stale_down is told each time the daemon doesn't answer.
    pub(super) fn stale_down(&mut self) {
        self.stale.down = self.stale.stopping;
    }

    /// restart_stale_now is the notice's button: whatever the daemon is doing.
    fn restart_stale_now(&mut self, cx: &mut Context<Self>) {
        if self.stale.restarting() {
            return;
        }
        if let Some(t) = self.stale.tracker.as_mut() {
            t.restart_now();
            self.restart_stale(cx);
        }
    }

    /// restart_stale stops the daemon. Once it no longer answers, the
    /// connection starts this app's own, as it does for any daemon that's
    /// gone (maybe_start_daemon, from the managed copy), and the first
    /// answer after that is the restart's outcome (stale_look).
    fn restart_stale(&mut self, cx: &mut Context<Self>) {
        self.stale.stopping = true;
        self.stale.down = false;
        cx.notify();
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let _ = client.raw(Method::POST, "/v1/shutdown", None).await;
            // It's down once it doesn't answer here, or didn't answer the
            // connection's own look, which may have started the next one
            // already.
            let until = Instant::now() + STOP_WAIT;
            while Instant::now() < until
                && !this.update(cx, |this, _| this.stale.down).unwrap_or(true)
                && client.get::<serde_json::Value>("/v1/version").await.is_ok()
            {
                cx.background_executor()
                    .timer(Duration::from_millis(100))
                    .await;
            }
            this.update(cx, |this, cx| {
                this.stale.stopping = false;
                this.stale.stopped = true;
                this.stale.asked_at = None;
                this.start_daemon_again(cx);
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// render_stale_notice is the line over the page while the daemon is
    /// older than the app: why, and the restart now.
    pub(super) fn render_stale_notice(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let stale = self.stale.current()?;
        let values = [
            ("daemon", stale.daemon.as_str().into()),
            ("app", stale.app.as_str().into()),
            ("count", stale.jobs.into()),
        ];
        let text = if stale.jobs > 0 {
            tv("shell.app.daemonStale", &values)
        } else {
            tv("shell.app.daemonStaleRestarted", &values)
        };
        let restarting = self.stale.restarting();
        let label = if restarting {
            t("shell.app.daemonStaleRestarting")
        } else if stale.jobs > 0 {
            tv("shell.app.daemonStaleRestartNow", &values)
        } else {
            t("shell.app.daemonStaleRestart")
        };
        let color = theme::starting();
        Some(
            div()
                .id("stale-daemon")
                .flex_none()
                .flex()
                .flex_col()
                .items_start()
                .gap_2()
                .px_4()
                .py_2()
                .border_b_1()
                .border_color(color.opacity(0.35))
                .bg(color.opacity(0.08))
                .text_sm()
                .text_color(theme::primary())
                .child(text)
                .child(controls::button_busy(
                    "stale-daemon-restart",
                    label,
                    Btn::Secondary,
                    restarting,
                    false,
                    cx.listener(|this, _: &ClickEvent, _, cx| this.restart_stale_now(cx)),
                ))
                .into_any_element(),
        )
    }
}

#[cfg(test)]
mod tests {
    use super::running;

    #[test]
    fn only_running_jobs_hold_a_restart_back() {
        let job = |status: &str| agentbox_api::Job {
            status: status.into(),
            ..Default::default()
        };
        assert_eq!(running(&[job("done"), job("running"), job("failed")]), 1);
        assert_eq!(running(&[]), 0);
    }
}
