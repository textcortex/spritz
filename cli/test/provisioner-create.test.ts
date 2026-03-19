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
          accessUrl: 'https://console.example.com/#chat/openclaw-tide-wind',
          chatUrl: 'https://console.example.com/#chat/openclaw-tide-wind',
          instanceUrl: 'https://console.example.com/w/openclaw-tide-wind/',
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
  assert.equal(payload.accessUrl, 'https://console.example.com/#chat/openclaw-tide-wind');
  assert.equal(payload.chatUrl, 'https://console.example.com/#chat/openclaw-tide-wind');
  assert.equal(payload.instanceUrl, 'https://console.example.com/w/openclaw-tide-wind/');
  assert.equal(payload.ownerId, 'user-123');
  assert.equal(payload.presetId, 'openclaw');
});

test('create sends preset inputs when requested', async (t) => {
  let requestBody: any = null;

  const server = http.createServer((req, res) => {
    const chunks: Buffer[] = [];
    req.on('data', (chunk) => chunks.push(Buffer.from(chunk)));
    req.on('end', () => {
      requestBody = JSON.parse(Buffer.concat(chunks).toString('utf8'));
      res.writeHead(201, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({
        status: 'success',
        data: {
          accessUrl: 'https://console.example.com/#chat/zeno-tide-wind',
          chatUrl: 'https://console.example.com/#chat/zeno-tide-wind',
          instanceUrl: 'https://console.example.com/w/zeno-tide-wind/',
          ownerId: 'user-123',
          presetId: 'zeno',
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
      'create',
      '--preset',
      'zeno',
      '--preset-input',
      'agentId=ag-123',
      '--preset-input',
      'mode=default',
      '--owner-id',
      'user-123',
      '--idempotency-key',
      'req-zeno-1',
    ],
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

  let stderr = '';
  child.stderr.on('data', (chunk) => {
    stderr += chunk.toString();
  });

  const exitCode = await new Promise<number | null>((resolve) => child.on('exit', resolve));
  assert.equal(exitCode, 0, `spz create should succeed: ${stderr}`);
  assert.deepEqual(requestBody, {
    presetId: 'zeno',
    presetInputs: {
      agentId: 'ag-123',
      mode: 'default',
    },
    ownerId: 'user-123',
    idempotencyKey: 'req-zeno-1',
    spec: {},
  });
});

test('create sends external owner identity when requested', async (t) => {
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
          accessUrl: 'https://console.example.com/#chat/openclaw-tide-wind',
          chatUrl: 'https://console.example.com/#chat/openclaw-tide-wind',
          instanceUrl: 'https://console.example.com/w/openclaw-tide-wind/',
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
    [
      '--import',
      'tsx',
      cliPath,
      'create',
      '--preset',
      'openclaw',
      '--owner-provider',
      'msteams',
      '--owner-tenant',
      '72f988bf-86f1-41af-91ab-2d7cd011db47',
      '--owner-subject',
      '6f0f9d4f-9b0e-4d52-8c3a-ef0fd64b9b9f',
      '--idempotency-key',
      'teams-123',
    ],
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
    presetId: 'openclaw',
    ownerRef: {
      type: 'external',
      provider: 'msteams',
      tenant: '72f988bf-86f1-41af-91ab-2d7cd011db47',
      subject: '6f0f9d4f-9b0e-4d52-8c3a-ef0fd64b9b9f',
    },
    idempotencyKey: 'teams-123',
    spec: {},
  });

  const payload = JSON.parse(stdout);
  assert.equal(payload.presetId, 'openclaw');
  assert.equal(payload.ownerId, undefined);
});

test('create rejects mixed owner-id and external owner flags', async () => {
  const child = spawn(
    process.execPath,
    [
      '--import',
      'tsx',
      cliPath,
      'create',
      '--preset',
      'openclaw',
      '--owner-id',
      'user-123',
      '--owner-provider',
      'discord',
      '--owner-subject',
      '123456789012345678',
    ],
    {
      env: {
        ...process.env,
        SPRITZ_API_URL: 'http://127.0.0.1:9/api',
        SPRITZ_BEARER_TOKEN: 'service-token',
        SPRITZ_CONFIG_DIR: mkdtempSync(path.join(os.tmpdir(), 'spz-config-')),
      },
      stdio: ['ignore', 'pipe', 'pipe'],
    },
  );

  let stderr = '';
  child.stderr.on('data', (chunk) => {
    stderr += chunk.toString();
  });

  const exitCode = await new Promise<number | null>((resolve) => child.on('exit', resolve));
  assert.notEqual(exitCode, 0, 'spz create should fail for conflicting owner inputs');
  assert.match(stderr, /mutually exclusive/);
});

test('create explains unresolved external owners with connect-account guidance', async (t) => {
  const server = http.createServer((req, res) => {
    const chunks: Buffer[] = [];
    req.on('data', (chunk) => chunks.push(Buffer.from(chunk)));
    req.on('end', () => {
      res.writeHead(409, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({
        status: 'fail',
        data: {
          message: 'external identity is unresolved',
          error: 'external_identity_unresolved',
          identity: {
            provider: 'discord',
            subject: '123456789012345678',
          },
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
      'create',
      '--preset',
      'openclaw',
      '--owner-provider',
      'discord',
      '--owner-subject',
      '123456789012345678',
    ],
    {
      env: {
        ...process.env,
        SPRITZ_API_URL: `http://127.0.0.1:${address.port}/api`,
        SPRITZ_BEARER_TOKEN: 'service-token',
        SPRITZ_CONFIG_DIR: mkdtempSync(path.join(os.tmpdir(), 'spz-config-')),
        AUDIENCE: 'agent',
      },
      stdio: ['ignore', 'pipe', 'pipe'],
    },
  );

  let stderr = '';
  child.stderr.on('data', (chunk) => {
    stderr += chunk.toString();
  });

  const exitCode = await new Promise<number | null>((resolve) => child.on('exit', resolve));
  assert.notEqual(exitCode, 0, 'spz create should fail for unresolved external owner');
  assert.match(stderr, /could not be resolved to a Spritz owner/i);
  assert.match(stderr, /connect their account/i);
  assert.match(stderr, /--owner-provider and --owner-subject/i);
});

test('create without owner input guides agent callers toward external owner flags', async () => {
  const child = spawn(
    process.execPath,
    ['--import', 'tsx', cliPath, 'create', '--preset', 'openclaw'],
    {
      env: {
        ...process.env,
        SPRITZ_API_URL: 'http://127.0.0.1:9/api',
        SPRITZ_BEARER_TOKEN: 'service-token',
        SPRITZ_CONFIG_DIR: mkdtempSync(path.join(os.tmpdir(), 'spz-config-')),
        AUDIENCE: 'agent',
      },
      stdio: ['ignore', 'pipe', 'pipe'],
    },
  );

  let stderr = '';
  child.stderr.on('data', (chunk) => {
    stderr += chunk.toString();
  });

  const exitCode = await new Promise<number | null>((resolve) => child.on('exit', resolve));
  assert.notEqual(exitCode, 0, 'spz create should fail when no owner input is provided');
  assert.match(stderr, /owner input is required/i);
  assert.match(stderr, /platform-native user ID with --owner-provider and --owner-subject/i);
  assert.match(stderr, /ask for clarification/i);
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
          accessUrl: 'http://localhost:8080/#chat/claude-code-tender-otter',
          chatUrl: 'http://localhost:8080/#chat/claude-code-tender-otter',
          instanceUrl: 'http://localhost:8080/w/claude-code-tender-otter/',
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
          accessUrl: 'https://console.example.com/#chat/openclaw-tide-wind',
          chatUrl: 'https://console.example.com/#chat/openclaw-tide-wind',
          instanceUrl: 'https://console.example.com/w/openclaw-tide-wind/',
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

test('create uses active profile api url and bearer token without SPRITZ env vars', async (t) => {
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
          accessUrl: 'https://console.example.com/#chat/openclaw-profile-smoke',
          chatUrl: 'https://console.example.com/#chat/openclaw-profile-smoke',
          instanceUrl: 'https://console.example.com/w/openclaw-profile-smoke/',
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

  const configDir = mkdtempSync(path.join(os.tmpdir(), 'spz-config-'));
  const profileChild = spawn(
    process.execPath,
    [
      '--import',
      'tsx',
      cliPath,
      'profile',
      'set',
      'zenobot',
      '--api-url',
      `http://127.0.0.1:${address.port}/api`,
      '--token',
      'profile-token',
      '--namespace',
      'spritz-staging',
    ],
    {
      env: {
        ...process.env,
        SPRITZ_CONFIG_DIR: configDir,
      },
      stdio: ['ignore', 'pipe', 'pipe'],
    },
  );
  const profileExitCode = await new Promise<number | null>((resolve) => profileChild.on('exit', resolve));
  assert.equal(profileExitCode, 0, 'profile set should succeed');

  const useChild = spawn(process.execPath, ['--import', 'tsx', cliPath, 'profile', 'use', 'zenobot'], {
    env: {
      ...process.env,
      SPRITZ_CONFIG_DIR: configDir,
    },
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  const useExitCode = await new Promise<number | null>((resolve) => useChild.on('exit', resolve));
  assert.equal(useExitCode, 0, 'profile use should succeed');

  const child = spawn(
    process.execPath,
    ['--import', 'tsx', cliPath, 'create', '--owner-id', 'user-123', '--preset', 'openclaw', '--idempotency-key', 'profile-request'],
    {
      env: {
        ...process.env,
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

  assert.equal(requestHeaders?.authorization, 'Bearer profile-token');
  assert.deepEqual(requestBody, {
    namespace: 'spritz-staging',
    presetId: 'openclaw',
    ownerId: 'user-123',
    idempotencyKey: 'profile-request',
    spec: {},
  });

  const payload = JSON.parse(stdout);
  assert.equal(payload.accessUrl, 'https://console.example.com/#chat/openclaw-profile-smoke');
  assert.equal(payload.chatUrl, 'https://console.example.com/#chat/openclaw-profile-smoke');
  assert.equal(payload.instanceUrl, 'https://console.example.com/w/openclaw-profile-smoke/');
});

test('profile show redacts bearer tokens', async () => {
  const configDir = mkdtempSync(path.join(os.tmpdir(), 'spz-config-'));

  const profileChild = spawn(
    process.execPath,
    [
      '--import',
      'tsx',
      cliPath,
      'profile',
      'set',
      'zenobot',
      '--api-url',
      'https://staging.spritz.textcortex.com/api',
      '--token',
      'super-secret-token',
    ],
    {
      env: {
        ...process.env,
        SPRITZ_CONFIG_DIR: configDir,
      },
      stdio: ['ignore', 'pipe', 'pipe'],
    },
  );
  const profileExitCode = await new Promise<number | null>((resolve) => profileChild.on('exit', resolve));
  assert.equal(profileExitCode, 0, 'profile set should succeed');

  const showChild = spawn(process.execPath, ['--import', 'tsx', cliPath, 'profile', 'show', 'zenobot'], {
    env: {
      ...process.env,
      SPRITZ_CONFIG_DIR: configDir,
    },
    stdio: ['ignore', 'pipe', 'pipe'],
  });

  let stdout = '';
  let stderr = '';
  showChild.stdout.on('data', (chunk) => {
    stdout += chunk.toString();
  });
  showChild.stderr.on('data', (chunk) => {
    stderr += chunk.toString();
  });

  const exitCode = await new Promise<number | null>((resolve) => showChild.on('exit', resolve));
  assert.equal(exitCode, 0, `profile show should succeed: ${stderr}`);
  assert.match(stdout, /Bearer Token: \(set\)/);
  assert.doesNotMatch(stdout, /super-secret-token/);
});
