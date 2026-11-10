//! Merging a pull request, wherever it's offered (PullRequestsPanel.tsx's
//! usePullMerge, lib/mergeMethod.ts, ReadyToReview.tsx's split Merge): the
//! method the repository allows, remembered per project (and the first pick
//! for every project), a split button whose ▾ picks it, and the confirmation
//! the pull requests' list asks for, naming the branch it merges into.
use std::rc::Rc;

use agentbox_api::PullRequest;
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::button::{Button, ButtonCustomVariant, ButtonRounded, ButtonVariants as _};
use gpui_component::menu::{DropdownMenu as _, PopupMenuItem};
use gpui_component::{Disableable as _, Sizable as _, h_flex, v_flex};
use serde_json::Value;

use super::controls::{self, Btn, Choice};
use crate::{prefs, theme};

/// ALL is every method, in GitHub's order, for a repository that said none.
pub const ALL: &[&str] = &["merge", "squash", "rebase"];
const KEY: &str = "mergeMethod";

/// label is a method's name (memory.pulls.method.*).
pub fn label(method: &str) -> String {
    match method {
        "merge" | "squash" | "rebase" => t(&format!("memory.pulls.method.{method}")),
        m => m.to_string(),
    }
}

/// offered is the methods the repository allows, or all of them.
pub fn offered(methods: &[String]) -> Vec<String> {
    if methods.is_empty() {
        ALL.iter().map(|s| s.to_string()).collect()
    } else {
        methods.to_vec()
    }
}

/// default_method is the project's remembered method if still allowed, else
/// the global one, else the first allowed (defaultMethod).
pub fn default_method(allowed: &[String], project: &str) -> String {
    pick_from(allowed, &prefs::get(KEY).unwrap_or(Value::Null), project)
}

fn pick_from(allowed: &[String], saved: &Value, project: &str) -> String {
    let project_pick = saved
        .get("projects")
        .and_then(|p| p.get(project))
        .and_then(Value::as_str);
    let global = saved.get("global").and_then(Value::as_str);
    [project_pick, global]
        .into_iter()
        .flatten()
        .find(|m| allowed.iter().any(|a| a == m))
        .map(String::from)
        .or_else(|| allowed.first().cloned())
        .unwrap_or_else(|| "merge".into())
}

/// remember keeps a pick: the project's method, and the global one when
/// none was set yet (withPick).
pub fn remember(project: &str, method: &str) {
    let saved = prefs::get(KEY).unwrap_or(Value::Null);
    prefs::set(KEY, with_pick(saved, project, method));
}

fn with_pick(saved: Value, project: &str, method: &str) -> Value {
    let mut obj = saved.as_object().cloned().unwrap_or_default();
    if !obj.get("global").is_some_and(Value::is_string) {
        obj.insert("global".into(), method.into());
    }
    let mut projects = obj
        .get("projects")
        .and_then(Value::as_object)
        .cloned()
        .unwrap_or_default();
    projects.insert(project.into(), method.into());
    obj.insert("projects".into(), Value::Object(projects));
    Value::Object(obj)
}

/// split is Merge with its method's ▾ beside it: a click merges with the
/// method shown, the menu picks another (ticked: the one in use).
#[allow(clippy::too_many_arguments)]
pub fn split(
    id: &str,
    text: String,
    hint: Option<AnyElement>,
    method: &str,
    methods: &[String],
    disabled: bool,
    on_merge: impl Fn(&ClickEvent, &mut Window, &mut App) + 'static,
    on_pick: controls::Pick<str>,
    cx: &App,
) -> AnyElement {
    let methods: Rc<Vec<String>> = Rc::new(methods.to_vec());
    let current = method.to_string();
    let menu = Button::new(SharedString::from(format!("{id}-method")))
        .small()
        .custom(
            ButtonCustomVariant::new(cx)
                .color(theme::accent())
                .foreground(theme::on_brand())
                .hover(theme::accent().opacity(0.9))
                .active(theme::accent()),
        )
        .rounded(ButtonRounded::None)
        .h(px(30.))
        .px_1p5()
        .icon(crate::icons::ic("chevron-down"))
        .disabled(disabled)
        .dropdown_menu_with_anchor(Anchor::BottomRight, move |mut menu, _, _| {
            menu = menu.min_w(px(200.)).label(t("memory.pulls.method"));
            for m in methods.iter() {
                let (name, on) = (label(m), *m == current);
                let item = PopupMenuItem::element(move |_, _| {
                    h_flex()
                        .gap_2()
                        .child(
                            div()
                                .w(px(12.))
                                .text_sm()
                                .text_color(theme::accent())
                                .child(crate::icons::of(if on { "✓" } else { "" })),
                        )
                        .child(div().text_sm().child(name.clone()))
                });
                let (pick, value) = (on_pick.clone(), m.clone());
                menu = menu.item(item.on_click(move |_, window, cx| pick(&value, window, cx)));
            }
            menu
        });
    h_flex()
        .flex_none()
        .items_center()
        .child(
            h_flex()
                .id(SharedString::from(id.to_string()))
                .h(px(30.))
                .gap_2()
                .px_3()
                .rounded_l_md()
                .bg(theme::brand(500))
                .text_sm()
                .font_weight(FontWeight::MEDIUM)
                .text_color(theme::on_brand())
                .when(disabled, |el| el.opacity(0.45))
                .when(!disabled, |el| {
                    el.cursor_pointer()
                        .hover(|s| s.opacity(0.9))
                        .on_click(on_merge)
                })
                .child(text)
                .children(hint),
        )
        .child(
            div()
                .rounded_r_md()
                .overflow_hidden()
                .border_l_1()
                .border_color(theme::on_brand().opacity(0.25))
                .when(disabled, |el| el.opacity(0.45))
                .child(menu),
        )
        .into_any_element()
}

/// Confirm is a merge waiting to be confirmed: which, and with what.
pub struct Confirm {
    pub project: String,
    pub pr: PullRequest,
    pub method: String,
    pub methods: Vec<String>,
}

/// confirm is the merge's confirmation over the window: what merges into
/// what, whether its checks are failing or still running, and the method.
pub fn confirm(
    c: &Confirm,
    on_method: controls::Pick<str>,
    on_cancel: impl Fn(&mut Window, &mut App) + 'static,
    on_confirm: impl Fn(&ClickEvent, &mut Window, &mut App) + 'static,
) -> AnyElement {
    let pr = &c.pr;
    let mut body = tv(
        "memory.pulls.confirm.body",
        &[
            ("title", (&pr.title).into()),
            ("head", (&pr.head_branch).into()),
            ("base", (&pr.base_branch).into()),
        ],
    );
    match pr.checks.as_str() {
        "failing" => body = format!("{body} {}", t("memory.pulls.confirm.failing")),
        "pending" => body = format!("{body} {}", t("memory.pulls.confirm.pending")),
        _ => {}
    }
    let choices = c
        .methods
        .iter()
        .map(|m| Choice::new(m.clone(), label(m)))
        .collect();
    let cancel = Rc::new(on_cancel);
    let backdrop = cancel.clone();
    crate::motion::scrim_in(
        div()
            .id("merge-confirm")
            .absolute()
            .inset_0()
            .bg(theme::scrim())
            .flex()
            .items_center()
            .justify_center()
            .on_mouse_down(MouseButton::Left, move |_, window, cx| backdrop(window, cx))
            .child(
                v_flex()
                    .id("merge-confirm-card")
                    .w(px(460.))
                    .max_w(relative(0.92))
                    .gap_4()
                    .p_6()
                    .rounded_xl()
                    .bg(theme::card())
                    .border_1()
                    .border_color(theme::line())
                    .shadow_lg()
                    .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
                    .child(
                        div()
                            .text_lg()
                            .font_weight(FontWeight::SEMIBOLD)
                            .text_color(theme::primary())
                            .child(tv(
                                "memory.pulls.confirm.title",
                                &[("number", pr.number.into())],
                            )),
                    )
                    .child(div().text_sm().text_color(theme::muted()).child(body))
                    .child(controls::labeled(
                        t("memory.pulls.method"),
                        controls::select_at(
                            "merge-confirm-method",
                            choices,
                            &c.method,
                            false,
                            on_method,
                            Anchor::TopLeft,
                        ),
                    ))
                    .child(
                        h_flex()
                            .gap_2()
                            .justify_end()
                            .child(controls::button(
                                "merge-confirm-cancel",
                                t("common.cancel"),
                                Btn::Ghost,
                                false,
                                move |_, w, cx| cancel(w, cx),
                            ))
                            .child(controls::button(
                                "merge-confirm-go",
                                t("memory.pulls.merge"),
                                Btn::Primary,
                                false,
                                on_confirm,
                            )),
                    ),
            ),
        "idmerge-confirm-in",
    )
    .into_any_element()
}

#[cfg(test)]
mod tests {
    use super::{ALL, Value, pick_from, with_pick};

    fn all() -> Vec<String> {
        ALL.iter().map(|s| s.to_string()).collect()
    }

    #[test]
    fn the_project_then_the_global_then_the_first() {
        assert_eq!(pick_from(&all(), &Value::Null, "p"), "merge");
        let saved = serde_json::json!({"global": "squash", "projects": {"p": "rebase"}});
        assert_eq!(pick_from(&all(), &saved, "p"), "rebase");
        assert_eq!(pick_from(&all(), &saved, "other"), "squash");
        let only = vec!["squash".to_string(), "rebase".to_string()];
        let saved = serde_json::json!({"global": "merge", "projects": {"p": "merge"}});
        assert_eq!(pick_from(&only, &saved, "p"), "squash");
    }

    #[test]
    fn the_first_pick_is_global_too() {
        let first = with_pick(Value::Null, "a", "squash");
        assert_eq!(
            first,
            serde_json::json!({"global": "squash", "projects": {"a": "squash"}})
        );
        let second = with_pick(first, "b", "rebase");
        assert_eq!(
            second,
            serde_json::json!({"global": "squash", "projects": {"a": "squash", "b": "rebase"}})
        );
    }
}
