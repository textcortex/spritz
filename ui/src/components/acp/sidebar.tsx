import { useState } from 'react';
import { Link } from 'react-router-dom';
import {
  PlusIcon,
  PencilIcon,
  LayoutGridIcon,
  ChevronRightIcon,
} from 'lucide-react';
import { cn, timeAgo } from '@/lib/utils';
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
  collapsed: boolean;
  onToggleCollapse: () => void;
  mobileOpen: boolean;
  onCloseMobile: () => void;
}

export function Sidebar({
  agents,
  selectedConversationId,
  onSelectConversation,
  onNewConversation,
  creatingConversationFor,
  collapsed,
  onToggleCollapse,
  mobileOpen,
  onCloseMobile,
}: SidebarProps) {
  const firstAgentName = agents.length > 0 ? agents[0].spritz.metadata.name : null;

  /* ── Collapsed desktop sidebar ── */
  function renderCollapsed() {
    return (
      <aside aria-label="Sidebar" className="flex h-full min-h-0 flex-col items-center gap-1 overflow-hidden bg-[#f9f9f9] p-3 dark:bg-sidebar">
        <Tooltip>
          <TooltipTrigger
            render={
              <button
                type="button"
                aria-label="Expand sidebar"
                onClick={onToggleCollapse}
                className="flex size-9 items-center justify-center rounded-lg text-foreground/70 transition-colors hover:bg-[#ececec] dark:hover:bg-muted/50"
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
                className="flex size-9 items-center justify-center rounded-lg text-foreground/70 transition-colors hover:bg-[#ececec] disabled:cursor-not-allowed disabled:opacity-50 dark:hover:bg-muted/50"
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
      <aside aria-label="Sidebar" className="flex h-full min-h-0 flex-col gap-4 overflow-hidden bg-[#f9f9f9] p-3 dark:bg-sidebar">
        {/* Header */}
        <div className="flex shrink-0 items-center justify-between">
          <span className="text-[15px] font-semibold tracking-tight">Spritz</span>
          {/* Hide collapse toggle on mobile — only show on desktop */}
          <Tooltip>
            <TooltipTrigger
              render={
                <button
                  type="button"
                  aria-label="Collapse sidebar"
                  onClick={onToggleCollapse}
                  className="hidden size-8 items-center justify-center rounded-lg text-foreground/60 transition-colors hover:bg-[#ececec] md:flex dark:hover:bg-muted/50"
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
            className="flex w-full items-center gap-3 rounded-lg px-3 py-2 text-[14px] text-foreground/80 transition-colors hover:bg-[#ececec] disabled:cursor-not-allowed disabled:opacity-50 dark:hover:bg-muted/50"
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
            className="flex w-full items-center gap-3 rounded-lg px-3 py-2 text-[14px] text-foreground/80 no-underline transition-colors hover:bg-[#ececec] dark:hover:bg-muted/50"
          >
            <LayoutGridIcon aria-hidden="true" className="size-[18px] shrink-0" />
            <span>Spritzes</span>
          </Link>
        </nav>

        {/* Conversation list */}
      <div role="list" aria-label="Conversations" className="flex min-h-0 flex-1 flex-col gap-2 overflow-y-auto">
          {agents.length === 0 && (
            <div className="p-6 text-center text-xs text-muted-foreground">
              No ACP-ready instances found.
            </div>
          )}
          {agents.map((group) => (
            <AgentSection
              key={group.spritz.metadata.name}
              group={group}
              selectedConversationId={selectedConversationId}
              onSelectConversation={(conv) => { onSelectConversation(conv); close(); }}
              onNewConversation={onNewConversation}
              creatingConversationFor={creatingConversationFor}
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

/* ── Agent section with animated expand/collapse ── */

function AgentSection({
  group,
  selectedConversationId,
  onSelectConversation,
  onNewConversation,
  creatingConversationFor,
}: {
  group: AgentGroup;
  selectedConversationId: string | null;
  onSelectConversation: (conversation: ConversationInfo) => void;
  onNewConversation: (spritzName: string) => void;
  creatingConversationFor?: string | null;
}) {
  const [expanded, setExpanded] = useState(true);
  const name = group.spritz.metadata.name;
  const creatingForThisAgent = creatingConversationFor === name;

  return (
    <div role="listitem" className="flex flex-col gap-0.5">
      {/* Agent header */}
      <div className="group flex items-center gap-1">
        <button
          type="button"
          aria-expanded={expanded}
          aria-label={`${name} conversations`}
          className="flex flex-1 items-center gap-2 rounded-lg px-3 py-1.5 text-left text-xs font-medium text-muted-foreground transition-colors hover:bg-[#ececec] dark:hover:bg-muted/50"
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
                className="flex size-6 items-center justify-center rounded-md text-muted-foreground opacity-0 transition-opacity hover:text-foreground group-hover:opacity-100 disabled:cursor-not-allowed disabled:opacity-50"
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
                      'flex w-full items-center gap-2 rounded-lg px-8 py-1.5 text-left text-[13px] transition-colors hover:bg-[#ececec] dark:hover:bg-muted/50',
                      isActive
                        ? 'bg-[#ececec] dark:bg-muted'
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
