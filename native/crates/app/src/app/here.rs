//! The people of shared projects (internal/daemon/sharepeople.go, the
//! Electron app's lib/here.ts): who is in each project now and what they have
//! open (GET /v1/here once the stream connects, then the stream's here
//! events), and what this window has open, which it tells the daemon
//! (PUT /v1/here) every api.HereRenewSeconds and soon after it changes. The
//! daemon passes what is open in a remote project on to its owner, and the
//! owner tells everyone in it. A person with id "" is this app's own user, on
//! whichever machine: they are left out of what is shown. Quitting says the
//! window left (DELETE /v1/here/<client>); a crash lets it lapse.
//!
//! A window in the background, or minimised, is still where its user is, as
//! in the Electron app: what it has open is only worked out from AppView.
use std::collections::HashMap;
use std::time::{Duration, Instant};

use agentbox_api::client::Method;
use agentbox_api::{HereRequest, PersonHere, ProjectHere};

/// RENEW is api.HereRenewSeconds (the generator keeps no constants).
const RENEW: Duration = Duration::from_secs(15);
use gpui::*;

use super::AppView;
use super::panel::{PView, Whose};

pub(super) struct Here {
    client: String,
    /// Who else is in each project, by its name.
    pub by_project: HashMap<String, Vec<PersonHere>>,
    /// The last body sent, and when; the place it named (typing aside).
    sent: String,
    sent_at: Option<Instant>,
    place: String,
    pending: bool,
}

impl Default for Here {
    fn default() -> Self {
        let nanos = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .map(|d| d.as_nanos())
            .unwrap_or_default();
        Self {
            client: format!("native-{:x}-{:x}", std::process::id(), nanos),
            by_project: HashMap::new(),
            sent: String::new(),
            sent_at: None,
            place: String::new(),
            pending: false,
        }
    }
}

/// others is a list's people besides this app's own user.
fn others(people: Vec<PersonHere>) -> Vec<PersonHere> {
    people.into_iter().filter(|p| !p.id.is_empty()).collect()
}

impl Here {
    /// forget makes the next frame say again what the window has open: the
    /// stream reconnected, maybe to a daemon that just started.
    pub fn forget(&mut self) {
        self.sent.clear();
        self.sent_at = None;
    }

    /// apply takes a here event's data: who is in one project now.
    pub fn apply(&mut self, ph: ProjectHere) {
        let people = others(ph.people);
        if people.is_empty() {
            self.by_project.remove(&ph.project);
        } else {
            self.by_project.insert(ph.project, people);
        }
    }

    /// seed replaces everything, from GET /v1/here.
    pub fn seed(&mut self, all: Vec<ProjectHere>) {
        self.by_project.clear();
        for ph in all {
            self.apply(ph);
        }
    }
}

impl AppView {
    /// people_in is who else is in a project now.
    pub(super) fn people_in(&self, project: &str) -> &[PersonHere] {
        self.here
            .by_project
            .get(project)
            .map(Vec::as_slice)
            .unwrap_or_default()
    }

    /// load_here reads who is in every project, as the stream (re)connects.
    pub(super) fn load_here(&mut self, cx: &mut Context<Self>) {
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            // A daemon from before people answers 404: nobody is shown.
            let Ok(all) = client.get::<Vec<ProjectHere>>("/v1/here").await else {
                return;
            };
            this.update(cx, |this, cx| {
                this.here.seed(all);
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// here_place is what the window shows (App.tsx hereOf): a project, the
    /// session of its chat or the agent open, and that agent's desktop or
    /// terminal when the panel shows it. Home, Settings and the pages over
    /// the chat show no project.
    fn here_place(&self) -> HereRequest {
        let mut req = HereRequest {
            client: self.here.client.clone(),
            ..Default::default()
        };
        let Some(project) = self.project.clone() else {
            return req;
        };
        if self.is_home()
            || self.settings
            || self.work.is_some()
            || self.side.page.is_some()
            || self.cut_off()
            || self.first_run_open()
        {
            return req;
        }
        req.project = project.clone();
        if self.open == "lead" {
            if let Some(s) = self.sessions.current(&project) {
                req.session = s.id.clone();
            }
            return req;
        }
        if let Some(a) = self.open_agent() {
            req.agent = a.name.clone();
            req.tab = "chat".into();
            let drawn = if self.narrow() {
                self.widths.drawer
            } else {
                !self.panel.folded
            };
            if let Whose::Agent(r) = self.panel_whose()
                && drawn
            {
                match self.panel.view_of(&r) {
                    PView::Desktop => req.tab = "desktop".into(),
                    PView::Terminal => req.tab = "terminal".into(),
                    _ => {}
                }
            }
        }
        req
    }

    /// here_check, on each frame, tells the daemon soon when what the window
    /// has open changed, and forgets the typing of a place it left.
    pub(super) fn here_check(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        if self.here.pending {
            return;
        }
        let place = serde_json::to_string(&self.here_place()).unwrap_or_default();
        if place != self.here.place {
            crate::people::stop_typing();
        }
        let mut req = self.here_place();
        req.typing = crate::people::typing();
        let body = serde_json::to_string(&req).unwrap_or_default();
        if body != self.here.sent {
            self.here.pending = true;
            cx.spawn_in(window, async move |this, cx| {
                cx.background_executor()
                    .timer(Duration::from_millis(120))
                    .await;
                this.update(cx, |this, cx| {
                    this.here.pending = false;
                    this.here_renew(cx)
                })
                .ok();
            })
            .detach();
        }
    }

    /// here_renew says what the window has open when it changed, or when the
    /// last renewal is HereRenewSeconds old. A typing that stopped is a
    /// change the presence tick finds within a second.
    pub(super) fn here_renew(&mut self, cx: &mut Context<Self>) {
        let place = self.here_place();
        let place_body = serde_json::to_string(&place).unwrap_or_default();
        let mut req = place;
        req.typing = crate::people::typing();
        let body = serde_json::to_string(&req).unwrap_or_default();
        let h = &mut self.here;
        let due = h.sent_at.is_none_or(|at| at.elapsed() >= RENEW);
        if !due && body == h.sent {
            return;
        }
        h.sent = body.clone();
        h.place = place_body;
        h.sent_at = Some(Instant::now());
        let client = self.client.clone();
        // A daemon from before people answers 404, and one not answering
        // fails: either way the next renewal, RENEW on, tries again.
        cx.spawn(async move |_, _| {
            let _ = client
                .raw(Method::PUT, "/v1/here", Some(body.into_bytes()))
                .await;
        })
        .detach();
    }

    /// leave_here says the window left, as it quits.
    pub(super) fn leave_here(&self, cx: &mut Context<Self>) {
        let client = self.client.clone();
        let path = format!("/v1/here/{}", crate::files::encode(&self.here.client));
        cx.on_app_quit(move |_, _| {
            let client = client.clone();
            let path = path.clone();
            async move {
                let _ = client.raw(Method::DELETE, &path, None).await;
            }
        })
        .detach();
    }
}

#[cfg(test)]
mod tests {
    use super::Here;
    use agentbox_api::{PersonHere, ProjectHere};

    fn person(id: &str) -> PersonHere {
        PersonHere {
            id: id.into(),
            name: id.into(),
            ..Default::default()
        }
    }

    #[test]
    fn this_apps_own_user_is_left_out_and_an_empty_project_forgotten() {
        let mut h = Here::default();
        h.seed(vec![ProjectHere {
            project: "r1~web".into(),
            people: vec![person(""), person("ana")],
        }]);
        assert_eq!(h.by_project["r1~web"].len(), 1);
        h.apply(ProjectHere {
            project: "r1~web".into(),
            people: vec![person("")],
        });
        assert!(!h.by_project.contains_key("r1~web"));
    }
}
