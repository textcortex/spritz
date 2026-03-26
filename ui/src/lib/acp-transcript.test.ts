import { describe, it, expect } from 'vite-plus/test';
import {
  createTranscript,
  applySessionUpdate,
  finalizeStreaming,
  finalizeHistoricalThinking,
  getPreviewText,
  serializeTranscript,
  hydrateTranscript,
} from './acp-transcript';

describe('createTranscript', () => {
  it('returns empty transcript with correct defaults', () => {
    const t = createTranscript();
    expect(t.messages).toEqual([]);
    expect(t.toolCallIndex.size).toBe(0);
    expect(t.availableCommands).toEqual([]);
    expect(t.currentMode).toBe('');
    expect(t.usage).toBeNull();
    expect(t.thinkingChunks).toEqual([]);
    expect(t.thinkingActive).toBe(false);
  });
});

describe('applySessionUpdate', () => {
  it('turns HTML error message chunks into toasts instead of transcript text', () => {
    const t = createTranscript();
    const result = applySessionUpdate(t, {
      sessionUpdate: 'agent_message_chunk',
      content: `<!DOCTYPE html>
<html lang="en-US">
  <head><title>example.com | 525: SSL handshake failed</title></head>
  <body><div>Error code 525</div></body>
</html>`,
    });

    expect(result).toEqual({
      toast: {
        type: 'error',
        message: 'HTTP 525 · example.com | 525: SSL handshake failed · example.com',
      },
    });
    expect(t.messages).toHaveLength(0);
  });

  it('stores available_commands_update without adding messages', () => {
    const t = createTranscript();
    applySessionUpdate(t, {
      sessionUpdate: 'available_commands_update',
      availableCommands: ['cmd1', 'cmd2'],
    });
    expect(t.availableCommands).toEqual(['cmd1', 'cmd2']);
    expect(t.messages).toHaveLength(0);
  });

  it('appends streaming assistant text for agent_message_chunk', () => {
    const t = createTranscript();
    applySessionUpdate(t, { sessionUpdate: 'agent_message_chunk', content: 'Hello' });
    applySessionUpdate(t, { sessionUpdate: 'agent_message_chunk', content: ' world' });
    expect(t.messages).toHaveLength(1);
    expect(t.messages[0].role).toBe('assistant');
    expect(t.messages[0].blocks[0].text).toBe('Hello world');
    expect(t.messages[0].streaming).toBe(true);
  });

  it('appends streaming user text for user_message_chunk', () => {
    const t = createTranscript();
    applySessionUpdate(t, { sessionUpdate: 'user_message_chunk', content: 'Hi there' });
    expect(t.messages).toHaveLength(1);
    expect(t.messages[0].role).toBe('user');
    expect(t.messages[0].blocks[0].text).toBe('Hi there');
  });

  it('filters command-like XML tags from message chunks', () => {
    const t = createTranscript();
    applySessionUpdate(t, { sessionUpdate: 'agent_message_chunk', content: '<command-name>foo</command-name>' });
    expect(t.messages).toHaveLength(0);
  });

  it('accumulates agent_thought_chunk', () => {
    const t = createTranscript();
    applySessionUpdate(t, { sessionUpdate: 'agent_thought_chunk', content: 'thinking...' });
    applySessionUpdate(t, { sessionUpdate: 'agent_thought_chunk', content: ' more thoughts' });
    expect(t.thinkingChunks).toHaveLength(1);
    expect(t.thinkingChunks[0].text).toBe('thinking... more thoughts');
    expect(t.thinkingChunks[0].kind).toBe('thought');
  });

  it('creates tool entry in thinkingChunks for tool_call', () => {
    const t = createTranscript();
    applySessionUpdate(t, {
      sessionUpdate: 'tool_call',
      toolCallId: 'tc-1',
      title: 'Read file',
      status: 'pending',
      rawInput: { path: '/foo' },
    });
    expect(t.messages).toHaveLength(0);
    const chunk = t.thinkingChunks.find((c) => c._toolCallId === 'tc-1');
    expect(chunk).toBeDefined();
    expect(chunk!.kind).toBe('tool');
    expect(chunk!.toolName).toBe('Read file');
    expect(chunk!.status).toBe('pending');
  });

  it('updates existing tool entry in thinkingChunks for tool_call_update', () => {
    const t = createTranscript();
    applySessionUpdate(t, {
      sessionUpdate: 'tool_call',
      toolCallId: 'tc-1',
      title: 'Read file',
      status: 'pending',
    });
    applySessionUpdate(t, {
      sessionUpdate: 'tool_call_update',
      toolCallId: 'tc-1',
      title: 'Read file',
      status: 'complete',
      rawOutput: 'file contents',
    });
    const chunk = t.thinkingChunks.find((c) => c._toolCallId === 'tc-1');
    expect(chunk).toBeDefined();
    expect(chunk!.status).toBe('complete');
    expect(chunk!.result).toBe('file contents');
  });

  it('sets usage for usage_update', () => {
    const t = createTranscript();
    applySessionUpdate(t, {
      sessionUpdate: 'usage_update',
      label: 'Tokens',
      used: 100,
      size: 1000,
    });
    expect(t.usage).toEqual({ label: 'Tokens', used: 100, size: 1000 });
  });

  it('sets currentMode for current_mode_update', () => {
    const t = createTranscript();
    applySessionUpdate(t, { sessionUpdate: 'current_mode_update', mode: 'code' });
    expect(t.currentMode).toBe('code');
  });

  it('returns conversationTitle for session_info_update', () => {
    const t = createTranscript();
    const result = applySessionUpdate(t, {
      sessionUpdate: 'session_info_update',
      title: 'My Chat',
    });
    expect(result).toEqual({ conversationTitle: 'My Chat' });
  });

  it('silently ignores heartbeat/ping/pong/ack', () => {
    const t = createTranscript();
    for (const type of ['heartbeat', 'ping', 'pong', 'ack']) {
      applySessionUpdate(t, { sessionUpdate: type });
    }
    expect(t.messages).toHaveLength(0);
  });

  it('renders unknown types as system messages', () => {
    const t = createTranscript();
    applySessionUpdate(t, { sessionUpdate: 'some_new_type', data: 'test' });
    expect(t.messages).toHaveLength(1);
    expect(t.messages[0].role).toBe('system');
    expect(t.messages[0].title).toBe('Some New Type');
  });

  it('appends historical text with message key coalescing', () => {
    const t = createTranscript();
    applySessionUpdate(t, {
      sessionUpdate: 'agent_message_chunk',
      content: 'Part 1',
      historyMessageId: 'msg-1',
    }, { historical: true });
    applySessionUpdate(t, {
      sessionUpdate: 'agent_message_chunk',
      content: ' Part 2',
      historyMessageId: 'msg-1',
    }, { historical: true });
    expect(t.messages).toHaveLength(1);
    expect(t.messages[0].blocks[0].text).toBe('Part 1 Part 2');
    expect(t.messages[0].streaming).toBe(false);
  });

  it('ignores repeated historical replay for the same message ids', () => {
    const t = createTranscript();
    const firstReplay = [
      {
        sessionUpdate: 'user_message_chunk',
        content: 'who is this',
        historyMessageId: 'msg-user-1',
      },
      {
        sessionUpdate: 'agent_message_chunk',
        content: "I'm Zeno.",
        historyMessageId: 'msg-assistant-1',
      },
      {
        sessionUpdate: 'agent_message_chunk',
        content: 'How can I help?',
        historyMessageId: 'msg-assistant-2',
      },
    ] as const;

    for (const update of firstReplay) {
      applySessionUpdate(t, update, { historical: true });
    }
    for (const update of firstReplay) {
      applySessionUpdate(t, update, { historical: true });
    }

    expect(t.messages).toHaveLength(3);
    expect(t.messages.map((message) => message.blocks[0].text)).toEqual([
      'who is this',
      "I'm Zeno.",
      'How can I help?',
    ]);
  });

  it('upgrades matching live messages when historical replay catches up', () => {
    const t = createTranscript();

    applySessionUpdate(t, { sessionUpdate: 'user_message_chunk', content: 'who is this' });
    applySessionUpdate(t, { sessionUpdate: 'agent_message_chunk', content: "I'm Zeno." });
    finalizeStreaming(t);

    applySessionUpdate(t, {
      sessionUpdate: 'user_message_chunk',
      content: 'who is this',
      historyMessageId: 'msg-user-1',
    }, { historical: true });
    applySessionUpdate(t, {
      sessionUpdate: 'agent_message_chunk',
      content: "I'm ",
      historyMessageId: 'msg-assistant-1',
    }, { historical: true });
    applySessionUpdate(t, {
      sessionUpdate: 'agent_message_chunk',
      content: 'Zeno.',
      historyMessageId: 'msg-assistant-1',
    }, { historical: true });

    expect(t.messages).toHaveLength(2);
    expect(t.messages.map((message) => message.blocks[0].text)).toEqual([
      'who is this',
      "I'm Zeno.",
    ]);
    expect(t.messages.map((message) => message._historyMessageId)).toEqual([
      'msg-user-1',
      'msg-assistant-1',
    ]);
  });

  it('adds plan messages', () => {
    const t = createTranscript();
    applySessionUpdate(t, {
      sessionUpdate: 'plan',
      entries: [{ text: 'Step 1', done: false }, { text: 'Step 2', done: true }],
    });
    expect(t.messages).toHaveLength(1);
    expect(t.messages[0].role).toBe('plan');
    expect(t.messages[0].blocks[0].type).toBe('plan');
  });

  it('tracks web tool calls in thinking chunks', () => {
    const t = createTranscript();
    applySessionUpdate(t, {
      sessionUpdate: 'tool_call',
      toolCallId: 'tc-ws',
      title: 'Search',
      _meta: { claudeCode: { toolName: 'WebSearch' } },
    });
    const webChunk = t.thinkingChunks.find((c) => c.kind === 'tool');
    expect(webChunk).toBeDefined();
    expect(webChunk!.toolKind).toBe('search');
  });
});

describe('finalizeStreaming', () => {
  it('marks all streaming messages as non-streaming', () => {
    const t = createTranscript();
    applySessionUpdate(t, { sessionUpdate: 'agent_message_chunk', content: 'Hello' });
    applySessionUpdate(t, { sessionUpdate: 'user_message_chunk', content: 'Hi' });
    expect(t.messages[0].streaming).toBe(true);
    finalizeStreaming(t);
    expect(t.messages.every((m) => !m.streaming)).toBe(true);
  });

  it('inserts thinking_done when web tools were used', () => {
    const t = createTranscript();
    applySessionUpdate(t, {
      sessionUpdate: 'tool_call',
      toolCallId: 'tc-1',
      title: 'Fetching',
      _meta: { claudeCode: { toolName: 'WebFetch' } },
    });
    finalizeStreaming(t);
    const thinkingDone = t.messages.find((m) => m.role === 'thinking_done');
    expect(thinkingDone).toBeDefined();
  });

  it('inserts thinking_done for non-web tools too', () => {
    const t = createTranscript();
    applySessionUpdate(t, {
      sessionUpdate: 'tool_call',
      toolCallId: 'tc-1',
      title: 'Read',
    });
    finalizeStreaming(t);
    const thinkingDone = t.messages.find((m) => m.role === 'thinking_done');
    expect(thinkingDone).toBeDefined();
    expect(thinkingDone!._thinkingChunks).toHaveLength(1);
  });

  it('ignores repeated historical replay for the same tool call ids', () => {
    const t = createTranscript();
    const replay = [
      {
        sessionUpdate: 'tool_call',
        toolCallId: 'tool-1',
        title: 'read',
        status: 'pending',
      },
      {
        sessionUpdate: 'tool_call_update',
        toolCallId: 'tool-1',
        title: 'read',
        status: 'completed',
        rawOutput: 'contents',
      },
      {
        sessionUpdate: 'tool_call',
        toolCallId: 'tool-2',
        title: 'write',
        status: 'pending',
      },
      {
        sessionUpdate: 'tool_call_update',
        toolCallId: 'tool-2',
        title: 'write',
        status: 'completed',
        rawOutput: 'ok',
      },
    ] as const;

    for (const update of replay) {
      applySessionUpdate(t, update, { historical: true });
    }
    finalizeStreaming(t);

    for (const update of replay) {
      applySessionUpdate(t, update, { historical: true });
    }
    finalizeHistoricalThinking(t);

    const thinkingDoneMessages = t.messages.filter((message) => message.role === 'thinking_done');
    expect(thinkingDoneMessages).toHaveLength(1);
    expect(thinkingDoneMessages[0]._thinkingChunks?.map((chunk) => chunk._toolCallId)).toEqual([
      'tool-1',
      'tool-2',
    ]);
  });
});

describe('getPreviewText', () => {
  it('returns last user/assistant text', () => {
    const t = createTranscript();
    applySessionUpdate(t, { sessionUpdate: 'user_message_chunk', content: 'Hello world' });
    expect(getPreviewText(t)).toBe('Hello world');
  });

  it('truncates to 120 chars with ellipsis', () => {
    const t = createTranscript();
    const longText = 'a'.repeat(200);
    applySessionUpdate(t, { sessionUpdate: 'agent_message_chunk', content: longText });
    const preview = getPreviewText(t);
    expect(preview.length).toBe(120);
    expect(preview.endsWith('…')).toBe(true);
  });

  it('returns empty string for empty transcript', () => {
    const t = createTranscript();
    expect(getPreviewText(t)).toBe('');
  });
});

describe('serializeTranscript / hydrateTranscript', () => {
  it('strips _renderedLength from blocks', () => {
    const t = createTranscript();
    applySessionUpdate(t, { sessionUpdate: 'agent_message_chunk', content: 'Hello' });
    t.messages[0].blocks[0]._renderedLength = 5;
    const serialized = serializeTranscript(t);
    const messages = serialized.messages as Array<{ blocks: Array<{ _renderedLength?: number }> }>;
    expect(messages[0].blocks[0]._renderedLength).toBeUndefined();
  });

  it('round-trips through serialize and hydrate', () => {
    const t = createTranscript();
    applySessionUpdate(t, { sessionUpdate: 'agent_message_chunk', content: 'Hello' });
    applySessionUpdate(t, { sessionUpdate: 'available_commands_update', availableCommands: ['cmd'] });
    applySessionUpdate(t, { sessionUpdate: 'current_mode_update', mode: 'code' });

    const serialized = serializeTranscript(t);
    const restored = hydrateTranscript(serialized);

    expect(restored.messages).toHaveLength(t.messages.length);
    expect(restored.availableCommands).toEqual(['cmd']);
    expect(restored.currentMode).toBe('code');
  });
});
