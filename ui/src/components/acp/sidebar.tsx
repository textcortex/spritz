import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import {
  PlusIcon,
  PencilIcon,
  LayoutGridIcon,
  ChevronRightIcon,
  LoaderCircleIcon,
} from 'lucide-react';
import { cn, timeAgo } from '@/lib/utils';
import { describeChatAction } from '@/lib/urls';
import { buildProvisioningPlaceholderSpritz, getProvisioningStatusLine } from '@/lib/provisioning';
import { BrandHeader } from '@/components/brand-header';
import { Tooltip, TooltipTrigger, TooltipContent } from '@/components/ui/tooltip';
import type { ConversationInfo } from '@/types/acp';
import type { Spritz } from '@/types/spritz';

function SidebarToggleIcon({ collapsed }: { collapsed: boolean }) {
  return collapsed ? (
    <svg aria-hidden="true" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><rect x="3" y="3" width="18" height="18" rx="2"/><line x1="15" y1="3" x2="15" y2="21"/></svg>
  ) : (
    <svg aria-hidden="true" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><rect x="3" y="3" width="18" height="18" rx="2"/><line x1="9" y1="3" x2="9" y2="21"/></svg>
  );
}

interface AgentGroup {
  spritz: Spritz;
  conversations: ConversationInfo[];
}

interface SidebarProps {
  agents: AgentGroup[];
  selectedConversationId: string | null;
  onSelectConversation: (conversation: ConversationInfo) => void;
  onNewConversation: (spritzName: string) => void;
  creatingConversationFor?: string | null;
  focusedSpritzName?: string | null;
  focusedSpritz?: Spritz | null;
  collapsed: boolean;
  onToggleCollapse: () => void;
  mobileOpen: boolean;
  onCloseMobile: () => void;
}

function sortAgentGroupsForFocus(groups: AgentGroup[], focusedSpritzName?: string | null): AgentGroup[] {
  if (!focusedSpritzName) return groups;
  return [...groups].sort((left, right) => {
    const leftFocused = left.spritz.metadata.name === focusedSpritzName;
    const rightFocused = right.spritz.metadata.name === focusedSpritzName;
    if (leftFocused === rightFocused) return 0;
    return leftFocused ? -1 : 1;
  });
}

export function Sidebar({
  agents,
  selectedConversationId,
  onSelectConversation,
  onNewConversation,
  creatingConversationFor,
  focusedSpritzName,
  focusedSpritz,
  collapsed,
  onToggleCollapse,
  mobileOpen,
  onCloseMobile,
}: SidebarProps) {
  const orderedAgents = sortAgentGroupsForFocus(agents, focusedSpritzName);
  const firstAgentName = orderedAgents.length > 0 ? orderedAgents[0].spritz.metadata.name : null;
  const focusMode = Boolean(focusedSpritzName);
  const focusedAgentInList = Boolean(
    focusedSpritzName && orderedAgents.some((group) => group.spritz.metadata.name === focusedSpritzName),
  );
  const focusedProvisioningSpritz = focusedSpritzName && !focusedAgentInList
    ? focusedSpritz || buildProvisioningPlaceholderSpritz(focusedSpritzName)
    : null;
  const showFocusedProvisioningSection = Boolean(focusedProvisioningSpritz);

  /* ── Collapsed desktop sidebar ── */
  function renderCollapsed() {
    return (
      <aside aria-label="Sidebar" className="flex h-full min-h-0 flex-col items-center gap-3 overflow-hidden border-r border-sidebar-border bg-sidebar p-3">
        <BrandHeader compact />
        <Tooltip>
          <TooltipTrigger
            render={
              <button
                type="button"
                aria-label="Expand sidebar"
                onClick={onToggleCollapse}
                className="flex size-9 items-center justify-center rounded-[var(--radius-lg)] text-foreground/70 transition-colors hover:bg-[var(--surface-emphasis)] hover:text-primary"
              />
            }
          >
            <SidebarToggleIcon collapsed />
          </TooltipTrigger>
          <TooltipContent side="right">Expand sidebar</TooltipContent>
        </Tooltip>
        <Tooltip>
          <TooltipTrigger
            render={
              <button
                type="button"
                aria-label="New chat"
                disabled={!firstAgentName || creatingConversationFor === firstAgentName}
                className="flex size-9 items-center justify-center rounded-[var(--radius-lg)] text-foreground/70 transition-colors hover:bg-[var(--surface-emphasis)] hover:text-primary disabled:cursor-not-allowed disabled:opacity-50"
                onClick={() => { if (firstAgentName && creatingConversationFor !== firstAgentName) onNewConversation(firstAgentName); }}
              />
            }
          >
            <PencilIcon aria-hidden="true" className="size-4" />
          </TooltipTrigger>
          <TooltipContent side="right">New chat</TooltipContent>
        </Tooltip>
      </aside>
    );
  }

  /* ── Expanded sidebar (desktop + mobile) ── */
  function renderExpanded(closeMobile?: () => void) {
    const close = closeMobile ?? (() => {});

    return (
      <aside aria-label="Sidebar" className="flex h-full min-h-0 flex-col gap-4 overflow-hidden border-r border-sidebar-border bg-sidebar p-3">
        {/* Header */}
        <div className="flex shrink-0 items-center justify-between">
          <BrandHeader />
          {/* Hide collapse toggle on mobile — only show on desktop */}
          <Tooltip>
            <TooltipTrigger
              render={
                <button
                type="button"
                aria-label="Collapse sidebar"
                onClick={onToggleCollapse}
                className="hidden size-8 items-center justify-center rounded-[var(--radius-lg)] text-foreground/60 transition-colors hover:bg-[var(--surface-emphasis)] hover:text-primary md:flex"
              />
            }
          >
              <SidebarToggleIcon collapsed={false} />
            </TooltipTrigger>
            <TooltipContent side="right">Collapse sidebar</TooltipContent>
          </Tooltip>
        </div>

        {/* Nav items */}
        <nav aria-label="Sidebar navigation" className="flex shrink-0 flex-col gap-0.5">
          <button
            type="button"
            disabled={!firstAgentName || creatingConversationFor === firstAgentName}
            className="flex w-full items-center gap-3 rounded-[var(--radius-lg)] px-3 py-2 text-[14px] text-foreground/80 transition-colors hover:bg-[var(--surface-emphasis)] hover:text-primary disabled:cursor-not-allowed disabled:opacity-50"
            onClick={() => {
              if (firstAgentName && creatingConversationFor !== firstAgentName) onNewConversation(firstAgentName);
              close();
            }}
          >
            <PencilIcon aria-hidden="true" className="size-[18px] shrink-0" />
            <span>New chat</span>
          </button>
          <Link
            to="/create"
            onClick={close}
            className="flex w-full items-center gap-3 rounded-[var(--radius-lg)] px-3 py-2 text-[14px] text-foreground/80 no-underline transition-colors hover:bg-[var(--surface-emphasis)] hover:text-primary"
          >
            <LayoutGridIcon aria-hidden="true" className="size-[18px] shrink-0" />
            <span>Instances</span>
          </Link>
        </nav>

        {/* Conversation list */}
      <div role="list" aria-label="Conversations" className="flex min-h-0 flex-1 flex-col gap-2 overflow-y-auto">
          {showFocusedProvisioningSection && focusedProvisioningSpritz && (
            <FocusedAgentProvisioningSection
              spritz={focusedProvisioningSpritz}
              selectedConversationId={selectedConversationId}
            />
          )}
          {orderedAgents.length === 0 && !showFocusedProvisioningSection && (
            <div className="p-6 text-center text-xs text-muted-foreground">
              No ACP-ready instances found.
            </div>
          )}
          {orderedAgents.map((group) => (
            <AgentSection
              key={group.spritz.metadata.name}
              group={group}
              selectedConversationId={selectedConversationId}
              onSelectConversation={(conv) => { onSelectConversation(conv); close(); }}
              onNewConversation={onNewConversation}
              creatingConversationFor={creatingConversationFor}
              defaultExpanded={!focusMode || group.spritz.metadata.name === focusedSpritzName}
              focused={group.spritz.metadata.name === focusedSpritzName}
            />
          ))}
        </div>
      </aside>
    );
  }

  return (
    <>
      {/* Desktop sidebar */}
      <div className="hidden h-full min-h-0 md:block">
        {collapsed ? renderCollapsed() : renderExpanded()}
      </div>

      {/* Mobile drawer — always expanded */}
      {mobileOpen && (
        <div
          role="dialog"
          aria-label="Sidebar navigation"
          aria-modal="true"
          className="fixed inset-0 z-40 md:hidden"
          onKeyDown={(e) => { if (e.key === 'Escape') onCloseMobile(); }}
        >
          <div className="absolute inset-0 bg-black/30" aria-hidden="true" onClick={onCloseMobile} />
          <div className="relative z-50 h-full w-[280px]">
            {renderExpanded(onCloseMobile)}
          </div>
        </div>
      )}
    </>
  );
}

function FocusedAgentProvisioningSection({
  spritz,
  selectedConversationId,
}: {
  spritz: Spritz;
  selectedConversationId: string | null;
}) {
  const name = spritz.metadata.name;
  const statusLine = getProvisioningStatusLine(spritz);
  const conversationLabel = describeChatAction(spritz).label;
  const conversationSelected = !selectedConversationId;

  return (
    <div role="listitem" className="flex flex-col gap-0.5">
      <div className="flex items-center gap-1">
        <div
          aria-current="true"
          className="flex flex-1 items-center gap-2 rounded-[var(--radius-lg)] bg-sidebar-accent px-3 py-1.5 text-left text-xs font-medium text-foreground"
        >
          <ChevronRightIcon aria-hidden="true" className="size-3 shrink-0 rotate-90" />
          <span className="truncate">{name}</span>
        </div>
      </div>
      <div className="flex flex-col gap-1">
        <div
          aria-current={conversationSelected ? 'true' : undefined}
          className={cn(
            'ml-8 flex items-center gap-2 rounded-[var(--radius-lg)] px-3 py-1.5 text-[13px] text-foreground',
            conversationSelected ? 'bg-sidebar-accent' : 'bg-transparent',
          )}
        >
          <LoaderCircleIcon aria-hidden="true" className="size-3.5 shrink-0 animate-spin text-muted-foreground" />
          <span className="truncate">{conversationLabel}</span>
        </div>
        <div className="px-11 text-xs text-muted-foreground">{statusLine}</div>
      </div>
    </div>
  );
}

/* ── Agent section with animated expand/collapse ── */

function AgentSection({
  group,
  selectedConversationId,
  onSelectConversation,
  onNewConversation,
  creatingConversationFor,
  defaultExpanded,
  focused,
}: {
  group: AgentGroup;
  selectedConversationId: string | null;
  onSelectConversation: (conversation: ConversationInfo) => void;
  onNewConversation: (spritzName: string) => void;
  creatingConversationFor?: string | null;
  defaultExpanded: boolean;
  focused: boolean;
}) {
  const [expanded, setExpanded] = useState(defaultExpanded);
  const name = group.spritz.metadata.name;
  const creatingForThisAgent = creatingConversationFor === name;

  useEffect(() => {
    setExpanded(defaultExpanded);
  }, [defaultExpanded]);

  return (
    <div role="listitem" className="flex flex-col gap-0.5">
      {/* Agent header */}
      <div className="group flex items-center gap-1">
        <button
          type="button"
          aria-expanded={expanded}
          aria-current={focused ? 'true' : undefined}
          aria-label={`${name} conversations`}
          className="flex flex-1 items-center gap-2 rounded-[var(--radius-lg)] px-3 py-1.5 text-left text-xs font-medium text-muted-foreground transition-colors hover:bg-[var(--surface-emphasis)] hover:text-primary"
          data-active={focused ? 'true' : 'false'}
          className={cn(
            'flex flex-1 items-center gap-2 rounded-[var(--radius-lg)] px-3 py-1.5 text-left text-xs font-medium transition-colors hover:bg-sidebar-accent',
            focused
              ? 'bg-sidebar-accent text-foreground'
              : 'text-muted-foreground',
          )}
          onClick={() => setExpanded(!expanded)}
        >
          <ChevronRightIcon
            aria-hidden="true"
            className={cn(
              'size-3 shrink-0 transition-transform duration-200 will-change-transform',
              expanded && 'rotate-90',
            )}
          />
          <span className="truncate">{name}</span>
        </button>
        <Tooltip>
          <TooltipTrigger
            render={
              <button
                type="button"
                aria-label={`New conversation for ${name}`}
                disabled={creatingForThisAgent}
                className="flex size-6 items-center justify-center rounded-[var(--radius-md)] text-muted-foreground opacity-0 transition-opacity hover:text-foreground group-hover:opacity-100 disabled:cursor-not-allowed disabled:opacity-50"
                onClick={() => { if (!creatingForThisAgent) onNewConversation(name); }}
              />
            }
          >
            <PlusIcon aria-hidden="true" className="size-3.5" />
          </TooltipTrigger>
          <TooltipContent side="right">New conversation</TooltipContent>
        </Tooltip>
      </div>

      {/* Animated collapsible body */}
      <div
        className="grid transition-[grid-template-rows] duration-200 ease-in-out will-change-[grid-template-rows]"
        style={{ gridTemplateRows: expanded ? '1fr' : '0fr' }}
      >
        <div className="overflow-hidden min-h-0">
          <div className="flex flex-col gap-0.5">
            {group.conversations.length === 0 && (
              <div className="p-3 text-xs text-muted-foreground">
                No conversations
              </div>
            )}
            {group.conversations.map((conv) => {
              const id = conv.metadata.name;
              const isActive = id === selectedConversationId;
              const title = conv.spec?.title || 'New conversation';
              const activity = conv.status?.lastActivityAt;
              return (
                <div key={id} className="group/conv flex items-center">
                  <button
                    type="button"
                    aria-current={isActive ? 'true' : undefined}
                    className={cn(
                      'flex w-full items-center gap-2 rounded-[var(--radius-lg)] px-8 py-1.5 text-left text-[13px] transition-colors hover:bg-[var(--surface-emphasis)]',
                      isActive
                        ? 'bg-[var(--surface-emphasis)] text-primary shadow-[inset_0_0_0_1px_color-mix(in_srgb,var(--primary)_14%,transparent)]'
                        : 'bg-transparent',
                    )}
                    onClick={() => onSelectConversation(conv)}
                  >
                    <span className="min-w-0 flex-1 truncate">{title}</span>
                    {activity && (
                      <span className="shrink-0 text-[11px] text-muted-foreground">
                        {timeAgo(activity)}
                      </span>
                    )}
                  </button>
                </div>
              );
            })}
          </div>
        </div>
      </div>
    </div>
  );
}
