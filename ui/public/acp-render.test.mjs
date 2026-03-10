import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';

function createElement(tagName) {
  return {
    tagName,
    children: [],
    className: '',
    dataset: {},
    textContent: '',
    open: false,
    append(...items) {
      this.children.push(...items);
    },
    appendChild(item) {
      this.children.push(item);
    },
    addEventListener() {},
    setAttribute(name, value) {
      this[name] = value;
    },
  };
}

function loadRenderModule() {
  const document = { createElement };
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
  const script = fs.readFileSync('/Users/onur/repos/spritz/ui/public/acp-render.js', 'utf8');
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

  assert.equal(transcript.messages.length, 1);
  const toolCard = transcript.messages[0];
  assert.equal(toolCard.kind, 'tool');
  assert.equal(toolCard.title, 'Search workspace');
  assert.equal(toolCard.status, 'completed');
  assert.equal(toolCard.blocks.some((block) => block.type === 'details' && block.title === 'Input'), true);
  assert.equal(toolCard.blocks.some((block) => block.type === 'details' && block.title === 'Result'), true);
});
