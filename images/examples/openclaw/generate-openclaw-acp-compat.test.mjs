import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { pathToFileURL } from "node:url";

import {
  ACP_CLI_COMPAT_BASENAME,
  ACP_COMPAT_BASENAME,
  generateOpenclawAcpCompat,
  resolveAcpCliDependencies,
} from "./generate-openclaw-acp-compat.mjs";

function makeTempPackageRoot() {
  return fs.mkdtempSync(path.join(os.tmpdir(), "spritz-openclaw-compat-"));
}

test("resolveAcpCliDependencies extracts the hashed bundles imported by acp-cli", () => {
  const dependencies = resolveAcpCliDependencies(`
    import { h as GATEWAY_CLIENT_NAMES, m as GATEWAY_CLIENT_MODES } from "./message-channel-abc123.js";
    import { p as GatewayClient, t as buildGatewayConnectionDetails } from "./call-def456.js";
    import { t as resolveGatewayConnectionAuth } from "./connection-auth-ghi789.js";
  `);

  assert.deepEqual(dependencies, {
    callBasename: "call-def456.js",
    connectionAuthBasename: "connection-auth-ghi789.js",
    messageChannelBasename: "message-channel-abc123.js",
  });
});

test("generateOpenclawAcpCompat writes stable compat modules for the installed package", async () => {
  const packageRoot = makeTempPackageRoot();
  const distDir = path.join(packageRoot, "dist");
  fs.mkdirSync(distDir, { recursive: true });

  fs.writeFileSync(
    path.join(distDir, "index.js"),
    'export function loadConfig() { return { gateway: { mode: "local" } }; }\n',
  );
  fs.writeFileSync(
    path.join(distDir, "call-demo.js"),
    [
      'export class GatewayClient {}',
      'export function buildGatewayConnectionDetails() { return { url: "ws://127.0.0.1:8080", urlSource: "local loopback" }; }',
      "",
    ].join("\n"),
  );
  fs.writeFileSync(
    path.join(distDir, "connection-auth-demo.js"),
    'export async function resolveGatewayConnectionAuth() { return { token: "token" }; }\n',
  );
  fs.writeFileSync(
    path.join(distDir, "message-channel-demo.js"),
    [
      'export const GATEWAY_CLIENT_NAMES = { CLI: "cli", CONTROL_UI: "openclaw-control-ui" };',
      'export const GATEWAY_CLIENT_MODES = { CLI: "cli", WEBCHAT: "webchat" };',
      "",
    ].join("\n"),
  );
  fs.writeFileSync(
    path.join(distDir, "acp-cli-demo.js"),
    [
      'import { GATEWAY_CLIENT_NAMES, GATEWAY_CLIENT_MODES } from "./message-channel-demo.js";',
      'import { GatewayClient, buildGatewayConnectionDetails } from "./call-demo.js";',
      'import { resolveGatewayConnectionAuth } from "./connection-auth-demo.js";',
      "class AcpGatewayAgent {}",
      "async function serveAcpGateway() {",
      "  return {",
      "    GatewayClient,",
      "    buildGatewayConnectionDetails,",
      "    resolveGatewayConnectionAuth,",
      "    GATEWAY_CLIENT_NAMES,",
      "    GATEWAY_CLIENT_MODES,",
      "  };",
      "}",
      "function registerAcpCli() {}",
      "export { registerAcpCli };",
      "",
    ].join("\n"),
  );

  const result = generateOpenclawAcpCompat(packageRoot);

  assert.equal(result.callBasename, "call-demo.js");
  assert.equal(result.connectionAuthBasename, "connection-auth-demo.js");
  assert.equal(result.messageChannelBasename, "message-channel-demo.js");
  assert.equal(path.basename(result.acpCliCompatPath), ACP_CLI_COMPAT_BASENAME);
  assert.equal(path.basename(result.compatPath), ACP_COMPAT_BASENAME);

  const compatModule = await import(pathToFileURL(result.compatPath).href);
  const acpCliCompatModule = await import(pathToFileURL(result.acpCliCompatPath).href);

  assert.equal(typeof compatModule.loadConfig, "function");
  assert.equal(compatModule.GatewayClient.name, "GatewayClient");
  assert.equal(compatModule.buildGatewayConnectionDetails().url, "ws://127.0.0.1:8080");
  assert.deepEqual(await compatModule.resolveGatewayConnectionAuth(), { token: "token" });
  assert.equal(compatModule.GATEWAY_CLIENT_NAMES.CONTROL_UI, "openclaw-control-ui");
  assert.equal(compatModule.GATEWAY_CLIENT_MODES.WEBCHAT, "webchat");
  assert.equal(acpCliCompatModule.AcpGatewayAgent.name, "AcpGatewayAgent");
  assert.equal(typeof acpCliCompatModule.serveAcpGateway, "function");
  assert.equal(typeof acpCliCompatModule.registerAcpCli, "function");
});
