import type { PermissionEntry } from '@/types/acp';

interface PermissionOption {
  optionId: string;
  name?: string;
}

interface PermissionDialogProps {
  entry: PermissionEntry;
  onRespond: () => void;
}

export function PermissionDialog({ entry, onRespond }: PermissionDialogProps) {
  const params = entry.params as Record<string, unknown> | undefined;
  const toolCall = params?.toolCall as Record<string, unknown> | undefined;
  const toolTitle = String(toolCall?.title || toolCall?.toolCallId || params?.tool || params?.name || 'Tool call');
  const options = (params?.options || []) as PermissionOption[];

  const handleOption = (option: PermissionOption) => {
    entry.respond({
      outcome: {
        outcome: 'selected',
        optionId: option.optionId,
      },
    });
    onRespond();
  };

  const handleDeny = () => {
    entry.reject('Permission denied by user.');
    onRespond();
  };

  return (
    <div role="alertdialog" aria-label={`${toolTitle} permission request`} className="flex flex-col gap-2.5 rounded-[var(--radius-xl)] border border-border bg-surface-subtle px-4 py-3.5 text-sm">
      <p className="m-0 text-sm">{toolTitle} is requesting permission.</p>
      <div className="flex flex-wrap gap-1.5">
        {options.length > 0 ? (
          options.map((option, i) => (
            <button
              key={option.optionId}
              type="button"
              className={
                i === 0
                  ? 'rounded-[var(--radius-lg)] border border-transparent bg-primary px-4 py-2 text-[13px] text-primary-foreground hover:opacity-85'
                  : 'rounded-[var(--radius-lg)] border border-border bg-background px-4 py-2 text-[13px] text-foreground hover:bg-muted'
              }
              onClick={() => handleOption(option)}
            >
              {option.name || option.optionId}
            </button>
          ))
        ) : (
          <>
            <button
              type="button"
              className="rounded-[var(--radius-lg)] border border-transparent bg-primary px-4 py-2 text-[13px] text-primary-foreground hover:opacity-85"
              onClick={() => { entry.respond({ allow: true }); onRespond(); }}
            >
              Allow
            </button>
            <button
              type="button"
              className="rounded-[var(--radius-lg)] border border-border bg-background px-4 py-2 text-[13px] text-foreground hover:bg-muted"
              onClick={handleDeny}
            >
              Deny
            </button>
          </>
        )}
      </div>
    </div>
  );
}
