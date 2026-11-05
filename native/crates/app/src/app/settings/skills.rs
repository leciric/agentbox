//! Settings → Skills, and a project's (SkillsPanel.tsx): the skills the
//! daemon keeps and installs into every agent and chat, switched on or off
//! AgentBox-wide (PATCH /v1/skills/{name}) or for one project (PUT
//! /v1/projects/{p}/skills/{name}), written here (PUT /v1/skills/{name}),
//! deleted, and imported from the user's AI tools, a folder or a git
//! repository (POST /v1/skills/scan, /import). Electron's dialogs are drawn
//! in the section itself: a skill opened, a new one, or an import, in place
//! of the list, with a way back to it.
use std::collections::{HashMap, HashSet};

use agentbox_api::client::Method;
use agentbox_api::{
    ImportSkillsRequest, SaveSkillRequest, ScanSkillsRequest, Skill, SkillCandidate, SkillDetail,
    SkillOverrideRequest, UpdateSkillRequest,
};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::input::Textarea;
use gpui_component::text::TextView;
use gpui_component::{h_flex, v_flex};

use super::super::AppView;
use super::super::controls::{Btn, badge, button, note, note_in, segmented, toggle};
use super::defaults::time_ago;
use super::kit::{ask, card, check, empty, enc, link, mono, title};
use super::{Entry, Group, group};
use crate::theme;

/// Where a skill dialog is open: Electron's SkillDialog and ImportDialog.
#[derive(Clone, PartialEq)]
enum View {
    List,
    Open(String),
    New,
    Import,
}

#[derive(Default)]
pub(super) struct Skills {
    /// lists are each scope's skills: "" for AgentBox-wide, else a project.
    lists: HashMap<String, Vec<Skill>>,
    error: HashMap<String, String>,
    said: HashMap<String, String>,
    filter: String,
    /// view is what's open, and in which scope.
    view: Option<(String, View)>,
    detail: Option<SkillDetail>,
    file: String,
    editing: bool,
    deleting: bool,
    busy: bool,
    from: String,
    source: Option<String>,
    scan: Option<Result<Vec<SkillCandidate>, String>>,
    picked: HashSet<String>,
}

/// format_bytes is skills.ts's: 512 B, 1.5 KB, 12 KB, 3.4 MB.
pub(super) fn format_bytes(n: i64) -> String {
    let f = n as f64;
    if n < 1024 {
        format!("{n} B")
    } else if n < 1024 * 1024 {
        if n < 10 * 1024 {
            format!("{:.1} KB", f / 1024.)
        } else {
            format!("{:.0} KB", f / 1024.)
        }
    } else {
        format!("{:.1} MB", f / 1024. / 1024.)
    }
}

/// skill_title reads a name as words: "review-pr" is "Review Pr".
fn skill_title(name: &str) -> String {
    name.split(|c: char| c.is_whitespace() || ":_-".contains(c))
        .filter(|w| !w.is_empty())
        .map(|w| {
            let mut c = w.chars();
            match c.next() {
                Some(f) => f.to_uppercase().collect::<String>() + c.as_str(),
                None => String::new(),
            }
        })
        .collect::<Vec<_>>()
        .join(" ")
}

/// skill_name_of is what a typed name becomes: lowercase, digits, hyphens.
fn skill_name_of(text: &str) -> String {
    let mut out = String::new();
    for c in text.to_lowercase().chars() {
        if c.is_ascii_lowercase() || c.is_ascii_digit() {
            out.push(c);
        } else if !out.ends_with('-') {
            out.push('-');
        }
    }
    out.trim_matches('-').chars().take(64).collect()
}

fn skill_template(name: &str) -> String {
    format!(
        "---\nname: {name}\ndescription: What this skill does, and when to use it.\n---\n\n# {}\n\nSay here, step by step, what the agent should do when it uses this skill.\n",
        skill_title(name)
    )
}

/// frontmatter splits a SKILL.md into its fields and the rest.
fn frontmatter(content: &str) -> (Vec<(String, String)>, String) {
    let Some(rest) = content
        .strip_prefix("---\n")
        .or_else(|| content.strip_prefix("---\r\n"))
    else {
        return (Vec::new(), content.to_string());
    };
    let Some(end) = rest.find("\n---") else {
        return (Vec::new(), content.to_string());
    };
    let fields = rest[..end]
        .lines()
        .filter_map(|l| {
            let (k, v) = l.split_once(':')?;
            Some((k.trim().to_string(), v.trim().trim_matches('"').to_string()))
        })
        .collect();
    let body = rest[end + 4..].trim_start_matches(['-', '\r', '\n']);
    (fields, body.to_string())
}

/// origin_of is where a skill came from, by its source (skills.ts).
fn origin_of(source: &str) -> &'static str {
    if source.is_empty() {
        return "agentbox";
    }
    let prefix = source.split(':').next().unwrap_or_default();
    for o in [
        "claude",
        "claude-plugin",
        "codex",
        "agents",
        "opencode",
        "cursor",
        "gemini",
    ] {
        if prefix == o {
            return o;
        }
    }
    if ["http:", "https:", "ssh:", "git@", "github.com/"]
        .iter()
        .any(|p| source.starts_with(p))
    {
        return "git";
    }
    "folder"
}

fn origin_label(origin: &str, plugin: &str) -> String {
    match origin {
        "agentbox" => t("skills.origin.agentbox"),
        "claude" => "Claude Code".into(),
        "claude-plugin" if !plugin.is_empty() => {
            tv("skills.origin.pluginNamed", &[("plugin", plugin.into())])
        }
        "claude-plugin" => t("skills.origin.plugin"),
        "codex" => "Codex".into(),
        "agents" => "~/.agents".into(),
        "opencode" => "OpenCode".into(),
        "cursor" => "Cursor".into(),
        "gemini" => "Gemini CLI".into(),
        "git" => t("skills.origin.git"),
        _ => t("skills.origin.folder"),
    }
}

fn source_label(source: &str) -> String {
    let origin = origin_of(source);
    let plugin = if origin == "claude-plugin" {
        source
            .split(':')
            .nth(1)
            .and_then(|p| p.split('/').next())
            .unwrap_or_default()
    } else {
        ""
    };
    origin_label(origin, plugin)
}

/// score is how well a skill matches a search (skills.ts's scoreSkill),
/// lower first; None when it doesn't.
fn score(s: &Skill, q: &str) -> Option<i64> {
    let at = |value: &str, base: i64, fuzzy: bool| -> Option<i64> {
        if value.is_empty() {
            return None;
        }
        if value == q {
            return Some(base);
        }
        if value.starts_with(q) {
            return Some(base + 2);
        }
        if value.match_indices(q).any(|(i, _)| {
            i == 0
                || value[..i]
                    .chars()
                    .last()
                    .is_some_and(|c| c.is_whitespace() || "/_:-".contains(c))
        }) {
            return Some(base + 4);
        }
        if value.contains(q) {
            return Some(base + 6);
        }
        if fuzzy {
            let mut rest = value;
            for ch in q.chars() {
                let i = rest.find(ch)?;
                rest = &rest[i + ch.len_utf8()..];
            }
            return Some(base + 100);
        }
        None
    };
    [
        at(&s.name.to_lowercase(), 0, true),
        at(&skill_title(&s.name).to_lowercase(), 1, false),
        at(&s.description.to_lowercase(), 30, false),
    ]
    .into_iter()
    .flatten()
    .min()
}

fn search_skills<'a>(skills: &'a [Skill], query: &str) -> Vec<&'a Skill> {
    let q = query
        .trim()
        .to_lowercase()
        .trim_start_matches(['$', '/'])
        .to_string();
    let mut out: Vec<(&Skill, i64)> = skills
        .iter()
        .filter_map(|s| {
            if q.is_empty() {
                Some((s, 0))
            } else {
                score(s, &q).map(|n| (s, n))
            }
        })
        .collect();
    out.sort_by(|a, b| a.1.cmp(&b.1).then_with(|| a.0.name.cmp(&b.0.name)));
    out.into_iter().map(|(s, _)| s).collect()
}

fn skills_path(scope: &str) -> String {
    if scope.is_empty() {
        "/v1/skills".into()
    } else {
        format!("/v1/projects/{}/skills", enc(scope))
    }
}

/// hue is the skill's own colour, from its name (skillHue).
fn hue(name: &str) -> f32 {
    let mut h: u32 = 0;
    for ch in name.encode_utf16() {
        h = h.wrapping_mul(31).wrapping_add(ch as u32);
    }
    (h % 360) as f32 / 360.
}

/// tile is SkillTile: the skill's initials on its colour.
fn tile(name: &str, dim: bool) -> AnyElement {
    let letters: String = name
        .split(|c: char| c.is_whitespace() || ":_-".contains(c))
        .filter(|w| !w.is_empty())
        .take(2)
        .filter_map(|w| w.chars().next())
        .flat_map(char::to_uppercase)
        .collect();
    let h = hue(name);
    div()
        .flex_none()
        .size(px(36.))
        .rounded_lg()
        .flex()
        .items_center()
        .justify_center()
        .bg(hsla(h, 0.8, 0.6, 0.25))
        .text_color(hsla(h, 0.9, 0.78, 1.))
        .text_sm()
        .font_weight(FontWeight::SEMIBOLD)
        .when(dim, |el| el.opacity(0.45))
        .child(if letters.is_empty() {
            "?".into()
        } else {
            letters
        })
        .into_any_element()
}

impl AppView {
    pub(super) fn skills_groups(&self) -> Vec<Group> {
        vec![
            group(
                "skills",
                "settings.group.skills.title",
                vec![Entry {
                    id: "skills",
                    label: t("settings.section.skills.title"),
                    description: t("settings.section.skills.description"),
                    keywords: t("settings.entry.skills.keywords"),
                    essential: false,
                    modified: false,
                    scope: String::new(),
                }],
            )
            .cards(),
        ]
    }

    pub(super) fn load_skills(&mut self, scope: String, cx: &mut Context<Self>) {
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client.get::<Vec<Skill>>(&skills_path(&scope)).await;
            this.update(cx, |this, cx| {
                let s = &mut this.settings_page.skills;
                match res {
                    Ok(list) => {
                        s.lists.insert(scope.clone(), list);
                        s.error.remove(&scope);
                    }
                    Err(e) => {
                        s.error.insert(scope, e.to_string());
                    }
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// skill_call sends one request about a scope's skills, then reads
    /// them again; done runs on its answer.
    #[allow(clippy::too_many_arguments)]
    fn skill_call(
        &mut self,
        scope: String,
        method: Method,
        path: String,
        body: Option<Vec<u8>>,
        window: &mut Window,
        cx: &mut Context<Self>,
        done: impl FnOnce(&mut Self, &[u8], &mut Window, &mut Context<Self>) + 'static,
    ) {
        let client = self.client.clone();
        self.settings_page.skills.busy = true;
        cx.spawn_in(window, async move |this, cx| {
            let res = client.raw(method, &path, body).await;
            this.update_in(cx, |this, window, cx| {
                let s = &mut this.settings_page.skills;
                s.busy = false;
                match res {
                    Ok(b) => {
                        s.error.remove(&scope);
                        done(this, &b, window, cx);
                    }
                    Err(e) => {
                        s.error.insert(scope.clone(), e.to_string());
                    }
                }
                this.load_skills(scope, cx);
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn toggle_skill(
        &mut self,
        scope: String,
        skill: &Skill,
        on: bool,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let name = enc(&skill.name);
        let (method, path, body) = if scope.is_empty() {
            (
                Method::PATCH,
                format!("/v1/skills/{name}"),
                serde_json::to_vec(&UpdateSkillRequest { enabled: Some(on) }),
            )
        } else {
            // Back to following the AgentBox-wide switch when it agrees.
            let r#override = if on == skill.enabled {
                ""
            } else if on {
                "on"
            } else {
                "off"
            };
            (
                Method::PUT,
                format!("/v1/projects/{}/skills/{name}", enc(&scope)),
                serde_json::to_vec(&SkillOverrideRequest {
                    r#override: r#override.into(),
                }),
            )
        };
        self.skill_call(scope, method, path, body.ok(), window, cx, |_, _, _, _| {});
    }

    fn open_skill(&mut self, scope: String, name: String, cx: &mut Context<Self>) {
        let s = &mut self.settings_page.skills;
        s.view = Some((scope, View::Open(name.clone())));
        s.detail = None;
        s.file = "SKILL.md".into();
        s.editing = false;
        s.deleting = false;
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client
                .get::<SkillDetail>(&format!("/v1/skills/{}", enc(&name)))
                .await;
            this.update(cx, |this, cx| {
                let s = &mut this.settings_page.skills;
                match res {
                    Ok(d) => s.detail = Some(d),
                    Err(e) => {
                        let scope = s.view.as_ref().map(|v| v.0.clone()).unwrap_or_default();
                        s.error.insert(scope, e.to_string());
                    }
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    fn set_draft(&mut self, text: String, window: &mut Window, cx: &mut Context<Self>) {
        self.settings_page
            .skill_draft
            .update(cx, |d, cx| d.set_value(text, window, cx));
    }

    fn new_skill(&mut self, scope: String, window: &mut Window, cx: &mut Context<Self>) {
        let s = &mut self.settings_page.skills;
        s.view = Some((scope, View::New));
        s.editing = true;
        self.clear_field("skill-name", window, cx);
        self.set_draft(skill_template("my-skill"), window, cx);
        cx.notify();
    }

    fn edit_skill(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let content = self
            .settings_page
            .skills
            .detail
            .as_ref()
            .and_then(|d| d.files.iter().find(|f| f.path == "SKILL.md"))
            .map(|f| f.content.clone())
            .unwrap_or_default();
        self.settings_page.skills.editing = true;
        self.set_draft(content, window, cx);
        cx.notify();
    }

    /// skill_name_typed keeps the draft's name and title the name typed.
    pub(super) fn skill_name_typed(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let name = skill_name_of(&self.field_value("skill-name", cx));
        let name = if name.is_empty() {
            "my-skill".to_string()
        } else {
            name
        };
        let draft = self.settings_page.skill_draft.read(cx).value().to_string();
        let mut out = Vec::new();
        let mut in_front = false;
        let mut titled = false;
        for (i, line) in draft.lines().enumerate() {
            if i == 0 && line == "---" {
                in_front = true;
                out.push(line.to_string());
                continue;
            }
            if in_front && line == "---" {
                in_front = false;
            } else if in_front && line.starts_with("name:") {
                out.push(format!("name: {name}"));
                continue;
            } else if !in_front && !titled && line.starts_with("# ") {
                titled = true;
                out.push(format!("# {}", skill_title(&name)));
                continue;
            }
            out.push(line.to_string());
        }
        let next = out.join("\n") + if draft.ends_with('\n') { "\n" } else { "" };
        if next != draft {
            self.set_draft(next, window, cx);
        }
    }

    fn save_skill(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let Some((scope, view)) = self.settings_page.skills.view.clone() else {
            return;
        };
        let creating = view == View::New;
        let target = match &view {
            View::Open(n) => n.clone(),
            _ => skill_name_of(&self.field_value("skill-name", cx)),
        };
        if target.is_empty() {
            return;
        }
        let content = self.settings_page.skill_draft.read(cx).value().to_string();
        let body = serde_json::to_vec(&SaveSkillRequest {
            content,
            enabled: None,
            project: if creating {
                scope.clone()
            } else {
                String::new()
            },
        })
        .ok();
        let said_scope = scope.clone();
        self.skill_call(
            scope.clone(),
            Method::PUT,
            format!("/v1/skills/{}", enc(&target)),
            body,
            window,
            cx,
            move |this, b, _, cx| {
                let saved: Skill = serde_json::from_slice(b).unwrap_or_default();
                let key = if creating {
                    "skills.created"
                } else {
                    "skills.saved"
                };
                this.settings_page
                    .skills
                    .said
                    .insert(said_scope, tv(key, &[("name", (&saved.name).into())]));
                this.open_skill(scope, saved.name, cx);
            },
        );
    }

    fn delete_skill(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let Some((scope, View::Open(name))) = self.settings_page.skills.view.clone() else {
            return;
        };
        let said_scope = scope.clone();
        self.skill_call(
            scope,
            Method::DELETE,
            format!("/v1/skills/{}", enc(&name)),
            None,
            window,
            cx,
            move |this, _, _, _| {
                let s = &mut this.settings_page.skills;
                s.view = None;
                s.detail = None;
                s.said.insert(
                    said_scope,
                    tv("skills.deleted", &[("name", (&name).into())]),
                );
            },
        );
    }

    fn start_import(&mut self, scope: String, cx: &mut Context<Self>) {
        let s = &mut self.settings_page.skills;
        s.view = Some((scope, View::Import));
        s.from = "tools".into();
        s.picked.clear();
        self.scan_skills(String::new(), cx);
    }

    /// scan_skills looks for skills at a source: "" for the user's AI tools.
    pub(super) fn scan_skills(&mut self, source: String, cx: &mut Context<Self>) {
        let s = &mut self.settings_page.skills;
        s.source = Some(source.clone());
        s.scan = None;
        s.picked.clear();
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client
                .post::<_, Vec<SkillCandidate>>(
                    "/v1/skills/scan",
                    &ScanSkillsRequest {
                        source: source.clone(),
                    },
                )
                .await;
            this.update(cx, |this, cx| {
                let s = &mut this.settings_page.skills;
                if s.source.as_deref() != Some(source.as_str()) {
                    return;
                }
                // Everything that can come in is picked, as Electron does.
                if let Ok(list) = &res {
                    s.picked = list
                        .iter()
                        .filter(|c| c.problem.is_empty())
                        .map(|c| c.name.clone())
                        .collect();
                }
                s.scan = Some(res.map_err(|e| e.to_string()));
                cx.notify();
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    fn import_skills(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let Some((scope, _)) = self.settings_page.skills.view.clone() else {
            return;
        };
        let s = &self.settings_page.skills;
        let body = serde_json::to_vec(&ImportSkillsRequest {
            source: s.source.clone().unwrap_or_default(),
            names: s.picked.iter().cloned().collect(),
            project: scope.clone(),
        })
        .ok();
        let said_scope = scope.clone();
        self.skill_call(
            scope,
            Method::POST,
            "/v1/skills/import".into(),
            body,
            window,
            cx,
            move |this, b, _, _| {
                let list: Vec<Skill> = serde_json::from_slice(b).unwrap_or_default();
                let s = &mut this.settings_page.skills;
                s.view = None;
                s.said.insert(
                    said_scope,
                    tv("skills.imported", &[("count", list.len().into())]),
                );
            },
        );
    }

    /// skills_look is Look in the import's folder or git field.
    pub(super) fn skills_look(&mut self, cx: &mut Context<Self>) {
        let field = if self.settings_page.skills.from == "git" {
            "skills-git"
        } else {
            "skills-folder"
        };
        let v = self.field_value(field, cx);
        if !v.is_empty() {
            self.scan_skills(v, cx);
        }
    }

    /// skills_card is the whole panel, for AgentBox (scope "") or a project.
    pub(super) fn skills_card(&self, scope: &str, cx: &mut Context<Self>) -> AnyElement {
        let s = &self.settings_page.skills;
        let view = s
            .view
            .as_ref()
            .filter(|(sc, _)| sc == scope)
            .map(|(_, v)| v.clone())
            .unwrap_or(View::List);
        // A skill opened, written or imported is a dialog over the list,
        // as the Electron app's SkillsPanel opens them.
        let dialog = match view {
            View::List => None,
            View::Open(name) => Some(self.skill_open(scope, &name, cx)),
            View::New => Some(self.skill_editor(scope, true, cx)),
            View::Import => Some(self.skills_import(scope, cx)),
        };
        let body = self.skills_list(scope, cx);
        let dialog = dialog.map(|d| {
            let close = cx.listener(|this, _: &ClickEvent, _, cx| {
                let s = &mut this.settings_page.skills;
                s.view = None;
                s.editing = false;
                s.deleting = false;
                cx.notify();
            });
            crate::app::controls::window_modal(
                format!("skill-dialog-{scope}"),
                px(760.),
                div()
                    .id("skill-dialog-body")
                    .flex_1()
                    .min_h_0()
                    .overflow_y_scroll()
                    .p_6()
                    .child(d),
                std::rc::Rc::new(close),
            )
        });
        v_flex()
            .id(SharedString::from(format!("skills-{scope}")))
            .gap_3()
            .when_some(s.error.get(scope), |el, e| {
                el.child(note_in(e.clone(), theme::error()))
            })
            .when_some(s.said.get(scope), |el, m| {
                el.child(note_in(m.clone(), theme::running()))
            })
            .child(body)
            .children(dialog)
            .into_any_element()
    }

    fn back_to_skills(&self, cx: &mut Context<Self>) -> AnyElement {
        // The dialog's close (it was a page's Back).
        crate::app::controls::button(
            "skills-back",
            "✕",
            crate::app::controls::Btn::Ghost,
            false,
            cx.listener(|this, _: &ClickEvent, _, cx| {
                let s = &mut this.settings_page.skills;
                s.view = None;
                s.editing = false;
                s.deleting = false;
                cx.notify();
            }),
        )
    }

    fn skills_list(&self, scope: &str, cx: &mut Context<Self>) -> AnyElement {
        let s = &self.settings_page.skills;
        let project = !scope.is_empty();
        let is_on = |k: &Skill| {
            if project {
                k.active.unwrap_or(false)
            } else {
                k.enabled
            }
        };
        let all = s.lists.get(scope);
        let list = all.map(Vec::as_slice).unwrap_or_default();
        let on = list.iter().filter(|k| is_on(k)).count();
        let filter = if s.filter.is_empty() {
            "all"
        } else {
            s.filter.as_str()
        };
        let filtered: Vec<Skill> = list
            .iter()
            .filter(|k| filter == "all" || (filter == "on") == is_on(k))
            .cloned()
            .collect();
        let query = self.field_value("skills-search", cx);
        let shown = search_skills(&filtered, &query);
        let (scope_a, scope_b, scope_c) = (scope.to_string(), scope.to_string(), scope.to_string());
        let head = h_flex()
            .flex_wrap()
            .gap_2()
            .child(
                v_flex()
                    .flex_1()
                    .min_w(px(240.))
                    .gap_2()
                    .child(note(if project {
                        t("skills.projectDescription")
                    } else {
                        t("skills.description")
                    }))
                    .child(
                        h_flex()
                            .gap_2()
                            .child(badge(
                                tv("skills.count", &[("count", list.len().into())]),
                                theme::accent(),
                            ))
                            .child(badge(
                                tv("skills.onCount", &[("count", on.into())]),
                                if on > 0 {
                                    theme::running()
                                } else {
                                    theme::muted()
                                },
                            )),
                    ),
            )
            .child(
                h_flex()
                    .gap_2()
                    .child(button(
                        SharedString::from(format!("skills-import-{scope}")),
                        t("skills.import"),
                        Btn::Secondary,
                        false,
                        cx.listener(move |this, _: &ClickEvent, _, cx| {
                            this.start_import(scope_a.clone(), cx)
                        }),
                    ))
                    .child(button(
                        SharedString::from(format!("skills-new-{scope}")),
                        t("skills.new"),
                        Btn::Primary,
                        false,
                        cx.listener(move |this, _: &ClickEvent, window, cx| {
                            this.new_skill(scope_b.clone(), window, cx)
                        }),
                    )),
            );
        let filters = ["all", "on", "off"]
            .into_iter()
            .map(|v| (SharedString::from(v), t(&format!("skills.filter.{v}"))))
            .collect();
        v_flex()
            .gap_3()
            .child(head)
            .when(all.is_none() && !s.error.contains_key(scope), |el| {
                el.child(note(t("common.loading")))
            })
            .when(!list.is_empty(), |el| {
                el.child(
                    h_flex()
                        .gap_3()
                        .child(div().flex_1().child(self.input_small("skills-search")))
                        .child(segmented(
                            &format!("skills-filter-{scope}"),
                            filters,
                            Some(filter),
                            false,
                            std::rc::Rc::new(cx.listener(|this, v: &str, _, cx| {
                                this.settings_page.skills.filter = v.to_string();
                                cx.notify();
                            })),
                        )),
                )
            })
            .when(all.is_some_and(|l| l.is_empty()), |el| {
                el.child(
                    card("skills-empty")
                        .items_center()
                        .py_8()
                        .child(title(t("skills.empty.title")))
                        .child(
                            div()
                                .max_w(px(420.))
                                .text_center()
                                .text_xs()
                                .text_color(theme::muted())
                                .child(t("skills.empty.body")),
                        )
                        .child(h_flex().gap_2().pt_2().child(button(
                            "skills-empty-import",
                            t("skills.empty.import"),
                            Btn::Primary,
                            false,
                            cx.listener(move |this, _: &ClickEvent, _, cx| {
                                this.start_import(scope_c.clone(), cx)
                            }),
                        ))),
                )
            })
            .when(!list.is_empty() && shown.is_empty(), |el| {
                el.child(note(t("skills.noMatch")))
            })
            .children(
                shown
                    .into_iter()
                    .map(|k| self.skill_row(scope, k, is_on(k), cx)),
            )
            .into_any_element()
    }

    fn skill_row(&self, scope: &str, k: &Skill, on: bool, cx: &mut Context<Self>) -> AnyElement {
        let project = !scope.is_empty();
        let override_here = project.then(|| k.overrides.get(scope).copied()).flatten();
        let (open_scope, open_name) = (scope.to_string(), k.name.clone());
        let (tog_scope, tog_skill) = (scope.to_string(), k.clone());
        let mut facts = vec![
            source_label(&k.source),
            format!(
                "{} · {}",
                tv("skills.files", &[("count", k.file_count.into())]),
                format_bytes(k.size)
            ),
        ];
        if let Some(o) = override_here {
            facts.push(t(if o { "skills.onHere" } else { "skills.offHere" }));
        } else if project {
            facts.push(t("skills.followsWide"));
        } else if !k.overrides.is_empty() {
            facts.push(tv(
                "skills.overrides",
                &[("count", k.overrides.len().into())],
            ));
        }
        card(SharedString::from(format!("skill-{scope}-{}", k.name)))
            .cursor_pointer()
            .hover(|st| st.bg(theme::raised()))
            .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                this.open_skill(open_scope.clone(), open_name.clone(), cx)
            }))
            .child(
                h_flex()
                    .gap_3()
                    .items_start()
                    .child(tile(&k.name, !on))
                    .child(
                        v_flex()
                            .flex_1()
                            .min_w_0()
                            .gap_0p5()
                            .child(
                                h_flex()
                                    .gap_2()
                                    .child(mono(format!("${}", k.name)))
                                    .when(!k.user_invocable, |el| {
                                        el.child(badge(t("skills.modelOnly"), theme::accent()))
                                    }),
                            )
                            .child(
                                div()
                                    .text_sm()
                                    .text_color(theme::muted())
                                    .line_clamp(2)
                                    .child(k.description.clone()),
                            )
                            .child(
                                div()
                                    .text_xs()
                                    .text_color(theme::faint())
                                    .child(facts.join(" · ")),
                            ),
                    )
                    .child(
                        div()
                            .id(SharedString::from(format!("skill-switch-{}", k.name)))
                            .on_click(|_, _, cx| cx.stop_propagation())
                            .child(toggle(
                                SharedString::from(format!("skill-toggle-{}", k.name)),
                                on,
                                self.settings_page.skills.busy,
                                cx.listener(move |this, _: &ClickEvent, window, cx| {
                                    cx.stop_propagation();
                                    this.toggle_skill(
                                        tog_scope.clone(),
                                        &tog_skill,
                                        !on,
                                        window,
                                        cx,
                                    )
                                }),
                            )),
                    ),
            )
            .into_any_element()
    }

    fn skill_open(&self, scope: &str, name: &str, cx: &mut Context<Self>) -> AnyElement {
        let s = &self.settings_page.skills;
        if s.editing {
            return self.skill_editor(scope, false, cx);
        }
        let summary = s
            .lists
            .get(scope)
            .and_then(|l| l.iter().find(|k| k.name == name))
            .cloned();
        let on = summary.as_ref().is_some_and(|k| {
            if scope.is_empty() {
                k.enabled
            } else {
                k.active.unwrap_or(false)
            }
        });
        let Some(d) = s.detail.as_ref() else {
            return v_flex()
                .gap_3()
                .child(self.back_to_skills(cx))
                .child(note(t("common.loading")))
                .into_any_element();
        };
        let file = d
            .files
            .iter()
            .find(|f| f.path == s.file)
            .or_else(|| d.files.first());
        let (tog_scope, tog_skill) = (scope.to_string(), summary.clone());
        let content: AnyElement = match file {
            Some(f) if f.binary => note(tv(
                "skills.binary",
                &[("size", (&format_bytes(f.size)).into())],
            )),
            Some(f) if f.path == "SKILL.md" => {
                let (fields, body) = frontmatter(&f.content);
                let description = fields
                    .iter()
                    .find(|(k, _)| k == "description")
                    .map(|(_, v)| v.clone())
                    .unwrap_or_default();
                let rest: Vec<_> = fields
                    .iter()
                    .filter(|(k, _)| k != "name" && k != "description")
                    .cloned()
                    .collect();
                v_flex()
                    .gap_4()
                    .child(
                        card("skill-when")
                            .child(
                                div()
                                    .text_xs()
                                    .text_color(theme::faint())
                                    .child(t("skills.whenToUse").to_uppercase()),
                            )
                            .child(
                                div()
                                    .text_sm()
                                    .text_color(if description.is_empty() {
                                        theme::error()
                                    } else {
                                        theme::fg()
                                    })
                                    .child(if description.is_empty() {
                                        t("skills.noDescription")
                                    } else {
                                        description
                                    }),
                            )
                            .when(!rest.is_empty(), |el| {
                                el.child(h_flex().flex_wrap().gap_1p5().children(
                                    rest.into_iter().map(|(k, v)| {
                                        h_flex()
                                            .gap_1p5()
                                            .px_2()
                                            .py_0p5()
                                            .rounded_md()
                                            .border_1()
                                            .border_color(theme::border())
                                            .child(mono(k))
                                            .child(div().text_xs().text_color(theme::fg()).child(v))
                                    }),
                                ))
                            }),
                    )
                    .child(TextView::markdown(
                        SharedString::from(format!("skill-md-{name}")),
                        body,
                    ))
                    .into_any_element()
            }
            Some(f) => div()
                .p_3()
                .rounded_lg()
                .bg(theme::bg())
                .font_family(crate::theme::mono())
                .text_xs()
                .text_color(theme::fg())
                .child(f.content.clone())
                .into_any_element(),
            None => note(t("common.loading")),
        };
        v_flex()
            .gap_3()
            .child(self.back_to_skills(cx))
            .child(
                card("skill-detail")
                    .gap_3()
                    .child(
                        h_flex()
                            .gap_3()
                            .items_start()
                            .child(tile(name, !on))
                            .child(
                                v_flex()
                                    .flex_1()
                                    .min_w_0()
                                    .child(title(skill_title(name)))
                                    .child(mono(format!("${name}")))
                                    .child(div().text_xs().text_color(theme::faint()).child(
                                        format!(
                                            "{} · {} · {}",
                                            source_label(&d.source),
                                            tv("skills.files", &[("count", d.file_count.into())]),
                                            tv(
                                                "skills.updated",
                                                &[("when", (&time_ago(&d.updated_at)).into())]
                                            ),
                                        ),
                                    )),
                            )
                            .child(
                                h_flex()
                                    .gap_2()
                                    .child(div().text_xs().text_color(theme::muted()).child(
                                        if scope.is_empty() {
                                            t("skills.agentboxWide")
                                        } else {
                                            t("skills.inThisProject")
                                        },
                                    ))
                                    .child(toggle(
                                        "skill-open-toggle",
                                        on,
                                        s.busy || summary.is_none(),
                                        cx.listener(move |this, _: &ClickEvent, window, cx| {
                                            if let Some(k) = &tog_skill {
                                                this.toggle_skill(
                                                    tog_scope.clone(),
                                                    k,
                                                    !on,
                                                    window,
                                                    cx,
                                                )
                                            }
                                        }),
                                    ))
                                    .child(button(
                                        "skill-edit",
                                        t("skills.edit"),
                                        Btn::Secondary,
                                        false,
                                        cx.listener(|this, _: &ClickEvent, window, cx| {
                                            this.edit_skill(window, cx)
                                        }),
                                    ))
                                    .child(button(
                                        "skill-delete",
                                        t("skills.delete"),
                                        Btn::Ghost,
                                        false,
                                        cx.listener(|this, _: &ClickEvent, _, cx| {
                                            this.settings_page.skills.deleting = true;
                                            cx.notify();
                                        }),
                                    )),
                            ),
                    )
                    .when(s.deleting, |el| {
                        el.child(ask(
                            "skill-delete",
                            tv("skills.deleteTitle", &[("name", name.into())]),
                            t("skills.deleteDescription"),
                            t("skills.delete"),
                            true,
                            s.busy,
                            cx.listener(|this, _: &ClickEvent, window, cx| {
                                this.delete_skill(window, cx)
                            }),
                            cx.listener(|this, _: &ClickEvent, _, cx| {
                                this.settings_page.skills.deleting = false;
                                cx.notify();
                            }),
                        ))
                    })
                    .when(d.files.len() > 1, |el| {
                        el.child(
                            h_flex()
                                .flex_wrap()
                                .gap_1()
                                .children(d.files.iter().map(|f| {
                                    let path = f.path.clone();
                                    let active = file.is_some_and(|x| x.path == f.path);
                                    div()
                                        .id(SharedString::from(format!("skill-file-{}", f.path)))
                                        .px_2()
                                        .py_0p5()
                                        .rounded_md()
                                        .text_xs()
                                        .font_family(crate::theme::mono())
                                        .cursor_pointer()
                                        .when(active, |el| {
                                            el.bg(theme::raised()).text_color(theme::fg())
                                        })
                                        .when(!active, |el| {
                                            el.text_color(theme::muted())
                                                .hover(|st| st.bg(theme::hover()))
                                        })
                                        .on_click(cx.listener(
                                            move |this, _: &ClickEvent, _, cx| {
                                                this.settings_page.skills.file = path.clone();
                                                cx.notify();
                                            },
                                        ))
                                        .child(f.path.clone())
                                })),
                        )
                    })
                    .child(content),
            )
            .into_any_element()
    }

    fn skill_editor(&self, scope: &str, creating: bool, cx: &mut Context<Self>) -> AnyElement {
        let s = &self.settings_page.skills;
        let name_ok = !creating || !skill_name_of(&self.field_value("skill-name", cx)).is_empty();
        v_flex()
            .gap_3()
            .child(self.back_to_skills(cx))
            .child(
                card("skill-editor")
                    .gap_3()
                    .when(creating, |el| {
                        el.child(title(t("skills.newTitle")))
                            .child(note(if scope.is_empty() {
                                t("skills.newDescription")
                            } else {
                                t("skills.newDescriptionProject")
                            }))
                            .child(
                                h_flex()
                                    .gap_3()
                                    .child(
                                        div()
                                            .text_sm()
                                            .text_color(theme::muted())
                                            .child(t("skills.name")),
                                    )
                                    .child(div().w(px(260.)).child(self.input_small("skill-name")))
                                    .child(note(t("skills.nameHint"))),
                            )
                    })
                    .child(mono("SKILL.md"))
                    .child(Textarea::new(&self.settings_page.skill_draft).h(px(320.)))
                    .child(note(t("skills.editHint")))
                    .child(
                        h_flex()
                            .gap_2()
                            .child(button(
                                "skill-save",
                                if creating {
                                    t("skills.create")
                                } else {
                                    t("common.save")
                                },
                                Btn::Primary,
                                s.busy || !name_ok,
                                cx.listener(|this, _: &ClickEvent, window, cx| {
                                    this.save_skill(window, cx)
                                }),
                            ))
                            .child(button(
                                "skill-cancel",
                                t("common.cancel"),
                                Btn::Ghost,
                                false,
                                cx.listener(move |this, _: &ClickEvent, _, cx| {
                                    let s = &mut this.settings_page.skills;
                                    if creating {
                                        s.view = None;
                                    }
                                    s.editing = false;
                                    cx.notify();
                                }),
                            )),
                    ),
            )
            .into_any_element()
    }

    fn skills_import(&self, scope: &str, cx: &mut Context<Self>) -> AnyElement {
        let s = &self.settings_page.skills;
        let from = s.from.as_str();
        let tabs = ["tools", "folder", "git"]
            .into_iter()
            .map(|v| (SharedString::from(v), t(&format!("skills.importFrom.{v}"))))
            .collect();
        let found: &[SkillCandidate] = match &s.scan {
            Some(Ok(l)) => l,
            _ => &[],
        };
        let importable = found.iter().filter(|c| c.problem.is_empty()).count();
        let picked = s.picked.len();
        let all_names: Vec<String> = found
            .iter()
            .filter(|c| c.problem.is_empty())
            .map(|c| c.name.clone())
            .collect();
        let waiting: Option<String> = match (&s.source, &s.scan) {
            (None, _) => Some(if from == "git" {
                t("skills.gitPrompt")
            } else {
                t("skills.folderPrompt")
            }),
            (Some(_), None) => Some(if from == "git" {
                t("skills.cloning")
            } else {
                t("skills.looking")
            }),
            _ => None,
        };
        v_flex()
            .gap_3()
            .child(self.back_to_skills(cx))
            .child(
                card("skills-import")
                    .gap_3()
                    .child(title(t("skills.importTitle")))
                    .child(note(if scope.is_empty() {
                        t("skills.importDescription")
                    } else {
                        t("skills.importDescriptionProject")
                    }))
                    .child(segmented(
                        "skills-from",
                        tabs,
                        Some(from),
                        false,
                        std::rc::Rc::new(cx.listener(|this, v: &str, _, cx| {
                            this.settings_page.skills.from = v.to_string();
                            if v == "tools" {
                                this.scan_skills(String::new(), cx);
                            } else {
                                let s = &mut this.settings_page.skills;
                                s.source = None;
                                s.scan = None;
                                s.picked.clear();
                                cx.notify();
                            }
                        })),
                    ))
                    .when(from != "tools", |el| {
                        el.child(
                            h_flex()
                                .gap_2()
                                .child(div().flex_1().child(self.input_small(if from == "git" {
                                    "skills-git"
                                } else {
                                    "skills-folder"
                                })))
                                .child(button(
                                    "skills-look",
                                    t("skills.look"),
                                    Btn::Secondary,
                                    false,
                                    cx.listener(|this, _: &ClickEvent, _, cx| this.skills_look(cx)),
                                )),
                        )
                        .child(note(if from == "git" {
                            t("skills.gitHint")
                        } else {
                            t("skills.folderHint")
                        }))
                    })
                    .when_some(waiting, |el, w| el.child(note(w)))
                    .when_some(s.scan.as_ref().and_then(|r| r.as_ref().err()), |el, e| {
                        el.child(note_in(e.clone(), theme::error()))
                    })
                    .when(matches!(&s.scan, Some(Ok(l)) if l.is_empty()), |el| {
                        el.child(empty(
                            "skills-scan",
                            if from == "tools" {
                                t("skills.noneInTools")
                            } else {
                                t("skills.noneThere")
                            },
                            String::new(),
                        ))
                    })
                    .when(importable > 0, |el| {
                        el.child(
                            h_flex()
                                .gap_3()
                                .child(link(
                                    "skills-pick-all",
                                    t("skills.selectAll"),
                                    cx.listener(move |this, _: &ClickEvent, _, cx| {
                                        this.settings_page.skills.picked =
                                            all_names.iter().cloned().collect();
                                        cx.notify();
                                    }),
                                ))
                                .child(link(
                                    "skills-pick-none",
                                    t("skills.selectNone"),
                                    cx.listener(|this, _: &ClickEvent, _, cx| {
                                        this.settings_page.skills.picked.clear();
                                        cx.notify();
                                    }),
                                )),
                        )
                    })
                    .children(found.iter().map(|c| {
                        let name = c.name.clone();
                        let on = s.picked.contains(&c.name);
                        let words = v_flex()
                            .gap_0p5()
                            .child(
                                h_flex()
                                    .gap_2()
                                    .child(mono(format!("${}", c.name)))
                                    .child(div().text_xs().text_color(theme::faint()).child(
                                        format!(
                                            "{} · {} · {}",
                                            origin_label(&c.origin, &c.plugin),
                                            tv("skills.files", &[("count", c.files.into())]),
                                            format_bytes(c.size)
                                        ),
                                    ))
                                    .when(c.exists && c.problem.is_empty(), |el| {
                                        el.child(badge(t("skills.replaces"), theme::starting()))
                                    }),
                            )
                            .child(
                                div()
                                    .text_xs()
                                    .text_color(theme::muted())
                                    .child(c.description.clone()),
                            )
                            .when(!c.problem.is_empty(), |el| {
                                el.child(note_in(
                                    tv("skills.cantImport", &[("why", (&c.problem).into())]),
                                    theme::starting(),
                                ))
                            });
                        check(
                            SharedString::from(format!("skills-cand-{}", c.name)),
                            on,
                            !c.problem.is_empty(),
                            words,
                            cx.listener(move |this, _: &ClickEvent, _, cx| {
                                let p = &mut this.settings_page.skills.picked;
                                if !p.remove(&name) {
                                    p.insert(name.clone());
                                }
                                cx.notify();
                            }),
                        )
                    }))
                    .child(h_flex().gap_2().child(button(
                        "skills-import-go",
                        tv("skills.importCount", &[("count", picked.into())]),
                        Btn::Primary,
                        picked == 0 || s.busy,
                        cx.listener(|this, _: &ClickEvent, window, cx| {
                            this.import_skills(window, cx)
                        }),
                    ))),
            )
            .into_any_element()
    }
}

#[cfg(test)]
mod tests {
    use super::{
        Skill, format_bytes, frontmatter, origin_of, search_skills, skill_name_of, skill_title,
    };

    #[test]
    fn names_titles_and_sizes_read_as_skills_ts() {
        assert_eq!(skill_name_of("Review PR!"), "review-pr");
        assert_eq!(skill_title("review-pr"), "Review Pr");
        assert_eq!(format_bytes(512), "512 B");
        assert_eq!(format_bytes(1536), "1.5 KB");
        assert_eq!(format_bytes(20 * 1024), "20 KB");
        assert_eq!(origin_of(""), "agentbox");
        assert_eq!(origin_of("claude-plugin:x/y"), "claude-plugin");
        assert_eq!(origin_of("https://github.com/a/b"), "git");
        assert_eq!(origin_of("/home/me/skills"), "folder");
        let (f, body) = frontmatter("---\nname: a\ndescription: when\n---\n\n# A\n");
        assert_eq!(f[1], ("description".into(), "when".into()));
        assert_eq!(body, "# A\n");
    }

    #[test]
    fn search_puts_the_best_match_first() {
        let s = |n: &str, d: &str| Skill {
            name: n.into(),
            description: d.into(),
            ..Default::default()
        };
        let list = vec![
            s("lint-go", "check"),
            s("review-pr", "lint the diff"),
            s("go", ""),
        ];
        let names: Vec<_> = search_skills(&list, "go")
            .iter()
            .map(|k| k.name.clone())
            .collect();
        assert_eq!(names, ["go", "lint-go"]);
        let names: Vec<_> = search_skills(&list, "lint")
            .iter()
            .map(|k| k.name.clone())
            .collect();
        assert_eq!(names, ["lint-go", "review-pr"]);
    }
}
