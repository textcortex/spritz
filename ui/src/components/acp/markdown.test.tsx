import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vite-plus/test';
import { Markdown } from './markdown';

vi.mock('flowtoken', () => ({
  AnimatedMarkdown: ({ content }: { content: string }) => <div data-testid="animated-markdown">{content}</div>,
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

describe('Markdown', () => {
  it('renders a summarized message for HTML error documents', () => {
    render(<Markdown content={CLOUDFLARE_525_HTML} />);

    const rendered = screen.getByTestId('animated-markdown');
    expect(rendered.textContent).toContain(
      'HTTP 525 · example.com | 525: SSL handshake failed · example.com · Cloudflare',
    );
    expect(rendered.textContent).not.toContain('<!DOCTYPE html>');
  });
});
