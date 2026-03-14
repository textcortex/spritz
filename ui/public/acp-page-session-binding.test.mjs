import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
import { uiDistPath } from '../test-paths.mjs';

function createElement(tagName) {
  const listeners = new Map();
  return {
    tagName,
    hidden: false,
    disabled: false,
    textContent: '',
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
    addEventListener(name, handler) {
      listeners.set(name, handler);
    },
    setAttribute(name, value) {
      this[name] = value;
    },
    querySelector() {
      return null;
    },
    contains() {
      return false;
    },
    click() {
      if (this.disabled) return;
      const handler = listeners.get('click');
      if (handler) handler({ preventDefault() {} });
    },
    keydown(event) {
      const handler = listeners.get('keydown');
      if (handler) handler(event);
    },
  };
}

function walk(node, predicate) {
  if (!node) return null;
  if (predicate(node)) return node;
  for (const child of node.children || []) {
    const match = walk(child, predicate);
    if (match) return match;
  }
  return null;
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
  vm.runInContext(fs.readFileSync(uiDistPath('acp-render.js'), 'utf8'), context, {
    filename: 'acp-render.js',
  });
  vm.runInContext(fs.readFileSync(uiDistPath('acp-page.js'), 'utf8'), context, {
    filename: 'acp-page.js',
  });
  return window;
}

test('ACP page rebinds the selected conversation before sending on a stale client', async () => {
  const startedConversations = [];
  const sentPrompts = [];
  let clientCount = 0;
  const toastMessages = [];
  const requestPaths = [];

  const window = loadModules(({ conversation }) => {
    clientCount += 1;
    const clientIndex = clientCount;
    const stale = clientIndex === 1;
    return {
      start: async () => {
        startedConversations.push(conversation?.metadata?.name || '');
      },
      isReady: () => true,
      getConversationId: () => (stale ? 'stale-conv' : conversation?.metadata?.name || ''),
      getSessionId: () => (stale ? 'session-stale' : conversation?.spec?.sessionId || ''),
      matchesConversation(targetConversation) {
        return (
          this.getConversationId() === (targetConversation?.metadata?.name || '') &&
          this.getSessionId() === (targetConversation?.spec?.sessionId || '')
        );
      },
      async sendPrompt(text) {
        sentPrompts.push({
          clientIndex,
          conversationId: conversation?.metadata?.name || '',
          text,
        });
        return { stopReason: 'end_turn' };
      },
      cancelPrompt() {},
      dispose() {},
    };
  });

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
      requestPaths.push(path);
      if (path === '/acp/agents') {
        return {
          items: [
            {
              spritz: {
                metadata: { name: 'young-crest' },
                status: {
                  acp: { agentInfo: { title: 'OpenClaw ACP Gateway', version: '2026.3.8' } },
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
              spec: { title: 'Test conversation', sessionId: 'session-fresh' },
              status: { bindingState: 'active', updatedAt: '2026-03-10T08:00:00Z' },
            },
          ],
        };
      }
      if (path === '/acp/conversations/conv-1/bootstrap') {
        return {
          conversation: {
            metadata: { name: 'conv-1' },
            spec: { title: 'Test conversation', sessionId: 'session-fresh', cwd: '/home/dev' },
            status: { bindingState: 'active', boundSessionId: 'session-fresh' },
          },
          effectiveSessionId: 'session-fresh',
          bindingState: 'active',
          replaced: false,
        };
      }
      throw new Error(`unexpected path ${path}`);
    },
    showToast(message) {
      toastMessages.push(message);
    },
    showNotice() {},
    clearNotice() {},
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

  const composer = walk(shellEl, (node) => node.tagName === 'textarea');
  const sendButton = walk(shellEl, (node) => node.tagName === 'button' && (node.textContent === 'Send' || node.className === 'acp-composer-send'));

  assert.ok(composer);
  assert.ok(sendButton);

  composer.value = 'test 3';
  sendButton.click();
  await new Promise((resolve) => setTimeout(resolve, 0));
  await new Promise((resolve) => setTimeout(resolve, 0));

  assert.deepEqual(startedConversations, ['conv-1', 'conv-1']);
  assert.deepEqual(sentPrompts, [{ clientIndex: 2, conversationId: 'conv-1', text: 'test 3' }]);
  assert.deepEqual(toastMessages, []);
  assert.equal(requestPaths.filter((path) => path === '/acp/conversations/conv-1/bootstrap').length, 0);
});

test('ACP page repairs a missing session by bootstrapping once and reconnecting', async () => {
  const startedConversations = [];
  const requestPaths = [];
  const sentPrompts = [];
  let clientCount = 0;

  const window = loadModules(({ conversation }) => {
    clientCount += 1;
    const clientIndex = clientCount;
    return {
      async start() {
        startedConversations.push(conversation?.metadata?.name || '');
        if (clientIndex === 1) {
          const error = new Error('session missing');
          error.code = 'ACP_SESSION_MISSING';
          throw error;
        }
      },
      isReady() {
        return clientIndex > 1;
      },
      getConversationId() {
        return conversation?.metadata?.name || '';
      },
      getSessionId() {
        return conversation?.spec?.sessionId || '';
      },
      matchesConversation(targetConversation) {
        return this.getConversationId() === (targetConversation?.metadata?.name || '') &&
          this.getSessionId() === (targetConversation?.spec?.sessionId || '');
      },
      async sendPrompt(text) {
        sentPrompts.push({ clientIndex, text });
        return { stopReason: 'end_turn' };
      },
      cancelPrompt() {},
      dispose() {},
    };
  });

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
      requestPaths.push(path);
      if (path === '/acp/agents') {
        return {
          items: [
            {
              spritz: {
                metadata: { name: 'young-crest' },
                status: {
                  acp: { agentInfo: { title: 'OpenClaw ACP Gateway', version: '2026.3.8' } },
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
              spec: { title: 'Test conversation', sessionId: 'session-stale' },
              status: { bindingState: 'active', updatedAt: '2026-03-10T08:00:00Z' },
            },
          ],
        };
      }
      if (path === '/acp/conversations/conv-1/bootstrap') {
        return {
          conversation: {
            metadata: { name: 'conv-1' },
            spec: { title: 'Test conversation', sessionId: 'session-fresh', cwd: '/home/dev' },
            status: { bindingState: 'active', boundSessionId: 'session-fresh' },
          },
          effectiveSessionId: 'session-fresh',
          bindingState: 'active',
          replaced: true,
        };
      }
      throw new Error(`unexpected path ${path}`);
    },
    showToast() {},
    showNotice() {},
    clearNotice() {},
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
  await new Promise((resolve) => setTimeout(resolve, 0));

  const composer = walk(shellEl, (node) => node.tagName === 'textarea');
  const sendButton = walk(shellEl, (node) => node.tagName === 'button' && (node.textContent === 'Send' || node.className === 'acp-composer-send'));

  assert.ok(composer);
  assert.ok(sendButton);
  composer.value = 'repair';
  sendButton.click();
  await new Promise((resolve) => setTimeout(resolve, 0));
  await new Promise((resolve) => setTimeout(resolve, 0));

  assert.deepEqual(startedConversations, ['conv-1', 'conv-1']);
  assert.equal(requestPaths.filter((path) => path === '/acp/conversations/conv-1/bootstrap').length, 1);
  assert.deepEqual(sentPrompts, [{ clientIndex: 2, text: 'repair' }]);
});
