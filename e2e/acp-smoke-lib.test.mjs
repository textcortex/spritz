import test from 'node:test';
import assert from 'node:assert/strict';

import {
  buildIdempotencyKey,
  buildSmokeToken,
  extractACPText,
  isForbiddenFailure,
  joinACPTextChunks,
  parsePresetList,
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
  assert.deepEqual(parsePresetList(' openclaw, claude-code ,, '), ['openclaw', 'claude-code']);
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
