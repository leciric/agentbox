//! The panel's views beyond the machine's own (RightPanel.tsx): an agent's
//! Browser (v2/PanelBrowser.tsx: a port of its machine through the daemon's
//! preview proxy, the ports its own Chromium has pages open on offered
//! first), its Android emulator (AndroidTab.tsx: start, stop, screenshot,
//! record, logcat, and its screen live), the element picker on its desktop
//! (PickElement.tsx), and the switch's + for the views it leaves out until
//! there's something in them (MoreViews, lib/agentTabs.ts).
use std::time::Instant;

use agentbox_api::{
    AndroidStatus, ElementPick, ElementPickResult, FleetAgent, MediaItem, PreviewInfo,
};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::button::{Button, ButtonVariants as _};
use gpui_component::input::{Input, InputEvent, InputState};
use gpui_component::menu::{DropdownMenu as _, PopupMenuItem};
use gpui_component::tooltip::Tooltip;
use gpui_component::{Sizable as _, h_flex, v_flex};

use super::AppView;
use super::controls::{self, Btn};
use super::panel::PView;
use crate::theme;
use crate::toasts;

pub struct Views {
    /// The Browser's port box, and the port shown (empty: its first page's).
    pub port: Entity<InputState>,
    shown_port: String,
    reloads: u64,
    /// The preview proxy's address, once read ("" when it's off).
    preview: Option<String>,
    /// The open agent's emulator, by its ref.
    pub android: Option<(String, AndroidStatus)>,
    android_started: Option<Instant>,
    android_busy: bool,
    /// The open agent's device stream, while its emulator has booted.
    #[cfg(feature = "video")]
    pub device: Option<(String, Entity<crate::device::DeviceView>)>,
    pub device_control: bool,
    /// The element picker runs on this agent's page.
    pub picking: Option<String>,
}

impl Views {
    pub fn new(window: &mut Window, cx: &mut Context<AppView>) -> (Self, Subscription) {
        let port = cx.new(|cx| InputState::new(window, cx).placeholder("3000"));
        let sub = cx.subscribe_in(
            &port,
            window,
            |this: &mut AppView, _, ev: &InputEvent, _, cx| {
                if let InputEvent::PressEnter { .. } = ev {
                    this.browser_go(cx);
                }
            },
        );
        (
            Self {
                port,
                shown_port: String::new(),
                reloads: 0,
                preview: None,
                android: None,
                android_started: None,
                android_busy: false,
                #[cfg(feature = "video")]
                device: None,
                device_control: false,
                picking: None,
            },
            sub,
        )
    }

    /// forget drops what was shown of the agent before.
    pub fn forget(&mut self, window: &mut Window, cx: &mut Context<AppView>) {
        self.shown_port.clear();
        self.android = None;
        self.android_started = None;
        #[cfg(feature = "video")]
        {
            self.device = None;
        }
        self.device_control = false;
        self.port.update(cx, |s, cx| s.set_value("", window, cx));
    }
}

/// A port of the agent's machine its Chromium has a page open on.
pub struct BrowserPort {
    pub port: String,
    pub path: String,
    pub title: String,
    pub url: String,
}

/// browser_ports is lib/rightPanel.ts's browserPorts: one per port, of the
/// pages on the machine's own localhost.
pub fn browser_ports(pages: &[agentbox_api::BrowserPage]) -> Vec<BrowserPort> {
    let mut out: Vec<BrowserPort> = Vec::new();
    for p in pages {
        let Some((scheme, rest)) = p.url.split_once("://") else {
            continue;
        };
        if scheme != "http" && scheme != "https" {
            continue;
        }
        let (authority, path) = match rest.find(['/', '?', '#']) {
            Some(i) => (&rest[..i], &rest[i..]),
            None => (rest, "/"),
        };
        let (host, port) = if let Some(h) = authority.strip_prefix('[') {
            match h.split_once(']') {
                Some((h, p)) => (format!("[{h}]"), p.trim_start_matches(':').to_string()),
                None => continue,
            }
        } else {
            match authority.rsplit_once(':') {
                Some((h, p)) => (h.to_string(), p.to_string()),
                None => (authority.to_string(), String::new()),
            }
        };
        if !["localhost", "127.0.0.1", "0.0.0.0", "[::1]"].contains(&host.as_str()) {
            continue;
        }
        let port = if port.is_empty() {
            if scheme == "https" { "443" } else { "80" }.to_string()
        } else {
            port
        };
        if out.iter().any(|x| x.port == port) {
            continue;
        }
        out.push(BrowserPort {
            port,
            path: if path.is_empty() {
                "/".into()
            } else {
                path.into()
            },
            title: p.title.clone(),
            url: p.url.clone(),
        });
    }
    out
}

/// preview_url is a port of an agent's machine through the preview proxy.
pub fn preview_url(a: &FleetAgent, port: &str, addr: &str, path: &str) -> String {
    let p = addr.rsplit(':').next().unwrap_or(addr);
    let path = if path.starts_with('/') {
        path.to_string()
    } else {
        format!("/{path}")
    };
    format!("http://{port}.{}.{}.localhost:{p}{path}", a.name, a.project)
}

/// picked_text is lib/picks.ts's pickedText: the element, for the message.
/// pick_image_name is the name a pick's crop goes as (pickImageName).
pub fn pick_image_name(n: usize, mime: &str) -> String {
    format!(
        "element-{n}.{}",
        if mime == "image/jpeg" { "jpg" } else { "png" }
    )
}

pub fn picked_text(n: usize, from: &str, el: &ElementPick) -> String {
    let b = &el.r#box;
    let mut lines = vec![
        format!(
            "<picked-element n=\"{n}\" from=\"{from}\"{}>",
            el.image
                .as_ref()
                .map(|i| format!(" image=\"{}\"", pick_image_name(n, &i.mime_type)))
                .unwrap_or_default()
        ),
        format!(
            "Page: {}{}",
            el.url,
            if el.title.is_empty() {
                String::new()
            } else {
                format!(" ({})", el.title)
            }
        ),
        format!("Selector: {}", el.selector),
        format!(
            "Box: {}×{} at {},{} in a {}×{} viewport",
            b.width, b.height, b.x, b.y, el.viewport.width, el.viewport.height
        ),
    ];
    if !el.text.is_empty() {
        lines.push(format!("Text: {}", el.text));
    }
    if !el.styles.is_empty() {
        lines.push(format!(
            "Styles: {}",
            el.styles
                .iter()
                .map(|s| format!("{}: {}", s.name, s.value))
                .collect::<Vec<_>>()
                .join("; ")
        ));
    }
    lines.extend([
        "HTML:".into(),
        "```html".into(),
        el.html.clone(),
        "```".into(),
        "</picked-element>".into(),
    ]);
    lines.join("\n")
}

/// pick_label is lib/picks.ts's pickLabel: button.primary, say.
pub fn pick_label(el: &ElementPick) -> String {
    let last = el.selector.rsplit(" > ").next().unwrap_or("");
    let mark = last
        .char_indices()
        .skip(1)
        .find(|(_, c)| matches!(c, '#' | '.'))
        .map(|(i, _)| {
            let rest = &last[i..];
            let end = rest[1..]
                .find(['.', '#', ':', '[', ' '])
                .map_or(rest.len(), |e| e + 1);
            rest[..end].to_string()
        })
        .or_else(|| last.starts_with(['#', '.']).then(|| last.to_string()))
        .unwrap_or_default();
    let tag = if el.tag.is_empty() {
        last.split(['.', '#', ':', '['])
            .next()
            .unwrap_or("")
            .to_string()
    } else {
        el.tag.clone()
    };
    format!("{tag}{mark}").replace('\\', "")
}

impl AppView {
    /// agent_views is an agent's switch (viewsOf) and what its + offers
    /// (hiddenTabs): Browser, Android and Evidence once there's something in
    /// them, or while open.
    pub(super) fn agent_views(&self, a: &FleetAgent, view: PView) -> (Vec<PView>, Vec<PView>) {
        let project = self.projects.iter().find(|p| p.name == a.project);
        let android_status = self
            .views
            .android
            .as_ref()
            .filter(|(r, _)| *r == a.r#ref)
            .map(|(_, s)| s);
        let android =
            project.is_some_and(|p| p.android) || android_status.is_some_and(|s| s.running);
        let browser = self.panel.browser_status().is_some_and(|b| {
            !browser_ports(&b.pages).is_empty()
                || (b.running
                    && b.pages
                        .iter()
                        .any(|p| !p.url.is_empty() && p.url != "about:blank"))
        });
        let evidence = self.panel.has_media(&a.r#ref);
        let mut shown = Vec::new();
        let mut hidden = Vec::new();
        for v in PView::ALL {
            if v == PView::Android && !android {
                continue;
            }
            let has = match v {
                PView::Browser => browser,
                PView::Android => android_status.is_some_and(|s| s.running),
                PView::Evidence => evidence,
                _ => true,
            };
            if has || v == view {
                shown.push(v);
            } else {
                hidden.push(v);
            }
        }
        (shown, hidden)
    }

    /// render_more_views is the + after the switch.
    pub(super) fn render_more_views(
        &self,
        r: &str,
        hidden: Vec<PView>,
        cx: &mut Context<Self>,
    ) -> AnyElement {
        if hidden.is_empty() {
            return div().into_any_element();
        }
        let view = cx.entity().downgrade();
        let r = r.to_string();
        Button::new("panel-more-views")
            .ghost()
            .small()
            .label("+")
            .tooltip(t("agent.view.openTab"))
            .dropdown_menu_with_anchor(Anchor::TopRight, move |mut menu, _, _| {
                for v in hidden.iter().copied() {
                    let label = match v {
                        PView::Browser => t("panel.more.browser"),
                        PView::Android => t("agent.view.openAndroid"),
                        _ => v.label(),
                    };
                    let (view, r) = (view.clone(), r.clone());
                    menu = menu.item(
                        PopupMenuItem::new(format!("{}  {label}", v.glyph())).on_click(
                            move |_, window, cx| {
                                view.update(cx, |this, cx| {
                                    this.set_view(&r, v, window, cx);
                                    if v == PView::Android {
                                        this.start_android(cx);
                                    }
                                })
                                .ok();
                            },
                        ),
                    );
                }
                menu
            })
            .into_any_element()
    }

    pub(super) fn load_preview(&mut self, cx: &mut Context<Self>) {
        if self.views.preview.is_some() {
            return;
        }
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let addr = client
                .get::<PreviewInfo>("/v1/preview")
                .await
                .map(|p| p.addr)
                .unwrap_or_default();
            this.update(cx, |this, cx| {
                this.views.preview = Some(addr);
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn browser_go(&mut self, cx: &mut Context<Self>) {
        let port: String = self
            .views
            .port
            .read(cx)
            .value()
            .chars()
            .filter(char::is_ascii_digit)
            .take(5)
            .collect();
        if !port.is_empty() {
            self.views.shown_port = port;
        }
        self.views.reloads += 1;
        cx.notify();
    }

    /// render_browser is PanelBrowser: the port box, the pages' ports, the page.
    pub(super) fn render_browser(
        &self,
        a: &FleetAgent,
        overlaid: bool,
        cx: &mut Context<Self>,
    ) -> AnyElement {
        let ports = self
            .panel
            .browser_status()
            .map(|b| browser_ports(&b.pages))
            .unwrap_or_default();
        let current = if self.views.shown_port.is_empty() {
            ports.first().map(|p| p.port.clone()).unwrap_or_default()
        } else {
            self.views.shown_port.clone()
        };
        let addr = self.views.preview.clone().unwrap_or_default();
        let page = ports.iter().find(|p| p.port == current);
        let url = if !addr.is_empty() && !current.is_empty() {
            preview_url(a, &current, &addr, page.map_or("/", |p| p.path.as_str()))
        } else {
            String::new()
        };
        let reload_tip: SharedString = t("panel.browser.reload").into();
        let out_tip: SharedString = t("panel.browser.openOutside").into();
        let open_url = url.clone();
        let bar = h_flex()
            .flex_none()
            .gap_1p5()
            .px_2()
            .py_1p5()
            .border_b_1()
            .border_color(theme::border())
            .child(
                div()
                    .id("browser-reload")
                    .px_1p5()
                    .rounded_md()
                    .text_color(theme::muted())
                    .cursor_pointer()
                    .hover(|s| s.bg(theme::hover()).text_color(theme::fg()))
                    .tooltip(move |w, cx| Tooltip::new(reload_tip.clone()).build(w, cx))
                    .on_click(cx.listener(|this, _: &ClickEvent, _, cx| this.browser_go(cx)))
                    .child("↻"),
            )
            .child(
                h_flex()
                    .flex_1()
                    .min_w_0()
                    .gap_1()
                    .px_2()
                    .h(px(28.))
                    .rounded_md()
                    .border_1()
                    .border_color(theme::border())
                    .bg(theme::bg())
                    .text_xs()
                    .child(
                        div()
                            .flex_none()
                            .text_color(theme::faint())
                            .child(t("panel.browser.port")),
                    )
                    .child(
                        div()
                            .w(px(64.))
                            .flex_none()
                            .font_family(theme::mono())
                            .child(Input::new(&self.views.port).appearance(false).xsmall()),
                    )
                    .child(
                        div()
                            .min_w_0()
                            .flex_1()
                            .truncate()
                            .font_family(theme::mono())
                            .text_color(theme::faint())
                            .child(if url.is_empty() && !current.is_empty() {
                                format!(":{current}")
                            } else {
                                url.clone()
                            }),
                    ),
            )
            .child(
                div()
                    .id("browser-outside")
                    .px_1p5()
                    .rounded_md()
                    .text_color(theme::muted())
                    .when(url.is_empty(), |el| el.opacity(0.4))
                    .when(!url.is_empty(), |el| {
                        el.cursor_pointer()
                            .hover(|s| s.bg(theme::hover()).text_color(theme::fg()))
                            .on_click(move |_, _, cx| cx.open_url(&open_url))
                    })
                    .tooltip(move |w, cx| Tooltip::new(out_tip.clone()).build(w, cx))
                    .child("↗"),
            );
        let pages = (!ports.is_empty()).then(|| {
            h_flex()
                .flex_none()
                .gap_1()
                .px_2()
                .py_1()
                .border_b_1()
                .border_color(theme::border())
                .children(ports.iter().map(|p| {
                    let on = p.port == current;
                    let port = p.port.clone();
                    let tip: SharedString = if p.title.is_empty() {
                        p.url.clone()
                    } else {
                        p.title.clone()
                    }
                    .into();
                    h_flex()
                        .id(SharedString::from(format!("browser-page-{}", p.port)))
                        .max_w(px(192.))
                        .gap_1()
                        .px_1p5()
                        .py(px(2.))
                        .rounded_md()
                        .text_xs()
                        .when(on, |el| el.bg(theme::raised()).text_color(theme::fg()))
                        .when(!on, |el| {
                            el.text_color(theme::muted())
                                .cursor_pointer()
                                .hover(|s| s.bg(theme::hover()).text_color(theme::fg()))
                        })
                        .tooltip(move |w, cx| Tooltip::new(tip.clone()).build(w, cx))
                        .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                            this.views.shown_port = port.clone();
                            cx.notify();
                        }))
                        .child("◍")
                        .child(div().truncate().child(if p.title.is_empty() {
                            format!(":{}", p.port)
                        } else {
                            p.title.clone()
                        }))
                }))
        });
        let body =
            if url.is_empty() {
                v_flex()
                    .size_full()
                    .items_center()
                    .justify_center()
                    .gap_2()
                    .px_6()
                    .bg(theme::panel())
                    .text_sm()
                    .text_color(theme::muted())
                    .child(div().text_2xl().text_color(theme::faint()).child("◍"))
                    .child(div().text_center().child(
                        if self.views.preview.as_deref() == Some("") {
                            t("panel.browser.proxyOff")
                        } else {
                            t("panel.browser.none")
                        },
                    ))
                    .into_any_element()
            } else if !crate::webview::available() {
                v_flex()
                    .size_full()
                    .items_center()
                    .justify_center()
                    .gap_2()
                    .px_6()
                    .text_sm()
                    .text_color(theme::muted())
                    .child(t("panel.browser.openOutside"))
                    .child(
                        div()
                            .font_family(theme::mono())
                            .text_xs()
                            .child(url.clone()),
                    )
                    .into_any_element()
            } else {
                div()
                    .size_full()
                    .bg(gpui::white())
                    .child(crate::webview::page(
                        format!("browser-{}", a.r#ref),
                        format!("{url}#{}", self.views.reloads),
                        !overlaid,
                    ))
                    .into_any_element()
            };
        v_flex()
            .size_full()
            .child(bar)
            .children(pages)
            .child(div().flex_1().min_h_0().child(body))
            .into_any_element()
    }

    // ——— Android ———

    pub(super) fn load_android(&mut self, cx: &mut Context<Self>) {
        let Some(a) = self.open_agent() else { return };
        if a.state != "running" {
            return;
        }
        let (p, n, r) = (a.project.clone(), a.name.clone(), a.r#ref.clone());
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let s = client
                .get::<AndroidStatus>(&format!("/v1/agents/{p}/{n}/android"))
                .await;
            this.update(cx, |this, cx| {
                if let Ok(s) = s
                    && this.open_agent().is_some_and(|a| a.r#ref == r)
                {
                    this.views.android = Some((r, s));
                    this.sync_device(cx);
                    cx.notify();
                }
            })
            .ok();
        })
        .detach();
    }

    /// sync_device connects the emulator's screen while Android shows it booted.
    pub(super) fn sync_device(&mut self, cx: &mut Context<Self>) {
        #[cfg(feature = "video")]
        {
            let want = self.open_agent().and_then(|a| {
                let booted = self
                    .views
                    .android
                    .as_ref()
                    .is_some_and(|(r, s)| *r == a.r#ref && s.booted);
                (booted
                    && self.panel_shows_agent()
                    && self.panel.view_of(&a.r#ref) == PView::Android)
                    .then(|| a.r#ref.clone())
            });
            match want {
                Some(r) if self.views.device.as_ref().is_none_or(|(d, _)| *d != r) => {
                    let (p, n) = super::agent::split(&r);
                    let path = format!("/v1/agents/{p}/{n}/android/stream");
                    let client = self.client.clone();
                    let control = self.views.device_control;
                    let v = cx.new(|cx| crate::device::DeviceView::new(client, path, control, cx));
                    self.views.device = Some((r, v));
                }
                None => self.views.device = None,
                _ => {}
            }
        }
        let _ = cx;
    }

    pub(super) fn start_android(&mut self, cx: &mut Context<Self>) {
        let Some(a) = self.open_agent() else { return };
        if a.state != "running" || self.views.android_busy {
            return;
        }
        let (p, n, r) = (a.project.clone(), a.name.clone(), a.r#ref.clone());
        let client = self.client.clone();
        self.views.android_busy = true;
        self.views.android_started = Some(Instant::now());
        cx.spawn(async move |this, cx| {
            // The request lasts until Android has booted; meanwhile the status shows it starting.
            let path = format!("/v1/agents/{p}/{n}/android/start");
            let body = serde_json::json!({});
            let res = client.post::<_, AndroidStatus>(&path, &body).await;
            this.update(cx, |this, cx| {
                this.views.android_busy = false;
                this.views.android_started = None;
                match res {
                    Ok(s) => {
                        this.views.android = Some((r.clone(), s));
                        this.sync_device(cx);
                    }
                    Err(e) => toasts::error(cx, e.to_string()),
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    pub(super) fn stop_android(&mut self, cx: &mut Context<Self>) {
        let Some(a) = self.open_agent() else { return };
        let (p, n, r) = (a.project.clone(), a.name.clone(), a.r#ref.clone());
        let client = self.client.clone();
        self.views.android_busy = true;
        cx.spawn(async move |this, cx| {
            let res = client
                .post::<_, AndroidStatus>(
                    &format!("/v1/agents/{p}/{n}/android/stop"),
                    &serde_json::json!({}),
                )
                .await;
            this.update(cx, |this, cx| {
                this.views.android_busy = false;
                match res {
                    Ok(s) => this.views.android = Some((r, s)),
                    Err(e) => toasts::error(cx, e.to_string()),
                }
                this.sync_device(cx);
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// android_capture saves the device's screenshot or its logcat to Media.
    fn android_capture(&mut self, logs: bool, cx: &mut Context<Self>) {
        let Some(a) = self.open_agent() else { return };
        let (p, n, r) = (a.project.clone(), a.name.clone(), a.r#ref.clone());
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = if logs {
                client
                    .post::<_, MediaItem>(
                        &format!("/v1/agents/{p}/{n}/media/logs"),
                        &agentbox_api::LogsRequest {
                            android: true,
                            since: "10m".into(),
                            name: "logcat".into(),
                            ..Default::default()
                        },
                    )
                    .await
            } else {
                client
                    .post::<_, MediaItem>(
                        &format!("/v1/agents/{p}/{n}/media/screenshot"),
                        &agentbox_api::ScreenshotRequest {
                            target: "android".into(),
                            name: "android screenshot".into(),
                            ..Default::default()
                        },
                    )
                    .await
            };
            this.update(cx, |this, cx| match res {
                Ok(m) => this.saved(r, m, cx),
                Err(e) => toasts::error(cx, e.to_string()),
            })
            .ok();
        })
        .detach();
    }

    fn android_record(&mut self, cx: &mut Context<Self>) {
        let Some(a) = self.open_agent() else { return };
        let (p, n, r) = (a.project.clone(), a.name.clone(), a.r#ref.clone());
        let here = self
            .panel
            .recording_status()
            .is_some_and(|s| s.recording && s.target == "android");
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let base = format!("/v1/agents/{p}/{n}/media/record");
            if here {
                let res = client
                    .post::<_, MediaItem>(&format!("{base}/stop"), &serde_json::json!({}))
                    .await;
                this.update(cx, |this, cx| {
                    match res {
                        Ok(m) => this.saved(r, m, cx),
                        Err(e) => toasts::error(cx, e.to_string()),
                    }
                    this.load_desktop_status(cx);
                })
                .ok();
            } else {
                let res = client
                    .post::<_, agentbox_api::RecordingStatus>(
                        &format!("{base}/start"),
                        &agentbox_api::RecordRequest {
                            target: "android".into(),
                            name: "android recording".into(),
                            ..Default::default()
                        },
                    )
                    .await;
                this.update(cx, |this, cx| {
                    if let Err(e) = res {
                        toasts::error(cx, e.to_string());
                    }
                    this.load_desktop_status(cx);
                })
                .ok();
            }
        })
        .detach();
    }

    /// render_android is AndroidTab, compact: the toolbar, the device, its footer.
    pub(super) fn render_android(&self, a: &FleetAgent, cx: &mut Context<Self>) -> AnyElement {
        let status = self
            .views
            .android
            .as_ref()
            .filter(|(r, _)| *r == a.r#ref)
            .map(|(_, s)| s.clone());
        let running = a.state == "running";
        let booted = running && status.as_ref().is_some_and(|s| s.booted);
        let starting = self.views.android_started.is_some()
            || status.as_ref().is_some_and(|s| s.running && !s.booted);
        let rec = self.panel.recording_status();
        let recording = rec.as_ref().is_some_and(|s| s.recording);
        let here = rec
            .as_ref()
            .is_some_and(|s| s.recording && s.target == "android");
        let elapsed = rec
            .as_ref()
            .and_then(|r| r.started_at.as_deref())
            .filter(|_| here)
            .map(|s| (chrono::Utc::now().timestamp_millis() - crate::media::ms(s)) / 1000);
        let control = self.views.device_control;
        let dot = if booted {
            theme::running()
        } else if starting {
            theme::starting()
        } else {
            theme::faint()
        };
        let device_name = if booted {
            status
                .as_ref()
                .map(|s| s.device.clone())
                .unwrap_or_default()
        } else if starting {
            t("agent.android.starting")
        } else {
            t("agent.android.off")
        };
        let icon_btn = |id: &'static str, glyph: String, tip: String, enabled: bool| {
            let tip: SharedString = tip.into();
            h_flex()
                .id(id)
                .h(px(26.))
                .px_2()
                .gap_1()
                .rounded_md()
                .text_sm()
                .text_color(theme::muted())
                .when(!enabled, |el| el.opacity(0.4))
                .when(enabled, |el| {
                    el.cursor_pointer()
                        .hover(|s| s.bg(theme::hover()).text_color(theme::fg()))
                })
                .tooltip(move |w, cx| Tooltip::new(tip.clone()).build(w, cx))
                .child(glyph)
        };
        let view = cx.entity().downgrade();
        let menu_running = status.as_ref().is_some_and(|s| s.running);
        let can_start = running && status.as_ref().is_some_and(|s| s.available) && !starting;
        let busy = self.views.android_busy;
        let menu = Button::new("android-menu")
            .ghost()
            .small()
            .label("⋯")
            .tooltip(t("agent.android.actions"))
            .dropdown_menu_with_anchor(Anchor::TopRight, move |menu, _, _| {
                let v1 = view.clone();
                let v2 = view.clone();
                let v3 = view.clone();
                let menu = menu.item(
                    PopupMenuItem::new(t("agent.android.sendClipboard"))
                        .disabled(!booted)
                        .on_click(move |_, _, cx| {
                            let text = cx
                                .read_from_clipboard()
                                .and_then(|c| c.text())
                                .unwrap_or_default();
                            #[cfg(feature = "video")]
                            v3.update(cx, |this, cx| {
                                if let Some((_, d)) = &this.views.device {
                                    d.read(cx).send_text(&text);
                                }
                            })
                            .ok();
                            #[cfg(not(feature = "video"))]
                            let _ = (&v3, text);
                        }),
                );
                let menu = menu.item(
                    PopupMenuItem::new(t("agent.android.saveLogcat"))
                        .disabled(!booted)
                        .on_click(move |_, _, cx| {
                            v1.update(cx, |this, cx| this.android_capture(true, cx))
                                .ok();
                        }),
                );
                let menu = menu.separator();
                if menu_running {
                    menu.item(
                        PopupMenuItem::new(t("agent.android.stop"))
                            .disabled(busy)
                            .on_click(move |_, _, cx| {
                                v2.update(cx, |this, cx| this.stop_android(cx)).ok();
                            }),
                    )
                } else {
                    menu.item(
                        PopupMenuItem::new(t("agent.android.start"))
                            .disabled(!can_start)
                            .on_click(move |_, _, cx| {
                                v2.update(cx, |this, cx| this.start_android(cx)).ok();
                            }),
                    )
                }
            });
        let toolbar = h_flex()
            .flex_none()
            .h(px(44.))
            .px_3()
            .gap_1p5()
            .border_b_1()
            .border_color(theme::border())
            .child(
                h_flex()
                    .min_w_0()
                    .h(px(28.))
                    .px_3()
                    .gap_2()
                    .rounded_full()
                    .border_1()
                    .border_color(theme::border())
                    .bg(theme::bg())
                    .text_xs()
                    .text_color(if booted { theme::fg() } else { theme::muted() })
                    .child(div().size(px(6.)).rounded_full().bg(dot))
                    .child("▯")
                    .child(div().truncate().child(device_name)),
            )
            .child(div().flex_1())
            .child(
                icon_btn(
                    "android-shot",
                    "⌾".into(),
                    t("agent.android.screenshotTip"),
                    booted,
                )
                .when(booted, |el| {
                    el.on_click(
                        cx.listener(|this, _: &ClickEvent, _, cx| this.android_capture(false, cx)),
                    )
                }),
            )
            .child(
                icon_btn(
                    "android-record",
                    if here {
                        format!("■ {}", super::panel::clock(elapsed.unwrap_or(0) as f64))
                    } else {
                        "●".into()
                    },
                    if here {
                        t("agent.browser.stopRecording")
                    } else if recording {
                        t("agent.android.browserRecording")
                    } else {
                        t("agent.android.recordTip")
                    },
                    booted && (here || !recording),
                )
                .when(here, |el| {
                    el.bg(theme::error().opacity(0.15))
                        .text_color(theme::error())
                })
                .when(!here, |el| el.text_color(theme::error()))
                .when(booted && (here || !recording), |el| {
                    el.on_click(cx.listener(|this, _: &ClickEvent, _, cx| this.android_record(cx)))
                }),
            )
            .child(div().w(px(1.)).h(px(18.)).bg(theme::border()))
            .child(
                h_flex()
                    .id("android-control")
                    .gap_1p5()
                    .text_xs()
                    .text_color(if control {
                        theme::accent()
                    } else {
                        theme::muted()
                    })
                    .child(controls::toggle(
                        "android-control-switch",
                        control,
                        !booted,
                        {
                            let view = cx.entity().downgrade();
                            move |_, _, cx| {
                                view.update(cx, |this, cx| {
                                    this.views.device_control = !this.views.device_control;
                                    #[cfg(feature = "video")]
                                    if let Some((_, d)) = &this.views.device {
                                        let on = this.views.device_control;
                                        d.update(cx, |d, cx| {
                                            d.control = on;
                                            cx.notify();
                                        });
                                    }
                                    cx.notify();
                                })
                                .ok();
                            }
                        },
                    ))
                    .child(t("agent.browser.takeControl")),
            )
            .child(menu);
        let body: AnyElement =
            if booted {
                self.render_device(cx)
            } else {
                let name = if a.title.is_empty() {
                    a.name.clone()
                } else {
                    a.title.clone()
                };
                let image = status
                    .as_ref()
                    .and_then(|s| s.images.first().cloned())
                    .unwrap_or_default();
                let api = image
                    .split(';')
                    .nth(1)
                    .map(|s| s.replace("android-", "API "))
                    .unwrap_or_default();
                let mut col = v_flex()
                    .max_w(px(360.))
                    .items_center()
                    .gap_3()
                    .text_center()
                    .child(
                        div()
                            .w(px(48.))
                            .h(px(96.))
                            .rounded(px(14.))
                            .border_1()
                            .border_color(theme::border())
                            .bg(theme::raised())
                            .flex()
                            .items_center()
                            .justify_center()
                            .text_color(theme::muted())
                            .child(if starting { "…" } else { "▯" }),
                    );
                if !running {
                    col = col.child(div().text_sm().text_color(theme::muted()).child(tv(
                        "agent.view.isState",
                        &[
                            ("name", (&name).into()),
                            ("state", super::state_label(&a.state).into()),
                        ],
                    )));
                } else if starting {
                    let secs = self
                        .views
                        .android_started
                        .map(|s| s.elapsed().as_secs())
                        .unwrap_or(0);
                    col = col
                        .child(div().text_sm().child(t("agent.android.starting")))
                        .child(div().text_xs().text_color(theme::muted()).child(
                            if self.views.android_started.is_some() {
                                tv(
                                    "agent.android.bootingSoFar",
                                    &[("seconds", (secs as i64).into())],
                                )
                            } else {
                                t("agent.android.booting")
                            },
                        ));
                } else if let Some(s) = status.as_ref().filter(|s| !s.available) {
                    col = col
                        .child(
                            div()
                                .text_sm()
                                .child(format!("⚠ {}", t("agent.android.unavailable"))),
                        )
                        .child(
                            div()
                                .text_xs()
                                .text_color(theme::muted())
                                .child(s.problem.clone()),
                        )
                        .child(controls::button(
                            "android-setup",
                            t("agent.android.openSettings"),
                            Btn::Secondary,
                            false,
                            cx.listener(|this, _: &ClickEvent, w, cx| this.open_settings(w, cx)),
                        ));
                } else {
                    col = col
                        .child(div().text_sm().child(tv(
                            "agent.android.notRunning",
                            &[("name", (&a.name).into())],
                        )))
                        .child(div().text_xs().text_color(theme::muted()).child(
                            if api.is_empty() {
                                t("agent.android.explain")
                            } else {
                                tv("agent.android.explainApi", &[("api", (&api).into())])
                            },
                        ))
                        .child(controls::button(
                            "android-start",
                            format!("▶ {}", t("agent.android.start")),
                            Btn::Primary,
                            status.is_none() || busy,
                            cx.listener(|this, _: &ClickEvent, _, cx| this.start_android(cx)),
                        ));
                }
                div()
                    .size_full()
                    .flex()
                    .items_center()
                    .justify_center()
                    .p_6()
                    .child(col)
                    .into_any_element()
            };
        let gpu = status.as_ref().map(|s| (s.gpu, s.gpu_problem.clone()));
        let footer = h_flex()
            .flex_none()
            .h(px(30.))
            .px_4()
            .gap_3()
            .border_t_1()
            .border_color(theme::border())
            .text_xs()
            .text_color(theme::muted())
            .child(
                div()
                    .min_w_0()
                    .truncate()
                    .font_family(theme::mono())
                    .child(if booted {
                        status.as_ref().map(|s| s.image.clone()).unwrap_or_default()
                    } else {
                        String::new()
                    }),
            )
            .when_some(gpu.filter(|_| booted), |el, (on, problem)| {
                let tip: SharedString = if on {
                    t("agent.android.gpuTip")
                } else if !problem.is_empty() {
                    tv(
                        "agent.android.gpuFailedTip",
                        &[("problem", (&problem).into())],
                    )
                } else {
                    t("agent.android.softwareTip")
                }
                .into();
                el.child(
                    div()
                        .id("android-gpu")
                        .flex_none()
                        .when(!on && !problem.is_empty(), |el| {
                            el.text_color(theme::starting())
                        })
                        .tooltip(move |w, cx| Tooltip::new(tip.clone()).build(w, cx))
                        .child(if on {
                            t("agent.android.gpu")
                        } else {
                            t("agent.android.software")
                        }),
                )
            })
            .child(div().flex_1())
            .child(div().flex_none().child(if !booted {
                String::new()
            } else if control {
                t("agent.android.controlOn")
            } else {
                t("agent.android.viewOnly")
            }));
        v_flex()
            .size_full()
            .child(toolbar)
            .child(div().flex_1().min_h_0().child(body))
            .child(footer)
            .into_any_element()
    }

    fn render_device(&self, _cx: &mut Context<Self>) -> AnyElement {
        #[cfg(feature = "video")]
        if let Some((_, d)) = &self.views.device {
            return div()
                .relative()
                .size_full()
                .child(div().absolute().inset_0().p_4().child(d.clone()))
                .into_any_element();
        }
        v_flex()
            .size_full()
            .items_center()
            .justify_center()
            .text_sm()
            .text_color(theme::muted())
            .child(t("agent.browser.connecting"))
            .into_any_element()
    }

    // ——— The element picker ———

    /// toggle_pick runs the picker on the agent's page, or stops it.
    pub(super) fn toggle_pick(&mut self, cx: &mut Context<Self>) {
        let Some(a) = self.open_agent() else { return };
        let (p, n, r) = (a.project.clone(), a.name.clone(), a.r#ref.clone());
        let client = self.client.clone();
        if self.views.picking.is_some() {
            cx.spawn(async move |_, _| {
                let _ = client
                    .raw(
                        agentbox_api::client::Method::POST,
                        &format!("/v1/agents/{p}/{n}/browser/pick/cancel"),
                        None,
                    )
                    .await;
            })
            .detach();
            return;
        }
        self.views.picking = Some(r.clone());
        // While it runs the view takes the pointer, as with Take control.
        if let Some(d) = &self.desktop {
            d.update(cx, |d, cx| {
                d.control = true;
                cx.notify();
            });
        }
        cx.spawn(async move |this, cx| {
            let res = client
                .post::<_, ElementPickResult>(
                    &format!("/v1/agents/{p}/{n}/browser/pick"),
                    &agentbox_api::ElementPickRequest {
                        hint: t("pick.hint"),
                    },
                )
                .await;
            this.update(cx, |this, cx| {
                this.views.picking = None;
                if let Some(d) = &this.desktop {
                    d.update(cx, |d, cx| {
                        d.control = false;
                        cx.notify();
                    });
                }
                match res {
                    Ok(ElementPickResult { element: Some(el) }) => {
                        this.send_pick(&r, el, cx);
                    }
                    Ok(_) => {}
                    Err(e) => toasts::error(cx, e.to_string()),
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    /// send_pick adds the element to the open chat's message, not sent.
    fn send_pick(&mut self, from: &str, el: ElementPick, cx: &mut Context<Self>) {
        let Some(chat) = self.chat.clone() else {
            return;
        };
        let label = pick_label(&el);
        chat.update(cx, |c, cx| {
            let n = c.draft_text(cx).matches("<picked-element ").count() + 1;
            c.append_draft(&picked_text(n, from, &el), cx);
            // Its crop goes with it, as a picture the AI tool sees.
            if let Some(i) = &el.image {
                use base64::Engine as _;
                if let Ok(bytes) = base64::engine::general_purpose::STANDARD.decode(&i.data) {
                    c.attach(
                        pick_image_name(n, &i.mime_type),
                        i.mime_type.clone(),
                        bytes,
                        cx,
                    );
                }
            }
        });
        toasts::info(cx, tv("pick.added", &[("name", (&label).into())]));
    }

    /// render_pick_button is PickButton, compact, for the desktop's toolbar.
    pub(super) fn render_pick_button(
        &self,
        a: &FleetAgent,
        connected: bool,
        cx: &mut Context<Self>,
    ) -> AnyElement {
        let picking = self.views.picking.as_deref() == Some(a.r#ref.as_str());
        let has_page = self
            .panel
            .browser_status()
            .is_some_and(|b| b.running && !b.pages.is_empty());
        let enabled = picking || (connected && has_page);
        let tip: SharedString = t(if picking {
            "pick.cancelTip"
        } else {
            "pick.tip"
        })
        .into();
        div()
            .id("desktop-pick")
            .px_1p5()
            .rounded_md()
            .text_sm()
            .text_color(if picking {
                theme::accent()
            } else {
                theme::muted()
            })
            .when(picking, |el| el.bg(theme::accent().opacity(0.18)))
            .when(!enabled, |el| el.opacity(0.4))
            .when(enabled, |el| {
                el.cursor_pointer()
                    .hover(|s| s.bg(theme::hover()).text_color(theme::fg()))
                    .on_click(cx.listener(|this, _: &ClickEvent, _, cx| this.toggle_pick(cx)))
            })
            .tooltip(move |w, cx| Tooltip::new(tip.clone()).build(w, cx))
            .child("⬚")
            .into_any_element()
    }
}

#[cfg(test)]
mod tests {
    use super::{ElementPick, browser_ports, pick_label, picked_text};

    #[test]
    fn browser_ports_are_the_machine_own_one_each() {
        let page = |url: &str| agentbox_api::BrowserPage {
            url: url.into(),
            ..Default::default()
        };
        let ports = browser_ports(&[
            page("http://localhost:5173/settings?x=1"),
            page("http://localhost:5173/other"),
            page("https://example.com/"),
            page("http://127.0.0.1:8080"),
            page("about:blank"),
            page("https://[::1]/a"),
        ]);
        let got: Vec<(&str, &str)> = ports
            .iter()
            .map(|p| (p.port.as_str(), p.path.as_str()))
            .collect();
        assert_eq!(
            got,
            [("5173", "/settings?x=1"), ("8080", "/"), ("443", "/a")]
        );
    }

    #[test]
    fn a_pick_is_labelled_by_its_tag_and_class() {
        let el = ElementPick {
            selector: "main > section.card > button.primary.big".into(),
            tag: "button".into(),
            ..Default::default()
        };
        assert_eq!(pick_label(&el), "button.primary");
        let text = picked_text(1, "demo/agent-01", &el);
        assert!(text.starts_with("<picked-element n=\"1\" from=\"demo/agent-01\">"));
        assert!(text.ends_with("</picked-element>"));
    }
}
