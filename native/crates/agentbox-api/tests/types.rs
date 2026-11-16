//! The generated types read what the daemon writes: Go's JSON, where a nil
//! slice or map is null, an omitempty field is missing, and a newer daemon may
//! send fields this client doesn't know.
use agentbox_api::*;

#[test]
fn reads_a_real_daemons_answers() {
    let t: ChatThread = serde_json::from_str(include_str!("testdata/lead-chat.json")).unwrap();
    assert_eq!(t.agent, "demo/lead");
    assert!(
        t.items
            .iter()
            .any(|i| i.kind == "assistant" && !i.text.is_empty())
    );
    let p: Vec<Project> = serde_json::from_str(include_str!("testdata/projects.json")).unwrap();
    assert_eq!(p[0].name, "demo");
}

#[test]
fn null_missing_and_unknown_fields() {
    let a: FleetAgent = serde_json::from_str(
        r#"{"ref":"p/agent-01","state":"running","background":null,"openCodeAccounts":null,"somethingNew":1}"#,
    )
    .unwrap();
    assert_eq!(a.r#ref, "p/agent-01");
    assert!(a.background.is_empty() && a.open_code_accounts.is_empty() && a.pr.is_none());
}

#[test]
fn omitempty_fields_are_left_out() {
    let body = serde_json::to_value(ChatMessageRequest {
        text: "hi".into(),
        ..Default::default()
    })
    .unwrap();
    assert_eq!(body, serde_json::json!({"text": "hi"}));
}
