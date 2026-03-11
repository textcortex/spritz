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

test('create uses bearer auth and provisioner fields for preset-based creation', async (t) => {
  let requestBody: any = null;
  let requestHeaders: http.IncomingHttpHeaders | null = null;

  const server = http.createServer((req, res) => {
    requestHeaders = req.headers;
    const chunks: Buffer[] = [];
    req.on('data', (chunk) => chunks.push(Buffer.from(chunk)));
    req.on('end', () => {
      requestBody = JSON.parse(Buffer.concat(chunks).toString('utf8'));
      res.writeHead(201, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({
        status: 'success',
        data: {
          accessUrl: 'https://console.example.com/w/openclaw-tide-wind/',
          ownerId: 'user-123',
          presetId: 'openclaw',
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
    ['--import', 'tsx', cliPath, 'create', '--preset', 'openclaw', '--owner-id', 'user-123', '--idle-ttl', '24h', '--ttl', '168h', '--idempotency-key', 'discord-123', '--source', 'discord', '--request-id', 'interaction-1'],
    {
      env: {
        ...process.env,
        SPRITZ_API_URL: `http://127.0.0.1:${address.port}/api`,
        SPRITZ_BEARER_TOKEN: 'service-token',
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
  assert.equal(exitCode, 0, `spz create should succeed: ${stderr}`);

  assert.equal(requestHeaders?.authorization, 'Bearer service-token');
  assert.equal(requestHeaders?.['x-spritz-user-id'], undefined);
  assert.deepEqual(requestBody, {
    presetId: 'openclaw',
    ownerId: 'user-123',
    idleTtl: '24h',
    ttl: '168h',
    idempotencyKey: 'discord-123',
    source: 'discord',
    requestId: 'interaction-1',
    spec: {},
  });

  const payload = JSON.parse(stdout);
  assert.equal(payload.accessUrl, 'https://console.example.com/w/openclaw-tide-wind/');
  assert.equal(payload.ownerId, 'user-123');
  assert.equal(payload.presetId, 'openclaw');
});

test('create falls back to local owner identity without bearer auth', async (t) => {
  let requestBody: any = null;
  let requestHeaders: http.IncomingHttpHeaders | null = null;

  const server = http.createServer((req, res) => {
    requestHeaders = req.headers;
    const chunks: Buffer[] = [];
    req.on('data', (chunk) => chunks.push(Buffer.from(chunk)));
    req.on('end', () => {
      requestBody = JSON.parse(Buffer.concat(chunks).toString('utf8'));
      res.writeHead(201, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({
        status: 'success',
        data: {
          accessUrl: 'http://localhost:8080/w/claude-code-tender-otter/',
          ownerId: 'local-user',
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

  const configDir = mkdtempSync(path.join(os.tmpdir(), 'spz-config-'));
  const child = spawn(
    process.execPath,
    ['--import', 'tsx', cliPath, 'create', '--image', 'example.com/spritz-claude-code:latest'],
    {
      env: {
        ...process.env,
        SPRITZ_API_URL: `http://127.0.0.1:${address.port}/api`,
        SPRITZ_USER_ID: 'local-user',
        SPRITZ_CONFIG_DIR: configDir,
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
  assert.equal(exitCode, 0, `spz create should succeed: ${stderr}`);

  assert.equal(requestHeaders?.authorization, undefined);
  assert.equal(requestHeaders?.['x-spritz-user-id'], 'local-user');
  assert.equal(requestBody.ownerId, 'local-user');
  assert.equal(requestBody.spec.image, 'example.com/spritz-claude-code:latest');

  const payload = JSON.parse(stdout);
  assert.equal(payload.ownerId, 'local-user');
});

test('create allows server-side default preset resolution', async (t) => {
  let requestBody: any = null;
  let requestHeaders: http.IncomingHttpHeaders | null = null;

  const server = http.createServer((req, res) => {
    requestHeaders = req.headers;
    const chunks: Buffer[] = [];
    req.on('data', (chunk) => chunks.push(Buffer.from(chunk)));
    req.on('end', () => {
      requestBody = JSON.parse(Buffer.concat(chunks).toString('utf8'));
      res.writeHead(201, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({
        status: 'success',
        data: {
          accessUrl: 'https://console.example.com/w/openclaw-tide-wind/',
          ownerId: 'user-123',
          presetId: 'openclaw',
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
    ['--import', 'tsx', cliPath, 'create', '--owner-id', 'user-123', '--idempotency-key', 'discord-default-preset'],
    {
      env: {
        ...process.env,
        SPRITZ_API_URL: `http://127.0.0.1:${address.port}/api`,
        SPRITZ_BEARER_TOKEN: 'service-token',
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
  assert.equal(exitCode, 0, `spz create should succeed: ${stderr}`);

  assert.equal(requestHeaders?.authorization, 'Bearer service-token');
  assert.deepEqual(requestBody, {
    ownerId: 'user-123',
    idempotencyKey: 'discord-default-preset',
    spec: {},
  });

  const payload = JSON.parse(stdout);
  assert.equal(payload.presetId, 'openclaw');
  assert.equal(payload.ownerId, 'user-123');
});
