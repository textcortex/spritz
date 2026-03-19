import { useState } from 'react';
import { Link } from 'react-router-dom';
import {
  PlusIcon,
  PencilIcon,
  LayoutGridIcon,
  Trash2Icon,
  EllipsisIcon,
  ChevronRightIcon,
} from 'lucide-react';
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
} from '@/components/ui/dropdown-menu';
import { cn } from '@/lib/utils';
import { Tooltip, TooltipTrigger, TooltipContent } from '@/components/ui/tooltip';
import type { ConversationInfo } from '@/types/acp';
import type { Spritz } from '@/types/spritz';

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
  onDeleteConversation: (conversationId: string) => void;
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
  onDeleteConversation,
  collapsed,
  onToggleCollapse,
  mobileOpen,
  onCloseMobile,
}: SidebarProps) {
  const firstAgentName = agents.length > 0 ? agents[0].spritz.metadata.name : null;

  /* ── Collapsed desktop sidebar ── */
  function renderCollapsed() {
    return (
      <aside className="flex h-full min-h-0 flex-col items-center gap-1 overflow-hidden bg-[#f9f9f9] py-3 dark:bg-sidebar">
        <Tooltip>
          <TooltipTrigger
            render={
              <button
                type="button"
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
                className="flex size-9 items-center justify-center rounded-lg text-foreground/70 transition-colors hover:bg-[#ececec] dark:hover:bg-muted/50"
                onClick={() => { if (firstAgentName) onNewConversation(firstAgentName); }}
              />
            }
          >
            <PencilIcon className="size-4" />
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
      <aside className="flex h-full min-h-0 flex-col overflow-hidden bg-[#f9f9f9] dark:bg-sidebar">
        {/* Header */}
        <div className="flex shrink-0 items-center justify-between px-3 pt-3 pb-1">
          <span className="text-[15px] font-semibold tracking-tight">Spritz</span>
          {/* Hide collapse toggle on mobile — only show on desktop */}
          <Tooltip>
            <TooltipTrigger
              render={
                <button
                  type="button"
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
        <div className="flex flex-col gap-0.5 px-2 pt-2">
          <button
            type="button"
            className="flex w-full items-center gap-3 rounded-lg px-3 py-2 text-[14px] text-foreground/80 transition-colors hover:bg-[#ececec] dark:hover:bg-muted/50"
            onClick={() => {
              if (firstAgentName) onNewConversation(firstAgentName);
              close();
            }}
          >
            <PencilIcon className="size-[18px] shrink-0" />
            <span>New chat</span>
          </button>
          <Link
            to="/create"
            onClick={close}
            className="flex w-full items-center gap-3 rounded-lg px-3 py-2 text-[14px] text-foreground/80 no-underline transition-colors hover:bg-[#ececec] dark:hover:bg-muted/50"
          >
            <LayoutGridIcon className="size-[18px] shrink-0" />
            <span>Spritzes</span>
          </Link>
        </div>

        {/* Conversation list */}
        <div className="mt-4 flex min-h-0 flex-1 flex-col overflow-y-auto px-2 pb-3">
          {agents.length === 0 && (
            <div className="px-3 py-6 text-center text-xs text-muted-foreground">
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
              onDeleteConversation={onDeleteConversation}
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
        <div className="fixed inset-0 z-40 md:hidden">
          <div className="absolute inset-0 bg-black/30" onClick={onCloseMobile} />
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
  onDeleteConversation,
}: {
  group: AgentGroup;
  selectedConversationId: string | null;
  onSelectConversation: (conversation: ConversationInfo) => void;
  onNewConversation: (spritzName: string) => void;
  onDeleteConversation: (conversationId: string) => void;
}) {
  const [expanded, setExpanded] = useState(true);
  const name = group.spritz.metadata.name;

  return (
    <div className="flex flex-col">
      {/* Agent header */}
      <div className="group flex items-center pr-1">
        <button
          type="button"
          className="flex flex-1 items-center gap-2 rounded-lg px-3 py-1.5 text-left text-xs font-medium text-muted-foreground transition-colors hover:bg-[#ececec] dark:hover:bg-muted/50"
          onClick={() => setExpanded(!expanded)}
        >
          <ChevronRightIcon
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
                className="flex size-6 items-center justify-center rounded-md text-muted-foreground opacity-0 transition-opacity hover:text-foreground group-hover:opacity-100"
                onClick={() => onNewConversation(name)}
              />
            }
          >
            <PlusIcon className="size-3.5" />
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
              <div className="px-3 py-2 text-xs text-muted-foreground">
                No conversations
              </div>
            )}
            {group.conversations.map((conv) => {
              const id = conv.metadata.name;
              const isActive = id === selectedConversationId;
              const title = conv.spec?.title || 'New conversation';
              return (
                <div key={id} className="group/conv relative">
                  <button
                    type="button"
                    className={cn(
                      'block w-full cursor-pointer rounded-lg px-3 py-2 text-left text-[14px] transition-colors hover:bg-[#ececec] dark:hover:bg-muted/50',
                      isActive
                        ? 'bg-[#ececec] dark:bg-muted'
                        : 'bg-transparent',
                    )}
                    onClick={() => onSelectConversation(conv)}
                  >
                    <span className="block truncate pr-6">{title}</span>
                  </button>
                  <div className="absolute right-1 top-0 flex h-full items-center">
                    <DropdownMenu>
                      <DropdownMenuTrigger
                        className="flex size-7 shrink-0 items-center justify-center rounded-md bg-transparent text-foreground/40 opacity-0 transition-opacity hover:text-foreground group-hover/conv:opacity-100 data-[popup-open]:opacity-100"
                      >
                        <EllipsisIcon className="size-4" />
                      </DropdownMenuTrigger>
                      <DropdownMenuContent side="bottom" align="start">
                        <DropdownMenuItem
                          variant="destructive"
                          onClick={() => onDeleteConversation(id)}
                        >
                          <Trash2Icon className="size-3.5" />
                          Delete
                        </DropdownMenuItem>
                      </DropdownMenuContent>
                    </DropdownMenu>
                  </div>
                </div>
              );
            })}
          </div>
        </div>
      </div>
    </div>
  );
}
