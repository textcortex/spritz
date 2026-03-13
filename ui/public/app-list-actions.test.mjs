import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
import { uiDistPath } from '../test-paths.mjs';

function createStorage() {
  const values = new Map();
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
  return {
    tagName,
    hidden: false,
    textContent: '',
    innerHTML: '',
    disabled: false,
    value: '',
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
    querySelector(selector) {
      if (selector === 'h1') return this._title || null;
      if (selector === 'p') return this._subtitle || null;
      return null;
    },
    closest() {
      return this._closest || null;
    },
  };
}

function collectText(node) {
  if (!node) return '';
  const own = typeof node.textContent === 'string' ? node.textContent : '';
  const childText = Array.isArray(node.children) ? node.children.map((child) => collectText(child)).join(' ') : '';
  return `${own} ${childText}`.replace(/\s+/g, ' ').trim();
}

test('spritz list shows a transitional chat action while workspace chat is still starting', async () => {
  const notice = createElement('div');
  const list = createElement('div');
  const refresh = createElement('button');
  const createForm = createElement('form');
  const randomButton = createElement('button');
  const createSection = createElement('section');
  const listSection = createElement('section');
  createForm._closest = createSection;
  list._closest = listSection;

  const title = createElement('h1');
  const subtitle = createElement('p');
  const header = createElement('header');
  header._title = title;
  header._subtitle = subtitle;
  const shell = createElement('main');
  shell.querySelector = (selector) => (selector === 'header' ? header : null);
  shell.append = (...items) => {
    shell.children.push(...items);
  };

  const document = {
    body: createElement('body'),
    head: createElement('head'),
    getElementById(id) {
      return {
        notice,
        list,
        refresh,
        'create-form': createForm,
        'name-random': randomButton,
      }[id] || null;
    },
    querySelector(selector) {
      if (selector === '.shell') return shell;
      return null;
    },
    createElement,
  };

  const window = {
    SPRITZ_CONFIG: { apiBaseUrl: '', auth: {}, repoDefaults: {}, launch: {} },
    SpritzACPPage: {
      chatPagePath(name = '') {
        return `#chat/${name}`;
      },
      chatNameFromHash() {
        return '';
      },
      conversationIdFromHash() {
        return '';
      },
      renderACPPage() {
        return { destroy() {} };
      },
    },
    SpritzPresetPanel: {
      setupPresetPanel() {
        return { reset() {} };
      },
    },
    location: {
      hash: '',
      pathname: '/',
      search: '',
      origin: 'http://example.test',
      href: 'http://example.test/',
      assign() {},
    },
    localStorage: createStorage(),
    sessionStorage: createStorage(),
    navigator: {},
    addEventListener() {},
    removeEventListener() {},
    open() {},
    setTimeout,
    clearTimeout,
    fetch: async () => ({
      ok: true,
      status: 200,
      text: async () =>
        JSON.stringify({
          items: [
            {
              metadata: { name: 'young-lagoon', namespace: 'spritz-test' },
              spec: { image: 'spritz-claude-code:latest' },
              status: {
                phase: 'Provisioning',
                message: 'waiting for deployment',
                url: 'https://example.test/w/young-lagoon/',
              },
            },
          ],
        }),
      statusText: 'OK',
    }),
    Headers,
    URL,
    console,
  };
  window.window = window;
  window.document = document;

  const context = vm.createContext({
    window,
    document,
    console,
    Headers,
    URL,
    navigator: window.navigator,
    fetch: window.fetch,
    setTimeout,
    clearTimeout,
  });
  context.globalThis = context.window;

  const script = fs.readFileSync(uiDistPath('app.js'), 'utf8');
  vm.runInContext(script, context, { filename: 'app.js' });

  await new Promise((resolve) => setTimeout(resolve, 0));
  await new Promise((resolve) => setTimeout(resolve, 0));

  assert.match(collectText(list), /young-lagoon/i);
  assert.match(collectText(list), /starting…|starting/i);
});
