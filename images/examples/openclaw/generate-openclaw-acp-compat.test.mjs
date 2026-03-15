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
    import { loadConfig } from "./index.js";
    import { h as GATEWAY_CLIENT_NAMES, m as GATEWAY_CLIENT_MODES } from "./message-channel-abc123.js";
    import { p as GatewayClient, t as buildGatewayConnectionDetails } from "./call-def456.js";
    import { t as resolveGatewayConnectionAuth } from "./connection-auth-ghi789.js";
  `);

  assert.deepEqual(dependencies, {
    callBasename: "call-def456.js",
    connectionAuthBasename: "connection-auth-ghi789.js",
    gatewayConstantsBasename: "message-channel-abc123.js",
    loadConfigBasename: "index.js",
  });
});

test("resolveAcpCliDependencies supports gateway constant imports in either order", () => {
  const dependencies = resolveAcpCliDependencies(`
    import { loadConfig } from "./index.js";
    import { m as GATEWAY_CLIENT_MODES, h as GATEWAY_CLIENT_NAMES } from "./message-channel-abc123.js";
    import { p as GatewayClient, t as buildGatewayConnectionDetails } from "./call-def456.js";
    import { t as resolveGatewayConnectionAuth } from "./connection-auth-ghi789.js";
  `);

  assert.deepEqual(dependencies, {
    callBasename: "call-def456.js",
    connectionAuthBasename: "connection-auth-ghi789.js",
    gatewayConstantsBasename: "message-channel-abc123.js",
    loadConfigBasename: "index.js",
  });
});

test("resolveAcpCliDependencies supports loadConfig and gateway constants from the same hashed bundle", () => {
  const dependencies = resolveAcpCliDependencies(`
    import { Gs as loadConfig, di as GATEWAY_CLIENT_NAMES, ui as GATEWAY_CLIENT_MODES } from "./model-selection-demo.js";
    import { p as GatewayClient, t as buildGatewayConnectionDetails } from "./call-demo.js";
    import { t as resolveGatewayConnectionAuth } from "./connection-auth-demo.js";
  `);

  assert.deepEqual(dependencies, {
    callBasename: "call-demo.js",
    connectionAuthBasename: "connection-auth-demo.js",
    gatewayConstantsBasename: "model-selection-demo.js",
    loadConfigBasename: "model-selection-demo.js",
  });
});

test("resolveAcpCliDependencies supports the current published OpenClaw ACP import layout", () => {
  const dependencies = resolveAcpCliDependencies(`
    import { Cm as GATEWAY_CLIENT_MODES, Eg as isKnownCoreToolId, Es as buildGatewayConnectionDetails, Wt as resolveGatewayConnectionAuth, u_ as loadConfig, uv as VERSION, wm as GATEWAY_CLIENT_NAMES, zs as GatewayClient, zv as listThinkingLevels } from "./reply-demo.js";
  `);

  assert.deepEqual(dependencies, {
    callBasename: "reply-demo.js",
    connectionAuthBasename: "reply-demo.js",
    gatewayConstantsBasename: "reply-demo.js",
    loadConfigBasename: "reply-demo.js",
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
  assert.equal(result.gatewayConstantsBasename, "message-channel-demo.js");
  assert.equal(result.loadConfigBasename, "index.js");
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

test("generateOpenclawAcpCompat supports hashed config bundles that also export gateway constants", async () => {
  const packageRoot = makeTempPackageRoot();
  const distDir = path.join(packageRoot, "dist");
  fs.mkdirSync(distDir, { recursive: true });

  fs.writeFileSync(
    path.join(distDir, "model-selection-demo.js"),
    [
      'function loadConfig() { return { gateway: { mode: "remote" } }; }',
      'export const y = { CLI: "cli", CONTROL_UI: "openclaw-control-ui" };',
      'export const z = { CLI: "cli", WEBCHAT: "webchat" };',
      'export { loadConfig as x };',
      "",
    ].join("\n"),
  );
  fs.writeFileSync(
    path.join(distDir, "call-demo.js"),
    [
      'export class GatewayClient {}',
      'export function buildGatewayConnectionDetails() { return { url: "wss://example.test", urlSource: "remote" }; }',
      "",
    ].join("\n"),
  );
  fs.writeFileSync(
    path.join(distDir, "connection-auth-demo.js"),
    'export async function resolveGatewayConnectionAuth() { return { password: "password" }; }\n',
  );
  fs.writeFileSync(
    path.join(distDir, "acp-cli-demo.js"),
    [
      'import { x as loadConfig, y as GATEWAY_CLIENT_NAMES, z as GATEWAY_CLIENT_MODES } from "./model-selection-demo.js";',
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
      "    loadConfig,",
      "  };",
      "}",
      "function registerAcpCli() {}",
      "export { registerAcpCli };",
      "",
    ].join("\n"),
  );

  const result = generateOpenclawAcpCompat(packageRoot);
  const compatModule = await import(pathToFileURL(result.compatPath).href);

  assert.equal(result.loadConfigBasename, "model-selection-demo.js");
  assert.equal(result.gatewayConstantsBasename, "model-selection-demo.js");
  assert.equal(compatModule.loadConfig().gateway.mode, "remote");
  assert.equal(compatModule.GatewayClient.name, "GatewayClient");
  assert.equal(compatModule.buildGatewayConnectionDetails().url, "wss://example.test");
  assert.deepEqual(await compatModule.resolveGatewayConnectionAuth(), { password: "password" });
  assert.equal(compatModule.GATEWAY_CLIENT_NAMES.CONTROL_UI, "openclaw-control-ui");
  assert.equal(compatModule.GATEWAY_CLIENT_MODES.WEBCHAT, "webchat");
});
