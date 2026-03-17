import { useState } from 'react';
import { Link } from 'react-router-dom';
import {
  PlusIcon,
  MessageSquareIcon,
  ChevronRightIcon,
  PanelLeftCloseIcon,
  PanelLeftOpenIcon,
  LayoutGridIcon,
} from 'lucide-react';
import { cn } from '@/lib/utils';
import { Button } from '@/components/ui/button';
import type { ConversationInfo } from '@/types/acp';
import type { Spritz } from '@/types/spritz';

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
  const sidebarContent = (
    <aside
      className={cn(
        'flex h-full flex-col border-r bg-[#fafafa] transition-all duration-200 dark:bg-sidebar',
        collapsed ? 'w-14' : 'w-[260px]',
      )}
    >
      {/* Header */}
      <div className="flex items-center justify-between border-b px-3 py-3">
        {!collapsed && (
          <h2 className="text-sm font-semibold">Conversations</h2>
        )}
        <Button
          variant="ghost"
          size="icon"
          className="size-8 shrink-0"
          onClick={onToggleCollapse}
          title={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
        >
          {collapsed ? (
            <PanelLeftOpenIcon className="size-4" />
          ) : (
            <PanelLeftCloseIcon className="size-4" />
          )}
        </Button>
      </div>

      {/* Agent groups */}
      <div className="scrollbar-hidden flex-1 overflow-y-auto py-2">
        {agents.length === 0 && !collapsed && (
          <div className="px-4 py-6 text-center text-xs text-muted-foreground">
            No ACP-ready workspaces found.
          </div>
        )}
        {agents.map((group) => (
          <AgentSection
            key={group.spritz.metadata.name}
            group={group}
            selectedConversationId={selectedConversationId}
            onSelectConversation={(conv) => {
              onSelectConversation(conv);
              onCloseMobile();
            }}
            onNewConversation={onNewConversation}
            collapsed={collapsed}
          />
        ))}
      </div>

      {/* Footer — back to create */}
      <div className="border-t p-2">
        <Link
          to="/create"
          onClick={onCloseMobile}
          className={cn(
            'flex items-center gap-2 rounded-lg border border-dashed px-3 py-2 text-xs text-muted-foreground transition-colors hover:bg-muted/50',
            collapsed && 'justify-center px-0',
          )}
        >
          <LayoutGridIcon className="size-3.5 shrink-0" />
          {!collapsed && <span>Create workspace</span>}
        </Link>
      </div>
    </aside>
  );

  return (
    <>
      {/* Desktop sidebar — inline */}
      <div className="hidden md:block">{sidebarContent}</div>

      {/* Mobile drawer — overlay */}
      {mobileOpen && (
        <div className="fixed inset-0 z-40 md:hidden">
          <div
            className="absolute inset-0 bg-black/30"
            onClick={onCloseMobile}
          />
          <div className="relative z-50 h-full w-[260px]">
            {/* Force expanded on mobile */}
            <aside className="flex h-full w-[260px] flex-col border-r bg-[#fafafa] dark:bg-sidebar">
              <div className="flex items-center justify-between border-b px-3 py-3">
                <h2 className="text-sm font-semibold">Conversations</h2>
                <Button
                  variant="ghost"
                  size="icon"
                  className="size-8"
                  onClick={onCloseMobile}
                >
                  <PanelLeftCloseIcon className="size-4" />
                </Button>
              </div>
              <div className="scrollbar-hidden flex-1 overflow-y-auto py-2">
                {agents.length === 0 && (
                  <div className="px-4 py-6 text-center text-xs text-muted-foreground">
                    No ACP-ready workspaces found.
                  </div>
                )}
                {agents.map((group) => (
                  <AgentSection
                    key={group.spritz.metadata.name}
                    group={group}
                    selectedConversationId={selectedConversationId}
                    onSelectConversation={(conv) => {
                      onSelectConversation(conv);
                      onCloseMobile();
                    }}
                    onNewConversation={onNewConversation}
                    collapsed={false}
                  />
                ))}
              </div>
              <div className="border-t p-2">
                <Link
                  to="/create"
                  onClick={onCloseMobile}
                  className="flex items-center gap-2 rounded-lg border border-dashed px-3 py-2 text-xs text-muted-foreground transition-colors hover:bg-muted/50"
                >
                  <LayoutGridIcon className="size-3.5 shrink-0" />
                  <span>Create workspace</span>
                </Link>
              </div>
            </aside>
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
        <Button
          variant="ghost"
          size="icon"
          className="size-8"
          onClick={() => onNewConversation(name)}
          title={`New conversation with ${name}`}
        >
          <MessageSquareIcon className="size-3.5" />
        </Button>
      </div>
    );
  }

  return (
    <div className="px-2">
      {/* Group header */}
      <div className="group flex items-center">
        <button
          type="button"
          className="flex flex-1 items-center gap-2 rounded-lg px-2 py-1.5 text-left text-xs font-medium text-muted-foreground transition-colors hover:bg-muted/50"
          onClick={() => setExpanded(!expanded)}
        >
          <ChevronRightIcon
            className={cn(
              'size-3 shrink-0 transition-transform duration-150',
              expanded && 'rotate-90',
            )}
          />
          <span className="truncate">{name}</span>
        </button>
        <Button
          variant="ghost"
          size="icon"
          className="size-6 opacity-0 transition-opacity group-hover:opacity-100"
          onClick={() => onNewConversation(name)}
          title="New conversation"
        >
          <PlusIcon className="size-3" />
        </Button>
      </div>

      {/* Conversations */}
      {expanded && (
        <div className="ml-1 flex flex-col gap-0.5 py-1">
          {group.conversations.length === 0 && (
            <div className="px-3 py-1 text-[10px] text-muted-foreground">No conversations</div>
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
                  'flex w-full items-center gap-2 rounded-lg px-3 py-2 text-left text-[13px] transition-colors hover:bg-muted/50',
                  isActive && 'bg-white font-medium dark:bg-muted',
                )}
                onClick={() => onSelectConversation(conv)}
              >
                <MessageSquareIcon className="size-3 shrink-0 text-muted-foreground" />
                <span className="truncate">{title}</span>
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}
