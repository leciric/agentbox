//! The tray icon, a Mac's menu bar icon (desktop/src/main/tray.ts,
//! traymodel.ts, trayicon.ts): how many agents are working and how many
//! things wait for the user, a menu of them by project, and the app's quick
//! actions. It holds no AgentBox logic: status() works out what to show from
//! the reads the window makes, and app/alerts.rs reads them and does what an
//! entry asks.
use std::collections::HashMap;

use agentbox_api::{Agent, Notification, NotificationPage, Project, ReviewPage};
use agentbox_i18n::{t, tv};

/// Tone is the icon's look: grey when AgentBox can't be reached.
#[derive(Clone, Copy, PartialEq, Eq, Debug, Hash)]
pub enum Tone {
    Idle,
    Working,
    Attention,
    Offline,
}

/// Target is where an entry goes: an agent's page, a question (where it's
/// answered), a project's page, or a pull request in the browser.
#[derive(Clone, Debug, PartialEq)]
pub enum Target {
    Agent(String),
    Question(Box<Notification>),
    Project(String),
    Url(String),
}

#[derive(Clone, Copy, PartialEq, Eq, Debug, Hash)]
pub enum ItemKind {
    Question,
    Permission,
    Review,
    Working,
}

#[derive(Clone, Debug, PartialEq)]
pub struct Item {
    pub kind: ItemKind,
    pub label: String,
    pub target: Target,
}

#[derive(Clone, Debug, PartialEq)]
pub struct TrayProject {
    pub name: String,
    pub label: String,
    pub items: Vec<Item>,
    /// Items left out, for "N more".
    pub more: usize,
}

#[derive(Clone, Debug, PartialEq)]
pub struct Status {
    pub connected: bool,
    pub working: usize,
    pub waiting: usize,
    /// Agents whose machine runs, which Pause all would pause.
    pub running: usize,
    pub tone: Tone,
    pub projects: Vec<TrayProject>,
}

pub const OFFLINE: Status = Status {
    connected: false,
    working: 0,
    waiting: 0,
    running: 0,
    tone: Tone::Offline,
    projects: Vec::new(),
};

/// PER_PROJECT is the most items a project lists before "N more".
pub const PER_PROJECT: usize = 6;
const LABEL_LENGTH: usize = 60;

/// Data is what the tray reads: GET /v1/agents, /v1/projects, the unseen
/// questions (GET /v1/notifications?unseen=1) and Ready to review.
pub struct Data {
    pub agents: Vec<Agent>,
    pub projects: Vec<Project>,
    pub questions: NotificationPage,
    pub review: Option<ReviewPage>,
}

/// one_line keeps a label to a menu's single line.
pub fn one_line(text: &str) -> String {
    let flat = text.split_whitespace().collect::<Vec<_>>().join(" ");
    if flat.chars().count() <= LABEL_LENGTH {
        return flat;
    }
    let cut: String = flat.chars().take(LABEL_LENGTH - 1).collect();
    format!("{}…", cut.trim_end())
}

fn agent_label(a: &Agent) -> &str {
    if a.title.is_empty() {
        &a.name
    } else {
        &a.title
    }
}

/// status is trayStatus: working is an agent mid-turn (chat "running", not
/// stalled); waiting on the user is a question passed to them, an AI tool
/// asking for a permission (chat "waiting") and an agent ready to review.
pub fn status(data: Option<&Data>) -> Status {
    let Some(data) = data else {
        return OFFLINE;
    };
    let by_ref: HashMap<&str, &Agent> = data.agents.iter().map(|a| (a.r#ref.as_str(), a)).collect();
    let mut items: Vec<(String, Vec<Item>)> = Vec::new();
    let mut add = |project: &str, item: Item| match items.iter_mut().find(|(p, _)| p == project) {
        Some((_, list)) => list.push(item),
        None => items.push((project.to_string(), vec![item])),
    };
    for n in &data.questions.items {
        let who = if !n.title.is_empty() {
            n.title.as_str()
        } else {
            by_ref
                .get(n.r#ref.as_str())
                .map(|a| a.title.as_str())
                .filter(|t| !t.is_empty())
                .unwrap_or(&n.agent)
        };
        let label = if n.text.is_empty() {
            who.to_string()
        } else {
            format!("{who}: {}", n.text)
        };
        add(
            &n.project,
            Item {
                kind: ItemKind::Question,
                label: one_line(&label),
                target: Target::Question(Box::new(n.clone())),
            },
        );
    }
    let permissions: Vec<&Agent> = data.agents.iter().filter(|a| a.chat == "waiting").collect();
    for a in &permissions {
        let label = tv("main.tray.permission", &[("agent", agent_label(a).into())]);
        add(
            &a.project,
            Item {
                kind: ItemKind::Permission,
                label: one_line(&label),
                target: Target::Agent(a.r#ref.clone()),
            },
        );
    }
    let review = data.review.as_ref();
    for r in review.map(|r| r.items.as_slice()).unwrap_or_default() {
        let name = if r.title.is_empty() {
            &r.agent
        } else {
            &r.title
        };
        let (label, target) = match &r.pr {
            Some(pr) => (
                tv(
                    "main.tray.reviewPR",
                    &[
                        ("agent", name.as_str().into()),
                        ("number", pr.number.to_string().into()),
                    ],
                ),
                if pr.url.is_empty() {
                    Target::Agent(r.r#ref.clone())
                } else {
                    Target::Url(pr.url.clone())
                },
            ),
            None => (name.clone(), Target::Agent(r.r#ref.clone())),
        };
        add(
            &r.project,
            Item {
                kind: ItemKind::Review,
                label: one_line(&label),
                target,
            },
        );
    }
    let working: Vec<&Agent> = data
        .agents
        .iter()
        .filter(|a| a.chat == "running" && a.stalled_since.is_none())
        .collect();
    for a in &working {
        add(
            &a.project,
            Item {
                kind: ItemKind::Working,
                label: one_line(agent_label(a)),
                target: Target::Agent(a.r#ref.clone()),
            },
        );
    }
    // In the sidebar's order, with any project the list didn't have after.
    let position = |name: &str| {
        data.projects
            .iter()
            .position(|p| p.name == name)
            .unwrap_or(usize::MAX)
    };
    items.sort_by_key(|(name, _)| position(name));
    let projects = items
        .into_iter()
        .map(|(name, list)| {
            let label = data
                .projects
                .iter()
                .find(|p| p.name == name)
                .map(|p| p.display_name.clone())
                .filter(|l| !l.is_empty())
                .unwrap_or_else(|| name.clone());
            TrayProject {
                more: list.len().saturating_sub(PER_PROJECT),
                items: list.into_iter().take(PER_PROJECT).collect(),
                name,
                label,
            }
        })
        .collect();
    let questions = (data.questions.total.max(0) as usize).max(data.questions.items.len());
    let ready = review.map_or(0, |r| (r.total.max(0) as usize).max(r.items.len()));
    let waiting = questions + permissions.len() + ready;
    Status {
        connected: true,
        working: working.len(),
        waiting,
        running: data.agents.iter().filter(|a| a.state == "running").count(),
        tone: if waiting > 0 {
            Tone::Attention
        } else if !working.is_empty() {
            Tone::Working
        } else {
            Tone::Idle
        },
        projects,
    }
}

/// summary is the menu's first line and the icon's tooltip.
pub fn summary(s: &Status) -> String {
    if !s.connected {
        return t("main.tray.offline");
    }
    if s.working == 0 && s.waiting == 0 {
        return t("main.tray.quiet");
    }
    let mut parts = Vec::new();
    if s.working > 0 {
        parts.push(tv("main.tray.working", &[("count", s.working.into())]));
    }
    if s.waiting > 0 {
        parts.push(tv("main.tray.waiting", &[("count", s.waiting.into())]));
    }
    parts.join(" · ")
}

/// mac_title is the text beside a Mac's menu bar icon, which has no room for
/// badges: what waits for the user, then what's working.
pub fn mac_title(s: &Status) -> String {
    let mut parts = Vec::new();
    if s.waiting > 0 {
        parts.push(format!("●{}", s.waiting));
    }
    if s.working > 0 {
        parts.push(format!("▶{}", s.working));
    }
    parts.join(" ")
}

#[derive(Clone, Debug, PartialEq)]
pub enum Action {
    Open,
    Go(Target),
    PauseAll,
    KeepRunning,
    Quit,
}

#[derive(Clone, Debug, PartialEq)]
pub enum Entry {
    Separator,
    /// A line: its label, whether it can be clicked, its dot, what it does.
    Item {
        label: String,
        enabled: bool,
        icon: Option<ItemKind>,
        action: Option<Action>,
    },
    Check {
        label: String,
        checked: bool,
        action: Action,
    },
}

fn line(label: String, action: Option<Action>) -> Entry {
    Entry::Item {
        enabled: action.is_some(),
        label,
        icon: None,
        action,
    }
}

/// menu is trayMenu, top to bottom: the summary, what needs the user and
/// what's working by project, then the app's actions. Electron's "Shut
/// AgentBox's VM down" is the command line's to do (vmpower.ts) and isn't here.
pub fn menu(s: &Status, keep_running: bool) -> Vec<Entry> {
    let mut out = vec![line(summary(s), None)];
    for p in &s.projects {
        out.push(Entry::Separator);
        out.push(line(p.label.clone(), None));
        for item in &p.items {
            out.push(Entry::Item {
                label: item.label.clone(),
                enabled: true,
                icon: Some(item.kind),
                action: Some(Action::Go(item.target.clone())),
            });
        }
        if p.more > 0 {
            out.push(line(
                tv("main.tray.more", &[("count", p.more.into())]),
                Some(Action::Go(Target::Project(p.name.clone()))),
            ));
        }
    }
    out.push(Entry::Separator);
    out.push(line(t("main.tray.open"), Some(Action::Open)));
    out.push(Entry::Item {
        label: t("main.tray.pauseAll"),
        enabled: s.running > 0,
        icon: None,
        action: Some(Action::PauseAll),
    });
    out.push(Entry::Separator);
    out.push(Entry::Check {
        label: t("main.tray.keepRunning"),
        checked: keep_running,
        action: Action::KeepRunning,
    });
    out.push(line(t("main.tray.quit"), Some(Action::Quit)));
    out
}

// The icon's pixels (trayicon.ts), drawn rather than shipped: its badges
// carry numbers. The app icon cut down to what reads at 16–32 pixels, a
// rounded square with the box glyph, and up to two badges: what waits for
// the user (orange, top right) and the agents working (green, bottom right).
// A Mac's menu bar takes a template, the glyph alone in black.

type Rgba = [f32; 4];
const WHITE: Rgba = [255., 255., 255., 255.];
const BLACK: Rgba = [0., 0., 0., 255.];
const CLEAR: Rgba = [0., 0., 0., 0.];
const ORANGE: Rgba = [249., 115., 22., 255.];
const GREEN: Rgba = [34., 197., 94., 255.];
const VIOLET: Rgba = [167., 139., 250., 255.];
const INDIGO: Rgba = [79., 70., 229., 255.];
const GREY_LIGHT: Rgba = [156., 163., 175., 255.];
const GREY_DARK: Rgba = [75., 85., 99., 255.];

/// GLYPH is lucide's "package" in its 24-unit grid.
const GLYPH: [&[(f32, f32)]; 3] = [
    &[
        (12., 2.),
        (21., 7.),
        (21., 17.),
        (12., 22.),
        (3., 17.),
        (3., 7.),
        (12., 2.),
    ],
    &[(3.3, 7.), (12., 12.), (20.7, 7.)],
    &[(12., 22.), (12., 12.)],
];

/// FONT is a 3×5 font for the badges' numbers.
fn font(c: char) -> &'static str {
    match c {
        '0' => "111101101101111",
        '1' => "010110010010111",
        '2' => "111001111100111",
        '3' => "111001111001111",
        '4' => "101101111001001",
        '5' => "111100111001111",
        '6' => "111100111101111",
        '7' => "111001001001001",
        '8' => "111101111101111",
        '9' => "111101111001111",
        '+' => "000010111010000",
        _ => "000000000000000",
    }
}

/// badge_text is what a badge says: the number, or 9+ past nine.
pub fn badge_text(n: usize) -> String {
    if n > 9 { "9+".into() } else { n.to_string() }
}

struct Badge {
    x0: f32,
    y0: f32,
    x1: f32,
    y1: f32,
    colour: Rgba,
    text: Vec<char>,
    unit: f32,
    text_x: f32,
    text_y: f32,
}

fn badge(size: f32, n: usize, colour: Rgba, top: bool) -> Badge {
    let text: Vec<char> = badge_text(n).chars().collect();
    let h = size * 0.5;
    let unit = h / 7.;
    let text_w = (text.len() as f32 * 4. - 1.) * unit;
    let w = h.max(text_w + unit * 2.5);
    let x0 = size - w;
    let y0 = if top { 0. } else { size - h };
    Badge {
        x0,
        y0,
        x1: size,
        y1: y0 + h,
        colour,
        text,
        unit,
        text_x: x0 + (w - text_w) / 2.,
        text_y: y0 + unit,
    }
}

fn in_pill(x0: f32, y0: f32, x1: f32, y1: f32, x: f32, y: f32, grow: f32) -> bool {
    let r = (y1 - y0) / 2. + grow;
    let cy = (y0 + y1) / 2.;
    let lo = x0 + r - grow;
    let hi = (x1 - r + grow).max(lo);
    let cx = x.clamp(lo, hi);
    (x - cx).powi(2) + (y - cy).powi(2) <= r * r
}

fn in_text(b: &Badge, x: f32, y: f32) -> bool {
    let col = ((x - b.text_x) / b.unit).floor();
    let row = ((y - b.text_y) / b.unit).floor();
    if !(0. ..=4.).contains(&row) || col < 0. {
        return false;
    }
    let (col, row) = (col as usize, row as usize);
    let c = col % 4;
    b.text
        .get(col / 4)
        .is_some_and(|ch| c < 3 && font(*ch).as_bytes()[row * 3 + c] == b'1')
}

fn in_rounded_square(size: f32, x: f32, y: f32) -> bool {
    let inset = size / 32.;
    let r = size * 0.24;
    let (lo, hi) = (inset + r, size - inset - r);
    let (cx, cy) = (x.clamp(lo, hi), y.clamp(lo, hi));
    (x - cx).powi(2) + (y - cy).powi(2) <= r * r
}

fn dist_to_segment(px: f32, py: f32, (ax, ay): (f32, f32), (bx, by): (f32, f32)) -> f32 {
    let (dx, dy) = (bx - ax, by - ay);
    let t = (((px - ax) * dx + (py - ay) * dy) / (dx * dx + dy * dy)).clamp(0., 1.);
    (px - ax - t * dx).hypot(py - ay - t * dy)
}

fn on_glyph(gx: f32, gy: f32, half: f32) -> bool {
    GLYPH.iter().any(|line| {
        line.windows(2)
            .any(|w| dist_to_segment(gx, gy, w[0], w[1]) <= half)
    })
}

fn mix(a: Rgba, b: Rgba, t: f32) -> Rgba {
    [
        a[0] + (b[0] - a[0]) * t,
        a[1] + (b[1] - a[1]) * t,
        a[2] + (b[2] - a[2]) * t,
        255.,
    ]
}

/// icon draws one, as RGBA pixels, size on a side; each pixel sampled 4×4
/// times with the shapes painted over each other, for smooth edges.
pub fn icon(size: u32, tone: Tone, waiting: usize, working: usize, template: bool) -> Vec<u8> {
    let s = size as f32;
    let mut badges = Vec::new();
    if !template {
        if waiting > 0 {
            badges.push(badge(s, waiting, ORANGE, true));
        }
        if working > 0 {
            badges.push(badge(s, working, GREEN, false));
        }
    }
    let span = if template { s * 0.92 } else { s * 0.66 };
    let origin = (s - span) / 2.;
    let half = if template { 1.1 } else { 1.15 };
    let (from, to) = if tone == Tone::Offline {
        (GREY_LIGHT, GREY_DARK)
    } else {
        (VIOLET, INDIGO)
    };
    let gap = s / 16.;
    const SAMPLES: usize = 4;
    let mut out = vec![0u8; (size * size * 4) as usize];
    for py in 0..size {
        for px in 0..size {
            let mut acc = [0f32; 4];
            for sy in 0..SAMPLES {
                for sx in 0..SAMPLES {
                    let x = px as f32 + (sx as f32 + 0.5) / SAMPLES as f32;
                    let y = py as f32 + (sy as f32 + 0.5) / SAMPLES as f32;
                    let gx = (x - origin) / span * 24.;
                    let gy = (y - origin) / span * 24.;
                    let mut c = CLEAR;
                    if template {
                        if on_glyph(gx, gy, half) {
                            c = BLACK;
                        }
                        // "Something needs you": a dot cut out of the glyph's top right.
                        if tone == Tone::Attention {
                            let (x0, y0, x1, y1) = (s * 0.62, 0., s, s * 0.38);
                            if in_pill(x0, y0, x1, y1, x, y, gap * 1.5) {
                                c = CLEAR;
                            }
                            if in_pill(x0, y0, x1, y1, x, y, 0.) {
                                c = BLACK;
                            }
                        }
                    } else {
                        if in_rounded_square(s, x, y) {
                            c = mix(from, to, ((x + y) / (2. * s)).min(1.));
                        }
                        if c[3] > 0. && on_glyph(gx, gy, half) {
                            c = WHITE;
                        }
                        for b in &badges {
                            if in_pill(b.x0, b.y0, b.x1, b.y1, x, y, gap) {
                                c = CLEAR;
                            }
                            if in_pill(b.x0, b.y0, b.x1, b.y1, x, y, 0.) {
                                c = if in_text(b, x, y) { WHITE } else { b.colour };
                            }
                        }
                    }
                    // Premultiplied, so a clear sample adds nothing to the colour.
                    for i in 0..3 {
                        acc[i] += c[i] * c[3];
                    }
                    acc[3] += c[3];
                }
            }
            let i = ((py * size + px) * 4) as usize;
            let a = acc[3];
            for k in 0..3 {
                out[i + k] = if a > 0. {
                    (acc[k] / a).round() as u8
                } else {
                    0
                };
            }
            out[i + 3] = (a / (SAMPLES * SAMPLES) as f32).round() as u8;
        }
    }
    out
}

/// dot is a menu entry's mark: orange for what waits on the user, violet
/// for review, green for working, the badges' colours.
pub fn dot(kind: ItemKind) -> Vec<u8> {
    let colour: [u8; 3] = match kind {
        ItemKind::Working => [34, 197, 94],
        ItemKind::Review => [139, 92, 246],
        _ => [249, 115, 22],
    };
    let size = 16;
    let mut px = vec![0u8; size * size * 4];
    for y in 0..size {
        for x in 0..size {
            let d = (x as f32 + 0.5 - size as f32 / 2.).hypot(y as f32 + 0.5 - size as f32 / 2.);
            let alpha = (4.5 - d).clamp(0., 1.);
            let i = (y * size + x) * 4;
            px[i..i + 3].copy_from_slice(&colour);
            px[i + 3] = (alpha * 255.).round() as u8;
        }
    }
    px
}

/// SIZE is the icon's side: a Mac's menu bar icons are 18pt, Windows' tray
/// takes 16px at 100%, a Linux tray is commonly 22–24px. Each gets its 2×.
pub const SIZE: u32 = if cfg!(target_os = "macos") {
    18
} else if cfg!(windows) {
    16
} else {
    24
};

/// Tray is the icon on screen, and what its menu's entries do by id.
pub struct Tray {
    icon: tray_icon::TrayIcon,
    actions: HashMap<String, Action>,
    drawn: Option<(Tone, String, String)>,
    /// The menu on screen: made again only when it changes, since a menu
    /// replaced while it's open takes the click with it.
    entries: Vec<Entry>,
}

impl Tray {
    /// new puts the icon in the tray; None where there's none to be had
    /// (Linux without libayatana-appindicator3, which it loads at run time
    /// and panics without).
    pub fn new() -> Option<Self> {
        let built = std::panic::catch_unwind(|| {
            tray_icon::TrayIconBuilder::new()
                .with_id("agentbox")
                .with_icon_as_template(cfg!(target_os = "macos"))
                // Windows opens the window on a left click (alerts.rs), as a
                // Mac's dock icon does; elsewhere any click opens the menu.
                .with_menu_on_left_click(!cfg!(windows))
                .build()
        });
        match built {
            Ok(Ok(icon)) => Some(Self {
                icon,
                actions: HashMap::new(),
                drawn: None,
                entries: Vec::new(),
            }),
            Ok(Err(e)) => {
                eprintln!("tray: {e}");
                None
            }
            Err(_) => {
                eprintln!("tray: no tray icon (libayatana-appindicator3 missing?)");
                None
            }
        }
    }

    /// draw shows a status: the icon, its tooltip and the menu.
    pub fn draw(&mut self, s: &Status, keep_running: bool) {
        let template = cfg!(target_os = "macos");
        let key = (s.tone, badge_text(s.waiting), badge_text(s.working));
        if self.drawn.as_ref() != Some(&key) {
            // Drawn at 2× for sharpness: the tray scales it to fit.
            let size = SIZE * 2;
            let px = icon(size, s.tone, s.waiting, s.working, template);
            if let Ok(i) = tray_icon::Icon::from_rgba(px, size, size) {
                // set_icon_with_as_template sets nothing but on a Mac.
                let _ = if template {
                    self.icon.set_icon_with_as_template(Some(i), true)
                } else {
                    self.icon.set_icon(Some(i))
                };
            }
            self.drawn = Some(key);
        }
        let _ = self
            .icon
            .set_tooltip(Some(format!("AgentBox: {}", summary(s))));
        if cfg!(target_os = "macos") {
            self.icon.set_title(Some(mac_title(s)));
        }
        let entries = menu(s, keep_running);
        if entries == self.entries {
            return;
        }
        self.entries = entries.clone();
        self.actions.clear();
        let m = tray_icon::menu::Menu::new();
        for (ix, entry) in entries.into_iter().enumerate() {
            let id = format!("tray-{ix}");
            let _ = match entry {
                Entry::Separator => m.append(&tray_icon::menu::PredefinedMenuItem::separator()),
                Entry::Item {
                    label,
                    enabled,
                    icon,
                    action,
                } => {
                    let label = mnemonic_safe(&label);
                    if let Some(a) = action {
                        self.actions.insert(id.clone(), a);
                    }
                    match icon.and_then(|k| tray_icon::menu::Icon::from_rgba(dot(k), 16, 16).ok()) {
                        Some(i) => m.append(&tray_icon::menu::IconMenuItem::with_id(
                            id,
                            label,
                            enabled,
                            Some(i),
                            None,
                        )),
                        None => m.append(&tray_icon::menu::MenuItem::with_id(
                            id, label, enabled, None,
                        )),
                    }
                }
                Entry::Check {
                    label,
                    checked,
                    action,
                } => {
                    self.actions.insert(id.clone(), action);
                    m.append(&tray_icon::menu::CheckMenuItem::with_id(
                        id,
                        mnemonic_safe(&label),
                        true,
                        checked,
                        None,
                    ))
                }
            };
        }
        self.icon.set_menu(Some(Box::new(m)));
    }

    /// action is what the menu entry with id does.
    pub fn action(&self, id: &str) -> Option<Action> {
        self.actions.get(id).cloned()
    }
}

/// mnemonic_safe keeps an & in a label: Windows and Linux take one for a
/// mnemonic.
fn mnemonic_safe(label: &str) -> String {
    if cfg!(target_os = "macos") {
        label.to_string()
    } else {
        label.replace('&', "&&")
    }
}

/// events is what happened to the tray since it was last asked: menu
/// entries chosen (by id), and whether the icon itself was clicked (Windows,
/// whose left click opens the window).
pub fn events() -> (Vec<String>, bool) {
    let mut chosen = Vec::new();
    while let Ok(e) = tray_icon::menu::MenuEvent::receiver().try_recv() {
        chosen.push(e.id.0);
    }
    let mut clicked = false;
    while let Ok(e) = tray_icon::TrayIconEvent::receiver().try_recv() {
        if let tray_icon::TrayIconEvent::Click {
            button: tray_icon::MouseButton::Left,
            button_state: tray_icon::MouseButtonState::Up,
            ..
        } = e
        {
            clicked = true;
        }
    }
    (chosen, clicked)
}

#[cfg(test)]
mod tests {
    use super::*;
    use agentbox_api::{PullRequest, ReviewItem};

    fn agent(project: &str, name: &str, state: &str, chat: &str) -> Agent {
        Agent {
            r#ref: format!("{project}/{name}"),
            project: project.into(),
            name: name.into(),
            state: state.into(),
            chat: chat.into(),
            ..Default::default()
        }
    }

    fn data() -> Data {
        Data {
            agents: vec![
                agent("demo", "agent-01", "running", "running"),
                agent("demo", "agent-02", "running", "waiting"),
                Agent {
                    stalled_since: Some("2026-01-01T00:00:00Z".into()),
                    ..agent("web", "agent-03", "running", "running")
                },
            ],
            projects: vec![
                Project {
                    name: "web".into(),
                    display_name: "Website".into(),
                    ..Default::default()
                },
                Project {
                    name: "demo".into(),
                    ..Default::default()
                },
            ],
            questions: NotificationPage {
                items: vec![Notification {
                    id: "n1".into(),
                    kind: "question".into(),
                    project: "demo".into(),
                    agent: "agent-01".into(),
                    r#ref: "demo/agent-01".into(),
                    text: "Which   port?".into(),
                    ..Default::default()
                }],
                total: 1,
                ..Default::default()
            },
            review: Some(ReviewPage {
                items: vec![ReviewItem {
                    project: "web".into(),
                    agent: "agent-04".into(),
                    r#ref: "web/agent-04".into(),
                    pr: Some(PullRequest {
                        number: 7,
                        url: "https://github.com/x/y/pull/7".into(),
                        ..Default::default()
                    }),
                    ..Default::default()
                }],
                total: 3,
                ..Default::default()
            }),
        }
    }

    #[test]
    fn status_counts_and_orders_by_the_sidebar() {
        let s = status(Some(&data()));
        assert_eq!((s.working, s.waiting, s.running), (1, 1 + 1 + 3, 3));
        assert_eq!(s.tone, Tone::Attention);
        let names: Vec<&str> = s.projects.iter().map(|p| p.label.as_str()).collect();
        assert_eq!(names, ["Website", "demo"]);
        let demo = &s.projects[1];
        assert_eq!(demo.items[0].label, "agent-01: Which port?");
        assert_eq!(demo.items[1].kind, ItemKind::Permission);
        assert_eq!(demo.items[2].kind, ItemKind::Working);
        // A stalled agent isn't working; a PR opens in the browser.
        assert_eq!(s.projects[0].items.len(), 1);
        assert_eq!(
            s.projects[0].items[0].target,
            Target::Url("https://github.com/x/y/pull/7".into())
        );
        assert_eq!(status(None), OFFLINE);
    }

    #[test]
    fn the_menu_ends_with_the_apps_actions() {
        let m = menu(&status(Some(&data())), true);
        assert!(matches!(&m[0], Entry::Item { enabled: false, .. }));
        assert!(matches!(
            m.last(),
            Some(Entry::Item {
                action: Some(Action::Quit),
                ..
            })
        ));
        assert!(
            m.iter()
                .any(|e| matches!(e, Entry::Check { checked: true, .. }))
        );
        let quiet = menu(
            &status(Some(&Data {
                agents: vec![],
                projects: vec![],
                questions: Default::default(),
                review: None,
            })),
            false,
        );
        assert!(quiet.iter().any(|e| matches!(
            e,
            Entry::Item {
                action: Some(Action::PauseAll),
                enabled: false,
                ..
            }
        )));
    }

    #[test]
    fn labels_stay_on_one_line() {
        let long = "word ".repeat(30);
        let l = one_line(&long);
        assert_eq!(l.chars().count(), LABEL_LENGTH);
        assert!(l.ends_with('…'));
        assert_eq!(badge_text(12), "9+");
    }

    #[test]
    fn the_icon_has_its_badges() {
        let px = icon(32, Tone::Attention, 3, 2, false);
        let at = |x: usize, y: usize| &px[(y * 32 + x) * 4..(y * 32 + x) * 4 + 4];
        // The square's corner is clear, its middle violet-ish, the badges'
        // edges orange (top right) and green (bottom right).
        assert_eq!(at(0, 0)[3], 0);
        assert!(at(4, 16)[3] == 255 && at(4, 16)[2] > 200);
        let orange = at(17, 8);
        assert!(orange[0] > 200 && orange[2] < 100, "{orange:?}");
        let green = at(17, 24);
        assert!(green[1] > 150 && green[0] < 100, "{green:?}");
        assert_eq!(dot(ItemKind::Review)[(8 * 16 + 8) * 4 + 3], 255);
    }
}
