import test from "node:test";
import assert from "node:assert/strict";

import { resolveAcpCliDependencies } from "./generate-openclaw-acp-compat.js";

test("resolveAcpCliDependencies supports legacy combined gateway imports", () => {
  const dependencies = resolveAcpCliDependencies(`
import { GatewayClient, buildGatewayConnectionDetails } from "./call-old.js";
import { resolveGatewayConnectionAuth } from "./connection-auth-old.js";
import { GATEWAY_CLIENT_NAMES, GATEWAY_CLIENT_MODES } from "./message-channel-old.js";
import { loadConfig } from "./index.js";
`);

  assert.deepEqual(dependencies, {
    gatewayClientBasename: "call-old.js",
    gatewayConnectionBasename: "call-old.js",
    connectionAuthBasename: "connection-auth-old.js",
    gatewayConstantsBasename: "message-channel-old.js",
    loadConfigBasename: "index.js",
  });
});

test("resolveAcpCliDependencies supports split gateway imports from current openclaw", () => {
  const dependencies = resolveAcpCliDependencies(`
import { loadConfig } from "./io-y3Az_Onx.js";
import { h as GATEWAY_CLIENT_NAMES, m as GATEWAY_CLIENT_MODES } from "./message-channel-BliByQBl.js";
import { u as GatewayClient } from "./method-scopes-BiEi0X2g.js";
import { n as buildGatewayConnectionDetails } from "./call-CQbSO4Fr.js";
import { t as resolveGatewayConnectionAuth } from "./connection-auth-Nl2a3tDb.js";
`);

  assert.deepEqual(dependencies, {
    gatewayClientBasename: "method-scopes-BiEi0X2g.js",
    gatewayConnectionBasename: "call-CQbSO4Fr.js",
    connectionAuthBasename: "connection-auth-Nl2a3tDb.js",
    gatewayConstantsBasename: "message-channel-BliByQBl.js",
    loadConfigBasename: "io-y3Az_Onx.js",
  });
});
