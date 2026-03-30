import { describe, it, expect, beforeEach, vi } from 'vite-plus/test';
import { act, render, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { ConfigProvider, resolveConfig } from '@/lib/config';
import { TerminalPage } from './terminal';
import { FakeWebSocket } from '@/test/helpers';

function createApiError(message: string, status: number) {
  const error = new Error(message) as Error & { status: number };
  error.status = status;
  return error;
}

const {
  terminalConstructor,
  fitAddonConstructor,
  requestMock,
  getAuthTokenMock,
  setAuthToken,
  emitTerminalData,
  setOnDataHandler,
} = vi.hoisted(() => {
  let authToken = '';
  let onDataHandler: ((data: string) => void) | null = null;
  return {
    terminalConstructor: vi.fn(),
    fitAddonConstructor: vi.fn(),
    requestMock: vi.fn(),
    getAuthTokenMock: () => authToken,
    setAuthToken: (value: string) => {
      authToken = value;
    },
    emitTerminalData: (data: string) => {
      onDataHandler?.(data);
    },
    setOnDataHandler: (handler: ((data: string) => void) | null) => {
      onDataHandler = handler;
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
      onData: vi.fn((handler: (data: string) => void) => {
        setOnDataHandler(handler);
        return {
          dispose: vi.fn(),
        };
      }),
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
  request: requestMock,
  getAuthToken: getAuthTokenMock,
}));

describe('TerminalPage branding', () => {
  let lastSocket: FakeWebSocket | null = null;
  let sockets: FakeWebSocket[] = [];
  let deferCloseEvents = false;

  beforeEach(() => {
    terminalConstructor.mockReset();
    fitAddonConstructor.mockReset();
    requestMock.mockReset();
    setAuthToken('');
    setOnDataHandler(null);
    lastSocket = null;
    sockets = [];
    deferCloseEvents = false;
    requestMock.mockImplementation((path: string, options?: { method?: string; body?: string }) => {
      if (path === '/spritzes/example-instance/terminal/connect-ticket' && options?.method === 'POST') {
        const payload = options?.body ? JSON.parse(options.body) as { session?: string } : {};
        const connectPath = payload?.session
          ? `/api/spritzes/example-instance/terminal?session=${encodeURIComponent(payload.session)}`
          : '/api/spritzes/example-instance/terminal';
        return Promise.resolve({
          type: 'connect-ticket',
          ticket: 'ticket-123',
          expiresAt: '2026-03-30T12:34:56Z',
          protocol: 'spritz-terminal.v1',
          connectPath,
        });
      }
      return Promise.resolve({});
    });
    Object.defineProperty(globalThis, 'WebSocket', {
      value: class extends FakeWebSocket {
        constructor(url: string, protocols?: string | string[]) {
          super(url, protocols);
          sockets.push(this);
          lastSocket = this;
        }

        close() {
          this.readyState = FakeWebSocket.CLOSED;
          const fireClose = () => this.onclose?.(new CloseEvent('close'));
          if (deferCloseEvents) {
            queueMicrotask(fireClose);
            return;
          }
          fireClose();
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

  it('uses a terminal connect ticket on the current host by default', async () => {
    setAuthToken('external-ui-token');
    const config = resolveConfig({
      apiBaseUrl: '/api',
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

    await waitFor(() => {
      expect(lastSocket?.url).toBe(
        'ws://localhost:3000/api/spritzes/example-instance/terminal',
      );
      expect(lastSocket?.protocols).toEqual([
        'spritz-terminal.v1',
        'spritz-ticket.v1.ticket-123',
      ]);
    });
  });

  it('uses an explicit websocket base url with terminal connect tickets for cross-host connections', async () => {
    setAuthToken('external-ui-token');
    const config = resolveConfig({
      apiBaseUrl: 'https://spritz.example.com/api',
      websocketBaseUrl: 'https://spritz.example.com/api',
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

    await waitFor(() => {
      expect(lastSocket?.url).toBe(
        'wss://spritz.example.com/api/spritzes/example-instance/terminal',
      );
      expect(lastSocket?.protocols).toEqual([
        'spritz-terminal.v1',
        'spritz-ticket.v1.ticket-123',
      ]);
    });
  });

  it('backs off before minting a fresh terminal connect ticket when the initial socket closes before opening', async () => {
    setAuthToken('expired-token');

    const config = resolveConfig({
      apiBaseUrl: 'https://spritz.example.com/api',
      websocketBaseUrl: 'https://spritz.example.com/api',
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

    await waitFor(() => {
      expect(lastSocket?.url).toBe(
        'wss://spritz.example.com/api/spritzes/example-instance/terminal',
      );
      expect(lastSocket?.protocols).toEqual([
        'spritz-terminal.v1',
        'spritz-ticket.v1.ticket-123',
      ]);
    });

    vi.useFakeTimers();
    act(() => {
      lastSocket?.close();
    });

    expect(requestMock.mock.calls.filter(([path]) => path === '/spritzes/example-instance/terminal/connect-ticket')).toHaveLength(1);

    act(() => {
      vi.advanceTimersByTime(2999);
    });

    expect(requestMock.mock.calls.filter(([path]) => path === '/spritzes/example-instance/terminal/connect-ticket')).toHaveLength(1);

    act(() => {
      vi.advanceTimersByTime(1);
    });
    vi.useRealTimers();

    await waitFor(() => {
      expect(requestMock.mock.calls.filter(([path]) => path === '/spritzes/example-instance/terminal/connect-ticket')).toHaveLength(2);
      expect(lastSocket?.url).toBe('wss://spritz.example.com/api/spritzes/example-instance/terminal');
    });
  });

  it('retries terminal connect-ticket failures after backoff', async () => {
    setAuthToken('external-ui-token');
    let ticketAttempts = 0;
    requestMock.mockImplementation((path: string, options?: { method?: string; body?: string }) => {
      if (path === '/spritzes/example-instance/terminal/connect-ticket' && options?.method === 'POST') {
        ticketAttempts += 1;
        if (ticketAttempts === 1) {
          return Promise.reject(createApiError('Terminal not ready.', 409));
        }
        const payload = options?.body ? JSON.parse(options.body) as { session?: string } : {};
        const connectPath = payload?.session
          ? `/api/spritzes/example-instance/terminal?session=${encodeURIComponent(payload.session)}`
          : '/api/spritzes/example-instance/terminal';
        return Promise.resolve({
          type: 'connect-ticket',
          ticket: 'ticket-123',
          expiresAt: '2026-03-30T12:34:56Z',
          protocol: 'spritz-terminal.v1',
          connectPath,
        });
      }
      return Promise.resolve({});
    });

    const config = resolveConfig({
      apiBaseUrl: 'https://spritz.example.com/api',
      websocketBaseUrl: 'https://spritz.example.com/api',
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

    await waitFor(() => {
      expect(ticketAttempts).toBe(1);
    });

    await waitFor(() => {
      expect(ticketAttempts).toBe(2);
      expect(lastSocket?.url).toBe('wss://spritz.example.com/api/spritzes/example-instance/terminal');
      expect(lastSocket?.protocols).toEqual([
        'spritz-terminal.v1',
        'spritz-ticket.v1.ticket-123',
      ]);
    }, { timeout: 5000 });
  });

  it('keeps the active terminal socket when an earlier socket closes late', async () => {
    deferCloseEvents = true;
    setAuthToken('external-ui-token');

    const firstConfig = resolveConfig({
      apiBaseUrl: 'https://spritz.example.com/api',
      websocketBaseUrl: 'https://first.example.com/api',
      auth: {
        mode: 'bearer',
        tokenStorageKeys: 'spritz-token',
      },
    });

    const secondConfig = resolveConfig({
      apiBaseUrl: 'https://spritz.example.com/api',
      websocketBaseUrl: 'https://second.example.com/api',
      auth: {
        mode: 'bearer',
        tokenStorageKeys: 'spritz-token',
      },
    });

    const view = render(
      <MemoryRouter initialEntries={['/terminal/example-instance']}>
        <ConfigProvider value={firstConfig}>
          <Routes>
            <Route path="/terminal/:name" element={<TerminalPage />} />
          </Routes>
        </ConfigProvider>
      </MemoryRouter>,
    );

    await waitFor(() => {
      expect(sockets[0]?.url).toBe(
        'wss://first.example.com/api/spritzes/example-instance/terminal',
      );
      expect(sockets[0]?.protocols).toEqual([
        'spritz-terminal.v1',
        'spritz-ticket.v1.ticket-123',
      ]);
    });

    view.rerender(
      <MemoryRouter initialEntries={['/terminal/example-instance']}>
        <ConfigProvider value={secondConfig}>
          <Routes>
            <Route path="/terminal/:name" element={<TerminalPage />} />
          </Routes>
        </ConfigProvider>
      </MemoryRouter>,
    );

    await waitFor(() => {
      expect(sockets[1]?.url).toBe(
        'wss://second.example.com/api/spritzes/example-instance/terminal',
      );
      expect(sockets[1]?.protocols).toEqual([
        'spritz-terminal.v1',
        'spritz-ticket.v1.ticket-123',
      ]);
    });

    await act(async () => {
      await Promise.resolve();
    });

    act(() => {
      sockets[1]?.simulateOpen();
    });

    act(() => {
      emitTerminalData('pwd\n');
    });

    expect(sockets[1]?.sent).toContain('pwd\n');
  });
});
