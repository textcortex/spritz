import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

import { configFromEnv, normalizeGatewayProxyHeaders } from "./acp-server.mjs";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

test("configFromEnv normalizes ACP paths and upstream timeout", () => {
  const config = configFromEnv({
    SPRITZ_OPENCLAW_ACP_GATEWAY_URL: "wss://gateway.example.com/ws",
    SPRITZ_OPENCLAW_ACP_PATH: "acp",
    SPRITZ_OPENCLAW_ACP_HEALTH_PATH: "health",
    SPRITZ_OPENCLAW_ACP_METADATA_PATH: "metadata",
    SPRITZ_OPENCLAW_ACP_UPSTREAM_CHECK_TIMEOUT_MS: "750",
  });

  assert.equal(config.acpPath, "/acp");
  assert.equal(config.healthPath, "/health");
  assert.equal(config.metadataPath, "/metadata");
  assert.equal(config.upstreamCheckTimeoutMs, 750);
});

test("normalizeGatewayProxyHeaders synthesizes Origin for trusted proxy control UI", () => {
  assert.deepEqual(
    normalizeGatewayProxyHeaders(
      {
        "X-Forwarded-Proto": "https",
        "X-Forwarded-Host": "spritz.example.com",
      },
      "ws://127.0.0.1:1234/control",
      true,
    ),
    {
      "X-Forwarded-Proto": "https",
      "X-Forwarded-Host": "spritz.example.com",
      Origin: "http://127.0.0.1:1234",
    },
  );
});

test("openclaw ACP server imports the shared ACP harness", () => {
  const source = fs.readFileSync(path.join(__dirname, "acp-server.mjs"), "utf8");

  assert.match(source, /\.\.\/shared\/spritz-acp-server\.mjs/);
  assert.match(source, /serveSpritzACPServer\(/);
  assert.match(source, /loadWSModule: async \(\) => loadWSModule\(env\)/);
});
