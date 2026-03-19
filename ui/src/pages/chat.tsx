import { useState, useEffect, useCallback, useRef } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { toast } from 'sonner';
import { MenuIcon, RotateCwIcon, ExternalLinkIcon } from 'lucide-react';
import { AlertDialog } from '@base-ui/react/alert-dialog';
import { request } from '@/lib/api';
import { cn } from '@/lib/utils';
import { useConfig } from '@/lib/config';
import { createACPClient } from '@/lib/acp-client';
import { createTranscript, applySessionUpdate, finalizeStreaming, finalizeHistoricalThinking, getPreviewText, isTranscriptBearingUpdate } from '@/lib/acp-transcript';
import { readCachedTranscript, writeCachedTranscript, evictCachedTranscript } from '@/lib/acp-cache';
import { useNotice } from '@/components/notice-banner';
import { Sidebar } from '@/components/acp/sidebar';
import { ChatMessage } from '@/components/acp/message';
import { ThinkingBlock } from '@/components/acp/thinking-block';
import { Composer } from '@/components/acp/composer';
import type { ComposerHandle } from '@/components/acp/composer';
import { PermissionDialog } from '@/components/acp/permission-dialog';
import { Skeleton } from '@/components/ui/skeleton';
import { Button } from '@/components/ui/button';
import { Tooltip, TooltipTrigger, TooltipContent } from '@/components/ui/tooltip';
import type { ACPClient, ACPTranscript, ConversationInfo, PermissionEntry } from '@/types/acp';
import type { Spritz } from '@/types/spritz';

interface AgentGroup {
  spritz: Spritz;
  conversations: ConversationInfo[];
}

const RECONNECT_DELAY_MS = 2000;

export function ChatPage() {
  const { name, conversationId: urlConversationId } = useParams<{ name: string; conversationId: string }>();
  const navigate = useNavigate();
  const config = useConfig();
  const { showNotice } = useNotice();

  const [agents, setAgents] = useState<AgentGroup[]>([]);
  const [loading, setLoading] = useState(true);
  const [selectedConversation, setSelectedConversation] = useState<ConversationInfo | null>(null);
  const [transcript, setTranscript] = useState<ACPTranscript>(createTranscript());
  const [promptInFlight, setPromptInFlight] = useState(false);
  const [clientReady, setClientReady] = useState(false);
  const [status, setStatus] = useState('');
  const [permissionQueue, setPermissionQueue] = useState<PermissionEntry[]>([]);
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);
  const [mobileMenuOpen, setMobileMenuOpen] = useState(false);
  const [pendingDeleteId, setPendingDeleteId] = useState<string | null>(null);

  const clientRef = useRef<ACPClient | null>(null);
  const transcriptRef = useRef<ACPTranscript>(transcript);
  const messagesEndRef = useRef<HTMLDivElement>(null);
  const composerRef = useRef<ComposerHandle>(null);
  const reconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const selectedConversationRef = useRef<ConversationInfo | null>(null);
  // Track whether cached transcript has been replaced by live replay data
  const cacheHydratedRef = useRef(false);
  const cacheReplacedByReplayRef = useRef(false);

  transcriptRef.current = transcript;
  selectedConversationRef.current = selectedConversation;

  // Fetch agents and conversations
  const fetchAgents = useCallback(async () => {
    try {
      const spritzes = await request<{ items: Spritz[] }>('/spritzes');
      const items = spritzes?.items || [];
      const acpReady = items.filter(
        (s) => s.status?.phase === 'Ready' && s.status?.acp?.state === 'ready',
      );

      const groups: AgentGroup[] = await Promise.all(
        acpReady.map(async (spritz) => {
          try {
            const convData = await request<{ items: ConversationInfo[] }>(
              `/acp/conversations?spritz=${encodeURIComponent(spritz.metadata.name)}`,
            );
            return { spritz, conversations: convData?.items || [] };
          } catch {
            return { spritz, conversations: [] };
          }
        }),
      );

      setAgents(groups);

      // Auto-select or auto-create conversation if name param matches a spritz
      if (name) {
        for (const group of groups) {
          if (group.spritz.metadata.name === name) {
            // If a specific conversation was requested via URL, prefer it
            if (urlConversationId) {
              const match = group.conversations.find((c) => c.metadata.name === urlConversationId);
              if (match) {
                setSelectedConversation(match);
                break;
              }
            }
            if (group.conversations.length > 0) {
              const conv = group.conversations[0];
              setSelectedConversation(conv);
              navigate(`/chat/${encodeURIComponent(name)}/${encodeURIComponent(conv.metadata.name)}`, { replace: true });
            } else {
              // Auto-create a conversation for this spritz
              try {
                const conv = await request<ConversationInfo>('/acp/conversations', {
                  method: 'POST',
                  headers: { 'Content-Type': 'application/json' },
                  body: JSON.stringify({ spritzName: name }),
                });
                if (conv) {
                  group.conversations.push(conv);
                  setSelectedConversation(conv);
                  navigate(`/chat/${encodeURIComponent(name)}/${encodeURIComponent(conv.metadata.name)}`, { replace: true });
                }
              } catch {
                // Failed to auto-create, user can do it manually
              }
            }
            break;
          }
        }
      }
    } catch (err) {
      showNotice(err instanceof Error ? err.message : 'Failed to load agents.');
    } finally {
      setLoading(false);
    }
  }, [name, urlConversationId, navigate, showNotice]);

  useEffect(() => {
    fetchAgents();
  }, [fetchAgents]);

  // Connect to ACP when conversation selected
  useEffect(() => {
    if (!selectedConversation) return;

    let cancelled = false;
    let retryCount = 0;
    const conversationId = selectedConversation.metadata.name;
    const spritzName = selectedConversation.spec?.spritzName || '';

    // Try loading from cache
    const cached = readCachedTranscript(conversationId);
    const newTranscript = cached || createTranscript();
    setTranscript(newTranscript);
    transcriptRef.current = newTranscript;
    cacheHydratedRef.current = newTranscript.messages.length > 0;
    cacheReplacedByReplayRef.current = false;

    const apiBase = config.apiBaseUrl || '';

    function needsBootstrap(conv: ConversationInfo, force?: boolean): boolean {
      if (force) return true;
      const sessionId = String(conv.spec?.sessionId || '').trim();
      if (!sessionId) return true;
      return String(conv.status?.bindingState || '').trim().toLowerCase() !== 'active';
    }

    async function connect(options: { forceBootstrap?: boolean } = {}) {
      if (cancelled) return;

      let effectiveConversation = selectedConversation!;
      let effectiveSessionId = String(effectiveConversation.spec?.sessionId || '').trim();

      // Step 1: Bootstrap if needed
      if (needsBootstrap(effectiveConversation, options.forceBootstrap)) {
        setStatus('Bootstrapping…');
        let bootstrapData: Record<string, unknown>;
        try {
          bootstrapData = (await request<Record<string, unknown>>(
            `/acp/conversations/${encodeURIComponent(conversationId)}/bootstrap`,
            { method: 'POST' },
          )) || {};
        } catch (err) {
          if (!cancelled) setStatus(err instanceof Error ? err.message : 'Bootstrap failed');
          return;
        }
        if (cancelled) return;

        const newSessionId = String(bootstrapData.effectiveSessionId || '');
        const replaced = Boolean(bootstrapData.replaced) ||
          (effectiveSessionId && newSessionId && effectiveSessionId !== newSessionId);

        // If session was replaced, clear stale cache
        if (replaced) {
          evictCachedTranscript(conversationId);
          const freshTranscript = createTranscript();
          setTranscript(freshTranscript);
          transcriptRef.current = freshTranscript;
          cacheHydratedRef.current = false;
          cacheReplacedByReplayRef.current = false;
        }

        effectiveSessionId = newSessionId;
        effectiveConversation = {
          metadata: selectedConversation!.metadata,
          spec: { ...selectedConversation!.spec, sessionId: effectiveSessionId },
          status: { ...selectedConversation!.status, bindingState: 'active' },
        };
        setSelectedConversation(effectiveConversation);
      }

      if (cancelled) return;

      // Step 2: Connect WebSocket
      const wsProtocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
      const wsHost = window.location.host;
      const wsUrl = `${wsProtocol}//${wsHost}${apiBase}/acp/conversations/${encodeURIComponent(conversationId)}/connect`;

      const client = createACPClient({
        wsUrl,
        conversation: effectiveConversation,
        onStatus: (text) => { if (!cancelled) setStatus(text); },
        onReadyChange: (ready) => { if (!cancelled) setClientReady(ready); },
        onUpdate: (update, opts) => {
          if (cancelled) return;
          const updateObj = update as Record<string, unknown>;

          // Cache/replay disambiguation: if we hydrated from cache and the
          // server is now replaying real transcript data, clear the stale
          // cached transcript so it doesn't mix with the fresh replay.
          if (
            cacheHydratedRef.current &&
            !cacheReplacedByReplayRef.current &&
            isTranscriptBearingUpdate(updateObj)
          ) {
            const freshTranscript = createTranscript();
            transcriptRef.current = freshTranscript;
            cacheReplacedByReplayRef.current = true;
          }

          const t = transcriptRef.current;
          const result = applySessionUpdate(t, updateObj, { historical: opts?.historical });
          setTranscript({ ...t });

          if (result?.toast) {
            toast[result.toast.type === 'error' ? 'error' : 'info'](result.toast.message);
          }
          // Persist title to server (matching staging behavior)
          if (result?.conversationTitle) {
            const newTitle = result.conversationTitle;
            setSelectedConversation((prev) =>
              prev ? { ...prev, spec: { ...prev.spec, title: newTitle } } : prev,
            );
            // Update sidebar conversation list with new title
            setAgents((prev) =>
              prev.map((group) => ({
                ...group,
                conversations: group.conversations.map((conv) =>
                  conv.metadata.name === conversationId
                    ? { ...conv, spec: { ...conv.spec, title: newTitle } }
                    : conv,
                ),
              })),
            );
            request(`/acp/conversations/${encodeURIComponent(conversationId)}`, {
              method: 'PATCH',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ title: newTitle }),
            }).catch(() => {});
          }

          // Cache
          writeCachedTranscript(conversationId, t, {
            spritzName,
            sessionId: effectiveSessionId,
            preview: getPreviewText(t),
          });
        },
        onPermissionRequest: (entry) => {
          if (!cancelled) setPermissionQueue((prev) => [...prev, entry]);
        },
        onPromptStateChange: (inFlight) => {
          if (!cancelled) {
            setPromptInFlight(inFlight);
            if (!inFlight) {
              finalizeStreaming(transcriptRef.current);
              setTranscript({ ...transcriptRef.current });
            }
          }
        },
        onClose: () => {
          if (cancelled) return;
          setStatus('Disconnected. Reconnecting…');
          // Auto-reconnect after delay (matching staging behavior)
          reconnectTimerRef.current = setTimeout(() => {
            if (cancelled) return;
            retryCount++;
            connect({ forceBootstrap: retryCount > 1 }).catch((err) => {
              if (!cancelled) setStatus(err instanceof Error ? err.message : 'Reconnect failed');
            });
          }, RECONNECT_DELAY_MS);
        },
      });

      clientRef.current = client;

      try {
        await client.start();
      } catch (err) {
        const error = err as Error & { code?: string };
        // Auto-retry with forced bootstrap if session is missing
        if (error.code === 'ACP_SESSION_MISSING' && retryCount === 0) {
          retryCount++;
          client.dispose();
          clientRef.current = null;
          await connect({ forceBootstrap: true });
          return;
        }
        if (!cancelled) {
          setStatus(error.message || 'Connection failed');
        }
        return;
      }

      if (cancelled) return;

      // If cache was hydrated but replay sent no real messages, clear stale cache
      if (cacheHydratedRef.current && !cacheReplacedByReplayRef.current) {
        const freshTranscript = createTranscript();
        setTranscript(freshTranscript);
        transcriptRef.current = freshTranscript;
      }

      // Bake any leftover thinking chunks from replay into thinking_done messages
      finalizeHistoricalThinking(transcriptRef.current);
      setTranscript({ ...transcriptRef.current });

      cacheHydratedRef.current = false;
      retryCount = 0;

      // Write final cache after bootstrap+replay complete
      writeCachedTranscript(conversationId, transcriptRef.current, {
        spritzName,
        sessionId: effectiveSessionId,
        preview: getPreviewText(transcriptRef.current),
      });
    }

    connect();

    return () => {
      cancelled = true;
      if (reconnectTimerRef.current) {
        clearTimeout(reconnectTimerRef.current);
        reconnectTimerRef.current = null;
      }
      clientRef.current?.dispose();
      clientRef.current = null;
      setClientReady(false);
      setPromptInFlight(false);
    };
  }, [selectedConversation?.metadata?.name, config.apiBaseUrl]);

  // Auto-focus composer when conversation becomes ready or agent finishes responding
  useEffect(() => {
    if (clientReady && !promptInFlight) {
      composerRef.current?.focus();
    }
  }, [clientReady, promptInFlight]);

  // Auto-scroll
  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [transcript.messages.length]);

  const handleSend = useCallback(
    async (text: string) => {
      const client = clientRef.current;
      if (!client || !client.isReady()) return;

      // Optimistically add user message
      const t = transcriptRef.current;
      t.messages.push({
        role: 'user',
        blocks: [{ type: 'text', text }],
        streaming: false,
      });
      setTranscript({ ...t });

      // Set title from first message if conversation has no real title
      const currentTitle = selectedConversationRef.current?.spec?.title || '';
      if (!currentTitle || currentTitle === 'New conversation') {
        const nextTitle = text.slice(0, 80);
        const convId = selectedConversationRef.current?.metadata?.name || '';
        const spritzN = selectedConversationRef.current?.spec?.spritzName || '';
        setSelectedConversation((prev) =>
          prev ? { ...prev, spec: { ...prev.spec, title: nextTitle } } : prev,
        );
        setAgents((prev) =>
          prev.map((group) => ({
            ...group,
            conversations: group.conversations.map((conv) =>
              conv.metadata.name === convId
                ? { ...conv, spec: { ...conv.spec, title: nextTitle } }
                : conv,
            ),
          })),
        );
        if (convId) {
          request(`/acp/conversations/${encodeURIComponent(convId)}`, {
            method: 'PATCH',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ title: nextTitle }),
          }).catch(() => {});
        }
      }

      setStatus('Waiting for agent\u2026');
      try {
        await client.sendPrompt(text);
        setStatus('Completed');
      } catch (err) {
        toast.error(err instanceof Error ? err.message : 'Failed to send message.');
      }
    },
    [],
  );

  const handleCancel = useCallback(() => {
    clientRef.current?.cancelPrompt();
  }, []);

  const handleSelectConversation = useCallback((conv: ConversationInfo) => {
    setSelectedConversation(conv);
    setPermissionQueue([]);
    const spritzName = conv.spec?.spritzName || name || '';
    if (spritzName) {
      navigate(`/chat/${encodeURIComponent(spritzName)}/${encodeURIComponent(conv.metadata.name)}`, { replace: true });
    }
  }, [name, navigate]);

  const handleNewConversation = useCallback(
    async (spritzName: string) => {
      try {
        const conv = await request<ConversationInfo>('/acp/conversations', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ spritzName }),
        });
        if (conv) {
          setSelectedConversation(conv);
          navigate(`/chat/${encodeURIComponent(spritzName)}/${encodeURIComponent(conv.metadata.name)}`, { replace: true });
          // Refresh agent list
          fetchAgents();
        }
      } catch (err) {
        toast.error(err instanceof Error ? err.message : 'Failed to create conversation.');
      }
    },
    [fetchAgents, navigate],
  );

  const handlePermissionRespond = useCallback(() => {
    setPermissionQueue((prev) => prev.slice(1));
  }, []);

  const handleDeleteConversation = useCallback(
    (conversationId: string) => {
      setPendingDeleteId(conversationId);
    },
    [],
  );

  const confirmDeleteConversation = useCallback(async () => {
    const conversationId = pendingDeleteId;
    if (!conversationId) return;
    setPendingDeleteId(null);
    try {
      await request(`/acp/conversations/${encodeURIComponent(conversationId)}`, {
        method: 'DELETE',
      });
      evictCachedTranscript(conversationId);
      setAgents((prev) =>
        prev.map((group) => ({
          ...group,
          conversations: group.conversations.filter(
            (c) => c.metadata.name !== conversationId,
          ),
        })),
      );
      if (selectedConversation?.metadata?.name === conversationId) {
        setSelectedConversation(null);
        navigate('/', { replace: true });
      }
      toast.info('Conversation deleted.');
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to delete conversation.');
    }
  }, [pendingDeleteId, selectedConversation, navigate]);

  if (loading) {
    return (
      <div className="grid h-dvh grid-cols-[1fr] md:grid-cols-[260px_minmax(0,1fr)] overflow-hidden">
        <div className="hidden md:flex flex-col gap-2 border-r border-[#e5e5e5] bg-[#fafafa] p-3 dark:border-border dark:bg-sidebar">
          <Skeleton className="h-9 w-full rounded-lg" />
          <Skeleton className="h-9 w-full rounded-lg" />
          <div className="flex flex-col gap-1">
            <Skeleton className="h-8 w-full rounded-[10px]" />
            <Skeleton className="h-8 w-full rounded-[10px]" />
            <Skeleton className="h-8 w-full rounded-[10px]" />
          </div>
        </div>
        <div className="flex flex-col">
          <div className="shrink-0 border-b border-[#e5e5e5] bg-[#fafafa] px-5 py-3 dark:border-border dark:bg-muted/10">
            <Skeleton className="h-4 w-48" />
          </div>
          <div className="flex-1 p-8" />
        </div>
      </div>
    );
  }

  return (
    <div
      className={cn(
        'grid h-dvh overflow-hidden transition-[grid-template-columns] duration-200 will-change-[grid-template-columns]',
        'grid-cols-[1fr] md:grid-cols-[260px_minmax(0,1fr)]',
        sidebarCollapsed && 'md:grid-cols-[56px_minmax(0,1fr)]',
      )}
    >
      <Sidebar
        agents={agents}
        selectedConversationId={selectedConversation?.metadata?.name || null}
        onSelectConversation={handleSelectConversation}
        onNewConversation={handleNewConversation}
        onDeleteConversation={handleDeleteConversation}
        collapsed={sidebarCollapsed}
        onToggleCollapse={() => setSidebarCollapsed(!sidebarCollapsed)}
        mobileOpen={mobileMenuOpen}
        onCloseMobile={() => setMobileMenuOpen(false)}
      />

      <div className="flex min-h-0 min-w-0 flex-col overflow-hidden bg-white dark:bg-background">
        {/* Header — matches original acp-main-header */}
        <div className="shrink-0 border-b border-[#e5e5e5] bg-[#fafafa] px-5 py-3 dark:border-border dark:bg-muted/10">
          <div className="flex items-center justify-between gap-3">
            <button
              className="inline-flex size-9 items-center justify-center rounded-[10px] border border-[#e5e5e5] bg-white text-black transition-colors hover:bg-[#f5f5f5] hover:border-[#ccc] md:hidden dark:border-border dark:bg-background dark:text-foreground"
              onClick={() => setMobileMenuOpen(true)}
            >
              <MenuIcon className="size-4" />
            </button>
            <div className="min-w-0 flex-1">
              <h2 className="m-0 truncate text-sm font-medium">
                {selectedConversation?.spec?.title || selectedConversation?.metadata?.name || 'Select a conversation'}
              </h2>
              {selectedConversation && (() => {
                const spritzName = selectedConversation.spec?.spritzName || '';
                const group = agents.find((g) => g.spritz.metadata.name === spritzName);
                const version = group?.spritz?.status?.acp?.agentInfo?.version;
                const parts = [spritzName, version ? `v${version}` : ''].filter(Boolean);
                return parts.length > 0 ? (
                  <p className="m-0 truncate text-xs opacity-60">{parts.join(' · ')}</p>
                ) : null;
              })()}
            </div>
            <div className="flex shrink-0 gap-2">
              <Tooltip>
                <TooltipTrigger
                  render={
                    <button
                      type="button"
                      className="inline-flex size-9 items-center justify-center rounded-[10px] border border-[#e5e5e5] bg-white text-black transition-colors hover:bg-[#f5f5f5] hover:border-[#ccc] dark:border-border dark:bg-background dark:text-foreground"
                      onClick={() => fetchAgents()}
                    />
                  }
                >
                  <RotateCwIcon className="size-4" />
                </TooltipTrigger>
                <TooltipContent>Refresh</TooltipContent>
              </Tooltip>
              {name && (
                <Tooltip>
                  <TooltipTrigger
                    render={
                      <a
                        href={`/terminal/${encodeURIComponent(name)}`}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="inline-flex size-9 items-center justify-center rounded-[10px] border border-[#e5e5e5] bg-white text-black transition-colors hover:bg-[#f5f5f5] hover:border-[#ccc] dark:border-border dark:bg-background dark:text-foreground"
                      />
                    }
                  >
                    <ExternalLinkIcon className="size-4" />
                  </TooltipTrigger>
                  <TooltipContent>Open instance</TooltipContent>
                </Tooltip>
              )}
            </div>
          </div>
          {/* Command bar — clickable slash command pills */}
          {transcript.availableCommands.length > 0 && (
            <div className="mt-2 flex flex-wrap gap-2 scrollbar-hidden overflow-x-auto">
              {transcript.availableCommands.map((cmd) => {
                const name = typeof cmd === 'string' ? cmd : (cmd as Record<string, string>).name || '';
                const description = typeof cmd === 'string' ? '' : (cmd as Record<string, string>).description || '';
                if (!name) return null;
                return (
                  <button
                    key={name}
                    type="button"
                    className="inline-flex items-center gap-1.5 rounded-full border border-[#e5e5e5] bg-white px-2.5 py-1.5 text-xs cursor-pointer transition-colors hover:bg-[#f5f5f5] hover:border-[#ccc] shrink-0"
                    title={description}
                    onClick={() => composerRef.current?.fillText(`/${name} `)}
                  >
                    /{name}
                  </button>
                );
              })}
            </div>
          )}
        </div>

        {/* Messages area */}
        <div className="flex flex-1 flex-col overflow-auto px-6 pt-7 pb-3" style={{ scrollbarGutter: 'stable' }}>
          {!selectedConversation ? (
            <div className="m-auto max-w-[420px] text-center text-sm opacity-70">
              Select a conversation or create a new instance.
            </div>
          ) : transcript.messages.length === 0 ? (
            <div className="m-auto flex max-w-[540px] flex-col gap-1.5 text-center">
              <strong className="block text-base font-medium">Start a conversation</strong>
              <p className="m-0 text-sm text-[#999]">Send a message to begin.</p>
            </div>
          ) : (
            <div className="mx-auto w-full max-w-[880px] flex flex-col gap-6 flex-1">
              {transcript.messages.map((message, i) => {
                const elements = [<ChatMessage key={`msg-${i}`} message={message} />];
                // Insert thinking block at its correct position (before the assistant response)
                if (
                  i === transcript.thinkingInsertIndex &&
                  (transcript.thinkingActive || transcript.thinkingChunks.length > 0)
                ) {
                  elements.unshift(
                    <ThinkingBlock
                      key="thinking"
                      chunks={transcript.thinkingChunks}
                      active={transcript.thinkingActive}
                      elapsedSeconds={transcript.thinkingElapsedSeconds}
                    />,
                  );
                }
                return elements;
              })}
              {/* If thinking insert index is past all messages, render at end */}
              {(transcript.thinkingActive || transcript.thinkingChunks.length > 0) &&
                transcript.thinkingInsertIndex >= transcript.messages.length && (
                <ThinkingBlock
                  key="thinking"
                  chunks={transcript.thinkingChunks}
                  active={transcript.thinkingActive}
                  elapsedSeconds={transcript.thinkingElapsedSeconds}
                />
              )}
              <div ref={messagesEndRef} />
            </div>
          )}
        </div>

        {/* Permission prompt + Composer */}
        {selectedConversation && (
          <div className="shrink-0">
            {permissionQueue.length > 0 && (
              <div className="mx-auto max-w-[880px] px-6 pb-2">
                <PermissionDialog
                  entry={permissionQueue[0]}
                  onRespond={handlePermissionRespond}
                />
              </div>
            )}
            <Composer
              ref={composerRef}
              onSend={handleSend}
              onCancel={handleCancel}
              disabled={!clientReady}
              promptInFlight={promptInFlight}
              status={status}
            />
          </div>
        )}
      </div>

      {/* Delete conversation confirmation */}
      <AlertDialog.Root open={!!pendingDeleteId} onOpenChange={(open) => { if (!open) setPendingDeleteId(null); }}>
        <AlertDialog.Portal>
          <AlertDialog.Backdrop className="fixed inset-0 z-50 bg-black/30" />
          <AlertDialog.Popup className="fixed left-1/2 top-1/2 z-50 w-[90vw] max-w-[360px] -translate-x-1/2 -translate-y-1/2 rounded-2xl border border-[#e5e5e5] bg-white p-6 shadow-xl dark:border-border dark:bg-popover">
            <AlertDialog.Title className="text-[15px] font-semibold">
              Delete conversation?
            </AlertDialog.Title>
            <AlertDialog.Description className="mt-2 text-sm text-muted-foreground">
              This will permanently delete this conversation and its history. This action cannot be undone.
            </AlertDialog.Description>
            <div className="mt-5 flex justify-end gap-2">
              <AlertDialog.Close
                className="rounded-lg border border-[#e5e5e5] bg-white px-3.5 py-2 text-[13px] font-medium transition-colors hover:bg-[#f5f5f5] dark:border-border dark:bg-muted dark:hover:bg-muted/80"
              >
                Cancel
              </AlertDialog.Close>
              <button
                type="button"
                className="rounded-lg bg-red-600 px-3.5 py-2 text-[13px] font-medium text-white transition-colors hover:bg-red-700"
                onClick={confirmDeleteConversation}
              >
                Delete
              </button>
            </div>
          </AlertDialog.Popup>
        </AlertDialog.Portal>
      </AlertDialog.Root>
    </div>
  );
}

