import { beforeEach, describe, expect, it, vi } from 'vite-plus/test';

vi.mock('./config', () => ({
  config: {
    apiBaseUrl: 'https://api.example.com',
    websocketBaseUrl: '',
    chatPathPrefix: '/c',
    ownerId: '',
    presets: [],
    repoDefaults: { url: '', dir: '', branch: '', hideInputs: '' },
    launch: { queryParams: '' },
    branding: {
      productName: '',
      logoUrl: '',
      faviconUrl: '',
      theme: {
        background: '',
        foreground: '',
        muted: '',
        mutedForeground: '',
        primary: '',
        primaryForeground: '',
        border: '',
        destructive: '',
        radius: '',
      },
      terminal: {
        background: '',
        foreground: '',
        cursor: '',
      },
    },
    auth: {
      mode: '',
      tokenStorage: 'localStorage',
      tokenStorageKeys: '',
      bearerTokenParam: 'token',
      loginUrl: '',
      returnToMode: 'auto',
      returnToParam: '',
      redirectOnUnauthorized: 'false',
      refresh: {
        enabled: 'false',
        url: '',
        method: 'POST',
        credentials: 'include',
        tokenStorageKeys: '',
        timeoutMs: '5000',
        cooldownMs: '30000',
        headers: '',
      },
    },
  },
}));

const CLOUDFLARE_525_HTML = `<!DOCTYPE html>
<html lang="en-US">
  <head>
    <title>example.com | 525: SSL handshake failed</title>
  </head>
  <body>
    <div>Cloudflare is unable to establish an SSL connection to the origin server.</div>
    <div>Error code 525</div>
  </body>
</html>`;

describe('request', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('summarizes HTML error responses instead of returning raw markup', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response(CLOUDFLARE_525_HTML, {
        status: 525,
        statusText: 'SSL handshake failed',
        headers: { 'Content-Type': 'text/html' },
      })),
    );

    const { request } = await import('./api');

    await expect(request('/status')).rejects.toThrow(
      'HTTP 525 · example.com | 525: SSL handshake failed · example.com · Cloudflare',
    );

    try {
      await request('/status');
    } catch (error) {
      expect(String(error)).not.toContain('<!DOCTYPE html>');
    }
  });
});
