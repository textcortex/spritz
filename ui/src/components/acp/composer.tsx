import { useState, useRef, useCallback, useImperativeHandle, forwardRef } from 'react';
import { SendIcon, SquareIcon } from 'lucide-react';
import { Tooltip, TooltipTrigger, TooltipContent } from '@/components/ui/tooltip';

const TERMINAL_STATUSES = ['connected', 'completed', 'disconnected', 'no acp-ready instances'];

export interface ComposerHandle {
  fillText: (value: string) => void;
  focus: () => void;
}

interface ComposerProps {
  onSend: (text: string) => void;
  onCancel: () => void;
  disabled: boolean;
  promptInFlight: boolean;
  status?: string;
}

export const Composer = forwardRef<ComposerHandle, ComposerProps>(function Composer({ onSend, onCancel, disabled, promptInFlight, status }, ref) {
  const [text, setText] = useState('');
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  useImperativeHandle(ref, () => ({
    fillText(value: string) {
      setText(value);
      setTimeout(() => textareaRef.current?.focus(), 0);
    },
    focus() {
      textareaRef.current?.focus();
    },
  }));

  const handleSubmit = useCallback(() => {
    const trimmed = text.trim();
    if (!trimmed || disabled) return;
    onSend(trimmed);
    setText('');
    if (textareaRef.current) {
      textareaRef.current.style.height = 'auto';
      textareaRef.current.focus();
    }
  }, [text, disabled, onSend]);

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
    setText(e.target.value);
    const el = e.target;
    el.style.height = 'auto';
    el.style.height = Math.min(el.scrollHeight, 180) + 'px';
  }, []);

  const isTerminal = status
    ? TERMINAL_STATUSES.some((s) => status.toLowerCase().startsWith(s))
    : true;

  return (
    <div className="shrink-0 bg-white dark:bg-background">
      <div className="mx-auto flex w-full max-w-[880px] flex-col gap-2.5 px-0 py-3 sm:py-4">
        {/* Composer input */}
        <div className="mx-3 flex flex-col rounded-[28px] border border-[#e5e5e5] bg-white shadow-[0_2px_8px_rgba(99,99,99,0.19)] focus-within:border-[#cccccc] sm:mx-0">
          <textarea
            ref={textareaRef}
            value={text}
            onChange={handleInput}
            onKeyDown={handleKeyDown}
            placeholder={promptInFlight ? 'Waiting for response\u2026' : 'Message the agent\u2026'}
            disabled={disabled || promptInFlight}
            rows={1}
            className="block w-full min-h-[24px] max-h-[180px] resize-none border-none bg-transparent rounded-t-[28px] px-5 pt-4 pb-1 font-inherit text-sm leading-[1.55] outline-none placeholder:text-[#999] focus:outline-none focus:ring-0 [&::-webkit-scrollbar]:hidden [-ms-overflow-style:none] [scrollbar-width:none] overflow-y-auto"
          />
          <div className="relative z-[1] -mt-4 flex items-center justify-end gap-2 rounded-b-[28px] bg-[linear-gradient(to_bottom,transparent,white_60%)] px-3 pb-3 pt-5">
            <Tooltip>
              <TooltipTrigger
                render={
                  <button
                    type="button"
                    className="flex size-9 items-center justify-center rounded-full border-none bg-black p-0 text-white transition-opacity will-change-[opacity] hover:opacity-80 disabled:opacity-40 disabled:cursor-not-allowed"
                    onClick={promptInFlight ? onCancel : handleSubmit}
                    disabled={!promptInFlight && (disabled || !text.trim())}
                  >
                    {promptInFlight ? (
                      <SquareIcon className="size-3.5 fill-current" />
                    ) : (
                      <SendIcon className="size-4" />
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
          <span className="inline-flex items-center gap-2 opacity-70">
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
    <span className="inline-grid shrink-0 grid-cols-[repeat(3,4px)] grid-rows-[repeat(3,4px)] gap-[1.5px]">
      {Array.from({ length: 9 }).map((_, i) => (
        <span
          key={i}
          className="rounded-[1px] bg-[#d4d4d4]"
          style={{
            animation: `grid-pulse 1.2s ease-in-out infinite ${[0, 0.1, 0.2, 0.7, 0.8, 0.3, 0.6, 0.5, 0.4][i]}s`,
            willChange: 'background-color',
          }}
        />
      ))}
    </span>
  );
}
