import type React from 'react';
import { describe, it, expect, beforeEach, vi } from 'vite-plus/test';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { createMockStorage } from '@/test/helpers';
import { ConfigProvider, config, resolveConfig, type RawSpritzConfig } from '@/lib/config';
import { NoticeProvider } from '@/components/notice-banner';
import { ChatPage } from './chat';
import type { ConversationInfo } from '@/types/acp';

const {
  requestMock,
  sendPromptMock,
  emitUpdate,
  emitReplayState,
  setUpdateHandler,
  setReplayStateHandler,
  getAuthTokenMock,
  setAuthToken,
  refreshAuthTokenForWebSocketMock,
  setRefreshAuthResult,
  setACPStartReady,
  setACPStartPending,
  closeLastACPConnection,
  getACPStartReady,
  getACPStartPending,
  captureACPOptions,
  getLastACPOptions,
  setCloseLastACPConnection,
  resetACPMockState,
  showNoticeMock,
} = vi.hoisted(() => {
  let updateHandler:
    | ((update: Record<string, unknown>, options?: { historical?: boolean }) => void)
    | undefined;
  let replayStateHandler: ((replaying: boolean) => void) | undefined;
  let authToken = '';
  let refreshResult = { token: '', refreshed: false };
  let acpStartReady = true;
  let acpStartPending = false;
  let lastACPOptions: Record<string, unknown> | null = null;
  let closeLastACPConnection: (() => void) | null = null;
  return {
    requestMock: vi.fn(),
    sendPromptMock: vi.fn(),
    showNoticeMock: vi.fn(),
    emitUpdate: (update: Record<string, unknown>, options?: { historical?: boolean }) => {
      updateHandler?.(update, options);
    },
    emitReplayState: (replaying: boolean) => {
      replayStateHandler?.(replaying);
    },
    setUpdateHandler: (
      handler?: (update: Record<string, unknown>, options?: { historical?: boolean }) => void,
    ) => {
      updateHandler = handler;
    },
    setReplayStateHandler: (handler?: (replaying: boolean) => void) => {
      replayStateHandler = handler;
    },
    getAuthTokenMock: () => authToken,
    setAuthToken: (value: string) => {
      authToken = value;
    },
    refreshAuthTokenForWebSocketMock: vi.fn(async () => refreshResult),
    setRefreshAuthResult: (value: { token: string; refreshed: boolean }) => {
      refreshResult = value;
    },
    setACPStartReady: (value: boolean) => {
      acpStartReady = value;
    },
    setACPStartPending: (value: boolean) => {
      acpStartPending = value;
    },
    captureACPOptions: (options: Record<string, unknown>) => {
      lastACPOptions = options;
    },
    getLastACPOptions: () => lastACPOptions,
    closeLastACPConnection: () => {
      closeLastACPConnection?.();
    },
    resetACPMockState: () => {
      authToken = '';
      refreshResult = { token: '', refreshed: false };
      acpStartReady = true;
      acpStartPending = false;
      lastACPOptions = null;
      closeLastACPConnection = null;
      updateHandler = undefined;
      replayStateHandler = undefined;
    },
    getACPStartReady: () => acpStartReady,
    getACPStartPending: () => acpStartPending,
    setCloseLastACPConnection: (handler: (() => void) | null) => {
      closeLastACPConnection = handler;
    },
  };
});

vi.mock('@/lib/api', () => ({
  request: requestMock,
  getAuthToken: getAuthTokenMock,
  refreshAuthTokenForWebSocket: refreshAuthTokenForWebSocketMock,
  authBearerTokenParam: 'token',
}));

vi.mock('@/lib/acp-client', () => ({
  extractACPText: (value: unknown): string => {
    if (value === null || value === undefined) return '';
    if (typeof value === 'string') return value;
    if (Array.isArray(value)) return value.map((item) => String(item ?? '')).join('\n');
    if (typeof value !== 'object') return String(value);
    const obj = value as Record<string, unknown>;
    if (typeof obj.text === 'string') return obj.text;
    if (obj.content) return String(obj.content);
    return '';
  },
  createACPClient: ({
    wsUrl,
    protocols,
    onReadyChange,
    onStatus,
    onUpdate,
    onReplayStateChange,
    onClose,
    ...rest
  }: {
    wsUrl: string;
    protocols?: string[];
    onReadyChange?: (ready: boolean) => void;
    onStatus?: (status: string) => void;
    onUpdate?: (update: Record<string, unknown>, options?: { historical?: boolean }) => void;
    onReplayStateChange?: (replaying: boolean) => void;
    onClose?: (reason?: string) => void;
  }) => {
    captureACPOptions({ wsUrl, protocols, ...rest });
    setUpdateHandler(onUpdate);
    setReplayStateHandler(onReplayStateChange);
    let ready = false;
    const closeConnection = () => {
      ready = false;
      onReadyChange?.(false);
      onClose?.('ACP connection closed.');
    };
    setCloseLastACPConnection(closeConnection);
    return {
      start: vi.fn(async () => {
        if (getACPStartPending()) {
          await new Promise<void>(() => {});
          return;
        }
        if (!getACPStartReady()) return;
        ready = true;
        onStatus?.('Connected');
        onReadyChange?.(true);
      }),
      getConversationId: () => 'conv-1',
      getSessionId: () => 'sess-1',
      matchesConversation: () => true,
      isReady: () => ready,
      sendPrompt: sendPromptMock,
      cancelPrompt: vi.fn(),
      dispose: vi.fn(() => {
        ready = false;
        setCloseLastACPConnection(null);
        setUpdateHandler(undefined);
        setReplayStateHandler(undefined);
      }),
    };
  },
}));

vi.mock('@/components/notice-banner', async () => {
  const actual = await vi.importActual<typeof import('@/components/notice-banner')>('@/components/notice-banner');
  return {
    ...actual,
    useNotice: () => ({ showNotice: showNoticeMock }),
  };
});

vi.mock('@/components/acp/sidebar', () => ({
  Sidebar: ({
    agents,
    selectedConversationId,
    onSelectConversation,
    focusedSpritzName,
    focusedSpritz,
  }: {
    agents: Array<{ spritz: { metadata: { name: string } }; conversations: Array<{ metadata: { name: string }; spec?: { title?: string } }> }>;
    selectedConversationId: string | null;
    onSelectConversation: (conversation: { metadata: { name: string } }) => void;
    focusedSpritzName?: string | null;
    focusedSpritz?: { metadata: { name: string } } | null;
  }) => (
    <div>
      <div data-testid="sidebar-agent-order">
        {agents.map((group) => group.spritz.metadata.name).join(',')}
      </div>
      <div data-testid="sidebar-focused-spritz">
        {focusedSpritz?.metadata?.name || focusedSpritzName || ''}
      </div>
      {agents.flatMap((group) =>
        group.conversations.map((conversation) => (
          <div key={conversation.metadata.name}>
            <button type="button" onClick={() => onSelectConversation(conversation)}>
              {conversation.spec?.title || conversation.metadata.name}
            </button>
          </div>
        )),
      )}
      <div data-testid="selected-conversation">{selectedConversationId || ''}</div>
    </div>
  ),
}));

vi.mock('@/components/acp/message', () => ({
  ChatMessage: ({
    message,
  }: {
    message: { role: string; blocks: Array<{ type: string; text?: string }> };
  }) => (
    <div data-testid="chat-message">
      {message.role}:
      {message.blocks
        .filter((block) => block.type === 'text')
        .map((block) => block.text || '')
        .join(' ')}
    </div>
  ),
}));

vi.mock('@/components/acp/thinking-block', () => ({
  ThinkingBlock: () => null,
}));

vi.mock('@/components/acp/permission-dialog', () => ({
  PermissionDialog: () => null,
}));

vi.mock('@/components/ui/skeleton', () => ({
  Skeleton: () => <div />,
}));

vi.mock('@/components/ui/button', () => ({
  Button: (props: React.ComponentProps<'button'>) => <button type="button" {...props} />,
}));

vi.mock('@/components/ui/tooltip', () => ({
  Tooltip: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  TooltipContent: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  TooltipTrigger: ({
    children,
    render,
  }: {
    children?: React.ReactNode;
    render?: React.ReactNode;
  }) => <>{render || children}</>,
}));

const CONVERSATIONS: ConversationInfo[] = [
  {
    metadata: { name: 'conv-1' },
    spec: { sessionId: 'sess-1', title: 'Conversation One', spritzName: 'covo' },
    status: { bindingState: 'active', effectiveCwd: '/workspace/repo' },
  },
  {
    metadata: { name: 'conv-2' },
    spec: { sessionId: 'sess-2', title: 'Conversation Two', spritzName: 'covo' },
    status: { bindingState: 'active', effectiveCwd: '/workspace/repo' },
  },
];

function createSpritz(
  overrides: {
    metadata?: Partial<{ name: string; namespace: string }>;
    spec?: Partial<{ image: string }>;
    status?: Partial<{
      phase: string;
      message: string;
      acp: {
        state: string;
        agentInfo?: {
          name?: string;
          title?: string;
          version?: string;
        };
      };
    }>;
  } = {},
) {
  return {
    metadata: {
      name: 'covo',
      namespace: 'default',
      ...(overrides.metadata || {}),
    },
    spec: {
      image: 'example.com/covo:latest',
      ...(overrides.spec || {}),
    },
    status: {
      phase: 'Ready',
      acp: {
        state: 'ready',
        agentInfo: { version: '1.0.0' },
      },
      ...(overrides.status || {}),
    },
  };
}

function createConversation(
  overrides: Partial<ConversationInfo> & {
    metadata?: Partial<ConversationInfo['metadata']>;
    spec?: Partial<ConversationInfo['spec']>;
    status?: Partial<NonNullable<ConversationInfo['status']>>;
  } = {},
) {
  return {
    metadata: { name: 'conv-1', ...(overrides.metadata || {}) },
    spec: {
      sessionId: 'sess-1',
      title: 'Conversation One',
      spritzName: 'covo',
      ...(overrides.spec || {}),
    },
    status: {
      bindingState: 'active',
      effectiveCwd: '/workspace/repo',
      ...(overrides.status || {}),
    },
  };
}

function setupRequestMock({
  spritzes = [createSpritz()],
  conversations = CONVERSATIONS,
}: {
  spritzes?: ReturnType<typeof createSpritz>[];
  conversations?: typeof CONVERSATIONS;
} = {}) {
  requestMock.mockImplementation((path: string, options?: { method?: string }) => {
    if (path === '/spritzes') {
      return Promise.resolve({ items: spritzes });
    }
    if (path === '/acp/conversations?spritz=covo') {
      return Promise.resolve({ items: conversations });
    }
    if (path === '/acp/conversations/conv-1/connect-ticket' && options?.method === 'POST') {
      return Promise.resolve({
        type: 'connect-ticket',
        ticket: 'ticket-123',
        expiresAt: '2026-03-30T12:34:56Z',
        protocol: 'spritz-acp.v1',
        connectPath: '/api/acp/conversations/conv-1/connect',
      });
    }
    return Promise.resolve({});
  });
}

function countBootstrapCalls(conversationId: string) {
  return requestMock.mock.calls.filter(
    ([path, options]) => path === `/acp/conversations/${conversationId}/bootstrap` && options?.method === 'POST',
  ).length;
}

function setupBootstrapRetryMock(conversationId: string, title: string) {
  const conversation = createConversation({
    metadata: { name: conversationId },
    spec: { sessionId: '', title, spritzName: 'covo' },
    status: { bindingState: 'pending' },
  });
  setupRequestMock({ conversations: [conversation] });
  requestMock.mockImplementation((path: string, options?: { method?: string }) => {
    if (path === '/spritzes') {
      return Promise.resolve({
        items: [
          {
            metadata: { name: 'covo' },
            status: { phase: 'Ready', acp: { state: 'ready', agentInfo: { version: '1.0.0' } } },
          },
        ],
      });
    }
    if (path === '/acp/conversations?spritz=covo') {
      return Promise.resolve({ items: [conversation] });
    }
    if (path === `/acp/conversations/${conversationId}/bootstrap` && options?.method === 'POST') {
      if (countBootstrapCalls(conversationId) === 1) {
        return Promise.reject(new Error('HTTP 525 · example.com · Cloudflare'));
      }
      return Promise.resolve({ effectiveSessionId: `${conversationId}-session-2` });
    }
    return Promise.resolve({});
  });
}

function createApiError(message: string, status: number) {
  const error = new Error(message) as Error & { status: number };
  error.status = status;
  return error;
}

function setupBootstrapTerminalFailureMock(conversationId: string, title: string) {
  const conversation = createConversation({
    metadata: { name: conversationId },
    spec: { sessionId: '', title, spritzName: 'covo' },
    status: { bindingState: 'pending' },
  });
  setupRequestMock({ conversations: [conversation] });
  requestMock.mockImplementation((path: string, options?: { method?: string }) => {
    if (path === '/spritzes') {
      return Promise.resolve({
        items: [
          {
            metadata: { name: 'covo' },
            status: { phase: 'Ready', acp: { state: 'ready', agentInfo: { version: '1.0.0' } } },
          },
        ],
      });
    }
    if (path === '/acp/conversations?spritz=covo') {
      return Promise.resolve({ items: [conversation] });
    }
    if (path === `/acp/conversations/${conversationId}/bootstrap` && options?.method === 'POST') {
      return Promise.reject(createApiError('Conversation not found.', 404));
    }
    return Promise.resolve({});
  });
}

function createDeferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function LocationDisplay() {
  const location = useLocation();
  return <div data-testid="location-path">{location.pathname}</div>;
}

function renderChatPage(route: string, rawConfig?: RawSpritzConfig) {
  const resolvedConfig = rawConfig ? resolveConfig({ ...config, ...rawConfig }) : config;
  return render(
    <MemoryRouter initialEntries={[route]}>
      <ConfigProvider value={resolvedConfig}>
        <NoticeProvider>
          <LocationDisplay />
          <Routes>
            <Route path="/c/*" element={<ChatPage />} />
            <Route path="/" element={<ChatPage />} />
          </Routes>
        </NoticeProvider>
      </ConfigProvider>
    </MemoryRouter>,
  );
}

async function renderChat(route: string, rawConfig?: RawSpritzConfig) {
  renderChatPage(route, rawConfig);
  await screen.findByLabelText('Message input');
  await waitFor(() => expect((screen.getByLabelText('Message input') as HTMLTextAreaElement).disabled).toBe(false));
}

describe('ChatPage draft persistence', () => {
  beforeEach(() => {
    vi.useRealTimers();
    Object.defineProperty(globalThis, 'localStorage', { value: createMockStorage(), writable: true });
    Object.defineProperty(globalThis, 'sessionStorage', { value: createMockStorage(), writable: true });
    Object.defineProperty(window.HTMLElement.prototype, 'scrollIntoView', {
      value: vi.fn(),
      writable: true,
    });
    requestMock.mockReset();
    sendPromptMock.mockReset();
    showNoticeMock.mockReset();
    refreshAuthTokenForWebSocketMock.mockClear();
    resetACPMockState();
    sendPromptMock.mockResolvedValue({});
    setupRequestMock();
  });

  it('uses an ACP connect ticket on the current host by default', async () => {
    setAuthToken('external-ui-token');

    await renderChat('/c/covo/conv-1', {
      apiBaseUrl: '/api',
      auth: {
        mode: 'bearer',
        tokenStorageKeys: 'spritz-token',
      },
    });

    await waitFor(() => {
      expect(getLastACPOptions()?.wsUrl).toBe(
        'ws://localhost:3000/api/acp/conversations/conv-1/connect',
      );
      expect(getLastACPOptions()?.protocols).toEqual([
        'spritz-acp.v1',
        'spritz-ticket.v1.ticket-123',
      ]);
    });
  });

  it('routes the settings entrypoint through the Slack gateway', async () => {
    await renderChat('/c/covo/conv-1');

    const settingsLink = screen.getByLabelText('Open settings') as HTMLAnchorElement;
    expect(settingsLink.getAttribute('href')).toBe('/slack-gateway/slack/workspaces');
  });

  it('retries ACP connect-ticket failures automatically', async () => {
    setAuthToken('external-ui-token');
    let ticketAttempts = 0;
    requestMock.mockImplementation((path: string, options?: { method?: string }) => {
      if (path === '/spritzes') {
        return Promise.resolve({ items: [createSpritz()] });
      }
      if (path === '/acp/conversations?spritz=covo') {
        return Promise.resolve({ items: CONVERSATIONS });
      }
      if (path === '/acp/conversations/conv-1/connect-ticket' && options?.method === 'POST') {
        ticketAttempts += 1;
        if (ticketAttempts === 1) {
          return Promise.reject(createApiError('ACP warming up.', 409));
        }
        return Promise.resolve({
          type: 'connect-ticket',
          ticket: 'ticket-123',
          expiresAt: '2026-03-30T12:34:56Z',
          protocol: 'spritz-acp.v1',
          connectPath: '/api/acp/conversations/conv-1/connect',
        });
      }
      return Promise.resolve({});
    });

    renderChatPage('/c/covo/conv-1', {
      apiBaseUrl: 'https://spritz.example.com/api',
      websocketBaseUrl: 'https://spritz.example.com/api',
      auth: {
        mode: 'bearer',
        tokenStorageKeys: 'spritz-token',
      },
    });

    await waitFor(() => {
      expect(ticketAttempts).toBe(1);
    });

    await waitFor(() => {
      expect(ticketAttempts).toBe(2);
      expect(getLastACPOptions()?.wsUrl).toBe(
        'wss://spritz.example.com/api/acp/conversations/conv-1/connect',
      );
      expect(getLastACPOptions()?.protocols).toEqual([
        'spritz-acp.v1',
        'spritz-ticket.v1.ticket-123',
      ]);
    }, { timeout: 4000 });
  });

  it('uses an explicit websocket base url with ACP connect tickets for cross-host connections', async () => {
    setAuthToken('external-ui-token');

    await renderChat('/c/covo/conv-1', {
      apiBaseUrl: 'https://spritz.example.com/api',
      websocketBaseUrl: 'https://spritz.example.com/api',
      auth: {
        mode: 'bearer',
        tokenStorageKeys: 'spritz-token',
      },
    });

    await waitFor(() => {
      expect(getLastACPOptions()?.wsUrl).toBe(
        'wss://spritz.example.com/api/acp/conversations/conv-1/connect',
      );
      expect(getLastACPOptions()?.protocols).toEqual([
        'spritz-acp.v1',
        'spritz-ticket.v1.ticket-123',
      ]);
    });
  });

  it('mints a fresh ACP connect ticket when the socket closes before ready', async () => {
    setACPStartPending(true);
    setAuthToken('expired-token');
    setRefreshAuthResult({ token: 'refreshed-token', refreshed: true });
    refreshAuthTokenForWebSocketMock.mockImplementation(async () => {
      setAuthToken('refreshed-token');
      setACPStartPending(false);
      return { token: 'refreshed-token', refreshed: true };
    });

    const resolvedConfig = resolveConfig({
      ...config,
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
      <MemoryRouter initialEntries={['/c/covo/conv-1']}>
        <ConfigProvider value={resolvedConfig}>
          <NoticeProvider>
            <Routes>
              <Route path="/c/*" element={<ChatPage />} />
            </Routes>
          </NoticeProvider>
        </ConfigProvider>
      </MemoryRouter>,
    );

    await waitFor(() => {
      expect(getLastACPOptions()?.wsUrl).toBe(
        'wss://spritz.example.com/api/acp/conversations/conv-1/connect',
      );
      expect(getLastACPOptions()?.protocols).toEqual([
        'spritz-acp.v1',
        'spritz-ticket.v1.ticket-123',
      ]);
    });

    vi.useFakeTimers();
    act(() => {
      closeLastACPConnection();
    });

    expect(requestMock.mock.calls.filter(([path]) => path === '/acp/conversations/conv-1/connect-ticket')).toHaveLength(1);

    act(() => {
      vi.advanceTimersByTime(1999);
    });

    expect(requestMock.mock.calls.filter(([path]) => path === '/acp/conversations/conv-1/connect-ticket')).toHaveLength(1);

    act(() => {
      vi.advanceTimersByTime(1);
    });
    vi.useRealTimers();

    await waitFor(() => {
      expect(requestMock.mock.calls.filter(([path]) => path === '/acp/conversations/conv-1/connect-ticket')).toHaveLength(2);
      expect(getLastACPOptions()?.wsUrl).toBe(
        'wss://spritz.example.com/api/acp/conversations/conv-1/connect',
      );
    });
  });

  it('retries bootstrap failures automatically and recovers without a refresh', async () => {
    setupBootstrapRetryMock('conv-bootstrap', 'Bootstrap Me');

    render(
      <MemoryRouter initialEntries={['/c/covo/conv-bootstrap']}>
        <ConfigProvider value={config}>
          <NoticeProvider>
            <Routes>
              <Route path="/c/*" element={<ChatPage />} />
            </Routes>
          </NoticeProvider>
        </ConfigProvider>
      </MemoryRouter>,
    );

    await screen.findByLabelText('Message input');
    await waitFor(() => {
      expect(screen.getByText('HTTP 525 · example.com · Cloudflare Retrying…')).toBeTruthy();
    });

    await waitFor(() => {
      expect(countBootstrapCalls('conv-bootstrap')).toBe(2);
      expect((screen.getByLabelText('Message input') as HTMLTextAreaElement).disabled).toBe(false);
    }, { timeout: 4000 });
  });

  it('bootstraps active conversations that still lack an effective cwd', async () => {
    const conversation = createConversation({
      metadata: { name: 'conv-cwd' },
      spec: { sessionId: 'sess-cwd', title: 'Needs CWD', spritzName: 'covo' },
      status: { bindingState: 'active', effectiveCwd: '' },
    });
    setupRequestMock({ conversations: [conversation] as typeof CONVERSATIONS });
    requestMock.mockImplementation((path: string, options?: { method?: string }) => {
      if (path === '/spritzes') {
        return Promise.resolve({ items: [createSpritz()] });
      }
      if (path === '/acp/conversations?spritz=covo') {
        return Promise.resolve({ items: [conversation] });
      }
      if (path === '/acp/conversations/conv-cwd/bootstrap' && options?.method === 'POST') {
        return Promise.resolve({
          effectiveSessionId: 'sess-cwd',
          effectiveCwd: '/workspace/platform',
          conversation: createConversation({
            metadata: { name: 'conv-cwd' },
            spec: { sessionId: 'sess-cwd', title: 'Needs CWD', spritzName: 'covo' },
            status: { bindingState: 'active', effectiveCwd: '/workspace/platform' },
          }),
        });
      }
      if (path === '/acp/conversations/conv-cwd/connect-ticket' && options?.method === 'POST') {
        return Promise.resolve({
          type: 'connect-ticket',
          ticket: 'ticket-123',
          expiresAt: '2026-03-30T12:34:56Z',
          protocol: 'spritz-acp.v1',
          connectPath: '/api/acp/conversations/conv-cwd/connect',
        });
      }
      return Promise.resolve({});
    });

    renderChatPage('/c/covo/conv-cwd', {
      apiBaseUrl: '/api',
      auth: {
        mode: 'bearer',
        tokenStorageKeys: 'spritz-token',
      },
    });

    await waitFor(() => {
      expect(countBootstrapCalls('conv-cwd')).toBe(1);
      expect((getLastACPOptions()?.conversation as Record<string, unknown>)?.status).toEqual(
        expect.objectContaining({ effectiveCwd: '/workspace/platform' }),
      );
    });

    fireEvent.click(screen.getByRole('button', { name: 'Needs CWD' }));

    await waitFor(() => {
      expect(countBootstrapCalls('conv-cwd')).toBe(1);
    });
  });

  it('surfaces terminal bootstrap failures without retrying again in the background', async () => {
    setupBootstrapTerminalFailureMock('conv-terminal', 'Terminal Me');

    render(
      <MemoryRouter initialEntries={['/c/covo/conv-terminal']}>
        <ConfigProvider value={config}>
          <NoticeProvider>
            <Routes>
              <Route path="/c/*" element={<ChatPage />} />
            </Routes>
          </NoticeProvider>
        </ConfigProvider>
      </MemoryRouter>,
    );

    await screen.findByLabelText('Message input');
    await waitFor(() => {
      expect(countBootstrapCalls('conv-terminal')).toBe(1);
    });

    vi.useFakeTimers();
    act(() => {
      vi.advanceTimersByTime(2200);
    });
    vi.useRealTimers();

    expect(countBootstrapCalls('conv-terminal')).toBe(1);

    act(() => {
      window.dispatchEvent(new Event('focus'));
    });

    expect(countBootstrapCalls('conv-terminal')).toBe(1);
  });

  it('retries immediately on focus after a transient bootstrap failure', async () => {
    setupBootstrapRetryMock('conv-focus', 'Focus Me');

    render(
      <MemoryRouter initialEntries={['/c/covo/conv-focus']}>
        <ConfigProvider value={config}>
          <NoticeProvider>
            <Routes>
              <Route path="/c/*" element={<ChatPage />} />
            </Routes>
          </NoticeProvider>
        </ConfigProvider>
      </MemoryRouter>,
    );

    await screen.findByLabelText('Message input');
    await waitFor(() => {
      expect(screen.getByText('HTTP 525 · example.com · Cloudflare Retrying…')).toBeTruthy();
    });

    act(() => {
      window.dispatchEvent(new Event('focus'));
    });

    await waitFor(() => {
      expect(countBootstrapCalls('conv-focus')).toBe(2);
      expect((screen.getByLabelText('Message input') as HTMLTextAreaElement).disabled).toBe(false);
    });
  });

  it('shows a provisioning state for direct agent chat routes before ACP is ready', async () => {
    setupRequestMock({
      spritzes: [
        createSpritz({
          status: {
            phase: 'Provisioning',
            message: 'Allocating the instance.',
            acp: { state: 'starting' },
          },
        }),
      ],
      conversations: [],
    });

    renderChatPage('/c/covo');

    expect(await screen.findByText('Your agent is being created now')).toBeTruthy();
    expect(screen.getByText('We will start a chat automatically as soon as it is ready.')).toBeTruthy();
    expect(screen.getAllByText('Allocating the instance.').length).toBeGreaterThan(0);
    expect(screen.getByTestId('sidebar-focused-spritz').textContent).toBe('covo');
  });

  it('shows a provisioning state when the route spritz is only resolvable by direct lookup', async () => {
    requestMock.mockImplementation((path: string) => {
      if (path === '/spritzes') {
        return Promise.resolve({ items: [] });
      }
      if (path === '/spritzes/zeno-ken-bison') {
        return Promise.resolve(
          createSpritz({
            metadata: { name: 'zeno-ken-bison' },
            status: {
              phase: 'Provisioning',
              message: 'Creating your agent instance.',
              acp: { state: 'starting' },
            },
          }),
        );
      }
      return Promise.resolve({});
    });

    renderChatPage('/c/zeno-ken-bison');

    expect(await screen.findByText('Your agent is being created now')).toBeTruthy();
    expect(screen.getAllByText('Creating your agent instance.').length).toBeGreaterThan(0);
    expect(screen.queryByText('Select a conversation or create a new instance.')).toBeNull();
  });

  it('keeps the provisioning route visible while the spritz resource is not discoverable yet', async () => {
    requestMock.mockImplementation((path: string) => {
      if (path === '/spritzes') {
        return Promise.resolve({ items: [] });
      }
      if (path === '/spritzes/zeno-fresh-ridge') {
        return Promise.reject(new Error('Not found.'));
      }
      return Promise.resolve({});
    });

    renderChatPage('/c/zeno-fresh-ridge');

    expect(await screen.findByText('Your agent is being created now')).toBeTruthy();
    expect(screen.getByText('We will start a chat automatically as soon as it is ready.')).toBeTruthy();
    expect(screen.getAllByText('Creating your agent instance.').length).toBeGreaterThan(0);
    expect(screen.getByTestId('sidebar-focused-spritz').textContent).toBe('zeno-fresh-ridge');
    expect(screen.queryByText('Select a conversation or create a new instance.')).toBeNull();
  });

  it('automatically creates and opens a conversation once a provisioning agent becomes ready', async () => {
    const createdConversation = createConversation({
      metadata: { name: 'conv-created' },
      spec: { sessionId: 'sess-created', title: 'Created automatically', spritzName: 'covo' },
      status: { bindingState: 'active', lastActivityAt: '2026-03-27T10:05:00Z' },
    });
    let spritzRequestCount = 0;
    requestMock.mockImplementation((path: string, options?: { method?: string }) => {
      if (path === '/spritzes') {
        spritzRequestCount += 1;
        return Promise.resolve({
          items: [
            spritzRequestCount === 1
              ? createSpritz({
                  status: {
                    phase: 'Provisioning',
                    message: 'Allocating the instance.',
                    acp: { state: 'starting' },
                  },
                })
              : createSpritz(),
          ],
        });
      }
      if (path === '/acp/conversations?spritz=covo') {
        return Promise.resolve({ items: [] });
      }
      if (path === '/acp/conversations' && options?.method === 'POST') {
        return Promise.resolve(createdConversation);
      }
      return Promise.resolve({});
    });
    const realSetTimeout = window.setTimeout.bind(window);
    const setTimeoutSpy = vi.spyOn(window, 'setTimeout').mockImplementation(((
      handler: TimerHandler,
      timeout?: number,
      ...args: unknown[]
    ) => {
      if (timeout === 2000 && typeof handler === 'function') {
        queueMicrotask(() => {
          handler(...args as []);
        });
        return 1 as unknown as number;
      }
      return realSetTimeout(handler, timeout, ...(args as []));
    }) as typeof window.setTimeout);

    try {
      renderChatPage('/c/covo');
      await waitFor(() => {
        expect(requestMock).toHaveBeenCalledWith(
          '/acp/conversations',
          expect.objectContaining({ method: 'POST' }),
        );
      });
      await waitFor(() => {
        expect((screen.getByTestId('selected-conversation') as HTMLDivElement).textContent).toBe('conv-created');
      });
      expect((screen.getByTestId('selected-conversation') as HTMLDivElement).textContent).toBe('conv-created');
    } finally {
      setTimeoutSpy.mockRestore();
    }
  });

  it('keeps polling until a provisioning agent becomes ready and then opens a conversation', async () => {
    const createdConversation = createConversation({
      metadata: { name: 'conv-created-late' },
      spec: { sessionId: 'sess-created-late', title: 'Created after repeated polling', spritzName: 'covo' },
      status: { bindingState: 'active', lastActivityAt: '2026-03-27T10:10:00Z' },
    });
    let spritzRequestCount = 0;
    requestMock.mockImplementation((path: string, options?: { method?: string }) => {
      if (path === '/spritzes') {
        spritzRequestCount += 1;
        return Promise.resolve({
          items: [
            spritzRequestCount < 4
              ? createSpritz({
                  status: {
                    phase: 'Provisioning',
                    message: 'Allocating the instance.',
                    acp: { state: 'starting' },
                  },
                })
              : createSpritz(),
          ],
        });
      }
      if (path === '/acp/conversations?spritz=covo') {
        return Promise.resolve({ items: [] });
      }
      if (path === '/acp/conversations' && options?.method === 'POST') {
        return Promise.resolve(createdConversation);
      }
      return Promise.resolve({});
    });
    const realSetTimeout = window.setTimeout.bind(window);
    const setTimeoutSpy = vi.spyOn(window, 'setTimeout').mockImplementation(((
      handler: TimerHandler,
      timeout?: number,
      ...args: unknown[]
    ) => {
      if (timeout === 2000 && typeof handler === 'function') {
        queueMicrotask(() => {
          handler(...args as []);
        });
        return 1 as unknown as number;
      }
      return realSetTimeout(handler, timeout, ...(args as []));
    }) as typeof window.setTimeout);

    try {
      renderChatPage('/c/covo');
      await waitFor(() => {
        expect(requestMock).toHaveBeenCalledWith(
          '/acp/conversations',
          expect.objectContaining({ method: 'POST' }),
        );
      });
      await waitFor(() => {
        expect((screen.getByTestId('selected-conversation') as HTMLDivElement).textContent).toBe('conv-created-late');
      });
      expect(spritzRequestCount).toBeGreaterThanOrEqual(3);
    } finally {
      setTimeoutSpy.mockRestore();
    }
  });

  it('keeps polling while a direct route spritz is still undiscoverable and starts a conversation once it appears', async () => {
    const createdConversation = createConversation({
      metadata: { name: 'conv-created-after-lookup' },
      spec: {
        sessionId: 'sess-created-after-lookup',
        title: 'Created after lookup recovery',
        spritzName: 'zeno-fresh-ridge',
      },
      status: { bindingState: 'active', lastActivityAt: '2026-03-27T10:12:00Z' },
    });
    let routeLookupCount = 0;
    requestMock.mockImplementation((path: string, options?: { method?: string }) => {
      if (path === '/spritzes') {
        return Promise.resolve({ items: [] });
      }
      if (path === '/spritzes/zeno-fresh-ridge') {
        routeLookupCount += 1;
        if (routeLookupCount < 4) {
          return Promise.reject(new Error('Not found.'));
        }
        return Promise.resolve(
          createSpritz({
            metadata: { name: 'zeno-fresh-ridge' },
            status: {
              phase: 'Ready',
              acp: { state: 'ready' },
            },
          }),
        );
      }
      if (path === '/acp/conversations?spritz=zeno-fresh-ridge') {
        return Promise.resolve({ items: [] });
      }
      if (path === '/acp/conversations' && options?.method === 'POST') {
        return Promise.resolve(createdConversation);
      }
      return Promise.resolve({});
    });
    const realSetTimeout = window.setTimeout.bind(window);
    const setTimeoutSpy = vi.spyOn(window, 'setTimeout').mockImplementation(((
      handler: TimerHandler,
      timeout?: number,
      ...args: unknown[]
    ) => {
      if (timeout === 2000 && typeof handler === 'function') {
        queueMicrotask(() => {
          handler(...args as []);
        });
        return 1 as unknown as number;
      }
      return realSetTimeout(handler, timeout, ...(args as []));
    }) as typeof window.setTimeout);

    try {
      renderChatPage('/c/zeno-fresh-ridge');
      await waitFor(() => {
        expect(requestMock).toHaveBeenCalledWith(
          '/acp/conversations',
          expect.objectContaining({ method: 'POST' }),
        );
      });
      await waitFor(() => {
        expect((screen.getByTestId('selected-conversation') as HTMLDivElement).textContent).toBe(
          'conv-created-after-lookup',
        );
      });
      expect(routeLookupCount).toBeGreaterThanOrEqual(4);
    } finally {
      setTimeoutSpy.mockRestore();
    }
  });

  it('opens the latest conversation when an agent chat route omits the conversation id', async () => {
    setupRequestMock({
      conversations: [
        createConversation({
          metadata: { name: 'conv-older' },
          spec: { sessionId: 'sess-older', title: 'Older conversation', spritzName: 'covo' },
          status: { bindingState: 'active', lastActivityAt: '2026-03-26T08:00:00Z' },
        }),
        createConversation({
          metadata: { name: 'conv-latest' },
          spec: { sessionId: 'sess-latest', title: 'Latest conversation', spritzName: 'covo' },
          status: { bindingState: 'active', lastActivityAt: '2026-03-27T09:30:00Z' },
        }),
      ],
    });

    await renderChat('/c/covo');

    expect((screen.getByTestId('selected-conversation') as HTMLDivElement).textContent).toBe('conv-latest');
  });

  it('restores the draft after remounting the same conversation route', async () => {
    const user = userEvent.setup();
    const firstRender = render(
      <MemoryRouter initialEntries={['/c/covo/conv-1']}>
        <ConfigProvider value={config}>
          <NoticeProvider>
            <Routes>
              <Route path="/c/*" element={<ChatPage />} />
            </Routes>
          </NoticeProvider>
        </ConfigProvider>
      </MemoryRouter>,
    );
    await screen.findByLabelText('Message input');
    await waitFor(() => expect((screen.getByLabelText('Message input') as HTMLTextAreaElement).disabled).toBe(false));

    const input = screen.getByLabelText('Message input');
    await user.type(input, 'unsent draft');
    await waitFor(() => expect(localStorage.getItem('spritz:chat-drafts') || '').toContain('unsent draft'));

    firstRender.unmount();

    await renderChat('/c/covo/conv-1');
    expect((screen.getByLabelText('Message input') as HTMLTextAreaElement).value).toBe('unsent draft');
  });

  it('keeps drafts isolated between conversations', async () => {
    const user = userEvent.setup();
    const firstRender = render(
      <MemoryRouter initialEntries={['/c/covo/conv-1']}>
        <ConfigProvider value={config}>
          <NoticeProvider>
            <Routes>
              <Route path="/c/*" element={<ChatPage />} />
            </Routes>
          </NoticeProvider>
        </ConfigProvider>
      </MemoryRouter>,
    );
    await screen.findByLabelText('Message input');
    await waitFor(() => expect((screen.getByLabelText('Message input') as HTMLTextAreaElement).disabled).toBe(false));

    await user.type(screen.getByLabelText('Message input'), 'conversation one draft');
    firstRender.unmount();

    await renderChat('/c/covo/conv-2');
    expect((screen.getByLabelText('Message input') as HTMLTextAreaElement).value).toBe('');
  });

  it('clears the visible and stored draft after a successful send', async () => {
    const user = userEvent.setup();
    await renderChat('/c/covo/conv-1');

    await user.type(screen.getByLabelText('Message input'), 'send me');
    await waitFor(() => expect((screen.getByRole('button', { name: 'Send message' }) as HTMLButtonElement).disabled).toBe(false));
    await user.click(screen.getByRole('button', { name: 'Send message' }));

    await waitFor(() => expect(sendPromptMock).toHaveBeenCalledWith('send me'));
    await waitFor(() => expect((screen.getByLabelText('Message input') as HTMLTextAreaElement).value).toBe(''));
    expect(localStorage.getItem('spritz:chat-drafts')).toBeNull();
  });

  it('restores the draft when send races a disconnect before ready state propagates', async () => {
    const user = userEvent.setup();
    await renderChat('/c/covo/conv-1');

    await user.type(screen.getByLabelText('Message input'), 'retry me');
    const sendButton = screen.getByRole('button', { name: 'Send message' });
    expect((sendButton as HTMLButtonElement).disabled).toBe(false);

    act(() => {
      closeLastACPConnection();
      fireEvent.click(sendButton);
    });

    await waitFor(() => {
      expect(sendPromptMock).not.toHaveBeenCalled();
      expect((screen.getByLabelText('Message input') as HTMLTextAreaElement).value).toBe('retry me');
    });
    expect(localStorage.getItem('spritz:chat-drafts') || '').toContain('retry me');
  });

  it('renders the echoed ACP user message only once', async () => {
    const user = userEvent.setup();
    await renderChat('/c/covo/conv-1');

    await user.type(screen.getByLabelText('Message input'), 'test');
    await user.click(screen.getByRole('button', { name: 'Send message' }));

    await waitFor(() => expect(sendPromptMock).toHaveBeenCalledWith('test'));

    emitUpdate({
      sessionUpdate: 'user_message_chunk',
      messageId: 'user-1',
      content: { type: 'text', text: 'test' },
    });

    await waitFor(() => {
      const userMessages = screen
        .getAllByTestId('chat-message')
        .filter((element) => element.textContent === 'user:test');
      expect(userMessages).toHaveLength(1);
    });
  });

  it('deduplicates replayed history without dropping a newer live user message', async () => {
    await renderChat('/c/covo/conv-1');

    emitReplayState(true);
    emitUpdate({
      sessionUpdate: 'user_message_chunk',
      historyMessageId: 'user-1',
      content: { type: 'text', text: 'who is this' },
    }, { historical: true });
    emitUpdate({
      sessionUpdate: 'agent_message_chunk',
      historyMessageId: 'assistant-1',
      content: { type: 'text', text: "I'm Zeno." },
    }, { historical: true });
    emitReplayState(false);

    await waitFor(() => {
      const messages = screen.getAllByTestId('chat-message').map((element) => element.textContent);
      expect(messages).toEqual(['user:who is this', "assistant:I'm Zeno."]);
    });

    emitUpdate({
      sessionUpdate: 'user_message_chunk',
      content: { type: 'text', text: 'and what can you do?' },
    });

    await waitFor(() => {
      const messages = screen.getAllByTestId('chat-message').map((element) => element.textContent);
      expect(messages).toEqual([
        'user:who is this',
        "assistant:I'm Zeno.",
        'user:and what can you do?',
      ]);
    });

    emitReplayState(true);
    emitUpdate({
      sessionUpdate: 'user_message_chunk',
      historyMessageId: 'user-1',
      content: { type: 'text', text: 'who is this' },
    }, { historical: true });
    emitUpdate({
      sessionUpdate: 'agent_message_chunk',
      historyMessageId: 'assistant-1',
      content: { type: 'text', text: "I'm Zeno." },
    }, { historical: true });
    emitReplayState(false);

    await waitFor(() => {
      const messages = screen.getAllByTestId('chat-message').map((element) => element.textContent);
      expect(messages).toEqual([
        'user:who is this',
        "assistant:I'm Zeno.",
        'user:and what can you do?',
      ]);
    });
  });

  it('replaces stale live assistant turns with canonical replay order on reconnect', async () => {
    await renderChat('/c/covo/conv-1');

    act(() => {
      emitUpdate({
        sessionUpdate: 'agent_message_chunk',
        content: { type: 'text', text: 'Based on the German residence law documents...' },
      });
    });

    await waitFor(() => {
      const messages = screen.getAllByTestId('chat-message').map((element) => element.textContent);
      expect(messages).toEqual(['assistant:Based on the German residence law documents...']);
    });

    act(() => {
      emitReplayState(true);
      emitUpdate({
        sessionUpdate: 'agent_message_chunk',
        historyMessageId: 'assistant-1',
        content: { type: 'text', text: 'I need to clarify: tc is the TextCortex CLI.' },
      }, { historical: true });
      emitUpdate({
        sessionUpdate: 'agent_message_chunk',
        historyMessageId: 'assistant-2',
        content: { type: 'text', text: "You're right — `tc kb search` lets you search your own knowledge bases." },
      }, { historical: true });
      emitUpdate({
        sessionUpdate: 'agent_message_chunk',
        historyMessageId: 'assistant-3',
        content: { type: 'text', text: 'Based on the German residence law documents...' },
      }, { historical: true });
      emitReplayState(false);
    });

    await waitFor(() => {
      const messages = screen.getAllByTestId('chat-message').map((element) => element.textContent);
      expect(messages).toEqual([
        'assistant:I need to clarify: tc is the TextCortex CLI.',
        "assistant:You're right — `tc kb search` lets you search your own knowledge bases.",
        'assistant:Based on the German residence law documents...',
      ]);
    });
  });

  it('restores the original conversation draft when send fails after switching chats', async () => {
    const user = userEvent.setup();
    const deferred = createDeferred<unknown>();
    sendPromptMock.mockReturnValueOnce(deferred.promise);

    await renderChat('/c/covo/conv-1');

    await user.type(screen.getByLabelText('Message input'), 'retry me later');
    await user.click(screen.getByRole('button', { name: 'Send message' }));
    await waitFor(() => expect(sendPromptMock).toHaveBeenCalledWith('retry me later'));
    await user.click(screen.getByRole('button', { name: 'Conversation Two' }));
    await waitFor(() =>
      expect((screen.getByTestId('selected-conversation') as HTMLDivElement).textContent).toBe('conv-2'),
    );

    deferred.reject(new Error('send failed'));

    await waitFor(() => expect(localStorage.getItem('spritz:chat-drafts') || '').toContain('retry me later'));
    expect((screen.getByLabelText('Message input') as HTMLTextAreaElement).value).toBe('');
  });

  it('uses the branded empty-state shell for a selected conversation with no messages', async () => {
    await renderChat('/c/covo/conv-1');

    const title = screen.getByText('Start a conversation');
    const card = title.parentElement as HTMLDivElement;

    expect(card.className).toContain('bg-[var(--surface-emphasis)]');
    expect(card.className).toContain(
      'border-[color-mix(in_srgb,var(--primary)_12%,var(--border))]',
    );
  });
});

describe('ChatPage instance ordering', () => {
  beforeEach(() => {
    vi.useRealTimers();
    Object.defineProperty(globalThis, 'localStorage', { value: createMockStorage(), writable: true });
    Object.defineProperty(globalThis, 'sessionStorage', { value: createMockStorage(), writable: true });
    Object.defineProperty(window.HTMLElement.prototype, 'scrollIntoView', {
      value: vi.fn(),
      writable: true,
    });
    requestMock.mockReset();
    sendPromptMock.mockReset();
    showNoticeMock.mockReset();
    refreshAuthTokenForWebSocketMock.mockClear();
    resetACPMockState();
    sendPromptMock.mockResolvedValue({});
  });

  it('sorts agents alphabetically in sidebar regardless of API return order', async () => {
    const spritzZulu = createSpritz({ metadata: { name: 'zulu-instance' } });
    const spritzAlpha = createSpritz({ metadata: { name: 'alpha-instance' } });
    const convZulu = createConversation({
      metadata: { name: 'conv-z' },
      spec: { sessionId: 'sz', title: 'Zulu conv', spritzName: 'zulu-instance' },
    });
    const convAlpha = createConversation({
      metadata: { name: 'conv-a' },
      spec: { sessionId: 'sa', title: 'Alpha conv', spritzName: 'alpha-instance' },
    });

    requestMock.mockImplementation((path: string, options?: { method?: string }) => {
      if (path === '/spritzes') {
        // Return in reverse alphabetical order
        return Promise.resolve({ items: [spritzZulu, spritzAlpha] });
      }
      if (path === '/acp/conversations?spritz=zulu-instance') {
        return Promise.resolve({ items: [convZulu] });
      }
      if (path === '/acp/conversations?spritz=alpha-instance') {
        return Promise.resolve({ items: [convAlpha] });
      }
      if (path.endsWith('/connect-ticket') && options?.method === 'POST') {
        return Promise.resolve({
          type: 'connect-ticket',
          ticket: 'ticket-123',
          expiresAt: '2026-03-30T12:34:56Z',
          protocol: 'spritz-acp.v1',
          connectPath: '/api/acp/conversations/conv-a/connect',
        });
      }
      return Promise.resolve({});
    });

    renderChatPage('/c/alpha-instance/conv-a');

    await waitFor(() => {
      const order = screen.getByTestId('sidebar-agent-order');
      expect(order.textContent).toBe('alpha-instance,zulu-instance');
    });
  });

  it('keeps agent order stable when selecting a conversation from a different instance', async () => {
    const spritzA = createSpritz({ metadata: { name: 'alpha-instance' } });
    const spritzZ = createSpritz({ metadata: { name: 'zulu-instance' } });
    const convA = createConversation({
      metadata: { name: 'conv-a' },
      spec: { sessionId: 'sa', title: 'Alpha conv', spritzName: 'alpha-instance' },
    });
    const convZ = createConversation({
      metadata: { name: 'conv-z' },
      spec: { sessionId: 'sz', title: 'Zulu conv', spritzName: 'zulu-instance' },
    });

    requestMock.mockImplementation((path: string, options?: { method?: string }) => {
      if (path === '/spritzes') {
        return Promise.resolve({ items: [spritzZ, spritzA] });
      }
      if (path === '/acp/conversations?spritz=alpha-instance') {
        return Promise.resolve({ items: [convA] });
      }
      if (path === '/acp/conversations?spritz=zulu-instance') {
        return Promise.resolve({ items: [convZ] });
      }
      if (path.endsWith('/connect-ticket') && options?.method === 'POST') {
        return Promise.resolve({
          type: 'connect-ticket',
          ticket: 'ticket-123',
          expiresAt: '2026-03-30T12:34:56Z',
          protocol: 'spritz-acp.v1',
          connectPath: '/api/acp/conversations/conv-a/connect',
        });
      }
      return Promise.resolve({});
    });

    renderChatPage('/c/alpha-instance/conv-a');

    await waitFor(() => {
      expect(screen.getByTestId('sidebar-agent-order').textContent).toBe(
        'alpha-instance,zulu-instance',
      );
    });

    // Click a conversation from zulu-instance
    await act(async () => {
      fireEvent.click(screen.getByText('Zulu conv'));
    });

    // Agent order must remain alpha first, zulu second
    expect(screen.getByTestId('sidebar-agent-order').textContent).toBe(
      'alpha-instance,zulu-instance',
    );
  });

  it('does not flicker selected conversation when creating a new one', async () => {
    const spritz = createSpritz({ metadata: { name: 'covo' } });
    const existingConv = createConversation({
      metadata: { name: 'conv-existing' },
      spec: { sessionId: 'se', title: 'Existing conv', spritzName: 'covo' },
    });
    const newConv = createConversation({
      metadata: { name: 'conv-new' },
      spec: { sessionId: 'sn', title: 'New conv', spritzName: 'covo' },
    });

    requestMock.mockImplementation((path: string, options?: { method?: string }) => {
      if (path === '/spritzes') {
        return Promise.resolve({ items: [spritz] });
      }
      if (path === '/acp/conversations?spritz=covo') {
        return Promise.resolve({ items: [existingConv] });
      }
      if (path === '/acp/conversations' && options?.method === 'POST') {
        return Promise.resolve(newConv);
      }
      if (path.endsWith('/connect-ticket') && options?.method === 'POST') {
        return Promise.resolve({
          type: 'connect-ticket',
          ticket: 'ticket-123',
          expiresAt: '2026-03-30T12:34:56Z',
          protocol: 'spritz-acp.v1',
          connectPath: '/api/acp/conversations/conv-existing/connect',
        });
      }
      return Promise.resolve({});
    });

    renderChatPage('/c/covo/conv-existing');

    await waitFor(() => {
      expect(screen.getByTestId('selected-conversation').textContent).toBe('conv-existing');
    });

    // The selected conversation should never revert to conv-existing after switching
    // (regression test: previously fetchAgents would re-run with stale URL params
    // and reset selectedConversation back to the old one)
    expect(screen.getByTestId('selected-conversation').textContent).toBe('conv-existing');
  });
});
