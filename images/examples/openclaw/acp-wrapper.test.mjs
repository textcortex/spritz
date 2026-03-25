import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

import {
  buildGatewayClientOptions,
  buildHistoryReplayUpdates,
  buildBridgeFallbackSessionKey,
  createLazyGatewayController,
  createSpritzAcpGatewayAgentClass,
  loadOpenclawCompat,
  parseArgs,
  resolveBridgeFallbackSessionKey,
  useTrustedProxyControlUiBridge,
} from './acp-wrapper.mjs';

test('trusted-proxy bridge uses control-ui profile without shared auth or device identity', () => {
  const opts = buildGatewayClientOptions({
    connectionUrl: 'ws://127.0.0.1:8080',
    gatewayToken: 'secret-token',
    gatewayPassword: 'secret-password',
    trustedProxyControlUi: true,
  });

  assert.equal(opts.url, 'ws://127.0.0.1:8080');
  assert.equal(opts.clientName, 'openclaw-control-ui');
  assert.equal(opts.mode, 'webchat');
  assert.equal(opts.deviceIdentity, false);
  assert.equal(opts.token, undefined);
  assert.equal(opts.password, undefined);
  assert.equal(opts.role, 'operator');
});

test('default bridge preserves CLI profile and shared auth', () => {
  const opts = buildGatewayClientOptions({
    connectionUrl: 'ws://127.0.0.1:8080',
    gatewayToken: 'secret-token',
    gatewayPassword: 'secret-password',
    trustedProxyControlUi: false,
  });

  assert.equal(opts.clientName, 'cli');
  assert.equal(opts.mode, 'cli');
  assert.equal(opts.token, 'secret-token');
  assert.equal(opts.password, 'secret-password');
  assert.equal(opts.deviceIdentity, undefined);
});

test('parseArgs accepts acp subcommand and file-based secrets', () => {
  const opts = parseArgs([
    'acp',
    '--url', 'ws://127.0.0.1:8080',
    '--session', 'agent:main:main',
    '--token-file', '/tmp/gateway.token',
    '--verbose',
  ], {
    readSecretFromFile(path, label) {
      assert.equal(path, '/tmp/gateway.token');
      assert.equal(label, 'Gateway token');
      return 'file-token';
    },
  });

  assert.equal(opts.gatewayUrl, 'ws://127.0.0.1:8080');
  assert.equal(opts.defaultSessionKey, 'agent:main:main');
  assert.equal(opts.gatewayToken, 'file-token');
  assert.equal(opts.verbose, true);
});

test('useTrustedProxyControlUiBridge reads truthy env values', () => {
  assert.equal(useTrustedProxyControlUiBridge({ SPRITZ_OPENCLAW_ACP_USE_CONTROL_UI_BRIDGE: '1' }), true);
  assert.equal(useTrustedProxyControlUiBridge({ SPRITZ_OPENCLAW_ACP_USE_CONTROL_UI_BRIDGE: 'true' }), true);
  assert.equal(useTrustedProxyControlUiBridge({ SPRITZ_OPENCLAW_ACP_USE_CONTROL_UI_BRIDGE: '0' }), false);
  assert.equal(useTrustedProxyControlUiBridge({}), false);
});

test('buildBridgeFallbackSessionKey maps ACP session ids onto normal agent-scoped gateway keys', () => {
  const sessionKey = buildBridgeFallbackSessionKey('123e4567-e89b-42d3-a456-426614174000');

  assert.equal(sessionKey, 'agent:main:spritz-acp:123e4567-e89b-42d3-a456-426614174000');
});

test('resolveBridgeFallbackSessionKey preserves existing gateway session keys', () => {
  assert.equal(resolveBridgeFallbackSessionKey('agent:main:main'), 'agent:main:main');
  assert.equal(resolveBridgeFallbackSessionKey('main'), 'main');
  assert.equal(
    resolveBridgeFallbackSessionKey('123e4567-e89b-42d3-a456-426614174000'),
    'agent:main:spritz-acp:123e4567-e89b-42d3-a456-426614174000',
  );
});

test('createSpritzAcpGatewayAgentClass uses mapped fallback keys for new and loaded sessions', async () => {
  class FakeBaseAgent {
    constructor() {
      this.logged = [];
      this.rateLimits = [];
      this.sessionStore = {
        entries: new Map(),
        createSession: ({ sessionId, sessionKey, cwd }) => {
          const session = { sessionId, sessionKey, cwd };
          this.sessionStore.entries.set(sessionId, session);
          return session;
        },
        hasSession: (sessionId) => this.sessionStore.entries.has(sessionId),
      };
      this.sentAvailableCommands = [];
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

  const created = await agent.newSession({
    cwd: '/home/dev',
    mcpServers: [],
  });

  const createdSession = agent.sessionStore.entries.get(created.sessionId);
  assert.match(created.sessionId, /^[0-9a-f-]{36}$/i);
  assert.equal(
    createdSession.sessionKey,
    `agent:main:spritz-acp:${created.sessionId}`,
  );

  await agent.loadSession({
    sessionId: created.sessionId,
    cwd: '/home/dev',
    mcpServers: [],
  });

  const loadedSession = agent.sessionStore.entries.get(created.sessionId);
  assert.equal(
    loadedSession.sessionKey,
    `agent:main:spritz-acp:${created.sessionId}`,
  );
  assert.deepEqual(agent.rateLimits, ['newSession']);
  assert.deepEqual(agent.sentAvailableCommands, [created.sessionId, created.sessionId]);
});

test('createSpritzAcpGatewayAgentClass ignores ACP meta session key overrides', async () => {
  class FakeBaseAgent {
    constructor() {
      this.sessionStore = {
        entries: new Map(),
        createSession: ({ sessionId, sessionKey, cwd }) => {
          const session = { sessionId, sessionKey, cwd };
          this.sessionStore.entries.set(sessionId, session);
          return session;
        },
        hasSession: () => false,
      };
      this.sentAvailableCommands = [];
      this.connection = {
        async sessionUpdate() {},
      };
      this.gateway = {
        async request() {
          return { messages: [] };
        },
      };
    }

    log() {}
    enforceSessionCreateRateLimit() {}
    async resolveSessionKeyFromMeta() {
      throw new Error('meta session keys must not be consulted');
    }
    async sendAvailableCommands(sessionId) {
      this.sentAvailableCommands.push(sessionId);
    }
  }

  const SpritzAgent = createSpritzAcpGatewayAgentClass(FakeBaseAgent, {});
  const agent = new SpritzAgent();

  await agent.loadSession({
    sessionId: '123e4567-e89b-42d3-a456-426614174000',
    cwd: '/home/dev',
    mcpServers: [],
    _meta: {
      sessionKey: 'agent:main:unexpected',
    },
  });

  const loadedSession = agent.sessionStore.entries.get('123e4567-e89b-42d3-a456-426614174000');
  assert.equal(
    loadedSession.sessionKey,
    'agent:main:spritz-acp:123e4567-e89b-42d3-a456-426614174000',
  );
});

test('buildHistoryReplayUpdates converts gateway history into ACP replay updates', () => {
  const updates = buildHistoryReplayUpdates([
    {
      role: 'user',
      content: [{ type: 'text', text: 'hello from history' }],
    },
    {
      role: 'assistant',
      content: [
        { type: 'toolCall', id: 'tool-1', name: 'bash', arguments: { command: 'pwd' } },
        { type: 'text', text: 'I checked the directory.' },
      ],
    },
    {
      role: 'toolResult',
      toolCallId: 'tool-1',
      toolName: 'bash',
      content: [{ type: 'tool_result', text: '/home/dev' }],
    },
  ]);

  assert.deepEqual(
    updates.map((update) => update.sessionUpdate),
    ['user_message_chunk', 'tool_call', 'agent_message_chunk', 'tool_call_update'],
  );
  assert.equal(updates[0].content.text, 'hello from history');
  assert.equal(updates[0].historyMessageId, 'history-0');
  assert.equal(updates[1].toolCallId, 'tool-1');
  assert.equal(updates[1].rawInput.command, 'pwd');
  assert.equal(updates[2].content.text, 'I checked the directory.');
  assert.equal(updates[2].historyMessageId, 'history-1');
  assert.equal(updates[3].rawOutput, '/home/dev');
});

test('loadSession replays persisted session transcript before returning', async () => {
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
        async request(method, params) {
          if (method === 'sessions.get') {
            assert.deepEqual(params, {
              key: 'agent:main:spritz-acp:123e4567-e89b-42d3-a456-426614174000',
              limit: 1000,
            });
            return {
              messages: [
                { role: 'user', content: [{ type: 'text', text: 'hello from history' }] },
                { role: 'assistant', content: [{ type: 'text', text: 'history reply' }] },
              ],
            };
          }
          throw new Error(`unexpected gateway method ${method}`);
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
    sessionId: '123e4567-e89b-42d3-a456-426614174000',
    cwd: '/home/dev',
    mcpServers: [],
  });

  assert.equal(agent.connection.updates.length, 2);
  assert.deepEqual(
    agent.connection.updates.map((entry) => entry.update.sessionUpdate),
    ['user_message_chunk', 'agent_message_chunk'],
  );
  assert.equal(agent.connection.updates[0].update.content.text, 'hello from history');
  assert.equal(agent.connection.updates[1].update.content.text, 'history reply');
  assert.deepEqual(agent.rateLimits, ['loadSession']);
  assert.deepEqual(agent.sentAvailableCommands, ['123e4567-e89b-42d3-a456-426614174000']);
});

test('loadSession skips transcript replay when gateway transcript read requires operator scope', async () => {
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
          assert.equal(method, 'sessions.get');
          throw Object.assign(new Error('missing scope: operator.read'), {
            code: 'INVALID_REQUEST',
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
    sessionId: '123e4567-e89b-42d3-a456-426614174000',
    cwd: '/home/dev',
    mcpServers: [],
  });

  assert.equal(agent.connection.updates.length, 0);
  assert.deepEqual(agent.rateLimits, ['loadSession']);
  assert.deepEqual(agent.sentAvailableCommands, ['123e4567-e89b-42d3-a456-426614174000']);
  assert.match(
    agent.logged.at(-1),
    /skipping transcript replay .* operator\.read/i,
  );
});

test('loadSession still fails on unrelated transcript replay errors', async () => {
  class FakeBaseAgent {
    constructor() {
      this.connection = {
        async sessionUpdate() {},
      };
      this.gateway = {
        async request() {
          throw new Error('gateway unavailable');
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
      sessionId: '123e4567-e89b-42d3-a456-426614174000',
      cwd: '/home/dev',
      mcpServers: [],
    }),
    /gateway unavailable/,
  );
});

test('Spritz ACP gateway agent ensures the gateway is ready before session lifecycle calls', async () => {
  class FakeBaseAgent {
    constructor() {
      this.sessionStore = {
        entries: new Map(),
        createSession: ({ sessionId, sessionKey, cwd }) => {
          const session = { sessionId, sessionKey, cwd };
          this.sessionStore.entries.set(sessionId, session);
          return session;
        },
        hasSession: () => false,
      };
      this.sentAvailableCommands = [];
      this.connection = {
        async sessionUpdate() {},
      };
      this.gateway = {
        async request() {
          return { messages: [] };
        },
      };
    }

    log() {}
    enforceSessionCreateRateLimit() {}
    async sendAvailableCommands(sessionId) {
      this.sentAvailableCommands.push(sessionId);
    }
  }

  const ensureGatewayReadyCalls = [];
  const SpritzAgent = createSpritzAcpGatewayAgentClass(
    FakeBaseAgent,
    {},
    {
      async ensureGatewayReady() {
        ensureGatewayReadyCalls.push('ready');
      },
    },
  );
  const agent = new SpritzAgent();

  const created = await agent.newSession({
    cwd: '/home/dev',
    mcpServers: [],
  });
  await agent.loadSession({
    sessionId: created.sessionId,
    cwd: '/home/dev',
    mcpServers: [],
  });

  assert.deepEqual(ensureGatewayReadyCalls, ['ready', 'ready']);
});

test('lazy gateway controller defers gateway start until the first session lifecycle call', async () => {
  const events = [];
  const controller = createLazyGatewayController(
    {
      start() {
        events.push('start');
      },
      stop() {
        events.push('stop');
      },
    },
    {
      async waitUntilReady() {
        events.push('wait');
      },
      onStop() {
        events.push('onStop');
      },
    },
  );

  assert.deepEqual(events, []);
  await controller.ensureReady();
  await controller.ensureReady();
  controller.stop();

  assert.deepEqual(events, ['start', 'wait', 'onStop', 'stop']);
});

test('loadOpenclawCompat loads the generated stable compat module from the package root', async () => {
  const packageRoot = fs.mkdtempSync(path.join(os.tmpdir(), 'spritz-openclaw-package-'));
  const distDir = path.join(packageRoot, 'dist');
  fs.mkdirSync(distDir, { recursive: true });
  fs.writeFileSync(
    path.join(distDir, 'spritz-acp-compat.js'),
    [
      'export class GatewayClient {}',
      'export class AcpGatewayAgent {}',
      'export function loadConfig() { return { ok: true }; }',
      'export function buildGatewayConnectionDetails() { return { url: "ws://127.0.0.1:8080" }; }',
      'export async function resolveGatewayConnectionAuth() { return { token: "secret" }; }',
      '',
    ].join('\n'),
  );

  const compat = await loadOpenclawCompat({
    SPRITZ_OPENCLAW_PACKAGE_ROOT: packageRoot,
  });

  assert.equal(compat.GatewayClient.name, 'GatewayClient');
  assert.equal(compat.AcpGatewayAgent.name, 'AcpGatewayAgent');
  assert.deepEqual(compat.loadConfig(), { ok: true });
  assert.equal(compat.buildGatewayConnectionDetails().url, 'ws://127.0.0.1:8080');
  assert.deepEqual(await compat.resolveGatewayConnectionAuth(), { token: 'secret' });
});
