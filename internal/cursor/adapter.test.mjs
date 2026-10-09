// Tests for adapter.mjs, against a fake SDK: run with `node --test`
// (cursor_test.go does, when node is on the PATH).

import assert from "node:assert/strict";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";

import { Adapter, configOptions, effortParameter, mcpServers, modelSelection, planEntries, promptUsage, toolCall, toolResult, userMessage } from "./adapter.mjs";

const opus = {
  id: "claude-opus-5-5",
  displayName: "Claude Opus 5.5",
  parameters: [{ id: "effort", values: [{ value: "low" }, { value: "high" }] }],
  variants: [{ params: [{ id: "effort", value: "high" }], displayName: "Opus high", isDefault: true }],
};
const composer = { id: "composer-2", displayName: "Composer 2" };

test("the model menu, the effort of the model running, and the modes", () => {
  const options = configOptions({ model: "claude-opus-5-5", mode: "auto-review" }, [composer, opus]);
  assert.deepEqual(options.map((o) => [o.id, o.category, o.currentValue]), [
    ["model", "model", "claude-opus-5-5"],
    ["effort", "thought_level", "high"], // the default variant's
    ["mode", "mode", "auto-review"],
  ]);
  assert.deepEqual(options[2].options.map((o) => o._meta.kind), ["auto_review", "full_access", "plan"]);
  // A model without levels has no effort option.
  assert.deepEqual(configOptions({ model: "composer-2", mode: "plan" }, [composer, opus]).map((o) => o.id), ["model", "mode"]);
  // A model Cursor didn't list (or no list at all) is still on the menu.
  assert.equal(configOptions({ model: "default", mode: "plan" }, [])[0].options[0].name, "Auto");
});

test("the SDK's model selection carries the effort only when the model takes it", () => {
  assert.deepEqual(modelSelection({ model: "claude-opus-5-5", effort: "low" }, [opus]), { id: "claude-opus-5-5", params: [{ id: "effort", value: "low" }] });
  assert.deepEqual(modelSelection({ model: "claude-opus-5-5", effort: "max" }, [opus]), { id: "claude-opus-5-5" });
  assert.deepEqual(modelSelection({ model: "composer-2", effort: "low" }, [composer]), { id: "composer-2" });
  assert.equal(effortParameter({ parameters: [{ id: "reasoning", values: [{ value: "x" }] }] }).id, "reasoning");
});

test("a prompt's blocks become the SDK's message, with the brief in front", () => {
  assert.equal(userMessage([{ type: "text", text: "hi" }]), "hi");
  assert.deepEqual(userMessage([{ type: "text", text: "look" }, { type: "image", data: "AAA", mimeType: "image/jpeg" }]), {
    text: "look",
    images: [{ data: "AAA", mimeType: "image/jpeg" }],
  });
  assert.equal(userMessage([{ type: "resource_link", name: "a.go", uri: "file:///a.go" }]), "[a.go](file:///a.go)");
  assert.equal(userMessage([{ type: "text", text: "go" }], "BRIEF"), "<system-reminder>\nBRIEF\n</system-reminder>\n\ngo");
});

test("tool calls read as ACP's", () => {
  assert.deepEqual(toolCall({ type: "shell", args: { command: "ls" } }), { title: "ls", kind: "execute", rawInput: { command: "ls" } });
  const write = toolCall({ type: "write", args: { path: "/w/a.txt", fileText: "x" } });
  assert.equal(write.kind, "edit");
  assert.deepEqual(write.locations, [{ path: "/w/a.txt" }]);
  assert.deepEqual(write.content, [{ type: "diff", path: "/w/a.txt", oldText: null, newText: "x" }]);
  assert.equal(toolCall({ type: "mcp", args: { providerIdentifier: "memory", toolName: "report" } }).title, "memory: report");
  assert.equal(toolCall({ type: "somethingNew", args: {} }).kind, "other");

  const ran = toolResult({ type: "shell", result: { status: "success", value: { stdout: "hi", stderr: "", exitCode: 1 } } });
  assert.equal(ran.status, "completed");
  assert.equal(ran.content[0].content.text, "hi\nexit code 1");
  assert.equal(toolResult({ type: "read", result: { status: "error", error: "no such file" } }).status, "failed");
  assert.equal(toolResult({ type: "edit", result: { status: "success", value: { diffString: "+x" } } }).content[0].content.text, "```diff\n+x\n```");
});

test("todos become a plan", () => {
  assert.deepEqual(planEntries([{ content: "a", status: "completed" }, { content: "b", status: "in_progress" }, { content: "c" }]), [
    { content: "a", priority: "medium", status: "completed" },
    { content: "b", priority: "medium", status: "in_progress" },
    { content: "c", priority: "medium", status: "pending" },
  ]);
});

test("usage is ACP's field and the per-model quota AgentBox's ledger reads", () => {
  const out = promptUsage({ inputTokens: 10, outputTokens: 2, cacheReadTokens: 5, cacheWriteTokens: 1, totalTokens: 18, reasoningTokens: 1 }, "composer-2");
  assert.deepEqual(out.usage, { totalTokens: 18, inputTokens: 10, outputTokens: 2, cachedReadTokens: 5, cachedWriteTokens: 1, thoughtTokens: 1 });
  assert.equal(out._meta.quota.model_usage[0].model, "composer-2");
  assert.equal(out._meta.quota.model_usage[0].token_count.cachedInputTokens, 5);
  assert.deepEqual(promptUsage(undefined, "x"), {});
});

test("MCP servers come from ~/.cursor/mcp.json and the client", () => {
  const dir = mkdtempSync(join(tmpdir(), "cursor-acp-"));
  const path = join(dir, "mcp.json");
  writeFileSync(path, JSON.stringify({ mcpServers: { memory: { type: "stdio", command: "/usr/local/bin/agentbox", args: ["memory", "mcp"] } } }));
  const servers = mcpServers([{ name: "extra", command: "x", args: ["y"], env: [{ name: "K", value: "V" }] }], path);
  assert.deepEqual(Object.keys(servers), ["memory", "extra"]);
  assert.deepEqual(servers.extra, { type: "stdio", command: "x", args: ["y"], env: { K: "V" } });
  assert.equal(mcpServers([], join(dir, "missing.json")), undefined);
});

// A fake @cursor/sdk: an agent whose runs stream the deltas a test gives it.
function fakeSdk(script) {
  const calls = { create: [], resume: [], send: [] };
  const agent = (id) => ({
    agentId: id,
    model: { id: "claude-opus-5-5", params: [{ id: "effort", value: "low" }] },
    close() {},
    async send(message, options) {
      calls.send.push({ message, options });
      let cancelled = false;
      let finish;
      const done = new Promise((resolve) => (finish = resolve));
      const run = {
        id: "run-1",
        agentId: id,
        supports: () => true,
        async cancel() {
          cancelled = true;
          finish({ id: "run-1", status: "cancelled" });
        },
        async steer() {
          return "complete_delivered";
        },
        wait: () => done,
      };
      queueMicrotask(async () => {
        for (const update of script.updates ?? []) await options.onDelta({ update });
        if (!script.hang && !cancelled) finish(script.result ?? { id: "run-1", status: "finished" });
      });
      return run;
    },
  });
  return {
    calls,
    Cursor: { models: { list: async () => [composer, opus] } },
    Agent: {
      create: async (options) => (calls.create.push(options), agent("agent-new")),
      resume: async (id, options) => (calls.resume.push({ id, options }), agent(id)),
    },
  };
}

function harness(script) {
  const sent = [];
  const conn = { notify: (method, params) => sent.push({ method, params }) };
  const sdk = fakeSdk(script);
  return { sdk, sent, adapter: new Adapter(sdk, conn), updates: () => sent.map((m) => m.params.update) };
}

test("a turn streams text, thinking and tool calls, and reports its usage", async () => {
  process.env.AGENTBOX_CURSOR_MCP = "/nonexistent";
  const { adapter, sdk, updates } = harness({
    updates: [
      { type: "thinking-delta", text: "hmm" },
      { type: "tool-call-started", callId: "c1", toolCall: { type: "shell", args: { command: "ls" } } },
      { type: "tool-call-completed", callId: "c1", toolCall: { type: "shell", args: { command: "ls" }, result: { status: "success", value: { stdout: "a" } } } },
      { type: "tool-call-started", callId: "c2", toolCall: { type: "read", args: { path: "/x" } } },
      { type: "text-delta", text: "Done" },
      { type: "turn-ended", usage: { inputTokens: 3, outputTokens: 1, cacheReadTokens: 0, cacheWriteTokens: 0 } },
    ],
  });
  const session = await adapter.newSession({ cwd: "/w", mcpServers: [] });
  assert.equal(session.sessionId, "agent-new");
  assert.equal(sdk.calls.create[0].local.cwd, "/w");
  assert.equal(sdk.calls.create[0].local.autoReview, true);
  assert.equal(sdk.calls.create[0].local.sandboxOptions.enabled, false);

  const result = await adapter.prompt({ sessionId: "agent-new", prompt: [{ type: "text", text: "go" }] });
  assert.equal(result.stopReason, "end_turn");
  assert.equal(result.usage.totalTokens, 4);
  const kinds = updates().map((u) => u.sessionUpdate + (u.status ? ":" + u.status : ""));
  assert.deepEqual(kinds, [
    "agent_thought_chunk",
    "tool_call:in_progress",
    "tool_call_update:completed",
    "tool_call:in_progress",
    "agent_message_chunk",
    "tool_call_update:failed", // c2 never finished
  ]);
});

test("changing the mode to full access opens the agent again without auto-review", async () => {
  const { adapter, sdk } = harness({});
  await adapter.newSession({ cwd: "/w" });
  await adapter.setConfigOption({ sessionId: "agent-new", configId: "mode", value: "full-access" });
  await adapter.setConfigOption({ sessionId: "agent-new", configId: "model", value: "claude-opus-5-5" });
  await adapter.setConfigOption({ sessionId: "agent-new", configId: "effort", value: "low" });
  await adapter.prompt({ sessionId: "agent-new", prompt: [{ type: "text", text: "go" }] });
  assert.equal(sdk.calls.resume.length, 1);
  assert.equal(sdk.calls.resume[0].options.local.autoReview, false);
  assert.deepEqual(sdk.calls.send[0].options.model, { id: "claude-opus-5-5", params: [{ id: "effort", value: "low" }] });
  assert.equal(sdk.calls.send[0].options.mode, "agent");
  await assert.rejects(adapter.setConfigOption({ sessionId: "agent-new", configId: "mode", value: "yolo" }), /no mode yolo/);
});

test("a resumed session takes the model it last ran", async () => {
  const { adapter } = harness({});
  const resumed = await adapter.resumeSession({ sessionId: "agent-old", cwd: "/w" });
  assert.equal(resumed.configOptions.find((o) => o.id === "model").currentValue, "claude-opus-5-5");
  assert.equal(resumed.configOptions.find((o) => o.id === "effort").currentValue, "low");
});

test("cancel stops the run and the turn says so", async () => {
  const { adapter } = harness({ hang: true });
  await adapter.newSession({ cwd: "/w" });
  const turn = adapter.prompt({ sessionId: "agent-new", prompt: [{ type: "text", text: "go" }] });
  await new Promise((r) => setTimeout(r, 10));
  const steered = await adapter.steer({ sessionId: "agent-new", prompt: [{ type: "text", text: "also this" }] });
  assert.deepEqual(steered, { outcome: "injected" });
  await adapter.cancel({ sessionId: "agent-new" });
  assert.equal((await turn).stopReason, "cancelled");
  assert.equal((await adapter.steer({ sessionId: "agent-new", prompt: [] })).outcome, "promptRequired");
});

test("a failed run is an error", async () => {
  const { adapter } = harness({ result: { id: "run-1", status: "error", error: { message: "model unavailable" } } });
  await adapter.newSession({ cwd: "/w" });
  await assert.rejects(adapter.prompt({ sessionId: "agent-new", prompt: [{ type: "text", text: "go" }] }), /model unavailable/);
});
