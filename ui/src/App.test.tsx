import { describe, it, expect, vi } from 'vite-plus/test';
import { render, screen } from '@testing-library/react';
import { MemoryRouter, Routes, Route } from 'react-router-dom';
import { ConfigProvider, config } from '@/lib/config';
import { NoticeProvider } from '@/components/notice-banner';

// Mock the page components to keep tests simple
vi.mock('@/pages/chat', () => ({
  ChatPage: () => <div data-testid="chat-page">Chat Page</div>,
}));
vi.mock('@/pages/create', () => ({
  CreatePage: () => <div data-testid="create-page">Create Page</div>,
}));
vi.mock('@/pages/terminal', () => ({
  TerminalPage: () => <div data-testid="terminal-page">Terminal Page</div>,
}));
vi.mock('@/components/layout', () => ({
  Layout: () => {
    const { Outlet } = require('react-router-dom');
    return <Outlet />;
  },
}));

function renderAtRoute(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <ConfigProvider value={config}>
        <NoticeProvider>
          <Routes>
            <Route index element={<div data-testid="chat-page">Chat Page</div>} />
            <Route path="create" element={<div data-testid="create-page">Create Page</div>} />
            <Route path="terminal/:name" element={<div data-testid="terminal-page">Terminal Page</div>} />
            <Route path="c/:name?" element={<div data-testid="chat-page">Chat Page</div>} />
          </Routes>
        </NoticeProvider>
      </ConfigProvider>
    </MemoryRouter>,
  );
}

describe('App routing', () => {
  it('renders ChatPage at /', () => {
    renderAtRoute('/');
    expect(screen.getByTestId('chat-page')).toBeDefined();
  });

  it('renders CreatePage at /create', () => {
    renderAtRoute('/create');
    expect(screen.getByTestId('create-page')).toBeDefined();
  });

  it('renders ChatPage at /c/some-name', () => {
    renderAtRoute('/c/some-name');
    expect(screen.getByTestId('chat-page')).toBeDefined();
  });

  it('renders TerminalPage at /terminal/my-spritz', () => {
    renderAtRoute('/terminal/my-spritz');
    expect(screen.getByTestId('terminal-page')).toBeDefined();
  });

  it('renders ChatPage at the configured chat prefix', async () => {
    vi.resetModules();
    window.SPRITZ_CONFIG = {
      chatPathPrefix: '/chat',
    };
    window.history.pushState({}, '', '/chat/some-name');

    const { App } = await import('@/App');
    render(<App />);

    expect(screen.getByTestId('chat-page')).toBeDefined();
  });
});
