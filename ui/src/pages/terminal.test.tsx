import { describe, it, expect, beforeEach, vi } from 'vite-plus/test';
import { act, render, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { ConfigProvider, resolveConfig } from '@/lib/config';
import { TerminalPage } from './terminal';
import { FakeWebSocket } from '@/test/helpers';

const {
  terminalConstructor,
  fitAddonConstructor,
  getAuthTokenMock,
  setAuthToken,
  refreshAuthTokenForWebSocketMock,
  setRefreshAuthResult,
} = vi.hoisted(() => {
  let authToken = '';
  let refreshResult = { token: '', refreshed: false };
  return {
    terminalConstructor: vi.fn(),
    fitAddonConstructor: vi.fn(),
    getAuthTokenMock: () => authToken,
    setAuthToken: (value: string) => {
      authToken = value;
    },
    refreshAuthTokenForWebSocketMock: vi.fn(async () => refreshResult),
    setRefreshAuthResult: (value: { token: string; refreshed: boolean }) => {
      refreshResult = value;
    },
  };
});

vi.mock('@xterm/xterm', () => ({
  Terminal: function MockTerminal(options: unknown) {
    terminalConstructor(options);
    return {
      loadAddon: vi.fn(),
      open: vi.fn(),
      write: vi.fn(),
      onData: vi.fn(() => ({ dispose: vi.fn() })),
      onBinary: vi.fn(() => ({ dispose: vi.fn() })),
      onResize: vi.fn(() => ({ dispose: vi.fn() })),
      dispose: vi.fn(),
    };
  },
}));

vi.mock('@xterm/addon-fit', () => ({
  FitAddon: function MockFitAddon() {
    fitAddonConstructor();
    return {
      fit: vi.fn(),
      proposeDimensions: vi.fn(() => ({ cols: 80, rows: 24 })),
    };
  },
}));

vi.mock('@/lib/api', () => ({
  getAuthToken: getAuthTokenMock,
  refreshAuthTokenForWebSocket: refreshAuthTokenForWebSocketMock,
  authBearerTokenParam: 'token',
}));

describe('TerminalPage branding', () => {
  let lastSocket: FakeWebSocket | null = null;

  beforeEach(() => {
    terminalConstructor.mockReset();
    fitAddonConstructor.mockReset();
    refreshAuthTokenForWebSocketMock.mockClear();
    setAuthToken('');
    setRefreshAuthResult({ token: '', refreshed: false });
    Object.defineProperty(globalThis, 'WebSocket', {
      value: class extends FakeWebSocket {
        constructor(url: string) {
          super(url);
          lastSocket = this;
        }
      },
      writable: true,
    });
  });

  it('passes branded terminal colors into xterm', () => {
    const config = resolveConfig({
      branding: {
        terminal: {
          background: '#101820',
          foreground: '#f5f5f5',
          cursor: '#ff6b00',
        },
      },
    });

    render(
      <MemoryRouter initialEntries={['/terminal/example-instance']}>
        <ConfigProvider value={config}>
          <Routes>
            <Route path="/terminal/:name" element={<TerminalPage />} />
          </Routes>
        </ConfigProvider>
      </MemoryRouter>,
    );

    expect(terminalConstructor).toHaveBeenCalledWith(expect.objectContaining({
      theme: expect.objectContaining({
        background: '#101820',
        foreground: '#f5f5f5',
        cursor: '#ff6b00',
      }),
    }));
    expect(fitAddonConstructor).toHaveBeenCalled();
  });

  it('uses the configured absolute api host and bearer token for terminal websocket connections', () => {
    setAuthToken('external-ui-token');
    const config = resolveConfig({
      apiBaseUrl: 'https://spritz.example.com/api',
      auth: {
        mode: 'bearer',
        tokenStorageKeys: 'spritz-token',
      },
    });

    render(
      <MemoryRouter initialEntries={['/terminal/example-instance']}>
        <ConfigProvider value={config}>
          <Routes>
            <Route path="/terminal/:name" element={<TerminalPage />} />
          </Routes>
        </ConfigProvider>
      </MemoryRouter>,
    );

    expect(lastSocket?.url).toBe(
      'wss://spritz.example.com/api/spritzes/example-instance/terminal?token=external-ui-token',
    );
  });

  it('refreshes bearer auth and reconnects when the initial terminal websocket closes before opening', async () => {
    setAuthToken('expired-token');
    setRefreshAuthResult({ token: 'refreshed-token', refreshed: true });
    refreshAuthTokenForWebSocketMock.mockImplementation(async () => {
      setAuthToken('refreshed-token');
      return { token: 'refreshed-token', refreshed: true };
    });

    const config = resolveConfig({
      apiBaseUrl: 'https://spritz.example.com/api',
      auth: {
        mode: 'bearer',
        tokenStorageKeys: 'spritz-token',
        refresh: {
          enabled: 'true',
          url: '/oauth/refresh',
          tokenStorageKeys: 'spritz-refresh-token',
        },
      },
    });

    render(
      <MemoryRouter initialEntries={['/terminal/example-instance']}>
        <ConfigProvider value={config}>
          <Routes>
            <Route path="/terminal/:name" element={<TerminalPage />} />
          </Routes>
        </ConfigProvider>
      </MemoryRouter>,
    );

    expect(lastSocket?.url).toBe(
      'wss://spritz.example.com/api/spritzes/example-instance/terminal?token=expired-token',
    );

    act(() => {
      lastSocket?.close();
    });

    await waitFor(() => {
      expect(refreshAuthTokenForWebSocketMock).toHaveBeenCalledTimes(1);
      expect(lastSocket?.url).toBe(
        'wss://spritz.example.com/api/spritzes/example-instance/terminal?token=refreshed-token',
      );
    });
  });
});
