import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';

function loadACPClientModule() {
  const sockets = [];

  class FakeWebSocket {
    static OPEN = 1;
    static CONNECTING = 0;
    static CLOSED = 3;

    constructor(url) {
      this.url = url;
      this.readyState = FakeWebSocket.CONNECTING;
      this.sent = [];
      this.onopen = null;
      this.onmessage = null;
      this.onclose = null;
      this.onerror = null;
      sockets.push(this);
    }

    send(payload) {
      this.sent.push(JSON.parse(payload));
    }

    close() {
      this.readyState = FakeWebSocket.CLOSED;
      if (typeof this.onclose === 'function') {
        this.onclose({});
      }
    }

    open() {
      this.readyState = FakeWebSocket.OPEN;
      this.onopen?.();
    }

    receive(payload) {
      this.onmessage?.({ data: JSON.stringify(payload) });
    }
  }

  const window = {
    WebSocket: FakeWebSocket,
    TextDecoder,
  };
  window.window = window;

  const context = vm.createContext({
    window,
    WebSocket: FakeWebSocket,
    TextDecoder,
    console,
  });
  context.globalThis = context.window;

  vm.runInContext(fs.readFileSync('/Users/onur/repos/spritz/ui/public/acp-client.js', 'utf8'), context, {
    filename: 'acp-client.js',
  });

  return {
    createACPClient: window.SpritzACPClient.createACPClient,
    sockets,
  };
}

test('ACP client does not report ready or send prompts before bootstrap completes', async () => {
  const { createACPClient, sockets } = loadACPClientModule();
  const readyStates = [];

  const client = createACPClient({
    wsUrl: 'ws://example.test/acp',
    conversation: {
      spec: {
        sessionId: 'session-existing',
        cwd: '/home/dev',
      },
    },
    onStatus() {},
    onReadyChange(value) {
      readyStates.push(value);
    },
  });

  const startPromise = client.start();
  const socket = sockets[0];
  socket.open();
  await new Promise((resolve) => setTimeout(resolve, 0));

  assert.equal(client.isReady(), false);
  const earlySend = client.sendPrompt('test 2').then(
    () => 'resolved',
    (error) => error.message,
  );
  await new Promise((resolve) => setTimeout(resolve, 0));

  assert.equal(socket.sent.length, 1);
  assert.equal(socket.sent[0].method, 'initialize');
  assert.equal(
    await Promise.race([
      earlySend,
      new Promise((resolve) => setTimeout(() => resolve('pending'), 5)),
    ]),
    'ACP session is not ready yet.',
  );

  socket.receive({
    jsonrpc: '2.0',
    id: socket.sent[0].id,
    result: {
      agentCapabilities: {
        loadSession: true,
      },
    },
  });
  await new Promise((resolve) => setTimeout(resolve, 0));

  assert.equal(socket.sent[1].method, 'session/load');
  socket.receive({
    jsonrpc: '2.0',
    id: socket.sent[1].id,
    result: {},
  });

  await startPromise;

  assert.equal(client.isReady(), true);
  assert.deepEqual(readyStates, [false, true]);
  client.dispose();
});

test('ACP client recreates the session when session/load reports a missing session', async () => {
  const { createACPClient, sockets } = loadACPClientModule();
  const seenSessionIds = [];

  const client = createACPClient({
    wsUrl: 'ws://example.test/acp',
    conversation: {
      spec: {
        sessionId: 'session-stale',
        cwd: '/home/dev',
      },
    },
    onStatus() {},
    async onSessionId(sessionId) {
      seenSessionIds.push(sessionId);
    },
  });

  const startPromise = client.start();
  const socket = sockets[0];
  socket.open();
  await new Promise((resolve) => setTimeout(resolve, 0));

  socket.receive({
    jsonrpc: '2.0',
    id: socket.sent[0].id,
    result: {
      agentCapabilities: {
        loadSession: true,
      },
    },
  });
  await new Promise((resolve) => setTimeout(resolve, 0));

  assert.equal(socket.sent[1].method, 'session/load');
  socket.receive({
    jsonrpc: '2.0',
    id: socket.sent[1].id,
    error: {
      code: -32603,
      message: 'Internal error',
      data: {
        details: 'Session session-stale not found',
      },
    },
  });
  await new Promise((resolve) => setTimeout(resolve, 0));

  assert.equal(socket.sent[2].method, 'session/new');
  socket.receive({
    jsonrpc: '2.0',
    id: socket.sent[2].id,
    result: {
      sessionId: 'session-fresh',
    },
  });

  await startPromise;

  assert.equal(client.isReady(), true);
  assert.deepEqual(seenSessionIds, ['session-fresh']);
  client.dispose();
});
