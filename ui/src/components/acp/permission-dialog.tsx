import { ShieldAlertIcon } from 'lucide-react';
import { Button } from '@/components/ui/button';
import type { PermissionEntry } from '@/types/acp';
import { extractACPText } from '@/lib/acp-client';

interface PermissionDialogProps {
  entry: PermissionEntry;
  onRespond: () => void;
}

export function PermissionDialog({ entry, onRespond }: PermissionDialogProps) {
  const params = entry.params as Record<string, unknown> | undefined;
  const description = extractACPText(params?.description || params?.message || '');
  const tool = String(params?.tool || params?.name || 'unknown');

  const handleAllow = () => {
    entry.respond({ allow: true });
    onRespond();
  };

  const handleDeny = () => {
    entry.reject('Permission denied by user.');
    onRespond();
  };

  return (
    <div className="rounded-2xl border border-amber-200 bg-amber-50 p-4 dark:border-amber-800 dark:bg-amber-950/30">
      <div className="mb-2 flex items-center gap-2">
        <ShieldAlertIcon className="size-4 text-amber-600" />
        <span className="text-sm font-medium">Permission Required</span>
      </div>
      <p className="mb-1 text-xs text-muted-foreground">
        Tool: <code className="rounded bg-muted px-1">{tool}</code>
      </p>
      {description && (
        <p className="mb-3 text-sm">{description}</p>
      )}
      <div className="flex gap-2">
        <Button size="sm" className="rounded-full" onClick={handleAllow}>
          Allow
        </Button>
        <Button size="sm" variant="outline" className="rounded-full" onClick={handleDeny}>
          Deny
        </Button>
      </div>
    </div>
  );
}
