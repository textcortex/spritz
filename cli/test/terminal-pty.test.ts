import assert from 'node:assert/strict';
import test from 'node:test';
import { once } from 'node:events';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { WebSocketServer } from 'ws';
import * as pty from 'node-pty';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const cliPath = path.join(__dirname, '..', 'src', 'index.ts');

async function waitForOutput(buffer: { value: string }, pattern: RegExp, timeoutMs = 4000): Promise<string> {
  const start = Date.now();
  while (Date.now() - start < timeoutMs) {
    if (pattern.test(buffer.value)) return buffer.value;
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  throw new Error(`Timed out waiting for ${pattern}`);
}

test('terminal returns to canonical mode after SIGKILL', { timeout: 15000 }, async (t) => {
  if (!process.env.SPRITZ_PTY_TEST) {
    t.skip();
    return;
  }

  const wss = new WebSocketServer({ port: 0 });
  t.after(() => {
    wss.close();
  });
  await once(wss, 'listening');
  const address = wss.address();
  const port = typeof address === 'object' && address ? address.port : 0;
  assert.ok(port > 0, 'websocket server should have a port');
  wss.on('connection', (socket) => {
    socket.on('message', () => undefined);
  });

  const shell = process.env.SHELL || 'bash';
  const env = {
    ...process.env,
    SPRITZ_API_URL: `http://127.0.0.1:${port}`,
    SPRITZ_USER_ID: 'test-user',
    SPRITZ_USER_EMAIL: 'test@example.com',
  };
  let ptyProcess: pty.IPty;
  try {
    ptyProcess = pty.spawn(shell, [], {
      cols: 80,
      rows: 24,
      cwd: process.cwd(),
      env,
    });
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    t.skip(`pty spawn failed in this environment: ${message}`);
    wss.close();
    return;
  }
  t.after(() => {
    try {
      ptyProcess.kill();
    } catch {
      // ignore
    }
  });

  const buffer = { value: '' };
  ptyProcess.onData((data) => {
    buffer.value += data;
  });

  const nodeBin = process.execPath;
  const cmd = `${nodeBin} --import tsx \"${cliPath}\" terminal devbox1 --namespace spritz >/tmp/spz-pty.log 2>&1 & echo CLI_PID:$!`;
  ptyProcess.write(`${cmd}\r`);
  const output = await waitForOutput(buffer, /CLI_PID:(\d+)/);
  const match = output.match(/CLI_PID:(\d+)/);
  assert.ok(match, 'should capture CLI pid');
  const pid = Number.parseInt(match![1], 10);
  assert.ok(Number.isFinite(pid), 'pid must be numeric');

  await Promise.race([
    once(wss, 'connection'),
    new Promise((_, reject) => setTimeout(() => reject(new Error('WS connect timeout')), 3000)),
  ]);
  await new Promise((resolve) => setTimeout(resolve, 300));
  process.kill(pid, 'SIGKILL');
  await new Promise((resolve) => setTimeout(resolve, 500));

  ptyProcess.write('stty -a\r');
  await waitForOutput(buffer, /(isig|icanon)/);
  const sttyOut = buffer.value;
  assert.ok(!/-isig/.test(sttyOut), 'ISIG should be enabled after reset');
  assert.ok(!/-icanon/.test(sttyOut), 'ICANON should be enabled after reset');
});
