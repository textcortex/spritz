import test from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';

class FakeElement {
  constructor(tagName, ownerDocument) {
    this.tagName = String(tagName || '').toLowerCase();
    this.ownerDocument = ownerDocument;
    this.children = [];
    this.parentNode = null;
    this.attributes = new Map();
    this.listeners = new Map();
    this.className = '';
    this.hidden = false;
    this.disabled = false;
    this.value = '';
    this._textContent = '';
    this._id = '';
  }

  set id(value) {
    this._id = String(value || '');
    if (this._id) {
      this.ownerDocument.byId.set(this._id, this);
    }
  }

  get id() {
    return this._id;
  }

  append(...items) {
    for (const item of items) {
      if (item === null || item === undefined) continue;
      if (item instanceof FakeElement) {
        item.parentNode = this;
        this.children.push(item);
        continue;
      }
      const textNode = new FakeElement('#text', this.ownerDocument);
      textNode.textContent = String(item);
      textNode.parentNode = this;
      this.children.push(textNode);
    }
  }

  prepend(...items) {
    const prepared = [];
    for (const item of items) {
      if (item === null || item === undefined) continue;
      if (item instanceof FakeElement) {
        item.parentNode = this;
        prepared.push(item);
        continue;
      }
      const textNode = new FakeElement('#text', this.ownerDocument);
      textNode.textContent = String(item);
      textNode.parentNode = this;
      prepared.push(textNode);
    }
    this.children = [...prepared, ...this.children];
  }

  addEventListener(type, handler) {
    this.listeners.set(type, handler);
  }

  dispatchEvent(event) {
    const handler = this.listeners.get(event.type);
    if (handler) {
      handler.call(this, event);
    }
  }

  querySelector(selector) {
    return this.ownerDocument.querySelectorWithin(this, selector);
  }

  set textContent(value) {
    this._textContent = String(value || '');
    this.children = [];
  }

  get textContent() {
    if (this.children.length) {
      return this.children.map((child) => child.textContent).join('');
    }
    return this._textContent;
  }
}

class FakeDocument {
  constructor() {
    this.byId = new Map();
  }

  createElement(tagName) {
    return new FakeElement(tagName, this);
  }

  getElementById(id) {
    return this.byId.get(id) || null;
  }

  querySelectorWithin(root, selector) {
    const match = (node) => {
      if (!(node instanceof FakeElement)) return false;
      if (selector === '.preset-help') {
        return node.className.split(/\s+/).includes('preset-help');
      }
      if (selector === '#preset-select') {
        return node.id === 'preset-select';
      }
      const inputNameMatch = selector.match(/^(input|textarea)\[name="([^"]+)"\]$/);
      if (inputNameMatch) {
        const [, tagName, name] = inputNameMatch;
        return node.tagName === tagName && node.attributes.get('name') === name;
      }
      return false;
    };

    const stack = [...root.children];
    while (stack.length) {
      const node = stack.shift();
      if (match(node)) return node;
      stack.unshift(...node.children);
    }
    return null;
  }
}

function namedField(document, tagName, name) {
  const element = document.createElement(tagName);
  element.attributes.set('name', name);
  return element;
}

function buildFormFixture() {
  const document = new FakeDocument();
  const form = document.createElement('form');
  const imageInput = namedField(document, 'input', 'image');
  const repoInput = namedField(document, 'input', 'repo');
  const branchInput = namedField(document, 'input', 'branch');
  const ttlInput = namedField(document, 'input', 'ttl');
  form.append(imageInput, repoInput, branchInput, ttlInput);
  return { document, form, imageInput, repoInput, branchInput, ttlInput };
}

test('setupPresetPanel injects the preset selector and updates image fields', async () => {
  const require = createRequire(import.meta.url);
  const { setupPresetPanel } = require('./preset-panel.js');

  const { document, form, imageInput, repoInput, branchInput, ttlInput } = buildFormFixture();
  let activePreset = null;
  let repoDefaultsApplied = 0;
  const presets = [
    {
      name: 'Starter Devbox',
      image: 'spritz-starter:latest',
      description: 'Starter image',
      repoUrl: 'https://example.com/a.git',
      branch: 'main',
      ttl: '8h',
      env: [{ name: 'FOO', value: 'bar' }],
    },
    {
      name: 'OpenClaw Devbox',
      image: 'spritz-openclaw:latest',
      description: 'OpenClaw image',
      repoUrl: 'https://example.com/b.git',
      branch: 'staging',
      ttl: '12h',
      env: [{ name: 'BAZ', value: 'qux' }],
    },
  ];

  const controller = setupPresetPanel({
    document,
    form,
    presets,
    hideRepoInputs: false,
    applyRepoDefaults() {
      repoDefaultsApplied += 1;
    },
    setActivePreset(preset) {
      activePreset = preset;
    },
  });

  assert.ok(controller, 'expected a preset controller');
  assert.equal(repoDefaultsApplied, 1);

  const presetSelect = document.getElementById('preset-select');
  assert.ok(presetSelect, 'expected preset select to be injected');
  assert.equal(imageInput.value, 'spritz-starter:latest');
  assert.equal(repoInput.value, 'https://example.com/a.git');
  assert.equal(branchInput.value, 'main');
  assert.equal(ttlInput.value, '8h');
  assert.equal(activePreset?.name, 'Starter Devbox');
  assert.equal(form.querySelector('.preset-help').textContent, 'Starter image');

  presetSelect.value = '1';
  presetSelect.dispatchEvent({ type: 'change' });

  assert.equal(imageInput.value, 'spritz-openclaw:latest');
  assert.equal(repoInput.value, 'https://example.com/b.git');
  assert.equal(branchInput.value, 'staging');
  assert.equal(ttlInput.value, '12h');
  assert.equal(activePreset?.name, 'OpenClaw Devbox');
  assert.equal(form.querySelector('.preset-help').textContent, 'OpenClaw image');

  controller.reset();
  assert.equal(presetSelect.value, '');
  assert.equal(form.querySelector('.preset-help').textContent, '');
  assert.equal(activePreset, null);
});

test('setupPresetPanel restores a saved preset selection and falls back to custom', async () => {
  const require = createRequire(import.meta.url);
  const { setupPresetPanel } = require('./preset-panel.js');

  const { document, form, imageInput, repoInput, branchInput, ttlInput } = buildFormFixture();
  let activePreset = null;
  const presets = [
    {
      name: 'Starter Devbox',
      image: 'spritz-starter:latest',
      description: 'Starter image',
      repoUrl: 'https://example.com/a.git',
      branch: 'main',
      ttl: '8h',
    },
    {
      name: 'OpenClaw Devbox',
      image: 'spritz-openclaw:latest',
      description: 'OpenClaw image',
      repoUrl: 'https://example.com/b.git',
      branch: 'staging',
      ttl: '12h',
    },
  ];

  const controller = setupPresetPanel({
    document,
    form,
    presets,
    hideRepoInputs: false,
    applyRepoDefaults() {},
    setActivePreset(preset) {
      activePreset = preset;
    },
  });

  assert.equal(
    controller.restoreSelection({ mode: 'preset', presetName: 'OpenClaw Devbox', presetImage: 'spritz-openclaw:latest' }),
    true,
  );
  assert.equal(document.getElementById('preset-select').value, '1');
  assert.equal(imageInput.value, 'spritz-openclaw:latest');
  assert.equal(repoInput.value, 'https://example.com/b.git');
  assert.equal(branchInput.value, 'staging');
  assert.equal(ttlInput.value, '12h');
  assert.equal(activePreset?.name, 'OpenClaw Devbox');

  assert.equal(controller.restoreSelection({ mode: 'custom' }), true);
  assert.equal(document.getElementById('preset-select').value, '');
  assert.equal(activePreset, null);
});

test('setupPresetPanel clears hidden repo defaults when a preset explicitly owns blank repo fields', async () => {
  const require = createRequire(import.meta.url);
  const { setupPresetPanel } = require('./preset-panel.js');

  const { document, form, imageInput, repoInput, branchInput } = buildFormFixture();
  repoInput.value = 'https://github.com/example/private.git';
  branchInput.value = 'staging';
  const presets = [
    {
      name: 'OpenClaw',
      image: 'spritz-openclaw:latest',
      description: 'OpenClaw image',
      repoUrl: '',
      branch: '',
    },
  ];

  setupPresetPanel({
    document,
    form,
    presets,
    hideRepoInputs: true,
    applyRepoDefaults() {},
    setActivePreset() {},
  });

  assert.equal(imageInput.value, 'spritz-openclaw:latest');
  assert.equal(repoInput.value, '');
  assert.equal(branchInput.value, '');
});
