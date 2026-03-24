import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { mkdtempSync } from 'node:fs';
import http from 'node:http';
import os from 'node:os';
import test from 'node:test';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const cliPath = path.join(__dirname, '..', 'src', 'index.ts');

test('chat send uses the internal token and prints assistant text by default', async (t) => {
  let requestBody: any = null;
  let requestHeaders: http.IncomingHttpHeaders | null = null;

  const server = http.createServer((req, res) => {
    requestHeaders = req.headers;
    const chunks: Buffer[] = [];
    req.on('data', (chunk) => chunks.push(Buffer.from(chunk)));
    req.on('end', () => {
      requestBody = JSON.parse(Buffer.concat(chunks).toString('utf8'));
      res.writeHead(200, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({
        status: 'success',
        data: {
          conversation: {
            metadata: { name: 'tidy-otter-conv' },
            spec: { spritzName: 'tidy-otter', sessionId: 'session-fresh' },
          },
          effectiveSessionId: 'session-fresh',
          assistantText: 'spritz debug',
          stopReason: 'end_turn',
          createdConversation: true,
        },
      }));
    });
  });
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  t.after(() => {
    server.close();
  });
  const address = server.address();
  assert.ok(address && typeof address === 'object');

  const child = spawn(
    process.execPath,
    [
      '--import',
      'tsx',
      cliPath,
      'chat',
      'send',
      '--instance',
      'tidy-otter',
      '--message',
      'hello from cli',
      '--owner-id',
      'user-123',
      '--cwd',
      '/workspace/app',
      '--title',
      'Debug Run',
      '--reason',
      'local smoke',
    ],
    {
      env: {
        ...process.env,
        SPRITZ_API_URL: `http://127.0.0.1:${address.port}/api`,
        SPRITZ_INTERNAL_TOKEN: 'internal-token',
        SPRITZ_CONFIG_DIR: mkdtempSync(path.join(os.tmpdir(), 'spz-config-')),
      },
      stdio: ['ignore', 'pipe', 'pipe'],
    },
  );

  let stdout = '';
  let stderr = '';
  child.stdout.on('data', (chunk) => {
    stdout += chunk.toString();
  });
  child.stderr.on('data', (chunk) => {
    stderr += chunk.toString();
  });

  const exitCode = await new Promise<number | null>((resolve) => child.on('exit', resolve));
  assert.equal(exitCode, 0, `spz chat send should succeed: ${stderr}`);
  assert.equal(stdout.trim(), 'spritz debug');
  assert.equal(requestHeaders?.['x-spritz-internal-token'], 'internal-token');
  assert.equal(requestHeaders?.['x-spritz-user-id'], 'user-123');
  assert.deepEqual(requestBody, {
    target: {
      spritzName: 'tidy-otter',
      cwd: '/workspace/app',
      title: 'Debug Run',
    },
    reason: 'local smoke',
    message: 'hello from cli',
  });
});

test('chat send supports existing conversations and json output', async (t) => {
  let requestBody: any = null;

  const server = http.createServer((req, res) => {
    const chunks: Buffer[] = [];
    req.on('data', (chunk) => chunks.push(Buffer.from(chunk)));
    req.on('end', () => {
      requestBody = JSON.parse(Buffer.concat(chunks).toString('utf8'));
      res.writeHead(200, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({
        status: 'success',
        data: {
          conversation: {
            metadata: { name: 'tidy-otter-conv' },
            spec: { spritzName: 'tidy-otter', sessionId: 'session-existing' },
          },
          effectiveSessionId: 'session-existing',
          assistantText: 'ok',
          stopReason: 'end_turn',
          createdConversation: false,
        },
      }));
    });
  });
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  t.after(() => {
    server.close();
  });
  const address = server.address();
  assert.ok(address && typeof address === 'object');

  const child = spawn(
    process.execPath,
    [
      '--import',
      'tsx',
      cliPath,
      'chat',
      'send',
      '--conversation',
      'tidy-otter-conv',
      '--message',
      'follow up',
      '--owner-id',
      'user-123',
      '--json',
    ],
    {
      env: {
        ...process.env,
        SPRITZ_API_URL: `http://127.0.0.1:${address.port}/api`,
        SPRITZ_INTERNAL_TOKEN: 'internal-token',
        SPRITZ_CONFIG_DIR: mkdtempSync(path.join(os.tmpdir(), 'spz-config-')),
      },
      stdio: ['ignore', 'pipe', 'pipe'],
    },
  );

  let stdout = '';
  let stderr = '';
  child.stdout.on('data', (chunk) => {
    stdout += chunk.toString();
  });
  child.stderr.on('data', (chunk) => {
    stderr += chunk.toString();
  });

  const exitCode = await new Promise<number | null>((resolve) => child.on('exit', resolve));
  assert.equal(exitCode, 0, `spz chat send --json should succeed: ${stderr}`);
  assert.deepEqual(requestBody, {
    target: {
      conversationId: 'tidy-otter-conv',
    },
    reason: 'spz chat send',
    message: 'follow up',
  });

  const payload = JSON.parse(stdout);
  assert.equal(payload.effectiveSessionId, 'session-existing');
  assert.equal(payload.assistantText, 'ok');
  assert.equal(payload.createdConversation, false);
});
