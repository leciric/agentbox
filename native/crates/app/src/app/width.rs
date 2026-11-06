//! How wide the panel is (lib/rightPanel.ts's listBox and panelBox, and
//! RightPanel.tsx's WidthHandle and RightPanelHost): its left edge drags the
//! list's, the lead's or an agent's width, each remembered, a double click
//! putting it back; where it doesn't fit beside the chat (a window under
//! 900 px, or less than the chat's 440 px left beside it), its strip stays
//! and the rest opens over the chat as a drawer.
use gpui::prelude::FluentBuilder as _;
use gpui::*;

use super::AppView;
use super::panel::Whose;
use super::pulls::RailTab;
use crate::{prefs, theme};

const BESIDE_MIN: f32 = 900.;
const CHAT_MIN: f32 = 440.;
const PANEL_MIN: f32 = 340.;
const PANEL_DEFAULT: f32 = 560.;
const LIST_DEFAULT: f32 = 352.;
const LIST_MIN: f32 = 220.;
/// The sidebar's width, which the chat and the panel share the rest of.
const SIDEBAR: f32 = 288.;

#[derive(Clone, Copy, PartialEq)]
pub enum Which {
    List,
    Lead,
    Agent,
}

pub struct Widths {
    lead: f32,
    agent: Option<f32>,
    list: Option<f32>,
    /// The window's width, as last drawn.
    pub window: f32,
    /// A drag under way: which width, from where, from how wide.
    drag: Option<(Which, f32, f32)>,
    /// Narrow, the drawer is open over the chat.
    pub drawer: bool,
    /// The drawer is sliding shut: still drawn, until its spring settles.
    pub closing: bool,
}

fn saved(key: &str) -> Option<f32> {
    prefs::get(key).and_then(|v| v.as_f64()).map(|v| v as f32)
}

impl Widths {
    pub fn load() -> Self {
        Self {
            lead: saved("panel.width").unwrap_or(PANEL_DEFAULT),
            agent: saved("panel.agentWidth"),
            list: saved("panel.listWidth"),
            window: 1440.,
            drag: None,
            drawer: false,
            closing: false,
        }
    }

    fn save(&self) {
        let num =
            |v: Option<f32>| v.map_or(serde_json::Value::Null, |v| serde_json::json!(v.round()));
        prefs::set("panel.width", num(Some(self.lead)));
        prefs::set("panel.agentWidth", num(self.agent));
        prefs::set("panel.listWidth", num(self.list));
    }

    pub fn dragging(&self) -> bool {
        self.drag.is_some()
    }
}

/// Box is where the panel goes: its width, and whether it is a drawer.
pub struct PanelBox {
    pub width: f32,
    pub drawer: bool,
}

/// list_box is listBox: the list's width, or a drawer's.
fn list_box(want: Option<f32>, room: f32, window: f32) -> PanelBox {
    let want = want.unwrap_or(LIST_DEFAULT).max(LIST_MIN);
    if window < BESIDE_MIN || room - CHAT_MIN < LIST_MIN {
        return PanelBox {
            width: want.max(300.).min(window - 24.).round(),
            drawer: true,
        };
    }
    PanelBox {
        width: want.min(room - CHAT_MIN).round(),
        drawer: false,
    }
}

/// panel_box is panelBox: a view's width, or a drawer's.
fn panel_box(want: f32, room: f32, window: f32) -> PanelBox {
    if window < BESIDE_MIN || room - CHAT_MIN < PANEL_MIN {
        return PanelBox {
            width: (window * 0.92)
                .min(want.max(420.))
                .min(window - 24.)
                .round(),
            drawer: true,
        };
    }
    PanelBox {
        width: want.min(room - CHAT_MIN).max(PANEL_MIN).round(),
        drawer: false,
    }
}

impl AppView {
    fn which(&self) -> Which {
        match self.panel_whose() {
            // The project's settings over the lead's panel: the lead's width.
            Whose::Lead if self.panel_settings.is_some() => Which::Lead,
            Whose::Lead if self.pulls.tab == RailTab::Agents => Which::List,
            Whose::List => Which::List,
            Whose::Lead => Which::Lead,
            Whose::Agent(_) | Whose::Past(_) => Which::Agent,
        }
    }

    /// panel_box is the panel's width now, and whether it's a drawer.
    pub(super) fn panel_box(&self) -> PanelBox {
        let w = &self.widths;
        let room = w.window - SIDEBAR;
        match self.which() {
            Which::List => list_box(w.list, room, w.window),
            Which::Lead => panel_box(w.lead, room, w.window),
            Which::Agent => {
                let want = w.agent.unwrap_or(if self.panel_shows_files() {
                    LIST_DEFAULT
                } else {
                    (room / 2.).round()
                });
                panel_box(want, room, w.window)
            }
        }
    }

    fn panel_shows_files(&self) -> bool {
        matches!(self.panel_whose(), Whose::Agent(r) if self.panel.view_of(&r) == super::panel::PView::Files)
    }

    /// narrow is whether the panel is a drawer over the chat, its strip beside it.
    pub(super) fn narrow(&self) -> bool {
        let w = &self.widths;
        let room = w.window - SIDEBAR;
        self.panel_box().drawer || list_box(w.list, room, w.window).drawer
    }

    /// width_handle is the panel's left edge, dragged.
    pub(super) fn width_handle(&self, width: f32, cx: &mut Context<Self>) -> AnyElement {
        let which = self.which();
        let on = self.widths.drag.is_some();
        div()
            .id("panel-splitter")
            .absolute()
            .left_0()
            .top_0()
            .bottom_0()
            .w(px(6.))
            .cursor_col_resize()
            .when(on, |el| el.bg(theme::accent().opacity(0.4)))
            .hover(|s| s.bg(theme::accent().opacity(0.3)))
            .on_mouse_down(
                MouseButton::Left,
                cx.listener(move |this, ev: &MouseDownEvent, _, cx| {
                    if ev.click_count >= 2 {
                        match which {
                            Which::List => this.widths.list = None,
                            Which::Lead => this.widths.lead = PANEL_DEFAULT,
                            Which::Agent => this.widths.agent = None,
                        }
                        this.widths.save();
                    } else {
                        this.widths.drag = Some((which, f32::from(ev.position.x), width));
                    }
                    cx.stop_propagation();
                    cx.notify();
                }),
            )
            .into_any_element()
    }

    /// drag_to follows the pointer while the edge is dragged.
    pub(super) fn drag_to(&mut self, x: Pixels, cx: &mut Context<Self>) {
        let Some((which, from, start)) = self.widths.drag else {
            return;
        };
        let next = start - (f32::from(x) - from);
        match which {
            Which::List => self.widths.list = Some(next.max(LIST_MIN)),
            Which::Lead => self.widths.lead = next.max(PANEL_MIN),
            Which::Agent => self.widths.agent = Some(next.max(PANEL_MIN)),
        }
        cx.notify();
    }

    pub(super) fn drag_end(&mut self, cx: &mut Context<Self>) {
        if self.widths.drag.take().is_some() {
            self.widths.save();
            cx.notify();
        }
    }

    /// close_drawer slides the drawer shut: it stays drawn while it goes.
    pub(super) fn close_drawer(&mut self, cx: &mut Context<Self>) {
        if !self.widths.drawer {
            return;
        }
        self.widths.drawer = false;
        self.widths.closing = true;
        cx.notify();
        cx.spawn(async move |this, cx| {
            cx.background_executor()
                .timer(std::time::Duration::from_millis(360))
                .await;
            this.update(cx, |this, cx| {
                if !this.widths.drawer {
                    this.widths.closing = false;
                    cx.notify();
                }
            })
            .ok();
        })
        .detach();
    }

    /// render_drawer is the panel over the chat, with its scrim, while open.
    pub(super) fn render_drawer(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let open = self.widths.drawer;
        if !(open || self.widths.closing)
            || !self.narrow()
            || self.is_home()
            || self.project.is_none()
        {
            return None;
        }
        let b = self.panel_box();
        let width = if matches!(self.which(), Which::List) {
            let room = self.widths.window - SIDEBAR;
            list_box(
                self.widths.list.or(Some(LIST_DEFAULT)),
                room,
                self.widths.window,
            )
            .width
        } else {
            b.width
        };
        Some(
            div()
                .id("panel-drawer-scrim")
                .absolute()
                .inset_0()
                .bg(theme::scrim())
                .on_mouse_down(
                    MouseButton::Left,
                    cx.listener(|this, _: &MouseDownEvent, _, cx| this.close_drawer(cx)),
                )
                .child(crate::motion::drawer(
                    div()
                        .id("panel-drawer")
                        .absolute()
                        .top_0()
                        .bottom_0()
                        .right_0()
                        .w(px(width))
                        .bg(theme::modal())
                        .border_l_1()
                        .border_color(theme::line())
                        .shadow(theme::shadow_drawer())
                        .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
                        .on_key_down(cx.listener(|this, ev: &KeyDownEvent, _, cx| {
                            if ev.keystroke.key == "escape" {
                                this.close_drawer(cx);
                            }
                        }))
                        .child(self.render_panel_body(width, cx)),
                    "panel-drawer-slide",
                    crate::motion::Side::Right,
                    px(width + 24.),
                    open,
                ))
                .with_spring(
                    "panel-drawer-scrim-fade",
                    SpringAnimation::new(crate::motion::GENTLE)
                        .to(open)
                        .from(false),
                    |el, phase| el.opacity(phase.0.clamp(0., 1.)),
                )
                .into_any_element(),
        )
    }
}

#[cfg(test)]
mod tests {
    use super::{CHAT_MIN, LIST_DEFAULT, SIDEBAR, list_box, panel_box};

    #[test]
    fn the_panel_goes_over_the_chat_where_it_does_not_fit() {
        let b = panel_box(560., 1440. - SIDEBAR, 1440.);
        assert!(!b.drawer);
        assert_eq!(b.width, 560.);
        // The chat keeps its 440 px.
        let b = panel_box(900., 1200. - SIDEBAR, 1200.);
        assert_eq!((b.width, b.drawer), (1200. - SIDEBAR - CHAT_MIN, false));
        let b = panel_box(560., 860. - SIDEBAR, 860.);
        assert!(b.drawer);
        let l = list_box(None, 1100. - SIDEBAR, 1100.);
        assert_eq!((l.width, l.drawer), (LIST_DEFAULT, false));
    }
}
