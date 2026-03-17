import { useState, useEffect, useCallback, useRef } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { toast } from 'sonner';
import { MenuIcon } from 'lucide-react';
import { request } from '@/lib/api';
import { useConfig } from '@/lib/config';
import { createACPClient } from '@/lib/acp-client';
import { createTranscript, applySessionUpdate, finalizeStreaming, getPreviewText } from '@/lib/acp-transcript';
import { readCachedTranscript, writeCachedTranscript } from '@/lib/acp-cache';
import { useNotice } from '@/components/notice-banner';
import { Sidebar } from '@/components/acp/sidebar';
import { ChatMessage } from '@/components/acp/message';
import { ThinkingBlock } from '@/components/acp/thinking-block';
import { Composer } from '@/components/acp/composer';
import { PermissionDialog } from '@/components/acp/permission-dialog';
import { Skeleton } from '@/components/ui/skeleton';
import { Button } from '@/components/ui/button';
import type { ACPClient, ACPTranscript, ConversationInfo, PermissionEntry } from '@/types/acp';
import type { Spritz } from '@/types/spritz';

interface AgentGroup {
  spritz: Spritz;
  conversations: ConversationInfo[];
}

export function ChatPage() {
  const { name } = useParams<{ name: string }>();
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

  const clientRef = useRef<ACPClient | null>(null);
  const transcriptRef = useRef<ACPTranscript>(transcript);
  const messagesEndRef = useRef<HTMLDivElement>(null);

  transcriptRef.current = transcript;

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
              `/acp/conversations?spritzName=${encodeURIComponent(spritz.metadata.name)}`,
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
            if (group.conversations.length > 0) {
              setSelectedConversation(group.conversations[0]);
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
  }, [name, showNotice]);

  useEffect(() => {
    fetchAgents();
  }, [fetchAgents]);

  // Connect to ACP when conversation selected
  useEffect(() => {
    if (!selectedConversation) return;

    let cancelled = false;
    const conversationId = selectedConversation.metadata.name;
    const spritzName = selectedConversation.spec?.spritzName || '';

    // Try loading from cache
    const cached = readCachedTranscript(conversationId);
    const newTranscript = cached || createTranscript();
    setTranscript(newTranscript);
    transcriptRef.current = newTranscript;

    const apiBase = config.apiBaseUrl || '';

    async function connect() {
      // Step 1: Bootstrap the conversation server-side
      setStatus('Bootstrapping…');
      let bootstrapData: Record<string, unknown>;
      try {
        bootstrapData = (await request<Record<string, unknown>>(
          `/acp/conversations/${encodeURIComponent(conversationId)}/bootstrap`,
          { method: 'POST' },
        )) || {};
      } catch (err) {
        setStatus(err instanceof Error ? err.message : 'Bootstrap failed');
        return;
      }

      if (cancelled) return;

      const effectiveSessionId = String(bootstrapData.effectiveSessionId || '');
      // Update conversation with sessionId from bootstrap
      const bootstrappedConversation: ConversationInfo = {
        metadata: selectedConversation!.metadata,
        spec: { ...selectedConversation!.spec, sessionId: effectiveSessionId },
        status: selectedConversation!.status,
      };

      // Step 2: Connect WebSocket to /connect endpoint (raw proxy)
      const wsProtocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
      const wsHost = window.location.host;
      const wsUrl = `${wsProtocol}//${wsHost}${apiBase}/acp/conversations/${encodeURIComponent(conversationId)}/connect`;

      const client = createACPClient({
        wsUrl,
        conversation: bootstrappedConversation,
      onStatus: (text) => setStatus(text),
      onReadyChange: (ready) => setClientReady(ready),
      onUpdate: (update) => {
        const t = transcriptRef.current;
        const result = applySessionUpdate(t, update as Record<string, unknown>);
        setTranscript({ ...t });

        if (result?.toast) {
          toast[result.toast.type === 'error' ? 'error' : 'info'](result.toast.message);
        }
        if (result?.conversationTitle) {
          setSelectedConversation((prev) =>
            prev ? { ...prev, spec: { ...prev.spec, title: result.conversationTitle! } } : prev,
          );
        }

        // Cache
        writeCachedTranscript(conversationId, t, {
          spritzName,
          sessionId: effectiveSessionId,
          preview: getPreviewText(t),
        });
      },
      onPermissionRequest: (entry) => {
        setPermissionQueue((prev) => [...prev, entry]);
      },
      onPromptStateChange: (inFlight) => {
        setPromptInFlight(inFlight);
        if (!inFlight) {
          finalizeStreaming(transcriptRef.current);
          setTranscript({ ...transcriptRef.current });
        }
      },
      onClose: () => {
        setStatus('Disconnected');
      },
    });

      clientRef.current = client;
      client.start().catch((err) => {
        if (!cancelled) {
          setStatus(err instanceof Error ? err.message : 'Connection failed');
        }
      });
    }

    connect();

    return () => {
      cancelled = true;
      clientRef.current?.dispose();
      clientRef.current = null;
      setClientReady(false);
      setPromptInFlight(false);
    };
  }, [selectedConversation, config.apiBaseUrl]);

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

      try {
        await client.sendPrompt(text);
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
  }, []);

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
          // Refresh agent list
          fetchAgents();
        }
      } catch (err) {
        toast.error(err instanceof Error ? err.message : 'Failed to create conversation.');
      }
    },
    [fetchAgents],
  );

  const handlePermissionRespond = useCallback(() => {
    setPermissionQueue((prev) => prev.slice(1));
  }, []);

  if (loading) {
    return (
      <div className="flex h-screen">
        <div className="hidden w-[260px] border-r bg-[#fafafa] p-4 md:block dark:bg-sidebar">
          <Skeleton className="mb-4 h-6 w-32" />
          <div className="space-y-2">
            <Skeleton className="h-8 w-full" />
            <Skeleton className="h-8 w-full" />
            <Skeleton className="h-8 w-full" />
          </div>
        </div>
        <div className="flex-1 p-8">
          <Skeleton className="h-8 w-48" />
        </div>
      </div>
    );
  }

  return (
    <div className="flex h-screen">
      <Sidebar
        agents={agents}
        selectedConversationId={selectedConversation?.metadata?.name || null}
        onSelectConversation={handleSelectConversation}
        onNewConversation={handleNewConversation}
        collapsed={sidebarCollapsed}
        onToggleCollapse={() => setSidebarCollapsed(!sidebarCollapsed)}
        mobileOpen={mobileMenuOpen}
        onCloseMobile={() => setMobileMenuOpen(false)}
      />

      <div className="flex flex-1 flex-col">
        {/* Header */}
        <div className="flex items-center gap-3 border-b bg-[#fafafa] px-4 py-2.5 dark:bg-muted/10">
          <Button
            variant="ghost"
            size="icon"
            className="size-8 md:hidden"
            onClick={() => setMobileMenuOpen(true)}
          >
            <MenuIcon className="size-4" />
          </Button>
          <div className="min-w-0 flex-1 text-sm font-medium truncate">
            {selectedConversation?.spec?.title || selectedConversation?.metadata?.name || 'Select a conversation'}
          </div>
          {status && (
            <span className="shrink-0 rounded-full border px-2.5 py-0.5 text-[10px] text-muted-foreground">
              {status}
            </span>
          )}
        </div>

        {/* Messages area */}
        <div className="relative flex-1 overflow-y-auto px-4 py-4">
          {!selectedConversation ? (
            <div className="flex h-full items-center justify-center text-muted-foreground">
              Select a conversation or create a new workspace.
            </div>
          ) : transcript.messages.length === 0 && !transcript.thinkingActive ? (
            <div className="flex h-full items-center justify-center text-muted-foreground">
              Start a conversation by sending a message.
            </div>
          ) : (
            <div className="mx-auto max-w-3xl space-y-4">
              {transcript.messages.map((message, i) => (
                <ChatMessage key={i} message={message} />
              ))}
              {transcript.thinkingActive && (
                <ThinkingBlock
                  chunks={transcript.thinkingChunks}
                  active={transcript.thinkingActive}
                  elapsedSeconds={transcript.thinkingElapsedSeconds}
                />
              )}
              {permissionQueue.length > 0 && (
                <PermissionDialog
                  entry={permissionQueue[0]}
                  onRespond={handlePermissionRespond}
                />
              )}
              <div ref={messagesEndRef} />
            </div>
          )}
          {/* Gradient fade at bottom */}
          <div className="pointer-events-none absolute inset-x-0 bottom-0 h-8 bg-gradient-to-t from-background to-transparent" />
        </div>

        {/* Composer */}
        {selectedConversation && (
          <Composer
            onSend={handleSend}
            onCancel={handleCancel}
            disabled={!clientReady}
            promptInFlight={promptInFlight}
          />
        )}
      </div>
    </div>
  );
}
