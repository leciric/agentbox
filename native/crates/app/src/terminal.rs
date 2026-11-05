//! An agent's terminal (its tmux session): the daemon's WebSocket stream
//! (GET /v1/agents/<p>/<a>/terminal) fed to alacritty_terminal's emulator, and
//! its grid drawn as GPUI text, a run per change of colour or style.
use std::cell::RefCell;
use std::rc::Rc;

use agentbox_api::stream::{In, Out};
use agentbox_api::{Client, TerminalResize};
use alacritty_terminal::event::{Event as TermEvent, EventListener};
use alacritty_terminal::grid::{Dimensions, Scroll};
use alacritty_terminal::index::{Column, Line};
use alacritty_terminal::term::cell::Flags;
use alacritty_terminal::term::{Config, Term, TermMode};
use alacritty_terminal::vte::ansi::{Color, NamedColor, Processor, Rgb};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::v_flex;
use tokio::sync::mpsc::UnboundedSender;

use crate::theme;

const FONT: &str = "DejaVu Sans Mono";
const FONT_SIZE: f32 = 13.;
const LINE_HEIGHT: f32 = 17.;

#[derive(Clone, Copy, PartialEq)]
struct Size {
    cols: usize,
    rows: usize,
}

impl Dimensions for Size {
    fn total_lines(&self) -> usize {
        self.rows
    }
    fn screen_lines(&self) -> usize {
        self.rows
    }
    fn columns(&self) -> usize {
        self.cols
    }
}

/// Listener keeps what the emulator asks to send back (answers to queries
/// such as the cursor's position), for the view to write to the stream.
#[derive(Clone, Default)]
struct Listener(Rc<RefCell<Vec<String>>>);

impl EventListener for Listener {
    fn send_event(&self, event: TermEvent) {
        if let TermEvent::PtyWrite(s) = event {
            self.0.borrow_mut().push(s);
        }
    }
}

pub struct TerminalView {
    term: Term<Listener>,
    parser: Processor,
    replies: Listener,
    tx: Option<UnboundedSender<Out>>,
    size: Size,
    /// Where the grid was laid out last frame, to size it to.
    bounds: Rc<RefCell<Option<Bounds<Pixels>>>>,
    cell_width: Option<Pixels>,
    status: Option<SharedString>,
    focus: FocusHandle,
    _task: Option<Task<()>>,
}

impl TerminalView {
    pub fn new(client: Client, path: String, cx: &mut Context<Self>) -> Self {
        let size = Size {
            cols: 120,
            rows: 32,
        };
        let replies = Listener::default();
        let mut this = Self {
            term: Term::new(Config::default(), &size, replies.clone()),
            parser: Processor::new(),
            replies,
            tx: None,
            size,
            bounds: Rc::default(),
            cell_width: None,
            status: Some("Connecting…".into()),
            focus: cx.focus_handle(),
            _task: None,
        };
        this.connect(client, path, cx);
        this
    }

    pub fn focus_handle(&self) -> &FocusHandle {
        &self.focus
    }

    fn connect(&mut self, client: Client, path: String, cx: &mut Context<Self>) {
        let Size { cols, rows } = self.size;
        let url = format!("{path}?cols={cols}&rows={rows}");
        self._task = Some(cx.spawn(async move |this, cx| {
            let mut stream = match client.stream(&url).await {
                Ok(s) => s,
                Err(e) => {
                    this.update(cx, |t, cx| {
                        t.status = Some(e.to_string().into());
                        cx.notify();
                    })
                    .ok();
                    return;
                }
            };
            let tx = stream.tx.clone();
            if this
                .update(cx, |t, cx| {
                    t.tx = Some(tx);
                    t.status = None;
                    t.send_size();
                    cx.notify();
                })
                .is_err()
            {
                return;
            }
            while let Some(msg) = stream.rx.recv().await {
                // Take whatever else has arrived too, and draw once.
                let mut bytes = Vec::new();
                let mut closed = None;
                let mut next = Some(msg);
                while let Some(m) = next.take() {
                    match m {
                        In::Bytes(b) => bytes.extend(b),
                        In::Closed(why) => closed = Some(why),
                    }
                    next = stream.rx.try_recv().ok();
                }
                let ok = this.update(cx, |t, cx| {
                    t.parser.advance(&mut t.term, &bytes);
                    let replies: Vec<String> = t.replies.0.borrow_mut().drain(..).collect();
                    for r in replies {
                        t.write(r.into_bytes());
                    }
                    if let Some(why) = closed {
                        t.status = Some(why.into());
                        t.tx = None;
                    }
                    cx.notify();
                });
                if ok.is_err() {
                    return;
                }
            }
        }));
    }

    fn write(&self, bytes: Vec<u8>) {
        if let Some(tx) = &self.tx {
            let _ = tx.send(Out::Bytes(bytes));
        }
    }

    fn send_size(&self) {
        if let Some(tx) = &self.tx {
            let msg = TerminalResize {
                cols: self.size.cols as u16,
                rows: self.size.rows as u16,
            };
            let _ = tx.send(Out::Text(serde_json::to_string(&msg).unwrap()));
        }
    }

    /// fit sizes the grid to the area it was given last frame.
    fn fit(&mut self, window: &mut Window) {
        let cell = *self.cell_width.get_or_insert_with(|| {
            let font = font(FONT);
            let ts = window.text_system();
            let id = ts.resolve_font(&font);
            ts.advance(id, px(FONT_SIZE), 'm')
                .map(|s| s.width)
                .unwrap_or(px(FONT_SIZE * 0.6))
        });
        let Some(b) = *self.bounds.borrow() else {
            return;
        };
        let cols = ((b.size.width / cell).floor() as usize).max(2);
        let rows = ((b.size.height / px(LINE_HEIGHT)).floor() as usize).max(2);
        let size = Size { cols, rows };
        if size != self.size {
            self.size = size;
            self.term.resize(size);
            self.send_size();
        }
    }

    fn on_key(&mut self, ev: &KeyDownEvent, _: &mut Window, cx: &mut Context<Self>) {
        let k = &ev.keystroke;
        let m = &k.modifiers;
        let app_cursor = self.term.mode().contains(TermMode::APP_CURSOR);
        let arrow = |c: char| {
            if app_cursor {
                format!("\x1bO{c}")
            } else {
                format!("\x1b[{c}")
            }
        };
        if m.control && m.shift && k.key == "v" {
            if let Some(text) = cx.read_from_clipboard().and_then(|c| c.text()) {
                let bracketed = self.term.mode().contains(TermMode::BRACKETED_PASTE);
                let text = if bracketed {
                    format!("\x1b[200~{text}\x1b[201~")
                } else {
                    text
                };
                self.write(text.into_bytes());
            }
            return;
        }
        let seq: Option<String> = match k.key.as_str() {
            "enter" => Some("\r".into()),
            "backspace" => Some("\x7f".into()),
            "tab" if m.shift => Some("\x1b[Z".into()),
            "tab" => Some("\t".into()),
            "escape" => Some("\x1b".into()),
            "up" => Some(arrow('A')),
            "down" => Some(arrow('B')),
            "right" => Some(arrow('C')),
            "left" => Some(arrow('D')),
            "home" => Some("\x1b[H".into()),
            "end" => Some("\x1b[F".into()),
            "pageup" if m.shift => {
                self.term.scroll_display(Scroll::PageUp);
                cx.notify();
                None
            }
            "pagedown" if m.shift => {
                self.term.scroll_display(Scroll::PageDown);
                cx.notify();
                None
            }
            "pageup" => Some("\x1b[5~".into()),
            "pagedown" => Some("\x1b[6~".into()),
            "delete" => Some("\x1b[3~".into()),
            "space" if m.control => Some("\0".into()),
            key if m.control && key.len() == 1 => {
                let c = key.as_bytes()[0].to_ascii_lowercase();
                matches!(c, b'a'..=b'z' | b'[' | b'\\' | b']')
                    .then(|| ((c & 0x1f) as char).to_string())
            }
            _ => k
                .key_char
                .clone()
                .map(|c| if m.alt { format!("\x1b{c}") } else { c }),
        };
        if let Some(s) = seq {
            // Typing brings the view back to the bottom, as terminals do.
            self.term.scroll_display(Scroll::Bottom);
            self.write(s.into_bytes());
            cx.stop_propagation();
        }
    }

    fn render_rows(&self) -> Vec<AnyElement> {
        let grid = self.term.grid();
        let offset = grid.display_offset() as i32;
        let cursor = grid.cursor.point;
        let base = font(FONT);
        let mut out = Vec::with_capacity(self.size.rows);
        for row in 0..self.size.rows.min(grid.screen_lines()) {
            let line = Line(row as i32 - offset);
            let mut text = String::new();
            let mut runs: Vec<TextRun> = Vec::new();
            for col in 0..grid.columns() {
                let cell = &grid[line][Column(col)];
                if cell.flags.contains(Flags::WIDE_CHAR_SPACER) {
                    continue;
                }
                let (mut fg, mut bg) = (color(cell.fg, true), color(cell.bg, false));
                if cell.flags.contains(Flags::INVERSE) {
                    std::mem::swap(&mut fg, &mut bg);
                    bg = bg.or(Some(theme::fg()));
                }
                let is_cursor = offset == 0
                    && line == cursor.line
                    && Column(col) == cursor.column
                    && self.status.is_none();
                if is_cursor {
                    bg = Some(theme::accent());
                }
                let fg = fg.unwrap_or(theme::fg());
                let fg = if cell.flags.contains(Flags::DIM) {
                    fg.opacity(0.6)
                } else {
                    fg
                };
                let mut f = base.clone();
                if cell.flags.contains(Flags::BOLD) {
                    f.weight = FontWeight::BOLD;
                }
                if cell.flags.contains(Flags::ITALIC) {
                    f.style = FontStyle::Italic;
                }
                let underline =
                    cell.flags
                        .intersects(Flags::ALL_UNDERLINES)
                        .then(|| UnderlineStyle {
                            thickness: px(1.),
                            color: Some(fg),
                            wavy: false,
                        });
                let c = if cell.c == '\0' || cell.c == '\t' {
                    ' '
                } else {
                    cell.c
                };
                let len = c.len_utf8();
                text.push(c);
                match runs.last_mut() {
                    Some(r)
                        if r.color == fg
                            && r.background_color == bg
                            && r.font == f
                            && r.underline == underline =>
                    {
                        r.len += len
                    }
                    _ => runs.push(TextRun {
                        len,
                        font: f,
                        color: fg,
                        background_color: bg,
                        underline,
                        strikethrough: None,
                    }),
                }
            }
            out.push(
                div()
                    .h(px(LINE_HEIGHT))
                    .whitespace_nowrap()
                    .child(StyledText::new(text).with_runs(runs))
                    .into_any_element(),
            );
        }
        out
    }
}

impl Focusable for TerminalView {
    fn focus_handle(&self, _: &App) -> FocusHandle {
        self.focus.clone()
    }
}

impl Render for TerminalView {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        self.fit(window);
        let bounds = self.bounds.clone();
        let size = self.size;
        v_flex()
            .id("terminal")
            .key_context("Terminal")
            .track_focus(&self.focus)
            .on_key_down(cx.listener(Self::on_key))
            .on_scroll(cx.listener(|this, ev: &ScrollEvent, _, cx| {
                let dy = ev.delta.pixel_delta(px(LINE_HEIGHT)).y;
                let lines = (dy / px(LINE_HEIGHT)).round() as i32;
                if lines != 0 {
                    this.term.scroll_display(Scroll::Delta(lines));
                    cx.notify();
                }
            }))
            .on_mouse_down(
                MouseButton::Left,
                cx.listener(|this, _, window, cx| this.focus.focus(window, cx)),
            )
            .size_full()
            .bg(gpui::rgb(0x0b0b0d))
            .p_2()
            .font_family(FONT)
            .text_size(px(FONT_SIZE))
            .line_height(px(LINE_HEIGHT))
            .text_color(theme::fg())
            .child(
                div()
                    .relative()
                    .flex_1()
                    .min_h_0()
                    .overflow_hidden()
                    .child(
                        canvas(
                            move |b, window, _| {
                                let changed = bounds.borrow().map(|o| o.size) != Some(b.size);
                                *bounds.borrow_mut() = Some(b);
                                if changed {
                                    window.refresh();
                                }
                            },
                            |_, _, _, _| {},
                        )
                        .absolute()
                        .size_full(),
                    )
                    .children(self.render_rows()),
            )
            .when_some(self.status.clone(), |el, s| {
                el.child(
                    div()
                        .pt_1()
                        .text_xs()
                        .text_color(theme::muted())
                        .child(format!("{s} · {}×{}", size.cols, size.rows)),
                )
            })
    }
}

/// color is a cell's colour in the xterm palette, None for the default.
fn color(c: Color, fg: bool) -> Option<Hsla> {
    match c {
        Color::Spec(Rgb { r, g, b }) => Some(rgb_u8(r, g, b)),
        Color::Indexed(i) => Some(indexed(i)),
        Color::Named(n) => match n {
            NamedColor::Foreground | NamedColor::BrightForeground => fg.then(theme::fg),
            NamedColor::Background => None,
            NamedColor::Cursor => Some(theme::accent()),
            n if (n as usize) < 16 => Some(indexed(n as u8)),
            n if (n as usize) >= NamedColor::DimBlack as usize
                && (n as usize) <= NamedColor::DimWhite as usize =>
            {
                Some(indexed((n as usize - NamedColor::DimBlack as usize) as u8).opacity(0.7))
            }
            _ => fg.then(theme::fg),
        },
    }
}

fn rgb_u8(r: u8, g: u8, b: u8) -> Hsla {
    gpui::rgb((r as u32) << 16 | (g as u32) << 8 | b as u32).into()
}

/// indexed is the 256-colour palette: 16 ANSI colours, a 6×6×6 cube, a
/// grey ramp.
fn indexed(i: u8) -> Hsla {
    const ANSI: [u32; 16] = [
        0x1d1f21, 0xcc6666, 0xb5bd68, 0xf0c674, 0x81a2be, 0xb294bb, 0x8abeb7, 0xc5c8c6, 0x666666,
        0xd54e53, 0xb9ca4a, 0xe7c547, 0x7aa6da, 0xc397d8, 0x70c0b1, 0xeaeaea,
    ];
    match i {
        0..=15 => gpui::rgb(ANSI[i as usize]).into(),
        16..=231 => {
            let i = i - 16;
            let v = |x: u8| if x == 0 { 0 } else { 55 + x * 40 };
            rgb_u8(v(i / 36), v((i / 6) % 6), v(i % 6))
        }
        _ => {
            let g = 8 + (i - 232) * 10;
            rgb_u8(g, g, g)
        }
    }
}

#[cfg(test)]
mod tests {
    use super::{Listener, Size};
    use alacritty_terminal::index::{Column, Line};
    use alacritty_terminal::term::{Config, Term};
    use alacritty_terminal::vte::ansi::Processor;

    #[::core::prelude::v1::test]
    fn emulates_what_a_shell_writes() {
        let size = Size { cols: 20, rows: 4 };
        let replies = Listener::default();
        let mut term = Term::new(Config::default(), &size, replies.clone());
        let mut p: Processor = Processor::new();
        p.advance(&mut term, b"hello\r\n\x1b[31mred\x1b[0m\x1b[6n");
        assert_eq!(term.grid()[Line(0)][Column(0)].c, 'h');
        assert_eq!(term.grid()[Line(1)][Column(2)].c, 'd');
        assert_ne!(
            term.grid()[Line(1)][Column(0)].fg,
            term.grid()[Line(0)][Column(0)].fg
        );
        // The cursor-position query is answered, for the view to send back.
        assert_eq!(replies.0.borrow().as_slice(), ["\x1b[2;4R"]);
    }
}
