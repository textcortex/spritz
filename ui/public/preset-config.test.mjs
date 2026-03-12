import test from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';

const require = createRequire(import.meta.url);
const { parsePresets } = require('/Users/onur/repos/spritz/ui/public/preset-config.js');

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
  assert.deepEqual(parsed, []);
  assert.equal(errors.length, 1);
});
