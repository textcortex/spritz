import { useState } from 'react';
import { ChevronDownIcon, WrenchIcon, InfoIcon } from 'lucide-react';
import { cn } from '@/lib/utils';
import { Markdown } from './markdown';
import type { ACPMessage, ACPBlock } from '@/types/acp';
import { Badge } from '@/components/ui/badge';

function BlockRenderer({ block }: { block: ACPBlock }) {
  const [open, setOpen] = useState(block.type === 'details' ? false : true);

  if (block.type === 'text') {
    return <Markdown content={block.text || ''} />;
  }

  if (block.type === 'details') {
    return (
      <div className="my-1 rounded-lg border border-border/50">
        <button
          type="button"
          className="flex w-full items-center gap-1.5 px-3 py-1.5 text-xs text-muted-foreground hover:bg-muted/50"
          onClick={() => setOpen(!open)}
        >
          <ChevronDownIcon className={cn('size-3 transition-transform', open && 'rotate-180')} />
          {block.title || 'Details'}
        </button>
        {open && (
          <pre className="max-h-52 overflow-auto border-t px-3 py-2 text-xs font-mono">
            {block.text || ''}
          </pre>
        )}
      </div>
    );
  }

  if (block.type === 'plan') {
    return (
      <ol className="list-decimal space-y-1 pl-5 text-sm">
        {(block.entries || []).map((entry, i) => (
          <li key={i} className={entry.done ? 'text-muted-foreground line-through' : ''}>
            {entry.text}
          </li>
        ))}
      </ol>
    );
  }

  if (block.type === 'keyValue') {
    return (
      <div className="text-xs text-muted-foreground">
        <span className="font-medium">{block.key}</span>: {block.value}
      </div>
    );
  }

  return null;
}

/* ── User bubble — right-aligned, dark background ── */
function UserBubble({ message }: { message: ACPMessage }) {
  return (
    <div className="flex justify-end">
      <div className="max-w-[80%] rounded-2xl rounded-br-sm bg-primary px-4 py-2.5 text-primary-foreground">
        <div className="space-y-1.5">
          {message.blocks.map((block, i) => (
            <BlockRenderer key={i} block={block} />
          ))}
        </div>
      </div>
    </div>
  );
}

/* ── Assistant message — left-aligned, borderless ── */
function AssistantMessage({ message }: { message: ACPMessage }) {
  return (
    <div className="w-full">
      <div className="space-y-2">
        {message.blocks.map((block, i) => (
          <BlockRenderer key={i} block={block} />
        ))}
      </div>
      {message.streaming && (
        <span className="mt-2 inline-block size-2 animate-pulse rounded-full bg-primary" />
      )}
    </div>
  );
}

/* ── Event card — tool, system, plan, thinking ── */
const eventIcons: Record<string, typeof WrenchIcon> = {
  tool: WrenchIcon,
  system: InfoIcon,
  plan: InfoIcon,
  thinking: InfoIcon,
  thinking_done: InfoIcon,
};

function EventCard({ message }: { message: ACPMessage }) {
  const Icon = eventIcons[message.role] || InfoIcon;
  const label =
    message.title ||
    (message.role === 'tool' ? 'Tool' : message.role === 'plan' ? 'Plan' : 'System');

  return (
    <div className="rounded-2xl border border-border/50 bg-[#fafafa] px-4 py-3 text-sm dark:bg-muted/10">
      <div className="mb-2 flex items-center gap-2">
        <Icon className="size-3.5 text-muted-foreground" />
        <span className="text-xs font-medium text-muted-foreground">{label}</span>
        {message.status && (
          <Badge
            variant={
              message.status === 'completed'
                ? 'default'
                : message.status === 'failed'
                  ? 'destructive'
                  : 'secondary'
            }
            className="text-[10px]"
          >
            {message.status}
          </Badge>
        )}
        {message.meta && (
          <span className="ml-auto text-[10px] text-muted-foreground">{message.meta}</span>
        )}
      </div>
      <div className="space-y-2">
        {message.blocks.map((block, i) => (
          <BlockRenderer key={i} block={block} />
        ))}
      </div>
      {message.streaming && (
        <span className="mt-1 inline-block size-2 animate-pulse rounded-full bg-primary" />
      )}
    </div>
  );
}

interface ChatMessageProps {
  message: ACPMessage;
}

export function ChatMessage({ message }: ChatMessageProps) {
  if (message.role === 'user') return <UserBubble message={message} />;
  if (message.role === 'assistant') return <AssistantMessage message={message} />;
  return <EventCard message={message} />;
}
