import { useState } from 'react';
import { Link } from 'react-router-dom';
import {
  PlusIcon,
  PencilIcon,
  LayoutGridIcon,
  MessageSquareIcon,
} from 'lucide-react';
import { cn } from '@/lib/utils';
import type { ConversationInfo } from '@/types/acp';
import type { Spritz } from '@/types/spritz';

/* Sidebar collapse/expand toggle icon matching the original acp-sidebar-toggle */
function SidebarToggleIcon({ collapsed }: { collapsed: boolean }) {
  return collapsed ? (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><rect x="3" y="3" width="18" height="18" rx="2"/><line x1="15" y1="3" x2="15" y2="21"/></svg>
  ) : (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><rect x="3" y="3" width="18" height="18" rx="2"/><line x1="9" y1="3" x2="9" y2="21"/></svg>
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
  collapsed,
  onToggleCollapse,
  mobileOpen,
  onCloseMobile,
}: SidebarProps) {
  const firstAgentName = agents.length > 0 ? agents[0].spritz.metadata.name : null;

  function renderSidebarInner(isCollapsed: boolean, closeMobile?: () => void) {
    const close = closeMobile ?? (() => {});

    return (
      <aside
        className="flex h-full min-h-0 flex-col overflow-hidden border-r border-[#e5e5e5] bg-[#fafafa] dark:border-border dark:bg-sidebar"
      >
        {/* Top section matching original acp-sidebar-top */}
        <div className="flex shrink-0 flex-col gap-2 border-b border-[#e5e5e5] bg-[#fafafa] p-3 dark:border-border dark:bg-sidebar">
          {/* Select row: just the toggle button */}
          <div className={cn('flex items-center gap-2', isCollapsed && 'justify-center')}>
            <button
              type="button"
              onClick={onToggleCollapse}
              className="flex size-9 items-center justify-center rounded-[10px] border border-[#e5e5e5] bg-white text-black transition-colors hover:bg-[#f5f5f5] hover:border-[#ccc] dark:border-border dark:bg-muted dark:hover:bg-muted/80"
              title={isCollapsed ? 'Expand sidebar' : 'Collapse sidebar'}
            >
              <SidebarToggleIcon collapsed={isCollapsed} />
            </button>
          </div>

          {/* Actions: New chat + Spritzes link */}
          {!isCollapsed && (
            <div className="flex flex-col gap-2">
              <button
                type="button"
                className="flex w-full items-center gap-2.5 rounded-lg border border-[#e5e5e5] bg-white px-2.5 py-2 text-[13px] font-medium transition-colors hover:bg-[#f5f5f5] hover:border-[#ccc] dark:border-border dark:bg-muted dark:hover:bg-muted/80"
                onClick={() => {
                  if (firstAgentName) onNewConversation(firstAgentName);
                  close();
                }}
              >
                <PencilIcon className="size-4 shrink-0" />
                <span>New chat</span>
              </button>
              <Link
                to="/create"
                onClick={close}
                className="flex w-full items-center gap-2.5 rounded-lg border border-[#e5e5e5] bg-white px-2.5 py-2 text-[13px] font-medium text-black no-underline transition-colors hover:bg-[#f5f5f5] hover:border-[#ccc] dark:border-border dark:bg-muted dark:text-foreground dark:hover:bg-muted/80"
              >
                <LayoutGridIcon className="size-4 shrink-0" />
                <span>Spritzes</span>
              </Link>
            </div>
          )}
        </div>

        {/* Thread list */}
        <div className="flex min-h-0 flex-1 flex-col gap-0.5 overflow-y-auto p-2">
          {agents.length === 0 && !isCollapsed && (
            <div className="px-3 py-6 text-center text-xs text-muted-foreground">
              No ACP-ready instances found.
            </div>
          )}
          {agents.map((group) => (
            <AgentSection
              key={group.spritz.metadata.name}
              group={group}
              selectedConversationId={selectedConversationId}
              onSelectConversation={(conv) => {
                onSelectConversation(conv);
                close();
              }}
              onNewConversation={onNewConversation}
              collapsed={isCollapsed}
            />
          ))}
        </div>
      </aside>
    );
  }

  return (
    <>
      {/* Desktop sidebar — fills grid cell */}
      <div className="hidden h-full min-h-0 md:block">
        {renderSidebarInner(collapsed)}
      </div>

      {/* Mobile drawer with backdrop */}
      {mobileOpen && (
        <div className="fixed inset-0 z-40 md:hidden">
          <div
            className="absolute inset-0 bg-black/30"
            onClick={onCloseMobile}
          />
          <div className="relative z-50 h-full w-[260px]">
            {renderSidebarInner(false, onCloseMobile)}
          </div>
        </div>
      )}
    </>
  );
}

function AgentSection({
  group,
  selectedConversationId,
  onSelectConversation,
  onNewConversation,
  collapsed,
}: {
  group: AgentGroup;
  selectedConversationId: string | null;
  onSelectConversation: (conversation: ConversationInfo) => void;
  onNewConversation: (spritzName: string) => void;
  collapsed: boolean;
}) {
  const [expanded, setExpanded] = useState(true);
  const name = group.spritz.metadata.name;

  if (collapsed) {
    return (
      <div className="flex flex-col items-center gap-1 py-1">
        <button
          type="button"
          className="flex size-8 items-center justify-center rounded-lg transition-colors hover:bg-[#f0f0f0] dark:hover:bg-muted/50"
          onClick={() => onNewConversation(name)}
          title={`New conversation with ${name}`}
        >
          <MessageSquareIcon className="size-3.5" />
        </button>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-0.5">
      {/* Agent header */}
      <div className="group flex items-center">
        <button
          type="button"
          className="flex flex-1 items-center gap-2 rounded-lg border-0 bg-transparent px-3 py-2 text-left text-[13px] font-medium transition-colors hover:bg-[#f0f0f0] dark:hover:bg-muted/50"
          onClick={() => setExpanded(!expanded)}
        >
          {/* Chevron */}
          <span
            className={cn(
              'inline-block h-[5px] w-[5px] shrink-0 border-b-[1.5px] border-r-[1.5px] border-[#999] transition-transform duration-150 will-change-transform',
              expanded ? 'rotate-45' : '-rotate-45',
            )}
          />
          <span className="truncate">{name}</span>
        </button>
        <button
          type="button"
          className="flex size-6 items-center justify-center rounded bg-transparent text-[#999] opacity-0 transition-opacity will-change-[opacity] hover:text-foreground group-hover:opacity-100"
          onClick={() => onNewConversation(name)}
          title="New conversation"
        >
          <PlusIcon className="size-3.5" />
        </button>
      </div>

      {/* Conversation items */}
      {expanded && (
        <div className="flex flex-col gap-0.5 pl-2">
          {group.conversations.length === 0 && (
            <div className="px-3 py-1 text-[11px] text-muted-foreground">
              No conversations
            </div>
          )}
          {group.conversations.map((conv) => {
            const id = conv.metadata.name;
            const isActive = id === selectedConversationId;
            const title = conv.spec?.title || 'New conversation';
            return (
              <button
                key={id}
                type="button"
                className={cn(
                  'block w-full cursor-pointer rounded-md px-3 py-1.5 text-left text-[13px] transition-colors hover:bg-[#f0f0f0] dark:hover:bg-muted/50',
                  isActive
                    ? 'bg-white dark:bg-muted'
                    : 'bg-transparent',
                )}
                onClick={() => onSelectConversation(conv)}
              >
                <span className="block truncate">{title}</span>
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}
