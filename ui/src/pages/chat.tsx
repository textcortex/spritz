import { useState, useEffect, useCallback, useRef } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { toast } from 'sonner';
import { MenuIcon, RotateCwIcon, ExternalLinkIcon } from 'lucide-react';
import { request } from '@/lib/api';
import { cn } from '@/lib/utils';
import { useConfig } from '@/lib/config';
import { useChatConnection } from '@/lib/use-chat-connection';
import { readChatDraft, writeChatDraft, clearChatDraft } from '@/lib/chat-draft';
import { buildFallbackConversationTitle, hasDurableConversationTitle } from '@/lib/conversation-title';
import {
  buildProvisioningPlaceholderSpritz,
  getProvisioningStatusLine,
  isSpritzChatReady,
} from '@/lib/provisioning';
import { chatConversationPath } from '@/lib/urls';
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
import type { ConversationInfo } from '@/types/acp';
import type { Spritz } from '@/types/spritz';

interface AgentGroup {
  spritz: Spritz;
  conversations: ConversationInfo[];
}

const PROVISIONING_POLL_INTERVAL_MS = 2000;

function getConversationActivityTime(conversation: ConversationInfo): number {
  const raw = String(conversation.status?.lastActivityAt || '').trim();
  if (!raw) return Number.NEGATIVE_INFINITY;
  const parsed = Date.parse(raw);
  return Number.isFinite(parsed) ? parsed : Number.NEGATIVE_INFINITY;
}

function sortConversationsByRecency(conversations: ConversationInfo[]): ConversationInfo[] {
  return [...conversations].sort((left, right) => {
    const diff = getConversationActivityTime(right) - getConversationActivityTime(left);
    return Number.isFinite(diff) ? diff : 0;
  });
}

function getLatestConversation(conversations: ConversationInfo[]): ConversationInfo | null {
  return sortConversationsByRecency(conversations)[0] || null;
}

export function ChatPage() {
  const { name, conversationId: urlConversationId } = useParams<{ name: string; conversationId: string }>();
  const navigate = useNavigate();
  const config = useConfig();
  const { showNotice } = useNotice();

  const [agents, setAgents] = useState<AgentGroup[]>([]);
  const [spritzes, setSpritzes] = useState<Spritz[]>([]);
  const [loading, setLoading] = useState(true);
  const [selectedConversation, setSelectedConversation] = useState<ConversationInfo | null>(null);
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);
  const [mobileMenuOpen, setMobileMenuOpen] = useState(false);
  const [creatingConversationFor, setCreatingConversationFor] = useState<string | null>(null);
  const [composerText, setComposerText] = useState('');

  const messagesEndRef = useRef<HTMLDivElement>(null);
  const composerRef = useRef<ComposerHandle>(null);
  const selectedConversationRef = useRef<ConversationInfo | null>(null);
  const autoCreatingConversationForRef = useRef<string | null>(null);

  selectedConversationRef.current = selectedConversation;

  const selectedSpritzName = selectedConversation?.spec?.spritzName || name || '';
  const selectedConversationId = selectedConversation?.metadata?.name || '';
  const focusedSpritz = name
    ? spritzes.find((spritz) => spritz.metadata.name === name) || buildProvisioningPlaceholderSpritz(name)
    : null;
  const provisioningSpritz = focusedSpritz && !isSpritzChatReady(focusedSpritz)
    ? focusedSpritz
    : null;
  const provisioningStatusLine = getProvisioningStatusLine(provisioningSpritz);

  // Fetch agents and conversations
  const fetchAgents = useCallback(async (): Promise<boolean> => {
    try {
      const spritzList = await request<{ items: Spritz[] }>('/spritzes');
      let items = spritzList?.items || [];
      if (name && !items.some((spritz) => spritz.metadata.name === name)) {
        try {
          const routeSpritz = await request<Spritz>(`/spritzes/${encodeURIComponent(name)}`);
          if (routeSpritz?.metadata?.name === name) {
            items = [routeSpritz, ...items];
          }
        } catch {
          // Fall back to the list result when the route lookup is unavailable.
        }
      }
      setSpritzes(items);
      const acpReady = items.filter((spritz) => isSpritzChatReady(spritz));

      const groups: AgentGroup[] = await Promise.all(
        acpReady.map(async (spritz) => {
          try {
            const convData = await request<{ items: ConversationInfo[] }>(
              `/acp/conversations?spritz=${encodeURIComponent(spritz.metadata.name)}`,
            );
            return {
              spritz,
              conversations: sortConversationsByRecency(convData?.items || []),
            };
          } catch {
            return { spritz, conversations: [] };
          }
        }),
      );

      setAgents(groups);

      if (!name) {
        return false;
      }

      const routeSpritz = items.find((spritz) => spritz.metadata.name === name);
      if (!routeSpritz) {
        setSelectedConversation(null);
        return true;
      }
      if (!isSpritzChatReady(routeSpritz)) {
        setSelectedConversation(null);
        return true;
      }

      const group = groups.find((entry) => entry.spritz.metadata.name === name);
      if (!group) {
        setSelectedConversation(null);
        return false;
      }

      if (urlConversationId) {
        const match = group.conversations.find((conversation) => conversation.metadata.name === urlConversationId);
        if (match) {
          setSelectedConversation(match);
          return false;
        }
      }

      const latestConversation = getLatestConversation(group.conversations);
      if (latestConversation) {
        setSelectedConversation(latestConversation);
        if (urlConversationId !== latestConversation.metadata.name) {
          navigate(chatConversationPath(name, latestConversation.metadata.name), { replace: true });
        }
        return false;
      }

      if (autoCreatingConversationForRef.current === name) {
        return false;
      }

      autoCreatingConversationForRef.current = name;
      setCreatingConversationFor(name);
      try {
        const conv = await request<ConversationInfo>('/acp/conversations', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ spritzName: name }),
        });
        if (conv) {
          setAgents((currentGroups) =>
            currentGroups.map((currentGroup) =>
              currentGroup.spritz.metadata.name === name
                ? {
                    ...currentGroup,
                    conversations: sortConversationsByRecency([...currentGroup.conversations, conv]),
                  }
                : currentGroup,
            ),
          );
          setSelectedConversation(conv);
          navigate(chatConversationPath(name, conv.metadata.name), { replace: true });
        }
      } catch (err) {
        showNotice(err instanceof Error ? err.message : 'Failed to start a conversation.');
      } finally {
        autoCreatingConversationForRef.current = null;
        setCreatingConversationFor((current) => (current === name ? null : current));
      }
      return false;
    } catch (err) {
      showNotice(err instanceof Error ? err.message : 'Failed to load agents.');
      return false;
    } finally {
      setLoading(false);
    }
  }, [name, urlConversationId, navigate, showNotice]);

  useEffect(() => {
    fetchAgents();
  }, [fetchAgents]);

  useEffect(() => {
    if (!provisioningSpritz) {
      return;
    }
    let cancelled = false;
    let timerId: number | null = null;

    const scheduleNextPoll = () => {
      timerId = window.setTimeout(() => {
        void pollUntilReady();
      }, PROVISIONING_POLL_INTERVAL_MS);
    };

    const pollUntilReady = async () => {
      const shouldContinuePolling = await fetchAgents();
      if (cancelled || !shouldContinuePolling) {
        return;
      }
      scheduleNextPoll();
    };

    scheduleNextPoll();
    return () => {
      cancelled = true;
      if (timerId !== null) {
        window.clearTimeout(timerId);
      }
    };
  }, [fetchAgents, provisioningSpritz?.metadata.name]);

  const applyConversationTitle = useCallback((conversationId: string, title?: string | null) => {
    const normalized = String(title || '').trim();
    if (!conversationId || !normalized) return;
    setSelectedConversation((prev) =>
      prev && prev.metadata.name === conversationId
        ? { ...prev, spec: { ...prev.spec, title: normalized } }
        : prev,
    );
    setAgents((prev) =>
      prev.map((group) => ({
        ...group,
        conversations: group.conversations.map((conv) =>
          conv.metadata.name === conversationId
            ? { ...conv, spec: { ...conv.spec, title: normalized } }
            : conv,
        ),
      })),
    );
  }, []);

  const {
    transcript,
    clientReady,
    promptInFlight,
    status,
    permissionQueue,
    sendPrompt,
    cancelPrompt,
    shiftPermissionQueue,
  } = useChatConnection({
    conversation: selectedConversation,
    apiBaseUrl: config.apiBaseUrl || '',
    websocketBaseUrl: config.websocketBaseUrl || '',
    onConversationUpdate: setSelectedConversation,
    onConversationTitle: applyConversationTitle,
  });

  useEffect(() => {
    if (!selectedSpritzName || !selectedConversationId) {
      setComposerText('');
      return;
    }
    setComposerText(readChatDraft(selectedSpritzName, selectedConversationId) || '');
  }, [selectedSpritzName, selectedConversationId]);

  useEffect(() => {
    if (!name || !selectedConversation) return;
    const conversationSpritzName = selectedConversation.spec?.spritzName || name;
    const targetConversationId = selectedConversation.metadata?.name || '';
    if (conversationSpritzName !== name || !targetConversationId || urlConversationId === targetConversationId) {
      return;
    }
    navigate(chatConversationPath(name, targetConversationId), { replace: true });
  }, [name, navigate, selectedConversation, urlConversationId]);

  useEffect(() => {
    if (!selectedSpritzName || !selectedConversationId) return;
    writeChatDraft(selectedSpritzName, selectedConversationId, composerText);
  }, [selectedSpritzName, selectedConversationId, composerText]);

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
      const activeConversation = selectedConversationRef.current;
      const activeConversationId = activeConversation?.metadata?.name || '';
      const activeSpritzName = activeConversation?.spec?.spritzName || name || '';
      const previousComposerText = composerText;
      const currentTitle = activeConversation?.spec?.title || '';
      const fallbackTitle = hasDurableConversationTitle(currentTitle)
        ? ''
        : buildFallbackConversationTitle(text);

      // ACP owns durable transcript entries, including the echoed user prompt.
      // Keep send feedback in ephemeral UI state and wait for ACP to write the
      // real message so the transcript cannot diverge or duplicate.
      try {
        await sendPrompt(text);
        if (activeConversationId && fallbackTitle) {
          applyConversationTitle(activeConversationId, fallbackTitle);
          request<ConversationInfo>(`/acp/conversations/${encodeURIComponent(activeConversationId)}`, {
            method: 'PATCH',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ title: fallbackTitle }),
          }).catch(() => {});
        }
        if (activeConversationId && activeSpritzName) {
          clearChatDraft(activeSpritzName, activeConversationId);
          if (selectedConversationRef.current?.metadata?.name === activeConversationId) {
            setComposerText('');
          }
        }
      } catch (err) {
        if (activeConversationId && activeSpritzName) {
          writeChatDraft(activeSpritzName, activeConversationId, previousComposerText);
          if (selectedConversationRef.current?.metadata?.name === activeConversationId) {
            setComposerText(previousComposerText);
          }
        }
        toast.error(err instanceof Error ? err.message : 'Failed to send message.');
      }
    },
    [applyConversationTitle, composerText, name, sendPrompt],
  );

  const handleSelectConversation = useCallback((conv: ConversationInfo) => {
    setSelectedConversation(conv);
    const spritzName = conv.spec?.spritzName || name || '';
    if (spritzName) {
      navigate(chatConversationPath(spritzName, conv.metadata.name), { replace: true });
    }
  }, [name, navigate]);

  const handleNewConversation = useCallback(
    async (spritzName: string) => {
      const normalizedSpritzName = String(spritzName || '').trim();
      if (!normalizedSpritzName) return;
      if (creatingConversationFor === normalizedSpritzName) {
        return;
      }
      setCreatingConversationFor(normalizedSpritzName);
      try {
        const conv = await request<ConversationInfo>('/acp/conversations', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ spritzName: normalizedSpritzName }),
        });
        if (conv) {
          setSelectedConversation(conv);
          navigate(chatConversationPath(normalizedSpritzName, conv.metadata.name), { replace: true });
          // Refresh agent list
          fetchAgents();
        }
      } catch (err) {
        toast.error(err instanceof Error ? err.message : 'Failed to create conversation.');
      } finally {
        setCreatingConversationFor((current) => (current === normalizedSpritzName ? null : current));
      }
    },
    [creatingConversationFor, fetchAgents, navigate],
  );

  if (loading) {
    return (
      <div className="grid h-dvh grid-cols-[1fr] md:grid-cols-[260px_minmax(0,1fr)] overflow-hidden">
        <div className="hidden border-r border-sidebar-border bg-sidebar p-3 md:flex md:flex-col md:gap-2">
          <Skeleton className="h-9 w-full rounded-[var(--radius-lg)]" />
          <Skeleton className="h-9 w-full rounded-[var(--radius-lg)]" />
          <div className="flex flex-col gap-1">
            <Skeleton className="h-8 w-full rounded-[var(--radius-md)]" />
            <Skeleton className="h-8 w-full rounded-[var(--radius-md)]" />
            <Skeleton className="h-8 w-full rounded-[var(--radius-md)]" />
          </div>
        </div>
        <div className="flex flex-col">
          <div className="shrink-0 border-b border-border bg-surface-subtle px-5 py-3">
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
          creatingConversationFor={creatingConversationFor}
          focusedSpritzName={name || null}
          focusedSpritz={focusedSpritz}
          collapsed={sidebarCollapsed}
          onToggleCollapse={() => setSidebarCollapsed(!sidebarCollapsed)}
        mobileOpen={mobileMenuOpen}
        onCloseMobile={() => setMobileMenuOpen(false)}
      />

      <div className="flex min-h-0 min-w-0 flex-col overflow-hidden bg-background">
        {/* Header — matches original acp-main-header */}
        <div className="shrink-0 border-b border-border bg-linear-to-b from-background via-background to-transparent px-5 py-3">
          <div className="flex items-center justify-between gap-3">
            <button
              type="button"
              aria-label="Open sidebar menu"
              className="inline-flex size-9 items-center justify-center rounded-[var(--radius-md)] border border-border bg-background text-foreground transition-colors hover:bg-muted md:hidden"
              onClick={() => setMobileMenuOpen(true)}
            >
              <MenuIcon aria-hidden="true" className="size-4" />
            </button>
            <div className="min-w-0 flex-1">
              <h2 className="m-0 truncate text-sm font-medium">
                {selectedConversation?.spec?.title
                  || selectedConversation?.metadata?.name
                  || focusedSpritz?.metadata?.name
                  || 'Select a conversation'}
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
              {!selectedConversation && provisioningSpritz && (
                <p className="m-0 truncate text-xs opacity-60">
                  {provisioningStatusLine}
                </p>
              )}
            </div>
            <div className="flex shrink-0 gap-2">
              <Tooltip>
                <TooltipTrigger
                  render={
                    <button
                      type="button"
                      aria-label="Refresh conversations"
                      className="inline-flex size-9 items-center justify-center rounded-[var(--radius-md)] border border-border bg-background text-foreground transition-colors hover:bg-muted"
                      onClick={() => fetchAgents()}
                    />
                  }
                >
                  <RotateCwIcon aria-hidden="true" className="size-4" />
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
                        aria-label="Open instance in new tab"
                        className="inline-flex size-9 items-center justify-center rounded-[var(--radius-md)] border border-border bg-background text-foreground transition-colors hover:bg-muted"
                      />
                    }
                  >
                    <ExternalLinkIcon aria-hidden="true" className="size-4" />
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
                    className="inline-flex shrink-0 items-center gap-1.5 rounded-[var(--radius-2xl)] border border-[color-mix(in_srgb,var(--primary)_14%,var(--border))] bg-[var(--surface-emphasis)] px-2.5 py-1.5 text-xs text-primary transition-colors hover:bg-[color-mix(in_srgb,var(--primary)_16%,var(--background))]"
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
        <div role="log" aria-label="Chat messages" aria-live="polite" className="flex flex-1 flex-col overflow-auto px-6 pt-7 pb-3" style={{ scrollbarGutter: 'stable' }}>
          {!selectedConversation ? (
            provisioningSpritz ? (
              <div className="m-auto flex max-w-[540px] flex-col gap-1.5 text-center">
                <strong className="block text-base font-medium">Your agent is being created now</strong>
                <p className="m-0 text-sm text-muted-foreground">
                  We will start a chat automatically as soon as it is ready.
                </p>
                {provisioningStatusLine && (
                  <p className="m-0 text-xs text-muted-foreground">
                    {provisioningStatusLine}
                  </p>
                )}
              </div>
            ) : (
              <div className="m-auto max-w-[420px] text-center text-sm opacity-70">
                Select a conversation or create a new instance.
              </div>
            )
          ) : transcript.messages.length === 0 ? (
            <div className="m-auto flex max-w-[540px] flex-col gap-1.5 rounded-[var(--radius-2xl)] border border-[color-mix(in_srgb,var(--primary)_12%,var(--border))] bg-[var(--surface-emphasis)] px-6 py-7 text-center shadow-[0_10px_30px_color-mix(in_srgb,var(--primary)_8%,transparent)]">
              <strong className="block text-base font-medium">Start a conversation</strong>
              <p className="m-0 text-sm text-muted-foreground">Send a message to begin.</p>
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
                  onRespond={shiftPermissionQueue}
                />
              </div>
            )}
            <Composer
              ref={composerRef}
              value={composerText}
              onValueChange={setComposerText}
              onSend={handleSend}
              onCancel={cancelPrompt}
              disabled={!clientReady}
              promptInFlight={promptInFlight}
              status={status}
            />
          </div>
        )}
      </div>

    </div>
  );
}
