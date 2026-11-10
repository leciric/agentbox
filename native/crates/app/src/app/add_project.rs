//! Add project (AddProjectDialog.tsx): a repository that's already there, a
//! new one AgentBox starts (git init and an initial commit), or one it clones
//! from a link in a job whose log the dialog shows (POST /v1/projects/clone).
//! The AI tool its agents run on, and which account of each tool it uses when
//! there is a choice, are picked as it's added.
use std::collections::BTreeMap;
use std::path::PathBuf;
use std::rc::Rc;
use std::time::Duration;

use agentbox_api::client::code_of;
use agentbox_api::{
    AddProjectRequest, AuthStatus, CloneProjectRequest, ERROR_BAD_REPO_URL, ERROR_FOLDER_EXISTS,
    ERROR_FOLDER_NOT_EMPTY, Job, Project, Settings, ToolAccount, UpdateProjectRequest,
    UpdateSettingsRequest,
};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::input::{Input, InputState};
use gpui_component::{h_flex, v_flex};

use super::AppView;
use super::agent::OnCancel;
use super::controls::{self, Btn, Choice};
use super::settings::kit::plain;
use super::sidebar_actions::label_of;
use super::sidebar_dialogs::Dialog;
use crate::chat::tool_label;
use crate::{theme, toasts};

/// AccountRow is a tool's accounts: its key, label, (name, default,
/// expired) and the names shown.
type AccountRow = (String, String, Vec<(String, bool, bool)>, Vec<String>);

pub struct AddProject {
    /// existing, new or clone.
    mode: &'static str,
    path: Entity<InputState>,
    name: Entity<InputState>,
    parent: Entity<InputState>,
    folder: Entity<InputState>,
    url: Entity<InputState>,
    clone_parent: Entity<InputState>,
    tool: String,
    /// The account picked per tool ("" is this machine's default): claude,
    /// github, codex, cursor, gemini and opencode:<provider>.
    accounts: BTreeMap<String, String>,
    auth: Option<AuthStatus>,
    settings: Option<Settings>,
    busy: bool,
    /// What the daemon said, and its code.
    error: Option<(String, String)>,
    job: Option<Job>,
    log: Vec<String>,
}

/// repo_name is the repository's name in a link, the folder git clone makes.
pub fn repo_name(url: &str) -> String {
    let s = url.trim().trim_end_matches('/');
    let at = s.rfind(['/', ':']).map_or(0, |i| i + 1);
    s[at..].trim_end_matches(".git").to_string()
}

/// looks_like_link is whether text could be a repository's link:
/// scheme://host/… or user@host:path.
fn looks_like_link(url: &str) -> bool {
    if url.chars().any(char::is_whitespace) || url.is_empty() {
        return false;
    }
    if let Some((scheme, rest)) = url.split_once("://") {
        return !scheme.is_empty()
            && scheme
                .chars()
                .all(|c| c.is_ascii_alphanumeric() || "+.-".contains(c))
            && rest
                .split_once('/')
                .is_some_and(|(h, p)| !h.is_empty() && !p.is_empty());
    }
    match url.split_once(':') {
        Some((host, path)) => host.len() >= 2 && !host.contains(['/', '\\']) && !path.is_empty(),
        None => false,
    }
}

fn join(parent: &str, name: &str) -> String {
    if parent.is_empty() || name.is_empty() {
        return String::new();
    }
    PathBuf::from(parent).join(name).display().to_string()
}

/// on_windows_drive is a folder on a Windows drive as WSL mounts it.
fn on_windows_drive(path: &str) -> bool {
    let b = path.as_bytes();
    path.starts_with("/mnt/")
        && b.len() >= 6
        && b[5].is_ascii_alphabetic()
        && (b.len() == 6 || b[6] == b'/')
}

fn accounts_of<'a>(auth: &'a AuthStatus, tool: &str) -> &'a [ToolAccount] {
    match tool {
        "codex" => &auth.codex_accounts,
        "cursor" => &auth.cursor_accounts,
        "gemini" => &auth.gemini_accounts,
        _ => &auth.open_code_accounts,
    }
}

impl AddProject {
    fn clone_path(&self, cx: &App) -> String {
        let url = self.url.read(cx).value().trim().to_string();
        if !looks_like_link(&url) {
            return String::new();
        }
        join(self.clone_parent.read(cx).value().trim(), &repo_name(&url))
    }
    fn new_path(&self, cx: &App) -> String {
        join(
            self.parent.read(cx).value().trim(),
            self.folder.read(cx).value().trim(),
        )
    }
    fn running(&self) -> bool {
        self.job.as_ref().is_some_and(|j| j.status == "running")
    }
    fn failed(&self) -> bool {
        self.job
            .as_ref()
            .is_some_and(|j| j.status != "running" && j.status != "succeeded")
    }
    fn code(&self) -> &str {
        self.error.as_ref().map_or("", |(_, c)| c.as_str())
    }
}

impl AppView {
    pub(super) fn open_add_project(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let input = |placeholder: &str, window: &mut Window, cx: &mut Context<Self>| {
            let p = placeholder.to_string();
            cx.new(|cx| InputState::new(window, cx).placeholder(p))
        };
        let path = input("/home/you/code/my-app", window, cx);
        let name = input("my-app", window, cx);
        // A new repository's parent starts as the folder the newest project
        // is in, likely where the user keeps their code.
        let parent_dir = self
            .projects
            .iter()
            .max_by(|a, b| a.created_at.cmp(&b.created_at))
            .and_then(|p| {
                PathBuf::from(&p.root)
                    .parent()
                    .map(|d| d.display().to_string())
            })
            .unwrap_or_default();
        let parent = input("/home/you/code", window, cx);
        parent.update(cx, |s, cx| s.set_value(parent_dir, window, cx));
        let folder = input("my-app", window, cx);
        let url = input("https://github.com/owner/repo", window, cx);
        let clone_parent = input("/home/you/src", window, cx);
        path.update(cx, |s, cx| s.focus(window, cx));
        self.side.dialog = Some(Dialog::AddProject(Box::new(AddProject {
            mode: "existing",
            path,
            name,
            parent,
            folder,
            url,
            clone_parent,
            tool: String::new(),
            accounts: BTreeMap::new(),
            auth: None,
            settings: None,
            busy: false,
            error: None,
            job: None,
            log: Vec::new(),
        })));
        cx.notify();
        let client = self.client.clone();
        cx.spawn_in(window, async move |this, cx| {
            let auth = client.get::<AuthStatus>("/v1/auth").await.ok();
            let settings = client.get::<Settings>("/v1/settings").await.ok();
            this.update_in(cx, |this, window, cx| {
                if let Some(Dialog::AddProject(d)) = &mut this.side.dialog {
                    d.auth = auth;
                    if let Some(s) = &settings {
                        let folder = s.clone_folder.clone();
                        d.clone_parent.update(cx, |i, cx| {
                            if i.value().is_empty() {
                                i.set_value(folder, window, cx)
                            }
                        });
                    }
                    d.settings = settings;
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn add_dialog(&mut self) -> Option<&mut AddProject> {
        match &mut self.side.dialog {
            Some(Dialog::AddProject(d)) => Some(d),
            _ => None,
        }
    }

    fn browse_into(&mut self, which: &'static str, window: &mut Window, cx: &mut Context<Self>) {
        let rx = super::sidebar_dialogs::pick(t("project.add.browse"), true, cx);
        cx.spawn_in(window, async move |this, cx| {
            let Some(dir) = rx.await.ok().flatten().and_then(|p| p.into_iter().next()) else {
                return;
            };
            this.update_in(cx, |this, window, cx| {
                if let Some(d) = this.add_dialog() {
                    let input = match which {
                        "parent" => d.parent.clone(),
                        "clone" => d.clone_parent.clone(),
                        _ => d.path.clone(),
                    };
                    input.update(cx, |s, cx| {
                        s.set_value(dir.display().to_string(), window, cx)
                    });
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn submit_add_project(&mut self, cx: &mut Context<Self>) {
        let client = self.client.clone();
        let Some(d) = self.add_dialog() else { return };
        let mut opencode = BTreeMap::new();
        let get = |k: &str| d.accounts.get(k).cloned().unwrap_or_default();
        let (claude, github, codex, cursor, gemini) = (
            get("claude"),
            get("github"),
            get("codex"),
            get("cursor"),
            get("gemini"),
        );
        for (k, v) in &d.accounts {
            if let Some(p) = k.strip_prefix("opencode:")
                && !v.is_empty()
            {
                opencode.insert(p.to_string(), v.clone());
            }
        }
        let tool = d.tool.clone();
        let code = d.code().to_string();
        let name = d.name.read(cx).value().trim().to_string();
        let cloning = d.mode == "clone";
        let creating = d.mode == "new";
        let clone_path = d.clone_path(cx);
        let clone_parent = d.clone_parent.read(cx).value().trim().to_string();
        let folder_was = d.settings.as_ref().map(|s| s.clone_folder.clone());
        // In clone mode with the folder in its way, this adds that folder as it is.
        if cloning && code != ERROR_FOLDER_EXISTS {
            let url = d.url.read(cx).value().trim().to_string();
            if url.is_empty() {
                return;
            }
            let req = CloneProjectRequest {
                url,
                path: clone_path,
                name,
                claude_account: claude,
                github_account: github,
                codex_account: codex,
                cursor_account: cursor,
                gemini_account: gemini,
                open_code_accounts: opencode,
                ..Default::default()
            };
            d.busy = true;
            d.error = None;
            d.job = None;
            d.log.clear();
            cx.notify();
            cx.spawn(async move |this, cx| {
                let res = client.post::<_, Job>("/v1/projects/clone", &req).await;
                let id = match res {
                    Ok(j) => {
                        let id = j.id.clone();
                        this.update(cx, |this, cx| {
                            if let Some(d) = this.add_dialog() {
                                d.job = Some(j);
                            }
                            cx.notify();
                        })
                        .ok();
                        id
                    }
                    Err(e) => {
                        let c = code_of(&e).to_string();
                        this.update(cx, |this, cx| {
                            if let Some(d) = this.add_dialog() {
                                d.busy = false;
                                d.error = Some((e.to_string(), c));
                            }
                            cx.notify();
                        })
                        .ok();
                        return;
                    }
                };
                // The job and its log, once a second, until it ends.
                loop {
                    cx.background_executor()
                        .timer(Duration::from_millis(800))
                        .await;
                    let job = client.get::<Job>(&format!("/v1/jobs/{id}")).await;
                    let log = client
                        .raw(
                            agentbox_api::client::Method::GET,
                            &format!("/v1/jobs/{id}/log"),
                            None,
                        )
                        .await;
                    let mut done = None;
                    let gone = this
                        .update(cx, |this, cx| {
                            let Some(d) = this.add_dialog() else {
                                return true;
                            };
                            if let Ok(b) = log {
                                d.log = String::from_utf8_lossy(&b)
                                    .lines()
                                    .map(String::from)
                                    .collect();
                            }
                            if let Ok(j) = job {
                                if j.status != "running" {
                                    d.busy = false;
                                    done = Some(j.clone());
                                }
                                d.job = Some(j);
                            }
                            cx.notify();
                            false
                        })
                        .unwrap_or(true);
                    if gone {
                        return;
                    }
                    if let Some(j) = done {
                        if j.status == "succeeded"
                            && let Ok(p) = serde_json::from_value::<Project>(j.result.clone())
                        {
                            // The folder used is where the next clone starts.
                            if folder_was.is_some_and(|f| f != clone_parent) {
                                let req = UpdateSettingsRequest {
                                    clone_folder: Some(clone_parent.clone()),
                                    ..Default::default()
                                };
                                let _ = client
                                    .raw(
                                        agentbox_api::client::Method::PATCH,
                                        "/v1/settings",
                                        serde_json::to_vec(&req).ok(),
                                    )
                                    .await;
                            }
                            added(this.clone(), client.clone(), p, tool.clone(), cx).await;
                        }
                        return;
                    }
                }
            })
            .detach();
            return;
        }
        let path = if cloning {
            clone_path
        } else if creating {
            d.new_path(cx)
        } else {
            d.path.read(cx).value().trim().to_string()
        };
        if path.is_empty() {
            return;
        }
        let req = AddProjectRequest {
            copy_to_linux: d.mode == "existing" && on_windows_drive(&path),
            path,
            name: if creating { String::new() } else { name },
            claude_account: claude,
            github_account: github,
            codex_account: codex,
            cursor_account: cursor,
            gemini_account: gemini,
            open_code_accounts: opencode,
            create: creating,
            commit_files: creating && code == ERROR_FOLDER_NOT_EMPTY,
        };
        d.busy = true;
        d.error = None;
        cx.notify();
        cx.spawn(async move |this, cx| {
            match client.post::<_, Project>("/v1/projects", &req).await {
                Ok(p) => added(this, client, p, tool, cx).await,
                Err(e) => {
                    let c = code_of(&e).to_string();
                    this.update(cx, |this, cx| {
                        if let Some(d) = this.add_dialog() {
                            d.busy = false;
                            d.error = Some((e.to_string(), c));
                        }
                        cx.notify();
                    })
                    .ok();
                }
            }
        })
        .detach();
    }

    pub(super) fn render_add_project(
        &self,
        d: &AddProject,
        cancel: OnCancel,
        cx: &mut Context<Self>,
    ) -> AnyElement {
        let view = cx.entity().downgrade();
        let busy = d.busy || d.running();
        let mode = d.mode;
        let code = d.code();
        let tabs = controls::segmented(
            "add-mode",
            vec![
                ("existing".into(), t("project.add.existing")),
                ("new".into(), t("project.add.new")),
                ("clone".into(), t("project.add.clone")),
            ],
            Some(mode),
            busy,
            Rc::new({
                let view = view.clone();
                move |v: &str, window: &mut Window, cx: &mut App| {
                    let m: &'static str = match v {
                        "new" => "new",
                        "clone" => "clone",
                        _ => "existing",
                    };
                    view.update(cx, |this, cx| {
                        if let Some(d) = this.add_dialog() {
                            d.mode = m;
                            d.error = None;
                            d.job = None;
                            d.log.clear();
                            let focus = match m {
                                "new" => d.folder.clone(),
                                "clone" => d.url.clone(),
                                _ => d.path.clone(),
                            };
                            focus.update(cx, |s, cx| s.focus(window, cx));
                        }
                        cx.notify();
                    })
                    .ok();
                }
            }),
        );
        let browse = |which: &'static str, cx: &mut Context<Self>| {
            controls::button(
                SharedString::from(format!("add-browse-{which}")),
                t("project.add.browse"),
                Btn::Secondary,
                busy,
                cx.listener(move |this, _: &ClickEvent, w, cx| this.browse_into(which, w, cx)),
            )
        };
        let with_browse =
            |input: &Entity<InputState>, which: &'static str, cx: &mut Context<Self>| {
                h_flex()
                    .gap_2()
                    .child(div().flex_1().min_w_0().child(Input::new(input)))
                    .child(browse(which, cx))
                    .into_any_element()
            };
        let mut body: Vec<AnyElement> = vec![
            div()
                .text_sm()
                .text_color(theme::muted())
                .child(
                    plain(&tv(
                        "project.add.description",
                        &[
                            ("kind", mode.into()),
                            ("where", t("project.add.thisMachine").into()),
                            ("branch", "\u{1}".into()),
                        ],
                    ))
                    .replace('\u{1}', "agentbox/<agent>"),
                )
                .into_any_element(),
            tabs,
        ];
        match mode {
            "clone" => {
                let clone_path = d.clone_path(cx);
                let mut url = vec![Input::new(&d.url).into_any_element()];
                if code == ERROR_BAD_REPO_URL {
                    url.push(controls::note_in(
                        d.error.as_ref().map(|e| e.0.clone()).unwrap_or_default(),
                        theme::error(),
                    ));
                }
                body.push(field(
                    t("project.add.urlLabel"),
                    v_flex().gap_1().children(url).into_any_element(),
                    Some(t("project.add.urlHint")),
                ));
                body.push(field(
                    t("project.add.cloneFolderLabel"),
                    with_browse(&d.clone_parent, "clone", cx),
                    Some(if clone_path.is_empty() {
                        t("project.add.cloneHintEmpty")
                    } else {
                        plain(&tv("project.add.cloneHint", &[("path", clone_path.into())]))
                    }),
                ));
                body.push(field(
                    t("project.add.nameLabel"),
                    Input::new(&d.name).into_any_element(),
                    Some(t("project.add.cloneNameHint")),
                ));
            }
            "new" => {
                let new_path = d.new_path(cx);
                body.push(field(
                    t("project.add.parentLabel"),
                    with_browse(&d.parent, "parent", cx),
                    None,
                ));
                body.push(field(
                    t("project.add.nameLabel"),
                    Input::new(&d.folder).into_any_element(),
                    Some(if new_path.is_empty() {
                        t("project.add.newHintEmpty")
                    } else {
                        plain(&tv("project.add.newHint", &[("path", new_path.into())]))
                    }),
                ));
            }
            _ => {
                body.push(field(
                    t("project.add.folderLabel"),
                    with_browse(&d.path, "path", cx),
                    None,
                ));
                if on_windows_drive(d.path.read(cx).value().trim()) {
                    body.push(controls::note_in(
                        plain(&t("project.add.windowsDrive")),
                        theme::accent(),
                    ));
                }
                body.push(field(
                    t("project.add.nameLabel"),
                    Input::new(&d.name).into_any_element(),
                    Some(t("project.add.nameHint")),
                ));
            }
        }
        // The AI tool its agents run when they name none.
        let mut tools = vec![Choice::new("", t("defaults.project.toolNone"))];
        tools.extend(
            ["claude", "codex", "opencode", "cursor", "gemini"]
                .map(|x| Choice::new(x, tool_label(x))),
        );
        let v2 = view.clone();
        body.push(field(
            t("project.add.toolLabel"),
            controls::select_at(
                "add-tool",
                tools,
                &d.tool,
                busy,
                Rc::new(move |v: &str, _, cx| {
                    let v = v.to_string();
                    v2.update(cx, |this, cx| {
                        if let Some(d) = this.add_dialog() {
                            d.tool = v;
                        }
                        cx.notify();
                    })
                    .ok();
                }),
                Anchor::TopLeft,
            ),
            Some(t("project.add.toolHint")),
        ));
        if let Some(auth) = &d.auth {
            body.push(self.account_rows(d, auth, busy, cx));
            if auth.github_accounts.is_empty() {
                body.push(
                    div()
                        .id("add-no-github")
                        .text_xs()
                        .text_color(theme::faint())
                        .cursor_pointer()
                        .hover(|s| s.text_color(theme::primary()))
                        .active(|s| s.opacity(0.7))
                        .on_click(cx.listener(|this, _: &ClickEvent, window, cx| {
                            this.side.dialog = None;
                            this.open_settings_at("accounts".into(), window, cx);
                        }))
                        .child(plain(&t("project.add.noGitHub")))
                        .into_any_element(),
                );
            }
        }
        if d.job.is_some() {
            body.push(job_log(&d.log, d.job.as_ref()));
        }
        let path_for_note = if mode == "new" {
            d.new_path(cx)
        } else {
            d.clone_path(cx)
        };
        if code == ERROR_FOLDER_NOT_EMPTY && mode == "new" {
            body.push(controls::note_in(
                plain(&tv(
                    "project.add.notEmpty",
                    &[("path", path_for_note.into())],
                )),
                theme::accent(),
            ));
        } else if code == ERROR_FOLDER_EXISTS && mode == "clone" {
            body.push(controls::note_in(
                plain(&tv(
                    "project.add.folderExists",
                    &[("path", path_for_note.into())],
                )),
                theme::accent(),
            ));
        } else if let Some((e, c)) = &d.error
            && c != ERROR_BAD_REPO_URL
        {
            body.push(controls::error_line(e.clone()));
        }
        let pending = busy;
        let label = match mode {
            "clone" if code == ERROR_FOLDER_EXISTS => t("project.add.addFolder"),
            "clone" if pending => t("project.add.cloning"),
            "clone" if d.failed() => t("project.add.cloneAgain"),
            "clone" => t("project.add.cloneAndAdd"),
            "new" if code == ERROR_FOLDER_NOT_EMPTY => t("project.add.commitAndAdd"),
            "new" if pending => t("project.add.creating"),
            "new" => t("project.add.createAndAdd"),
            _ if on_windows_drive(d.path.read(cx).value().trim()) => t(if pending {
                "project.add.copying"
            } else {
                "project.add.copyAndAdd"
            }),
            _ => t("project.add.submit"),
        };
        let empty = match mode {
            "clone" => d.url.read(cx).value().trim().is_empty(),
            "new" => d.new_path(cx).is_empty(),
            _ => d.path.read(cx).value().trim().is_empty(),
        };
        form_modal(
            "add-project",
            t("project.add.title"),
            body,
            None,
            cancel,
            label,
            empty || pending,
            cx.listener(|this, _: &ClickEvent, _, cx| this.submit_add_project(cx)),
        )
    }

    /// account_rows are the account choices, one row per tool with accounts,
    /// and one line naming the tools with none.
    fn account_rows(
        &self,
        d: &AddProject,
        auth: &AuthStatus,
        busy: bool,
        cx: &mut Context<Self>,
    ) -> AnyElement {
        let view = cx.entity().downgrade();
        // (key, label, accounts: (name, default, expired), display names)
        let mut rows: Vec<AccountRow> = Vec::new();
        if !auth.claude_accounts.is_empty() {
            let a: Vec<_> = auth
                .claude_accounts
                .iter()
                .map(|a| (a.name.clone(), a.default, a.valid == "rejected"))
                .collect();
            let names = a.iter().map(|x| x.0.clone()).collect();
            rows.push(("claude".into(), tool_label("claude"), a, names));
        }
        for tool in ["codex"].iter().copied() {
            let list = accounts_of(auth, tool);
            if !list.is_empty() {
                let a: Vec<_> = list
                    .iter()
                    .map(|a| (a.name.clone(), a.default, a.expired))
                    .collect();
                let names = a.iter().map(|x| x.0.clone()).collect();
                rows.push((tool.into(), tool_label(tool), a, names));
            }
        }
        let mut providers: Vec<String> = auth
            .open_code_accounts
            .iter()
            .map(|a| a.provider.clone())
            .filter(|p| !p.is_empty())
            .collect();
        providers.sort();
        providers.dedup();
        for p in &providers {
            let a: Vec<_> = auth
                .open_code_accounts
                .iter()
                .filter(|a| &a.provider == p)
                .map(|a| (a.name.clone(), a.default, a.expired))
                .collect();
            let names = a.iter().map(|x| x.0.clone()).collect();
            rows.push((
                format!("opencode:{p}"),
                format!("{} · {p}", tool_label("opencode")),
                a,
                names,
            ));
        }
        for tool in ["cursor", "gemini"] {
            let list = accounts_of(auth, tool);
            if !list.is_empty() {
                let a: Vec<_> = list
                    .iter()
                    .map(|a| (a.name.clone(), a.default, a.expired))
                    .collect();
                let names = a.iter().map(|x| x.0.clone()).collect();
                rows.push((tool.into(), tool_label(tool), a, names));
            }
        }
        if !auth.github_accounts.is_empty() {
            let a: Vec<_> = auth
                .github_accounts
                .iter()
                .map(|a| (a.name.clone(), a.default, false))
                .collect();
            let names = auth
                .github_accounts
                .iter()
                .map(|a| {
                    if a.login.is_empty() {
                        a.name.clone()
                    } else {
                        format!("{} ({})", a.name, a.login)
                    }
                })
                .collect();
            rows.push(("github".into(), "GitHub".into(), a, names));
        }
        let signed_out: Vec<String> = [
            (auth.claude_accounts.is_empty(), tool_label("claude")),
            (auth.codex_accounts.is_empty(), tool_label("codex")),
            (providers.is_empty(), tool_label("opencode")),
            (auth.cursor_accounts.is_empty(), tool_label("cursor")),
            (auth.gemini_accounts.is_empty(), tool_label("gemini")),
            (auth.github_accounts.is_empty(), "GitHub".to_string()),
        ]
        .into_iter()
        .filter(|(none, _)| *none)
        .map(|(_, n)| n)
        .collect();
        let mut out = v_flex()
            .gap_2()
            .child(
                div()
                    .text_xs()
                    .font_weight(FontWeight::MEDIUM)
                    .text_color(theme::muted())
                    .child(t("project.add.accountsLabel")),
            )
            .child(
                div()
                    .text_xs()
                    .text_color(theme::faint())
                    .child(t("project.add.accountsHint")),
            );
        for (key, label, accounts, names) in rows {
            let chosen = d.accounts.get(&key).cloned().unwrap_or_default();
            let default = accounts.iter().find(|a| a.1).map(|a| a.0.clone());
            let mut choices = vec![Choice::new(
                "",
                match &default {
                    Some(n) => tv("project.newAgent.defaultOption", &[("value", n.into())]),
                    None => t("common.default"),
                },
            )];
            choices.extend(
                accounts
                    .iter()
                    .zip(names)
                    .map(|(a, n)| Choice::new(a.0.clone(), n)),
            );
            let on = if chosen.is_empty() {
                accounts.iter().find(|a| a.1)
            } else {
                accounts.iter().find(|a| a.0 == chosen)
            };
            let (state, color) = match on {
                None => (
                    tv(
                        "project.accounts.goneState",
                        &[("account", chosen.clone().into())],
                    ),
                    theme::starting(),
                ),
                Some(a) if a.2 => (
                    tv(
                        "project.accounts.expiredState",
                        &[("account", a.0.clone().into())],
                    ),
                    theme::starting(),
                ),
                Some(a) => (
                    tv(
                        "project.accounts.signedIn",
                        &[("account", a.0.clone().into())],
                    ),
                    theme::running(),
                ),
            };
            let (v, k) = (view.clone(), key.clone());
            out = out.child(
                h_flex()
                    .gap_3()
                    .items_start()
                    .child(
                        div()
                            .w(px(130.))
                            .flex_none()
                            .pt_1()
                            .text_sm()
                            .text_color(theme::muted())
                            .child(label),
                    )
                    .child(
                        v_flex()
                            .flex_1()
                            .min_w_0()
                            .gap_0p5()
                            .child(controls::select_at(
                                &format!("add-account-{key}"),
                                choices,
                                &chosen,
                                busy,
                                Rc::new(move |val: &str, _, cx| {
                                    let (val, k) = (val.to_string(), k.clone());
                                    v.update(cx, |this, cx| {
                                        if let Some(d) = this.add_dialog() {
                                            d.accounts.insert(k, val);
                                        }
                                        cx.notify();
                                    })
                                    .ok();
                                }),
                                Anchor::TopLeft,
                            ))
                            .child(div().text_xs().text_color(color).child(state)),
                    ),
            );
        }
        if !signed_out.is_empty() {
            out = out.child(div().text_xs().text_color(theme::faint()).child(tv(
                "project.add.notSignedIn",
                &[("tools", signed_out.join(", ").into())],
            )));
        }
        out.into_any_element()
    }
}

/// added finishes an add: the tool set, the list read again, a toast.
async fn added(
    this: WeakEntity<AppView>,
    client: agentbox_api::client::Client,
    mut p: Project,
    tool: String,
    cx: &mut AsyncApp,
) {
    if !tool.is_empty() {
        let req = UpdateProjectRequest {
            agent_tool: Some(tool),
            ..Default::default()
        };
        match client
            .raw(
                agentbox_api::client::Method::PATCH,
                &format!("/v1/projects/{}", p.name),
                serde_json::to_vec(&req).ok(),
            )
            .await
            .and_then(|b| Ok(serde_json::from_slice::<Project>(&b)?))
        {
            Ok(n) => p = n,
            Err(e) => {
                cx.update(|cx| toasts::error(cx, e.to_string()));
            }
        }
    }
    this.update(cx, |this, cx| {
        this.side.dialog = None;
        toasts::Toast::new(
            toasts::Kind::Success,
            tv("project.add.added", &[("name", label_of(&p).into())]),
        )
        .show(cx);
        this.projects.push(p);
        this.reload_projects(cx);
        this.load_sections(cx);
        cx.notify();
    })
    .ok();
}

/// job_log is a job's log, its last lines, as JobProgress shows it.
pub(super) fn job_log(lines: &[String], job: Option<&Job>) -> AnyElement {
    let start = lines.len().saturating_sub(200);
    v_flex()
        .gap_1()
        .when_some(job.filter(|j| !j.error.is_empty()), |el, j| {
            el.child(controls::error_line(j.error.clone()))
        })
        .child(
            div()
                .id("job-log")
                .h(px(160.))
                .overflow_y_scroll()
                .p_2()
                .rounded_lg()
                .bg(theme::ink())
                .border_1()
                .border_color(theme::line())
                .font_family(theme::mono())
                .text_xs()
                .text_color(theme::muted())
                .children(lines[start..].iter().map(|l| div().child(l.clone()))),
        )
        .into_any_element()
}

/// field is a form's labelled control with its hint.
pub(super) fn field(label: String, input: AnyElement, hint: Option<String>) -> AnyElement {
    v_flex()
        .gap_1()
        .child(
            div()
                .text_xs()
                .font_weight(FontWeight::MEDIUM)
                .text_color(theme::muted())
                .child(label),
        )
        .child(input)
        .when_some(hint, |el, h| {
            el.child(div().text_xs().text_color(theme::faint()).child(h))
        })
        .into_any_element()
}

/// form_modal is a dialog with a long form: its body scrolls, its buttons
/// stay at the bottom; `left` is a button of its own at the footer's left.
#[allow(clippy::too_many_arguments)]
pub(super) fn form_modal(
    id: &'static str,
    title: String,
    body: Vec<AnyElement>,
    left: Option<AnyElement>,
    on_cancel: OnCancel,
    confirm: String,
    disabled: bool,
    on_confirm: impl Fn(&ClickEvent, &mut Window, &mut App) + 'static,
) -> AnyElement {
    let backdrop = on_cancel.clone();
    crate::motion::scrim_in(
        div()
            .id(SharedString::from(format!("{id}-backdrop")))
            .absolute()
            .inset_0()
            .bg(theme::scrim())
            .flex()
            .items_center()
            .justify_center()
            .on_mouse_down(MouseButton::Left, move |_, window, cx| backdrop(window, cx))
            .child(crate::motion::pop_in(
                v_flex()
                    .id(SharedString::from(format!("{id}-card")))
                    .w(px(580.))
                    .max_w(relative(0.94))
                    .max_h(relative(0.9))
                    .rounded_xl()
                    .bg(theme::card())
                    .border_1()
                    .border_color(theme::line())
                    .shadow_lg()
                    .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
                    .child(
                        div()
                            .px_6()
                            .pt_6()
                            .pb_3()
                            .text_lg()
                            .font_weight(FontWeight::SEMIBOLD)
                            .text_color(theme::primary())
                            .child(title),
                    )
                    .child(
                        v_flex()
                            .id(SharedString::from(format!("{id}-body")))
                            .flex_1()
                            .min_h_0()
                            .overflow_y_scroll()
                            .px_6()
                            .pb_4()
                            .gap_4()
                            .children(body),
                    )
                    .child(
                        h_flex()
                            .px_6()
                            .py_4()
                            .gap_2()
                            .border_t_1()
                            .border_color(theme::line())
                            .children(left)
                            .child(div().flex_1())
                            .child(controls::button(
                                SharedString::from(format!("{id}-cancel")),
                                t("common.cancel"),
                                Btn::Ghost,
                                false,
                                move |_, w, cx| on_cancel(w, cx),
                            ))
                            .child(controls::button(
                                SharedString::from(format!("{id}-confirm")),
                                confirm,
                                Btn::Primary,
                                disabled,
                                on_confirm,
                            )),
                    ),
                "add-project-in",
            )),
        "idsharedstringfromformatid-backdrop-in",
    )
    .into_any_element()
}

#[cfg(test)]
mod tests {
    use super::{looks_like_link, on_windows_drive, repo_name};

    #[test]
    fn reads_links_like_the_daemon() {
        assert_eq!(repo_name("https://github.com/acme/my-app.git"), "my-app");
        assert_eq!(repo_name("git@host:acme/my-app"), "my-app");
        assert!(looks_like_link("https://github.com/acme/x"));
        assert!(looks_like_link("git@github.com:acme/x"));
        assert!(!looks_like_link("acme"));
        assert!(on_windows_drive("/mnt/c/code"));
        assert!(!on_windows_drive("/mnt/data2"));
    }
}
