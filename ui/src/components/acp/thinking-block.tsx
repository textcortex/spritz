import { useState, useEffect, useRef, useCallback } from 'react';
import type { ThinkingChunk } from '@/types/acp';

/* ── Constants ── */

const THINKING_WORDS = ['Thinking', 'Planning', 'Refining'];

const MORPH_SVG = (
  <svg className="block" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
    <path d="M 12 8 C 14.21 8 16 9.79 16 12 C 16 14.21 14.21 16 12 16 C 9.79 16 8 14.21 8 12 C 8 9.79 9.79 8 12 8 Z">
      <animate
        attributeName="d"
        values="M 12 8 C 14.21 8 16 9.79 16 12 C 16 14.21 14.21 16 12 16 C 9.79 16 8 14.21 8 12 C 8 9.79 9.79 8 12 8 Z;M 12 12 C 14 8.5 19 8.5 19 12 C 19 15.5 14 15.5 12 12 C 10 8.5 5 8.5 5 12 C 5 15.5 10 15.5 12 12 Z;M 12 16 C 14.21 16 16 14.21 16 12 C 16 9.79 14.21 8 12 8 C 9.79 8 8 9.79 8 12 C 8 14.21 9.79 16 12 16 Z;M 12 12 C 14 8.5 19 8.5 19 12 C 19 15.5 14 15.5 12 12 C 10 8.5 5 8.5 5 12 C 5 15.5 10 15.5 12 12 Z;M 12 8 C 14.21 8 16 9.79 16 12 C 16 14.21 14.21 16 12 16 C 9.79 16 8 14.21 8 12 C 8 9.79 9.79 8 12 8 Z"
        dur="6s"
        repeatCount="indefinite"
        calcMode="spline"
        keySplines="0.4 0 0.2 1; 0.4 0 0.2 1; 0.4 0 0.2 1; 0.4 0 0.2 1"
      />
    </path>
  </svg>
);

/* ── Helpers ── */

function excerpt(text: string, max = 120): string {
  const n = String(text || '').replace(/\s+/g, ' ').trim();
  if (!n) return '';
  return n.length <= max ? n : n.slice(0, max - 1) + '…';
}

function domainFromUrl(url: string): string {
  try { return new URL(url).hostname.replace(/^www\./, ''); }
  catch { return url; }
}

/* ── Inline SVG icons ── */

function SearchIcon() {
  return (
    <svg aria-hidden="true" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/>
    </svg>
  );
}

function ChevronIcon() {
  return (
    <svg aria-hidden="true" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
      <polyline points="9 18 15 12 9 6"/>
    </svg>
  );
}

function ToolIcon() {
  return (
    <svg aria-hidden="true" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z"/>
    </svg>
  );
}

/* ── Timeline step renderers ── */

function ThoughtStep({ chunk }: { chunk: ThinkingChunk }) {
  const [showFull, setShowFull] = useState(false);
  const text = (chunk.text || '').trim();
  if (!text) return null;
  const isLong = text.length > 120;

  return (
    <div className="flex items-start gap-2.5 relative pb-1.5" style={{ paddingLeft: 0 }}>
      {/* dot */}
      <div className="relative z-[1] mt-[5px] h-[7px] w-[7px] shrink-0 rounded-full bg-primary" />
      <div className="flex-1 min-w-0">
        {isLong && !showFull ? (
          <div>
            <span
              className="cursor-pointer text-xs leading-1 text-muted-foreground"
              onClick={() => setShowFull(true)}
            >
              {excerpt(text, 120)}
              <span className="ml-0.5 text-[11px] text-muted-foreground/80"> ...more</span>
            </span>
          </div>
        ) : isLong ? (
          <div>
            <span
              className="cursor-pointer whitespace-pre-wrap break-words text-xs leading-[1.5] text-foreground/80"
              onClick={() => setShowFull(false)}
            >
              {text}
            </span>
          </div>
        ) : (
          <span className="text-xs leading-[1.4] text-muted-foreground">{text}</span>
        )}
      </div>
    </div>
  );
}

function SearchStep({ items }: { items: ThinkingChunk[] }) {
  const pills = items
    .map(item => {
      const clean = (item.text || '').replace(/^"|"$/g, '').trim();
      return item.url || clean;
    })
    .filter(t => t && t !== 'undefined');

  if (pills.length === 0) return null;

  return (
    <div className="flex items-start gap-2.5 relative pb-1.5">
      <div className="relative z-[1] mt-[5px] h-[7px] w-[7px] shrink-0 rounded-full bg-muted-foreground" />
      <div className="flex flex-1 flex-col gap-1.5 min-w-0">
        <span className="block text-xs text-muted-foreground">Searching</span>
        <div className="flex flex-wrap gap-1.5">
          {pills.map((text, i) => (
            <span
              key={i}
              className="inline-flex max-w-[280px] items-center gap-1 overflow-hidden rounded-[var(--radius-2xl)] bg-muted px-2 py-0.5 pl-1.5 text-[11px] leading-[1.3] text-foreground/80 text-ellipsis whitespace-nowrap"
            >
              <span className="inline-flex shrink-0 text-muted-foreground"><SearchIcon /></span>
              <span>{text}</span>
            </span>
          ))}
        </div>
      </div>
    </div>
  );
}

function FetchStep({ items }: { items: ThinkingChunk[] }) {
  const sources = items
    .filter(item => item.url)
    .map(item => ({ url: item.url!, domain: domainFromUrl(item.url!) }));

  if (sources.length === 0) return null;

  return (
    <div className="flex items-start gap-2.5 relative pb-1.5">
      <div className="relative z-[1] mt-[5px] h-[7px] w-[7px] shrink-0 rounded-full bg-muted-foreground" />
      <div className="flex flex-1 flex-col gap-1.5 min-w-0">
        <span className="block text-xs text-muted-foreground">Reviewing sources</span>
        <div
          className="flex flex-col overflow-hidden rounded-[var(--radius-lg)]"
          style={{ background: 'color-mix(in srgb, var(--muted) 40%, transparent)', border: '1px solid color-mix(in srgb, var(--border) 80%, transparent)' }}
        >
          {sources.map((src, i) => (
            <div
              key={i}
              className="flex items-center gap-2 px-3 py-2"
              style={{ borderBottom: i < sources.length - 1 ? '1px solid color-mix(in srgb, var(--border) 80%, transparent)' : 'none' }}
            >
              <img
                src={`https://www.google.com/s2/favicons?domain=${src.domain}&sz=32`}
                alt=""
                width={16}
                height={16}
                className="shrink-0 rounded-[2px]"
              />
              <a
                href={src.url}
                target="_blank"
                rel="noopener noreferrer"
                className="flex-1 overflow-hidden text-ellipsis whitespace-nowrap text-xs text-foreground no-underline hover:underline"
              >
                {src.url}
              </a>
              <span className="shrink-0 text-[11px] text-muted-foreground">{src.domain}</span>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}

function GenericToolStep({ chunk }: { chunk: ThinkingChunk }) {
  const [detailsOpen, setDetailsOpen] = useState(false);
  const name = chunk.toolName || chunk.text || 'Tool call';
  const statusClass =
    chunk.status === 'completed' ? 'completed'
    : chunk.status === 'failed' ? 'failed'
    : 'pending';

  const dotColor =
    statusClass === 'completed' ? 'bg-[#34d399]'
    : statusClass === 'failed' ? 'bg-[#f87171]'
    : 'bg-[#60a5fa]';

  const statusBadgeStyle =
    statusClass === 'completed' ? { color: '#059669', background: 'rgba(5,150,105,0.1)' }
    : statusClass === 'failed' ? { color: '#dc2626', background: 'rgba(220,38,38,0.1)' }
    : { color: '#666', background: 'rgba(0,0,0,0.05)' };

  return (
    <div className="flex items-start gap-2.5 relative pb-1.5">
      <div className={`w-[7px] h-[7px] rounded-full ${dotColor} shrink-0 mt-[5px] relative z-[1]`} />
      <div className="flex-1 min-w-0">
        <div className="flex items-center gap-1.5">
          <span className="inline-flex shrink-0 text-muted-foreground"><ToolIcon /></span>
          <span className="text-xs text-muted-foreground">{name}</span>
          {chunk.status && chunk.status !== 'pending' && (
            <span
              className="inline-block rounded-[var(--radius-2xl)] px-1.5 py-[1px] text-[10px] capitalize"
              style={statusBadgeStyle}
            >
              {chunk.status.replace(/_/g, ' ')}
            </span>
          )}
        </div>
        {(chunk.input || chunk.result) && (
          <div className="flex flex-col gap-1">
            <button
              type="button"
              aria-expanded={detailsOpen}
              onClick={() => setDetailsOpen(!detailsOpen)}
              className="w-fit cursor-pointer select-none border-none bg-transparent p-0 text-[11px] text-muted-foreground"
            >
              <span aria-hidden="true">{detailsOpen ? '▾' : '▸'}</span> Details
            </button>
            {detailsOpen && (
              <div className="flex flex-col gap-1.5">
                {chunk.input && (
                  <div className="flex flex-col gap-0.5">
                    <div className="text-[10px] font-semibold uppercase tracking-[0.5px] text-muted-foreground">Input</div>
                    <pre className="m-0 max-h-[200px] overflow-auto rounded-[var(--radius-md)] bg-muted/60 p-2 text-[11px]">
                      {chunk.input}
                    </pre>
                  </div>
                )}
                {chunk.result && (
                  <div className="flex flex-col gap-0.5">
                    <div className="text-[10px] font-semibold uppercase tracking-[0.5px] text-muted-foreground">Result</div>
                    <pre className="m-0 max-h-[200px] overflow-auto rounded-[var(--radius-md)] bg-muted/60 p-2 text-[11px]">
                      {chunk.result}
                    </pre>
                  </div>
                )}
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}

function FinishedStep() {
  return (
    <div className="flex items-start gap-2.5 relative pb-0">
      <div className="relative z-[1] mt-[5px] h-[7px] w-[7px] shrink-0 rounded-full bg-foreground/70" />
      <div className="flex-1 min-w-0">
        <span className="text-[13px] font-medium leading-[1.4] text-foreground/80">Finished</span>
      </div>
    </div>
  );
}

/* ── Grouped timeline builder ── */

interface TimelineGroup {
  kind: 'thought' | 'search' | 'fetch' | 'generic' | 'finished';
  items: ThinkingChunk[];
}

function buildGroupedTimeline(chunks: ThinkingChunk[], showFinished: boolean): TimelineGroup[] {
  const groups: TimelineGroup[] = [];
  let i = 0;

  while (i < chunks.length) {
    const chunk = chunks[i];

    if (chunk.kind === 'thought') {
      if (chunk.text?.trim()) {
        groups.push({ kind: 'thought', items: [chunk] });
      }
      i++;
      continue;
    }

    if (chunk.kind !== 'tool') { i++; continue; }

    // Group consecutive search
    if (chunk.toolKind === 'search') {
      const items: ThinkingChunk[] = [];
      while (i < chunks.length && chunks[i].kind === 'tool' && chunks[i].toolKind === 'search') {
        items.push(chunks[i]);
        i++;
      }
      groups.push({ kind: 'search', items });
      continue;
    }

    // Group consecutive fetch
    if (chunk.toolKind === 'fetch') {
      const items: ThinkingChunk[] = [];
      while (i < chunks.length && chunks[i].kind === 'tool' && chunks[i].toolKind === 'fetch') {
        items.push(chunks[i]);
        i++;
      }
      groups.push({ kind: 'fetch', items });
      continue;
    }

    // Generic tool
    groups.push({ kind: 'generic', items: [chunk] });
    i++;
  }

  if (showFinished) {
    groups.push({ kind: 'finished', items: [] });
  }

  return groups;
}

/* ── Main ThinkingBlock component ── */

interface ThinkingBlockProps {
  chunks: ThinkingChunk[];
  active: boolean;
  elapsedSeconds: number;
}

export function ThinkingBlock({ chunks, active, elapsedSeconds }: ThinkingBlockProps) {
  const [expanded, setExpanded] = useState(true);
  const [wordIndex, setWordIndex] = useState(0);
  const [wordAnim, setWordAnim] = useState<'' | 'word-exit' | 'word-enter'>('');
  const wordRef = useRef<HTMLSpanElement>(null);
  const prevActiveRef = useRef(active);

  // Auto-expand when thinking starts, auto-collapse when done
  useEffect(() => {
    if (active && !prevActiveRef.current) {
      setExpanded(true);
    } else if (!active && prevActiveRef.current) {
      setExpanded(false);
    }
    prevActiveRef.current = active;
  }, [active]);

  // Rotating words
  useEffect(() => {
    if (!active) return;
    const interval = setInterval(() => {
      setWordAnim('word-exit');
      setTimeout(() => {
        setWordIndex(prev => (prev + 1) % THINKING_WORDS.length);
        setWordAnim('word-enter');
        setTimeout(() => setWordAnim(''), 240);
      }, 160);
    }, 2500);
    return () => clearInterval(interval);
  }, [active]);

  if (!chunks.length && !active) return null;

  const isDone = !active;
  const currentWord = THINKING_WORDS[wordIndex];
  const longestWord = THINKING_WORDS.reduce((a, b) => a.length >= b.length ? a : b);
  const doneText = 'Thought';
  const timeline = buildGroupedTimeline(chunks, isDone && chunks.length > 0);

  return (
    <div
      role="region"
      aria-label={isDone ? 'Thought process completed' : 'Agent thinking'}
      className="self-start py-1"
      style={{ maxWidth: 'min(820px, 86%)' }}
    >
      {/* Header button */}
      <button
        type="button"
        className="inline-flex cursor-pointer items-center gap-2 border-none bg-transparent px-0 py-1 text-[13px] font-medium leading-none text-muted-foreground hover:text-foreground"
        style={{ color: isDone ? 'var(--muted-foreground)' : 'var(--muted-foreground)', fontFamily: 'inherit' }}
        onClick={() => setExpanded(!expanded)}
        aria-expanded={expanded}
      >
        {/* Morph SVG icon — hidden when done */}
        {!isDone && (
          <span className="inline-flex shrink-0 items-center justify-center text-muted-foreground">
            {MORPH_SVG}
          </span>
        )}

        {/* Word label */}
        {isDone ? (
          <span className="text-sm font-normal leading-[1.4]">{doneText}</span>
        ) : (
          <span className="inline-grid overflow-hidden text-[13px] leading-[1.4]" style={{ paddingBottom: 1 }}>
            {/* Invisible sizer to prevent layout shift */}
            <span className="col-start-1 row-start-1 invisible whitespace-nowrap shimmer-text" aria-hidden="true">
              {longestWord}
            </span>
            {/* Visible animated label */}
            <span
              ref={wordRef}
              className={`col-start-1 row-start-1 whitespace-nowrap shimmer-text ${wordAnim}`}
            >
              {currentWord}
            </span>
          </span>
        )}

        {/* Chevron */}
        <span
          className="inline-flex shrink-0 items-center justify-center text-muted-foreground transition-transform duration-200 will-change-transform"
          style={{ transform: expanded ? 'rotate(90deg)' : 'rotate(0deg)' }}
        >
          <ChevronIcon />
        </span>
      </button>

      {/* Collapsible body — grid-template-rows transition */}
      <div
        className="grid transition-[grid-template-rows] duration-300 ease-in-out will-change-[grid-template-rows]"
        style={{ gridTemplateRows: expanded ? '1fr' : '0fr' }}
      >
        <div className="overflow-hidden min-h-0">
          {/* Timeline with vertical line */}
          <div className="relative pl-3 pt-1.5 pb-0.5">
            {/* Vertical line */}
            <div
              className="absolute top-1.5 bottom-0.5 w-px bg-border"
              style={{ left: 15 /* 12px padding + 3px to center on 7px dot */ }}
            />
            {timeline.map((group, i) => {
              if (group.kind === 'thought') return <ThoughtStep key={i} chunk={group.items[0]} />;
              if (group.kind === 'search') return <SearchStep key={i} items={group.items} />;
              if (group.kind === 'fetch') return <FetchStep key={i} items={group.items} />;
              if (group.kind === 'finished') return <FinishedStep key={i} />;
              return <GenericToolStep key={i} chunk={group.items[0]} />;
            })}
          </div>
        </div>
      </div>
    </div>
  );
}
