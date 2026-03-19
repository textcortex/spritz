import assert from 'node:assert/strict';
import test from 'node:test';

import {
  connectACPWebSocket,
  runACPInstancePrompt,
  withACPInstanceClient,
} from './acp-client.mjs';

class FakeWebSocket extends EventTarget {
  constructor(url, options = {}) {
    super();
    this.url = url;
    this.sent = [];
    this.closed = false;
    this.options = options;
    setTimeout(() => {
      this.dispatchEvent(new Event('open'));
    }, 0);
  }

  send(payload) {
    this.sent.push(JSON.parse(payload));
    this.options.onSend?.(this, JSON.parse(payload));
  }

  close() {
    this.closed = true;
  }

  terminate() {
    this.closed = true;
  }
}

test('connectACPWebSocket correlates RPC responses and collects session updates', async () => {
  const client = await connectACPWebSocket('ws://127.0.0.1:2529/', 1, {
    WebSocket: class extends FakeWebSocket {
      constructor(url) {
        super(url, {
          onSend(socket, message) {
            if (message.method === 'initialize') {
              socket.dispatchEvent(new MessageEvent('message', {
                data: JSON.stringify({
                  jsonrpc: '2.0',
                  method: 'session/update',
                  params: {
                    update: {
                      sessionUpdate: 'agent_message_chunk',
                      content: [{ type: 'text', text: 'smoke-' }],
                    },
                  },
                }),
              }));
              socket.dispatchEvent(new MessageEvent('message', {
                data: JSON.stringify({
                  jsonrpc: '2.0',
                  id: message.id,
                  result: { ok: true },
                }),
              }));
            }
          },
        });
      }
    },
  });

  const response = await client.rpc('init-1', 'initialize', { hello: 'world' });
  assert.deepEqual(response, { jsonrpc: '2.0', id: 'init-1', result: { ok: true } });
  assert.equal(client.updates.length, 1);
  assert.equal(client.updates[0].sessionUpdate, 'agent_message_chunk');
  client.close();
});

test('connectACPWebSocket times out RPC calls without a response', async () => {
  const client = await connectACPWebSocket('ws://127.0.0.1:2529/', 1, {
    WebSocket: FakeWebSocket,
  });

  await assert.rejects(
    () => client.rpc('prompt-1', 'session/prompt', {}),
    /ACP request session\/prompt timed out/,
  );

  client.close();
});

test('withACPInstanceClient always closes the websocket client and stops the port-forward', async () => {
  let stopped = false;
  let closed = false;

  const result = await withACPInstanceClient(
    {
      namespace: 'example-ns',
      instanceName: 'example-instance',
      endpoint: { port: 2529, path: '/' },
      timeoutSeconds: 1,
      startPortForward: async () => ({
        localPort: 19555,
        async stop() {
          stopped = true;
        },
      }),
      connectWebSocket: async () => ({
        async rpc() {
          return {};
        },
        close() {
          closed = true;
        },
      }),
    },
    async () => 'ok',
  );

  assert.equal(result, 'ok');
  assert.equal(closed, true);
  assert.equal(stopped, true);
});

test('runACPInstancePrompt delegates the ACP flow through the instance client owner', async () => {
  const calls = [];
  const result = await runACPInstancePrompt({
    namespace: 'example-ns',
    instanceName: 'example-instance',
    endpoint: { port: 2529, path: '/' },
    timeoutSeconds: 1,
    promptText: 'Reply with smoke-token',
    settleDelayMs: 0,
    withInstanceClient: async (_options, callback) => callback({
      updates: [
        { sessionUpdate: 'agent_message_chunk', content: [{ type: 'text', text: 'spritz-' }] },
        { sessionUpdate: 'agent_message_chunk', content: [{ type: 'text', text: 'smoke-ok' }] },
      ],
      async rpc(id, method, params) {
        calls.push({ id, method, params });
        if (method === 'initialize') {
          return { result: { capabilities: {} } };
        }
        if (method === 'session/new') {
          return { result: { sessionId: 'session-123' } };
        }
        if (method === 'session/prompt') {
          return { result: { stopReason: 'end_turn' } };
        }
        throw new Error(`unexpected RPC method ${method}`);
      },
    }),
  });

  assert.deepEqual(calls.map((entry) => entry.method), ['initialize', 'session/new', 'session/prompt']);
  assert.equal(result.sessionId, 'session-123');
  assert.equal(result.stopReason, 'end_turn');
  assert.equal(result.assistantText, 'spritz-smoke-ok');
});
