import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';

function createStorage(seed = {}) {
  const values = new Map(Object.entries(seed));
  return {
    getItem(key) {
      return values.has(key) ? values.get(key) : null;
    },
    setItem(key, value) {
      values.set(key, String(value));
    },
    removeItem(key) {
      values.delete(key);
    },
  };
}

function createElement(tagName) {
  let innerHTML = '';
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
    get innerHTML() {
      return innerHTML;
    },
    set innerHTML(value) {
      innerHTML = String(value);
      this.children = [];
    },
  };
}

function collectText(node) {
  if (!node) return '';
  const own = typeof node.textContent === 'string' ? node.textContent : '';
  const childText = Array.isArray(node.children) ? node.children.map((child) => collectText(child)).join(' ') : '';
  return `${own} ${childText}`.replace(/\s+/g, ' ').trim();
}

function loadModules(storageSeed = {}, createACPClient = null) {
  const document = { createElement };
  const window = {
    document,
    location: {
      hash: '#chat/young-crest/conv-1',
      assign() {},
      replace() {},
      origin: 'https://example.test',
    },
    sessionStorage: createStorage(storageSeed),
    open() {},
    setTimeout,
    clearTimeout,
    SpritzACPClient: {
      createACPClient: createACPClient || function defaultCreateACPClient() {
        return {
          start: async () => {},
          isReady: () => true,
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

test('ACP page restores cached transcript when revisiting a conversation', async () => {
  const cacheKey = 'spritz:acp:transcript:conv-1';
  let releaseStart = () => {};
  const window = loadModules(
    {
      [cacheKey]: JSON.stringify({
        version: 1,
        conversationId: 'conv-1',
        transcript: {
          messages: [
            {
              id: 'assistant-1',
              kind: 'assistant',
              title: '',
              status: '',
              tone: '',
              meta: '',
              blocks: [{ type: 'text', text: 'Cached assistant reply.' }],
              streaming: false,
              toolCallId: '',
            },
          ],
          availableCommands: [],
          currentMode: '',
          usage: null,
        },
      }),
    },
    () => ({
      start: async () => {
        await new Promise((resolve) => {
          releaseStart = resolve;
        });
      },
      isReady: () => true,
      cancelPrompt() {},
      dispose() {},
    }),
  );

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
              spec: { title: 'Cached conversation', sessionId: 'sess-1' },
              status: { updatedAt: '2026-03-10T06:00:00Z' },
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
  await new Promise((resolve) => setTimeout(resolve, 0));

  assert.match(collectText(shellEl), /Cached assistant reply\./);
  releaseStart();
});

test('ACP page replaces cached transcript with backend replay during bootstrap', async () => {
  const cacheKey = 'spritz:acp:transcript:conv-1';
  const window = loadModules(
    {
      [cacheKey]: JSON.stringify({
        version: 1,
        conversationId: 'conv-1',
        transcript: {
          messages: [
            {
              id: 'assistant-cached',
              kind: 'assistant',
              title: '',
              status: '',
              tone: '',
              meta: '',
              blocks: [{ type: 'text', text: 'Cached assistant reply.' }],
              streaming: false,
              toolCallId: '',
            },
          ],
          availableCommands: [],
          currentMode: '',
          usage: null,
        },
      }),
    },
    ({ onUpdate }) => ({
      start: async () => {
        onUpdate({ sessionUpdate: 'user_message_chunk', content: { type: 'text', text: 'Replay user message.' } });
        onUpdate({ sessionUpdate: 'agent_message_chunk', content: { type: 'text', text: 'Replay assistant reply.' } });
      },
      isReady: () => true,
      cancelPrompt() {},
      dispose() {},
    }),
  );

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
              spec: { title: 'Replay conversation', sessionId: 'sess-1' },
              status: { updatedAt: '2026-03-10T06:00:00Z' },
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
  await new Promise((resolve) => setTimeout(resolve, 0));

  const text = collectText(shellEl);
  assert.match(text, /Replay user message\./);
  assert.match(text, /Replay assistant reply\./);
  assert.doesNotMatch(text, /Cached assistant reply\./);
});

test('ACP page clears cached transcript when backend replay returns no transcript updates', async () => {
  const cacheKey = 'spritz:acp:transcript:conv-1';
  const window = loadModules(
    {
      [cacheKey]: JSON.stringify({
        version: 1,
        conversationId: 'conv-1',
        transcript: {
          messages: [
            {
              id: 'assistant-cached',
              kind: 'assistant',
              title: '',
              status: '',
              tone: '',
              meta: '',
              blocks: [{ type: 'text', text: 'Cached assistant reply.' }],
              streaming: false,
              toolCallId: '',
            },
          ],
          availableCommands: [],
          currentMode: '',
          usage: null,
        },
      }),
    },
    () => ({
      start: async () => {},
      isReady: () => true,
      cancelPrompt() {},
      dispose() {},
    }),
  );

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
              spec: { title: 'Replay conversation', sessionId: 'sess-1' },
              status: { updatedAt: '2026-03-10T06:00:00Z' },
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
  await new Promise((resolve) => setTimeout(resolve, 0));

  const text = collectText(shellEl);
  assert.doesNotMatch(text, /Cached assistant reply\./);
  assert.match(text, /Conversation is ready\. Send a message to begin\./);
});
