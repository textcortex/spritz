import { describe, it, expect, beforeEach, vi } from 'vite-plus/test';
import { render } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { ConfigProvider, resolveConfig } from '@/lib/config';
import { TerminalPage } from './terminal';
import { FakeWebSocket } from '@/test/helpers';

const { terminalConstructor, fitAddonConstructor } = vi.hoisted(() => ({
  terminalConstructor: vi.fn(),
  fitAddonConstructor: vi.fn(),
}));

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
  getAuthToken: () => '',
  authBearerTokenParam: 'token',
}));

describe('TerminalPage branding', () => {
  beforeEach(() => {
    terminalConstructor.mockReset();
    fitAddonConstructor.mockReset();
    Object.defineProperty(globalThis, 'WebSocket', {
      value: FakeWebSocket,
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
});
