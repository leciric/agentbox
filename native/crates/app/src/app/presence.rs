//! The window's presence lease (api.PresenceRequest), as the Electron app's
//! lib/presence.ts keeps one for each of its windows: what is on screen, so
//! the daemon spends its background work where it is seen. It samples an
//! agent's live resources only while the panel's Resources view shows them
//! (resources.rs), and backs its housekeeping off for projects no window shows,
//! or for everything while the computer is on battery or hot (hostpower.rs).
//!
//! What the window shows is worked out from AppView on each frame; a change
//! renews the lease 150 ms later (switching pages makes one renewal), and a
//! tick renews it every third of its TTL whatever happens. A hidden window
//! (minimised, covered on macOS, closed to the tray) draws no frames, so its
//! visibility changing renews it too, with no views. Quitting gives the lease
//! up; a crash leaves it to lapse on its own.
use std::time::{Duration, Instant};

use agentbox_api::client::Method;
use agentbox_api::{HostPower, PresenceRequest, PresenceResponse, PresenceView};
use gpui::*;

use super::AppView;
use super::panel::{PView, Whose};

/// The renewal until the daemon says its TTL: a third of 45 seconds.
const DEFAULT_EVERY: Duration = Duration::from_secs(15);
/// The host's power is read again this often (hostpower.rs).
const POWER_EVERY: u64 = 5;

pub(super) struct Presence {
    client: String,
    /// The last body sent, and when.
    sent: String,
    sent_at: Option<Instant>,
    every: Duration,
    /// A renewal is due in a moment.
    pending: bool,
    power: HostPower,
    ticks: u64,
    _tick: Option<Task<()>>,
    _visibility: Option<Subscription>,
}

impl Default for Presence {
    fn default() -> Self {
        let nanos = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .map(|d| d.as_nanos())
            .unwrap_or_default();
        Self {
            client: format!("native-{:x}-{:x}", std::process::id(), nanos),
            sent: String::new(),
            sent_at: None,
            every: DEFAULT_EVERY,
            pending: false,
            power: crate::hostpower::read(),
            ticks: 0,
            _tick: None,
            _visibility: None,
        }
    }
}

/// merge_views is the lease's views: one per project and agent, asking for
/// resources if any of its views does (lib/presence.ts mergeViews).
pub(super) fn merge_views(views: impl IntoIterator<Item = PresenceView>) -> Vec<PresenceView> {
    let mut out: Vec<PresenceView> = Vec::new();
    for v in views {
        if v.project.is_empty() {
            continue;
        }
        match out
            .iter_mut()
            .find(|o| o.project == v.project && o.agent == v.agent)
        {
            Some(o) => o.resources |= v.resources,
            None => out.push(v),
        }
    }
    out.sort_by(|a, b| (&a.project, &a.agent).cmp(&(&b.project, &b.agent)));
    out
}

impl AppView {
    /// start_presence takes the window's lease and keeps it.
    pub(super) fn start_presence(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        self.presence._visibility =
            Some(cx.observe_window_visibility(window, |this, _, window, cx| {
                this.presence_soon(window, cx)
            }));
        self.presence._tick = Some(cx.spawn_in(window, async move |this, cx| {
            loop {
                cx.background_executor().timer(Duration::from_secs(1)).await;
                if this
                    .update_in(cx, |this, window, cx| this.presence_tick(window, cx))
                    .is_err()
                {
                    return;
                }
            }
        }));
        cx.on_app_quit(|this, _| {
            let client = this.client.clone();
            let path = format!(
                "/v1/presence/{}",
                crate::files::encode(&this.presence.client)
            );
            async move {
                let _ = client.raw(Method::DELETE, &path, None).await;
            }
        })
        .detach();
    }

    /// presence_request is what the window shows now.
    pub(super) fn presence_request(&self, window: &Window) -> PresenceRequest {
        let visible = window.is_visible();
        PresenceRequest {
            client: self.presence.client.clone(),
            kind: "native".into(),
            visible,
            views: if visible {
                merge_views(self.presence_views())
            } else {
                Vec::new()
            },
            power: Some(self.presence.power.clone()),
        }
    }

    /// presence_views is the project or agent open, and the agent whose
    /// Resources the panel shows. Home, Settings and the first run show no
    /// project, as the Electron app's presenceOf has it.
    fn presence_views(&self) -> Vec<PresenceView> {
        let Some(project) = self.project.clone() else {
            return Vec::new();
        };
        if self.is_home() || self.settings || self.cut_off() || self.first_run_open() {
            return Vec::new();
        }
        let mut views = vec![PresenceView {
            project: project.clone(),
            agent: match self.open_agent() {
                Some(a) if self.open != "lead" => a.r#ref.clone(),
                _ => String::new(),
            },
            resources: false,
        }];
        if let Some(r) = self.resources_shown() {
            views.push(PresenceView {
                project,
                agent: r,
                resources: true,
            });
        }
        views
    }

    /// resources_shown is the agent whose Resources view is drawn: in the
    /// panel beside the chat, or in its drawer in a narrow window.
    pub(super) fn resources_shown(&self) -> Option<String> {
        let Whose::Agent(r) = self.panel_whose() else {
            return None;
        };
        if self.panel.view_of(&r) != PView::Resources || self.overlaid_page() {
            return None;
        }
        let drawn = if self.narrow() {
            self.widths.drawer
        } else {
            !self.panel.folded
        };
        let awake = self
            .agent_by_ref(&r)
            .is_some_and(|a| !super::agent_status::asleep(&a.state));
        (drawn && awake).then_some(r)
    }

    /// overlaid_page is a page drawn over the chat and its panel.
    fn overlaid_page(&self) -> bool {
        self.settings || self.work.is_some() || self.side.page.is_some()
    }

    /// presence_check, on each frame, renews the lease soon if what it says
    /// has changed.
    pub(super) fn presence_check(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        if self.presence._tick.is_none() || self.presence.pending {
            return;
        }
        let body = serde_json::to_string(&self.presence_request(window)).unwrap_or_default();
        if body != self.presence.sent {
            self.presence_soon(window, cx);
        }
    }

    /// presence_soon renews the lease in a moment.
    fn presence_soon(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        if self.presence.pending {
            return;
        }
        self.presence.pending = true;
        cx.spawn_in(window, async move |this, cx| {
            cx.background_executor()
                .timer(Duration::from_millis(150))
                .await;
            this.update_in(cx, |this, window, cx| {
                this.presence.pending = false;
                this.presence_renew(true, window, cx)
            })
            .ok();
        })
        .detach();
    }

    fn presence_tick(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        self.presence.ticks += 1;
        if self.presence.ticks.is_multiple_of(POWER_EVERY) {
            let power = crate::hostpower::read();
            if power != self.presence.power {
                self.presence.power = power;
                self.presence_renew(true, window, cx);
                return;
            }
        }
        self.presence_renew(false, window, cx);
    }

    /// presence_renew sends the lease when it changed (changed), or when the
    /// last renewal is a third of the TTL old.
    fn presence_renew(&mut self, changed: bool, window: &mut Window, cx: &mut Context<Self>) {
        let req = self.presence_request(window);
        let body = serde_json::to_string(&req).unwrap_or_default();
        let p = &mut self.presence;
        let due = p.sent_at.is_none_or(|at| at.elapsed() >= p.every);
        if !due && (!changed || body == p.sent) {
            return;
        }
        p.sent = body.clone();
        p.sent_at = Some(Instant::now());
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client
                .raw(Method::PUT, "/v1/presence", Some(body.into_bytes()))
                .await
                .and_then(|b| Ok(serde_json::from_slice::<PresenceResponse>(&b)?));
            this.update(cx, |this, _| match res {
                Ok(r) if r.ttl_seconds > 0 => {
                    this.presence.every = Duration::from_secs(r.ttl_seconds as u64) / 3;
                }
                Ok(_) => {}
                // A daemon from before presence, or one not answering: the
                // next tick tries again.
                Err(_) => this.presence.sent_at = None,
            })
            .ok();
        })
        .detach();
    }
}

#[cfg(test)]
mod tests {
    use super::merge_views;
    use agentbox_api::PresenceView;

    fn v(project: &str, agent: &str, resources: bool) -> PresenceView {
        PresenceView {
            project: project.into(),
            agent: agent.into(),
            resources,
        }
    }

    #[test]
    fn merges_views_of_the_same_agent() {
        let got = merge_views([
            v("p", "p/agent-02", false),
            v("", "", false),
            v("a", "", false),
            v("p", "p/agent-02", true),
        ]);
        assert_eq!(got, [v("a", "", false), v("p", "p/agent-02", true)]);
    }
}
