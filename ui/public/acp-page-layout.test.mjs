import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
import { uiDistPath } from '../test-paths.mjs';

function createElement(tagName) {
  return {
    tagName,
    hidden: false,
    disabled: false,
    textContent: '',
    innerHTML: '',
    value: '',
    className: '',
    children: [],
    dataset: {},
    style: {},
    append(...items) {
      this.children.push(...items);
    },
    appendChild(item) {
      this.children.push(item);
    },
    remove() {
      this.removed = true;
    },
    addEventListener() {},
    setAttribute(name, value) {
      this[name] = value;
    },
    querySelector() {
      return null;
    },
    contains() {
      return false;
    },
  };
}

function loadModules() {
  const document = { createElement };
  const window = {
    document,
    location: {
      hash: '#chat/young-crest/conv-1',
      assign() {},
      replace() {},
      origin: 'https://example.test',
    },
    open() {},
    setTimeout,
    clearTimeout,
    SpritzACPClient: {
      createACPClient({ conversation }) {
        return {
          start: async () => {},
          isReady: () => true,
          getConversationId: () => conversation?.metadata?.name || '',
          getSessionId: () => conversation?.spec?.sessionId || '',
          matchesConversation(targetConversation) {
            return (
              this.getConversationId() === (targetConversation?.metadata?.name || '') &&
              this.getSessionId() === (targetConversation?.spec?.sessionId || '')
            );
          },
          cancelPrompt() {},
          dispose() {},
        };
      },
      extractACPText(value) {
        return typeof value?.text === 'string' ? value.text : '';
      },
    },
  };
  window.window = window;
  const context = vm.createContext({ window, document, console, setTimeout, clearTimeout });
  context.globalThis = context.window;
  const renderScript = fs.readFileSync(uiDistPath('acp-render.js'), 'utf8');
  vm.runInContext(renderScript, context, { filename: 'acp-render.js' });
  const pageScript = fs.readFileSync(uiDistPath('acp-page.js'), 'utf8');
  vm.runInContext(pageScript, context, { filename: 'acp-page.js' });
  return { window, document };
}

test('ACP page renders a two-pane shell with a single sidebar rail', async () => {
  const { window } = loadModules();
  const shellEl = createElement('main');
  const createSection = createElement('section');
  const listSection = createElement('section');

  window.SpritzACPPage.renderACPPage('young-crest', 'conv-1', {
    activePage: null,
    apiBaseUrl: '',
    authBearerTokenParam: 'token',
    getAuthToken() {
      return '';
    },
    async request(path) {
      if (path === '/acp/agents') {
        return {
          items: [
            {
              spritz: {
                metadata: { name: 'young-crest' },
                status: {
                  acp: {
                    agentInfo: { title: 'OpenClaw ACP Gateway', name: 'openclaw-acp', version: '2026.3.8' },
                  },
                  url: 'https://example.test/w/young-crest/',
                },
              },
            },
          ],
        };
      }
      if (path.startsWith('/acp/conversations?')) {
        return {
          items: [
            {
              metadata: { name: 'conv-1' },
              spec: { title: 'Test conversation', sessionId: 'sess-1', cwd: '/home/dev' },
              status: { updatedAt: '2026-03-10T05:44:00Z' },
            },
          ],
        };
      }
      if (path === '/acp/conversations/conv-1/bootstrap') {
        return {
          conversation: {
            metadata: { name: 'conv-1' },
            spec: { title: 'Test conversation', sessionId: 'sess-1', cwd: '/home/dev' },
            status: { bindingState: 'active', boundSessionId: 'sess-1', updatedAt: '2026-03-10T05:44:00Z' },
          },
          effectiveSessionId: 'sess-1',
          bindingState: 'active',
          replaced: false,
        };
      }
      throw new Error(`unexpected path ${path}`);
    },
    showNotice() {},
    buildOpenUrl(url) {
      return url;
    },
    cleanupTerminal() {},
    shellEl,
    createSection,
    listSection,
    setHeaderCopy() {},
  });

  await new Promise((resolve) => setTimeout(resolve, 0));

  assert.equal(shellEl.children.length, 1);
  const card = shellEl.children[0];
  assert.equal(card.className.includes('acp-shell'), true);
  assert.ok(card.children.length >= 2);
  assert.equal(card.children[0].className.includes('acp-sidebar'), true);
  const mainEl = card.children.find((c) => c.className && c.className.includes('acp-main'));
  assert.ok(mainEl);
});
