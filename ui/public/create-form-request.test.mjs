import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
import { uiDistPath } from '../test-paths.mjs';

function loadCreateFormRequestModule() {
  const context = {
    console,
    globalThis: {},
    module: { exports: {} },
  };
  context.globalThis = context;
  vm.createContext(context);
  vm.runInContext(fs.readFileSync(uiDistPath('create-form-request.js'), 'utf8'), context, {
    filename: uiDistPath('create-form-request.js'),
  });
  return context.module.exports;
}

function plain(value) {
  return JSON.parse(JSON.stringify(value));
}

test('resolveRepoSelection falls back to repo defaults for presets without repo ownership', async () => {
  const { resolveRepoSelection } = loadCreateFormRequestModule();

  assert.deepEqual(
    plain(resolveRepoSelection({
      activePreset: { name: 'Starter (minimal)', image: 'spritz-starter:latest' },
      repoValue: '',
      branchValue: '',
      defaultRepoUrl: 'https://github.com/example/repo.git',
      defaultRepoBranch: 'main',
    })),
    {
      repoUrl: 'https://github.com/example/repo.git',
      repoBranch: 'main',
    },
  );
});

test('resolveRepoSelection preserves explicit blank repo settings owned by the preset', async () => {
  const { resolveRepoSelection } = loadCreateFormRequestModule();

  assert.deepEqual(
    plain(resolveRepoSelection({
      activePreset: {
        name: 'OpenClaw',
        image: 'spritz-openclaw:latest',
        repoUrl: '',
        branch: '',
      },
      repoValue: '',
      branchValue: '',
      defaultRepoUrl: 'https://github.com/example/private.git',
      defaultRepoBranch: 'staging',
    })),
    {
      repoUrl: '',
      repoBranch: '',
    },
  );
});

test('buildCreatePayload uses presetId and does not serialize preset env overrides', async () => {
  const { buildCreatePayload } = loadCreateFormRequestModule();

  const payload = buildCreatePayload({
    name: '',
    imageValue: 'registry.example/spritz-claude-code@sha256:abc',
    namespace: 'spritz-production',
    ownerId: 'user-123',
    activePreset: {
      id: 'claude-code',
      name: 'Claude Code',
      image: 'registry.example/spritz-claude-code@sha256:abc',
      namePrefix: 'claude-code',
      env: [
        {
          name: 'ANTHROPIC_API_KEY',
          valueFrom: {
            secretKeyRef: {
              name: 'spritz-claude-code-anthropic',
              key: 'api-key',
            },
          },
        },
      ],
    },
    repoValue: '',
    branchValue: '',
    defaultRepoUrl: '',
    defaultRepoBranch: '',
    defaultRepoDir: '',
    ttlValue: '',
  });

  assert.equal(payload.presetId, 'claude-code');
  assert.equal(payload.namePrefix, 'claude-code');
  assert.deepEqual(plain(payload.spec.owner), { id: 'user-123' });
  assert.equal(payload.spec.image, undefined);
  assert.equal(payload.spec.env, undefined);
});

test('buildCreatePayload falls back to explicit image when preset is no longer aligned', async () => {
  const { buildCreatePayload } = loadCreateFormRequestModule();

  const payload = buildCreatePayload({
    name: '',
    imageValue: 'registry.example/custom-image:latest',
    activePreset: {
      id: 'claude-code',
      name: 'Claude Code',
      image: 'registry.example/spritz-claude-code@sha256:abc',
      namePrefix: 'claude-code',
    },
    repoValue: '',
    branchValue: '',
    defaultRepoUrl: '',
    defaultRepoBranch: '',
    defaultRepoDir: '',
    ttlValue: '',
  });

  assert.equal(payload.presetId, undefined);
  assert.equal(payload.spec.image, 'registry.example/custom-image:latest');
});
