import { useState, useRef, useCallback } from 'react';
import { ArrowUpIcon, XIcon } from 'lucide-react';
import { Button } from '@/components/ui/button';

interface ComposerProps {
  onSend: (text: string) => void;
  onCancel: () => void;
  disabled: boolean;
  promptInFlight: boolean;
}

export function Composer({ onSend, onCancel, disabled, promptInFlight }: ComposerProps) {
  const [text, setText] = useState('');
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  const handleSubmit = useCallback(() => {
    const trimmed = text.trim();
    if (!trimmed || disabled) return;
    onSend(trimmed);
    setText('');
    if (textareaRef.current) {
      textareaRef.current.style.height = 'auto';
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

  return (
    <div className="px-4 pb-4 pt-2">
      <div className="mx-auto max-w-3xl rounded-[28px] border border-border bg-background shadow-[0_2px_8px_rgba(99,99,99,0.12)]">
        <textarea
          ref={textareaRef}
          value={text}
          onChange={handleInput}
          onKeyDown={handleKeyDown}
          placeholder={promptInFlight ? 'Waiting for response…' : 'Send a message…'}
          disabled={disabled}
          rows={1}
          className="scrollbar-hidden block w-full min-h-[24px] max-h-[180px] resize-none border-0 bg-transparent px-5 pt-4 pb-1 text-sm leading-relaxed outline-none placeholder:text-muted-foreground focus:outline-none focus:ring-0"
        />
        <div className="flex items-center justify-end gap-2 px-3 pb-3">
          {promptInFlight ? (
            <Button
              type="button"
              variant="outline"
              size="icon"
              className="size-9 rounded-full"
              onClick={onCancel}
              title="Cancel"
            >
              <XIcon className="size-4" />
            </Button>
          ) : (
            <Button
              type="button"
              size="icon"
              className="size-9 rounded-full"
              onClick={handleSubmit}
              disabled={disabled || !text.trim()}
              title="Send"
            >
              <ArrowUpIcon className="size-4" />
            </Button>
          )}
        </div>
      </div>
    </div>
  );
}
