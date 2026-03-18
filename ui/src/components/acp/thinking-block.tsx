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
    <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/>
    </svg>
  );
}

function ChevronIcon() {
  return (
    <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
      <polyline points="9 18 15 12 9 6"/>
    </svg>
  );
}

function ToolIcon() {
  return (
    <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
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
      <div className="w-[7px] h-[7px] rounded-full bg-[#60a5fa] shrink-0 mt-[5px] relative z-[1]" />
      <div className="flex-1 min-w-0">
        {isLong && !showFull ? (
          <div>
            <span
              className="text-xs text-[#666] leading-[1.4] cursor-pointer"
              onClick={() => setShowFull(true)}
            >
              {excerpt(text, 120)}
              <span className="text-[#999] text-[11px] ml-0.5"> ...more</span>
            </span>
          </div>
        ) : isLong ? (
          <div>
            <span
              className="text-xs text-[#555] leading-[1.5] whitespace-pre-wrap break-words cursor-pointer"
              onClick={() => setShowFull(false)}
            >
              {text}
            </span>
          </div>
        ) : (
          <span className="text-xs text-[#666] leading-[1.4]">{text}</span>
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
      <div className="w-[7px] h-[7px] rounded-full bg-[#a3a3a3] shrink-0 mt-[5px] relative z-[1]" />
      <div className="flex-1 min-w-0">
        <span className="block text-xs text-[#999] mb-1.5">Searching</span>
        <div className="flex flex-wrap gap-1.5">
          {pills.map((text, i) => (
            <span
              key={i}
              className="inline-flex items-center gap-1 py-0.5 px-2 pl-1.5 rounded-full text-[11px] text-[#525252] leading-[1.3] max-w-[280px] overflow-hidden text-ellipsis whitespace-nowrap"
              style={{ background: 'rgba(0,0,0,0.05)' }}
            >
              <span className="shrink-0 text-[#999] inline-flex"><SearchIcon /></span>
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
      <div className="w-[7px] h-[7px] rounded-full bg-[#a3a3a3] shrink-0 mt-[5px] relative z-[1]" />
      <div className="flex-1 min-w-0">
        <span className="block text-xs text-[#999] mb-1.5">Reviewing sources</span>
        <div
          className="flex flex-col rounded-lg overflow-hidden"
          style={{ background: 'rgba(0,0,0,0.02)', border: '1px solid rgba(0,0,0,0.06)' }}
        >
          {sources.map((src, i) => (
            <div
              key={i}
              className="flex items-center gap-2 px-3 py-2"
              style={{ borderBottom: i < sources.length - 1 ? '1px solid rgba(0,0,0,0.06)' : 'none' }}
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
                className="flex-1 text-xs text-[#333] no-underline hover:underline whitespace-nowrap overflow-hidden text-ellipsis"
              >
                {src.url}
              </a>
              <span className="shrink-0 text-[11px] text-[#999]">{src.domain}</span>
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
          <span className="inline-flex text-[#999] shrink-0"><ToolIcon /></span>
          <span className="text-xs text-[#999]">{name}</span>
          {chunk.status && chunk.status !== 'pending' && (
            <span
              className="inline-block text-[10px] py-[1px] px-1.5 rounded-full ml-0.5 capitalize"
              style={statusBadgeStyle}
            >
              {chunk.status.replace(/_/g, ' ')}
            </span>
          )}
        </div>
        {(chunk.input || chunk.result) && (
          <div className="mt-1">
            <button
              type="button"
              onClick={() => setDetailsOpen(!detailsOpen)}
              className="border-none bg-transparent p-0 text-[11px] text-[#999] cursor-pointer select-none"
            >
              {detailsOpen ? '▾' : '▸'} Details
            </button>
            {detailsOpen && (
              <div className="flex flex-col gap-1.5 mt-1.5">
                {chunk.input && (
                  <div>
                    <div className="text-[10px] font-semibold text-[#999] mb-0.5 uppercase tracking-[0.5px]">Input</div>
                    <pre className="m-0 max-h-[200px] overflow-auto rounded-md p-2 text-[11px]" style={{ background: 'rgba(0,0,0,0.03)' }}>
                      {chunk.input}
                    </pre>
                  </div>
                )}
                {chunk.result && (
                  <div>
                    <div className="text-[10px] font-semibold text-[#999] mb-0.5 uppercase tracking-[0.5px]">Result</div>
                    <pre className="m-0 max-h-[200px] overflow-auto rounded-md p-2 text-[11px]" style={{ background: 'rgba(0,0,0,0.03)' }}>
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
      <div className="w-[7px] h-[7px] rounded-full bg-[#525252] shrink-0 mt-[5px] relative z-[1]" />
      <div className="flex-1 min-w-0">
        <span className="text-[13px] text-[#525252] font-medium leading-[1.4]">Finished</span>
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
      className="self-start py-1"
      style={{ maxWidth: 'min(820px, 86%)' }}
    >
      {/* Header button */}
      <button
        type="button"
        className="inline-flex items-center gap-2 py-1 px-0 border-none bg-transparent cursor-pointer text-[13px] font-medium leading-none hover:text-[#333]"
        style={{ color: isDone ? '#999' : '#666', fontFamily: 'inherit' }}
        onClick={() => setExpanded(!expanded)}
        aria-expanded={expanded}
      >
        {/* Morph SVG icon — hidden when done */}
        {!isDone && (
          <span className="inline-flex items-center justify-center shrink-0 text-[#999]">
            {MORPH_SVG}
          </span>
        )}

        {/* Word label */}
        {isDone ? (
          <span className="text-[13px] text-[#999] leading-[1.4]">{doneText}</span>
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
          className="inline-flex items-center justify-center shrink-0 text-[#999] transition-transform duration-200"
          style={{ transform: expanded ? 'rotate(90deg)' : 'rotate(0deg)' }}
        >
          <ChevronIcon />
        </span>
      </button>

      {/* Collapsible body — grid-template-rows transition */}
      <div
        className="grid transition-[grid-template-rows] duration-300 ease-in-out"
        style={{ gridTemplateRows: expanded ? '1fr' : '0fr' }}
      >
        <div className="overflow-hidden min-h-0">
          {/* Timeline with vertical line */}
          <div className="relative pl-3 pt-1.5 pb-0.5">
            {/* Vertical line */}
            <div
              className="absolute top-1.5 bottom-0.5 w-px bg-[#e0e0e0]"
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
