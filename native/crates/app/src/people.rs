//! The people of a shared project (#822; the Electron app's lib/people.ts,
//! lib/here.ts and components/People.tsx): how a person looks (their colour,
//! one of the session hues picked by their name so it is the same on every
//! AgentBox, and their initials), and where they are, as avatars and lines.
//! Desktops and terminals stay shared: being there is only shown, nothing is
//! locked. Who is where comes from the daemon (app/here.rs keeps it); this is
//! only how it is drawn, so the chat and the app draw it alike.
use std::cell::Cell;
use std::time::{Duration, Instant};

use agentbox_api::PersonHere;
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::h_flex;
use gpui_component::tooltip::Tooltip;

use crate::theme;

/// TYPING is how long after the last key a message is still being typed.
pub const TYPING: Duration = Duration::from_secs(4);

thread_local! {
    static TYPING_UNTIL: Cell<Option<Instant>> = const { Cell::new(None) };
}

/// note_typing says a key was pressed in the composer of what is on screen
/// (lib/here.ts noteTyping); app/here.rs tells the daemon.
pub fn note_typing() {
    TYPING_UNTIL.with(|c| c.set(Some(Instant::now() + TYPING)));
}

/// typing is whether this window's user is writing a message now.
pub fn typing() -> bool {
    TYPING_UNTIL.with(|c| c.get().is_some_and(|until| Instant::now() < until))
}

/// stop_typing forgets the typing, as moving elsewhere does.
pub fn stop_typing() {
    TYPING_UNTIL.with(|c| c.set(None));
}

/// color_name is the hue a person shows in (lib/people.ts personColor): the
/// same for the same name, whatever its case or spaces.
pub fn color_name(name: &str) -> &'static str {
    let mut h: u32 = 0;
    for ch in name.trim().to_lowercase().chars() {
        h = h.wrapping_mul(31).wrapping_add(ch as u32);
    }
    theme::SESSION_HUES[h as usize % theme::SESSION_HUES.len()]
}

/// color is a person's colour, painted.
pub fn color(name: &str) -> Hsla {
    theme::session_hue(color_name(name))
}

/// initials are the first letters of a name's first two words, with a
/// possessive dropped ("Alice's AgentBox" is AA, "bob-laptop" BL).
pub fn initials(name: &str) -> String {
    let chars: Vec<char> = name.chars().collect();
    let mut kept = String::new();
    let mut i = 0;
    while i < chars.len() {
        let possessive = matches!(chars[i], '\'' | '’')
            && chars
                .get(i + 1)
                .is_some_and(|c| c.eq_ignore_ascii_case(&'s'))
            && chars
                .get(i + 2)
                .is_none_or(|c| !(c.is_alphanumeric() || *c == '_'));
        if possessive {
            i += 2;
            continue;
        }
        kept.push(chars[i]);
        i += 1;
    }
    let words: Vec<&str> = kept
        .split(|c: char| c.is_whitespace() || matches!(c, '.' | '_' | '-'))
        .filter(|w| !w.is_empty())
        .collect();
    let first = |w: &str| w.chars().next().map(String::from).unwrap_or_default();
    match words.as_slice() {
        [] => "?".into(),
        [one] => first(one).to_uppercase(),
        [a, b, ..] => format!("{}{}", first(a), first(b)).to_uppercase(),
    }
}

/// avatar is a person's initials on their colour (PersonAvatar): 16 px
/// ("xs") or 20 px, ringed in the chat's ground so a row of them overlaps
/// cleanly.
pub fn avatar(name: &str, size: f32) -> Div {
    div()
        .flex_none()
        .size(px(size))
        .flex()
        .items_center()
        .justify_center()
        .rounded_full()
        .border(px(1.5))
        .border_color(theme::chat())
        .bg(color(name))
        .text_color(theme::on_bright())
        .text_size(px(if size < 18. { 8.5 } else { 10. }))
        .line_height(px(size - 3.))
        .font_weight(FontWeight::SEMIBOLD)
        .child(initials(name))
}

/// avatars is a row of people, overlapping, the first few and how many more
/// (Avatars), with everyone's name in its tooltip; None for nobody.
pub fn avatars(
    id: impl Into<ElementId>,
    people: &[PersonHere],
    max: usize,
    size: f32,
) -> Option<AnyElement> {
    if people.is_empty() {
        return None;
    }
    let names: SharedString = people
        .iter()
        .map(|p| p.name.as_str())
        .collect::<Vec<_>>()
        .join(", ")
        .into();
    let more = people.len().saturating_sub(max);
    Some(
        h_flex()
            .id(id)
            .flex_none()
            .children(
                people
                    .iter()
                    .take(max)
                    .enumerate()
                    .map(|(i, p)| avatar(&p.name, size).when(i > 0, |el| el.ml(px(-6.)))),
            )
            .when(more > 0, |el| {
                el.child(
                    div()
                        .ml_1()
                        .text_size(theme::text::CAPTION)
                        .text_color(theme::subtle())
                        .child(tv("people.more", &[("count", more.into())])),
                )
            })
            .tooltip(move |w, cx| Tooltip::new(names.clone()).build(w, cx))
            .into_any_element(),
    )
}

/// where_line says where a person is (useWhere): on an agent's desktop or
/// terminal, on an agent, in the chat, or in the project.
pub fn where_line(p: &PersonHere) -> String {
    let who = [("name", (&p.name).into()), ("agent", (&p.agent).into())];
    if !p.agent.is_empty() && p.tab == "desktop" {
        tv("people.onDesktop", &who)
    } else if !p.agent.is_empty() && p.tab == "terminal" {
        tv("people.inTerminal", &who)
    } else if !p.agent.is_empty() {
        tv("people.onAgent", &who)
    } else if !p.session.is_empty() {
        tv("people.inChat", &who)
    } else {
        tv("people.inProject", &who)
    }
}

/// on_agent is the people on one agent of a project, by its name.
pub fn on_agent<'a>(people: &'a [PersonHere], agent: &str) -> Vec<&'a PersonHere> {
    people.iter().filter(|p| p.agent == agent).collect()
}

/// here_people is a project's (or one of its agents') people as avatars with
/// where each one is in the tooltip, and its access when the AgentBox that
/// shares it lets this one only look or chat (HerePeople, beside the
/// project's title).
pub fn here_people(
    project: &agentbox_api::Project,
    people: &[PersonHere],
    agent: Option<&str>,
) -> Option<AnyElement> {
    let shown: Vec<PersonHere> = match agent {
        Some(a) => on_agent(people, a).into_iter().cloned().collect(),
        None => people.to_vec(),
    };
    let access = project
        .remote
        .as_ref()
        .map(|r| r.access.as_str())
        .filter(|a| *a == "view" || *a == "chat");
    if shown.is_empty() && access.is_none() {
        return None;
    }
    let lines: SharedString = shown
        .iter()
        .map(where_line)
        .collect::<Vec<_>>()
        .join("\n")
        .into();
    let owner = project
        .remote
        .as_ref()
        .map(|r| r.owner.clone())
        .unwrap_or_default();
    Some(
        h_flex()
            .flex_none()
            .gap_2()
            .when(!shown.is_empty(), |el| {
                el.child(
                    h_flex()
                        .id(SharedString::from(format!("here-{}", project.name)))
                        .children(
                            shown.iter().take(3).enumerate().map(|(i, p)| {
                                avatar(&p.name, 20.).when(i > 0, |el| el.ml(px(-6.)))
                            }),
                        )
                        .when(shown.len() > 3, |el| {
                            el.child(
                                div()
                                    .ml_1()
                                    .text_size(theme::text::CAPTION)
                                    .text_color(theme::subtle())
                                    .child(tv(
                                        "people.more",
                                        &[("count", (shown.len() - 3).into())],
                                    )),
                            )
                        })
                        .tooltip(move |w, cx| Tooltip::new(lines.clone()).build(w, cx)),
                )
            })
            .when_some(access, |el, access| {
                let tip: SharedString = if access == "view" {
                    tv("people.viewOnlyNote", &[("owner", (&owner).into())])
                } else {
                    t("peers.access.chat")
                }
                .into();
                el.child(
                    div()
                        .id(SharedString::from(format!("access-{}", project.name)))
                        .flex_none()
                        .px_2()
                        .rounded_full()
                        .border_1()
                        .border_color(theme::line())
                        .text_size(theme::text::MICRO)
                        .text_color(theme::subtle())
                        .tooltip(move |w, cx| Tooltip::new(tip.clone()).build(w, cx))
                        .child(if access == "view" {
                            t("people.viewOnly")
                        } else {
                            t("people.chatOnly")
                        }),
                )
            })
            .into_any_element(),
    )
}

/// session_of_ref is the session of a project's chat a lead's ref names
/// (api.ts sessionOfRef): "main" for "web/lead", "s2" for "web/lead.s2",
/// None for an agent's ref.
pub fn session_of_ref(r: &str) -> Option<&str> {
    let (_, name) = r.split_once('/')?;
    if name == "lead" {
        return Some("main");
    }
    name.strip_prefix("lead.")
}

/// in_chat is who of a project's people is in the chat with the ref: the
/// same session of the project's chat (main also holds whoever is in the
/// project with nothing open), or an agent's own chat (ChatPeople's here).
pub fn in_chat<'a>(chat: &str, people: &'a [PersonHere]) -> Vec<&'a PersonHere> {
    let name = chat.split_once('/').map(|(_, n)| n).unwrap_or("");
    match session_of_ref(chat) {
        Some(session) => people
            .iter()
            .filter(|p| {
                if p.session.is_empty() {
                    p.agent.is_empty() && session == "main"
                } else {
                    p.session == session
                }
            })
            .collect(),
        None => people
            .iter()
            .filter(|p| {
                p.session.is_empty() && p.agent == name && (p.tab.is_empty() || p.tab == "chat")
            })
            .collect(),
    }
}

/// chat_people, above a chat's composer, is who is typing in it and where
/// everyone else in its project is (ChatPeople); None for nobody.
pub fn chat_people(chat: &str, people: &[PersonHere]) -> Option<AnyElement> {
    if people.is_empty() {
        return None;
    }
    let here = in_chat(chat, people);
    let typing: Vec<&PersonHere> = here.iter().copied().filter(|p| p.typing).collect();
    let rest: Vec<&PersonHere> = people
        .iter()
        .filter(|p| !p.typing || !here.iter().any(|h| h.id == p.id))
        .collect();
    let typing_line = (!typing.is_empty()).then(|| {
        let text = if typing.len() == 1 {
            tv("people.typingOne", &[("name", (&typing[0].name).into())])
        } else {
            let names = typing
                .iter()
                .map(|p| p.name.as_str())
                .collect::<Vec<_>>()
                .join(", ");
            tv("people.typingMany", &[("names", (&names).into())])
        };
        h_flex()
            .min_w_0()
            .gap_1p5()
            .text_size(theme::text::CAPTION)
            .text_color(theme::subtle())
            .child(
                h_flex().flex_none().children(
                    typing
                        .iter()
                        .take(3)
                        .enumerate()
                        .map(|(i, p)| avatar(&p.name, 16.).when(i > 0, |el| el.ml(px(-6.)))),
                ),
            )
            .child(crate::motion::pulse(
                div().min_w_0().truncate().child(text),
                SharedString::from(format!("typing-{chat}")),
            ))
    });
    Some(
        gpui_component::v_flex()
            .mb_1p5()
            .px_2()
            .gap_1()
            .min_w_0()
            .children(typing_line)
            .when(!rest.is_empty(), |el| {
                el.child(h_flex().min_w_0().flex_wrap().gap_x_3().gap_y_1().children(
                    rest.into_iter().map(|p| {
                        h_flex()
                            .min_w_0()
                            .gap_1p5()
                            .text_size(theme::text::CAPTION)
                            .text_color(theme::subtle())
                            .child(avatar(&p.name, 16.))
                            .child(div().min_w_0().truncate().child(where_line(p)))
                    }),
                ))
            })
            .into_any_element(),
    )
}

#[cfg(test)]
mod tests {
    use super::{color_name, in_chat, initials, session_of_ref};
    use crate::theme;
    use agentbox_api::PersonHere;

    fn here(id: &str, session: &str, agent: &str, tab: &str, typing: bool) -> PersonHere {
        PersonHere {
            id: id.into(),
            name: id.into(),
            project: "r1~web".into(),
            session: session.into(),
            agent: agent.into(),
            tab: tab.into(),
            typing,
            since: String::new(),
        }
    }

    #[test]
    fn a_person_keeps_one_colour_for_one_name_whatever_its_case() {
        assert_eq!(color_name("Ana"), color_name(" ana "));
        assert!(theme::SESSION_HUES.contains(&color_name("Bob")));
    }

    #[test]
    fn initials_as_the_electron_app_makes_them() {
        assert_eq!(initials("Ana"), "A");
        assert_eq!(initials("Alice's AgentBox"), "AA");
        assert_eq!(initials("Alice’S laptop"), "AL");
        assert_eq!(initials("bob-laptop"), "BL");
        assert_eq!(initials("bobs"), "B");
        assert_eq!(initials(""), "?");
    }

    #[test]
    fn the_session_a_lead_ref_names() {
        assert_eq!(session_of_ref("web/lead"), Some("main"));
        assert_eq!(session_of_ref("web/lead.s2"), Some("s2"));
        assert_eq!(session_of_ref("web/agent-01"), None);
    }

    #[test]
    fn who_is_in_which_chat() {
        let people = [
            here("main", "", "", "", false),
            here("s2", "s2", "", "", true),
            here("desk", "", "agent-01", "desktop", false),
            here("chat", "", "agent-01", "chat", true),
        ];
        let ids = |v: Vec<&PersonHere>| v.into_iter().map(|p| p.id.clone()).collect::<Vec<_>>();
        assert_eq!(ids(in_chat("r1~web/lead", &people)), ["main"]);
        assert_eq!(ids(in_chat("r1~web/lead.s2", &people)), ["s2"]);
        assert_eq!(ids(in_chat("r1~web/agent-01", &people)), ["chat"]);
    }
}
