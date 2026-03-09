import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

import {
  buildGatewayClientOptions,
  loadOpenclawCompat,
  parseArgs,
  useTrustedProxyControlUiBridge,
} from './acp-wrapper.mjs';

test('trusted-proxy bridge uses control-ui profile without shared auth or device identity', () => {
  const opts = buildGatewayClientOptions({
    connectionUrl: 'ws://127.0.0.1:8080',
    gatewayToken: 'secret-token',
    gatewayPassword: 'secret-password',
    trustedProxyControlUi: true,
  });

  assert.equal(opts.url, 'ws://127.0.0.1:8080');
  assert.equal(opts.clientName, 'openclaw-control-ui');
  assert.equal(opts.mode, 'webchat');
  assert.equal(opts.deviceIdentity, false);
  assert.equal(opts.token, undefined);
  assert.equal(opts.password, undefined);
  assert.equal(opts.role, 'operator');
});

test('default bridge preserves CLI profile and shared auth', () => {
  const opts = buildGatewayClientOptions({
    connectionUrl: 'ws://127.0.0.1:8080',
    gatewayToken: 'secret-token',
    gatewayPassword: 'secret-password',
    trustedProxyControlUi: false,
  });

  assert.equal(opts.clientName, 'cli');
  assert.equal(opts.mode, 'cli');
  assert.equal(opts.token, 'secret-token');
  assert.equal(opts.password, 'secret-password');
  assert.equal(opts.deviceIdentity, undefined);
});

test('parseArgs accepts acp subcommand and file-based secrets', () => {
  const opts = parseArgs([
    'acp',
    '--url', 'ws://127.0.0.1:8080',
    '--session', 'agent:main:main',
    '--token-file', '/tmp/gateway.token',
    '--verbose',
  ], {
    readSecretFromFile(path, label) {
      assert.equal(path, '/tmp/gateway.token');
      assert.equal(label, 'Gateway token');
      return 'file-token';
    },
  });

  assert.equal(opts.gatewayUrl, 'ws://127.0.0.1:8080');
  assert.equal(opts.defaultSessionKey, 'agent:main:main');
  assert.equal(opts.gatewayToken, 'file-token');
  assert.equal(opts.verbose, true);
});

test('useTrustedProxyControlUiBridge reads truthy env values', () => {
  assert.equal(useTrustedProxyControlUiBridge({ SPRITZ_OPENCLAW_ACP_USE_CONTROL_UI_BRIDGE: '1' }), true);
  assert.equal(useTrustedProxyControlUiBridge({ SPRITZ_OPENCLAW_ACP_USE_CONTROL_UI_BRIDGE: 'true' }), true);
  assert.equal(useTrustedProxyControlUiBridge({ SPRITZ_OPENCLAW_ACP_USE_CONTROL_UI_BRIDGE: '0' }), false);
  assert.equal(useTrustedProxyControlUiBridge({}), false);
});

test('loadOpenclawCompat loads the generated stable compat module from the package root', async () => {
  const packageRoot = fs.mkdtempSync(path.join(os.tmpdir(), 'spritz-openclaw-package-'));
  const distDir = path.join(packageRoot, 'dist');
  fs.mkdirSync(distDir, { recursive: true });
  fs.writeFileSync(
    path.join(distDir, 'spritz-acp-compat.js'),
    [
      'export class GatewayClient {}',
      'export class AcpGatewayAgent {}',
      'export function loadConfig() { return { ok: true }; }',
      'export function buildGatewayConnectionDetails() { return { url: "ws://127.0.0.1:8080" }; }',
      'export async function resolveGatewayConnectionAuth() { return { token: "secret" }; }',
      '',
    ].join('\n'),
  );

  const compat = await loadOpenclawCompat({
    SPRITZ_OPENCLAW_PACKAGE_ROOT: packageRoot,
  });

  assert.equal(compat.GatewayClient.name, 'GatewayClient');
  assert.equal(compat.AcpGatewayAgent.name, 'AcpGatewayAgent');
  assert.deepEqual(compat.loadConfig(), { ok: true });
  assert.equal(compat.buildGatewayConnectionDetails().url, 'ws://127.0.0.1:8080');
  assert.deepEqual(await compat.resolveGatewayConnectionAuth(), { token: 'secret' });
});
