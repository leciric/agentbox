//! Settings → Phone (PhoneSettings.tsx): chatting from a phone. Serving the
//! phone page on this computer's network (PATCH /v1/lan), pairing a phone
//! with a QR code (POST /v1/lan/pairings: the daemon gives the code's rows,
//! drawn here module by module), reaching it from anywhere through a tunnel,
//! and the paired phones, each with what it may do (PATCH
//! /v1/lan/phones/{id}), what it did (GET …/audit), and unpairing (DELETE).
//! The list is read again every three seconds while the section is open, so
//! a phone that pairs shows up.
use std::time::Duration;

use agentbox_api::client::Method;
use agentbox_api::{
    LANAudit, LANPairing, LANPhone, LANStatus, UpdateLANPhoneRequest, UpdateLANRequest,
};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::{h_flex, v_flex};

use super::super::AppView;
use super::super::controls::{Btn, button, labeled, note_in, toggle};
use super::kit::note;
use super::kit::{ask, enc, link, local_date, local_date_time, mono, plain};
use super::{Entry, Group, entry, group};
use crate::theme;

const SCOPES: [&str; 3] = ["read", "chat", "answer"];
const TUNNEL_MODES: [&str; 3] = ["hub", "quick", "named"];

#[derive(Default)]
pub(super) struct Phone {
    lan: Option<Result<LANStatus, String>>,
    pairing: Option<LANPairing>,
    error: Option<String>,
    said: Option<String>,
    busy: bool,
    /// confirming is the serve question asked: for the chat, or the tunnel.
    confirming: Option<&'static str>,
    want_qr: bool,
    editing_tunnel: bool,
    merging: Option<String>,
    audits: std::collections::HashMap<String, Result<LANAudit, String>>,
    phones_seen: Option<usize>,
    polling: bool,
}

/// minutes_left is how long a pairing code still holds, rounded up.
fn minutes_left(expires: &str) -> i64 {
    chrono::DateTime::parse_from_rfc3339(expires)
        .map(|e| {
            let ms = e.timestamp_millis() - chrono::Utc::now().timestamp_millis();
            (ms + 59_999).div_euclid(60_000)
        })
        .unwrap_or(0)
}

/// qr draws a QR code from its rows ('1' is dark), with its quiet zone.
fn qr(rows: &[String], module: f32) -> AnyElement {
    let quiet = 3usize;
    let n = rows.first().map_or(0, |r| r.len()) + 2 * quiet;
    v_flex()
        .flex_none()
        .bg(theme::paper())
        .rounded_lg()
        .p(px(module * quiet as f32))
        .children(rows.iter().map(|row| {
            // Each row as runs of light and dark, so a code is a few hundred
            // boxes rather than a few thousand.
            let mut runs: Vec<(bool, usize)> = Vec::new();
            for c in row.chars() {
                let dark = c == '1';
                match runs.last_mut() {
                    Some((d, len)) if *d == dark => *len += 1,
                    _ => runs.push((dark, 1)),
                }
            }
            h_flex()
                .h(px(module))
                .children(runs.into_iter().map(|(dark, len)| {
                    div()
                        .h_full()
                        .w(px(module * len as f32))
                        .when(dark, |el| el.bg(theme::pitch()))
                }))
        }))
        .w(px(module * n as f32))
        .into_any_element()
}

impl AppView {
    pub(super) fn phone_groups(&self) -> Vec<Group> {
        vec![group(
            "phone",
            "defaults.phone.title",
            vec![
                entry(
                    "phone-chat",
                    "defaults.phone.chatLabel",
                    "defaults.phone.chatDescription",
                    "defaults.phone.chatKeywords",
                ),
                entry(
                    "phone-tunnel",
                    "defaults.phone.tunnelLabel",
                    "defaults.phone.tunnelDescription",
                    "defaults.phone.tunnelKeywords",
                ),
                entry(
                    "phones",
                    "defaults.phone.pairedLabel",
                    "defaults.phone.pairedDescription",
                    "defaults.phone.pairedKeywords",
                ),
            ],
        )]
    }

    pub(super) fn load_lan(&mut self, cx: &mut Context<Self>) {
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client.get::<LANStatus>("/v1/lan").await;
            this.update(cx, |this, cx| {
                this.got_lan(res.map_err(|e| e.to_string()), cx);
                this.poll_lan(cx);
            })
            .ok();
        })
        .detach();
    }

    fn poll_lan(&mut self, cx: &mut Context<Self>) {
        let p = &mut self.settings_page.phone;
        if p.polling {
            return;
        }
        p.polling = true;
        cx.spawn(async move |this, cx| {
            cx.background_executor().timer(Duration::from_secs(3)).await;
            this.update(cx, |this, cx| {
                this.settings_page.phone.polling = false;
                if this.settings && this.settings_page.open == "phone" {
                    this.load_lan(cx);
                }
            })
            .ok();
        })
        .detach();
    }

    /// got_lan keeps what the daemon says, makes the code Electron would
    /// make at this point, and notices a phone that just paired.
    fn got_lan(&mut self, res: Result<LANStatus, String>, cx: &mut Context<Self>) {
        let p = &mut self.settings_page.phone;
        if let Ok(st) = &res {
            let n = st.phones.len();
            if p.phones_seen.is_some_and(|seen| n > seen) && p.pairing.is_some() {
                p.pairing = None;
                p.said = Some(match st.phones.first().filter(|ph| !ph.name.is_empty()) {
                    Some(ph) => tv("defaults.phone.pairedToast", &[("name", (&ph.name).into())]),
                    None => t("defaults.phone.pairedToastYourPhone"),
                });
            }
            p.phones_seen = Some(n);
            if !st.enabled {
                p.pairing = None;
            }
            let reachable = st.listening || !st.tunnel.url.is_empty();
            let qr_now = p.want_qr && st.enabled && reachable && !st.urls.is_empty();
            p.lan = Some(res);
            if qr_now {
                p.want_qr = false;
                self.pair_phone(cx);
            }
        } else {
            p.lan = Some(res);
        }
        cx.notify();
    }

    fn update_lan(&mut self, req: UpdateLANRequest, cx: &mut Context<Self>) {
        self.settings_page.phone.busy = true;
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let body = serde_json::to_vec(&req).unwrap_or_default();
            let res = client.raw(Method::PATCH, "/v1/lan", Some(body)).await;
            this.update(cx, |this, cx| {
                let p = &mut this.settings_page.phone;
                p.busy = false;
                match res.map(|b| serde_json::from_slice::<LANStatus>(&b)) {
                    Ok(Ok(st)) => {
                        p.error = None;
                        p.editing_tunnel = false;
                        this.got_lan(Ok(st), cx);
                    }
                    Ok(Err(e)) => p.error = Some(e.to_string()),
                    Err(e) => p.error = Some(e.to_string()),
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    fn pair_phone(&mut self, cx: &mut Context<Self>) {
        self.settings_page.phone.busy = true;
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client
                .raw(Method::POST, "/v1/lan/pairings", None)
                .await
                .and_then(|b| Ok(serde_json::from_slice::<LANPairing>(&b)?));
            this.update(cx, |this, cx| {
                let p = &mut this.settings_page.phone;
                p.busy = false;
                match res {
                    Ok(pairing) => {
                        p.pairing = Some(pairing);
                        p.error = None;
                    }
                    Err(e) => p.error = Some(format!("{} {e}", t("defaults.phone.qrFailed"))),
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    fn phone_call(
        &mut self,
        method: Method,
        path: String,
        body: Option<Vec<u8>>,
        said: Option<String>,
        cx: &mut Context<Self>,
    ) {
        self.settings_page.phone.busy = true;
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client.raw(method, &path, body).await;
            this.update(cx, |this, cx| {
                let p = &mut this.settings_page.phone;
                p.busy = false;
                match res {
                    Ok(_) => {
                        p.error = None;
                        if said.is_some() {
                            p.said = said;
                        }
                    }
                    Err(e) => p.error = Some(e.to_string()),
                }
                this.load_lan(cx);
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    fn set_scopes(&mut self, phone: &LANPhone, scopes: Vec<String>, cx: &mut Context<Self>) {
        let body = serde_json::to_vec(&UpdateLANPhoneRequest { scopes }).ok();
        self.phone_call(
            Method::PATCH,
            format!("/v1/lan/phones/{}", enc(&phone.id)),
            body,
            None,
            cx,
        );
    }

    fn load_audit(&mut self, id: String, cx: &mut Context<Self>) {
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client
                .get::<LANAudit>(&format!("/v1/lan/phones/{}/audit", enc(&id)))
                .await;
            this.update(cx, |this, cx| {
                this.settings_page
                    .phone
                    .audits
                    .insert(id, res.map_err(|e| e.to_string()));
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    pub(super) fn phone_entry(&self, e: &Entry, cx: &mut Context<Self>) -> Option<AnyElement> {
        let p = &self.settings_page.phone;
        let st = p.lan.as_ref().and_then(|r| r.as_ref().ok());
        let port = st.map_or(7780, |s| s.port);
        let serve_ask = |for_: &'static str, cx: &mut Context<Self>| {
            ask(
                "phone-serve",
                t("defaults.phone.serveTitle"),
                tv("defaults.phone.serveDescription", &[("port", port.into())]),
                t("defaults.phone.serveConfirm"),
                false,
                p.busy,
                cx.listener(move |this, _: &ClickEvent, _, cx| {
                    this.settings_page.phone.confirming = None;
                    if for_ == "chat" {
                        this.settings_page.phone.want_qr = true;
                        this.update_lan(
                            UpdateLANRequest {
                                enabled: Some(true),
                                ..Default::default()
                            },
                            cx,
                        );
                    } else {
                        this.update_lan(
                            UpdateLANRequest {
                                enabled: Some(true),
                                tunnel: Some(true),
                                ..Default::default()
                            },
                            cx,
                        );
                    }
                }),
                cx.listener(|this, _: &ClickEvent, _, cx| {
                    this.settings_page.phone.confirming = None;
                    cx.notify();
                }),
            )
        };
        match e.id {
            "phone-chat" => {
                let mut row = self.more_row(
                    e,
                    Some(tv("defaults.phone.chatDetails", &[("port", port.into())])),
                    cx,
                );
                if let Some(Err(err)) = &p.lan {
                    row.body.push(note_in(err.clone(), theme::rose(300)));
                }
                if let Some(err) = &p.error {
                    row.body.push(note_in(err.clone(), theme::rose(300)));
                }
                if let Some(s) = &p.said {
                    row.body.push(note_in(s.clone(), theme::emerald(300)));
                }
                row.control = Some(match st {
                    Some(s) if s.enabled => toggle(
                        "switch-phone-chat",
                        true,
                        p.busy,
                        cx.listener(|this, _: &ClickEvent, _, cx| {
                            this.update_lan(
                                UpdateLANRequest {
                                    enabled: Some(false),
                                    ..Default::default()
                                },
                                cx,
                            )
                        }),
                    ),
                    _ => button(
                        "phone-start",
                        t("defaults.phone.pair"),
                        Btn::Primary,
                        p.busy || st.is_none(),
                        cx.listener(|this, _: &ClickEvent, _, cx| {
                            this.settings_page.phone.confirming = Some("chat");
                            cx.notify();
                        }),
                    ),
                });
                if p.confirming == Some("chat") {
                    row.body.push(serve_ask("chat", cx));
                }
                if let Some(s) = st.filter(|s| s.enabled) {
                    row.body
                        .push(note_in(t("defaults.phone.plainHttp"), theme::amber(300)));
                    if !s.tunnel.enabled {
                        row.body.push(note(t("defaults.phone.httpsOnly")));
                    }
                    if !s.tunnel.url.is_empty() && !s.listening {
                    } else if s.listening && !s.urls.is_empty() {
                        row.body.push(note(plain(&if s.urls.len() > 1 {
                            tv(
                                "defaults.phone.phonesOpenMore",
                                &[
                                    ("address", (&s.urls[0]).into()),
                                    ("others", (&s.urls[1..].join(", ")).into()),
                                ],
                            )
                        } else {
                            tv(
                                "defaults.phone.phonesOpen",
                                &[("address", (&s.urls[0]).into())],
                            )
                        })));
                    } else {
                        let msg = if s.error.is_empty() {
                            t("defaults.phone.openingPort")
                        } else {
                            s.error.clone()
                        };
                        row.body.push(note_in(
                            msg,
                            if s.error.is_empty() || s.error.starts_with("waiting") {
                                theme::muted()
                            } else {
                                theme::error()
                            },
                        ));
                    }
                    if s.listening && !s.warning.is_empty() {
                        row.body.push(note_in(s.warning.clone(), theme::amber(300)));
                    }
                    if s.web_version.is_empty() {
                        row.body.push(note(t("defaults.phone.gettingPage")));
                    }
                    match &p.pairing {
                        Some(pairing) => row.body.push(self.pairing_code(pairing, cx)),
                        None if (s.listening || !s.tunnel.url.is_empty()) && !s.urls.is_empty() => {
                            row.body.push(
                                h_flex()
                                    .child(button(
                                        "phone-pair",
                                        t("defaults.phone.pair"),
                                        Btn::Secondary,
                                        p.busy,
                                        cx.listener(|this, _: &ClickEvent, _, cx| {
                                            this.pair_phone(cx)
                                        }),
                                    ))
                                    .into_any_element(),
                            )
                        }
                        None => {}
                    }
                }
                Some(row.render())
            }
            "phone-tunnel" => {
                let mut row = self.more_row(e, Some(t("defaults.phone.tunnelDetails")), cx);
                let tunnel = st.map(|s| &s.tunnel);
                let on = st.is_some_and(|s| s.enabled && s.tunnel.enabled);
                let enabled = st.is_some_and(|s| s.enabled);
                row.control = Some(toggle(
                    "switch-phone-tunnel",
                    on,
                    p.busy || st.is_none(),
                    cx.listener(move |this, _: &ClickEvent, _, cx| {
                        if !on && !enabled {
                            this.settings_page.phone.confirming = Some("tunnel");
                            cx.notify();
                        } else if !on {
                            this.update_lan(
                                UpdateLANRequest {
                                    enabled: Some(true),
                                    tunnel: Some(true),
                                    ..Default::default()
                                },
                                cx,
                            )
                        } else {
                            this.update_lan(
                                UpdateLANRequest {
                                    tunnel: Some(false),
                                    ..Default::default()
                                },
                                cx,
                            )
                        }
                    }),
                ));
                if p.confirming == Some("tunnel") {
                    row.body.push(serve_ask("tunnel", cx));
                }
                let Some(tn) = tunnel.filter(|_| on || p.editing_tunnel) else {
                    return Some(row.render());
                };
                let tailnet = if tn.transport == "tailscale" {
                    if !tn.tailnet.is_empty() && tn.tailnet != "-" {
                        tn.tailnet.clone()
                    } else {
                        "Tailscale".into()
                    }
                } else {
                    String::new()
                };
                if on {
                    row.body.push(if tailnet.is_empty() {
                        note_in(t("defaults.phone.tunnelWarning"), theme::amber(300))
                    } else {
                        note(t("defaults.phone.tailnetNote"))
                    });
                    if tn.state == "running" && !tn.url.is_empty() {
                        let a = [
                            ("address", (&tn.url).into()),
                            ("tailnet", (&tailnet).into()),
                        ];
                        let key = if !tailnet.is_empty() {
                            "defaults.phone.tunnelOpenTailnet"
                        } else if tn.mode == "quick" {
                            "defaults.phone.tunnelOpenQuick"
                        } else {
                            "defaults.phone.tunnelOpenNamed"
                        };
                        row.body.push(note(plain(&tv(key, &a))));
                    } else {
                        row.body.push(note_in(
                            if tn.error.is_empty() {
                                t("defaults.phone.startingTunnel")
                            } else {
                                tn.error.clone()
                            },
                            if tn.state == "failed" {
                                theme::error()
                            } else {
                                theme::muted()
                            },
                        ));
                    }
                }
                if on && !p.editing_tunnel {
                    row.body.push(
                        v_flex()
                            .gap_2()
                            .children(TUNNEL_MODES.iter().map(|m| {
                                let m = *m;
                                let no_hub =
                                    m == "hub" && (tn.hub.is_empty() || tn.hub_has_no_addresses);
                                let picked = tn.mode == m;
                                let named_unset = m == "named" && tn.named_hostname.is_empty();
                                let hint = match m {
                                    "hub" if tn.hub.is_empty() => {
                                        t("defaults.phone.mode.hub.noHub")
                                    }
                                    "hub" if tn.hub_has_no_addresses => tv(
                                        "defaults.phone.mode.hub.none",
                                        &[("hub", (&tn.hub).into())],
                                    ),
                                    "hub" if !tailnet.is_empty() => tv(
                                        "defaults.phone.mode.hub.tailscale",
                                        &[
                                            ("hub", (&tn.hub).into()),
                                            ("tailnet", (&tailnet).into()),
                                        ],
                                    ),
                                    "hub" => tv(
                                        "defaults.phone.mode.hub.hint",
                                        &[("hub", (&tn.hub).into())],
                                    ),
                                    "named" if !tn.named_hostname.is_empty() => tv(
                                        "defaults.phone.mode.named.set",
                                        &[("hostname", (&tn.named_hostname).into())],
                                    ),
                                    _ => t(&format!("defaults.phone.mode.{m}.hint")),
                                };
                                v_flex()
                                    .id(SharedString::from(format!("tunnel-mode-{m}")))
                                    .gap_0p5()
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
                                    .when(no_hub || p.busy, |el| el.opacity(0.6))
                                    .when(!no_hub && !p.busy && !picked, |el| {
                                        el.cursor_pointer()
                                            .hover(|st| st.bg(theme::surface_raised()))
                                            .active(|s| s.bg(theme::surface_strong()))
                                            .on_click(cx.listener(
                                                move |this, _: &ClickEvent, _, cx| {
                                                    if named_unset {
                                                        this.settings_page.phone.editing_tunnel =
                                                            true;
                                                        cx.notify();
                                                    } else {
                                                        this.update_lan(
                                                            UpdateLANRequest {
                                                                tunnel_mode: Some(m.into()),
                                                                ..Default::default()
                                                            },
                                                            cx,
                                                        )
                                                    }
                                                },
                                            ))
                                    })
                                    .child(
                                        div()
                                            .text_size(theme::text::UI)
                                            .font_weight(FontWeight::MEDIUM)
                                            .text_color(theme::title())
                                            .child(t(&format!("defaults.phone.mode.{m}"))),
                                    )
                                    .child(
                                        div()
                                            .min_w_0()
                                            .text_size(theme::text::CAPTION)
                                            .text_color(theme::muted())
                                            .child(hint),
                                    )
                            }))
                            .into_any_element(),
                    );
                }
                if tn.named && !p.editing_tunnel {
                    row.body.push(note(plain(&tv(
                        "defaults.phone.namedTunnel",
                        &[
                            ("hostname", (&tn.hostname).into()),
                            ("origin", (&tn.origin).into()),
                        ],
                    ))));
                }
                if p.editing_tunnel {
                    let keep = !tn.named_hostname.is_empty();
                    let host = self.field_value("tunnel-hostname", cx);
                    let token = self.field_value("tunnel-token", cx);
                    row.body.push(
                        v_flex()
                            .gap_2()
                            .child(note(plain(&tv(
                                "defaults.phone.makeTunnel",
                                &[("origin", (&tn.origin).into())],
                            ))))
                            .child(labeled(
                                t("defaults.phone.publicHostname"),
                                self.input_small("tunnel-hostname"),
                            ))
                            .child(labeled(
                                t("defaults.phone.token"),
                                v_flex()
                                    .gap_1()
                                    .child(self.input_small("tunnel-token"))
                                    .child(note(if keep {
                                        t("defaults.phone.tokenKeep")
                                    } else {
                                        t("defaults.phone.tokenNew")
                                    })),
                            ))
                            .child(
                                h_flex()
                                    .gap_2()
                                    .child(button(
                                        "tunnel-use",
                                        t("defaults.phone.useTunnel"),
                                        Btn::Primary,
                                        p.busy || host.is_empty() || (!keep && token.is_empty()),
                                        cx.listener(|this, _: &ClickEvent, _, cx| {
                                            this.save_tunnel(cx)
                                        }),
                                    ))
                                    .child(button(
                                        "tunnel-cancel",
                                        t("common.cancel"),
                                        Btn::Ghost,
                                        false,
                                        cx.listener(|this, _: &ClickEvent, _, cx| {
                                            this.settings_page.phone.editing_tunnel = false;
                                            cx.notify();
                                        }),
                                    )),
                            )
                            .into_any_element(),
                    );
                } else if on && tn.mode == "named" {
                    row.body.push(link(
                        "tunnel-change",
                        t("defaults.phone.changeTunnel"),
                        cx.listener(|this, _: &ClickEvent, _, cx| {
                            this.settings_page.phone.editing_tunnel = true;
                            cx.notify();
                        }),
                    ));
                }
                Some(row.render())
            }
            "phones" => {
                let mut row = self.more_row(e, None, cx);
                let phones = st.map(|s| s.phones.as_slice()).unwrap_or_default();
                if phones.is_empty() {
                    row.body.push(note(t("defaults.phone.noneYet")));
                }
                for ph in phones {
                    row.body.push(self.phone_row(ph, cx));
                }
                Some(row.render())
            }
            _ => None,
        }
    }

    pub(super) fn save_tunnel(&mut self, cx: &mut Context<Self>) {
        let host = self.field_value("tunnel-hostname", cx);
        let token = self.field_value("tunnel-token", cx);
        let keep = self
            .settings_page
            .phone
            .lan
            .as_ref()
            .and_then(|r| r.as_ref().ok())
            .is_some_and(|s| !s.tunnel.named_hostname.is_empty());
        if host.is_empty() {
            return;
        }
        self.update_lan(
            UpdateLANRequest {
                tunnel_hostname: Some(host),
                tunnel_token: (!token.is_empty() || !keep).then_some(token),
                ..Default::default()
            },
            cx,
        );
    }

    fn pairing_code(&self, pairing: &LANPairing, cx: &mut Context<Self>) -> AnyElement {
        let left = minutes_left(&pairing.expires);
        let expired = left <= 0;
        let url = pairing.urls.first().cloned().unwrap_or_default();
        let copy = url.clone();
        h_flex()
            .id("phone-pairing")
            .gap_4()
            .items_start()
            .p_5()
            .rounded(theme::radius::XXL)
            .border_1()
            .border_color(theme::line())
            .bg(theme::surface_faint())
            .child(
                div()
                    .when(expired, |el| el.opacity(0.2))
                    .child(qr(&pairing.qr, 4.)),
            )
            .child(
                v_flex()
                    .flex_1()
                    .min_w_0()
                    .gap_2()
                    .child(
                        div()
                            .text_size(theme::text::UI)
                            .font_weight(FontWeight::MEDIUM)
                            .text_color(theme::primary())
                            .child(t("defaults.phone.scan")),
                    )
                    .child(note(if expired {
                        t("defaults.phone.codeExpired")
                    } else {
                        tv("defaults.phone.codeValid", &[("minutes", left.into())])
                    }))
                    .child(mono(url))
                    .child(
                        h_flex()
                            .gap_2()
                            .pt_1()
                            .child(button(
                                "phone-copy",
                                t("defaults.phone.copyAddress"),
                                Btn::Secondary,
                                false,
                                cx.listener(move |this, _: &ClickEvent, _, cx| {
                                    cx.write_to_clipboard(ClipboardItem::new_string(copy.clone()));
                                    this.settings_page.phone.said = Some(t("common.copied"));
                                    cx.notify();
                                }),
                            ))
                            .child(button(
                                "phone-new-code",
                                t("defaults.phone.newCode"),
                                Btn::Secondary,
                                self.settings_page.phone.busy,
                                cx.listener(|this, _: &ClickEvent, _, cx| this.pair_phone(cx)),
                            ))
                            .child(button(
                                "phone-done",
                                t("common.done"),
                                Btn::Ghost,
                                false,
                                cx.listener(|this, _: &ClickEvent, _, cx| {
                                    this.settings_page.phone.pairing = None;
                                    cx.notify();
                                }),
                            )),
                    ),
            )
            .into_any_element()
    }

    fn phone_row(&self, ph: &LANPhone, cx: &mut Context<Self>) -> AnyElement {
        let p = &self.settings_page.phone;
        let has = |s: &str| ph.scopes.iter().any(|x| x == s);
        let mut facts = tv(
            "defaults.phone.pairedOn",
            &[("date", (&local_date(&ph.paired)).into())],
        );
        if !ph.last_seen.is_empty() {
            facts = format!(
                "{facts} · {}",
                tv(
                    "defaults.phone.lastSeen",
                    &[("when", (&local_date_time(&ph.last_seen)).into())]
                )
            );
        }
        if !ph.last_addr.is_empty() {
            facts = format!(
                "{facts} · {}",
                tv(
                    "defaults.phone.fromAddr",
                    &[("addr", (&ph.last_addr).into())]
                )
            );
        }
        let unpair = ph.clone();
        let merge_on = has("merge");
        let merge_phone = ph.clone();
        let audit_open = p.audits.contains_key(&ph.id);
        let audit_id = ph.id.clone();
        v_flex()
            .id(SharedString::from(format!("phone-{}", ph.id)))
            .gap_3()
            .px_3()
            .py_2p5()
            .rounded_xl()
            .border_1()
            .border_color(theme::line())
            .child(
                h_flex()
                    .gap_3()
                    .child(
                        div()
                            .flex_none()
                            .text_size(px(16.))
                            .text_color(theme::subtle())
                            .child(crate::icons::of("smartphone")),
                    )
                    .child(
                        v_flex()
                            .flex_1()
                            .min_w_0()
                            .child(
                                div()
                                    .text_size(theme::text::UI)
                                    .font_weight(FontWeight::MEDIUM)
                                    .text_color(theme::primary())
                                    .truncate()
                                    .child(ph.name.clone()),
                            )
                            .child(note(facts)),
                    )
                    .child(button(
                        SharedString::from(format!("phone-unpair-{}", ph.id)),
                        t("defaults.phone.unpair"),
                        Btn::Ghost,
                        p.busy,
                        cx.listener(move |this, _: &ClickEvent, _, cx| {
                            this.phone_call(
                                Method::DELETE,
                                format!("/v1/lan/phones/{}", enc(&unpair.id)),
                                None,
                                Some(tv(
                                    "defaults.phone.unpairedToast",
                                    &[("name", (&unpair.name).into())],
                                )),
                                cx,
                            )
                        }),
                    )),
            )
            .child(
                h_flex()
                    .pl_7()
                    .flex_wrap()
                    .gap_1p5()
                    .child(div().mr_1().child(note(t("defaults.phone.mayLabel"))))
                    .children(SCOPES.iter().map(|s| {
                        let s = *s;
                        let on = has(s);
                        let phone = ph.clone();
                        div()
                            .id(SharedString::from(format!("phone-scope-{}-{s}", ph.id)))
                            .px_2p5()
                            .py_0p5()
                            .rounded_full()
                            .border_1()
                            .text_size(theme::text::CAPTION)
                            .border_color(if on {
                                theme::brand(400).opacity(0.5)
                            } else {
                                theme::line()
                            })
                            .when(on, |el| {
                                el.bg(theme::brand(500).opacity(0.15))
                                    .text_color(theme::primary())
                            })
                            .when(!on, |el| {
                                el.text_color(theme::subtle())
                                    .line_through()
                                    .hover(|st| st.text_color(theme::secondary()))
                            })
                            .cursor_pointer()
                            .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                                let mut next: Vec<String> =
                                    phone.scopes.iter().filter(|x| *x != s).cloned().collect();
                                if !on {
                                    next.push(s.into());
                                }
                                this.set_scopes(&phone, next, cx)
                            }))
                            .child(t(&format!("defaults.phone.scope.{s}")))
                    })),
            )
            .child(
                h_flex()
                    .pl_7()
                    .gap_3()
                    .items_start()
                    .child(
                        v_flex()
                            .flex_1()
                            .min_w_0()
                            .child(
                                div()
                                    .text_size(theme::text::CAPTION)
                                    .text_color(theme::primary())
                                    .child(t("defaults.phone.mergeLabel")),
                            )
                            .child(note(t("defaults.phone.mergeDescription"))),
                    )
                    .child(toggle(
                        SharedString::from(format!("phone-merge-{}", ph.id)),
                        merge_on,
                        p.busy,
                        cx.listener(move |this, _: &ClickEvent, _, cx| {
                            if merge_on {
                                let next = merge_phone
                                    .scopes
                                    .iter()
                                    .filter(|x| *x != "merge")
                                    .cloned()
                                    .collect();
                                this.set_scopes(&merge_phone, next, cx)
                            } else {
                                this.settings_page.phone.merging = Some(merge_phone.id.clone());
                                cx.notify();
                            }
                        }),
                    )),
            )
            .when(p.merging.as_deref() == Some(ph.id.as_str()), |el| {
                let phone = ph.clone();
                el.child(ask(
                    "phone-merge",
                    tv(
                        "defaults.phone.mergeConfirmTitle",
                        &[("name", (&ph.name).into())],
                    ),
                    t("defaults.phone.mergeConfirmDescription"),
                    t("defaults.phone.mergeConfirm"),
                    true,
                    p.busy,
                    cx.listener(move |this, _: &ClickEvent, _, cx| {
                        this.settings_page.phone.merging = None;
                        let mut next = phone.scopes.clone();
                        next.push("merge".into());
                        this.set_scopes(&phone, next, cx)
                    }),
                    cx.listener(|this, _: &ClickEvent, _, cx| {
                        this.settings_page.phone.merging = None;
                        cx.notify();
                    }),
                ))
            })
            .child(div().pl_7().child(link(
                SharedString::from(format!("phone-audit-{}", ph.id)),
                if audit_open {
                    t("defaults.phone.hideAudit")
                } else {
                    t("defaults.phone.showAudit")
                },
                cx.listener(move |this, _: &ClickEvent, _, cx| {
                    if this.settings_page.phone.audits.remove(&audit_id).is_none() {
                        this.load_audit(audit_id.clone(), cx);
                    }
                    cx.notify();
                }),
            )))
            .when_some(p.audits.get(&ph.id), |el, a| {
                el.child(
                    div().pl_7().child(match a {
                        Err(e) => note_in(e.clone(), theme::rose(300)),
                        Ok(a) if a.entries.is_empty() => note(t("defaults.phone.auditEmpty")),
                        Ok(a) => v_flex()
                            .gap_0p5()
                            .p_1p5()
                            .rounded_lg()
                            .border_1()
                            .border_color(theme::line())
                            .bg(theme::surface_faint())
                            .children(a.entries.iter().map(|x| {
                                let (word, color) = match x.result.as_str() {
                                    "ok" => (t("defaults.phone.auditOk"), theme::emerald(300)),
                                    "refused" => {
                                        (t("defaults.phone.auditRefused"), theme::rose(300))
                                    }
                                    _ => (t("defaults.phone.auditFailed"), theme::amber(300)),
                                };
                                h_flex()
                                    .gap_2()
                                    .px_1()
                                    .py_0p5()
                                    .text_size(theme::text::CAPTION)
                                    .child(
                                        div()
                                            .w(px(56.))
                                            .flex_none()
                                            .font_weight(FontWeight::MEDIUM)
                                            .text_color(color)
                                            .child(word),
                                    )
                                    .child(
                                        div()
                                            .flex_1()
                                            .min_w_0()
                                            .truncate()
                                            .child(mono(x.route.clone())),
                                    )
                                    .child(div().text_color(theme::faint()).child(
                                        if x.scope.is_empty() {
                                            t("defaults.phone.auditNever")
                                        } else {
                                            x.scope.clone()
                                        },
                                    ))
                                    .child(
                                        div()
                                            .text_color(theme::faint())
                                            .child(local_date_time(&x.at)),
                                    )
                            }))
                            .into_any_element(),
                    }),
                )
            })
            .into_any_element()
    }
}
