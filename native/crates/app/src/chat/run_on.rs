//! Where the agents a shared project's chat makes for you run (#821; the
//! Electron app's RunOnChip.tsx and lib/runOn.ts): beside the composer's To:
//! while the message goes to the lead of a project another AgentBox shares
//! with this one. On the owner's PC, on their AI accounts and within the slots
//! they gave you; or on yours, cloned with your GitHub access and on your AI
//! accounts (internal/daemon/sharemachines.go). "On my PC" is greyed out with
//! the reason when this computer can't (ShareRunOn.why).
//!
//! Each chat remembers its own choice on this computer, by its ref, and sends
//! it with every message (ChatMessageRequest.runOn), so the owner's lead hears
//! it each time it changes; a choice this computer can't honour now reads, and
//! is sent, as the owner's.
use std::collections::BTreeMap;
use std::time::{Duration, Instant};

use agentbox_api::ShareRunOn;
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::button::{Button, ButtonVariants as _};
use gpui_component::menu::{DropdownMenu as _, PopupMenuItem};
use gpui_component::{h_flex, v_flex};

use super::ChatView;
use crate::theme;

/// KEY is where this computer keeps each chat's choice (prefs.rs), as the
/// Electron app keeps agentbox.composer.runOn: only "peer" is an entry.
const KEY: &str = "composer.runOn";

/// How often the chip asks again whether this computer can take agents.
const EVERY: Duration = Duration::from_secs(30);

/// chosen is what a chat chose: "peer" (this computer) or "owner".
pub fn chosen(r: &str) -> &'static str {
    let kept: BTreeMap<String, String> = crate::prefs::get(KEY)
        .and_then(|v| serde_json::from_value(v).ok())
        .unwrap_or_default();
    if kept.get(r).map(String::as_str) == Some("peer") {
        "peer"
    } else {
        "owner"
    }
}

/// choose keeps a chat's choice; the owner's is no entry.
pub fn choose(r: &str, run_on: &str) {
    let mut kept: BTreeMap<String, String> = crate::prefs::get(KEY)
        .and_then(|v| serde_json::from_value(v).ok())
        .unwrap_or_default();
    if run_on == "peer" {
        kept.insert(r.to_string(), "peer".into());
    } else {
        kept.remove(r);
    }
    crate::prefs::set(KEY, serde_json::to_value(&kept).unwrap_or_default());
}

/// sent is what a message carries: this computer only while it can take the
/// agents, whatever was chosen, so a choice it can no longer honour never
/// reaches the owner's lead (lib/runOn.ts sentRunOn).
pub fn sent(chosen: &str, here: bool) -> &'static str {
    if chosen == "peer" && here {
        "peer"
    } else {
        "owner"
    }
}

impl ChatView {
    /// run_on_shown is whether the chip shows: a shared project's lead chat,
    /// with the message going to the lead.
    fn run_on_shown(&self) -> bool {
        self.shared_by.is_some() && self.is_lead() && self.composer.target.is_none()
    }

    /// run_on_sent is what the next message to the lead carries ("" for a
    /// project this AgentBox owns).
    pub(super) fn run_on_sent(&self) -> String {
        if self.shared_by.is_none() || !self.is_lead() {
            return String::new();
        }
        let here = self.run_on.as_ref().is_some_and(|r| r.here);
        sent(chosen(&self.agent), here).to_string()
    }

    /// load_run_on asks whether this computer can take the agents, every
    /// EVERY while the chip shows.
    fn load_run_on(&mut self, cx: &mut Context<Self>) {
        if self.run_on_asked.is_some_and(|at| at.elapsed() < EVERY) {
            return;
        }
        self.run_on_asked = Some(Instant::now());
        let Some(project) = self.agent.split_once('/').map(|(p, _)| p.to_string()) else {
            return;
        };
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let path = format!("/v1/projects/{}/run-on", crate::files::encode(&project));
            let res = client.get::<ShareRunOn>(&path).await;
            this.update(cx, |this, cx| {
                this.run_on = Some(res.unwrap_or_else(|e| ShareRunOn {
                    why: e.to_string(),
                    ..Default::default()
                }));
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// render_run_on is the chip, and its menu of the two places.
    pub(super) fn render_run_on(&mut self, cx: &mut Context<Self>) -> Option<AnyElement> {
        if !self.run_on_shown() {
            return None;
        }
        self.load_run_on(cx);
        let owner = self.shared_by.clone().unwrap_or_default();
        let info = self.run_on.clone();
        let here = info.as_ref().is_some_and(|r| r.here);
        let shown = sent(chosen(&self.agent), here);
        let label = if shown == "peer" {
            t("chat.runOn.mine")
        } else {
            tv("chat.runOn.owner", &[("owner", (&owner).into())])
        };
        let mine_hint = if here {
            t("chat.runOn.mineHint")
        } else {
            match &info {
                Some(i) if !i.why.is_empty() => i.why.clone(),
                Some(_) => t("chat.runOn.unavailable"),
                None => t("chat.runOn.checking"),
            }
        };
        let view = cx.entity().downgrade();
        let r = self.agent.clone();
        let mine = shown == "peer";
        Some(
            // RunOnChip: h-8 max-w-48 rounded-full px-2.5, 12px; the brand's
            // wash while it says this computer.
            Button::new("chat-run-on")
                .ghost()
                .compact()
                .h_8()
                .max_w(px(192.))
                .px(px(10.))
                .gap_1()
                .rounded(gpui_component::button::ButtonRounded::Size(px(999.)))
                .text_size(theme::text::CAPTION)
                .text_color(if mine {
                    theme::brand(200)
                } else {
                    theme::subtle()
                })
                .when(mine, |b| b.bg(theme::brand(500).opacity(0.15)))
                .child(
                    h_flex()
                        .min_w_0()
                        .gap_1()
                        .text_size(theme::text::CAPTION)
                        .line_height(px(18.))
                        .child(
                            crate::icons::ic(if mine { "laptop" } else { "server" })
                                .size(px(14.))
                                .flex_none(),
                        )
                        .child(
                            div()
                                .min_w_0()
                                .truncate()
                                .font_weight(FontWeight::MEDIUM)
                                .child(label.clone()),
                        )
                        .child(
                            div()
                                .flex_none()
                                .opacity(0.7)
                                .child(crate::icons::ic("chevron-down").size(px(12.))),
                        ),
                )
                .tooltip(tv("chat.runOn.tip", &[("owner", (&owner).into())]))
                .dropdown_menu_with_anchor(Anchor::BottomLeft, move |menu, _, _| {
                    let option = |value: &'static str, title: String, hint: String| {
                        let on = shown == value;
                        PopupMenuItem::element(move |_, _| {
                            h_flex()
                                .w(px(296.))
                                .items_start()
                                .gap_2p5()
                                .py_0p5()
                                .child(
                                    crate::icons::ic(if value == "peer" {
                                        "laptop"
                                    } else {
                                        "server"
                                    })
                                    .size(px(14.))
                                    .mt_0p5()
                                    .flex_none()
                                    .text_color(theme::subtle()),
                                )
                                .child(
                                    v_flex()
                                        .flex_1()
                                        .min_w_0()
                                        .gap_0p5()
                                        .child(
                                            div()
                                                .text_size(theme::text::UI)
                                                .font_weight(FontWeight::MEDIUM)
                                                .text_color(theme::primary())
                                                .child(title.clone()),
                                        )
                                        .child(
                                            div()
                                                .whitespace_normal()
                                                .text_size(theme::text::CAPTION)
                                                .text_color(theme::subtle())
                                                .child(hint.clone()),
                                        ),
                                )
                                .child(
                                    div()
                                        .w(px(14.))
                                        .flex_none()
                                        .mt_0p5()
                                        .text_color(theme::primary())
                                        .child(crate::icons::of(if on { "✓" } else { "" })),
                                )
                        })
                    };
                    let pick = |value: &'static str| {
                        let (view, r) = (view.clone(), r.clone());
                        move |_: &ClickEvent, window: &mut Window, cx: &mut App| {
                            choose(&r, value);
                            view.update(cx, |this, cx| {
                                this.focus_composer(window, cx);
                                cx.notify();
                            })
                            .ok();
                        }
                    };
                    menu.min_w(px(320.))
                        .label(t("chat.runOn.heading"))
                        .item(
                            option(
                                "owner",
                                tv("chat.runOn.owner", &[("owner", (&owner).into())]),
                                tv("chat.runOn.ownerHint", &[("owner", (&owner).into())]),
                            )
                            .on_click(pick("owner")),
                        )
                        .item(
                            option("peer", t("chat.runOn.mine"), mine_hint.clone())
                                .disabled(!here)
                                .on_click(pick("peer")),
                        )
                })
                .into_any_element(),
        )
    }
}

#[cfg(test)]
mod tests {
    use super::sent;

    #[test]
    fn a_message_asks_for_this_computer_only_while_it_can_take_agents() {
        assert_eq!(sent("peer", true), "peer");
        assert_eq!(sent("peer", false), "owner");
        assert_eq!(sent("owner", true), "owner");
    }
}
