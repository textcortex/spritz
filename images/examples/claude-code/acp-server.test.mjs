import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { spawn } from "node:child_process";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { ACPRuntime } from "./acp-server.mjs";

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

async function openSocket(url) {
  const socket = new WebSocket(url);
  await onceEvent(socket, "open");
  return socket;
}

async function sendRPC(socket, payload) {
  socket.send(JSON.stringify(payload));
  const event = await onceEvent(socket, "message");
  return JSON.parse(String(event.data));
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

  close(_code, _reason) {
    if (this.readyState !== 1) {
      return;
    }
    this.closeCode = _code ?? null;
    this.closeReason = _reason ?? null;
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

async function sendRuntimeRPC(socket, payload) {
  const count = socket.sent.length;
  socket.emit("message", JSON.stringify(payload));
  const line = await waitFor(() => socket.sent.length > count ? socket.sent.at(-1) : null);
  return JSON.parse(line);
}

test("health and metadata endpoints stay available with a fake ws module", async () => {
  const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "spritz-claude-code-test-"));
  const wsRoot = path.join(tempRoot, "ws");
  fs.mkdirSync(wsRoot, { recursive: true });
  fs.writeFileSync(
    path.join(wsRoot, "index.js"),
    "export class WebSocketServer { constructor() {} on() {} handleUpgrade(_req, _socket, _head, callback) { callback({ readyState: 1, on() {}, send() {}, close() {} }); } }\n",
  );
  const packageRoot = path.join(tempRoot, "claude-agent-acp");
  fs.mkdirSync(packageRoot, { recursive: true });
  fs.writeFileSync(path.join(packageRoot, "package.json"), JSON.stringify({ version: "0.21.0" }));
  const child = spawn("node", [path.join(TEST_DIR, "acp-server.mjs")], {
    cwd: tempRoot,
    env: {
      ...process.env,
      ANTHROPIC_API_KEY: "test-key",
      SPRITZ_CLAUDE_CODE_ACP_LISTEN_ADDR: "127.0.0.1:32531",
      SPRITZ_CLAUDE_CODE_WS_PACKAGE_ROOT: wsRoot,
      SPRITZ_CLAUDE_CODE_AGENT_PACKAGE_ROOT: packageRoot,
      SPRITZ_CLAUDE_CODE_ACP_BIN: "node",
      SPRITZ_CLAUDE_CODE_ACP_ARGS_JSON: "[]",
    },
    stdio: ["ignore", "pipe", "pipe"],
  });
  await once(child.stdout, "data");

  const [healthRes, metadataRes, acpRes] = await Promise.all([
    fetch("http://127.0.0.1:32531/healthz"),
    fetch("http://127.0.0.1:32531/.well-known/spritz-acp"),
    fetch("http://127.0.0.1:32531/"),
  ]);

  assert.equal(healthRes.status, 200);
  assert.deepEqual(await healthRes.json(), { ok: true });
  assert.equal(metadataRes.status, 200);
  assert.equal((await metadataRes.json()).agentInfo.title, "Claude Code ACP Gateway");
  assert.equal(acpRes.status, 426);

  child.kill("SIGTERM");
  await once(child, "exit");
});

test("session/load works after reconnecting to the same ACP server", async (t) => {
  const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "spritz-claude-code-runtime-"));
  const adapterPath = path.join(tempRoot, "mock-adapter.mjs");
  fs.writeFileSync(
    adapterPath,
    [
      "const sessions = new Set();",
      "process.stdin.setEncoding('utf8');",
      "let pending = '';",
      "function send(message) { process.stdout.write(JSON.stringify(message) + '\\n'); }",
      "function handle(message) {",
      "  if (message.method === 'initialize') {",
      "    globalThis.initCount = (globalThis.initCount || 0) + 1;",
      "    if (globalThis.initCount > 1) { sessions.clear(); }",
      "    send({ id: message.id, jsonrpc: '2.0', result: { protocolVersion: 1, agentCapabilities: { loadSession: true, promptCapabilities: {} }, agentInfo: { name: 'mock', title: 'Mock', version: '1.0.0' } } });",
      "    return;",
      "  }",
      "  if (message.method === 'session/new') {",
      "    const sessionId = 'session-1';",
      "    sessions.add(sessionId);",
      "    send({ id: message.id, jsonrpc: '2.0', result: { sessionId } });",
      "    return;",
      "  }",
      "  if (message.method === 'session/load') {",
      "    if (!sessions.has(message.params?.sessionId)) {",
      "      send({ id: message.id, jsonrpc: '2.0', error: { code: -32002, message: `Resource not found: ${message.params?.sessionId}` } });",
      "      return;",
      "    }",
      "    send({ id: message.id, jsonrpc: '2.0', result: { sessionId: message.params.sessionId } });",
      "    return;",
      "  }",
      "  send({ id: message.id, jsonrpc: '2.0', result: {} });",
      "}",
      "process.stdin.on('data', (chunk) => {",
      "  pending += chunk;",
      "  let newline = pending.indexOf('\\n');",
      "  while (newline !== -1) {",
      "    const line = pending.slice(0, newline).trim();",
      "    pending = pending.slice(newline + 1);",
      "    if (line) { handle(JSON.parse(line)); }",
      "    newline = pending.indexOf('\\n');",
      "  }",
      "});",
    ].join("\n"),
  );
  const runtime = new ACPRuntime(
    {
      adapterBin: "node",
      adapterArgs: [adapterPath],
      workdir: tempRoot,
    },
    {
      ...process.env,
      ANTHROPIC_API_KEY: "test-key",
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
  assert.equal(created.result.sessionId, "session-1");
  firstSocket.close();

  const secondSocket = new FakeSocket();
  runtime.attach(secondSocket);
  await sendRuntimeRPC(secondSocket, { id: "init-2", jsonrpc: "2.0", method: "initialize", params: {} });
  const loaded = await sendRuntimeRPC(secondSocket, {
    id: "load-1",
    jsonrpc: "2.0",
    method: "session/load",
    params: { cwd: "/workspace", mcpServers: [], sessionId: "session-1" },
  });
  assert.deepEqual(loaded, {
    id: "load-1",
    jsonrpc: "2.0",
    result: { sessionId: "session-1" },
  });
  secondSocket.close();

  runtime.stop();
  if (runtime.child) {
    await once(runtime.child, "exit");
  }
});

test("a new ACP client handoff replaces the previous attached socket", async (t) => {
  const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "spritz-claude-code-runtime-"));
  const adapterPath = path.join(tempRoot, "mock-adapter.mjs");
  fs.writeFileSync(
    adapterPath,
    [
      "process.stdin.setEncoding('utf8');",
      "let pending = '';",
      "function send(message) { process.stdout.write(JSON.stringify(message) + '\\n'); }",
      "function handle(message) {",
      "  if (message.method === 'initialize') {",
      "    send({ id: message.id, jsonrpc: '2.0', result: { protocolVersion: 1, agentCapabilities: { loadSession: true, promptCapabilities: {} }, agentInfo: { name: 'mock', title: 'Mock', version: '1.0.0' } } });",
      "    return;",
      "  }",
      "  send({ id: message.id, jsonrpc: '2.0', result: {} });",
      "}",
      "process.stdin.on('data', (chunk) => {",
      "  pending += chunk;",
      "  let newline = pending.indexOf('\\n');",
      "  while (newline !== -1) {",
      "    const line = pending.slice(0, newline).trim();",
      "    pending = pending.slice(newline + 1);",
      "    if (line) { handle(JSON.parse(line)); }",
      "    newline = pending.indexOf('\\n');",
      "  }",
      "});",
    ].join("\n"),
  );
  const runtime = new ACPRuntime(
    {
      adapterBin: "node",
      adapterArgs: [adapterPath],
      workdir: tempRoot,
      metadata: {
        protocolVersion: 1,
        agentCapabilities: { loadSession: true, promptCapabilities: {} },
        agentInfo: { name: "mock", title: "Mock", version: "1.0.0" },
        authMethods: [],
      },
    },
    {
      ...process.env,
      ANTHROPIC_API_KEY: "test-key",
    },
    console,
  );
  t.after(() => {
    runtime.stop();
  });

  const firstSocket = new FakeSocket();
  runtime.attach(firstSocket);
  await sendRuntimeRPC(firstSocket, { id: "init-1", jsonrpc: "2.0", method: "initialize", params: {} });

  const secondSocket = new FakeSocket();
  runtime.attach(secondSocket);

  assert.equal(firstSocket.readyState, 3);
  assert.equal(firstSocket.closeCode, 1001);
  assert.equal(firstSocket.closeReason, "ACP client replaced");

  const initResponse = await sendRuntimeRPC(secondSocket, {
    id: "init-2",
    jsonrpc: "2.0",
    method: "initialize",
    params: {},
  });
  assert.deepEqual(initResponse, {
    id: "init-2",
    jsonrpc: "2.0",
    result: {
      protocolVersion: 1,
      agentCapabilities: { loadSession: true, promptCapabilities: {} },
      agentInfo: { name: "mock", title: "Mock", version: "1.0.0" },
      authMethods: [],
    },
  });
});

test("initialize preserves adapter-negotiated auth methods across reconnects", async (t) => {
  const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "spritz-claude-code-runtime-"));
  const adapterPath = path.join(tempRoot, "mock-adapter.mjs");
  fs.writeFileSync(
    adapterPath,
    [
      "process.stdin.setEncoding('utf8');",
      "let pending = '';",
      "function send(message) { process.stdout.write(JSON.stringify(message) + '\\n'); }",
      "function handle(message) {",
      "  if (message.method === 'initialize') {",
      "    const supportsGatewayAuth = message.params?.clientCapabilities?.auth?._meta?.gateway === true;",
      "    send({",
      "      id: message.id,",
      "      jsonrpc: '2.0',",
      "      result: {",
      "        protocolVersion: 1,",
      "        agentCapabilities: { loadSession: true, promptCapabilities: {} },",
      "        agentInfo: { name: 'mock', title: 'Mock', version: '1.0.0' },",
      "        authMethods: supportsGatewayAuth ? [{ id: 'gateway', name: 'Gateway' }] : [],",
      "      },",
      "    });",
      "    return;",
      "  }",
      "  send({ id: message.id, jsonrpc: '2.0', result: {} });",
      "}",
      "process.stdin.on('data', (chunk) => {",
      "  pending += chunk;",
      "  let newline = pending.indexOf('\\n');",
      "  while (newline !== -1) {",
      "    const line = pending.slice(0, newline).trim();",
      "    pending = pending.slice(newline + 1);",
      "    if (line) { handle(JSON.parse(line)); }",
      "    newline = pending.indexOf('\\n');",
      "  }",
      "});",
    ].join("\n"),
  );
  const runtime = new ACPRuntime(
    {
      adapterBin: "node",
      adapterArgs: [adapterPath],
      workdir: tempRoot,
      metadata: {
        protocolVersion: 1,
        agentCapabilities: { loadSession: true, promptCapabilities: {} },
        agentInfo: { name: "mock", title: "Mock", version: "1.0.0" },
        authMethods: [],
      },
    },
    {
      ...process.env,
      ANTHROPIC_API_KEY: "test-key",
    },
    console,
  );
  t.after(() => {
    runtime.stop();
  });

  const firstSocket = new FakeSocket();
  runtime.attach(firstSocket);
  const firstInit = await sendRuntimeRPC(firstSocket, {
    id: "init-1",
    jsonrpc: "2.0",
    method: "initialize",
    params: {
      protocolVersion: 1,
      clientCapabilities: {
        auth: {
          _meta: {
            gateway: true,
          },
        },
      },
      clientInfo: {
        name: "client-a",
        title: "Client A",
        version: "1.0.0",
      },
    },
  });
  assert.deepEqual(firstInit.result.authMethods, [{ id: "gateway", name: "Gateway" }]);
  firstSocket.close();

  const secondSocket = new FakeSocket();
  runtime.attach(secondSocket);
  const secondInit = await sendRuntimeRPC(secondSocket, {
    id: "init-2",
    jsonrpc: "2.0",
    method: "initialize",
    params: {
      protocolVersion: 1,
      clientCapabilities: {},
      clientInfo: {
        name: "client-b",
        title: "Client B",
        version: "1.0.0",
      },
    },
  });
  assert.deepEqual(secondInit.result.authMethods, [{ id: "gateway", name: "Gateway" }]);
});
