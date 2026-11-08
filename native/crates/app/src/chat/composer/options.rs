//! The chat's settings under the message box, grouped so the row stays short:
//! what runs the chat (the AI tool and its account), what it thinks with (the
//! model, its context window and effort, one picker that reads as a summary:
//! "Opus 4.1 · 200k · High"), how it may act (the permission mode, with plan
//! first inside it) and the rest of the tool's settings behind one "more".
//! Each chip folds to its icon as the composer narrows, and the row wraps
//! before anything is cut. Every choice the separate chips offered is in one
//! of these menus.
use agentbox_api::{CHAT_OPTION_PLAN, ChatOption, LeadRunners};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::button::Button;
use gpui_component::menu::{DropdownMenu as _, PopupMenu, PopupMenuItem};
use gpui_component::{h_flex, v_flex};

use super::option_chip;
use crate::chat::ChatView;
use crate::theme;

/// Under these composer widths a chip's words fold away, leaving its icon
/// (Electron's @max-[…]/composer container queries).
const TOOL_NAME_AT: f32 = 736.;
const ACCOUNT_AT: f32 = 560.;
const SUMMARY_AT: f32 = 560.;
const MODE_AT: f32 = 672.;
/// A setting with more choices than this gets a submenu of its own inside
/// its group's menu (OpenCode's models), so the others stay in reach.
const INLINE_CHOICES: usize = 8;

/// name_of is an option's current choice by name.
pub(super) fn name_of(o: &ChatOption) -> String {
    o.choices
        .iter()
        .find(|c| c.value == o.value)
        .map(|c| c.name.clone())
        .unwrap_or_else(|| o.value.clone())
}

/// summary is the model picker's label: the model, the context window, and
/// the effort unless it's the tool's default ("Opus 4.1 · 200k · High").
pub(super) fn summary(
    model: Option<&ChatOption>,
    window: Option<&ChatOption>,
    effort: Option<&ChatOption>,
) -> Vec<String> {
    [
        model.map(name_of),
        window.map(name_of),
        effort.filter(|e| e.value != "default").map(name_of),
    ]
    .into_iter()
    .flatten()
    .filter(|p| !p.is_empty())
    .collect()
}

/// chip_label is a chip's inside: its icon, its words, a chevron.
fn chip_label(icon: Option<AnyElement>, words: Option<String>, tint: Option<Hsla>) -> Div {
    h_flex()
        .gap(px(6.))
        .min_w_0()
        .text_size(theme::text::UI)
        .line_height(px(19.5))
        .when_some(tint, |el, c| el.text_color(c))
        .children(icon)
        .when_some(words, |el, w| {
            el.child(div().min_w_0().max_w(px(200.)).truncate().child(w))
        })
        .child(
            div()
                .text_color(theme::faint())
                .child(crate::icons::ic("chevron-down").size(px(14.))),
        )
}

/// choice_item is one choice in a menu: a check when it's the one, its name
/// and what it's about (OptionMenu's rows).
fn choice_item(
    name: String,
    about: String,
    on: bool,
    pick: impl Fn(&mut App) + 'static,
) -> PopupMenuItem {
    PopupMenuItem::element(move |_, _| {
        h_flex()
            .items_start()
            .gap_2()
            .child(
                div()
                    .w(px(12.))
                    .flex_none()
                    .text_color(theme::accent())
                    .child(crate::icons::of(if on { "✓" } else { "" })),
            )
            .child(v_flex().child(div().text_sm().child(name.clone())).when(
                !about.is_empty(),
                |el| {
                    el.child(
                        // A long one (plan's) wraps rather than
                        // widening the menu past the window.
                        div()
                            .max_w(px(280.))
                            .whitespace_normal()
                            .text_xs()
                            .text_color(theme::muted())
                            .child(about.clone()),
                    )
                },
            ))
    })
    .on_click(move |_, _, cx| pick(cx))
}

/// choices adds an option's choices to a menu, under their groups' labels.
fn choices(mut menu: PopupMenu, o: &ChatOption, view: &WeakEntity<ChatView>) -> PopupMenu {
    let mut group = String::new();
    for c in &o.choices {
        if c.group != group {
            group = c.group.clone();
            if !group.is_empty() {
                menu = menu.label(group.clone());
            }
        }
        let on = c.value == o.value;
        let (view, id, v) = (view.clone(), o.id.clone(), c.value.clone());
        menu = menu.item(choice_item(
            c.name.clone(),
            c.description.clone(),
            on,
            move |cx| {
                if !on {
                    view.update(cx, |this, cx| this.set_option(id.clone(), v.clone(), cx))
                        .ok();
                }
            },
        ));
    }
    menu
}

/// section is one option in a group's menu: its name and its choices, or a
/// submenu of them when they're many.
fn section(
    menu: PopupMenu,
    o: &ChatOption,
    view: &WeakEntity<ChatView>,
    window: &mut Window,
    cx: &mut Context<PopupMenu>,
) -> PopupMenu {
    let menu = menu.separator();
    if o.choices.len() > INLINE_CHOICES {
        let (o, view) = (o.clone(), view.clone());
        return menu.submenu(
            format!("{}: {}", o.name, name_of(&o)),
            window,
            cx,
            move |m, _, _| choices(m.max_h(px(420.)).scrollable(true), &o, &view),
        );
    }
    choices(menu.label(o.name.clone()), o, view)
}

/// tool_menu is the runner picker's menu: every tool and account the chat
/// may move to (an agent's, its own tool's accounts only).
fn tool_menu(
    mut menu: PopupMenu,
    r: &LeadRunners,
    tool: &str,
    account: &str,
    lead: bool,
    view: &WeakEntity<ChatView>,
) -> PopupMenu {
    menu = menu.label(if lead {
        t("chat.runner.title")
    } else {
        t("chat.runner.titleAgent")
    });
    for runner in &r.runners {
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
            let name = if acc.is_empty() {
                runner.name.clone()
            } else {
                format!("{} · {acc}", runner.name)
            };
            let about = if !runner.ready && runner.installed {
                runner.reason.clone()
            } else {
                String::new()
            };
            let (view, tl, a) = (view.clone(), runner.tool.clone(), acc.clone());
            menu = menu.item(
                choice_item(name, about, on, move |cx| {
                    if !on {
                        view.update(cx, |this, cx| this.set_runner(tl.clone(), a.clone(), cx))
                            .ok();
                    }
                })
                .disabled(!runner.ready && runner.installed),
            );
        }
    }
    menu
}

impl ChatView {
    /// render_options are the chat's settings, under the message box: the
    /// tool and account, the model (with its context window and effort), the
    /// mode (with plan), and more.
    pub(super) fn render_options(&self, cx: &mut Context<Self>) -> AnyElement {
        let opts = &self.session.options;
        let width = self.composer.width;
        let find = |cat: &str| {
            opts.iter()
                .find(|o| o.category == cat && o.r#type == "select")
                .cloned()
        };
        let view = cx.entity().downgrade();
        let mut row = h_flex().gap_0p5().flex_wrap().items_center().min_w_0();

        // What runs the chat: the tool's mark, its name and the account.
        let runner = self
            .composer
            .runners
            .as_ref()
            .filter(|r| !r.runners.is_empty())
            .cloned();
        let tool = runner
            .as_ref()
            .map(|r| r.tool.clone())
            .filter(|t| !t.is_empty())
            .unwrap_or_else(|| self.session.tool.clone());
        let tool_name = crate::chat::tool_label(&tool);
        let account = runner
            .as_ref()
            .map(|r| r.account.clone())
            .unwrap_or_default();
        let named_account = !account.is_empty() && account != "default";
        let tool_words: Vec<String> = [
            (width >= TOOL_NAME_AT).then(|| tool_name.clone()),
            (named_account && width >= ACCOUNT_AT).then(|| account.clone()),
        ]
        .into_iter()
        .flatten()
        .collect();
        let tool_tip = if named_account {
            tv(
                "chat.runner.labelAccount",
                &[
                    ("tool", (&tool_name).into()),
                    ("account", (&account).into()),
                ],
            )
        } else {
            tv("chat.runner.label", &[("tool", (&tool_name).into())])
        };
        let mark = crate::icons::tool_mark(&tool, px(16.), theme::muted());
        let model = find("model");
        let window_opt = find("context_window");
        let effort = find("thought_level");
        match &runner {
            Some(r) => {
                let (r, lead, view) = (r.clone(), self.is_lead(), view.clone());
                let (tool, account) = (tool.clone(), account.clone());
                row = row.child(
                    option_chip(Button::new("opt-runner"))
                        .child(chip_label(
                            Some(mark),
                            (!tool_words.is_empty()).then(|| tool_words.join(" · ")),
                            None,
                        ))
                        .tooltip(tool_tip)
                        .dropdown_menu_with_anchor(Anchor::BottomLeft, move |menu, _, _| {
                            tool_menu(menu.min_w(px(220.)), &r, &tool, &account, lead, &view)
                        }),
                );
            }
            // Nothing to switch to: the tool's mark, named.
            None => {
                let bare = model.is_none() && window_opt.is_none() && effort.is_none();
                row = row.child(
                    h_flex()
                        .id("opt-tool")
                        .h_7()
                        .px_2()
                        .gap(px(6.))
                        .rounded_lg()
                        .text_size(theme::text::UI)
                        .text_color(theme::muted())
                        .child(mark)
                        .when(bare || width >= TOOL_NAME_AT, |el| {
                            el.child(tool_name.clone())
                        })
                        .tooltip(move |w, cx| {
                            gpui_component::tooltip::Tooltip::new(tool_name.clone()).build(w, cx)
                        }),
                );
            }
        }

        // What it thinks with: model · context window · effort.
        let group: Vec<ChatOption> = [&model, &window_opt, &effort]
            .into_iter()
            .flatten()
            .cloned()
            .collect();
        if !group.is_empty() {
            let parts = summary(model.as_ref(), window_opt.as_ref(), effort.as_ref());
            let full = parts.join(" · ");
            // Narrow, the model alone; without one, effort's brain.
            let shown = if width >= SUMMARY_AT {
                full.clone()
            } else {
                model.as_ref().map(name_of).unwrap_or_default()
            };
            let brain = model
                .is_none()
                .then(|| crate::icons::ic("brain").size(px(16.)).into_any_element());
            let words = if shown.is_empty() {
                (brain.is_none()).then(|| t("chat.options.effort"))
            } else {
                Some(shown)
            };
            let view = view.clone();
            row = row.child(
                option_chip(Button::new("opt-model"))
                    .child(chip_label(brain, words, None))
                    .tooltip(tv(
                        "native.chat.options.modelTip",
                        &[(
                            "setup",
                            (if full.is_empty() {
                                t("chat.options.effort")
                            } else {
                                full
                            })
                            .into(),
                        )],
                    ))
                    .dropdown_menu_with_anchor(Anchor::BottomLeft, move |menu, window, cx| {
                        let mut menu = menu.min_w(px(220.)).max_h(px(480.)).scrollable(true);
                        for o in &group {
                            menu = section(menu, o, &view, window, cx);
                        }
                        menu
                    }),
            );
        }

        // How it may act: the permission mode, and plan first inside it.
        let plan = opts
            .iter()
            .find(|o| o.id == CHAT_OPTION_PLAN && o.r#type == "boolean")
            .cloned();
        let mode = find("mode").filter(|m| plan.is_none() || m.choices.len() > 1);
        let planning = plan.as_ref().is_some_and(|p| p.value == "true");
        if let Some(m) = mode {
            let tint = planning.then(theme::accent);
            let (icon, words) = if planning {
                ("list-checks", t("chat.options.plan"))
            } else {
                ("shield-check", name_of(&m))
            };
            let tip = if planning {
                format!("{} · {}", t("chat.options.plan"), name_of(&m))
            } else {
                name_of(&m)
            };
            let view = view.clone();
            row = row.child(
                option_chip(Button::new("opt-mode"))
                    .child(chip_label(
                        Some(crate::icons::ic(icon).size(px(16.)).into_any_element()),
                        (width >= MODE_AT).then_some(words),
                        tint,
                    ))
                    .tooltip(tv("native.chat.options.modeTip", &[("mode", tip.into())]))
                    .dropdown_menu_with_anchor(Anchor::BottomLeft, move |menu, window, cx| {
                        let mut menu = menu.min_w(px(240.)).max_h(px(480.)).scrollable(true);
                        if let Some(p) = &plan {
                            let (view, id) = (view.clone(), p.id.clone());
                            menu = menu.item(choice_item(
                                t("chat.options.plan"),
                                if planning {
                                    t("chat.options.planOnTip")
                                } else {
                                    t("chat.options.planTip")
                                },
                                planning,
                                move |cx| {
                                    let value = if planning { "false" } else { "true" }.to_string();
                                    view.update(cx, |this, cx| {
                                        this.set_option(id.clone(), value, cx)
                                    })
                                    .ok();
                                },
                            ));
                        }
                        section(menu, &m, &view, window, cx)
                    }),
            );
        } else if let Some(p) = plan {
            // Plan alone: its toggle, as before.
            row = row.child(self.plan_toggle(&p, cx));
        }

        // The rest of the tool's settings, behind one "more".
        let others: Vec<ChatOption> = opts
            .iter()
            .filter(|o| {
                o.r#type == "select"
                    && !matches!(
                        o.category.as_str(),
                        "model" | "context_window" | "thought_level" | "mode"
                    )
            })
            .cloned()
            .collect();
        if !others.is_empty() {
            let tip = others
                .iter()
                .map(|o| format!("{}: {}", o.name, name_of(o)))
                .collect::<Vec<_>>()
                .join(" · ");
            let view = view.clone();
            row = row.child(
                option_chip(Button::new("opt-more"))
                    .child(chip_label(
                        Some(
                            crate::icons::ic("sliders-horizontal")
                                .size(px(16.))
                                .into_any_element(),
                        ),
                        None,
                        None,
                    ))
                    .tooltip(tv(
                        "native.chat.options.moreTip",
                        &[("settings", tip.into())],
                    ))
                    .dropdown_menu_with_anchor(Anchor::BottomLeft, move |menu, window, cx| {
                        let mut menu = menu.min_w(px(220.)).max_h(px(480.)).scrollable(true);
                        for o in &others {
                            menu = section(menu, o, &view, window, cx);
                        }
                        menu
                    }),
            );
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
        row.into_any_element()
    }

    /// plan_toggle is plan on its own, for a tool with no mode to pick.
    fn plan_toggle(&self, p: &ChatOption, cx: &mut Context<Self>) -> AnyElement {
        let on = p.value == "true";
        let view = cx.entity().downgrade();
        let id = p.id.clone();
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
            .hover(|el| el.bg(theme::surface_strong()))
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
            })
            .into_any_element()
    }
}

#[cfg(test)]
mod tests {
    use agentbox_api::{ChatOption, ChatOptionChoice};

    use super::summary;

    fn opt(cat: &str, value: &str, choices: &[(&str, &str)]) -> ChatOption {
        ChatOption {
            id: cat.into(),
            name: cat.into(),
            category: cat.into(),
            r#type: "select".into(),
            value: value.into(),
            choices: choices
                .iter()
                .map(|(v, n)| ChatOptionChoice {
                    value: (*v).into(),
                    name: (*n).into(),
                    ..Default::default()
                })
                .collect(),
            ..Default::default()
        }
    }

    #[test]
    fn the_model_picker_reads_as_a_summary() {
        let model = opt("model", "opus", &[("opus", "Opus 4.1")]);
        let window = opt("context_window", "1m", &[("200k", "200k"), ("1m", "1M")]);
        let high = opt(
            "thought_level",
            "high",
            &[("default", "Default"), ("high", "High")],
        );
        let default = opt("thought_level", "default", &[("default", "Default")]);
        assert_eq!(
            summary(Some(&model), Some(&window), Some(&high)),
            ["Opus 4.1", "1M", "High"]
        );
        // The tool's own effort says nothing; an unknown value shows as itself.
        assert_eq!(summary(Some(&model), None, Some(&default)), ["Opus 4.1"]);
        let odd = opt("model", "gpt-9", &[]);
        assert_eq!(summary(Some(&odd), None, None), ["gpt-9"]);
    }
}
