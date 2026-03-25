#!/usr/bin/env node

import http from "node:http";
import net from "node:net";
import { PassThrough, Readable, Writable } from "node:stream";
import { pathToFileURL } from "node:url";

import {
  parseGatewayHeaders,
  startGatewayProxy,
} from "./gateway-proxy.js";
import {
  normalizePath,
  resolveWSExports,
  serveSpritzACPServer,
} from "../shared/spritz-acp-server.js";
import {
  buildGatewayClientOptions,
  buildSpritzOpenclawAcpMetadata,
  createLazyGatewayController,
  createSpritzAcpGatewayAgentClass,
  importOpenclawDependency,
  loadAcpSdk,
  loadOpenclawCompat,
  parseArgs,
  readOpenclawPackageVersion,
  useTrustedProxyControlUiBridge,
} from "./acp-wrapper.js";

const DEFAULT_LISTEN_ADDR = "0.0.0.0:2529";
const DEFAULT_ACP_PATH = "/";
const DEFAULT_HEALTH_PATH = "/healthz";
const DEFAULT_METADATA_PATH = "/.well-known/spritz-acp";
const DEFAULT_UPSTREAM_TIMEOUT_MS = 500;
const WEBSOCKET_OPEN = 1;

type GatewayAgent = {
  handleGatewayDisconnect: (reason: string) => void;
  handleGatewayEvent: (event: unknown) => Promise<void> | void;
  handleGatewayReconnect: () => void;
  start: () => void;
};

function parseBoolEnv(key, fallback, env = process.env) {
  const value = env[key];
  if (typeof value !== "string" || value.trim() === "") {
    return fallback;
  }
  switch (value.trim().toLowerCase()) {
    case "1":
    case "true":
    case "yes":
    case "on":
      return true;
    case "0":
    case "false":
    case "no":
    case "off":
      return false;
    default:
      return fallback;
  }
}

async function loadWSModule(env = process.env) {
  return await importOpenclawDependency("ws", env);
}

function encodeFrameForACPInput(data) {
  const payload = Buffer.isBuffer(data) ? data : Buffer.from(data);
  if (payload.length === 0) {
    return null;
  }
  if (payload[payload.length - 1] === 0x0a) {
    return payload;
  }
  return Buffer.concat([payload, Buffer.from("\n")]);
}

async function createRuntime(config, env = process.env, logger = console) {
  const [sdk, compat] = await Promise.all([loadAcpSdk(env), loadOpenclawCompat(env)]);
  const AgentSideConnection = sdk.AgentSideConnection ?? sdk.default?.AgentSideConnection;
  const ndJsonStream = sdk.ndJsonStream ?? sdk.default?.ndJsonStream;
  if (!AgentSideConnection || !ndJsonStream) {
    throw new Error("Failed to load ACP SDK from the installed OpenClaw package.");
  }

  const {
    AcpGatewayAgent,
    GatewayClient,
    buildGatewayConnectionDetails,
    loadConfig,
    resolveGatewayConnectionAuth,
  } = compat;

  const openclawConfig = loadConfig();
  const connection = buildGatewayConnectionDetails({
    config: openclawConfig,
    url: config.gatewayURL,
  });
  const gatewayURLOverrideSource =
    connection.urlSource === "cli --url"
      ? "cli"
      : connection.urlSource === "env OPENCLAW_GATEWAY_URL"
        ? "env"
        : undefined;
  const creds = await resolveGatewayConnectionAuth({
    config: openclawConfig,
    explicitAuth: {
      token: config.gatewayToken,
      password: config.gatewayPassword,
    },
    env,
    urlOverride: gatewayURLOverrideSource ? connection.url : undefined,
    urlOverrideSource: gatewayURLOverrideSource,
  });

  let gatewayCleanup: () => Promise<void> = async () => {};
  let effectiveGatewayURL = connection.url;
  if (config.gatewayHeaders) {
    const proxy = await startGatewayProxy({
      upstreamURL: connection.url,
      headers: config.gatewayHeaders,
      trustedProxyControlUi: config.trustedProxyControlUi,
      env,
      logger,
    });
    effectiveGatewayURL = proxy.localURL;
    gatewayCleanup = proxy.close;
  }

  const agents = new Set<GatewayAgent>();
  let stopped = false;
  let onGatewayReadyResolve: () => void = () => {};
  let onGatewayReadyReject: (error: Error) => void = () => {};
  let gatewayReadySettled = false;
  const gatewayReady = new Promise<void>((resolve, reject) => {
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
      connectionUrl: effectiveGatewayURL,
      gatewayToken: config.trustedProxyControlUi ? undefined : creds.token,
      gatewayPassword: config.trustedProxyControlUi ? undefined : creds.password,
      trustedProxyControlUi: config.trustedProxyControlUi,
    }),
    onEvent: (evt) => {
      for (const agent of agents) {
        void agent.handleGatewayEvent(evt);
      }
    },
    onHelloOk: () => {
      resolveGatewayReady();
      for (const agent of agents) {
        agent.handleGatewayReconnect();
      }
    },
    onConnectError: (error) => {
      rejectGatewayReady(error);
    },
    onClose: (code, reason) => {
      if (!stopped) {
        rejectGatewayReady(new Error(`gateway closed before ready (${code}): ${reason}`));
      }
      for (const agent of agents) {
        agent.handleGatewayDisconnect(`${code}: ${reason}`);
      }
    },
  });

  const gatewayController = createLazyGatewayController(gateway, {
    waitUntilReady: () => gatewayReady,
    onStop: () => {
      resolveGatewayReady();
    },
  });
  const SpritzAcpGatewayAgent = createSpritzAcpGatewayAgentClass(AcpGatewayAgent, env, {
    ensureGatewayReady: () => gatewayController.ensureReady(),
  });

  return {
    metadata: buildSpritzOpenclawAcpMetadata(readOpenclawPackageVersion(env)),
    async health() {
      try {
        await checkUpstreamReachability(connection.url, config.upstreamCheckTimeoutMs);
        return { ok: true };
      } catch (error) {
        return {
          ok: false,
          error: error instanceof Error ? error.message : String(error),
        };
      }
    },
    attachWebSocket(websocket) {
      const inbound = new PassThrough();
      const outbound = new PassThrough();
      let agent: GatewayAgent | null = null;
      let closed = false;
      let pendingOutput = Buffer.alloc(0);

      const cleanup = (reason = "ACP client disconnected") => {
        if (closed) {
          return;
        }
        closed = true;
        inbound.end();
        outbound.destroy();
        if (agent) {
          agents.delete(agent);
          agent.handleGatewayDisconnect(reason);
        }
        try {
          websocket.close();
        } catch {}
      };

      outbound.on("data", (chunk) => {
        pendingOutput = Buffer.concat([pendingOutput, Buffer.from(chunk)]);
        while (true) {
          const newlineIndex = pendingOutput.indexOf(0x0a);
          if (newlineIndex === -1) {
            break;
          }
          const line = pendingOutput.subarray(0, newlineIndex).toString("utf8").replace(/\r$/, "");
          pendingOutput = pendingOutput.subarray(newlineIndex + 1);
          if (!line) {
            continue;
          }
          if (websocket.readyState === WEBSOCKET_OPEN) {
            websocket.send(line);
          }
        }
      });
      outbound.on("error", () => cleanup("ACP output stream failed"));

      websocket.on("message", (data) => {
        const payload = encodeFrameForACPInput(data);
        if (payload) {
          inbound.write(payload);
        }
      });
      websocket.on("close", () => cleanup());
      websocket.on("error", () => cleanup("ACP websocket failed"));

      const stream = ndJsonStream(Writable.toWeb(outbound), Readable.toWeb(inbound));
      new AgentSideConnection((connectionInstance: unknown) => {
        agent = new (SpritzAcpGatewayAgent as any)(
          connectionInstance,
          gateway,
          config.agentOptions,
        );
        agents.add(agent);
        agent.start();
        return agent;
      }, stream);
    },
    async close() {
      stopped = true;
      gatewayController.stop();
      for (const agent of agents) {
        agent.handleGatewayDisconnect("ACP server stopping");
      }
      agents.clear();
      await gatewayCleanup();
    },
  };
}

async function checkUpstreamReachability(rawURL, timeoutMs) {
  const parsed = new URL(rawURL);
  const port = parsed.port
    ? Number.parseInt(parsed.port, 10)
    : parsed.protocol === "wss:" || parsed.protocol === "https:"
      ? 443
      : 80;

  await new Promise<void>((resolve, reject) => {
    const socket = net.createConnection({ host: parsed.hostname, port });
    const timeout = setTimeout(() => {
      socket.destroy();
      reject(new Error(`upstream reachability timed out for ${parsed.hostname}:${port}`));
    }, timeoutMs);

    socket.once("connect", () => {
      clearTimeout(timeout);
      socket.end();
      resolve();
    });
    socket.once("error", (error) => {
      clearTimeout(timeout);
      reject(error);
    });
  });
}

export function configFromEnv(env = process.env) {
  const gatewayURL = env.SPRITZ_OPENCLAW_ACP_GATEWAY_URL?.trim();
  if (!gatewayURL) {
    throw new Error("SPRITZ_OPENCLAW_ACP_GATEWAY_URL is required");
  }

  const listenAddr = env.SPRITZ_OPENCLAW_ACP_LISTEN_ADDR?.trim() || DEFAULT_LISTEN_ADDR;
  const acpPath = normalizePath(env.SPRITZ_OPENCLAW_ACP_PATH, DEFAULT_ACP_PATH);
  const healthPath = normalizePath(env.SPRITZ_OPENCLAW_ACP_HEALTH_PATH, DEFAULT_HEALTH_PATH);
  const metadataPath = normalizePath(
    env.SPRITZ_OPENCLAW_ACP_METADATA_PATH,
    DEFAULT_METADATA_PATH,
  );
  const gatewayHeaders = parseGatewayHeaders(env.SPRITZ_OPENCLAW_ACP_GATEWAY_HEADERS_JSON);
  const trustedProxyControlUi = useTrustedProxyControlUiBridge(env);

  return {
    listenAddr,
    acpPath,
    healthPath,
    metadataPath,
    gatewayURL,
    gatewayHeaders,
    trustedProxyControlUi,
    gatewayToken: env.OPENCLAW_GATEWAY_TOKEN?.trim() || undefined,
    gatewayPassword: env.OPENCLAW_GATEWAY_PASSWORD?.trim() || undefined,
    upstreamCheckTimeoutMs: Number.parseInt(
      env.SPRITZ_OPENCLAW_ACP_UPSTREAM_CHECK_TIMEOUT_MS ?? `${DEFAULT_UPSTREAM_TIMEOUT_MS}`,
      10,
    ),
    agentOptions: parseArgs([
      "acp",
      "--url",
      gatewayURL,
      ...(env.SPRITZ_OPENCLAW_ACP_GATEWAY_TOKEN_FILE
        ? ["--token-file", env.SPRITZ_OPENCLAW_ACP_GATEWAY_TOKEN_FILE]
        : []),
      ...(env.SPRITZ_OPENCLAW_ACP_GATEWAY_PASSWORD_FILE
        ? ["--password-file", env.SPRITZ_OPENCLAW_ACP_GATEWAY_PASSWORD_FILE]
        : []),
      ...(env.SPRITZ_OPENCLAW_ACP_PROVENANCE ? ["--provenance", env.SPRITZ_OPENCLAW_ACP_PROVENANCE] : []),
      ...(parseBoolEnv("SPRITZ_OPENCLAW_ACP_VERBOSE", false, env) ? ["--verbose"] : []),
    ]),
  };
}

export async function serveSpritzOpenclawAcpServer(env = process.env, logger = console) {
  const config = configFromEnv(env);
  const runtime = await createRuntime(config, env, logger);
  return serveSpritzACPServer({
    config,
    runtime,
    loadWSModule: async () => loadWSModule(env),
    logger,
    serverName: "spritz-openclaw-acp-server",
  });
}

async function main() {
  await serveSpritzOpenclawAcpServer();
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch((error) => {
    console.error(String(error));
    process.exit(1);
  });
}
