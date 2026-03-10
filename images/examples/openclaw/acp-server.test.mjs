import test from "node:test";
import assert from "node:assert/strict";
import http from "node:http";

import {
  createACPRequestHandler,
  parseListenAddress,
} from "./acp-server.mjs";

test("parseListenAddress supports IPv4 and bracketed IPv6", () => {
  assert.deepEqual(parseListenAddress("0.0.0.0:2529"), {
    host: "0.0.0.0",
    port: 2529,
  });
  assert.deepEqual(parseListenAddress("[::]:2529"), {
    host: "::",
    port: 2529,
  });
});

test("ACP request handler serves health and metadata without mutating runtime state", async () => {
  let healthChecks = 0;
  const handler = createACPRequestHandler({
    config: {
      acpPath: "/",
      healthPath: "/healthz",
      metadataPath: "/.well-known/spritz-acp",
    },
    runtime: {
      metadata: {
        protocolVersion: 1,
        agentInfo: { name: "openclaw-acp", title: "OpenClaw ACP Gateway", version: "1.2.3" },
      },
      async health() {
        healthChecks += 1;
        return { ok: true };
      },
    },
    logger: {
      warn() {},
    },
  });

  const server = http.createServer((req, res) => {
    void handler(req, res);
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  const address = server.address();
  assert.ok(address && typeof address !== "string");
  const baseURL = `http://127.0.0.1:${address.port}`;

  const [healthRes, metadataRes, upgradeRes] = await Promise.all([
    fetch(`${baseURL}/healthz`),
    fetch(`${baseURL}/.well-known/spritz-acp`),
    fetch(`${baseURL}/`),
  ]);

  assert.equal(healthRes.status, 200);
  assert.deepEqual(await healthRes.json(), { ok: true });
  assert.equal(healthChecks, 1);

  assert.equal(metadataRes.status, 200);
  assert.deepEqual(await metadataRes.json(), {
    protocolVersion: 1,
    agentInfo: { name: "openclaw-acp", title: "OpenClaw ACP Gateway", version: "1.2.3" },
  });

  assert.equal(upgradeRes.status, 426);

  await new Promise((resolve) => server.close(resolve));
});
