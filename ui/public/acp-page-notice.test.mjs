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

function collectText(node) {
  if (!node) return '';
  const own = typeof node.textContent === 'string' ? node.textContent : '';
  const childText = Array.isArray(node.children) ? node.children.map((child) => collectText(child)).join(' ') : '';
  return `${own} ${childText}`.replace(/\s+/g, ' ').trim();
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

test('ACP page surfaces HTML tool failures as toasts without dumping raw markup', async () => {
  const toastMessages = [];
  const window = loadModules(({ conversation, onUpdate, onReadyChange }) => ({
    async start() {
      if (typeof onReadyChange === 'function') {
        onReadyChange(true);
      }
      setTimeout(() => {
        if (typeof onUpdate !== 'function') return;
        onUpdate({
          sessionUpdate: 'tool_call',
          toolCallId: 'tool-502',
          title: 'Fetch workspace',
          status: 'completed',
          rawInput: { url: 'https://staging.spritz.textcortex.com/' },
        });
        onUpdate({
          sessionUpdate: 'tool_call_update',
          toolCallId: 'tool-502',
          status: 'completed',
          rawOutput:
            '<!DOCTYPE html><html><head><title>textcortex.com | 502: Bad gateway</title></head><body>' +
            '<span class="code-label">Error code 502</span><span>staging.spritz.textcortex.com</span>' +
            '<span>Cloudflare</span><p>The web server reported a bad gateway error.</p></body></html>',
        });
      }, 0);
    },
    isReady: () => true,
    getConversationId: () => conversation?.metadata?.name || '',
    getSessionId: () => conversation?.spec?.sessionId || '',
    matchesConversation(targetConversation) {
      return (
        this.getConversationId() === (targetConversation?.metadata?.name || '') &&
        this.getSessionId() === (targetConversation?.spec?.sessionId || '')
      );
    },
    dispose() {},
    cancelPrompt() {},
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
    showNotice() {},
    clearNotice() {},
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

  assert.equal(toastMessages.length, 1);
  assert.match(toastMessages[0], /502/i);
  assert.match(toastMessages[0], /staging\.spritz\.textcortex\.com/i);
  assert.equal(toastMessages[0].includes('<!DOCTYPE html>'), false);
});

test('ACP page surfaces HTML assistant failures as toasts without restoring markup into chat', async () => {
  const toastMessages = [];
  const window = loadModules(({ onUpdate, conversation }) => ({
    start: async () => {
      onUpdate({
        sessionUpdate: 'agent_message_chunk',
        content: {
          type: 'text',
          text:
            '<!DOCTYPE html><html><head><title>textcortex.com | 502: Bad gateway</title></head><body>' +
            '<span class="code-label">Error code 502</span><span>staging.spritz.textcortex.com</span>' +
            '<span>Cloudflare</span><p>The web server reported a bad gateway error.</p></body></html>',
        },
      });
    },
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
    showNotice() {},
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

  assert.equal(toastMessages.length, 1);
  assert.match(toastMessages[0], /502/i);
  assert.equal(toastMessages[0].includes('<!DOCTYPE html>'), false);
  assert.doesNotMatch(collectText(shellEl), /<!DOCTYPE html>/);
});
