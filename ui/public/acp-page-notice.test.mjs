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
  };
}

function loadModules(createACPClient) {
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
      createACPClient,
      extractACPText(value) {
        return typeof value?.text === 'string' ? value.text : '';
      },
    },
  };
  window.window = window;
  const context = vm.createContext({ window, document, console, setTimeout, clearTimeout, URL, URLSearchParams });
  context.globalThis = context.window;
  vm.runInContext(fs.readFileSync('/Users/onur/repos/spritz/ui/public/acp-render.js', 'utf8'), context, {
    filename: 'acp-render.js',
  });
  vm.runInContext(fs.readFileSync('/Users/onur/repos/spritz/ui/public/acp-page.js', 'utf8'), context, {
    filename: 'acp-page.js',
  });
  return window;
}

test('ACP client dispose errors do not surface as global notices', async () => {
  const noticeMessages = [];
  const toastMessages = [];
  const disposedError = Object.assign(new Error('ACP client disposed.'), { code: 'ACP_CLIENT_DISPOSED' });
  const window = loadModules(({ conversation }) => ({
    start: async () => {
      throw disposedError;
    },
    isReady: () => false,
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
  }));
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
                  acp: { agentInfo: { title: 'OpenClaw ACP Gateway', version: '2026.3.8' } },
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
    showNotice(message) {
      noticeMessages.push(message);
    },
    clearNotice() {
      noticeMessages.push('');
    },
    showToast(message) {
      toastMessages.push(message);
    },
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
  await new Promise((resolve) => setTimeout(resolve, 0));

  assert.deepEqual(noticeMessages.filter(Boolean), []);
  assert.deepEqual(toastMessages, []);
});

test('ACP page surfaces real startup errors as toasts', async () => {
  const noticeMessages = [];
  const toastMessages = [];
  const window = loadModules(({ conversation }) => ({
    start: async () => {
      throw new Error('Failed to connect to ACP gateway.');
    },
    isReady: () => false,
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
  }));
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
                  acp: { agentInfo: { title: 'OpenClaw ACP Gateway', version: '2026.3.8' } },
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
    showNotice(message) {
      noticeMessages.push(message);
    },
    clearNotice() {
      noticeMessages.push('');
    },
    showToast(message) {
      toastMessages.push(message);
    },
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
  await new Promise((resolve) => setTimeout(resolve, 0));

  assert.deepEqual(noticeMessages.filter(Boolean), []);
  assert.deepEqual(toastMessages, ['Failed to connect to ACP gateway.']);
});
