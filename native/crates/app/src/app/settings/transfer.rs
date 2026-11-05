//! Settings → Transfer (TransferSettings.tsx): moving this AgentBox's data
//! to another one through one file. Export previews what there is (GET
//! /v1/transfer/export), and the user ticks projects, their parts and logins,
//! names the file and a passphrase; import opens a file (POST
//! /v1/transfer/import/inspect) and merges what's ticked. Both run as the
//! daemon's jobs, followed here with their log, then their result.
//!
//! "Choose…" asks the system's file picker (GPUI's, through the desktop
//! portal on Linux); a path can always be typed. A new project's repository
//! goes where the file suggests: this page has no field to move it.
use std::collections::{HashMap, HashSet};
use std::path::PathBuf;

use agentbox_api::{
    InstanceExportPreview, InstanceExportProjectChoice, InstanceExportRequest,
    InstanceExportResult, InstanceImportContents, InstanceImportInspectRequest,
    InstanceImportProjectChoice, InstanceImportReport, InstanceImportRequest, Job,
};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::{h_flex, v_flex};

use super::super::AppView;
use super::super::controls::{Btn, badge, button, note, note_in};
use super::kit::{check, local_date_time, mono};
use super::skills::format_bytes;
use super::{Entry, Group, entry, group};
use crate::theme;

/// What an export takes unless unticked: everything but secrets.
const OPT_IN: [&str; 1] = ["secrets"];

/// Pick is what's ticked of one project: whether it goes, which parts, and
/// whether its repository goes as a bundle.
#[derive(Clone)]
struct Pick {
    on: bool,
    kinds: HashSet<String>,
    bundle: bool,
}

#[derive(Default)]
pub(super) struct Transfer {
    preview: Option<Result<InstanceExportPreview, String>>,
    picks: HashMap<String, Pick>,
    logins: HashSet<String>,
    plain: bool,
    export_job: Option<String>,
    export_error: Option<String>,
    contents: Option<InstanceImportContents>,
    inspect_error: Option<String>,
    ask_passphrase: bool,
    import_picks: HashMap<String, Pick>,
    import_logins: Option<HashSet<String>>,
    import_job: Option<String>,
    import_error: Option<String>,
    busy: bool,
}

fn kind_label(kind: &str) -> String {
    let key = format!("transfer.kind.{kind}");
    if agentbox_i18n::has(&key) {
        t(&key)
    } else {
        kind.to_string()
    }
}

fn login_label(kind: &str) -> String {
    let key = format!("transfer.login.{kind}");
    if agentbox_i18n::has(&key) {
        t(&key)
    } else {
        kind.to_string()
    }
}

/// home is where a picker starts: the user's home folder.
fn home() -> PathBuf {
    std::env::var_os("HOME")
        .or_else(|| std::env::var_os("USERPROFILE"))
        .map(PathBuf::from)
        .unwrap_or_default()
}

impl AppView {
    pub(super) fn transfer_groups(&self) -> Vec<Group> {
        vec![group(
            "transfer",
            "settings.section.transfer.title",
            vec![
                entry(
                    "export",
                    "transfer.export.title",
                    "",
                    "settings.entry.transfer.keywords",
                ),
                entry(
                    "import",
                    "transfer.import.title",
                    "transfer.import.description",
                    "settings.entry.transfer.keywords",
                ),
            ],
        )]
    }

    pub(super) fn load_transfer_preview(&mut self, cx: &mut Context<Self>) {
        if !self.settings || self.settings_page.open != "transfer" {
            return;
        }
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client
                .get::<InstanceExportPreview>("/v1/transfer/export")
                .await;
            this.update(cx, |this, cx| {
                this.settings_page.transfer.preview = Some(res.map_err(|e| e.to_string()));
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn export_pick(&self, name: &str) -> Pick {
        let tr = &self.settings_page.transfer;
        if let Some(p) = tr.picks.get(name) {
            return p.clone();
        }
        let kinds = tr
            .preview
            .as_ref()
            .and_then(|r| r.as_ref().ok())
            .and_then(|p| p.projects.iter().find(|p| p.name == name))
            .map(|p| {
                p.categories
                    .iter()
                    .map(|c| c.kind.clone())
                    .filter(|k| !OPT_IN.contains(&k.as_str()))
                    .collect()
            })
            .unwrap_or_default();
        Pick {
            on: true,
            kinds,
            bundle: false,
        }
    }

    fn set_export_pick(&mut self, name: &str, change: impl FnOnce(&mut Pick)) {
        let mut p = self.export_pick(name);
        change(&mut p);
        self.settings_page
            .transfer
            .picks
            .insert(name.to_string(), p);
    }

    fn import_pick(&self, name: &str) -> Pick {
        let tr = &self.settings_page.transfer;
        if let Some(p) = tr.import_picks.get(name) {
            return p.clone();
        }
        let kinds = tr
            .contents
            .as_ref()
            .and_then(|c| c.projects.iter().find(|p| p.name == name))
            .map(|p| p.categories.iter().map(|c| c.kind.clone()).collect())
            .unwrap_or_default();
        Pick {
            on: true,
            kinds,
            bundle: false,
        }
    }

    fn set_import_pick(&mut self, name: &str, change: impl FnOnce(&mut Pick)) {
        let mut p = self.import_pick(name);
        change(&mut p);
        self.settings_page
            .transfer
            .import_picks
            .insert(name.to_string(), p);
    }

    fn import_logins_on(&self) -> HashSet<String> {
        let tr = &self.settings_page.transfer;
        tr.import_logins.clone().unwrap_or_else(|| {
            tr.contents
                .iter()
                .flat_map(|c| &c.logins)
                .filter(|l| l.items > 0)
                .map(|l| l.kind.clone())
                .collect()
        })
    }

    /// pick_path asks the system's file picker for a path, into a field.
    fn pick_path(
        &mut self,
        field: &'static str,
        save: bool,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        if save {
            let name = format!(
                "agentbox-{}.agentbox",
                chrono::Local::now().format("%Y-%m-%d")
            );
            let rx = cx.prompt_for_new_path(&home(), Some(&name));
            cx.spawn_in(window, async move |this, cx| {
                let picked = match rx.await {
                    Ok(Ok(Some(p))) => Some(Ok(p)),
                    Ok(Err(e)) => Some(Err(e)),
                    _ => None,
                };
                this.update_in(cx, |this, window, cx| {
                    this.got_path(field, picked, window, cx)
                })
                .ok();
            })
            .detach();
        } else {
            let rx = cx.prompt_for_paths(PathPromptOptions {
                files: true,
                directories: false,
                multiple: false,
                prompt: Some(t("transfer.import.title").into()),
            });
            cx.spawn_in(window, async move |this, cx| {
                let picked = match rx.await {
                    Ok(Ok(Some(p))) => p.into_iter().next().map(Ok),
                    Ok(Err(e)) => Some(Err(e)),
                    _ => None,
                };
                this.update_in(cx, |this, window, cx| {
                    this.got_path(field, picked, window, cx)
                })
                .ok();
            })
            .detach();
        }
    }

    fn got_path(
        &mut self,
        field: &'static str,
        picked: Option<anyhow::Result<PathBuf>>,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        match picked {
            Some(Ok(p)) => {
                if let Some(i) = self.settings_page.fields.get(field) {
                    let v = p.display().to_string();
                    i.update(cx, |i, cx| i.set_value(v, window, cx));
                }
            }
            Some(Err(e)) => {
                let tr = &mut self.settings_page.transfer;
                if field == "transfer-export-path" {
                    tr.export_error = Some(e.to_string());
                } else {
                    tr.inspect_error = Some(e.to_string());
                }
            }
            None => {}
        }
        cx.notify();
    }

    fn export_ready(&self, cx: &App) -> (bool, Option<String>, Option<String>, i64) {
        let tr = &self.settings_page.transfer;
        let projects = tr
            .preview
            .as_ref()
            .and_then(|r| r.as_ref().ok())
            .map(|p| p.projects.as_slice())
            .unwrap_or_default();
        let chosen: Vec<_> = projects
            .iter()
            .filter(|p| {
                let k = self.export_pick(&p.name);
                k.on && !k.kinds.is_empty()
            })
            .collect();
        let secret = !tr.logins.is_empty()
            || chosen.iter().any(|p| {
                self.export_pick(&p.name)
                    .kinds
                    .iter()
                    .any(|k| OPT_IN.contains(&k.as_str()))
            });
        let total = chosen
            .iter()
            .map(|p| {
                let k = self.export_pick(&p.name);
                p.categories
                    .iter()
                    .filter(|c| k.kinds.contains(&c.kind))
                    .map(|c| {
                        if c.kind == "repository" && k.bundle {
                            p.bundle_size
                        } else {
                            c.size
                        }
                    })
                    .sum::<i64>()
            })
            .sum();
        let encrypting = !tr.plain || secret;
        let pass = self.field_value("transfer-pass", cx);
        let again = self.field_value("transfer-pass-again", cx);
        let problem = if !encrypting {
            None
        } else if pass.chars().count() < 8 {
            Some(t("transfer.passphrase.short"))
        } else if again != pass {
            Some(t("transfer.passphrase.differ"))
        } else {
            None
        };
        let path = self.field_value("transfer-export-path", cx);
        let not_yet = if chosen.is_empty() && tr.logins.is_empty() {
            Some(t("transfer.export.needChoice"))
        } else if path.is_empty() {
            Some(t("transfer.export.needPath"))
        } else {
            None
        };
        (
            not_yet.is_none() && problem.is_none(),
            not_yet,
            problem.filter(|_| !pass.is_empty()),
            total,
        )
    }

    fn start_export(&mut self, cx: &mut Context<Self>) {
        let tr = &self.settings_page.transfer;
        let projects: Vec<InstanceExportProjectChoice> = tr
            .preview
            .as_ref()
            .and_then(|r| r.as_ref().ok())
            .map(|p| p.projects.as_slice())
            .unwrap_or_default()
            .iter()
            .filter_map(|p| {
                let k = self.export_pick(&p.name);
                (k.on && !k.kinds.is_empty()).then(|| InstanceExportProjectChoice {
                    name: p.name.clone(),
                    categories: k.kinds.into_iter().collect(),
                    bundle: k.bundle,
                })
            })
            .collect();
        let secret = !tr.logins.is_empty()
            || projects
                .iter()
                .any(|p| p.categories.iter().any(|k| OPT_IN.contains(&k.as_str())));
        let encrypting = !tr.plain || secret;
        let req = InstanceExportRequest {
            path: self.field_value("transfer-export-path", cx),
            passphrase: if encrypting {
                self.field_value("transfer-pass", cx)
            } else {
                String::new()
            },
            no_encrypt: !encrypting,
            projects,
            logins: tr.logins.iter().cloned().collect(),
        };
        self.settings_page.transfer.busy = true;
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client.post::<_, Job>("/v1/transfer/export", &req).await;
            this.update(cx, |this, cx| {
                let tr = &mut this.settings_page.transfer;
                tr.busy = false;
                match res {
                    Ok(j) => {
                        tr.export_error = None;
                        tr.export_job = Some(j.id.clone());
                        this.follow_job(j.id, cx);
                    }
                    Err(e) => tr.export_error = Some(e.to_string()),
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    pub(super) fn inspect_import(&mut self, cx: &mut Context<Self>) {
        let path = self.field_value("transfer-import-path", cx);
        if path.is_empty() {
            return;
        }
        let req = InstanceImportInspectRequest {
            path,
            passphrase: self.field_value("transfer-import-pass", cx),
        };
        self.settings_page.transfer.busy = true;
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client
                .post::<_, InstanceImportContents>("/v1/transfer/import/inspect", &req)
                .await;
            this.update(cx, |this, cx| {
                let tr = &mut this.settings_page.transfer;
                tr.busy = false;
                tr.import_picks.clear();
                tr.import_logins = None;
                tr.import_job = None;
                match res {
                    Ok(c) => {
                        tr.contents = Some(c);
                        tr.inspect_error = None;
                    }
                    Err(e) => {
                        let msg = e.to_string();
                        // The daemon asks for the passphrase by its error.
                        if msg.contains("passphrase") {
                            tr.ask_passphrase = true;
                        }
                        tr.contents = None;
                        tr.inspect_error = Some(msg);
                    }
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    fn start_instance_import(&mut self, cx: &mut Context<Self>) {
        let Some(contents) = self.settings_page.transfer.contents.clone() else {
            return;
        };
        let projects = contents
            .projects
            .iter()
            .filter_map(|p| {
                let k = self.import_pick(&p.name);
                k.on.then(|| InstanceImportProjectChoice {
                    name: p.name.clone(),
                    categories: k.kinds.into_iter().collect(),
                    path: if p.exists {
                        String::new()
                    } else {
                        p.path.clone()
                    },
                })
            })
            .collect();
        let req = InstanceImportRequest {
            path: self.field_value("transfer-import-path", cx),
            passphrase: self.field_value("transfer-import-pass", cx),
            projects,
            logins: self.import_logins_on().into_iter().collect(),
        };
        self.settings_page.transfer.busy = true;
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client.post::<_, Job>("/v1/transfer/import", &req).await;
            this.update(cx, |this, cx| {
                let tr = &mut this.settings_page.transfer;
                tr.busy = false;
                match res {
                    Ok(j) => {
                        tr.import_error = None;
                        tr.import_job = Some(j.id.clone());
                        this.follow_job(j.id, cx);
                    }
                    Err(e) => tr.import_error = Some(e.to_string()),
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    fn path_field(&self, field: &'static str, save: bool, cx: &mut Context<Self>) -> AnyElement {
        h_flex()
            .gap_2()
            .child(div().flex_1().child(self.input_small(field)))
            .child(button(
                SharedString::from(format!("{field}-choose")),
                t("transfer.choose"),
                Btn::Secondary,
                false,
                cx.listener(move |this, _: &ClickEvent, window, cx| {
                    this.pick_path(field, save, window, cx)
                }),
            ))
            .into_any_element()
    }

    /// job_result is a finished job's result, once it succeeded.
    fn job_result(&self, id: &str) -> Option<serde_json::Value> {
        let j = self.settings_page.setup.jobs.get(id)?.job.as_ref()?;
        (j.status == agentbox_api::JOB_SUCCEEDED).then(|| j.result.clone())
    }

    pub(super) fn transfer_entry(&self, e: &Entry, cx: &mut Context<Self>) -> Option<AnyElement> {
        match e.id {
            "export" => Some(self.export_row(e, cx)),
            "import" => Some(self.import_row(e, cx)),
            _ => None,
        }
    }

    fn export_row(&self, e: &Entry, cx: &mut Context<Self>) -> AnyElement {
        let tr = &self.settings_page.transfer;
        let mut row = self.more_row(e, None, cx);
        let preview = match &tr.preview {
            None => {
                row.description = t("common.loading");
                return row.render();
            }
            Some(Err(err)) => {
                row.body.push(note_in(err.clone(), theme::error()));
                return row.render();
            }
            Some(Ok(p)) => p,
        };
        row.description = t("transfer.export.description");
        let (ready, not_yet, problem, total) = self.export_ready(cx);
        let secret = !tr.logins.is_empty()
            || preview.projects.iter().any(|p| {
                let k = self.export_pick(&p.name);
                k.on && k.kinds.iter().any(|k| OPT_IN.contains(&k.as_str()))
            });
        let encrypting = !tr.plain || secret;
        if preview.projects.is_empty() {
            row.body.push(note(t("transfer.export.noProjects")));
        }
        for p in &preview.projects {
            let pick = self.export_pick(&p.name);
            let name = p.name.clone();
            let on = pick.on;
            let mut box_ = v_flex()
                .id(SharedString::from(format!("transfer-project-{}", p.name)))
                .gap_2()
                .p_3()
                .rounded_xl()
                .border_1()
                .border_color(theme::border())
                .bg(theme::bg())
                .child(check(
                    SharedString::from(format!("transfer-p-{}", p.name)),
                    on,
                    false,
                    div()
                        .font_weight(FontWeight::MEDIUM)
                        .child(p.display_name.clone()),
                    cx.listener(move |this, _: &ClickEvent, _, cx| {
                        this.set_export_pick(&name, |k| k.on = !on);
                        cx.notify();
                    }),
                ));
            if on {
                box_ = box_.child(h_flex().flex_wrap().gap_x_4().gap_y_1p5().pl_6().children(
                    p.categories.iter().map(|c| {
                        let (name, kind) = (p.name.clone(), c.kind.clone());
                        let has = pick.kinds.contains(&c.kind);
                        let size = if c.kind == "repository" {
                            format_bytes(if pick.bundle { p.bundle_size } else { c.size })
                        } else {
                            tv(
                                "transfer.count",
                                &[
                                    ("count", c.items.into()),
                                    ("size", (&format_bytes(c.size)).into()),
                                ],
                            )
                        };
                        div().min_w(px(180.)).child(check(
                            SharedString::from(format!("transfer-k-{}-{}", p.name, c.kind)),
                            has,
                            false,
                            h_flex()
                                .gap_1()
                                .child(kind_label(&c.kind))
                                .child(div().text_color(theme::faint()).child(size)),
                            cx.listener(move |this, _: &ClickEvent, _, cx| {
                                this.set_export_pick(&name, |k| {
                                    if !k.kinds.remove(&kind) {
                                        k.kinds.insert(kind.clone());
                                    }
                                });
                                cx.notify();
                            }),
                        ))
                    }),
                ));
                if pick.kinds.contains("repository") {
                    let (a, b) = (p.name.clone(), p.name.clone());
                    let reference = format!(
                        "{} — {}{}",
                        t("transfer.repo.reference"),
                        if p.remote.is_empty() {
                            t("transfer.repo.noRemote")
                        } else {
                            p.remote.clone()
                        },
                        if p.branch.is_empty() {
                            String::new()
                        } else {
                            format!(" ({})", p.branch)
                        }
                    );
                    box_ = box_.child(
                        v_flex()
                            .gap_1()
                            .pl_6()
                            .text_xs()
                            .child(check(
                                SharedString::from(format!("transfer-ref-{}", p.name)),
                                !pick.bundle,
                                false,
                                reference,
                                cx.listener(move |this, _: &ClickEvent, _, cx| {
                                    this.set_export_pick(&a, |k| k.bundle = false);
                                    cx.notify();
                                }),
                            ))
                            .child(check(
                                SharedString::from(format!("transfer-bundle-{}", p.name)),
                                pick.bundle,
                                false,
                                tv(
                                    "transfer.repo.bundle",
                                    &[("size", (&format_bytes(p.bundle_size)).into())],
                                ),
                                cx.listener(move |this, _: &ClickEvent, _, cx| {
                                    this.set_export_pick(&b, |k| k.bundle = true);
                                    cx.notify();
                                }),
                            )),
                    );
                }
            }
            row.body.push(box_.into_any_element());
        }
        row.body.push(
            v_flex()
                .gap_1p5()
                .child(
                    div()
                        .text_xs()
                        .font_weight(FontWeight::MEDIUM)
                        .text_color(theme::muted())
                        .child(t("transfer.logins.title")),
                )
                .child(h_flex().flex_wrap().gap_x_4().gap_y_1p5().children(
                    preview.logins.iter().map(|l| {
                        let kind = l.kind.clone();
                        let on = tr.logins.contains(&l.kind);
                        div().min_w(px(220.)).child(check(
                            SharedString::from(format!("transfer-login-{}", l.kind)),
                            on,
                            l.accounts.is_empty(),
                            h_flex().gap_1().child(login_label(&l.kind)).child(
                                div()
                                    .text_color(theme::faint())
                                    .child(if l.accounts.is_empty() {
                                        t("transfer.logins.none")
                                    } else {
                                        l.accounts.join(", ")
                                    }),
                            ),
                            cx.listener(move |this, _: &ClickEvent, _, cx| {
                                let s = &mut this.settings_page.transfer.logins;
                                if !s.remove(&kind) {
                                    s.insert(kind.clone());
                                }
                                cx.notify();
                            }),
                        ))
                    }),
                ))
                .child(note(t("transfer.logins.note")))
                .into_any_element(),
        );
        row.body.push(
            v_flex()
                .gap_2()
                .child(self.path_field("transfer-export-path", true, cx))
                .when(encrypting, |el| {
                    el.child(
                        h_flex()
                            .gap_2()
                            .child(div().flex_1().child(self.input_small("transfer-pass")))
                            .child(
                                div()
                                    .flex_1()
                                    .child(self.input_small("transfer-pass-again")),
                            ),
                    )
                })
                .child(check(
                    "transfer-plain",
                    tr.plain && !secret,
                    secret,
                    t("transfer.export.plain"),
                    cx.listener(|this, _: &ClickEvent, _, cx| {
                        let tr = &mut this.settings_page.transfer;
                        tr.plain = !tr.plain;
                        cx.notify();
                    }),
                ))
                .when(secret, |el| {
                    el.child(note(t("transfer.export.plainRefused")))
                })
                .into_any_element(),
        );
        let running = tr.export_job.as_ref().is_some_and(|j| {
            self.settings_page
                .setup
                .jobs
                .get(j)
                .and_then(|v| v.job.as_ref())
                .is_none_or(|j| j.status == agentbox_api::JOB_RUNNING)
        });
        row.body.push(
            h_flex()
                .flex_wrap()
                .gap_3()
                .child(button(
                    "transfer-export-start",
                    t("transfer.export.start"),
                    Btn::Primary,
                    !ready || tr.busy || running,
                    cx.listener(|this, _: &ClickEvent, _, cx| this.start_export(cx)),
                ))
                .child(note(not_yet.unwrap_or_else(|| {
                    tv(
                        "transfer.export.total",
                        &[("size", (&format_bytes(total)).into())],
                    )
                })))
                .when_some(problem, |el, p| el.child(note_in(p, theme::starting())))
                .into_any_element(),
        );
        if let Some(e) = &tr.export_error {
            row.body.push(note_in(e.clone(), theme::error()));
        }
        if let Some(j) = &tr.export_job {
            row.body.push(self.job_progress(j));
            if let Some(r) = self
                .job_result(j)
                .and_then(|v| serde_json::from_value::<InstanceExportResult>(v).ok())
            {
                row.body.push(note_in(
                    format!(
                        "{} {}",
                        tv(
                            "transfer.export.done",
                            &[
                                ("path", (&r.path).into()),
                                ("size", (&format_bytes(r.size)).into())
                            ],
                        ),
                        if r.encrypted {
                            t("transfer.export.encrypted")
                        } else {
                            t("transfer.export.notEncrypted")
                        }
                    ),
                    theme::running(),
                ));
            }
        }
        row.render()
    }

    fn import_row(&self, e: &Entry, cx: &mut Context<Self>) -> AnyElement {
        let tr = &self.settings_page.transfer;
        let mut row = self.more_row(e, None, cx);
        row.body
            .push(self.path_field("transfer-import-path", false, cx));
        if tr.ask_passphrase {
            row.body.push(self.input_small("transfer-import-pass"));
        }
        row.body.push(
            h_flex()
                .child(button(
                    "transfer-open",
                    t("transfer.import.open"),
                    Btn::Secondary,
                    tr.busy || self.field_value("transfer-import-path", cx).is_empty(),
                    cx.listener(|this, _: &ClickEvent, _, cx| this.inspect_import(cx)),
                ))
                .into_any_element(),
        );
        if let Some(e) = &tr.inspect_error {
            row.body.push(note_in(e.clone(), theme::error()));
        }
        if let Some(c) = &tr.contents {
            row.body.push(note(tv(
                "transfer.import.about",
                &[
                    ("date", (&local_date_time(&c.created_at)).into()),
                    ("app", (&c.app).into()),
                    ("encrypted", if c.encrypted { "yes" } else { "no" }.into()),
                ],
            )));
            for p in &c.projects {
                let pick = self.import_pick(&p.name);
                let (name, on) = (p.name.clone(), pick.on);
                let mut box_ = v_flex()
                    .id(SharedString::from(format!("transfer-import-{}", p.name)))
                    .gap_2()
                    .p_3()
                    .rounded_xl()
                    .border_1()
                    .border_color(theme::border())
                    .bg(theme::bg())
                    .child(
                        h_flex()
                            .gap_2()
                            .child(check(
                                SharedString::from(format!("transfer-ip-{}", p.name)),
                                on,
                                false,
                                div()
                                    .font_weight(FontWeight::MEDIUM)
                                    .child(p.display_name.clone()),
                                cx.listener(move |this, _: &ClickEvent, _, cx| {
                                    this.set_import_pick(&name, |k| k.on = !on);
                                    cx.notify();
                                }),
                            ))
                            .child(if p.exists {
                                badge(t("transfer.import.merges"), theme::accent())
                            } else {
                                badge(t("transfer.import.new"), theme::running())
                            }),
                    );
                if on {
                    box_ = box_.child(h_flex().flex_wrap().gap_x_4().gap_y_1p5().pl_6().children(
                        p.categories.iter().map(|k| {
                            let (name, kind) = (p.name.clone(), k.kind.clone());
                            div().min_w(px(180.)).child(check(
                                SharedString::from(format!("transfer-ik-{}-{}", p.name, k.kind)),
                                pick.kinds.contains(&k.kind),
                                false,
                                h_flex().gap_1().child(kind_label(&k.kind)).child(
                                    div().text_color(theme::faint()).child(tv(
                                        "transfer.count",
                                        &[
                                            ("count", k.items.into()),
                                            ("size", (&format_bytes(k.size)).into()),
                                        ],
                                    )),
                                ),
                                cx.listener(move |this, _: &ClickEvent, _, cx| {
                                    this.set_import_pick(&name, |p| {
                                        if !p.kinds.remove(&kind) {
                                            p.kinds.insert(kind.clone());
                                        }
                                    });
                                    cx.notify();
                                }),
                            ))
                        }),
                    ));
                    if !p.exists && pick.kinds.contains("repository") {
                        let plan = if p.plan.is_empty() { "none" } else { &p.plan };
                        let mut line = t(&format!("transfer.plan.{plan}"));
                        if !p.remote.is_empty() {
                            line = format!("{line} — {}", p.remote);
                        }
                        if p.bundled {
                            line = format!("{line} · {}", t("transfer.import.bundled"));
                        }
                        box_ = box_.child(
                            v_flex()
                                .gap_1()
                                .pl_6()
                                .child(note(line))
                                .child(mono(p.path.clone()))
                                .when(!p.plan_error.is_empty(), |el| {
                                    el.child(note_in(p.plan_error.clone(), theme::starting()))
                                }),
                        );
                    }
                }
                row.body.push(box_.into_any_element());
            }
            if !c.logins.is_empty() {
                let on = self.import_logins_on();
                row.body.push(
                    v_flex()
                        .gap_1p5()
                        .child(
                            div()
                                .text_xs()
                                .font_weight(FontWeight::MEDIUM)
                                .text_color(theme::muted())
                                .child(t("transfer.logins.title")),
                        )
                        .child(h_flex().flex_wrap().gap_x_4().gap_y_1p5().children(
                            c.logins.iter().map(|l| {
                                let kind = l.kind.clone();
                                let all = on.clone();
                                div().min_w(px(180.)).child(check(
                                    SharedString::from(format!("transfer-il-{}", l.kind)),
                                    on.contains(&l.kind),
                                    l.items == 0,
                                    h_flex().gap_1().child(login_label(&l.kind)).child(
                                        div().text_color(theme::faint()).child(tv(
                                            "transfer.accounts",
                                            &[("count", l.items.into())],
                                        )),
                                    ),
                                    cx.listener(move |this, _: &ClickEvent, _, cx| {
                                        let mut next = all.clone();
                                        if !next.remove(&kind) {
                                            next.insert(kind.clone());
                                        }
                                        this.settings_page.transfer.import_logins = Some(next);
                                        cx.notify();
                                    }),
                                ))
                            }),
                        ))
                        .into_any_element(),
                );
            }
            let running = tr.import_job.as_ref().is_some_and(|j| {
                self.settings_page
                    .setup
                    .jobs
                    .get(j)
                    .and_then(|v| v.job.as_ref())
                    .is_none_or(|j| j.status == agentbox_api::JOB_RUNNING)
            });
            row.body.push(
                h_flex()
                    .child(button(
                        "transfer-import-start",
                        t("transfer.import.start"),
                        Btn::Primary,
                        tr.busy || running,
                        cx.listener(|this, _: &ClickEvent, _, cx| this.start_instance_import(cx)),
                    ))
                    .into_any_element(),
            );
        }
        if let Some(e) = &tr.import_error {
            row.body.push(note_in(e.clone(), theme::error()));
        }
        if let Some(j) = &tr.import_job {
            row.body.push(self.job_progress(j));
            if let Some(r) = self
                .job_result(j)
                .and_then(|v| serde_json::from_value::<InstanceImportReport>(v).ok())
            {
                row.body.push(self.import_report(&r));
            }
        }
        row.render()
    }

    fn import_report(&self, r: &InstanceImportReport) -> AnyElement {
        let col = |text: String, w: Option<f32>, color: Hsla| {
            let el = div().text_xs().text_color(color);
            match w {
                Some(w) => el.w(px(w)).flex_none().child(text),
                None => el.flex_1().min_w_0().child(text),
            }
        };
        v_flex()
            .gap_2()
            .p_3()
            .rounded_xl()
            .border_1()
            .border_color(theme::border())
            .bg(theme::bg())
            .child(
                div()
                    .text_sm()
                    .font_weight(FontWeight::MEDIUM)
                    .text_color(theme::fg())
                    .child(tv(
                        "transfer.report.summary",
                        &[
                            ("added", r.added.into()),
                            ("skipped", r.skipped.into()),
                            ("conflicted", r.conflicted.into()),
                        ],
                    )),
            )
            .when(!r.projects_added.is_empty(), |el| {
                el.child(note(tv(
                    "transfer.report.newProjects",
                    &[("names", (&r.projects_added.join(", ")).into())],
                )))
            })
            .child(
                h_flex()
                    .gap_2()
                    .child(col(t("transfer.report.part"), None, theme::muted()))
                    .child(col(t("transfer.report.added"), Some(80.), theme::muted()))
                    .child(col(
                        t("transfer.report.skipped"),
                        Some(100.),
                        theme::muted(),
                    ))
                    .child(col(
                        t("transfer.report.conflicted"),
                        Some(160.),
                        theme::muted(),
                    )),
            )
            .children(r.entries.iter().map(|e| {
                h_flex()
                    .gap_2()
                    .items_start()
                    .child(
                        v_flex()
                            .flex_1()
                            .min_w_0()
                            .child(mono(e.id.clone()))
                            .children(e.notes.iter().map(|n| note(n.clone()))),
                    )
                    .child(col(e.added.to_string(), Some(80.), theme::fg()))
                    .child(col(e.skipped.to_string(), Some(100.), theme::fg()))
                    .child(col(
                        e.conflicted.to_string(),
                        Some(160.),
                        if e.conflicted > 0 {
                            theme::starting()
                        } else {
                            theme::fg()
                        },
                    ))
            }))
            .when(!r.report_path.is_empty(), |el| {
                el.child(note(tv(
                    "transfer.report.saved",
                    &[("path", (&r.report_path).into())],
                )))
            })
            .into_any_element()
    }
}
