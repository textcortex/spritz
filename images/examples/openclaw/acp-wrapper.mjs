#!/usr/bin/env node

import fs from "node:fs";
import path from "node:path";
import { createRequire } from "node:module";
import { Readable, Writable } from "node:stream";
import { pathToFileURL } from "node:url";

const GATEWAY_CLIENT_NAMES = {
  CLI: "cli",
  CONTROL_UI: "openclaw-control-ui",
};

const GATEWAY_CLIENT_MODES = {
  CLI: "cli",
  WEBCHAT: "webchat",
};

const TRUTHY_VALUES = new Set(["1", "true", "yes", "on"]);
const DEFAULT_OPENCLAW_PACKAGE_ROOT = "/usr/local/lib/node_modules/openclaw";

/**
 * Returns whether the image-owned ACP bridge should impersonate a trusted-proxy
 * Control UI client instead of the normal CLI ACP client.
 */
export function useTrustedProxyControlUiBridge(env = process.env) {
  const raw = env.SPRITZ_OPENCLAW_ACP_USE_CONTROL_UI_BRIDGE;
  if (typeof raw !== "string") {
    return false;
  }
  return TRUTHY_VALUES.has(raw.trim().toLowerCase());
}

/**
 * Parses the CLI args accepted by the image-owned ACP wrapper. The wrapper
 * accepts the leading `acp` subcommand so it can be dropped in place of the
 * normal `openclaw` binary.
 */
export function parseArgs(argv, helpers = { readSecretFromFile: defaultReadSecretFromFile }) {
  const args = normalizeCliArgs(argv);
  const opts = {};
  let tokenFile;
  let passwordFile;

  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    if (arg === "--url" || arg === "--gateway-url") {
      opts.gatewayUrl = args[index + 1];
      index += 1;
      continue;
    }
    if (arg === "--token" || arg === "--gateway-token") {
      opts.gatewayToken = args[index + 1];
      index += 1;
      continue;
    }
    if (arg === "--token-file" || arg === "--gateway-token-file") {
      tokenFile = args[index + 1];
      index += 1;
      continue;
    }
    if (arg === "--password" || arg === "--gateway-password") {
      opts.gatewayPassword = args[index + 1];
      index += 1;
      continue;
    }
    if (arg === "--password-file" || arg === "--gateway-password-file") {
      passwordFile = args[index + 1];
      index += 1;
      continue;
    }
    if (arg === "--session") {
      opts.defaultSessionKey = args[index + 1];
      index += 1;
      continue;
    }
    if (arg === "--session-label") {
      opts.defaultSessionLabel = args[index + 1];
      index += 1;
      continue;
    }
    if (arg === "--require-existing") {
      opts.requireExistingSession = true;
      continue;
    }
    if (arg === "--reset-session") {
      opts.resetSession = true;
      continue;
    }
    if (arg === "--no-prefix-cwd") {
      opts.prefixCwd = false;
      continue;
    }
    if (arg === "--provenance") {
      const normalized = normalizeAcpProvenanceMode(args[index + 1]);
      if (!normalized) {
        throw new Error("Invalid --provenance value. Use off, meta, or meta+receipt.");
      }
      opts.provenanceMode = normalized;
      index += 1;
      continue;
    }
    if (arg === "--verbose" || arg === "-v") {
      opts.verbose = true;
      continue;
    }
    if (arg === "--help" || arg === "-h") {
      opts.help = true;
      continue;
    }
  }

  if (typeof opts.gatewayToken === "string" && tokenFile?.trim()) {
    throw new Error("Use either --token or --token-file.");
  }
  if (typeof opts.gatewayPassword === "string" && passwordFile?.trim()) {
    throw new Error("Use either --password or --password-file.");
  }
  if (tokenFile?.trim()) {
    opts.gatewayToken = helpers.readSecretFromFile(tokenFile, "Gateway token");
  }
  if (passwordFile?.trim()) {
    opts.gatewayPassword = helpers.readSecretFromFile(passwordFile, "Gateway password");
  }

  return opts;
}

/**
 * Builds the Gateway client profile used by the wrapper. In trusted-proxy mode
 * the bridge must connect as a Control UI operator session without a device
 * identity; otherwise OpenClaw will force pairing for the CLI ACP client.
 */
export function buildGatewayClientOptions(params) {
  const base = {
    url: params.connectionUrl,
    clientDisplayName: "ACP",
    clientVersion: "acp",
    role: "operator",
  };

  if (params.trustedProxyControlUi) {
    return {
      ...base,
      clientName: GATEWAY_CLIENT_NAMES.CONTROL_UI,
      mode: GATEWAY_CLIENT_MODES.WEBCHAT,
      deviceIdentity: false,
      token: undefined,
      password: undefined,
    };
  }

  return {
    ...base,
    clientName: GATEWAY_CLIENT_NAMES.CLI,
    mode: GATEWAY_CLIENT_MODES.CLI,
    token: params.gatewayToken,
    password: params.gatewayPassword,
  };
}

function normalizeCliArgs(argv) {
  if (argv[0] === "acp") {
    return argv.slice(1);
  }
  return argv.slice();
}

function normalizeAcpProvenanceMode(value) {
  if (typeof value !== "string") {
    return undefined;
  }
  const normalized = value.trim().toLowerCase();
  if (!normalized) {
    return undefined;
  }
  if (normalized === "off" || normalized === "meta" || normalized === "meta+receipt") {
    return normalized;
  }
  return undefined;
}

function defaultReadSecretFromFile(filePath, label) {
  try {
    return fs.readFileSync(filePath, "utf8").trim();
  } catch (error) {
    throw new Error(`Failed to read ${label} from ${filePath}: ${String(error)}`);
  }
}

function resolveOpenclawPackageRoot(env = process.env) {
  const raw = env.SPRITZ_OPENCLAW_PACKAGE_ROOT;
  if (typeof raw === "string" && raw.trim()) {
    return raw.trim();
  }
  return DEFAULT_OPENCLAW_PACKAGE_ROOT;
}

async function importOpenclawDependency(specifier, env = process.env) {
  const packageRoot = resolveOpenclawPackageRoot(env);
  const requireFromOpenclaw = createRequire(path.join(packageRoot, "package.json"));
  const resolvedPath = requireFromOpenclaw.resolve(specifier);
  return await import(pathToFileURL(resolvedPath).href);
}

async function importOpenclawModule(relativePath, env = process.env) {
  const packageRoot = resolveOpenclawPackageRoot(env);
  const resolvedPath = path.join(packageRoot, relativePath);
  return await import(pathToFileURL(resolvedPath).href);
}

async function serveSpritzOpenclawAcp(opts = {}, env = process.env) {
  const sdk = await importOpenclawDependency("@agentclientprotocol/sdk", env);
  const { loadConfig } = await importOpenclawModule("dist/config/config.js", env);
  const { buildGatewayConnectionDetails } = await importOpenclawModule(
    "dist/gateway/call.js",
    env,
  );
  const { GatewayClient } = await importOpenclawModule("dist/gateway/client.js", env);
  const { resolveGatewayConnectionAuth } = await importOpenclawModule(
    "dist/gateway/connection-auth.js",
    env,
  );
  const { AcpGatewayAgent } = await importOpenclawModule("dist/acp/translator.js", env);

  const AgentSideConnection =
    sdk.AgentSideConnection ?? sdk.default?.AgentSideConnection;
  const ndJsonStream = sdk.ndJsonStream ?? sdk.default?.ndJsonStream;
  if (!AgentSideConnection || !ndJsonStream) {
    throw new Error("Failed to load ACP SDK from the installed OpenClaw package.");
  }

  const cfg = loadConfig();
  const connection = buildGatewayConnectionDetails({
    config: cfg,
    url: opts.gatewayUrl,
  });
  const gatewayUrlOverrideSource =
    connection.urlSource === "cli --url"
      ? "cli"
      : connection.urlSource === "env OPENCLAW_GATEWAY_URL"
        ? "env"
        : undefined;
  const creds = await resolveGatewayConnectionAuth({
    config: cfg,
    explicitAuth: {
      token: opts.gatewayToken,
      password: opts.gatewayPassword,
    },
    env,
    urlOverride: gatewayUrlOverrideSource ? connection.url : undefined,
    urlOverrideSource: gatewayUrlOverrideSource,
  });

  const trustedProxyControlUi = useTrustedProxyControlUiBridge(env);
  let agent = null;
  let onClosed = () => {};
  const closed = new Promise((resolve) => {
    onClosed = resolve;
  });
  let stopped = false;
  let onGatewayReadyResolve = () => {};
  let onGatewayReadyReject = () => {};
  let gatewayReadySettled = false;
  const gatewayReady = new Promise((resolve, reject) => {
    onGatewayReadyResolve = resolve;
    onGatewayReadyReject = reject;
  });
  const resolveGatewayReady = () => {
    if (gatewayReadySettled) {
      return;
    }
    gatewayReadySettled = true;
    onGatewayReadyResolve();
  };
  const rejectGatewayReady = (error) => {
    if (gatewayReadySettled) {
      return;
    }
    gatewayReadySettled = true;
    onGatewayReadyReject(error instanceof Error ? error : new Error(String(error)));
  };

  const gateway = new GatewayClient({
    ...buildGatewayClientOptions({
      connectionUrl: connection.url,
      gatewayToken: trustedProxyControlUi ? undefined : creds.token,
      gatewayPassword: trustedProxyControlUi ? undefined : creds.password,
      trustedProxyControlUi,
    }),
    onEvent: (event) => {
      void agent?.handleGatewayEvent(event);
    },
    onHelloOk: () => {
      resolveGatewayReady();
      agent?.handleGatewayReconnect();
    },
    onConnectError: (error) => {
      rejectGatewayReady(error);
    },
    onClose: (code, reason) => {
      if (!stopped) {
        rejectGatewayReady(new Error(`gateway closed before ready (${code}): ${reason}`));
      }
      agent?.handleGatewayDisconnect(`${code}: ${reason}`);
      if (stopped) {
        onClosed();
      }
    },
  });

  const shutdown = () => {
    if (stopped) {
      return;
    }
    stopped = true;
    resolveGatewayReady();
    gateway.stop();
    onClosed();
  };

  process.once("SIGINT", shutdown);
  process.once("SIGTERM", shutdown);

  gateway.start();
  await gatewayReady.catch((error) => {
    shutdown();
    throw error;
  });
  if (stopped) {
    return closed;
  }

  const input = Writable.toWeb(process.stdout);
  const output = Readable.toWeb(process.stdin);
  const stream = ndJsonStream(input, output);

  new AgentSideConnection((connectionInstance) => {
    agent = new AcpGatewayAgent(connectionInstance, gateway, opts);
    agent.start();
    return agent;
  }, stream);

  return closed;
}

function printHelp() {
  console.log(`Usage: spritz-openclaw-acp-wrapper [acp] [options]

Image-owned ACP wrapper for Spritz OpenClaw workloads.

Options:
  --url <url>             Gateway WebSocket URL
  --token <token>         Gateway auth token
  --token-file <path>     Read gateway auth token from file
  --password <password>   Gateway auth password
  --password-file <path>  Read gateway auth password from file
  --session <key>         Default session key
  --session-label <label> Default session label to resolve
  --require-existing      Fail if the session key/label does not exist
  --reset-session         Reset the session key before first use
  --no-prefix-cwd         Do not prefix prompts with the working directory
  --provenance <mode>     ACP provenance mode: off, meta, or meta+receipt
  --verbose, -v           Verbose logging to stderr
  --help, -h              Show this help message
`);
}

async function main() {
  const opts = parseArgs(process.argv.slice(2));
  if (opts.help) {
    printHelp();
    return;
  }
  await serveSpritzOpenclawAcp(opts);
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch((error) => {
    console.error(String(error));
    process.exit(1);
  });
}
