import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
import { uiDistPath } from '../test-paths.mjs';

function createClassList() {
  const classes = new Set();
  return {
    add(c) { classes.add(c); },
    remove(c) { classes.delete(c); },
    contains(c) { return classes.has(c); },
    toggle(c) { if (classes.has(c)) { classes.delete(c); return false; } classes.add(c); return true; },
  };
}

function createElement(tagName) {
  return {
    tagName,
    children: [],
    className: '',
    dataset: {},
    textContent: '',
    innerHTML: '',
    open: false,
    classList: createClassList(),
    append(...items) {
      this.children.push(...items);
    },
    appendChild(item) {
      this.children.push(item);
      return item;
    },
    addEventListener() {},
    setAttribute(name, value) {
      this[name] = value;
    },
  };
}

function collectText(node) {
  if (!node) return '';
  const own = typeof node.textContent === 'string' ? node.textContent : '';
  const html = typeof node.innerHTML === 'string' ? node.innerHTML.replace(/<[^>]*>/g, ' ') : '';
  const childText = Array.isArray(node.children) ? node.children.map((child) => collectText(child)).join(' ') : '';
  return `${own} ${html} ${childText}`.replace(/\s+/g, ' ').trim();
}

function loadRenderModule() {
  const document = {
    createElement,
    createDocumentFragment() { return createElement('fragment'); },
    createTextNode(text) { return { textContent: text }; },
  };
  const window = {
    document,
    SpritzACPClient: {
      extractACPText(value) {
        if (value == null) return '';
        if (typeof value === 'string') return value;
        if (Array.isArray(value)) return value.map((item) => this.extractACPText(item)).join('\n');
        if (typeof value.text === 'string') return value.text;
        if (value.content) return this.extractACPText(value.content);
        if (value.resource?.text) return value.resource.text;
        return '';
      },
    },
  };
  window.window = window;
  const context = vm.createContext({ window, document, console });
  context.globalThis = context.window;
  const script = fs.readFileSync(uiDistPath('acp-render.js'), 'utf8');
  vm.runInContext(script, context, { filename: 'acp-render.js' });
  return window.SpritzACPRender;
}

test('ACP render adapter keeps commands out of transcript and upserts tool cards', () => {
  const ACPRender = loadRenderModule();
  const transcript = ACPRender.createTranscript();

  ACPRender.applySessionUpdate(transcript, {
    sessionUpdate: 'available_commands_update',
    availableCommands: [
      { name: 'help', description: 'Show help' },
      { name: 'bash', description: 'Run bash' },
    ],
  });

  assert.equal(transcript.messages.length, 0);
  assert.deepEqual(
    transcript.availableCommands.map((item) => item.name),
    ['help', 'bash'],
  );

  ACPRender.applySessionUpdate(transcript, {
    sessionUpdate: 'tool_call',
    toolCallId: 'tool-1',
    title: 'Search workspace',
    status: 'in_progress',
    rawInput: { query: 'acp' },
  });

  ACPRender.applySessionUpdate(transcript, {
    sessionUpdate: 'tool_call_update',
    toolCallId: 'tool-1',
    status: 'completed',
    rawOutput: { result: 'done' },
  });

  // Tool calls now go through thinkingChunks instead of creating card messages
  assert.equal(transcript.messages.length, 0);
  assert.equal(transcript.thinkingChunks.length, 1);
  assert.equal(transcript.thinkingChunks[0].kind, 'tool');
  assert.equal(transcript.thinkingChunks[0].text, 'Search workspace');
});

test('ACP render adapter summarizes HTML error pages in tool results', () => {
  const ACPRender = loadRenderModule();
  const transcript = ACPRender.createTranscript();

  ACPRender.applySessionUpdate(transcript, {
    sessionUpdate: 'tool_call',
    toolCallId: 'tool-502',
    title: 'Fetch workspace',
    status: 'completed',
    rawInput: { url: 'https://staging.spritz.textcortex.com/' },
  });

  ACPRender.applySessionUpdate(transcript, {
    sessionUpdate: 'tool_call_update',
    toolCallId: 'tool-502',
    status: 'completed',
    rawOutput:
      '<!DOCTYPE html><html><head><title>textcortex.com | 502: Bad gateway</title></head><body>' +
      '<span class="code-label">Error code 502</span><span>staging.spritz.textcortex.com</span>' +
      '<span>Cloudflare</span><p>The web server reported a bad gateway error.</p></body></html>',
  });

  // Tool calls now go through thinkingChunks instead of creating card messages
  assert.equal(transcript.messages.length, 0);
  assert.equal(transcript.thinkingChunks.length, 1);
  assert.equal(transcript.thinkingChunks[0].kind, 'tool');
  assert.equal(transcript.thinkingChunks[0].text, 'Fetch workspace');
});

test('ACP render adapter drops HTML error pages from assistant text updates', () => {
  const ACPRender = loadRenderModule();
  const transcript = ACPRender.createTranscript();

  const result = ACPRender.applySessionUpdate(transcript, {
    sessionUpdate: 'agent_message_chunk',
    content: {
      type: 'text',
      text:
        '<!DOCTYPE html><html><head><title>textcortex.com | 502: Bad gateway</title></head><body>' +
        '<span class="code-label">Error code 502</span><span>staging.spritz.textcortex.com</span>' +
        '<span>Cloudflare</span></body></html>',
    },
  });

  assert.equal(transcript.messages.length, 0);
  assert.equal(result?.toast?.type, 'error');
  assert.match(result?.toast?.message || '', /502/i);
  assert.equal((result?.toast?.message || '').includes('<!DOCTYPE html>'), false);
});

test('ACP render adapter sanitizes raw HTML error pages at render time', () => {
  const ACPRender = loadRenderModule();

  const node = ACPRender.renderMessage({
    type: 'assistant',
    blocks: [
      {
        type: 'text',
        text:
          '<!DOCTYPE html><html><head><title>textcortex.com | 502: Bad gateway</title></head><body>' +
          '<span class="code-label">Error code 502</span><span>staging.spritz.textcortex.com</span>' +
          '<span>Cloudflare</span></body></html>',
      },
    ],
  });

  const text = collectText(node);
  assert.match(text, /502/i);
  assert.match(text, /staging\.spritz\.textcortex\.com/i);
  assert.equal(text.includes('<!DOCTYPE html>'), false);
});

test('ACP render adapter treats bootstrap replay chunks as historical messages', () => {
  const ACPRender = loadRenderModule();
  const transcript = ACPRender.createTranscript();

  ACPRender.applySessionUpdate(
    transcript,
    {
      sessionUpdate: 'user_message_chunk',
      historyMessageId: 'history-0',
      content: { type: 'text', text: 'Earlier user message' },
    },
    { historical: true },
  );
  ACPRender.applySessionUpdate(
    transcript,
    {
      sessionUpdate: 'agent_message_chunk',
      historyMessageId: 'history-1',
      content: { type: 'text', text: 'Earlier assistant message' },
    },
    { historical: true },
  );

  assert.equal(transcript.messages.length, 2);
  assert.equal(transcript.messages[0].type, 'user');
  assert.equal(transcript.messages[0].streaming, false);
  assert.equal(transcript.messages[0].blocks[0].text, 'Earlier user message');
  assert.equal(transcript.messages[1].type, 'assistant');
  assert.equal(transcript.messages[1].streaming, false);
  assert.equal(transcript.messages[1].blocks[0].text, 'Earlier assistant message');
});

test('ACP render adapter coalesces bootstrap replay chunks for the same historical message', () => {
  const ACPRender = loadRenderModule();
  const transcript = ACPRender.createTranscript();

  ACPRender.applySessionUpdate(
    transcript,
    {
      sessionUpdate: 'agent_message_chunk',
      historyMessageId: 'history-1',
      content: { type: 'text', text: 'Earlier assistant ' },
    },
    { historical: true },
  );
  ACPRender.applySessionUpdate(
    transcript,
    {
      sessionUpdate: 'agent_message_chunk',
      historyMessageId: 'history-1',
      content: { type: 'text', text: 'message' },
    },
    { historical: true },
  );

  assert.equal(transcript.messages.length, 1);
  assert.equal(transcript.messages[0].blocks[0].text, 'Earlier assistant message');
});

test('ACP render adapter hydrates legacy cached messages that used kind', () => {
  const ACPRender = loadRenderModule();
  const transcript = ACPRender.hydrateTranscript({
    messages: [
      {
        id: 'legacy-user',
        kind: 'user',
        blocks: [{ type: 'text', text: 'Legacy user message' }],
        toolCallId: '',
      },
      {
        id: 'legacy-tool',
        kind: 'tool',
        blocks: [{ type: 'details', title: 'Result', text: 'done', open: true }],
        toolCallId: 'tool-legacy',
      },
    ],
  });

  assert.equal(transcript.messages.length, 2);
  assert.equal(transcript.messages[0].type, 'user');
  assert.equal(transcript.messages[1].type, 'tool');
  assert.equal(transcript.toolCallIndex.get('tool-legacy'), 1);
});
