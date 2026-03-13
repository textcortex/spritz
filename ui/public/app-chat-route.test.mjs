import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
import { uiPublicPath } from '../test-paths.mjs';

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

test('chat hash route initializes without throwing', () => {
  const notice = createElement('div');
  const list = createElement('div');
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

  let renderedName = '';
  let renderCallCount = 0;

  const document = {
    body: createElement('body'),
    head: createElement('head'),
    getElementById(id) {
      return {
        notice,
        list,
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
      chatNameFromHash(hash = '') {
        return hash.startsWith('#chat/') ? decodeURIComponent(hash.slice('#chat/'.length)) : '';
      },
      conversationIdFromHash() {
        return '';
      },
      renderACPPage(name) {
        renderedName = name;
        renderCallCount += 1;
        return {
          destroy() {},
        };
      },
    },
    SpritzPresetPanel: {
      setupPresetPanel() {
        return { reset() {} };
      },
    },
    location: {
      hash: '#chat/young-crest',
      pathname: '/',
      search: '',
      origin: 'http://example.test',
      href: 'http://example.test/#chat/young-crest',
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
    fetch: async () => ({ ok: true, status: 200, text: async () => '{}', statusText: 'OK' }),
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

  const script = fs.readFileSync(uiPublicPath('app.js'), 'utf8');
  assert.doesNotThrow(() => {
    vm.runInContext(script, context, { filename: 'app.js' });
  });
  assert.equal(renderCallCount, 1);
  assert.equal(renderedName, 'young-crest');
});
