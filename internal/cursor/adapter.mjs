// AgentBox's Agent Client Protocol adapter for Cursor, over Cursor's
// TypeScript SDK (@cursor/sdk), not the cursor-agent command line.
//
// Run with no arguments it speaks ACP on stdin and stdout, one JSON-RPC message
// a line, the way claude-agent-acp and codex-acp do: sessions are local SDK
// agents in the agent's worktree, turns are runs, and what a run streams
// (text, thinking, tool calls, todos) becomes session/update notifications.
// Run with a command it does one job for the daemon and exits: `models`,
// `login` or `check-key` (see main at the bottom).
//
// It is modelled on T3 Code's Cursor adapter (pingdotgg/t3code,
// apps/server/src/orchestration-v2/Adapters/CursorAgentSdk.ts and
// CursorAdapterV2.ts). What the SDK can't do shapes what it offers:
//
//   - No permission requests. A local run never asks its host before a tool
//     call; the closest it has is Cursor's own Auto-review classifier, which
//     decides by itself and fails a call it doesn't like (MCP calls included).
//     So the permission modes are Cursor's: auto-review, full access, and
//     plan, and the adapter never sends session/request_permission.
//   - No background tasks it reports, and no subagent sessions.
//   - No system prompt for an ordinary account (server-gated), so AgentBox's
//     brief (~/AGENTBOX.md) goes in front of a new session's first message.
//
// The SDK is found through AGENTBOX_CURSOR_SDK, the directory mise or npm
// installed @cursor/sdk into, since it isn't next to this file.

import { readFileSync, existsSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import { createInterface } from "node:readline";
import { pathToFileURL } from "node:url";

const VERSION = "1";
// What Cursor calls its "Auto" model: the account's own choice of model.
const DEFAULT_MODEL = "default";
const HOME = homedir();
const BRIEF = process.env.AGENTBOX_BRIEF || join(HOME, "AGENTBOX.md");
const MCP_CONFIG = process.env.AGENTBOX_CURSOR_MCP || join(HOME, ".cursor", "mcp.json");

// The permission modes, as one "mode" option. Their kinds are the ones
// AgentBox gives every tool's modes (acp.ConfigChoice.Kind): an autonomous
// agent is switched to the full_access one as its session starts.
const MODES = [
  { value: "auto-review", name: "Auto-review", description: "Cursor's classifier approves or refuses each tool call; nobody is asked", kind: "auto_review" },
  { value: "full-access", name: "Full access", description: "Every tool call runs: the agent's machine is the sandbox", kind: "full_access" },
  { value: "plan", name: "Plan", description: "Plans without changing anything", kind: "plan" },
];

// The model parameters that are an effort level, in the order Cursor's models
// use them (T3's cursorSdkParameterPriority).
const EFFORT_PARAMS = ["effort", "reasoning"];

export async function loadSdk() {
  const dir = process.env.AGENTBOX_CURSOR_SDK;
  if (!dir) return import("@cursor/sdk");
  for (const pkg of [dir, join(dir, "node_modules", "@cursor", "sdk"), join(dir, "lib", "node_modules", "@cursor", "sdk")]) {
    const manifest = join(pkg, "package.json");
    if (!existsSync(manifest)) continue;
    const meta = JSON.parse(readFileSync(manifest, "utf8"));
    if (meta.name !== "@cursor/sdk") continue;
    const entry = meta.exports?.["."]?.import || meta.module || meta.main || "index.js";
    return import(pathToFileURL(join(pkg, entry)).href);
  }
  throw new Error(`@cursor/sdk isn't installed under ${dir}`);
}

// ---------------------------------------------------------------------------
// JSON-RPC over stdio

class Connection {
  constructor(input, output, handlers) {
    this.output = output;
    this.handlers = handlers;
    this.lines = createInterface({ input, crlfDelay: Infinity });
    this.lines.on("line", (line) => this.receive(line));
  }

  closed() {
    return new Promise((resolve) => this.lines.on("close", resolve));
  }

  write(message) {
    this.output.write(JSON.stringify({ jsonrpc: "2.0", ...message }) + "\n");
  }

  notify(method, params) {
    this.write({ method, params });
  }

  async receive(line) {
    if (!line.trim()) return;
    let message;
    try {
      message = JSON.parse(line);
    } catch {
      this.write({ id: null, error: { code: -32700, message: "parse error" } });
      return;
    }
    if (message.method === undefined) return; // a response: this adapter asks the client nothing
    const handler = this.handlers[message.method];
    if (message.id === undefined) {
      handler?.(message.params ?? {})?.catch?.((err) => log(`${message.method}: ${describe(err)}`));
      return;
    }
    if (!handler) {
      this.write({ id: message.id, error: { code: -32601, message: `method not found: ${message.method}` } });
      return;
    }
    try {
      const result = await handler(message.params ?? {});
      this.write({ id: message.id, result: result ?? null });
    } catch (err) {
      this.write({ id: message.id, error: { code: err.code ?? -32603, message: describe(err), data: err.data } });
    }
  }
}

function log(text) {
  process.stderr.write(`cursor-acp: ${text}\n`);
}

function describe(err) {
  if (err?.name === "AuthenticationError") {
    return `Cursor refused the sign-in (${err.message}): sign in again with agentbox auth cursor, or in Settings → Accounts`;
  }
  return err?.message || String(err);
}

function rpcError(code, message) {
  return Object.assign(new Error(message), { code });
}

// ---------------------------------------------------------------------------
// What the ACP client sees

// readMcpServers reads the MCP servers AgentBox writes for Cursor in the
// cursor-agent command line's own format, {"mcpServers": {name: {command,
// args, env}}}, and adds the ones the client passed with the session.
export function mcpServers(fromClient = [], path = MCP_CONFIG) {
  const servers = {};
  try {
    const config = JSON.parse(readFileSync(path, "utf8"));
    for (const [name, server] of Object.entries(config.mcpServers ?? {})) servers[name] = server;
  } catch {
    // no file, or one this can't read: the client's servers are all there is
  }
  for (const s of fromClient) {
    if (!s?.name) continue;
    if (s.command) {
      const env = Object.fromEntries((s.env ?? []).map((v) => [v.name, v.value]));
      servers[s.name] = { type: "stdio", command: s.command, args: s.args ?? [], ...(Object.keys(env).length ? { env } : {}) };
    } else if (s.url) {
      const headers = Object.fromEntries((s.headers ?? []).map((h) => [h.name, h.value]));
      servers[s.name] = { type: s.type === "sse" ? "sse" : "http", url: s.url, ...(Object.keys(headers).length ? { headers } : {}) };
    }
  }
  return Object.keys(servers).length ? servers : undefined;
}

// effortParameter is the model's effort parameter, when it has one.
export function effortParameter(model) {
  for (const id of EFFORT_PARAMS) {
    const p = model?.parameters?.find((param) => param.id === id && param.values?.length);
    if (p) return p;
  }
  return undefined;
}

// defaultEffort is the effort the model's default variant runs at.
export function defaultEffort(model) {
  const param = effortParameter(model);
  if (!param) return undefined;
  const variant = model.variants?.find((v) => v.isDefault) ?? model.variants?.[0];
  const chosen = variant?.params?.find((p) => p.id === param.id)?.value;
  return chosen ?? param.values[0].value;
}

// configOptions are the session's settings: its model, the model's effort
// when it has levels, and the permission mode.
export function configOptions(state, models) {
  const options = [];
  const current = models.find((m) => m.id === state.model);
  const modelChoices = models.map((m) => ({ value: m.id, name: m.displayName || m.id, description: m.description ?? "" }));
  if (!modelChoices.some((c) => c.value === state.model)) {
    modelChoices.unshift({ value: state.model, name: state.model === DEFAULT_MODEL ? "Auto" : state.model, description: "" });
  }
  options.push({ id: "model", name: "Model", description: "The model Cursor runs", category: "model", type: "select", currentValue: state.model, options: modelChoices });
  const effort = effortParameter(current);
  if (effort) {
    const value = effort.values.some((v) => v.value === state.effort) ? state.effort : defaultEffort(current);
    options.push({
      id: "effort",
      name: effort.displayName || "Effort",
      description: "How hard the model thinks",
      category: "thought_level",
      type: "select",
      currentValue: value,
      options: effort.values.map((v) => ({ value: v.value, name: v.displayName || v.value, description: "" })),
    });
  }
  options.push({
    id: "mode",
    name: "Mode",
    description: "How Cursor decides whether a tool call may run",
    category: "mode",
    type: "select",
    currentValue: state.mode,
    options: MODES.map((m) => ({ value: m.value, name: m.name, description: m.description, _meta: { kind: m.kind } })),
  });
  return options;
}

// modelSelection is the SDK's { id, params } for the session's model and effort.
export function modelSelection(state, models) {
  const model = models.find((m) => m.id === state.model);
  const effort = effortParameter(model);
  if (!effort || !state.effort || !effort.values.some((v) => v.value === state.effort)) return { id: state.model };
  return { id: state.model, params: [{ id: effort.id, value: state.effort }] };
}

// userMessage turns ACP prompt blocks into the SDK's message: the text, with
// links and embedded resources written into it, and the images beside it.
export function userMessage(blocks, preface) {
  const parts = [];
  const images = [];
  for (const b of blocks ?? []) {
    switch (b.type) {
      case "text":
        parts.push(b.text ?? "");
        break;
      case "image":
        if (b.data) images.push({ data: b.data, mimeType: b.mimeType || "image/png" });
        else if (b.uri) images.push({ url: b.uri });
        break;
      case "resource_link":
        parts.push(`[${b.name || b.uri}](${b.uri})`);
        break;
      case "resource":
        if (b.resource?.text !== undefined) parts.push(`<resource uri="${b.resource.uri}">\n${b.resource.text}\n</resource>`);
        break;
    }
  }
  let text = parts.join("\n\n");
  if (preface) text = `<system-reminder>\n${preface}\n</system-reminder>\n\n${text}`;
  return images.length ? { text, images } : text;
}

const TOOL_KINDS = {
  shell: "execute",
  read: "read",
  readLints: "read",
  write: "edit",
  edit: "edit",
  delete: "delete",
  grep: "search",
  glob: "search",
  ls: "search",
  semSearch: "search",
  task: "think",
  createPlan: "think",
  updateTodos: "think",
  mcp: "other",
};

// toolCall describes a Cursor tool call as an ACP tool_call's fields.
export function toolCall(call) {
  const args = call?.args ?? {};
  const path = args.path ?? args.targetFile ?? args.filePath;
  let title = call?.type ?? "tool";
  switch (call?.type) {
    case "shell":
      title = args.command ?? "shell";
      break;
    case "read":
    case "write":
    case "edit":
    case "delete":
    case "readLints":
      title = `${call.type[0].toUpperCase()}${call.type.slice(1)} ${path ?? ""}`.trim();
      break;
    case "grep":
      title = `grep ${args.pattern ?? ""}`.trim();
      break;
    case "glob":
      title = `glob ${args.globPattern ?? args.pattern ?? ""}`.trim();
      break;
    case "ls":
      title = `ls ${path ?? args.directory ?? ""}`.trim();
      break;
    case "semSearch":
      title = `Search: ${args.query ?? ""}`.trim();
      break;
    case "mcp":
      title = [args.providerIdentifier ?? args.serverName, args.toolName ?? args.name].filter(Boolean).join(": ") || "MCP tool";
      break;
    case "task":
      title = args.description ?? "Task";
      break;
  }
  const out = { title, kind: TOOL_KINDS[call?.type] ?? "other", rawInput: args };
  if (typeof path === "string") out.locations = [{ path }];
  if (call?.type === "write" && typeof path === "string" && typeof args.fileText === "string") {
    out.content = [{ type: "diff", path, oldText: null, newText: args.fileText }];
  }
  return out;
}

// toolResult is what a finished tool call reports: its status, and its output as text.
export function toolResult(call) {
  const result = call?.result;
  if (!result) return { status: "completed" };
  if (result.status === "error") {
    const message = typeof result.error === "string" ? result.error : result.error?.message ?? JSON.stringify(result.error ?? "failed");
    return { status: "failed", content: [{ type: "content", content: { type: "text", text: message } }], rawOutput: result };
  }
  const value = result.value ?? {};
  let text;
  if (call.type === "shell") {
    text = [value.stdout, value.stderr].filter(Boolean).join("\n");
    if (value.exitCode) text += `${text ? "\n" : ""}exit code ${value.exitCode}`;
  } else if ((call.type === "edit" || call.type === "write") && typeof value.diffString === "string") {
    text = "```diff\n" + value.diffString + "\n```";
  } else if (call.type === "mcp" && Array.isArray(value.content)) {
    text = value.content.map((c) => (c.type === "text" ? c.text : `[${c.type}]`)).join("\n");
  }
  const out = { status: "completed", rawOutput: value };
  if (text) out.content = [{ type: "content", content: { type: "text", text } }];
  return out;
}

// planEntries turns Cursor's todo list into ACP plan entries.
export function planEntries(todos) {
  const statuses = { pending: "pending", in_progress: "in_progress", inProgress: "in_progress", completed: "completed", cancelled: "completed" };
  return (todos ?? []).map((t) => ({
    content: t.content ?? t.title ?? "",
    priority: "medium",
    status: statuses[t.status] ?? "pending",
  }));
}

// usage reads a run's TokenUsage into ACP's usage field and the per-model
// quota AgentBox's token ledger reads (internal/acp/usage.go).
export function promptUsage(usage, model) {
  if (!usage) return {};
  const total = usage.totalTokens || usage.inputTokens + usage.outputTokens + usage.cacheReadTokens + usage.cacheWriteTokens;
  const acp = {
    totalTokens: total,
    inputTokens: usage.inputTokens,
    outputTokens: usage.outputTokens,
    cachedReadTokens: usage.cacheReadTokens,
    cachedWriteTokens: usage.cacheWriteTokens,
    ...(usage.reasoningTokens ? { thoughtTokens: usage.reasoningTokens } : {}),
  };
  const count = {
    totalTokens: total,
    inputTokens: usage.inputTokens,
    cachedInputTokens: usage.cacheReadTokens,
    cachedWriteTokens: usage.cacheWriteTokens,
    outputTokens: usage.outputTokens,
    reasoningOutputTokens: usage.reasoningTokens ?? 0,
  };
  return { usage: acp, _meta: { quota: { token_count: count, model_usage: [{ model, token_count: count }] } } };
}

// addUsage sums the usage of a run's turns, for a run whose result has none.
function addUsage(sum, u) {
  if (!u) return sum;
  const s = sum ?? { inputTokens: 0, outputTokens: 0, cacheReadTokens: 0, cacheWriteTokens: 0, reasoningTokens: 0, totalTokens: 0 };
  s.inputTokens += u.inputTokens ?? 0;
  s.outputTokens += u.outputTokens ?? 0;
  s.cacheReadTokens += u.cacheReadTokens ?? 0;
  s.cacheWriteTokens += u.cacheWriteTokens ?? 0;
  s.reasoningTokens += u.reasoningTokens ?? 0;
  s.totalTokens = s.inputTokens + s.outputTokens + s.cacheReadTokens + s.cacheWriteTokens;
  return s;
}

// ---------------------------------------------------------------------------
// The adapter

export class Adapter {
  constructor(sdk, conn) {
    this.sdk = sdk;
    this.conn = conn;
    this.sessions = new Map();
    this.models = undefined;
  }

  handlers() {
    return {
      initialize: () => this.initialize(),
      "session/new": (p) => this.newSession(p),
      "session/resume": (p) => this.resumeSession(p),
      "session/load": (p) => this.resumeSession(p),
      "session/prompt": (p) => this.prompt(p),
      "session/cancel": (p) => this.cancel(p),
      "session/set_config_option": (p) => this.setConfigOption(p),
      "session/set_mode": (p) => this.setConfigOption({ sessionId: p.sessionId, configId: "mode", value: p.modeId }),
      "session/set_model": (p) => this.setConfigOption({ sessionId: p.sessionId, configId: "model", value: p.modelId }),
      "_session/steering": (p) => this.steer(p),
    };
  }

  initialize() {
    return {
      protocolVersion: 1,
      agentCapabilities: {
        loadSession: false,
        promptCapabilities: { image: true, embeddedContext: true },
        sessionCapabilities: { resume: {} },
      },
      agentInfo: { name: "agentbox-cursor-acp", title: "Cursor", version: VERSION },
      authMethods: [],
      _meta: { steering: { supported: true } },
    };
  }

  // listModels asks Cursor which models the account can run, once per process.
  // A failure leaves the menu with the session's own model in it rather than
  // failing the session: the run itself says whether the model works.
  async listModels() {
    if (this.models) return this.models;
    try {
      this.models = await this.sdk.Cursor.models.list();
    } catch (err) {
      if (err?.name === "AuthenticationError") throw err;
      log(`listing models: ${describe(err)}`);
      return [];
    }
    return this.models;
  }

  agentOptions(session) {
    const servers = mcpServers(session.mcpFromClient);
    return {
      model: modelSelection(session.state, session.models),
      // Cursor's "agent" or "plan"; the permission half of the mode is autoReview.
      mode: session.state.mode === "plan" ? "plan" : "agent",
      local: {
        cwd: session.cwd,
        // The worktree's own rules (AGENTS.md, .cursor/rules): what the
        // repository says about itself, as every other tool reads it.
        settingSources: ["project"],
        // The agent's machine is the sandbox, as for every AI tool AgentBox
        // runs; Cursor's own sandbox would only take the network and the
        // rest of the machine away from the agent.
        sandboxOptions: { enabled: false },
        autoReview: session.state.mode === "auto-review",
      },
      ...(servers ? { mcpServers: servers } : {}),
    };
  }

  async open(session, operation) {
    const options = this.agentOptions(session);
    session.agent = operation === "create" ? await this.sdk.Agent.create(options) : await this.sdk.Agent.resume(session.id, options);
    session.openedAs = session.state.mode === "auto-review";
    // A resumed agent remembers the model it last ran; the client sets its
    // own choice again on top if it has one.
    if (operation === "resume" && session.agent.model?.id) {
      session.state.model = session.agent.model.id;
      session.state.effort = session.agent.model.params?.find((p) => EFFORT_PARAMS.includes(p.id))?.value;
    }
    return session.agent.agentId;
  }

  async newSession(params) {
    const session = this.makeSession(params);
    session.models = await this.listModels();
    session.id = await this.open(session, "create");
    session.briefDue = true;
    this.sessions.set(session.id, session);
    return { sessionId: session.id, configOptions: configOptions(session.state, session.models), modes: this.modes(session) };
  }

  async resumeSession(params) {
    if (!params.sessionId) throw rpcError(-32602, "sessionId is required");
    const session = this.makeSession(params);
    session.id = params.sessionId;
    session.models = await this.listModels();
    try {
      await this.open(session, "resume");
    } catch (err) {
      if (err?.name === "AgentNotFoundError" || err?.name === "UnknownAgentError") {
        throw rpcError(-32002, `Cursor has no session ${params.sessionId} in this worktree: ${describe(err)}`);
      }
      throw err;
    }
    this.sessions.set(session.id, session);
    return { configOptions: configOptions(session.state, session.models), modes: this.modes(session) };
  }

  makeSession(params) {
    return {
      cwd: params.cwd || process.cwd(),
      mcpFromClient: params.mcpServers ?? [],
      state: { model: DEFAULT_MODEL, effort: undefined, mode: "auto-review" },
      models: [],
      agent: undefined,
      run: undefined,
      cancelled: false,
      briefDue: false,
    };
  }

  // modes are the same choices as the "mode" option, for clients that read
  // ACP's older session modes instead.
  modes(session) {
    return { currentModeId: session.state.mode, availableModes: MODES.map((m) => ({ id: m.value, name: m.name, description: m.description })) };
  }

  session(id) {
    const session = this.sessions.get(id);
    if (!session) throw rpcError(-32602, `no session ${id}`);
    return session;
  }

  async setConfigOption({ sessionId, configId, value }) {
    const session = this.session(sessionId);
    const v = String(value);
    switch (configId) {
      case "model": {
        session.state.model = v;
        const model = session.models.find((m) => m.id === v);
        // A level the new model doesn't have goes back to its default.
        if (!effortParameter(model)?.values.some((e) => e.value === session.state.effort)) session.state.effort = defaultEffort(model);
        break;
      }
      case "effort":
        session.state.effort = v;
        break;
      case "mode":
        if (!MODES.some((m) => m.value === v)) throw rpcError(-32602, `no mode ${v}: use ${MODES.map((m) => m.value).join(", ")}`);
        session.state.mode = v;
        break;
      default:
        throw rpcError(-32602, `no setting ${configId}`);
    }
    const options = configOptions(session.state, session.models);
    this.conn.notify("session/update", { sessionId, update: { sessionUpdate: "config_option_update", configOptions: options } });
    if (configId === "mode") this.conn.notify("session/update", { sessionId, update: { sessionUpdate: "current_mode_update", currentModeId: v } });
    return { configOptions: options };
  }

  update(sessionId, update) {
    this.conn.notify("session/update", { sessionId, update });
  }

  async prompt({ sessionId, prompt }) {
    const session = this.session(sessionId);
    if (session.run) throw rpcError(-32603, "a turn is already running in this session");
    // Auto-review is set when the agent is opened, not per run, so a mode
    // that changed it opens the agent again; the conversation carries on.
    if (session.openedAs !== (session.state.mode === "auto-review")) {
      session.agent?.close();
      await this.open(session, "resume");
    }
    let preface;
    if (session.briefDue) {
      session.briefDue = false;
      try {
        preface = readFileSync(BRIEF, "utf8").trim() || undefined;
      } catch {
        // no brief: an agent of an older AgentBox, or a session outside one
      }
    }
    const message = userMessage(prompt, preface);
    // Each tool call, and whether it has finished.
    const tools = new Map();
    let turnUsage;
    let messageId = 0;
    let inText = false;
    session.cancelled = false;
    const onDelta = ({ update }) => {
      switch (update.type) {
        case "text-delta":
          if (!inText) messageId++;
          inText = true;
          this.update(sessionId, { sessionUpdate: "agent_message_chunk", messageId: `${messageId}`, content: { type: "text", text: update.text } });
          return;
        case "thinking-delta":
          inText = false;
          this.update(sessionId, { sessionUpdate: "agent_thought_chunk", content: { type: "text", text: update.text } });
          return;
        case "tool-call-started":
        case "partial-tool-call": {
          inText = false;
          const fields = toolCall(update.toolCall);
          if (!tools.has(update.callId)) {
            tools.set(update.callId, false);
            this.update(sessionId, { sessionUpdate: "tool_call", toolCallId: update.callId, status: "in_progress", ...fields });
          } else {
            this.update(sessionId, { sessionUpdate: "tool_call_update", toolCallId: update.callId, ...fields });
          }
          return;
        }
        case "tool-call-completed": {
          inText = false;
          if (!tools.has(update.callId)) {
            this.update(sessionId, { sessionUpdate: "tool_call", toolCallId: update.callId, status: "in_progress", ...toolCall(update.toolCall) });
          }
          tools.set(update.callId, true);
          this.update(sessionId, { sessionUpdate: "tool_call_update", toolCallId: update.callId, ...toolResult(update.toolCall) });
          if (update.toolCall?.type === "updateTodos") {
            const todos = update.toolCall.args?.todos ?? update.toolCall.result?.value?.todos;
            if (todos) this.update(sessionId, { sessionUpdate: "plan", entries: planEntries(todos) });
          }
          return;
        }
        case "turn-ended":
          turnUsage = addUsage(turnUsage, update.usage);
          return;
      }
    };
    const selection = modelSelection(session.state, session.models);
    let run;
    try {
      run = await session.agent.send(message, { model: selection, mode: session.state.mode === "plan" ? "plan" : "agent", onDelta });
    } catch (err) {
      if (!/already has active run/i.test(err?.message ?? "")) throw err;
      // A run an earlier adapter process left behind (it was stopped mid-turn):
      // force expires it, the way T3 Code recovers an abandoned run.
      run = await session.agent.send(message, { model: selection, mode: session.state.mode === "plan" ? "plan" : "agent", onDelta, local: { force: true } });
    }
    session.run = run;
    let result;
    try {
      result = await run.wait();
    } catch (err) {
      if (session.cancelled || err?.name === "AbortError") return { stopReason: "cancelled" };
      throw err;
    } finally {
      session.run = undefined;
      // A tool call the run never finished (it was stopped, or failed) ends with it.
      for (const [callId, finished] of tools) {
        if (!finished) this.update(sessionId, { sessionUpdate: "tool_call_update", toolCallId: callId, status: "failed" });
      }
    }
    if (result.status === "cancelled" || session.cancelled) return { stopReason: "cancelled", ...promptUsage(result.usage ?? turnUsage, selection.id) };
    if (result.status === "error") {
      throw rpcError(-32603, `Cursor's run failed: ${result.error?.message ?? "no reason given"}`);
    }
    return { stopReason: "end_turn", ...promptUsage(result.usage ?? turnUsage, result.model?.id ?? selection.id) };
  }

  async cancel({ sessionId }) {
    const session = this.sessions.get(sessionId);
    if (!session?.run) return;
    session.cancelled = true;
    try {
      await session.run.cancel();
    } catch (err) {
      if (err?.name !== "AbortError") log(`cancelling: ${describe(err)}`);
    }
  }

  // steer puts a message into the running turn when Cursor's run takes one
  // (Run.steer). Without a running turn, or when Cursor says the turn can't
  // take it, the message is handed back for the client to send as a turn.
  async steer({ sessionId, prompt }) {
    const session = this.session(sessionId);
    const run = session.run;
    if (!run || typeof run.steer !== "function" || (run.supports && !run.supports("steer"))) {
      return { outcome: "promptRequired", reason: "no turn is running that can take it" };
    }
    const message = userMessage(prompt);
    const text = typeof message === "string" ? message : message.text;
    try {
      const outcome = await run.steer(text);
      return outcome === "complete_delivered" ? { outcome: "injected" } : { outcome: "promptRequired", reason: "the turn ended first" };
    } catch (err) {
      return { outcome: "promptRequired", reason: describe(err) };
    }
  }
}

// ---------------------------------------------------------------------------
// One-off commands for the daemon, each printing JSON lines on stdout

async function readStdin() {
  const chunks = [];
  for await (const chunk of process.stdin) chunks.push(chunk);
  return Buffer.concat(chunks).toString("utf8").trim();
}

let print = (value) => process.stdout.write(JSON.stringify(value) + "\n");

// models prints the models the stored sign-in (or CURSOR_API_KEY) can run,
// as AgentBox's menu entries: {value, name, description, efforts}.
async function models(sdk) {
  const list = await sdk.Cursor.models.list();
  print(list.map((m) => ({ value: m.id, name: m.displayName || m.id, description: m.description ?? "", efforts: effortParameter(m)?.values.map((v) => v.value) ?? [] })));
}

// login runs Cursor's browser sign-in: it prints {"loginUrl"} as soon as the
// page to open is known, then {"email"} once the browser finished and the
// minted key is saved to the store file.
async function login(sdk, storePath) {
  const result = await sdk.Cursor.auth.login({
    openBrowser: false,
    store: new sdk.FileCredentialStore(storePath),
    apiKeyName: "AgentBox",
    onLoginUrl: (url) => print({ loginUrl: url }),
  });
  print({ email: result.email ?? "", expiresAtMs: result.apiKeyExpiresAtMs });
}

// checkKey reads an API key from stdin, asks Cursor whose it is, and saves it
// to the store file in the SDK's own format, the same file a sign-in writes.
async function checkKey(sdk, storePath) {
  const apiKey = await readStdin();
  if (!apiKey) throw new Error("no API key on stdin");
  let me;
  try {
    me = await sdk.Cursor.me({ apiKey });
  } catch (err) {
    if (err?.name === "AuthenticationError") throw new Error(`Cursor doesn't accept this API key (${err.message})`);
    throw err;
  }
  await new sdk.FileCredentialStore(storePath).save({
    version: 1,
    backendUrl: process.env.CURSOR_BACKEND_URL || "https://api2.cursor.sh",
    apiKey,
    createdAtMs: Date.now(),
    ...(me.userEmail ? { email: me.userEmail } : {}),
  });
  print({ email: me.userEmail ?? "", keyName: me.apiKeyName ?? "" });
}

async function main() {
  const [command, arg] = process.argv.slice(2);
  if (command !== undefined && command !== "acp") {
    // The commands' answer is their stdout, so the SDK's logging goes elsewhere.
    const stdout = process.stdout.write.bind(process.stdout);
    process.stdout.write = process.stderr.write.bind(process.stderr);
    print = (value) => stdout(JSON.stringify(value) + "\n");
  }
  const sdk = await loadSdk();
  switch (command) {
    case undefined:
    case "acp": {
      // The SDK logs to stdout ("INFO AgentSkillsCursorRulesService ..."),
      // which is the protocol's channel: everything but the protocol's own
      // messages goes to stderr instead, where the daemon keeps it.
      const stdout = process.stdout.write.bind(process.stdout);
      process.stdout.write = process.stderr.write.bind(process.stderr);
      const conn = new Connection(process.stdin, { write: stdout }, {});
      conn.handlers = new Adapter(sdk, conn).handlers();
      await conn.closed();
      process.exit(0);
      break;
    }
    case "models":
      await models(sdk);
      break;
    case "login":
      await login(sdk, arg);
      break;
    case "check-key":
      await checkKey(sdk, arg);
      break;
    default:
      throw new Error(`unknown command ${command}: use acp, models, login or check-key`);
  }
}

if (import.meta.url === pathToFileURL(process.argv[1] ?? "").href) {
  main().catch((err) => {
    process.stderr.write(describe(err) + "\n");
    process.exit(1);
  });
}
