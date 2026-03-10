#!/usr/bin/env node

import { randomUUID } from "node:crypto";
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
const DEFAULT_FALLBACK_AGENT_ID = "main";
const DEFAULT_FALLBACK_SESSION_PREFIX = "spritz-acp";
const UUIDISH_SESSION_ID_PATTERN =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

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

function normalizeBridgeToken(value, fallback) {
  if (typeof value !== "string") {
    return fallback;
  }
  const normalized = value
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9_-]+/g, "-")
    .replace(/^-+|-+$/g, "");
  return normalized || fallback;
}

function readMetaString(record, keys) {
  for (const key of keys) {
    const value = record?.[key];
    if (typeof value === "string" && value.trim()) {
      return value.trim();
    }
  }
  return undefined;
}

function readMetaBool(record, keys) {
  for (const key of keys) {
    const value = record?.[key];
    if (typeof value === "boolean") {
      return value;
    }
    if (typeof value === "string") {
      const normalized = value.trim().toLowerCase();
      if (normalized === "true") return true;
      if (normalized === "false") return false;
    }
  }
  return undefined;
}

function parseSessionMeta(meta) {
  if (!meta || typeof meta !== "object" || Array.isArray(meta)) {
    return {};
  }
  return {
    sessionKey: readMetaString(meta, ["sessionKey", "session", "key"]),
    sessionLabel: readMetaString(meta, ["sessionLabel", "label"]),
    resetSession: readMetaBool(meta, ["resetSession", "reset"]),
    requireExisting: readMetaBool(meta, ["requireExistingSession", "requireExisting"]),
    prefixCwd: readMetaBool(meta, ["prefixCwd"]),
  };
}

function normalizeHistoryContent(content) {
  if (Array.isArray(content)) {
    return content.filter((item) => item && typeof item === "object");
  }
  if (typeof content === "string" && content.trim()) {
    return [{ type: "text", text: content }];
  }
  return [];
}

function readTextFromHistoryContent(content) {
  return content
    .map((item) => {
      if (typeof item.text === "string" && item.text.trim()) {
        return item.text;
      }
      if (typeof item.content === "string" && item.content.trim()) {
        return item.content;
      }
      return "";
    })
    .filter(Boolean)
    .join("\n")
    .trim();
}

function buildHistoryToolCallUpdate(item) {
  const toolCallId =
    (typeof item.id === "string" && item.id.trim()) ||
    (typeof item.toolCallId === "string" && item.toolCallId.trim()) ||
    (typeof item.tool_call_id === "string" && item.tool_call_id.trim()) ||
    "";
  if (!toolCallId) {
    return null;
  }
  const toolName =
    (typeof item.name === "string" && item.name.trim()) ||
    (typeof item.toolName === "string" && item.toolName.trim()) ||
    (typeof item.tool_name === "string" && item.tool_name.trim()) ||
    "tool";
  const rawInput =
    item.arguments ??
    item.args ??
    item.input ??
    item.rawInput ??
    undefined;
  return {
    sessionUpdate: "tool_call",
    toolCallId,
    title: `${toolName}`,
    status: "completed",
    rawInput,
    kind: toolName,
  };
}

function buildHistoryToolResultUpdate(message, content) {
  const toolCallId =
    (typeof message.toolCallId === "string" && message.toolCallId.trim()) ||
    (typeof message.tool_call_id === "string" && message.tool_call_id.trim()) ||
    (typeof message.id === "string" && message.id.trim()) ||
    "";
  if (!toolCallId) {
    return null;
  }
  const rawOutput = readTextFromHistoryContent(content) || message.result || message.output || "";
  return {
    sessionUpdate: "tool_call_update",
    toolCallId,
    status: message.is_error || message.isError ? "failed" : "completed",
    rawOutput,
  };
}

/**
 * Converts persisted OpenClaw session transcript entries into ACP session updates so
 * `session/load` can reconstruct prior transcript state for any ACP client.
 */
export function buildHistoryReplayUpdates(messages = []) {
  if (!Array.isArray(messages)) {
    return [];
  }

  const updates = [];
  for (const [index, rawMessage] of messages.entries()) {
    if (!rawMessage || typeof rawMessage !== "object") {
      continue;
    }
    const historyMessageId =
      (typeof rawMessage.id === "string" && rawMessage.id.trim()) || `history-${index}`;
    const role = typeof rawMessage.role === "string" ? rawMessage.role.toLowerCase() : "";
    const content = normalizeHistoryContent(rawMessage.content);

    if (role === "user") {
      const text = readTextFromHistoryContent(content);
      if (text) {
        updates.push({
          sessionUpdate: "user_message_chunk",
          historyMessageId,
          content: { type: "text", text },
        });
      }
      continue;
    }

    if (role === "assistant") {
      for (const item of content) {
        const type = typeof item.type === "string" ? item.type.toLowerCase() : "";
        if (["toolcall", "tool_call", "tooluse", "tool_use"].includes(type)) {
          const toolUpdate = buildHistoryToolCallUpdate(item);
          if (toolUpdate) {
            updates.push(toolUpdate);
          }
        }
      }
      const text = readTextFromHistoryContent(content);
      if (text) {
        updates.push({
          sessionUpdate: "agent_message_chunk",
          historyMessageId,
          content: { type: "text", text },
        });
      }
      continue;
    }

    if (role === "toolresult" || role === "tool_result" || role === "tool") {
      const toolResultUpdate = buildHistoryToolResultUpdate(rawMessage, content);
      if (toolResultUpdate) {
        updates.push(toolResultUpdate);
      }
    }
  }

  return updates;
}

async function replayGatewayTranscript(agent, session) {
  if (!agent?.gateway?.request || !agent?.connection?.sessionUpdate) {
    return;
  }

  const transcript = await agent.gateway.request("sessions.get", {
    key: session.sessionKey,
    limit: 1000,
  });

  const updates = buildHistoryReplayUpdates(transcript?.messages);
  for (const update of updates) {
    await agent.connection.sessionUpdate({
      sessionId: session.sessionId,
      update,
    });
  }
}

/**
 * Returns the deterministic gateway session key used for ACP session IDs that
 * do not already carry an explicit gateway session key.
 */
export function buildBridgeFallbackSessionKey(sessionId, env = process.env) {
  const normalizedSessionID =
    typeof sessionId === "string" && sessionId.trim() ? sessionId.trim() : randomUUID();
  const agentId = normalizeBridgeToken(
    env.SPRITZ_OPENCLAW_ACP_FALLBACK_AGENT_ID,
    DEFAULT_FALLBACK_AGENT_ID,
  );
  const prefix = normalizeBridgeToken(
    env.SPRITZ_OPENCLAW_ACP_FALLBACK_SESSION_PREFIX,
    DEFAULT_FALLBACK_SESSION_PREFIX,
  );
  return `agent:${agentId}:${prefix}:${normalizedSessionID}`;
}

/**
 * Preserves explicit/listed gateway session keys and only maps ACP-generated
 * UUID session IDs onto deterministic OpenClaw gateway session keys.
 */
export function resolveBridgeFallbackSessionKey(sessionId, env = process.env) {
  const normalized = typeof sessionId === "string" ? sessionId.trim() : "";
  if (!normalized) {
    return buildBridgeFallbackSessionKey("", env);
  }
  if (!UUIDISH_SESSION_ID_PATTERN.test(normalized)) {
    return normalized;
  }
  return buildBridgeFallbackSessionKey(normalized, env);
}

/**
 * Extends OpenClaw's ACP gateway agent so the default ACP session flow maps to
 * normal agent-scoped gateway sessions instead of ACP runtime session keys.
 */
export function createSpritzAcpGatewayAgentClass(AcpGatewayAgent, env = process.env) {
  return class SpritzOpenclawAcpGatewayAgent extends AcpGatewayAgent {
    async newSession(params) {
      if (params.mcpServers.length > 0) {
        this.log(`ignoring ${params.mcpServers.length} MCP servers`);
      }
      this.enforceSessionCreateRateLimit("newSession");

      const sessionId = randomUUID();
      const meta = parseSessionMeta(params?._meta);
      const sessionKey = await this.resolveSessionKeyFromMeta({
        meta,
        fallbackKey: resolveBridgeFallbackSessionKey(sessionId, env),
      });

      const session = this.sessionStore.createSession({
        sessionId,
        sessionKey,
        cwd: params.cwd,
      });
      this.log(`newSession: ${session.sessionId} -> ${session.sessionKey}`);
      await this.sendAvailableCommands(session.sessionId);
      return { sessionId: session.sessionId };
    }

    async loadSession(params) {
      if (params.mcpServers.length > 0) {
        this.log(`ignoring ${params.mcpServers.length} MCP servers`);
      }
      if (!this.sessionStore.hasSession(params.sessionId)) {
        this.enforceSessionCreateRateLimit("loadSession");
      }

      const meta = parseSessionMeta(params?._meta);
      const sessionKey = await this.resolveSessionKeyFromMeta({
        meta,
        fallbackKey: resolveBridgeFallbackSessionKey(params.sessionId, env),
      });

      const session = this.sessionStore.createSession({
        sessionId: params.sessionId,
        sessionKey,
        cwd: params.cwd,
      });
      this.log(`loadSession: ${session.sessionId} -> ${session.sessionKey}`);
      await replayGatewayTranscript(this, session);
      await this.sendAvailableCommands(session.sessionId);
      return {};
    }
  };
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

export async function loadOpenclawCompat(env = process.env) {
  const packageRoot = resolveOpenclawPackageRoot(env);
  const resolvedPath = path.join(packageRoot, "dist", "spritz-acp-compat.js");
  return await import(pathToFileURL(resolvedPath).href);
}

async function serveSpritzOpenclawAcp(opts = {}, env = process.env) {
  const sdk = await importOpenclawDependency("@agentclientprotocol/sdk", env);
  const {
    AcpGatewayAgent,
    GatewayClient,
    buildGatewayConnectionDetails,
    loadConfig,
    resolveGatewayConnectionAuth,
  } = await loadOpenclawCompat(env);

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
  const SpritzAcpGatewayAgent = createSpritzAcpGatewayAgentClass(AcpGatewayAgent, env);

  new AgentSideConnection((connectionInstance) => {
    agent = new SpritzAcpGatewayAgent(connectionInstance, gateway, opts);
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
