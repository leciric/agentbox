//! One conversation, the lead's or an agent's: its items, kept up to date from
//! the event stream's chat events, as the timeline's rows (chat/rows.rs), and
//! a composer that sends a message (chat/composer.rs). Ctrl+F finds in it
//! (chat/find.rs).
use std::collections::{HashMap, HashSet};
use std::sync::Arc;
use std::time::{Duration, Instant};

use agentbox_api::{ChatEvent, ChatItem, ChatSession, ChatThread, Checkpoint, Client};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::{h_flex, v_flex};

use crate::{bench, theme};
use agentbox_i18n::{t, tv};

mod composer;
pub use composer::ReviewCard;
mod evidence;
mod find;
mod items;
pub mod rows;
mod stream;

pub use composer::{Composer, match_files, tokens};

/// session_line is what the chat's header says of its session (chat.status.*).
fn session_line(s: &ChatSession) -> String {
    let key = format!("chat.status.{}", s.state);
    if s.state.is_empty() || !agentbox_i18n::has(&key) {
        return String::new();
    }
    tv(&key, &[("tool", (&tool_label(&s.tool)).into())])
}

/// A chat that isn't ready is asked for again this many times before it
/// says so (lib/chat.ts's readyAttempts), and keeps being asked for after.
const READY_TRIES: u32 = 6;

/// ready_delay is the wait before the next try: 0.5 s doubling to 4 s
/// (retryLoadDelay), then every 5 s (chatReadyRetryEvery).
fn ready_delay(failures: u32) -> Duration {
    if failures > READY_TRIES {
        return Duration::from_secs(5);
    }
    Duration::from_millis((500u64 << failures.saturating_sub(1).min(3)).min(4000))
}

pub fn tool_label(tool: &str) -> String {
    match tool {
        "claude" | "" => "Claude Code",
        "codex" => "Codex",
        "opencode" => "OpenCode",
        "cursor" => "Cursor",
        "gemini" => "Gemini CLI",
        other => other,
    }
    .to_string()
}

/// How many messages the first page holds, as the Electron app asks.
const PAGE: usize = 200;

/// What the chat asks of the window around it.
#[derive(Clone, Debug)]
pub enum ChatAction {
    /// Roll the agent back to a turn's checkpoint, once confirmed.
    Rollback {
        checkpoint: String,
        n: i64,
    },
    /// Fork a new agent from a turn's checkpoint.
    Fork {
        checkpoint: String,
        n: i64,
    },
    /// Open an agent by its ref.
    OpenAgent(String),
    /// Open a project.
    OpenProject(String),
    /// Fork the lead's session into a new one, from an item on.
    ForkSession(String),
    /// Start or resume the sleeping agent.
    Wake,
    /// A new lead session, from the composer (the prompt cache card): with
    /// a first message to send there.
    NewSession {
        text: String,
    },
    /// A past agent's chat: continue it in a new agent, or delete it.
    ContinuePast,
    DeletePast,
    /// A closed lead session read from History: continue it in a new one,
    /// reopen it, or delete it.
    ContinueClosed,
    /// Open the review on a finished agent's work (its card's Review).
    OpenReview(Box<agentbox_api::ReviewItem>),
    /// Merge a finished agent's pull request, once confirmed (its card's
    /// Merge): the project and the number.
    Merge(String, i64),
    ReopenClosed,
    DeleteClosed,
    /// The review banner's Review: the project's Work on what waits.
    ReviewProject,
}

/// The picture shown over everything, zoomed.
pub struct Zoom {
    pub image: Arc<Image>,
    pub name: String,
    pub scale: f32,
    pub pan: Point<Pixels>,
    pub drag: Option<Point<Pixels>>,
    pub focus: FocusHandle,
}

/// btn is a small button of the chat's: primary (the brand's) or not.
pub(crate) fn btn(
    id: &'static str,
    label: String,
    primary: bool,
    disabled: bool,
    on_click: impl Fn(&ClickEvent, &mut Window, &mut App) + 'static,
) -> AnyElement {
    div()
        .id(id)
        .flex_none()
        .px_2p5()
        .py_1()
        .rounded_md()
        .text_xs()
        .font_weight(FontWeight::MEDIUM)
        .map(|el| {
            if primary {
                el.bg(theme::brand(500)).text_color(theme::on_brand())
            } else {
                el.border_1()
                    .border_color(theme::line())
                    .bg(theme::surface_raised())
                    .text_color(theme::primary())
            }
        })
        .when(disabled, |el| el.opacity(0.5))
        .when(!disabled, |el| {
            el.cursor_pointer()
                .hover(|s| s.opacity(0.85))
                .on_click(on_click)
        })
        .child(label)
        .into_any_element()
}

pub struct ChatView {
    /// Counts the glides to the end and whatever else moves the chat on
    /// purpose: a glide that isn't the latest stops where it is.
    glide: u64,
    client: Client,
    /// The API prefix of this chat: /v1/projects/<p>[/sessions/<s>] (the
    /// lead) or /v1/agents/<p>/<a>.
    prefix: String,
    title: SharedString,
    /// Whether it draws its own title bar: not under the project's header.
    pub header: bool,
    /// The chat's ref, as chat events name it (project/lead, project/agent-01).
    pub agent: String,
    /// Its agent's name, for the composer's words (the lead: none).
    pub agent_name: String,
    /// The agent's machine is stopped or paused: a message wakes it.
    pub asleep: Option<String>,
    /// A project's lead: how many of its agents wait for review, the banner
    /// over its composer (ReviewCount's banner).
    pub review_count: usize,
    /// The timeline's row width at the last paint, for a user bubble's
    /// one-line test (render_user).
    row_width: std::rc::Rc<std::cell::Cell<f32>>,
    /// A finished agent's review card over its composer (AgentReviewCard),
    /// handed in by the app, and the agent's media counts by kind for it.
    pub review_card: Option<ReviewCard>,
    media_kinds: std::collections::BTreeMap<String, i64>,
    /// A past agent's chat: read-only, Continue and Delete where the
    /// composer was (ChatTab's readOnly).
    pub past: bool,
    /// A closed lead session read from History: read-only, Delete, Reopen
    /// and Continue where the composer was (SessionHistory's readOnly).
    pub closed: bool,
    /// The finished agents' cards, as they're read.
    evidence: evidence::Evidences,
    /// Text to add to the composer's on the next frame (a picked element).
    append: Vec<String>,
    /// Focus the message box when next drawn (its element isn't there yet
    /// in the frame a chat is opened in).
    pub(crate) focus_next: bool,
    items: Vec<ChatItem>,
    rows: Vec<rows::Keyed>,
    session: ChatSession,
    seq: i64,
    loaded: bool,
    error: Option<SharedString>,
    /// page_load counts the pages asked for, so a load still retrying stops
    /// once another has been asked for.
    page_load: u64,
    list: ListState,
    composer: Composer,
    /// When the last unpainted chunk arrived, for bench's arrival-to-paint.
    arrived: Option<Instant>,
    /// How much of each streaming message is on screen (bytes): the reveal
    /// (chat/stream.rs) eases toward what has arrived rather than jumping.
    pub(crate) shown: HashMap<String, std::rc::Rc<std::cell::Cell<usize>>>,
    last_frame: Option<Instant>,
    /// Opened: tool calls and work groups (by item id or row key), settled
    /// turns past their fold, subagents, handoffs.
    expanded: HashSet<String>,
    open_turns: HashSet<String>,
    /// Permission requests whose answer is on its way.
    answering: HashSet<String>,
    /// The agent's checkpoints, by the user message whose turn each ends.
    checkpoints: HashMap<String, Checkpoint>,
    /// Pictures in the conversation, read once each (None while on the way).
    images: HashMap<String, Option<Arc<Image>>>,
    pub zoom: Option<Zoom>,
    /// The conversation has items before those held, and a read of them is out.
    older: bool,
    loading_older: bool,
    /// What's held is a window around a found item, not the latest: newer
    /// items exist past it, and events wait for Jump to latest.
    newer: bool,
    /// The list shows its end (else Jump to latest shows).
    at_end: bool,
    find: Option<find::Find>,
    focus: FocusHandle,
    _tick: Option<Task<()>>,
    /// Rows that arrived at the end after the chat loaded, and when: they
    /// slide in (motion::enter) the first time they're drawn; history
    /// scrolled into view never does.
    pub(crate) entering: std::collections::HashMap<String, std::time::Instant>,
    _subs: Vec<Subscription>,
}

impl EventEmitter<ChatAction> for ChatView {}

impl ChatView {
    /// status_line is what its session is doing, in words.
    pub fn status_line(&self) -> String {
        session_line(&self.session)
    }

    /// working is whether a turn runs.
    pub fn working(&self) -> bool {
        matches!(self.session.state.as_str(), "running" | "starting")
    }

    pub fn is_lead(&self) -> bool {
        self.prefix.starts_with("/v1/projects/")
    }

    pub fn new(
        client: Client,
        prefix: String,
        agent: String,
        title: SharedString,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) -> Self {
        let (composer, subs) = Composer::new(window, cx);
        let list = ListState::new(0, ListAlignment::Bottom, px(800.));
        list.set_follow_mode(FollowMode::Tail);
        // Scrolled to the top of what's loaded: read the page before it.
        let view = cx.entity().downgrade();
        list.set_scroll_handler(move |ev, _, cx| {
            let top = ev.visible_range.start == 0 && ev.is_scrolled;
            let (following, last) = (ev.is_following_tail, ev.visible_range.end);
            view.update(cx, |this, cx| {
                if top {
                    this.load_older(cx);
                }
                // (The list is borrowed here: ask it nothing.)
                let end = following || last >= this.rows.len();
                if end != this.at_end {
                    this.at_end = end;
                    cx.notify();
                }
            })
            .ok();
        });
        let mut this = Self {
            client,
            prefix,
            title,
            header: true,
            row_width: Default::default(),
            agent,
            agent_name: String::new(),
            asleep: None,
            review_count: 0,
            review_card: None,
            media_kinds: Default::default(),
            past: false,
            closed: false,
            evidence: Default::default(),
            append: Vec::new(),
            focus_next: false,
            items: Vec::new(),
            rows: Vec::new(),
            session: ChatSession::default(),
            seq: 0,
            loaded: false,
            error: None,
            page_load: 0,
            list,
            composer,
            arrived: None,
            shown: Default::default(),
            last_frame: None,
            expanded: Default::default(),
            open_turns: Default::default(),
            answering: Default::default(),
            checkpoints: Default::default(),
            images: Default::default(),
            zoom: None,
            older: false,
            loading_older: false,
            newer: false,
            at_end: true,
            glide: 0,
            find: None,
            focus: cx.focus_handle(),
            _tick: None,
            entering: Default::default(),
            _subs: subs,
        };
        this.load(cx);
        this
    }

    /// rebuild makes the rows again from the items, and tells the list
    /// which of them changed: the ones before and after what differs stay.
    fn rebuild(&mut self, cx: &mut Context<Self>) {
        let new = rows::rows(
            &self.items,
            &self.session,
            &self.open_turns,
            &self.checkpoints,
        );
        let old = &self.rows;
        let pre = old
            .iter()
            .zip(&new)
            .take_while(|(a, b)| a.key == b.key)
            .count();
        let room = old.len().min(new.len()) - pre;
        let suf = old
            .iter()
            .rev()
            .zip(new.iter().rev())
            .take(room)
            .take_while(|(a, b)| a.key == b.key)
            .count();
        let mut changed: Vec<usize> = (0..pre).filter(|&i| old[i].sig != new[i].sig).collect();
        changed.extend(
            (0..suf)
                .filter(|&k| old[old.len() - 1 - k].sig != new[new.len() - 1 - k].sig)
                .map(|k| new.len() - 1 - k),
        );
        // What's new past the old end, once there was something: arrivals.
        if !old.is_empty() && suf == 0 && new.len() > old.len().max(pre) {
            let now = std::time::Instant::now();
            self.entering
                .retain(|_, t| now.duration_since(*t).as_secs() < 2);
            for r in &new[pre.max(old.len())..] {
                self.entering.entry(r.key.clone()).or_insert(now);
            }
        }
        if pre + suf != old.len() || pre + suf != new.len() {
            self.list
                .splice(pre..old.len() - suf, new.len() - pre - suf);
        }
        for ix in changed {
            self.list.remeasure_items(ix..ix + 1);
        }
        self.rows = new;
        self.fetch_images(cx);
        self.tick(cx);
    }

    /// tick redraws every second while a turn runs, for its "Working for".
    fn tick(&mut self, cx: &mut Context<Self>) {
        let running = self
            .items
            .iter()
            .any(|i| i.kind == "user" && i.result.is_none())
            || self.working();
        if !running {
            self._tick = None;
            return;
        }
        if self._tick.is_some() {
            return;
        }
        self._tick = Some(cx.spawn(async move |this, cx| {
            loop {
                cx.background_executor().timer(Duration::from_secs(1)).await;
                if this.update(cx, |_, cx| cx.notify()).is_err() {
                    return;
                }
            }
        }));
    }

    /// fetch_images reads the pictures the conversation holds that it
    /// hasn't yet (GET …/chat/images/<id>).
    fn fetch_images(&mut self, cx: &mut Context<Self>) {
        let wanted: Vec<(String, String)> = self
            .items
            .iter()
            .flat_map(|i| i.images.iter())
            .filter(|im| !self.images.contains_key(&im.id))
            .map(|im| (im.id.clone(), im.mime_type.clone()))
            .collect();
        for (id, mime) in wanted {
            self.images.insert(id.clone(), None);
            let client = self.client.clone();
            let path = format!("{}/chat/images/{id}", self.prefix);
            cx.spawn(async move |this, cx| {
                // A picture that couldn't be read for a moment is read again,
                // and one the daemon still couldn't be reached for is
                // forgotten, to be asked for the next time the chat changes,
                // rather than left a blank placeholder for good.
                let mut failures = 0;
                let res = loop {
                    let res = client
                        .raw(agentbox_api::client::Method::GET, &path, None)
                        .await;
                    match &res {
                        Err(e) if failures < 4 && agentbox_api::client::not_ready(e) => {
                            failures += 1;
                            cx.background_executor().timer(ready_delay(failures)).await;
                        }
                        _ => break res,
                    }
                };
                this.update(cx, |this, cx| {
                    // Not reached, or the daemon couldn't answer (503): asked for
                    // again later. One it doesn't have (404) stays a placeholder.
                    if res.as_ref().is_err_and(|e| {
                        agentbox_api::client::unreachable(e)
                            || e.downcast_ref::<agentbox_api::client::DaemonError>()
                                .is_some_and(|d| d.status == 503)
                    }) {
                        this.images.remove(&id);
                    }
                    if let Ok(bytes) = res {
                        let format = ImageFormat::from_mime_type(&mime).unwrap_or(ImageFormat::Png);
                        this.images.insert(
                            id.clone(),
                            Some(Arc::new(Image::from_bytes(format, bytes.to_vec()))),
                        );
                        this.list.remeasure();
                        cx.notify();
                    }
                })
                .ok();
            })
            .detach();
        }
    }

    /// load_checkpoints reads the agent's turns it can go back to (an agent's
    /// chat only: the lead has no worktree of its own to roll back).
    fn load_checkpoints(&mut self, cx: &mut Context<Self>) {
        if self.is_lead() {
            return;
        }
        let client = self.client.clone();
        let path = format!("{}/checkpoints", self.prefix);
        cx.spawn(async move |this, cx| {
            let Ok(list) = client.get::<Vec<Checkpoint>>(&path).await else {
                return;
            };
            this.update(cx, |this, cx| {
                this.checkpoints = list
                    .into_iter()
                    .filter(|c| c.kind == "turn" && !c.turn.is_empty())
                    .map(|c| (c.turn.clone(), c))
                    .collect();
                this.rebuild(cx);
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    pub fn latest_checkpoint(&self) -> i64 {
        self.checkpoints
            .values()
            .map(|c| c.number)
            .max()
            .unwrap_or(0)
    }

    /// load_older reads the page before the oldest item held, and puts it above
    /// without moving what's on screen.
    fn load_older(&mut self, cx: &mut Context<Self>) {
        if !self.older || self.loading_older {
            return;
        }
        let Some(first) = self.items.first().map(|i| i.id.clone()) else {
            return;
        };
        self.loading_older = true;
        cx.notify();
        let client = self.client.clone();
        let path = format!("{}/chat?limit={PAGE}&before={first}", self.prefix);
        cx.spawn(async move |this, cx| {
            let res = client.get::<ChatThread>(&path).await;
            this.update(cx, |this, cx| {
                this.loading_older = false;
                cx.notify();
                let Ok(t) = res else { return };
                // The page may overlap what an event already brought.
                let have: HashSet<&str> = this.items.iter().map(|i| i.id.as_str()).collect();
                let page: Vec<ChatItem> = t
                    .items
                    .into_iter()
                    .filter(|i| !have.contains(i.id.as_str()))
                    .collect();
                let top = this.list.logical_scroll_top();
                let before = this.rows.len();
                this.items.splice(0..0, page);
                this.older = t.older;
                this.rebuild(cx);
                let added = this.rows.len().saturating_sub(before);
                this.list.scroll_to(ListOffset {
                    item_ix: top.item_ix + added,
                    offset_in_item: top.offset_in_item,
                });
            })
            .ok();
        })
        .detach();
    }

    /// set_draft puts text in the composer, not sent (lib/drafts.ts composeIn).
    pub fn set_draft(&mut self, text: &str, window: &mut Window, cx: &mut Context<Self>) {
        let text = text.to_string();
        self.composer
            .input
            .update(cx, |s, cx| s.set_value(text, window, cx));
    }

    /// draft_text is what the composer holds, not sent.
    pub fn draft_text(&self, cx: &App) -> String {
        self.composer.input.read(cx).value().to_string()
    }

    /// attach adds a file to the message being written: a picture shown as
    /// one when it's one.
    pub fn attach(&mut self, name: String, mime: String, bytes: Vec<u8>, cx: &mut Context<Self>) {
        let image = ImageFormat::from_mime_type(&mime)
            .filter(|f| *f != ImageFormat::Svg)
            .map(|f| Arc::new(Image::from_bytes(f, bytes.clone())));
        self.composer.attachments.push(composer::Attachment {
            name,
            mime,
            bytes,
            image,
        });
        cx.notify();
    }

    /// append_draft adds text after the draft, on the next frame.
    pub fn append_draft(&mut self, text: &str, cx: &mut Context<Self>) {
        self.append.push(text.to_string());
        cx.notify();
    }

    /// focus_composer puts the cursor in the message box.
    pub fn focus_composer(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        self.composer.input.update(cx, |s, cx| s.focus(window, cx));
        // Again once drawn: a chat just opened has no box in the tree yet,
        // and the window's focus falls back to the app's root.
        self.focus_next = true;
        cx.notify();
    }

    /// stop ends the turn in progress (POST …/chat/cancel).
    fn stop(&mut self, cx: &mut Context<Self>) {
        self.post_empty("chat/cancel", cx);
    }

    fn post_empty(&mut self, what: &str, cx: &mut Context<Self>) {
        let client = self.client.clone();
        let path = format!("{}/{what}", self.prefix);
        cx.spawn(async move |this, cx| {
            let res = client
                .raw(agentbox_api::client::Method::POST, &path, None)
                .await;
            this.update(cx, |this, cx| {
                if let Err(e) = res {
                    crate::toasts::error(cx, e.to_string());
                }
                this.error = None;
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// is_empty is whether the chat has nothing in it yet (New chat is off).
    pub fn is_empty(&self) -> bool {
        self.items.is_empty()
    }

    pub fn load(&mut self, cx: &mut Context<Self>) {
        self.read_page(format!("{}/chat?limit={PAGE}", self.prefix), None, cx);
        self.load_checkpoints(cx);
        self.load_media_counts(cx);
        self.composer.load_cache(&self.client, &self.prefix, cx);
    }

    /// load_media_counts reads what the agent made, by kind, for its review
    /// card (useMediaCounts on the agent).
    fn load_media_counts(&mut self, cx: &mut Context<Self>) {
        if self.is_lead() {
            return;
        }
        let Some(rest) = self.prefix.strip_prefix("/v1/agents/") else {
            return;
        };
        let Some((p, a)) = rest.split_once('/') else {
            return;
        };
        let path = format!("/v1/media/counts?project={p}&agent={a}");
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let Ok(c) = client.get::<agentbox_api::MediaCounts>(&path).await else {
                return;
            };
            this.update(cx, |this, cx| {
                if this.media_kinds != c.kinds {
                    this.media_kinds = c.kinds;
                    cx.notify();
                }
            })
            .ok();
        })
        .detach();
    }

    /// read_page replaces what's held with a page: the latest, or the
    /// window around a found item (reveal), scrolled to it.
    fn read_page(&mut self, path: String, reveal: Option<String>, cx: &mut Context<Self>) {
        let client = self.client.clone();
        self.page_load += 1;
        let load = self.page_load;
        cx.spawn(async move |this, cx| {
            // A chat asked for before the daemon has it ready (a session just
            // made, a lead still starting: 404), or while it can't be reached
            // (a connection refused or dropped, 503) is asked for again, as
            // the Electron app's chat does (lib/chat.ts's chatReadyRetry):
            // a few times, slowing down, then every few seconds while it's
            // shown. Only then does it say why it can't load.
            let mut failures = 0u32;
            let res = loop {
                let res = client.get::<ChatThread>(&path).await;
                let Err(e) = &res else { break res };
                if !agentbox_api::client::not_ready(e) {
                    break res;
                }
                failures += 1;
                let lost = agentbox_api::client::unreachable(e);
                let more = this
                    .update(cx, |this, cx| {
                        if this.page_load != load {
                            return false;
                        }
                        if failures >= READY_TRIES {
                            this.error = Some(if lost {
                                t("chat.connectionLost").into()
                            } else {
                                e.to_string().into()
                            });
                            cx.notify();
                        }
                        true
                    })
                    .unwrap_or(false);
                if !more {
                    return;
                }
                cx.background_executor().timer(ready_delay(failures)).await;
            };
            this.update(cx, |this, cx| {
                if this.page_load != load {
                    return;
                }
                match res {
                    Ok(t) => {
                        this.agent = t.agent;
                        this.seq = t.seq;
                        this.session = t.session;
                        this.items = t.items;
                        this.older = t.older;
                        this.newer = t.newer;
                        this.rows.clear();
                        this.rebuild(cx);
                        this.list
                            .reset(this.rows.len() + this.composer.failed.len());
                        this.loaded = true;
                        this.error = None;
                        match reveal.and_then(|id| this.row_of(&id)) {
                            Some(ix) => {
                                this.list.set_follow_mode(FollowMode::Normal);
                                this.list.scroll_to_reveal_item(ix);
                                this.at_end = false;
                            }
                            None => {
                                this.list.set_follow_mode(FollowMode::Tail);
                                this.at_end = true;
                            }
                        }
                        bench::mark("chat-loaded");
                    }
                    Err(e) => this.error = Some(e.to_string().into()),
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// row_of is the row that shows an item, opening the fold it's behind.
    fn row_of(&mut self, id: &str) -> Option<usize> {
        let ix = self.items.iter().position(|i| i.id == id)?;
        let find = |rows: &[rows::Keyed]| {
            rows.iter().position(|r| match &r.row {
                rows::Row::User(i)
                | rows::Row::Woken(i)
                | rows::Row::Note(i)
                | rows::Row::Compaction(i)
                | rows::Row::Assistant { ix: i, .. } => *i == ix,
                rows::Row::Subagent { ix: i, children } => *i == ix || children.contains(&ix),
                rows::Row::Work { items, .. } => items.contains(&ix),
                _ => false,
            })
        };
        if let Some(r) = find(&self.rows) {
            return Some(r);
        }
        // Behind a settled turn's fold: open it.
        let it = &self.items[ix];
        let turn = if it.turn.is_empty() {
            it.id.clone()
        } else {
            it.turn.clone()
        };
        self.open_turns.insert(turn);
        let new = rows::rows(
            &self.items,
            &self.session,
            &self.open_turns,
            &self.checkpoints,
        );
        self.rows = new;
        self.list
            .reset(self.rows.len() + self.composer.failed.len());
        find(&self.rows)
    }

    /// reveal shows an item: scrolled to where it's held, else read around it.
    pub fn reveal(&mut self, id: String, cx: &mut Context<Self>) {
        if let Some(ix) = self.row_of(&id) {
            self.glide += 1;
            self.list.set_follow_mode(FollowMode::Normal);
            self.list.scroll_to_reveal_item(ix);
            self.at_end = false;
            cx.notify();
            return;
        }
        self.read_page(
            format!("{}/chat?around={id}&limit=50", self.prefix),
            Some(id),
            cx,
        );
    }

    /// jump_to_latest goes back to the end, reading the latest page again
    /// when what's held is a window around a found item.
    fn jump_to_latest(&mut self, cx: &mut Context<Self>) {
        if self.newer {
            self.read_page(format!("{}/chat?limit={PAGE}", self.prefix), None, cx);
            return;
        }
        // The last screenful glides by (Electron's scrollTo smooth);
        // anything further is skipped to, since a long chat would take
        // seconds, and with reduced motion it all is.
        let list = self.list.clone();
        let left = |list: &ListState| {
            f32::from(list.max_offset_for_scrollbar().y + list.scroll_px_offset_for_scrollbar().y)
        };
        let screen = f32::from(list.viewport_bounds().size.height).max(200.);
        let far = left(&list) - screen;
        if far > 0. {
            list.scroll_by(px(far));
        }
        let total = left(&list).max(0.);
        if cx.reduce_motion() || total < 1. {
            self.land_at_end(cx);
            return;
        }
        self.glide += 1;
        let mine = self.glide;
        cx.spawn(async move |this, cx| {
            let (start, mut gone) = (std::time::Instant::now(), 0f32);
            loop {
                cx.background_executor()
                    .timer(std::time::Duration::from_millis(8))
                    .await;
                let t =
                    (start.elapsed().as_secs_f32() / crate::motion::NORMAL.as_secs_f32()).min(1.);
                let want = total * crate::motion::ease_out(t);
                // Something else has moved the chat since (a message
                // revealed, another glide): leave it where that put it.
                let still = this.update(cx, |this, cx| {
                    let still = this.glide == mine;
                    if still {
                        list.scroll_by(px(want - gone));
                        cx.notify();
                    }
                    still
                });
                if !still.unwrap_or(false) {
                    return;
                }
                gone = want;
                if t >= 1. {
                    break;
                }
            }
            this.update(cx, |this, cx| {
                if this.glide == mine {
                    this.land_at_end(cx)
                }
            })
            .ok();
        })
        .detach();
    }

    /// land_at_end puts the chat at its end, following what comes.
    fn land_at_end(&mut self, cx: &mut Context<Self>) {
        self.list.set_follow_mode(FollowMode::Tail);
        self.list.scroll_to_end();
        self.at_end = true;
        cx.notify();
    }

    /// apply folds one chat event in. Events the page already holds (seq at
    /// or below its own) are skipped.
    pub fn apply(&mut self, ev: ChatEvent, cx: &mut Context<Self>) {
        if !self.loaded || ev.seq <= self.seq {
            return;
        }
        self.seq = ev.seq;
        if ev.cleared {
            self.items.clear();
            self.rows.clear();
            self.list.reset(0);
        }
        if let Some(s) = ev.session {
            let was_busy = self.session.turn_started_at.is_some();
            self.session = s;
            if was_busy && self.session.turn_started_at.is_none() {
                // A turn ended: it may have a checkpoint now, and the cache's
                // clock starts again.
                self.load_checkpoints(cx);
                self.composer.load_cache(&self.client, &self.prefix, cx);
            }
        }
        // A window around a found item takes no new items: Jump to latest
        // reads them.
        if self.newer && (ev.item.is_some() || ev.append.is_some()) {
            if let Some(item) = &ev.item
                && let Some(ix) = self.items.iter().position(|i| i.id == item.id)
            {
                self.items[ix] = item.clone();
            }
            self.rebuild(cx);
            cx.notify();
            return;
        }
        if let Some(item) = ev.item {
            // A reply that starts streaming now is revealed from nothing.
            if item.streaming {
                self.shown.entry(item.id.clone()).or_default();
            }
            match self.items.iter().position(|i| i.id == item.id) {
                Some(ix) => self.items[ix] = item,
                None => self.items.push(item),
            }
        }
        self.shown
            .retain(|id, _| self.items.iter().any(|i| &i.id == id && i.streaming));
        if let Some(app) = ev.append
            && let Some(ix) = self.items.iter().rposition(|i| i.id == app.id)
        {
            self.items[ix].text.push_str(&app.text);
            if bench::on() && self.arrived.is_none() {
                self.arrived = Some(Instant::now());
            }
        }
        self.rebuild(cx);
        cx.notify();
    }

    /// scroll_bench scrolls the list by `step` every frame for `frames`
    /// frames, sampling the time between frames, then reports.
    pub fn scroll_bench(
        &mut self,
        step: f32,
        frames: usize,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        fn tick(
            view: Entity<ChatView>,
            step: f32,
            left: usize,
            last: Instant,
            window: &mut Window,
            cx: &mut App,
        ) {
            let now = Instant::now();
            bench::sample("scroll-frame-interval", now - last);
            if left == 0 {
                bench::report();
                return;
            }
            view.update(cx, |this, cx| {
                this.list.scroll_by(px(step));
                cx.notify();
            });
            let v = view.clone();
            window.on_next_frame(move |window, cx| tick(v, step, left - 1, now, window, cx));
        }
        let view = cx.entity();
        self.list.set_follow_mode(FollowMode::Normal);
        let start = Instant::now();
        window.on_next_frame(move |window, cx| tick(view, step, frames, start, window, cx));
        cx.notify();
    }

    fn toggle(&mut self, id: &str, cx: &mut Context<Self>) {
        if !self.expanded.remove(id) {
            self.expanded.insert(id.to_string());
        }
        if let Some(ix) = self.rows.iter().position(|r| {
            r.key == id
                || match &r.row {
                    rows::Row::Work { items, .. } => items.iter().any(|&i| self.items[i].id == id),
                    rows::Row::Subagent { children, .. } => {
                        children.iter().any(|&i| self.items[i].id == id)
                    }
                    rows::Row::Note(i) => self.items[*i].id == id.trim_start_matches("handoff:"),
                    _ => false,
                }
        }) {
            self.list.remeasure_items(ix..ix + 1);
        }
        cx.notify();
    }

    fn toggle_turn(&mut self, turn: &str, cx: &mut Context<Self>) {
        if !self.open_turns.remove(turn) {
            self.open_turns.insert(turn.to_string());
        }
        self.rebuild(cx);
        cx.notify();
    }

    /// answer picks one of a permission request's options; `path` is the
    /// chat it waits in (an agent's, for the lead's card of it).
    fn answer(&mut self, path: String, item: String, option: String, cx: &mut Context<Self>) {
        let client = self.client.clone();
        self.answering.insert(item.clone());
        cx.notify();
        cx.spawn(async move |this, cx| {
            let res = client
                .post::<_, serde_json::Value>(
                    &path,
                    &agentbox_api::ChatAnswerRequest { option_id: option },
                )
                .await;
            this.update(cx, |this, cx| {
                this.answering.remove(&item);
                if let Err(e) = res {
                    crate::toasts::error(cx, e.to_string());
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    pub fn open_zoom(
        &mut self,
        image: Arc<Image>,
        name: String,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let focus = cx.focus_handle();
        focus.focus(window, cx);
        self.zoom = Some(Zoom {
            image,
            name,
            scale: 1.,
            pan: Point::default(),
            drag: None,
            focus,
        });
        cx.notify();
    }

    /// render_hero is an empty lead chat's welcome, as ChatTab's Hero (and
    /// HomeHero): the tool's mark in a box, a title and a line under it.
    fn render_hero(&self, window: &Window) -> Option<AnyElement> {
        if !self.loaded || !self.items.is_empty() {
            return None;
        }
        let (project, name) = self.agent.split_once('/')?;
        let lead = name == "lead" || name.starts_with("lead.");
        if !lead {
            return None;
        }
        let (title, line) = if project == agentbox_api::HOME_PROJECT {
            (t("chat.hero.homeTitle"), t("chat.hero.home"))
        } else {
            (
                tv("chat.hero.title", &[("project", project.into())]),
                tv(
                    "chat.hero.project",
                    &[
                        ("project", project.into()),
                        ("branch", t("chat.hero.itsBranch").into()),
                    ],
                ),
            )
        };
        let tool = if self.session.tool.is_empty() {
            "claude"
        } else {
            self.session.tool.as_str()
        };
        Some(
            div()
                .absolute()
                .top_0()
                .left_0()
                .right_0()
                .flex()
                .flex_col()
                .items_center()
                .justify_center()
                .mt_6()
                .min_h(window.viewport_size().height * 0.42)
                .pt_6()
                .child(
                    div()
                        .mb_5()
                        .size_12()
                        .flex()
                        .items_center()
                        .justify_center()
                        .rounded_2xl()
                        .border_1()
                        .border_color(theme::line_strong())
                        .bg(theme::overlay())
                        .child(crate::icons::ai_icon(tool, px(20.), theme::brand(300))),
                )
                .child(
                    div()
                        .text_size(theme::text::DISPLAY)
                        .text_color(theme::primary())
                        .child(title),
                )
                .child(
                    div()
                        .mt_2()
                        .max_w(theme::balanced_width(window, &line, px(14.), px(448.)))
                        .text_center()
                        .text_size(theme::text::BODY)
                        .line_height(relative(1.625))
                        .text_color(theme::subtle())
                        .child(line),
                )
                .into_any_element(),
        )
    }

    fn render_header(&self, working: bool) -> AnyElement {
        h_flex()
            .h_12()
            .flex_none()
            .px_6()
            .gap_3()
            .border_b_1()
            .border_color(theme::line())
            .child(
                div()
                    .text_color(theme::primary())
                    .font_weight(FontWeight::SEMIBOLD)
                    .child(self.title.clone()),
            )
            .child(
                div()
                    .text_xs()
                    .text_color(if working {
                        theme::starting()
                    } else {
                        theme::faint()
                    })
                    .child(session_line(&self.session)),
            )
            .child(div().flex_1())
            .into_any_element()
    }

    fn render_jump(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        if (self.at_end && !self.newer) || !self.loaded {
            return None;
        }
        Some(
            div()
                .absolute()
                .bottom_3()
                .left_0()
                .right_0()
                .flex()
                .justify_center()
                // ChatTab's pill: opaque, so the messages under it don't
                // read through its words, and it fades in.
                .child(crate::motion::enter(
                    h_flex()
                        .id("chat-jump-latest")
                        .gap_1p5()
                        .px_3()
                        .py_1p5()
                        .rounded_full()
                        .border_1()
                        .border_color(theme::line_strong())
                        .bg(theme::solid(theme::overlay()))
                        .shadow(theme::shadow_pop())
                        .cursor_pointer()
                        .text_size(theme::text::CAPTION)
                        .text_color(theme::tertiary())
                        .hover(|el| el.text_color(theme::title()))
                        .active(|s| {
                            s.bg(theme::layered(&[theme::overlay(), theme::surface_strong()]))
                        })
                        .child(crate::icons::ic("arrow-down").size(px(14.)))
                        .child(t(if self.newer {
                            "chat.tab.jumpToLatest"
                        } else {
                            "chat.tab.scrollToEnd"
                        }))
                        .on_click(
                            cx.listener(|this, _: &ClickEvent, _, cx| this.jump_to_latest(cx)),
                        ),
                    "chat-jump-in",
                    0,
                ))
                .into_any_element(),
        )
    }
}

impl Render for ChatView {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        if std::mem::take(&mut self.focus_next) {
            cx.defer_in(window, |this, window, cx| {
                this.composer.input.update(cx, |s, cx| s.focus(window, cx))
            });
        }
        if !self.append.is_empty() {
            let mut text = self.draft_text(cx);
            for more in std::mem::take(&mut self.append) {
                if !text.trim().is_empty() {
                    text.push_str("\n\n");
                }
                text.push_str(&more);
            }
            self.composer
                .input
                .update(cx, |s, cx| s.set_value(text, window, cx));
        }
        self.want_evidence(cx);
        let viewport = self.list.viewport_bounds();
        crate::webview::set_scroller(self.list.clone());
        crate::webview::set_clip((viewport.size.height > px(0.)).then_some(viewport));
        if bench::on() {
            if let Some(at) = self.arrived.take() {
                window.on_next_frame(move |_, _| {
                    bench::sample("chunk-arrival-to-frame", at.elapsed())
                });
            }
            if self.session.state == "running" {
                let now = Instant::now();
                if let Some(last) = self.last_frame {
                    bench::sample("stream-render-interval", now - last);
                }
                self.last_frame = Some(now);
            } else if self.last_frame.take().is_some() {
                bench::report();
            }
        }
        let view = cx.entity();
        let working = self.working();
        let zoom = self.render_zoom(window, cx);
        let gutter = crate::app::controls::list_overflows(&self.list);
        v_flex()
            .id("chat")
            .key_context("Chat")
            .track_focus(&self.focus)
            .relative()
            .size_full()
            .bg(theme::chat())
            // Ctrl+F finds in this chat; Escape closes the find bar.
            .capture_key_down(cx.listener(|this, ev: &KeyDownEvent, window, cx| {
                let k = &ev.keystroke;
                if k.modifiers.control && !k.modifiers.shift && k.key == "f" {
                    this.open_find(window, cx);
                    cx.stop_propagation();
                }
                // Mod+. and Mod+Shift+. move a project chat's To: on.
                if k.modifiers.secondary(cx) && (k.key == "." || k.key == ">") {
                    let back = k.modifiers.shift || k.key == ">";
                    this.cycle_target(if back { -1 } else { 1 }, window, cx);
                    cx.stop_propagation();
                }
            }))
            .on_drop(cx.listener(|this, paths: &ExternalPaths, _, cx| {
                this.composer.drop_paths(paths.paths(), &this.session, cx);
                cx.notify();
            }))
            .when(self.header, |el| el.child(self.render_header(working)))
            .children(self.render_find(cx))
            .child(
                div()
                    .relative()
                    .flex_1()
                    .min_h_0()
                    .child(crate::webview::begin())
                    // ChatTab's chat-scroller: Chromium's thin scrollbar takes
                    // its gutter off the right while the chat overflows.
                    .child(
                        div()
                            .size_full()
                            .when(gutter, |el| el.pr(crate::app::controls::SCROLLBAR))
                            // ChatTab's content pads its bottom by the composer's
                            // height + 28, its offsetHeight, a whole pixel: the
                            // lead's review banner is 36.5px tall there (36 here),
                            // which rounds the room up.
                            .when(self.review_count > 0 && self.is_lead(), |el| el.pb(px(1.)))
                            .child(
                                list(self.list.clone(), move |ix, window, cx| {
                                    view.read(cx).render_row(ix, view.clone(), window)
                                })
                                .size_full(),
                            ),
                    )
                    .child(crate::app::controls::list_scrollbar(&self.list, gutter))
                    .child(crate::webview::end())
                    .when(self.loading_older, |el| {
                        el.child(
                            div()
                                .absolute()
                                .top_2()
                                .left_0()
                                .right_0()
                                .flex()
                                .justify_center()
                                .child(
                                    div()
                                        .px_2()
                                        .rounded_md()
                                        .bg(theme::surface_raised())
                                        .text_xs()
                                        .text_color(theme::muted())
                                        .child(t("chat.tab.loadingOlder")),
                                ),
                        )
                    })
                    .when(!self.loaded && self.error.is_none(), |el| {
                        el.child(
                            div()
                                .absolute()
                                .inset_0()
                                .bg(theme::chat())
                                .child(crate::motion::skeleton_chat("chat-loading")),
                        )
                    })
                    .children(self.render_hero(window))
                    .children(self.render_jump(cx)),
            )
            .when_some(self.error.clone(), |el, e| {
                el.child(
                    div()
                        .px_6()
                        .py_1()
                        .text_xs()
                        .text_color(theme::error())
                        .child(e),
                )
            })
            .child(
                div()
                    .relative()
                    .child(self.render_composer(window, cx))
                    .child(crate::spot::spot("composer")),
            )
            .children(zoom)
    }
}
