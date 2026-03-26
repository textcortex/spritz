import type React from 'react';
import { describe, it, expect, beforeEach, vi } from 'vite-plus/test';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { createMockStorage } from '@/test/helpers';
import { ConfigProvider, config } from '@/lib/config';
import { NoticeProvider } from '@/components/notice-banner';
import { ChatPage } from './chat';

const { requestMock, sendPromptMock, emitUpdate, emitReplayState, setUpdateHandler, setReplayStateHandler } = vi.hoisted(() => {
  let updateHandler:
    | ((update: Record<string, unknown>, options?: { historical?: boolean }) => void)
    | undefined;
  let replayStateHandler: ((replaying: boolean) => void) | undefined;
  return {
    requestMock: vi.fn(),
    sendPromptMock: vi.fn(),
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
  };
});

vi.mock('@/lib/api', () => ({
  request: requestMock,
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
    onReadyChange,
    onStatus,
    onUpdate,
    onReplayStateChange,
  }: {
    onReadyChange?: (ready: boolean) => void;
    onStatus?: (status: string) => void;
    onUpdate?: (update: Record<string, unknown>, options?: { historical?: boolean }) => void;
    onReplayStateChange?: (replaying: boolean) => void;
  }) => {
    setUpdateHandler(onUpdate);
    setReplayStateHandler(onReplayStateChange);
    return {
      start: vi.fn(async () => {
        onStatus?.('Connected');
        onReadyChange?.(true);
      }),
      getConversationId: () => 'conv-1',
      getSessionId: () => 'sess-1',
      matchesConversation: () => true,
      isReady: () => true,
      sendPrompt: sendPromptMock,
      cancelPrompt: vi.fn(),
      dispose: vi.fn(() => {
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
    useNotice: () => ({ showNotice: vi.fn() }),
  };
});

vi.mock('@/components/acp/sidebar', () => ({
  Sidebar: ({
    agents,
    selectedConversationId,
    onSelectConversation,
  }: {
    agents: Array<{ spritz: { metadata: { name: string } }; conversations: Array<{ metadata: { name: string }; spec?: { title?: string } }> }>;
    selectedConversationId: string | null;
    onSelectConversation: (conversation: { metadata: { name: string } }) => void;
  }) => (
    <div>
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

const CONVERSATIONS = [
  {
    metadata: { name: 'conv-1' },
    spec: { sessionId: 'sess-1', title: 'Conversation One', spritzName: 'covo' },
    status: { bindingState: 'active' },
  },
  {
    metadata: { name: 'conv-2' },
    spec: { sessionId: 'sess-2', title: 'Conversation Two', spritzName: 'covo' },
    status: { bindingState: 'active' },
  },
];

function setupRequestMock() {
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
      return Promise.resolve({ items: CONVERSATIONS });
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

async function renderChat(route: string) {
  render(
    <MemoryRouter initialEntries={[route]}>
      <ConfigProvider value={config}>
        <NoticeProvider>
          <Routes>
            <Route path="/c/:name/:conversationId" element={<ChatPage />} />
            <Route path="/c/:name" element={<ChatPage />} />
            <Route path="/" element={<ChatPage />} />
          </Routes>
        </NoticeProvider>
      </ConfigProvider>
    </MemoryRouter>,
  );
  await screen.findByLabelText('Message input');
  await waitFor(() => expect((screen.getByLabelText('Message input') as HTMLTextAreaElement).disabled).toBe(false));
}

describe('ChatPage draft persistence', () => {
  beforeEach(() => {
    Object.defineProperty(globalThis, 'localStorage', { value: createMockStorage(), writable: true });
    Object.defineProperty(globalThis, 'sessionStorage', { value: createMockStorage(), writable: true });
    Object.defineProperty(window.HTMLElement.prototype, 'scrollIntoView', {
      value: vi.fn(),
      writable: true,
    });
    requestMock.mockReset();
    sendPromptMock.mockReset();
    setUpdateHandler(undefined);
    setReplayStateHandler(undefined);
    sendPromptMock.mockResolvedValue({});
    setupRequestMock();
  });

  it('restores the draft after remounting the same conversation route', async () => {
    const user = userEvent.setup();
    const firstRender = render(
      <MemoryRouter initialEntries={['/c/covo/conv-1']}>
        <ConfigProvider value={config}>
          <NoticeProvider>
            <Routes>
              <Route path="/c/:name/:conversationId" element={<ChatPage />} />
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
              <Route path="/c/:name/:conversationId" element={<ChatPage />} />
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
});
