import type { ACPClient, ACPClientOptions, PermissionEntry } from '@/types/acp';

export function extractACPText(value: unknown): string {
  if (value === null || value === undefined) return '';
  if (typeof value === 'string') return value;
  if (Array.isArray(value)) {
    return value.map((item) => extractACPText(item)).filter(Boolean).join('\n');
  }
  if (typeof value !== 'object') return String(value);
  const obj = value as Record<string, unknown>;
  if (typeof obj.text === 'string') return obj.text;
  if (obj.type === 'content' && obj.content) return extractACPText(obj.content);
  if (obj.content) return extractACPText(obj.content);
  if (obj.resource && typeof obj.resource === 'object') {
    const res = obj.resource as Record<string, unknown>;
    if (typeof res.text === 'string') return res.text;
    if (typeof res.uri === 'string') return res.uri;
  }
  return '';
}

interface ACPError extends Error {
  code: string;
  rpcError?: unknown;
}

function createACPError(code: string, message: string): ACPError {
  const error = new Error(message) as ACPError;
  error.code = code;
  return error;
}

function createRPCError(payload: Record<string, unknown> | null): ACPError {
  const baseMessage = String(payload?.message || 'ACP request failed.');
  const data = payload?.data as Record<string, unknown> | undefined;
  const details = typeof data?.details === 'string' && data.details.trim() ? data.details.trim() : '';
  const error = createACPError('ACP_RPC_ERROR', details ? `${baseMessage}: ${details}` : baseMessage);
  error.rpcError = payload || null;
  return error;
}

/**
 * Create an ACP client that connects via the API's proxied WebSocket.
 *
 * The server handles the ACP bootstrap (initialize + session/load) via
 * POST /acp/conversations/:id/bootstrap before we connect.
 * The WebSocket at /acp/conversations/:id/connect is a raw proxy to the workspace.
 * We just need to send session/prompt over it — no client-side bootstrap needed.
 */
export function createACPClient(options: ACPClientOptions): ACPClient {
  const {
    wsUrl,
    conversation,
    onStatus,
    onReadyChange,
    onUpdate,
    onPermissionRequest,
    onPromptStateChange,
    onClose,
    onProtocolError,
  } = options;

  let ws: WebSocket | null = null;
  let nextId = 1;
  let ready = false;
  let disposed = false;
  const pending = new Map<string, { resolve: (v: unknown) => void; reject: (e: Error) => void; method: string }>();
  const sessionId = conversation?.spec?.sessionId || '';

  function cleanupPending(error: Error) {
    pending.forEach(({ reject }) => reject(error));
    pending.clear();
  }

  function sendRaw(payload: unknown) {
    if (!ws || ws.readyState !== WebSocket.OPEN) {
      throw new Error('ACP socket is not connected.');
    }
    ws.send(JSON.stringify(payload));
  }

  function requestRPC(method: string, params: unknown): Promise<unknown> {
    return new Promise((resolve, reject) => {
      const id = nextId++;
      pending.set(String(id), { resolve, reject, method });
      try {
        sendRaw({ jsonrpc: '2.0', id, method, params });
      } catch (err) {
        pending.delete(String(id));
        reject(err);
      }
    });
  }

  function respond(id: unknown, result: unknown) {
    sendRaw({ jsonrpc: '2.0', id, result });
  }

  function respondError(id: unknown, code: number, message: string) {
    sendRaw({ jsonrpc: '2.0', id, error: { code, message } });
  }

  function notify(method: string, params: unknown) {
    sendRaw({ jsonrpc: '2.0', method, params });
  }

  function handleIncoming(message: Record<string, unknown>) {
    // RPC response
    if (message.id !== undefined && (message.result !== undefined || message.error)) {
      const key = String(message.id);
      const pendingRequest = pending.get(key);
      if (!pendingRequest) return;
      pending.delete(key);
      if (message.error) {
        if (pendingRequest.method === 'session/prompt') onPromptStateChange?.(false);
        pendingRequest.reject(createRPCError(message.error as Record<string, unknown>));
        return;
      }
      pendingRequest.resolve(message.result);
      if (pendingRequest.method === 'session/prompt') onPromptStateChange?.(false);
      return;
    }

    // Session update notification
    if (message.method === 'session/update') {
      const params = message.params as Record<string, unknown> | undefined;
      onUpdate?.(params?.update as Record<string, unknown>);
      return;
    }

    // Permission request
    if (message.method === 'session/request_permission' && message.id !== undefined) {
      const entry: PermissionEntry = {
        params: message.params,
        respond(result: unknown) {
          respond(message.id, result);
        },
        reject(messageText?: string) {
          respondError(message.id, -32000, messageText || 'Permission denied.');
        },
      };
      onPermissionRequest?.(entry);
      return;
    }

    // Unsupported server request
    if (message.id !== undefined) {
      respondError(message.id, -32601, 'Method not supported by Spritz ACP UI.');
    }
  }

  const client: ACPClient = {
    start() {
      disposed = false;
      ready = false;
      onReadyChange?.(false);
      onStatus?.('Connecting…');

      return new Promise<void>((resolve, reject) => {
        ws = new WebSocket(wsUrl);
        ws.onopen = () => {
          // No client-side bootstrap needed — server already did it.
          // The WebSocket is a raw proxy to the workspace.
          ready = true;
          onReadyChange?.(true);
          onStatus?.('Connected');
          resolve();
        };
        ws.onmessage = (event) => {
          try {
            const data = typeof event.data === 'string' ? event.data : new TextDecoder().decode(event.data);
            handleIncoming(JSON.parse(data));
          } catch (err) {
            onProtocolError?.(err);
          }
        };
        ws.onerror = () => {
          if (!ready) reject(new Error('Failed to connect to ACP gateway.'));
        };
        ws.onclose = () => {
          ready = false;
          cleanupPending(createACPError('ACP_CONNECTION_CLOSED', 'ACP connection closed.'));
          onReadyChange?.(false);
          if (!disposed) onClose?.('ACP connection closed.');
        };
      });
    },
    getConversationId() {
      return conversation?.metadata?.name || '';
    },
    getSessionId() {
      return sessionId || '';
    },
    matchesConversation(target) {
      const targetId = target?.metadata?.name || '';
      if (targetId && targetId !== (conversation?.metadata?.name || '')) return false;
      const expectedSessionId = target?.spec?.sessionId || '';
      if (expectedSessionId && sessionId && expectedSessionId !== sessionId) return false;
      return true;
    },
    isReady() {
      return Boolean(ws && ws.readyState === WebSocket.OPEN && ready);
    },
    async sendPrompt(text: string) {
      if (!sessionId || !client.isReady()) {
        throw new Error('ACP session is not ready yet.');
      }
      onPromptStateChange?.(true);
      return requestRPC('session/prompt', {
        sessionId,
        prompt: [{ type: 'text', text }],
      });
    },
    cancelPrompt() {
      if (!sessionId || !client.isReady()) return;
      notify('session/cancel', { sessionId });
      onStatus?.('Cancelling…');
    },
    dispose() {
      disposed = true;
      ready = false;
      cleanupPending(createACPError('ACP_CLIENT_DISPOSED', 'ACP client disposed.'));
      try {
        if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) {
          ws.close();
        }
      } catch {
        // ignore
      }
    },
  };

  return client;
}
