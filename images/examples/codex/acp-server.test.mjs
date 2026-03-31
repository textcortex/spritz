import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { spawn } from "node:child_process";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { CodexACPRuntime } from "./acp-server.mjs";

const TEST_DIR = path.dirname(fileURLToPath(import.meta.url));

function once(child, event) {
  if (event === "exit" && child.exitCode !== null) {
    return Promise.resolve([child.exitCode, child.signalCode]);
  }
  return new Promise((resolve) => child.once(event, resolve));
}

function onceEvent(target, event, timeoutMs = 5000) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      cleanup();
      reject(new Error(`timed out waiting for ${event}`));
    }, timeoutMs);
    const cleanup = () => {
      clearTimeout(timer);
      target.removeEventListener?.(event, onEvent);
      target.removeEventListener?.("error", onError);
    };
    const onEvent = (value) => {
      cleanup();
      resolve(value);
    };
    const onError = (error) => {
      cleanup();
      reject(error);
    };
    target.addEventListener?.(event, onEvent, { once: true });
    target.addEventListener?.("error", onError, { once: true });
  });
}

class FakeSocket {
  constructor() {
    this.handlers = new Map();
    this.readyState = 1;
    this.sent = [];
    this.closeCode = null;
    this.closeReason = null;
  }

  on(event, handler) {
    this.handlers.set(event, handler);
  }

  send(payload) {
    this.sent.push(String(payload));
  }

  close(code, reason) {
    if (this.readyState !== 1) {
      return;
    }
    this.closeCode = code ?? null;
    this.closeReason = reason ?? null;
    this.readyState = 3;
    const handler = this.handlers.get("close");
    if (handler) {
      handler();
    }
  }

  emit(event, payload) {
    const handler = this.handlers.get(event);
    if (handler) {
      handler(payload);
    }
  }
}

function waitFor(predicate, timeoutMs = 5000) {
  const start = Date.now();
  return new Promise((resolve, reject) => {
    const tick = () => {
      const value = predicate();
      if (value) {
        resolve(value);
        return;
      }
      if (Date.now() - start >= timeoutMs) {
        reject(new Error("timed out waiting for condition"));
        return;
      }
      setTimeout(tick, 10);
    };
    tick();
  });
}

async function sendRuntimeRPC(socket, payload) {
  const count = socket.sent.length;
  socket.emit("message", JSON.stringify(payload));
  const line = await waitFor(() => {
    const messages = socket.sent.slice(count).map((entry) => JSON.parse(entry));
    return messages.find((message) => message.id === payload.id) || null;
  });
  return line;
}

async function sendRuntimeRPCWithUpdates(socket, payload) {
  const count = socket.sent.length;
  socket.emit("message", JSON.stringify(payload));
  const response = await waitFor(() => {
    const messages = socket.sent.slice(count).map((entry) => JSON.parse(entry));
    return messages.find((message) => message.id === payload.id) || null;
  });
  const messages = socket.sent.slice(count).map((entry) => JSON.parse(entry));
  const updates = messages.filter((message) => message.method === "session/update");
  return { response, updates };
}

function writeMockCodex(tempRoot) {
  const codexPath = path.join(tempRoot, "mock-codex.mjs");
  fs.writeFileSync(
    codexPath,
    [
      "#!/usr/bin/env node",
      "import fs from 'node:fs';",
      "const args = process.argv.slice(2);",
      "function findFlagValue(flag) {",
      "  const index = args.indexOf(flag);",
      "  return index === -1 ? '' : String(args[index + 1] || '');",
      "}",
      "if (args[0] === 'login' && args[1] === '--with-api-key') {",
      "  process.stdin.resume();",
      "  process.stdin.on('data', () => {});",
      "  process.stdin.on('end', () => process.exit(0));",
      "  process.exit(0);",
      "}",
      "if (args[0] !== 'exec') { process.exit(1); }",
      "const captureFile = String(process.env.CODEX_ARGS_CAPTURE || '');",
      "if (captureFile) {",
      "  fs.appendFileSync(captureFile, JSON.stringify(args) + '\\n');",
      "}",
      "const isResume = args[1] === 'resume';",
      "const prompt = String(args[args.length - 1] || '');",
      "const outputFile = findFlagValue('--output-last-message');",
      "const threadId = 'thread-1';",
      "const text = isResume ? `Resumed: ${prompt}` : `Started: ${prompt}`;",
      "if (outputFile) { fs.writeFileSync(outputFile, text + '\\n'); }",
      "process.stdout.write(JSON.stringify({ type: 'thread.started', thread_id: threadId }) + '\\n');",
      "process.stdout.write(JSON.stringify({ type: 'turn.started' }) + '\\n');",
      "process.stdout.write(JSON.stringify({ type: 'item.completed', item: { id: 'item-1', type: 'agent_message', text } }) + '\\n');",
      "process.stdout.write(JSON.stringify({ type: 'turn.completed', usage: { input_tokens: 1, output_tokens: 1 } }) + '\\n');",
    ].join("\n"),
    { mode: 0o755 },
  );
  return codexPath;
}

test("health and metadata endpoints stay available with a fake ws module", async () => {
  const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "spritz-codex-test-"));
  const wsRoot = path.join(tempRoot, "ws");
  fs.mkdirSync(wsRoot, { recursive: true });
  fs.writeFileSync(
    path.join(wsRoot, "index.js"),
    "export class WebSocketServer { constructor() {} on() {} handleUpgrade(_req, _socket, _head, callback) { callback({ readyState: 1, on() {}, send() {}, close() {} }); } }\n",
  );
  const packageRoot = path.join(tempRoot, "codex");
  fs.mkdirSync(packageRoot, { recursive: true });
  fs.writeFileSync(path.join(packageRoot, "package.json"), JSON.stringify({ version: "0.114.0" }));
  const codexPath = writeMockCodex(tempRoot);
  const child = spawn("node", [path.join(TEST_DIR, "acp-server.mjs")], {
    cwd: tempRoot,
    env: {
      ...process.env,
      OPENAI_API_KEY: "test-key",
      SPRITZ_CODEX_ACP_LISTEN_ADDR: "127.0.0.1:32532",
      SPRITZ_CODEX_WS_PACKAGE_ROOT: wsRoot,
      SPRITZ_CODEX_PACKAGE_ROOT: packageRoot,
      SPRITZ_CODEX_BIN: codexPath,
    },
    stdio: ["ignore", "pipe", "pipe"],
  });
  await once(child.stdout, "data");

  const [healthRes, metadataRes, acpRes] = await Promise.all([
    fetch("http://127.0.0.1:32532/healthz"),
    fetch("http://127.0.0.1:32532/.well-known/spritz-acp"),
    fetch("http://127.0.0.1:32532/"),
  ]);

  assert.equal(healthRes.status, 200);
  assert.deepEqual(await healthRes.json(), { ok: true });
  assert.equal(metadataRes.status, 200);
  assert.equal((await metadataRes.json()).agentInfo.title, "Codex ACP Gateway");
  assert.equal(acpRes.status, 426);

  child.kill("SIGTERM");
  await once(child, "exit");
});

test("codex ACP server uses the shared ACP harness", () => {
  const source = fs.readFileSync(path.join(TEST_DIR, "acp-server.mjs"), "utf8");

  assert.match(source, /\.\.\/shared\/spritz-acp-server\.mjs/);
  assert.match(source, /serveSpritzACPServer\(/);
});

test("session/load reuses a freshly created session before the first prompt", async (t) => {
  const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "spritz-codex-runtime-"));
  const codexPath = writeMockCodex(tempRoot);
  const runtime = new CodexACPRuntime(
    {
      codexBin: codexPath,
      codexArgs: [],
      workdir: tempRoot,
      model: "",
      configOptions: [],
      metadata: {
        protocolVersion: 1,
        agentCapabilities: { loadSession: true, promptCapabilities: {} },
        agentInfo: { name: "codex", title: "Codex", version: "1.0.0" },
        authMethods: [],
      },
    },
    {
      ...process.env,
      OPENAI_API_KEY: "test-key",
    },
    console,
  );
  t.after(() => {
    runtime.stop();
  });

  const firstSocket = new FakeSocket();
  runtime.attach(firstSocket);
  await sendRuntimeRPC(firstSocket, { id: "init-1", jsonrpc: "2.0", method: "initialize", params: {} });
  const created = await sendRuntimeRPC(firstSocket, {
    id: "new-1",
    jsonrpc: "2.0",
    method: "session/new",
    params: { cwd: "/workspace", mcpServers: [] },
  });
  const sessionId = String(created.result.sessionId);
  assert.ok(sessionId);
  firstSocket.close();

  const secondSocket = new FakeSocket();
  runtime.attach(secondSocket);
  await sendRuntimeRPC(secondSocket, { id: "init-2", jsonrpc: "2.0", method: "initialize", params: {} });
  const loaded = await sendRuntimeRPC(secondSocket, {
    id: "load-1",
    jsonrpc: "2.0",
    method: "session/load",
    params: { cwd: "/workspace", mcpServers: [], sessionId },
  });
  assert.deepEqual(loaded, {
    id: "load-1",
    jsonrpc: "2.0",
    result: {
      modes: { currentModeId: "default" },
      models: {},
      configOptions: [],
    },
  });
});

test("session/prompt captures Codex replies and replays them on load", async (t) => {
  const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "spritz-codex-runtime-"));
  const codexPath = writeMockCodex(tempRoot);
  const runtime = new CodexACPRuntime(
    {
      codexBin: codexPath,
      codexArgs: [],
      workdir: tempRoot,
      model: "",
      configOptions: [],
      metadata: {
        protocolVersion: 1,
        agentCapabilities: { loadSession: true, promptCapabilities: {} },
        agentInfo: { name: "codex", title: "Codex", version: "1.0.0" },
        authMethods: [],
      },
    },
    {
      ...process.env,
      OPENAI_API_KEY: "test-key",
    },
    console,
  );
  t.after(() => {
    runtime.stop();
  });

  const firstSocket = new FakeSocket();
  runtime.attach(firstSocket);
  await sendRuntimeRPC(firstSocket, { id: "init-1", jsonrpc: "2.0", method: "initialize", params: {} });
  const created = await sendRuntimeRPC(firstSocket, {
    id: "new-1",
    jsonrpc: "2.0",
    method: "session/new",
    params: { cwd: tempRoot, mcpServers: [] },
  });
  const sessionId = String(created.result.sessionId);

  const firstPrompt = await sendRuntimeRPCWithUpdates(firstSocket, {
    id: "prompt-1",
    jsonrpc: "2.0",
    method: "session/prompt",
    params: {
      sessionId,
      prompt: [{ type: "text", text: "hello world" }],
    },
  });
  assert.deepEqual(firstPrompt.response, {
    id: "prompt-1",
    jsonrpc: "2.0",
    result: { stopReason: "end_turn" },
  });
  assert.equal(firstPrompt.updates[0].params.update.sessionUpdate, "user_message_chunk");
  assert.equal(firstPrompt.updates[0].params.update.content.text, "hello world");
  assert.equal(firstPrompt.updates[1].params.update.sessionUpdate, "agent_message_chunk");
  assert.equal(firstPrompt.updates[1].params.update.content.text, "Started: hello world");
  firstSocket.close();

  const secondSocket = new FakeSocket();
  runtime.attach(secondSocket);
  await sendRuntimeRPC(secondSocket, { id: "init-2", jsonrpc: "2.0", method: "initialize", params: {} });
  const replayed = await sendRuntimeRPCWithUpdates(secondSocket, {
    id: "load-1",
    jsonrpc: "2.0",
    method: "session/load",
    params: { cwd: tempRoot, mcpServers: [], sessionId },
  });
  assert.deepEqual(replayed.response, {
    id: "load-1",
    jsonrpc: "2.0",
    result: {
      modes: { currentModeId: "default" },
      models: {},
      configOptions: [],
    },
  });
  assert.equal(replayed.updates.length, 2);
  assert.equal(replayed.updates[0].params.update.historyMessageId.startsWith("user-"), true);
  assert.equal(replayed.updates[1].params.update.historyMessageId.startsWith("assistant-"), true);

  const secondPrompt = await sendRuntimeRPCWithUpdates(secondSocket, {
    id: "prompt-2",
    jsonrpc: "2.0",
    method: "session/prompt",
    params: {
      sessionId,
      prompt: [{ type: "text", text: "second turn" }],
    },
  });
  assert.deepEqual(secondPrompt.response, {
    id: "prompt-2",
    jsonrpc: "2.0",
    result: { stopReason: "end_turn" },
  });
  assert.equal(secondPrompt.updates[1].params.update.content.text, "Resumed: second turn");
});

test("session/prompt passes the configured Codex profile to fresh and resumed exec calls", async (t) => {
  const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "spritz-codex-runtime-"));
  const codexPath = writeMockCodex(tempRoot);
  const argsCapturePath = path.join(tempRoot, "codex-args.jsonl");
  const runtime = new CodexACPRuntime(
    {
      codexBin: codexPath,
      codexArgs: [],
      workdir: tempRoot,
      model: "",
      profile: "gateway-profile",
      configOptions: [],
      metadata: {
        protocolVersion: 1,
        agentCapabilities: { loadSession: true, promptCapabilities: {} },
        agentInfo: { name: "codex", title: "Codex", version: "1.0.0" },
        authMethods: [],
      },
    },
    {
      ...process.env,
      OPENAI_API_KEY: "test-key",
      CODEX_ARGS_CAPTURE: argsCapturePath,
    },
    console,
  );
  t.after(() => {
    runtime.stop();
  });

  const socket = new FakeSocket();
  runtime.attach(socket);
  await sendRuntimeRPC(socket, { id: "init-1", jsonrpc: "2.0", method: "initialize", params: {} });
  const created = await sendRuntimeRPC(socket, {
    id: "new-1",
    jsonrpc: "2.0",
    method: "session/new",
    params: { cwd: tempRoot, mcpServers: [] },
  });
  const sessionId = String(created.result.sessionId);

  await sendRuntimeRPC(socket, {
    id: "prompt-1",
    jsonrpc: "2.0",
    method: "session/prompt",
    params: {
      sessionId,
      prompt: [{ type: "text", text: "hello world" }],
    },
  });
  await sendRuntimeRPC(socket, {
    id: "prompt-2",
    jsonrpc: "2.0",
    method: "session/prompt",
    params: {
      sessionId,
      prompt: [{ type: "text", text: "second turn" }],
    },
  });

  const capturedArgs = fs
    .readFileSync(argsCapturePath, "utf8")
    .trim()
    .split("\n")
    .filter(Boolean)
    .map((line) => JSON.parse(line));

  assert.equal(capturedArgs.length, 2);
  assert.deepEqual(capturedArgs[0].slice(0, 5), [
    "exec",
    "-p",
    "gateway-profile",
    "--json",
    "--skip-git-repo-check",
  ]);
  assert.deepEqual(capturedArgs[1].slice(0, 6), [
    "exec",
    "-p",
    "gateway-profile",
    "resume",
    "--json",
    "--skip-git-repo-check",
  ]);
});
