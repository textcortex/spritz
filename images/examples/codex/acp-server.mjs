#!/usr/bin/env node

import { spawn, spawnSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { pathToFileURL } from "node:url";

import {
  normalizePath,
  serveSpritzACPServer,
} from "../shared/spritz-acp-server.mjs";

const DEFAULTS = {
  listenAddr: "0.0.0.0:2529",
  acpPath: "/",
  healthPath: "/healthz",
  metadataPath: "/.well-known/spritz-acp",
  wsRoot: "/usr/local/lib/node_modules/ws",
  codexBin: "codex",
  codexArgs: ["--dangerously-bypass-approvals-and-sandbox"],
  codexPackageRoot: "/usr/local/lib/node_modules/@openai/codex",
  agentName: "codex-cli",
  agentTitle: "Codex ACP Gateway",
  workdir: "/workspace",
  requiredEnv: ["OPENAI_API_KEY"],
  model: "",
  profile: "",
  shutdownTimeoutMs: 5_000,
};

const WEBSOCKET_OPEN = 1;

function parseArgsJSON(value) {
  if (typeof value !== "string" || value.trim() === "") {
    return [...DEFAULTS.codexArgs];
  }
  const parsed = JSON.parse(value);
  if (!Array.isArray(parsed) || parsed.some((item) => typeof item !== "string")) {
    throw new Error("SPRITZ_CODEX_ARGS_JSON must be a JSON array of strings");
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

function cloneACPValue(value) {
  return value === undefined ? undefined : JSON.parse(JSON.stringify(value));
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

function ensureLine(data) {
  const payload = Buffer.isBuffer(data) ? data : Buffer.from(data);
  return payload[payload.length - 1] === 0x0a
    ? payload
    : Buffer.concat([payload, Buffer.from("\n")]);
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

function extractACPText(value) {
  if (value === null || value === undefined) {
    return "";
  }
  if (typeof value === "string") {
    return value;
  }
  if (Array.isArray(value)) {
    return value.map((item) => extractACPText(item)).filter(Boolean).join("\n");
  }
  if (typeof value !== "object") {
    return String(value);
  }
  if (typeof value.text === "string") {
    return value.text;
  }
  if (value.type === "content" && value.content) {
    return extractACPText(value.content);
  }
  if (value.content) {
    return extractACPText(value.content);
  }
  return "";
}

function extractPromptText(prompt) {
  const text = extractACPText(prompt).trim();
  if (!text) {
    throw new Error("session prompt did not contain any text");
  }
  return text;
}

function buildConfig(env) {
  const codexPackageRoot = env.SPRITZ_CODEX_PACKAGE_ROOT || DEFAULTS.codexPackageRoot;
  const model = String(env.SPRITZ_CODEX_MODEL || DEFAULTS.model || "").trim();
  const profile = String(env.SPRITZ_CODEX_PROFILE || DEFAULTS.profile || "").trim();
  const configOptions = [];
  if (model) {
    configOptions.push({
      id: "model",
      name: "Model",
      currentValue: model,
    });
  }
  return {
    listenAddr: env.SPRITZ_CODEX_ACP_LISTEN_ADDR || DEFAULTS.listenAddr,
    acpPath: normalizePath(env.SPRITZ_CODEX_ACP_PATH, DEFAULTS.acpPath),
    healthPath: normalizePath(env.SPRITZ_CODEX_ACP_HEALTH_PATH, DEFAULTS.healthPath),
    metadataPath: normalizePath(env.SPRITZ_CODEX_ACP_METADATA_PATH, DEFAULTS.metadataPath),
    wsRoot: env.SPRITZ_CODEX_WS_PACKAGE_ROOT || DEFAULTS.wsRoot,
    codexBin: env.SPRITZ_CODEX_BIN || DEFAULTS.codexBin,
    codexArgs: parseArgsJSON(env.SPRITZ_CODEX_ARGS_JSON),
    workdir: env.SPRITZ_CODEX_WORKDIR || DEFAULTS.workdir,
    requiredEnv: parseRequiredEnv(env.SPRITZ_CODEX_REQUIRED_ENV),
    agentName: env.SPRITZ_CODEX_AGENT_NAME || DEFAULTS.agentName,
    agentTitle: env.SPRITZ_CODEX_AGENT_TITLE || DEFAULTS.agentTitle,
    agentVersion: env.SPRITZ_CODEX_AGENT_VERSION || readVersion(codexPackageRoot),
    model,
    profile,
    configOptions,
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
        name: env.SPRITZ_CODEX_AGENT_NAME || DEFAULTS.agentName,
        title: env.SPRITZ_CODEX_AGENT_TITLE || DEFAULTS.agentTitle,
        version: env.SPRITZ_CODEX_AGENT_VERSION || readVersion(codexPackageRoot),
      },
      authMethods: [],
    },
  };
}

function buildSessionState(config, cwd) {
  return {
    sessionId: randomUUID(),
    cwd,
    threadId: "",
    transcript: [],
    modes: {
      currentModeId: "default",
    },
    models: config.model
      ? {
          currentModelId: config.model,
        }
      : {},
    configOptions: cloneACPValue(config.configOptions),
  };
}

function buildSessionLoadResult(session) {
  return {
    modes: cloneACPValue(session.modes) || {},
    models: cloneACPValue(session.models) || {},
    configOptions: cloneACPValue(session.configOptions) || [],
  };
}

function closeChild(child, timerRef) {
  if (child.exitCode !== null || child.killed) {
    return;
  }
  child.kill("SIGTERM");
  timerRef.current = setTimeout(() => {
    if (child.exitCode === null && !child.killed) {
      child.kill("SIGKILL");
    }
  }, DEFAULTS.shutdownTimeoutMs);
}

class CodexACPRuntime {
  constructor(config, env, logger) {
    this.config = config;
    this.env = env;
    this.logger = logger;
    this.socket = null;
    this.killTimer = { current: null };
    this.sessions = new Map();
    this.activePrompt = null;
  }

  normalizeRPCError(error) {
    if (error && typeof error === "object" && Number.isInteger(error.code) && typeof error.message === "string") {
      return {
        code: error.code,
        message: error.message,
        ...(error.data !== undefined ? { data: error.data } : {}),
      };
    }
    return {
      code: -32000,
      message: error instanceof Error ? error.message : String(error || "ACP request failed."),
    };
  }

  sendRPCResponse(socket, id, body) {
    if (socket.readyState !== WEBSOCKET_OPEN) {
      return;
    }
    socket.send(JSON.stringify({ jsonrpc: "2.0", id, ...body }));
  }

  sendSessionUpdate(update) {
    if (this.socket?.readyState !== WEBSOCKET_OPEN) {
      return;
    }
    this.socket.send(
      JSON.stringify({
        jsonrpc: "2.0",
        method: "session/update",
        params: { update },
      }),
    );
  }

  closeSocket(code, reason) {
    if (this.socket?.readyState === WEBSOCKET_OPEN) {
      this.socket.close(code, reason);
    }
    this.socket = null;
  }

  claimSocket(socket) {
    if (this.socket === socket) {
      return;
    }
    const previousSocket = this.socket;
    this.socket = socket;
    if (previousSocket?.readyState === WEBSOCKET_OPEN) {
      previousSocket.close(1001, "ACP client replaced");
    }
  }

  replayTranscript(session) {
    for (const message of session.transcript) {
      this.sendSessionUpdate({
        sessionUpdate: message.role === "user" ? "user_message_chunk" : "agent_message_chunk",
        content: { type: "text", text: message.text },
        historyMessageId: message.id,
      });
    }
  }

  buildCodexCommand(session, promptText, outputFile) {
    const modelArgs = this.config.model ? ["--model", this.config.model] : [];
    const profileArgs = this.config.profile ? ["-p", this.config.profile] : [];
    if (session.threadId) {
      return [
        "exec",
        ...profileArgs,
        "resume",
        "--json",
        "--skip-git-repo-check",
        ...modelArgs,
        ...this.config.codexArgs,
        "--output-last-message",
        outputFile,
        session.threadId,
        promptText,
      ];
    }
    return [
      "exec",
      ...profileArgs,
      "--json",
      "--skip-git-repo-check",
      "-C",
      session.cwd,
      ...modelArgs,
      ...this.config.codexArgs,
      "--output-last-message",
      outputFile,
      promptText,
    ];
  }

  async promptSession(session, promptText) {
    if (this.activePrompt) {
      throw new Error("a Codex prompt is already running for this runtime");
    }

    const userMessage = {
      id: `user-${randomUUID()}`,
      role: "user",
      text: promptText,
    };
    session.transcript.push(userMessage);
    this.sendSessionUpdate({
      sessionUpdate: "user_message_chunk",
      content: { type: "text", text: promptText },
      messageId: userMessage.id,
    });

    const tempDir = fs.mkdtempSync(path.join(os.tmpdir(), "spritz-codex-acp-"));
    const outputFile = path.join(tempDir, "last-message.txt");
    const args = this.buildCodexCommand(session, promptText, outputFile);

    return new Promise((resolve, reject) => {
      const child = spawn(this.config.codexBin, args, {
        cwd: session.cwd,
        env: this.env,
        stdio: ["ignore", "pipe", "pipe"],
      });
      const state = {
        sessionId: session.sessionId,
        child,
        cancelled: false,
        assistantText: "",
        stderrLines: [],
        stdoutNoise: [],
      };
      this.activePrompt = state;

      child.stdout.on(
        "data",
        createLineForwarder((line) => {
          const event = parseACPMessage(line);
          if (event.invalid) {
            state.stdoutNoise.push(line);
            return;
          }
          if (event.type === "thread.started" && typeof event.thread_id === "string" && event.thread_id.trim()) {
            session.threadId = event.thread_id.trim();
            return;
          }
          if (event.type === "item.completed") {
            const item = event.item;
            if (item && typeof item === "object" && String(item.type || "").includes("message")) {
              const text = extractACPText(item.text ?? item.content).trim();
              if (text) {
                state.assistantText = text;
              }
            }
          }
        }),
      );
      child.stderr.on(
        "data",
        createLineForwarder((line) => {
          state.stderrLines.push(line);
          this.logger.error?.(`[codex-acp] ${line}`);
        }),
      );
      child.on("error", (error) => {
        this.activePrompt = null;
        fs.rmSync(tempDir, { recursive: true, force: true });
        reject(new Error(`failed to start Codex: ${String(error)}`));
      });
      child.on("exit", (code, signal) => {
        if (this.killTimer.current) {
          clearTimeout(this.killTimer.current);
          this.killTimer.current = null;
        }
        this.activePrompt = null;

        let assistantText = state.assistantText;
        if (!assistantText) {
          try {
            assistantText = fs.readFileSync(outputFile, "utf8").trim();
          } catch {
            assistantText = "";
          }
        }
        fs.rmSync(tempDir, { recursive: true, force: true });

        if (state.cancelled) {
          resolve({ stopReason: "cancelled" });
          return;
        }
        if (signal || code !== 0) {
          const details = [...state.stderrLines, ...state.stdoutNoise].filter(Boolean).join("\n").trim();
          const status = signal ? `signal ${signal}` : `exit code ${code ?? 0}`;
          reject(new Error(details ? `Codex prompt failed with ${status}: ${details}` : `Codex prompt failed with ${status}`));
          return;
        }

        if (assistantText) {
          const assistantMessage = {
            id: `assistant-${randomUUID()}`,
            role: "assistant",
            text: assistantText,
          };
          session.transcript.push(assistantMessage);
          this.sendSessionUpdate({
            sessionUpdate: "agent_message_chunk",
            content: { type: "text", text: assistantText },
            messageId: assistantMessage.id,
          });
        }
        resolve({ stopReason: "end_turn" });
      });
    });
  }

  cancelPrompt(sessionId) {
    if (!this.activePrompt || this.activePrompt.sessionId !== sessionId) {
      return;
    }
    this.activePrompt.cancelled = true;
    closeChild(this.activePrompt.child, this.killTimer);
  }

  attach(socket) {
    socket.on("message", async (data) => {
      let payload = null;
      try {
        payload = parseACPMessage(Buffer.isBuffer(data) ? data.toString("utf8") : String(data));
        if (payload.invalid) {
          socket.close(1002, "invalid ACP JSON");
          return;
        }
        if (payload.method === "initialize" && payload.id !== undefined) {
          this.sendRPCResponse(socket, payload.id, { result: this.config.metadata });
          return;
        }
        if (payload.method === "session/new" && payload.id !== undefined) {
          this.claimSocket(socket);
          const cwd = String(payload.params?.cwd || this.config.workdir || DEFAULTS.workdir).trim() || DEFAULTS.workdir;
          const session = buildSessionState(this.config, cwd);
          this.sessions.set(session.sessionId, session);
          this.sendRPCResponse(socket, payload.id, {
            result: {
              sessionId: session.sessionId,
              ...buildSessionLoadResult(session),
            },
          });
          return;
        }
        if (payload.method === "session/load" && payload.id !== undefined) {
          this.claimSocket(socket);
          const sessionId = String(payload.params?.sessionId || "").trim();
          const session = this.sessions.get(sessionId);
          if (!session) {
            throw {
              code: -32002,
              message: `Resource not found: ${sessionId}`,
            };
          }
          this.replayTranscript(session);
          this.sendRPCResponse(socket, payload.id, { result: buildSessionLoadResult(session) });
          return;
        }
        if (payload.method === "session/prompt" && payload.id !== undefined) {
          this.claimSocket(socket);
          const sessionId = String(payload.params?.sessionId || "").trim();
          const session = this.sessions.get(sessionId);
          if (!session) {
            throw {
              code: -32002,
              message: `Resource not found: ${sessionId}`,
            };
          }
          const promptText = extractPromptText(payload.params?.prompt);
          const result = await this.promptSession(session, promptText);
          this.sendRPCResponse(socket, payload.id, { result });
          return;
        }
        if (payload.method === "session/cancel") {
          this.claimSocket(socket);
          this.cancelPrompt(String(payload.params?.sessionId || "").trim());
          return;
        }
        if (payload.id !== undefined) {
          this.sendRPCResponse(socket, payload.id, {
            error: {
              code: -32601,
              message: "Method not supported by the Codex ACP bridge.",
            },
          });
        }
      } catch (error) {
        if (payload?.id !== undefined) {
          this.sendRPCResponse(socket, payload.id, { error: this.normalizeRPCError(error) });
          return;
        }
        this.logger.error?.(`codex ACP bridge error: ${String(error?.message || error)}`);
        socket.close(1011, "codex ACP bridge error");
      }
    });
    socket.on("close", () => {
      if (this.socket === socket) {
        this.socket = null;
      }
    });
    socket.on("error", (error) => {
      this.logger.warn?.(`codex ACP websocket error: ${String(error)}`);
      if (this.socket === socket) {
        this.socket = null;
      }
    });
  }

  stop() {
    this.closeSocket(1001, "server shutting down");
    if (this.activePrompt?.child) {
      this.activePrompt.cancelled = true;
      closeChild(this.activePrompt.child, this.killTimer);
    }
  }
}

async function main(env = process.env, logger = console) {
  const config = buildConfig(env);
  const runtime = new CodexACPRuntime(config, env, logger);
  const runtimeAdapter = {
    metadata: config.metadata,
    async health() {
      const missingEnv = resolveMissingEnv(config.requiredEnv, env);
      if (missingEnv.length > 0) {
        return { ok: false, error: `missing required env: ${missingEnv.join(", ")}` };
      }
      if (!commandExists(config.codexBin, env)) {
        return { ok: false, error: `command not found: ${config.codexBin}` };
      }
      return { ok: true };
    },
    attachWebSocket(socket) {
      const missingEnv = resolveMissingEnv(config.requiredEnv, env);
      if (missingEnv.length > 0) {
        socket.close(1011, `missing required env: ${missingEnv.join(", ")}`);
        return;
      }
      runtime.attach(socket);
    },
    async close() {
      runtime.stop();
    },
  };

  await serveSpritzACPServer({
    config,
    runtime: runtimeAdapter,
    loadWSModule: async () => import(pathToFileURL(path.join(config.wsRoot, "index.js")).href),
    logger,
    serverName: "spritz-codex-acp-server",
    shutdownTimeoutMs: DEFAULTS.shutdownTimeoutMs,
  });
}

const entrypoint = process.argv[1];

if (entrypoint && import.meta.url === pathToFileURL(entrypoint).href) {
  main().catch((error) => {
    console.error(error instanceof Error ? error.stack || error.message : String(error));
    process.exit(1);
  });
}

export { CodexACPRuntime, buildConfig, main };
