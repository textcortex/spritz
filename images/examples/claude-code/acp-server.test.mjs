import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { spawn } from "node:child_process";
import test from "node:test";

function once(child, event) {
  return new Promise((resolve) => child.once(event, resolve));
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
  const child = spawn("node", ["images/examples/claude-code/acp-server.mjs"], {
    cwd: process.cwd(),
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
