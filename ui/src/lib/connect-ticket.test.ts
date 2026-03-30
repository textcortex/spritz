import { beforeEach, describe, expect, it, vi } from 'vite-plus/test';
import { resolveWebSocketConnect } from '@/lib/connect-ticket';

function createApiError(message: string, status: number) {
  const error = new Error(message) as Error & { status: number };
  error.status = status;
  return error;
}

const { requestMock } = vi.hoisted(() => ({
  requestMock: vi.fn(),
}));

vi.mock('@/lib/api', () => ({
  request: requestMock,
}));

describe('resolveWebSocketConnect', () => {
  beforeEach(() => {
    requestMock.mockReset();
  });

  it('falls back to the legacy bearer websocket when ticket minting is unsupported', async () => {
    requestMock.mockRejectedValue(createApiError('Not found.', 404));

    await expect(resolveWebSocketConnect({
      apiBaseUrl: 'https://spritz.example.com/api',
      websocketBaseUrl: 'https://spritz.example.com/api',
      directConnectPath: '/acp/conversations/conv-1/connect',
      ticketPath: '/acp/conversations/conv-1/connect-ticket',
      useConnectTicket: true,
      bearerToken: 'legacy-token',
      bearerTokenParam: 'token',
    })).resolves.toEqual({
      wsUrl: 'wss://spritz.example.com/api/acp/conversations/conv-1/connect?token=legacy-token',
      protocols: [],
    });
  });
});
