// @ts-nocheck

import test from "node:test";
import assert from "node:assert/strict";

import {
  buildHistoryReplayUpdates,
  buildGatewayClientOptions,
  createSpritzAcpGatewayAgentClass,
  findPendingPromptBySessionKey,
} from "./acp-wrapper.js";

test("loadSession skips transcript replay when gateway transcript read requires operator scope", async () => {
  class FakeBaseAgent {
    constructor() {
      this.logged = [];
      this.rateLimits = [];
      this.sentAvailableCommands = [];
      this.connection = {
        updates: [],
        async sessionUpdate(payload) {
          this.updates.push(payload);
        },
      };
      this.gateway = {
        async request(method) {
          assert.equal(method, "sessions.get");
          throw Object.assign(new Error("missing scope: operator.read"), {
            code: "INVALID_REQUEST",
          });
        },
      };
      this.sessionStore = {
        entries: new Map(),
        createSession: ({ sessionId, sessionKey, cwd }) => {
          const session = { sessionId, sessionKey, cwd };
          this.sessionStore.entries.set(sessionId, session);
          return session;
        },
        hasSession: () => false,
      };
    }

    log(message) {
      this.logged.push(message);
    }

    enforceSessionCreateRateLimit(method) {
      this.rateLimits.push(method);
    }

    async resolveSessionKeyFromMeta({ fallbackKey }) {
      return fallbackKey;
    }

    async sendAvailableCommands(sessionId) {
      this.sentAvailableCommands.push(sessionId);
    }
  }

  const SpritzAgent = createSpritzAcpGatewayAgentClass(FakeBaseAgent, {});
  const agent = new SpritzAgent();

  await agent.loadSession({
    sessionId: "123e4567-e89b-42d3-a456-426614174000",
    cwd: "/home/dev",
    mcpServers: [],
  });

  assert.equal(agent.connection.updates.length, 0);
  assert.deepEqual(agent.rateLimits, ["loadSession"]);
  assert.deepEqual(agent.sentAvailableCommands, ["123e4567-e89b-42d3-a456-426614174000"]);
  assert.match(agent.logged.at(-1), /skipping transcript replay .* operator\.read/i);
});

test("loadSession still fails on unrelated transcript replay errors", async () => {
  class FakeBaseAgent {
    constructor() {
      this.connection = {
        async sessionUpdate() {},
      };
      this.gateway = {
        async request() {
          throw new Error("gateway unavailable");
        },
      };
      this.sessionStore = {
        entries: new Map(),
        createSession: ({ sessionId, sessionKey, cwd }) => {
          const session = { sessionId, sessionKey, cwd };
          this.sessionStore.entries.set(sessionId, session);
          return session;
        },
        hasSession: () => false,
      };
    }

    log() {}
    enforceSessionCreateRateLimit() {}
    async resolveSessionKeyFromMeta({ fallbackKey }) {
      return fallbackKey;
    }
    async sendAvailableCommands() {}
  }

  const SpritzAgent = createSpritzAcpGatewayAgentClass(FakeBaseAgent, {});
  const agent = new SpritzAgent();

  await assert.rejects(
    agent.loadSession({
      sessionId: "123e4567-e89b-42d3-a456-426614174000",
      cwd: "/home/dev",
      mcpServers: [],
    }),
    /gateway unavailable/,
  );
});

test("trusted-proxy control-ui bridge keeps device identity enabled", () => {
  const options = buildGatewayClientOptions({
    connectionUrl: "ws://127.0.0.1:8080",
    trustedProxyControlUi: true,
  });

  assert.equal(options.clientName, "openclaw-control-ui");
  assert.equal(options.mode, "webchat");
  assert.equal(options.role, "operator");
  assert.equal(options.token, undefined);
  assert.equal(options.password, undefined);
  assert.equal(options.deviceIdentity, undefined);
});

test("prompt emits a user_message_chunk before delegating to the gateway", async () => {
  class FakeBaseAgent {
    constructor() {
      this.connection = {
        updates: [],
        async sessionUpdate(payload) {
          this.updates.push(payload);
        },
      };
      this.promptCalls = [];
      this.sessionStore = {
        getSession: (sessionId) => ({ sessionId }),
      };
    }

    async prompt(params) {
      this.promptCalls.push(params);
      return { stopReason: "end_turn" };
    }
  }

  const SpritzAgent = createSpritzAcpGatewayAgentClass(FakeBaseAgent, {});
  const agent = new SpritzAgent();

  const result = await agent.prompt({
    sessionId: "session-1",
    prompt: [
      { type: "text", text: "test" },
      { type: "text", text: "who are you" },
    ],
  });

  assert.deepEqual(result, { stopReason: "end_turn" });
  assert.deepEqual(agent.promptCalls, [
    {
      sessionId: "session-1",
      prompt: [
        { type: "text", text: "test" },
        { type: "text", text: "who are you" },
      ],
    },
  ]);
  assert.deepEqual(agent.connection.updates, [
    {
      sessionId: "session-1",
      update: {
        sessionUpdate: "user_message_chunk",
        content: {
          type: "text",
          text: "test\nwho are you",
        },
      },
    },
  ]);
});

test("history replay strips ACP sender metadata and cwd prefix from user text", () => {
  const updates = buildHistoryReplayUpdates([
    {
      role: "user",
      content: [
        {
          type: "text",
          text:
            "Sender (untrusted metadata):\n```json\n{\n  \"label\": \"ACP\"\n}\n```\n\n[Wed 2026-03-25 18:35 UTC] [Working directory: ~]\n\ntest",
        },
      ],
    },
  ]);

  assert.deepEqual(updates, [
    {
      sessionUpdate: "user_message_chunk",
      historyMessageId: "history-0",
      content: {
        type: "text",
        text: "test",
      },
    },
  ]);
});

test("history replay suppresses assistant NO_REPLY-only text", () => {
  const updates = buildHistoryReplayUpdates([
    {
      role: "assistant",
      content: [{ type: "text", text: "  NO_REPLY  " }],
    },
  ]);

  assert.deepEqual(updates, []);
});

test("history replay strips a glued leading NO_REPLY token from assistant text", () => {
  const updates = buildHistoryReplayUpdates([
    {
      role: "assistant",
      content: [{ type: "text", text: "NO_REPLYActual answer" }],
    },
  ]);

  assert.deepEqual(updates, [
    {
      sessionUpdate: "agent_message_chunk",
      historyMessageId: "history-0",
      content: {
        type: "text",
        text: "Actual answer",
      },
    },
  ]);
});

test("findPendingPromptBySessionKey falls back to the sole session match when run IDs differ", () => {
  const pending = {
    sessionId: "session-1",
    sessionKey: "agent:main:spritz-acp:session-1",
    idempotencyKey: "client-run-id",
  };
  const pendingPrompts = new Map([["session-1", pending]]);

  assert.equal(
    findPendingPromptBySessionKey(
      pendingPrompts,
      "agent:main:spritz-acp:session-1",
      "gateway-run-id",
    ),
    pending,
  );
});

test("findPendingPromptBySessionKey keeps ambiguous mismatched runs unresolved", () => {
  const pendingPrompts = new Map([
    [
      "session-1",
      {
        sessionId: "session-1",
        sessionKey: "shared-session",
        idempotencyKey: "client-run-1",
      },
    ],
    [
      "session-2",
      {
        sessionId: "session-2",
        sessionKey: "shared-session",
        idempotencyKey: "client-run-2",
      },
    ],
  ]);

  assert.equal(
    findPendingPromptBySessionKey(pendingPrompts, "shared-session", "gateway-run-id"),
    undefined,
  );
});

test("live tool updates still emit when the gateway tool run ID differs from the prompt ID", async () => {
  class FakeBaseAgent {
    constructor() {
      const updates = [];
      this.connection = {
        updates,
        async sessionUpdate(payload) {
          updates.push(payload);
        },
      };
      this.pendingPrompts = new Map([
        [
          "session-1",
          {
            sessionId: "session-1",
            sessionKey: "agent:main:spritz-acp:session-1",
            idempotencyKey: "client-run-id",
          },
        ],
      ]);
    }

    async handleAgentEvent(evt) {
      const payload = evt.payload;
      if (!payload?.data || payload.stream !== "tool" || !payload.sessionKey) {
        return;
      }
      const pending = this.findPendingBySessionKey(payload.sessionKey, payload.runId);
      if (!pending) {
        return;
      }
      await this.connection.sessionUpdate({
        sessionId: pending.sessionId,
        update: {
          sessionUpdate: "tool_call",
          toolCallId: payload.data.toolCallId,
          status: "in_progress",
        },
      });
    }
  }

  const SpritzAgent = createSpritzAcpGatewayAgentClass(FakeBaseAgent, {});
  const agent = new SpritzAgent();

  await agent.handleAgentEvent({
    payload: {
      stream: "tool",
      runId: "gateway-run-id",
      sessionKey: "agent:main:spritz-acp:session-1",
      data: {
        phase: "start",
        toolCallId: "tool-1",
      },
    },
  });

  assert.deepEqual(agent.connection.updates, [
    {
      sessionId: "session-1",
      update: {
        sessionUpdate: "tool_call",
        toolCallId: "tool-1",
        status: "in_progress",
      },
    },
  ]);
});

test("handleDeltaEvent emits tool_call updates for live assistant tool blocks", async () => {
  class FakeBaseAgent {
    constructor() {
      const updates = [];
      this.connection = {
        updates,
        async sessionUpdate(payload) {
          updates.push(payload);
        },
      };
      this.pendingPrompts = new Map([
        [
          "session-1",
          {
            sessionId: "session-1",
            sessionKey: "agent:main:spritz-acp:session-1",
            idempotencyKey: "client-run-id",
          },
        ],
      ]);
    }

    async handleDeltaEvent(sessionId, messageData) {
      const content = messageData.content ?? [];
      const pending = this.pendingPrompts.get(sessionId);
      if (!pending) {
        return;
      }
      const fullText = content
        .filter((block) => block?.type === "text")
        .map((block) => block.text ?? "")
        .join("\n")
        .trimEnd();
      if (!fullText) {
        return;
      }
      await this.connection.sessionUpdate({
        sessionId,
        update: {
          sessionUpdate: "agent_message_chunk",
          content: {
            type: "text",
            text: fullText,
          },
        },
      });
    }
  }

  const SpritzAgent = createSpritzAcpGatewayAgentClass(FakeBaseAgent, {});
  const agent = new SpritzAgent();

  await agent.handleDeltaEvent("session-1", {
    content: [
      {
        type: "toolCall",
        id: "functions.exec:0",
        name: "exec",
        args: { command: "echo hi" },
      },
      {
        type: "text",
        text: "hi",
      },
    ],
  });

  assert.deepEqual(agent.connection.updates, [
    {
      sessionId: "session-1",
      update: {
        sessionUpdate: "tool_call",
        toolCallId: "functions.exec:0",
        title: "exec",
        status: "in_progress",
        rawInput: { command: "echo hi" },
        type: "exec",
      },
    },
    {
      sessionId: "session-1",
      update: {
        sessionUpdate: "agent_message_chunk",
        content: {
          type: "text",
          text: "hi",
        },
      },
    },
  ]);
});

test("handleDeltaEvent does not wait on transcript fetch before assistant text", async () => {
  class FakeBaseAgent {
    constructor() {
      const updates = [];
      this.connection = {
        updates,
        async sessionUpdate(payload) {
          updates.push(payload);
        },
      };
      this.pendingPrompts = new Map([
        [
          "session-1",
          {
            sessionId: "session-1",
            sessionKey: "agent:main:spritz-acp:session-1",
            idempotencyKey: "client-run-id",
          },
        ],
      ]);
      this.gateway = {
        async request() {
          throw new Error("handleDeltaEvent must not fetch transcript state");
        },
      };
    }

    async handleDeltaEvent(sessionId, messageData) {
      const content = messageData.content ?? [];
      const pending = this.pendingPrompts.get(sessionId);
      if (!pending) {
        return;
      }
      const fullText = content
        .filter((block) => block?.type === "text")
        .map((block) => block.text ?? "")
        .join("\n")
        .trimEnd();
      if (!fullText) {
        return;
      }
      await this.connection.sessionUpdate({
        sessionId,
        update: {
          sessionUpdate: "agent_message_chunk",
          content: {
            type: "text",
            text: fullText,
          },
        },
      });
    }
  }

  const SpritzAgent = createSpritzAcpGatewayAgentClass(FakeBaseAgent, {});
  const agent = new SpritzAgent();

  await agent.handleDeltaEvent("session-1", {
    content: [{ type: "text", text: "hi" }],
  });

  assert.deepEqual(agent.connection.updates, [
    {
      sessionId: "session-1",
      update: {
        sessionUpdate: "agent_message_chunk",
        content: {
          type: "text",
          text: "hi",
        },
      },
    },
  ]);
});

test("handleDeltaEvent suppresses NO_REPLY lead fragments and silent-only finals", async () => {
  class FakeBaseAgent {
    constructor() {
      const updates = [];
      this.connection = {
        updates,
        async sessionUpdate(payload) {
          updates.push(payload);
        },
      };
      this.pendingPrompts = new Map([
        [
          "session-1",
          {
            sessionId: "session-1",
            sessionKey: "agent:main:spritz-acp:session-1",
            idempotencyKey: "client-run-id",
          },
        ],
      ]);
    }

    async handleDeltaEvent(sessionId, messageData) {
      const content = messageData.content ?? [];
      const pending = this.pendingPrompts.get(sessionId);
      if (!pending) {
        return;
      }
      const fullText = content
        .filter((block) => block?.type === "text")
        .map((block) => block.text ?? "")
        .join("\n")
        .trimEnd();
      if (!fullText) {
        return;
      }
      await this.connection.sessionUpdate({
        sessionId,
        update: {
          sessionUpdate: "agent_message_chunk",
          content: {
            type: "text",
            text: fullText,
          },
        },
      });
    }
  }

  const SpritzAgent = createSpritzAcpGatewayAgentClass(FakeBaseAgent, {});
  const agent = new SpritzAgent();

  for (const text of ["NO", "NO_", "NO_RE", "NO_REPLY"]) {
    await agent.handleDeltaEvent("session-1", {
      content: [{ type: "text", text }],
    });
  }

  assert.deepEqual(agent.connection.updates, []);
});

test("handleDeltaEvent keeps normal NO-prefixed text", async () => {
  class FakeBaseAgent {
    constructor() {
      const updates = [];
      this.connection = {
        updates,
        async sessionUpdate(payload) {
          updates.push(payload);
        },
      };
      this.pendingPrompts = new Map([
        [
          "session-1",
          {
            sessionId: "session-1",
            sessionKey: "agent:main:spritz-acp:session-1",
            idempotencyKey: "client-run-id",
          },
        ],
      ]);
    }

    async handleDeltaEvent(sessionId, messageData) {
      const content = messageData.content ?? [];
      const pending = this.pendingPrompts.get(sessionId);
      if (!pending) {
        return;
      }
      const fullText = content
        .filter((block) => block?.type === "text")
        .map((block) => block.text ?? "")
        .join("\n")
        .trimEnd();
      if (!fullText) {
        return;
      }
      await this.connection.sessionUpdate({
        sessionId,
        update: {
          sessionUpdate: "agent_message_chunk",
          content: {
            type: "text",
            text: fullText,
          },
        },
      });
    }
  }

  const SpritzAgent = createSpritzAcpGatewayAgentClass(FakeBaseAgent, {});
  const agent = new SpritzAgent();

  await agent.handleDeltaEvent("session-1", {
    content: [{ type: "text", text: "NOW" }],
  });

  assert.deepEqual(agent.connection.updates, [
    {
      sessionId: "session-1",
      update: {
        sessionUpdate: "agent_message_chunk",
        content: {
          type: "text",
          text: "NOW",
        },
      },
    },
  ]);
});

test("prompt polls transcript for silent tool runs while the prompt is active", async () => {
  class FakeBaseAgent {
    constructor() {
      let transcriptRequestCount = 0;
      const updates = [];
      this.connection = {
        updates,
        async sessionUpdate(payload) {
          updates.push(payload);
        },
      };
      this.pendingPrompts = new Map();
      this.gateway = {
        async request(method, params) {
          assert.equal(method, "sessions.get");
          assert.equal(params.key, "agent:main:spritz-acp:session-1");
          transcriptRequestCount += 1;
          if (transcriptRequestCount === 1) {
            return {
              messages: [],
            };
          }
          return {
            messages: [
              {
                role: "assistant",
                content: [
                  {
                    type: "toolCall",
                    id: "functions.exec:0",
                    name: "exec",
                    args: { command: "echo hi" },
                  },
                ],
              },
              {
                role: "tool_result",
                toolCallId: "functions.exec:0",
                content: [{ type: "text", text: "hi" }],
              },
            ],
          };
        },
      };
      this.sessionStore = {
        getSession: (sessionId) => ({
          sessionId,
          sessionKey: "agent:main:spritz-acp:session-1",
        }),
      };
      this.resolvePrompt = null;
    }

    async prompt(params) {
      return await new Promise((resolve) => {
        this.pendingPrompts.set(params.sessionId, {
          sessionId: params.sessionId,
          sessionKey: "agent:main:spritz-acp:session-1",
          idempotencyKey: "client-run-id",
          resolve,
        });
        this.resolvePrompt = () => {
          this.pendingPrompts.delete(params.sessionId);
          resolve({ stopReason: "end_turn" });
        };
      });
    }
  }

  const intervalCallbacks = [];
  const clearedIntervals = [];
  const SpritzAgent = createSpritzAcpGatewayAgentClass(FakeBaseAgent, {}, {
    setInterval(callback) {
      intervalCallbacks.push(callback);
      return callback;
    },
    clearInterval(timer) {
      clearedIntervals.push(timer);
    },
    toolTranscriptSyncIntervalMs: 1,
  });
  const agent = new SpritzAgent();

  const promptPromise = agent.prompt({
    sessionId: "session-1",
    prompt: [{ type: "text", text: "run it" }],
  });

  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(intervalCallbacks.length, 1);
  await intervalCallbacks[0]();
  agent.resolvePrompt();
  await assert.doesNotReject(promptPromise);

  assert.deepEqual(agent.connection.updates, [
    {
      sessionId: "session-1",
      update: {
        sessionUpdate: "user_message_chunk",
        content: {
          type: "text",
          text: "run it",
        },
      },
    },
    {
      sessionId: "session-1",
      update: {
        sessionUpdate: "tool_call",
        toolCallId: "functions.exec:0",
        title: "exec",
        status: "in_progress",
        rawInput: { command: "echo hi" },
        type: "exec",
      },
    },
    {
      sessionId: "session-1",
      update: {
        sessionUpdate: "tool_call_update",
        toolCallId: "functions.exec:0",
        status: "completed",
        rawOutput: "hi",
      },
    },
  ]);
  assert.deepEqual(clearedIntervals, [intervalCallbacks[0]]);
});

test("finishPrompt backfills tool lifecycle from transcript before closing the run", async () => {
  class FakeBaseAgent {
    constructor() {
      const updates = [];
      this.connection = {
        updates,
        async sessionUpdate(payload) {
          updates.push(payload);
        },
      };
      this.pendingPrompts = new Map();
      this.gateway = {
        async request(method, params) {
          assert.equal(method, "sessions.get");
          assert.equal(params.key, "agent:main:spritz-acp:session-1");
          return {
            messages: [
              {
                role: "assistant",
                content: [
                  {
                    type: "toolCall",
                    id: "functions.exec:0",
                    name: "exec",
                    args: { command: "echo hi" },
                  },
                ],
              },
              {
                role: "tool_result",
                toolCallId: "functions.exec:0",
                content: [{ type: "text", text: "hi" }],
              },
            ],
          };
        },
      };
      this.sessionStore = {
        clearActiveRun() {},
      };
      this.snapshotUpdates = [];
    }

    async getSessionSnapshot() {
      return { title: "session" };
    }

    async sendSessionSnapshotUpdate(sessionId, snapshot, options) {
      this.snapshotUpdates.push({ sessionId, snapshot, options });
    }

    async finishPrompt(sessionId, pending, stopReason) {
      this.pendingPrompts.delete(sessionId);
      this.sessionStore.clearActiveRun(sessionId);
      const snapshot = await this.getSessionSnapshot(pending.sessionKey);
      await this.sendSessionSnapshotUpdate(sessionId, snapshot, { includeControls: false });
      pending.resolve?.({ stopReason });
    }
  }

  const SpritzAgent = createSpritzAcpGatewayAgentClass(FakeBaseAgent, {});
  const agent = new SpritzAgent();
  const pending = {
    sessionId: "session-1",
    sessionKey: "agent:main:spritz-acp:session-1",
    idempotencyKey: "client-run-id",
    transcriptMessageCursor: 0,
    resolve() {},
  };
  agent.pendingPrompts.set("session-1", pending);

  await agent.finishPrompt("session-1", pending, "end_turn");

  assert.deepEqual(agent.connection.updates, [
    {
      sessionId: "session-1",
      update: {
        sessionUpdate: "tool_call",
        toolCallId: "functions.exec:0",
        title: "exec",
        status: "in_progress",
        rawInput: { command: "echo hi" },
        type: "exec",
      },
    },
    {
      sessionId: "session-1",
      update: {
        sessionUpdate: "tool_call_update",
        toolCallId: "functions.exec:0",
        status: "completed",
        rawOutput: "hi",
      },
    },
  ]);
  assert.deepEqual(agent.snapshotUpdates, [
    {
      sessionId: "session-1",
      snapshot: { title: "session" },
      options: { includeControls: false },
    },
  ]);
});

test("prompt transcript sync ignores tool activity that predates the active prompt", async () => {
  class FakeBaseAgent {
    constructor() {
      let transcriptRequestCount = 0;
      const updates = [];
      this.connection = {
        updates,
        async sessionUpdate(payload) {
          updates.push(payload);
        },
      };
      this.pendingPrompts = new Map();
      this.gateway = {
        async request(method, params) {
          assert.equal(method, "sessions.get");
          assert.equal(params.key, "agent:main:spritz-acp:session-1");
          transcriptRequestCount += 1;
          if (transcriptRequestCount === 1) {
            return {
              messages: [
                {
                  role: "assistant",
                  content: [
                    {
                      type: "toolCall",
                      id: "functions.exec:old",
                      name: "exec",
                      args: { command: "echo old" },
                    },
                  ],
                },
                {
                  role: "tool_result",
                  toolCallId: "functions.exec:old",
                  content: [{ type: "text", text: "old" }],
                },
              ],
            };
          }
          return {
            messages: [
              {
                role: "assistant",
                content: [
                  {
                    type: "toolCall",
                    id: "functions.exec:old",
                    name: "exec",
                    args: { command: "echo old" },
                  },
                ],
              },
              {
                role: "tool_result",
                toolCallId: "functions.exec:old",
                content: [{ type: "text", text: "old" }],
              },
              {
                role: "assistant",
                content: [
                  {
                    type: "toolCall",
                    id: "functions.exec:new",
                    name: "exec",
                    args: { command: "echo new" },
                  },
                ],
              },
              {
                role: "tool_result",
                toolCallId: "functions.exec:new",
                content: [{ type: "text", text: "new" }],
              },
            ],
          };
        },
      };
      this.sessionStore = {
        getSession: (sessionId) => ({
          sessionId,
          sessionKey: "agent:main:spritz-acp:session-1",
        }),
      };
      this.resolvePrompt = null;
    }

    async prompt(params) {
      return await new Promise((resolve) => {
        this.pendingPrompts.set(params.sessionId, {
          sessionId: params.sessionId,
          sessionKey: "agent:main:spritz-acp:session-1",
          idempotencyKey: "client-run-id",
          resolve,
        });
        this.resolvePrompt = () => {
          this.pendingPrompts.delete(params.sessionId);
          resolve({ stopReason: "end_turn" });
        };
      });
    }
  }

  const intervalCallbacks = [];
  const SpritzAgent = createSpritzAcpGatewayAgentClass(FakeBaseAgent, {}, {
    setInterval(callback) {
      intervalCallbacks.push(callback);
      return callback;
    },
    clearInterval() {},
    toolTranscriptSyncIntervalMs: 1,
  });
  const agent = new SpritzAgent();

  const promptPromise = agent.prompt({
    sessionId: "session-1",
    prompt: [{ type: "text", text: "run it" }],
  });

  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(intervalCallbacks.length, 1);
  await intervalCallbacks[0]();
  agent.resolvePrompt();
  await assert.doesNotReject(promptPromise);

  assert.deepEqual(agent.connection.updates, [
    {
      sessionId: "session-1",
      update: {
        sessionUpdate: "user_message_chunk",
        content: {
          type: "text",
          text: "run it",
        },
      },
    },
    {
      sessionId: "session-1",
      update: {
        sessionUpdate: "tool_call",
        toolCallId: "functions.exec:new",
        title: "exec",
        status: "in_progress",
        rawInput: { command: "echo new" },
        type: "exec",
      },
    },
    {
      sessionId: "session-1",
      update: {
        sessionUpdate: "tool_call_update",
        toolCallId: "functions.exec:new",
        status: "completed",
        rawOutput: "new",
      },
    },
  ]);
});
