//! An agent's Settings over its chat (AgentView.tsx's settings tab, opened
//! from its ⋯): its Machine, Code and AI tool (OverviewTab.tsx), the secrets
//! only it gets (SecretsTab.tsx with an agent's ref: GET/PUT/DELETE
//! /v1/agents/{p}/{a}/secrets) beside the project's it inherits, and its own
//! connectors (ConnectorsTab.tsx for an agent: …/connectors, connect,
//! disconnect, remove). The catalog to add a connector from is Settings'.
use agentbox_api::client::Method;
use agentbox_api::{Connector, FleetAgent, Secret, SetSecretRequest};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::input::{Input, InputState};
use gpui_component::{h_flex, v_flex};

use super::AppView;
use super::agent::ai_label;
use super::controls::{self, Btn};
use super::settings::defaults::time_ago;
use crate::theme;
use crate::toasts;

pub const SECTIONS: [&str; 5] = ["machine", "code", "ai", "secrets", "connectors"];

pub struct AgentSettings {
    pub section: &'static str,
    r#ref: String,
    diff: Option<String>,
    secrets: Option<Result<Vec<Secret>, String>>,
    connectors: Option<Result<Vec<Connector>, String>>,
    name: Entity<InputState>,
    value: Entity<InputState>,
    busy: bool,
}

impl AgentSettings {
    pub fn new(window: &mut Window, cx: &mut Context<AppView>) -> Self {
        Self {
            section: "machine",
            r#ref: String::new(),
            diff: None,
            secrets: None,
            connectors: None,
            name: cx.new(|cx| InputState::new(window, cx).placeholder("OPENAI_API_KEY")),
            value: cx.new(|cx| {
                InputState::new(window, cx)
                    .masked(true)
                    .placeholder(t("agent.secrets.valuePlaceholder"))
            }),
            busy: false,
        }
    }
}

fn strip_code(s: &str) -> String {
    s.replace("<code>", "").replace("</code>", "")
}

impl AppView {
    /// open_agent_settings shows the open agent's Settings over its chat.
    pub(super) fn open_agent_settings(&mut self, cx: &mut Context<Self>) {
        self.agent.page = Some(super::agent::Page::Settings);
        let Some(a) = self.open_agent().cloned() else {
            return;
        };
        if self.agent_settings.r#ref != a.r#ref {
            self.agent_settings.r#ref = a.r#ref.clone();
            self.agent_settings.diff = None;
            self.agent_settings.secrets = None;
            self.agent_settings.connectors = None;
        }
        self.load_agent_settings(cx);
        cx.notify();
    }

    fn load_agent_settings(&mut self, cx: &mut Context<Self>) {
        let r = self.agent_settings.r#ref.clone();
        let (p, a) = super::agent::split(&r);
        let base = format!("/v1/agents/{p}/{a}");
        let client = self.client.clone();
        let section = self.agent_settings.section;
        cx.spawn(async move |this, cx| {
            let diff = if section == "code" {
                client
                    .raw(Method::GET, &format!("{base}/diff?stat=true"), None)
                    .await
                    .ok()
                    .map(|b| String::from_utf8_lossy(&b).into_owned())
            } else {
                None
            };
            let secrets = if section == "secrets" {
                Some(
                    client
                        .get::<Vec<Secret>>(&format!("{base}/secrets"))
                        .await
                        .map_err(|e| e.to_string()),
                )
            } else {
                None
            };
            let connectors = if section == "connectors" {
                Some(
                    client
                        .get::<Vec<Connector>>(&format!("{base}/connectors"))
                        .await
                        .map_err(|e| e.to_string()),
                )
            } else {
                None
            };
            this.update(cx, |this, cx| {
                let s = &mut this.agent_settings;
                if s.r#ref != r {
                    return;
                }
                if diff.is_some() {
                    s.diff = diff;
                }
                if secrets.is_some() {
                    s.secrets = secrets;
                }
                if connectors.is_some() {
                    s.connectors = connectors;
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn agent_call(
        &mut self,
        method: Method,
        path: String,
        body: Option<Vec<u8>>,
        ok: String,
        cx: &mut Context<Self>,
    ) {
        let (p, a) = super::agent::split(&self.agent_settings.r#ref);
        let url = format!("/v1/agents/{p}/{a}{path}");
        let client = self.client.clone();
        self.agent_settings.busy = true;
        cx.spawn(async move |this, cx| {
            let res = client.raw(method, &url, body).await;
            this.update(cx, |this, cx| {
                this.agent_settings.busy = false;
                match res {
                    Ok(_) => {
                        if !ok.is_empty() {
                            toasts::info(cx, ok);
                        }
                    }
                    Err(e) => toasts::error(cx, e.to_string()),
                }
                this.load_agent_settings(cx);
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    fn store_secret(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let name = self.agent_settings.name.read(cx).value().trim().to_string();
        let value = self.agent_settings.value.read(cx).value().to_string();
        if name.is_empty() || value.is_empty() {
            return;
        }
        let body = serde_json::to_vec(&SetSecretRequest {
            value,
            ..Default::default()
        })
        .unwrap_or_default();
        self.agent_settings
            .value
            .update(cx, |s, cx| s.set_value("", window, cx));
        self.agent_settings
            .name
            .update(cx, |s, cx| s.set_value("", window, cx));
        self.agent_call(
            Method::PUT,
            format!("/secrets/{}", crate::files::encode(&name)),
            Some(body),
            tv("agent.secrets.stored", &[("name", (&name).into())]),
            cx,
        );
    }

    /// render_agent_settings is the Settings page over the agent's chat.
    pub(super) fn render_agent_settings(&self, cx: &mut Context<Self>) -> AnyElement {
        let Some(a) = self.open_agent().cloned() else {
            return div().into_any_element();
        };
        let s = &self.agent_settings;
        let name = if a.title.is_empty() {
            a.name.clone()
        } else {
            a.title.clone()
        };
        let nav = v_flex()
            .w(px(192.))
            .flex_none()
            .h_full()
            .px_3()
            .py_4()
            .gap_0p5()
            .border_r_1()
            .border_color(theme::line_faint())
            .child(
                div()
                    .px_2p5()
                    .pb_1()
                    .text_size(theme::text::CAPTION)
                    .font_weight(FontWeight::MEDIUM)
                    .text_color(theme::muted())
                    .child(tv("agent.view.settingsOf", &[("name", (&name).into())])),
            )
            .children(SECTIONS.iter().map(|sec| {
                let on = s.section == *sec;
                let glyph = match *sec {
                    "machine" => "monitor",
                    "code" => "folder-git-2",
                    "ai" => "square-terminal",
                    "secrets" => "key-round",
                    _ => "wrench",
                };
                h_flex()
                    .id(SharedString::from(format!("agent-settings-{sec}")))
                    .gap_2()
                    .px_2p5()
                    .py(px(7.))
                    .rounded_lg()
                    .text_size(theme::text::UI)
                    .when(on, |el| {
                        el.bg(theme::surface_raised()).text_color(theme::title())
                    })
                    .when(!on, |el| {
                        el.text_color(theme::muted())
                            .cursor_pointer()
                            .hover(|s| s.bg(theme::surface()).text_color(theme::primary()))
                    })
                    .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                        this.agent_settings.section = sec;
                        this.load_agent_settings(cx);
                        cx.notify();
                    }))
                    .child(
                        div()
                            .flex_none()
                            .text_size(px(16.))
                            .text_color(if on {
                                theme::brand(300)
                            } else {
                                theme::subtle()
                            })
                            .child(crate::icons::of(glyph)),
                    )
                    .child(t(&format!("agent.section.{sec}")))
            }));
        let body = match s.section {
            "machine" => self.settings_machine(&a),
            "code" => self.settings_code(&a),
            "ai" => self.settings_ai(&a),
            "secrets" => self.settings_secrets(&a, cx),
            _ => self.settings_connectors(cx),
        };
        v_flex()
            .size_full()
            .child(
                h_flex()
                    .flex_none()
                    .px_4()
                    .py_2()
                    .gap_2()
                    .child(controls::button(
                        "agent-settings-back",
                        format!("← {}", t("agent.view.backToChat")),
                        Btn::Ghost,
                        false,
                        cx.listener(|this, _: &ClickEvent, _, cx| {
                            this.agent.page = None;
                            cx.notify();
                        }),
                    ))
                    .child(
                        div()
                            .text_size(theme::text::UI)
                            .text_color(theme::muted())
                            .child(t("agent.tab.settings")),
                    ),
            )
            .child(
                h_flex()
                    .flex_1()
                    .min_h_0()
                    .items_start()
                    .border_t_1()
                    .border_color(theme::line_faint())
                    .child(nav)
                    .child(
                        div()
                            .id("agent-settings-body")
                            .flex_1()
                            .min_w_0()
                            .h_full()
                            .overflow_y_scroll()
                            .px_6()
                            .py_5()
                            .child(body),
                    ),
            )
            .into_any_element()
    }

    fn settings_card(rows: Vec<(String, String, bool)>) -> Div {
        // Card and its Rows (ui/card.tsx): a label in subtle beside its
        // value, a hairline between them.
        super::settings::kit::panel(v_flex())
            .px_5()
            .py_3()
            .children(
                rows.into_iter()
                    .enumerate()
                    .map(|(i, (label, value, mono))| {
                        h_flex()
                            .min_h_9()
                            .gap_4()
                            .text_sm()
                            .when(i > 0, |el| {
                                el.border_t_1().border_color(theme::line_faint())
                            })
                            .child(
                                div()
                                    .w(px(112.))
                                    .flex_none()
                                    .text_size(theme::text::UI)
                                    .text_color(theme::subtle())
                                    .child(label),
                            )
                            .child(
                                div()
                                    .min_w_0()
                                    .flex_1()
                                    .truncate()
                                    .text_color(theme::secondary())
                                    .when(mono, |el| {
                                        el.font_family(theme::mono()).text_size(theme::text::UI)
                                    })
                                    .child(value),
                            )
                    }),
            )
    }

    fn settings_machine(&self, a: &FleetAgent) -> AnyElement {
        let dash = || "—".to_string();
        let usage = self.panel.usage_of(&a.r#ref);
        let metric = |label: String, value: String, hint: String| {
            v_flex()
                .flex_1()
                .min_w(px(120.))
                .gap_0p5()
                .p_3()
                .rounded_xl()
                .border_1()
                .border_color(theme::line_faint())
                .bg(theme::surface_faint())
                .child(
                    div()
                        .text_size(theme::text::CAPTION)
                        .font_weight(FontWeight::MEDIUM)
                        .text_color(theme::muted())
                        .child(label),
                )
                .child(
                    div()
                        .text_size(theme::text::DISPLAY)
                        .font_weight(FontWeight::SEMIBOLD)
                        .text_color(theme::title())
                        .child(value),
                )
                .child(div().text_xs().text_color(theme::faint()).child(hint))
        };
        v_flex()
            .gap_4()
            .child(
                Self::settings_card(vec![
                    (
                        t("agent.overview.state"),
                        super::state_label(&a.state),
                        false,
                    ),
                    (
                        t("agent.overview.ip"),
                        if a.ip.is_empty() {
                            dash()
                        } else {
                            a.ip.clone()
                        },
                        true,
                    ),
                    (t("agent.overview.instance"), a.instance.clone(), true),
                    (
                        t("agent.overview.startedFrom"),
                        if a.source.is_empty() {
                            dash()
                        } else {
                            a.source.clone()
                        },
                        true,
                    ),
                    (
                        t("agent.overview.created"),
                        super::past::local_or(&a.created_at),
                        false,
                    ),
                ])
                .child(
                    h_flex()
                        .mt_2()
                        .gap_3()
                        .flex_wrap()
                        .child(metric(
                            "CPU".into(),
                            usage
                                .and_then(|u| u.0)
                                .map(|c| format!("{c:.0}%"))
                                .unwrap_or_else(dash),
                            t("agent.overview.cpuHint"),
                        ))
                        .child(metric(
                            t("agent.overview.memory"),
                            usage
                                .and_then(|u| u.1)
                                .map(crate::files::human_bytes)
                                .unwrap_or_else(dash),
                            t("agent.overview.memoryHint"),
                        )),
                ),
            )
            .into_any_element()
    }

    fn settings_code(&self, a: &FleetAgent) -> AnyElement {
        let wt = a.worktree.clone();
        let wt2 = a.worktree.clone();
        v_flex()
            .gap_4()
            .child(
                Self::settings_card(vec![
                    (t("agent.view.branch"), a.branch.clone(), true),
                    (t("agent.overview.basedOn"), a.base_ref.clone(), true),
                    (t("agent.overview.worktree"), a.worktree.clone(), true),
                ])
                .child(
                    h_flex()
                        .gap_2()
                        .child(controls::button(
                            "settings-copy-worktree",
                            t("agent.overview.copyPath"),
                            Btn::Secondary,
                            false,
                            move |_, _, cx| {
                                cx.write_to_clipboard(ClipboardItem::new_string(wt.clone()));
                                toasts::info(cx, t("agent.view.copiedWorktree"));
                            },
                        ))
                        .child(controls::button(
                            "settings-open-worktree",
                            t("agent.overview.openFolder"),
                            Btn::Secondary,
                            false,
                            move |_, _, cx| cx.open_with_system(std::path::Path::new(&wt2)),
                        )),
                ),
            )
            .child(
                v_flex()
                    .gap_2()
                    .child(div().text_xs().text_color(theme::muted()).child(tv(
                        "agent.overview.changesSince",
                        &[("ref", (&a.base_ref).into())],
                    )))
                    .child(
                        div()
                            .p_3p5()
                            .rounded_xl()
                            .border_1()
                            .border_color(theme::line())
                            .bg(theme::well())
                            .font_family(theme::mono())
                            .text_xs()
                            .line_height(rems(1.6))
                            .text_color(theme::subtle())
                            .whitespace_normal()
                            .children(
                                match &self.agent_settings.diff {
                                    None => vec![t("common.loading")],
                                    Some(d) if d.trim().is_empty() => {
                                        vec![t("agent.overview.noChanges")]
                                    }
                                    Some(d) => d.lines().map(String::from).collect(),
                                }
                                .into_iter()
                                .map(|l| div().child(l)),
                            ),
                    ),
            )
            .into_any_element()
    }

    fn settings_ai(&self, a: &FleetAgent) -> AnyElement {
        let none = a.ai == "none";
        let account = match a.ai.as_str() {
            "claude" => a.claude_account.clone(),
            "codex" => a.codex_account.clone(),
            "cursor" => a.cursor_account.clone(),
            "gemini" => a.gemini_account.clone(),
            _ => String::new(),
        };
        let mut rows = vec![(t("agent.overview.tool"), ai_label(&a.ai), false)];
        if !none {
            rows.push((
                t("agent.overview.interface"),
                t(if a.interface == "cli" {
                    "agent.tab.terminal"
                } else {
                    "agent.tab.chat"
                }),
                false,
            ));
        }
        rows.push((
            t("agent.overview.permissions"),
            if none {
                "—".into()
            } else if a.autonomous {
                t("agent.overview.bypassed")
            } else {
                t("agent.overview.asks")
            },
            false,
        ));
        if !account.is_empty() {
            rows.push((t("agent.overview.account"), account, false));
        }
        rows.push((
            t("agent.overview.shell"),
            format!("agentbox shell {}", a.r#ref),
            true,
        ));
        Self::settings_card(rows).into_any_element()
    }

    fn settings_secrets(&self, a: &FleetAgent, cx: &mut Context<Self>) -> AnyElement {
        let s = &self.agent_settings;
        let ready =
            !s.name.read(cx).value().trim().is_empty() && !s.value.read(cx).value().is_empty();
        let list = match &s.secrets {
            Some(Ok(l)) => l.clone(),
            _ => Vec::new(),
        };
        let (inherited, mine): (Vec<Secret>, Vec<Secret>) =
            list.into_iter().partition(|x| x.scope == "project");
        let row = |sec: &Secret, remove: bool, cx: &mut Context<Self>| {
            let name = sec.name.clone();
            h_flex()
                .flex_wrap()
                .gap_3()
                .px_3()
                .py_2p5()
                .rounded_xl()
                .border_1()
                .border_color(theme::line_faint())
                .bg(theme::surface_faint())
                .hover(|st| st.border_color(theme::line_strong()))
                .child(
                    div()
                        .font_family(theme::mono())
                        .text_size(theme::text::UI)
                        .text_color(theme::primary())
                        .child(format!("${}", sec.name)),
                )
                .child(super::settings::kit::badge(
                    t(if sec.scope == "project" {
                        "agent.secrets.scopeProject"
                    } else {
                        "agent.secrets.scopeAgent"
                    }),
                    theme::muted(),
                ))
                .child(
                    div()
                        .flex_1()
                        .min_w(px(80.))
                        .text_xs()
                        .text_color(theme::subtle())
                        .child(tv(
                            "agent.secrets.updated",
                            &[("when", time_ago(&sec.updated_at).into())],
                        )),
                )
                .when(remove, |el| {
                    el.child(controls::button(
                        SharedString::from(format!("secret-remove-{}", sec.name)),
                        t("common.remove"),
                        Btn::Ghost,
                        self.agent_settings.busy,
                        cx.listener(move |this, _: &ClickEvent, _, cx| {
                            this.agent_call(
                                Method::DELETE,
                                format!("/secrets/{}", crate::files::encode(&name)),
                                None,
                                tv("agent.secrets.removed", &[("name", (&name).into())]),
                                cx,
                            )
                        }),
                    ))
                })
                .into_any_element()
        };
        let card = |title: String, description: String, body: Vec<AnyElement>| {
            super::settings::kit::panel(v_flex())
                .gap_3()
                .px_5()
                .pt_4()
                .pb_5()
                .child(
                    v_flex()
                        .child(
                            div()
                                .text_sm()
                                .font_weight(FontWeight::SEMIBOLD)
                                .text_color(theme::primary())
                                .child(title),
                        )
                        .child(
                            div()
                                .text_xs()
                                .text_color(theme::subtle())
                                .child(description),
                        ),
                )
                .children(body)
        };
        let form = card(
            t("agent.secrets.giveAgent"),
            t("agent.secrets.giveAgentDescription"),
            vec![
                h_flex()
                    .flex_wrap()
                    .gap_3()
                    .items_end()
                    .child(controls::labeled(
                        t("agent.secrets.name"),
                        Input::new(&s.name),
                    ))
                    .child(controls::labeled(
                        t("agent.secrets.value"),
                        Input::new(&s.value).mask_toggle(),
                    ))
                    .child(controls::button(
                        "secret-store",
                        t("agent.secrets.store"),
                        Btn::Primary,
                        !ready || s.busy,
                        cx.listener(|this, _: &ClickEvent, w, cx| this.store_secret(w, cx)),
                    ))
                    .into_any_element(),
                div()
                    .text_xs()
                    .text_color(theme::faint())
                    .child(t("agent.secrets.nameHint"))
                    .into_any_element(),
            ],
        );
        let mut col = v_flex().gap_4().child(form);
        if !inherited.is_empty() {
            let rows: Vec<AnyElement> = inherited.iter().map(|x| row(x, false, cx)).collect();
            col = col.child(card(
                tv(
                    "agent.secrets.fromProject",
                    &[("count", inherited.len().into())],
                ),
                t("agent.secrets.fromProjectDescription"),
                rows,
            ));
        }
        let mut own: Vec<AnyElement> = Vec::new();
        match &s.secrets {
            None => own.push(super::panel::loading("agent_settings")),
            Some(Err(e)) => own.push(controls::note_in(e.clone(), theme::rose(300))),
            Some(Ok(_)) if mine.is_empty() => own.push(
                div()
                    .text_xs()
                    .text_color(theme::muted())
                    .child(strip_code(&t("agent.secrets.none")))
                    .into_any_element(),
            ),
            _ => own.extend(mine.iter().map(|x| row(x, true, cx))),
        }
        let _ = a;
        col.child(card(
            tv("agent.secrets.ownTitle", &[("count", mine.len().into())]),
            t("agent.secrets.ownDescription"),
            own,
        ))
        .into_any_element()
    }

    fn settings_connectors(&self, cx: &mut Context<Self>) -> AnyElement {
        let list = match &self.agent_settings.connectors {
            None => return super::panel::loading("agent_settings"),
            Some(Err(e)) => return controls::note_in(e.clone(), theme::rose(300)),
            Some(Ok(l)) => l.clone(),
        };
        let (project, mine): (Vec<Connector>, Vec<Connector>) =
            list.into_iter().partition(|c| c.scope == "project");
        let row = |c: &Connector, own: bool, cx: &mut Context<Self>| {
            let (status, color) = super::settings::connectors::status(c);
            let (n1, n2) = (c.name.clone(), c.name.clone());
            let connected = c.status == agentbox_api::CONNECTOR_CONNECTED;
            h_flex()
                .flex_wrap()
                .gap_3()
                .px_3()
                .py_2p5()
                .rounded_xl()
                .border_1()
                .border_color(theme::line_faint())
                .bg(theme::surface_faint())
                .hover(|st| st.border_color(theme::line_strong()))
                .child(div().size(px(7.)).rounded_full().bg(color))
                .child(
                    v_flex()
                        .flex_1()
                        .min_w_0()
                        .child(
                            div()
                                .font_family(theme::mono())
                                .text_size(theme::text::UI)
                                .text_color(theme::primary())
                                .child(c.name.clone()),
                        )
                        .child(
                            div()
                                .truncate()
                                .text_xs()
                                .font_family(theme::mono())
                                .text_color(theme::subtle())
                                .child(c.url.clone()),
                        ),
                )
                .child(div().text_xs().text_color(color).child(status))
                .when(own, |el| {
                    el.child(controls::button(
                        SharedString::from(format!("connector-toggle-{}", c.name)),
                        t(if connected {
                            "project.connectors.disconnect"
                        } else {
                            "project.connectors.connect"
                        }),
                        Btn::Secondary,
                        self.agent_settings.busy,
                        cx.listener(move |this, _: &ClickEvent, _, cx| {
                            let action = if connected { "disconnect" } else { "connect" };
                            let ok = if connected {
                                String::new()
                            } else {
                                tv("project.connectors.connected", &[("name", (&n1).into())])
                            };
                            this.agent_call(
                                Method::POST,
                                format!("/connectors/{}/{action}", crate::files::encode(&n1)),
                                None,
                                ok,
                                cx,
                            )
                        }),
                    ))
                    .child(controls::button(
                        SharedString::from(format!("connector-remove-{}", c.name)),
                        t("common.remove"),
                        Btn::Ghost,
                        self.agent_settings.busy,
                        cx.listener(move |this, _: &ClickEvent, _, cx| {
                            this.agent_call(
                                Method::DELETE,
                                format!("/connectors/{}", crate::files::encode(&n2)),
                                None,
                                tv("project.connectors.removed", &[("name", (&n2).into())]),
                                cx,
                            )
                        }),
                    ))
                })
                .into_any_element()
        };
        let card = |title: String, description: String, rows: Vec<AnyElement>| {
            super::settings::kit::panel(v_flex())
                .gap_3()
                .px_5()
                .pt_4()
                .pb_5()
                .child(
                    v_flex()
                        .child(
                            div()
                                .text_sm()
                                .font_weight(FontWeight::SEMIBOLD)
                                .text_color(theme::primary())
                                .child(title),
                        )
                        .child(
                            div()
                                .text_xs()
                                .text_color(theme::subtle())
                                .child(description),
                        ),
                )
                .children(rows)
        };
        let mut col = v_flex().gap_4();
        let own_rows: Vec<AnyElement> = if mine.is_empty() {
            vec![
                div()
                    .text_xs()
                    .text_color(theme::muted())
                    .child(strip_code(&t("project.connectors.none")))
                    .into_any_element(),
            ]
        } else {
            mine.iter().map(|c| row(c, true, cx)).collect()
        };
        col = col.child(card(
            tv(
                "project.connectors.agentOwn",
                &[("count", mine.len().into())],
            ),
            t("project.connectors.agentOwnDescription"),
            own_rows,
        ));
        if !project.is_empty() {
            let rows: Vec<AnyElement> = project.iter().map(|c| row(c, false, cx)).collect();
            col = col.child(card(
                tv(
                    "project.connectors.fromProject",
                    &[("count", project.len().into())],
                ),
                t("project.connectors.fromProjectDescription"),
                rows,
            ));
        }
        col.into_any_element()
    }
}
