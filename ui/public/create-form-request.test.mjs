import test from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';

test('resolveRepoSelection falls back to repo defaults for presets without repo ownership', async () => {
  const require = createRequire(import.meta.url);
  const { resolveRepoSelection } = require('./create-form-request.js');

  assert.deepEqual(
    resolveRepoSelection({
      activePreset: { name: 'Starter (minimal)', image: 'spritz-starter:latest' },
      repoValue: '',
      branchValue: '',
      defaultRepoUrl: 'https://github.com/example/repo.git',
      defaultRepoBranch: 'main',
    }),
    {
      repoUrl: 'https://github.com/example/repo.git',
      repoBranch: 'main',
    },
  );
});

test('resolveRepoSelection preserves explicit blank repo settings owned by the preset', async () => {
  const require = createRequire(import.meta.url);
  const { resolveRepoSelection } = require('./create-form-request.js');

  assert.deepEqual(
    resolveRepoSelection({
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
    }),
    {
      repoUrl: '',
      repoBranch: '',
    },
  );
});
