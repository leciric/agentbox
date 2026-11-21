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

#[test]
fn reads_run_slots_as_the_daemon_writes_them() {
    // GET /v1/run-slots, with nobody holding or waiting: Go's nil slices.
    let idle: RunSlots = serde_json::from_str(
        r#"{"total":1,"free":1,"inUse":0,"queued":0,"slotSize":3435973837,"pool":12884901888,
            "codeUse":0,"freeMemory":12884901888,"holders":null,"queue":null,"projects":null,
            "somethingNew":true}"#,
    )
    .unwrap();
    assert_eq!(
        (idle.total, idle.free, idle.slot_size),
        (1, 1, 3_435_973_837)
    );
    assert!(idle.holders.is_empty() && idle.queue.is_empty() && idle.projects.is_empty());

    let busy: RunSlots = serde_json::from_str(
        r#"{"total":2,"free":0,"inUse":2,"queued":1,
            "holders":[{"agent":"demo/agent-01","project":"demo","since":"2026-11-20T12:00:00Z",
                        "expires":"2026-11-20T12:05:00Z","reserved":3435973837,"use":2147483648,"peak":2400000000}],
            "queue":[{"agent":"demo/agent-03","project":"demo","ahead":0,"since":"2026-11-20T12:02:00Z"}],
            "projects":[{"project":"demo","peak":3435973837,"learned":true,"runs":7,"free":0,"inUse":2,"queued":1}]}"#,
    )
    .unwrap();
    assert_eq!(busy.holders[0].r#use, 2_147_483_648);
    assert_eq!(busy.holders[0].expires, "2026-11-20T12:05:00Z");
    assert_eq!(
        (busy.queue[0].agent.as_str(), busy.queue[0].ahead),
        ("demo/agent-03", 0)
    );
    assert!(busy.projects[0].learned && busy.projects[0].in_use == 2);
}

#[test]
fn an_agent_says_where_it_stands_for_a_run_slot() {
    let waiting: FleetAgent = serde_json::from_str(
        r#"{"ref":"p/agent-03","state":"running","runSlot":{"state":"waiting","ahead":2,"since":"2026-11-20T12:02:00Z"}}"#,
    )
    .unwrap();
    let slot = waiting.run_slot.unwrap();
    assert_eq!(
        (slot.state.as_str(), slot.ahead, slot.expires),
        (RUN_SLOT_WAITING, 2, None)
    );
    // Holding one, the agent event says so; having none, the field is left out.
    let change: AgentChange = serde_json::from_str(
        r#"{"ref":"p/agent-01","state":"running","runSlot":{"state":"holding","since":"2026-11-20T12:00:00Z","expires":"2026-11-20T12:05:00Z"}}"#,
    )
    .unwrap();
    assert_eq!(change.run_slot.unwrap().state, RUN_SLOT_HOLDING);
    let none: AgentChange =
        serde_json::from_str(r#"{"ref":"p/agent-01","state":"stopped"}"#).unwrap();
    assert!(none.run_slot.is_none());
    assert_eq!(EVENT_RUN_SLOTS, "run-slots");
}
