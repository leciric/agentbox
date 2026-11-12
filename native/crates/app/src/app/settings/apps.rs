//! Settings → Use AgentBox in another app (AppsSettings.tsx): the outside
//! assistants (Claude, ChatGPT, Poke…) paired with this computer over remote
//! MCP, through the hub. The list is GET /v1/remote/mcp; a pairing is made by
//! a three-step dialog (which app, what it may do, how to connect it: POST
//! /v1/remote/mcp/pairings), given a new code (POST …/pairings/{id}/code,
//! which opens the dialog on its last step) and revoked (DELETE).
use std::collections::BTreeSet;
use std::rc::Rc;

use agentbox_api::client::Method;
use agentbox_api::{MCPPairRequest, MCPPairing, MCPPairingSecret, MCPPairings};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::{h_flex, v_flex};

use super::super::AppView;
use super::super::controls::{Btn, button, copy_button, error_line, note_in, window_modal};
use super::kit::{ask, badge, check, enc, label, local_date_time, note};
use super::{Entry, Group, entry, group};
use crate::theme;
use crate::toasts::{Kind, Toast};

const CLIENTS: [&str; 4] = ["claude", "chatgpt", "poke", "other"];
const ACCESS: [&str; 3] = ["read", "chat", "full"];
/// NAME is the wizard's name field (settings.rs's MORE_FIELDS).
pub(super) const NAME: &str = "apps-name";

#[derive(Default)]
pub(super) struct Apps {
    list: Option<Result<MCPPairings, String>>,
    busy: bool,
    /// revoking is the pairing the Revoke question is asked about.
    revoking: Option<MCPPairing>,
    wizard: Option<Wizard>,
}

/// Wizard is the pairing dialog: its step (1 to 3) and what was chosen.
struct Wizard {
    step: u8,
    client: String,
    access: String,
    /// only: "Only these" projects, not all of them.
    only: bool,
    picked: BTreeSet<String>,
    creating: bool,
    error: Option<String>,
    secret: Option<MCPPairingSecret>,
    /// fresh is a pairing being made, with its three steps; a new code for
    /// an old pairing shows the last one alone.
    fresh: bool,
}

/// known is a pairing's client as one of the four the catalog has words for.
fn known(client: &str) -> &'static str {
    CLIENTS
        .iter()
        .find(|c| **c == client)
        .copied()
        .unwrap_or("other")
}

/// wants_token is whether a client signs in with a token, not only OAuth.
fn wants_token(client: &str) -> bool {
    matches!(client, "poke" | "other")
}

/// unavailable is why apps can't reach this computer, in the user's words.
fn unavailable(list: &MCPPairings) -> Option<String> {
    if list.available {
        return None;
    }
    let hub = [("hub", (&list.hub).into())];
    Some(match list.reason.as_str() {
        "no-hub" => t("settings.apps.noHub"),
        "old-hub" => tv("settings.apps.oldHub", &hub),
        "offline" => tv("settings.apps.offline", &hub),
        _ if !list.error.is_empty() => list.error.clone(),
        _ => t("settings.apps.loadFailed"),
    })
}

/// can_pair is whether a new app can be paired: the hub is there and new
/// enough. An unreachable hub still lets the daemon say so itself.
fn can_pair(list: &MCPPairings) -> bool {
    list.available || !matches!(list.reason.as_str(), "no-hub" | "old-hub")
}

/// request is what the wizard asks the daemon for.
fn request(w: &Wizard, name: String) -> MCPPairRequest {
    MCPPairRequest {
        label: if name.is_empty() {
            t(&format!("settings.apps.client.{}", w.client))
        } else {
            name
        },
        client: w.client.clone(),
        access: w.access.clone(),
        projects: if w.only {
            w.picked.iter().cloned().collect()
        } else {
            Vec::new()
        },
        token: wants_token(&w.client),
    }
}

/// radio is a radio button's mark.
fn radio(on: bool) -> Div {
    div()
        .flex_none()
        .mt(px(2.))
        .size(px(14.))
        .rounded_full()
        .border_1()
        .border_color(if on {
            theme::brand(500)
        } else {
            theme::line_vivid()
        })
        .bg(theme::sunken())
        .flex()
        .items_center()
        .justify_center()
        .when(on, |el| {
            el.child(div().size(px(6.)).rounded_full().bg(theme::brand(500)))
        })
}

/// option is one of a few things to choose between, as a card: its name, a
/// line about it, and the brand's edge when it's the one chosen.
fn option(
    id: String,
    picked: bool,
    disabled: bool,
    mark: bool,
    name: String,
    about: String,
    on_click: impl Fn(&ClickEvent, &mut Window, &mut App) + 'static,
) -> Stateful<Div> {
    h_flex()
        .id(SharedString::from(id))
        .items_start()
        .gap_2p5()
        .px_4()
        .py_3()
        .rounded_xl()
        .border_1()
        .border_color(if picked {
            theme::brand(500).opacity(0.6)
        } else {
            theme::line()
        })
        .bg(if picked {
            theme::brand(500).opacity(0.1)
        } else {
            theme::surface_faint()
        })
        .when(disabled, |el| el.opacity(0.6))
        .when(!disabled, |el| {
            el.cursor_pointer()
                .when(!picked, |el| {
                    el.hover(|s| s.bg(theme::surface_raised()))
                        .active(|s| s.bg(theme::surface_strong()))
                })
                .on_click(on_click)
        })
        .when(mark, |el| el.child(radio(picked)))
        .child(
            v_flex()
                .flex_1()
                .min_w_0()
                .gap_0p5()
                .child(
                    div()
                        .text_size(theme::text::UI)
                        .font_weight(FontWeight::MEDIUM)
                        .text_color(theme::title())
                        .child(name),
                )
                .child(
                    div()
                        .min_w_0()
                        .text_size(theme::text::CAPTION)
                        .line_height(relative(1.5))
                        .text_color(theme::muted())
                        .child(about),
                ),
        )
}

/// numbered is a step's number in a disc: the wizard's place, and the
/// numbered things to do in the other app.
fn numbered(n: usize, lit: bool) -> Div {
    div()
        .flex_none()
        .size(px(20.))
        .rounded_full()
        .flex()
        .items_center()
        .justify_center()
        .text_size(px(11.))
        .font_weight(FontWeight::SEMIBOLD)
        .when(lit, |el| {
            el.bg(theme::brand(500)).text_color(theme::on_brand())
        })
        .when(!lit, |el| {
            el.bg(theme::surface_raised())
                .border_1()
                .border_color(theme::line())
                .text_color(theme::muted())
        })
        .child(n.to_string())
}

/// secret_box is something to copy into the other app, as it's typed: the
/// text wraps in its box however long it is, with Copy beside it.
fn secret_box(id: &'static str, text: &str, big: bool) -> AnyElement {
    h_flex()
        .w_full()
        .min_w_0()
        .items_start()
        .gap_2()
        .pl_3()
        .pr_1p5()
        .py_1p5()
        .rounded_lg()
        .border_1()
        .border_color(theme::line())
        .bg(theme::sunken())
        .child(
            div()
                .flex_1()
                .min_w_0()
                .overflow_hidden()
                .whitespace_normal()
                .font_family(theme::mono())
                .map(|el| {
                    if big {
                        el.py_0p5()
                            .text_size(theme::text::DISPLAY)
                            .font_weight(FontWeight::SEMIBOLD)
                            .text_color(theme::title())
                    } else {
                        el.py(px(3.))
                            .text_size(theme::text::CAPTION)
                            .line_height(relative(1.5))
                            .text_color(theme::secondary())
                    }
                })
                .child(text.to_string()),
        )
        .child(
            copy_button(
                SharedString::from(format!("apps-copy-{id}")),
                text.to_string(),
                t("settings.apps.wizard.copy"),
            )
            .when(big, |el| el.mt(px(5.)))
            .text_color(theme::muted()),
        )
        .into_any_element()
}

/// field is a part of the wizard: its name over what it holds.
fn field(name: &str, body: impl IntoElement) -> Div {
    v_flex()
        .w_full()
        .min_w_0()
        .gap_1p5()
        .child(label(t(name)))
        .child(body)
}

/// warning is the amber box: what pairing an app that can write risks.
fn warning(text: String) -> AnyElement {
    h_flex()
        .w_full()
        .min_w_0()
        .items_start()
        .gap_1p5()
        .px_2p5()
        .py_1p5()
        .rounded(px(6.))
        .border_1()
        .border_color(theme::amber(400).opacity(0.3))
        .bg(theme::amber(400).opacity(0.08))
        .text_size(theme::text::CAPTION)
        .line_height(relative(1.5))
        .text_color(theme::amber(300))
        .child(
            div()
                .flex_none()
                .pt(px(2.))
                .child(crate::icons::ic("triangle-alert").size(px(12.))),
        )
        .child(div().min_w_0().flex_1().child(text))
        .into_any_element()
}

impl AppView {
    pub(super) fn apps_groups(&self) -> Vec<Group> {
        // One group, under the section's own heading: it has no title.
        let mut apps = group(
            "apps",
            "settings.section.apps.title",
            vec![entry(
                "apps",
                "settings.apps.pairedLabel",
                "",
                "settings.apps.keywords",
            )],
        );
        apps.title.clear();
        vec![apps]
    }

    pub(super) fn load_apps(&mut self, cx: &mut Context<Self>) {
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client.get::<MCPPairings>("/v1/remote/mcp").await;
            this.update(cx, |this, cx| {
                this.settings_page.apps.list = Some(res.map_err(|e| e.to_string()));
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// project_title is a project as the app names it.
    fn project_title(&self, name: &str) -> String {
        self.projects
            .iter()
            .find(|p| p.name == name && !p.display_name.is_empty())
            .map_or_else(|| name.to_string(), |p| p.display_name.clone())
    }

    fn open_apps_wizard(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        self.clear_field(NAME, window, cx);
        self.settings_page.apps.wizard = Some(Wizard {
            step: 1,
            client: "claude".into(),
            access: "chat".into(),
            only: false,
            picked: BTreeSet::new(),
            creating: false,
            error: None,
            secret: None,
            fresh: true,
        });
        cx.notify();
    }

    /// close_apps_wizard shuts the dialog; the list is read again, since a
    /// pairing made or signed in to shows there.
    fn close_apps_wizard(&mut self, cx: &mut Context<Self>) {
        self.settings_page.apps.wizard = None;
        self.load_apps(cx);
        cx.notify();
    }

    /// apps_named puts the client's name in the name field, unless the user
    /// typed one of their own (another client's name isn't theirs).
    fn apps_named(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let Some(w) = &self.settings_page.apps.wizard else {
            return;
        };
        let name = t(&format!("settings.apps.client.{}", w.client));
        let typed = self.field_value(NAME, cx);
        let ours = typed.is_empty()
            || CLIENTS
                .iter()
                .any(|c| t(&format!("settings.apps.client.{c}")) == typed);
        if ours && let Some(i) = self.settings_page.fields.get(NAME) {
            i.update(cx, |i, cx| i.set_value(name, window, cx));
        }
    }

    /// apps_pair is the wizard's Pair (and Enter in its name).
    pub(super) fn apps_pair(&mut self, cx: &mut Context<Self>) {
        let name = self.field_value(NAME, cx);
        let Some(w) = &mut self.settings_page.apps.wizard else {
            return;
        };
        if w.step != 2 || w.creating || (w.only && w.picked.is_empty()) {
            return;
        }
        w.creating = true;
        w.error = None;
        let req = request(w, name);
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client
                .post::<_, MCPPairingSecret>("/v1/remote/mcp/pairings", &req)
                .await;
            this.update(cx, |this, cx| {
                // Closed while it waited: the pairing shows in the list, and
                // a new code signs it in.
                if let Some(w) = &mut this.settings_page.apps.wizard {
                    w.creating = false;
                    match res {
                        Ok(secret) => {
                            w.secret = Some(secret);
                            w.step = 3;
                        }
                        Err(e) => w.error = Some(e.to_string()),
                    }
                }
                this.load_apps(cx);
                cx.notify();
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    /// apps_new_code makes a pairing a new code and shows how to use it.
    fn apps_new_code(&mut self, pairing: MCPPairing, cx: &mut Context<Self>) {
        self.settings_page.apps.busy = true;
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let path = format!("/v1/remote/mcp/pairings/{}/code", enc(&pairing.id));
            let res = client
                .raw(Method::POST, &path, None)
                .await
                .and_then(|b| Ok(serde_json::from_slice::<MCPPairingSecret>(&b)?));
            this.update(cx, |this, cx| {
                let a = &mut this.settings_page.apps;
                a.busy = false;
                match res {
                    Ok(secret) => {
                        a.wizard = Some(Wizard {
                            step: 3,
                            client: known(&pairing.client).into(),
                            access: pairing.access.clone(),
                            only: false,
                            picked: BTreeSet::new(),
                            creating: false,
                            error: None,
                            secret: Some(secret),
                            fresh: false,
                        })
                    }
                    Err(e) => Toast::new(Kind::Error, t("settings.apps.wizard.codeFailed"))
                        .description(e.to_string())
                        .show(cx),
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    fn apps_revoke(&mut self, pairing: MCPPairing, cx: &mut Context<Self>) {
        self.settings_page.apps.busy = true;
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let path = format!("/v1/remote/mcp/pairings/{}", enc(&pairing.id));
            let res = client.raw(Method::DELETE, &path, None).await;
            this.update(cx, |this, cx| {
                let a = &mut this.settings_page.apps;
                a.busy = false;
                a.revoking = None;
                let name = [("name", (&pairing.label).into())];
                match res {
                    Ok(_) => Toast::new(Kind::Success, tv("settings.apps.revoked", &name)).show(cx),
                    Err(e) => Toast::new(Kind::Error, tv("settings.apps.revokeFailed", &name))
                        .description(e.to_string())
                        .show(cx),
                }
                this.load_apps(cx);
                cx.notify();
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    pub(super) fn apps_entry(&self, e: &Entry, cx: &mut Context<Self>) -> Option<AnyElement> {
        if e.id != "apps" {
            return None;
        }
        let a = &self.settings_page.apps;
        let list = a.list.as_ref().and_then(|r| r.as_ref().ok());
        let mut row = self.more_row(e, None, cx);
        row.control = Some(button(
            "apps-pair",
            t("settings.apps.pair"),
            Btn::Primary,
            a.busy || !list.is_some_and(can_pair),
            cx.listener(|this, _: &ClickEvent, window, cx| this.open_apps_wizard(window, cx)),
        ));
        if let Some(Err(err)) = &a.list {
            row.body.push(error_line(format!(
                "{}: {err}",
                t("settings.apps.loadFailed")
            )));
        }
        if let Some(list) = list {
            if let Some(why) = unavailable(list) {
                row.body.push(warning(why));
            }
            // With no hub, or one too old, there is nothing to list.
            if can_pair(list) || !list.pairings.is_empty() {
                if list.pairings.is_empty() {
                    row.body.push(note(t("settings.apps.empty")));
                }
                for p in &list.pairings {
                    row.body.push(self.apps_row(p, cx));
                }
            }
        }
        if let Some(p) = &a.revoking {
            let yes = p.clone();
            row.body.push(ask(
                "apps-revoke",
                tv("settings.apps.revokeTitle", &[("name", (&p.label).into())]),
                t("settings.apps.revokeBody"),
                t("settings.apps.revoke"),
                true,
                a.busy,
                cx.listener(move |this, _: &ClickEvent, _, cx| this.apps_revoke(yes.clone(), cx)),
                cx.listener(|this, _: &ClickEvent, _, cx| {
                    this.settings_page.apps.revoking = None;
                    cx.notify();
                }),
            ));
        }
        if let Some(w) = &a.wizard {
            row.body.push(self.apps_wizard(w, cx));
        }
        Some(row.render())
    }

    /// apps_row is one paired app: its name and what it may do, which
    /// projects, who signed in and when it was last used, and its two actions.
    fn apps_row(&self, p: &MCPPairing, cx: &mut Context<Self>) -> AnyElement {
        let a = &self.settings_page.apps;
        let projects = match p.projects.as_slice() {
            [] => t("settings.apps.allProjects"),
            few if few.len() <= 2 => few
                .iter()
                .map(|n| self.project_title(n))
                .collect::<Vec<_>>()
                .join(", "),
            many => tv("settings.apps.projectCount", &[("n", many.len().into())]),
        };
        let signed = if p.apps.is_empty() {
            t("settings.apps.notSignedIn")
        } else {
            tv(
                "settings.apps.signedIn",
                &[("apps", p.apps.join(", ").into())],
            )
        };
        let used = if p.last_used_at.is_empty() {
            t("settings.apps.neverUsed")
        } else {
            tv(
                "settings.apps.lastUsed",
                &[("when", local_date_time(&p.last_used_at).into())],
            )
        };
        let tone = match p.access.as_str() {
            "full" => theme::starting(),
            "chat" => theme::accent(),
            _ => theme::muted(),
        };
        let access = if ACCESS.contains(&p.access.as_str()) {
            t(&format!("settings.apps.access.{}", p.access))
        } else {
            p.access.clone()
        };
        let (again, gone) = (p.clone(), p.clone());
        h_flex()
            .id(SharedString::from(format!("app-{}", p.id)))
            .gap_3()
            .px_3()
            .py_2p5()
            .rounded_xl()
            .border_1()
            .border_color(theme::line())
            .child(
                div()
                    .flex_none()
                    .text_color(theme::subtle())
                    .child(crate::icons::ic("share-2").size(px(16.))),
            )
            .child(
                v_flex()
                    .flex_1()
                    .min_w_0()
                    .gap_0p5()
                    .child(
                        h_flex()
                            .min_w_0()
                            .gap_2()
                            .child(
                                div()
                                    .min_w_0()
                                    .text_size(theme::text::UI)
                                    .font_weight(FontWeight::MEDIUM)
                                    .text_color(theme::primary())
                                    .truncate()
                                    .child(p.label.clone()),
                            )
                            .child(badge(access, tone)),
                    )
                    .child(note(format!("{projects} · {signed} · {used}"))),
            )
            .child(
                div()
                    .id(SharedString::from(format!("app-code-tip-{}", p.id)))
                    .flex_none()
                    .tooltip(|w, cx| {
                        gpui_component::tooltip::Tooltip::new(t("settings.apps.newCodeHint"))
                            .build(w, cx)
                    })
                    .child(button(
                        SharedString::from(format!("app-code-{}", p.id)),
                        t("settings.apps.newCode"),
                        Btn::Ghost,
                        a.busy,
                        cx.listener(move |this, _: &ClickEvent, _, cx| {
                            this.apps_new_code(again.clone(), cx)
                        }),
                    )),
            )
            .child(button(
                SharedString::from(format!("app-revoke-{}", p.id)),
                t("settings.apps.revoke"),
                Btn::DangerQuiet,
                a.busy,
                cx.listener(move |this, _: &ClickEvent, _, cx| {
                    this.settings_page.apps.revoking = Some(gone.clone());
                    cx.notify();
                }),
            ))
            .into_any_element()
    }

    /// apps_wizard is the pairing dialog, over the window.
    fn apps_wizard(&self, w: &Wizard, cx: &mut Context<Self>) -> AnyElement {
        let titles = [
            "settings.apps.wizard.stepApp",
            "settings.apps.wizard.stepAccess",
            "settings.apps.wizard.stepConnect",
        ];
        let step = usize::from(w.step.clamp(1, 3));
        let head = v_flex()
            .flex_none()
            .gap_3()
            .px_6()
            .pt_6()
            .pb_4()
            .child(
                div()
                    .text_size(theme::text::HEADING)
                    .font_weight(FontWeight::SEMIBOLD)
                    .text_color(theme::title())
                    .child(t("settings.apps.wizard.title")),
            )
            .when(w.fresh, |el| {
                el.child(h_flex().flex_wrap().gap_x_3().gap_y_1p5().children(
                    titles.iter().enumerate().map(|(i, key)| {
                        let n = i + 1;
                        h_flex().gap_1p5().child(numbered(n, n <= step)).child(
                            div()
                                .text_size(theme::text::CAPTION)
                                .font_weight(FontWeight::MEDIUM)
                                .text_color(if n == step {
                                    theme::primary()
                                } else {
                                    theme::subtle()
                                })
                                .child(t(key)),
                        )
                    }),
                ))
            })
            .when(!w.fresh, |el| {
                let name = w
                    .secret
                    .as_ref()
                    .map(|s| s.pairing.label.clone())
                    .unwrap_or_default();
                el.child(
                    div()
                        .min_w_0()
                        .truncate()
                        .text_size(theme::text::UI)
                        .text_color(theme::muted())
                        .child(format!("{} · {name}", t(titles[2]))),
                )
            });
        let body = match step {
            1 => self.apps_step_app(w, cx),
            2 => self.apps_step_access(w, cx),
            _ => self.apps_step_connect(w),
        };
        let none_picked = w.only && w.picked.is_empty();
        let foot = h_flex()
            .flex_none()
            .gap_2()
            .justify_end()
            .px_6()
            .py_4()
            .border_t_1()
            .border_color(theme::line_faint())
            .map(|el| match step {
                1 => el
                    .child(button(
                        "apps-cancel",
                        t("common.cancel"),
                        Btn::Ghost,
                        false,
                        cx.listener(|this, _: &ClickEvent, _, cx| this.close_apps_wizard(cx)),
                    ))
                    .child(button(
                        "apps-next",
                        t("settings.apps.wizard.next"),
                        Btn::Primary,
                        false,
                        cx.listener(|this, _: &ClickEvent, window, cx| {
                            this.apps_named(window, cx);
                            if let Some(w) = &mut this.settings_page.apps.wizard {
                                w.step = 2;
                            }
                            cx.notify();
                        }),
                    )),
                2 => el
                    .child(button(
                        "apps-back",
                        t("settings.apps.wizard.back"),
                        Btn::Ghost,
                        w.creating,
                        cx.listener(|this, _: &ClickEvent, _, cx| {
                            if let Some(w) = &mut this.settings_page.apps.wizard {
                                w.step = 1;
                                w.error = None;
                            }
                            cx.notify();
                        }),
                    ))
                    .child(button(
                        "apps-create",
                        if w.creating {
                            t("settings.apps.wizard.creating")
                        } else {
                            t("settings.apps.wizard.create")
                        },
                        Btn::Primary,
                        w.creating || none_picked,
                        cx.listener(|this, _: &ClickEvent, _, cx| this.apps_pair(cx)),
                    )),
                _ => el.child(button(
                    "apps-done",
                    t("settings.apps.wizard.done"),
                    Btn::Primary,
                    false,
                    cx.listener(|this, _: &ClickEvent, _, cx| this.close_apps_wizard(cx)),
                )),
            });
        // A click beside the dialog closes it, except on its last step: the
        // token there is shown only once, and Done is the way out.
        let last = step == 3;
        let beside = cx.listener(move |this, _: &ClickEvent, _, cx| {
            let creating = this
                .settings_page
                .apps
                .wizard
                .as_ref()
                .is_some_and(|w| w.creating);
            if !last && !creating {
                this.close_apps_wizard(cx);
            }
        });
        window_modal(
            "apps-wizard",
            px(540.),
            v_flex()
                .flex_1()
                .min_h_0()
                .min_w_0()
                .bg(theme::modal())
                .child(head)
                .child(
                    div()
                        .id("apps-wizard-body")
                        .flex_1()
                        .min_h_0()
                        .min_w_0()
                        .overflow_y_scroll()
                        .px_6()
                        .pb_5()
                        .child(body),
                )
                .child(foot),
            Rc::new(beside),
        )
    }

    /// Step 1, "Which app?": the four kinds of app, two to a line.
    fn apps_step_app(&self, w: &Wizard, cx: &mut Context<Self>) -> AnyElement {
        let card = |c: &'static str, cx: &mut Context<Self>| {
            option(
                format!("apps-client-{c}"),
                w.client == c,
                false,
                false,
                t(&format!("settings.apps.client.{c}")),
                t(&format!("settings.apps.client.{c}.description")),
                cx.listener(move |this, _: &ClickEvent, _, cx| {
                    if let Some(w) = &mut this.settings_page.apps.wizard {
                        w.client = c.into();
                    }
                    cx.notify();
                }),
            )
            .flex_1()
            .min_w_0()
        };
        v_flex()
            .gap_2()
            .children(CLIENTS.chunks(2).map(|pair| {
                h_flex()
                    .items_stretch()
                    .gap_2()
                    .children(pair.iter().map(|c| card(c, cx)).collect::<Vec<_>>())
            }))
            .into_any_element()
    }

    /// Step 2, "What may it do?": its name, its access and its projects.
    fn apps_step_access(&self, w: &Wizard, cx: &mut Context<Self>) -> AnyElement {
        let scope = |only: bool, key: &str, cx: &mut Context<Self>| {
            h_flex()
                .id(SharedString::from(format!("apps-projects-{only}")))
                .items_start()
                .gap_2()
                .text_size(theme::text::UI)
                .text_color(theme::secondary())
                .when(w.creating, |el| el.opacity(0.6))
                .when(!w.creating, |el| {
                    el.cursor_pointer()
                        .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                            if let Some(w) = &mut this.settings_page.apps.wizard {
                                w.only = only;
                            }
                            cx.notify();
                        }))
                })
                .child(radio(w.only == only))
                .child(div().flex_1().min_w_0().child(t(key)))
        };
        v_flex()
            .gap_5()
            .child(field(
                "settings.apps.wizard.nameLabel",
                self.input_small(NAME),
            ))
            .child(field(
                "settings.apps.wizard.accessLabel",
                v_flex()
                    .gap_2()
                    .children(ACCESS.iter().map(|level| {
                        let level = *level;
                        option(
                            format!("apps-access-{level}"),
                            w.access == level,
                            w.creating,
                            true,
                            t(&format!("settings.apps.access.{level}")),
                            t(&format!("settings.apps.access.{level}.description")),
                            cx.listener(move |this, _: &ClickEvent, _, cx| {
                                if let Some(w) = &mut this.settings_page.apps.wizard {
                                    w.access = level.into();
                                }
                                cx.notify();
                            }),
                        )
                    }))
                    .when(w.access != "read", |el| {
                        el.child(warning(t("settings.apps.wizard.warning")))
                    }),
            ))
            .child(field(
                "settings.apps.wizard.projectsLabel",
                v_flex()
                    .gap_2()
                    .child(scope(false, "settings.apps.wizard.projectsAll", cx))
                    .child(scope(true, "settings.apps.wizard.projectsSome", cx))
                    .when(w.only, |el| {
                        el.child(
                            v_flex()
                                .ml(px(22.))
                                .gap_2()
                                .px_3()
                                .py_2p5()
                                .rounded_lg()
                                .border_1()
                                .border_color(theme::line())
                                .bg(theme::surface_faint())
                                .children(self.projects.iter().map(|p| {
                                    let name = p.name.clone();
                                    check(
                                        SharedString::from(format!("apps-project-{}", p.name)),
                                        w.picked.contains(&p.name),
                                        w.creating,
                                        div().truncate().child(self.project_title(&p.name)),
                                        cx.listener(move |this, _: &ClickEvent, _, cx| {
                                            if let Some(w) = &mut this.settings_page.apps.wizard
                                                && !w.picked.remove(&name)
                                            {
                                                w.picked.insert(name.clone());
                                            }
                                            cx.notify();
                                        }),
                                    )
                                }))
                                .when(w.picked.is_empty(), |el| {
                                    el.child(note_in(
                                        t("settings.apps.wizard.projectsNone"),
                                        theme::amber(300),
                                    ))
                                }),
                        )
                    }),
            ))
            .when_some(w.error.as_ref(), |el, e| {
                el.child(error_line(format!(
                    "{}: {e}",
                    t("settings.apps.wizard.failed")
                )))
            })
            .into_any_element()
    }

    /// Step 3, "Connect it": the address, the code, the token when there is
    /// one, and what to do with them in that app.
    fn apps_step_connect(&self, w: &Wizard) -> AnyElement {
        let Some(s) = &w.secret else {
            return div().into_any_element();
        };
        v_flex()
            .gap_5()
            .child(field(
                "settings.apps.wizard.urlLabel",
                secret_box("url", &s.url, false),
            ))
            .child(
                field(
                    "settings.apps.wizard.codeLabel",
                    secret_box("code", &s.code, true),
                )
                .child(note(t("settings.apps.wizard.codeHint"))),
            )
            .when(!s.token.is_empty(), |el| {
                el.child(
                    field(
                        "settings.apps.wizard.tokenLabel",
                        secret_box("token", &s.token, false),
                    )
                    .child(note_in(
                        t("settings.apps.wizard.tokenHint"),
                        theme::amber(300),
                    )),
                )
            })
            .child(
                v_flex()
                    .gap_2p5()
                    .pt_4()
                    .border_t_1()
                    .border_color(theme::line_faint())
                    .children((1..=3usize).map(|n| {
                        h_flex()
                            .items_start()
                            .gap_2p5()
                            .child(numbered(n, false))
                            .child(
                                div()
                                    .flex_1()
                                    .min_w_0()
                                    .text_size(theme::text::UI)
                                    .line_height(relative(1.5))
                                    .text_color(theme::secondary())
                                    .child(t(&format!("settings.apps.steps.{}.{n}", w.client))),
                            )
                    })),
            )
            .into_any_element()
    }
}

#[cfg(test)]
mod tests {
    // Not `super::*`: gpui's own `test` would stand in for #[test].
    use super::{ACCESS, BTreeSet, CLIENTS, MCPPairings, Wizard};
    use super::{can_pair, known, request, unavailable};

    fn wizard(client: &str) -> Wizard {
        Wizard {
            step: 2,
            client: client.into(),
            access: "chat".into(),
            only: false,
            picked: BTreeSet::from(["demo".to_string()]),
            creating: false,
            error: None,
            secret: None,
            fresh: true,
        }
    }

    #[test]
    fn a_token_is_asked_for_poke_and_other_apps_only() {
        for (client, token) in [
            ("claude", false),
            ("chatgpt", false),
            ("poke", true),
            ("other", true),
        ] {
            assert_eq!(
                request(&wizard(client), "Mine".into()).token,
                token,
                "{client}"
            );
        }
    }

    #[test]
    fn all_projects_sends_none_and_only_these_sends_the_ticked() {
        let mut w = wizard("claude");
        assert!(request(&w, "Mine".into()).projects.is_empty());
        w.only = true;
        assert_eq!(request(&w, "Mine".into()).projects, ["demo"]);
    }

    #[test]
    fn a_pairing_left_unnamed_takes_its_apps_name() {
        assert_eq!(request(&wizard("chatgpt"), String::new()).label, "ChatGPT");
        assert_eq!(request(&wizard("poke"), "Phone".into()).label, "Phone");
    }

    #[test]
    fn an_unknown_client_reads_as_another_app() {
        assert_eq!(known("poke"), "poke");
        assert_eq!(known("cursor"), "other");
    }

    #[test]
    fn pairing_is_off_without_a_hub_new_enough() {
        let list = |available: bool, reason: &str| MCPPairings {
            available,
            reason: reason.into(),
            hub: "hub.example.com".into(),
            ..Default::default()
        };
        assert!(can_pair(&list(true, "")));
        assert!(!can_pair(&list(false, "no-hub")));
        assert!(!can_pair(&list(false, "old-hub")));
        assert!(can_pair(&list(false, "offline")));
        assert!(unavailable(&list(true, "")).is_none());
        assert!(
            unavailable(&list(false, "offline"))
                .unwrap()
                .contains("hub.example.com")
        );
    }

    #[test]
    fn every_client_and_level_has_its_words() {
        for c in CLIENTS {
            for key in [
                format!("settings.apps.client.{c}"),
                format!("settings.apps.client.{c}.description"),
                format!("settings.apps.steps.{c}.1"),
                format!("settings.apps.steps.{c}.2"),
                format!("settings.apps.steps.{c}.3"),
            ] {
                assert!(agentbox_i18n::has(&key), "{key}");
            }
        }
        for a in ACCESS {
            assert!(agentbox_i18n::has(&format!("settings.apps.access.{a}")));
            assert!(agentbox_i18n::has(&format!(
                "settings.apps.access.{a}.description"
            )));
        }
    }
}
