import { useState } from 'react';
import { cn } from '@/lib/utils';
import { Markdown } from './markdown';
import { ThinkingBlock } from './thinking-block';
import type { ACPMessage, ACPBlock } from '@/types/acp';

/* ── Block renderer matching main's renderBlock() ── */

function BlockRenderer({ block }: { block: ACPBlock }) {
  if (block.type === 'text') {
    return (
      <div className="acp-block">
        <Markdown content={block.text || ''} />
      </div>
    );
  }

  if (block.type === 'details') {
    return <DetailsBlock title={block.title || 'Details'} text={block.text || ''} defaultOpen={block.open !== false} />;
  }

  if (block.type === 'plan') {
    return (
      <ol className="m-0 list-decimal space-y-1 pl-5 text-sm">
        {(block.entries || []).map((entry, i) => (
          <li key={i} className={entry.done ? 'text-[#999] line-through' : ''}>
            {entry.text}
          </li>
        ))}
      </ol>
    );
  }

  if (block.type === 'keyValue') {
    return (
      <dl className="m-0 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-[13px]">
        {(block.entries || []).map((entry, i) => (
          <div key={i} className="contents">
            <dt className="font-semibold">{entry.label || ''}</dt>
            <dd className="m-0 opacity-80">{entry.value || ''}</dd>
          </div>
        ))}
        {/* Fallback for legacy flat key/value format */}
        {!block.entries?.length && block.key && (
          <div className="contents">
            <dt className="font-semibold">{block.key}</dt>
            <dd className="m-0 opacity-80">{block.value}</dd>
          </div>
        )}
      </dl>
    );
  }

  if (block.type === 'tags') {
    return (
      <div className="flex flex-wrap gap-1.5">
        {(block.items || []).map((item, i) => (
          <span
            key={i}
            className="inline-flex items-center rounded-full px-2.5 py-0.5 text-[11px] bg-[rgba(0,0,0,0.05)] text-[#525252]"
            title={item.title || undefined}
          >
            {item.label || item.name || ''}
          </span>
        ))}
      </div>
    );
  }

  return null;
}

/* ── Collapsible details block ── */

function DetailsBlock({ title, text, defaultOpen }: { title: string; text: string; defaultOpen: boolean }) {
  const [open, setOpen] = useState(defaultOpen);

  return (
    <div className="flex flex-col">
      <button
        type="button"
        className="flex items-center gap-1.5 border-none bg-transparent p-0 text-left text-[13px] font-semibold cursor-pointer hover:opacity-70"
        onClick={() => setOpen(!open)}
      >
        <span className={cn('inline-block text-[10px] transition-transform', open ? 'rotate-90' : 'rotate-0')}>
          &#9654;
        </span>
        {title}
      </button>
      {open && (
        <pre className="mt-1.5 m-0 max-h-[300px] overflow-auto rounded-md bg-[rgba(0,0,0,0.03)] p-2 text-xs leading-[1.55] font-[SFMono-Regular,JetBrains_Mono,Menlo,monospace]">
          {text}
        </pre>
      )}
    </div>
  );
}

/* ── Status pill — matches main's .acp-status-pill ── */

function StatusPill({ status, tone }: { status: string; tone?: string }) {
  const key = tone || status;
  const colors =
    key === 'success' || key === 'completed'
      ? 'bg-[rgba(22,163,74,0.14)] text-[#166534]'
      : key === 'danger' || key === 'failed' || key === 'error'
        ? 'bg-[rgba(220,38,38,0.12)] text-[#991b1b]'
        : 'bg-[rgba(55,130,255,0.12)] text-[#1c3f8a]';

  return (
    <span className={cn('inline-flex items-center rounded-full px-2 py-1 text-[11px] capitalize', colors)}>
      {status.replace(/_/g, ' ')}
    </span>
  );
}

/* ── User bubble — right-aligned, black background ── */
/* Main: .acp-message--user { align-self: flex-end } */
/* Main: .acp-message--user .acp-bubble { background: #000; color: white; border-bottom-right-radius: 6px } */

function UserBubble({ message }: { message: ACPMessage }) {
  return (
    <article className="flex flex-col gap-2 max-w-[min(820px,86%)] self-end">
      <div className="rounded-[20px] rounded-br-[6px] border border-transparent bg-black px-4 py-2 text-white">
        {message.blocks.map((block, i) => (
          <BlockRenderer key={i} block={block} />
        ))}
      </div>
    </article>
  );
}

/* ── Assistant message — full-width, no bubble ── */
/* Main: .acp-message--assistant { align-self: stretch; max-width: none; width: 100% } */
/* Main: .acp-message--assistant .acp-bubble { background: transparent; border: none; padding: 4px 0; border-radius: 0 } */

function AssistantMessage({ message }: { message: ACPMessage }) {
  return (
    <article className="flex flex-col gap-2 self-stretch w-full">
      <div className="py-1 px-0">
        <div className="space-y-3">
          {message.blocks.map((block, i) => (
            <BlockRenderer key={i} block={block} />
          ))}
        </div>
        {message.streaming && (
          <span className="mt-1 inline-block size-1.5 animate-pulse rounded-full bg-black" />
        )}
      </div>
    </article>
  );
}

/* ── Event card — tool, system, plan ── */
/* Main: .acp-message--tool, .acp-message--plan, .acp-message--system { align-self: flex-start; max-width: min(820px, 86%) } */
/* Main: .acp-event-card { border-radius: 20px; padding: 12px 16px; border: 1px solid #f0f0f0; background: #fafafa; font-size: 13px } */

function EventCard({ message }: { message: ACPMessage }) {
  return (
    <article className="flex flex-col gap-2 self-start max-w-[min(820px,86%)]">
      <div className="rounded-[20px] border border-[#f0f0f0] bg-[#fafafa] px-4 py-3 text-[13px]">
        {/* Header: title + status pill */}
        {(message.title || message.status) && (
          <div className="mb-1.5 flex items-center justify-between gap-3">
            <strong className="text-[13px] font-semibold">
              {message.title || (message.role === 'tool' ? 'Tool' : message.role === 'plan' ? 'Plan' : 'Update')}
            </strong>
            <div className="flex items-center gap-2">
              {message.meta && (
                <span className="text-xs opacity-60">{message.meta}</span>
              )}
              {message.status && (
                <StatusPill status={message.status} tone={message.tone} />
              )}
            </div>
          </div>
        )}
        {/* Body: blocks */}
        <div className="flex flex-col gap-3">
          {message.blocks.map((block, i) => (
            <BlockRenderer key={i} block={block} />
          ))}
        </div>
        {message.streaming && (
          <span className="mt-2 inline-block size-1.5 animate-pulse rounded-full bg-[#3782ff]" />
        )}
      </div>
    </article>
  );
}

/* ── Main export ── */

interface ChatMessageProps {
  message: ACPMessage;
}

export function ChatMessage({ message }: ChatMessageProps) {
  if (message.role === 'user') return <UserBubble message={message} />;
  if (message.role === 'assistant') return <AssistantMessage message={message} />;
  // Active thinking is rendered by the live ThinkingBlock in chat.tsx
  if (message.role === 'thinking') return null;
  // Baked thinking_done: render as a collapsed ThinkingBlock with stored chunks
  if (message.role === 'thinking_done') {
    const chunks = message._thinkingChunks || [];
    if (chunks.length === 0) return null;
    return (
      <ThinkingBlock
        chunks={chunks}
        active={false}
        elapsedSeconds={message._thinkingElapsedSeconds || 0}
      />
    );
  }
  return <EventCard message={message} />;
}
