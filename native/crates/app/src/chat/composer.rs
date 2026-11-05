//! The composer (Composer.tsx): a message box that grows with what's
//! written (Enter sends, Shift+Enter starts a new line), files and pictures
//! attached by picking, pasting or dropping them, "/" for the tool's
//! commands and "@" for files, projects and agents, the chat's settings
//! (model, context window, effort, mode, plan, the AI tool and account), and
//! what waits above it: a permission request, the sleeping agent's wake
//! button, the prompt cache about to expire (holding the message until you
//! choose), changed connectors, the plan's tasks.
use std::path::PathBuf;
use std::sync::Arc;

use agentbox_api::{
    CHAT_OPTION_PLAN, ChatCache, ChatCacheChoice, ChatFileUpload, ChatImageUpload,
    ChatMessageRequest, ChatOption, ChatOptionRequest, ChatSession, Client, LeadRunnerRequest,
    LeadRunners,
};
use agentbox_i18n::{t, tv};
use base64::Engine as _;
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::button::{Button, ButtonVariants as _};
use gpui_component::input::{Enter, Escape, InputEvent, MoveDown, MoveUp, Textarea, TextareaState};
use gpui_component::menu::{DropdownMenu as _, PopupMenuItem};
use gpui_component::{Sizable as _, h_flex, v_flex};

use super::{ChatAction, ChatView};
use crate::theme;

const MAX_IMAGES: usize = 8;
const MAX_IMAGE_BYTES: usize = ((5 << 20) / 4) * 3;
const MAX_FILES: usize = 10;
const MAX_FILE_BYTES: usize = 25 << 20;
const MAX_FILES_BYTES: usize = 50 << 20;
const MENU_LIMIT: usize = 8;

/// A file or picture waiting to go with the next message.
#[derive(Clone)]
pub struct Attachment {
    pub name: String,
    pub mime: String,
    pub bytes: Vec<u8>,
    pub image: Option<Arc<Image>>,
}

/// One line of the "/" or "@" menu.
#[derive(Clone, Debug, PartialEq)]
pub struct MenuItem {
    pub label: String,
    pub hint: String,
    /// What replaces text[start..cursor].
    pub insert: String,
    pub start: usize,
}

pub struct Composer {
    pub input: Entity<TextareaState>,
    pub attachments: Vec<Attachment>,
    menu_ix: usize,
    /// The menu Escape closed, by the token it was open for.
    dismissed: Option<String>,
    files: Option<Vec<String>>,
    projects: Option<Vec<(String, String)>>,
    agents: Option<Vec<(String, String, String)>>,
    asked_mentions: bool,
    cache: Option<ChatCache>,
    /// A message written while the cache card asks: sent once you choose.
    held: Option<String>,
    choosing: bool,
    tasks_open: bool,
    runners: Option<LeadRunners>,
    placeholder: String,
    sending: bool,
}

fn mime_of(name: &str) -> String {
    let ext = name.rsplit('.').next().unwrap_or("").to_lowercase();
    match ext.as_str() {
        "png" => "image/png",
        "jpg" | "jpeg" => "image/jpeg",
        "gif" => "image/gif",
        "webp" => "image/webp",
        "svg" => "image/svg+xml",
        "pdf" => "application/pdf",
        "json" => "application/json",
        "zip" => "application/zip",
        "csv" => "text/csv",
        "html" | "htm" => "text/html",
        "md" | "txt" | "log" | "rs" | "go" | "ts" | "tsx" | "js" | "py" | "sh" | "toml"
        | "yaml" | "yml" => "text/plain",
        _ => "application/octet-stream",
    }
    .to_string()
}

/// slash_query is what's typed after a "/" that is the whole message.
fn slash_query(text: &str) -> Option<&str> {
    let q = text.strip_prefix('/')?;
    (!q.contains(char::is_whitespace)).then_some(q)
}

/// mention_at is the "@" token the cursor ends: where it starts and what
/// follows it (lib/mentions.ts mentionAt).
pub fn mention_at(text: &str, cursor: usize) -> Option<(usize, String)> {
    let before = text.get(..cursor)?;
    let at = before.rfind('@')?;
    let query = &before[at + 1..];
    if query.contains(char::is_whitespace) {
        return None;
    }
    let ok = at == 0 || before[..at].ends_with(char::is_whitespace);
    ok.then(|| (at, query.to_string()))
}

/// match_files ranks paths against a query: substring matches first, the
/// earliest and shortest best, then subsequences (matchFiles).
pub fn match_files(files: &[String], query: &str, limit: usize) -> Vec<String> {
    let q = query.trim().to_lowercase();
    if q.is_empty() {
        return files.iter().take(limit).cloned().collect();
    }
    let mut scored: Vec<(usize, usize, usize, &String)> = Vec::new();
    for f in files {
        let l = f.to_lowercase();
        if let Some(i) = l.find(&q) {
            scored.push((0, i, f.len(), f));
            continue;
        }
        // A subsequence: "cmpsr" finds Composer.tsx, ranked by its spread.
        let mut it = l.char_indices();
        let (mut first, mut last) = (None, 0);
        let all = q.chars().all(|c| {
            for (i, d) in it.by_ref() {
                if d == c {
                    first.get_or_insert(i);
                    last = i;
                    return true;
                }
            }
            false
        });
        if all {
            scored.push((1, last - first.unwrap_or(0), f.len(), f));
        }
    }
    scored.sort();
    scored
        .into_iter()
        .take(limit)
        .map(|s| s.3.clone())
        .collect()
}

impl Composer {
    pub fn new(window: &mut Window, cx: &mut Context<ChatView>) -> (Self, Vec<Subscription>) {
        let input = cx.new(|cx| {
            TextareaState::new(window, cx)
                .auto_grow(1, 10)
                .submit_on_enter(true)
                .placeholder(t("chat.composer.placeholder"))
        });
        let sub = cx.subscribe_in(
            &input,
            window,
            |this, _, ev: &InputEvent, window, cx| match ev {
                InputEvent::PressEnter { shift: false, .. } => this.submit(window, cx),
                InputEvent::Change => {
                    this.composer.menu_ix = 0;
                    this.want_mentions(cx);
                    cx.notify();
                }
                _ => {}
            },
        );
        (
            Self {
                input,
                attachments: Vec::new(),
                menu_ix: 0,
                dismissed: None,
                files: None,
                projects: None,
                agents: None,
                asked_mentions: false,
                cache: None,
                held: None,
                choosing: false,
                tasks_open: false,
                runners: None,
                placeholder: String::new(),
                sending: false,
            },
            vec![sub],
        )
    }

    /// load_cache reads where the lead's prompt cache stands (GET …/chat/cache).
    pub fn load_cache(&mut self, client: &Client, prefix: &str, cx: &mut Context<ChatView>) {
        if !prefix.starts_with("/v1/projects/") || prefix.starts_with("/v1/projects/_home") {
            return;
        }
        let client = client.clone();
        let path = format!("{prefix}/chat/cache");
        cx.spawn(async move |this, cx| {
            let res = client.get::<ChatCache>(&path).await;
            this.update(cx, |this, cx| {
                this.composer.cache = res.ok();
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn add(&mut self, a: Attachment, no_images: bool, cx: &mut App) {
        let mut a = a;
        if a.image.is_some() && (no_images || a.bytes.len() > MAX_IMAGE_BYTES) {
            a.image = None; // goes as a file
        }
        if a.image.is_some() {
            if self
                .attachments
                .iter()
                .filter(|x| x.image.is_some())
                .count()
                >= MAX_IMAGES
            {
                crate::toasts::error(
                    cx,
                    tv("chat.composer.maxImages", &[("count", MAX_IMAGES.into())]),
                );
                return;
            }
        } else {
            if a.bytes.len() > MAX_FILE_BYTES {
                crate::toasts::error(
                    cx,
                    tv(
                        "chat.files.tooBig",
                        &[
                            ("name", (&a.name).into()),
                            (
                                "max",
                                crate::files::human_bytes(MAX_FILE_BYTES as i64).into(),
                            ),
                        ],
                    ),
                );
                return;
            }
            let files: Vec<&Attachment> = self
                .attachments
                .iter()
                .filter(|x| x.image.is_none())
                .collect();
            if files.len() >= MAX_FILES {
                crate::toasts::error(
                    cx,
                    tv("chat.files.maxFiles", &[("count", MAX_FILES.into())]),
                );
                return;
            }
            if files.iter().map(|f| f.bytes.len()).sum::<usize>() + a.bytes.len() > MAX_FILES_BYTES
            {
                crate::toasts::error(
                    cx,
                    tv(
                        "chat.files.maxTotal",
                        &[(
                            "max",
                            crate::files::human_bytes(MAX_FILES_BYTES as i64).into(),
                        )],
                    ),
                );
                return;
            }
        }
        self.attachments.push(a);
    }

    /// drop_paths attaches files dropped on the chat, or picked.
    pub fn drop_paths(&mut self, paths: &[PathBuf], session: &ChatSession, cx: &mut App) {
        if paths.is_empty() {
            crate::toasts::Toast::new(
                crate::toasts::Kind::Error,
                t("chat.composer.nothingAttached"),
            )
            .description(t("chat.composer.nothingAttachedHint"))
            .show(cx);
            return;
        }
        for p in paths {
            if p.is_dir() {
                continue;
            }
            let name = p
                .file_name()
                .map(|n| n.to_string_lossy().into_owned())
                .unwrap_or_default();
            let bytes = match std::fs::read(p) {
                Ok(b) => b,
                Err(e) => {
                    crate::toasts::Toast::new(crate::toasts::Kind::Error, t("chat.files.read"))
                        .description(e.to_string())
                        .show(cx);
                    continue;
                }
            };
            let mime = mime_of(&name);
            let image = ImageFormat::from_mime_type(&mime)
                .filter(|f| *f != ImageFormat::Svg)
                .map(|f| Arc::new(Image::from_bytes(f, bytes.clone())));
            self.add(
                Attachment {
                    name,
                    mime,
                    bytes,
                    image,
                },
                session.no_images,
                cx,
            );
        }
    }

    /// paste attaches the pictures and files on the clipboard; it says
    /// whether it took the paste (text pastes as text).
    pub fn paste(&mut self, item: &ClipboardItem, session: &ChatSession, cx: &mut App) -> bool {
        let mut took = false;
        for e in item.entries() {
            match e {
                ClipboardEntry::Image(im) => {
                    let n = self.attachments.len() + 1;
                    let name = format!(
                        "{}-{n}.{}",
                        t("chat.images.theImage").to_lowercase().replace(' ', "-"),
                        im.format.extension()
                    );
                    self.add(
                        Attachment {
                            name,
                            mime: im.format.mime_type().to_string(),
                            bytes: im.bytes.clone(),
                            image: Some(Arc::new(Image::from_bytes(im.format, im.bytes.clone()))),
                        },
                        session.no_images,
                        cx,
                    );
                    took = true;
                }
                ClipboardEntry::ExternalPaths(p) => {
                    self.drop_paths(p.paths(), session, cx);
                    took = true;
                }
                ClipboardEntry::String(_) => {}
            }
        }
        took
    }
}

impl ChatView {
    fn text(&self, cx: &App) -> String {
        self.composer.input.read(cx).value().to_string()
    }

    /// want_mentions reads, once, what "@" can name: the worktree's files,
    /// the projects and the agents.
    fn want_mentions(&mut self, cx: &mut Context<Self>) {
        if self.composer.asked_mentions {
            return;
        }
        let text = self.text(cx);
        let cursor = self.composer.input.read(cx).cursor();
        if mention_at(&text, cursor).is_none() {
            return;
        }
        self.composer.asked_mentions = true;
        let client = self.client.clone();
        let files = format!("{}/files", self.prefix);
        cx.spawn(async move |this, cx| {
            let f = client.get::<agentbox_api::WorktreeFiles>(&files).await;
            let p = client
                .get::<Vec<agentbox_api::Project>>("/v1/projects")
                .await;
            let a = client
                .get::<Vec<agentbox_api::FleetAgent>>("/v1/agents")
                .await;
            this.update(cx, |this, cx| {
                this.composer.files = Some(f.map(|f| f.files).unwrap_or_default());
                this.composer.projects = Some(
                    p.unwrap_or_default()
                        .into_iter()
                        .filter(|p| p.name != agentbox_api::HOME_PROJECT)
                        .map(|p| (p.name, p.display_name))
                        .collect(),
                );
                this.composer.agents = Some(
                    a.unwrap_or_default()
                        .into_iter()
                        .map(|a| (a.project, a.name, a.title))
                        .collect(),
                );
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// menu is what the "/" or "@" menu offers now, and the token it's for.
    fn menu(&self, cx: &App) -> (Vec<MenuItem>, String) {
        let text = self.text(cx);
        let cursor = self.composer.input.read(cx).cursor().min(text.len());
        if let Some(q) = slash_query(&text) {
            let token = format!("/{q}");
            if self.composer.dismissed.as_deref() == Some(token.as_str()) {
                return (Vec::new(), token);
            }
            let ql = q.to_lowercase();
            let items = self
                .session
                .commands
                .iter()
                .filter(|c| c.name.to_lowercase().contains(&ql))
                .take(9)
                .map(|c| MenuItem {
                    label: format!("/{}", c.name),
                    hint: c.description.clone(),
                    insert: format!("/{} ", c.name),
                    start: 0,
                })
                .collect();
            return (items, token);
        }
        let Some((start, q)) = mention_at(&text, cursor) else {
            return (Vec::new(), String::new());
        };
        let token = format!("{start}:{q}");
        if self.composer.dismissed.as_deref() == Some(token.as_str()) {
            return (Vec::new(), token);
        }
        let ql = q.to_lowercase();
        let mut items: Vec<MenuItem> = Vec::new();
        let project = self.agent.split('/').next().unwrap_or("");
        let home = project == agentbox_api::HOME_PROJECT;
        for (name, display) in self.composer.projects.iter().flatten() {
            if name.to_lowercase().contains(&ql) || display.to_lowercase().contains(&ql) {
                items.push(MenuItem {
                    label: if display.is_empty() || display == name {
                        name.clone()
                    } else {
                        format!("{display} ({name})")
                    },
                    hint: t("chat.snap.project"),
                    insert: format!("@{name} "),
                    start,
                });
            }
        }
        for (p, name, title) in self.composer.agents.iter().flatten() {
            if (!home && p != project) || format!("{p}/{name}") == self.agent {
                continue;
            }
            if name.to_lowercase().contains(&ql) || title.to_lowercase().contains(&ql) {
                items.push(MenuItem {
                    label: if title.is_empty() {
                        name.clone()
                    } else {
                        format!("{title} ({name})")
                    },
                    hint: t("chat.find.from.agent"),
                    insert: format!("@{name} "),
                    start,
                });
            }
        }
        items.truncate(MENU_LIMIT / 2);
        for f in match_files(
            self.composer.files.as_deref().unwrap_or(&[]),
            &q,
            MENU_LIMIT,
        ) {
            items.push(MenuItem {
                label: f.clone(),
                hint: String::new(),
                insert: format!("@{f} "),
                start,
            });
        }
        items.truncate(MENU_LIMIT + 2);
        (items, token)
    }

    fn pick(&mut self, item: MenuItem, window: &mut Window, cx: &mut Context<Self>) {
        let text = self.text(cx);
        let cursor = self.composer.input.read(cx).cursor().min(text.len());
        let start = item.start.min(cursor);
        let next = format!("{}{}{}", &text[..start], item.insert, &text[cursor..]);
        let at = start + item.insert.len();
        self.composer.input.update(cx, |s, cx| {
            s.set_value(next, window, cx);
            s.set_selected_range(at..at, cx);
        });
        self.composer.menu_ix = 0;
        cx.notify();
    }

    fn last_mine(&self) -> Option<String> {
        self.items
            .iter()
            .rev()
            .find(|i| i.kind == "user" && i.parent.is_empty() && !i.hidden)
            .map(|i| i.text.clone())
    }

    /// cache_card is the prompt cache about to expire, when it asks first.
    fn cache_card(&self) -> Option<&ChatCache> {
        self.composer
            .cache
            .as_ref()
            .filter(|c| c.due && self.session.turn_started_at.is_none())
    }

    pub(super) fn submit(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let text = self.text(cx).trim().to_string();
        if (text.is_empty() && self.composer.attachments.is_empty()) || self.composer.sending {
            return;
        }
        if self.composer.held.is_some() {
            return;
        }
        if self.cache_card().is_some() {
            // Held until you choose how to send it, on the card.
            self.composer.held = Some(text);
            self.composer
                .input
                .update(cx, |s, cx| s.set_value("", window, cx));
            cx.notify();
            return;
        }
        self.send_text(text, window, cx);
    }

    fn uploads(&self) -> (Vec<ChatImageUpload>, Vec<ChatFileUpload>) {
        let b64 = |b: &[u8]| base64::engine::general_purpose::STANDARD.encode(b);
        let mut images = Vec::new();
        let mut files = Vec::new();
        for a in &self.composer.attachments {
            if a.image.is_some() {
                images.push(ChatImageUpload {
                    mime_type: a.mime.clone(),
                    data: b64(&a.bytes),
                    name: a.name.clone(),
                });
            } else {
                files.push(ChatFileUpload {
                    name: a.name.clone(),
                    mime_type: a.mime.clone(),
                    data: b64(&a.bytes),
                });
            }
        }
        (images, files)
    }

    fn send_text(&mut self, text: String, window: &mut Window, cx: &mut Context<Self>) {
        let (images, files) = self.uploads();
        let kept = std::mem::take(&mut self.composer.attachments);
        self.composer
            .input
            .update(cx, |s, cx| s.set_value("", window, cx));
        self.composer.sending = true;
        self.list.set_follow_mode(FollowMode::Tail);
        let client = self.client.clone();
        let path = format!("{}/chat/messages", self.prefix);
        let back = text.clone();
        cx.spawn_in(window, async move |this, cx| {
            let res = client
                .post::<_, serde_json::Value>(
                    &path,
                    &ChatMessageRequest {
                        text,
                        images,
                        files,
                        ..Default::default()
                    },
                )
                .await;
            this.update_in(cx, |this, window, cx| {
                this.composer.sending = false;
                if let Err(e) = res {
                    // What didn't go comes back, unless something new was written.
                    if this.text(cx).is_empty() {
                        this.composer
                            .input
                            .update(cx, |s, cx| s.set_value(back, window, cx));
                        this.composer.attachments = kept;
                    }
                    crate::toasts::Toast::new(
                        crate::toasts::Kind::Error,
                        tv("chat.composer.notSent", &[("error", e.to_string().into())]),
                    )
                    .show(cx);
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    /// choose_cache answers the cache card: compact first, or send as it is.
    fn choose_cache(&mut self, compact: bool, window: &mut Window, cx: &mut Context<Self>) {
        let text = self
            .composer
            .held
            .take()
            .unwrap_or_else(|| self.text(cx).trim().to_string());
        let (images, files) = self.uploads();
        self.composer.attachments.clear();
        self.composer
            .input
            .update(cx, |s, cx| s.set_value("", window, cx));
        self.composer.choosing = true;
        cx.notify();
        let client = self.client.clone();
        let path = format!("{}/chat/cache", self.prefix);
        cx.spawn_in(window, async move |this, cx| {
            let res = client
                .post::<_, serde_json::Value>(
                    &path,
                    &ChatCacheChoice {
                        compact,
                        text: text.clone(),
                        images,
                        files,
                    },
                )
                .await;
            this.update_in(cx, |this, window, cx| {
                this.composer.choosing = false;
                match res {
                    Ok(_) => this.composer.cache = None,
                    Err(e) => {
                        this.composer
                            .input
                            .update(cx, |s, cx| s.set_value(text, window, cx));
                        crate::toasts::error(cx, e.to_string());
                    }
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// pick_files opens the system's file dialog and attaches what's picked.
    fn pick_files(&mut self, cx: &mut Context<Self>) {
        let rx = pick_paths(t("chat.composer.attach"), cx);
        cx.spawn(async move |this, cx| {
            let Some(paths) = rx.await.ok().flatten() else {
                return;
            };
            this.update(cx, |this, cx| {
                let session = this.session.clone();
                this.composer.drop_paths(&paths, &session, cx);
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn set_option(&mut self, id: String, value: String, cx: &mut Context<Self>) {
        let client = self.client.clone();
        let path = format!("{}/chat/options/{id}", self.prefix);
        cx.spawn(async move |_, cx| {
            let body = serde_json::to_vec(&ChatOptionRequest { value }).unwrap_or_default();
            if let Err(e) = client
                .raw(agentbox_api::client::Method::PUT, &path, Some(body))
                .await
            {
                cx.update(|cx| crate::toasts::error(cx, e.to_string()));
            }
        })
        .detach();
    }

    /// lead_runners is the tool and account the chat's composer runs on.
    pub fn lead_runners(&self) -> Option<&LeadRunners> {
        self.composer.runners.as_ref()
    }

    fn load_runners(&mut self, cx: &mut Context<Self>) {
        let client = self.client.clone();
        let path = format!("{}/chat/runners", self.prefix);
        cx.spawn(async move |this, cx| {
            let res = client.get::<LeadRunners>(&path).await;
            this.update(cx, |this, cx| {
                this.composer.runners = res.ok();
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn set_runner(&mut self, tool: String, account: String, cx: &mut Context<Self>) {
        let client = self.client.clone();
        let path = format!("{}/chat/runner", self.prefix);
        cx.spawn(async move |this, cx| {
            let body = serde_json::to_vec(&LeadRunnerRequest { tool, account }).unwrap_or_default();
            let res = client
                .raw(agentbox_api::client::Method::PUT, &path, Some(body))
                .await;
            this.update(cx, |this, cx| {
                if let Err(e) = res {
                    crate::toasts::error(cx, e.to_string());
                }
                this.load_runners(cx);
            })
            .ok();
        })
        .detach();
    }

    fn placeholder(&self) -> String {
        let pending = self.items.iter().any(|i| {
            i.kind == "permission" && i.permission.as_ref().is_some_and(|p| p.outcome.is_empty())
        });
        let tool = super::tool_label(&self.session.tool);
        if let Some(state) = &self.asleep {
            tv(
                "chat.composer.wakeToChat",
                &[("state", state.into()), ("name", (&self.agent_name).into())],
            )
        } else if self.composer.held.is_some() {
            t("chat.composer.held")
        } else if pending {
            t("chat.composer.answerRequest")
        } else if self.session.turn_started_at.is_some() {
            tv("chat.composer.busy", &[("tool", tool.into())])
        } else if self.items.is_empty() && !self.older && self.is_lead() {
            t("chat.composer.placeholderLead")
        } else {
            t("chat.composer.placeholder")
        }
    }

    fn option_button(&self, o: &ChatOption, label: String, cx: &mut Context<Self>) -> AnyElement {
        let view = cx.entity().downgrade();
        let (id, value, choices) = (o.id.clone(), o.value.clone(), o.choices.clone());
        Button::new(SharedString::from(format!("opt-{}", o.id)))
            .ghost()
            .xsmall()
            .label(format!("{label} ▾"))
            .dropdown_menu_with_anchor(Anchor::BottomLeft, move |menu, _, _| {
                let mut menu = menu.max_h(px(420.)).scrollable(true);
                let mut group = String::new();
                for c in &choices {
                    if c.group != group {
                        group = c.group.clone();
                        menu = menu.separator();
                        if !group.is_empty() {
                            menu = menu.label(group.clone());
                        }
                    }
                    let on = c.value == value;
                    let (name, about) = (c.name.clone(), c.description.clone());
                    let (view, id, v) = (view.clone(), id.clone(), c.value.clone());
                    menu = menu.item(
                        PopupMenuItem::element(move |_, _| {
                            h_flex()
                                .items_start()
                                .gap_2()
                                .child(
                                    div()
                                        .w(px(12.))
                                        .flex_none()
                                        .text_color(theme::accent())
                                        .child(if on { "✓" } else { "" }),
                                )
                                .child(v_flex().child(div().text_sm().child(name.clone())).when(
                                    !about.is_empty(),
                                    |el| {
                                        el.child(
                                            div()
                                                .text_xs()
                                                .text_color(theme::muted())
                                                .child(about.clone()),
                                        )
                                    },
                                ))
                        })
                        .on_click(move |_, _, cx| {
                            if on {
                                return;
                            }
                            view.update(cx, |this, cx| this.set_option(id.clone(), v.clone(), cx))
                                .ok();
                        }),
                    );
                }
                menu
            })
            .into_any_element()
    }

    /// render_options are the chat's settings, under the message box.
    fn render_options(&self, cx: &mut Context<Self>) -> AnyElement {
        let opts = &self.session.options;
        let find = |cat: &str| {
            opts.iter()
                .find(|o| o.category == cat && o.r#type == "select")
        };
        let name_of = |o: &ChatOption| {
            o.choices
                .iter()
                .find(|c| c.value == o.value)
                .map(|c| c.name.clone())
                .unwrap_or_else(|| o.value.clone())
        };
        let mut row = h_flex().gap_0p5().flex_wrap().items_center();
        // The AI tool and account it runs on (RunnerPicker).
        if let Some(r) = self
            .composer
            .runners
            .as_ref()
            .filter(|r| !r.runners.is_empty())
        {
            let current = format!(
                "{}{}",
                super::tool_label(&r.tool),
                if r.account.is_empty() || r.account == "default" {
                    String::new()
                } else {
                    format!(" · {}", r.account)
                }
            );
            let view = cx.entity().downgrade();
            let runners = r.runners.clone();
            let (tool, account) = (r.tool.clone(), r.account.clone());
            let lead = self.is_lead();
            row = row.child(
                Button::new("opt-runner")
                    .ghost()
                    .xsmall()
                    .label(format!("{current} ▾"))
                    .dropdown_menu_with_anchor(Anchor::BottomLeft, move |menu, _, _| {
                        let mut menu = menu.label(if lead {
                            t("chat.runner.title")
                        } else {
                            t("chat.runner.titleAgent")
                        });
                        for runner in &runners {
                            if !lead && runner.tool != tool {
                                continue;
                            }
                            let accounts: Vec<String> = if runner.accounts.is_empty() {
                                vec![String::new()]
                            } else {
                                runner.accounts.iter().map(|a| a.name.clone()).collect()
                            };
                            for acc in accounts {
                                let on = runner.tool == tool && (acc == account || acc.is_empty());
                                let label = format!(
                                    "{}{}{}",
                                    if on { "✓ " } else { "   " },
                                    runner.name,
                                    if acc.is_empty() {
                                        String::new()
                                    } else {
                                        format!(" · {acc}")
                                    }
                                );
                                let (view, tl, a) =
                                    (view.clone(), runner.tool.clone(), acc.clone());
                                menu = menu.item(
                                    PopupMenuItem::new(label)
                                        .disabled(!runner.ready && runner.installed)
                                        .on_click(move |_, _, cx| {
                                            view.update(cx, |this, cx| {
                                                this.set_runner(tl.clone(), a.clone(), cx)
                                            })
                                            .ok();
                                        }),
                                );
                            }
                        }
                        menu
                    }),
            );
        }
        if let Some(m) = find("model") {
            row = row.child(self.option_button(m, name_of(m), cx));
        }
        if let Some(c) = find("context_window") {
            row = row.child(self.option_button(c, name_of(c), cx));
        }
        if let Some(e) = find("thought_level") {
            let label = if e.value == "default" {
                t("chat.options.effort")
            } else {
                name_of(e)
            };
            row = row.child(self.option_button(e, label, cx));
        }
        let plan = opts
            .iter()
            .find(|o| o.id == CHAT_OPTION_PLAN && o.r#type == "boolean");
        if let Some(p) = plan {
            let on = p.value == "true";
            let view = cx.entity().downgrade();
            let id = p.id.clone();
            row = row.child(
                div()
                    .id("opt-plan")
                    .px_2()
                    .py_0p5()
                    .rounded_md()
                    .cursor_pointer()
                    .text_xs()
                    .when(on, |el| {
                        el.bg(theme::accent().opacity(0.2))
                            .text_color(theme::accent())
                    })
                    .when(!on, |el| el.text_color(theme::muted()))
                    .hover(|el| el.bg(theme::hover()))
                    .child(t("chat.options.plan"))
                    .tooltip({
                        let tip = if on {
                            t("chat.options.planOnTip")
                        } else {
                            t("chat.options.planTip")
                        };
                        move |w, cx| gpui_component::tooltip::Tooltip::new(tip.clone()).build(w, cx)
                    })
                    .on_click(move |_, _, cx| {
                        let value = if on { "false" } else { "true" }.to_string();
                        view.update(cx, |this, cx| this.set_option(id.clone(), value, cx))
                            .ok();
                    }),
            );
        }
        if let Some(m) = find("mode")
            && (plan.is_none() || m.choices.len() > 1)
        {
            row = row.child(self.option_button(m, name_of(m), cx));
        }
        let others: Vec<&ChatOption> = opts
            .iter()
            .filter(|o| {
                o.r#type == "select"
                    && !matches!(
                        o.category.as_str(),
                        "model" | "context_window" | "thought_level" | "mode"
                    )
            })
            .collect();
        for o in others {
            row = row.child(self.option_button(o, format!("{}: {}", o.name, name_of(o)), cx));
        }
        if self.session.state == "starting"
            && !opts
                .iter()
                .any(|o| o.category == "thought_level" || o.category == "mode")
        {
            row = row.child(
                div()
                    .px_1p5()
                    .text_xs()
                    .text_color(theme::faint())
                    .child(t("chat.options.loading")),
            );
        }
        if self.session.context_size > 0 && self.session.context_used > 0 {
            let pct = (self.session.context_used * 100 / self.session.context_size).min(100);
            let tip = tv(
                "chat.context.used",
                &[
                    ("used", tokens(self.session.context_used).into()),
                    ("total", tokens(self.session.context_size).into()),
                ],
            );
            row = row.child(
                div()
                    .id("context-meter")
                    .px_1p5()
                    .text_xs()
                    .text_color(if pct >= 80 {
                        theme::starting()
                    } else {
                        theme::faint()
                    })
                    .child(format!("{pct}%"))
                    .tooltip(move |w, cx| {
                        gpui_component::tooltip::Tooltip::new(tip.clone()).build(w, cx)
                    }),
            );
        }
        row.into_any_element()
    }

    fn banner(tint: Hsla) -> Div {
        v_flex()
            .gap_1()
            .px_3()
            .py_2()
            .rounded_t_xl()
            .border_1()
            .border_b_0()
            .border_color(tint.opacity(0.3))
            .bg(tint.opacity(0.07))
            .text_sm()
    }

    /// render_above is what waits on the composer, attached to its top.
    fn render_above(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let view = cx.entity();
        if let Some(state) = &self.asleep {
            return Some(
                Self::banner(theme::muted())
                    .child(
                        h_flex()
                            .gap_2()
                            .child("☾")
                            .child(
                                div()
                                    .flex_1()
                                    .min_w_0()
                                    .text_color(theme::muted())
                                    .child(tv(
                                        "chat.composer.asleep",
                                        &[
                                            ("state", state.into()),
                                            ("name", (&self.agent_name).into()),
                                        ],
                                    )),
                            )
                            .child(super::btn(
                                "chat-wake",
                                tv("chat.composer.wake", &[("state", state.into())]),
                                true,
                                false,
                                cx.listener(|_, _: &ClickEvent, _, cx| cx.emit(ChatAction::Wake)),
                            )),
                    )
                    .into_any_element(),
            );
        }
        let pending: Vec<&agentbox_api::ChatItem> = self
            .items
            .iter()
            .filter(|i| {
                i.kind == "permission"
                    && i.permission.as_ref().is_some_and(|p| p.outcome.is_empty())
            })
            .collect();
        if let Some(req) = pending.first() {
            let p = req.permission.clone().unwrap_or_default();
            let tool = self
                .items
                .iter()
                .rev()
                .find_map(|i| {
                    i.tool
                        .as_ref()
                        .filter(|t| !p.call_id.is_empty() && t.call_id == p.call_id)
                })
                .cloned();
            let approval = !p.approval.is_empty();
            let detail = if approval {
                p.detail.clone()
            } else {
                tool.as_ref()
                    .map(|t| {
                        if t.command.is_empty() {
                            t.paths.first().cloned().unwrap_or_default()
                        } else {
                            t.command.clone()
                        }
                    })
                    .unwrap_or_else(|| p.detail.clone())
            };
            let diffs = if p.diffs.is_empty() {
                tool.as_ref().map(|t| t.diffs.clone()).unwrap_or_default()
            } else {
                p.diffs.clone()
            };
            let heading = if approval {
                t(match p.approval.as_str() {
                    "project" => "chat.permission.projectApproval",
                    "agent" => "chat.permission.agentApproval",
                    _ => "chat.permission.approval",
                })
            } else {
                tv(
                    "chat.permission.asks",
                    &[
                        ("tool", super::tool_label(&self.session.tool).into()),
                        (
                            "action",
                            tool.as_ref()
                                .map(|t| t.kind.clone())
                                .unwrap_or_default()
                                .into(),
                        ),
                    ],
                )
            };
            let path = format!("{}/chat/permissions/{}", self.prefix, req.id);
            return Some(
                Self::banner(theme::starting())
                    .child(
                        h_flex()
                            .gap_2()
                            .text_xs()
                            .child(div().text_color(theme::starting()).child("!"))
                            .child(
                                div()
                                    .flex_1()
                                    .font_weight(FontWeight::MEDIUM)
                                    .text_color(theme::fg())
                                    .child(heading),
                            )
                            .when(pending.len() > 1, |el| {
                                el.child(div().text_color(theme::faint()).child(tv(
                                    "chat.permission.oneOf",
                                    &[("count", pending.len().into())],
                                )))
                            }),
                    )
                    .child(div().text_color(theme::fg()).child(p.title.clone()))
                    .when(!detail.is_empty() && !p.title.contains(&detail), |el| {
                        el.child(
                            div()
                                .max_h(px(80.))
                                .overflow_hidden()
                                .text_xs()
                                .font_family(theme::mono())
                                .text_color(theme::muted())
                                .child(detail.clone()),
                        )
                    })
                    .children(
                        diffs.iter().take(3).enumerate().map(|(i, d)| {
                            super::items::diff_block(format!("pd-{}-{i}", req.id), d)
                        }),
                    )
                    .child(
                        div()
                            .pt_1()
                            .child(self.permission_buttons(&req.id, path, &p, &view)),
                    )
                    .into_any_element(),
            );
        }
        if let Some(c) = self.cache_card() {
            let now = chrono::Utc::now().timestamp_millis();
            let left = c
                .expires_at
                .as_deref()
                .and_then(super::rows::millis)
                .map_or(0, |e| e - now);
            let idle = c
                .idle_since
                .as_deref()
                .and_then(super::rows::millis)
                .map_or(0, |i| now - i);
            let expired = left <= 0;
            let span = |ms: i64| super::rows::format_duration(ms.abs() as f64 / 1000.);
            let busy = self.composer.choosing;
            let held = self.composer.held.clone();
            return Some(
                Self::banner(theme::starting())
                    .child(
                        h_flex()
                            .gap_2()
                            .text_xs()
                            .child("⌛")
                            .child(
                                div()
                                    .flex_1()
                                    .font_weight(FontWeight::MEDIUM)
                                    .text_color(theme::fg())
                                    .child(if expired {
                                        t("chat.cache.expired")
                                    } else {
                                        tv("chat.cache.expires", &[("span", span(left).into())])
                                    }),
                            )
                            .child(
                                div()
                                    .text_color(theme::faint())
                                    .child(tv("chat.cache.idle", &[("span", span(idle).into())])),
                            ),
                    )
                    .child(
                        div().text_color(theme::muted()).child(tv(
                            "chat.cache.resend",
                            &[
                                ("expired", if expired { "yes" } else { "no" }.into()),
                                (
                                    "tokens",
                                    tv(
                                        "chat.cache.tokens",
                                        &[("n", tokens(c.context_used).into())],
                                    )
                                    .into(),
                                ),
                            ],
                        )),
                    )
                    .when_some(held.clone(), |el, h| {
                        el.child(
                            div()
                                .px_2p5()
                                .py_1p5()
                                .rounded_lg()
                                .bg(theme::raised())
                                .text_color(theme::fg())
                                .child(h),
                        )
                    })
                    .child(
                        h_flex()
                            .gap_1p5()
                            .pt_1()
                            .child(super::btn(
                                "cache-new",
                                t("chat.cache.newSession"),
                                false,
                                busy,
                                cx.listener(|this, _: &ClickEvent, window, cx| {
                                    let text = this
                                        .composer
                                        .held
                                        .take()
                                        .unwrap_or_else(|| this.text(cx).trim().to_string());
                                    this.composer
                                        .input
                                        .update(cx, |s, cx| s.set_value("", window, cx));
                                    cx.emit(ChatAction::NewSession { text });
                                    cx.notify();
                                }),
                            ))
                            .child(div().flex_1())
                            .child(super::btn(
                                "cache-send",
                                t("chat.cache.sendAnyway"),
                                false,
                                busy,
                                cx.listener(|this, _: &ClickEvent, window, cx| {
                                    this.choose_cache(false, window, cx)
                                }),
                            ))
                            .child(super::btn(
                                "cache-compact",
                                if busy {
                                    t("chat.cache.compacting")
                                } else {
                                    t("chat.cache.compactAndSend")
                                },
                                true,
                                busy,
                                cx.listener(|this, _: &ClickEvent, window, cx| {
                                    this.choose_cache(true, window, cx)
                                }),
                            )),
                    )
                    .into_any_element(),
            );
        }
        if self.session.tools_changed {
            let key = if self.session.no_resume {
                "chat.composer.toolsChangedNoResume"
            } else {
                "chat.composer.toolsChanged"
            };
            return Some(
                Self::banner(theme::accent())
                    .child(
                        h_flex()
                            .gap_2()
                            .child(
                                div()
                                    .flex_1()
                                    .min_w_0()
                                    .text_color(theme::muted())
                                    .child(tv(
                                        key,
                                        &[("tool", super::tool_label(&self.session.tool).into())],
                                    )),
                            )
                            .child(super::btn(
                                "tools-reload",
                                t("chat.composer.reloadNow"),
                                false,
                                false,
                                cx.listener(|this, _: &ClickEvent, _, cx| {
                                    this.post_empty("chat/reload", cx)
                                }),
                            )),
                    )
                    .into_any_element(),
            );
        }
        None
    }

    /// render_tasks is the plan's tasks, a chip that opens to the list (TasksBadge).
    fn render_tasks(&self, cx: &mut Context<Self>) -> Option<(AnyElement, Option<AnyElement>)> {
        let plan = self.items.iter().rev().find(|i| i.kind == "plan")?;
        if plan.plan.is_empty() {
            return None;
        }
        let done = plan.plan.iter().filter(|e| e.status == "completed").count();
        let all = plan.plan.len();
        let open = self.composer.tasks_open;
        let chip = div()
            .id("tasks")
            .px_2()
            .py_0p5()
            .rounded_md()
            .cursor_pointer()
            .text_xs()
            .text_color(if done == all {
                theme::running()
            } else {
                theme::muted()
            })
            .hover(|el| el.bg(theme::hover()))
            .child(if done == all {
                format!("✓ {}", t("chat.tasks.allDone"))
            } else {
                format!("{} {done}/{all}", t("chat.tasks.label"))
            })
            .on_click(cx.listener(|this, _: &ClickEvent, _, cx| {
                this.composer.tasks_open = !this.composer.tasks_open;
                cx.notify();
            }))
            .into_any_element();
        let list = open.then(|| {
            v_flex()
                .gap_0p5()
                .px_3()
                .py_2()
                .rounded_lg()
                .border_1()
                .border_color(theme::border())
                .bg(theme::panel())
                .text_sm()
                .children(plan.plan.iter().map(|e| {
                    let (mark, color) = match e.status.as_str() {
                        "completed" => ("✓", theme::running()),
                        "in_progress" => ("●", theme::starting()),
                        _ => ("○", theme::faint()),
                    };
                    h_flex()
                        .gap_2()
                        .child(div().flex_none().text_color(color).child(mark))
                        .child(
                            div()
                                .text_color(if e.status == "completed" {
                                    theme::muted()
                                } else {
                                    theme::fg()
                                })
                                .child(e.content.clone()),
                        )
                }))
                .into_any_element()
        });
        Some((chip, list))
    }

    pub(super) fn render_composer(
        &mut self,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) -> AnyElement {
        if self.past {
            return h_flex()
                .flex_none()
                .mx_4()
                .mb_4()
                .px_3()
                .py_2p5()
                .gap_2()
                .rounded_xl()
                .border_1()
                .border_color(theme::border())
                .bg(theme::raised())
                .child(super::btn(
                    "past-delete",
                    t("common.delete"),
                    false,
                    false,
                    cx.listener(|_, _: &ClickEvent, _, cx| cx.emit(ChatAction::DeletePast)),
                ))
                .child(
                    div()
                        .flex_1()
                        .min_w_0()
                        .text_sm()
                        .text_color(theme::muted())
                        .child(t("pastAgents.readOnly")),
                )
                .child(super::btn(
                    "past-continue",
                    t("pastAgents.continue"),
                    true,
                    false,
                    cx.listener(|_, _: &ClickEvent, _, cx| cx.emit(ChatAction::ContinuePast)),
                ))
                .into_any_element();
        }
        let placeholder = self.placeholder();
        if placeholder != self.composer.placeholder {
            self.composer.placeholder = placeholder.clone();
            self.composer
                .input
                .update(cx, |s, cx| s.set_placeholder(placeholder, window, cx));
        }
        if self.composer.runners.is_none() && self.loaded {
            // Read once: an empty answer stops it asking again.
            self.composer.runners = Some(LeadRunners::default());
            self.load_runners(cx);
        }
        let (menu, token) = self.menu(cx);
        let ix = self.composer.menu_ix.min(menu.len().saturating_sub(1));
        // A turn that waits on a permission runs still, and can be stopped.
        let working = self.working() || self.session.turn_started_at.is_some();
        let view = cx.entity();
        let above = self.render_above(cx);
        let tasks = self.render_tasks(cx);
        let (tasks_chip, tasks_list) = match tasks {
            Some((c, l)) => (Some(c), l),
            None => (None, None),
        };
        let menu_el = (!menu.is_empty()).then(|| {
            let mention = !token.starts_with('/');
            v_flex()
                .mb_1()
                .p_1()
                .rounded_lg()
                .border_1()
                .border_color(theme::border())
                .bg(theme::panel())
                .shadow_md()
                .child(
                    div()
                        .px_2()
                        .py_0p5()
                        .text_xs()
                        .text_color(theme::faint())
                        .child(if mention {
                            t("chat.composer.mentions")
                        } else {
                            t("chat.composer.commands")
                        }),
                )
                .children(menu.iter().enumerate().map(|(i, m)| {
                    let (v, item) = (view.clone(), m.clone());
                    h_flex()
                        .id(SharedString::from(format!("menu-{i}")))
                        .gap_3()
                        .px_2()
                        .py_1()
                        .rounded_md()
                        .cursor_pointer()
                        .text_sm()
                        .when(i == ix, |el| el.bg(theme::hover()))
                        .hover(|el| el.bg(theme::hover()))
                        .on_click(move |_, window, cx| {
                            v.update(cx, |this, cx| this.pick(item.clone(), window, cx))
                        })
                        .child(
                            div()
                                .flex_none()
                                .text_color(theme::fg())
                                .font_family(if mention { theme::mono() } else { "" })
                                .child(m.label.clone()),
                        )
                        .child(
                            div()
                                .min_w_0()
                                .truncate()
                                .text_xs()
                                .text_color(theme::faint())
                                .child(m.hint.clone()),
                        )
                }))
                .into_any_element()
        });
        let can_send = !self.composer.sending
            && (!self.text(cx).trim().is_empty() || !self.composer.attachments.is_empty());
        let attachments = (!self.composer.attachments.is_empty()).then(|| {
            h_flex()
                .gap_1p5()
                .flex_wrap()
                .pb_2()
                .children(self.composer.attachments.iter().enumerate().map(|(i, a)| {
                    let remove = cx.listener(move |this, _: &ClickEvent, _, cx| {
                        if i < this.composer.attachments.len() {
                            this.composer.attachments.remove(i);
                        }
                        cx.notify();
                    });
                    let x = div()
                        .id(SharedString::from(format!("att-x-{i}")))
                        .absolute()
                        .top(px(-6.))
                        .right(px(-6.))
                        .size_5()
                        .flex()
                        .items_center()
                        .justify_center()
                        .rounded_full()
                        .border_1()
                        .border_color(theme::border())
                        .bg(theme::raised())
                        .cursor_pointer()
                        .text_xs()
                        .text_color(theme::muted())
                        .child("✕")
                        .on_click(remove);
                    match &a.image {
                        Some(im) => div()
                            .relative()
                            .size_16()
                            .child(
                                div()
                                    .size_full()
                                    .rounded_lg()
                                    .overflow_hidden()
                                    .border_1()
                                    .border_color(theme::border())
                                    .child(
                                        img(im.clone()).size_full().object_fit(ObjectFit::Cover),
                                    ),
                            )
                            .child(x)
                            .into_any_element(),
                        None => div()
                            .relative()
                            .child(
                                h_flex()
                                    .h_11()
                                    .max_w(px(220.))
                                    .gap_2()
                                    .px_2()
                                    .pr_3()
                                    .rounded_xl()
                                    .border_1()
                                    .border_color(theme::border())
                                    .bg(theme::panel())
                                    .child(div().text_color(theme::muted()).child("▤"))
                                    .child(
                                        v_flex()
                                            .min_w_0()
                                            .child(div().text_sm().truncate().child(a.name.clone()))
                                            .child(
                                                div().text_xs().text_color(theme::faint()).child(
                                                    crate::files::human_bytes(a.bytes.len() as i64),
                                                ),
                                            ),
                                    ),
                            )
                            .child(x)
                            .into_any_element(),
                    }
                }))
                .into_any_element()
        });
        let session = self.session.clone();
        let paste_view = view.downgrade();
        let input = Textarea::new(&self.composer.input)
            .appearance(false)
            .on_paste(move |item, _, cx| {
                paste_view
                    .update(cx, |this, cx| {
                        let took = this.composer.paste(item, &session, cx);
                        cx.notify();
                        took
                    })
                    .unwrap_or(false)
            });
        let menu_len = menu.len();
        let menu_for_enter = menu.clone();
        v_flex()
            .flex_none()
            .px_4()
            .pt_2()
            .pb_3()
            .border_t_1()
            .border_color(theme::border())
            .children(tasks_list)
            .children(menu_el)
            .children(above)
            .child(
                v_flex()
                    .id("composer")
                    .rounded_xl()
                    .border_1()
                    .border_color(theme::border())
                    .bg(theme::panel())
                    .px_3()
                    .pt_2()
                    .pb_1p5()
                    // While a menu is open, its keys are its own.
                    .capture_action(cx.listener(move |this, _: &MoveDown, _, cx| {
                        if menu_len > 0 {
                            this.composer.menu_ix = (this.composer.menu_ix + 1) % menu_len;
                            cx.stop_propagation();
                            cx.notify();
                        }
                    }))
                    .capture_action(cx.listener(move |this, _: &MoveUp, window, cx| {
                        if menu_len > 0 {
                            this.composer.menu_ix =
                                (this.composer.menu_ix + menu_len - 1) % menu_len;
                            cx.stop_propagation();
                            cx.notify();
                        } else if this.text(cx).is_empty()
                            && let Some(last) = this.last_mine()
                        {
                            // Up in an empty composer brings your last message back.
                            let at = last.len();
                            this.composer.input.update(cx, |s, cx| {
                                s.set_value(last, window, cx);
                                s.set_selected_range(at..at, cx);
                            });
                            cx.stop_propagation();
                        }
                    }))
                    .capture_action(cx.listener(move |this, a: &Enter, window, cx| {
                        if !a.shift && !menu_for_enter.is_empty() {
                            let ix = this.composer.menu_ix.min(menu_for_enter.len() - 1);
                            this.pick(menu_for_enter[ix].clone(), window, cx);
                            cx.stop_propagation();
                        }
                    }))
                    .capture_action(cx.listener(move |this, _: &Escape, _, cx| {
                        if menu_len > 0 {
                            this.composer.dismissed = Some(token.clone());
                            cx.stop_propagation();
                            cx.notify();
                        }
                    }))
                    .children(attachments)
                    .child(div().text_sm().child(input))
                    .child(
                        h_flex()
                            .pt_1()
                            .gap_1()
                            .child(
                                div()
                                    .id("attach")
                                    .size_7()
                                    .flex()
                                    .items_center()
                                    .justify_center()
                                    .rounded_md()
                                    .cursor_pointer()
                                    .text_color(theme::muted())
                                    .hover(|el| el.bg(theme::hover()).text_color(theme::fg()))
                                    .child("+")
                                    .tooltip(|w, cx| {
                                        gpui_component::tooltip::Tooltip::new(t(
                                            "chat.composer.attachTip",
                                        ))
                                        .build(w, cx)
                                    })
                                    .on_click(cx.listener(|this, _: &ClickEvent, _, cx| {
                                        this.pick_files(cx)
                                    })),
                            )
                            .child(div().flex_1().min_w_0().child(self.render_options(cx)))
                            .children(tasks_chip)
                            .when(working, |el| {
                                el.child(super::btn(
                                    "stop-turn",
                                    t("chat.composer.stopTurn"),
                                    false,
                                    false,
                                    cx.listener(|this, _: &ClickEvent, _, cx| this.stop(cx)),
                                ))
                            })
                            .child(
                                div()
                                    .id("send")
                                    .px_3()
                                    .py_1()
                                    .rounded_md()
                                    .text_sm()
                                    .font_weight(FontWeight::MEDIUM)
                                    .bg(theme::accent())
                                    .text_color(gpui::white())
                                    .when(!can_send, |el| el.opacity(0.4))
                                    .when(can_send, |el| {
                                        el.cursor_pointer().hover(|el| el.opacity(0.9))
                                    })
                                    .child(if working {
                                        tv(
                                            "chat.composer.sendWhileWorking",
                                            &[(
                                                "tool",
                                                super::tool_label(&self.session.tool).into(),
                                            )],
                                        )
                                    } else {
                                        t("chat.composer.send")
                                    })
                                    .on_click(cx.listener(|this, _: &ClickEvent, window, cx| {
                                        this.submit(window, cx)
                                    })),
                            ),
                    ),
            )
            .into_any_element()
    }
}

pub fn tokens(n: i64) -> String {
    if n >= 1_000_000 {
        let v = n as f64 / 1_000_000.;
        let s = if n % 1_000_000 == 0 {
            format!("{v:.0}")
        } else {
            format!("{v:.1}")
        };
        tv("chat.tokens.millions", &[("n", s.into())])
    } else if n >= 1000 {
        tv("chat.tokens.thousands", &[("n", ((n + 500) / 1000).into())])
    } else {
        n.to_string()
    }
}

/// pick_paths asks for files with the system's dialog: GTK's on Linux,
/// where GTK already runs on the main thread for the webviews, the
/// platform's own elsewhere.
fn pick_paths(title: String, cx: &mut App) -> tokio::sync::oneshot::Receiver<Option<Vec<PathBuf>>> {
    let (tx, rx) = tokio::sync::oneshot::channel();
    #[cfg(target_os = "linux")]
    {
        use gtk::prelude::*;
        let _ = cx;
        if gtk::is_initialized_main_thread() {
            let dialog = gtk::FileChooserNative::new(
                Some(&title),
                None::<&gtk::Window>,
                gtk::FileChooserAction::Open,
                None,
                None,
            );
            dialog.set_select_multiple(true);
            let tx = std::cell::RefCell::new(Some(tx));
            dialog.connect_response(move |d, resp| {
                let paths = (resp == gtk::ResponseType::Accept).then(|| d.filenames());
                if let Some(tx) = tx.borrow_mut().take() {
                    let _ = tx.send(paths);
                }
                d.destroy();
            });
            dialog.show();
            // Kept until it answers (a native dialog isn't held by GTK).
            KEEP.with_borrow_mut(|k| {
                k.retain(|d| d.is_visible());
                k.push(dialog);
            });
            return rx;
        }
        let _ = tx.send(None);
        rx
    }
    #[cfg(not(target_os = "linux"))]
    {
        let answer = cx.prompt_for_paths(PathPromptOptions {
            files: true,
            directories: false,
            multiple: true,
            prompt: Some(title.into()),
        });
        cx.background_executor()
            .spawn(async move {
                let paths = answer.await.ok().and_then(|r| r.ok()).flatten();
                let _ = tx.send(paths);
            })
            .detach();
        rx
    }
}

#[cfg(target_os = "linux")]
thread_local! {
    static KEEP: std::cell::RefCell<Vec<gtk::FileChooserNative>> = const { std::cell::RefCell::new(Vec::new()) };
}

#[cfg(test)]
mod tests {
    use super::{match_files, mention_at, slash_query};

    #[test]
    fn mentions_and_commands_are_found_where_the_cursor_is() {
        assert_eq!(
            mention_at("look at @src/ma", 15),
            Some((8, "src/ma".into()))
        );
        assert_eq!(mention_at("mail a@b", 8), None);
        assert_eq!(mention_at("@", 1), Some((0, String::new())));
        assert_eq!(slash_query("/comp"), Some("comp"));
        assert_eq!(slash_query("/compact now"), None);
        let files: Vec<String> = ["desktop/src/Composer.tsx", "native/chat.rs", "README.md"]
            .iter()
            .map(|s| s.to_string())
            .collect();
        assert_eq!(match_files(&files, "chat", 8), ["native/chat.rs"]);
        assert_eq!(
            match_files(&files, "cmpsr", 8),
            ["desktop/src/Composer.tsx"]
        );
    }
}
