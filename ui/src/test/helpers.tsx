import { type ReactElement } from 'react';
import { render } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { ConfigProvider, type RawSpritzConfig, type SpritzConfig, resolveConfig } from '@/lib/config';
import { NoticeProvider } from '@/components/notice-banner';

const DEFAULT_TEST_CONFIG: SpritzConfig = resolveConfig();

export function renderWithProviders(
  ui: ReactElement,
  options?: {
    config?: RawSpritzConfig;
    initialEntries?: string[];
  },
) {
  const config = resolveConfig({ ...DEFAULT_TEST_CONFIG, ...options?.config });
  return render(
    <MemoryRouter initialEntries={options?.initialEntries || ['/']}>
      <ConfigProvider value={config}>
        <NoticeProvider>{ui}</NoticeProvider>
      </ConfigProvider>
    </MemoryRouter>,
  );
}

export function createMockStorage(): Storage {
  const store = new Map<string, string>();
  return {
    get length() {
      return store.size;
    },
    clear() {
      store.clear();
    },
    getItem(key: string) {
      return store.get(key) ?? null;
    },
    key(index: number) {
      return [...store.keys()][index] ?? null;
    },
    removeItem(key: string) {
      store.delete(key);
    },
    setItem(key: string, value: string) {
      store.set(key, value);
    },
  };
}

export class FakeWebSocket {
  static CONNECTING = 0;
  static OPEN = 1;
  static CLOSING = 2;
  static CLOSED = 3;

  CONNECTING = 0;
  OPEN = 1;
  CLOSING = 2;
  CLOSED = 3;

  readyState = FakeWebSocket.CONNECTING;
  url: string;
  protocols: string[];
  sent: string[] = [];

  onopen: ((event: Event) => void) | null = null;
  onmessage: ((event: MessageEvent) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  onclose: ((event: CloseEvent) => void) | null = null;

  constructor(url: string, protocols?: string | string[]) {
    this.url = url;
    if (Array.isArray(protocols)) {
      this.protocols = protocols;
    } else if (typeof protocols === 'string' && protocols.trim()) {
      this.protocols = [protocols];
    } else {
      this.protocols = [];
    }
  }

  autoRespond = false;

  send(data: string) {
    if (this.readyState !== FakeWebSocket.OPEN) {
      throw new Error('WebSocket is not open');
    }
    this.sent.push(data);
    if (this.autoRespond) {
      try {
        const msg = JSON.parse(data);
        if (msg.id !== undefined && msg.method) {
          // Auto-respond to RPC requests with a success result
          queueMicrotask(() => this.simulateMessage({ jsonrpc: '2.0', id: msg.id, result: {} }));
        }
      } catch { /* ignore */ }
    }
  }

  close() {
    this.readyState = FakeWebSocket.CLOSED;
    this.onclose?.(new CloseEvent('close'));
  }

  // Test helpers
  simulateOpen() {
    this.readyState = FakeWebSocket.OPEN;
    this.onopen?.(new Event('open'));
  }

  simulateMessage(data: unknown) {
    this.onmessage?.(new MessageEvent('message', { data: JSON.stringify(data) }));
  }

  simulateError() {
    this.onerror?.(new Event('error'));
  }
}
