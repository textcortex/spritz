import { useState, useEffect, useCallback, useImperativeHandle, forwardRef, useRef } from 'react';
import { Link } from 'react-router-dom';
import { toast } from 'sonner';
import { RefreshCwIcon, Trash2Icon, TerminalIcon, MessageSquareIcon, ExternalLinkIcon, CopyIcon, LoaderIcon, CircleCheckIcon, CircleXIcon, CircleDotIcon } from 'lucide-react';
import { cn } from '@/lib/utils';
import { request } from '@/lib/api';
import { buildOpenUrl, describeChatAction, terminalPath, chatPath } from '@/lib/urls';
import { useNotice } from '@/components/notice-banner';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { Skeleton } from '@/components/ui/skeleton';
import type { Spritz } from '@/types/spritz';

function phaseBadgeVariant(phase: string): 'default' | 'secondary' | 'destructive' | 'outline' {
  const p = phase.toLowerCase();
  if (p === 'ready') return 'default';
  if (p === 'failed' || p === 'error') return 'destructive';
  return 'secondary';
}

function isProvisioning(phase: string): boolean {
  const p = phase.toLowerCase();
  return p !== 'ready' && p !== 'failed' && p !== 'error' && p !== '';
}

function PhaseIcon({ phase }: { phase: string }) {
  const p = phase.toLowerCase();
  if (p === 'ready') return <CircleCheckIcon className="size-3.5 text-green-500" />;
  if (p === 'failed' || p === 'error') return <CircleXIcon className="size-3.5 text-destructive" />;
  if (isProvisioning(p)) return <LoaderIcon className="size-3.5 animate-spin text-primary" />;
  return <CircleDotIcon className="size-3.5 text-muted-foreground" />;
}

function SpritzSkeleton() {
  return (
    <div className="flex items-center justify-between gap-4 px-4 py-3">
      <div className="flex flex-1 flex-col gap-2">
        <Skeleton className="h-4 w-32" />
        <Skeleton className="h-3 w-48" />
      </div>
      <div className="flex gap-1.5">
        <Skeleton className="h-8 w-16" />
        <Skeleton className="h-8 w-16" />
        <Skeleton className="h-8 w-16" />
      </div>
    </div>
  );
}

interface SpritzItemProps {
  spritz: Spritz;
  onDelete: (name: string) => void;
  deleting: string | null;
}

function SpritzItem({ spritz, onDelete, deleting }: SpritzItemProps) {
  const name = spritz.metadata?.name || 'unknown';
  const namespace = spritz.metadata?.namespace;
  const phase = spritz.status?.phase || 'unknown';
  const image = spritz.spec?.image || '';
  const message = spritz.status?.message || '';
  const terminalReady = phase === 'Ready';
  const chatAction = describeChatAction(spritz);
  const isDeleting = deleting === name;

  const sshMode = spritz.spec?.ssh?.mode;
  const sshInfo = spritz.status?.ssh;
  const hasSSH = sshMode === 'gateway' || (sshInfo?.host && sshInfo?.user);

  const handleOpen = () => {
    const url = buildOpenUrl(spritz.status?.url, spritz);
    if (!url) return;
    try {
      const parsed = new URL(url, window.location.href);
      if (parsed.hostname === window.location.hostname) {
        window.location.assign(url);
        return;
      }
    } catch {
      /* fall through */
    }
    window.open(url, '_blank');
  };

  const handleSSH = async () => {
    let cmd: string;
    if (sshMode === 'gateway') {
      const parts = ['spz', 'ssh', name];
      if (namespace) parts.push('--namespace', namespace);
      cmd = parts.join(' ');
    } else {
      const port = sshInfo?.port || 22;
      cmd = `ssh ${sshInfo?.user}@${sshInfo?.host} -p ${port}`;
    }
    try {
      await navigator.clipboard.writeText(cmd);
      toast.info('SSH command copied to clipboard.');
    } catch {
      window.prompt('SSH command', cmd);
    }
  };

  const provisioning = isProvisioning(phase);

  return (
    <div className={cn(
      "flex flex-col gap-2 px-4 py-3 sm:flex-row sm:items-center sm:justify-between sm:gap-4 transition-colors",
      provisioning && "bg-primary/[0.02]",
    )}>
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <PhaseIcon phase={phase} />
          <span className="font-medium">{name}</span>
          <Badge variant={phaseBadgeVariant(phase)} className="text-[10px]">
            {phase}
          </Badge>
        </div>
        <p className="truncate text-xs text-muted-foreground">
          {message ? `${image} · ${message}` : image}
        </p>
      </div>
      <div className="flex flex-wrap gap-1.5">
        <Button variant="outline" size="sm" className="rounded-full" onClick={handleOpen} title="Open">
          <ExternalLinkIcon className="size-3.5" />
          Open
        </Button>
        <Link to={terminalReady ? terminalPath(name) : '#'}>
          <Button
            variant="outline"
            size="sm"
            className="rounded-full"
            disabled={!terminalReady}
            title={terminalReady ? 'Open terminal' : 'Terminal is available once provisioning completes.'}
          >
            <TerminalIcon className="size-3.5" />
            Terminal
          </Button>
        </Link>
        <Link to={chatPath(name)}>
          <Button
            variant="outline"
            size="sm"
            className="rounded-full"
            disabled={chatAction.disabled}
            title={chatAction.title}
          >
            <MessageSquareIcon className="size-3.5" />
            {chatAction.label}
          </Button>
        </Link>
        {hasSSH && (
          <Button variant="outline" size="sm" className="rounded-full" onClick={handleSSH} title="Copy SSH command">
            <CopyIcon className="size-3.5" />
            SSH
          </Button>
        )}
        <Button
          variant="destructive"
          size="sm"
          className="rounded-full"
          onClick={() => onDelete(name)}
          disabled={isDeleting}
        >
          <Trash2Icon className="size-3.5" />
          {isDeleting ? 'Deleting…' : 'Delete'}
        </Button>
      </div>
    </div>
  );
}

export interface SpritzListHandle {
  refresh: () => void;
}

export const SpritzList = forwardRef<SpritzListHandle>(function SpritzList(_, ref) {
  const [spritzes, setSpritzes] = useState<Spritz[]>([]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [deleting, setDeleting] = useState<string | null>(null);
  const { showNotice } = useNotice();
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const fetchSpritzes = useCallback(async () => {
    try {
      const data = await request<{ items: Spritz[] }>('/spritzes');
      setSpritzes(data?.items || []);
      showNotice('', 'info');
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : 'Failed to load spritzes.';
      showNotice(message);
    } finally {
      setLoading(false);
      setRefreshing(false);
    }
  }, [showNotice]);

  // Expose refresh to parent
  useImperativeHandle(ref, () => ({
    refresh: () => fetchSpritzes(),
  }), [fetchSpritzes]);

  // Initial fetch
  useEffect(() => {
    fetchSpritzes();
  }, [fetchSpritzes]);

  // Auto-poll: 3s when provisioning, 10s otherwise (for ACP state changes)
  useEffect(() => {
    const hasProvisioning = spritzes.some((s) => isProvisioning(s.status?.phase || ''));
    const interval = hasProvisioning ? 3000 : 10000;

    pollRef.current = setInterval(() => fetchSpritzes(), interval);

    return () => {
      if (pollRef.current) {
        clearInterval(pollRef.current);
        pollRef.current = null;
      }
    };
  }, [spritzes, fetchSpritzes]);

  const handleRefresh = async () => {
    setRefreshing(true);
    await fetchSpritzes();
  };

  const handleDelete = async (name: string) => {
    setDeleting(name);
    try {
      await request(`/spritzes/${encodeURIComponent(name)}`, { method: 'DELETE' });
      setSpritzes((prev) => prev.filter((s) => s.metadata?.name !== name));
      toast.info('Spritz deleted.');
      setTimeout(() => fetchSpritzes(), 250);
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : 'Failed to delete Spritz.';
      showNotice(message);
    } finally {
      setDeleting(null);
    }
  };

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center justify-between">
        <h2 className="text-xl font-semibold">Active Spritzes</h2>
        <Button
          variant="ghost"
          size="sm"
          onClick={handleRefresh}
          disabled={refreshing}
        >
          <RefreshCwIcon className={`size-3.5 ${refreshing ? 'animate-spin' : ''}`} />
          Refresh
        </Button>
      </div>
      <div className="divide-y rounded-[20px] border border-[#e5e5e5] bg-card text-card-foreground dark:border-border">
        {loading ? (
          <>
            <SpritzSkeleton />
            <SpritzSkeleton />
            <SpritzSkeleton />
          </>
        ) : spritzes.length === 0 ? (
          <div className="px-6 py-8 text-center">
            <p className="text-muted-foreground">No Spritzes yet</p>
            <p className="text-sm text-muted-foreground">Create one above to get started.</p>
          </div>
        ) : (
          spritzes.map((spritz) => (
            <SpritzItem
              key={spritz.metadata?.name}
              spritz={spritz}
              onDelete={handleDelete}
              deleting={deleting}
            />
          ))
        )}
      </div>
    </div>
  );
});
