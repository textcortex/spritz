#!/usr/bin/env node

import http from "node:http";
import { pathToFileURL } from "node:url";

import { resolveWSExports } from "../shared/spritz-acp-server.js";
import { importOpenclawDependency } from "./acp-wrapper.js";

const DEFAULT_PROXY_LISTEN_HOST = "127.0.0.1";
const DEFAULT_PROXY_LISTEN_PORT = 0;
const WEBSOCKET_OPEN = 1;

export function parseGatewayHeaders(raw) {
  if (typeof raw !== "string" || raw.trim() === "") {
    return null;
  }
  const decoded = JSON.parse(raw);
  if (!decoded || typeof decoded !== "object" || Array.isArray(decoded)) {
    throw new Error(
      "gateway proxy headers must be a JSON object of string header values",
    );
  }
  const headers = {};
  for (const [key, value] of Object.entries(decoded)) {
    if (typeof key !== "string" || typeof value !== "string") {
      continue;
    }
    const trimmedKey = key.trim();
    const trimmedValue = value.trim();
    if (!trimmedKey || !trimmedValue) {
      continue;
    }
    headers[trimmedKey] = trimmedValue;
  }
  return Object.keys(headers).length > 0 ? headers : null;
}

export function normalizeGatewayProxyHeaders(headers, upstreamURL, trustedProxyControlUi) {
  if (!headers || Object.keys(headers).length === 0) {
    return undefined;
  }
  const normalized = { ...headers };
  if (!trustedProxyControlUi || normalized.Origin || normalized.origin) {
    return normalized;
  }

  try {
    const parsed = new URL(upstreamURL);
    if (parsed.protocol === "ws:") {
      normalized.Origin = `http://${parsed.host}`;
      return normalized;
    }
    if (parsed.protocol === "wss:") {
      normalized.Origin = `https://${parsed.host}`;
      return normalized;
    }
    if (parsed.protocol === "http:" || parsed.protocol === "https:") {
      normalized.Origin = `${parsed.protocol}//${parsed.host}`;
      return normalized;
    }
  } catch {
    // Fall through to forwarded headers.
  }

  const scheme = normalized["X-Forwarded-Proto"] || normalized["x-forwarded-proto"] || "https";
  const host = normalized["X-Forwarded-Host"] || normalized["x-forwarded-host"] || "localhost";
  normalized.Origin = `${scheme}://${host}`;
  return normalized;
}

export function rewriteConnectFrameAsTrustedProxyControlUi(payload) {
  let frame;
  try {
    frame = JSON.parse(payload.toString("utf8"));
  } catch {
    return payload;
  }
  if (frame?.type !== "req" || frame?.method !== "connect" || !frame.params?.client) {
    return payload;
  }
  frame.params.client.id = "openclaw-control-ui";
  frame.params.client.mode = "webchat";
  return Buffer.from(JSON.stringify(frame), "utf8");
}

export function resolveUpgradeRequestPath(requestURL, fallbackPath) {
  if (typeof requestURL !== "string" || requestURL.trim() === "") {
    return fallbackPath;
  }

  try {
    return new URL(requestURL, "http://spritz-gateway-proxy.local").pathname || fallbackPath;
  } catch {
    const [path] = requestURL.split("?", 1);
    return path || fallbackPath;
  }
}

async function loadWSModule(env = process.env) {
  return await importOpenclawDependency("ws", env);
}

export async function startGatewayProxy(params) {
  const {
    upstreamURL,
    headers,
    trustedProxyControlUi = false,
    env = process.env,
    logger = console,
    listenHost = DEFAULT_PROXY_LISTEN_HOST,
    listenPort = DEFAULT_PROXY_LISTEN_PORT,
  } = params;
  const wsModule = await loadWSModule(env);
  const { WebSocket, WebSocketServer } = resolveWSExports(wsModule);
  if (!WebSocket || !WebSocketServer) {
    throw new Error("Failed to load ws module for the Spritz OpenClaw gateway proxy.");
  }

  const upstream = new URL(upstreamURL);
  const proxyServer = http.createServer((req, res) => {
    res.writeHead(404);
    res.end();
  });
  const wss = new WebSocketServer({ noServer: true });

  wss.on("connection", (clientSocket) => {
    const normalizedHeaders = normalizeGatewayProxyHeaders(
      headers,
      upstreamURL,
      trustedProxyControlUi,
    );
    const upstreamSocket = new WebSocket(
      upstreamURL,
      normalizedHeaders ? { headers: normalizedHeaders } : undefined,
    );

    let closed = false;
    const closeBoth = () => {
      if (closed) {
        return;
      }
      closed = true;
      try {
        clientSocket.close();
      } catch {}
      try {
        upstreamSocket.close();
      } catch {}
    };

    upstreamSocket.on("message", (data, isBinary) => {
      if (clientSocket.readyState !== WEBSOCKET_OPEN) {
        closeBoth();
        return;
      }
      clientSocket.send(data, { binary: isBinary });
    });
    upstreamSocket.on("close", () => {
      closeBoth();
    });
    upstreamSocket.on("error", (error) => {
      logger?.error?.(`gateway proxy upstream dial failed: ${String(error)}`);
      closeBoth();
    });

    clientSocket.on("message", (data, isBinary) => {
      if (upstreamSocket.readyState !== WEBSOCKET_OPEN) {
        return;
      }
      const payload = Buffer.isBuffer(data) ? data : Buffer.from(data);
      const nextPayload = trustedProxyControlUi
        ? rewriteConnectFrameAsTrustedProxyControlUi(payload)
        : payload;
      upstreamSocket.send(nextPayload, { binary: isBinary });
    });
    clientSocket.on("close", () => {
      closeBoth();
    });
    clientSocket.on("error", () => {
      closeBoth();
    });
  });

  proxyServer.on("upgrade", (request, socket, head) => {
    const requestPath = resolveUpgradeRequestPath(request.url, upstream.pathname);
    if (requestPath !== upstream.pathname) {
      socket.destroy();
      return;
    }
    wss.handleUpgrade(request, socket, head, (websocket) => {
      wss.emit("connection", websocket, request);
    });
  });

  await new Promise<void>((resolve, reject) => {
    proxyServer.once("error", reject);
    proxyServer.listen(listenPort, listenHost, () => resolve());
  });
  const address = proxyServer.address();
  if (!address || typeof address === "string") {
    throw new Error("failed to bind local gateway proxy");
  }
  const localURL = `ws://${listenHost}:${address.port}${upstream.pathname || "/"}${upstream.search || ""}`;

  return {
    localURL,
    close: async () => {
      for (const client of wss.clients) {
        try {
          client.close();
        } catch {}
      }
      await new Promise<void>((resolve) => proxyServer.close(() => resolve()));
    },
  };
}

export function gatewayProxyConfigFromEnv(env = process.env) {
  const upstreamURL = env.SPRITZ_OPENCLAW_GATEWAY_PROXY_UPSTREAM_URL?.trim();
  if (!upstreamURL) {
    throw new Error("SPRITZ_OPENCLAW_GATEWAY_PROXY_UPSTREAM_URL is required");
  }

  return {
    upstreamURL,
    headers: parseGatewayHeaders(env.SPRITZ_OPENCLAW_GATEWAY_PROXY_HEADERS_JSON),
    trustedProxyControlUi:
      env.SPRITZ_OPENCLAW_GATEWAY_PROXY_USE_CONTROL_UI_BRIDGE?.trim() === "1",
    listenHost:
      env.SPRITZ_OPENCLAW_GATEWAY_PROXY_LISTEN_HOST?.trim() || DEFAULT_PROXY_LISTEN_HOST,
    listenPort: Number.parseInt(
      env.SPRITZ_OPENCLAW_GATEWAY_PROXY_LISTEN_PORT ?? `${DEFAULT_PROXY_LISTEN_PORT}`,
      10,
    ),
  };
}

export async function serveSpritzOpenclawGatewayProxy(env = process.env, logger = console) {
  const config = gatewayProxyConfigFromEnv(env);
  const proxy = await startGatewayProxy({
    ...config,
    env,
    logger,
  });
  logger?.log?.(`spritz-openclaw-gateway-proxy listening on ${proxy.localURL}`);
  return proxy;
}

async function main() {
  const proxy = await serveSpritzOpenclawGatewayProxy();
  const shutdown = async (code) => {
    try {
      await proxy.close();
    } finally {
      process.exit(code);
    }
  };

  process.once("SIGINT", () => {
    void shutdown(0);
  });
  process.once("SIGTERM", () => {
    void shutdown(0);
  });

  await new Promise(() => {});
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch((error) => {
    console.error(String(error));
    process.exit(1);
  });
}
