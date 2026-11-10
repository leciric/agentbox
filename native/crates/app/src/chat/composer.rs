//! The composer (Composer.tsx): a message box that grows with what's
//! written (Enter sends, Shift+Enter starts a new line), files and pictures
//! attached by picking, pasting or dropping them, "/" for the tool's
//! commands and "@" for files, projects and agents, the chat's settings
//! (options.rs: the AI tool and account, the model with its context window
//! and effort, the mode with plan, and more), and
//! what waits above it: a permission request, the sleeping agent's wake
//! button, the prompt cache about to expire (holding the message until you
//! choose), changed connectors, the plan's tasks.
use std::path::PathBuf;
use std::sync::Arc;

use agentbox_api::{
    ChatCache, ChatCacheChoice, ChatFileUpload, ChatImageUpload, ChatMessageRequest,
    ChatOptionRequest, ChatSession, Client, LeadRunnerRequest, LeadRunners,
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

mod options;

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

/// An agent a message can go to: its name and title.
type Target = (String, String);

/// A message that didn't go, kept to send again as it was or to edit.
pub struct Failed {
    key: usize,
    text: String,
    attachments: Vec<Attachment>,
    to: String,
    error: String,
}

pub struct Composer {
    /// Its width as last drawn, for what folds in a narrow one.
    pub width: f32,
    pub input: Entity<TextareaState>,
    pub attachments: Vec<Attachment>,
    menu_ix: usize,
    /// The menu Escape closed, by the token it was open for.
    dismissed: Option<String>,
    files: Option<Vec<String>>,
    projects: Option<Vec<(String, String)>>,
    /// The agents "@" can name and a lead's message can go to: project,
    /// name, title and the lead session that made it.
    agents: Option<Vec<(String, String, String, String)>>,
    /// Who a project chat's message goes to: an agent of the project
    /// (ChatMessageRequest.to), or its lead (None) (shared/messageTarget.ts).
    target: Option<String>,
    /// Messages that didn't reach the daemon, to send again or edit
    /// (lib/sending.ts): their text, files, target and why.
    pub(super) failed: Vec<Failed>,
    asked_mentions: bool,
    /// The skills "/" and "$" offer (Settings → Skills), read once.
    skills: Option<Vec<agentbox_api::Skill>>,
    asked_skills: bool,
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

/// skill_mention_at is the $word the cursor is in (skillMentionAt): a $ at
/// the start or after whitespace, and what's typed after it so far.
pub fn skill_mention_at(text: &str, cursor: usize) -> Option<(usize, String)> {
    let before = text.get(..cursor)?;
    let word = |c: char| c.is_ascii_alphanumeric() || matches!(c, ':' | '_' | '-');
    let q_len = before
        .chars()
        .rev()
        .take_while(|c| word(*c))
        .map(char::len_utf8)
        .sum::<usize>();
    let at = before.len() - q_len;
    if !before[..at].ends_with('$') {
        return None;
    }
    let dollar = at - 1;
    if dollar > 0 && !before[..dollar].ends_with(char::is_whitespace) {
        return None;
    }
    if text[cursor..].chars().next().is_some_and(word) {
        return None;
    }
    Some((dollar, before[at..].to_string()))
}

/// skill_title is a skill's name as words: review-pr is "Review Pr".
fn skill_title(name: &str) -> String {
    name.split(|c: char| c.is_whitespace() || matches!(c, ':' | '_' | '-'))
        .filter(|w| !w.is_empty())
        .map(|w| {
            let mut c = w.chars();
            c.next()
                .map(|f| f.to_uppercase().chain(c).collect())
                .unwrap_or_default()
        })
        .collect::<Vec<String>>()
        .join(" ")
}

/// search_skills is searchSkills: by name first (its letters in order at
/// worst), then its title, then its description; all by name for none.
pub fn search_skills(
    skills: &[agentbox_api::Skill],
    query: &str,
    limit: usize,
) -> Vec<agentbox_api::Skill> {
    let q = query.trim().trim_start_matches(['$', '/']).to_lowercase();
    let score = |value: &str, base: usize, fuzzy: bool| -> Option<usize> {
        if value.is_empty() {
            return None;
        }
        if value == q {
            return Some(base);
        }
        if value.starts_with(&q) {
            return Some(base + 2);
        }
        if value.match_indices(&q).any(|(i, _)| {
            i == 0 || value[..i].ends_with(|c: char| c.is_whitespace() || "/_:-".contains(c))
        }) {
            return Some(base + 4);
        }
        if value.contains(&q) {
            return Some(base + 6);
        }
        if fuzzy {
            let mut it = value.chars();
            return q
                .chars()
                .all(|ch| it.any(|c| c == ch))
                .then_some(base + 100);
        }
        None
    };
    let mut out: Vec<(usize, &agentbox_api::Skill)> = skills
        .iter()
        .filter_map(|s| {
            if q.is_empty() {
                return Some((0, s));
            }
            [
                score(&s.name.to_lowercase(), 0, true),
                score(&skill_title(&s.name).to_lowercase(), 1, false),
                score(&s.description.to_lowercase(), 30, false),
            ]
            .into_iter()
            .flatten()
            .min()
            .map(|sc| (sc, s))
        })
        .collect();
    out.sort_by(|a, b| a.0.cmp(&b.0).then_with(|| a.1.name.cmp(&b.1.name)));
    out.into_iter()
        .take(limit)
        .map(|(_, s)| s.clone())
        .collect()
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
                    this.want_skills(cx);
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
                width: 896.,
                dismissed: None,
                files: None,
                projects: None,
                agents: None,
                asked_mentions: false,
                skills: None,
                asked_skills: false,
                target: None,
                failed: Vec::new(),
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

    /// want_skills reads, once, the skills this chat's agent has and a user
    /// may invoke (composerSkills): a project's active ones, or AgentBox's
    /// enabled ones in Home.
    fn want_skills(&mut self, cx: &mut Context<Self>) {
        if self.composer.asked_skills {
            return;
        }
        let text = self.text(cx);
        let cursor = self.composer.input.read(cx).cursor().min(text.len());
        if slash_query(&text).is_none() && skill_mention_at(&text, cursor).is_none() {
            return;
        }
        self.composer.asked_skills = true;
        let project = self.agent.split('/').next().unwrap_or("").to_string();
        let home = project == agentbox_api::HOME_PROJECT;
        let path = if home {
            "/v1/skills".to_string()
        } else {
            format!("/v1/projects/{project}/skills")
        };
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let list = client
                .get::<Vec<agentbox_api::Skill>>(&path)
                .await
                .unwrap_or_default();
            this.update(cx, |this, cx| {
                this.composer.skills = Some(
                    list.into_iter()
                        .filter(|s| {
                            (if home {
                                s.enabled
                            } else {
                                s.active.unwrap_or(false)
                            }) && s.user_invocable
                        })
                        .collect(),
                );
                cx.notify();
            })
            .ok();
        })
        .detach();
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
        self.load_mentions(cx);
    }

    /// load_mentions reads what "@" can name and a message can go to.
    fn load_mentions(&mut self, cx: &mut Context<Self>) {
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
                        .map(|a| (a.project, a.name, a.title, a.lead_session))
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
            // The chat's skills first, then the tool's commands: Claude Code
            // lists skills among its commands too, which show once, as skills.
            let skills = self.composer.skills.as_deref().unwrap_or_default();
            let found = search_skills(skills, q, 6);
            let mut items: Vec<MenuItem> = found
                .iter()
                .map(|s| MenuItem {
                    label: format!("${}", s.name),
                    hint: s.description.clone(),
                    insert: format!("${} ", s.name),
                    start: 0,
                })
                .collect();
            items.extend(
                self.session
                    .commands
                    .iter()
                    .filter(|c| {
                        c.name.to_lowercase().contains(&ql)
                            && !skills.iter().any(|s| s.name == c.name)
                    })
                    .take(9usize.saturating_sub(items.len()))
                    .map(|c| MenuItem {
                        label: format!("/{}", c.name),
                        hint: c.description.clone(),
                        insert: format!("/{} ", c.name),
                        start: 0,
                    }),
            );
            return (items, token);
        }
        // "$" anywhere lists the skills alone, for one mid-sentence.
        if let Some((start, q)) = skill_mention_at(&text, cursor) {
            let token = format!("${start}:{q}");
            if self.composer.dismissed.as_deref() == Some(token.as_str()) {
                return (Vec::new(), token);
            }
            let items = search_skills(self.composer.skills.as_deref().unwrap_or_default(), &q, 8)
                .into_iter()
                .map(|s| MenuItem {
                    label: format!("${}", s.name),
                    hint: s.description.clone(),
                    insert: format!("${} ", s.name),
                    start,
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
        for (p, name, title, _) in self.composer.agents.iter().flatten() {
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

    /// targets are what this project chat can write to besides its lead
    /// (targetsOf): the agents its session made, then the project's others.
    /// cycle_target moves the To: chip on to the next agent (or back with
    /// `step` -1), round to the lead (Mod+. and Mod+Shift+., messageTarget
    /// cycleTarget); one no longer there starts again from the lead.
    pub(super) fn cycle_target(
        &mut self,
        step: isize,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let (own, others) = self.targets();
        if own.is_empty() && others.is_empty() {
            return;
        }
        let order: Vec<Option<String>> = std::iter::once(None)
            .chain(own.into_iter().chain(others).map(|(n, _)| Some(n)))
            .collect();
        let at = order
            .iter()
            .position(|o| *o == self.composer.target)
            .unwrap_or(0) as isize;
        let len = order.len() as isize;
        self.composer.target = order[((at + step + len) % len) as usize].clone();
        self.focus_composer(window, cx);
        cx.notify();
    }

    fn targets(&self) -> (Vec<Target>, Vec<Target>) {
        if !self.is_lead() {
            return (Vec::new(), Vec::new());
        }
        let project = self.agent.split('/').next().unwrap_or("");
        if project == agentbox_api::HOME_PROJECT {
            return (Vec::new(), Vec::new());
        }
        let name = self.agent.split('/').nth(1).unwrap_or("");
        let session = if name == "lead" {
            "main"
        } else {
            name.strip_prefix("lead.").unwrap_or("")
        };
        let mut mine: Vec<&(String, String, String, String)> = self
            .composer
            .agents
            .iter()
            .flatten()
            .filter(|(p, n, ..)| p == project && n != "lead" && !n.starts_with("lead."))
            .collect();
        mine.sort_by_key(|(_, n, ..)| {
            let digits: String = n.chars().filter(char::is_ascii_digit).collect();
            (digits.parse::<u64>().unwrap_or(0), n.clone())
        });
        let (own, others): (Vec<_>, Vec<_>) = mine
            .into_iter()
            .partition(|(.., s)| !session.is_empty() && s == session);
        let pair = |v: Vec<&(String, String, String, String)>| {
            v.into_iter()
                .map(|(_, n, title, _)| (n.clone(), title.clone()))
                .collect()
        };
        (pair(own), pair(others))
    }

    /// at_target reads "@agent-31 " at the start of a message as its target
    /// (atTarget): the agent, and the message without it.
    fn at_target(&self, text: &str) -> Option<(String, String)> {
        let rest = text.trim_start().strip_prefix('@')?;
        let (name, body) = rest.split_once(char::is_whitespace)?;
        let (own, others) = self.targets();
        own.iter()
            .chain(others.iter())
            .any(|(n, _)| n == name)
            .then(|| (name.to_string(), body.trim().to_string()))
    }

    fn send_text(&mut self, text: String, window: &mut Window, cx: &mut Context<Self>) {
        let (to, text) = match self.at_target(&text) {
            Some((to, body)) => (to, body),
            None => (self.composer.target.clone().unwrap_or_default(), text),
        };
        self.send_to(text, to, window, cx);
    }

    fn send_to(&mut self, text: String, to: String, window: &mut Window, cx: &mut Context<Self>) {
        let (images, files) = self.uploads();
        let kept = std::mem::take(&mut self.composer.attachments);
        self.composer
            .input
            .update(cx, |s, cx| s.set_value("", window, cx));
        self.composer.sending = true;
        // The chat takes it as a turn now, as Electron's does; the session's
        // own state replaces this as it arrives. A message to an agent from
        // the lead's chat leaves a notice here, and no turn.
        if to.is_empty() {
            if !matches!(
                self.session.state.as_str(),
                "running" | "starting" | "waiting"
            ) {
                let was = std::mem::replace(&mut self.session.state, "running".into());
                let at = std::time::Instant::now();
                self.unsent_state = Some((was, at));
                // A turn that never began gives the chat back what it said.
                cx.spawn(async move |this, cx| {
                    cx.background_executor().timer(super::UNSENT_HOLD).await;
                    this.update(cx, |this, cx| {
                        if this.unsent_state.as_ref().is_some_and(|(_, a)| *a == at)
                            && let Some((was, _)) = this.unsent_state.take()
                        {
                            this.session.state = was;
                            cx.emit(ChatAction::SendFailed(this.agent.clone()));
                            cx.notify();
                        }
                    })
                    .ok();
                })
                .detach();
                self.tick(cx);
            }
            cx.emit(ChatAction::Sent(self.agent.clone()));
        }
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
                        to: to.clone(),
                    },
                )
                .await;
            this.update_in(cx, |this, _window, cx| {
                this.composer.sending = false;
                if let Err(e) = res {
                    if let Some((was, _)) = this.unsent_state.take() {
                        this.session.state = was;
                    }
                    if to.is_empty() {
                        cx.emit(ChatAction::SendFailed(this.agent.clone()));
                    }
                    // What didn't go stays, marked, to send again or edit.
                    let key = this
                        .composer
                        .failed
                        .iter()
                        .map(|f| f.key + 1)
                        .max()
                        .unwrap_or(0);
                    this.composer.failed.push(Failed {
                        key,
                        text: back,
                        attachments: kept,
                        to,
                        error: e.to_string(),
                    });
                    this.failed_changed(this.composer.failed.len() - 1);
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    /// retry_failed sends a message that didn't go again, as it was.
    fn retry_failed(&mut self, key: usize, window: &mut Window, cx: &mut Context<Self>) {
        let Some(i) = self.composer.failed.iter().position(|f| f.key == key) else {
            return;
        };
        let f = self.composer.failed.remove(i);
        self.failed_changed(self.composer.failed.len() + 1);
        let held = std::mem::replace(&mut self.composer.attachments, f.attachments);
        let draft = self.text(cx);
        self.send_to(f.text, f.to, window, cx);
        // What was being written meanwhile stays in the composer.
        self.composer.attachments = held;
        if !draft.is_empty() {
            self.composer
                .input
                .update(cx, |s, cx| s.set_value(draft, window, cx));
        }
    }

    /// edit_failed takes a message that didn't go back into the composer.
    fn edit_failed(&mut self, key: usize, window: &mut Window, cx: &mut Context<Self>) {
        let Some(i) = self.composer.failed.iter().position(|f| f.key == key) else {
            return;
        };
        let f = self.composer.failed.remove(i);
        self.failed_changed(self.composer.failed.len() + 1);
        let mut text = f.text;
        let draft = self.text(cx);
        if !draft.trim().is_empty() {
            text = format!("{text}\n\n{draft}");
        }
        self.composer.attachments.extend(f.attachments);
        if !f.to.is_empty() {
            self.composer.target = Some(f.to);
        }
        self.composer
            .input
            .update(cx, |s, cx| s.set_value(text, window, cx));
        self.focus_composer(window, cx);
        cx.notify();
    }

    /// failed_row is a message that didn't go, in the timeline where it
    /// was sent (Timeline NotSent): it, why, and Send again and Edit.
    pub(super) fn failed_row(&self, n: usize, view: &Entity<Self>) -> AnyElement {
        let Some(f) = self.composer.failed.get(n) else {
            return div().into_any_element();
        };
        let (k1, k2) = (f.key, f.key);
        let (v1, v2) = (view.downgrade(), view.downgrade());
        v_flex()
            .w_full()
            .min_w_0()
            .overflow_hidden()
            .px_4()
            .py_2()
            .items_end()
            .gap_1()
            .child(
                div()
                    .max_w(relative(0.85))
                    .px_3()
                    .py_2()
                    .rounded_xl()
                    .bg(theme::surface_raised())
                    .border_1()
                    .border_color(theme::error().opacity(0.4))
                    .text_sm()
                    .text_color(theme::primary())
                    .child(f.text.clone()),
            )
            // Why, on its own lines inside the column (the daemon's error
            // can be a paragraph: three lines of it, the rest on hover),
            // and what to do about it under that, where a long error can't
            // push it off the page.
            .child(
                h_flex()
                    .w_full()
                    .justify_end()
                    .items_start()
                    .gap_2()
                    .text_xs()
                    .child(
                        div()
                            .flex_none()
                            .mt(px(1.))
                            .text_color(theme::error())
                            .child(crate::icons::ic("circle-alert")),
                    )
                    .child(
                        crate::app::controls::clamped(
                            ("failed-why", f.key as u64),
                            tv("chat.composer.notSent", &[("error", (&f.error).into())]),
                            3,
                        )
                        .max_w(relative(0.8))
                        .text_color(theme::error()),
                    ),
            )
            .child(
                h_flex()
                    .flex_none()
                    .gap_1()
                    .text_xs()
                    .child(crate::app::controls::button(
                        ("failed-retry", f.key as u64),
                        t("chat.sending.retry"),
                        crate::app::controls::Btn::Ghost,
                        false,
                        move |_, window, cx| {
                            v1.update(cx, |this, cx| this.retry_failed(k1, window, cx))
                                .ok();
                        },
                    ))
                    .child(crate::app::controls::button(
                        ("failed-edit", f.key as u64),
                        t("chat.sending.edit"),
                        crate::app::controls::Btn::Ghost,
                        false,
                        move |_, window, cx| {
                            v2.update(cx, |this, cx| this.edit_failed(k2, window, cx))
                                .ok();
                        },
                    )),
            )
            .into_any_element()
    }

    /// failed_changed has the timeline's rows follow the messages that
    /// didn't go, last in it, of which there were `before`.
    fn failed_changed(&mut self, before: usize) {
        let at = self.rows.len();
        self.list
            .splice(at..at + before, self.composer.failed.len());
        self.list.scroll_to_end();
    }

    /// render_target is the To: chip of a project's chat (TargetPicker): its
    /// lead, or one of its agents, the session's first.
    fn render_target(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let (own, others) = self.targets();
        if own.is_empty() && others.is_empty() {
            return None;
        }
        let text = self.text(cx);
        let shown = self
            .at_target(&text)
            .map(|(to, _)| to)
            .or_else(|| self.composer.target.clone());
        let label = shown.clone().unwrap_or_else(|| t("chat.target.lead"));
        let view = cx.entity().downgrade();
        let current = shown;
        Some(
            // TargetChip: h-8 max-w-44 rounded-full px-2.5, 12px, "To:" faint.
            Button::new("chat-target")
                .ghost()
                .compact()
                .h_8()
                .max_w(px(176.))
                .px(px(10.))
                .gap_1()
                .rounded(gpui_component::button::ButtonRounded::Size(px(999.)))
                .text_size(px(12.))
                .text_color(if current.is_some() {
                    theme::brand(200)
                } else {
                    theme::subtle()
                })
                .when(current.is_some(), |b| b.bg(theme::brand(500).opacity(0.15)))
                .child(
                    h_flex()
                        .min_w_0()
                        .gap_1()
                        .text_size(px(12.))
                        .line_height(px(18.))
                        .child(
                            div()
                                .flex_none()
                                .text_color(theme::faint())
                                .child(t("chat.target.to")),
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
                .tooltip(tv("chat.target.label", &[("target", (&label).into())]))
                .dropdown_menu_with_anchor(Anchor::BottomLeft, move |menu, _, _| {
                    let pick = |to: Option<String>| {
                        let view = view.clone();
                        move |_: &ClickEvent, _: &mut Window, cx: &mut App| {
                            view.update(cx, |this, cx| {
                                this.composer.target = to.clone();
                                cx.notify();
                            })
                            .ok();
                        }
                    };
                    let mark = |on: bool| if on { "✓ " } else { "   " };
                    let mut menu = menu.max_h(px(420.)).scrollable(true).item(
                        PopupMenuItem::new(format!(
                            "{}{}",
                            mark(current.is_none()),
                            t("chat.target.lead")
                        ))
                        .on_click(pick(None)),
                    );
                    for (title, list) in
                        [("chat.target.own", &own), ("chat.target.others", &others)]
                    {
                        if list.is_empty() {
                            continue;
                        }
                        menu = menu.separator().label(t(title));
                        for (name, about) in list.iter() {
                            let on = current.as_deref() == Some(name.as_str());
                            let label = if about.is_empty() {
                                name.clone()
                            } else {
                                format!("{name} · {about}")
                            };
                            menu = menu.item(
                                PopupMenuItem::new(format!("{}{label}", mark(on)))
                                    .on_click(pick(Some(name.clone()))),
                            );
                        }
                    }
                    menu
                })
                .into_any_element(),
        )
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
                // A read that failed leaves the empty answer in place: None
                // would have the next frame ask again, and the one after,
                // for as long as it fails.
                this.composer.runners = Some(res.unwrap_or_default());
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
        } else if let Some(to) = &self.composer.target {
            tv("chat.composer.placeholderTo", &[("name", to.into())])
        } else if pending {
            t("chat.composer.answerRequest")
        } else if self.session.turn_started_at.is_some() {
            tv("chat.composer.busy", &[("tool", tool.into())])
        } else if self.items.is_empty() && !self.older {
            // Composer.tsx: the lead's own words in a project's chat, Home's
            // and an agent's the plain ones.
            t(
                if self.is_lead()
                    && !self
                        .agent
                        .starts_with(&format!("{}/", agentbox_api::HOME_PROJECT))
                {
                    "chat.composer.placeholderLead"
                } else {
                    "chat.composer.placeholderEmpty"
                },
            )
        } else {
            t("chat.composer.placeholder")
        }
    }

    /// banner is Attached: a card on the composer's top edge, 20px in from
    /// each side, its foot tucked 16px under the composer (-mb-4), in a tone:
    /// amber for what asks, rose for what failed, neutral otherwise.
    fn banner(tint: Hsla) -> Div {
        let (border, bg) = if tint == theme::starting() {
            (
                theme::amber(300).opacity(0.25),
                theme::attached_warning().opacity(0.9),
            )
        } else if tint == theme::error() {
            (
                theme::rose(400).opacity(0.25),
                theme::attached_danger().opacity(0.9),
            )
        } else {
            (theme::line_strong(), theme::attached_neutral().opacity(0.9))
        };
        v_flex()
            .mx(px(20.))
            .mb(px(-16.))
            .px(px(14.))
            .pt(px(10.))
            .pb_6()
            .rounded_t(px(16.))
            .border_1()
            .border_b_0()
            .border_color(border)
            .bg(bg)
            .text_size(theme::text::UI)
    }

    /// context_meter is ContextMeter: a ring of how full the context window
    /// is, once it's a fifth full, amber past 85%.
    fn context_meter(&self) -> Option<AnyElement> {
        let s = &self.session;
        if s.context_size <= 0 {
            return None;
        }
        let fraction = (s.context_used as f64 / s.context_size as f64).min(1.);
        if fraction < 0.2 {
            return None;
        }
        let tip = tv(
            "chat.context.used",
            &[
                ("used", tokens(s.context_used).into()),
                ("total", tokens(s.context_size).into()),
            ],
        );
        let arc = if fraction > 0.85 {
            theme::amber(400)
        } else {
            theme::brand(400)
        };
        Some(
            div()
                .id("context-meter")
                .flex_none()
                .size_7()
                .flex()
                .items_center()
                .justify_center()
                .tooltip(move |w, cx| {
                    gpui_component::tooltip::Tooltip::new(tip.clone()).build(w, cx)
                })
                .child(
                    img(ring_image(fraction.max(0.02), theme::surface_strong(), arc)).size(px(18.)),
                )
                .into_any_element(),
        )
    }

    /// render_review_card is AgentReviewCard: over a finished agent's
    /// composer, what it made, its pull request and Review changes; while it
    /// works on changes asked for, it says so; asleep, it carries Start.
    fn render_review_card(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let card = self.review_card.as_ref()?;
        if card.item.is_none() && !card.asked {
            return None;
        }
        let wake = self.asleep.clone();
        let count = |k: &str| self.media_kinds.get(k).copied().unwrap_or(0);
        let made: Vec<(&str, String)> = [
            ("images", "screenshot", "v2.card.screenshots"),
            ("film", "recording", "v2.card.recordings"),
            ("globe", "page", "v2.card.pages"),
        ]
        .into_iter()
        .filter(|(_, k, _)| count(k) > 0)
        .map(|(icon, k, key)| (icon, tv(key, &[("count", count(k).into())])))
        .collect();
        let title = if card.asked {
            t("v2.card.requested")
        } else if card.item.as_ref().is_some_and(|i| !i.verdict.is_empty()) {
            t("v2.card.reviewed")
        } else {
            t("v2.card.ready")
        };
        let pr = card.pr.as_ref().map(|pr| {
            let tone = if pr.state == "merged" {
                theme::violet(300)
            } else if pr.checks == "failing" || pr.conflict {
                theme::rose(300)
            } else {
                theme::secondary()
            };
            let label = if pr.state == "merged" {
                t("v2.card.prMerged")
            } else if pr.state == "closed" {
                t("v2.card.prClosed")
            } else if pr.draft {
                t("memory.pulls.draft")
            } else if pr.conflict {
                t("memory.pulls.conflicts")
            } else if pr.checks == "failing" {
                t("v2.card.checksFailing")
            } else if pr.checks == "passing" {
                t("v2.card.checksPass")
            } else {
                t("v2.card.prOpen")
            };
            let url = pr.url.clone();
            h_flex()
                .gap_1()
                .text_color(tone)
                .child(
                    crate::icons::ic(if pr.state == "merged" {
                        "git-merge"
                    } else {
                        "git-pull-request"
                    })
                    .size(px(14.)),
                )
                .child(
                    div()
                        .id("review-card-pr")
                        // PrNumberLink: brand-300 when it links.
                        .when(!pr.url.is_empty(), |el| el.text_color(theme::brand(300)))
                        .cursor_pointer()
                        .hover(|el| el.underline())
                        .on_click(move |_, _, cx| cx.open_url(&url))
                        .child(format!("#{}", pr.number)),
                )
                .child(label)
        });
        let item = card.item.clone();
        Some(
            h_flex()
                .mx_3()
                .flex_wrap()
                .gap_x_3()
                .gap_y(px(6.))
                .px_3()
                .py_2()
                .rounded_t_xl()
                .border_1()
                .border_b_0()
                .border_color(theme::line_strong())
                .bg(theme::composer())
                .child(
                    h_flex()
                        .min_w_0()
                        .gap_2()
                        .child({
                            let name = if card.asked {
                                "loader-circle"
                            } else {
                                "circle-check"
                            };
                            crate::icons::icon(
                                name,
                                crate::icons::ic(name)
                                    .size(px(16.))
                                    .text_color(theme::subtle()),
                            )
                        })
                        .child(
                            div()
                                .flex_none()
                                .text_size(theme::text::UI)
                                .line_height(px(19.5))
                                .font_weight(FontWeight::MEDIUM)
                                .text_color(theme::title())
                                .child(title),
                        )
                        .when_some(wake.clone(), |el, state| {
                            el.child(
                                h_flex()
                                    .flex_none()
                                    .gap_1()
                                    .text_size(px(12.))
                                    .text_color(theme::muted())
                                    .child(crate::icons::ic("moon").size(px(14.)))
                                    .child(tv("v2.card.asleep", &[("state", (&state).into())])),
                            )
                        }),
                )
                .child(
                    h_flex()
                        .min_w_0()
                        .flex_1()
                        .flex_wrap()
                        .gap_x(px(10.))
                        .gap_y_1()
                        .text_size(px(12.))
                        .line_height(px(18.))
                        .text_color(theme::subtle())
                        .children(made.into_iter().map(|(icon, text)| {
                            h_flex()
                                .gap_1()
                                .child(
                                    crate::icons::ic(icon)
                                        .size(px(14.))
                                        .text_color(theme::faint()),
                                )
                                .child(text)
                        }))
                        .children(pr),
                )
                .when(item.is_some() || wake.is_some(), |el| {
                    el.child(
                        h_flex()
                            .ml_auto()
                            .flex_none()
                            .gap_2()
                            .when_some(wake, |el, state| {
                                el.child(super::btn(
                                    "review-card-wake",
                                    tv("chat.composer.wake", &[("state", (&state).into())]),
                                    false,
                                    false,
                                    cx.listener(|_, _: &ClickEvent, _, cx| {
                                        cx.emit(ChatAction::Wake)
                                    }),
                                ))
                            })
                            .when_some(item, |el, item| {
                                let asked = card.asked;
                                el.child(
                                    h_flex()
                                        .id("review-open")
                                        .h_7()
                                        .gap_2()
                                        .px_3()
                                        .rounded_lg()
                                        .text_size(theme::text::UI)
                                        .font_weight(FontWeight::MEDIUM)
                                        .cursor_pointer()
                                        .map(|el| {
                                            if asked {
                                                el.border_1()
                                                    .border_color(theme::line())
                                                    .bg(theme::surface_raised())
                                                    .text_color(theme::primary())
                                                    .hover(|el| el.bg(theme::surface_strong()))
                                                    .active(|s| s.bg(theme::surface_vivid()))
                                            } else {
                                                el.bg(theme::brand(500))
                                                    .text_color(theme::on_brand())
                                                    .hover(|el| el.bg(theme::brand(600)))
                                                    .active(|s| s.bg(theme::brand(700)))
                                            }
                                        })
                                        .on_click(cx.listener(move |_, _: &ClickEvent, _, cx| {
                                            cx.emit(ChatAction::OpenReview(Box::new(item.clone())))
                                        }))
                                        .child(t("v2.card.review"))
                                        .child(crate::icons::ic("arrow-right").size(px(16.))),
                                )
                            }),
                    )
                })
                .into_any_element(),
        )
    }

    /// render_review is a project lead's "N agents ready to review" over its
    /// composer (ReviewV2's ReviewCount banner: the dock, mx-3 rounded-t-xl
    /// border-line-strong bg-composer, px-3 py-1.5 text-ui).
    fn render_review(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let count = self.review_count;
        if count == 0 || !self.is_lead() {
            return None;
        }
        Some(
            h_flex()
                .mx_3()
                .gap_2()
                .px_3()
                .py(px(6.))
                .rounded_t_xl()
                .border_1()
                .border_b_0()
                .border_color(theme::line_strong())
                .bg(theme::composer())
                .text_size(theme::text::UI)
                .child(
                    div()
                        .flex_none()
                        .size(px(6.))
                        .rounded_full()
                        .border_1()
                        .border_color(theme::secondary()),
                )
                .child(
                    div()
                        .flex_1()
                        .min_w_0()
                        .truncate()
                        .text_color(theme::secondary())
                        .child(tv("v2.review.readyBanner", &[("count", count.into())])),
                )
                .child(
                    h_flex()
                        .id("review-banner-open")
                        .flex_none()
                        .gap_1()
                        .px_2()
                        .py(px(2.))
                        .rounded_md()
                        .font_weight(FontWeight::MEDIUM)
                        .text_color(theme::title())
                        .cursor_pointer()
                        .hover(|el| el.bg(theme::surface_strong()))
                        .active(|s| s.bg(theme::surface_vivid()))
                        .on_click(cx.listener(|_, _: &ClickEvent, _, cx| {
                            cx.emit(ChatAction::ReviewProject)
                        }))
                        .child(t("v2.review.review"))
                        .child(crate::icons::ic("arrow-right").size(px(14.))),
                )
                .into_any_element(),
        )
    }

    /// render_above is what waits on the composer, attached to its top.
    fn render_above(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let view = cx.entity();
        let carded = self
            .review_card
            .as_ref()
            .is_some_and(|c| c.item.is_some() || c.asked);
        if let Some(state) = self.asleep.as_ref().filter(|_| !carded) {
            return Some(
                // Composer.tsx's asleep bar: a moon, the words in tertiary
                // (leading-relaxed), and an h-6 brand Start with its Play.
                Self::banner(theme::muted())
                    .child(
                        h_flex()
                            .gap_2()
                            .child(
                                crate::icons::ic("moon")
                                    .size(px(14.))
                                    .text_color(theme::muted()),
                            )
                            .child(
                                div()
                                    .flex_1()
                                    .min_w_0()
                                    .line_height(relative(1.625))
                                    .text_color(theme::tertiary())
                                    .child(tv(
                                        "chat.composer.asleep",
                                        &[
                                            ("state", state.into()),
                                            ("name", (&self.agent_name).into()),
                                        ],
                                    )),
                            )
                            .child(
                                h_flex()
                                    .id("chat-wake")
                                    .flex_none()
                                    .h_6()
                                    .gap_1()
                                    .px(px(10.))
                                    .rounded_md()
                                    .bg(theme::brand(500).opacity(0.9))
                                    .text_size(px(12.))
                                    .font_weight(FontWeight::MEDIUM)
                                    .text_color(white())
                                    .cursor_pointer()
                                    .hover(|el| el.bg(theme::brand(500)))
                                    .on_click(cx.listener(|_, _: &ClickEvent, _, cx| {
                                        cx.emit(ChatAction::Wake)
                                    }))
                                    .child(crate::icons::ic("play").size(px(12.)))
                                    .child(tv("chat.composer.wake", &[("state", state.into())])),
                            ),
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
                // The tool call's command or first file; the request's own
                // detail only for an approval (PermissionBanner).
                tool.as_ref()
                    .map(|t| {
                        if t.command.is_empty() {
                            t.paths.first().cloned().unwrap_or_default()
                        } else {
                            t.command.clone()
                        }
                    })
                    .unwrap_or_default()
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
                            .text_size(px(12.))
                            .line_height(px(18.))
                            .child(
                                crate::icons::ic("shield-alert")
                                    .size(px(14.))
                                    .text_color(theme::amber(300)),
                            )
                            .child(
                                div()
                                    .font_weight(FontWeight::MEDIUM)
                                    .text_color(theme::amber(100))
                                    .child(heading),
                            )
                            .when(pending.len() > 1, |el| {
                                el.child(
                                    div()
                                        .ml_auto()
                                        .text_color(theme::amber(200).opacity(0.6))
                                        .child(tv(
                                            "chat.permission.oneOf",
                                            &[("count", pending.len().into())],
                                        )),
                                )
                            }),
                    )
                    .child(
                        div()
                            .mt_1()
                            .text_size(theme::text::UI)
                            .line_height(px(19.5))
                            .text_color(theme::primary())
                            .child(p.title.clone()),
                    )
                    .when(approval && !detail.is_empty(), |el| {
                        el.child(
                            div()
                                .mt_0p5()
                                .text_size(px(12.))
                                .text_color(theme::muted())
                                .child(detail.clone()),
                        )
                    })
                    .when(
                        !approval && !detail.is_empty() && !p.title.contains(&detail),
                        |el| {
                            el.child(
                                div()
                                    .mt_1()
                                    .max_h(px(80.))
                                    .overflow_hidden()
                                    .text_size(px(12.))
                                    .font_family(theme::mono())
                                    .text_color(theme::muted())
                                    .child(detail.clone()),
                            )
                        },
                    )
                    .children(
                        diffs.iter().take(3).enumerate().map(|(i, d)| {
                            super::items::diff_block(format!("pd-{}-{i}", req.id), d)
                        }),
                    )
                    .child(self.permission_buttons(&req.id, path, &p, &view, true))
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
                            .child(crate::icons::of("⌛"))
                            .child(
                                div()
                                    .flex_1()
                                    .font_weight(FontWeight::MEDIUM)
                                    .text_color(theme::primary())
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
                                .bg(theme::surface_raised())
                                .text_color(theme::primary())
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
                            .child(super::btn_busy(
                                "cache-compact",
                                if busy {
                                    t("chat.cache.compacting")
                                } else {
                                    t("chat.cache.compactAndSend")
                                },
                                true,
                                busy,
                                false,
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

    /// render_tasks is TasksBadge: the plan on the composer's top edge, its
    /// task under way, how many are done and a bar of each, opening to the list.
    fn render_tasks(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let plan = self.items.iter().rev().find(|i| i.kind == "plan")?;
        if plan.plan.is_empty() {
            return None;
        }
        let done = plan.plan.iter().filter(|e| e.status == "completed").count();
        let all = plan.plan.len();
        let finished = done == all;
        let current = plan
            .plan
            .iter()
            .find(|e| e.status == "in_progress")
            .or_else(|| plan.plan.iter().find(|e| e.status == "pending"))
            .map(|e| e.content.clone())
            .unwrap_or_default();
        let open = self.composer.tasks_open;
        let head = h_flex()
            .id("tasks")
            .w_full()
            .gap_2()
            .text_size(px(12.))
            .line_height(px(18.))
            .cursor_pointer()
            .on_click(cx.listener(|this, _: &ClickEvent, _, cx| {
                this.composer.tasks_open = !this.composer.tasks_open;
                cx.notify();
            }))
            .child(
                crate::icons::ic("list-todo")
                    .size(px(14.))
                    .text_color(theme::muted()),
            )
            .child(
                div()
                    .flex_none()
                    .text_color(theme::subtle())
                    .child(t("chat.tasks.label")),
            )
            .child(
                div()
                    .min_w_0()
                    .flex_1()
                    .truncate()
                    .font_weight(FontWeight::MEDIUM)
                    .text_color(theme::secondary())
                    .child(if finished {
                        t("chat.tasks.allDone")
                    } else {
                        current
                    }),
            )
            .child(
                div()
                    .flex_none()
                    .text_color(if finished {
                        theme::emerald(400)
                    } else {
                        theme::subtle()
                    })
                    .child(format!("{done}/{all}")),
            )
            .child(
                h_flex()
                    .flex_none()
                    .gap_0p5()
                    .children(plan.plan.iter().take(10).map(|e| {
                        div()
                            .h(px(3.))
                            .w(px(10.))
                            .rounded_full()
                            .bg(match e.status.as_str() {
                                "completed" => theme::emerald(400),
                                "in_progress" => theme::brand(400),
                                _ => theme::ghost(),
                            })
                    })),
            )
            .child(crate::motion::flip(
                crate::icons::ic("chevron-down")
                    .size(px(14.))
                    .text_color(theme::subtle()),
                "composer-plan-chevron",
                open,
            ));
        let list = open.then(|| {
            v_flex().mt_2().gap_1().children(plan.plan.iter().map(|e| {
                let (color, text) = match e.status.as_str() {
                    "completed" => (theme::emerald(400), theme::subtle()),
                    "in_progress" => (theme::brand(300), theme::primary()),
                    _ => (theme::faint(), theme::muted()),
                };
                h_flex()
                    .items_start()
                    .gap_2()
                    .text_size(theme::text::UI)
                    .line_height(relative(1.625))
                    .child(
                        div()
                            .flex_none()
                            .mt(px(3.))
                            .size(px(14.))
                            .flex()
                            .items_center()
                            .justify_center()
                            .text_color(color)
                            .map(|el| match e.status.as_str() {
                                "completed" => el.child(crate::icons::ic("check").size(px(14.))),
                                "in_progress" => {
                                    el.child(div().size_1p5().rounded_full().bg(color))
                                }
                                _ => el.child(
                                    div()
                                        .size_1p5()
                                        .rounded_full()
                                        .border_1()
                                        .border_color(color),
                                ),
                            }),
                    )
                    .child(
                        div()
                            .text_color(text)
                            .when(e.status == "completed", |el| el.line_through())
                            .child(e.content.clone()),
                    )
            }))
        });
        Some(
            Self::banner(theme::muted())
                .child(head)
                .children(list)
                .into_any_element(),
        )
    }

    pub(super) fn render_composer(
        &mut self,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) -> AnyElement {
        if self.closed {
            return h_flex()
                .flex_none()
                .mx_4()
                .mb_4()
                .px_3()
                .py_2p5()
                .gap_2()
                .rounded_xl()
                .border_1()
                .border_color(theme::line())
                .bg(theme::surface_raised())
                .child(super::btn(
                    "closed-delete",
                    t("common.delete"),
                    false,
                    false,
                    cx.listener(|_, _: &ClickEvent, _, cx| cx.emit(ChatAction::DeleteClosed)),
                ))
                .child(
                    div()
                        .flex_1()
                        .min_w_0()
                        .text_sm()
                        .text_color(theme::muted())
                        .child(t("sessions.readOnly")),
                )
                .child(super::btn(
                    "closed-reopen",
                    t("sessions.reopen"),
                    false,
                    false,
                    cx.listener(|_, _: &ClickEvent, _, cx| cx.emit(ChatAction::ReopenClosed)),
                ))
                .child(super::btn(
                    "closed-continue",
                    t("sessions.continue"),
                    true,
                    false,
                    cx.listener(|_, _: &ClickEvent, _, cx| cx.emit(ChatAction::ContinueClosed)),
                ))
                .into_any_element();
        }
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
                .border_color(theme::line())
                .bg(theme::surface_raised())
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
        let review = self
            .render_review(cx)
            .or_else(|| self.render_review_card(cx));
        if self.is_lead() && !self.composer.asked_mentions && self.loaded {
            self.load_mentions(cx);
        }
        let target = self.render_target(cx);
        let tasks = self.render_tasks(cx);
        let meter = self.context_meter();
        let menu_el = (!menu.is_empty()).then(|| {
            let mention = !token.starts_with('/');
            let skills = menu.iter().filter(|m| m.label.starts_with('$')).count();
            let grouped = !mention && skills > 0 && skills < menu.len();
            let head = |text: String| {
                div()
                    .px_2()
                    .py_0p5()
                    .text_xs()
                    .text_color(theme::faint())
                    .child(text)
                    .into_any_element()
            };
            v_flex()
                .mb_1()
                .p_1()
                .rounded_lg()
                .border_1()
                .border_color(theme::line())
                .bg(theme::card())
                .shadow_md()
                // "/" lists the chat's skills ($name) and then the tool's
                // commands: each under its own head when there are both, as
                // Electron's list, where they used to run on as one.
                .when(!grouped, |el| {
                    el.child(head(if token.starts_with('$') || skills == menu.len() {
                        t("chat.composer.skills")
                    } else if mention {
                        t("chat.composer.mentions")
                    } else {
                        t("chat.composer.commands")
                    }))
                })
                .children(menu.iter().enumerate().flat_map(|(i, m)| {
                    let (v, item) = (view.clone(), m.clone());
                    let skill = m.label.starts_with('$');
                    let first = i == 0 || menu[i - 1].label.starts_with('$') != skill;
                    let group = (grouped && first).then(|| {
                        head(if skill {
                            t("chat.composer.skills")
                        } else {
                            t("chat.composer.commandsGroup")
                        })
                    });
                    let row = h_flex()
                        .id(SharedString::from(format!("menu-{i}")))
                        .gap_3()
                        .px_2()
                        // One height for every row: a mono name beside a
                        // smaller hint made some a pixel taller.
                        .h(px(28.))
                        .flex_none()
                        .rounded_md()
                        .cursor_pointer()
                        .text_sm()
                        .when(i == ix, |el| el.bg(theme::surface_strong()))
                        .hover(|el| el.bg(theme::surface_strong()))
                        .active(|s| s.bg(theme::surface_vivid()))
                        .on_click(move |_, window, cx| {
                            v.update(cx, |this, cx| this.pick(item.clone(), window, cx))
                        })
                        .child(
                            div()
                                .flex_none()
                                .text_color(theme::primary())
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
                        .into_any_element();
                    group.into_iter().chain(Some(row))
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
                        .border_color(theme::line())
                        .bg(theme::surface_raised())
                        .cursor_pointer()
                        .text_xs()
                        .text_color(theme::muted())
                        .child(crate::icons::of("✕"))
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
                                    .border_color(theme::line())
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
                                    .border_color(theme::line())
                                    .bg(theme::card())
                                    .child(
                                        div()
                                            .text_color(theme::muted())
                                            .child(crate::icons::of("▤")),
                                    )
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
        // Composer.tsx's textarea: 14px, leading-relaxed, no padding of its
        // own (Size::Size(16) is 14px text and 2px/8px of the input's).
        let input = Textarea::new(&self.composer.input)
            .appearance(false)
            .with_size(gpui_component::Size::Size(px(16.)))
            .line_height(px(22.75))
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
        let focused = self
            .composer
            .input
            .read(cx)
            .focus_handle(cx)
            .contains_focused(window, cx);
        let width_view = view.downgrade();
        div()
            .flex_none()
            .w_full()
            .px_6()
            .pt(px(28.))
            .pb_5()
            .child(
                v_flex()
                    .w_full()
                    .max_w(px(896.))
                    .mx_auto()
                    .relative()
                    // Its width, for SetupChip's fold (a container query in Electron).
                    .child(
                        canvas(
                            move |bounds, _, cx| {
                                let w = f32::from(bounds.size.width);
                                width_view
                                    .update(cx, |this, cx| {
                                        if (this.composer.width - w).abs() > 0.5 {
                                            this.composer.width = w;
                                            cx.notify();
                                        }
                                    })
                                    .ok();
                            },
                            |_, _, _, _| {},
                        )
                        .absolute()
                        .left_0()
                        .right_0()
                        .h_0(),
                    )
                    .children(menu_el)
                    .children(review)
                    .children(tasks)
                    // Attached's slide-up, on whichever banner waits.
                    .children(
                        above.map(|el| {
                            crate::motion::enter(v_flex().child(el), "composer-banner", 0)
                        }),
                    )
                    .child(
                        v_flex()
                            .id("composer")
                            .rounded(px(22.))
                            .border_1()
                            .border_color(if focused {
                                theme::line_vivid()
                            } else {
                                theme::line_strong()
                            })
                            .bg(theme::composer())
                            .shadow(theme::shadow_composer(focused))
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
                                } else if this.composer.target.is_some() && this.text(cx).is_empty()
                                {
                                    // Esc in an empty composer points it back at the lead.
                                    this.composer.target = None;
                                    cx.stop_propagation();
                                    cx.notify();
                                }
                            }))
                            .children(attachments)
                            .child(
                                div()
                                    .px_2()
                                    .pb(px(4.))
                                    .pt(px(11.))
                                    .text_sm()
                                    .line_height(relative(1.625))
                                    .child(input),
                            )
                            .child(
                                h_flex()
                                    .px_2()
                                    .pb_2()
                                    .gap_1()
                                    .children(target)
                                    .child(
                                        div()
                                            .id("attach")
                                            .flex_none()
                                            .size_8()
                                            .flex()
                                            .items_center()
                                            .justify_center()
                                            .rounded_full()
                                            .cursor_pointer()
                                            .text_color(theme::subtle())
                                            .hover(|el| {
                                                el.bg(theme::surface_raised())
                                                    .text_color(theme::primary())
                                            })
                                            .child(crate::icons::ic("paperclip").size(px(16.)))
                                            .tooltip(|w, cx| {
                                                gpui_component::tooltip::Tooltip::new(t(
                                                    "chat.composer.attachTip",
                                                ))
                                                .build(w, cx)
                                            })
                                            .on_click(cx.listener(
                                                |this, _: &ClickEvent, _, cx| this.pick_files(cx),
                                            )),
                                    )
                                    .child(div().flex_1().min_w_0().child(self.render_options(cx)))
                                    .children(meter)
                                    .when(working, |el| {
                                        el.child(super::btn(
                                            "stop-turn",
                                            t("chat.composer.stopTurn"),
                                            false,
                                            false,
                                            cx.listener(|this, _: &ClickEvent, _, cx| {
                                                this.stop(cx)
                                            }),
                                        ))
                                    })
                                    .child({
                                        let label: SharedString = if working {
                                            tv(
                                                "chat.composer.sendWhileWorking",
                                                &[(
                                                    "tool",
                                                    super::tool_label(&self.session.tool).into(),
                                                )],
                                            )
                                            .into()
                                        } else {
                                            t("chat.composer.send").into()
                                        };
                                        // Electron's round send: an arrow on the brand,
                                        // its words in the tooltip.
                                        // The send button: size-8, brand-500, at 30% while there's nothing to send.
                                        div()
                                            .id("send")
                                            .flex_none()
                                            .size_8()
                                            .rounded_full()
                                            .flex()
                                            .items_center()
                                            .justify_center()
                                            .bg(theme::brand(500))
                                            .text_color(theme::on_brand())
                                            .when(!can_send, |el| el.opacity(0.3))
                                            .when(can_send, |el| {
                                                el.cursor_pointer()
                                                    .hover(|el| el.bg(theme::brand(600)))
                                                    .active(|el| el.bg(theme::brand(700)))
                                            })
                                            .tooltip(move |w, cx| {
                                                gpui_component::tooltip::Tooltip::new(label.clone())
                                                    .build(w, cx)
                                            })
                                            .child(crate::icons::ic("arrow-up").size(px(16.)))
                                            .on_click(cx.listener(
                                                |this, _: &ClickEvent, window, cx| {
                                                    this.submit(window, cx)
                                                },
                                            ))
                                    }),
                            ),
                    ),
            )
            .into_any_element()
    }
}

/// option_chip is a composer option's trigger as Electron's OptionMenu:
/// h-7, px-2, rounded-lg, text-ui, text-muted.
fn option_chip(b: Button) -> Button {
    b.ghost()
        .compact()
        .h_7()
        .px_2()
        .rounded(gpui_component::button::ButtonRounded::Size(px(8.)))
        .text_size(theme::text::UI)
        .text_color(theme::muted())
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

thread_local! {
    static RINGS: std::cell::RefCell<std::collections::HashMap<(u32, u32, u32), Arc<Image>>> =
        std::cell::RefCell::new(std::collections::HashMap::new());
}

/// ring_image is ContextMeter's ring as the SVG Electron draws: r 7 in an
/// 18 box, a 2px track and the filled share over it from the top, round-capped.
fn ring_image(fraction: f64, track: Hsla, arc: Hsla) -> Arc<Image> {
    let key = (
        (fraction * 1000.).round() as u32,
        rgb_key(track),
        rgb_key(arc),
    );
    RINGS.with(|r| {
        r.borrow_mut()
            .entry(key)
            .or_insert_with(|| {
                let c = 2. * std::f64::consts::PI * 7.;
                let svg = format!(
                    r#"<svg xmlns="http://www.w3.org/2000/svg" width="72" height="72" viewBox="0 0 18 18"><g transform="rotate(-90 9 9)"><circle cx="9" cy="9" r="7" fill="none" stroke="{}" stroke-opacity="{}" stroke-width="2"/><circle cx="9" cy="9" r="7" fill="none" stroke="{}" stroke-opacity="{}" stroke-width="2" stroke-linecap="round" stroke-dasharray="{c}" stroke-dashoffset="{}"/></g></svg>"#,
                    theme::hex(track),
                    track.a,
                    theme::hex(arc),
                    arc.a,
                    c * (1. - fraction)
                );
                Arc::new(Image::from_bytes(ImageFormat::Svg, svg.into_bytes()))
            })
            .clone()
    })
}

fn rgb_key(c: Hsla) -> u32 {
    let r = c.to_rgb();
    ((r.r * 255.) as u32) << 24
        | ((r.g * 255.) as u32) << 16
        | ((r.b * 255.) as u32) << 8
        | (r.a * 255.) as u32
}

/// ReviewCard is what a finished agent's review card shows (AgentReviewCard):
/// its review item, whether changes asked for are under way, its PR.
#[derive(Clone, PartialEq, Default)]
pub struct ReviewCard {
    pub item: Option<agentbox_api::ReviewItem>,
    pub asked: bool,
    pub pr: Option<agentbox_api::PullRequest>,
}
