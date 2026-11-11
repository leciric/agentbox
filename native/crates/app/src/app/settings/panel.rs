//! A project's settings in the right-hand panel beside its lead's chat
//! (RightPanel.tsx's ProjectView, ProjectView.tsx's ProjectSettingsBody): a
//! header with back, Work, Settings (pressed) and ✕; the view's title,
//! "Project settings · demo"; its sections as a row of chips
//! (SettingsSections.tsx, narrower than its @3xl); and the open section,
//! scrolling on its own. The groups are the Settings page's project section's
//! (project.rs), the essential ones first and the rest under Advanced, as
//! ProjectSettings.tsx draws them.
use agentbox_api::Project;
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::tooltip::Tooltip;
use gpui_component::{h_flex, v_flex};

use super::super::AppView;
use super::super::controls::{self, Btn, button};
use super::{Group, kit};
use crate::theme;

/// The sections, in ProjectView.tsx's projectSettingsSections order: id, its
/// title's key, its icon.
pub(in crate::app) const SECTIONS: [(&str, &str, &str); 8] = [
    (
        "repository",
        "project.view.section.repository",
        "folder-git-2",
    ),
    (
        "general",
        "project.view.section.general",
        "sliders-horizontal",
    ),
    ("brief", "project.view.section.brief", "file-text"),
    ("memory", "project.view.section.memory", "brain"),
    ("tokens", "project.view.section.tokens", "coins"),
    ("secrets", "project.view.section.secrets", "key-round"),
    ("connectors", "project.view.section.connectors", "plug"),
    ("skills", "project.view.section.skills", "wand-sparkles"),
];

/// section_of is a section's id as SECTIONS has it, or General's.
pub(in crate::app) fn section_of(id: &str) -> &'static str {
    SECTIONS
        .iter()
        .find(|(s, _, _)| *s == id)
        .map_or("general", |(s, _, _)| s)
}

/// The groups the panel's General leaves to their own sections.
const ELSEWHERE: [&str; 3] = ["p-secrets", "p-tokens", "p-skills"];

/// ghost_icon is Button size icon-sm variant ghost: 32px, a 15px icon.
fn ghost_icon(id: &'static str, icon: &'static str, tip: String, pressed: bool) -> Stateful<Div> {
    let tip: SharedString = tip.into();
    div()
        .id(id)
        .flex_none()
        .size_8()
        .flex()
        .items_center()
        .justify_center()
        .rounded_lg()
        .cursor_pointer()
        .when(pressed, |el| {
            el.bg(theme::surface_raised()).text_color(theme::primary())
        })
        .when(!pressed, |el| el.text_color(theme::muted()))
        .hover(|el| el.bg(theme::surface_raised()).text_color(theme::primary()))
        .tooltip(move |window, cx| Tooltip::new(tip.clone()).build(window, cx))
        .child(crate::icons::ic(icon).size(px(15.)))
}

impl AppView {
    /// open_project_settings opens the lead's panel on its project's
    /// settings, at a section (openPanelOn(chat, 'settings')).
    pub(in crate::app) fn open_project_settings(
        &mut self,
        section: &str,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let Some(name) = self.project.clone() else {
            return;
        };
        self.settings = false;
        self.panel_settings = Some(section_of(section));
        self.load_accounts(window, cx);
        self.load_project(name.clone(), window, cx);
        self.load_tokens(name.clone(), cx);
        if section == "skills" {
            self.load_skills(name, cx);
        }
        cx.notify();
    }

    fn panel_project(&self) -> Option<&Project> {
        let name = self.project.as_ref()?;
        self.projects.iter().find(|p| &p.name == name)
    }

    /// project_buttons are the lead's panel's Work and Settings, beside its
    /// ⋯ (ProjectButtons): Settings pressed while the panel shows them, and
    /// clicked again, back to what it showed.
    pub(in crate::app) fn project_buttons(
        &self,
        gap: Pixels,
        cx: &mut Context<Self>,
    ) -> AnyElement {
        let [work, settings] = self.project_button_items(cx);
        h_flex()
            .flex_none()
            .gap(gap)
            .child(work)
            .child(settings)
            .into_any_element()
    }

    /// project_button_items are project_buttons unwrapped, for a header
    /// that lays them out as its own children. Like Chromium's buttons in
    /// a row too narrow, they shrink, down to their icon.
    pub(in crate::app) fn project_button_items(
        &self,
        cx: &mut Context<Self>,
    ) -> [Stateful<Div>; 2] {
        let on = self.panel_settings.is_some();
        [
            ghost_icon("panel-open-work", "briefcase", t("work.open"), false).on_click(
                cx.listener(|this, _: &ClickEvent, window, cx| {
                    let p = this.project.clone().unwrap_or_default();
                    this.open_work(p, String::new(), window, cx)
                }),
            ),
            ghost_icon(
                "panel-project-settings",
                "settings",
                t("v2.sidebar.projectSettings"),
                on,
            )
            .on_click(cx.listener(move |this, _: &ClickEvent, window, cx| {
                if on {
                    this.panel_settings = None;
                    cx.notify();
                } else {
                    this.open_project_settings("general", window, cx);
                }
            })),
        ]
    }

    /// render_project_settings is the panel's header and body while it shows
    /// the project's settings.
    pub(in crate::app) fn render_project_settings(
        &self,
        cx: &mut Context<Self>,
    ) -> (AnyElement, AnyElement) {
        let section = self.panel_settings.unwrap_or("general");
        // The header: h-11 bg-rail pl-2 pr-1.5 gap-1, a faint line under it.
        let close = |this: &mut Self, cx: &mut Context<Self>| {
            this.panel_settings = None;
            cx.notify();
        };
        let header = h_flex()
            .flex_none()
            .h(px(44.))
            .pl_2()
            .pr_1p5()
            .gap_1()
            .border_b_1()
            .border_color(theme::line_faint())
            .bg(theme::rail())
            .child(
                ghost_icon("panel-back", "arrow-left", t("panel.back"), false)
                    .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| close(this, cx))),
            )
            .child(div().flex_1())
            .child(self.project_buttons(px(4.), cx))
            .child(
                ghost_icon("panel-close", "x", t("panel.back"), false)
                    .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| close(this, cx))),
            )
            .into_any_element();

        let name = self
            .panel_project()
            .map(|p| {
                if p.display_name.is_empty() {
                    p.name.clone()
                } else {
                    p.display_name.clone()
                }
            })
            .unwrap_or_default();
        // ViewTitle: h-8 px-3 gap-1.5, 12px medium secondary, a faint line.
        let title = h_flex()
            .flex_none()
            .h_8()
            .px_3()
            // Chromium puts the line's half pixel above it.
            .pt(px(1.))
            .gap(px(6.))
            .border_b_1()
            .border_color(theme::line_faint())
            .text_size(px(12.))
            .font_weight(FontWeight::MEDIUM)
            .text_color(theme::secondary())
            .child(
                div()
                    .text_color(theme::subtle())
                    .child(crate::icons::ic("sliders-horizontal").size(px(14.))),
            )
            .child(t("panel.view.settings"))
            .child(
                div()
                    .ml_1()
                    .min_w_0()
                    .truncate()
                    .font_weight(FontWeight::NORMAL)
                    .text_color(theme::subtle())
                    .child(format!("· {name}")),
            );
        // The sections, narrow: flex-wrap gap-1 px-3 py-2, each chip
        // px-2.5 py-1.5 rounded-lg text-ui with a size-4 icon.
        let chips = h_flex()
            .flex_none()
            .flex_wrap()
            .gap_1()
            .px_3()
            .py_2()
            .children(SECTIONS.iter().map(|(id, key, icon)| {
                let on = *id == section;
                let id: &'static str = id;
                h_flex()
                    .id(SharedString::from(format!("panel-settings-{id}")))
                    .flex_none()
                    .gap_2()
                    .px_2p5()
                    .pt(px(7.))
                    .pb(px(6.))
                    .rounded_lg()
                    .text_size(theme::text::UI)
                    .line_height(px(19.))
                    .cursor_pointer()
                    .when(on, |el| {
                        el.bg(theme::surface_raised()).text_color(theme::title())
                    })
                    .when(!on, |el| {
                        el.text_color(theme::muted())
                            .hover(|s| s.bg(theme::surface()).text_color(theme::primary()))
                    })
                    .child(
                        div()
                            .flex_none()
                            .text_color(if on {
                                theme::brand(300)
                            } else {
                                theme::subtle()
                            })
                            .child(crate::icons::ic(icon).size(px(16.))),
                    )
                    .child(t(key))
                    .on_click(cx.listener(move |this, _: &ClickEvent, window, cx| {
                        this.open_project_settings(id, window, cx)
                    }))
            }));

        let body = match self.panel_project() {
            Some(p) => self.project_settings_section(p, section, cx),
            None => div().into_any_element(),
        };
        let page = &self.settings_page;
        let gutter = page.panel_scroll.max_offset().y > px(0.5);
        let scroller = div()
            .relative()
            .flex_1()
            .min_h_0()
            .child(
                div()
                    .id("panel-settings-open")
                    .size_full()
                    .overflow_y_scroll()
                    .track_scroll(&page.panel_scroll)
                    .when(gutter, |el| el.pr(controls::SCROLLBAR))
                    .child(body),
            )
            .child(controls::scrollbar(&page.panel_scroll, gutter));
        let body = v_flex()
            .size_full()
            .child(title)
            .child(chips)
            .child(scroller)
            .into_any_element();
        (header, body)
    }

    /// project_settings_section is one section, in its column: max-w-3xl
    /// (max-w-6xl for Tokens) px-8 py-7.
    fn project_settings_section(
        &self,
        p: &Project,
        section: &str,
        cx: &mut Context<Self>,
    ) -> AnyElement {
        let column = v_flex()
            .w_full()
            .max_w(px(if section == "tokens" { 1152. } else { 768. }))
            .mx_auto()
            .px_8()
            .py_7();
        let groups = self.project_groups(p);
        let card_of = |id: &str| {
            groups
                .iter()
                .find(|g| g.id == id)
                .and_then(|g| g.entries.first())
        };
        let inner: AnyElement = match section {
            "general" => controls::narrow_rows(|| self.project_general(p, &groups, cx)),
            "tokens" => self.tokens_card(&p.name, cx),
            "secrets" => match card_of("p-secrets") {
                Some(e) => self.render_card(e, cx),
                None => div().into_any_element(),
            },
            "skills" => self.skills_card(&p.name, cx),
            "repository" => self.project_repository(p),
            // Notes, memory and connectors are the Settings page's still.
            other => {
                let to = match other {
                    "memory" => "memory".to_string(),
                    "connectors" => "connectors".to_string(),
                    _ => format!("project:{}", p.name),
                };
                kit::panel(v_flex())
                    .px_5()
                    .py_4()
                    .gap_3()
                    .items_start()
                    .text_size(theme::text::UI)
                    .text_color(theme::muted())
                    .child(t("native.settings.later"))
                    .child(button(
                        "panel-settings-page",
                        t("common.settings"),
                        Btn::Secondary,
                        false,
                        cx.listener(move |this, _: &ClickEvent, window, cx| {
                            this.open_settings_at(to.clone(), window, cx)
                        }),
                    ))
                    .into_any_element()
            }
        };
        column.child(inner).into_any_element()
    }

    /// project_general is ProjectSettingsBody's General: the icon's card, the
    /// essential groups, Advanced folded over the rest, and the project's
    /// actions.
    fn project_general(&self, p: &Project, groups: &[Group], cx: &mut Context<Self>) -> AnyElement {
        let mut essentials = Vec::new();
        let mut advanced = Vec::new();
        for g in groups.iter().filter(|g| !ELSEWHERE.contains(&g.id)) {
            let kept: Vec<_> = g.entries.iter().filter(|e| e.essential).cloned().collect();
            let rest: Vec<_> = g.entries.iter().filter(|e| !e.essential).cloned().collect();
            if !kept.is_empty() {
                essentials.push(g.with(kept));
            }
            if !rest.is_empty() {
                advanced.push(g.with(rest));
            }
        }
        let open = self.settings_page.panel_advanced;
        let count: usize = advanced.iter().map(|g| g.entries.len()).sum();
        let changed: usize = advanced
            .iter()
            .map(|g| g.entries.iter().filter(|e| e.modified).count())
            .sum();
        let name = p.name.clone();
        // Card: panel rounded-2xl, its header px-5 pt-4 pb-1, a size-7 icon
        // box, text-sm semibold and text-xs subtle; its body px-5 pt-3 pb-5.
        let icon_card = kit::panel(v_flex())
            .mb_4()
            .child(
                h_flex()
                    .gap_3()
                    .px_5()
                    .pt_4()
                    .pb_1()
                    // Its size-7 box has no shrink-0 in Electron, so beside
                    // three lines it gives way to its icon's 16px.
                    .child(
                        div()
                            .flex_none()
                            .w(px(16.))
                            .h_7()
                            .flex()
                            .items_center()
                            .justify_center()
                            .rounded_lg()
                            .bg(theme::surface())
                            .border_1()
                            .border_color(theme::line())
                            .text_color(theme::muted())
                            .child(crate::icons::ic("face-slightly-smiling").size(px(16.))),
                    )
                    .child(
                        v_flex()
                            .flex_1()
                            .min_w_0()
                            .child(
                                div()
                                    .text_size(theme::text::BODY)
                                    .line_height(px(20.))
                                    .font_weight(FontWeight::SEMIBOLD)
                                    .text_color(theme::primary())
                                    .child(t("projectIcon.label")),
                            )
                            .child(
                                div()
                                    .text_size(theme::text::CAPTION)
                                    .line_height(px(16.))
                                    .text_color(theme::subtle())
                                    .child(t("projectIcon.description")),
                            ),
                    )
                    .child(button(
                        "panel-change-icon",
                        t("projectIcon.change"),
                        Btn::Secondary,
                        false,
                        cx.listener(move |this, _: &ClickEvent, window, cx| {
                            this.open_icon_dialog(name.clone(), window, cx)
                        }),
                    )),
            )
            .child(
                // ProjectIcon size-10 rounded-xl text-heading.
                div().px_5().pt_3().pb_5().child(
                    div()
                        .size_10()
                        .rounded_xl()
                        .overflow_hidden()
                        .flex()
                        .items_center()
                        .justify_center()
                        .bg(theme::surface_strong())
                        .text_size(theme::text::HEADING)
                        .font_weight(FontWeight::SEMIBOLD)
                        .text_color(theme::secondary())
                        .map(|el| {
                            if self.side.icons.contains_key(&p.name) {
                                el.child(self.project_icon(p, 40.))
                            } else {
                                let name = if p.display_name.is_empty() {
                                    &p.name
                                } else {
                                    &p.display_name
                                };
                                el.child(
                                    name.chars()
                                        .next()
                                        .unwrap_or('?')
                                        .to_uppercase()
                                        .to_string(),
                                )
                            }
                        }),
                ),
            );
        let root = p.root.clone();
        let actions = kit::panel(v_flex())
            .mt_4()
            .child(
                h_flex()
                    .gap_3()
                    .px_5()
                    .pt_4()
                    .pb_1()
                    .child(
                        div()
                            .flex_none()
                            .size_7()
                            .flex()
                            .items_center()
                            .justify_center()
                            .rounded_lg()
                            .bg(theme::surface())
                            .border_1()
                            .border_color(theme::line())
                            .text_color(theme::muted())
                            .child(crate::icons::ic("folder-git-2").size(px(16.))),
                    )
                    .child(
                        div()
                            .text_size(theme::text::BODY)
                            .font_weight(FontWeight::SEMIBOLD)
                            .text_color(theme::primary())
                            .child(t("project.view.actions")),
                    ),
            )
            .child(
                h_flex()
                    .flex_wrap()
                    .gap_2()
                    .px_5()
                    .pt_3()
                    .pb_5()
                    .child(button(
                        "panel-open-folder",
                        t("project.view.openFolder"),
                        Btn::Secondary,
                        false,
                        move |_, _, cx| cx.open_with_system(std::path::Path::new(&root)),
                    )),
            );
        v_flex()
            .child(icon_card)
            .child(
                v_flex()
                    .gap_8()
                    .children(essentials.iter().map(|g| self.render_group(g, cx)))
                    .when(!advanced.is_empty(), |el| {
                        // Advanced: its toggle, and while open the rest.
                        el.child(
                            v_flex()
                                .gap_8()
                                .child(
                                    h_flex()
                                        .id("panel-settings-advanced")
                                        .w_auto()
                                        .gap(px(6.))
                                        .px_1()
                                        .rounded_md()
                                        .text_size(px(13.))
                                        .font_weight(FontWeight::SEMIBOLD)
                                        .text_color(theme::primary())
                                        .cursor_pointer()
                                        .hover(|s| s.text_color(theme::title()))
                                        .active(|s| s.opacity(0.7))
                                        .on_click(cx.listener(|this, _: &ClickEvent, _, cx| {
                                            this.settings_page.panel_advanced =
                                                !this.settings_page.panel_advanced;
                                            cx.notify();
                                        }))
                                        .child(div().text_color(theme::subtle()).child(
                                            crate::motion::chevron("chev", open, Some(px(16.))),
                                        ))
                                        .child(t("settings.page.advanced"))
                                        .child(
                                            div()
                                                .font_weight(FontWeight::NORMAL)
                                                .text_color(theme::subtle())
                                                .child(format!(
                                                    "· {}",
                                                    tv(
                                                        if changed > 0 {
                                                            "settings.page.advancedCountChanged"
                                                        } else {
                                                            "settings.page.advancedCount"
                                                        },
                                                        &[
                                                            ("count", count.into()),
                                                            ("changed", changed.into()),
                                                        ],
                                                    )
                                                )),
                                        ),
                                )
                                .when(open, |el| {
                                    el.children(advanced.iter().map(|g| self.render_group(g, cx)))
                                }),
                        )
                    }),
            )
            .child(actions)
            .into_any_element()
    }

    /// project_repository is the Repository section's card: the folder, the
    /// branch new agents start from, the env files.
    fn project_repository(&self, p: &Project) -> AnyElement {
        let row = |label: String, value: String| {
            h_flex()
                .min_h_9()
                .gap_4()
                .border_t_1()
                .border_color(theme::line_faint())
                .child(
                    div()
                        .w(px(112.))
                        .flex_none()
                        .text_size(theme::text::UI)
                        .text_color(theme::subtle())
                        .child(label),
                )
                .child(
                    div()
                        .flex_1()
                        .min_w_0()
                        .truncate()
                        .font_family(theme::MONO)
                        .text_size(theme::text::UI)
                        .text_color(theme::secondary())
                        .child(value),
                )
        };
        let env = if p.env_files.is_empty() {
            t("project.view.envNone")
        } else {
            p.env_files.join(", ")
        };
        kit::panel(v_flex())
            .child(
                h_flex()
                    .gap_3()
                    .px_5()
                    .pt_4()
                    .pb_1()
                    .child(
                        div()
                            .flex_none()
                            .size_7()
                            .flex()
                            .items_center()
                            .justify_center()
                            .rounded_lg()
                            .bg(theme::surface())
                            .border_1()
                            .border_color(theme::line())
                            .text_color(theme::muted())
                            .child(crate::icons::ic("folder-git-2").size(px(16.))),
                    )
                    .child(
                        div()
                            .text_size(theme::text::BODY)
                            .font_weight(FontWeight::SEMIBOLD)
                            .text_color(theme::primary())
                            .child(t("project.view.section.repository")),
                    ),
            )
            .child(
                v_flex()
                    .px_5()
                    .pt_3()
                    .pb_5()
                    .child(row(t("project.view.folder"), p.root.clone()).border_t_0())
                    .child(row(t("project.view.newAgentsFrom"), p.branch.clone()))
                    .child(row(t("project.view.envFiles"), env)),
            )
            .into_any_element()
    }
}
