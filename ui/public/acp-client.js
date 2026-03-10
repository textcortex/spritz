(function (global) {
  const ACP_CLIENT_INFO = {
    name: 'spritz-ui',
    title: 'Spritz ACP UI',
    version: '1.0.0',
  };

  function extractACPText(value) {
    if (value === null || value === undefined) return '';
    if (typeof value === 'string') return value;
    if (Array.isArray(value)) {
      return value.map((item) => extractACPText(item)).filter(Boolean).join('\n');
    }
    if (typeof value !== 'object') return String(value);
    if (typeof value.text === 'string') return value.text;
    if (value.type === 'content' && value.content) return extractACPText(value.content);
    if (value.content) return extractACPText(value.content);
    if (value.resource) {
      if (typeof value.resource.text === 'string') return value.resource.text;
      if (typeof value.resource.uri === 'string') return value.resource.uri;
    }
    return '';
  }

  function createACPClient(options) {
    const {
      wsUrl,
      conversation,
      onStatus,
      onReadyChange,
      onAgentInfo,
      onUpdate,
      onPermissionRequest,
      onPromptStateChange,
      onClose,
      onProtocolError,
    } = options;

    let ws = null;
    let nextId = 1;
    let ready = false;
    let disposed = false;
    const pending = new Map();
    let sessionId = conversation?.spec?.sessionId || '';
    let loadSessionSupported = false;
    let bootstrapComplete = false;

    function createACPError(code, message) {
      const error = new Error(message);
      error.code = code;
      return error;
    }

    function createRPCError(payload) {
      const baseMessage = String(payload?.message || 'ACP request failed.');
      const details =
        typeof payload?.data?.details === 'string' && payload.data.details.trim()
          ? payload.data.details.trim()
          : '';
      const error = createACPError('ACP_RPC_ERROR', details ? `${baseMessage}: ${details}` : baseMessage);
      error.rpcError = payload || null;
      return error;
    }

    function cleanupPending(error) {
      const resolvedError =
        error instanceof Error
          ? error
          : createACPError('ACP_REQUEST_CANCELLED', String(error || 'ACP request cancelled.'));
      pending.forEach(({ reject }) => reject(resolvedError));
      pending.clear();
    }

    function sendRaw(payload) {
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        throw new Error('ACP socket is not connected.');
      }
      ws.send(JSON.stringify(payload));
    }

    function requestRPC(method, params) {
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

    function respond(id, result) {
      sendRaw({ jsonrpc: '2.0', id, result });
    }

    function respondError(id, code, message) {
      sendRaw({ jsonrpc: '2.0', id, error: { code, message } });
    }

    function notify(method, params) {
      sendRaw({ jsonrpc: '2.0', method, params });
    }

    function isMissingSessionError(error) {
      const message = String(error?.message || '').toLowerCase();
      return message.includes('session') && message.includes('not found');
    }

    async function bootstrapSession() {
      onStatus('Negotiating ACP…');
      const init = await requestRPC('initialize', {
        protocolVersion: 1,
        clientCapabilities: {},
        clientInfo: ACP_CLIENT_INFO,
      });
      loadSessionSupported = Boolean(init?.agentCapabilities?.loadSession);
      if (typeof onAgentInfo === 'function') {
        onAgentInfo(init?.agentInfo || null);
      }
      if (!loadSessionSupported) {
        throw createACPError('ACP_LOAD_SESSION_UNSUPPORTED', 'ACP agent does not support session/load.');
      }
      if (!sessionId) {
        throw createACPError('ACP_SESSION_ID_REQUIRED', 'Conversation is missing an ACP session id.');
      }
      onStatus('Loading conversation…');
      try {
        await requestRPC('session/load', {
          sessionId,
          cwd: conversation?.spec?.cwd || '/home/dev',
          mcpServers: [],
        });
      } catch (error) {
        if (isMissingSessionError(error)) {
          const details =
            typeof error?.rpcError?.data?.details === 'string' && error.rpcError.data.details.trim()
              ? error.rpcError.data.details.trim()
              : error.message || 'ACP session not found.';
          throw createACPError('ACP_SESSION_MISSING', details);
        }
        throw error;
      }
      onStatus('Connected');
    }

    function handleIncoming(message) {
      if (message.id !== undefined && (message.result !== undefined || message.error)) {
        const key = String(message.id);
        const pendingRequest = pending.get(key);
        if (!pendingRequest) return;
        pending.delete(key);
        if (message.error) {
          if (pendingRequest.method === 'session/prompt' && typeof onPromptStateChange === 'function') {
            onPromptStateChange(false);
          }
          pendingRequest.reject(createRPCError(message.error));
          return;
        }
        pendingRequest.resolve(message.result);
        if (pendingRequest.method === 'session/prompt' && typeof onPromptStateChange === 'function') {
          onPromptStateChange(false);
        }
        return;
      }

      if (message.method === 'session/update') {
        if (typeof onUpdate === 'function') {
          onUpdate(message.params?.update);
        }
        return;
      }

      if (message.method === 'session/request_permission' && message.id !== undefined) {
        if (typeof onPermissionRequest === 'function') {
          onPermissionRequest({
            params: message.params,
            respond(result) {
              respond(message.id, result);
            },
            reject(messageText) {
              respondError(message.id, -32000, messageText || 'Permission denied.');
            },
          });
        }
        return;
      }

      if (message.id !== undefined) {
        respondError(message.id, -32601, 'Method not supported by Spritz ACP UI.');
      }
    }

    function start() {
      disposed = false;
      ready = false;
      bootstrapComplete = false;
      if (typeof onReadyChange === 'function') {
        onReadyChange(false);
      }
      return new Promise((resolve, reject) => {
        ws = new WebSocket(wsUrl);
        ws.onopen = async () => {
          try {
            await bootstrapSession();
            bootstrapComplete = true;
            ready = true;
            if (typeof onReadyChange === 'function') {
              onReadyChange(true);
            }
            resolve();
          } catch (err) {
            bootstrapComplete = false;
            ready = false;
            if (typeof onReadyChange === 'function') {
              onReadyChange(false);
            }
            reject(err);
            try {
              ws.close();
            } catch {
              // ignore
            }
          }
        };
        ws.onmessage = (event) => {
          try {
            const data = typeof event.data === 'string' ? event.data : new TextDecoder().decode(event.data);
            handleIncoming(JSON.parse(data));
          } catch (err) {
            if (typeof onProtocolError === 'function') {
              onProtocolError(err);
            }
          }
        };
        ws.onerror = () => {
          if (!ready) {
            reject(new Error('Failed to connect to ACP gateway.'));
          }
        };
        ws.onclose = () => {
          ready = false;
          bootstrapComplete = false;
          cleanupPending(createACPError('ACP_CONNECTION_CLOSED', 'ACP connection closed.'));
          if (typeof onReadyChange === 'function') {
            onReadyChange(false);
          }
          if (!disposed && typeof onClose === 'function') {
            onClose('ACP connection closed.');
          }
        };
      });
    }

    return {
      start,
      getConversationId() {
        return conversation?.metadata?.name || '';
      },
      getSessionId() {
        return sessionId || '';
      },
      matchesConversation(targetConversation) {
        const targetConversationId = targetConversation?.metadata?.name || '';
        if (targetConversationId && targetConversationId !== (conversation?.metadata?.name || '')) {
          return false;
        }
        const expectedSessionId = targetConversation?.spec?.sessionId || '';
        if (expectedSessionId && sessionId && expectedSessionId !== sessionId) {
          return false;
        }
        return true;
      },
      isReady() {
        return Boolean(ws && ws.readyState === WebSocket.OPEN && ready && bootstrapComplete);
      },
      async sendPrompt(text) {
        if (!sessionId || !this.isReady()) {
          throw new Error('ACP session is not ready yet.');
        }
        if (typeof onPromptStateChange === 'function') {
          onPromptStateChange(true);
        }
        return requestRPC('session/prompt', {
          sessionId,
          prompt: [{ type: 'text', text }],
        });
      },
      cancelPrompt() {
        if (!sessionId || !this.isReady()) return;
        notify('session/cancel', { sessionId });
        if (typeof onStatus === 'function') {
          onStatus('Cancelling…');
        }
      },
      dispose() {
        disposed = true;
        ready = false;
        bootstrapComplete = false;
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
  }

  global.SpritzACPClient = {
    createACPClient,
    extractACPText,
  };
})(window);
