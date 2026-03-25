import test from 'node:test';
import assert from 'node:assert/strict';

import { rewriteConnectFrameAsTrustedProxyControlUi } from './acp-server.mjs';

test('trusted-proxy control-ui rewrite preserves auth and device payloads', () => {
  const original = {
    type: 'req',
    method: 'connect',
    params: {
      client: {
        id: 'openclaw-gateway-client',
        mode: 'backend',
      },
      auth: {
        token: 'device-token',
      },
      device: {
        id: 'device-123',
        publicKey: 'pubkey',
        signature: 'sig',
        signedAt: 123,
        nonce: 'nonce-1',
      },
    },
  };

  const rewritten = JSON.parse(
    rewriteConnectFrameAsTrustedProxyControlUi(Buffer.from(JSON.stringify(original), 'utf8'))
      .toString('utf8'),
  );

  assert.equal(rewritten.params.client.id, 'openclaw-control-ui');
  assert.equal(rewritten.params.client.mode, 'webchat');
  assert.deepEqual(rewritten.params.auth, original.params.auth);
  assert.deepEqual(rewritten.params.device, original.params.device);
});

test('trusted-proxy control-ui rewrite ignores non-connect frames', () => {
  const original = Buffer.from(
    JSON.stringify({
      type: 'req',
      method: 'chat.send',
      params: { text: 'hello' },
    }),
    'utf8',
  );

  const rewritten = rewriteConnectFrameAsTrustedProxyControlUi(original);
  assert.deepEqual(rewritten, original);
});
