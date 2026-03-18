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
    <div className="flex flex-col gap-2.5 rounded-2xl border border-[#e5e5e5] bg-[#fafafa] px-4 py-3.5 text-sm">
      <p className="m-0 text-sm">{toolTitle} is requesting permission.</p>
      <div className="flex flex-wrap gap-1.5">
        {options.length > 0 ? (
          options.map((option, i) => (
            <button
              key={option.optionId}
              type="button"
              className={
                i === 0
                  ? 'rounded-full border border-transparent bg-black px-4 py-2 text-[13px] text-white hover:opacity-80'
                  : 'rounded-full border border-[#e5e5e5] bg-white px-4 py-2 text-[13px] text-black hover:border-[#ccc] hover:bg-[#f5f5f5]'
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
              className="rounded-full border border-transparent bg-black px-4 py-2 text-[13px] text-white hover:opacity-80"
              onClick={() => { entry.respond({ allow: true }); onRespond(); }}
            >
              Allow
            </button>
            <button
              type="button"
              className="rounded-full border border-[#e5e5e5] bg-white px-4 py-2 text-[13px] text-black hover:border-[#ccc] hover:bg-[#f5f5f5]"
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
