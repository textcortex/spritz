import test from 'node:test';
import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import {
  assertSmokeCreateResponse,
  buildSmokeSpzEnvironment,
  buildIdempotencyKey,
  buildSmokeToken,
  extractACPText,
  isForbiddenFailure,
  joinACPTextChunks,
  parseSmokeArgs,
  parsePresetList,
  resolveACPEndpoint,
  resolveWebSocketConstructor,
  resolveSpzCommand,
  runCommand,
  waitForWebSocketOpen,
} from './acp-smoke-lib.mjs';

const thisFile = fileURLToPath(import.meta.url);
const expectedCliDir = path.join(path.dirname(path.dirname(thisFile)), 'cli');

test('resolveSpzCommand prefers explicit binary env override', () => {
  assert.deepEqual(resolveSpzCommand({ SPRITZ_SMOKE_SPZ_BIN: '/tmp/spz' }, { hasSpzOnPath: true }), {
    command: '/tmp/spz',
    args: [],
  });
});

test('resolveSpzCommand prefers the checked-out CLI before any global spz on PATH', () => {
  assert.deepEqual(resolveSpzCommand({}, { hasSpzOnPath: true }), {
    command: 'pnpm',
    args: ['--dir', expectedCliDir, 'exec', 'tsx', 'src/index.ts'],
  });
});

test('parsePresetList normalizes comma-delimited values', () => {
  assert.deepEqual(parsePresetList(' OPENCLAW, Claude Code ,, '), ['openclaw', 'claude-code']);
});

test('parseSmokeArgs requires explicit presets instead of assuming example ids', () => {
  assert.throws(
    () => parseSmokeArgs(['--owner-id', 'user-123'], { SPRITZ_SMOKE_API_URL: 'https://example.com/api', SPRITZ_SMOKE_BEARER_TOKEN: 'token' }),
    /--presets is required/,
  );
});

test('parseSmokeArgs normalizes provided preset ids', () => {
  const { values } = parseSmokeArgs(
    ['--owner-id', 'user-123', '--presets', 'OPENCLAW,Claude Code'],
    { SPRITZ_SMOKE_API_URL: 'https://example.com/api', SPRITZ_SMOKE_BEARER_TOKEN: 'token' },
  );
  assert.deepEqual(values.presets, ['openclaw', 'claude-code']);
});

test('parseSmokeArgs prefers the smoke namespace env over the generic namespace env', () => {
  const { values } = parseSmokeArgs(['--owner-id', 'user-123', '--presets', 'openclaw'], {
    SPRITZ_SMOKE_API_URL: 'https://example.com/api',
    SPRITZ_SMOKE_BEARER_TOKEN: 'token',
    SPRITZ_NAMESPACE: 'generic-ns',
    SPRITZ_SMOKE_NAMESPACE: 'smoke-ns',
  });
  assert.equal(values.namespace, 'smoke-ns');
});

test('parseSmokeArgs requires an explicit smoke api url and bearer token', () => {
  assert.throws(
    () => parseSmokeArgs(['--owner-id', 'user-123', '--presets', 'openclaw'], {}),
    /SPRITZ_SMOKE_API_URL is required/,
  );
  assert.throws(
    () => parseSmokeArgs(
      ['--owner-id', 'user-123', '--presets', 'openclaw'],
      { SPRITZ_SMOKE_API_URL: 'https://example.com/api' },
    ),
    /SPRITZ_SMOKE_BEARER_TOKEN is required/,
  );
});

test('buildSmokeSpzEnvironment strips ambient spritz auth and profile state', () => {
  const env = buildSmokeSpzEnvironment(
    {
      PATH: process.env.PATH,
      SPRITZ_API_URL: 'http://ambient/api',
      SPRITZ_BEARER_TOKEN: 'ambient-token',
      SPRITZ_PROFILE: 'ambient-profile',
      SPRITZ_NAMESPACE: 'ambient-ns',
      SPRITZ_USER_ID: 'ambient-user',
    },
    {
      apiUrl: 'https://smoke.example.com/api',
      bearerToken: 'smoke-token',
      namespace: 'smoke-ns',
      configDir: '/tmp/smoke-config',
    },
  );

  assert.equal(env.SPRITZ_API_URL, 'https://smoke.example.com/api');
  assert.equal(env.SPRITZ_BEARER_TOKEN, 'smoke-token');
  assert.equal(env.SPRITZ_NAMESPACE, 'smoke-ns');
  assert.equal(env.SPRITZ_CONFIG_DIR, '/tmp/smoke-config');
  assert.equal(env.SPRITZ_PROFILE, undefined);
  assert.equal(env.SPRITZ_USER_ID, undefined);
});

test('extractACPText flattens nested content blocks', () => {
  assert.equal(
    extractACPText([{ type: 'text', text: 'hello' }, { type: 'content', content: [{ text: 'world' }] }]),
    'hello\nworld',
  );
});

test('extractACPText falls back to resource uri when text is empty', () => {
  assert.equal(
    extractACPText({ resource: { text: '', uri: 'file://smoke-fallback.txt' } }),
    'file://smoke-fallback.txt',
  );
});

test('joinACPTextChunks preserves chunked tokens without inserted separators', () => {
  assert.equal(joinACPTextChunks([{ text: 'spr' }, { text: 'itz-smoke-openclaw' }]), 'spritz-smoke-openclaw');
});

test('buildIdempotencyKey and smoke token normalize preset names', () => {
  assert.equal(buildIdempotencyKey('spritz smoke', 'claude-code'), 'spritz-smoke-claude-code');
  assert.equal(buildSmokeToken('openclaw'), 'spritz-smoke-openclaw');
});

test('resolveACPEndpoint prefers discovered ACP port/path and normalizes the path', () => {
  assert.deepEqual(
    resolveACPEndpoint({
      status: {
        acp: {
          endpoint: {
            port: 9421,
            path: 'rpc',
          },
        },
      },
    }),
    { port: 9421, path: '/rpc' },
  );
  assert.deepEqual(resolveACPEndpoint({}), { port: 2529, path: '/' });
});

test('resolveWebSocketConstructor returns a usable client constructor', () => {
  class FakeWebSocket {}
  const WebSocketCtor = resolveWebSocketConstructor({
    globalObject: {},
    requireFn() {
      return { WebSocket: FakeWebSocket };
    },
  });
  assert.equal(typeof WebSocketCtor, 'function');
  assert.equal(WebSocketCtor, FakeWebSocket);
});

test('assertSmokeCreateResponse accepts canonicalized preset ids from the API', () => {
  const instanceName = assertSmokeCreateResponse({
    spritz: { metadata: { name: 'openclaw-calm-ridge' } },
    ownerId: 'user-123',
    actorType: 'service',
    presetId: 'openclaw',
    chatUrl: 'https://example.com/c/openclaw-calm-ridge',
    instanceUrl: 'https://example.com/i/openclaw-calm-ridge/',
    accessUrl: 'https://example.com/c/openclaw-calm-ridge',
  }, 'user-123', 'OPENCLAW');

  assert.equal(instanceName, 'openclaw-calm-ridge');
});

test('isForbiddenFailure only accepts explicit forbidden command failures', () => {
  assert.equal(isForbiddenFailure({ code: 1, stderr: 'forbidden', stdout: '' }), true);
  assert.equal(isForbiddenFailure({ code: 1, stderr: 'internal server error', stdout: '' }), false);
  assert.equal(isForbiddenFailure({ code: 0, stderr: '', stdout: 'ok' }), false);
});

test('runCommand marks timed-out child processes', async () => {
  const result = await runCommand('node', ['-e', 'setTimeout(() => {}, 1000)'], { timeoutMs: 25 });

  assert.equal(result.timedOut, true);
  assert.equal(result.code, 124);
});

test('runCommand escalates to SIGKILL when the child ignores SIGTERM', async () => {
  const started = Date.now();
  const result = await runCommand(
    'node',
    ['-e', "process.on('SIGTERM', () => {}); setInterval(() => {}, 1000)"],
    { timeoutMs: 25 },
  );

  assert.equal(result.timedOut, true);
  assert.equal(result.code, 124);
  assert.ok(Date.now() - started < 2500);
});

test('waitForWebSocketOpen rejects and closes on handshake timeout', async () => {
  let closeCount = 0;
  const socket = new EventTarget();
  socket.close = () => {
    closeCount += 1;
  };
  socket.terminate = () => {
    closeCount += 1;
  };

  await assert.rejects(
    () => waitForWebSocketOpen(socket, 10),
    /ACP websocket handshake timed out/,
  );
  assert.equal(closeCount > 0, true);
});

test('waitForWebSocketOpen resolves when the socket opens', async () => {
  const socket = new EventTarget();
  socket.close = () => {};
  socket.terminate = () => {};
  const openPromise = waitForWebSocketOpen(socket, 100);
  setTimeout(() => socket.dispatchEvent(new Event('open')), 0);
  await openPromise;
});
