import test from 'node:test';
import assert from 'node:assert/strict';

import {
  assertSmokeCreateResponse,
  buildIdempotencyKey,
  buildSmokeToken,
  extractACPText,
  isForbiddenFailure,
  joinACPTextChunks,
  parseSmokeArgs,
  parsePresetList,
  resolveWebSocketConstructor,
  resolveSpzCommand,
  summarizeWorkspaceFailure,
} from './acp-smoke-lib.mjs';

test('resolveSpzCommand prefers explicit binary env override', () => {
  assert.deepEqual(resolveSpzCommand({ SPRITZ_SMOKE_SPZ_BIN: '/tmp/spz' }, { hasSpzOnPath: true }), {
    command: '/tmp/spz',
    args: [],
  });
});

test('resolveSpzCommand prefers spz on path before pnpm fallback', () => {
  assert.deepEqual(resolveSpzCommand({}, { hasSpzOnPath: true }), {
    command: 'spz',
    args: [],
  });
});

test('parsePresetList normalizes comma-delimited values', () => {
  assert.deepEqual(parsePresetList(' OPENCLAW, Claude Code ,, '), ['openclaw', 'claude-code']);
});

test('parseSmokeArgs requires explicit presets instead of assuming example ids', () => {
  assert.throws(
    () => parseSmokeArgs(['--owner-id', 'user-123'], {}),
    /--presets is required/,
  );
});

test('parseSmokeArgs normalizes provided preset ids', () => {
  const { values } = parseSmokeArgs(['--owner-id', 'user-123', '--presets', 'OPENCLAW,Claude Code'], {});
  assert.deepEqual(values.presets, ['openclaw', 'claude-code']);
});

test('extractACPText flattens nested content blocks', () => {
  assert.equal(
    extractACPText([{ type: 'text', text: 'hello' }, { type: 'content', content: [{ text: 'world' }] }]),
    'hello\nworld',
  );
});

test('joinACPTextChunks preserves chunked tokens without inserted separators', () => {
  assert.equal(joinACPTextChunks([{ text: 'spr' }, { text: 'itz-smoke-openclaw' }]), 'spritz-smoke-openclaw');
});

test('buildIdempotencyKey and smoke token normalize preset names', () => {
  assert.equal(buildIdempotencyKey('spritz smoke', 'claude-code'), 'spritz-smoke-claude-code');
  assert.equal(buildSmokeToken('openclaw'), 'spritz-smoke-openclaw');
});

test('resolveWebSocketConstructor returns a usable client constructor', () => {
  const WebSocketCtor = resolveWebSocketConstructor();
  assert.equal(typeof WebSocketCtor, 'function');
});

test('assertSmokeCreateResponse accepts canonicalized preset ids from the API', () => {
  const workspaceName = assertSmokeCreateResponse({
    spritz: { metadata: { name: 'openclaw-calm-ridge' } },
    ownerId: 'user-123',
    actorType: 'service',
    presetId: 'openclaw',
    chatUrl: 'https://example.com/#chat/openclaw-calm-ridge',
    workspaceUrl: 'https://example.com/w/openclaw-calm-ridge/',
    accessUrl: 'https://example.com/#chat/openclaw-calm-ridge',
  }, 'user-123', 'OPENCLAW');

  assert.equal(workspaceName, 'openclaw-calm-ridge');
});

test('summarizeWorkspaceFailure prioritizes shared mount init failures', () => {
  const result = summarizeWorkspaceFailure({
    spritz: { status: { phase: 'Provisioning', message: 'waiting for deployment' } },
    podList: {
      items: [
        {
          status: {
            initContainerStatuses: [
              {
                name: 'shared-mounts-init',
                state: {
                  waiting: {
                    reason: 'CrashLoopBackOff',
                    message: 'timed out talking to spritz-api',
                  },
                },
              },
            ],
          },
        },
      ],
    },
  });

  assert.deepEqual(result, {
    stage: 'shared-mount-init',
    message: 'timed out talking to spritz-api',
  });
});

test('summarizeWorkspaceFailure reports image pull failures distinctly', () => {
  const result = summarizeWorkspaceFailure({
    spritz: { status: { phase: 'Provisioning', message: 'waiting for deployment' } },
    podList: {
      items: [
        {
          status: {
            containerStatuses: [
              {
                name: 'spritz',
                state: {
                  waiting: {
                    reason: 'ImagePullBackOff',
                    message: 'image not found',
                  },
                },
              },
            ],
          },
        },
      ],
    },
  });

  assert.deepEqual(result, {
    stage: 'image-pull',
    message: 'image not found',
  });
});

test('isForbiddenFailure only accepts explicit forbidden command failures', () => {
  assert.equal(isForbiddenFailure({ code: 1, stderr: 'forbidden', stdout: '' }), true);
  assert.equal(isForbiddenFailure({ code: 1, stderr: 'internal server error', stdout: '' }), false);
  assert.equal(isForbiddenFailure({ code: 0, stderr: '', stdout: 'ok' }), false);
});
