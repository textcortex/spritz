import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { mkdtempSync, writeFileSync, readFileSync } from 'node:fs';
import http from 'node:http';
import net from 'node:net';
import os from 'node:os';
import test from 'node:test';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { WebSocketServer } from 'ws';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const cliPath = path.join(__dirname, '..', 'src', 'index.ts');

function writeExecutable(filePath: string, contents: string) {
  writeFileSync(filePath, contents, { encoding: 'utf8', mode: 0o755 });
}

function buildTestEnv(baseUrl: string, extraEnv: NodeJS.ProcessEnv = {}) {
  const configDir = mkdtempSync(path.join(os.tmpdir(), 'spz-config-'));
  return {
    ...process.env,
    SPRITZ_API_URL: baseUrl,
    SPRITZ_BEARER_TOKEN: 'service-token',
    SPRITZ_CONFIG_DIR: configDir,
    ...extraEnv,
  };
}

function listen(server: http.Server) {
  return new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
}

function spawnCli(args: string[], env: NodeJS.ProcessEnv) {
  return spawn(process.execPath, ['--import', 'tsx', cliPath, ...args], {
    env,
    stdio: ['ignore', 'pipe', 'pipe'],
  });
}

async function getFreePort() {
  return await new Promise<number>((resolve, reject) => {
    const server = net.createServer();
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const address = server.address();
      if (!address || typeof address !== 'object') {
        reject(new Error('failed to allocate port'));
        return;
      }
      const port = address.port;
      server.close((err) => {
        if (err) reject(err);
        else resolve(port);
      });
    });
  });
}

async function waitForPattern(buffer: { value: string }, pattern: RegExp, timeoutMs = 5000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (pattern.test(buffer.value)) return;
    await new Promise((resolve) => setTimeout(resolve, 25));
  }
  throw new Error(`timed out waiting for ${pattern}: ${buffer.value}`);
}

test('port-forward --print describes the default websocket mapping', async () => {
  const localPort = await getFreePort();
  let requestHeaders: http.IncomingHttpHeaders | null = null;
  const child = spawnCli(
    ['port-forward', 'devbox1', '--local', String(localPort), '--remote', '4000', '--print'],
    buildTestEnv('http://127.0.0.1:38080/api'),
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
  assert.equal(exitCode, 0, `spz port-forward --print should succeed: ${stderr}`);

  assert.equal(requestHeaders, null);
  assert.match(
    stdout.trim(),
    new RegExp(
      `^127\\.0\\.0\\.1:${localPort} -> 127\\.0\\.0\\.1:4000 via ws://127\\.0\\.0\\.1:38080/api/spritzes/devbox1/port-forward\\?port=4000$`
    )
  );
});

test('port-forward --transport ssh --print prints the SSH command for the requested mapping', async (t) => {
  let requestHeaders: http.IncomingHttpHeaders | null = null;
  let requestPath = '';
  let requestMethod = '';
  let requestBody: any = null;

  const server = http.createServer((req, res) => {
    requestHeaders = req.headers;
    requestPath = req.url || '';
    requestMethod = req.method || '';
    const chunks: Buffer[] = [];
    req.on('data', (chunk) => chunks.push(Buffer.from(chunk)));
    req.on('end', () => {
      requestBody = JSON.parse(Buffer.concat(chunks).toString('utf8'));
      res.writeHead(200, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({
        status: 'success',
        data: {
          host: '127.0.0.1',
          user: 'spritz',
          cert: 'ssh-ed25519-cert-v01@openssh.com AAAATEST',
          port: 2222,
          known_hosts: '[127.0.0.1]:2222 ssh-ed25519 AAAAKNOWNHOST',
        },
      }));
    });
  });
  await listen(server);
  t.after(() => {
    server.close();
  });
  const address = server.address();
  assert.ok(address && typeof address === 'object');

  const tempDir = mkdtempSync(path.join(os.tmpdir(), 'spz-port-forward-'));
  const fakeKeygen = path.join(tempDir, 'ssh-keygen');
  const fakeSsh = path.join(tempDir, 'ssh');
  const sshArgsLog = path.join(tempDir, 'ssh-args.log');

  writeExecutable(
    fakeKeygen,
    `#!/usr/bin/env bash
set -euo pipefail
target=""
while (($#)); do
  if [[ "$1" == "-f" ]]; then
    target="$2"
    shift 2
    continue
  fi
  shift
done
printf '%s\\n' 'PRIVATE KEY' > "$target"
printf '%s\\n' 'ssh-ed25519 AAAATEST generated@test' > "\${target}.pub"
chmod 600 "$target" "\${target}.pub"
`,
  );
  writeExecutable(
    fakeSsh,
    `#!/usr/bin/env bash
set -euo pipefail
printf '%s\\n' "$@" > "$SSH_ARGS_LOG"
`,
  );

  const child = spawnCli(
    ['port-forward', 'devbox1', '--transport', 'ssh', '--local', '3000', '--remote', '4000', '--print'],
    buildTestEnv(`http://127.0.0.1:${address.port}/api`, {
      SPRITZ_SSH_KEYGEN: fakeKeygen,
    }),
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
  assert.equal(exitCode, 0, `spz port-forward should succeed: ${stderr}`);

  assert.equal(requestHeaders?.authorization, 'Bearer service-token');
  assert.equal(requestMethod, 'POST');
  assert.equal(requestPath, '/api/spritzes/devbox1/ssh');
  assert.deepEqual(requestBody, {
    public_key: 'ssh-ed25519 AAAATEST generated@test\n',
  });
  assert.match(stdout, / -N /);
  assert.match(stdout, / -L 127\.0\.0\.1:3000:127\.0\.0\.1:4000 /);
  assert.match(stdout, / -p 2222 /);
  assert.match(stdout, /spritz@127\.0\.0\.1/);
});

test('port-forward --transport ssh executes the SSH client with the expected loopback mapping', async (t) => {
  const server = http.createServer((req, res) => {
    const chunks: Buffer[] = [];
    req.on('data', (chunk) => chunks.push(Buffer.from(chunk)));
    req.on('end', () => {
      res.writeHead(200, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({
        status: 'success',
        data: {
          host: '127.0.0.1',
          user: 'spritz',
          cert: 'ssh-ed25519-cert-v01@openssh.com AAAATEST',
          port: 2201,
          known_hosts: '[127.0.0.1]:2201 ssh-ed25519 AAAAKNOWNHOST',
        },
      }));
    });
  });
  await listen(server);
  t.after(() => {
    server.close();
  });
  const address = server.address();
  assert.ok(address && typeof address === 'object');

  const tempDir = mkdtempSync(path.join(os.tmpdir(), 'spz-port-forward-'));
  const fakeKeygen = path.join(tempDir, 'ssh-keygen');
  const fakeSsh = path.join(tempDir, 'ssh');
  const sshArgsLog = path.join(tempDir, 'ssh-args.log');

  writeExecutable(
    fakeKeygen,
    `#!/usr/bin/env bash
set -euo pipefail
target=""
while (($#)); do
  if [[ "$1" == "-f" ]]; then
    target="$2"
    shift 2
    continue
  fi
  shift
done
printf '%s\\n' 'PRIVATE KEY' > "$target"
printf '%s\\n' 'ssh-ed25519 AAAATEST generated@test' > "\${target}.pub"
chmod 600 "$target" "\${target}.pub"
`,
  );
  writeExecutable(
    fakeSsh,
    `#!/usr/bin/env bash
set -euo pipefail
printf '%s\\n' "$@" > "$SSH_ARGS_LOG"
`,
  );

  const child = spawnCli(
    ['port-forward', 'devbox1', '--namespace', 'spritz', '--local', '3000', '--remote', '4000', '--transport', 'ssh'],
    buildTestEnv(`http://127.0.0.1:${address.port}/api`, {
      SPRITZ_SSH_KEYGEN: fakeKeygen,
      SPRITZ_SSH_BINARY: fakeSsh,
      SSH_ARGS_LOG: sshArgsLog,
    }),
  );

  let stderr = '';
  child.stderr.on('data', (chunk) => {
    stderr += chunk.toString();
  });

  const exitCode = await new Promise<number | null>((resolve) => child.on('exit', resolve));
  assert.equal(exitCode, 0, `spz port-forward should succeed: ${stderr}`);

  const args = readFileSync(sshArgsLog, 'utf8').trim().split('\n');
  assert.ok(args.includes('-N'));
  const localIndex = args.indexOf('-L');
  assert.notEqual(localIndex, -1);
  assert.equal(args[localIndex + 1], '127.0.0.1:3000:127.0.0.1:4000');
  const portIndex = args.indexOf('-p');
  assert.notEqual(portIndex, -1);
  assert.equal(args[portIndex + 1], '2201');
  assert.equal(args.at(-1), 'spritz@127.0.0.1');
});

test('port-forward proxies localhost traffic over websocket by default', async (t) => {
  const localPort = await getFreePort();
  let upgradePath = '';
  let authorization = '';
  let origin = '';
  const server = http.createServer();
  const wss = new WebSocketServer({ noServer: true });
  await listen(server);
  t.after(() => {
    wss.close();
    server.close();
  });
  const address = server.address();
  assert.ok(address && typeof address === 'object');

  server.on('upgrade', (req, socket, head) => {
    upgradePath = req.url || '';
    authorization = req.headers.authorization || '';
    origin = req.headers.origin || '';
    wss.handleUpgrade(req, socket, head, (ws) => {
      wss.emit('connection', ws, req);
    });
  });
  wss.on('connection', (ws) => {
    ws.on('message', (payload) => {
      ws.send(payload);
    });
  });

  const child = spawnCli(
    ['port-forward', 'devbox1', '--namespace', 'spritz', '--local', String(localPort), '--remote', '4000'],
    buildTestEnv(`http://127.0.0.1:${address.port}/api`),
  );
  let stderr = '';
  const stderrBuffer = { value: '' };
  child.stderr.on('data', (chunk) => {
    const text = chunk.toString();
    stderr += text;
    stderrBuffer.value += text;
  });
  t.after(() => {
    child.kill('SIGTERM');
  });

  await waitForPattern(stderrBuffer, new RegExp(`forwarding 127\\.0\\.0\\.1:${localPort}`));

  const client = net.connect(localPort, '127.0.0.1');
  t.after(() => {
    client.destroy();
  });
  const replyPromise = new Promise<Buffer>((resolve) => {
    client.once('data', (chunk) => resolve(Buffer.from(chunk)));
  });
  client.write('ping');
  const reply = await replyPromise;
  assert.equal(reply.toString(), 'ping');
  assert.equal(authorization, 'Bearer service-token');
  assert.equal(origin, `http://127.0.0.1:${address.port}`);
  assert.equal(upgradePath, '/api/spritzes/devbox1/port-forward?namespace=spritz&port=4000');

  child.kill('SIGTERM');
  const exitCode = await new Promise<number | null>((resolve) => child.on('exit', resolve));
  assert.equal(exitCode, 0, `spz port-forward should exit cleanly: ${stderr}`);
});

test('port-forward preserves EOF-framed exchanges over websocket', async (t) => {
  const localPort = await getFreePort();
  const server = http.createServer();
  const wss = new WebSocketServer({ noServer: true });
  await listen(server);
  t.after(() => {
    wss.close();
    server.close();
  });
  const address = server.address();
  assert.ok(address && typeof address === 'object');

  server.on('upgrade', (req, socket, head) => {
    wss.handleUpgrade(req, socket, head, (ws) => {
      wss.emit('connection', ws, req);
    });
  });
  wss.on('connection', (ws) => {
    const chunks: Buffer[] = [];
    ws.on('message', (payload, isBinary) => {
      if (isBinary) {
        chunks.push(Buffer.from(payload as Buffer));
        return;
      }
      const control = JSON.parse(payload.toString('utf8'));
      assert.equal(control.type, 'eof');
      assert.equal(Buffer.concat(chunks).toString('utf8'), 'ping');
      ws.send(Buffer.from('pong'));
      ws.send(JSON.stringify({ type: 'eof' }));
    });
  });

  const child = spawnCli(
    ['port-forward', 'devbox1', '--namespace', 'spritz', '--local', String(localPort), '--remote', '4000'],
    buildTestEnv(`http://127.0.0.1:${address.port}/api`),
  );
  let stderr = '';
  const stderrBuffer = { value: '' };
  child.stderr.on('data', (chunk) => {
    const text = chunk.toString();
    stderr += text;
    stderrBuffer.value += text;
  });
  t.after(() => {
    child.kill('SIGTERM');
  });

  await waitForPattern(stderrBuffer, new RegExp(`forwarding 127\\.0\\.0\\.1:${localPort}`));

  const client = net.connect(localPort, '127.0.0.1');
  const replyPromise = new Promise<string>((resolve, reject) => {
    let payload = '';
    client.on('data', (chunk) => {
      payload += chunk.toString();
    });
    client.on('end', () => resolve(payload));
    client.on('error', reject);
  });
  client.write('ping');
  client.end();

  const reply = await replyPromise;
  assert.equal(reply, 'pong');

  child.kill('SIGTERM');
  const exitCode = await new Promise<number | null>((resolve) => child.on('exit', resolve));
  assert.equal(exitCode, 0, `spz port-forward should exit cleanly: ${stderr}`);
});

test('port-forward falls back to SSH when websocket startup validation is rejected by default', async (t) => {
  const tempDir = mkdtempSync(path.join(os.tmpdir(), 'spz-port-forward-'));
  const fakeKeygen = path.join(tempDir, 'ssh-keygen');
  const fakeSsh = path.join(tempDir, 'ssh');
  const sshArgsLog = path.join(tempDir, 'ssh-args.log');
  const server = http.createServer((req, res) => {
    if ((req.url || '').includes('/ssh')) {
      const chunks: Buffer[] = [];
      req.on('data', (chunk) => chunks.push(Buffer.from(chunk)));
      req.on('end', () => {
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({
          status: 'success',
          data: {
            host: '127.0.0.1',
            user: 'spritz',
            cert: 'ssh-ed25519-cert-v01@openssh.com AAAATEST',
            port: 2201,
            known_hosts: '[127.0.0.1]:2201 ssh-ed25519 AAAAKNOWNHOST',
          },
        }));
      });
      return;
    }
    res.writeHead(404, { 'Content-Type': 'text/plain' });
    res.end('missing');
  });
  await listen(server);
  t.after(() => {
    server.close();
  });
  const address = server.address();
  assert.ok(address && typeof address === 'object');

  writeExecutable(
    fakeKeygen,
    `#!/usr/bin/env bash
set -euo pipefail
target=""
while (($#)); do
  if [[ "$1" == "-f" ]]; then
    target="$2"
    shift 2
    continue
  fi
  shift
done
printf '%s\\n' 'PRIVATE KEY' > "$target"
printf '%s\\n' 'ssh-ed25519 AAAATEST generated@test' > "\${target}.pub"
chmod 600 "$target" "\${target}.pub"
`,
  );
  writeExecutable(
    fakeSsh,
    `#!/usr/bin/env bash
set -euo pipefail
printf '%s\\n' "$@" > "$SSH_ARGS_LOG"
`,
  );

  const child = spawnCli(
    ['port-forward', 'devbox1', '--namespace', 'spritz', '--local', '3000', '--remote', '4000'],
    buildTestEnv(`http://127.0.0.1:${address.port}/api`, {
      SPRITZ_SSH_KEYGEN: fakeKeygen,
      SPRITZ_SSH_BINARY: fakeSsh,
      SSH_ARGS_LOG: sshArgsLog,
    }),
  );

  let stderr = '';
  child.stderr.on('data', (chunk) => {
    stderr += chunk.toString();
  });

  const exitCode = await new Promise<number | null>((resolve) => child.on('exit', resolve));
  assert.equal(exitCode, 0, `spz port-forward should fall back to SSH: ${stderr}`);
  assert.match(stderr, /Websocket port-forward unavailable; falling back to legacy SSH/);

  const args = readFileSync(sshArgsLog, 'utf8').trim().split('\n');
  assert.ok(args.includes('-N'));
  const localIndex = args.indexOf('-L');
  assert.notEqual(localIndex, -1);
  assert.equal(args[localIndex + 1], '127.0.0.1:3000:127.0.0.1:4000');
});

test('port-forward fails during startup when websocket validation is rejected for explicit websocket transport', async (t) => {
  const localPort = await getFreePort();
  const server = http.createServer((req, res) => {
    res.writeHead(503, { 'Content-Type': 'text/plain' });
    res.end('unavailable');
  });
  await listen(server);
  t.after(() => {
    server.close();
  });
  const address = server.address();
  assert.ok(address && typeof address === 'object');

  const child = spawnCli(
    ['port-forward', 'devbox1', '--transport', 'ws', '--local', String(localPort), '--remote', '4000'],
    buildTestEnv(`http://127.0.0.1:${address.port}/api`),
  );

  let stderr = '';
  child.stderr.on('data', (chunk) => {
    stderr += chunk.toString();
  });

  const exitCode = await new Promise<number | null>((resolve) => child.on('exit', resolve));
  assert.notEqual(exitCode, 0, 'spz port-forward should fail before listening');
  assert.match(stderr, /port-forward validation failed: 503/);

  await assert.rejects(
    () =>
      new Promise<void>((resolve, reject) => {
        const socket = net.connect(localPort, '127.0.0.1');
        socket.once('connect', () => {
          socket.destroy();
          resolve();
        });
        socket.once('error', reject);
      }),
  );
});

test('port-forward rejects missing remote port', async () => {
  const child = spawnCli(
    ['port-forward', 'devbox1', '--local', '3000'],
    buildTestEnv('http://127.0.0.1:9/api'),
  );

  let stderr = '';
  child.stderr.on('data', (chunk) => {
    stderr += chunk.toString();
  });

  const exitCode = await new Promise<number | null>((resolve) => child.on('exit', resolve));
  assert.notEqual(exitCode, 0, 'spz port-forward should reject missing --remote');
  assert.match(stderr, /--remote is required/);
});
