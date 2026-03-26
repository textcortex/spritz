import { describe, it, expect } from 'vite-plus/test';
import { normalizeHtmlErrorText, summarizeHtmlErrorDocument } from './html-error';

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

describe('summarizeHtmlErrorDocument', () => {
  it('reduces full HTML error pages into a short summary', () => {
    expect(summarizeHtmlErrorDocument(CLOUDFLARE_525_HTML)).toBe(
      'HTTP 525 · example.com | 525: SSL handshake failed · example.com · Cloudflare',
    );
  });

  it('returns null for normal non-HTML text', () => {
    expect(summarizeHtmlErrorDocument('plain text error')).toBeNull();
  });
});

describe('normalizeHtmlErrorText', () => {
  it('leaves non-HTML text alone', () => {
    expect(normalizeHtmlErrorText('plain text error')).toBe('plain text error');
  });
});
