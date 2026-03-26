import { useRef, useCallback, useImperativeHandle, forwardRef, useLayoutEffect } from 'react';
import { SendIcon, SquareIcon } from 'lucide-react';
import { Tooltip, TooltipTrigger, TooltipContent } from '@/components/ui/tooltip';

const TERMINAL_STATUSES = ['connected', 'completed', 'disconnected', 'no acp-ready instances'];

export interface ComposerHandle {
  fillText: (value: string) => void;
  focus: () => void;
}

interface ComposerProps {
  value: string;
  onValueChange: (value: string) => void;
  onSend: (text: string) => void;
  onCancel: () => void;
  disabled: boolean;
  promptInFlight: boolean;
  status?: string;
}

function syncTextareaHeight(textarea: HTMLTextAreaElement | null) {
  if (!textarea) return;
  textarea.style.height = 'auto';
  textarea.style.height = Math.min(textarea.scrollHeight, 180) + 'px';
}

export const Composer = forwardRef<ComposerHandle, ComposerProps>(function Composer({ value, onValueChange, onSend, onCancel, disabled, promptInFlight, status }, ref) {
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  useImperativeHandle(ref, () => ({
    fillText(value: string) {
      onValueChange(value);
      setTimeout(() => textareaRef.current?.focus(), 0);
    },
    focus() {
      textareaRef.current?.focus();
    },
  }), [onValueChange]);

  useLayoutEffect(() => {
    syncTextareaHeight(textareaRef.current);
  }, [value]);

  const handleSubmit = useCallback(() => {
    const trimmed = value.trim();
    if (!trimmed || disabled) return;
    onSend(trimmed);
    if (textareaRef.current) {
      textareaRef.current.style.height = 'auto';
      textareaRef.current.focus();
    }
  }, [value, disabled, onSend]);

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault();
        if (promptInFlight) return;
        handleSubmit();
      }
    },
    [handleSubmit, promptInFlight],
  );

  const handleInput = useCallback((e: React.ChangeEvent<HTMLTextAreaElement>) => {
    onValueChange(e.target.value);
    syncTextareaHeight(e.target);
  }, [onValueChange]);

  const isTerminal = status
    ? TERMINAL_STATUSES.some((s) => status.toLowerCase().startsWith(s))
    : true;

  return (
    <div className="shrink-0 bg-background">
      <div className="mx-auto flex w-[90%] max-w-[880px] flex-col gap-2.5 px-0 py-3 sm:py-4">
        {/* Composer input */}
        <div
          className="mx-3 flex cursor-text flex-col rounded-[var(--radius-2xl)] border border-border bg-background shadow-[0_6px_24px_color-mix(in_srgb,var(--primary)_10%,transparent)] focus-within:border-primary sm:mx-0"
          onClick={(e) => {
            if ((e.target as HTMLElement).closest('button')) return;
            textareaRef.current?.focus();
          }}
        >
          <textarea
            ref={textareaRef}
            value={value}
            onChange={handleInput}
            onKeyDown={handleKeyDown}
            aria-label="Message input"
            placeholder={promptInFlight ? 'Waiting for response\u2026' : 'Message the agent\u2026'}
            disabled={disabled || promptInFlight}
            rows={1}
            className="block w-full min-h-[24px] max-h-[180px] resize-none rounded-t-[var(--radius-2xl)] border-none bg-transparent px-5 pt-4 pb-1 font-inherit text-sm leading-[1.55] outline-none placeholder:text-muted-foreground focus:outline-none focus:ring-0 [&::-webkit-scrollbar]:hidden [-ms-overflow-style:none] [scrollbar-width:none] overflow-y-auto"
          />
          <div className="relative z-[1] -mt-4 flex items-center justify-end gap-2 rounded-b-[var(--radius-2xl)] bg-[linear-gradient(to_bottom,transparent,var(--background)_60%)] px-3 pb-3">
            <Tooltip>
              <TooltipTrigger
                render={
                  <button
                    type="button"
                    aria-label={promptInFlight ? 'Stop response' : 'Send message'}
                    className="flex size-9 items-center justify-center rounded-[var(--radius-lg)] border-none bg-primary p-0 text-primary-foreground transition-opacity will-change-[opacity] hover:opacity-85 disabled:cursor-not-allowed disabled:opacity-40"
                    onClick={promptInFlight ? onCancel : handleSubmit}
                    disabled={!promptInFlight && (disabled || !value.trim())}
                  >
                    {promptInFlight ? (
                      <SquareIcon aria-hidden="true" className="size-3.5 fill-current" />
                    ) : (
                      <SendIcon aria-hidden="true" className="size-4" />
                    )}
                  </button>
                }
              />
              <TooltipContent>{promptInFlight ? 'Stop' : 'Send'}</TooltipContent>
            </Tooltip>
          </div>
        </div>

        {/* Status row — below composer, matching original acp-status-row */}
        <div className="grid grid-cols-[auto_auto_1fr] items-center gap-3 px-3 text-[13px] sm:px-0">
          <span role="status" aria-live="polite" className="inline-flex items-center gap-2 opacity-70">
            {status && !isTerminal && <GridLoader />}
            {status && <span>{status}</span>}
          </span>
          <span>{/* status-meta placeholder */}</span>
          <span className="justify-self-end text-xs opacity-60">
            Enter sends. Shift+Enter adds a new line.
          </span>
        </div>
      </div>
    </div>
  );
});

/** 3x3 grid loader matching the old .grid-loader */
function GridLoader() {
  return (
    <span role="img" aria-label="Loading" className="inline-grid shrink-0 grid-cols-[repeat(3,4px)] grid-rows-[repeat(3,4px)] gap-[1.5px]">
      {Array.from({ length: 9 }).map((_, i) => (
        <span
          key={i}
          className="rounded-[1px] bg-muted"
          style={{
            animation: `grid-pulse 1.2s ease-in-out infinite ${[0, 0.1, 0.2, 0.7, 0.8, 0.3, 0.6, 0.5, 0.4][i]}s`,
            willChange: 'background-color',
          }}
        />
      ))}
    </span>
  );
}
