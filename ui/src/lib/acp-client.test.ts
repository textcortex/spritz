import { describe, it, expect, beforeEach, afterEach, vi } from 'vite-plus/test';
import { FakeWebSocket } from '@/test/helpers';
import { extractACPText, createACPClient } from './acp-client';
import type { ACPClientOptions, ConversationInfo } from '@/types/acp';

describe('extractACPText', () => {
  it('returns empty string for null', () => {
    expect(extractACPText(null)).toBe('');
  });

  it('returns empty string for undefined', () => {
    expect(extractACPText(undefined)).toBe('');
  });

  it('returns string directly', () => {
    expect(extractACPText('hello')).toBe('hello');
  });

  it('joins array items', () => {
    expect(extractACPText(['hello', ' world'])).toBe('hello\n world');
  });

  it('extracts text from object with text property', () => {
    expect(extractACPText({ text: 'hi' })).toBe('hi');
  });

  it('extracts text from content wrapper', () => {
    expect(extractACPText({ type: 'content', content: 'nested' })).toBe('nested');
  });

  it('extracts text from resource object', () => {
    expect(extractACPText({ resource: { text: 'resource text' } })).toBe('resource text');
  });

  it('extracts uri from resource when no text', () => {
    expect(extractACPText({ resource: { uri: 'file://foo.txt' } })).toBe('file://foo.txt');
  });

  it('handles nested arrays', () => {
    const input = [{ text: 'a' }, { text: 'b' }];
    expect(extractACPText(input)).toBe('a\nb');
  });
});

describe('createACPClient', () => {
  let lastWs: FakeWebSocket;

  beforeEach(() => {
    vi.stubGlobal('WebSocket', class extends FakeWebSocket {
      constructor(url: string) {
        super(url);
        lastWs = this;
      }
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  const CONVERSATION: ConversationInfo = {
    metadata: { name: 'conv-1' },
    spec: { sessionId: 'sess-1' },
  };

  function createTestClient(overrides: Partial<ACPClientOptions> = {}) {
    return createACPClient({
      wsUrl: 'ws://test/connect',
      conversation: CONVERSATION,
      ...overrides,
    });
  }

  it('calls onReadyChange(true) after socket opens', async () => {
    const onReadyChange = vi.fn();
    const client = createTestClient({ onReadyChange });

    const startPromise = client.start();
    lastWs.simulateOpen();
    await startPromise;

    expect(onReadyChange).toHaveBeenCalledWith(true);
    expect(client.isReady()).toBe(true);
  });

  it('does not send prompts before socket opens', () => {
    const client = createTestClient();
    client.start(); // Don't await — socket not open yet
    expect(() => client.sendPrompt('hello')).rejects.toThrow('not ready');
  });

  it('sendPrompt sends JSON-RPC with correct method/params', async () => {
    const client = createTestClient();
    const startPromise = client.start();
    lastWs.simulateOpen();
    await startPromise;

    // Don't await the prompt — it waits for response
    client.sendPrompt('hello world');

    expect(lastWs.sent).toHaveLength(1);
    const msg = JSON.parse(lastWs.sent[0]);
    expect(msg.jsonrpc).toBe('2.0');
    expect(msg.method).toBe('session/prompt');
    expect(msg.params.sessionId).toBe('sess-1');
    expect(msg.params.prompt).toEqual([{ type: 'text', text: 'hello world' }]);
  });

  it('matchesConversation validates conversation identity', () => {
    const client = createTestClient();
    expect(client.matchesConversation(CONVERSATION)).toBe(true);
    expect(client.matchesConversation({
      metadata: { name: 'other' },
      spec: { sessionId: 'sess-1' },
    })).toBe(false);
  });

  it('routes RPC responses to pending promises', async () => {
    const client = createTestClient();
    const startPromise = client.start();
    lastWs.simulateOpen();
    await startPromise;

    const promptPromise = client.sendPrompt('test');
    const msg = JSON.parse(lastWs.sent[0]);

    lastWs.simulateMessage({ jsonrpc: '2.0', id: msg.id, result: { ok: true } });
    const result = await promptPromise;
    expect(result).toEqual({ ok: true });
  });

  it('dispatches session/update to onUpdate', async () => {
    const onUpdate = vi.fn();
    const client = createTestClient({ onUpdate });
    const startPromise = client.start();
    lastWs.simulateOpen();
    await startPromise;

    lastWs.simulateMessage({
      jsonrpc: '2.0',
      method: 'session/update',
      params: { update: { sessionUpdate: 'heartbeat' } },
    });

    expect(onUpdate).toHaveBeenCalledWith({ sessionUpdate: 'heartbeat' });
  });

  it('dispatches session/request_permission to onPermissionRequest', async () => {
    const onPermissionRequest = vi.fn();
    const client = createTestClient({ onPermissionRequest });
    const startPromise = client.start();
    lastWs.simulateOpen();
    await startPromise;

    lastWs.simulateMessage({
      jsonrpc: '2.0',
      id: 99,
      method: 'session/request_permission',
      params: { tool: 'bash', command: 'rm -rf /' },
    });

    expect(onPermissionRequest).toHaveBeenCalledTimes(1);
    const entry = onPermissionRequest.mock.calls[0][0];
    expect(entry.params).toEqual({ tool: 'bash', command: 'rm -rf /' });
  });

  it('responds with -32601 for unsupported server requests', async () => {
    const client = createTestClient();
    const startPromise = client.start();
    lastWs.simulateOpen();
    await startPromise;

    lastWs.simulateMessage({
      jsonrpc: '2.0',
      id: 42,
      method: 'unknown/method',
    });

    // Should have sent an error response
    const responses = lastWs.sent.map((s) => JSON.parse(s));
    const errorResponse = responses.find((r) => r.error?.code === -32601);
    expect(errorResponse).toBeDefined();
    expect(errorResponse.id).toBe(42);
  });

  it('dispose closes socket and rejects pending requests', async () => {
    const client = createTestClient();
    const startPromise = client.start();
    lastWs.simulateOpen();
    await startPromise;

    const promptPromise = client.sendPrompt('test');
    client.dispose();

    await expect(promptPromise).rejects.toThrow();
    expect(client.isReady()).toBe(false);
  });

  it('getConversationId returns conversation name', () => {
    const client = createTestClient();
    expect(client.getConversationId()).toBe('conv-1');
  });

  it('getSessionId returns session id', () => {
    const client = createTestClient();
    expect(client.getSessionId()).toBe('sess-1');
  });
});
