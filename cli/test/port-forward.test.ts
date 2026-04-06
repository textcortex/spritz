import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { mkdtempSync, writeFileSync, readFileSync } from 'node:fs';
import http from 'node:http';
import os from 'node:os';
import test from 'node:test';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

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

test('port-forward --print prints the SSH command for the requested mapping', async (t) => {
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

  const child = spawnCli(
    ['port-forward', 'devbox1', '--local', '3000', '--remote', '4000', '--print'],
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
  assert.equal(exitCode, 0, `spz port-forward --print should succeed: ${stderr}`);

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

test('port-forward executes the SSH client with the expected loopback mapping', async (t) => {
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
