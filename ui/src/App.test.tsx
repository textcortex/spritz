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
            <Route path="chat/:name?" element={<div data-testid="chat-page">Chat Page</div>} />
          </Routes>
        </NoticeProvider>
      </ConfigProvider>
    </MemoryRouter>,
  );
}

describe('App routing', () => {
  it('renders ChatPage at /', () => {
    renderAtRoute('/');
    expect(screen.getByTestId('chat-page')).toBeInTheDocument();
  });

  it('renders CreatePage at /create', () => {
    renderAtRoute('/create');
    expect(screen.getByTestId('create-page')).toBeInTheDocument();
  });

  it('renders ChatPage at /chat/some-name', () => {
    renderAtRoute('/chat/some-name');
    expect(screen.getByTestId('chat-page')).toBeInTheDocument();
  });

  it('renders TerminalPage at /terminal/my-spritz', () => {
    renderAtRoute('/terminal/my-spritz');
    expect(screen.getByTestId('terminal-page')).toBeInTheDocument();
  });
});
