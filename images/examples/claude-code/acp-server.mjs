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

async function main(env = process.env, logger = console) {
  const config = buildConfig(env);
  const wsModule = await import(pathToFileURL(path.join(config.wsRoot, "index.js")).href);
  const WebSocketServer =
    wsModule?.WebSocketServer ?? wsModule?.default?.WebSocketServer ?? wsModule?.default;
  if (!WebSocketServer) {
    throw new Error("Failed to load WebSocketServer from ws");
  }

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
    const child = spawn(config.adapterBin, config.adapterArgs, {
      cwd: config.workdir,
      env,
      stdio: ["pipe", "pipe", "pipe"],
    });
    const killTimer = { current: null };
    let closed = false;

    const closeSocket = (code, reason) => {
      if (closed) {
        return;
      }
      closed = true;
      if (socket.readyState === WEBSOCKET_OPEN) {
        socket.close(code, reason);
      }
      closeChild(child, killTimer);
    };

    child.stdout.on(
      "data",
      createLineForwarder((line) => {
        if (socket.readyState !== WEBSOCKET_OPEN) {
          closeChild(child, killTimer);
          return;
        }
        socket.send(line);
      }),
    );
    child.stderr.on("data", createLineForwarder((line) => logger.error?.(`[claude-code-acp] ${line}`)));
    child.on("error", (error) => {
      logger.error?.(`claude-agent-acp failed to start: ${String(error)}`);
      closeSocket(1011, "claude-agent-acp failed to start");
    });
    child.on("exit", (code, signal) => {
      if (killTimer.current) {
        clearTimeout(killTimer.current);
      }
      if (!closed) {
        const status = signal ? `signal ${signal}` : `exit code ${code ?? 0}`;
        logger.warn?.(`claude-agent-acp exited with ${status}`);
        closeSocket(1011, `claude-agent-acp exited with ${status}`);
      }
    });
    socket.on("message", (data) => {
      if (!child.stdin.destroyed) {
        child.stdin.write(ensureLine(data));
      }
    });
    socket.on("close", () => closeSocket(1000, "client closed"));
    socket.on("error", (error) => {
      logger.warn?.(`claude-code ACP websocket error: ${String(error)}`);
      closeSocket(1011, "client websocket error");
    });
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
}

if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch((error) => {
    console.error(error instanceof Error ? error.stack || error.message : String(error));
    process.exit(1);
  });
}
