import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
import { uiDistPath } from '../test-paths.mjs';

function loadPresetConfigModule() {
  const context = {
    console,
    globalThis: {},
    module: { exports: {} },
  };
  context.globalThis = context;
  vm.createContext(context);
  vm.runInContext(fs.readFileSync(uiDistPath('preset-config.js'), 'utf8'), context, {
    filename: uiDistPath('preset-config.js'),
  });
  return context.module.exports;
}

const { parsePresets } = loadPresetConfigModule();

function plain(value) {
  return JSON.parse(JSON.stringify(value));
}

test('parsePresets returns raw arrays directly', () => {
  const presets = [{ name: 'OpenClaw', image: 'example/openclaw' }];
  assert.equal(parsePresets(presets), presets);
});

test('parsePresets returns null for the runtime placeholder', () => {
  assert.equal(parsePresets('__SPRITZ_UI_PRESETS__', { placeholder: '__SPRITZ_UI_PRESETS__' }), null);
});

test('parsePresets fails closed for malformed values', () => {
  const errors = [];
  const parsed = parsePresets('[{"name":"OpenClaw","image":"broken"}', {
    logger: { error: (...args) => errors.push(args) },
  });
  assert.deepEqual(plain(parsed), []);
  assert.equal(errors.length, 1);
});
