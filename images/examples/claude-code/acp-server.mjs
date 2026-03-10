#!/usr/bin/env node

import { spawn, spawnSync } from "node:child_process";
import fs from "node:fs";
import http from "node:http";
import path from "node:path";
import { pathToFileURL } from "node:url";

const DEFAULTS = {
  listenAddr: "0.0.0.0:2529",
  acpPath: "/",
  healthPath: "/healthz",
  metadataPath: "/.well-known/spritz-acp",
  wsRoot: "/usr/local/lib/node_modules/ws",
  adapterBin: "claude-agent-acp",
  agentPackageRoot: "/usr/local/lib/node_modules/@zed-industries/claude-agent-acp",
  agentName: "claude-agent-acp",
  agentTitle: "Claude Code ACP Gateway",
  workdir: "/workspace",
  requiredEnv: ["ANTHROPIC_API_KEY"],
  shutdownTimeoutMs: 5_000,
};

const WEBSOCKET_OPEN = 1;
const DEFAULT_CLIENT_CAPABILITIES = Object.freeze({
  auth: {
    terminal: true,
    _meta: {
      gateway: true,
    },
  },
  _meta: {
    "terminal-auth": true,
    terminal_output: true,
  },
});

function trimPath(value, fallback) {
  const next = typeof value === "string" ? value.trim() : "";
  if (!next) {
    return fallback;
  }
  return next.startsWith("/") ? next : `/${next}`;
}

function parseListenAddr(value) {
  const raw = String(value || "").trim();
  if (!raw) {
    throw new Error("SPRITZ_CLAUDE_CODE_ACP_LISTEN_ADDR is required");
  }
  if (raw.startsWith("[")) {
    const closing = raw.indexOf("]");
    if (closing === -1 || raw[closing + 1] !== ":") {
      throw new Error(`invalid listen address: ${raw}`);
    }
    return {
      host: raw.slice(1, closing),
      port: Number.parseInt(raw.slice(closing + 2), 10),
    };
  }
  const separator = raw.lastIndexOf(":");
  if (separator === -1) {
    throw new Error(`invalid listen address: ${raw}`);
  }
  return {
    host: raw.slice(0, separator),
    port: Number.parseInt(raw.slice(separator + 1), 10),
  };
}

function parseArgsJSON(value) {
  if (typeof value !== "string" || value.trim() === "") {
    return [];
  }
  const parsed = JSON.parse(value);
  if (!Array.isArray(parsed) || parsed.some((item) => typeof item !== "string")) {
    throw new Error("SPRITZ_CLAUDE_CODE_ACP_ARGS_JSON must be a JSON array of strings");
  }
  return parsed.map((item) => item.trim()).filter(Boolean);
}

function parseRequiredEnv(value) {
  if (typeof value !== "string" || value.trim() === "") {
    return [...DEFAULTS.requiredEnv];
  }
  return value
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
}

function resolveMissingEnv(requiredEnv, env) {
  return requiredEnv.filter((name) => !String(env[name] || "").trim());
}

function commandExists(bin, env) {
  const result = spawnSync("/bin/sh", ["-lc", "command -v \"$0\" >/dev/null 2>&1", bin], {
    env,
    stdio: "ignore",
  });
  return result.status === 0;
}

function readVersion(packageRoot) {
  try {
    const pkg = JSON.parse(fs.readFileSync(path.join(packageRoot, "package.json"), "utf8"));
    return typeof pkg.version === "string" && pkg.version.trim() ? pkg.version.trim() : "unknown";
  } catch {
    return "unknown";
  }
}

function buildConfig(env) {
  const agentPackageRoot =
    env.SPRITZ_CLAUDE_CODE_AGENT_PACKAGE_ROOT || DEFAULTS.agentPackageRoot;
  return {
    listenAddr: env.SPRITZ_CLAUDE_CODE_ACP_LISTEN_ADDR || DEFAULTS.listenAddr,
    acpPath: trimPath(env.SPRITZ_CLAUDE_CODE_ACP_PATH, DEFAULTS.acpPath),
    healthPath: trimPath(env.SPRITZ_CLAUDE_CODE_ACP_HEALTH_PATH, DEFAULTS.healthPath),
    metadataPath: trimPath(env.SPRITZ_CLAUDE_CODE_ACP_METADATA_PATH, DEFAULTS.metadataPath),
    wsRoot: env.SPRITZ_CLAUDE_CODE_WS_PACKAGE_ROOT || DEFAULTS.wsRoot,
    adapterBin: env.SPRITZ_CLAUDE_CODE_ACP_BIN || DEFAULTS.adapterBin,
    adapterArgs: parseArgsJSON(env.SPRITZ_CLAUDE_CODE_ACP_ARGS_JSON),
    requiredEnv: parseRequiredEnv(env.SPRITZ_CLAUDE_CODE_REQUIRED_ENV),
    workdir: env.SPRITZ_CLAUDE_CODE_WORKDIR || DEFAULTS.workdir,
    metadata: {
      protocolVersion: 1,
      agentCapabilities: {
        loadSession: true,
        promptCapabilities: {
          image: true,
          embeddedContext: true,
        },
        mcpCapabilities: {
          http: true,
          sse: true,
        },
      },
      agentInfo: {
        name: env.SPRITZ_CLAUDE_CODE_AGENT_NAME || DEFAULTS.agentName,
        title: env.SPRITZ_CLAUDE_CODE_AGENT_TITLE || DEFAULTS.agentTitle,
        version: env.SPRITZ_CLAUDE_CODE_AGENT_VERSION || readVersion(agentPackageRoot),
      },
      authMethods: [],
    },
  };
}

function writeJSON(res, status, body) {
  res.writeHead(status, { "content-type": "application/json" });
  res.end(JSON.stringify(body));
}

function createLineForwarder(onLine) {
  let pending = "";
  return (chunk) => {
    pending += chunk.toString("utf8");
    let newline = pending.indexOf("\n");
    while (newline !== -1) {
      const line = pending.slice(0, newline).replace(/\r$/, "");
      pending = pending.slice(newline + 1);
      if (line.trim()) {
        onLine(line);
      }
      newline = pending.indexOf("\n");
    }
  };
}

function ensureLine(data) {
  const payload = Buffer.isBuffer(data) ? data : Buffer.from(data);
  return payload[payload.length - 1] === 0x0a
    ? payload
    : Buffer.concat([payload, Buffer.from("\n")]);
}

function parseACPMessage(line) {
  try {
    return JSON.parse(line);
  } catch (error) {
    return {
      jsonrpc: "2.0",
      invalid: true,
      error,
      raw: line,
    };
  }
}

function cloneACPValue(value) {
  return value === undefined ? undefined : JSON.parse(JSON.stringify(value));
}

function buildDefaultInitializeRequest(config) {
  return {
    protocolVersion: 1,
    clientCapabilities: cloneACPValue(DEFAULT_CLIENT_CAPABILITIES),
    clientInfo: {
      name: config.metadata?.agentInfo?.name || DEFAULTS.agentName,
      title: config.metadata?.agentInfo?.title || DEFAULTS.agentTitle,
      version: config.metadata?.agentInfo?.version || "1.0.0",
    },
  };
}

function normalizeInitializeRequest(config, request) {
  const fallback = buildDefaultInitializeRequest(config);
  const params = request && typeof request === "object" ? request : {};
  return {
    protocolVersion: Number.isInteger(params.protocolVersion) ? params.protocolVersion : fallback.protocolVersion,
    clientCapabilities:
      params.clientCapabilities && typeof params.clientCapabilities === "object"
        ? cloneACPValue(params.clientCapabilities)
        : fallback.clientCapabilities,
    clientInfo:
      params.clientInfo && typeof params.clientInfo === "object"
        ? {
            name: String(params.clientInfo.name || fallback.clientInfo.name),
            title: String(params.clientInfo.title || fallback.clientInfo.title),
            version: String(params.clientInfo.version || fallback.clientInfo.version),
          }
        : fallback.clientInfo,
  };
}

function normalizeInitializeResult(config, result) {
  const fallback = config.metadata || {};
  const payload = result && typeof result === "object" ? result : {};
  return {
    protocolVersion: Number.isInteger(payload.protocolVersion)
      ? payload.protocolVersion
      : fallback.protocolVersion || 1,
    agentCapabilities:
      payload.agentCapabilities && typeof payload.agentCapabilities === "object"
        ? cloneACPValue(payload.agentCapabilities)
        : cloneACPValue(fallback.agentCapabilities || {}),
    agentInfo:
      payload.agentInfo && typeof payload.agentInfo === "object"
        ? cloneACPValue(payload.agentInfo)
        : cloneACPValue(fallback.agentInfo || {}),
    authMethods: Array.isArray(payload.authMethods)
      ? cloneACPValue(payload.authMethods)
      : cloneACPValue(fallback.authMethods || []),
  };
}

function closeChild(child, timerRef) {
  if (child.exitCode !== null || child.killed) {
    return;
  }
  child.stdin.end();
  child.kill("SIGTERM");
  timerRef.current = setTimeout(() => {
    if (child.exitCode === null && !child.killed) {
      child.kill("SIGKILL");
    }
  }, DEFAULTS.shutdownTimeoutMs);
}

/**
 * Owns the long-lived claude-agent-acp child process for the workspace.
 * The runtime survives websocket reconnects so ACP session ids remain valid
 * between Spritz bootstrap and the browser chat connection.
 */
class ACPRuntime {
  constructor(config, env, logger) {
    this.config = config;
    this.env = env;
    this.logger = logger;
    this.child = null;
    this.killTimer = { current: null };
    this.socket = null;
    this.internalRequestID = 1;
    this.internalPending = new Map();
    this.initialized = false;
    this.initializing = null;
    this.initializeResult = null;
  }

  ensureStarted() {
    if (this.child && this.child.exitCode === null && !this.child.killed) {
      return;
    }
    const child = spawn(this.config.adapterBin, this.config.adapterArgs, {
      cwd: this.config.workdir,
      env: this.env,
      stdio: ["pipe", "pipe", "pipe"],
    });
    child.stdout.on(
      "data",
      createLineForwarder((line) => {
        const message = parseACPMessage(line);
        if (message.invalid) {
          this.logger.error?.(`[claude-code-acp] invalid JSON from adapter: ${message.raw}`);
          return;
        }
        if (message.id !== undefined) {
          const pending = this.internalPending.get(String(message.id));
          if (pending) {
            this.internalPending.delete(String(message.id));
            if (message.error) {
              pending.reject(message.error);
            } else {
              pending.resolve(message.result);
            }
            return;
          }
        }
        if (this.socket?.readyState === WEBSOCKET_OPEN) {
          this.socket.send(JSON.stringify(message));
        }
      }),
    );
    child.stderr.on(
      "data",
      createLineForwarder((line) => this.logger.error?.(`[claude-code-acp] ${line}`)),
    );
    child.on("error", (error) => {
      this.logger.error?.(`claude-agent-acp failed to start: ${String(error)}`);
      this.closeSocket(1011, "claude-agent-acp failed to start");
      this.rejectInternalRequests({
        code: -32000,
        message: `claude-agent-acp failed to start: ${String(error)}`,
      });
      this.child = null;
      this.initialized = false;
      this.initializing = null;
      this.initializeResult = null;
    });
    child.on("exit", (code, signal) => {
      if (this.killTimer.current) {
        clearTimeout(this.killTimer.current);
      }
      this.child = null;
      this.initialized = false;
      this.initializing = null;
      this.initializeResult = null;
      this.rejectInternalRequests({
        code: -32000,
        message: signal ? `signal ${signal}` : `exit code ${code ?? 0}`,
      });
      const status = signal ? `signal ${signal}` : `exit code ${code ?? 0}`;
      this.logger.warn?.(`claude-agent-acp exited with ${status}`);
      this.closeSocket(1011, `claude-agent-acp exited with ${status}`);
    });
    this.child = child;
  }

  rejectInternalRequests(error) {
    for (const pending of this.internalPending.values()) {
      pending.reject(error);
    }
    this.internalPending.clear();
  }

  requestChild(method, params) {
    this.ensureStarted();
    return new Promise((resolve, reject) => {
      const id = `spritz-runtime-${this.internalRequestID++}`;
      this.internalPending.set(String(id), { resolve, reject });
      this.child.stdin.write(
        ensureLine(
          JSON.stringify({
            jsonrpc: "2.0",
            id,
            method,
            params,
          }),
        ),
      );
    });
  }

  async ensureInitialized(request) {
    if (this.initialized) {
      return this.initializeResult;
    }
    if (this.initializing) {
      await this.initializing;
      return this.initializeResult;
    }
    const initializeRequest = normalizeInitializeRequest(this.config, request);
    this.initializing = this.requestChild("initialize", initializeRequest)
      .then((result) => {
        this.initializeResult = normalizeInitializeResult(this.config, result);
        this.initialized = true;
        return this.initializeResult;
      })
      .finally(() => {
        this.initializing = null;
      });
    return this.initializing;
  }

  closeSocket(code, reason) {
    if (this.socket?.readyState === WEBSOCKET_OPEN) {
      this.socket.close(code, reason);
    }
    this.socket = null;
  }

  attach(socket) {
    if (this.socket && this.socket.readyState === WEBSOCKET_OPEN) {
      this.socket.close(1001, "ACP client replaced");
    }
    this.ensureStarted();
    this.socket = socket;
    socket.on("message", async (data) => {
      try {
        const payload = parseACPMessage(Buffer.isBuffer(data) ? data.toString("utf8") : String(data));
        if (payload.invalid) {
          socket.close(1002, "invalid ACP JSON");
          return;
        }
        if (payload.method === "initialize" && payload.id !== undefined) {
          const result = await this.ensureInitialized(payload.params);
          if (socket.readyState === WEBSOCKET_OPEN) {
            socket.send(
              JSON.stringify({
                jsonrpc: "2.0",
                id: payload.id,
                result,
              }),
            );
          }
          return;
        }
        await this.ensureInitialized();
        if (this.child?.stdin && !this.child.stdin.destroyed) {
          this.child.stdin.write(ensureLine(JSON.stringify(payload)));
        }
      } catch (error) {
        this.logger.error?.(`claude-code ACP bridge error: ${String(error?.message || error)}`);
        socket.close(1011, "claude-code ACP bridge error");
      }
    });
    socket.on("close", () => {
      if (this.socket === socket) {
        this.socket = null;
      }
    });
    socket.on("error", (error) => {
      this.logger.warn?.(`claude-code ACP websocket error: ${String(error)}`);
      if (this.socket === socket) {
        this.socket = null;
      }
    });
  }

  stop() {
    this.closeSocket(1001, "server shutting down");
    if (this.child) {
      closeChild(this.child, this.killTimer);
    }
  }
}

async function main(env = process.env, logger = console) {
  const config = buildConfig(env);
  const wsModule = await import(pathToFileURL(path.join(config.wsRoot, "index.js")).href);
  const WebSocketServer =
    wsModule?.WebSocketServer ?? wsModule?.default?.WebSocketServer ?? wsModule?.default;
  if (!WebSocketServer) {
    throw new Error("Failed to load WebSocketServer from ws");
  }

  const runtime = new ACPRuntime(config, env, logger);
  const server = http.createServer((req, res) => {
    const pathname = new URL(req.url ?? "/", "http://spritz-acp.local").pathname;
    if (req.method === "GET" && pathname === config.healthPath) {
      const missingEnv = resolveMissingEnv(config.requiredEnv, env);
      if (missingEnv.length > 0) {
        writeJSON(res, 503, { ok: false, error: `missing required env: ${missingEnv.join(", ")}` });
        return;
      }
      if (!commandExists(config.adapterBin, env)) {
        writeJSON(res, 503, { ok: false, error: `command not found: ${config.adapterBin}` });
        return;
      }
      writeJSON(res, 200, { ok: true });
      return;
    }
    if (req.method === "GET" && pathname === config.metadataPath) {
      writeJSON(res, 200, config.metadata);
      return;
    }
    if (req.method === "GET" && pathname === config.acpPath) {
      res.writeHead(426, { "content-type": "text/plain; charset=utf-8" });
      res.end("upgrade required");
      return;
    }
    res.writeHead(404);
    res.end();
  });

  const wss = new WebSocketServer({ noServer: true });
  wss.on("connection", (socket) => {
    const missingEnv = resolveMissingEnv(config.requiredEnv, env);
    if (missingEnv.length > 0) {
      socket.close(1011, `missing required env: ${missingEnv.join(", ")}`);
      return;
    }
    runtime.attach(socket);
  });

  server.on("upgrade", (request, socket, head) => {
    const pathname = new URL(request.url ?? "/", "http://spritz-acp.local").pathname;
    if (pathname !== config.acpPath) {
      socket.destroy();
      return;
    }
    wss.handleUpgrade(request, socket, head, (websocket) => {
      wss.emit("connection", websocket, request);
    });
  });

  const { host, port } = parseListenAddr(config.listenAddr);
  server.listen(port, host, () => {
    logger.log?.(`spritz-claude-code-acp-server listening on ${host}:${port}${config.acpPath}`);
  });
  let shuttingDown = false;
  const shutdown = () => {
    if (shuttingDown) {
      return;
    }
    shuttingDown = true;
    runtime.stop();
    server.closeIdleConnections?.();
    server.closeAllConnections?.();
    server.close(() => {
      process.exit(0);
    });
    setTimeout(() => process.exit(1), DEFAULTS.shutdownTimeoutMs).unref();
  };
  process.once("SIGTERM", shutdown);
  process.once("SIGINT", shutdown);
}

const entrypoint = process.argv[1];

if (entrypoint && import.meta.url === pathToFileURL(entrypoint).href) {
  main().catch((error) => {
    console.error(error instanceof Error ? error.stack || error.message : String(error));
    process.exit(1);
  });
}

export { ACPRuntime, buildConfig, main };
