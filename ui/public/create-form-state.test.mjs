import test from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';

function createStorage(seed = {}) {
  const values = new Map(Object.entries(seed).map(([key, value]) => [key, String(value)]));
  return {
    getItem(key) {
      return values.has(key) ? values.get(key) : null;
    },
    setItem(key, value) {
      values.set(key, String(value));
    },
    removeItem(key) {
      values.delete(key);
    },
    dump() {
      return Object.fromEntries(values.entries());
    },
  };
}

test('buildCreateFormState keeps preset selection only when image still matches', async () => {
  const require = createRequire(import.meta.url);
  const { buildCreateFormState } = require('./create-form-state.js');

  const state = buildCreateFormState({
    activePreset: {
      name: 'OpenClaw Devbox',
      image: 'spritz-openclaw:latest',
    },
    image: 'custom-image:latest',
    repo: 'https://example.com/repo.git',
    branch: 'main',
    ttl: '8h',
    namespace: 'team-a',
    userConfig: 'sharedMounts: []',
  });

  assert.deepEqual(state.selection, { mode: 'custom' });
  assert.equal(state.fields.image, 'custom-image:latest');
});

test('writeCreateFormState stores reusable form state without a name field', async () => {
  const require = createRequire(import.meta.url);
  const {
    CREATE_FORM_STORAGE_KEY,
    buildCreateFormState,
    readCreateFormState,
    writeCreateFormState,
  } = require('./create-form-state.js');

  const storage = createStorage();
  const input = buildCreateFormState({
    activePreset: {
      name: 'Claude Code',
      image: 'spritz-claude-code:latest',
    },
    image: 'spritz-claude-code:latest',
    repo: 'https://example.com/repo.git',
    branch: 'staging',
    ttl: '12h',
    namespace: 'workspace-a',
    userConfig: 'sharedMounts: []',
  });

  writeCreateFormState(storage, input);

  const raw = JSON.parse(storage.getItem(CREATE_FORM_STORAGE_KEY));
  assert.equal(raw.name, undefined);
  assert.deepEqual(readCreateFormState(storage), input);
});
