//! Settings → Connectors (ConnectorsTab.tsx with target "", and
//! ConnectorImportDialog.tsx): the remote MCP servers every project's agents
//! and chats get as tools, signed in once, here. Listed from GET
//! /v1/connectors, added from the catalog or a URL (PUT
//! /v1/connectors/{name}), signed in through the user's browser (POST
//! …/connect, then the list is read again until it stops waiting), given or
//! left out, disconnected, removed, and imported from the user's AI tools
//! (POST /v1/connectors/scan, /import). No answer carries a token.
//!
//! The import here is AgentBox-wide only (Settings' own scope), and a server
//! whose header needs a value from the user's shell isn't offered to pick:
//! it's added by hand with its token.
use std::collections::HashSet;
use std::time::Duration;

use agentbox_api::client::Method;
use agentbox_api::{
    CONNECTOR_CONNECTED, CONNECTOR_CONNECTING, CONNECTOR_ERROR, CONNECTOR_NONE, CONNECTOR_O_AUTH,
    CONNECTOR_PROBLEM_SSE, CONNECTOR_PROBLEM_STDIO, CONNECTOR_PROBLEM_VARIABLE, CONNECTOR_SECRET,
    ConnectResult, Connector, ConnectorScan, ImportConnector, ImportConnectorsRequest,
    ImportConnectorsResult, ScanConnectorsRequest, SetConnectorRequest,
};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::{h_flex, v_flex};

use super::super::AppView;
use super::super::controls::{Btn, badge, button, labeled, note, note_in, segmented, toggle};
use super::defaults::time_ago;
use super::kit::{ask, card, check, enc, link, mono, title};
use super::{Entry, Group, group};
use crate::theme;

/// A connector the catalog offers (lib/connectors.ts's connectorPresets).
struct Preset {
    label: &'static str,
    name: &'static str,
    url: &'static str,
    /// secret is (its name, the header it goes in, how to make one), for a
    /// server that takes a token rather than a browser sign-in.
    secret: Option<(&'static str, &'static str, &'static str)>,
}

const PRESETS: [Preset; 5] = [
    Preset {
        label: "Notion",
        name: "notion",
        url: "https://mcp.notion.com/mcp",
        secret: None,
    },
    Preset {
        label: "Linear",
        name: "linear",
        url: "https://mcp.linear.app/mcp",
        secret: None,
    },
    Preset {
        label: "Sentry",
        name: "sentry",
        url: "https://mcp.sentry.dev/mcp",
        secret: None,
    },
    Preset {
        label: "Hatch",
        name: "hatch",
        url: "https://usehatch.dev/mcp",
        secret: None,
    },
    Preset {
        label: "Figma",
        name: "figma",
        url: "https://mcp.figma.com/mcp",
        secret: Some((
            "FIGMA_TOKEN",
            "X-Figma-Token",
            "https://help.figma.com/hc/en-us/articles/8085703771159-Manage-personal-access-tokens",
        )),
    },
];

#[derive(Default)]
pub(super) struct Connectors {
    list: Option<Vec<Connector>>,
    error: Option<String>,
    said: Option<String>,
    /// form is the catalog's pick: a preset's name, or "custom".
    form: Option<String>,
    auth: String,
    removing: Option<String>,
    busy: bool,
    /// waiting is the connector whose browser sign-in this page started.
    waiting: Option<String>,
    polling: bool,
    importing: bool,
    scan: Option<Result<ConnectorScan, String>>,
    picked: HashSet<String>,
}

/// connector_url puts https:// before an address typed without it.
fn connector_url(typed: &str) -> String {
    let url = typed.trim();
    if url.is_empty() || url.contains("://") {
        url.to_string()
    } else {
        format!("https://{url}")
    }
}

/// connector_name is the name a URL suggests: mcp.notion.com is notion.
fn connector_name(url: &str) -> String {
    let rest = url.split("://").nth(1).unwrap_or_default();
    let host = rest
        .split(['/', '?', '#'])
        .next()
        .unwrap_or_default()
        .rsplit('@')
        .next()
        .unwrap_or_default()
        .split(':')
        .next()
        .unwrap_or_default()
        .to_lowercase();
    if host.is_empty()
        || host == "localhost"
        || host.chars().all(|c| c.is_ascii_digit() || c == '.')
    {
        return String::new();
    }
    let mut parts: Vec<&str> = host
        .split('.')
        .filter(|p| !["www", "mcp", "api"].contains(p))
        .collect();
    let n = parts.len();
    if n > 2
        && ["co", "com", "net", "org", "gov", "ac", "edu"].contains(&parts[n - 2])
        && parts[n - 1].len() == 2
    {
        parts.pop();
    }
    let name = if parts.len() > 1 {
        parts[parts.len() - 2]
    } else {
        parts.first().copied().unwrap_or_default()
    };
    let name: String = name
        .chars()
        .map(|c| {
            if c.is_ascii_alphanumeric() || c == '_' || c == '-' {
                c
            } else {
                '-'
            }
        })
        .collect();
    name.trim_start_matches('-').chars().take(40).collect()
}

/// secret_name_for is the secret a connector's token is kept as: ACME_TOKEN.
fn secret_name_for(connector: &str) -> String {
    let mut base: String = connector
        .to_uppercase()
        .chars()
        .map(|c| {
            if c.is_ascii_alphanumeric() || c == '_' {
                c
            } else {
                '_'
            }
        })
        .collect();
    if base.starts_with(|c: char| c.is_ascii_digit()) {
        base.insert(0, '_');
    }
    if base.is_empty() {
        base
    } else {
        format!("{base}_TOKEN")
    }
}

pub(crate) fn status(c: &Connector) -> (String, Hsla) {
    match c.status.as_str() {
        CONNECTOR_CONNECTED => (t("project.connectors.status.connected"), theme::running()),
        CONNECTOR_CONNECTING => (t("project.connectors.status.connecting"), theme::starting()),
        CONNECTOR_ERROR => (t("project.connectors.status.error"), theme::error()),
        _ => (t("project.connectors.status.disconnected"), theme::muted()),
    }
}

/// time_until is utils.ts's timeUntil: in 5 minutes, in 3 hours, in 2 days.
pub(crate) fn time_until(iso: &str) -> String {
    let Ok(at) = chrono::DateTime::parse_from_rfc3339(iso) else {
        return String::new();
    };
    let s = (at.timestamp() - chrono::Utc::now().timestamp()).max(0);
    if s < 3600 {
        tv("common.inMinutes", &[("n", ((s + 30) / 60).max(1).into())])
    } else if s < 86400 {
        tv("common.inHours", &[("n", ((s + 1800) / 3600).into())])
    } else {
        tv("common.inDays", &[("n", ((s + 43200) / 86400).into())])
    }
}

fn token_line(c: &Connector) -> Option<String> {
    if c.status != CONNECTOR_CONNECTED {
        return None;
    }
    match c.auth.as_str() {
        CONNECTOR_SECRET if !c.secret.is_empty() => Some(tv(
            "project.connectors.token.fromSecret",
            &[("secret", (&c.secret).into())],
        )),
        CONNECTOR_O_AUTH => Some(match &c.expires_at {
            None => t("project.connectors.token.noExpiry"),
            Some(e)
                if chrono::DateTime::parse_from_rfc3339(e)
                    .is_ok_and(|at| at.timestamp() <= chrono::Utc::now().timestamp()) =>
            {
                t("project.connectors.token.expired")
            }
            Some(e) => tv(
                "project.connectors.token.expires",
                &[("when", (&time_until(e)).into())],
            ),
        }),
        _ => None,
    }
}

fn host_of(url: &str) -> String {
    url.split("://")
        .nth(1)
        .unwrap_or(url)
        .split('/')
        .next()
        .unwrap_or_default()
        .to_string()
}

impl AppView {
    pub(super) fn connectors_groups(&self) -> Vec<Group> {
        vec![
            group(
                "connectors",
                "settings.group.connectors.title",
                vec![Entry {
                    id: "connectors",
                    label: t("settings.section.connectors.title"),
                    description: t("settings.section.connectors.description"),
                    keywords: t("settings.entry.connectors.keywords"),
                    essential: false,
                    modified: false,
                    scope: String::new(),
                }],
            )
            .cards(),
        ]
    }

    pub(super) fn load_connectors(&mut self, cx: &mut Context<Self>) {
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client.get::<Vec<Connector>>("/v1/connectors").await;
            this.update(cx, |this, cx| {
                let c = &mut this.settings_page.connectors;
                match res {
                    Ok(list) => {
                        // A sign-in this page started has ended: say how.
                        if let Some(w) = c.waiting.clone()
                            && let Some(done) = list
                                .iter()
                                .find(|x| x.name == w && x.status != CONNECTOR_CONNECTING)
                        {
                            c.waiting = None;
                            c.said = Some(if done.status == CONNECTOR_CONNECTED {
                                format!(
                                    "{} {}",
                                    tv("project.connectors.connected", &[("name", (&w).into())]),
                                    t("project.connectors.connectedDetail")
                                )
                            } else {
                                tv("project.connectors.notConnected", &[("name", (&w).into())])
                            });
                        }
                        c.list = Some(list);
                        c.error = None;
                    }
                    Err(e) => c.error = Some(e.to_string()),
                }
                this.poll_connectors(cx);
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// poll_connectors reads the list again every two seconds while a
    /// browser sign-in is waiting, as Electron refetches on its events.
    fn poll_connectors(&mut self, cx: &mut Context<Self>) {
        let c = &mut self.settings_page.connectors;
        let waiting = c
            .list
            .iter()
            .flatten()
            .any(|x| x.status == CONNECTOR_CONNECTING);
        if !waiting || c.polling || !self.settings || self.settings_page.open != "connectors" {
            return;
        }
        c.polling = true;
        cx.spawn(async move |this, cx| {
            cx.background_executor().timer(Duration::from_secs(2)).await;
            this.update(cx, |this, cx| {
                this.settings_page.connectors.polling = false;
                this.load_connectors(cx);
            })
            .ok();
        })
        .detach();
    }

    fn connector_call(
        &mut self,
        method: Method,
        path: String,
        body: Option<Vec<u8>>,
        cx: &mut Context<Self>,
        done: impl FnOnce(&mut Self, &[u8], &mut Context<Self>) + 'static,
    ) {
        let client = self.client.clone();
        self.settings_page.connectors.busy = true;
        cx.spawn(async move |this, cx| {
            let res = client.raw(method, &path, body).await;
            this.update(cx, |this, cx| {
                let c = &mut this.settings_page.connectors;
                c.busy = false;
                match res {
                    Ok(b) => {
                        c.error = None;
                        done(this, &b, cx);
                    }
                    Err(e) => c.error = Some(e.to_string()),
                }
                this.load_connectors(cx);
                cx.notify();
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    fn connect_connector(&mut self, name: String, cx: &mut Context<Self>) {
        let path = format!("/v1/connectors/{}/connect", enc(&name));
        self.connector_call(Method::POST, path, None, cx, move |this, b, cx| {
            if let Ok(r) = serde_json::from_slice::<ConnectResult>(b) {
                this.settings_page.connectors.waiting = Some(name);
                cx.open_url(&r.authorization_url);
            }
        });
    }

    fn pick_preset(&mut self, which: String, window: &mut Window, cx: &mut Context<Self>) {
        let preset = PRESETS.iter().find(|p| p.name == which);
        let c = &mut self.settings_page.connectors;
        if c.form.as_deref() == Some(which.as_str()) {
            c.form = None;
            cx.notify();
            return;
        }
        c.form = Some(which);
        c.auth = if preset.is_some_and(|p| p.secret.is_some()) {
            CONNECTOR_SECRET.into()
        } else {
            CONNECTOR_O_AUTH.into()
        };
        let set =
            |this: &Self, name: &str, v: &str, window: &mut Window, cx: &mut Context<Self>| {
                if let Some(i) = this.settings_page.fields.get(name) {
                    let v = v.to_string();
                    i.update(cx, |i, cx| i.set_value(v, window, cx));
                }
            };
        set(
            self,
            "connector-url",
            preset.map_or("", |p| p.url),
            window,
            cx,
        );
        set(
            self,
            "connector-name",
            preset.map_or("", |p| p.name),
            window,
            cx,
        );
        set(
            self,
            "connector-secret",
            preset.and_then(|p| p.secret).map_or("", |s| s.0),
            window,
            cx,
        );
        set(
            self,
            "connector-header",
            preset
                .and_then(|p| p.secret)
                .map_or("Authorization", |s| s.1),
            window,
            cx,
        );
        set(self, "connector-token", "", window, cx);
        cx.notify();
    }

    /// shown_connector_name is the name typed, or the one the URL suggests.
    fn shown_connector_name(&self, cx: &App) -> String {
        let typed = self.field_value("connector-name", cx);
        if typed.is_empty() {
            connector_name(&connector_url(&self.field_value("connector-url", cx)))
        } else {
            typed
        }
    }

    pub(super) fn add_connector(&mut self, cx: &mut Context<Self>) {
        let c = &self.settings_page.connectors;
        let auth = c.auth.clone();
        let name = self.shown_connector_name(cx);
        let url = connector_url(&self.field_value("connector-url", cx));
        if name.is_empty() || url.is_empty() {
            return;
        }
        let mut req = SetConnectorRequest {
            url,
            auth: auth.clone(),
            ..Default::default()
        };
        if auth == CONNECTOR_SECRET {
            let secret = self.field_value("connector-secret", cx);
            req.secret = if secret.is_empty() {
                secret_name_for(&name)
            } else {
                secret
            };
            req.header = self.field_value("connector-header", cx);
            req.secret_value = self.field_value("connector-token", cx);
        }
        let body = serde_json::to_vec(&req).ok();
        let path = format!("/v1/connectors/{}", enc(&name));
        self.connector_call(Method::PUT, path, body, cx, move |this, b, cx| {
            let added: Connector = serde_json::from_slice(b).unwrap_or_default();
            let c = &mut this.settings_page.connectors;
            c.form = None;
            c.said = Some(tv(
                "project.connectors.added",
                &[("name", (&added.name).into())],
            ));
            if auth == CONNECTOR_O_AUTH {
                this.connect_connector(added.name, cx);
            }
        });
    }

    fn give_connector(&mut self, c: &Connector, enabled: bool, cx: &mut Context<Self>) {
        let req = SetConnectorRequest {
            url: c.url.clone(),
            auth: c.auth.clone(),
            secret: c.secret.clone(),
            header: c.header.clone(),
            scheme: c.scheme.clone(),
            secret_value: String::new(),
            enabled: Some(enabled),
        };
        let path = format!("/v1/connectors/{}", enc(&c.name));
        self.connector_call(
            Method::PUT,
            path,
            serde_json::to_vec(&req).ok(),
            cx,
            |_, _, _| {},
        );
    }

    fn start_connector_import(&mut self, cx: &mut Context<Self>) {
        let c = &mut self.settings_page.connectors;
        c.importing = true;
        c.scan = None;
        c.picked.clear();
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client
                .post::<_, ConnectorScan>("/v1/connectors/scan", &ScanConnectorsRequest::default())
                .await;
            this.update(cx, |this, cx| {
                let c = &mut this.settings_page.connectors;
                if let Ok(s) = &res {
                    c.picked = s
                        .candidates
                        .iter()
                        .filter(|x| x.problem.is_empty() && x.existing.is_empty() && !x.needs_value)
                        .map(|x| x.url.clone())
                        .collect();
                }
                c.scan = Some(res.map_err(|e| e.to_string()));
                cx.notify();
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    fn import_connectors(&mut self, cx: &mut Context<Self>) {
        let c = &self.settings_page.connectors;
        let servers = c
            .picked
            .iter()
            .map(|url| ImportConnector {
                url: url.clone(),
                ..Default::default()
            })
            .collect();
        let body = serde_json::to_vec(&ImportConnectorsRequest {
            project: String::new(),
            servers,
        })
        .ok();
        self.connector_call(
            Method::POST,
            "/v1/connectors/import".into(),
            body,
            cx,
            |this, b, cx| {
                let res: ImportConnectorsResult = serde_json::from_slice(b).unwrap_or_default();
                let c = &mut this.settings_page.connectors;
                c.importing = false;
                let mut said = tv(
                    "project.connectors.imported",
                    &[("count", res.added.len().into())],
                );
                let oauth: Vec<String> = res
                    .added
                    .iter()
                    .filter(|x| x.auth == CONNECTOR_O_AUTH && x.status != CONNECTOR_CONNECTED)
                    .map(|x| x.name.clone())
                    .collect();
                if !oauth.is_empty() {
                    said = format!("{said}. {}", t("project.connectors.importedSignIn"));
                }
                for f in &res.failed {
                    let name = if f.name.is_empty() { &f.url } else { &f.name };
                    said = format!(
                        "{said} {}",
                        tv(
                            "project.connectors.importFailed",
                            &[("name", name.into()), ("error", (&f.error).into())]
                        )
                    );
                }
                c.said = Some(said);
                for name in oauth {
                    this.connect_connector(name, cx);
                }
            },
        );
    }

    pub(super) fn connectors_card(&self, cx: &mut Context<Self>) -> AnyElement {
        let c = &self.settings_page.connectors;
        let list = c.list.as_deref().unwrap_or_default();
        v_flex()
            .id("connectors")
            .gap_4()
            .when_some(c.error.clone(), |el, e| {
                el.child(note_in(e, theme::error()))
            })
            .when_some(c.said.clone(), |el, s| {
                el.child(note_in(s, theme::running()))
            })
            .child(if c.importing {
                self.connector_import(cx)
            } else {
                self.connector_catalog(cx)
            })
            .child(
                card("connectors-list")
                    .gap_3()
                    .child(title(tv(
                        "project.connectors.wideOwn",
                        &[("count", list.len().into())],
                    )))
                    .child(note(t("project.connectors.wideOwnDescription")))
                    .when(c.list.is_none() && c.error.is_none(), |el| {
                        el.child(note(t("common.loading")))
                    })
                    .when(c.list.as_ref().is_some_and(|l| l.is_empty()), |el| {
                        el.child(note(
                            t("project.connectors.none")
                                .replace("<code>", "")
                                .replace("</code>", ""),
                        ))
                    })
                    .children(list.iter().map(|x| self.connector_row(x, cx))),
            )
            .into_any_element()
    }

    fn connector_catalog(&self, cx: &mut Context<Self>) -> AnyElement {
        let c = &self.settings_page.connectors;
        let list = c.list.as_deref().unwrap_or_default();
        let tile = |id: String,
                    mark: String,
                    label: String,
                    detail: String,
                    on: bool,
                    cx: &mut Context<Self>| {
            let pick = id.clone();
            v_flex()
                .id(SharedString::from(format!("connector-preset-{id}")))
                .gap_1()
                .px_3()
                .py_2p5()
                .rounded_lg()
                .border_1()
                .border_color(if on {
                    theme::accent().opacity(0.6)
                } else {
                    theme::line()
                })
                .when(on, |el| el.bg(theme::accent().opacity(0.08)))
                .cursor_pointer()
                .hover(|st| st.bg(theme::surface_raised()))
                .on_click(cx.listener(move |this, _: &ClickEvent, window, cx| {
                    this.pick_preset(pick.clone(), window, cx)
                }))
                .child(
                    h_flex()
                        .gap_2()
                        .child(
                            div()
                                .size(px(22.))
                                .rounded_md()
                                .bg(theme::surface_raised())
                                .flex()
                                .items_center()
                                .justify_center()
                                .text_xs()
                                .font_weight(FontWeight::SEMIBOLD)
                                .text_color(theme::primary())
                                .child(crate::icons::of(mark)),
                        )
                        .child(
                            div()
                                .text_sm()
                                .font_weight(FontWeight::MEDIUM)
                                .text_color(theme::primary())
                                .child(label),
                        ),
                )
                .child(
                    div()
                        .text_xs()
                        .text_color(theme::faint())
                        .line_clamp(2)
                        .child(detail),
                )
                .into_any_element()
        };
        let mut tiles: Vec<AnyElement> = PRESETS
            .iter()
            .map(|p| {
                let existing = list.iter().find(|x| x.url.trim_end_matches('/') == p.url);
                let detail = match existing {
                    Some(x) => tv("project.connectors.addedAs", &[("name", (&x.name).into())]),
                    None => t(&format!("project.connectors.blurb.{}", p.name)),
                };
                let on = c.form.as_deref() == Some(p.name);
                tile(
                    p.name.into(),
                    p.label[..1].into(),
                    p.label.into(),
                    detail,
                    on,
                    cx,
                )
            })
            .collect();
        tiles.push(tile(
            "custom".into(),
            "+".into(),
            t("project.connectors.customUrl"),
            t("project.connectors.customDetail"),
            c.form.as_deref() == Some("custom"),
            cx,
        ));
        card("connectors-catalog")
            .gap_3()
            .child(
                h_flex()
                    .gap_2()
                    .child(
                        div()
                            .flex_1()
                            .child(title(t("project.connectors.giveWide"))),
                    )
                    .child(button(
                        "connectors-import",
                        t("project.connectors.import"),
                        Btn::Secondary,
                        false,
                        cx.listener(|this, _: &ClickEvent, _, cx| this.start_connector_import(cx)),
                    )),
            )
            .child(note(t("project.connectors.giveWideDescription")))
            .child(div().grid().grid_cols(3).gap_2().children(tiles))
            .when(c.form.is_some(), |el| el.child(self.connector_form(cx)))
            .into_any_element()
    }

    fn connector_form(&self, cx: &mut Context<Self>) -> AnyElement {
        let c = &self.settings_page.connectors;
        let preset = PRESETS.iter().find(|p| Some(p.name) == c.form.as_deref());
        let auth = c.auth.as_str();
        let name = self.shown_connector_name(cx);
        let taken = c.list.iter().flatten().any(|x| x.name == name);
        let ready = !name.is_empty()
            && !taken
            && !self.field_value("connector-url", cx).is_empty()
            && (auth != CONNECTOR_SECRET || !self.field_value("connector-token", cx).is_empty());
        let auths = [
            (CONNECTOR_O_AUTH, "project.connectors.authOAuth"),
            (CONNECTOR_SECRET, "project.connectors.authSecret"),
            (CONNECTOR_NONE, "project.connectors.authNone"),
        ]
        .into_iter()
        .map(|(v, k)| (SharedString::from(v), t(k)))
        .collect();
        let secret_shown = {
            let s = self.field_value("connector-secret", cx);
            if s.is_empty() {
                secret_name_for(&name)
            } else {
                s
            }
        };
        v_flex()
            .gap_3()
            .pt_2()
            .border_t_1()
            .border_color(theme::line())
            .child(
                h_flex()
                    .gap_3()
                    .items_start()
                    .child(labeled(
                        t("project.connectors.nameLabel"),
                        v_flex()
                            .gap_1()
                            .child(self.input_small("connector-name"))
                            .child(if taken {
                                note_in(t("project.connectors.nameTaken"), theme::error())
                            } else if self.field_value("connector-name", cx).is_empty()
                                && !name.is_empty()
                            {
                                note(name.clone())
                            } else {
                                note(t("project.connectors.nameHint"))
                            }),
                    ))
                    .child(labeled(
                        t("project.connectors.urlLabel"),
                        v_flex()
                            .gap_1()
                            .child(self.input_small("connector-url"))
                            .child(note(t("project.connectors.urlHint"))),
                    )),
            )
            .when(preset.is_none(), |el| {
                el.child(segmented(
                    "connector-auth",
                    auths,
                    Some(auth),
                    false,
                    std::rc::Rc::new(cx.listener(|this, v: &str, _, cx| {
                        this.settings_page.connectors.auth = v.to_string();
                        cx.notify();
                    })),
                ))
            })
            .when_some(preset.and_then(|p| p.secret), |el, _| {
                el.child(note(t("project.connectors.figma.why")))
            })
            .when(auth == CONNECTOR_SECRET, |el| {
                el.child(labeled(
                    if preset.is_some_and(|p| p.secret.is_some()) {
                        t("project.connectors.figma.tokenLabel")
                    } else {
                        t("project.connectors.tokenLabel")
                    },
                    v_flex()
                        .gap_1()
                        .child(self.input_small("connector-token"))
                        .child(note(t("project.connectors.tokenHintWide"))),
                ))
                .child(
                    h_flex()
                        .gap_3()
                        .child(labeled(
                            t("project.connectors.secretLabel"),
                            v_flex()
                                .gap_1()
                                .child(self.input_small("connector-secret"))
                                .when(self.field_value("connector-secret", cx).is_empty(), |el| {
                                    el.child(note(secret_shown.clone()))
                                }),
                        ))
                        .child(labeled(
                            t("project.connectors.headerLabel"),
                            v_flex()
                                .gap_1()
                                .child(self.input_small("connector-header"))
                                .when(
                                    self.field_value("connector-header", cx)
                                        .eq_ignore_ascii_case("authorization"),
                                    |el| el.child(note(t("project.connectors.headerHint"))),
                                ),
                        )),
                )
            })
            .child(
                h_flex()
                    .gap_3()
                    .when_some(preset.and_then(|p| p.secret), |el, s| {
                        let url = s.2;
                        el.child(link(
                            "connector-how",
                            t("project.connectors.howToMake"),
                            move |_, _, cx| cx.open_url(url),
                        ))
                    })
                    .child(
                        div()
                            .flex_1()
                            .min_w_0()
                            .when(auth == CONNECTOR_O_AUTH, |el| {
                                el.child(note(t("project.connectors.browserOpens")))
                            }),
                    )
                    .child(button(
                        "connector-add",
                        if auth == CONNECTOR_O_AUTH {
                            t("project.connectors.addAndConnect")
                        } else {
                            t("common.add")
                        },
                        Btn::Primary,
                        !ready || c.busy,
                        cx.listener(|this, _: &ClickEvent, _, cx| this.add_connector(cx)),
                    )),
            )
            .into_any_element()
    }

    fn connector_row(&self, x: &Connector, cx: &mut Context<Self>) -> AnyElement {
        let c = &self.settings_page.connectors;
        let (label, color) = status(x);
        let oauth = x.auth == CONNECTOR_O_AUTH;
        let waiting = x.status == CONNECTOR_CONNECTING;
        let removing = c.removing.as_deref() == Some(x.name.as_str());
        let mut facts = Vec::new();
        if let Some(tok) = token_line(x) {
            facts.push(tok);
        }
        if x.auth == CONNECTOR_NONE {
            facts.push(t("project.connectors.publicServer"));
        }
        if !x.issuer.is_empty() {
            facts.push(tv(
                "project.connectors.signedInWith",
                &[("host", (&host_of(&x.issuer)).into())],
            ));
        }
        if let Some(at) = x
            .connected_at
            .as_ref()
            .filter(|_| x.status == CONNECTOR_CONNECTED)
        {
            facts.push(tv(
                "project.connectors.since",
                &[("when", (&time_ago(at)).into())],
            ));
        }
        if !x.overrides.is_empty() {
            facts.push(tv(
                "project.connectors.overridden",
                &[("count", x.overrides.len().into())],
            ));
        }
        let (give, connect, disconnect, remove) =
            (x.clone(), x.name.clone(), x.name.clone(), x.name.clone());
        v_flex()
            .id(SharedString::from(format!("connector-{}", x.name)))
            .gap_1p5()
            .px_3()
            .py_2p5()
            .rounded_lg()
            .border_1()
            .border_color(theme::line())
            .child(
                h_flex()
                    .flex_wrap()
                    .gap_2()
                    .child(
                        div()
                            .font_family(crate::theme::mono())
                            .text_sm()
                            .text_color(theme::primary())
                            .child(x.name.clone()),
                    )
                    .child(badge(label, color))
                    .when(!x.enabled, |el| {
                        el.child(badge(t("project.connectors.off"), theme::muted()))
                    })
                    .child(
                        div()
                            .flex_1()
                            .min_w_0()
                            .truncate()
                            .child(mono(x.url.clone())),
                    )
                    .child(toggle(
                        SharedString::from(format!("connector-give-{}", x.name)),
                        x.enabled,
                        c.busy,
                        cx.listener(move |this, _: &ClickEvent, _, cx| {
                            this.give_connector(&give, !give.enabled, cx)
                        }),
                    ))
                    .when(oauth && !waiting && x.status != CONNECTOR_CONNECTED, |el| {
                        el.child(button(
                            SharedString::from(format!("connector-connect-{}", x.name)),
                            if x.status == CONNECTOR_ERROR {
                                t("project.connectors.connectAgain")
                            } else {
                                t("project.connectors.connect")
                            },
                            Btn::Primary,
                            c.busy,
                            cx.listener(move |this, _: &ClickEvent, _, cx| {
                                this.connect_connector(connect.clone(), cx)
                            }),
                        ))
                    })
                    .when(
                        oauth && (x.status == CONNECTOR_CONNECTED || waiting),
                        |el| {
                            el.child(button(
                                SharedString::from(format!("connector-disconnect-{}", x.name)),
                                if waiting {
                                    t("common.cancel")
                                } else {
                                    t("project.connectors.disconnect")
                                },
                                Btn::Ghost,
                                c.busy,
                                cx.listener(move |this, _: &ClickEvent, _, cx| {
                                    let path =
                                        format!("/v1/connectors/{}/disconnect", enc(&disconnect));
                                    this.connector_call(Method::POST, path, None, cx, |_, _, _| {})
                                }),
                            ))
                        },
                    )
                    .child(button(
                        SharedString::from(format!("connector-remove-{}", x.name)),
                        t("common.remove"),
                        Btn::Ghost,
                        false,
                        cx.listener(move |this, _: &ClickEvent, _, cx| {
                            this.settings_page.connectors.removing = Some(remove.clone());
                            cx.notify();
                        }),
                    )),
            )
            .when(!x.error.is_empty(), |el| {
                el.child(note_in(x.error.clone(), theme::error()))
            })
            .when(!facts.is_empty(), |el| {
                el.child(
                    div()
                        .text_xs()
                        .text_color(theme::faint())
                        .child(facts.join(" ")),
                )
            })
            .when(removing, |el| {
                let name = x.name.clone();
                el.child(ask(
                    "connector-remove",
                    tv(
                        "project.connectors.removeTitle",
                        &[("name", (&x.name).into())],
                    ),
                    t("project.connectors.removeDescriptionWide"),
                    t("common.remove"),
                    true,
                    c.busy,
                    cx.listener(move |this, _: &ClickEvent, _, cx| {
                        let n = name.clone();
                        let path = format!("/v1/connectors/{}", enc(&n));
                        this.connector_call(Method::DELETE, path, None, cx, move |this, _, _| {
                            let c = &mut this.settings_page.connectors;
                            c.removing = None;
                            c.said =
                                Some(tv("project.connectors.removed", &[("name", (&n).into())]));
                        })
                    }),
                    cx.listener(|this, _: &ClickEvent, _, cx| {
                        this.settings_page.connectors.removing = None;
                        cx.notify();
                    }),
                ))
            })
            .into_any_element()
    }

    fn connector_import(&self, cx: &mut Context<Self>) -> AnyElement {
        let c = &self.settings_page.connectors;
        let scan = match &c.scan {
            Some(Ok(s)) => Some(s),
            _ => None,
        };
        let found = scan.map(|s| s.candidates.as_slice()).unwrap_or_default();
        let importable: Vec<String> = found
            .iter()
            .filter(|x| x.problem.is_empty() && x.existing.is_empty() && !x.needs_value)
            .map(|x| x.url.clone())
            .collect();
        let picked = c.picked.len();
        card("connectors-import")
            .gap_3()
            .child(title(t("project.connectors.importTitle")))
            .child(note(t("project.connectors.importDescription")))
            .child(note(format!(
                "{}: {}",
                t("project.connectors.importScope"),
                t("project.connectors.importScopeWide")
            )))
            .when(c.scan.is_none(), |el| {
                el.child(note(t("project.connectors.importLooking")))
            })
            .when_some(c.scan.as_ref().and_then(|r| r.as_ref().err()), |el, e| {
                el.child(note_in(e.clone(), theme::error()))
            })
            .children(scan.into_iter().flat_map(|s| &s.unreadable).map(|u| {
                note_in(
                    tv(
                        "project.connectors.importUnreadable",
                        &[
                            ("tool", (&u.tool).into()),
                            ("path", (&u.path).into()),
                            ("error", (&u.error).into()),
                        ],
                    ),
                    theme::starting(),
                )
            }))
            .when(scan.is_some() && found.is_empty(), |el| {
                el.child(note(t("project.connectors.importNone")))
            })
            .when(!importable.is_empty(), |el| {
                el.child(
                    h_flex()
                        .gap_3()
                        .child(link(
                            "connectors-pick-all",
                            t("project.connectors.importSelectAll"),
                            cx.listener(move |this, _: &ClickEvent, _, cx| {
                                this.settings_page.connectors.picked =
                                    importable.iter().cloned().collect();
                                cx.notify();
                            }),
                        ))
                        .child(link(
                            "connectors-pick-none",
                            t("project.connectors.importSelectNone"),
                            cx.listener(|this, _: &ClickEvent, _, cx| {
                                this.settings_page.connectors.picked.clear();
                                cx.notify();
                            }),
                        )),
                )
            })
            .children(found.iter().map(|x| {
                let problem = match x.problem.as_str() {
                    "" => None,
                    CONNECTOR_PROBLEM_STDIO => Some(tv(
                        "project.connectors.importProblemStdio",
                        &[("command", (&x.command).into())],
                    )),
                    CONNECTOR_PROBLEM_SSE => Some(t("project.connectors.importProblemSse")),
                    CONNECTOR_PROBLEM_VARIABLE => {
                        Some(t("project.connectors.importProblemVariable"))
                    }
                    _ => Some(t("project.connectors.importProblemUrl")),
                };
                let blocked = problem.is_some() || !x.existing.is_empty() || x.needs_value;
                let url = x.url.clone();
                let on = c.picked.contains(&x.url);
                let words = v_flex()
                    .gap_0p5()
                    .child(h_flex().gap_2().child(mono(x.name.clone())).when(
                        !x.existing.is_empty(),
                        |el| {
                            el.child(badge(
                                tv(
                                    "project.connectors.importExisting",
                                    &[("name", (&x.existing).into())],
                                ),
                                theme::accent(),
                            ))
                        },
                    ))
                    .when(!x.url.is_empty(), |el| el.child(mono(x.url.clone())))
                    .child(match &problem {
                        Some(p) => note_in(p.clone(), theme::starting()),
                        None if x.needs_value => note(tv(
                            "project.connectors.importNeedsValue",
                            &[
                                ("header", (&x.header).into()),
                                ("variable", (&x.variable).into()),
                            ],
                        )),
                        None if !x.header.is_empty() => note(tv(
                            "project.connectors.importHeader",
                            &[
                                ("header", (&x.header).into()),
                                ("secret", (&x.secret).into()),
                            ],
                        )),
                        None => note(t("project.connectors.importOAuth")),
                    });
                check(
                    SharedString::from(format!("connector-cand-{}", x.url)),
                    on,
                    blocked,
                    words,
                    cx.listener(move |this, _: &ClickEvent, _, cx| {
                        let p = &mut this.settings_page.connectors.picked;
                        if !p.remove(&url) {
                            p.insert(url.clone());
                        }
                        cx.notify();
                    }),
                )
            }))
            .child(
                h_flex()
                    .gap_2()
                    .child(button(
                        "connectors-import-go",
                        tv(
                            "project.connectors.importCount",
                            &[("count", picked.into())],
                        ),
                        Btn::Primary,
                        picked == 0 || c.busy,
                        cx.listener(|this, _: &ClickEvent, _, cx| this.import_connectors(cx)),
                    ))
                    .child(button(
                        "connectors-import-cancel",
                        t("common.cancel"),
                        Btn::Ghost,
                        false,
                        cx.listener(|this, _: &ClickEvent, _, cx| {
                            this.settings_page.connectors.importing = false;
                            cx.notify();
                        }),
                    )),
            )
            .into_any_element()
    }
}

#[cfg(test)]
mod tests {
    use super::{connector_name, connector_url, secret_name_for};

    #[test]
    fn names_come_from_urls_as_connectors_ts_makes_them() {
        assert_eq!(connector_name("https://mcp.notion.com/mcp"), "notion");
        assert_eq!(connector_name("https://api.acme.co.uk/mcp"), "acme");
        assert_eq!(connector_name("https://localhost:8080/mcp"), "");
        assert_eq!(
            connector_url("mcp.acme.dev/mcp"),
            "https://mcp.acme.dev/mcp"
        );
        assert_eq!(secret_name_for("my-acme"), "MY_ACME_TOKEN");
        assert_eq!(secret_name_for("1x"), "_1X_TOKEN");
    }
}
