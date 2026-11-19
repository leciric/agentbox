//! What AgentBox didn't carry on by itself as it started, after it stopped
//! unexpectedly or restarted too many times in a row while agents worked,
//! and the line over the page with Resume all (RestartHoldNotice.tsx,
//! internal/daemon/restart.go): those agents On hold and those chats' turns
//! wait for you, and Resume all carries them on one at a time, as the VM's
//! memory allows. Read as the app connects, and kept by EVENT_RESTART_HOLD.
use agentbox_api::{RestartHold, ResumeAllResult};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;

use super::AppView;
use super::controls::{self, Btn};
use crate::theme;

#[derive(Default)]
pub(super) struct RestartHoldState {
    hold: Option<RestartHold>,
    /// resuming is a Resume all under way.
    resuming: bool,
    /// error is why the last Resume all failed.
    error: Option<String>,
}

/// waiting is what the notice says waits: (key, the counts it takes).
fn waiting(agents: usize, chats: usize) -> (&'static str, Vec<(&'static str, usize)>) {
    match (agents, chats) {
        (0, n) => ("shell.app.restartHoldChats", vec![("count", n)]),
        (n, 0) => ("shell.app.restartHoldAgents", vec![("count", n)]),
        (a, c) => (
            "shell.app.restartHoldBoth",
            vec![("agents", a), ("chats", c)],
        ),
    }
}

impl AppView {
    /// load_restart_hold reads what waits for you.
    pub(super) fn load_restart_hold(&mut self, cx: &mut Context<Self>) {
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client.get::<RestartHold>("/v1/restart-hold").await;
            this.update(cx, |this, cx| {
                if let Ok(hold) = res {
                    this.apply_restart_hold(hold, cx);
                }
            })
            .ok();
        })
        .detach();
    }

    /// apply_restart_hold shows hold, from a read or an event.
    pub(super) fn apply_restart_hold(&mut self, hold: RestartHold, cx: &mut Context<Self>) {
        self.restart_hold.hold = Some(hold);
        cx.notify();
    }

    /// resume_all is the notice's button.
    fn resume_all(&mut self, cx: &mut Context<Self>) {
        if self.restart_hold.resuming {
            return;
        }
        self.restart_hold.resuming = true;
        self.restart_hold.error = None;
        cx.notify();
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client
                .post::<_, ResumeAllResult>("/v1/restart-hold/resume", &serde_json::json!({}))
                .await;
            this.update(cx, |this, cx| {
                this.restart_hold.resuming = false;
                if let Err(e) = res {
                    this.restart_hold.error = Some(e.to_string());
                }
                this.load_restart_hold(cx);
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// render_restart_hold_notice is the line over the page while anything
    /// waits for you: why, how many, and Resume all.
    pub(super) fn render_restart_hold_notice(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let hold = self.restart_hold.hold.as_ref()?;
        let (agents, chats) = (hold.agents.len(), hold.chats.len());
        if agents + chats == 0 {
            return None;
        }
        let title = if hold.unexpected {
            t("shell.app.restartHoldUnexpected")
        } else {
            t("shell.app.restartHoldTooOften")
        };
        let (key, counts) = waiting(agents, chats);
        let values: Vec<(&str, agentbox_i18n::Value)> =
            counts.into_iter().map(|(k, n)| (k, n.into())).collect();
        let resuming = self.restart_hold.resuming;
        let label = if resuming {
            t("shell.app.restartHoldResuming")
        } else {
            t("shell.app.restartHoldResumeAll")
        };
        let color = theme::starting();
        let note = |text: String| div().text_xs().text_color(theme::secondary()).child(text);
        Some(
            div()
                .id("restart-hold")
                .flex_none()
                .flex()
                .flex_col()
                .items_start()
                .gap_1()
                .px_4()
                .py_2()
                .border_b_1()
                .border_color(color.opacity(0.35))
                .bg(color.opacity(0.08))
                .text_sm()
                .text_color(theme::primary())
                .child(div().font_weight(FontWeight::MEDIUM).child(title))
                .child(note(tv(key, &values)))
                .when(!hold.why.is_empty(), |el| {
                    el.child(note(tv(
                        "shell.app.restartHoldWhy",
                        &[("why", hold.why.as_str().into())],
                    )))
                })
                .when_some(self.restart_hold.error.clone(), |el, e| el.child(note(e)))
                .child(div().pt_1().child(controls::button_busy(
                    "restart-hold-resume-all",
                    label,
                    Btn::Secondary,
                    resuming,
                    false,
                    cx.listener(|this, _: &ClickEvent, _, cx| this.resume_all(cx)),
                )))
                .into_any_element(),
        )
    }
}

#[cfg(test)]
mod tests {
    use super::waiting;
    use agentbox_i18n::t;

    #[test]
    fn says_agents_chats_or_both() {
        assert_eq!(
            waiting(3, 0),
            ("shell.app.restartHoldAgents", vec![("count", 3)])
        );
        assert_eq!(
            waiting(0, 2),
            ("shell.app.restartHoldChats", vec![("count", 2)])
        );
        assert_eq!(
            waiting(3, 2),
            (
                "shell.app.restartHoldBoth",
                vec![("agents", 3), ("chats", 2)]
            )
        );
        for key in [
            "shell.app.restartHoldUnexpected",
            "shell.app.restartHoldTooOften",
            "shell.app.restartHoldAgents",
            "shell.app.restartHoldChats",
            "shell.app.restartHoldBoth",
            "shell.app.restartHoldResumeAll",
            "agent.status.onHoldRestart",
        ] {
            assert_ne!(t(key), key, "{key} isn't in the catalog");
        }
    }
}
