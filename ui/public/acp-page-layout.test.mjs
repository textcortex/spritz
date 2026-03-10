import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';

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
      createACPClient() {
        return {
          start: async () => {},
          isReady: () => false,
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
  const renderScript = fs.readFileSync('/Users/onur/repos/spritz/ui/public/acp-render.js', 'utf8');
  vm.runInContext(renderScript, context, { filename: 'acp-render.js' });
  const pageScript = fs.readFileSync('/Users/onur/repos/spritz/ui/public/acp-page.js', 'utf8');
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
              spec: { title: 'Test conversation', sessionId: 'sess-1' },
              status: { updatedAt: '2026-03-10T05:44:00Z' },
            },
          ],
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
  assert.equal(card.children.length, 2);
  assert.equal(card.children[0].className.includes('acp-sidebar'), true);
  assert.equal(card.children[1].className.includes('acp-main'), true);
});
