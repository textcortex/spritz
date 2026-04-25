import { afterEach, describe, it, expect, vi } from 'vite-plus/test';
import { render, screen } from '@testing-library/react';
import { MemoryRouter, Routes, Route } from 'react-router-dom';
import { ConfigProvider, config } from '@/lib/config';
import { NoticeProvider } from '@/components/notice-banner';
import * as AppModule from '@/App';
import { buildLegacySlackGatewayRedirectURL, inferBrowserRouterBasename } from '@/App';

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
vi.mock('@/pages/settings', () => ({
  SettingsPage: () => <div data-testid="settings-page">Settings Page</div>,
}));
vi.mock('@/components/layout', () => ({
  Layout: () => {
    const { Outlet } = require('react-router-dom');
    return <Outlet />;
  },
}));

afterEach(() => {
  delete window.SPRITZ_CONFIG;
  vi.restoreAllMocks();
});

function renderAtRoute(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <ConfigProvider value={config}>
        <NoticeProvider>
          <Routes>
            <Route index element={<div data-testid="chat-page">Chat Page</div>} />
            <Route path="create" element={<div data-testid="create-page">Create Page</div>} />
            <Route path="terminal/:name" element={<div data-testid="terminal-page">Terminal Page</div>} />
            <Route path="c/*" element={<div data-testid="chat-page">Chat Page</div>} />
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

  it('renders ChatPage at /c/some-name/some-conversation (same route, no remount)', () => {
    renderAtRoute('/c/some-name/some-conversation');
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

  it('infers a router basename for prefixed app routes', () => {
    expect(inferBrowserRouterBasename('/app/settings/slack/workspaces')).toBe('/app');
    expect(inferBrowserRouterBasename('/settings/slack/workspaces')).toBeUndefined();
    expect(inferBrowserRouterBasename('/app/c/some-name')).toBe('/app');
    expect(inferBrowserRouterBasename('/app/chat/some-name', '/chat')).toBe('/app');
    expect(inferBrowserRouterBasename('/spritz/slack-gateway/settings/channels')).toBeUndefined();
    expect(inferBrowserRouterBasename('/app/spritz/slack-gateway/settings/channels')).toBe('/app');
  });

  it('infers a router basename at a configured prefixed app root', () => {
    expect(inferBrowserRouterBasename('/spritz/', '/c', '/spritz/slack-gateway')).toBe('/spritz');
    expect(inferBrowserRouterBasename('/spritz', '/c', 'https://spritz.example.test/spritz/slack-gateway')).toBe('/spritz');
    expect(inferBrowserRouterBasename('/spritz/', '/c', '/slack-gateway', '/spritz/api')).toBe('/spritz');
  });

  it('renders settings routes below an inferred basename', async () => {
    vi.resetModules();
    window.history.pushState({}, '', '/app/settings/slack/workspaces');

    const { App } = await import('@/App');
    render(<App />);

    expect(screen.getByTestId('settings-page')).toBeDefined();
  });

  it('maps legacy Slack gateway SPA paths to the real gateway URL', () => {
    expect(
      buildLegacySlackGatewayRedirectURL(
        '/spritz/slack-gateway/slack/workspaces',
        '?teamId=T123',
        '#details',
      ),
    ).toBe('/slack-gateway/slack/workspaces?teamId=T123#details');
  });

  it('redirects legacy Slack gateway routes instead of rendering a blank page', () => {
    const replaceSpy = vi
      .spyOn(AppModule.browserLocation, 'replace')
      .mockImplementation(() => undefined);
    window.history.pushState({}, '', '/spritz/slack-gateway/settings/channels');

    render(<AppModule.App />);

    expect(replaceSpy).toHaveBeenCalledWith('/slack-gateway/settings/channels');
    replaceSpy.mockRestore();
  });

  it('forces document navigation when the gateway path matches the legacy SPA path', async () => {
    vi.resetModules();
    window.SPRITZ_CONFIG = {
      slackGatewayBasePath: '/spritz/slack-gateway',
    };
    const runtimeAppModule = await import('@/App');
    const replaceSpy = vi
      .spyOn(runtimeAppModule.browserLocation, 'replace')
      .mockImplementation(() => undefined);
    window.history.pushState({}, '', '/spritz/slack-gateway/slack/workspaces');

    render(<runtimeAppModule.App />);

    expect(replaceSpy).toHaveBeenCalledWith('/spritz/slack-gateway/slack/workspaces');
  });
});
