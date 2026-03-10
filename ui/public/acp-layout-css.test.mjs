import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';

const styles = fs.readFileSync('/Users/onur/repos/spritz/ui/public/styles.css', 'utf8');

function getRule(selector) {
  const escapedSelector = selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const match = styles.match(new RegExp(`${escapedSelector}\\s*\\{([\\s\\S]*?)\\n\\}`, 'm'));
  assert.ok(match, `expected CSS rule for ${selector}`);
  return match[1];
}

function assertDecl(rule, declaration, selector) {
  assert.match(
    rule,
    new RegExp(`(^|\\n)\\s*${declaration.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}\\s*;`, 'm'),
    `expected "${declaration}" in ${selector}`,
  );
}

test('ACP chat shell uses viewport-bound layout with internal scrolling', () => {
  const chatShell = getRule('.shell[data-view="chat"]');
  assertDecl(chatShell, 'display: flex', '.shell[data-view="chat"]');
  assertDecl(chatShell, 'flex-direction: column', '.shell[data-view="chat"]');
  assertDecl(chatShell, 'height: 100dvh', '.shell[data-view="chat"]');
  assertDecl(chatShell, 'overflow: hidden', '.shell[data-view="chat"]');

  const acpShell = getRule('.acp-shell');
  assertDecl(acpShell, 'flex: 1', '.acp-shell');
  assertDecl(acpShell, 'min-height: 0', '.acp-shell');

  const sidebar = getRule('.acp-sidebar');
  assertDecl(sidebar, 'min-height: 0', '.acp-sidebar');
  assertDecl(sidebar, 'overflow: hidden', '.acp-sidebar');

  const threadList = getRule('.acp-thread-list');
  assertDecl(threadList, 'min-height: 0', '.acp-thread-list');
  assertDecl(threadList, 'overflow: auto', '.acp-thread-list');

  const main = getRule('.acp-main');
  assertDecl(main, 'min-height: 0', '.acp-main');
  assertDecl(main, 'overflow: hidden', '.acp-main');

  const mainBody = getRule('.acp-main-body');
  assertDecl(mainBody, 'min-height: 0', '.acp-main-body');
  assertDecl(mainBody, 'overflow: auto', '.acp-main-body');
});
