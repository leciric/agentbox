//! What the v2 sidebar does besides listing (SidebarV2.tsx, projectOrder.ts,
//! sidebarHidden.ts, SidebarSections.tsx): its head with Help and Add
//! project, the filters, pins, the row's menu, dragging projects within the
//! pins and the sections and sections among themselves, the sections' own
//! headers and menus, project icons, and the foot with Media, Jobs and the
//! update check. Pins are this computer's (prefs), as Electron keeps them in
//! localStorage; hidden projects, sections and the order are the daemon's.
use agentbox_api::client::Method;
use agentbox_api::{
    Project, ProjectIcon, ProjectLayout, Section, SectionProjects, SidebarLayout,
    UpdateSectionRequest,
};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::button::{Button, ButtonVariants as _};
use gpui_component::menu::{ContextMenuExt as _, DropdownMenu as _, PopupMenu, PopupMenuItem};
use gpui_component::tooltip::Tooltip;
use gpui_component::{Sizable as _, h_flex, v_flex};

use super::AppView;
use super::foot::FootPage;
use super::sidebar::{FILTER_PROJECTS, FILTER_ST, PINS, STATES, Shown, save_strings, shown_of};
use super::sidebar_dialogs::Dialog;
use crate::{theme, toasts};

/// Place is where a row sits, which says what dropping on it does.
#[derive(Clone, PartialEq)]
pub enum Place {
    Pinned,
    Section(String),
    Rest,
    Hidden,
}

/// A project being dragged.
#[derive(Clone)]
pub struct DragProject {
    name: String,
    label: String,
}

/// A section being dragged by its header.
#[derive(Clone)]
pub struct DragSection {
    id: String,
    name: String,
}

/// What follows the pointer while dragging: the name.
struct DragPreview(SharedString);

impl Render for DragPreview {
    fn render(&mut self, _: &mut Window, _: &mut Context<Self>) -> impl IntoElement {
        div()
            .px_2()
            .py_1()
            .rounded_md()
            .bg(theme::surface_raised())
            .border_1()
            .border_color(theme::line())
            .text_sm()
            .text_color(theme::primary())
            .shadow_md()
            .child(self.0.clone())
    }
}

pub(super) fn label_of(p: &Project) -> String {
    if p.display_name.is_empty() {
        p.name.clone()
    } else {
        p.display_name.clone()
    }
}

/// glyph_button is a small square button drawn with a glyph and its tooltip.
fn glyph_button(id: impl Into<ElementId>, glyph: &'static str, tip: String) -> Stateful<Div> {
    let tip: SharedString = tip.into();
    div()
        .id(id)
        .flex_none()
        .size_7()
        .flex()
        .items_center()
        .justify_center()
        .rounded_md()
        .text_sm()
        .text_color(theme::muted())
        .cursor_pointer()
        .hover(|el| el.bg(theme::surface_strong()).text_color(theme::primary()))
        .tooltip(move |w, cx| Tooltip::new(tip.clone()).build(w, cx))
        .child(crate::icons::of(glyph))
}

impl AppView {
    // ——— The head ———

    pub(super) fn sidebar_head(&self, cx: &mut Context<Self>) -> AnyElement {
        let view = cx.entity().downgrade();
        let version = self.conn.version.clone().unwrap_or_default();
        let help = Button::new("help-menu")
            .ghost()
            .small()
            .label("?")
            .tooltip(t("tour.help.menu"))
            .dropdown_menu_with_anchor(Anchor::TopRight, move |menu, _, _| {
                let (v1, v2, v3) = (view.clone(), view.clone(), view.clone());
                let version = version.clone();
                menu.min_w(px(220.))
                    .item(
                        PopupMenuItem::new(t("tour.help.take")).on_click(move |_, _, cx| {
                            v1.update(cx, |this, cx| this.replay_tour(cx)).ok();
                        }),
                    )
                    .label(t("tour.whatsNew"))
                    .item(
                        PopupMenuItem::new(tv(
                            "shell.whatsNew.version",
                            &[("version", version.clone().into())],
                        ))
                        .on_click(move |_, _, cx| {
                            let version = version.clone();
                            v2.update(cx, |this, cx| this.show_whats_new(version, cx))
                                .ok();
                        }),
                    )
                    .separator()
                    .item(
                        PopupMenuItem::new(tv("tour.help.keys", &[("keys", "Ctrl+/".into())]))
                            .on_click(move |_, _, cx| {
                                v3.update(cx, |this, cx| {
                                    this.side.dialog = Some(Dialog::Keys);
                                    cx.notify();
                                })
                                .ok();
                            }),
                    )
            });
        h_flex()
            .h(px(56.))
            .flex_none()
            .px_4()
            .gap_2()
            .child(theme::logo(px(28.)))
            .child(
                div()
                    .text_size(theme::text::BODY)
                    .text_color(theme::title())
                    .font_weight(FontWeight::SEMIBOLD)
                    .child("AgentBox"),
            )
            .child(div().ml_1().size_2().rounded_full().bg(if self.connected {
                theme::running()
            } else {
                theme::error()
            }))
            .child(div().flex_1())
            .child(
                div()
                    .relative()
                    .child(help)
                    .child(crate::spot::spot("help")),
            )
            .child(
                glyph_button("add-project", "⊕", t("shell.home.addProject")).on_click(cx.listener(
                    |this, _: &ClickEvent, window, cx| this.open_add_project(window, cx),
                )),
            )
            .into_any_element()
    }

    // ——— The filters ———

    pub(super) fn filter_bar(&self, cx: &mut Context<Self>) -> AnyElement {
        let on = self.side.st.len() + self.side.only.len();
        let mut chips: Vec<(String, String, bool)> = self
            .side
            .st
            .iter()
            .filter_map(|st| {
                STATES
                    .iter()
                    .find(|(s, _)| s == st)
                    .map(|(s, k)| (s.to_string(), t(k), true))
            })
            .collect();
        chips.extend(self.side.only.iter().map(|name| {
            let label = self
                .projects
                .iter()
                .find(|p| &p.name == name)
                .map_or_else(|| name.clone(), label_of);
            (name.clone(), label, false)
        }));
        let button = div()
            .id("filter-button")
            .relative()
            .flex_none()
            .size_8()
            .flex()
            .items_center()
            .justify_center()
            .rounded_lg()
            .border_1()
            .border_color(if on > 0 {
                theme::muted()
            } else {
                theme::line()
            })
            .when(on > 0, |el| el.bg(theme::surface_strong()))
            .text_color(if on > 0 { theme::primary() } else { theme::muted() })
            .cursor_pointer()
            .hover(|el| el.text_color(theme::primary()))
            .tooltip(|w, cx| Tooltip::new(t("v2.filters.button")).build(w, cx))
            .on_click(cx.listener(|this, _: &ClickEvent, _, cx| {
                this.side.filter_open = !this.side.filter_open;
                cx.notify();
            }))
            .child(crate::icons::of("☰"))
            .when(on > 0, |el| {
                el.child(
                    div()
                        .absolute()
                        .top(px(-5.))
                        .right(px(-5.))
                        .min_w(px(16.))
                        .h(px(16.))
                        .px_1()
                        .rounded_full()
                        .bg(theme::surface_raised())
                        .border_1()
                        .border_color(theme::line())
                        .flex()
                        .items_center()
                        .justify_center()
                        .text_size(px(9.5))
                        .font_weight(FontWeight::SEMIBOLD)
                        .child(on.to_string()),
                )
            })
            .when(self.side.filter_open, |el| {
                // From the button's top-left corner, whatever its alignment.
                el.child(
                    div().absolute().top_0().left_0().child(deferred(
                        anchored()
                            .anchor(Anchor::TopLeft)
                            .offset(point(px(-236.), px(38.)))
                            .snap_to_window_with_margin(px(8.))
                            .child(self.filter_popover(cx)),
                    )),
                )
            });
        v_flex()
            .px_3()
            .py_2()
            .gap_1p5()
            .child(
                h_flex()
                    .gap_1p5()
                    .child(div().flex_1().min_w_0().child(
                        gpui_component::input::Input::new(&self.side.search).cleanable(true),
                    ))
                    .child(button),
            )
            .when(!chips.is_empty(), |el| {
                el.child(
                    h_flex()
                        .flex_wrap()
                        .gap_1()
                        .children(chips.into_iter().map(|(key, label, st)| {
                            let k = key.clone();
                            let remove: SharedString =
                                tv("v2.filters.remove", &[("name", (&label).into())]).into();
                            h_flex()
                                .max_w_full()
                                .gap_1()
                                .px_2()
                                .py_0p5()
                                .rounded_full()
                                .border_1()
                                .border_color(theme::line())
                                .bg(theme::surface_raised())
                                .text_xs()
                                .text_color(theme::muted())
                                .child(div().min_w_0().truncate().child(label))
                                .child(
                                    div()
                                        .id(SharedString::from(format!("chip-x-{key}")))
                                        .cursor_pointer()
                                        .text_color(theme::faint())
                                        .hover(|el| el.text_color(theme::primary()))
                                        .tooltip(move |w, cx| {
                                            Tooltip::new(remove.clone()).build(w, cx)
                                        })
                                        .on_click(cx.listener(
                                            move |this, _: &ClickEvent, _, cx| {
                                                if st {
                                                    this.side.st.retain(|s| *s != k);
                                                } else {
                                                    this.side.only.retain(|s| *s != k);
                                                }
                                                this.save_filters(cx);
                                            },
                                        ))
                                        .child(crate::icons::of("×")),
                                )
                        }))
                        .child(
                            div()
                                .id("filters-reset")
                                .px_1()
                                .text_xs()
                                .text_color(theme::faint())
                                .cursor_pointer()
                                .hover(|el| el.text_color(theme::primary()))
                                .on_click(cx.listener(|this, _: &ClickEvent, _, cx| {
                                    this.side.st.clear();
                                    this.side.only.clear();
                                    this.save_filters(cx);
                                }))
                                .child(t("v2.filters.reset")),
                        ),
                )
            })
            .into_any_element()
    }

    fn save_filters(&mut self, cx: &mut Context<Self>) {
        save_strings(FILTER_ST, &self.side.st);
        save_strings(FILTER_PROJECTS, &self.side.only);
        cx.notify();
    }

    fn filter_popover(&self, cx: &mut Context<Self>) -> AnyElement {
        let mut totals = [0usize; 4];
        for p in &self.projects {
            for (i, n) in self.st_counts(&p.name).iter().enumerate() {
                totals[i] += n;
            }
        }
        let chip = |id: SharedString, on: bool, label: String, count: Option<usize>| {
            h_flex()
                .id(id)
                .gap_1p5()
                .px_2()
                .py_0p5()
                .rounded_full()
                .border_1()
                .border_color(if on { theme::muted() } else { theme::line() })
                .when(on, |el| el.bg(theme::surface_strong()))
                .text_xs()
                .text_color(if on { theme::primary() } else { theme::muted() })
                .cursor_pointer()
                .hover(|el| el.text_color(theme::primary()))
                .when(on, |el| el.child(crate::icons::of("✓")))
                .child(div().max_w(px(180.)).truncate().child(label))
                .when_some(count, |el, n| {
                    el.child(div().text_color(theme::faint()).child(n.to_string()))
                })
        };
        let group = |title: String| {
            div()
                .pb_1p5()
                .text_xs()
                .text_color(theme::muted())
                .child(title)
        };
        v_flex()
            .id("filter-popover")
            .occlude()
            .w(px(300.))
            .p_3()
            .gap_3()
            .rounded_lg()
            .bg(theme::card())
            .border_1()
            .border_color(theme::line())
            .shadow_lg()
            .on_mouse_down_out(cx.listener(|this, _: &MouseDownEvent, _, cx| {
                this.side.filter_open = false;
                cx.notify();
            }))
            .on_click(|_, _, cx| cx.stop_propagation())
            .child(
                v_flex().child(group(t("v2.filters.state"))).child(
                    h_flex()
                        .flex_wrap()
                        .gap_1()
                        .children(STATES.iter().enumerate().map(|(i, (st, key))| {
                            let on = self.side.st.iter().any(|s| s == st);
                            let st = st.to_string();
                            chip(format!("st-{st}").into(), on, t(key), Some(totals[i])).on_click(
                                cx.listener(move |this, _: &ClickEvent, _, cx| {
                                    toggle(&mut this.side.st, &st);
                                    this.save_filters(cx);
                                }),
                            )
                        })),
                ),
            )
            .when(self.projects.len() > 1, |el| {
                el.child(
                    v_flex().child(group(t("v2.filters.project"))).child(
                        h_flex()
                            .flex_wrap()
                            .gap_1()
                            .children(self.projects.iter().map(|p| {
                                let on = self.side.only.contains(&p.name);
                                let name = p.name.clone();
                                chip(format!("fp-{}", p.name).into(), on, label_of(p), None)
                                    .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                                        toggle(&mut this.side.only, &name);
                                        this.save_filters(cx);
                                    }))
                            })),
                    ),
                )
            })
            .into_any_element()
    }

    // ——— Lists and sections ———

    /// list_head is a list's heading: Pinned, Projects, with Review all on
    /// the first and New section where it belongs.
    pub(super) fn list_head(
        &self,
        id: &'static str,
        label: String,
        review: bool,
        new_section: bool,
        cx: &mut Context<Self>,
    ) -> AnyElement {
        h_flex()
            .id(id)
            .px_4()
            .pt_3()
            .pb_1()
            .gap_2()
            .text_xs()
            .text_color(theme::faint())
            .child(label.to_uppercase())
            .child(div().flex_1())
            .when(review, |el| el.children(self.review_all(cx)))
            .when(new_section, |el| {
                el.child(
                    glyph_button("new-section", "⊞", t("shell.sidebar.newSection")).on_click(
                        cx.listener(|this, _: &ClickEvent, window, cx| {
                            this.open_section_name(None, None, window, cx)
                        }),
                    ),
                )
            })
            // Dropped on the Projects heading, a project leaves its section.
            .when(id == "head-projects", |el| {
                el.drag_over::<DragProject>(|s, _, _, _| s.bg(theme::accent().opacity(0.12)))
                    .on_drop(cx.listener(|this, d: &DragProject, _, cx| {
                        this.move_project(d.name.clone(), None, None, cx)
                    }))
            })
            .into_any_element()
    }

    /// section_header is a section's name and count: a click folds it,
    /// its menu renames or deletes it, a project dropped on it goes to its
    /// end, and dragged it moves among the sections.
    pub(super) fn section_header(
        &self,
        s: &Section,
        count: usize,
        cx: &mut Context<Self>,
    ) -> AnyElement {
        let id = s.id.clone();
        let collapsed = s.collapsed;
        let view = cx.entity().downgrade();
        let menu_s = s.clone();
        let menu = move |menu: PopupMenu, _: &mut Window, _: &mut Context<PopupMenu>| {
            let (v1, v2, v3) = (view.clone(), view.clone(), view.clone());
            let (s1, s2, s3) = (menu_s.clone(), menu_s.clone(), menu_s.clone());
            menu.label(tv(
                "shell.sidebar.sectionMenu",
                &[("name", (&menu_s.name).into())],
            ))
            .item(
                PopupMenuItem::new(if s1.collapsed {
                    t("shell.sidebar.expand")
                } else {
                    t("shell.sidebar.collapse")
                })
                .on_click(move |_, _, cx| {
                    let s1 = s1.clone();
                    v1.update(cx, |this, cx| this.fold_section(&s1.id, !s1.collapsed, cx))
                        .ok();
                }),
            )
            .item(
                PopupMenuItem::new(t("common.rename")).on_click(move |_, window, cx| {
                    let s2 = s2.clone();
                    v2.update(cx, |this, cx| {
                        this.open_section_name(Some(s2), None, window, cx)
                    })
                    .ok();
                }),
            )
            .separator()
            .item(
                PopupMenuItem::new(t("shell.sidebar.deleteSection")).on_click(move |_, _, cx| {
                    let s3 = s3.clone();
                    v3.update(cx, |this, cx| {
                        let count = this.projects.iter().filter(|p| p.section == s3.id).count();
                        this.side.dialog = Some(Dialog::DeleteSection {
                            id: s3.id.clone(),
                            name: s3.name.clone(),
                            count,
                        });
                        cx.notify();
                    })
                    .ok();
                }),
            )
        };
        let drag = DragSection {
            id: s.id.clone(),
            name: s.name.clone(),
        };
        let fold = id.clone();
        h_flex()
            .id(SharedString::from(format!("section-{}", s.id)))
            .mx_2()
            .mt_2()
            .px_2()
            .py_1()
            .gap_1p5()
            .rounded_md()
            .text_xs()
            .text_color(theme::faint())
            .cursor_pointer()
            .hover(|el| el.bg(theme::surface_raised()).text_color(theme::muted()))
            .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                this.fold_section(&fold, !collapsed, cx)
            }))
            .on_drag(drag, |d: &DragSection, _, _, cx| {
                let name: SharedString = d.name.clone().into();
                cx.new(|_| DragPreview(name))
            })
            .drag_over::<DragProject>(|s, _, _, _| s.bg(theme::accent().opacity(0.15)))
            .drag_over::<DragSection>(|s, _, _, _| s.border_t_2().border_color(theme::accent()))
            .on_drop(cx.listener({
                let id = id.clone();
                move |this, d: &DragProject, _, cx| {
                    this.move_project(d.name.clone(), Some(id.clone()), None, cx)
                }
            }))
            .on_drop(cx.listener({
                let id = id.clone();
                move |this, d: &DragSection, _, cx| this.move_section(&d.id, &id, cx)
            }))
            .child(crate::icons::of(if collapsed { "▸" } else { "▾" }))
            .child(
                div()
                    .min_w_0()
                    .truncate()
                    .font_weight(FontWeight::MEDIUM)
                    .child(s.name.to_uppercase()),
            )
            .child(div().child(count.to_string()))
            .child(div().flex_1())
            .child(
                Button::new(SharedString::from(format!("section-menu-{}", s.id)))
                    .ghost()
                    .xsmall()
                    .icon(crate::icons::ic("ellipsis"))
                    .dropdown_menu_with_anchor(Anchor::TopRight, menu.clone()),
            )
            .context_menu(menu)
            .into_any_element()
    }

    /// drop_box is where a project dropped goes to a list's end: an empty
    /// section's, or out of any.
    pub(super) fn drop_box(
        &self,
        id: SharedString,
        label: String,
        section: Option<String>,
        cx: &mut Context<Self>,
    ) -> AnyElement {
        div()
            .id(id)
            .mx_3()
            .my_1()
            .px_2p5()
            .py_1p5()
            .rounded_lg()
            .border_1()
            .border_dashed()
            .border_color(theme::line())
            .text_xs()
            .text_color(theme::faint())
            .drag_over::<DragProject>(|s, _, _, _| {
                s.border_color(theme::accent())
                    .bg(theme::accent().opacity(0.1))
                    .text_color(theme::accent())
            })
            .on_drop(cx.listener(move |this, d: &DragProject, _, cx| {
                this.move_project(d.name.clone(), section.clone(), None, cx)
            }))
            .child(label)
            .into_any_element()
    }

    // ——— A project's row: icon, menu, dragging ———

    /// project_icon is a project's icon: its emoji, its picture, or its
    /// initial (ProjectIcon.tsx).
    pub(super) fn project_icon(&self, p: &Project, size: f32) -> AnyElement {
        let base = div()
            .flex_none()
            .size(px(size))
            .flex()
            .items_center()
            .justify_center()
            .rounded_md()
            .overflow_hidden();
        match self.side.icons.get(&p.name).map(|(_, s)| s) {
            Some(Shown::Emoji(e)) => base.text_size(px(size * 0.62)).child(e.clone()),
            Some(Shown::Image(i)) => base.child(img(i.clone()).size(px(size))),
            _ => base
                .bg(theme::surface_raised())
                .text_size(px(size * 0.5))
                .text_color(theme::muted())
                .child(
                    label_of(p)
                        .chars()
                        .next()
                        .unwrap_or('?')
                        .to_uppercase()
                        .to_string(),
                ),
        }
        .into_any_element()
    }

    /// row_dnd wraps a project's row with its menu and its dragging.
    pub(super) fn row_dnd(
        &self,
        p: &Project,
        display: &str,
        place: Place,
        row: AnyElement,
        cx: &mut Context<Self>,
    ) -> AnyElement {
        let menu = self.project_menu(p, &place, cx);
        let drag = DragProject {
            name: p.name.clone(),
            label: display.to_string(),
        };
        let target = p.name.clone();
        let after = self
            .side
            .drop_edge
            .as_ref()
            .is_some_and(|(n, a)| *n == p.name && *a);
        div()
            .id(SharedString::from(format!("dnd-{}", p.name)))
            .when(place != Place::Hidden, |el| {
                let place = place.clone();
                el.on_drag(drag, |d: &DragProject, _, _, cx| {
                    let label: SharedString = d.label.clone().into();
                    cx.new(|_| DragPreview(label))
                })
                .on_drag_move::<DragProject>(cx.listener({
                    let target = target.clone();
                    move |this, e: &DragMoveEvent<DragProject>, _, cx| {
                        if !e.bounds.contains(&e.event.position) {
                            return;
                        }
                        let after = e.event.position.y > e.bounds.center().y;
                        let now = Some((target.clone(), after));
                        if this.side.drop_edge != now {
                            this.side.drop_edge = now;
                            cx.notify();
                        }
                    }
                }))
                .drag_over::<DragProject>(move |s, _, _, _| {
                    if after {
                        s.border_b_2().border_color(theme::accent())
                    } else {
                        s.border_t_2().border_color(theme::accent())
                    }
                })
                .on_drop(cx.listener(move |this, d: &DragProject, _, cx| {
                    let after = this
                        .side
                        .drop_edge
                        .take()
                        .is_some_and(|(n, a)| n == target && a);
                    if d.name == target {
                        return;
                    }
                    match &place {
                        Place::Pinned => this.move_pin(&d.name, &target, after, cx),
                        Place::Section(s) => this.move_project(
                            d.name.clone(),
                            Some(s.clone()),
                            Some((target.clone(), after)),
                            cx,
                        ),
                        Place::Rest => this.move_project(d.name.clone(), None, None, cx),
                        Place::Hidden => {}
                    }
                }))
            })
            .context_menu(menu)
            .child(row)
            .into_any_element()
    }

    /// project_menu is a row's menu (ProjectHeader.tsx projectActions, and
    /// the sidebar's Hide and Move to section).
    fn project_menu(
        &self,
        p: &Project,
        place: &Place,
        cx: &mut Context<Self>,
    ) -> impl Fn(PopupMenu, &mut Window, &mut Context<PopupMenu>) -> PopupMenu + 'static {
        let view = cx.entity().downgrade();
        let name = p.name.clone();
        let root = p.root.clone();
        let pinned = self.side.pins.contains(&p.name);
        let hidden = *place == Place::Hidden || self.side.hidden.contains(&p.name);
        let others = self.projects.len() > 1;
        let current = p.section.clone();
        let sections: Vec<(String, String)> = self
            .side
            .sections
            .iter()
            .map(|s| (s.id.clone(), s.name.clone()))
            .collect();
        move |menu: PopupMenu, window: &mut Window, cx: &mut Context<PopupMenu>| {
            let v = view.clone();
            let n = name.clone();
            let act = move |f: fn(&mut AppView, String, &mut Window, &mut Context<AppView>)| {
                let (v, n) = (v.clone(), n.clone());
                move |_: &ClickEvent, window: &mut Window, cx: &mut App| {
                    let n = n.clone();
                    v.update(cx, |this, cx| f(this, n, window, cx)).ok();
                }
            };
            let mut menu = menu
                .min_w(px(220.))
                .item(PopupMenuItem::new(t("v2.sidebar.newTask")).on_click(act(
                    |this, n, window, cx| {
                        this.select_project(n, window, cx);
                        if let Some(chat) = &this.chat {
                            chat.update(cx, |c, cx| c.focus_composer(window, cx));
                        }
                    },
                )))
                .item(
                    PopupMenuItem::new(t("group.menu.fanOut"))
                        .on_click(act(|this, n, window, cx| this.open_fan_out(n, window, cx))),
                );
            if others {
                menu = menu.item(PopupMenuItem::new(t("v2.talk.button")).on_click(act(
                    |this, n, _, cx| {
                        this.side.dialog = Some(Dialog::Talk { project: n });
                        cx.notify();
                    },
                )));
            }
            menu =
                menu.item(PopupMenuItem::new(t("work.title")).on_click(act(
                    |this, n, window, cx| this.open_work(n, String::new(), window, cx),
                )))
                .separator()
                .item(
                    PopupMenuItem::new(if pinned {
                        t("v2.sidebar.unpin")
                    } else {
                        t("v2.sidebar.pin")
                    })
                    .on_click(act(|this, n, _, cx| this.toggle_pin(&n, cx))),
                )
                .item(
                    PopupMenuItem::new(t("v2.sidebar.projectSettings")).on_click(act(
                        |this, n, window, cx| {
                            this.open_settings_at(format!("project:{n}"), window, cx)
                        },
                    )),
                )
                .item(
                    PopupMenuItem::new(if hidden {
                        t("v2.sidebar.unhide")
                    } else {
                        t("v2.sidebar.hide")
                    })
                    .on_click(act(|this, n, _, cx| this.toggle_hidden(&n, cx))),
                );
            let (secs, cur, v, n) = (
                sections.clone(),
                current.clone(),
                view.clone(),
                name.clone(),
            );
            menu = menu.submenu(t("v2.sidebar.moveToSection"), window, cx, move |m, _, _| {
                let mut m = m;
                for (id, label) in secs.iter().filter(|(id, _)| *id != cur) {
                    let (v, n, id) = (v.clone(), n.clone(), id.clone());
                    m = m.item(PopupMenuItem::new(label.clone()).on_click(move |_, _, cx| {
                        let (n, id) = (n.clone(), id.clone());
                        v.update(cx, |this, cx| this.move_project(n, Some(id), None, cx))
                            .ok();
                    }));
                }
                if !cur.is_empty() {
                    let (v, n) = (v.clone(), n.clone());
                    m = m.item(PopupMenuItem::new(t("v2.sidebar.noSection")).on_click(
                        move |_, _, cx| {
                            let n = n.clone();
                            v.update(cx, |this, cx| this.move_project(n, None, None, cx))
                                .ok();
                        },
                    ));
                }
                if secs.len() > 1 || (secs.len() == 1 && cur.is_empty()) {
                    m = m.separator();
                }
                let (v, n) = (v.clone(), n.clone());
                m.item(PopupMenuItem::new(t("v2.sidebar.newSectionFor")).on_click(
                    move |_, window, cx| {
                        let n = n.clone();
                        v.update(cx, |this, cx| {
                            this.open_section_name(None, Some(n), window, cx)
                        })
                        .ok();
                    },
                ))
            });
            let (v, n, r) = (view.clone(), name.clone(), root.clone());
            menu.submenu(t("common.more"), window, cx, move |m, _, _| {
                let (v1, v2) = (v.clone(), v.clone());
                let (n1, n2, r) = (n.clone(), n.clone(), r.clone());
                m.item(PopupMenuItem::new(t("projectIcon.change")).on_click(
                    move |_, window, cx| {
                        let n = n1.clone();
                        v1.update(cx, |this, cx| this.open_icon_dialog(n, window, cx))
                            .ok();
                    },
                ))
                .item(
                    PopupMenuItem::new(t("project.view.openFolder"))
                        .on_click(move |_, _, cx| cx.open_with_system(std::path::Path::new(&r))),
                )
                .item(PopupMenuItem::new(t("v2.menu.agentYourself")).on_click(
                    move |_, window, cx| {
                        let n = n2.clone();
                        v2.update(cx, |this, cx| this.open_new_agent(n, window, cx))
                            .ok();
                    },
                ))
            })
        }
    }

    // ——— Pins, hidden, sections and the order ———

    pub(super) fn toggle_pin(&mut self, name: &str, cx: &mut Context<Self>) {
        toggle(&mut self.side.pins, name);
        save_strings(PINS, &self.side.pins);
        cx.notify();
    }

    /// move_pin puts a pin before or after another, by the half it was
    /// dropped on (projectOrder.ts movePin); a project not pinned yet is
    /// pinned there.
    fn move_pin(&mut self, name: &str, target: &str, after: bool, cx: &mut Context<Self>) {
        let pins = &mut self.side.pins;
        if !pins.iter().any(|p| p == target) {
            return;
        }
        pins.retain(|p| p != name);
        let Some(at) = pins.iter().position(|p| p == target) else {
            return;
        };
        pins.insert(if after { at + 1 } else { at }, name.to_string());
        save_strings(PINS, &self.side.pins);
        cx.notify();
    }

    /// toggle_hidden hides a project (unpinning it: a pin is never hidden) or
    /// shows it again (PUT /v1/sidebar).
    fn toggle_hidden(&mut self, name: &str, cx: &mut Context<Self>) {
        let mut hidden: Vec<String> = self.side.hidden.iter().cloned().collect();
        if self.side.hidden.contains(name) {
            hidden.retain(|n| n != name);
        } else {
            hidden.push(name.to_string());
            if self.side.pins.iter().any(|p| p == name) {
                self.toggle_pin(name, cx);
            }
        }
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client
                .raw(
                    Method::PUT,
                    "/v1/sidebar",
                    serde_json::to_vec(&SidebarLayout { hidden }).ok(),
                )
                .await
                .and_then(|b| Ok(serde_json::from_slice::<SidebarLayout>(&b)?));
            this.update(cx, |this, cx| {
                match res {
                    Ok(l) => this.side.hidden = l.hidden.into_iter().collect(),
                    Err(e) => toasts::Toast::new(toasts::Kind::Error, t("v2.sidebar.hideFailed"))
                        .description(e.to_string())
                        .show(cx),
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// layout_now is the order as it stands: the sections, in order, with
    /// their projects, and the projects in none.
    fn layout_now(&self) -> ProjectLayout {
        let mut ps: Vec<&Project> = self.projects.iter().collect();
        ps.sort_by_key(|p| (p.position, p.name.clone()));
        let known = |id: &str| self.side.sections.iter().any(|s| s.id == id);
        ProjectLayout {
            sections: self
                .side
                .sections
                .iter()
                .map(|s| SectionProjects {
                    id: s.id.clone(),
                    projects: ps
                        .iter()
                        .filter(|p| p.section == s.id)
                        .map(|p| p.name.clone())
                        .collect(),
                })
                .collect(),
            loose: ps
                .iter()
                .filter(|p| !known(&p.section))
                .map(|p| p.name.clone())
                .collect(),
        }
    }

    /// move_project puts a project in a section (None: in none), where
    /// before or after another (by the half it was dropped on) or at the end
    /// (PUT /v1/projects/layout). It leaves the pins.
    pub(super) fn move_project(
        &mut self,
        name: String,
        to: Option<String>,
        at: Option<(String, bool)>,
        cx: &mut Context<Self>,
    ) {
        let mut layout = self.layout_now();
        for s in &mut layout.sections {
            if let Some(i) = s.projects.iter().position(|p| *p == name) {
                s.projects.remove(i);
            }
        }
        if let Some(i) = layout.loose.iter().position(|p| *p == name) {
            layout.loose.remove(i);
        }
        let list = match &to {
            Some(id) => match layout.sections.iter_mut().find(|s| s.id == *id) {
                Some(s) => &mut s.projects,
                None => return,
            },
            None => &mut layout.loose,
        };
        let i = at
            .and_then(|(a, after)| {
                list.iter()
                    .position(|p| *p == a)
                    .map(|i| i + usize::from(after))
            })
            .unwrap_or(list.len());
        list.insert(i.min(list.len()), name.clone());
        if self.side.pins.contains(&name) && to.is_some() {
            self.toggle_pin(&name, cx);
        }
        self.put_layout(layout, false, cx);
    }

    /// move_section puts a section where another is.
    fn move_section(&mut self, id: &str, target: &str, cx: &mut Context<Self>) {
        if id == target {
            return;
        }
        let mut layout = self.layout_now();
        let Some(from) = layout.sections.iter().position(|s| s.id == id) else {
            return;
        };
        let s = layout.sections.remove(from);
        let Some(at) = layout.sections.iter().position(|s| s.id == target) else {
            return;
        };
        let to = if from <= at { at + 1 } else { at };
        layout.sections.insert(to, s);
        self.put_layout(layout, true, cx);
    }

    fn put_layout(&mut self, layout: ProjectLayout, sections: bool, cx: &mut Context<Self>) {
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client
                .raw(
                    Method::PUT,
                    "/v1/projects/layout",
                    serde_json::to_vec(&layout).ok(),
                )
                .await
                .and_then(|b| Ok(serde_json::from_slice::<Vec<Project>>(&b)?));
            this.update(cx, |this, cx| {
                match res {
                    Ok(p) => this.projects = p,
                    Err(e) => toasts::error(cx, e.to_string()),
                }
                if sections {
                    this.load_sections(cx);
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// load_sections reads the sections again.
    pub(super) fn load_sections(&mut self, cx: &mut Context<Self>) {
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            if let Ok(mut s) = client.get::<Vec<Section>>("/v1/sections").await {
                s.sort_by_key(|s| s.position);
                this.update(cx, |this, cx| {
                    this.side.sections = s;
                    cx.notify();
                })
                .ok();
            }
        })
        .detach();
    }

    /// patch_section renames a section or folds it (PATCH /v1/sections/{id}).
    pub(super) fn patch_section(
        &mut self,
        id: &str,
        req: UpdateSectionRequest,
        cx: &mut Context<Self>,
    ) {
        if let Some(s) = self.side.sections.iter_mut().find(|s| s.id == id) {
            if let Some(c) = req.collapsed {
                s.collapsed = c;
            }
            if let Some(n) = &req.name {
                s.name = n.clone();
            }
        }
        cx.notify();
        let client = self.client.clone();
        let path = format!("/v1/sections/{id}");
        cx.spawn(async move |this, cx| {
            let res = client
                .raw(Method::PATCH, &path, serde_json::to_vec(&req).ok())
                .await;
            this.update(cx, |this, cx| {
                if let Err(e) = res {
                    toasts::error(cx, e.to_string());
                }
                this.load_sections(cx);
            })
            .ok();
        })
        .detach();
    }

    fn fold_section(&mut self, id: &str, collapsed: bool, cx: &mut Context<Self>) {
        self.patch_section(
            id,
            UpdateSectionRequest {
                name: None,
                collapsed: Some(collapsed),
            },
            cx,
        );
    }

    /// add_section makes a section at the end, and moves a project into it
    /// when one asked for it.
    pub(super) fn add_section(
        &mut self,
        name: String,
        moving: Option<String>,
        cx: &mut Context<Self>,
    ) {
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client
                .post::<_, Section>("/v1/sections", &agentbox_api::AddSectionRequest { name })
                .await;
            this.update(cx, |this, cx| match res {
                Ok(s) => {
                    this.side.sections.push(s.clone());
                    if let Some(p) = moving {
                        this.move_project(p, Some(s.id), None, cx);
                    }
                    this.load_sections(cx);
                }
                Err(e) => toasts::error(cx, e.to_string()),
            })
            .ok();
        })
        .detach();
    }

    pub(super) fn delete_section(&mut self, id: String, cx: &mut Context<Self>) {
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client
                .raw(Method::DELETE, &format!("/v1/sections/{id}"), None)
                .await;
            this.update(cx, |this, cx| {
                if let Err(e) = res {
                    toasts::error(cx, e.to_string());
                }
                this.load_sections(cx);
                this.reload_projects(cx);
            })
            .ok();
        })
        .detach();
    }

    /// reload_projects reads the projects again, without a window.
    pub(super) fn reload_projects(&mut self, cx: &mut Context<Self>) {
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            if let Ok(p) = client.get::<Vec<Project>>("/v1/projects").await {
                this.update(cx, |this, cx| {
                    this.projects = p;
                    this.load_icons(cx);
                    cx.notify();
                })
                .ok();
            }
        })
        .detach();
    }

    /// load_icons reads the icons of the projects whose icon changed.
    pub(super) fn load_icons(&mut self, cx: &mut Context<Self>) {
        for p in &self.projects {
            if self
                .side
                .icons
                .get(&p.name)
                .is_some_and(|(v, _)| *v == p.icon_version)
            {
                continue;
            }
            // Read once per version, even if the read fails.
            self.side
                .icons
                .insert(p.name.clone(), (p.icon_version, Shown::None));
            let (client, name, version) = (self.client.clone(), p.name.clone(), p.icon_version);
            cx.spawn(async move |this, cx| {
                if let Ok(icon) = client
                    .get::<ProjectIcon>(&format!("/v1/projects/{name}/icon"))
                    .await
                {
                    this.update(cx, |this, cx| {
                        this.side.icons.insert(name, (version, shown_of(&icon)));
                        cx.notify();
                    })
                    .ok();
                }
            })
            .detach();
        }
    }

    // ——— Drafts ———

    /// keep_draft remembers what the open chat's composer holds, before
    /// another chat replaces it.
    pub(super) fn keep_draft(&mut self, cx: &App) {
        let Some(chat) = &self.chat else {
            return;
        };
        let c = chat.read(cx);
        let text = c.draft_text(cx);
        let changed = if text.trim().is_empty() {
            self.side.drafts.remove(&c.agent).is_some()
        } else {
            self.side.drafts.insert(c.agent.clone(), text.clone()) != Some(text)
        };
        if changed {
            crate::prefs::set(
                super::sidebar::DRAFTS,
                serde_json::to_value(&self.side.drafts).unwrap_or_default(),
            );
        }
    }

    // ——— The foot ———

    pub(super) fn sidebar_foot(&self, cx: &mut Context<Self>) -> AnyElement {
        let page = self.side.page;
        let item = |id: &'static str, glyph: &'static str, label: String, on: bool| {
            let tip: SharedString = label.into();
            div()
                .id(id)
                .flex_none()
                .size_8()
                .flex()
                .items_center()
                .justify_center()
                .rounded_lg()
                .cursor_pointer()
                .text_color(if on { theme::accent() } else { theme::muted() })
                .when(on, |el| el.bg(theme::surface_strong()))
                .hover(|el| el.bg(theme::surface_raised()).text_color(theme::primary()))
                .tooltip(move |w, cx| Tooltip::new(tip.clone()).build(w, cx))
                .child(crate::icons::of(glyph))
        };
        let settings = self.settings;
        let work = self.work.is_some() && !settings && page.is_none();
        h_flex()
            .flex_none()
            .p_2()
            .gap_1()
            .border_t_1()
            .border_color(theme::line())
            .child(
                item("foot-settings", "⚙", t("common.settings"), settings).on_click(
                    cx.listener(|this, _: &ClickEvent, window, cx| this.open_settings(window, cx)),
                ),
            )
            .child(
                item("foot-work", "briefcase", t("work.title"), work).on_click(cx.listener(
                    |this, _: &ClickEvent, window, cx| {
                        this.open_work(String::new(), String::new(), window, cx)
                    },
                )),
            )
            .child(
                item(
                    "foot-media",
                    "images",
                    t("shell.nav.media"),
                    page == Some(FootPage::Media) && !settings,
                )
                .on_click(cx.listener(|this, _: &ClickEvent, window, cx| {
                    this.open_foot_page(FootPage::Media, window, cx)
                })),
            )
            .child(
                item(
                    "foot-jobs",
                    "list-checks",
                    t("shell.nav.jobs"),
                    page == Some(FootPage::Jobs) && !settings,
                )
                .on_click(cx.listener(|this, _: &ClickEvent, window, cx| {
                    this.open_foot_page(FootPage::Jobs, window, cx)
                })),
            )
            .child(div().flex_1())
            .child(self.update_button(cx))
            .into_any_element()
    }

    fn update_button(&self, cx: &mut Context<Self>) -> AnyElement {
        use crate::update::Phase;
        // Installed: the button restarts into the new version.
        if let Some(Phase::Ready { relaunch, .. }) = &self.side.app_update {
            let relaunch = relaunch.clone();
            return glyph_button("update-restart", "⟳", t("native.update.restart"))
                .text_color(theme::running())
                .on_click(move |_, _, cx| {
                    restart_into(&relaunch, cx);
                })
                .into_any_element();
        }
        let offer = self.side.update.as_ref().and_then(|u| {
            u.available
                .as_ref()
                .filter(|a| newer(&a.version, &u.current))
                .cloned()
        });
        if let Some(offer) = offer {
            let progress = self.side.app_update.as_ref().map(update_label);
            let updating = progress.is_some();
            let label = progress
                .unwrap_or_else(|| tv("v2.update.to", &[("version", (&offer.version).into())]));
            let tip = if updating {
                label
            } else {
                format!(
                    "{label} · {}",
                    update_hint(&self.side.app_support, &offer.version)
                )
            };
            return glyph_button("update-offer", if updating { "◌" } else { "⇪" }, tip)
                .text_color(theme::running())
                .when(updating, |el| el.opacity(0.6).cursor_default())
                .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                    this.start_app_update(offer.url.clone(), cx)
                }))
                .into_any_element();
        }
        let label = if self.side.checking {
            t("v2.update.checking")
        } else {
            t("v2.update.check")
        };
        glyph_button("update-check", "↻", label)
            .when(self.side.checking, |el| el.opacity(0.5))
            .on_click(cx.listener(|this, _: &ClickEvent, _, cx| this.check_update(cx)))
            .into_any_element()
    }

    /// start_app_update updates the app in place (update.rs), or opens the
    /// release page where this copy can't (useAppUpdate.ts's start); pinned
    /// is the page of the release the check found.
    fn start_app_update(&mut self, pinned: String, cx: &mut Context<Self>) {
        use crate::update::{self, Failure, Phase, Support};
        let Support::InPlace(install) = self.side.app_support.clone() else {
            cx.open_url(&pinned);
            return;
        };
        if self.side.app_update.is_some() {
            return;
        }
        self.side.app_update = Some(Phase::Preparing);
        cx.notify();
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let prepared: Result<update::Plan, Failure> = async {
                // A restart cuts a job short: the update waits for them.
                let jobs = client
                    .get::<Vec<agentbox_api::Job>>("/v1/jobs")
                    .await
                    .unwrap_or_default();
                let busy = jobs.iter().filter(|j| j.status == "running").count();
                if busy > 0 {
                    return Err(Failure::new("busy", busy.to_string()));
                }
                let status = client
                    .get::<agentbox_api::UpdateStatus>("/v1/update")
                    .await
                    .unwrap_or_default();
                // A daemon of another version than this app picks the
                // release for its own (updater.ts's isOutdated).
                let app = update::app_version();
                if let Some(app) = app
                    && !status.current.is_empty()
                    && status.current.trim_start_matches('v') != app.trim_start_matches('v')
                {
                    return Err(Failure::new("daemonStale", status.current.clone()));
                }
                let release = client
                    .get::<agentbox_api::UpdateRelease>("/v1/update/release")
                    .await
                    .map_err(|e| Failure::new("noRelease", e.to_string()))?;
                let current = app.map(str::to_string).unwrap_or(status.current.clone());
                let offered = status.available.as_ref().map(|a| a.version.as_str());
                update::plan(&release, &install, &current, offered)
            }
            .await;
            let plan = match prepared {
                Ok(p) => p,
                Err(f) => {
                    this.update(cx, |this, cx| this.update_failed(f, &pinned, cx))
                        .ok();
                    return;
                }
            };
            // The download blocks: a thread of its own, saying how far it got.
            let (tx, mut rx) = tokio::sync::mpsc::unbounded_channel();
            std::thread::spawn(move || {
                let progress = |p: Phase| {
                    let _ = tx.send(Ok(p));
                };
                let done = update::apply(&plan, &progress).map(|relaunch| Phase::Ready {
                    version: plan.version.clone(),
                    relaunch,
                });
                let _ = tx.send(done);
            });
            while let Some(msg) = rx.recv().await {
                let stop = this
                    .update(cx, |this, cx| match msg {
                        Ok(Phase::Ready { version, relaunch }) => {
                            this.side.app_update = Some(Phase::Ready {
                                version: version.clone(),
                                relaunch: relaunch.clone(),
                            });
                            // Installed: straight into the new version, as
                            // Electron's quitAndInstall. Should starting it
                            // fail, the button and the toast's stay to try again.
                            if !restart_into(&relaunch, cx) {
                                toasts::Toast::new(
                                    toasts::Kind::Info,
                                    tv("native.update.ready", &[("version", version.into())]),
                                )
                                .action(t("native.update.restart"), move |_, cx| {
                                    restart_into(&relaunch, cx);
                                })
                                .show(cx);
                            }
                            cx.notify();
                            true
                        }
                        Ok(p) => {
                            this.side.app_update = Some(p);
                            cx.notify();
                            false
                        }
                        Err(f) => {
                            this.update_failed(f, &pinned, cx);
                            true
                        }
                    })
                    .unwrap_or(true);
                if stop {
                    break;
                }
            }
        })
        .detach();
    }

    /// update_failed says why the update stopped, offering the release page.
    fn update_failed(&mut self, f: crate::update::Failure, pinned: &str, cx: &mut Context<Self>) {
        self.side.app_update = None;
        let count = f.detail.parse::<usize>().unwrap_or(0);
        let msg = tv(
            &format!("shell.update.error.{}", f.code),
            &[
                ("detail", f.detail.clone().into()),
                ("version", f.detail.clone().into()),
                ("count", count.into()),
            ],
        );
        let page = f.url.unwrap_or_else(|| pinned.to_string());
        toasts::Toast::new(toasts::Kind::Error, t("shell.update.failed"))
            .description(msg)
            .action(t("shell.update.releasePage"), move |_, cx| {
                cx.open_url(&page)
            })
            .show(cx);
        cx.notify();
    }

    pub(super) fn load_update(&mut self, cx: &mut Context<Self>) {
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            if let Ok(u) = client.get::<agentbox_api::UpdateStatus>("/v1/update").await {
                this.update(cx, |this, cx| {
                    this.side.update = Some(u);
                    cx.notify();
                })
                .ok();
            }
        })
        .detach();
    }

    /// check_update asks for the latest release: a newer one shows as the
    /// update button, else a toast says this one is the latest.
    fn check_update(&mut self, cx: &mut Context<Self>) {
        if self.side.checking {
            return;
        }
        self.side.checking = true;
        cx.notify();
        let client = self.client.clone();
        let current = self
            .side
            .update
            .as_ref()
            .map(|u| u.current.clone())
            .or_else(|| self.conn.version.clone())
            .unwrap_or_default();
        cx.spawn(async move |this, cx| {
            let res = client
                .get::<agentbox_api::UpdateRelease>("/v1/update/release")
                .await;
            this.update(cx, |this, cx| {
                this.side.checking = false;
                match res {
                    Ok(r) if newer(&r.version, &current) => this.load_update(cx),
                    Ok(_) => {
                        toasts::info(cx, tv("v2.update.latest", &[("version", current.into())]))
                    }
                    Err(e) => toasts::Toast::new(toasts::Kind::Error, t("v2.update.failed"))
                        .description(e.to_string())
                        .show(cx),
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }
}

/// update_label is what the update button says while it runs
/// (appUpdate.ts's updateLabel).
fn update_label(p: &crate::update::Phase) -> String {
    use crate::update::Phase;
    match p {
        Phase::Preparing => t("shell.update.preparing"),
        Phase::Download { received, total } => {
            let percent = if *total > 0 {
                received * 100 / total
            } else {
                0
            };
            tv(
                "shell.update.downloading",
                &[("percent", (percent as usize).into())],
            )
        }
        Phase::Verify => t("shell.update.verifying"),
        Phase::Install => t("shell.update.installing"),
        Phase::Ready { .. } => t("native.update.restart"),
    }
}

/// update_hint is the update button's tooltip: what clicking it does.
fn update_hint(support: &crate::update::Support, version: &str) -> String {
    match support {
        crate::update::Support::InPlace(_) => {
            tv("native.update.inPlace", &[("version", version.into())])
        }
        crate::update::Support::ReleasePage(reason) => {
            t(&format!("shell.update.releasePage.{reason}"))
        }
    }
}

/// restart_into starts the new version and quits this one; false, saying
/// why, when the new one couldn't be started.
fn restart_into(relaunch: &std::path::Path, cx: &mut App) -> bool {
    match crate::update::restart(relaunch) {
        Ok(()) => {
            cx.quit();
            true
        }
        Err(e) => {
            toasts::Toast::new(toasts::Kind::Error, t("shell.update.failed"))
                .description(tv(
                    "shell.update.error.install",
                    &[("detail", e.to_string().into())],
                ))
                .show(cx);
            false
        }
    }
}

/// toggle adds a value to a list, or takes it out.
pub(super) fn toggle(list: &mut Vec<String>, v: &str) {
    if list.iter().any(|x| x == v) {
        list.retain(|x| x != v);
    } else {
        list.push(v.to_string());
    }
}

/// newer is whether version a is past b (nightly.ts isUpgrade): their
/// numbers compared in order, a prerelease before its release.
pub(super) fn newer(a: &str, b: &str) -> bool {
    fn parts(v: &str) -> (Vec<u64>, String) {
        let v = v.trim_start_matches('v');
        let (core, pre) = v.split_once('-').unwrap_or((v, ""));
        (
            core.split('.').map(|n| n.parse().unwrap_or(0)).collect(),
            pre.to_string(),
        )
    }
    if a.is_empty() || b.is_empty() {
        return false;
    }
    let ((ca, pa), (cb, pb)) = (parts(a), parts(b));
    match ca.cmp(&cb) {
        std::cmp::Ordering::Greater => true,
        std::cmp::Ordering::Less => false,
        std::cmp::Ordering::Equal => match (pa.is_empty(), pb.is_empty()) {
            (true, false) => true,
            (false, false) => pa > pb,
            _ => false,
        },
    }
}

#[cfg(test)]
mod tests {
    use super::{newer, toggle};

    #[test]
    fn newer_compares_numbers_then_prereleases() {
        assert!(newer("0.17.0", "0.16.3"));
        assert!(newer("0.17.0", "0.17.0-nightly.20261030.156"));
        assert!(newer(
            "0.17.0-nightly.20261031.1",
            "0.17.0-nightly.20261030.156"
        ));
        assert!(!newer("0.16.3", "0.17.0"));
        assert!(!newer("0.17.0", "0.17.0"));
        assert!(!newer("", "0.17.0"));
    }

    #[test]
    fn toggle_adds_and_removes() {
        let mut l = vec!["a".to_string()];
        toggle(&mut l, "b");
        toggle(&mut l, "a");
        assert_eq!(l, vec!["b".to_string()]);
    }
}
