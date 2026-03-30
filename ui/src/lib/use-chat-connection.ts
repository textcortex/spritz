import { useCallback, useEffect, useRef, useState } from 'react';
import { toast } from 'sonner';
import { createACPClient } from '@/lib/acp-client';
import { request, getAuthToken } from '@/lib/api';
import {
  applyChatTranscriptUpdate,
  createChatTranscriptSession,
  finalizeConnectedTranscriptSession,
  finalizePromptStreaming,
  noteReplayState,
  persistChatTranscriptSession,
  replaceChatTranscriptSession,
  type ChatTranscriptSession,
} from '@/lib/chat-transcript-session';
import { getReconnectDelayMs, shouldForceBootstrapOnRetry } from '@/lib/chat-retry-policy';
import { resolveWebSocketConnect } from '@/lib/connect-ticket';
import type { ACPClient, ACPTranscript, ConversationInfo, PermissionEntry } from '@/types/acp';

interface UseChatConnectionOptions {
  conversation: ConversationInfo | null;
  apiBaseUrl: string;
  websocketBaseUrl: string;
  onConversationUpdate: (conversation: ConversationInfo) => void;
  onConversationTitle: (conversationId: string, title: string) => void;
}

interface UseChatConnectionResult {
  transcript: ACPTranscript;
  clientReady: boolean;
  promptInFlight: boolean;
  status: string;
  permissionQueue: PermissionEntry[];
  sendPrompt: (text: string) => Promise<void>;
  cancelPrompt: () => void;
  shiftPermissionQueue: () => void;
}

function getErrorStatus(error: unknown): number | null {
  const status = (error as { status?: unknown })?.status;
  return typeof status === 'number' ? status : null;
}

function shouldRetryBootstrap(error: unknown): boolean {
  const status = getErrorStatus(error);
  if (status === null) return true;
  return status >= 500 || status === 408 || status === 425 || status === 429;
}

function shouldRetryConnectTicket(error: unknown): boolean {
  const status = getErrorStatus(error);
  if (status === null) return true;
  return status === 409 || status >= 500 || status === 408 || status === 425 || status === 429;
}

/**
 * Owns chat bootstrap, ACP websocket lifecycle, reconnect policy, and transcript updates.
 */
export function useChatConnection({
  conversation,
  apiBaseUrl,
  websocketBaseUrl,
  onConversationUpdate,
  onConversationTitle,
}: UseChatConnectionOptions): UseChatConnectionResult {
  const [transcript, setTranscript] = useState<ACPTranscript>(() => createChatTranscriptSession('').transcript);
  const [promptInFlight, setPromptInFlight] = useState(false);
  const [clientReady, setClientReady] = useState(false);
  const [status, setStatus] = useState('');
  const [permissionQueue, setPermissionQueue] = useState<PermissionEntry[]>([]);

  const clientRef = useRef<ACPClient | null>(null);
  const transcriptSessionRef = useRef<ChatTranscriptSession>(createChatTranscriptSession(''));
  const reconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const clientReadyRef = useRef(false);

  clientReadyRef.current = clientReady;

  const syncTranscript = useCallback((next: ACPTranscript) => {
    transcriptSessionRef.current.transcript = next;
    setTranscript({ ...next });
  }, []);

  const shiftPermissionQueue = useCallback(() => {
    setPermissionQueue((prev) => prev.slice(1));
  }, []);

  const sendPrompt = useCallback(async (text: string) => {
    const client = clientRef.current;
    if (!client || !client.isReady()) {
      throw new Error('ACP session is not ready yet.');
    }
    setStatus('Waiting for agent…');
    await client.sendPrompt(text);
    setStatus('Completed');
  }, []);

  const cancelPrompt = useCallback(() => {
    clientRef.current?.cancelPrompt();
  }, []);

  const conversationId = conversation?.metadata?.name || '';

  useEffect(() => {
    if (!conversation) {
      clientRef.current?.dispose();
      clientRef.current = null;
      transcriptSessionRef.current = createChatTranscriptSession('');
      syncTranscript(transcriptSessionRef.current.transcript);
      setClientReady(false);
      setPromptInFlight(false);
      setPermissionQueue([]);
      setStatus('');
      return;
    }

    let cancelled = false;
    let retryCount = 0;
    let connectInFlight = false;
    let autoReconnectEnabled = true;
    const activeConversation = conversation;
    const conversationId = activeConversation.metadata.name;
    const spritzName = activeConversation.spec?.spritzName || '';

    const initialSession = createChatTranscriptSession(conversationId);
    transcriptSessionRef.current = initialSession;
    syncTranscript(initialSession.transcript);

    function needsBootstrap(conv: ConversationInfo, forceBootstrap = false): boolean {
      if (forceBootstrap) return true;
      const sessionId = String(conv.spec?.sessionId || '').trim();
      if (!sessionId) return true;
      return String(conv.status?.bindingState || '').trim().toLowerCase() !== 'active';
    }

    function scheduleReconnect(options: {
      forceBootstrap?: boolean;
      immediate?: boolean;
      statusText?: string;
    } = {}) {
      if (cancelled || !autoReconnectEnabled) return;
      const {
        forceBootstrap = false,
        immediate = false,
        statusText = 'Disconnected. Reconnecting…',
      } = options;
      setStatus(statusText);
      if (reconnectTimerRef.current) {
        clearTimeout(reconnectTimerRef.current);
        reconnectTimerRef.current = null;
      }
      const runReconnect = () => {
        if (cancelled || connectInFlight) return;
        retryCount += 1;
        void connect({
          forceBootstrap: shouldForceBootstrapOnRetry(retryCount, forceBootstrap),
        }).catch((err) => {
          if (!cancelled) setStatus(err instanceof Error ? err.message : 'Reconnect failed');
        });
      };
      if (immediate) {
        queueMicrotask(runReconnect);
        return;
      }
      reconnectTimerRef.current = setTimeout(runReconnect, getReconnectDelayMs(retryCount));
    }

    function triggerForegroundRecovery() {
      if (cancelled || !autoReconnectEnabled || connectInFlight || clientReadyRef.current) return;
      scheduleReconnect({ immediate: true, statusText: 'Reconnecting…' });
    }

    async function connect(options: {
      forceBootstrap?: boolean;
    } = {}) {
      if (cancelled || connectInFlight) return;
      connectInFlight = true;
      const { forceBootstrap = false } = options;

      try {
        let effectiveConversation: ConversationInfo = activeConversation;
        let effectiveSessionId = String(effectiveConversation.spec?.sessionId || '').trim();

        if (needsBootstrap(effectiveConversation, forceBootstrap)) {
          setStatus('Bootstrapping…');
          let bootstrapData: Record<string, unknown>;
          try {
            bootstrapData = (await request<Record<string, unknown>>(
              `/acp/conversations/${encodeURIComponent(conversationId)}/bootstrap`,
              { method: 'POST' },
            )) || {};
          } catch (err) {
            if (!cancelled) {
              const message = err instanceof Error ? err.message : 'Bootstrap failed';
              if (shouldRetryBootstrap(err)) {
                scheduleReconnect({
                  forceBootstrap: true,
                  statusText: `${message} Retrying…`,
                });
              } else {
                autoReconnectEnabled = false;
                setStatus(message);
              }
            }
            return;
          }
          if (cancelled) return;

          const newSessionId = String(bootstrapData.effectiveSessionId || '');
          const replaced = Boolean(bootstrapData.replaced) ||
            (effectiveSessionId && newSessionId && effectiveSessionId !== newSessionId);

          if (replaced) {
            transcriptSessionRef.current = replaceChatTranscriptSession(conversationId);
            syncTranscript(transcriptSessionRef.current.transcript);
          }

          effectiveSessionId = newSessionId;
          effectiveConversation = {
            metadata: activeConversation.metadata,
            spec: { ...activeConversation.spec, sessionId: effectiveSessionId },
            status: { ...activeConversation.status, bindingState: 'active' },
          };
          onConversationUpdate(effectiveConversation);
        }

        if (cancelled) return;

        let wsUrl = '';
        let protocols: string[] = [];
        try {
          ({ wsUrl, protocols } = await resolveWebSocketConnect({
            apiBaseUrl,
            websocketBaseUrl,
            directConnectPath: `/acp/conversations/${encodeURIComponent(conversationId)}/connect`,
            ticketPath: `/acp/conversations/${encodeURIComponent(conversationId)}/connect-ticket`,
            useConnectTicket: Boolean(getAuthToken()),
          }));
        } catch (err) {
          if (!cancelled) {
            const message = err instanceof Error ? err.message : 'Connection failed';
            if (shouldRetryConnectTicket(err)) {
              scheduleReconnect({
                statusText: `${message} Retrying…`,
              });
            } else {
              autoReconnectEnabled = false;
              setStatus(message);
            }
          }
          return;
        }

        noteReplayState(transcriptSessionRef.current, true);
        let socketReady = false;
        let closeHandledReconnect = false;

        const client = createACPClient({
          wsUrl,
          protocols,
          conversation: effectiveConversation,
          onStatus: (text) => {
            if (!cancelled) setStatus(text);
          },
          onReadyChange: (ready) => {
            if (ready) {
              socketReady = true;
            }
            clientReadyRef.current = ready;
            if (!cancelled) setClientReady(ready);
          },
          onReplayStateChange: (replaying) => {
            if (cancelled) return;
            const nextTranscript = noteReplayState(transcriptSessionRef.current, replaying);
            if (replaying && nextTranscript) {
              syncTranscript(nextTranscript);
            }
          },
          onUpdate: (update, updateOptions) => {
            if (cancelled) return;
            const updateObj = update as Record<string, unknown>;
            const result = applyChatTranscriptUpdate(transcriptSessionRef.current, updateObj, {
              historical: updateOptions?.historical,
            });
            syncTranscript(transcriptSessionRef.current.transcript);

            if (result?.toast) {
              toast[result.toast.type === 'error' ? 'error' : 'info'](result.toast.message);
            }
            if (result?.conversationTitle) {
              const newTitle = result.conversationTitle;
              onConversationTitle(conversationId, newTitle);
              request(`/acp/conversations/${encodeURIComponent(conversationId)}`, {
                method: 'PATCH',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ title: newTitle }),
              }).catch(() => {});
            }

            persistChatTranscriptSession(transcriptSessionRef.current, {
              conversationId,
              spritzName,
              sessionId: effectiveSessionId,
            });
          },
          onPermissionRequest: (entry) => {
            if (!cancelled) setPermissionQueue((prev) => [...prev, entry]);
          },
          onPromptStateChange: (inFlight) => {
            if (!cancelled) {
              setPromptInFlight(inFlight);
              if (!inFlight) {
                syncTranscript(finalizePromptStreaming(transcriptSessionRef.current));
              }
            }
          },
          onClose: () => {
            clientReadyRef.current = false;
            clientRef.current = null;
            if (!socketReady) {
              connectInFlight = false;
              closeHandledReconnect = true;
            }
            if (cancelled) return;
            if (!socketReady) {
              scheduleReconnect({ immediate: true, statusText: 'Reconnecting…' });
              return;
            }
            scheduleReconnect();
          },
        });

        clientRef.current = client;

        try {
          await client.start();
        } catch (err) {
          if (closeHandledReconnect) {
            return;
          }
          const error = err as Error & { code?: string };
          if (error.code === 'ACP_SESSION_MISSING' && retryCount === 0) {
            retryCount += 1;
            client.dispose();
            clientRef.current = null;
            await connect({ forceBootstrap: true });
            return;
          }
          if (!cancelled) {
            scheduleReconnect({
              statusText: `${error.message || 'Connection failed'} Reconnecting…`,
            });
          }
          return;
        }

        if (cancelled) return;

        syncTranscript(finalizeConnectedTranscriptSession(transcriptSessionRef.current));
        retryCount = 0;
        persistChatTranscriptSession(transcriptSessionRef.current, {
          conversationId,
          spritzName,
          sessionId: effectiveSessionId,
        });
      } finally {
        connectInFlight = false;
      }
    }

    void connect();

    const handleWindowFocus = () => {
      triggerForegroundRecovery();
    };
    const handleOnline = () => {
      triggerForegroundRecovery();
    };
    const handleVisibilityChange = () => {
      if (document.visibilityState === 'visible') {
        triggerForegroundRecovery();
      }
    };

    window.addEventListener('focus', handleWindowFocus);
    window.addEventListener('online', handleOnline);
    document.addEventListener('visibilitychange', handleVisibilityChange);

    return () => {
      cancelled = true;
      window.removeEventListener('focus', handleWindowFocus);
      window.removeEventListener('online', handleOnline);
      document.removeEventListener('visibilitychange', handleVisibilityChange);
      if (reconnectTimerRef.current) {
        clearTimeout(reconnectTimerRef.current);
        reconnectTimerRef.current = null;
      }
      clientRef.current?.dispose();
      clientRef.current = null;
      clientReadyRef.current = false;
      setClientReady(false);
      setPromptInFlight(false);
      setPermissionQueue([]);
    };
  }, [conversationId, apiBaseUrl, websocketBaseUrl, onConversationTitle, onConversationUpdate, syncTranscript]);

  return {
    transcript,
    clientReady,
    promptInFlight,
    status,
    permissionQueue,
    sendPrompt,
    cancelPrompt,
    shiftPermissionQueue,
  };
}
