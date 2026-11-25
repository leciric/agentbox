//! Settings → Share's paired AgentBoxes (#813, #821, #822; ShareSettings.tsx),
//! under the phones in the same section: this AgentBox pairs another with a
//! one-time link (POST /v1/share/pairings, through the same page phones use),
//! and chooses for each one which projects it reaches, what it may do there
//! (view, chat, or chat + make agents + merge), how many agents it may run
//! here, and whether the agents its chat messages ask for may run on its own
//! PC (PATCH /v1/share/peers/{id}); each shows what it spent here and what it
//! did (GET …/activity). Or this one pastes another's link (POST
//! /v1/share/remotes), and the projects that one shares show in the sidebar,
//! under Remote. The section reads it all again every three seconds while it
//! is open (phone.rs's poll), and at once on the stream's share events.
use std::collections::HashMap;

use agentbox_api::client::Method;
use agentbox_api::{
    AddShareRemoteRequest, ShareActivity, SharePairing, SharePeer, ShareRemote, ShareStatus,
    UpdateSharePeerRequest, UpdateShareRequest,
};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::{h_flex, v_flex};

use super::super::AppView;
use super::super::controls::{Btn, Choice, button, error_line, labeled, note_in, toggle};
use super::kit::{enc, local_date, local_date_time, mono, note};
use super::{Entry, Group, entry, group};
use crate::theme;

/// MAX_SLOTS is the most agents a paired AgentBox may run here by default
/// (lib/shareSlots.ts maxShareSlots).
pub(super) const MAX_SLOTS: i64 = 64;

/// SLOTS are the limits a peer's own select offers; -1 is the default.
const SLOTS: [i64; 9] = [0, 1, 2, 3, 4, 6, 8, 12, 16];

/// ACTIVITY_PAGE is how many lines of a peer's activity show at first, and
/// MORE how many more each "Show more" adds.
const ACTIVITY_PAGE: usize = 8;
const MORE: usize = 20;

#[derive(Default)]
pub(super) struct Share {
    st: Option<Result<ShareStatus, String>>,
    pairing: Option<SharePairing>,
    peers_seen: Option<usize>,
    busy: bool,
    error: Option<String>,
    said: Option<String>,
    /// Each peer's activity, read once its row shows, and how much of it shows.
    activity: HashMap<String, Result<Vec<ShareActivity>, String>>,
    shown: HashMap<String, usize>,
    /// The name and default slots go into their fields on the next frame
    /// (share_sync), as they arrive.
    fill: bool,
}

/// parse_slots is the default slots as typed: a whole number from 0 to
/// MAX_SLOTS, or None for anything else, which saves nothing
/// (lib/shareSlots.ts parseSlots).
pub(super) fn parse_slots(text: &str) -> Option<i64> {
    let s = text.trim();
    if s.is_empty() || !s.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    s.parse::<i64>().ok().filter(|n| *n <= MAX_SLOTS)
}

/// activity_text says what a peer did, in the app's language.
fn activity_text(a: &ShareActivity) -> String {
    let agent = [("agent", (&a.agent).into())];
    match a.kind.as_str() {
        "create-agent" if a.detail == "chat" => tv("peers.did.create-agent-chat", &agent),
        "create-agent" | "start-agent" | "stop-agent" | "remove-agent" | "prompt" | "answer"
        | "review" | "open-desktop" | "open-terminal" => {
            tv(&format!("peers.did.{}", a.kind), &agent)
        }
        "message" => t("peers.did.message"),
        "merge" if !a.agent.is_empty() => tv("peers.did.merge", &agent),
        "merge" => tv("peers.did.merge-pr", &[("number", (&a.detail).into())]),
        _ => tv("peers.did.other", &[("kind", (&a.kind).into())]),
    }
}

/// chip is a project's toggle in a peer's row (ShareSettings' chip).
fn chip(id: SharedString, label: String, on: bool, busy: bool) -> Stateful<Div> {
    div()
        .id(id)
        .px_2p5()
        .py_0p5()
        .rounded_full()
        .border_1()
        .text_size(theme::text::CAPTION)
        .when(on, |el| {
            el.border_color(theme::brand(400).opacity(0.5))
                .bg(theme::brand(500).opacity(0.15))
                .text_color(theme::primary())
        })
        .when(!on, |el| {
            el.border_color(theme::line())
                .bg(theme::surface_raised())
                .text_color(theme::subtle())
                .hover(|s| s.text_color(theme::secondary()))
        })
        .when(busy, |el| el.opacity(0.6))
        .when(!busy, |el| el.cursor_pointer())
        .child(label)
}

/// caption is a peer row's small label before a control.
fn caption(text: String) -> Div {
    div()
        .flex_none()
        .text_size(theme::text::CAPTION)
        .text_color(theme::subtle())
        .child(text)
}

impl AppView {
    pub(super) fn share_groups(&self) -> Vec<Group> {
        vec![group(
            "share",
            "peers.title",
            vec![
                entry(
                    "share-peers",
                    "peers.peersLabel",
                    "peers.peersDescription",
                    "peers.peersKeywords",
                ),
                entry(
                    "share-join",
                    "peers.joinLabel",
                    "peers.joinDescription",
                    "peers.joinKeywords",
                ),
            ],
        )]
    }

    fn share_status(&self) -> Option<&ShareStatus> {
        self.settings_page
            .share
            .st
            .as_ref()
            .and_then(|r| r.as_ref().ok())
    }

    /// load_share reads the share status; a peer that just paired takes the
    /// link away and says so.
    pub(super) fn load_share(&mut self, cx: &mut Context<Self>) {
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client.get::<ShareStatus>("/v1/share").await;
            this.update(cx, |this, cx| {
                this.got_share(res.map_err(|e| e.to_string()));
                this.want_activity(cx);
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn got_share(&mut self, res: Result<ShareStatus, String>) {
        let s = &mut self.settings_page.share;
        if let Ok(st) = &res {
            let n = st.peers.len();
            if s.peers_seen.is_some_and(|seen| n > seen) && s.pairing.is_some() {
                s.pairing = None;
                if let Some(p) = st.peers.first() {
                    s.said = Some(tv("peers.pairedToast", &[("name", (&p.name).into())]));
                }
            }
            s.peers_seen = Some(n);
            let was = s.st.as_ref().and_then(|r| r.as_ref().ok());
            if was.is_none_or(|w| w.name != st.name || w.slots != st.slots) {
                s.fill = true;
            }
        }
        s.st = Some(res);
    }

    /// on_share_event takes the stream's share events: the status again
    /// while Settings shows it, and a peer's activity when it did something.
    pub(in crate::app) fn on_share_event(&mut self, peer: Option<String>, cx: &mut Context<Self>) {
        if !self.settings || self.settings_page.open != "phone" {
            return;
        }
        self.load_share(cx);
        if let Some(p) = peer {
            self.load_activity_of(p, cx);
        }
    }

    /// share_sync writes the name and the default slots into their fields
    /// once they're read, but not into the one being typed in.
    pub(in crate::app) fn share_sync(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        if !self.settings_page.share.fill {
            return;
        }
        let Some(st) = self.share_status().cloned() else {
            return;
        };
        self.settings_page.share.fill = false;
        for (name, value) in [
            ("share-name", st.name),
            ("share-slots", st.slots.to_string()),
        ] {
            let Some(input) = self.settings_page.fields.get(name) else {
                continue;
            };
            let focused = input.read(cx).focus_handle(cx).is_focused(window);
            if !focused && input.read(cx).value() != value {
                input.update(cx, |i, cx| i.set_value(value, window, cx));
            }
        }
    }

    /// share_commit saves the name or the default slots as they're left or
    /// Enter is pressed: a slots field emptied, or holding anything but 0 to
    /// 64, saves nothing and goes back to what is saved.
    pub(super) fn share_commit(&mut self, name: &str, cx: &mut Context<Self>) {
        let Some(st) = self.share_status() else {
            return;
        };
        let typed = self.field_value(name, cx);
        let req = match name {
            "share-name" if typed != st.name => UpdateShareRequest {
                name: Some(typed),
                ..Default::default()
            },
            "share-slots" => match parse_slots(&typed) {
                Some(n) if n != st.slots => UpdateShareRequest {
                    slots: Some(n),
                    ..Default::default()
                },
                _ => {
                    self.settings_page.share.fill = true;
                    cx.notify();
                    return;
                }
            },
            _ => return,
        };
        self.share_call(
            Method::PATCH,
            "/v1/share".into(),
            serde_json::to_vec(&req).ok(),
            None,
            cx,
        );
    }

    /// share_call sends one request, says what went wrong, and reads the
    /// status (and the projects, for a remote) again once it's done.
    fn share_call(
        &mut self,
        method: Method,
        path: String,
        body: Option<Vec<u8>>,
        said: Option<String>,
        cx: &mut Context<Self>,
    ) {
        self.settings_page.share.busy = true;
        let client = self.client.clone();
        let remotes = path.starts_with("/v1/share/remotes");
        cx.spawn(async move |this, cx| {
            let res = client.raw(method, &path, body).await;
            this.update(cx, |this, cx| {
                let s = &mut this.settings_page.share;
                s.busy = false;
                match res {
                    Ok(_) => {
                        s.error = None;
                        if said.is_some() {
                            s.said = said;
                        }
                        s.fill = true;
                    }
                    Err(e) => s.error = Some(e.to_string()),
                }
                this.load_share(cx);
                if remotes {
                    this.load_sidebar(cx);
                    this.reload_projects(cx);
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    fn pair_share(&mut self, cx: &mut Context<Self>) {
        self.settings_page.share.busy = true;
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client
                .raw(Method::POST, "/v1/share/pairings", None)
                .await
                .and_then(|b| Ok(serde_json::from_slice::<SharePairing>(&b)?));
            this.update(cx, |this, cx| {
                let s = &mut this.settings_page.share;
                s.busy = false;
                match res {
                    Ok(p) => {
                        s.pairing = Some(p);
                        s.error = None;
                    }
                    Err(e) => s.error = Some(format!("{} {e}", t("peers.pairFailed"))),
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    fn update_peer(&mut self, id: &str, req: UpdateSharePeerRequest, cx: &mut Context<Self>) {
        self.share_call(
            Method::PATCH,
            format!("/v1/share/peers/{}", enc(id)),
            serde_json::to_vec(&req).ok(),
            None,
            cx,
        );
    }

    /// join_share pastes another AgentBox's link.
    pub(super) fn join_share(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let link = self.field_value("share-link", cx);
        if link.is_empty() || self.settings_page.share.busy {
            return;
        }
        self.settings_page.share.busy = true;
        self.clear_field("share-link", window, cx);
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client
                .post::<_, ShareRemote>("/v1/share/remotes", &AddShareRemoteRequest { link })
                .await;
            this.update(cx, |this, cx| {
                let s = &mut this.settings_page.share;
                s.busy = false;
                match res {
                    Ok(r) => {
                        s.error = None;
                        s.said = Some(tv("peers.joinedToast", &[("name", (&r.name).into())]));
                    }
                    Err(e) => s.error = Some(e.to_string()),
                }
                this.load_share(cx);
                this.load_sidebar(cx);
                this.reload_projects(cx);
                cx.notify();
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    fn load_activity_of(&mut self, peer: String, cx: &mut Context<Self>) {
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client
                .get::<Vec<ShareActivity>>(&format!(
                    "/v1/share/peers/{}/activity?limit=100",
                    enc(&peer)
                ))
                .await;
            this.update(cx, |this, cx| {
                this.settings_page
                    .share
                    .activity
                    .insert(peer, res.map_err(|e| e.to_string()));
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    pub(super) fn share_entry(&self, e: &Entry, cx: &mut Context<Self>) -> Option<AnyElement> {
        match e.id {
            "share-peers" => Some(self.share_peers(e, cx)),
            "share-join" => Some(self.share_join(e, cx)),
            _ => None,
        }
    }

    fn share_peers(&self, e: &Entry, cx: &mut Context<Self>) -> AnyElement {
        let s = &self.settings_page.share;
        let mut row = self.more_row(e, None, cx);
        let Some(st) = self.share_status() else {
            if let Some(Err(err)) = &s.st {
                row.body.push(error_line(err.clone()));
            }
            return row.render();
        };
        row.body
            .push(note_in(t("peers.accountsWarning"), theme::amber(300)));
        row.body.push(
            h_flex()
                .gap_3()
                .items_start()
                .flex_wrap()
                .child(labeled(
                    t("peers.nameLabel"),
                    self.input_small("share-name"),
                ))
                .child(labeled(
                    t("peers.slotsLabel"),
                    v_flex()
                        .gap_1()
                        .child(self.input_small("share-slots"))
                        .child(note(t("peers.slotsDetails"))),
                ))
                .into_any_element(),
        );
        if !st.ready {
            let lan_on = self.settings_page.phone.lan_enabled();
            row.body.push(note(if lan_on {
                tv("peers.noAddress", &[("error", (&st.error).into())])
            } else {
                t("peers.phonesOff")
            }));
        }
        if let Some(err) = &s.error {
            row.body.push(error_line(err.clone()));
        }
        if let Some(said) = &s.said {
            row.body.push(note_in(said.clone(), theme::emerald(300)));
        }
        match &s.pairing {
            Some(p) => row.body.push(self.pairing_link(p, cx)),
            None => row.body.push(
                h_flex()
                    .child(button(
                        "share-pair",
                        t("peers.pair"),
                        Btn::Secondary,
                        !st.ready || s.busy,
                        cx.listener(|this, _: &ClickEvent, _, cx| this.pair_share(cx)),
                    ))
                    .into_any_element(),
            ),
        }
        if st.peers.is_empty() {
            row.body.push(note(t("peers.noPeers")));
        }
        for p in &st.peers {
            row.body.push(self.peer_row(p, st.slots, cx));
        }
        row.render()
    }

    fn pairing_link(&self, p: &SharePairing, cx: &mut Context<Self>) -> AnyElement {
        let copy = p.link.clone();
        v_flex()
            .id("share-link")
            .min_w_0()
            .gap_2()
            .px_3()
            .py_2p5()
            .rounded(theme::radius::XL)
            .border_1()
            .border_color(theme::line())
            .child(
                h_flex()
                    .min_w_0()
                    .gap_2()
                    .child(
                        crate::icons::ic("link-2")
                            .size(px(16.))
                            .flex_none()
                            .text_color(theme::subtle()),
                    )
                    .child(div().flex_1().min_w_0().child(mono(p.link.clone())))
                    .child(button(
                        "share-copy",
                        t("peers.copy"),
                        Btn::Secondary,
                        false,
                        cx.listener(move |this, _: &ClickEvent, _, cx| {
                            cx.write_to_clipboard(ClipboardItem::new_string(copy.clone()));
                            this.settings_page.share.said = Some(t("peers.copied"));
                            cx.notify();
                        }),
                    )),
            )
            .child(note(tv(
                "peers.linkNote",
                &[("time", (&local_date_time(&p.expires)).into())],
            )))
            .child(h_flex().child(button(
                "share-link-done",
                t("common.done"),
                Btn::Ghost,
                false,
                cx.listener(|this, _: &ClickEvent, _, cx| {
                    this.settings_page.share.pairing = None;
                    cx.notify();
                }),
            )))
            .into_any_element()
    }

    fn peer_row(&self, p: &SharePeer, default_slots: i64, cx: &mut Context<Self>) -> AnyElement {
        let s = &self.settings_page.share;
        let busy = s.busy;
        let limit = if p.slots < 0 { default_slots } else { p.slots };
        let mut facts = tv(
            "defaults.phone.pairedOn",
            &[("date", (&local_date(&p.paired)).into())],
        );
        if !p.last_seen.is_empty() {
            facts = format!(
                "{facts} · {}",
                tv(
                    "defaults.phone.lastSeen",
                    &[("when", (&local_date_time(&p.last_seen)).into())]
                )
            );
        }
        if !p.last_addr.is_empty() {
            facts = format!(
                "{facts} · {}",
                tv(
                    "defaults.phone.fromAddr",
                    &[("addr", (&p.last_addr).into())]
                )
            );
        }
        facts = format!(
            "{facts} · {}",
            tv(
                "peers.running",
                &[("running", p.running.into()), ("slots", limit.into())]
            )
        );
        let usage = tv(
            "peers.usage",
            &[
                (
                    "tokens",
                    (&super::defaults::format_tokens(p.usage.tokens)).into(),
                ),
                ("minutes", p.usage.agent_minutes.into()),
            ],
        );
        let id = p.id.clone();
        let shared = p.projects.clone();
        let local: Vec<(String, String)> = self
            .projects
            .iter()
            .filter(|x| x.remote.is_none())
            .map(|x| {
                let label = if x.display_name.is_empty() {
                    x.name.clone()
                } else {
                    x.display_name.clone()
                };
                (x.name.clone(), label)
            })
            .collect();
        let access = if p.access.is_empty() {
            "full".to_string()
        } else {
            p.access.clone()
        };
        let own = p.own_machine;
        let own_note = if !own {
            t("peers.ownMachineOff")
        } else if p.machine {
            tv("peers.ownMachineOn", &[("name", (&p.name).into())])
        } else {
            tv("peers.ownMachineAway", &[("name", (&p.name).into())])
        };
        let (unpair_id, unpair_name) = (p.id.clone(), p.name.clone());
        let all_id = p.id.clone();
        let all_on = p.all_projects;
        v_flex()
            .id(SharedString::from(format!("share-peer-{}", p.id)))
            .min_w_0()
            .gap_3()
            .px_3()
            .py_2p5()
            .rounded(theme::radius::XL)
            .border_1()
            .border_color(theme::line())
            // Its name, what's known of it, and Unpair.
            .child(
                h_flex()
                    .min_w_0()
                    .gap_3()
                    .child(
                        crate::icons::ic("monitor")
                            .size(px(16.))
                            .flex_none()
                            .text_color(theme::subtle()),
                    )
                    .child(
                        v_flex()
                            .flex_1()
                            .min_w_0()
                            .child(
                                div()
                                    .truncate()
                                    .text_size(theme::text::UI)
                                    .font_weight(FontWeight::MEDIUM)
                                    .text_color(theme::primary())
                                    .child(p.name.clone()),
                            )
                            .child(note(facts))
                            .child(note(usage)),
                    )
                    .child(button(
                        SharedString::from(format!("share-unpair-{}", p.id)),
                        t("defaults.phone.unpair"),
                        Btn::Ghost,
                        busy,
                        cx.listener(move |this, _: &ClickEvent, _, cx| {
                            this.share_call(
                                Method::DELETE,
                                format!("/v1/share/peers/{}", enc(&unpair_id)),
                                None,
                                Some(tv(
                                    "peers.unpairedToast",
                                    &[("name", (&unpair_name).into())],
                                )),
                                cx,
                            )
                        }),
                    )),
            )
            // The projects it reaches.
            .child(
                h_flex()
                    .pl_7()
                    .min_w_0()
                    .flex_wrap()
                    .gap_1p5()
                    .child(caption(t("peers.reaches")).mr_1())
                    .child(
                        chip(
                            SharedString::from(format!("share-all-{}", p.id)),
                            t("peers.allProjects"),
                            all_on,
                            busy,
                        )
                        .when(!busy, |el| {
                            el.on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                                this.update_peer(
                                    &all_id,
                                    UpdateSharePeerRequest {
                                        all_projects: Some(!all_on),
                                        ..Default::default()
                                    },
                                    cx,
                                )
                            }))
                        }),
                    )
                    .when(!all_on, |el| {
                        el.children(local.into_iter().map(|(name, label)| {
                            let on = shared.contains(&name);
                            let (id, shared) = (id.clone(), shared.clone());
                            chip(
                                SharedString::from(format!("share-project-{}-{name}", p.id)),
                                label,
                                on,
                                busy,
                            )
                            .when(!busy, |el| {
                                el.on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                                    let mut next: Vec<String> =
                                        shared.iter().filter(|x| **x != name).cloned().collect();
                                    if !on {
                                        next.push(name.clone());
                                    }
                                    this.update_peer(
                                        &id,
                                        UpdateSharePeerRequest {
                                            projects: Some(next),
                                            ..Default::default()
                                        },
                                        cx,
                                    )
                                }))
                            })
                        }))
                    }),
            )
            .when(!all_on && p.projects.is_empty(), |el| {
                el.child(div().pl_7().child(note(t("peers.nothingShared"))))
            })
            // What it may do, and how many agents it may run here.
            .child(
                h_flex()
                    .pl_7()
                    .min_w_0()
                    .gap_3()
                    .child(caption(t("peers.access")))
                    .child(div().w(px(256.)).child({
                        let id = p.id.clone();
                        super::super::controls::settings_select_at(
                            &format!("share-access-{}", p.id),
                            ["view", "chat", "full"]
                                .into_iter()
                                .map(|v| Choice::new(v, t(&format!("peers.access.{v}"))))
                                .collect(),
                            &access,
                            busy,
                            std::rc::Rc::new(cx.listener(move |this, v: &str, _, cx| {
                                this.update_peer(
                                    &id,
                                    UpdateSharePeerRequest {
                                        access: Some(v.to_string()),
                                        ..Default::default()
                                    },
                                    cx,
                                )
                            })),
                            Anchor::TopLeft,
                        )
                    })),
            )
            .child(
                h_flex()
                    .pl_7()
                    .min_w_0()
                    .gap_3()
                    .child(caption(t("peers.slots")))
                    .child(div().w(px(160.)).child({
                        let id = p.id.clone();
                        let mut choices = vec![Choice::new(
                            "-1",
                            tv("peers.slotsDefault", &[("n", default_slots.into())]),
                        )];
                        choices.extend(
                            SLOTS
                                .iter()
                                .map(|n| Choice::new(n.to_string(), n.to_string())),
                        );
                        super::super::controls::settings_select_at(
                            &format!("share-slots-{}", p.id),
                            choices,
                            &p.slots.to_string(),
                            busy,
                            std::rc::Rc::new(cx.listener(move |this, v: &str, _, cx| {
                                if let Ok(n) = v.parse::<i64>() {
                                    this.update_peer(
                                        &id,
                                        UpdateSharePeerRequest {
                                            slots: Some(n),
                                            ..Default::default()
                                        },
                                        cx,
                                    )
                                }
                            })),
                            Anchor::TopLeft,
                        )
                    })),
            )
            .child(self.peer_activity(p, cx))
            // Whether the agents its chat messages ask for may run on its
            // own PC (internal/daemon/sharemachines.go): its accounts, not
            // your slots.
            .child(
                h_flex()
                    .pl_7()
                    .min_w_0()
                    .gap_3()
                    .items_start()
                    .child(toggle(
                        SharedString::from(format!("share-own-machine-{}", p.id)),
                        own,
                        busy,
                        {
                            let id = p.id.clone();
                            cx.listener(move |this, _: &ClickEvent, _, cx| {
                                this.update_peer(
                                    &id,
                                    UpdateSharePeerRequest {
                                        own_machine: Some(!own),
                                        ..Default::default()
                                    },
                                    cx,
                                )
                            })
                        },
                    ))
                    .child(
                        v_flex()
                            .flex_1()
                            .min_w_0()
                            .gap_0p5()
                            .child(
                                div()
                                    .text_size(theme::text::CAPTION)
                                    .text_color(theme::secondary())
                                    .child(t("peers.ownMachine")),
                            )
                            .child(note(own_note)),
                    ),
            )
            .into_any_element()
    }

    /// peer_activity is what a paired AgentBox did here, the newest first,
    /// with when (internal/daemon/shareactivity.go).
    fn peer_activity(&self, p: &SharePeer, cx: &mut Context<Self>) -> AnyElement {
        let s = &self.settings_page.share;
        let limit = s.shown.get(&p.id).copied().unwrap_or(ACTIVITY_PAGE);
        let all: &[ShareActivity] = match s.activity.get(&p.id) {
            Some(Ok(a)) => a,
            _ => &[],
        };
        let more = all.len() > limit;
        let id = p.id.clone();
        v_flex()
            .pl_7()
            .min_w_0()
            .gap_1p5()
            .child(caption(t("peers.activity")))
            .map(|el| match s.activity.get(&p.id) {
                Some(Err(e)) => el.child(error_line(e.clone())),
                _ if all.is_empty() => el.child(
                    div()
                        .text_size(theme::text::CAPTION)
                        .text_color(theme::faint())
                        .child(t("peers.activityEmpty")),
                ),
                _ => el.child(
                    v_flex()
                        .min_w_0()
                        .gap_1()
                        .border_l_1()
                        .border_color(theme::line_faint())
                        .pl_3()
                        .children(all.iter().take(limit).map(|a| {
                            h_flex()
                                .min_w_0()
                                .items_start()
                                .gap_2()
                                .text_size(theme::text::CAPTION)
                                .child(
                                    div()
                                        .w(px(112.))
                                        .flex_none()
                                        .text_color(theme::faint())
                                        .child(local_date_time(&a.at)),
                                )
                                .child(
                                    div()
                                        .flex_1()
                                        .min_w_0()
                                        .text_color(theme::secondary())
                                        .child(if a.project.is_empty() {
                                            activity_text(a)
                                        } else {
                                            format!("{} · {}", activity_text(a), a.project)
                                        }),
                                )
                        })),
                ),
            })
            .when(more, |el| {
                el.child(h_flex().child(button(
                    SharedString::from(format!("share-activity-more-{}", p.id)),
                    t("peers.activityMore"),
                    Btn::Ghost,
                    false,
                    cx.listener(move |this, _: &ClickEvent, _, cx| {
                        let n = this
                            .settings_page
                            .share
                            .shown
                            .entry(id.clone())
                            .or_insert(ACTIVITY_PAGE);
                        *n += MORE;
                        cx.notify();
                    }),
                )))
            })
            .into_any_element()
    }

    fn share_join(&self, e: &Entry, cx: &mut Context<Self>) -> AnyElement {
        let s = &self.settings_page.share;
        let mut row = self.more_row(e, None, cx);
        let typed = !self.field_value("share-link", cx).is_empty();
        row.body.push(
            h_flex()
                .min_w_0()
                .gap_2()
                .child(
                    div()
                        .flex_1()
                        .min_w_0()
                        .child(self.input_small("share-link")),
                )
                .child(button(
                    "share-join",
                    t("peers.join"),
                    Btn::Primary,
                    !typed || s.busy,
                    cx.listener(|this, _: &ClickEvent, window, cx| this.join_share(window, cx)),
                ))
                .into_any_element(),
        );
        let remotes = self
            .share_status()
            .map(|st| st.remotes.as_slice())
            .unwrap_or_default();
        if remotes.is_empty() {
            row.body.push(note(t("peers.noRemotes")));
        }
        for r in remotes {
            row.body.push(self.remote_row(r, cx));
        }
        row.render()
    }

    fn remote_row(&self, r: &ShareRemote, cx: &mut Context<Self>) -> AnyElement {
        let busy = self.settings_page.share.busy;
        let id = r.id.clone();
        let machine = if r.machine {
            tv("peers.machineOn", &[("name", (&r.name).into())])
        } else {
            let why = if r.machine_error.is_empty() {
                t("peers.machineConnecting")
            } else {
                r.machine_error.clone()
            };
            tv(
                "peers.machineOff",
                &[("name", (&r.name).into()), ("why", (&why).into())],
            )
        };
        h_flex()
            .id(SharedString::from(format!("share-remote-{}", r.id)))
            .min_w_0()
            .gap_3()
            .px_3()
            .py_2p5()
            .rounded(theme::radius::XL)
            .border_1()
            .border_color(theme::line())
            .child(
                crate::icons::ic("monitor")
                    .size(px(16.))
                    .flex_none()
                    .text_color(theme::subtle()),
            )
            .child(
                v_flex()
                    .flex_1()
                    .min_w_0()
                    .child(
                        div()
                            .truncate()
                            .text_size(theme::text::UI)
                            .font_weight(FontWeight::MEDIUM)
                            .text_color(theme::primary())
                            .child(r.name.clone()),
                    )
                    .child(
                        h_flex()
                            .min_w_0()
                            .flex_wrap()
                            .gap_1()
                            .child(note(tv(
                                "peers.remoteProjects",
                                &[("count", r.projects.into())],
                            )))
                            .child(note("·"))
                            .child(mono(r.url.clone())),
                    )
                    .when(!r.error.is_empty(), |el| {
                        el.child(error_line(r.error.clone()))
                    })
                    .when(r.error.is_empty(), |el| {
                        el.child(if r.machine {
                            note(machine)
                        } else {
                            note_in(machine, theme::muted())
                        })
                    }),
            )
            .child(button(
                SharedString::from(format!("share-forget-{}", r.id)),
                t("peers.forget"),
                Btn::Ghost,
                busy,
                cx.listener(move |this, _: &ClickEvent, _, cx| {
                    this.share_call(
                        Method::DELETE,
                        format!("/v1/share/remotes/{}", enc(&id)),
                        None,
                        None,
                        cx,
                    )
                }),
            ))
            .into_any_element()
    }

    /// want_activity reads the activity of every peer shown that hasn't been
    /// read yet (on the section's poll).
    pub(super) fn want_activity(&mut self, cx: &mut Context<Self>) {
        let ids: Vec<String> = self
            .share_status()
            .map(|st| {
                st.peers
                    .iter()
                    .filter(|p| !self.settings_page.share.activity.contains_key(&p.id))
                    .map(|p| p.id.clone())
                    .collect()
            })
            .unwrap_or_default();
        for id in ids {
            self.settings_page
                .share
                .activity
                .insert(id.clone(), Ok(Vec::new()));
            self.load_activity_of(id, cx);
        }
    }
}

#[cfg(test)]
mod tests {
    use super::{activity_text, parse_slots};
    use agentbox_api::ShareActivity;

    #[test]
    fn default_slots_as_typed() {
        assert_eq!(parse_slots("3"), Some(3));
        assert_eq!(parse_slots(" 64 "), Some(64));
        assert_eq!(parse_slots("65"), None);
        assert_eq!(parse_slots(""), None);
        assert_eq!(parse_slots("-1"), None);
        assert_eq!(parse_slots("2.5"), None);
    }

    #[test]
    fn what_a_peer_did_reads_in_words() {
        let a = |kind: &str, agent: &str, detail: &str| ShareActivity {
            kind: kind.into(),
            agent: agent.into(),
            detail: detail.into(),
            ..Default::default()
        };
        assert_eq!(
            activity_text(&a("create-agent", "agent-3", "chat")),
            "had the chat make agent-3"
        );
        assert_eq!(
            activity_text(&a("create-agent", "agent-3", "")),
            "made agent-3"
        );
        assert_eq!(
            activity_text(&a("merge", "", "42")),
            "merged pull request #42"
        );
        assert_eq!(
            activity_text(&a("open-desktop", "agent-3", "")),
            "opened agent-3's desktop"
        );
        assert_eq!(activity_text(&a("teleport", "", "")), "did teleport");
    }
}
