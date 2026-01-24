import assert from 'node:assert/strict';
import test from 'node:test';
import {
  kittyKeyboardOffSequence,
  terminalHardResetSequence,
  terminalResetSequence,
} from '../src/terminal_sequences.js';

test('terminal reset sequence forces kitty enhancements off', () => {
  assert.match(kittyKeyboardOffSequence, /\x1b\[=0u/);
  assert.match(kittyKeyboardOffSequence, /\x1b\[<999u/);
  assert.equal(
    terminalResetSequence.includes(kittyKeyboardOffSequence),
    true,
    'terminal reset should include kitty keyboard reset',
  );
});

test('terminal hard reset includes RIS and kitty reset', () => {
  assert.match(terminalHardResetSequence, /^\x1bc/);
  assert.equal(
    terminalHardResetSequence.includes(kittyKeyboardOffSequence),
    true,
    'hard reset should include kitty reset sequence',
  );
});
