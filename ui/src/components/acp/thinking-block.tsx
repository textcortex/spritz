import { useState, useEffect, useRef } from 'react';
import { ChevronRightIcon, GlobeIcon, SearchIcon } from 'lucide-react';
import { cn } from '@/lib/utils';
import type { ThinkingChunk } from '@/types/acp';

const THINKING_WORDS = ['Thinking', 'Planning', 'Refining'];

/* ── Grid loader — 3×3 pulsing dots ── */
function GridLoader() {
  return (
    <div className="inline-grid grid-cols-3 gap-[1.5px]">
      {Array.from({ length: 9 }).map((_, i) => (
        <span
          key={i}
          className="size-[4px] rounded-[1px]"
          style={{
            animation: 'grid-pulse 1.2s ease-in-out infinite',
            animationDelay: `${[0, 100, 200, 300, 700, 300, 600, 500, 800][i]}ms`,
            background: '#d4d4d4',
          }}
        />
      ))}
    </div>
  );
}

/* ── Timeline step ── */
function TimelineStep({
  children,
  isLast,
}: {
  children: React.ReactNode;
  isLast: boolean;
}) {
  return (
    <div className="relative flex gap-3 pb-2 last:pb-0">
      {/* Vertical line */}
      {!isLast && (
        <div className="absolute left-[3px] top-[10px] bottom-0 w-px bg-border" />
      )}
      {/* Dot */}
      <div className="relative z-10 mt-[5px] size-[7px] shrink-0 rounded-full bg-muted-foreground/40" />
      {/* Content */}
      <div className="flex-1 min-w-0">{children}</div>
    </div>
  );
}

interface ThinkingBlockProps {
  chunks: ThinkingChunk[];
  active: boolean;
  elapsedSeconds: number;
}

export function ThinkingBlock({ chunks, active, elapsedSeconds }: ThinkingBlockProps) {
  const [wordIndex, setWordIndex] = useState(0);
  const [expanded, setExpanded] = useState(active);
  const [animClass, setAnimClass] = useState('');
  const prevIndexRef = useRef(wordIndex);

  // Rotate thinking words
  useEffect(() => {
    if (!active) return;
    const interval = setInterval(() => {
      setWordIndex((i) => (i + 1) % THINKING_WORDS.length);
    }, 2500);
    return () => clearInterval(interval);
  }, [active]);

  // Word swap animation
  useEffect(() => {
    if (prevIndexRef.current === wordIndex) return;
    prevIndexRef.current = wordIndex;
    setAnimClass('word-enter');
    const timer = setTimeout(() => setAnimClass(''), 240);
    return () => clearTimeout(timer);
  }, [wordIndex]);

  const hasContent = chunks.some((c) => c.kind === 'tool' && c.toolKind);
  if (!hasContent && !active) return null;

  const searchChunks = chunks.filter((c) => c.kind === 'tool' && c.toolKind === 'search');
  const fetchChunks = chunks.filter((c) => c.kind === 'tool' && c.toolKind === 'fetch');
  const hasTimeline = searchChunks.length > 0 || fetchChunks.length > 0;

  return (
    <div className="py-1">
      {/* Header */}
      <button
        type="button"
        className="flex items-center gap-2 py-1 text-[13px] text-muted-foreground hover:text-foreground/70"
        onClick={() => setExpanded(!expanded)}
      >
        {active ? (
          <GridLoader />
        ) : (
          <ChevronRightIcon
            className={cn(
              'size-3.5 transition-transform duration-150',
              expanded && 'rotate-90',
            )}
          />
        )}
        {/* Label with word swap */}
        <span className="inline-grid overflow-hidden">
          {/* Sizer for stable width */}
          <span className="invisible col-start-1 row-start-1 whitespace-nowrap text-[13px]">
            {THINKING_WORDS.reduce((a, b) => (a.length > b.length ? a : b))}…
          </span>
          <span
            className={cn(
              'col-start-1 row-start-1 whitespace-nowrap font-medium',
              active && 'shimmer-text',
              animClass,
            )}
          >
            {active ? THINKING_WORDS[wordIndex] + '…' : `Thought for ${elapsedSeconds}s`}
          </span>
        </span>
      </button>

      {/* Expandable body */}
      {expanded && hasTimeline && (
        <div className="ml-1 mt-1 pl-2">
          {searchChunks.length > 0 && (
            <div className="mb-2">
              <div className="mb-1.5 flex items-center gap-1.5 text-[11px] text-muted-foreground">
                <SearchIcon className="size-3" />
                Searching
              </div>
              <div className="space-y-0">
                {searchChunks.map((chunk, i) => (
                  <TimelineStep key={i} isLast={i === searchChunks.length - 1 && fetchChunks.length === 0}>
                    <span className="inline-flex items-center gap-1 rounded-full bg-muted/60 px-2.5 py-0.5 text-[11px] text-muted-foreground">
                      <SearchIcon className="size-2.5 shrink-0" />
                      <span className="max-w-[240px] truncate">{chunk.text}</span>
                    </span>
                  </TimelineStep>
                ))}
              </div>
            </div>
          )}
          {fetchChunks.length > 0 && (
            <div>
              <div className="mb-1.5 flex items-center gap-1.5 text-[11px] text-muted-foreground">
                <GlobeIcon className="size-3" />
                Reviewing sources
              </div>
              <div className="rounded-lg border bg-background overflow-hidden">
                {fetchChunks.map((chunk, i) => {
                  let domain = '';
                  try {
                    if (chunk.url) domain = new URL(chunk.url).hostname;
                  } catch { /* ignore */ }
                  return (
                    <div
                      key={i}
                      className={cn(
                        'flex items-center gap-2 px-3 py-2',
                        i < fetchChunks.length - 1 && 'border-b border-border/50',
                      )}
                    >
                      {domain && (
                        <img
                          src={`https://www.google.com/s2/favicons?domain=${domain}&sz=16`}
                          alt=""
                          className="size-3 shrink-0 rounded-sm"
                        />
                      )}
                      {chunk.url ? (
                        <a
                          href={chunk.url}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="flex-1 truncate text-xs text-foreground hover:underline"
                        >
                          {chunk.text || domain}
                        </a>
                      ) : (
                        <span className="flex-1 truncate text-xs">{chunk.text}</span>
                      )}
                      {domain && (
                        <span className="shrink-0 text-[11px] text-muted-foreground">{domain}</span>
                      )}
                    </div>
                  );
                })}
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
