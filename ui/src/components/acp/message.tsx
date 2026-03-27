import { useEffect, useMemo, useState } from 'react';
import { CheckIcon, CopyIcon } from 'lucide-react';
import { cn } from '@/lib/utils';
import { Markdown } from './markdown';
import { ThinkingBlock } from './thinking-block';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import type { ACPMessage, ACPBlock } from '@/types/acp';

/* ── Block renderer matching main's renderBlock() ── */

function BlockRenderer({ block, streaming }: { block: ACPBlock; streaming?: boolean }) {
  if (block.type === 'text') {
    return (
      <div className="acp-block">
        <Markdown content={block.text || ''} streaming={streaming} />
      </div>
    );
  }

  if (block.type === 'details') {
    return <DetailsBlock title={block.title || 'Details'} text={block.text || ''} defaultOpen={block.open !== false} />;
  }

  if (block.type === 'plan') {
    return (
      <ol className="m-0 flex list-decimal flex-col gap-1 pl-5 text-sm">
        {(block.entries || []).map((entry, i) => (
          <li key={i} className={entry.done ? 'text-muted-foreground line-through' : ''}>
            {entry.done && <span className="sr-only">(completed) </span>}
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
            className="inline-flex items-center rounded-[var(--radius-2xl)] bg-muted px-2.5 py-0.5 text-[11px] text-foreground/80"
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
    <div className="flex flex-col gap-1.5">
      <button
        type="button"
        aria-expanded={open}
        className="flex items-center gap-1.5 border-none bg-transparent p-0 text-left text-[13px] font-semibold cursor-pointer hover:opacity-70"
        onClick={() => setOpen(!open)}
      >
        <span aria-hidden="true" className={cn('inline-block text-[10px] transition-transform will-change-transform', open ? 'rotate-90' : 'rotate-0')}>
          &#9654;
        </span>
        {title}
      </button>
      {open && (
        <pre className="m-0 max-h-[300px] overflow-auto rounded-[var(--radius-md)] bg-muted/60 p-2 text-xs leading-[1.55] font-[SFMono-Regular,JetBrains_Mono,Menlo,monospace]">
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
        : 'bg-[color-mix(in_srgb,var(--primary)_12%,transparent)] text-primary';

  return (
    <span className={cn('inline-flex items-center rounded-[var(--radius-2xl)] px-2 py-1 text-[11px] capitalize', colors)}>
      {status.replace(/_/g, ' ')}
    </span>
  );
}

function extractCopyText(blocks: ACPBlock[]): string {
  return blocks
    .flatMap((block) => {
      if (block.type === 'text') return [String(block.text || '').trim()];
      if (block.type === 'details') {
        return [block.title, block.text]
          .map((value) => String(value || '').trim())
          .filter(Boolean);
      }
      if (block.type === 'plan') {
        return (block.entries || [])
          .map((entry) => String(entry.text || '').trim())
          .filter(Boolean);
      }
      if (block.type === 'keyValue') {
        if (block.entries?.length) {
          return block.entries
            .map((entry) => {
              const label = String(entry.label || '').trim();
              const value = String(entry.value || '').trim();
              return label && value ? `${label}: ${value}` : label || value;
            })
            .filter(Boolean);
        }
        const label = String(block.key || '').trim();
        const value = String(block.value || '').trim();
        return [label && value ? `${label}: ${value}` : label || value].filter(Boolean);
      }
      if (block.type === 'tags') {
        return (block.items || [])
          .map((item) => String(item.label || item.name || '').trim())
          .filter(Boolean);
      }
      return [];
    })
    .join('\n\n')
    .trim();
}

async function copyTextToClipboard(text: string) {
  if (!text) return;

  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text);
      return;
    } catch {
      // Fall through to the DOM-based copy path for insecure/local contexts.
    }
  }

  const textarea = document.createElement('textarea');
  textarea.value = text;
  textarea.setAttribute('readonly', 'true');
  textarea.setAttribute('aria-hidden', 'true');
  textarea.style.position = 'fixed';
  textarea.style.top = '0';
  textarea.style.left = '0';
  textarea.style.width = '1px';
  textarea.style.height = '1px';
  textarea.style.padding = '0';
  textarea.style.border = '0';
  textarea.style.opacity = '0';

  document.body.appendChild(textarea);
  textarea.focus();
  textarea.select();
  textarea.setSelectionRange(0, textarea.value.length);

  const copied = document.execCommand('copy');
  document.body.removeChild(textarea);

  if (!copied) {
    throw new Error('Copy failed');
  }
}

function MessageActions({ text, align = 'left' }: { text: string; align?: 'left' | 'right' }) {
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    if (!copied) return undefined;
    const timer = window.setTimeout(() => setCopied(false), 1800);
    return () => window.clearTimeout(timer);
  }, [copied]);

  async function handleCopy() {
    if (!text) return;
    await copyTextToClipboard(text);
    setCopied(true);
  }

  return (
    <div
      className={cn(
        'mt-1 flex items-center opacity-0 transition-all duration-200 group-hover/message:translate-y-0 group-hover/message:opacity-100 group-focus-within/message:translate-y-0 group-focus-within/message:opacity-100',
        align === 'right' ? 'justify-end' : 'justify-start',
      )}
    >
      <Tooltip>
        <TooltipTrigger
          render={
            <button
              type="button"
              aria-label={copied ? 'Message copied' : 'Copy message'}
              className="inline-flex size-8 items-center justify-center rounded-[var(--radius-md)] text-muted-foreground transition-colors duration-150 hover:bg-muted hover:text-foreground focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
              onClick={() => {
                handleCopy().catch(() => {});
              }}
            />
          }
        >
          <span className="relative size-[14px]" aria-hidden="true">
            <CopyIcon
              className={cn(
                'absolute inset-0 size-[14px] copy-action-icon copy-action-icon--copy',
                copied ? 'copy-action-icon-hidden' : 'copy-action-icon-visible',
              )}
            />
            <CheckIcon
              className={cn(
                'absolute inset-0 size-[14px] copy-action-icon copy-action-icon--check',
                copied ? 'copy-action-icon-visible' : 'copy-action-icon-hidden',
              )}
            />
          </span>
        </TooltipTrigger>
        <TooltipContent side="top" sideOffset={8}>
          {copied ? 'Copied' : 'Copy'}
        </TooltipContent>
      </Tooltip>
    </div>
  );
}

/* ── User bubble — right-aligned, black background ── */
/* Main: .acp-message--user { align-self: flex-end } */
/* Main: .acp-message--user .acp-bubble { background: #000; color: white; border-bottom-right-radius: 6px } */

function UserBubble({ message }: { message: ACPMessage }) {
  const copyText = useMemo(() => extractCopyText(message.blocks), [message.blocks]);

  return (
    <article aria-label="Your message" className="group/message flex max-w-[min(820px,86%)] flex-col gap-2 self-end">
      <div className="rounded-[var(--radius-2xl)] rounded-br-[var(--radius-sm)] border border-transparent bg-primary px-4 py-2 text-primary-foreground">
        {message.blocks.map((block, i) => (
          <BlockRenderer key={i} block={block} />
        ))}
      </div>
      {copyText && <MessageActions text={copyText} align="right" />}
    </article>
  );
}

/* ── Assistant message — full-width, no bubble ── */
/* Main: .acp-message--assistant { align-self: stretch; max-width: none; width: 100% } */
/* Main: .acp-message--assistant .acp-bubble { background: transparent; border: none; padding: 4px 0; border-radius: 0 } */

function AssistantMessage({ message }: { message: ACPMessage }) {
  const copyText = useMemo(() => extractCopyText(message.blocks), [message.blocks]);

  return (
    <article aria-label="Assistant message" className="group/message flex w-full flex-col gap-2 self-stretch">
      <div className="py-1 px-0">
        <div className="flex flex-col gap-3">
          {message.blocks.map((block, i) => (
            <BlockRenderer key={i} block={block} streaming={message.streaming} />
          ))}
        </div>
        {message.streaming && (
          <span role="status" aria-label="Generating response" className="mt-1 inline-block size-1.5 animate-pulse rounded-full bg-primary will-change-[opacity]" />
        )}
      </div>
      {copyText && <MessageActions text={copyText} />}
    </article>
  );
}

/* ── Event card — tool, system, plan ── */
/* Main: .acp-message--tool, .acp-message--plan, .acp-message--system { align-self: flex-start; max-width: min(820px, 86%) } */
/* Main: .acp-event-card { border-radius: 20px; padding: 12px 16px; border: 1px solid #f0f0f0; background: #fafafa; font-size: 13px } */

function EventCard({ message }: { message: ACPMessage }) {
  const label = message.title || (message.role === 'tool' ? 'Tool result' : message.role === 'plan' ? 'Plan' : 'System update');
  return (
    <article aria-label={label} className="flex flex-col gap-2 self-start max-w-[min(820px,86%)]">
      <div className="flex flex-col gap-1.5 rounded-[var(--radius-xl)] border border-border bg-surface-subtle px-4 py-3 text-[13px]">
        {/* Header: title + status pill */}
        {(message.title || message.status) && (
          <div className="flex items-center justify-between gap-3">
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
            <BlockRenderer key={i} block={block} streaming={message.streaming} />
          ))}
        </div>
        {message.streaming && (
          <span role="status" aria-label="Processing" className="mt-2 inline-block size-1.5 animate-pulse rounded-full bg-primary will-change-[opacity]" />
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
