import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import vm from 'node:vm';
import { uiPath, uiPublicPath } from './test-paths.mjs';
import { execFileSync } from 'node:child_process';

function renderConfig(env = {}) {
  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'spritz-ui-'));
  fs.copyFileSync(uiPublicPath('config.js'), path.join(tmpDir, 'config.js'));
  fs.copyFileSync(uiPublicPath('index.html'), path.join(tmpDir, 'index.html'));

  execFileSync('/bin/sh', [uiPath('entrypoint.sh')], {
    env: {
      ...process.env,
      SPRITZ_UI_HTML_DIR: tmpDir,
      SPRITZ_UI_SKIP_NGINX: '1',
      ...env,
    },
  });

  const source = fs.readFileSync(path.join(tmpDir, 'config.js'), 'utf8');
  const context = { window: {} };
  vm.createContext(context);
  vm.runInContext(source, context);
  return context.window.SPRITZ_CONFIG;
}

test('entrypoint renders presets as a real JS array even with nested JSON env values', () => {
  const nestedConfig = JSON.stringify({ browser: { enabled: true, headless: true } });
  const presets = JSON.stringify([
    {
      name: 'OpenClaw',
      image: 'registry.example/openclaw@sha256:123',
      env: [{ name: 'OPENCLAW_CONFIG_JSON', value: nestedConfig }],
    },
  ]);

  const config = renderConfig({ SPRITZ_UI_PRESETS: presets });
  assert.ok(Array.isArray(config.presets));
  assert.equal(config.presets[0].image, 'registry.example/openclaw@sha256:123');
  assert.equal(config.presets[0].env[0].value, nestedConfig);
});

test('entrypoint renders null when presets are unset', () => {
  const config = renderConfig();
  assert.equal(config.presets, null);
});
